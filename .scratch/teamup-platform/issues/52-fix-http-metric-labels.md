# 52 — HTTP metric labels are unusable: mojibake status, unbounded path

**What to build:** Make `teamup_http_requests_total` and friends carry labels a human and a scraper can both read.

**Blocked by:** None

**Status:** completed

- [x] Label `status` with the decimal status code instead of the character at that code point
- [x] Label `path` with the matched route template so cardinality is bounded by routes, not by traffic
- [x] Collapse unmatched requests to a single label value rather than echoing the requested URL
- [x] Add tests asserting the actual exposed label values

**Type:** bug

## Evidence

Two defects in `internal/observability/observability.go`, both in `GinMetrics`.

**1. Status label was a rune conversion.** `string(rune(status))` yields the character at that Unicode code point, not the decimal digits:

```go
httpRequestsTotal.WithLabelValues(method, path, string(rune(status))).Inc()
```

Observed on the real `/metrics` output: 200 became `È`, 404 became `Ɣ`, 500 became `Ǵ`, 503 became `Ƿ`. Every alert and dashboard that groups by `status` — including the `HighErrorRate` rule in ticket 10, which matches `status=~"5.."` — matched nothing and would have stayed silent through a total outage.

**2. Path label was the raw URL.** `sanitizePath` was the identity function with a comment admitting it:

```go
func sanitizePath(path string) string {
	// Replace UUIDs and IDs with placeholders
	// This is a simplified version; in production use a proper path matcher
	return path
}
```

Two requests for two different tasks produced two time series. 25 requests to non-existent URLs produced 25 series. Since the URL is attacker-controlled, this is a remote cardinality bomb against the scraper, reachable by anyone who can send unauthenticated 404s.

The label feeds three metrics, so each junk URL cost three series: `teamup_http_requests_total`, `teamup_http_request_duration_seconds`, `teamup_http_requests_in_flight`.

## Why it matters

Ticket 10 specified `strconv.Itoa(rw.statusCode)` (line 371) and `sanitizePath(r.URL.Path) // /api/channels/123 → /api/channels/:id` (line 361). The implementation drifted from both. The result was not a degraded metric but a useless one: the RED metrics that ticket 10 exists to provide could not be broken down by status at all, and the cardinality growth would eventually take the scraper down.

## Comments

**Root cause.** Two independent implementation shortcuts in `GinMetrics`, both at the point where labels are chosen. `string(rune(int))` is a legal Go conversion with the wrong meaning here — it is the "code point to character" conversion, not a number-to-text one — and it compiles silently. `sanitizePath` was a placeholder that shipped.

**The fix** (`internal/observability/observability.go`):

- `strconv.Itoa(status)` for the status label.
- A new `routeLabel(c *gin.Context)` returning `c.FullPath()` — the template Gin matched, e.g. `/api/tasks/:id`. This bounds cardinality by construction rather than by pattern-matching the URL after the fact: there is exactly one label value per registered route, and no regex to keep in sync with the router.
- `c.FullPath()` is `""` when no route matched. Those collapse to the constant `<unmatched>`. Echoing the raw URL for 404s would have left the cardinality bomb fully armed while looking fixed.
- `sanitizePath` deleted; it had no other caller.

Gin resolves the route before running the handler chain, so `FullPath()` is already populated when the middleware is entered and the in-flight gauge increments and decrements the same series.

**Tests added** — `internal/observability/observability_test.go`, all asserting against the exposition text served by `MetricsHandler`, i.e. the bytes Prometheus actually scrapes:

- `TestGinMetricsLabelsStatusWithTheDecimalCode` (`:101`) — a matched 200 and an unmatched 404. At RED: `status labels = ["È" "Ɣ"], want ["200" "404"]`.
- `TestGinMetricsStatusLabelForEveryCode` (`:121`) — table over 12 codes the API returns. At RED every one was mojibake.
- `TestGinMetricsLabelsPathWithTheRouteTemplate` (`:143`) — two different task UUIDs produce one series labelled `/api/tasks/:id` across all three HTTP metrics, and it counted both requests. At RED: 2 series per metric.
- `TestGinMetricsCollapsesUnmatchedPaths` (`:174`) — 25 distinct junk URLs produce one series, which does not contain any fragment of the requested URLs, and it counted all 25. At RED: 25 series.
- `TestGinMetricsInFlightReturnsToZero` (`:201`) — the gauge settles to 0. This one **passed at RED**; it is a regression guard on the fix, not a reproduction of the bug, because the hazard it covers (computing the path label twice from different sources, so `Dec` misses the series `Inc` touched) is one the fix could have introduced.

**Deliberately left alone.**

- **`GinLogger` still logs `c.Request.URL.Path`.** That is correct and should stay: a log line wants the URL that was actually requested. Only the metric label needs collapsing, because only the label becomes a time series.
- **`/metrics` is still unauthenticated** — ticket 51, and it is what makes defect 2 remotely exploitable rather than merely wasteful. Out of scope here; the cardinality fix stands on its own regardless of how 51 is resolved.
- **`cmd/teamup/main.go` untouched** — owned by another agent this round. No change was needed there; `GinMetrics()` keeps its signature.
- **The `<unmatched>` spelling** is a placeholder-style value that cannot collide with a real Gin route (routes must begin with `/`). Not made configurable.
- **Metrics named in ticket 10 but never implemented** — `dbPoolStats`, `livekit*`, `wsMessageDuration`, `totalUsers`, `callDuration`, `tasksByAssignee` — remain absent, and `messagesTotal` is missing the spec's `type` label. Pre-existing gaps against the spec, not label defects; they belong to their own ticket.

**Verification** (`TEAMUP_TEST_DATABASE_URL` → `teamup_test_c` on :5440):

```
go build ./...                              clean
go vet ./...                                clean
gofmt -l internal/observability             clean
go test -race ./internal/observability/...  ok  1.6s
go test -race ./internal/... ./pkg/...      358 passed, 0 failed, 0 skipped
```

Baseline on a clean checkout was 341 passed / 0 failed / 0 skipped; the 17 added tests and subtests account for the difference. No new skips.

Two pre-existing environment failures, both reproduced on a stashed clean checkout and neither caused by this change:

- `go build ./...` needs `cmd/teamup/web/dist` to exist for the `all:web/dist` embed (see issue 43). Verified with a placeholder file, which was then removed.
- `make test-unit` reports `go: no such tool "covdata"` for `internal/httpx`, `internal/testsupport` and `pkg/errors` — the three packages with no test files. This Go 1.25.0 install has no `covdata` in its `GOTOOLDIR`, so `-covermode=atomic` cannot finish. It is a broken toolchain install, not a test failure; the same suite passes without `-coverprofile`.

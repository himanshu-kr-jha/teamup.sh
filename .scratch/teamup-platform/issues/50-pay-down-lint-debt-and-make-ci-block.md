# 50 — Pay down the 26 lint findings so CI can block on all of them

**What to build:** Clear the pre-existing golangci-lint findings, then flip CI from `only-new-issues` to blocking.

**Blocked by:** None

**Status:** open — flagged for review, landed as-is

- [ ] Triage the 20 `errcheck` findings — each is an ignored error, some may be real defects
- [ ] Fix the 4 `staticcheck`, 1 `ineffassign`, 1 `unused`
- [ ] Remove `only-new-issues: true` from `.github/workflows/ci.yml`
- [ ] Confirm a clean full-repo run in CI, not just locally

**Type:** task

## Evidence

`golangci-lint` v2.6.2 reports **26 pre-existing findings**: 20 `errcheck`, 4 `staticcheck`, 1 `ineffassign`, 1 `unused`.

Ticket 43 set CI to run golangci-lint with `only-new-issues: true` on pull requests, so new code is held to the standard while inherited debt does not redden every PR. `gofmt` and `go vet` block unconditionally and are clean.

## Why this is worth doing rather than leaving

`only-new-issues` is the right default for adopting a linter on an existing codebase, but it has a specific weakness: it depends on the action correctly diffing against the base branch. When that goes wrong it fails *open* — the run passes and nobody notices the linter stopped looking. A clean repo blocking on everything has no such failure mode.

The 20 `errcheck` findings deserve individual attention rather than a blanket `//nolint`. The audit that produced tickets 33–47 found that **swallowed errors were a recurring theme** in this codebase — `Enqueue` dropping messages into an empty `default` branch, `cleanupStaleConnections` with an empty loop body. Some of those 20 are likely to be the same class of bug rather than noise.

Note `golangci-lint` v1.64.8 refuses to run against a Go 1.25 target; v2.6.2 or later is required.

See [[43-add-github-actions-pipeline]] and [[36-surface-dropped-messages-on-slow-consumers]].

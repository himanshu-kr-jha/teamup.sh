# 51 — `/metrics` is served to anyone

**What to build:** Stop exposing the Prometheus endpoint publicly.

**Blocked by:** None

**Status:** open

- [ ] Decide the mechanism: bind metrics to a separate port, require auth, or restrict by network policy
- [ ] Ensure the Helm chart's default ingress does not publish it
- [ ] Add a test asserting an unauthenticated request is refused

**Type:** task

## Evidence

`/metrics` is registered at `cmd/teamup/main.go:457` and appears on the auth-skip list at `cmd/teamup/main.go:562`. Confirmed returning **200 with no credentials**.

The Helm chart's default ingress path is `/`, so a standard install publishes it to whatever the ingress is reachable from. NOTES.txt warns when a `/` path is configured, but a warning is not a control.

## Why it matters

Prometheus metrics are not neutral. The RED metrics plus the custom business metrics from ticket 10 expose active user counts, message and call volumes, and per-endpoint error rates. For a self-hosted product that is competitive and operational intelligence about the customer's own organization, readable by anyone who can reach the ingress.

It is also a reconnaissance aid: error-rate and latency histograms per route reveal which endpoints exist and which are failing.

## Direction

Serving metrics on a separate port bound to the cluster network is the usual answer and works cleanly with the `ServiceMonitor` the chart already renders — Prometheus scrapes the pod directly, so the endpoint never needs to be on the public ingress at all. That likely means the chart should stop routing it rather than the application starting to authenticate it.

Found while building the Helm chart ([[47-helm-chart-has-no-templates]]). See also [[10-observability]].

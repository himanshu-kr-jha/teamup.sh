# A status page shows whether anything is down

Status: ready-for-human
Severity: Medium
Asked for by: the user, "create a service status page to see if any outage is there".

## What was missing

Nothing outside `/readyz`, which only operators can read and which forgets an outage the moment it ends.

## Comments

2026-11-08, orchestrator: decision 49.
`internal/status` records the readiness checks into Redis once every 30 seconds per replica and serves `GET /api/status` without a session; `web/src/features/status` is the page and the in-app banner.
Verified: `internal/status/status_test.go` (incidents and their length, an ongoing outage, a replica's failure not hidden by another's pass, no history without Redis, nothing private in the report, the Redis store against real Redis), and `web/e2e/meetings.y-features.spec.ts`, which pauses the real MinIO container and sees the page, the banner and the incident afterwards.
Not verified: on the VM, behind nginx.

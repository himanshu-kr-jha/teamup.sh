# Progress log

Newest entries at the bottom.
Who: orchestrator = Opus (plans, verifies, reports). Senior developer = Opus subagent. Tester = Sonnet subagent.

## 2026-11-06

- Read the task code, tracker and deploy assets. Wrote `spec.md`.
- Bugs already seen while reading, to be filed as tickets:
  - The board has no Blocked column, so blocked tasks vanish from it.
  - Standup is empty for an org admin who runs no team.
  - Re-saving status `done` overwrites `completed_at`, which would corrupt tracking.
  - Creating a task as `done` never sets `completed_at`.
  - Standup "closed since yesterday" loses Friday's work on Monday.
- Uncommitted read-receipt work was already in the tree when this started. It belongs to the user: keep it, do not revert it.

- Tester (Sonnet): wrote failing tests for tickets 01-04 in `internal/tasks/` (`completion_test.go`, `dashboard_test.go`, `scrum_test.go`, `contributions_test.go`), HTTP only, one shared `seedDash` fixture in `dashboard_test.go`. 22 tests, 21 red, 1 already green (reopen clears `completed_at`). Package compiles, existing tests unchanged. Response shapes beyond spec.md were assumed (see the tester report); the developer should confirm or adjust them with the orchestrator.

- Senior developer (Opus): tickets 01, 02, 03, 04, 13 and 15 resolved. New code in `internal/tasks/dashboard.go`, `scrum.go`, `contributions.go`, migration `0015_scrums`, and fixes in `tasks.go`. Decisions 9 to 17 added to `spec.md`. Added tests for ticket 13, for non-id activity values, and for a second organization on the dashboard and contribution routes. No tester-written assertion was changed.

- Senior developer (Opus): tickets 10, 11, 12, 14 and 16 resolved, test-first, and the backend halves of 31 and 37 done with their frames written under `## Comments` in those tickets. 31 and 37 stay open for the frontend handlers.
  - One path for message changes: the socket `chat.edit` and `chat.delete` frames now call `EditMessage` and `DeleteMessage`, which do the broadcasting.
  - Edits reuse the partial `chat.message` frame rather than `chat.edit`, as instructed, so `ChatNotifications.tsx` must ignore a frame with an empty `userId` or every edit will chime. Noted in ticket 31.
  - `migrations/0015_scrums.up.sql` now backfills `completed_at` on existing done tasks, not undone on the way down. A database that already ran 0015 will not get the backfill and needs the statement run by hand.
  - `TestACallAcceptFrameUsesTheRoomItNames` replaced, because it pinned the ticket 10 defect.

- Senior frontend developer (Opus): ticket 05 and bug tickets 30, 31, 32, 34, 35, 36, 37, 38, 39 resolved. Ticket 33 left alone, pending a product decision.
  New code in `web/src/features/tasks/dashboard/`, `BoardView.tsx`, `dueDate.ts`, `liveQuery.ts`, `features/auth/session.ts`, `features/chat/composerDraft.ts`, `features/chat/liveChanges.ts`, and `shared/types/dashboard.ts`. `StandupView.tsx` deleted.
  `vite.config.ts`: the service worker's API cache is now NetworkFirst, because StaleWhileRevalidate gave every refetch over HTTPS the previous answer, which would have defeated tickets 36 and 37 and the live scrum view.
  Checked in Chromium against a mocked API only. Browser testing against the real backend is still to come.


- Tester (Sonnet), real-stack E2E: real Go server, real Postgres (`teamup_e2e`), real Chromium, people seeded through the API, every person signing in through the login form.
  New files: `web/playwright.config.ts`, `web/vitest.config.ts` (keeps Vitest out of `e2e/`), `web/e2e/global-setup.ts`, `web/e2e/support/{api,people,fixtures}.ts`, `web/e2e/dashboard.spec.ts`, `web/e2e/screenshots.spec.ts`, `web/e2e/run.sh`.
  Rerun everything with `web/e2e/run.sh` (drops and recreates the e2e database, starts the server, runs the suite, stops the server).
  Defects filed: 50 (logout does not end the session, `rt` cookie survives and the session is never revoked), 51 (saved theme is not restored on load), 52 (task due today shows as overdue today; test 9 is red on purpose).
  Notes: the Overview lists an overdue task twice (under its status and under Overdue), which `logic.ts` documents as intended. All seed tasks read "New" until the first scrum is saved, which follows decision 14.
  The machine was under heavy memory pressure from thousands of `docker-proxy` processes (not ours) during part of the run, so some runs timed out for that reason alone.
  Final full run of `web/e2e/run.sh`: 42 tests, 40 green, 2 red and both are real defects (test 30 = ticket 50, test 9 = ticket 52).
  Screenshots: 72 images (3 roles x tabs x 1280/375 x light/dark, plus dialogs and a saved scrum) in the session scratchpad `e2e-shots/`, with a `layout__*.txt` per role and size.
  The dark screenshots go through the header toggle because of ticket 51.

- Senior developer (Opus): tickets 50, 51, 52 resolved test-first, plus the phone tab strip polish.
  50: `/auth/logout` stays public and revokes from the refresh cookie. `rt` moved from `Path=/api/auth/refresh` to `Path=/api/auth`, because the old path never reached `/logout`. Old-path cookies are expired on every login, refresh and logout, and that code can go once a refresh token's lifetime has passed since deploying. Tests in `internal/auth/logout_test.go` use a real cookie jar.
  The logout flow now pauses React Query first (`onlineManager.setOnline(false)`), because clearing the cache refetched on a dead session and logged 401s, which failed E2E test 30 once the session really ended.
  51: inline script in `web/index.html`, the rule in `web/src/shared/theme.ts`, and a test that runs the real script against it. Header and Settings share `applyTheme`, and the header reads the document class through `useIsDark`.
  52: `endOfDueDay` in `dueDate.ts`, used by `CreateTaskDialog` and `TaskDetailPage`. The server is unchanged. Tasks saved earlier keep their midnight value, as accepted in the ticket. Ticket 34 has a note that 52 supersedes its overdue rule. `dueDate.test.ts` runs in several time zones, so `vitest.config.ts` runs that one file in the forks pool.
  Polish: `/tasks` tab strip has a fade on whichever end hides tabs, and centres the active tab.
  Renamed `useTheme` to `settleTheme` in `web/e2e/screenshots.spec.ts`, which was a lint error (rules-of-hooks).
  The frontend is embedded in the Go binary, so rebuild it after `pnpm build` or `web/e2e/run.sh` tests the previous frontend.
  Final numbers: Go 763 pass (tests and subtests), 0 fail, 14 packages; vet and gofmt clean; Vitest 167; tsc, lint, build clean; full `web/e2e/run.sh` 49 of 49.

- Senior developer (Opus): ticket 06 resolved. `docs/DEPLOYMENT.md` rewritten after running the production stack from empty, and the guide's own commands were then extracted and rehearsed.
  The stack as shipped could not start on a new machine: no GHCR image, `minio/minio` removed upstream, no migrations, no restart policies, and a healthcheck that could not run. All fixed, see the ticket.
  Measured 50 simulated people with 2000 tasks: dashboard p50 14.8 ms, p95 22.8 ms, about 300 MiB in total, no errors. Recommended VM is 4 vCPU, 8 GB, 100 GB, with calls estimated rather than measured.
  Restore and rollback were tested. Rolling back `0015_scrums` drops saved scrums for good.
  Filed 53 (dev and CI still pull the removed MinIO images, so a fresh clone fails at `./setup.sh`) and 54 (no password recovery).
  Incident: a test of Docker's 50000-60000/udp mapping took this shared machine to load 88 and under 1 GB of available memory for about 30 minutes. Other containers stayed up. One root-owned `docker-proxy` for 127.0.0.2:42928 is left over, because killing it needs root.

- Senior developer (Opus): tickets 53 and 54 resolved.
  54: `POST /api/users/:id/reset-password` and a Reset password button on the Organization page. It reuses the add-member password generator and dialog and the existing change-password page. Admin or owner only, an admin cannot reset an owner, nobody can reset themselves, another organization is 404. No audit entry, because nothing in the product writes the audit log.
  Login page text replaces the dead link. Migration 0016 makes the email unique only among people not deleted, so a deleted member can be added back as a new account. `docs/DEPLOYMENT.md` no longer has the SQL workaround.
  53: dev compose, `setup.sh`, `reset.sh` and CI use the pinned Chainguard images. No health check, same as production. The bucket comes from a one-shot client container. Also fixed: `setup.sh` could claim 7882 twice, and the dev compose admin password was too short for auto-setup.
  `setup.sh`, the dev compose and `reset.sh` were run for real in a temporary copy. CI could not be run, and was reviewed by hand. See the tickets for what was left alone, notably `make build` failing under pnpm 11.
  Final numbers: Go 774 pass (tests and subtests), 0 fail, 0 skip, 14 packages (763 before); vet and gofmt clean; Vitest 167; tsc, lint, build clean; full `web/e2e/run.sh` 50 of 50.

## Final state, 2026-11-06

- Orchestrator made four last small fixes: deactivating a member ends their sessions (test-first), change-password page asks for 12 characters like the server, dead Google and Microsoft buttons removed from the login page, pnpm pinned in `web/package.json`.
- Verified by the orchestrator in one final run: Go 775 pass, 0 fail, 0 skip; vet and gofmt clean; vitest 167 pass; tsc, lint and build clean; Playwright E2E 50 of 50.
- Open: ticket 33 is a product decision, status needs-info. Nothing is committed.
- To rerun the tests, start these throwaway services, then export the variables below. The E2E entry point is `web/e2e/run.sh`.

```bash
docker run -d --name teamup-dev-test-pg -e POSTGRES_USER=teamup -e POSTGRES_PASSWORD=teamup -e POSTGRES_DB=postgres -p 127.0.0.1:55450:5432 postgres:16-alpine
docker run -d --name teamup-dev-test-redis -p 127.0.0.1:6379:6379 redis:7-alpine
docker run -d --name teamup-dev-test-minio -p 127.0.0.1:9000:9000 -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin minio/minio:latest server /data
docker run -d --name teamup-dev-test-livekit -p 127.0.0.1:7880:7880 livekit/livekit-server:latest --dev --bind 0.0.0.0
export TEAMUP_TEST_DATABASE_URL="postgres://teamup:teamup@localhost:55450/teamup_test?sslmode=disable" TEAMUP_TEST_REDIS_URL=redis://localhost:6379
export TEAMUP_TEST_MINIO_ENDPOINT=localhost:9000 TEAMUP_TEST_MINIO_ACCESS_KEY=minioadmin TEAMUP_TEST_MINIO_SECRET_KEY=minioadmin TEAMUP_TEST_MINIO_BUCKET=teamup-files-test
go build -o bin/teamup ./cmd/teamup && make test-db TEST_DATABASE_URL="$TEAMUP_TEST_DATABASE_URL"
```

The buckets `teamup-files-test` and `teamup` must exist in that MinIO, and `minio/minio` is only available if already cached (ticket 53).

## Deployment, 2026-11-06

- Oracle Always Free was tried first in Mumbai and failed: out of A1 capacity at both 2 OCPU/12 GB and 1 OCPU/6 GB, and the region has one availability domain.
- Deployed instead to an AWS EC2 `m7i-flex.large` in ap-southeast-2 (2 vCPU, 8 GB, 96 GB, x86, Ubuntu 24.04) following `docs/DEPLOYMENT.md`.
- The VM has an uncommitted copy of the working tree at base commit 9b339c9, not a git checkout, so `git pull` upgrades do not apply until the work is committed and the VM is cloned from it.
- Verified: all services up, `/readyz` ready, TLS valid, ports 5432 and 7880 closed from outside, 7881 and 7882/udp reachable, one backup taken and its archives read, `certbot renew --dry-run` succeeded and nginx came back.
- Not verified on this VM: file upload and a real call in a browser, a restore, and a real certificate renewal, because a dry run does not run the deploy hook.
- Open: backups are on the VM only, the public IP is not known to be an Elastic IP, and the `.sslip.io` name depends on that IP.

- 2026-11-06: the user stopped the VM to save cost while new features are built. A backup is on the laptop at `~/.teamup-aws/backups/`. The public IP changes on restart, so the `.sslip.io` domain, `.env`, certificate and LiveKit settings need redoing for the new address.

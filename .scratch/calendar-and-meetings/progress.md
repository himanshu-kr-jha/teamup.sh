# Progress log

Newest entries at the bottom.
Who: orchestrator = Opus (plans, verifies, reports). Senior developer = Opus subagent. Tester = Sonnet subagent.

## 2026-11-06

- Read the calls and tasks code, wrote `spec.md` and tickets 01 to 10.
- Live VM runs base commit 9b339c9 plus the uncommitted tree as of that deployment. Nothing here is committed, and nothing touches the VM until the user agrees.

- 2026-11-06, Tester (Sonnet): failing backend tests for tickets 01 to 05, 61 tests in 6 files (`internal/calls/{livekit_helpers,attendance,knock,standing}_test.go`, `internal/calendar/{harness,calendar}_test.go`, `internal/tasks/added_to_test.go`). 59 red, 2 green on purpose (the log has no edit route; an assignee who also watches appears once). Existing tests unchanged and green. Assumed response shapes are listed under Comments in ticket 01. `internal/calendar` has a stub harness to wire once the package exists. Nothing committed.

## 2026-11-07

- Senior developer (Opus): tickets 01 to 05 resolved, backend only, nothing committed.
  New: `internal/calendar/`, `internal/calls/{webhook,knock,standing}.go`, migrations 0017 to 0021, frames in `pkg/protocol`, the LiveKit webhook in both compose files.
  Decisions 22 to 34 added to `spec.md`. Routes, shapes and frames are under `## Comments` in each ticket.
  `calls` now imports `tasks` for the org chart: `HasAuthorityOver` exported, `AuthorityOverAny` and `AnswerableFor` added. `go.mod` gains `go-retryablehttp` and `go-cleanhttp` as indirect, needed by `livekit/protocol/webhook`, and `go-isatty` moved to 0.0.20.
  `GET /calls/:id` now hides detail from people who may not read the log (decision 28). The current frontend does not call it.
  Checked against real LiveKit v1.13.11 with a real WebRTC participant in a throwaway script: signature, joined, left, screen share, room finished, an occurrence ending and reopening, and a meeting opening.
  Test services used: `teamup-cal-test-{pg,redis,minio,livekit}` on 127.0.0.1:55461 to 55466, LiveKit on single TCP and UDP ports. To rerun: `TEAMUP_TEST_DATABASE_URL` as in the ticket brief, `TEAMUP_TEST_REDIS_URL=redis://localhost:55462`, `TEAMUP_TEST_MINIO_ENDPOINT=localhost:55463` with `minioadmin` for both keys and bucket `teamup-files-test`, `TEAMUP_TEST_LIVEKIT_URL=ws://localhost:55464`.

- 2026-11-07, Senior frontend developer: tickets 06, 07 and 08 resolved, nothing committed. Comments in each ticket say what was seen and what was not.
  Checked in real Chromium through the embedded frontend, against the real server, Postgres, Redis, MinIO and LiveKit test services, with real webhooks, on 127.0.0.1:55470 (the port test LiveKit posts to). Exploratory scripts are in the session scratchpad and are not committed; comprehensive E2E stays with ticket 09.
  Backend changes: the organizer also gets the 5-minute reminder (decisions 16, 31); `Service.EndCall` deleted rooms by sid and LiveKit deletes by name, so every teardown failed and a declined one-to-one left the caller in a closed call; `call.ended` named a person who declined by their email (or "Somebody") and now by name. All test-first.
  Frontend also fixed along the way: the call title bar and minimise button were white on white in light mode, native date inputs were dark on dark, a person signed out lost a call link at login, ringing went on for somebody already in the call.
  Existing `web/e2e/dashboard.spec.ts` updated for the new Added to column.
  Decisions 35 to 37 added to `spec.md`.

- 2026-11-07, Tester (Sonnet): ticket 09, real-stack browser tests. Nothing committed, no production code changed.
  New: `web/e2e/meetings.spec.ts` (23 tests), `web/e2e/meetings.z-shots.spec.ts` (4), `web/e2e/support/meetings.ts`, `web/e2e/run-meetings.sh`, `web/e2e/test-services.env`, `web/e2e/README-services.md`. Changed: `web/e2e/run.sh` (exports `E2E_DATABASE_URL`, `E2E_PORT`), `web/e2e/support/{fixtures,api}.ts` (extra people, narrow console-noise allowances), `web/playwright.config.ts` (fake media flags, 20 second action timeout).
  Full run: 77 tests, 75 passed, 2 failed (10.5 minutes). The 50 existing tests pass, including the dashboard test the last developer changed. The 2 failures are red on purpose: defect 30 (`issues/30-join-toast-covers-controls-on-phone.md`) and defect 31 (`issues/31-attendance-offers-call-log-that-always-refuses.md`). `go test ./...` passes with `. web/e2e/test-services.env`.
  To rerun: `. web/e2e/test-services.env && make build && web/e2e/run-meetings.sh`. LiveKit must post its webhook to the port the server listens on (55470 here); `run-meetings.sh` checks it. Screenshots: `/tmp/claude-1000/-home-himanshu-kumar-jha-Documents-Ongoing-Projects-teamUp/47fd8d20-9a9e-48c5-8ba0-78ed94fb07ed/scratchpad/e2e-shots-meetings/`.
  Ticket 09 left at ready-for-human: see its Comments for what is covered and what is not.

- 2026-11-07, Senior developer (Opus): tickets 20 to 31 fixed, 32 to 34 filed and fixed, nothing committed. Each ticket's `## Comments` says what changed and how it was verified.
  Migrations 0022 (closes stale unscheduled calls, adds `idx_call_participants_user`), 0023 (`task_watchers.added_by`, with a backfill from `task.added` notifications), 0024 (`standing_meeting_schedules`). The `go test` template needs them: see `web/e2e/README-services.md`.
  Decisions 19, 22 to 24, 27 to 30, 35 and 37 changed, 38 to 43 added. Two people in a landscape space now sit side by side. Merely opening a meeting's page starts nothing: decision 35.
  New: `internal/calls/media.go` (a small interface over LiveKit, so the rules can be tested on what they ask for and when), `web/src/features/calls/lobby.ts`, `web/e2e/meetings.y-fixes.spec.ts`.
  Also fixed along the way: the notification menu stayed open over the page it had just navigated to, and blocked clicks on it. `TestScrumAddedToMigrationKeepsSavedScrumsReadable` assumed its migration was the latest. The scrum attendance tests, and `TestWithoutWebhooksAScrumDayReadsUnknownAndNobodyIsMarkedAbsent`, passed or failed by the time of day they ran, once a day before its start lists only who came. That last one failed for the first time when UTC passed midnight.
  Ticket 35 filed and fixed: an earlier call ringing out pulled somebody out of the call they were in. Found because the suite's own screenshots caught it.
  The new browser tests were run with the fixes taken out: 11 of the 13 go red. The two that stay green are the cancelled meeting's wording and ticket 26, whose server fixes were not part of what was taken out; ticket 26's Go tests were red first.
  Final full run, fresh, on the final tree: `92 passed (11.5m)`. Earlier full runs this session failed on tests that still described the old behaviour, on tests of mine that interfered with the next spec, and once on a timing assumption in the chime test, now fixed by waiting for the connection.
  Blank page flake: not found. See below.
- 2026-11-07, blank white page for Mia at 375 wide, light theme, bounded look.
  25 fresh sessions at that size each visited 6 pages with 3 screenshots a page, so 450 screenshots: none blank, no page with empty text, no pageerror, no console error, and no navigation beyond the ones asked for.
  Read: a `RouterProvider` with no fallback draws nothing until its first loaders answer, which is a real blank moment on every first load. It now draws the page background, but every shot in `meetings.z-shots.spec.ts` asserts content first, so this does not explain a shot.
  Read: `registerType: 'autoUpdate'` makes the page call `window.location.reload()` when a new service worker activates, without warning. That would give a blank moment after the assertions, but none happened in the probe, and the worker does not change during a run, so I have no evidence it is the cause. It would also drop a call in any open tab after a deploy, which is worth a separate look.
  The original screenshot was overwritten, so it could not be examined. A shot with no text now fails with the URL and load state, so if it returns there will be evidence. Not shown to be harmless, and not explained.
- 2026-11-07, Senior developer (Opus): ticket 10 ready-for-human, nothing committed, VM not touched. Its Comments have every number.
  A deploy did drop calls: `autoUpdate` reloaded a tab in a real call as soon as the browser found a new `sw.js` (reproduced, one session closed and a second opened). Fixed with prompt mode, `src/sw/register.ts` and `src/sw/UpdateOffer.tsx`, tested by `web/e2e/meetings.y-deploy.spec.ts` and `web/e2e/support/swap-server.ts`. Decision 44. `urlPattern` is the correct key and the API rules apply over HTTPS. It does not explain the blank page.
  Rehearsal on a copy of the 2026-11-06 backup: `16 -> 24`, existing rows identical, 16.3 s unavailable, webhook answered 200 through the production compose config. `0022` closed 0 calls and `0023` had 0 watchers, because the pilot has neither, so both were also checked on planted rows.
  Rollback was run all three ways: only changing the image gives a 502; `migrate down` eight times keeps tasks, messages and calls and destroys everything calendar and attendance; restoring the backup loses everything since it. `docs/DEPLOYMENT.md` says so.
  The backup is at migration 16 and 9b339c9 has 14, because live was an uncommitted tree that no longer exists. The old side ran on 9b339c9 with the files for 15 and 16 added, which is a stand-in.
  All rehearsal containers, volumes, images, the worktree and the restored data are deleted.
  Also fixed: the full suite failed once in `meetings.z-shots.spec.ts`, because ticket 35's fix was incomplete. See that ticket. The rehearsal's image predates this one change to `ActiveCallProvider.tsx`.

- 2026-11-08, orchestrator: tickets 36, 37 and 38 for the user's three new asks, done directly after the developer agent stalled twice without writing anything.
  Mail settings under Organization, Email, with a test send; welcome email on add and on CSV import; port 465 and named senders; no password over an unencrypted remote connection (decisions 47 and 48).
  Meetings take a start and a length from 5, 10, 15, 30, 45 min or 1 hour (decision 45).
  A meeting running past its end shows as extended for whoever is in it, growing while it runs (decision 46).
  The Organization tabs scroll on a phone instead of the page.
  Mailpit joins the test services (`web/e2e/README-services.md`).

- 2026-11-08, orchestrator: tickets 39 to 42 for the user's four new asks.
  A public status page at `/status` with 24 hours of history and an in-app banner while something is down (decision 49).
  The organization's own colour and logo under Organization, Appearance, on every page including sign-in, TeamUp's look by default (decision 50).
  PDFs drawn in the chat with pdf.js, and Download on PDF chips, pictures, the viewers and the message menu (decision 51).
  The number of chats with something unread on the Chat tab (decision 52), which found the badge coming back after a refetch, now fixed.
  pdf.js is added with pnpm 9's lockfile format, so the Docker build's `--frozen-lockfile` accepts it.

- 2026-11-08, orchestrator: tickets 43 to 47 for the user's next five asks.
  Forwarding with files (decision 53), month, week and day views (decision 54), profile pictures (decision 55), and Call as a voice call with microphones on (decision 56).
  End-to-end encryption is written up in ticket 47 with its costs, waiting on a choice (decision 57).
  Migration 0025 adds `messages.forwarded` and `user_avatars`; its down migration clears uploaded avatars.
  Also fixed: the phone header cut the name to "T…".

- 2026-11-08, orchestrator: onboarding and release flow, asked for by the user.
  `docs/ONBOARDING.md` (tools, running, the repo, three test layers, making a change, recipes) and `docs/BUILD-AND-DEPLOY.md` (the flow, CI, deploying, a new address, rolling back).
  `scripts/test-services.sh` starts every test container from nothing and migrates the test database; checked by removing them all and running the Go tests on fresh ones.
  `scripts/deploy.sh` is what was done by hand for each deploy, with server details kept in the untracked `.deploy.env`; its `--check` was run against the live server.
  New make targets: `test-services`, `test-go`, `test-web`, `e2e`, `check`, `deploy`, `deploy-check`.
  README, ARCHITECTURE, DEPLOYMENT and the test-services notes updated to match what is built.

- 2026-11-08, orchestrator: tickets 48 to 53.
  Mentions by username or name with suggestions and notifications (decision 58), history a day at a time (59), Esc to close (60), voice and video messages (61), several files at once (62).
  The meeting note-taker is written up in ticket 53, waiting on choices (63).
  `make check` stalled when run in the background with its output piped; the targets themselves pass when run one at a time.

- 2026-11-08, orchestrator: ticket 53 built (decision 63): the meeting note-taker, with Sarvam and Claude, OpenAI or the user's own model.
  Also: the status page now records an outage somebody saw on it, and samples every 15 seconds, because a short outage between samples vanished from the history (found by the full browser run).

- 2026-11-08, orchestrator: the first real meeting had no minutes: NVIDIA had retired the default model, and a clip after a mute was refused as over 30 seconds. Both fixed (ticket 53), with a Meeting notes page, retry, and a notice when minutes fail.

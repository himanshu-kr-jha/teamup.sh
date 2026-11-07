# Real-stack browser tests

Status: ready-for-human
Blocked by: 06, 07, 08

Extend `web/e2e` with real LiveKit, real server and real Chromium with fake media: two browsers in a call, join ping, screen share by a non-organizer, knock and admit, attendance rows, calendar across people, standing meeting attendance, Added to.
Screenshots in every role at 1280 and 375, light and dark. File defects as tickets.

## Comments

### 2026-11-07 - Tester: what is covered, what is red, what is not

Specs: `web/e2e/meetings.spec.ts` (23 tests) and `web/e2e/meetings.z-shots.spec.ts` (4 tests, 8 combinations of role set, width and theme).
The shots spec is named so that it runs after `meetings.spec.ts` and before `screenshots.spec.ts`.
Run with `web/e2e/run-meetings.sh` (sibling of `run.sh`); services and how LiveKit must be told where to post its webhooks are in `web/e2e/README-services.md` and `web/e2e/test-services.env`, which `go test` can source too.
Full run, 2026-11-07: 77 tests, 75 passed, 2 failed, 10.5 minutes.
The 2 failures are red on purpose, one per defect: ticket 30 (join toast covers the knock prompt and call controls on a phone) and ticket 31 (attendance offers a call log to people who can never read it).
Go tests with the same env: `go test ./...` all ok.

**Covered with real LiveKit webhooks and separate browser contexts**: two people in a call with attendance rows, leave and rejoin as two sessions; join toast and chime (oscillator spy) for others and not oneself; share by a non-organizer, two sharers, stage swap, share start and stop in the log; tiles at 1, 2, 3, 5 and 9 real people against the best possible tile size, and nine at 375; knock, deny, ask again after the real minute, admit with no reload, prompt clearing on another member, nobody connected, call ending while waiting, org admin and a manager with authority at the door; call log for member, admin, manager with authority, unrelated person, other organization (another org made with SQL), no edit routes; calendar (focus, open slot, added to a meeting without doing anything, no titles in response or DOM or labels for people without authority and titles for manager and admin, conflict warning then go ahead, move and cancel notify and the organizer is not told, invitee cannot move, past call at its real times, a call the invitee never joined); standing meetings (admin creates through the dialog, employee and manager refused, everybody joins without knocking, rejoin is the same record, present, late, absent, past days, scope for admin, manager, employee and team filter, unknown when no connection was reported, archive keeps history, the join toast in a standing room); tasks (Mia adds Ann; Added to for Ann and its notification; By person, Scrum, Contribution and its detail; a saved scrum records it and does not change; completion is not double credited; removal takes it away; adding twice tells once).

**Webhooks never arriving versus a product bug**: every attendance wait goes through `waitForLog`, which, if no session or event was ever recorded, fails with `WEBHOOKS NEVER ARRIVED` and the setup to check, and otherwise fails naming what was missing and printing the log.
`run-meetings.sh` also refuses to start when the LiveKit container does not post to the port the server will listen on, when the embedded frontend was built without `VITE_LIVEKIT_URL`, or when the binary is older than the source.

**Found while writing it**
- The frontend takes LiveKit's address at build time (`VITE_LIVEKIT_URL`, else `ws://<host>:7880`), so testing a non-default LiveKit needs `. web/e2e/test-services.env && make build`. The preflight checks this.
- An employee cannot put a peer on a task: the picker offers only people they may assign to and the server answers 403. That is the existing rule ("same org chart as assigning") and the test uses a manager. Not filed.
- Following the link of a call you are invited to makes your page ring briefly, which is two oscillators, and the chime counter is reset after joining to tell that from the join chime.
- Somebody arriving less than two seconds after you connected is not announced to you (the settle window in `joinPing.ts`), so tests wait between arrivals.
- LiveKit's client logs `DataChannel error ... User-Initiated Abort` and `publisher data channel ... closed unexpectedly` as console errors when somebody hangs up. They are let through narrowly in `support/fixtures.ts`, together with the door's 403, 409, 404 on token and 429 on knock, and 403 on `/requests` and 404 on `/log`.

**Flakes seen and classified**: the two LiveKit console messages above (once each, on leaving), a strict-mode toast match and a call hidden behind a "+N" chip (test bugs, fixed), and once a blank white page for Mia at 375 wide light in the screenshot spec (not reproduced in 8 more runs of that test, cause unknown).

**Not verified**: a real phone browser or real camera, listening to the chime (oscillators are counted, nothing is heard), autoplay refusal (the suite runs with `--autoplay-policy=no-user-gesture-required`), a speaking person staying on the page and paging above 16 people (the earlier developer's fake participants covered paging; this suite stops at nine real people), a real clock change, a browser zone other than Asia/Calcutta, a real midnight crossing of an occurrence date, a partial webhook outage, and a deployment behind the production nginx.
Of the 208 screenshots I opened about twenty across every screen type, both widths and both themes. The rest were checked only by the automated layout check (no sideways scroll, nothing off the right edge), which found nothing; the clipped-text notes are labels cut with an ellipsis on narrow calendar columns.

Left at ready-for-human rather than resolved: two defects are open and the rest of the screenshots have not been looked at by a person.

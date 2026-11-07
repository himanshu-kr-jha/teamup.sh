# Deployment changes

Status: ready-for-human
Blocked by: 09

Document the webhook, the new migration and the upgrade steps in `docs/DEPLOYMENT.md`, and rehearse the upgrade from the version now live with data in it.
Do not touch the live VM without the user's say-so.

## Comments

### 2026-11-07, senior developer

The docs are done and the upgrade was rehearsed on real data, on this machine only.
The upgrade of the real VM is the user's call, so this is ready-for-human.
Nothing here touched the VM, and all rehearsal data, secrets, images, volumes and the worktree are deleted.

**A deploy dropped calls. Reproduced and fixed.**
`meetings.y-deploy.spec.ts`, with `support/swap-server.ts` in front of the real server, makes a real call and swaps to a new `sw.js` while it runs.
Against the old `autoUpdate` build the page in the call reloaded once as soon as the browser checked for an update: `loads: 1`, document replaced, and in the call log Alice's first session closed and a second opened, with camera and microphone back off.
The fix is `registerType: 'prompt'`, `src/sw/register.ts` on the browser's own API, and `src/sw/UpdateOffer.tsx`, which offers **Reload** only when this tab is not in a call.
The plugin's `registerSW` was deliberately not kept, because in prompt mode it reloads every tab on `controlling`, so accepting in one tab would still drop a call in another.
A tab left open now checks once an hour.
Three browser tests cover it; the first of them was red before the fix.
The browser's own update check did not fire within 45 seconds under Playwright on signed-in pages, so the tests call `registration.update()`.
In plain headless Chromium with no driver it fired about 5.6 seconds after opening the login page, so this is how a real person hits it: opening TeamUp in another tab.

`urlPattern` is the right key: `workbox-build` 7.4.1 reads it and the routes are in the generated `sw.js`.
Over HTTPS on the rehearsal stack, `api-get` held `/api/tasks/dashboard`, `/api/channels`, `/api/users` and `/api/presence/availability`, so the rules do apply.
The patterns start `https://`, so they do nothing on plain http or on the e2e server.
`devOptions.enabled` only affects `vite dev`, and nothing of it is in the production build. It stays, and `web/dev-dist/` is now ignored.

Blank page: this is not its explanation.
`sw.js` does not change during an e2e run, so this reload cannot happen in one.
The blank shot stays unexplained, and a shot with no text still fails with the URL and load state.

**The rehearsal could not use the real old version.**
The backup is at migration 16, and commit 9b339c9 has migrations up to 14, because the live image was built from an uncommitted tree that no longer exists and is on no local image.
A literal 9b339c9 image's `migrate up` refuses the backup with `no migration found for version 16`.
Its server, started past `migrate`, did serve the data.
So the stand-in is 9b339c9 with the files for 15 and 16 added.
The database path, which is the risky part, does not depend on that, and everything run against the old code does.

Numbers, row counts only.

| | |
| --- | --- |
| Rows in the backup | users 2, tasks 1, messages 2, channels 1, calls 1, call_participants 2, scrums 1, scrum_entries 1, scrum_participants 2, task_activities 3, task_comments 1, teams 1, team_members 1, notifications 1, org_configs 5, refresh_tokens 5, push_subscriptions 0, attachments 0, task_watchers 0 |
| Migrations | `version 16 -> 24`, not dirty, in the real `migrate` service |
| Existing rows, real backup through the real migrations | 36 of 37 tables identical row for row on their old columns; only `schema_migrations` differs |
| Row counts across the upgrade | No pre-existing table changed; 6 new tables, all empty |
| Backup step / `up -d` / nginx restart | 1.6 s / 11.9 s / 0.8 s |
| Unavailable to a client polling every 200 ms | 16.3 s |
| Image build | Not timed as a production step; about 2 minutes here with a warm cache |
| `0022` | 0 calls closed: the only call had already ended |
| `0023` | 0 watcher rows, so 0 and 0, and nobody loses credit |
| Real browser after the upgrade | Sign-in, tasks, chat, calls, calendar and settings load with no page errors; 2 console 401s are the probes before sign-in |

Because the real data has nothing for `0022` and `0023` to act on, both were also run on rows planted on a copy.
Call 5 days old, person left after 60 minutes: ended, 60 minutes.
Call 3 days old, last seen joining 10 minutes in: ended, 10 minutes.
Call 2 hours old: left active.
Watcher with a `task.added` notification: somebody else. Watcher without: self.

Webhook, run for real through the production compose config with a real browser in a call through nginx:
`room_started`, `participant_joined`, `participant_left` and `room_finished` all sent and all answered 200, one session recorded, opened and closed, 2 events.
The key is `LIVEKIT_API_KEY`, and the URL is `http://teamup:8080/api/livekit/webhook` over the compose network.
nginx needs no route, but its `/api/` block does expose the path: an unsigned post through nginx answers 401.

Rollback, each run for real.
- Only changing `TEAMUP_IMAGE`: `migrate` refuses (`no migration found for version 24`), the server does not start, the site answers 502, the database is untouched.
- `migrate down` eight times with the new image, then the old image: 6 s, `pg_dump --schema-only` identical to the backup's, old server signs in and works. Tasks, messages and calls survive. Calendar blocks, unheld meetings, standing meetings, call sessions and events, knock requests, `added_by` and added-to scrum rows are destroyed. Going forward again applies `16 -> 24`.
- Restoring the pre-upgrade backup: 26 s, all row counts equal to just before the upgrade, old server signs in and works. Everything since the backup is lost.
- Old server on the new schema, past `migrate`: the stand-in worked on all pages and writes, with no errors. It is not recommended: a watcher it adds has no `added_by`, and it is one stand-in.

A tab open through the upgrade, on the old `autoUpdate` build, with the real old-to-new swap: not reloaded, the new worker waited, one more reload stayed on the old build, and closing all tabs gave the new build.
So this upgrade does not reload anybody, and people on old tabs have to close them to get the new version.

Also seen: a port other than 443 breaks the app's socket, because nginx's `$host` drops the port and the Origin check then refuses it. It cannot happen on the production ports, so nginx is unchanged and the docs have a troubleshooting line.
On a laptop, `use_external_ip: true` advertises an address the browser cannot hairpin to, so the call aborted with `participant_connection_aborted` until the rehearsal set `node_ip: 127.0.0.1`. That is the one change to the LiveKit config, and the webhook part was left as it is.

Not verified.
- The real live image, and old-code behaviour of anything beyond sign-in, pages and basic writes.
- The real VM, a public address, and a real certificate. The steps for a changed address are written and not run.
- A call between two people in this rehearsal. One person, with fake media.
- The upgrade's time with a realistically sized database. This one is 104 KB.
- Whether the browser's own update check fires on a signed-in page: seen only on the login page.
- MinIO data: `attachments` has 0 rows, so no file was downloaded after the upgrade. No migration touches files.
- Whether the VM changed after this backup was taken. If it did, the numbers above move.

`docs/DEPLOYMENT.md` now covers the webhook, the migrations, the steps, what people's tabs do, the rollback with these results, and a VM with no fixed address.

The rehearsal's image was built before the last change to `ActiveCallProvider.tsx` (ticket 35), which has no bearing on the database or the deployment. Rebuild before the real upgrade in any case.

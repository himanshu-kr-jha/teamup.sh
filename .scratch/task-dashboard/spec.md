# Task dashboard, saved scrums, and startup readiness

Status: resolved
Target: one startup, 50 people, self-hosted.

## What we are building

A clean task dashboard where everybody sees their tasks summarised, at the level they answer for.

| Who | Sees |
| --- | --- |
| Employee | Only their own work: pending, ongoing, blocked, overdue, recently completed. |
| Manager | The people on teams they manage (including nested teams), in two ways: **By person** (pick one person, see their tasks) and **Scrum summary** (everybody at once: who is on what). |
| Admin / owner (the "organization" view) | The same as a manager, for the whole organization, with a team filter. |

The server decides the scope. The client never asks for a wider one, and cannot get one by asking.

## Vocabulary

- **Pending** = status `todo`.
- **Ongoing** = status `in_progress` or `in_review`.
- **Blocked** = status `blocked`.
- **Completed** = status `done`, dated by `completed_at`, credited to the assignee.
- **Overdue** = not done, and `due_date` has passed.

## Decisions

1. **Scrum summary is live.** It is computed from current tasks on each read, and the client refetches on focus and every 30 seconds. A newly added pending or ongoing task appears without anybody doing anything.
2. **"Done" in the scrum summary means done since the last saved scrum** for that scope, not "in the last 24 hours". A rolling 24 hours loses Friday's work on Monday. With no earlier scrum, it falls back to the last 7 days.
3. **A completed scrum is an immutable snapshot.** "Complete scrum" writes `scrums`, `scrum_participants` and `scrum_entries`. Names, titles and statuses are copied in, so later renames, edits or deletions do not rewrite history. People with no work are recorded too. There is no edit or delete endpoint.
4. **Who may complete a scrum:** the manager of the team, or an org admin/owner. `team_id` is nullable: null means "everybody the caller answers for at that moment".
5. **Who may read a saved scrum:** an org admin/owner, whoever held it, or the manager of its team. Everybody else gets 404, not 403, so ids cannot be probed.
6. **Tracking is derived from existing data, not a second ledger.**
   - Completed = tasks assigned to the person with `completed_at` in the range.
   - Involved = tasks where they are or were the assignee (from `task_activities`), the reporter, a watcher, or a commenter.
   - Soft-deleted tasks are excluded.
7. **Authority reuses the existing rule** (`authorityFacts` / `hasAuthorityOver` in `internal/tasks`). Org admins are not visible as "managed people" to a manager, matching that rule. No second statement of who outranks whom.
8. **No drag-and-drop, sprints, charts library, CSV export or scheduled scrums.** Not asked for.
9. **An admin always gets the organization view,** whether or not they also run teams. The existing board's "admin who runs teams keeps their team board" rule is not carried over, because the table above says admin sees the whole organization.
10. **A manager's own work is not on their dashboard** unless they sit on a team they manage. The view is "the people on teams they manage". A manager can still open their own contribution detail.
11. **A team filter includes nested teams,** for the dashboard, scrums and contributions. A manager asking for a team they do not answer for gets 404. An employee's filter is ignored, because they have nothing to narrow.
12. **"That scope" for done-since is exact:** a team's last scrum for a team filter (whoever held it), the last organization-wide scrum for an admin with no filter, and that manager's own last no-team scrum for a manager with no filter. A team scrum does not reset the unfiltered view, or the other way round.
13. **An employee's completed work is always the last 7 days,** and `lastScrumAt` is null for them, because no scrum is saved for a self scope.
14. **`newSinceLastScrum` is open tasks created after the done-since point.** It uses `created_at`, so a task reassigned to somebody later is not "new" to them.
15. **"Manager of its team" for reading a saved scrum follows the chart,** so the manager of a parent team may read a nested team's scrums. That matches who may complete one.
16. **Involved respects the range this way:** a task counts if it was live during it, meaning created by `to` and not completed before `from`. Open is current and ignores the range. Completed uses `completed_at` in the range. With no `from` or `to`, the range is the last 30 days.
17. **Contribution detail uses `hasAuthorityOver`,** not the audience list, so a deactivated person's history stays readable by whoever has authority over them, and a manager can read their own. Soft-deleted users are 404.

## API (all under the authenticated group)

- `GET /tasks/dashboard?teamId=` - `{ scope: "self"|"team"|"org", teams: [{id,name}], totals: {...}, people: [{ userId, name, teams, counts: {pending, ongoing, blocked, overdue, completed}, tasks: {...buckets}, newSinceLastScrum: [taskIds] }], lastScrumAt }`
- `POST /scrums` body `{ teamId?, notes? }` - saves the snapshot. Returns the scrum.
- `GET /scrums?teamId=&limit=&before=` - saved scrums the caller may read, newest first.
- `GET /scrums/:id` - one saved scrum with participants and entries.
- `GET /tasks/contributions?from=&to=&teamId=` - per person: `completed`, `involved`, `open`. Employee gets only self.
- `GET /tasks/contributions/:userId?from=&to=` - the tasks behind those numbers. 404 if the caller has no authority over that person.

## Frontend

- `/tasks` opens on a new **Overview** tab, alongside the existing board.
- Employee tabs: Overview, Board. Manager and admin tabs: Overview, By person, Scrum, History, Contribution, Board.
- Plain role-appropriate language, works on a phone, loading/empty/error states all handled.

## Non-goals for this effort

- Rewriting the existing board, chat or calls beyond bug fixes.
- OIDC completion. Password login is the real path for now.

## Tickets

See `issues/`. Status lives in each ticket, and the running log is `progress.md`.

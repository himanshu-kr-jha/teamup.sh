# Tasks somebody was added to count

Status: resolved

Implement decisions 17 and 18 across dashboard, scrum snapshot and contributions.
Check adding, removing and reassigning: an assignee who is also on the task appears once, under their assigned work.

## Comments

### 2026-11-07 - Senior developer: built, and what the frontend needs

Migration `0021_scrum_added_to`: `scrum_participants.added_to`, `scrum_entries.added_to`. Scrums saved before it read as zero and `false`. Rolling back removes added-to entries, so they cannot read as somebody's own work.

- `GET /api/tasks/dashboard`: each person has `tasks.addedTo` (the same task summaries) and `counts.addedTo`, and there is `totals.addedTo`. Open tasks they watch and are not assigned. Not in any other bucket, overdue included.
- `GET /api/tasks/contributions`: each person has `addedTo`, a number. `GET /api/tasks/contributions/:userId`: `addedTo`, a list of task summaries, in the same range as `involved`. `completed` is unchanged.
- `GET /api/scrums/:id`: `participants[].counts.addedTo`, and `entries[].addedTo` true for those tasks, so they can be drawn apart from the person's own.
- `POST /api/tasks/:id/watchers` notifies, as `task.added`, only when it really added somebody and they are not the one asking. `AddWatcher` now returns whether it did.

# 11 - A team lead can add any employee to their team and gain authority over them

Status: resolved
Type: bug

## What is wrong

`POST /teams/:id/members` is gated only by `requireTeamManager` (the caller manages that team). The service then inserts any user of the organisation, with no check on who is being added and no consent from the user or the manager who really has them. Every authority decision in `internal/tasks` (`authorityFacts` / `hasAuthorityOver`, board `scope=team`, `AssignableUsers`, delete, reassign, and the planned dashboard "managed people") is derived from `team_members`. So a lead can pull in anybody who is not an org admin and immediately:

- see that person's tasks on the team board and (per spec) in the dashboard / scrum,
- assign work to them, reassign their tasks, delete tasks they filed,
- do this to their own manager or to a peer's reports (add the boss to your sub-team and you now "manage" the boss).

The spec rule "Org admins are not visible as managed people" only protects admins, not managers or other teams' staff.

## Where

- `internal/org/routes.go:86` route gated by `requireTeamManager` only.
- `internal/org/routes.go:720-746` `AddTeamMember` handler.
- `internal/org/org.go:848-872` `AddTeamMember` inserts any `users` row of the org.
- Consumers: `internal/tasks/tasks.go:743-800` (`authorityFacts`), `:209-300` (`AssignableUsers`), `:258` ManagedBy clause.

## How to reproduce

1. Seed org with admin, manager M (manages team B), manager P (manages team A, parent of B), member E (reports to P, on team A only).
2. As M, `POST /teams/<B>/members {"userId": P}` returns 200.
3. As M, `GET /tasks/assignable-users` now lists P, and `PATCH /tasks/<task assigned to P> {"assigneeId": M}` succeeds, and `DELETE /tasks/<task P filed>` succeeds. Same with E.
Today all succeed; they should be refused.

## Simple fix

In the non-admin branch of the add-member path, refuse a target who manages any team (`teams.manager_id = target`) or who already sits on a team the actor does not manage (admins unrestricted). A one-query check in `AddTeamMember` given an `Actor` (same `Actor{UserID, IsAdmin}` type `CreateTeam` already takes).

## Answer

`POST /teams/:id/members` now goes through `AddTeamMemberAs`.
A lead may add somebody who is already on a team in their chart, or somebody with no ties outside it, and themselves.
Running a team counts as a tie even with no `team_members` row, otherwise the boss, who sits on no team, would pass as "on no team yet".
Org admins may add anybody, and the CSV import is admin-only and keeps calling `AddTeamMember` directly.
Test: `TestWhoALeadMayAddToTheirTeam` in `internal/org/routes_test.go:568`.
Nothing in `internal/org` tests depended on the old behaviour.
Existing memberships a lead already claimed this way are not removed.

# 54 — Reconcile the team board with hasAuthorityOver

**What to build:** Make the `scope=team` task filter and `hasAuthorityOver` answer the same question, so a manager cannot be shown a card that every action on it refuses.

**Blocked by:** None — can start immediately

**Status:** completed — product decision taken (read-only); tracked as GitHub #47.

- [x] Decide the rule once: is an org administrator who sits on a managed team visible to that team's manager, actionable by them, or neither? — **visible, not actionable**, and the board now says so
- [x] Express it in one place both the `ManagedBy` filter and `hasAuthorityOver` read
- [x] Add a test asserting the two agree across a whole org chart, not just one pair
- [x] Decide what `scope=team` shows an administrator who manages no team (today: nothing, while they may act on everyone) — the whole organisation; one who *does* manage teams keeps their teams

**Type:** bug

## Evidence

Two predicates decide who a manager may see and who they may touch, and they disagree.

- **Visibility** — `internal/tasks/tasks.go:213-234`, the `ManagedBy` filter. Walks down from every team the manager runs and returns any task assigned to a member. No exclusions.
- **Authority** — `internal/tasks/tasks.go:688-737`, `hasAuthorityOver`. Walks the same subtree, but first returns `false` if the subject holds `owner`/`admin` or `is_admin` (lines 697-711).

So anyone who administers the organisation *and* sits on a team is visible to that team's manager and immune to them. This is not exotic: the person who installs and administers TeamUp at a 55-person startup is usually also an engineer on a team.

### Reproduction

`internal/hierarchyverify/consequences_test.go:TestManagerSeesATaskItCannotTouch`. Quinn holds the `admin` role and is a member of Backend; Bob manages Backend.

```
    Bob's team board DOES contain Quinn's task
    but DeleteTask is refused: FORBIDDEN: only the person who filed this task, their manager, or an administrator may delete it
    and reassigning to Quinn is refused: FORBIDDEN: you can only assign work to yourself or to somebody on a team you manage
    AssignableUsers for Bob returns 17 people and excludes Quinn, while the team board includes him — the two disagree
```

Three surfaces, three different answers about the same person:

| Surface | Includes Quinn? | Code |
|---|---|---|
| `GET /tasks?scope=team` | yes | `internal/tasks/tasks.go:213` via `internal/tasks/routes.go:105` |
| `GET /tasks/assignable-users` | no | `internal/tasks/tasks.go:296-302` |
| `PATCH /tasks/:id {assigneeId}` | refused | `internal/tasks/tasks.go:643-650` |
| `DELETE /tasks/:id` | refused | `internal/tasks/tasks.go:916` |

The user-visible bug: a manager's board shows a card, they drag it, they get a 403 quoting a rule that contradicts what they are looking at. They cannot even hand it back after moving it away.

### Scale of the disagreement

`internal/hierarchyverify/hierarchy_test.go:TestVisibilityAndAuthorityAgree` cross-checks every (manager, subject) pair in a 55-person, four-level chart:

```
    DISAGREEMENT: Eve -> Quinn : visible in scope=team? true, MayAssign/hasAuthorityOver? false
    DISAGREEMENT: Bob -> Quinn : visible in scope=team? true, MayAssign/hasAuthorityOver? false
    DISAGREEMENT: Quinn -> Ada : visible in scope=team? false, MayAssign/hasAuthorityOver? true
    ... (Quinn -> every other person)
    total disagreements between the task-list filter and hasAuthorityOver: 56
```

Two classes, and they want different answers:

1. **Visible, not actionable** (`Eve -> Quinn`, `Bob -> Quinn`) — 2 pairs. The real bug above.
2. **Actionable, not visible** (`Quinn -> everyone`) — 54 pairs. An administrator may act on anybody but their `scope=team` board is empty, because they manage no team. Arguably intended, but it means an admin has no board that shows the work they are entitled to touch. Worth a deliberate decision rather than a side effect.

A third, benign, is recorded in `TestManagerOwnWorkMissingFromTeamBoard`: a manager who sits on the team *above* the one they run does not see their own work under `scope=team`. `scope=mine` covers it.

### Note on the check order

`hasAuthorityOver` short-circuits on `isOrgAdmin` at `internal/tasks/tasks.go:689` before any organisation predicate. Every caller today scopes by org first (`CreateTask` at `:439-447`, `UpdateTask` at `:635-642`, `DeleteTask` at `:906-914`), so it is not reachable — `TestOrgChartAtDepth` confirms a cross-org assignment is refused with `NOT_FOUND: assignee not found in this organization`. Recorded so the next caller does not assume the function is self-scoping.

## Comments

**Root cause.** Three surfaces each ran their own copy of "who outranks whom". The
`ManagedBy` filter walked the managed subtree with no exclusions; `hasAuthorityOver`
walked the same subtree but first excluded anybody holding `owner`/`admin`/`is_admin`;
`AssignableUsers` carried a third copy of the exclusion inline in its SQL. Nothing tied
them together, so they drifted — 56 disagreeing pairs in a 55-person chart.

**The decision, as built.** Read-only. The manager still sees the admin's card. Every
task returned from the list and from `GET /tasks/:id` now carries a `can` object —
`{edit, reassign, delete, reason}` — derived from the same `hasAuthorityOver` the write
paths call, so the board cannot offer a button the API refuses.

- `delete` mirrors `DeleteTask`: authority over whoever filed the task.
- `reassign` is authority over whoever currently holds the work (true when unassigned).
- `edit` is unconditionally true, because `UpdateTask` gates the assignee and nothing
  else. Carried anyway so the client reads one shape, and so a future tightening has an
  obvious place to land. Said plainly in the comment on `Abilities`.

**One source of truth.** `hasAuthorityOver` no longer owns any SQL of its own. Both it
and the board read `authorityFacts`, one query returning `(name, administers, managed)`
per person, and both apply `authorityFact.allows()`. Adding a fourth surface cannot
reintroduce a fourth copy of the rule without deleting that function.

**N+1.** `attachAbilities` collects every reporter and assignee on the page, then makes a
single `authorityFacts` call. A 40-card board costs three statements: rows, labels,
authority. Pinned by `internal/tasks/tasks_test.go:TestABoardResolvesItsAbilitiesInOnePassNotOnePerCard`,
which counts statements with a real pgx `QueryTracer` rather than trusting the comment.

**Write path changed (deliberate, one gate).** `UpdateTask` now also requires authority
over the *current* assignee when `assigneeId` changes. Without it a manager who may not
delete an admin's task could still move it quietly onto one of their own reports, and the
`reassign` flag would have been promising something the server did not honour. Test:
`internal/tasks/tasks_test.go:TestReassigningNeedsAuthorityOverWhoeverHoldsTheWork`.

**Mirror case.** An administrator who runs no team gets the whole organisation on
`scope=team`; one who does run teams keeps their subtree, because widening theirs would
drop sales onto a tab labelled with their own team and the team filter only offers teams
they manage. Justified in the comment on the `ManagedBy` branch in `ListTasks`.

**Tests added**

- `internal/tasks/tasks_test.go:TestTheBoardAndHasAuthorityOverNeverDisagree` — the
  property, over a chart containing an admin-who-is-also-a-member, a manager, an ordinary
  member, an outsider and a second organisation.
- `internal/tasks/tasks_test.go:TestTheBoardsDeleteFlagPredictsWhatDeletingDoes` — all 25
  (caller, reporter) pairs, board flag vs what `DeleteTask` actually returns.
- `internal/tasks/tasks_test.go:TestAManagerSeesAnAdminsWorkOnTheirTeamBoardButCannotActOnIt`
- `internal/tasks/tasks_test.go:TestAnAdminWhoManagesNoTeamStillHasATeamBoard`
- `internal/tasks/tasks_test.go:TestABoardResolvesItsAbilitiesInOnePassNotOnePerCard`
- `internal/tasks/routes_test.go:TestTheListAndTheDeleteGiveTheSameAnswer` — the same
  invariant over HTTP, plus the 404 for a caller in another organisation.

**Left alone**

- `AssignableUsers` still carries its own inline copy of the admin exclusion. It was
  already correct and already has a test that every name it offers is accepted
  (`TestAssignableUsersMatchesWhoMayActuallyBeAssigned`), so folding it into
  `authorityFacts` would have been a rewrite of a working query inside a bug fix. It
  remains the one place the rule is stated twice.
- `hasAuthorityOver` used to return an *error* for a subject not present in the
  organisation; it now returns false. Unreachable — every caller looks the row up
  org-scoped first — and false is the fail-closed answer.
- `internal/tasks/tasks.go:ListTasks` still filters `scope=team` on `assignee_id`, so an
  unassigned task on a managed team appears on nobody's team board. Pre-existing, out of
  scope.

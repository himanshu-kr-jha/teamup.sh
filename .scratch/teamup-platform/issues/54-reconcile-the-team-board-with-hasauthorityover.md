# 54 — Reconcile the team board with hasAuthorityOver

**What to build:** Make the `scope=team` task filter and `hasAuthorityOver` answer the same question, so a manager cannot be shown a card that every action on it refuses.

**Blocked by:** None — can start immediately

**Status:** open — needs a product decision; tracked as GitHub #47.

- [ ] Decide the rule once: is an org administrator who sits on a managed team visible to that team's manager, actionable by them, or neither?
- [ ] Express it in one place both the `ManagedBy` filter and `hasAuthorityOver` read
- [ ] Add a test asserting the two agree across a whole org chart, not just one pair
- [ ] Decide what `scope=team` shows an administrator who manages no team (today: nothing, while they may act on everyone)

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

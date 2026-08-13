# 53 — Serialise the team cycle check with the write it guards

**What to build:** Make `requireNoCycle` and the `parent_id` UPDATE a single serialised unit, so two concurrent re-parents cannot each pass the check and together produce a loop.

**Blocked by:** None — can start immediately

**Status:** completed — UpdateTeam now takes a per-organisation advisory lock and runs the cycle check inside the same transaction as the write (commit f9b3088). Independently re-tested: no loop in 40 concurrent attempts.

- [ ] Move the ancestry check inside the same transaction as the UPDATE in `UpdateTeam`
- [ ] Take a lock that actually orders the two writers (row locks on the teams involved, or a per-org advisory lock) — a plain `BEGIN` at READ COMMITTED does not stop this
- [ ] Add a test that fires two opposing re-parents concurrently and asserts no cycle results
- [ ] Add a test asserting every surviving team is still reachable from a root after concurrent re-parents
- [ ] Consider a repair path for an install that already has a loop, since nothing today can detect one

**Type:** bug

## Evidence

`internal/org/org.go:510-543` (`UpdateTeam`) calls `requireNoCycle` at line 521, which runs its own `SELECT` on its own pooled connection (`internal/org/org.go:552-575`). The `UPDATE` at line 527 then runs as a separate statement on a separate connection. Nothing holds a lock between them, so two requests that are each legal against the pre-move tree both pass and both commit.

Contrast `DeleteTeam` at `internal/org/org.go:600-604`, which does take `FOR UPDATE` before grafting. The cycle path was not given the same treatment.

### Reproduction

Two teams A and B, both roots. Fire `UpdateTeam(A, parent=B)` and `UpdateTeam(B, parent=A)` concurrently.

`internal/hierarchyverify/claims_test.go:TestConcurrentReparentCanStillMakeALoop` — 40 attempts, real Postgres:

```
--- FAIL: TestConcurrentReparentCanStillMakeALoop (14.94s)
    36 of 40 concurrent re-parents created a cycle the check is supposed to refuse
```

It is not a narrow window. It lands roughly nine times in ten.

### What it costs

`internal/hierarchyverify/escalation_test.go:TestCycleGrantsMutualAuthority` drives the race, then asserts the consequence:

```
    a cycle now exists: A.parent = B and B.parent = A
    PRIVILEGE ESCALATION: AliceMgr can now assign work to bWorker, who is on a team she never managed
    PRIVILEGE ESCALATION: BobMgr can now assign work to aWorker
    AliceMgr's team board now shows: [aWorker bWorker]
    BobMgr's team board now shows: [aWorker bWorker]
    teams reachable from a root: the org now has 0 root team(s)
```

Three separate harms, all permanent and all silent:

1. **Privilege escalation, mutually.** Every recursive walk in the codebase UNIONs, so a cycle terminates rather than hanging — and instead makes each manager an ancestor of the other. `hasAuthorityOver` (`internal/tasks/tasks.go:716-732`), `ManagesTeam` (`internal/org/org.go:486-496`), `ListTasks`' `ManagedBy` filter (`internal/tasks/tasks.go:218-228`), `ManagedTeams` (`internal/tasks/tasks.go:343-350`) and `AssignableUsers` (`internal/tasks/tasks.go:278-285`) all walk `parent_id` the same way and all grant the same crossed authority. `ManagesTeam` backs `requireTeamManager` (`internal/org/routes.go:105`), so the escalation reaches team membership too: each manager can now add and remove people on the other's team.

2. **The org chart detaches from every root.** The count above is `0 root team(s)`. Any UI that renders the tree from the roots down loses both teams and everyone on them.

3. **Nothing detects it.** The code comment at `internal/org/org.go:545-551` correctly predicts this failure mode ("a cycle terminates rather than hanging, and instead quietly makes everybody in the cycle a manager of everybody else") and then leaves the check unserialised.

### Reachability

`PATCH /api/teams/:id` with `{"parentId": ...}` — `internal/org/routes.go:71`, admin-gated. Two administrators reorganising at the same time, one administrator's drag-and-drop UI firing two PATCHes, or a scripted bulk reorg. It does not require an attacker; a reorganisation is exactly when both requests happen.

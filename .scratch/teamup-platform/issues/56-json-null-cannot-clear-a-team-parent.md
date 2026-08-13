# 56 — JSON null cannot clear a team's parent over HTTP

**What to build:** Let a client say "make this team a root" the way JSON says it — `{"parentId": null}` — instead of only via the empty string.

**Blocked by:** None — can start immediately

**Status:** completed — parentId is read as raw JSON, so null and "" both mean top level and an absent key still means leave it alone (commit 60f9c8d).

- [ ] Distinguish an absent `parentId` from an explicit `null` when decoding the PATCH body
- [ ] Keep `""` working, or migrate the frontend off it in the same change
- [ ] Add a route-level test covering all three: absent, `null`, and an id

**Type:** bug

## Evidence

`UpdateTeam` in the service layer takes a genuine three-state parent — `**uuid.UUID`, documented at `internal/org/org.go:503-509`. The HTTP handler collapses it back to two.

`internal/org/routes.go:602-627`:

```go
var req struct {
    ...
    ParentID    *string `json:"parentId"`
}
...
var parentRef **uuid.UUID
if req.ParentID != nil {
    parentID, err := optionalUUID(req.ParentID)   // "" -> nil
    ...
    parentRef = &parentID
}
```

`encoding/json` leaves `req.ParentID` as `nil` for **both** an absent key and an explicit `null`. So `{"parentId": null}` takes the same branch as `{"name": "x"}` and the team does not move. Only `{"parentId": ""}` reaches `optionalUUID` (`internal/org/routes.go:582-591`) and produces the pointer-to-nil that lifts the team to the root.

### Reproduction

`internal/hierarchyverify/http_test.go:TestReparentOverHTTP`, driving the registered route:

```
--- PASS: .../moving_a_team_under_another_team
--- PASS: .../a_cycle_is_refused_over_HTTP          (400, "a team cannot be moved underneath itself or one of the teams below it")
--- PASS: .../omitting_parentId_leaves_the_team_where_it_is
--- FAIL: .../JSON_null_should_lift_a_team_to_the_root
      PATCH {"parentId": null} did NOT lift Frontend to the root; parent is still 4f3d7fc6-...
--- PASS: .../empty_string_does_lift_a_team_to_the_root
```

The request returns `200 OK` with the unchanged team in the body. It does not fail; it silently does nothing.

### Severity

Low, and deliberately filed as such. The shipped frontend already sends `''` — `web/src/features/admin/AdminPage.tsx:910` (`parentId: moveToId === TOP_LEVEL ? '' : moveToId`) — and `web/src/shared/api/client.ts:132` documents the convention. So the UI works today.

It costs anything that is not this frontend: a curl script, a future mobile client, an integration. Those will reach for `null`, get a 200, and quietly not move the team.

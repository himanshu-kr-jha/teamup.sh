# 42 — Validate PATCH /api/tasks/:id field values instead of splicing raw JSON into SQL

**What to build:** The task update handler binds a free-form `map[string]interface{}` and passes the raw JSON values straight into typed Postgres columns. A bad date or a number where a string belongs becomes a 500 from the driver instead of a 400. Type-check the updatable fields the way `CreateTask` already does.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Failing test: `PATCH /api/tasks/:id` with `{"dueDate":"not-a-date"}` returns 400, not 500
- [x] Failing test: `PATCH /api/tasks/:id` with `{"title":7}` returns 400, not 500
- [x] Extend `validateUpdates` to cover `title`, `description`, `dueDate`, `startDate`, `storyPoints`, `position`
- [x] Reuse the `optionalTime` helper the create path already has

**Type:** bug

## Evidence

`internal/tasks/routes.go:251` binds an untyped map. `internal/tasks/tasks.go:701-707` splices whatever arrives into the statement executed at `tasks.go:728`:

```go
args = append(args, value)
sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
```

`validateUpdates` only checks `status`, `priority` and `taskType` (`tasks.go:566-572`) plus `assigneeId`/`parentTaskId`. The rest of `updatableFields` is unchecked.

Verified against the live database with pgx v5:

- `{"dueDate": "not-a-date"}` → `ERROR: invalid input syntax for type timestamp with time zone: "not-a-date" (SQLSTATE 22007)`. Same for `startDate`.
- `{"title": 7}` → `failed to encode args[0]: unable to encode 7 into text format for varchar (OID 1043): cannot find encode plan`.

Both surface as `500`. The column names themselves are safe — they come from the `updatableFields` allow-list, so this is a validation and status-code problem, not SQL injection.

`CreateTask` shows the intended pattern: `internal/tasks/routes.go:154-165` uses a typed struct with `binding:"oneof=…"` plus `optionalTime`/`optionalUUID` helpers. The PATCH path needs the same.

Benign by comparison and not worth blocking on: JSON numbers into the `integer` columns `story_points`/`position` encode fine, and `parentTaskId` is already normalised to `uuid.UUID` by `validateUpdates`.

### Related, lower priority

`SearchFiles` (`internal/chat/chat.go:1730`) joins `JOIN channels c ON c.id = a.ref_id::uuid` while `attachments.ref_id` is `character varying` with no CHECK constraint. It is safe today because every writer validates `ref_id` through `uuid.Parse` first (`internal/files/files.go:198`) and `a.category = 'chat'` filters before the cast — but the guarantee lives in Go, not the schema. One non-UUID `ref_id` on a `category='chat'` row would break file search for the whole organisation with `invalid input syntax for type uuid`. A CHECK constraint or a typed column would make it structural.

## Comments

**Root cause.** `UpdateTask` type-checked only the three enum fields. Every other entry in `updatableFields` went into `args` as whatever `encoding/json` produced and was handed to pgx, so the *column* decided whether the request was valid. A column rejection is a driver error, and `httpx.Error` maps a non-`AppError` to 500 with the message replaced — so the caller got `{"error":"failed to update task"}` and no way to know which field was wrong.

**Failing test.** `internal/tasks/routes_test.go:45` (`TestUpdateTaskRejectsValuesTheColumnCannotHold`), a table of ten bad bodies driven through the real gin router. At RED, ten of ten failed:

```
--- FAIL: TestUpdateTaskRejectsValuesTheColumnCannotHold/date_that_is_not_a_date
    routes_test.go:73: want 400, got 500: {"error":"failed to update task"}
--- FAIL: .../number_where_a_title_belongs
    routes_test.go:73: want 400, got 500: {"error":"failed to update task"}
--- FAIL: .../title_removed_from_a_not-null_column
    routes_test.go:73: want 400, got 500: {"error":"failed to update task"}
--- FAIL: .../blank_title
    routes_test.go:73: want 400, got 200: {... "title":"   " ...}
--- FAIL: .../fractional_story_points
    routes_test.go:73: want 400, got 200: {... "storyPoints":1 ...}
```

Two of those are worse than a 500. `{"title":"   "}` was accepted, and `{"storyPoints":1.5}` was accepted *and silently truncated to 1* — the request succeeded while storing something the caller never sent.

`TestUpdateTaskRejectsTheWholeBodyWhenOneFieldIsBad` (`routes_test.go:148`) also failed at RED; it pins that validation runs before the write, so a half-valid body changes nothing.

**Fix.** `internal/tasks/tasks.go`, inside `validateUpdates` (after the enum checks) plus one new helper:

- `title` — must be a string, non-blank after trimming, and at most `titleMaxLength` (500, matching `varchar(500)`).
- `description` — must be a string when present; `null` is allowed and clears the column.
- `dueDate` / `startDate` — must be a string, parsed with the create path's `optionalTime`, and **normalised to a `time.Time`** so a string never reaches the driver. `null` or `""` clears the column, as on create.
- `storyPoints` / `position` — go through the new `wholeNumber` helper (`tasks.go`), which accepts a JSON number that is integral and fits int32 and returns an `int`. `null` clears.

Every rejection is `apperrors.CodeBadRequest` with the field name in the message, which `httpx.Error` turns into a 400 whose body reaches the caller intact.

`TestUpdateTaskAcceptsWellFormedValues` (`routes_test.go:98`) is the other half: well-formed values still apply, and `null` still clears each nullable column. It passed at RED and still passes, so the fix did not buy 400s by breaking the happy path.

**Deliberately left alone.**

- **PATCH is now marginally stricter than POST on `title`.** Create uses `binding:"required"`, which rejects `""` but accepts `"   "`. PATCH rejects both. A task with a whitespace title is a defect either way; aligning create is a one-line change but belongs to its own ticket rather than being smuggled in here.
- **`beforeValue` (`tasks.go`) returns `""` for `dueDate`, `startDate`, `storyPoints` and `position`,** so every PATCH of those records a task-activity row with a null `old_value` even when the value did not change. Pre-existing, orthogonal to the status code, untouched.
- **`projects` and `sprints` tables.** Confirmed unused by this package; nothing was built on them.
- **The `SearchFiles` / `attachments.ref_id` note above** — different package, left for its own ticket.

**Verification** (`TEAMUP_TEST_DATABASE_URL` → `teamup_test_c`):

```
go build ./...                                  clean
go vet ./...                                    clean
go test -race ./internal/tasks/...              ok  17.8s
go test -race -v ./internal/... ./pkg/... ./cmd/...
    289 passed, 0 failed, 0 skipped
```

Baseline before the change was 286 passed / 0 failed / 0 skipped; the three added tests account for the difference.

`go build ./...` needs `cmd/teamup/web/dist` to exist (see issue 43) — the SPA was built locally first.

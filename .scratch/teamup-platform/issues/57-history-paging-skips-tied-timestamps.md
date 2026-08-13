# 57 — Channel history paging skips messages that share a timestamp

**What to build:** Make the `before` cursor page on a total order, so a run of messages sharing one `created_at` cannot be stepped over.

**Blocked by:** None — can start immediately

**Status:** completed — the cursor is now (created_at, id) (commit f9b3088). Note the trigger is rarer than it first appears: created_at is set from Go per statement, so it needs two messages inside one microsecond AND a page boundary between them.

- [ ] Page on a tuple (`created_at`, `id`) rather than `created_at` alone, or use a monotonic cursor
- [ ] Add a test that seeds a run of messages with an identical timestamp and walks past it
- [ ] Reject a malformed `before` instead of silently ignoring it (see below)

**Type:** bug

## Evidence

`internal/chat/chat.go:1280`:

```sql
AND ($2::timestamptz IS NULL OR m.created_at < $2)
```

The client's next cursor is the `created_at` of the oldest message on the page it just received. Every message sharing that instant is `< $2`-false and is skipped — the whole tied run, not just the one already seen.

### Reproduction

`internal/hierarchyverify/escalation_test.go:TestGetMessagesPagingWithIdenticalTimestamps` — 60 messages in one channel, all with the same `created_at`, `limit=50`:

```
    60 messages sharing one timestamp: first page 50, second page 0 (10 expected)
    paging past a run of identical timestamps returned 0 messages, want 10
```

Ten messages are unreachable. Paging terminates cleanly and the client concludes it has reached the start of the channel.

### The ordinary path is fine

`TestGetMessagesPagingBackwards` — 140 messages, one second apart, `limit=50`:

```
    walked 3 pages and saw 140 of 140 messages
```

No duplicates, no gaps, terminates. So the claim that history pages backwards beyond 50 holds for real conversation; this ticket is about the tied-timestamp edge.

### When timestamps tie

`created_at` defaults to `now()` (`migrations/0001_initial_schema.up.sql`, `messages` table), which in Postgres is transaction start time — **constant for every row inserted in one transaction**. So any bulk write ties: a data import, a migration backfill, a future "paste a transcript" feature, seeded demo data. Live chat over the WebSocket writes one message per transaction at microsecond resolution, so it does not tie in practice today.

### Second, smaller defect in the same handler

`internal/chat/routes.go:489-494`:

```go
if b := c.Query("before"); b != "" {
    if parsed, err := time.Parse(time.RFC3339Nano, b); err == nil {
        before = &parsed
    }
}
```

A `before` that does not parse is discarded, and the endpoint returns the newest page instead. A client that formats its cursor wrong gets page 1 forever and its "load older" button never advances — with no error to point at. A 400 would say what happened.

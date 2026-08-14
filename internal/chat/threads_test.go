package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
)

// ── Threads (issue 28) ────────────────────────────────────────────────────
//
// reply_to has been stored on every message and round-tripped through the API
// since the beginning, and nothing ever read it: there was no way to fetch a
// thread and no way to see one. A reply was therefore recorded and then
// rendered inline like any other message, which makes the relationship it
// records invisible.
//
// The tests below fix the three decisions that turn a column into a feature —
// how deep a thread goes, what a reply does to the channel timeline, and who
// is allowed to read one.

// ── Depth ─────────────────────────────────────────────────────────────────

// A thread here is one level deep, as it is in Slack: a reply to a reply joins
// the thread it was already in rather than starting a nested one.
//
// The rule has to be enforced on the way in, not on the way out. reply_to
// points at a single parent, so nothing in the schema stops a chain — and a
// reader that flattens what it finds still has to walk an arbitrarily deep
// tree to do it, on every read, for ever. Re-pointing the reply at the root
// once, when it is written, is what actually keeps the depth at one.
func TestAReplyToAReplyJoinsTheThreadItIsAlreadyIn(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	root, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "what broke?", nil, nil, false)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	first, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "the migration", &root.ID, nil, false)
	if err != nil {
		t.Fatalf("first reply: %v", err)
	}
	if first.ReplyTo == nil || *first.ReplyTo != root.ID {
		t.Fatalf("a reply to a root should hang off the root, got %v", first.ReplyTo)
	}

	// The one that would otherwise deepen the tree.
	second, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "which migration?", &first.ID, nil, false)
	if err != nil {
		t.Fatalf("reply to reply: %v", err)
	}
	if second.ReplyTo == nil || *second.ReplyTo != root.ID {
		t.Errorf("a reply to a reply should join the same thread: got %v, want the root %v",
			second.ReplyTo, root.ID)
	}

	// And it has to be true in the row, not only in the value handed back —
	// the next reader is the database.
	var stored uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT reply_to FROM messages WHERE id = $1`, second.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored reply_to: %v", err)
	}
	if stored != root.ID {
		t.Errorf("stored reply_to: got %v want %v", stored, root.ID)
	}

	// Depth creeping back one message at a time is exactly the failure mode,
	// so keep going: a reply to the reply to the reply is still the same thread.
	third, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "the one from Tuesday", &second.ID, nil, false)
	if err != nil {
		t.Fatalf("third: %v", err)
	}
	if third.ReplyTo == nil || *third.ReplyTo != root.ID {
		t.Errorf("depth crept back in: got %v want %v", third.ReplyTo, root.ID)
	}
}

// A parent in another channel is not a parent. Left unchecked it is both a
// broken thread — the reply belongs to a conversation its readers cannot see —
// and a way to attach a message to a channel you are not in.
func TestReplyingToAMessageInAnotherChannelIsRefused(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	here := testsupport.SeedChannel(t, pool, org, "here", org.OwnerID)
	elsewhere := testsupport.SeedChannel(t, pool, org, "elsewhere", org.OwnerID)

	theirs, err := svc.SendMessage(ctx, org.ID, elsewhere, org.OwnerID, "over here", nil, nil, false)
	if err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	if _, err := svc.SendMessage(ctx, org.ID, here, org.OwnerID, "reaching across", &theirs.ID, nil, false); err == nil {
		t.Fatal("a reply must not point at a message in a different channel")
	}

	// And nothing was written: a refused reply is not a message.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM messages WHERE channel_id = $1`, here).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("the refused reply was stored anyway: %d messages in the channel", count)
	}
}

// ── The channel timeline ──────────────────────────────────────────────────

// A reply belongs in its thread, not in the middle of the channel. What the
// timeline needs instead is the count, so the parent can offer a way in.
func TestRepliesLeaveTheTimelineAndCountOnTheirParent(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	root, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "standup?", nil, nil, false)
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "in five", &root.ID, nil, false); err != nil {
		t.Fatalf("reply: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	last, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "make it ten", &root.ID, nil, false)
	if err != nil {
		t.Fatalf("second reply: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "unrelated", nil, nil, false); err != nil {
		t.Fatalf("unrelated: %v", err)
	}

	timeline, err := svc.GetMessages(ctx, ch, 50, nil, nil)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(timeline) != 2 {
		t.Fatalf("the timeline should hold the two top-level messages, got %d: %+v", len(timeline), timeline)
	}
	if timeline[0].ID != root.ID {
		t.Fatalf("first entry should be the root, got %+v", timeline[0])
	}
	if timeline[0].ReplyCount != 2 {
		t.Errorf("root reply count: got %d want 2", timeline[0].ReplyCount)
	}
	if timeline[0].LastReplyAt == nil || !timeline[0].LastReplyAt.Equal(last.CreatedAt) {
		t.Errorf("last reply time: got %v want %v", timeline[0].LastReplyAt, last.CreatedAt)
	}
	if timeline[1].ReplyCount != 0 {
		t.Errorf("a message nobody replied to should offer no thread, got %d", timeline[1].ReplyCount)
	}
	if timeline[1].LastReplyAt != nil {
		t.Errorf("a message nobody replied to has no last reply, got %v", timeline[1].LastReplyAt)
	}
}

// A deleted reply keeps its place in the thread exactly as a deleted message
// keeps its place in the channel, so it keeps its place in the count too —
// otherwise "2 replies" opens a thread showing three things.
func TestADeletedReplyStillCounts(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	root, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "who is on call?", nil, nil, false)
	reply, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "me", &root.ID, nil, false)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if err := svc.DeleteMessage(ctx, reply.ID, org.OwnerID); err != nil {
		t.Fatalf("delete reply: %v", err)
	}

	timeline, err := svc.GetMessages(ctx, ch, 50, nil, nil)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(timeline) != 1 {
		t.Fatalf("expected the root alone, got %d", len(timeline))
	}
	if timeline[0].ReplyCount != 1 {
		t.Errorf("a deleted reply keeps its place, so it keeps its count: got %d want 1",
			timeline[0].ReplyCount)
	}

	thread, err := svc.GetThread(ctx, org.ID, root.ID)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(thread.Replies) != 1 {
		t.Fatalf("the tombstone should still be in the thread, got %d", len(thread.Replies))
	}
	if thread.Replies[0].DeletedAt == nil || thread.Replies[0].Content != "" {
		t.Errorf("a deleted reply keeps its place and loses its contents: %+v", thread.Replies[0])
	}
}

// queryCounter counts the round trips a pool makes, which is the only way to
// tell a batched read from an N+1 apart from watching the server log.
type queryCounter struct {
	mu sync.Mutex
	n  int
}

func (q *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.n++
	return ctx
}

func (q *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (q *queryCounter) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.n
}

// countingService is the same service against the same database, through a
// pool that reports how many queries it was asked to run.
func countingService(t *testing.T, pool *pgxpool.Pool) (*Service, *queryCounter) {
	t.Helper()
	counter := &queryCounter{}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("traced pool: %v", err)
	}
	t.Cleanup(traced.Close)
	return NewService(traced, nil), counter
}

// Counting replies per message is the obvious implementation and it is fifty
// round trips for a fifty-message page, on the query that runs on every channel
// open, every socket reconnect and every tab focus.
//
// Asserting "one query" outright would pin the number of unrelated round trips
// history happens to make today, so this asks the question that actually
// matters: does reading a bigger page cost more queries than reading a small
// one?
func TestReplyCountsCostTheSameWhateverThePageSize(t *testing.T) {
	_, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	small := testsupport.SeedChannel(t, pool, org, "small", org.OwnerID)
	large := testsupport.SeedChannel(t, pool, org, "large", org.OwnerID)

	seed := func(channel uuid.UUID, roots int) {
		t.Helper()
		for i := 0; i < roots; i++ {
			var rootID uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO messages (id, org_id, channel_id, user_id, content, created_at, kind)
				VALUES (gen_random_uuid(), $1, $2, $3, $4, NOW() - make_interval(secs => $5), 'user')
				RETURNING id
			`, org.ID, channel, org.OwnerID, fmt.Sprintf("root %d", i), 1000-i).Scan(&rootID); err != nil {
				t.Fatalf("seed root: %v", err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO messages (id, org_id, channel_id, user_id, content, reply_to, created_at, kind)
				SELECT gen_random_uuid(), $1, $2, $3, 'reply ' || r, $4,
				       NOW() - make_interval(secs => $5), 'user'
				FROM generate_series(1, 3) r
			`, org.ID, channel, org.OwnerID, rootID, 500-i); err != nil {
				t.Fatalf("seed replies: %v", err)
			}
		}
	}
	seed(small, 1)
	seed(large, 30)

	traced, counter := countingService(t, pool)

	before := counter.count()
	page, err := traced.GetMessages(ctx, small, 50, nil, nil)
	if err != nil {
		t.Fatalf("small page: %v", err)
	}
	forOne := counter.count() - before
	if len(page) != 1 || page[0].ReplyCount != 3 {
		t.Fatalf("small page did not come back as expected: %+v", page)
	}

	before = counter.count()
	page, err = traced.GetMessages(ctx, large, 50, nil, nil)
	if err != nil {
		t.Fatalf("large page: %v", err)
	}
	forThirty := counter.count() - before
	if len(page) != 30 {
		t.Fatalf("large page: got %d roots want 30", len(page))
	}
	for _, m := range page {
		if m.ReplyCount != 3 {
			t.Fatalf("every root has three replies, %v reported %d", m.ID, m.ReplyCount)
		}
	}

	if forThirty != forOne {
		t.Errorf("reading 30 messages took %d queries and reading 1 took %d —"+
			" the reply counts are being fetched one message at a time", forThirty, forOne)
	}
}

// The counting query is `reply_to = ANY(...)`, which runs on every channel
// open. Asked directly rather than assumed: with the scan discouraged, is
// there an index path for it at all?
func TestCountingRepliesCanUseAnIndex(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	root, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "root", nil, nil, false)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO messages (id, org_id, channel_id, user_id, content, reply_to, created_at, kind)
		SELECT gen_random_uuid(), $1, $2, $3, 'chatter ' || i, NULL,
		       NOW() - (i || ' seconds')::interval, 'user'
		FROM generate_series(1, 2000) i
	`, org.ID, ch, org.OwnerID); err != nil {
		t.Fatalf("seed corpus: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE messages`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	// On a table small enough to seed in a test a scan is genuinely the
	// cheapest plan, so left alone the planner would choose one whatever the
	// indexes looked like. Discouraged, the only remaining reason to see a scan
	// is that no index path exists.
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}

	var plan strings.Builder
	rows, err := tx.Query(ctx, `EXPLAIN `+replyCountSQL, []uuid.UUID{root.ID})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("plan: %v", err)
	}

	if strings.Contains(plan.String(), "Seq Scan on messages") {
		t.Errorf("counting replies has no index to use and scans the whole table:\n%s", plan.String())
	}
}

// ── Reading a thread ──────────────────────────────────────────────────────

func TestAThreadReadsAsARootAndItsRepliesInOrder(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	root, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "deploy window?", nil, nil, false)
	time.Sleep(2 * time.Millisecond)
	first, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "after standup", &root.ID, nil, false)
	time.Sleep(2 * time.Millisecond)
	second, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "works for me", &root.ID, nil, false)
	// Something else in the channel that is nothing to do with this thread.
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "unrelated", nil, nil, false); err != nil {
		t.Fatalf("unrelated: %v", err)
	}

	thread, err := svc.GetThread(ctx, org.ID, root.ID)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if thread.Root.ID != root.ID || thread.ChannelID != ch {
		t.Fatalf("wrong thread came back: %+v", thread)
	}
	if thread.Root.ReplyCount != 2 {
		t.Errorf("root reply count: got %d want 2", thread.Root.ReplyCount)
	}
	if len(thread.Replies) != 2 {
		t.Fatalf("expected two replies, got %d: %+v", len(thread.Replies), thread.Replies)
	}
	if thread.Replies[0].ID != first.ID || thread.Replies[1].ID != second.ID {
		t.Errorf("replies should read oldest-first: %v then %v",
			thread.Replies[0].ID, thread.Replies[1].ID)
	}
	if thread.Replies[0].UserName == "" {
		t.Error("a reply needs its author's name to render")
	}
}

// Opening a thread from a reply is the normal way in — you click "3 replies"
// on the parent, but you also arrive from search, from a notification, or from
// the reply itself. All of them mean the same conversation.
func TestAThreadCanBeOpenedFromAnyMessageInIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	root, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "root", nil, nil, false)
	reply, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "reply", &root.ID, nil, false)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	thread, err := svc.GetThread(ctx, org.ID, reply.ID)
	if err != nil {
		t.Fatalf("thread from a reply: %v", err)
	}
	if thread.Root.ID != root.ID {
		t.Errorf("opening a thread from a reply should find the root: got %v want %v",
			thread.Root.ID, root.ID)
	}
	if len(thread.Replies) != 1 || thread.Replies[0].ID != reply.ID {
		t.Errorf("the reply should be in its own thread: %+v", thread.Replies)
	}
}

// A message is soft-deleted and keeps its place precisely so replies still
// have a parent. The thread it was holding therefore has to survive it.
func TestAThreadWhoseRootWasDeletedStillReads(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	root, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "regrettable question", nil, nil, false)
	reply, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "useful answer", &root.ID, nil, false)
	if err := svc.DeleteMessage(ctx, root.ID, org.OwnerID); err != nil {
		t.Fatalf("delete root: %v", err)
	}

	thread, err := svc.GetThread(ctx, org.ID, root.ID)
	if err != nil {
		t.Fatalf("a thread whose root was deleted must still read: %v", err)
	}
	if thread.Root.DeletedAt == nil {
		t.Error("the root should come back marked as deleted so it renders as a tombstone")
	}
	if thread.Root.Content != "" {
		t.Errorf("a deleted root keeps its place and loses its contents, got %q", thread.Root.Content)
	}
	if len(thread.Replies) != 1 || thread.Replies[0].ID != reply.ID {
		t.Fatalf("the replies outlive the root: %+v", thread.Replies)
	}
	if thread.Replies[0].Content != "useful answer" {
		t.Errorf("a reply is not deleted with its parent: %q", thread.Replies[0].Content)
	}

	// And it is still repliable: the parent is a tombstone, not a hole.
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "still true", &root.ID, nil, false); err != nil {
		t.Errorf("replying under a deleted root: %v", err)
	}
}

// Reactions are keyed on a message id and gated on its channel, and a reply
// lives in the same channel as its root — so they work on a reply without a
// second rule. Asserted rather than assumed, because "does this feature apply
// to that one" is exactly the question nobody checks.
func TestReactionsWorkOnAReply(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "mate-thread@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, mate)

	root, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "shall we?", nil, nil, false)
	reply, _ := svc.SendMessage(ctx, org.ID, ch, mate, "yes", &root.ID, nil, false)

	if err := svc.AddReaction(ctx, reply.ID, org.OwnerID, "👍"); err != nil {
		t.Fatalf("react to a reply: %v", err)
	}

	thread, err := svc.GetThread(ctx, org.ID, root.ID)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if got := len(thread.Replies[0].Reactions["👍"]); got != 1 {
		t.Errorf("the thread view should carry a reply's reactions, got %d reactors", got)
	}
}

// ── Unread ────────────────────────────────────────────────────────────────

// Replies deliberately do not raise the channel's unread badge.
//
// The read marker is a point in time advanced from the channel timeline — the
// client marks up to the newest message it can see. A reply is not on the
// timeline, so if it counted, a thread reply would raise a badge that reading
// the channel could never clear, and the count would sit there for ever
// pointing at nothing. The thread's own reply count is what says a
// conversation moved on.
func TestARepliedThreadDoesNotLeaveAnUnclearableBadge(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	reader := testsupport.SeedUser(t, pool, org, "reader-thread@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, reader)

	root, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "top level", nil, nil, false)

	channels, err := svc.ListChannels(ctx, org.ID, reader)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(channels) != 1 || channels[0].UnreadCount != 1 {
		t.Fatalf("a top-level message should count as unread: %+v", channels)
	}

	// The reader catches up on everything the timeline shows them.
	if err := svc.MarkRead(ctx, ch, reader, root.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	channels, _ = svc.ListChannels(ctx, org.ID, reader)
	if channels[0].UnreadCount != 0 {
		t.Fatalf("reading the timeline should clear the badge, got %d", channels[0].UnreadCount)
	}

	// Now somebody replies in the thread. It is not on the timeline, so there
	// is nothing the reader could mark as read to make a badge go away.
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "in the thread", &root.ID, nil, false); err != nil {
		t.Fatalf("reply: %v", err)
	}

	channels, err = svc.ListChannels(ctx, org.ID, reader)
	if err != nil {
		t.Fatalf("list again: %v", err)
	}
	if channels[0].UnreadCount != 0 {
		t.Errorf("a thread reply must not raise a badge the channel cannot clear, got %d",
			channels[0].UnreadCount)
	}
}

// ── Authorization ─────────────────────────────────────────────────────────

// A thread endpoint that resolves a message id to its conversation is a new
// way to ask for messages, and it must be no easier to get through than
// opening the channel itself.
func TestAThreadIsNotReadableFromAnotherOrganization(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, theirs, "theirs", theirs.OwnerID)

	root, _ := svc.SendMessage(ctx, theirs.ID, ch, theirs.OwnerID, "our business", nil, nil, false)
	if _, err := svc.SendMessage(ctx, theirs.ID, ch, theirs.OwnerID, "quite", &root.ID, nil, false); err != nil {
		t.Fatalf("reply: %v", err)
	}

	// The organization it belongs to can read it. Asserted first so the
	// refusals below are refusals of something that otherwise works, rather
	// than of a call that never returns anything to anybody.
	if _, err := svc.GetThread(ctx, theirs.ID, root.ID); err != nil {
		t.Fatalf("the owning organization must be able to read its own thread: %v", err)
	}

	if _, err := svc.GetThread(ctx, mine.ID, root.ID); err == nil {
		t.Error("a thread in another organization must not be readable")
	}
	// Nor by resolving it from one of its replies, which is the same read by
	// another door.
	var replyID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM messages WHERE reply_to = $1`, root.ID).Scan(&replyID); err != nil {
		t.Fatalf("find reply: %v", err)
	}
	if _, err := svc.GetThread(ctx, mine.ID, replyID); err == nil {
		t.Error("a thread must not be reachable from a reply in another organization")
	}
	if _, err := svc.GetThread(ctx, mine.ID, uuid.New()); err == nil {
		t.Error("an unknown message must report not-found")
	}
}

// asMember builds a router with a stand-in for the auth middleware, so a
// request can say who is making it without minting a real token.
func asMember(t *testing.T, svc *Service, orgID, userID string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Set("user_roles", []string{"member"})
		c.Next()
	})
	RegisterRoutes(api, svc)
	return r
}

func getThread(t *testing.T, r *gin.Engine, messageID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/messages/"+messageID.String()+"/thread", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Reading a thread requires the same channel access as reading the channel.
// Without that, the thread route is a way to read a private channel one
// message id at a time.
func TestTheThreadEndpointRequiresTheSameAccessAsTheChannel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	private := testsupport.SeedChannel(t, pool, mine, "private", mine.OwnerID)
	outsider := testsupport.SeedUser(t, pool, mine, "outsider-thread@example.test", "member")

	root, _ := svc.SendMessage(ctx, mine.ID, private, mine.OwnerID, "internal", nil, nil, false)
	reply, err := svc.SendMessage(ctx, mine.ID, private, mine.OwnerID, "quite", &root.ID, nil, false)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}

	// A member of the channel can read it.
	w := getThread(t, asMember(t, svc, mine.ID.String(), mine.OwnerID.String()), root.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("a channel member should be able to read the thread: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Root    Message   `json:"root"`
		Replies []Message `json:"replies"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Root.ID != root.ID || len(body.Replies) != 1 {
		t.Errorf("unexpected thread body: %s", w.Body.String())
	}

	// Somebody in the same organization who is not in the channel cannot.
	w = getThread(t, asMember(t, svc, mine.ID.String(), outsider.String()), root.ID)
	if w.Code == http.StatusOK {
		t.Errorf("a non-member read a private channel's thread: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "internal") {
		t.Errorf("the refusal leaked the message: %s", w.Body.String())
	}

	// And neither can anybody in another organization — by the root's id or by
	// a reply's.
	for _, id := range []uuid.UUID{root.ID, reply.ID} {
		w = getThread(t, asMember(t, svc, theirs.ID.String(), theirs.OwnerID.String()), id)
		if w.Code == http.StatusOK {
			t.Errorf("another organization read the thread through %v: %s", id, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "internal") || strings.Contains(w.Body.String(), "quite") {
			t.Errorf("the refusal leaked the conversation: %s", w.Body.String())
		}
	}
}

// ── The backfill ──────────────────────────────────────────────────────────

// Flattening on the way in only holds for messages written after it existed.
// Rows already in the table were written when reply_to could point anywhere,
// so some of them are chains — and a chained reply is invisible under the new
// reader: it is off the timeline for having a parent, and out of its root's
// thread for not pointing at the root.
//
// This runs the migration against exactly that data rather than trusting the
// SQL by reading it.
func TestTheMigrationFlattensRepliesWrittenBeforeThreadsExisted(t *testing.T) {
	_, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "legacy", org.OwnerID)
	elsewhere := testsupport.SeedChannel(t, pool, org, "elsewhere", org.OwnerID)

	// Written the way the old code allowed: a chain three deep, and a reply
	// whose parent is in a different channel entirely.
	insert := func(content string, replyTo *uuid.UUID, channel uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO messages (id, org_id, channel_id, user_id, content, reply_to, created_at, kind)
			VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, NOW(), 'user')
			RETURNING id
		`, org.ID, channel, org.OwnerID, content, replyTo).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", content, err)
		}
		return id
	}

	root := insert("root", nil, ch)
	first := insert("first", &root, ch)
	second := insert("second", &first, ch)
	third := insert("third", &second, ch)
	faraway := insert("faraway", nil, elsewhere)
	crossed := insert("crossed", &faraway, ch)

	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "0014_flatten_threads.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	// Twice: a migration that is not idempotent is one that cannot be re-run
	// after a partial deploy.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run migration (pass %d): %v", i+1, err)
		}
	}

	parentOf := func(id uuid.UUID) *uuid.UUID {
		t.Helper()
		var parent *uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT reply_to FROM messages WHERE id = $1`, id).Scan(&parent); err != nil {
			t.Fatalf("read reply_to: %v", err)
		}
		return parent
	}

	for _, m := range []struct {
		name string
		id   uuid.UUID
	}{{"first", first}, {"second", second}, {"third", third}} {
		got := parentOf(m.id)
		if got == nil || *got != root {
			t.Errorf("%s should have been re-pointed at the root: got %v want %v", m.name, got, root)
		}
	}

	// A reply to a message in another channel was never a coherent thread. It
	// becomes a top-level message where it was actually written, which is the
	// only place its readers can see it.
	if got := parentOf(crossed); got != nil {
		t.Errorf("a cross-channel reply should have been cut loose, still points at %v", got)
	}

	// And the whole thread is readable, which is the point of the exercise.
	svcThread, err := NewService(pool, nil).GetThread(ctx, org.ID, root)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(svcThread.Replies) != 3 {
		t.Errorf("all three replies belong to the one thread, got %d", len(svcThread.Replies))
	}
	timeline, err := NewService(pool, nil).GetMessages(ctx, ch, 50, nil, nil)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(timeline) != 2 {
		t.Errorf("the timeline should hold the root and the cut-loose reply, got %d: %+v", len(timeline), timeline)
	}
}

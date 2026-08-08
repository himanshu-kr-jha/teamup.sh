package chat

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
)

func newService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	return NewService(pool, nil), pool
}

// ── Channels (ticket 05) ──────────────────────────────────────────────────

func TestCreateChannelMakesTheCreatorAnOwner(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "general"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "group", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if ch.ID == uuid.Nil {
		t.Fatal("channel was not persisted")
	}

	// A channel nobody can administer would be unusable.
	if !svc.isMember(ctx, ch.ID, org.OwnerID) {
		t.Error("the creator should be a member of their own channel")
	}
	var role string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM channel_members WHERE channel_id = $1 AND user_id = $2`,
		ch.ID, org.OwnerID).Scan(&role); err != nil {
		t.Fatalf("load membership: %v", err)
	}
	if role != "owner" {
		t.Errorf("creator role: got %q want owner", role)
	}
}

func TestListChannelsShowsOnlyWhatTheUserCanSee(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedUser(t, pool, org, "outsider@example.test", "member")

	private := "private"
	if _, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &private, "group", nil, nil); err != nil {
		t.Fatalf("create private: %v", err)
	}
	public := "announcements"
	if _, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &public, "public", nil, nil); err != nil {
		t.Fatalf("create public: %v", err)
	}

	owner, err := svc.ListChannels(ctx, org.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("list for owner: %v", err)
	}
	if len(owner) != 2 {
		t.Errorf("owner should see both channels, got %d", len(owner))
	}

	seen, err := svc.ListChannels(ctx, org.ID, outsider)
	if err != nil {
		t.Fatalf("list for outsider: %v", err)
	}
	if len(seen) != 1 || seen[0].Type != "public" {
		t.Errorf("a non-member should see only the public channel, got %+v", seen)
	}
}

func TestListChannelsIsEmptyForAFreshOrganization(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	channels, err := svc.ListChannels(context.Background(), org.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("a new organization should have no channels, got %d", len(channels))
	}
}

func TestGetChannelDoesNotCrossOrganizations(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	ch := testsupport.SeedChannel(t, pool, theirs, "theirs", theirs.OwnerID)
	if _, err := svc.GetChannel(ctx, mine.ID, ch); err == nil {
		t.Fatal("a channel from another organization must not be readable")
	}
	if _, err := svc.GetChannel(ctx, mine.ID, uuid.New()); err == nil {
		t.Fatal("an unknown channel must report not-found")
	}
}

func TestAddMemberIsIdempotentAndScoped(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "mate@example.test", "member")

	name := "general"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "group", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := svc.AddMember(ctx, org.ID, ch.ID, mate, "member"); err != nil {
			t.Fatalf("add member (pass %d): %v", i, err)
		}
	}
	loaded, _ := svc.GetChannel(ctx, org.ID, ch.ID)
	if loaded.MemberCount != 2 {
		t.Errorf("adding twice must not duplicate membership, got %d members", loaded.MemberCount)
	}

	if err := svc.AddMember(ctx, org.ID, ch.ID, other.OwnerID, "member"); err == nil {
		t.Error("a user from another organization must not join")
	}

	// The newly added member now sees the channel.
	channels, _ := svc.ListChannels(ctx, org.ID, mate)
	if len(channels) != 1 {
		t.Errorf("added member should see the channel, got %d", len(channels))
	}
}

// ── Messages (ticket 06) ──────────────────────────────────────────────────

func TestMessageSurvivesAndLoadsAsHistory(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	// The regression: the WebSocket path broadcast but stored nothing, so a
	// message existed only for clients already watching.
	sent, err := svc.SendMessage(ctx, ch, org.OwnerID, "hello world", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	history, err := svc.GetMessages(ctx, ch, 50, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("message did not persist, history has %d entries", len(history))
	}
	if history[0].ID != sent.ID || history[0].Content != "hello world" {
		t.Errorf("wrong message came back: %+v", history[0])
	}
	if history[0].UserName == "" {
		t.Error("history should carry the author's name for rendering")
	}
}

func TestSendMessageRefusesNonMembers(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedUser(t, pool, org, "outsider2@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "private", org.OwnerID)

	if _, err := svc.SendMessage(ctx, ch, outsider, "let me in", nil); err == nil {
		t.Fatal("a non-member must not be able to post")
	}
}

func TestHistoryIsOldestFirstAndPagesBackwards(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	for _, body := range []string{"first", "second", "third"} {
		if _, err := svc.SendMessage(ctx, ch, org.OwnerID, body, nil); err != nil {
			t.Fatalf("send %s: %v", body, err)
		}
		time.Sleep(2 * time.Millisecond) // distinct created_at ordering
	}

	page, err := svc.GetMessages(ctx, ch, 2, nil)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("limit ignored, got %d", len(page))
	}
	// Newest two, rendered oldest-first.
	if page[0].Content != "second" || page[1].Content != "third" {
		t.Errorf("unexpected page order: %q, %q", page[0].Content, page[1].Content)
	}

	older, err := svc.GetMessages(ctx, ch, 10, &page[0].CreatedAt)
	if err != nil {
		t.Fatalf("older page: %v", err)
	}
	if len(older) != 1 || older[0].Content != "first" {
		t.Errorf("paging backwards failed: %+v", older)
	}
}

func TestEditMessageIsLimitedToItsAuthor(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "mate2@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, mate)

	msg, err := svc.SendMessage(ctx, ch, org.OwnerID, "original", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if err := svc.EditMessage(ctx, msg.ID, mate, "hijacked"); err == nil {
		t.Error("another member must not edit someone else's message")
	}
	if err := svc.EditMessage(ctx, msg.ID, org.OwnerID, "corrected"); err != nil {
		t.Fatalf("author edit: %v", err)
	}

	history, _ := svc.GetMessages(ctx, ch, 10, nil)
	if history[0].Content != "corrected" {
		t.Errorf("edit not persisted: %q", history[0].Content)
	}
	if history[0].EditedAt == nil {
		t.Error("an edited message should be marked as edited")
	}
}

func TestDeleteMessageRemovesItFromHistory(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	msg, _ := svc.SendMessage(ctx, ch, org.OwnerID, "oops", nil)
	if err := svc.DeleteMessage(ctx, msg.ID, org.OwnerID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	history, _ := svc.GetMessages(ctx, ch, 10, nil)
	if len(history) != 0 {
		t.Errorf("deleted message still in history: %+v", history)
	}
}

// A channel owner moderates; an ordinary member does not.
func TestChannelOwnerCanDeleteAnotherMembersMessage(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "mate3@example.test", "member")

	name := "general"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "group", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.AddMember(ctx, org.ID, ch.ID, mate, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}

	msg, err := svc.SendMessage(ctx, ch.ID, mate, "spam", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := svc.DeleteMessage(ctx, msg.ID, org.OwnerID); err != nil {
		t.Fatalf("owner moderation: %v", err)
	}
}

func TestUnreadCountReflectsReadState(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	reader := testsupport.SeedUser(t, pool, org, "reader@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, reader)

	var last uuid.UUID
	for _, body := range []string{"one", "two"} {
		m, err := svc.SendMessage(ctx, ch, org.OwnerID, body, nil)
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		last = m.ID
		time.Sleep(2 * time.Millisecond)
	}

	channels, _ := svc.ListChannels(ctx, org.ID, reader)
	if len(channels) != 1 || channels[0].UnreadCount != 2 {
		t.Fatalf("expected 2 unread, got %+v", channels)
	}

	if err := svc.MarkRead(ctx, ch, reader, last); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	channels, _ = svc.ListChannels(ctx, org.ID, reader)
	if channels[0].UnreadCount != 0 {
		t.Errorf("unread should clear after reading, got %d", channels[0].UnreadCount)
	}

	// The author's own messages never count as unread for them.
	authorView, _ := svc.ListChannels(ctx, org.ID, org.OwnerID)
	if authorView[0].UnreadCount != 0 {
		t.Errorf("your own messages should not be unread, got %d", authorView[0].UnreadCount)
	}
}

// Read state must never go backwards, or a late acknowledgement resurrects
// already-read messages.
func TestMarkReadNeverMovesBackwards(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	reader := testsupport.SeedUser(t, pool, org, "reader2@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, reader)

	first, _ := svc.SendMessage(ctx, ch, org.OwnerID, "one", nil)
	time.Sleep(2 * time.Millisecond)
	second, _ := svc.SendMessage(ctx, ch, org.OwnerID, "two", nil)

	if err := svc.MarkRead(ctx, ch, reader, second.ID); err != nil {
		t.Fatalf("mark read newest: %v", err)
	}
	if err := svc.MarkRead(ctx, ch, reader, first.ID); err != nil {
		t.Fatalf("mark read older: %v", err)
	}

	channels, _ := svc.ListChannels(ctx, org.ID, reader)
	if channels[0].UnreadCount != 0 {
		t.Errorf("an older acknowledgement must not resurrect unread, got %d", channels[0].UnreadCount)
	}
}

func TestReactionsRoundTrip(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	msg, _ := svc.SendMessage(ctx, ch, org.OwnerID, "ship it", nil)
	if err := svc.AddReaction(ctx, msg.ID, org.OwnerID, "🚀"); err != nil {
		t.Fatalf("add reaction: %v", err)
	}
	// Reacting twice with the same emoji is a no-op rather than an error.
	if err := svc.AddReaction(ctx, msg.ID, org.OwnerID, "🚀"); err != nil {
		t.Fatalf("duplicate reaction: %v", err)
	}

	history, _ := svc.GetMessages(ctx, ch, 10, nil)
	if got := len(history[0].Reactions["🚀"]); got != 1 {
		t.Errorf("expected one reactor, got %d", got)
	}

	if err := svc.RemoveReaction(ctx, msg.ID, org.OwnerID, "🚀"); err != nil {
		t.Fatalf("remove reaction: %v", err)
	}
	history, _ = svc.GetMessages(ctx, ch, 10, nil)
	if len(history[0].Reactions) != 0 {
		t.Errorf("reaction not removed: %+v", history[0].Reactions)
	}
}

func TestReplyToIsPersisted(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	parent, _ := svc.SendMessage(ctx, ch, org.OwnerID, "question", nil)
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.SendMessage(ctx, ch, org.OwnerID, "answer", &parent.ID); err != nil {
		t.Fatalf("reply: %v", err)
	}

	history, _ := svc.GetMessages(ctx, ch, 10, nil)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[1].ReplyTo == nil || *history[1].ReplyTo != parent.ID {
		t.Errorf("reply link lost: %+v", history[1].ReplyTo)
	}
}

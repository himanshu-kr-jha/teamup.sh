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
	sent, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "hello world", nil, false)
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

	if _, err := svc.SendMessage(ctx, org.ID, ch, outsider, "let me in", nil, false); err == nil {
		t.Fatal("a non-member must not be able to post")
	}
}

func TestHistoryIsOldestFirstAndPagesBackwards(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	for _, body := range []string{"first", "second", "third"} {
		if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, body, nil, false); err != nil {
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

	msg, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "original", nil, false)
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

	msg, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "oops", nil, false)
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

	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, mate, "spam", nil, false)
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
		m, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, body, nil, false)
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

	first, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "one", nil, false)
	time.Sleep(2 * time.Millisecond)
	second, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "two", nil, false)

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

	msg, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "ship it", nil, false)
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

	parent, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "question", nil, false)
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "answer", &parent.ID, false); err != nil {
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

// ── Org-wide channels ─────────────────────────────────────────────────────

// The reported bug: a public channel was listed to everybody but only ever had
// its creator as a member, so anybody else was refused on every write.
func TestAnyoneInTheOrgCanPostInAPublicChannel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "other@example.test", "member")

	name := "public-talk"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, other, "hello", nil, false)
	if err != nil {
		t.Fatalf("a member of the organization could not post: %v", err)
	}
	if msg.UserID != other {
		t.Errorf("wrong author: %v", msg.UserID)
	}

	// And the membership row now exists, which is what attachment access,
	// unread markers and the member list all key off.
	if !svc.isMember(ctx, ch.ID, other) {
		t.Error("posting in a public channel should have joined it")
	}
}

func TestJoiningAPublicChannelIsIdempotent(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "public-twice"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// Seeded after the channel existed, so they were never enrolled — the case
	// EnsureMember is the safety net for.
	other := testsupport.SeedUser(t, pool, org, "twice@example.test", "member")

	joined, err := svc.EnsureMember(ctx, org.ID, ch.ID, other)
	if err != nil {
		t.Fatalf("first join: %v", err)
	}
	if !joined {
		t.Error("the first join should have created a row")
	}
	again, err := svc.EnsureMember(ctx, org.ID, ch.ID, other)
	if err != nil {
		t.Fatalf("second join: %v", err)
	}
	if again {
		t.Error("joining twice should not create a second row")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM channel_members WHERE channel_id = $1 AND user_id = $2`,
		ch.ID, other).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("membership rows: got %d want 1", count)
	}
}

func TestAPrivateChannelStillRefusesOutsiders(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedUser(t, pool, org, "outsider2@example.test", "member")

	name := "private-talk"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "group", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, outsider, "let me in", nil, false); err == nil {
		t.Fatal("an outsider posted in a private channel")
	}
	if svc.isMember(ctx, ch.ID, outsider) {
		t.Error("a refused write must not have joined them")
	}
}

func TestEveryoneReadsAnAnnouncementChannelButOnlyAdminsPost(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	member := testsupport.SeedUser(t, pool, org, "listener@example.test", "member")

	name := "company-news"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "announcement", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	// Everybody sees it without being added.
	channels, err := svc.ListChannels(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, c := range channels {
		if c.ID == ch.ID {
			found = true
		}
	}
	if !found {
		t.Error("an announcement channel should be visible to the whole organization")
	}

	// But it is a broadcast, not a conversation.
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, member, "can I talk here", nil, false); err == nil {
		t.Error("a plain member posted in an announcement channel")
	}
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "we have news", nil, false); err != nil {
		t.Errorf("the channel owner could not post an announcement: %v", err)
	}
	// An organization admin who is not a channel admin may also post.
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, member, "official", nil, true); err != nil {
		t.Errorf("an organization admin could not post an announcement: %v", err)
	}
}

func TestListMembersNamesEveryoneInTheChannel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "listed@example.test", "member")

	name := "with-members"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "group", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := svc.AddMember(ctx, org.ID, ch.ID, other, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}

	members, err := svc.ListMembers(ctx, org.ID, ch.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members: got %d want 2", len(members))
	}
	// The settings panel shows people, so it needs names rather than ids.
	if members[0].Role != "owner" {
		t.Errorf("owner should sort first, got %q", members[0].Role)
	}
	for _, m := range members {
		if m.Name == "" || m.Email == "" {
			t.Errorf("member has no name or email: %+v", m)
		}
	}
}

func TestListMembersIsScopedToTheOrganization(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	stranger := testsupport.SeedOrg(t, pool)

	name := "ours"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "group", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	members, err := svc.ListMembers(ctx, stranger.ID, ch.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("another organization read our channel roster: %d rows", len(members))
	}
}

// A freshly sent message came back with no author, so the sender's own bubble
// rendered unattributed until the page refetched.
func TestASentMessageKnowsItsAuthor(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "attributed"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "hello", nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if msg.UserName == "" {
		t.Error("the message came back with no author name")
	}
}

// An announcement has to reach somebody who has never opened the channel,
// which means membership cannot wait until their first visit.
func TestAnOrgWideChannelEnrolsEverybodyWhenItIsCreated(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	quiet := testsupport.SeedUser(t, pool, org, "quiet@example.test", "member")

	name := "all-hands"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "announcement", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	if !svc.isMember(ctx, ch.ID, quiet) {
		t.Error("somebody who never opened the channel is not in it")
	}
	if ch.MemberCount != 2 {
		t.Errorf("member count: got %d want 2", ch.MemberCount)
	}
}

func TestAPrivateChannelDoesNotEnrolTheOrganization(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedUser(t, pool, org, "notinvited@example.test", "member")

	name := "just-us"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "group", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if svc.isMember(ctx, ch.ID, outsider) {
		t.Error("a private channel enrolled the whole organization")
	}
	if ch.MemberCount != 1 {
		t.Errorf("member count: got %d want 1", ch.MemberCount)
	}
}

func TestJoiningTheOrgLaterStillPutsYouInItsOpenChannels(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	open := "news"
	announcement, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &open, "announcement", nil, nil)
	if err != nil {
		t.Fatalf("create announcement: %v", err)
	}
	shut := "private"
	private, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &shut, "group", nil, nil)
	if err != nil {
		t.Fatalf("create private: %v", err)
	}

	// Somebody added to the organization after the channels already existed.
	latecomer := testsupport.SeedUser(t, pool, org, "latecomer@example.test", "member")
	if err := svc.EnrolInOpenChannels(ctx, org.ID, latecomer); err != nil {
		t.Fatalf("enrol: %v", err)
	}

	if !svc.isMember(ctx, announcement.ID, latecomer) {
		t.Error("a new member was left out of the announcement channel")
	}
	if svc.isMember(ctx, private.ID, latecomer) {
		t.Error("a new member was put into a private channel")
	}
}

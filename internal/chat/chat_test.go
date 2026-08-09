package chat

import (
	"strings"

	"context"
	"github.com/teamup/teamup/pkg/protocol"
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
	sent, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "hello world", nil, nil, false)
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

	if _, err := svc.SendMessage(ctx, org.ID, ch, outsider, "let me in", nil, nil, false); err == nil {
		t.Fatal("a non-member must not be able to post")
	}
}

func TestHistoryIsOldestFirstAndPagesBackwards(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	for _, body := range []string{"first", "second", "third"} {
		if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, body, nil, nil, false); err != nil {
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

	msg, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "original", nil, nil, false)
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

// A deleted message keeps its place and loses its contents. It used to vanish
// outright, which left a silent hole where it had been — and a reply pointing
// at nothing.
func TestDeleteMessageTakesItsContentsButNotItsPlace(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	msg, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "oops", nil, nil, false)
	if err := svc.DeleteMessage(ctx, msg.ID, org.OwnerID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	history, _ := svc.GetMessages(ctx, ch, 10, nil)
	if len(history) != 1 {
		t.Fatalf("the message should keep its place: %+v", history)
	}
	if history[0].DeletedAt == nil {
		t.Error("it should be marked deleted")
	}
	if history[0].Content != "" {
		t.Errorf("its contents should be gone, got %q", history[0].Content)
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

	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, mate, "spam", nil, nil, false)
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
		m, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, body, nil, nil, false)
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

	first, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "one", nil, nil, false)
	time.Sleep(2 * time.Millisecond)
	second, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "two", nil, nil, false)

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

	msg, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "ship it", nil, nil, false)
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

	parent, _ := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "question", nil, nil, false)
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "answer", &parent.ID, nil, false); err != nil {
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

	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, other, "hello", nil, nil, false)
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

	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, outsider, "let me in", nil, nil, false); err == nil {
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
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, member, "can I talk here", nil, nil, false); err == nil {
		t.Error("a plain member posted in an announcement channel")
	}
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "we have news", nil, nil, false); err != nil {
		t.Errorf("the channel owner could not post an announcement: %v", err)
	}
	// An organization admin who is not a channel admin may also post.
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, member, "official", nil, nil, true); err != nil {
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

	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "hello", nil, nil, false)
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

// ── Attachments travel with the message ───────────────────────────────────

// seedUpload files an upload against a channel the way the upload endpoint
// does, leaving it unclaimed by any message.
func seedUpload(t *testing.T, pool *pgxpool.Pool, orgID, channelID, uploader uuid.UUID, filename string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO attachments (id, org_id, uploaded_by, category, ref_id, object_key,
		                         filename, content_type, size_bytes, created_at)
		VALUES (gen_random_uuid(), $1, $2, 'chat', $3, $4, $5, 'text/plain', 12, NOW())
		RETURNING id
	`, orgID, uploader, channelID.String(), "key/"+uuid.New().String(), filename).Scan(&id); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	return id
}

func TestAMessageCarriesTheFilesSentWithIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "with-files"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	upload := seedUpload(t, pool, org.ID, ch.ID, org.OwnerID, "notes.txt")

	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "here you go", nil,
		[]uuid.UUID{upload}, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Filename != "notes.txt" {
		t.Fatalf("the file did not travel with the message: %+v", msg.Attachments)
	}

	// And it is still there when the conversation is reloaded.
	history, err := svc.GetMessages(ctx, ch.ID, 50, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history: got %d messages", len(history))
	}
	if len(history[0].Attachments) != 1 {
		t.Errorf("history lost the attachment: %+v", history[0].Attachments)
	}
}

func TestAFileCanBeSentOnItsOwn(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "file-only"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	upload := seedUpload(t, pool, org.ID, ch.ID, org.OwnerID, "solo.txt")

	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "", nil, []uuid.UUID{upload}, false)
	if err != nil {
		t.Fatalf("a file on its own should be a message: %v", err)
	}
	if len(msg.Attachments) != 1 {
		t.Errorf("attachments: %+v", msg.Attachments)
	}
}

func TestAnEmptyMessageWithNoFileIsRefused(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "empty"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "   ", nil, nil, false); err == nil {
		t.Error("an empty message with no attachment was accepted")
	}
}

func TestAMessageCannotClaimSomebodyElsesUpload(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "uploader@example.test", "member")

	name := "not-yours"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	theirs := seedUpload(t, pool, org.ID, ch.ID, other, "private.txt")

	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "mine now", nil,
		[]uuid.UUID{theirs}, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(msg.Attachments) != 0 {
		t.Errorf("a message claimed somebody else's upload: %+v", msg.Attachments)
	}
}

func TestAMessageCannotClaimAnUploadFromAnotherChannel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	here := "here"
	mine, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &here, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	there := "there"
	elsewhere, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &there, "public", nil, nil)
	if err != nil {
		t.Fatalf("create other channel: %v", err)
	}
	upload := seedUpload(t, pool, org.ID, elsewhere.ID, org.OwnerID, "elsewhere.txt")

	msg, err := svc.SendMessage(ctx, org.ID, mine.ID, org.OwnerID, "look", nil,
		[]uuid.UUID{upload}, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(msg.Attachments) != 0 {
		t.Errorf("a message pulled in a file from another channel: %+v", msg.Attachments)
	}
}

// ── Direct messages ───────────────────────────────────────────────────────

func TestOpeningAConversationTwiceReturnsTheSameOne(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "dm-other@example.test", "member")

	first, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, other)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	again, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, other)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if first.ID != again.ID {
		t.Error("opening a chat twice started a second conversation")
	}
	// And from the other side too.
	theirSide, err := svc.OpenDirectMessage(ctx, org.ID, other, org.OwnerID)
	if err != nil {
		t.Fatalf("their side: %v", err)
	}
	if theirSide.ID != first.ID {
		t.Error("each side got a different conversation")
	}
}

func TestBothPeopleCanTalkInADirectMessage(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "dm-talk@example.test", "member")

	dm, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, other)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := svc.SendMessage(ctx, org.ID, dm.ID, org.OwnerID, "hello", nil, nil, false); err != nil {
		t.Fatalf("owner send: %v", err)
	}
	if _, err := svc.SendMessage(ctx, org.ID, dm.ID, other, "hi back", nil, nil, false); err != nil {
		t.Fatalf("other send: %v", err)
	}

	history, err := svc.GetMessages(ctx, dm.ID, 50, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("messages: got %d want 2", len(history))
	}
}

func TestNobodyElseCanReadADirectMessage(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "dm-private@example.test", "member")
	nosy := testsupport.SeedUser(t, pool, org, "dm-nosy@example.test", "member")

	dm, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, other)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, err := svc.SendMessage(ctx, org.ID, dm.ID, nosy, "butting in", nil, nil, false); err == nil {
		t.Error("a third party posted in somebody else's conversation")
	}
	channels, err := svc.ListChannels(ctx, org.ID, nosy)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, c := range channels {
		if c.ID == dm.ID {
			t.Error("a third party can see somebody else's conversation")
		}
	}
}

func TestADirectMessageIsNamedAfterTheOtherPerson(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "dm-named@example.test", "member")

	dm, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, other)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Each side sees the person they are talking to, not their own name.
	mine, err := svc.ListChannels(ctx, org.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("list mine: %v", err)
	}
	theirs, err := svc.ListChannels(ctx, org.ID, other)
	if err != nil {
		t.Fatalf("list theirs: %v", err)
	}
	nameIn := func(list []Channel) string {
		for _, c := range list {
			if c.ID == dm.ID && c.Name != nil {
				return *c.Name
			}
		}
		return ""
	}
	if nameIn(mine) == "" || nameIn(theirs) == "" {
		t.Fatalf("a direct message showed no name: mine=%q theirs=%q", nameIn(mine), nameIn(theirs))
	}
	if nameIn(mine) == nameIn(theirs) {
		t.Errorf("both sides saw the same name %q", nameIn(mine))
	}
}

func TestYouCannotStartAConversationWithYourself(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	if _, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, org.OwnerID); err == nil {
		t.Error("opened a conversation with yourself")
	}
}

func TestYouCannotStartAConversationAcrossOrganizations(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	if _, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, theirs.OwnerID); err == nil {
		t.Error("opened a conversation with another tenant's user")
	}
}

// Both sides of a direct message are its owners, so either could have added a
// third person — which breaks the pair and makes the next "new chat" between
// them start a second conversation.
func TestADirectMessageCannotBeTurnedIntoAGroup(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "dm-pair@example.test", "member")
	third := testsupport.SeedUser(t, pool, org, "dm-third@example.test", "member")

	dm, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, other)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if err := svc.AddMember(ctx, org.ID, dm.ID, third, "member"); err == nil {
		t.Fatal("a third person was added to a direct message")
	}

	// And the pair still resolves to the same conversation.
	again, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, other)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if again.ID != dm.ID {
		t.Error("the conversation was split in two")
	}
}

// Leaving a public channel used to be undone the moment you looked at it
// again, because opening an org-wide channel joins you to it.
func TestLeavingAPublicChannelSticks(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	member := testsupport.SeedUser(t, pool, org, "leaver@example.test", "member")

	name := "leavable"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !svc.isMember(ctx, ch.ID, member) {
		t.Fatal("they should start in an org-wide channel")
	}

	if err := svc.RemoveMember(ctx, org.ID, ch.ID, member); err != nil {
		t.Fatalf("leave: %v", err)
	}

	// The automatic join must respect the decision.
	joined, err := svc.EnsureMember(ctx, org.ID, ch.ID, member)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if joined || svc.isMember(ctx, ch.ID, member) {
		t.Error("touching the channel put them straight back in")
	}

	// And it is gone from their sidebar.
	channels, err := svc.ListChannels(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, c := range channels {
		if c.ID == ch.ID {
			t.Error("a channel they left is still listed")
		}
	}
}

func TestAChannelYouLeftCanBeFoundAndRejoined(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	member := testsupport.SeedUser(t, pool, org, "rejoiner@example.test", "member")

	name := "rejoinable"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.RemoveMember(ctx, org.ID, ch.ID, member); err != nil {
		t.Fatalf("leave: %v", err)
	}

	browsable, err := svc.BrowsableChannels(ctx, org.ID, member)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	var found bool
	for _, c := range browsable {
		if c.ID == ch.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("a channel they left cannot be found again")
	}

	if err := svc.RejoinChannel(ctx, org.ID, ch.ID, member); err != nil {
		t.Fatalf("rejoin: %v", err)
	}
	if !svc.isMember(ctx, ch.ID, member) {
		t.Error("rejoining did not put them back")
	}
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, member, "back", nil, nil, false); err != nil {
		t.Errorf("they cannot post after rejoining: %v", err)
	}
}

// Somebody still in a channel has no business seeing it in the browse list.
func TestBrowseOnlyOffersChannelsYouAreNotIn(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "already-in"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	private := "not-yours"
	priv, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &private, "group", nil, nil)
	if err != nil {
		t.Fatalf("create private: %v", err)
	}
	outsider := testsupport.SeedUser(t, pool, org, "browser@example.test", "member")

	browsable, err := svc.BrowsableChannels(ctx, org.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	for _, c := range browsable {
		if c.ID == ch.ID {
			t.Error("a channel they are already in was offered")
		}
	}

	// A private channel is never browsable, whoever asks.
	theirs, err := svc.BrowsableChannels(ctx, org.ID, outsider)
	if err != nil {
		t.Fatalf("browse outsider: %v", err)
	}
	for _, c := range theirs {
		if c.ID == priv.ID {
			t.Error("a private channel was offered for browsing")
		}
	}
}

// ── Unread counts ─────────────────────────────────────────────────────────

func unreadFor(t *testing.T, svc *Service, orgID, userID, channelID uuid.UUID) int {
	t.Helper()
	channels, err := svc.ListChannels(context.Background(), orgID, userID)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	for _, c := range channels {
		if c.ID == channelID {
			return c.UnreadCount
		}
	}
	t.Fatalf("channel %s not listed for %s", channelID, userID)
	return 0
}

// Nothing in the app ever marked a channel read, so last_read_at stayed null
// and every message ever sent counted as unread for ever.
func TestReadingAChannelClearsItsUnreadCount(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	reader := testsupport.SeedUser(t, pool, org, "reader@example.test", "member")

	name := "unread"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	first, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "one", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "two", nil, nil, false); err != nil {
		t.Fatalf("send: %v", err)
	}

	if got := unreadFor(t, svc, org.ID, reader, ch.ID); got != 2 {
		t.Errorf("unread before reading: got %d want 2", got)
	}

	// Reading up to the first message clears only that one.
	if err := svc.MarkRead(ctx, ch.ID, reader, first.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if got := unreadFor(t, svc, org.ID, reader, ch.ID); got != 1 {
		t.Errorf("unread after reading the first: got %d want 1", got)
	}

	latest, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "three", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := svc.MarkRead(ctx, ch.ID, reader, latest.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if got := unreadFor(t, svc, org.ID, reader, ch.ID); got != 0 {
		t.Errorf("unread after reading everything: got %d want 0", got)
	}
}

// Your own messages are not news to you.
func TestYourOwnMessagesAreNeverUnread(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "self"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "talking to myself", nil, nil, false); err != nil {
		t.Fatalf("send: %v", err)
	}

	if got := unreadFor(t, svc, org.ID, org.OwnerID, ch.ID); got != 0 {
		t.Errorf("own message counted as unread: got %d", got)
	}
}

// A read marker must not be advanced by a message from somewhere else.
func TestAReadMarkerIsPerChannel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	reader := testsupport.SeedUser(t, pool, org, "per-channel@example.test", "member")

	here := "here"
	mine, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &here, "public", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	there := "there"
	other, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &there, "public", nil, nil)
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	if _, err := svc.SendMessage(ctx, org.ID, mine.ID, org.OwnerID, "unread here", nil, nil, false); err != nil {
		t.Fatalf("send: %v", err)
	}
	elsewhere, err := svc.SendMessage(ctx, org.ID, other.ID, org.OwnerID, "read there", nil, nil, false)
	if err != nil {
		t.Fatalf("send elsewhere: %v", err)
	}

	// Reading the other channel must leave this one untouched.
	if err := svc.MarkRead(ctx, mine.ID, reader, elsewhere.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if got := unreadFor(t, svc, org.ID, reader, mine.ID); got != 1 {
		t.Errorf("a message from another channel cleared this one: got %d want 1", got)
	}
}

// ── Read receipts, editing and deleting ───────────────────────────────────

func TestAMessageSaysHowManyPeopleHaveReadIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	reader := testsupport.SeedUser(t, pool, org, "receipt-reader@example.test", "member")

	name := "receipts"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "did you see this", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	history, err := svc.GetMessages(ctx, ch.ID, 50, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if history[0].ReadCount != 0 {
		t.Errorf("nobody has read it yet, got %d", history[0].ReadCount)
	}

	if err := svc.MarkRead(ctx, ch.ID, reader, msg.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	history, err = svc.GetMessages(ctx, ch.ID, 50, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if history[0].ReadCount != 1 {
		t.Errorf("read count: got %d want 1", history[0].ReadCount)
	}

	who, err := svc.ReadBy(ctx, org.ID, msg.ID)
	if err != nil {
		t.Fatalf("read by: %v", err)
	}
	if len(who) != 1 || who[0].UserID != reader {
		t.Fatalf("wrong readers: %+v", who)
	}
	if who[0].Name == "" {
		t.Error("a reader should be named")
	}
}

// You have obviously read your own message; counting yourself would make every
// message look read the moment it was sent.
func TestTheAuthorIsNotCountedAsHavingReadTheirOwnMessage(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "self-read"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "hello", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := svc.MarkRead(ctx, ch.ID, org.OwnerID, msg.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	who, err := svc.ReadBy(ctx, org.ID, msg.ID)
	if err != nil {
		t.Fatalf("read by: %v", err)
	}
	if len(who) != 0 {
		t.Errorf("the author was counted: %+v", who)
	}
}

func TestADeletedMessageKeepsItsPlaceWithoutItsContents(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "deletions"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "something regrettable", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := svc.DeleteMessage(ctx, msg.ID, org.OwnerID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	history, err := svc.GetMessages(ctx, ch.ID, 50, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("a deleted message should keep its place, got %d messages", len(history))
	}
	if history[0].DeletedAt == nil {
		t.Error("it should be marked as deleted")
	}
	if history[0].Content != "" {
		t.Errorf("the contents should be gone, got %q", history[0].Content)
	}
}

func TestAMessageCanBeEditedForAWhileAndThenNot(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "edits"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "teh typo", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if err := svc.EditMessage(ctx, msg.ID, org.OwnerID, "the typo"); err != nil {
		t.Fatalf("edit straight away: %v", err)
	}
	history, err := svc.GetMessages(ctx, ch.ID, 50, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if history[0].Content != "the typo" {
		t.Errorf("the edit did not take: %q", history[0].Content)
	}
	if history[0].EditedAt == nil {
		t.Error("an edited message should say so")
	}

	// Once the window has passed, the conversation stops changing underneath
	// the people who have already read it.
	if _, err := pool.Exec(ctx,
		`UPDATE messages SET created_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, msg.ID); err != nil {
		t.Fatalf("age the message: %v", err)
	}
	if err := svc.EditMessage(ctx, msg.ID, org.OwnerID, "too late"); err == nil {
		t.Error("an old message was edited")
	}
}

func TestADeletedMessageCannotBeEdited(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "edit-deleted"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "gone", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := svc.DeleteMessage(ctx, msg.ID, org.OwnerID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.EditMessage(ctx, msg.ID, org.OwnerID, "back again"); err == nil {
		t.Error("a deleted message was edited back into existence")
	}
}

// The conversation list shows the other person's availability, which needs
// their id and not only their name.
func TestADirectMessageCarriesTheOtherPersonsId(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "counterpart@example.test", "member")

	dm, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, other)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	mine, err := svc.ListChannels(ctx, org.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *Channel
	for i := range mine {
		if mine[i].ID == dm.ID {
			found = &mine[i]
		}
	}
	if found == nil {
		t.Fatal("the conversation is missing")
	}
	if found.CounterpartID == nil || *found.CounterpartID != other {
		t.Errorf("wrong counterpart: %+v", found.CounterpartID)
	}

	// And from their side it is the other way round.
	theirs, err := svc.ListChannels(ctx, org.ID, other)
	if err != nil {
		t.Fatalf("list theirs: %v", err)
	}
	for _, c := range theirs {
		if c.ID == dm.ID {
			if c.CounterpartID == nil || *c.CounterpartID != org.OwnerID {
				t.Errorf("their side points at the wrong person: %+v", c.CounterpartID)
			}
		}
	}
}

// A channel is a room, not a person, so it has no counterpart.
func TestAChannelHasNoCounterpart(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	name := "roomy"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	channels, err := svc.ListChannels(ctx, org.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, c := range channels {
		if c.ID == ch.ID && c.CounterpartID != nil {
			t.Errorf("a channel should have no counterpart, got %v", c.CounterpartID)
		}
	}
}

// A missed call has to land somewhere the person will actually look, and the
// conversation with whoever called is that place — even if they have never
// spoken before.
func TestAMissedCallIsWrittenIntoTheConversationAsAnEvent(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "was-away@example.test", "member")

	written, err := svc.RecordCallEvent(ctx, org.ID, org.OwnerID, other, "Missed call")
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	dm, err := svc.OpenDirectMessage(ctx, org.ID, other, org.OwnerID)
	if err != nil {
		t.Fatalf("open the conversation: %v", err)
	}
	// The conversation it names is the one whoever called will be taken to, so
	// it has to be the same one either of them would open by hand.
	if written != dm.ID {
		t.Errorf("the note went to %v but their conversation is %v", written, dm.ID)
	}
	history, err := svc.GetMessages(ctx, dm.ID, 10, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("the missed call should be in the conversation, got %d messages", len(history))
	}
	if history[0].Content != "Missed call" {
		t.Errorf("unexpected note: %q", history[0].Content)
	}
	// Marked as something that happened, not something the caller said —
	// otherwise it reads as a message from them and invites a reply.
	if history[0].Kind != "event" {
		t.Errorf("a missed call is an event, got kind %q", history[0].Kind)
	}
	if history[0].UserID != org.OwnerID {
		t.Errorf("the note should name who called, got %v", history[0].UserID)
	}
}

// Everything written before events existed, and everything anybody types, is
// an ordinary message. Nothing should come back with an empty kind.
func TestAnOrdinaryMessageIsNotAnEvent(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "hello", nil, nil, false); err != nil {
		t.Fatalf("send: %v", err)
	}
	history, err := svc.GetMessages(ctx, ch, 10, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if history[0].Kind != "user" {
		t.Errorf("something somebody typed is a message, got kind %q", history[0].Kind)
	}
}

// pushedTo records who a message would have reached on their devices.
type pushedTo struct {
	recipients []uuid.UUID
	payloads   []protocol.Push
}

func (p *pushedTo) Notify(context.Context, uuid.UUID, uuid.UUID, string, string, string, map[string]any) error {
	return nil
}
func (p *pushedTo) NotifyMany(context.Context, uuid.UUID, []uuid.UUID, string, string, string, map[string]any) error {
	return nil
}
func (p *pushedTo) PushToUsers(_ context.Context, userIDs []uuid.UUID, payload protocol.Push) {
	p.recipients = append(p.recipients, userIDs...)
	p.payloads = append(p.payloads, payload)
}

// A message that only arrives if you happen to be looking is not a message, so
// everybody else in the conversation is reached on their devices.
func TestAMessageReachesEverybodyElseInTheChannel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "gets-told@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, other)

	pushes := &pushedTo{}
	svc.SetNotifier(pushes)

	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "the build is broken", nil, nil, false); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(pushes.recipients) != 1 || pushes.recipients[0] != other {
		t.Fatalf("only the other member should be told, got %v", pushes.recipients)
	}
	// The sender is looking at what they just wrote.
	for _, r := range pushes.recipients {
		if r == org.OwnerID {
			t.Error("nobody should be notified about their own message")
		}
	}

	got := pushes.payloads[0]
	if got.Title != "#general" {
		t.Errorf("a channel message should name the channel, got %q", got.Title)
	}
	if !strings.Contains(got.Body, "the build is broken") {
		t.Errorf("the message should be quoted, got %q", got.Body)
	}
	// One notification per conversation, replaced as it goes on.
	if got.Tag != "chat-"+ch.String() {
		t.Errorf("unexpected tag %q", got.Tag)
	}
	if got.Data["url"] != "/chat/"+ch.String() {
		t.Errorf("tapping it should open the conversation, got %v", got.Data["url"])
	}
}

// In a direct message the person is the conversation, so naming the channel
// would say "#" and nothing else.
func TestADirectMessageIsAnnouncedByWhoSentIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "dm-push@example.test", "member")

	pushes := &pushedTo{}
	svc.SetNotifier(pushes)

	dm, err := svc.OpenDirectMessage(ctx, org.ID, org.OwnerID, other)
	if err != nil {
		t.Fatalf("open dm: %v", err)
	}
	if _, err := svc.SendMessage(ctx, org.ID, dm.ID, org.OwnerID, "are you around?", nil, nil, false); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := pushes.payloads[0]
	if strings.HasPrefix(got.Title, "#") {
		t.Errorf("a direct message is from a person, not a channel: %q", got.Title)
	}
	if got.Body != "are you around?" {
		t.Errorf("it should not repeat the name in the body, got %q", got.Body)
	}
}

// A wall of text does not belong on a lock screen, and a file on its own still
// has to say something.
func TestWhatADeviceIsShownIsShortAndNeverEmpty(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "brevity@example.test", "member")
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, other)

	pushes := &pushedTo{}
	svc.SetNotifier(pushes)

	long := strings.Repeat("a very long thought ", 60)
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, long, nil, nil, false); err != nil {
		t.Fatalf("send: %v", err)
	}
	summary := pushes.payloads[0].Body
	if len([]rune(summary)) >= len([]rune(long)) {
		t.Errorf("a wall of text should be cut down, got %d characters", len([]rune(summary)))
	}
	// Saying it was cut, rather than stopping mid-word as though that was all.
	if !strings.HasSuffix(summary, "…") {
		t.Errorf("a shortened message should show that it was: %q", summary)
	}

	// A file on its own is a message, and an empty notification says nothing.
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "", nil,
		[]uuid.UUID{seedUpload(t, pool, org.ID, ch, org.OwnerID, "notes.txt")}, false); err != nil {
		t.Fatalf("send a file: %v", err)
	}
	last := pushes.payloads[len(pushes.payloads)-1].Body
	if !strings.Contains(last, "Sent a file") {
		t.Errorf("a message that is only a file should say so, got %q", last)
	}
}

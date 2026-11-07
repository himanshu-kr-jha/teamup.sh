package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

// readReceipt is the outbound frame, decoded the way a browser would.
type readReceipt struct {
	ChannelID string     `json:"channelId"`
	UserID    string     `json:"userId"`
	From      *time.Time `json:"from"`
	To        time.Time  `json:"to"`
}

// The tick under your own message says whether anybody has read it. It is
// drawn from readCount, which is counted when the history is fetched — so
// until now it only changed if you reloaded. Somebody reading your message
// while you are looking at it is exactly when you want to be told.
func TestReadingSomebodyElsesMessageTellsTheSender(t *testing.T) {
	h := newWSHarness(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, h.pool)
	reader := testsupport.SeedUser(t, h.pool, org, "reader@example.com", "member")
	ch := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID, reader)

	sender := h.dial(t, "sender", org.ID, org.OwnerID)

	msg, err := h.svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "did this land?", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if err := h.svc.MarkRead(ctx, ch, reader, msg.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	frame := sender.await(t, protocol.TypeChatReadReceipt, 5*time.Second)
	var got readReceipt
	if err := json.Unmarshal(frame.Payload, &got); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if got.ChannelID != ch.String() {
		t.Errorf("receipt is for channel %s, want %s", got.ChannelID, ch)
	}
	if got.UserID != reader.String() {
		t.Errorf("receipt names %s as the reader, want %s", got.UserID, reader)
	}
	// The window has to cover the message, or the sender cannot tell which of
	// their messages just became read.
	if got.To.Before(msg.CreatedAt) {
		t.Errorf("the new marker %s is before the message it read (%s)", got.To, msg.CreatedAt)
	}
	if got.From != nil && !got.From.Before(msg.CreatedAt) {
		t.Errorf("the previous marker %s already covered the message (%s), so nothing changed", got.From, msg.CreatedAt)
	}
}

// The reader must not be told about their own reading: it is their own act,
// and their client has already accounted for it.
func TestTheReaderIsNotSentTheirOwnReceipt(t *testing.T) {
	h := newWSHarness(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, h.pool)
	reader := testsupport.SeedUser(t, h.pool, org, "reader@example.com", "member")
	ch := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID, reader)

	readerConn := h.dial(t, "reader", org.ID, reader)

	msg, err := h.svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "did this land?", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	// Step over the message itself arriving.
	readerConn.await(t, protocol.TypeChatMessage, 5*time.Second)

	if err := h.svc.MarkRead(ctx, ch, reader, msg.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}

	readerConn.expectNothing(t, protocol.TypeChatReadReceipt, 1500*time.Millisecond)
}

// Marking read again, with the marker already past the message, changes
// nothing — so it must not be announced. Without this every poll of an idle
// channel would be a broadcast to everyone in it, and a client counting
// receipts would count the same reader twice.
func TestReadingWhatYouHaveAlreadyReadIsNotAnnounced(t *testing.T) {
	h := newWSHarness(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, h.pool)
	reader := testsupport.SeedUser(t, h.pool, org, "reader@example.com", "member")
	ch := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID, reader)

	sender := h.dial(t, "sender", org.ID, org.OwnerID)

	msg, err := h.svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "did this land?", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := h.svc.MarkRead(ctx, ch, reader, msg.ID); err != nil {
		t.Fatalf("first mark read: %v", err)
	}
	sender.await(t, protocol.TypeChatReadReceipt, 5*time.Second)

	if err := h.svc.MarkRead(ctx, ch, reader, msg.ID); err != nil {
		t.Fatalf("second mark read: %v", err)
	}
	sender.expectNothing(t, protocol.TypeChatReadReceipt, 1500*time.Millisecond)
}

// A read marker from somebody who is not in the channel must not move it, and
// must not be announced either. MarkRead is reachable over HTTP with any
// channel id.
func TestANonMemberCannotAnnounceAReadReceipt(t *testing.T) {
	h := newWSHarness(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, h.pool)
	outsider := testsupport.SeedUser(t, h.pool, org, "outsider@example.com", "member")
	ch := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID)

	sender := h.dial(t, "sender", org.ID, org.OwnerID)

	msg, err := h.svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "members only", nil, nil, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	// Not an error — the update simply matches no row — but nothing may be
	// announced from it.
	_ = h.svc.MarkRead(ctx, ch, outsider, msg.ID)

	sender.expectNothing(t, protocol.TypeChatReadReceipt, 1500*time.Millisecond)
}

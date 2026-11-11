package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

// A reply is not in the conversation, so reading the conversation is not
// reading it. Opening the thread is, and the writer is told live.
func TestAReplyIsReadByOpeningItsThreadNotByReadingTheConversation(t *testing.T) {
	h := newWSHarness(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, h.pool)
	reader := testsupport.SeedUser(t, h.pool, org, "reader@example.com", "member")
	ch := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID, reader)

	root, err := h.svc.SendMessage(ctx, org.ID, ch, reader, "a question", nil, nil, false)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	reply, err := h.svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "the answer", &root.ID, nil, false)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	later, err := h.svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "something else", nil, nil, false)
	if err != nil {
		t.Fatalf("later: %v", err)
	}

	readCount := func() int {
		t.Helper()
		thread, err := h.svc.GetThread(ctx, org.ID, root.ID)
		if err != nil {
			t.Fatalf("thread: %v", err)
		}
		return thread.Replies[0].ReadCount
	}

	// Reading the conversation past it does not count.
	if err := h.svc.MarkRead(ctx, ch, reader, later.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if n := readCount(); n != 0 {
		t.Fatalf("reading the conversation counted as reading the reply: %d", n)
	}
	if reads, _ := h.svc.ReadBy(ctx, org.ID, reply.ID); len(reads) != 0 {
		t.Errorf("read by %v before the thread was opened", reads)
	}

	writer := h.dial(t, "writer", org.ID, org.OwnerID)
	if err := h.svc.MarkThreadRead(ctx, org.ID, reader, reply.ID); err != nil {
		t.Fatalf("mark thread read: %v", err)
	}
	frame := writer.await(t, protocol.TypeChatReadReceipt, 5*time.Second)
	var got struct {
		ThreadID string    `json:"threadId"`
		To       time.Time `json:"to"`
	}
	if err := json.Unmarshal(frame.Payload, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ThreadID != root.ID.String() || got.To.Before(reply.CreatedAt) {
		t.Errorf("receipt %+v does not cover the reply in thread %s", got, root.ID)
	}
	if n := readCount(); n != 1 {
		t.Errorf("after opening the thread the reply is read by %d, want 1", n)
	}
	if reads, _ := h.svc.ReadBy(ctx, org.ID, reply.ID); len(reads) != 1 || reads[0].UserID != reader {
		t.Errorf("read by %v, want the reader", reads)
	}

	// Never backwards: reading the root again changes nothing.
	if err := h.svc.MarkThreadRead(ctx, org.ID, reader, root.ID); err != nil {
		t.Fatalf("mark root: %v", err)
	}
	if n := readCount(); n != 1 {
		t.Errorf("the marker went back: %d", n)
	}
}

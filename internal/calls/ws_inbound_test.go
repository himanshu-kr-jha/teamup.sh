package calls

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

// Every call frame below is decoded from bytes rather than hand-built, because
// the defect was in decoding: msg.Payload was an empty interface that
// encoding/json fills with a map, so the handlers' assertion to []byte failed
// and every call a client placed over the socket was discarded.

// wsConn is a connection that says who is on the other end and keeps whatever
// is sent back to them.
type wsConn struct {
	userID string
	orgID  string

	mu   sync.Mutex
	sent []*protocol.Message
}

func (c *wsConn) UserID() string { return c.userID }
func (c *wsConn) OrgID() string  { return c.orgID }

func (c *wsConn) Enqueue(msg *protocol.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
}

func (c *wsConn) firstOfType(t *testing.T, msgType string) *protocol.Message {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, msg := range c.sent {
		if msg.Type == msgType {
			return msg
		}
	}
	t.Fatalf("the caller was never sent %s; they got %v", msgType, typesOf(c.sent))
	return nil
}

func typesOf(msgs []*protocol.Message) []string {
	types := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		types = append(types, msg.Type)
	}
	return types
}

// recordingBroadcaster keeps what the hub would have delivered to whom.
type recordingBroadcaster struct {
	mu     sync.Mutex
	toUser map[string][]*protocol.Message
}

func newRecordingBroadcaster() *recordingBroadcaster {
	return &recordingBroadcaster{toUser: map[string][]*protocol.Message{}}
}

func (b *recordingBroadcaster) BroadcastToUser(userID string, msg *protocol.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.toUser[userID] = append(b.toUser[userID], msg)
}

func (b *recordingBroadcaster) BroadcastToChannel(string, *protocol.Message, string) {}

func (b *recordingBroadcaster) firstToUser(t *testing.T, userID, msgType string) *protocol.Message {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, msg := range b.toUser[userID] {
		if msg.Type == msgType {
			return msg
		}
	}
	t.Fatalf("%s was never sent %s; they got %v", userID, msgType, typesOf(b.toUser[userID]))
	return nil
}

func decodeFrame(t *testing.T, frame string) *protocol.Message {
	t.Helper()
	var msg protocol.Message
	if err := json.Unmarshal([]byte(frame), &msg); err != nil {
		t.Fatalf("decode frame %s: %v", frame, err)
	}
	return &msg
}

// decodePayload reads a payload the way a client would: off the wire, out of
// the marshalled envelope, rather than out of the server's own struct.
func decodePayload(t *testing.T, msg *protocol.Message, into any) {
	t.Helper()

	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal %s: %v", msg.Type, err)
	}
	var frame struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(encoded, &frame); err != nil {
		t.Fatalf("decode %s envelope %s: %v", msg.Type, encoded, err)
	}
	if err := json.Unmarshal(frame.Payload, into); err != nil {
		t.Fatalf("decode %s payload %s: %v", msg.Type, frame.Payload, err)
	}
}

func TestACallInviteFrameCreatesTheCallAndRingsTheInvitee(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)
	inviteeID := testsupport.SeedUser(t, pool, org, "invitee@example.test", "member")

	broadcaster := newRecordingBroadcaster()
	svc.SetBroadcaster(broadcaster)

	caller := &wsConn{userID: org.OwnerID.String(), orgID: org.ID.String()}
	svc.HandleCallInvite(context.Background(), caller, decodeFrame(t, `{
		"type": "call.invite",
		"payload": {"callType": "audio", "title": "Design sync", "invitees": ["`+inviteeID.String()+`"]}
	}`))

	calls, err := svc.ListCalls(context.Background(), org.ID, org.OwnerID, "", nil)
	if err != nil {
		t.Fatalf("list calls: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("the call.invite frame was dropped: %d calls exist", len(calls))
	}
	if calls[0].CallType != "audio" {
		t.Errorf("call type: got %q want audio (from the frame)", calls[0].CallType)
	}
	if calls[0].Title == nil || *calls[0].Title != "Design sync" {
		t.Errorf("title: got %v want Design sync (from the frame)", calls[0].Title)
	}

	var invited protocol.CallInvitedPayload
	decodePayload(t, caller.firstOfType(t, protocol.TypeCallInvited), &invited)
	if invited.RoomName != calls[0].RoomName {
		t.Errorf("the caller was pointed at room %q, want %q", invited.RoomName, calls[0].RoomName)
	}

	var rung protocol.CallInvitedPayload
	decodePayload(t, broadcaster.firstToUser(t, inviteeID.String(), protocol.TypeCallInvited), &rung)
	if rung.RoomName != calls[0].RoomName {
		t.Errorf("the invitee was rung for room %q, want %q", rung.RoomName, calls[0].RoomName)
	}
	if rung.CallerID != org.OwnerID.String() {
		t.Errorf("the invitee was told the caller is %q, want %q", rung.CallerID, org.OwnerID)
	}
}

func TestACallAcceptFrameUsesTheRoomItNames(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	broadcaster := newRecordingBroadcaster()
	svc.SetBroadcaster(broadcaster)

	callee := &wsConn{userID: org.OwnerID.String(), orgID: org.ID.String()}
	svc.HandleCallAccept(context.Background(), callee,
		decodeFrame(t, `{"type":"call.accept","payload":{"roomName":"call-abc123"}}`))

	var accepted protocol.CallInvitedPayload
	decodePayload(t, broadcaster.firstToUser(t, org.OwnerID.String(), protocol.TypeCallInvited), &accepted)
	if accepted.RoomName != "call-abc123" {
		t.Errorf("room name from the frame: got %q want call-abc123", accepted.RoomName)
	}
}

func TestACallDeclineFrameEndsTheRoomItNames(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	broadcaster := newRecordingBroadcaster()
	svc.SetBroadcaster(broadcaster)

	callee := &wsConn{userID: org.OwnerID.String(), orgID: org.ID.String()}
	svc.HandleCallDecline(context.Background(), callee,
		decodeFrame(t, `{"type":"call.decline","payload":{"roomName":"call-def456","reason":"busy"}}`))

	var ended protocol.CallEndedPayload
	decodePayload(t, broadcaster.firstToUser(t, org.OwnerID.String(), protocol.TypeCallEnded), &ended)
	if ended.RoomName != "call-def456" {
		t.Errorf("room name from the frame: got %q want call-def456", ended.RoomName)
	}
}

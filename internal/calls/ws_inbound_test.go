package calls

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

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

// nothingToUser is how "no token was minted for them" is observed: a token
// only ever reaches somebody inside a frame addressed to them, so a user the
// hub was never asked to deliver to was never handed credentials.
func (b *recordingBroadcaster) nothingToUser(t *testing.T, userID, why string) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if got := b.toUser[userID]; len(got) > 0 {
		t.Fatalf("%s (%s) was sent %v; nothing should have reached them", userID, why, typesOf(got))
	}
}

func (c *wsConn) noneOfType(t *testing.T, msgType, why string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, msg := range c.sent {
		if msg.Type == msgType {
			t.Fatalf("the caller was sent %s (%s); they got %v", msgType, why, typesOf(c.sent))
		}
	}
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

// newTokenService is a service that can actually mint LiveKit tokens, so a
// test can tell the difference between "no token was issued" and "tokens do
// not work here". The URL is left empty, so there is still no room service and
// CreateCall takes the same stub-room path as everywhere else.
func newTokenService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	return NewService(pool, Config{
		LiveKitAPIKey:    "APItestkey",
		LiveKitAPISecret: "secret-long-enough-to-sign-with-0123456789",
	}), pool
}

// A call invite names whoever it likes: the frame is client-supplied, and the
// handler used to ring those ids and mint them room credentials without ever
// asking whether they belong to the caller's organization. The shipped client
// never sends this frame, but anybody who can open a socket can.
func TestACallInviteIsScopedToTheCallersOrganization(t *testing.T) {
	tests := []struct {
		name string
		// invitees is built per-run from the seeded ids.
		invitees func(mine, theirs string) []string
		// refused says the whole invite is turned down: no call, and nobody
		// rung.
		refused bool
	}{
		{
			name:     "somebody in your own organization",
			invitees: func(mine, _ string) []string { return []string{mine} },
		},
		{
			name:     "somebody in another organization",
			invitees: func(_, theirs string) []string { return []string{theirs} },
			refused:  true,
		},
		{
			name:     "a mix of both",
			invitees: func(mine, theirs string) []string { return []string{mine, theirs} },
			refused:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, pool := newTokenService(t)
			ctx := context.Background()
			org := testsupport.SeedOrg(t, pool)
			outsider := testsupport.SeedOrg(t, pool)
			mate := testsupport.SeedUser(t, pool, org, "mate@example.test", "member")

			broadcaster := newRecordingBroadcaster()
			svc.SetBroadcaster(broadcaster)

			invitees, err := json.Marshal(tc.invitees(mate.String(), outsider.OwnerID.String()))
			if err != nil {
				t.Fatalf("encode invitees: %v", err)
			}
			caller := &wsConn{userID: org.OwnerID.String(), orgID: org.ID.String()}
			svc.HandleCallInvite(ctx, caller, decodeFrame(t, `{
				"type": "call.invite",
				"payload": {"callType": "audio", "title": "Sync", "invitees": `+string(invitees)+`}
			}`))

			// Nothing at all reaches the other organization: no ring, and so no
			// token, which is what makes this a tenant boundary rather than a
			// cosmetic one.
			broadcaster.nothingToUser(t, outsider.OwnerID.String(), "another organization")

			calls, err := svc.ListCalls(ctx, org.ID, org.OwnerID, "", nil)
			if err != nil {
				t.Fatalf("list calls: %v", err)
			}

			if tc.refused {
				if len(calls) != 0 {
					t.Errorf("the invite named an outsider and should have been refused whole; %d calls exist", len(calls))
				}
				broadcaster.nothingToUser(t, mate.String(), "invited alongside an outsider")
				caller.noneOfType(t, protocol.TypeCallInvited, "the invite was refused")

				// And the caller is told, so their client stops waiting on a
				// call that was never made.
				var refused protocol.CallEndedPayload
				decodePayload(t, caller.firstOfType(t, protocol.TypeCallEnded), &refused)
				if refused.Reason != "invalid_invitee" {
					t.Errorf("refusal reason: got %q want invalid_invitee", refused.Reason)
				}
				return
			}

			if len(calls) != 1 {
				t.Fatalf("a valid invite should have made a call; %d exist", len(calls))
			}
			var rung protocol.CallInvitedPayload
			decodePayload(t, broadcaster.firstToUser(t, mate.String(), protocol.TypeCallInvited), &rung)
			if rung.Token == "" {
				t.Error("somebody in your own organization should get a token to join with")
			}
			if rung.RoomName != calls[0].RoomName {
				t.Errorf("rung for room %q, want %q", rung.RoomName, calls[0].RoomName)
			}
		})
	}
}

// Ring is the other door onto the same credentials: the HTTP create-call
// handler hands it the invitee list straight off the request body.
func TestRingSkipsSomebodyFromAnotherOrganization(t *testing.T) {
	svc, pool := newTokenService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "mate@example.test", "member")

	broadcaster := newRecordingBroadcaster()
	svc.SetBroadcaster(broadcaster)

	call, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, nil)
	if err != nil {
		t.Fatalf("create call: %v", err)
	}
	svc.Ring(ctx, call, org.OwnerID.String(), "Owner",
		[]uuid.UUID{mate, outsider.OwnerID})

	broadcaster.nothingToUser(t, outsider.OwnerID.String(), "another organization")

	var rung protocol.CallInvitedPayload
	decodePayload(t, broadcaster.firstToUser(t, mate.String(), protocol.TypeCallInvited), &rung)
	if rung.Token == "" {
		t.Error("somebody in the call's own organization should still be rung with a token")
	}
}

func acceptFrame(room string) string {
	return `{"type":"call.accept","payload":{"roomName":"` + room + `"}}`
}

// A join token used to be signed for whatever room name the frame carried, so
// knowing or guessing a name was enough to join any live call on the instance.
func TestACallAcceptFrameTokenIsOnlyForSomebodyEntitledToJoin(t *testing.T) {
	svc, pool := newTokenService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	invitee := testsupport.SeedUser(t, pool, org, "invitee@example.test", "member")
	member := testsupport.SeedUser(t, pool, org, "member@example.test", "member")
	bystander := testsupport.SeedUser(t, pool, org, "bystander@example.test", "member")
	other := testsupport.SeedOrg(t, pool)
	channel := testsupport.SeedChannel(t, pool, org, "standup", org.OwnerID, member)

	private, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{invitee.String()})
	if err != nil {
		t.Fatalf("create private call: %v", err)
	}
	team, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", &channel, nil)
	if err != nil {
		t.Fatalf("create channel call: %v", err)
	}
	ended, err := svc.CreateCall(ctx, org.ID, org.OwnerID, nil, "video", nil, []string{invitee.String()})
	if err != nil {
		t.Fatalf("create ended call: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE calls SET status = 'ended', ended_at = NOW() WHERE id = $1`, ended.ID); err != nil {
		t.Fatalf("end call: %v", err)
	}

	cases := []struct {
		name    string
		org     uuid.UUID
		user    uuid.UUID
		room    string
		callID  uuid.UUID
		allowed bool
	}{
		{"somebody invited", org.ID, invitee, private.RoomName, private.ID, true},
		{"a member of the call's channel", org.ID, member, team.RoomName, team.ID, true},
		{"somebody in another organization", other.ID, other.OwnerID, private.RoomName, private.ID, false},
		{"a colleague who was not invited", org.ID, bystander, private.RoomName, private.ID, false},
		{"somebody invited, once it has ended", org.ID, invitee, ended.RoomName, ended.ID, false},
		{"a room that does not exist", org.ID, invitee, "call-abc123", uuid.Nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			broadcaster := newRecordingBroadcaster()
			svc.SetBroadcaster(broadcaster)

			conn := &wsConn{userID: tc.user.String(), orgID: tc.org.String()}
			svc.HandleCallAccept(ctx, conn, decodeFrame(t, acceptFrame(tc.room)))

			if !tc.allowed {
				broadcaster.nothingToUser(t, tc.user.String(), tc.name)
				return
			}
			var accepted protocol.CallInvitedPayload
			decodePayload(t, broadcaster.firstToUser(t, tc.user.String(), protocol.TypeCallInvited), &accepted)
			if accepted.RoomName != tc.room || accepted.Token == "" {
				t.Errorf("accepted: got room %q and token %q, want room %q and a token", accepted.RoomName, accepted.Token, tc.room)
			}
			if accepted.CallID != tc.callID.String() {
				t.Errorf("call id: got %q want %s", accepted.CallID, tc.callID)
			}
		})
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

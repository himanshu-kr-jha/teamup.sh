package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	nhooyr "nhooyr.io/websocket"

	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/internal/websocket"
	"github.com/teamup/teamup/pkg/protocol"
)

// The tests in this file drive real frames over a real socket into the real
// hub, rather than calling a handler with a hand-built payload.
//
// That distinction is the whole point. Every inbound handler used to
// type-assert msg.Payload to []byte against a field encoding/json always fills
// with map[string]interface{}, so every client-to-server frame was discarded
// before the handler did anything. A test that constructs a protocol.Message
// itself chooses the payload's concrete type and so cannot see that at all —
// which is why eighteen tickets closed green over a dead inbound path.

// ── Harness ───────────────────────────────────────────────────────────────

// stubAuthService hands out tokens for seeded users, standing in for the real
// JWT service so the handshake is the only thing being faked.
type stubAuthService struct {
	mu     sync.Mutex
	claims map[string]websocket.Claims
}

func (a *stubAuthService) issue(orgID, userID uuid.UUID) string {
	token := uuid.NewString()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.claims[token] = websocket.Claims{UserID: userID.String(), OrgID: orgID.String()}
	return token
}

func (a *stubAuthService) ValidateAccessToken(_ context.Context, token string) (*websocket.Claims, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	claims, ok := a.claims[token]
	if !ok {
		return nil, errors.New("unknown token")
	}
	return &claims, nil
}

// stubPresenceService keeps presence out of Redis; what it records is enough to
// tell whether an inbound presence frame was decoded.
type stubPresenceService struct {
	mu       sync.Mutex
	statuses []string
}

func (p *stubPresenceService) SetOnline(string, string) error  { return nil }
func (p *stubPresenceService) SetOffline(string, string) error { return nil }

func (p *stubPresenceService) UpdateStatus(_, _ string, status, _, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statuses = append(p.statuses, status)
	return nil
}

func (p *stubPresenceService) HandlePresenceUpdate(context.Context, websocket.WSConnection, *protocol.Message) {
}

func (p *stubPresenceService) GetPresence(string, string) (*websocket.Presence, error) {
	return nil, nil
}
func (p *stubPresenceService) GetAllOnline(string) ([]websocket.Presence, error) { return nil, nil }

// wsServices satisfies the hub's service interface with the real chat service;
// the call handlers live in another package and are not what these tests drive.
type wsServices struct{ *Service }

func (wsServices) HandleCallInvite(context.Context, websocket.WSConnection, *protocol.Message)    {}
func (wsServices) HandleCallAccept(context.Context, websocket.WSConnection, *protocol.Message)    {}
func (wsServices) HandleCallDecline(context.Context, websocket.WSConnection, *protocol.Message)   {}
func (wsServices) HandleCallSignaling(context.Context, websocket.WSConnection, *protocol.Message) {}

type wsHarness struct {
	svc    *Service
	pool   *pgxpool.Pool
	hub    *websocket.Hub
	auth   *stubAuthService
	server *httptest.Server
}

func newWSHarness(t *testing.T) *wsHarness {
	t.Helper()

	svc, pool := newService(t)
	svc.SetLogger(zerolog.Nop())

	auth := &stubAuthService{claims: map[string]websocket.Claims{}}
	hub := websocket.NewHub(wsServices{svc}, &stubPresenceService{}, auth, zerolog.Nop())
	hub.SetChannelRoster(svc)
	svc.SetBroadcaster(hub)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/ws", websocket.WSHandler(hub, auth, nil))

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &wsHarness{svc: svc, pool: pool, hub: hub, auth: auth, server: server}
}

// wireFrame is what a browser sees: a type and a blob of JSON. Decoded here
// independently of protocol.Message so the assertions are about the wire
// format rather than the server's own envelope type.
type wireFrame struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type wsClient struct {
	name string
	conn *nhooyr.Conn
}

// dial opens a socket as a seeded user, the way the browser does.
func (h *wsHarness) dial(t *testing.T, name string, orgID, userID uuid.UUID) *wsClient {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(h.server.URL, "http") + "/ws"
	conn, _, err := nhooyr.Dial(ctx, url, &nhooyr.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + h.auth.issue(orgID, userID)}},
	})
	if err != nil {
		t.Fatalf("dial as %s: %v", name, err)
	}
	t.Cleanup(func() { _ = conn.Close(nhooyr.StatusNormalClosure, "") })

	return &wsClient{name: name, conn: conn}
}

func (c *wsClient) send(t *testing.T, msgType string, payload any) {
	t.Helper()

	frame, err := json.Marshal(map[string]any{"type": msgType, "payload": payload})
	if err != nil {
		t.Fatalf("marshal %s: %v", msgType, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, nhooyr.MessageText, frame); err != nil {
		t.Fatalf("%s sending %s: %v", c.name, msgType, err)
	}
}

// await returns the first frame of the given type, stepping over the presence
// and ping traffic the server sends unprompted.
func (c *wsClient) await(t *testing.T, msgType string, within time.Duration) wireFrame {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()

	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			t.Fatalf("%s never received %s: %v", c.name, msgType, err)
		}
		var frame wireFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("%s received an undecodable frame %s: %v", c.name, data, err)
		}
		if frame.Type == msgType {
			return frame
		}
	}
}

// expectNothing asserts a frame of the given type never arrives.
func (c *wsClient) expectNothing(t *testing.T, msgType string, within time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()

	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return // the deadline passed with nothing of that type: correct
		}
		var frame wireFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}
		if frame.Type == msgType {
			t.Fatalf("%s received %s, which should never have been sent: %s", c.name, msgType, frame.Payload)
		}
	}
}

func decodePayload(t *testing.T, frame wireFrame, into any) {
	t.Helper()
	if err := json.Unmarshal(frame.Payload, into); err != nil {
		t.Fatalf("decode %s payload %s: %v", frame.Type, frame.Payload, err)
	}
}

// ── Inbound frames (ticket 33) ────────────────────────────────────────────

func TestAChatSendFrameIsPersistedAndDelivered(t *testing.T) {
	h := newWSHarness(t)
	org := testsupport.SeedOrg(t, h.pool)
	listenerID := testsupport.SeedUser(t, h.pool, org, "listener@example.test", "member")
	channelID := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID, listenerID)

	sender := h.dial(t, "sender", org.ID, org.OwnerID)
	listener := h.dial(t, "listener", org.ID, listenerID)

	sender.send(t, protocol.TypeChatSend, protocol.ChatSendPayload{
		ChannelID: channelID.String(),
		Content:   "hello over WEBSOCKET",
		TempID:    "temp-1",
	})

	var ack protocol.ChatSentPayload
	decodePayload(t, sender.await(t, protocol.TypeChatSent, 5*time.Second), &ack)
	if ack.TempID != "temp-1" {
		t.Errorf("acknowledgement should name the client's temp id, got %q", ack.TempID)
	}

	var delivered protocol.ChatMessagePayload
	decodePayload(t, listener.await(t, protocol.TypeChatMessage, 5*time.Second), &delivered)
	if delivered.Message.Content != "hello over WEBSOCKET" {
		t.Errorf("delivered content: got %q", delivered.Message.Content)
	}

	var content string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT content FROM messages WHERE id = $1`, ack.MessageID).Scan(&content); err != nil {
		t.Fatalf("the message sent over the socket was never persisted: %v", err)
	}
	if content != "hello over WEBSOCKET" {
		t.Errorf("persisted content: got %q", content)
	}
}

func TestAChatSendFrameFromANonMemberIsRefused(t *testing.T) {
	h := newWSHarness(t)
	org := testsupport.SeedOrg(t, h.pool)
	outsiderID := testsupport.SeedUser(t, h.pool, org, "outsider@example.test", "member")
	channelID := testsupport.SeedChannel(t, h.pool, org, "private", org.OwnerID)

	member := h.dial(t, "member", org.ID, org.OwnerID)
	outsider := h.dial(t, "outsider", org.ID, outsiderID)

	outsider.send(t, protocol.TypeChatSend, protocol.ChatSendPayload{
		ChannelID: channelID.String(),
		Content:   "let me in",
	})

	member.expectNothing(t, protocol.TypeChatMessage, time.Second)

	var count int
	if err := h.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM messages WHERE channel_id = $1`, channelID).Scan(&count); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 0 {
		t.Errorf("a non-member's message was stored anyway: %d rows", count)
	}
}

func TestAChatEditFrameChangesThePersistedMessage(t *testing.T) {
	h := newWSHarness(t)
	org := testsupport.SeedOrg(t, h.pool)
	readerID := testsupport.SeedUser(t, h.pool, org, "reader@example.test", "member")
	channelID := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID, readerID)

	author := h.dial(t, "author", org.ID, org.OwnerID)
	reader := h.dial(t, "reader", org.ID, readerID)

	author.send(t, protocol.TypeChatSend, protocol.ChatSendPayload{
		ChannelID: channelID.String(),
		Content:   "frist post",
	})
	var ack protocol.ChatSentPayload
	decodePayload(t, author.await(t, protocol.TypeChatSent, 5*time.Second), &ack)
	reader.await(t, protocol.TypeChatMessage, 5*time.Second)

	author.send(t, protocol.TypeChatEdit, protocol.ChatEditPayload{
		MessageID: ack.MessageID,
		Content:   "first post",
	})

	var edited protocol.ChatMessagePayload
	decodePayload(t, reader.await(t, protocol.TypeChatMessage, 5*time.Second), &edited)
	if edited.Message.Content != "first post" {
		t.Errorf("edit broadcast content: got %q", edited.Message.Content)
	}

	var content string
	var editedAt *time.Time
	if err := h.pool.QueryRow(context.Background(),
		`SELECT content, edited_at FROM messages WHERE id = $1`, ack.MessageID).Scan(&content, &editedAt); err != nil {
		t.Fatalf("load edited message: %v", err)
	}
	if content != "first post" {
		t.Errorf("persisted content after edit: got %q", content)
	}
	if editedAt == nil {
		t.Error("an edited message should be stamped as edited")
	}
}

func TestAChatDeleteFrameRemovesThePersistedMessage(t *testing.T) {
	h := newWSHarness(t)
	org := testsupport.SeedOrg(t, h.pool)
	readerID := testsupport.SeedUser(t, h.pool, org, "reader@example.test", "member")
	channelID := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID, readerID)

	author := h.dial(t, "author", org.ID, org.OwnerID)
	reader := h.dial(t, "reader", org.ID, readerID)

	author.send(t, protocol.TypeChatSend, protocol.ChatSendPayload{
		ChannelID: channelID.String(),
		Content:   "regrettable",
	})
	var ack protocol.ChatSentPayload
	decodePayload(t, author.await(t, protocol.TypeChatSent, 5*time.Second), &ack)
	reader.await(t, protocol.TypeChatMessage, 5*time.Second)

	author.send(t, protocol.TypeChatDelete, protocol.ChatDeletePayload{MessageID: ack.MessageID})

	var removed protocol.ChatDeletePayload
	decodePayload(t, reader.await(t, protocol.TypeChatDelete, 5*time.Second), &removed)
	if removed.MessageID != ack.MessageID {
		t.Errorf("delete broadcast named %q, want %q", removed.MessageID, ack.MessageID)
	}

	var deletedAt *time.Time
	if err := h.pool.QueryRow(context.Background(),
		`SELECT deleted_at FROM messages WHERE id = $1`, ack.MessageID).Scan(&deletedAt); err != nil {
		t.Fatalf("load deleted message: %v", err)
	}
	if deletedAt == nil {
		t.Error("the message sent for deletion is still there")
	}
}

func TestAChatReadFrameAdvancesTheReadMarker(t *testing.T) {
	h := newWSHarness(t)
	org := testsupport.SeedOrg(t, h.pool)
	readerID := testsupport.SeedUser(t, h.pool, org, "reader@example.test", "member")
	channelID := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID, readerID)

	author := h.dial(t, "author", org.ID, org.OwnerID)
	reader := h.dial(t, "reader", org.ID, readerID)

	author.send(t, protocol.TypeChatSend, protocol.ChatSendPayload{
		ChannelID: channelID.String(),
		Content:   "did you see this",
	})
	var ack protocol.ChatSentPayload
	decodePayload(t, author.await(t, protocol.TypeChatSent, 5*time.Second), &ack)
	reader.await(t, protocol.TypeChatMessage, 5*time.Second)

	reader.send(t, protocol.TypeChatRead, protocol.ChatReadPayload{
		ChannelID: channelID.String(),
		MessageID: ack.MessageID,
	})

	// Nothing is broadcast for a read, so poll the marker the frame moves.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var lastReadAt *time.Time
		if err := h.pool.QueryRow(context.Background(),
			`SELECT last_read_at FROM channel_members WHERE channel_id = $1 AND user_id = $2`,
			channelID, readerID).Scan(&lastReadAt); err != nil {
			t.Fatalf("load read marker: %v", err)
		}
		if lastReadAt != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the read marker never moved, so the chat.read frame was dropped")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestAChatTypingFrameReachesTheOtherMembers(t *testing.T) {
	h := newWSHarness(t)
	org := testsupport.SeedOrg(t, h.pool)
	watcherID := testsupport.SeedUser(t, h.pool, org, "watcher@example.test", "member")
	channelID := testsupport.SeedChannel(t, h.pool, org, "general", org.OwnerID, watcherID)

	typist := h.dial(t, "typist", org.ID, org.OwnerID)
	watcher := h.dial(t, "watcher", org.ID, watcherID)

	typist.send(t, protocol.TypeChatTyping, protocol.ChatTypingPayload{
		ChannelID: channelID.String(),
		IsTyping:  true,
	})

	var typing protocol.ChatTypingPayload
	decodePayload(t, watcher.await(t, protocol.TypeChatTyping, 5*time.Second), &typing)
	if typing.ChannelID != channelID.String() || !typing.IsTyping {
		t.Errorf("typing payload did not survive the round trip: %+v", typing)
	}
}

// ── Authorizing the typing indicator (ticket 41) ──────────────────────────
//
// HandleTyping was the one chat handler with no authorization of any kind: it
// handed a client-supplied channel id straight to the broadcaster. Nothing
// surfaced it because the handler returned on the payload assertion before
// reaching the broadcast, so fixing inbound decoding is what turned a dead
// handler into a live unauthorized one.

func TestATypingFrameFromANonMemberReachesNobody(t *testing.T) {
	h := newWSHarness(t)
	org := testsupport.SeedOrg(t, h.pool)
	outsiderID := testsupport.SeedUser(t, h.pool, org, "outsider@example.test", "member")
	channelID := testsupport.SeedChannel(t, h.pool, org, "private", org.OwnerID)

	member := h.dial(t, "member", org.ID, org.OwnerID)
	outsider := h.dial(t, "outsider", org.ID, outsiderID)

	outsider.send(t, protocol.TypeChatTyping, protocol.ChatTypingPayload{
		ChannelID: channelID.String(),
		IsTyping:  true,
	})

	// Receiving this would tell the outsider the channel exists and let them
	// put state into a conversation they cannot see.
	member.expectNothing(t, protocol.TypeChatTyping, time.Second)
}

func TestATypingFrameCannotCrossOrganizations(t *testing.T) {
	h := newWSHarness(t)
	ours := testsupport.SeedOrg(t, h.pool)
	theirs := testsupport.SeedOrg(t, h.pool)
	theirChannel := testsupport.SeedChannel(t, h.pool, theirs, "their-general", theirs.OwnerID)

	watcher := h.dial(t, "their-member", theirs.ID, theirs.OwnerID)
	intruder := h.dial(t, "our-member", ours.ID, ours.OwnerID)

	intruder.send(t, protocol.TypeChatTyping, protocol.ChatTypingPayload{
		ChannelID: theirChannel.String(),
		IsTyping:  true,
	})

	watcher.expectNothing(t, protocol.TypeChatTyping, time.Second)
}

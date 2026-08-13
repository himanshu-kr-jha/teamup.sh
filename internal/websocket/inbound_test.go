package websocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"nhooyr.io/websocket"

	"github.com/teamup/teamup/pkg/protocol"
)

// readFrame reads one message off the client end of a socket and decodes the
// envelope, so a test can say what the server actually put on the wire rather
// than what it put in a channel.
func readFrame(t *testing.T, client *websocket.Conn) *protocol.Message {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, data, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}

	var msg protocol.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode frame %q: %v", data, err)
	}
	return &msg
}

// writeFrame sends a frame from the client end, the way a browser does.
func writeFrame(t *testing.T, client *websocket.Conn, frame string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// The client opens a socket and introduces itself with conn.init, and stays in
// its 'connecting' state until the server answers conn.ack — everything handed
// to send() queues in the meantime. handleMessage had no case for conn.init, so
// it fell through to the unknown-message default and nothing ever sent a
// conn.ack: the ack type was declared and had no senders at all. The handshake
// never completed and the queue never drained.
func TestIntroducingAConnectionIsAcknowledged(t *testing.T) {
	pool := newSockets(t)
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())

	conn, client := pool.open(t, hub, "conn-1", "user-1", "org-1")
	go conn.ReadLoop()
	go conn.WriteLoop()

	writeFrame(t, client, `{"type":"conn.init","payload":{"clientInfo":{"platform":"web","version":"1.0"}}}`)

	msg := readFrame(t, client)
	if msg.Type != protocol.TypeConnAck {
		t.Fatalf("the handshake was not acknowledged: got %q want %q", msg.Type, protocol.TypeConnAck)
	}

	var ack protocol.ConnAckPayload
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		t.Fatalf("decode ack payload: %v", err)
	}
	if ack.ConnectionID != "conn-1" {
		t.Errorf("connection id: got %q want conn-1", ack.ConnectionID)
	}
	if ack.UserID != "user-1" {
		t.Errorf("user id: got %q want user-1", ack.UserID)
	}
	if ack.OrgID != "org-1" {
		t.Errorf("org id: got %q want org-1", ack.OrgID)
	}
	if ack.ServerTime == "" {
		t.Error("the ack should carry the server's clock")
	}
}

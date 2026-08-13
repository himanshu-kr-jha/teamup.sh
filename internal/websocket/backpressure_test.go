package websocket

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/pkg/protocol"
)

// syncBuffer collects log output from whichever goroutine writes it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A connection nobody drains, with its log captured.
func stalledConnection(hub *Hub, logs *syncBuffer, buffer int) *Connection {
	conn := testConnection(hub, "conn-slow", "user-slow", "org-1")
	conn.SendCh = make(chan *protocol.Message, buffer)
	conn.logger = zerolog.New(logs)
	return conn
}

func fill(conn *Connection, n int, msgType string) {
	for i := 0; i < n; i++ {
		conn.Enqueue(protocol.NewMessage(msgType, map[string]string{"content": "hello"}))
	}
}

// Enqueue gave up on a full buffer with an empty default branch: no counter, no
// log line, nothing. A user on a weak link lost live messages and neither they
// nor an operator had any way to find out — the messages are still in Postgres,
// so the only symptom is somebody eventually saying they never saw one.
func TestDroppingAMessageToASlowClientIsReported(t *testing.T) {
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())
	logs := &syncBuffer{}
	conn := stalledConnection(hub, logs, 8)

	const msgType = protocol.TypeChatMessage
	before := testutil.ToFloat64(wsMessagesDropped.WithLabelValues(msgType))

	// Eight fit; the rest have nowhere to go. Kept well under the threshold at
	// which the connection is closed, so this is only about the reporting.
	fill(conn, 8+5, msgType)

	dropped := testutil.ToFloat64(wsMessagesDropped.WithLabelValues(msgType)) - before
	if dropped != 5 {
		t.Errorf("dropped messages counted: got %v want 5", dropped)
	}

	if !strings.Contains(logs.String(), "user-slow") {
		t.Errorf("the drop was not logged against the connection: %q", logs.String())
	}
}

// Counting a drop tells an operator. It does not help the user, whose client
// still believes it has everything. A connection that is provably not keeping
// up is closed so that the client reconnects and refetches.
func TestAClientThatNeverCatchesUpIsDisconnected(t *testing.T) {
	pool := newSockets(t)
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())

	conn, _ := pool.open(t, hub, "conn-slow", "user-slow", "org-1")
	conn.logger = zerolog.Nop()

	// Eight fill the buffer; then one short of the threshold. A client that is
	// merely behind is left alone.
	fill(conn, 8+dropsBeforeClose-1, protocol.TypeChatMessage)
	select {
	case <-conn.ctx.Done():
		t.Fatal("a connection that is only behind was closed")
	default:
	}

	fill(conn, 1, protocol.TypeChatMessage)
	select {
	case <-conn.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a connection that never keeps up was left open, diverging silently")
	}
}

// The count is of consecutive failures, so a client that recovers is not
// punished for a burst it already caught up on.
func TestCatchingUpForgivesEarlierDrops(t *testing.T) {
	pool := newSockets(t)
	hub := NewHub(nil, stubPresence{}, nil, zerolog.Nop())

	conn, _ := pool.open(t, hub, "conn-bursty", "user-bursty", "org-1")
	conn.logger = zerolog.Nop()

	// A burst that overruns the buffer, but stops just short.
	fill(conn, 8+dropsBeforeClose-1, protocol.TypeChatMessage)
	drain(conn)

	// They catch up, and a second identical burst is judged on its own.
	fill(conn, 8+dropsBeforeClose-1, protocol.TypeChatMessage)
	select {
	case <-conn.ctx.Done():
		t.Fatal("a client that caught up was closed for drops it had recovered from")
	default:
	}
}

package presence

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/teamup/teamup/pkg/protocol"
)

// The frame is decoded here rather than hand-built, because that is where the
// defect was: msg.Payload was an empty interface, encoding/json filled it with
// a map, and the handler's assertion to []byte failed so every presence.update
// a client sent was discarded.

// EnvRedisURL names the Redis to test against; the default matches the one the
// development stack runs.
const EnvRedisURL = "TEAMUP_TEST_REDIS_URL"

func testRedis(t *testing.T) *redis.Client {
	t.Helper()

	url := os.Getenv(EnvRedisURL)
	if url == "" {
		url = "redis://localhost:6379"
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", EnvRedisURL, url, err)
	}

	client := redis.NewClient(opts)
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Skipf("set %s to a reachable Redis to run presence tests: %v", EnvRedisURL, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// wsConn is a connection that only has to say who is on the other end.
type wsConn struct {
	userID string
	orgID  string
}

func (c wsConn) UserID() string            { return c.userID }
func (c wsConn) OrgID() string             { return c.orgID }
func (c wsConn) Enqueue(*protocol.Message) {}

func TestAPresenceUpdateFrameChangesTheStoredStatus(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)

	conn := wsConn{userID: uuid.NewString(), orgID: uuid.NewString()}
	t.Cleanup(func() {
		client.Del(context.Background(), "presence:"+conn.orgID+":"+conn.userID)
	})

	var msg protocol.Message
	frame := []byte(`{"type":"presence.update","payload":{"status":"away","customStatus":"back at three","customStatusEmoji":"🚶"}}`)
	if err := json.Unmarshal(frame, &msg); err != nil {
		t.Fatalf("decode frame: %v", err)
	}

	svc.HandlePresenceUpdate(context.Background(), conn, &msg)

	got, err := svc.GetPresence(conn.userID, conn.orgID)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if got == nil {
		t.Fatal("the presence.update frame was dropped: nothing was stored")
	}
	if got.Status != "away" {
		t.Errorf("status: got %q want away", got.Status)
	}
	if got.CustomStatus != "back at three" {
		t.Errorf("custom status: got %q", got.CustomStatus)
	}
	if got.CustomEmoji != "🚶" {
		t.Errorf("custom emoji: got %q", got.CustomEmoji)
	}
}

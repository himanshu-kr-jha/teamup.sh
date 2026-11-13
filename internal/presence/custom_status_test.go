package presence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/pkg/protocol"
)

// A custom status is shown beside a name everywhere the person appears, and is
// stored in Redis and sent to everybody in the organization, so it is kept to
// a line of text and an emoji.
func TestACustomStatusIsKeptShort(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	userID, orgID := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() { client.Del(context.Background(), presenceKey(orgID, userID)) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", userID); c.Set("org_id", orgID); c.Next() })
	RegisterRoutes(r.Group("/api"), svc)
	patch := func(text, emoji string) int {
		raw, _ := json.Marshal(map[string]string{"status": "away", "customStatus": text, "customStatusEmoji": emoji})
		req := httptest.NewRequest(http.MethodPatch, "/api/presence", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// 100 characters, each several bytes, is still one line.
	if code := patch(strings.Repeat("é", 100), "👩🏽‍💻"); code != http.StatusOK {
		t.Errorf("100 characters and a composed emoji: %d", code)
	}
	if code := patch(strings.Repeat("a", 101), ""); code != http.StatusBadRequest {
		t.Errorf("101 characters: want 400, got %d", code)
	}
	if code := patch("", strings.Repeat("🚶", 5)); code != http.StatusBadRequest {
		t.Errorf("a string of emoji: want 400, got %d", code)
	}

	// The same limits hold for a status sent over the socket.
	conn := wsConn{userID: uuid.NewString(), orgID: orgID}
	t.Cleanup(func() { client.Del(context.Background(), presenceKey(orgID, conn.userID)) })
	frame, _ := json.Marshal(map[string]any{"type": "presence.update", "payload": map[string]string{
		"status": "away", "customStatus": strings.Repeat("x", 1<<20), "customStatusEmoji": "🚶",
	}})
	var msg protocol.Message
	if err := json.Unmarshal(frame, &msg); err != nil {
		t.Fatal(err)
	}
	svc.HandlePresenceUpdate(context.Background(), conn, &msg)
	if got, _ := svc.GetPresence(conn.userID, orgID); got != nil && len(got.CustomStatus) > 100 {
		t.Errorf("a megabyte of status over the socket was stored (%d bytes)", len(got.CustomStatus))
	}
}

func TestOneEmojiIsOneSymbolHoweverItIsBuilt(t *testing.T) {
	for _, e := range []string{"🚶", "🇮🇳", "1️⃣", "❤️", "👍🏾", "👨‍👩‍👧‍👦", "🏳️‍🌈", "🏴󠁧󠁢󠁳󠁣󠁴󠁿"} {
		if err := validCustomStatus("", e); err != nil {
			t.Errorf("%q is one emoji: %v", e, err)
		}
	}
	for _, e := range []string{"🚶🚶", "🇮🇳🇬🇧", "ab", "👍🏾👍🏾"} {
		if err := validCustomStatus("", e); err == nil {
			t.Errorf("%q is more than one", e)
		}
	}
}

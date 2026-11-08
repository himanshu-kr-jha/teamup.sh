package notetaker

// The note-taker from a person joining to everybody holding the minutes, with
// the speech-to-text service and the model replaced by local stand-ins and the
// room by clips handed straight in.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/mail"
	"github.com/teamup/teamup/internal/org"
	"github.com/teamup/teamup/internal/testsupport"
)

type recorded struct {
	mu       sync.Mutex
	notified []uuid.UUID
	mailed   []string
}

func (r *recorded) Notify(_ context.Context, _, userID uuid.UUID, kind, _, _ string, data map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if kind == "minutes.ready" && strings.HasSuffix(data["url"].(string), "/minutes") {
		r.notified = append(r.notified, userID)
	}
	return nil
}

func (r *recorded) Send(_ context.Context, _ uuid.UUID, m mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.Contains(m.Body, "Ship on Friday") {
		r.mailed = append(r.mailed, m.To)
	}
	return nil
}

func (r *recorded) Configured(context.Context, uuid.UUID) bool { return true }

// stand-ins answers as Sarvam and as Claude would.
func standIns(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/speech-to-text":
			_ = r.ParseMultipartForm(1 << 20)
			f, _, _ := r.FormFile("file")
			audio, _ := io.ReadAll(f)
			// The clip's bytes stand for what was said.
			_ = json.NewEncoder(w).Encode(map[string]string{"transcript": string(audio)})
		case "/v1/messages":
			var body struct {
				Messages []struct{ Content string } `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !strings.Contains(body.Messages[0].Content, "Ada Archer: Let us ship on Friday") ||
				!strings.Contains(body.Messages[0].Content, "Bo Baker: Agreed") {
				http.Error(w, "the transcript did not arrive", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text",
				"text": `{"summary": "The team agreed to ship.", "decisions": ["Ship on Friday"], "actionItems": [{"owner": "Ada Archer", "task": "Tag the release"}], "openQuestions": []}`}}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type world struct {
	svc            *Service
	orgs           *org.Service
	orgID          uuid.UUID
	ada, bo, carol uuid.UUID
	callID         uuid.UUID
	sent           *recorded
	joins          int
	mu             sync.Mutex
}

func newWorld(t *testing.T) *world {
	pool := testsupport.DB(t)
	o := testsupport.SeedOrg(t, pool)
	ada := testsupport.SeedUser(t, pool, o, "ada@example.com", "member")
	bo := testsupport.SeedUser(t, pool, o, "bo@example.com", "member")
	carol := testsupport.SeedUser(t, pool, o, "carol@example.com", "member")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE users SET name = CASE id WHEN $1 THEN 'Ada Archer' WHEN $2 THEN 'Bo Baker' ELSE name END`, ada, bo); err != nil {
		t.Fatal(err)
	}
	callID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO calls (id, org_id, room_name, title, status, started_at, created_by) VALUES ($1, $2, 'room-1', 'Weekly', 'active', NOW(), $3)`, callID, o.ID, ada); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO call_participants (call_id, user_id, joined_at) VALUES ($1, $2, NOW()), ($1, $3, NOW())`, callID, ada, bo); err != nil {
		t.Fatal(err)
	}
	orgs := org.NewService(pool)
	w := &world{orgs: orgs, orgID: o.ID, ada: ada, bo: bo, carol: carol, callID: callID, sent: &recorded{}}
	w.svc = NewService(pool, orgs, LiveKit{}, "https://teamup.example", zerolog.Nop())
	w.svc.SetNotifier(w.sent)
	w.svc.SetMailer(w.sent)
	w.svc.GracePeriod = 10 * time.Millisecond
	start := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	// Instead of a room: Ada and Bo say one thing each.
	w.svc.join = func(sess *session) error {
		w.mu.Lock()
		w.joins++
		w.mu.Unlock()
		sess.clips <- Clip{Speaker: Speaker{UserID: ada.String(), Name: "Ada Archer"}, StartedAt: start, EndedAt: start.Add(3 * time.Second), Audio: []byte("Let us ship on Friday")}
		sess.clips <- Clip{Speaker: Speaker{UserID: bo.String(), Name: "Bo Baker"}, StartedAt: start.Add(4 * time.Second), EndedAt: start.Add(5 * time.Second), Audio: []byte("Agreed")}
		return nil
	}
	return w
}

func (w *world) router(t *testing.T, userID uuid.UUID, roles ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", w.orgID.String())
		c.Set("user_id", userID.String())
		c.Set("user_roles", roles)
		c.Next()
	})
	RegisterRoutes(api, w.svc)
	return r
}

func call(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestOnlyAnAdministratorSetsItUpAndTheKeysNeverComeBack(t *testing.T) {
	w := newWorld(t)
	admin := w.router(t, w.ada, "admin")
	if got := call(t, w.router(t, w.bo, "member"), http.MethodPut, "/api/org/notetaker", map[string]any{"enabled": false}); got.Code != http.StatusForbidden {
		t.Fatalf("a member cannot turn it off: %d", got.Code)
	}

	saved := call(t, admin, http.MethodPut, "/api/org/notetaker", map[string]any{
		"enabled": true, "sttKey": "sk-sarvam-secret", "llmProvider": "anthropic", "llmKey": "sk-ant-secret",
	})
	if saved.Code != http.StatusOK || strings.Contains(saved.Body.String(), "secret") || !strings.Contains(saved.Body.String(), `"ready":true`) {
		t.Fatalf("saved: %d %s", saved.Code, saved.Body.String())
	}
	// Turned off, keys kept.
	off := call(t, admin, http.MethodPut, "/api/org/notetaker", map[string]any{"enabled": false, "llmProvider": "anthropic"})
	if !strings.Contains(off.Body.String(), `"sttKeySet":true`) || !strings.Contains(off.Body.String(), `"ready":false`) {
		t.Errorf("off, keys kept: %s", off.Body.String())
	}
	set, _ := w.svc.Settings(context.Background(), w.orgID)
	if set.STTKey != "sk-sarvam-secret" || set.Enabled {
		t.Errorf("stored: %+v", set)
	}
	if got := call(t, admin, http.MethodPut, "/api/org/notetaker", map[string]any{"llmProvider": "gemini"}); got.Code != http.StatusBadRequest {
		t.Errorf("an unknown model provider: %d", got.Code)
	}
}

func TestEverybodyWhoWasThereGetsTheMinutesAndNobodyElseCanReadThem(t *testing.T) {
	w := newWorld(t)
	srv := standIns(t)
	ctx := context.Background()
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}

	// Two people joining, perhaps on two servers: one note-taker.
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")

	ada := w.router(t, w.ada, "member")
	var notes Notes
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got := call(t, ada, http.MethodGet, "/api/calls/"+w.callID.String()+"/minutes", nil)
		_ = json.Unmarshal(got.Body.Bytes(), &notes)
		if notes.Status == "ready" || notes.Status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if notes.Status != "ready" || notes.Minutes == nil {
		t.Fatalf("minutes: %+v", notes)
	}
	if w.joins != 1 {
		t.Errorf("joined once: %d", w.joins)
	}
	m := notes.Minutes
	if m.Title != "Weekly" || m.Decisions[0] != "Ship on Friday" || len(m.Attendees) != 2 {
		t.Errorf("minutes: %+v", m)
	}
	if len(notes.Transcript) != 2 || notes.Transcript[0].Speaker != "Ada Archer" || notes.Transcript[1].Text != "Agreed" {
		t.Errorf("transcript, in order: %+v", notes.Transcript)
	}

	w.sent.mu.Lock()
	notified, mailed := len(w.sent.notified), w.sent.mailed
	w.sent.mu.Unlock()
	if notified != 2 || len(mailed) != 2 {
		t.Errorf("both told and emailed: %d %v", notified, mailed)
	}

	if got := call(t, w.router(t, w.carol, "member"), http.MethodGet, "/api/calls/"+w.callID.String()+"/minutes", nil); got.Code != http.StatusNotFound {
		t.Errorf("Carol was not there: %d", got.Code)
	}
	if got := call(t, w.router(t, w.carol, "admin"), http.MethodGet, "/api/calls/"+w.callID.String()+"/minutes", nil); got.Code != http.StatusOK {
		t.Errorf("an administrator may read them: %d", got.Code)
	}
}

func TestTurnedOffItDoesNotJoin(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	_ = w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: false, STTKey: "k", LLMKey: "k"}, true)
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	time.Sleep(100 * time.Millisecond)
	if w.joins != 0 {
		t.Errorf("off means off")
	}
	if got := call(t, w.router(t, w.ada, "member"), http.MethodGet, "/api/calls/"+w.callID.String()+"/minutes", nil); got.Code != http.StatusNotFound {
		t.Errorf("no minutes: %d", got.Code)
	}
}

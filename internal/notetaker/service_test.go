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
	failed   []uuid.UUID
	mailed   []string
}

func (r *recorded) Notify(_ context.Context, _, userID uuid.UUID, kind, _, _ string, data map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !strings.HasSuffix(data["url"].(string), "/minutes") {
		return nil
	}
	switch kind {
	case "minutes.ready":
		r.notified = append(r.notified, userID)
	case "minutes.failed":
		r.failed = append(r.failed, userID)
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
	// Pointed at another address, the saved keys do not go with it.
	call(t, admin, http.MethodPut, "/api/org/notetaker", map[string]any{
		"enabled": false, "llmProvider": "anthropic", "llmBaseUrl": "https://attacker.example", "sttBaseUrl": "https://attacker.example"})
	set, _ = w.svc.Settings(context.Background(), w.orgID)
	if set.LLMKey != "" || set.STTKey != "" {
		t.Errorf("the saved keys went with the new address: %q %q", set.LLMKey, set.STTKey)
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

// A guest let in by the meeting's link is heard, and named in the minutes,
// though they have no account to receive them.
func TestAGuestIsHeardAndNamedInTheMinutes(t *testing.T) {
	w := newWorld(t)
	srv := standIns(t)
	ctx := context.Background()
	pool := w.svc.db
	guestID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO call_guests (id, call_id, name) VALUES ($1, $2, 'Pat Partner')`, guestID, w.callID); err != nil {
		t.Fatal(err)
	}
	join := w.svc.join
	w.svc.join = func(sess *session) error {
		start := time.Date(2026, 11, 8, 10, 0, 6, 0, time.UTC)
		sess.clips <- Clip{Speaker: Speaker{UserID: "guest-" + guestID.String(), Name: "Pat Partner"}, StartedAt: start, EndedAt: start.Add(time.Second), Audio: []byte("Sounds good to us")}
		return join(sess)
	}
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: srv.URL, LLMKey: "k", LLMBaseURL: srv.URL}, true); err != nil {
		t.Fatal(err)
	}
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	notes := w.waitFor(t, w.router(t, w.ada, "member"))
	if notes.Status != "ready" {
		t.Fatalf("minutes: %+v", notes)
	}
	if got := notes.Minutes.Attendees; len(got) != 3 || got[2] != "Pat Partner (guest)" {
		t.Errorf("attendees: %v", got)
	}
	heard := false
	for _, s := range notes.Transcript {
		heard = heard || (s.Speaker == "Pat Partner" && s.Text == "Sounds good to us")
	}
	if !heard {
		t.Errorf("the guest is in the transcript: %+v", notes.Transcript)
	}
	w.sent.mu.Lock()
	defer w.sent.mu.Unlock()
	if len(w.sent.notified) != 2 {
		t.Errorf("only members are sent the minutes: %d", len(w.sent.notified))
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

// waitFor polls a call's minutes until they are done one way or the other.
func (w *world) waitFor(t *testing.T, as *gin.Engine) Notes {
	t.Helper()
	var notes Notes
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got := call(t, as, http.MethodGet, "/api/calls/"+w.callID.String()+"/minutes", nil)
		_ = json.Unmarshal(got.Body.Bytes(), &notes)
		if notes.Status == "ready" || notes.Status == "failed" || notes.Status == "empty" {
			return notes
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("still %q", notes.Status)
	return notes
}

// What happened in the first real meeting: NVIDIA had retired the model asked
// for, and answered 410. The minutes are written by a Nemotron model it still
// offers, even one that thinks aloud before answering.
func TestARetiredNVIDIAModelStillGetsTheMinutesWritten(t *testing.T) {
	w := newWorld(t)
	stt := standIns(t)
	var asked []string
	var mu sync.Mutex
	nv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(rw).Encode(map[string]any{"data": []map[string]string{{"id": "meta/llama-4"}, {"id": "nvidia/nemotron-3-ultra-550b-a55b"}}})
		case "/v1/chat/completions":
			var body struct {
				Model     string `json:"model"`
				MaxTokens int    `json:"max_tokens"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			asked = append(asked, body.Model)
			mu.Unlock()
			if body.Model != "nvidia/nemotron-3-ultra-550b-a55b" {
				http.Error(rw, `{"status":410,"title":"Gone"}`, http.StatusGone)
				return
			}
			if body.MaxTokens < 4096 {
				http.Error(rw, "too few tokens to think and answer", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(rw).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{
				"content": `<think>They said {ship}. Shape: {"summary": ...}</think>{"summary": "Agreed to ship.", "decisions": ["Ship on Friday"], "actionItems": [], "openQuestions": []}`}}}})
		}
	}))
	defer nv.Close()
	ctx := context.Background()
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: stt.URL,
		LLMProvider: "nvidia", LLMKey: "nvapi-k", LLMModel: "nvidia/llama-3.3-nemotron-super-49b-v1", LLMBaseURL: nv.URL + "/v1"}, true); err != nil {
		t.Fatal(err)
	}
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	notes := w.waitFor(t, w.router(t, w.ada, "member"))
	if notes.Status != "ready" || notes.Minutes.Summary != "Agreed to ship." {
		t.Fatalf("minutes: %+v", notes)
	}
	if len(asked) != 2 || asked[0] != "nvidia/llama-3.3-nemotron-super-49b-v1" {
		t.Errorf("asked the chosen model, then one NVIDIA still offers: %v", asked)
	}

	// And an administrator naming a retired model is told so when saving.
	got := call(t, w.router(t, w.ada, "admin"), http.MethodPut, "/api/org/notetaker", map[string]any{
		"enabled": true, "llmProvider": "nvidia", "llmModel": "nvidia/llama-3.3-nemotron-super-49b-v1", "llmBaseUrl": nv.URL + "/v1"})
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "nemotron-3-ultra-550b-a55b") {
		t.Errorf("a retired model is refused with one that works: %d %s", got.Code, got.Body.String())
	}
}

func TestFailedMinutesAreWrittenAgainFromTheTranscriptAndListed(t *testing.T) {
	quick(t) // the model stays down through every attempt
	w := newWorld(t)
	stt := standIns(t)
	broken := true
	var mu sync.Mutex
	model := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		down := broken
		mu.Unlock()
		if down {
			http.Error(rw, "overloaded", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"content": []map[string]string{{"type": "text",
			"text": `{"summary": "The team agreed to ship on Friday.", "decisions": [], "actionItems": [{"owner": "Ada Archer", "task": "Tag it"}], "openQuestions": []}`}}})
	}))
	defer model.Close()
	ctx := context.Background()
	if err := w.orgs.SetConfig(ctx, w.orgID, ConfigKey, Settings{Enabled: true, STTKey: "k", STTBaseURL: stt.URL, LLMKey: "k", LLMBaseURL: model.URL}, true); err != nil {
		t.Fatal(err)
	}
	w.svc.PersonJoined(ctx, w.orgID, w.callID, "room-1")
	ada := w.router(t, w.ada, "member")
	notes := w.waitFor(t, ada)
	if notes.Status != "failed" || !strings.Contains(notes.Error, "503") || len(notes.Transcript) != 2 {
		t.Fatalf("failed, with why, and the transcript kept: %+v", notes)
	}
	w.sent.mu.Lock()
	failedTold := len(w.sent.failed)
	w.sent.mu.Unlock()
	if failedTold != 2 {
		t.Errorf("both told the minutes failed: %d", failedTold)
	}

	retry := "/api/calls/" + w.callID.String() + "/minutes/retry"
	if got := call(t, w.router(t, w.carol, "member"), http.MethodPost, retry, nil); got.Code != http.StatusNotFound {
		t.Errorf("Carol was not there: %d", got.Code)
	}
	mu.Lock()
	broken = false
	mu.Unlock()
	if got := call(t, ada, http.MethodPost, retry, nil); got.Code != http.StatusAccepted {
		t.Fatalf("retry: %d %s", got.Code, got.Body.String())
	}
	notes = w.waitFor(t, ada)
	if notes.Status != "ready" || notes.Error != "" {
		t.Fatalf("written again: %+v", notes)
	}
	if got := call(t, ada, http.MethodPost, retry, nil); got.Code != http.StatusConflict {
		t.Errorf("ready minutes are not written again: %d", got.Code)
	}

	// The Meeting notes page.
	type page struct {
		Items   []NotesItem `json:"items"`
		HasMore bool        `json:"hasMore"`
	}
	list := func(r *gin.Engine, query string) page {
		var p page
		got := call(t, r, http.MethodGet, "/api/meeting-notes"+query, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("list %s: %d %s", query, got.Code, got.Body.String())
		}
		_ = json.Unmarshal(got.Body.Bytes(), &p)
		return p
	}
	mine := list(ada, "")
	if len(mine.Items) != 1 || mine.Items[0].Title != "Weekly" || mine.Items[0].Status != "ready" ||
		mine.Items[0].ActionItems != 1 || !mine.Items[0].Attended || len(mine.Items[0].Attendees) != 2 {
		t.Errorf("Ada's: %+v", mine)
	}
	if got := list(ada, "?q=friday"); len(got.Items) != 1 {
		t.Errorf("found by what the summary says: %+v", got)
	}
	if got := list(ada, "?q=budget"); len(got.Items) != 0 {
		t.Errorf("not found: %+v", got)
	}
	if got := list(w.router(t, w.carol, "member"), "?scope=all"); len(got.Items) != 0 {
		t.Errorf("a member sees only meetings they were in: %+v", got)
	}
	if got := list(w.router(t, w.carol, "admin"), "?scope=all"); len(got.Items) != 1 || got.Items[0].Attended {
		t.Errorf("an administrator sees every meeting: %+v", got)
	}
	if got := list(w.router(t, w.carol, "admin"), ""); len(got.Items) != 0 {
		t.Errorf("and by default only their own: %+v", got)
	}

	// Another organization's administrator sees none of it.
	other := testsupport.SeedOrg(t, w.svc.db)
	boss := testsupport.SeedUser(t, w.svc.db, other, "boss@other.example", "admin")
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", other.ID.String())
		c.Set("user_id", boss.String())
		c.Set("user_roles", []string{"admin"})
		c.Next()
	})
	RegisterRoutes(api, w.svc)
	if got := list(r, "?scope=all"); len(got.Items) != 0 {
		t.Errorf("another organization: %+v", got)
	}
	if got := call(t, r, http.MethodGet, "/api/calls/"+w.callID.String()+"/minutes", nil); got.Code != http.StatusNotFound {
		t.Errorf("another organization's minutes: %d", got.Code)
	}
}

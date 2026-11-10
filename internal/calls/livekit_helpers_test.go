package calls

// Shared fixture for the attendance (ticket 01), knock (ticket 02) and
// standing meeting (ticket 03) tests. Everything goes through HTTP and through
// signed LiveKit webhooks, the way production reaches the code, so these tests
// do not depend on how the developer names internal functions or tables.
//
// webhookRouter below mounts RegisterWebhook on a group with no session middleware,
// exactly as cmd/teamup does.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/teamup/teamup/internal/notifications"
	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

const (
	lkKey    = "devkey"
	lkSecret = "devsecret-devsecret-devsecret-0123456789"
)

// ── frames and pushes the service sends ───────────────────────────────────

type sentFrame struct {
	To      string
	Type    string
	Payload map[string]any
}

type frameLog struct {
	mu     sync.Mutex
	frames []sentFrame
}

func (f *frameLog) BroadcastToUser(userID string, msg *protocol.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := map[string]any{}
	_ = json.Unmarshal(msg.Payload, &p)
	f.frames = append(f.frames, sentFrame{To: userID, Type: msg.Type, Payload: p})
}

func (f *frameLog) BroadcastToChannel(string, *protocol.Message, string) {}

// knockFrames are the frames about knocking sent to one person, in order.
func (f *frameLog) knockFrames(to uuid.UUID) []sentFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sentFrame{}
	for _, fr := range f.frames {
		if fr.To == to.String() && strings.HasPrefix(fr.Type, "call.knock") {
			out = append(out, fr)
		}
	}
	return out
}

func (f *frameLog) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = nil
}

// pushLog wraps the real notification service, so that however the developer
// chooses to notify (a push, or a bell entry), both are visible to the test.
type pushLog struct {
	*notifications.Service
	mu     sync.Mutex
	pushed map[uuid.UUID]int
}

func (p *pushLog) PushToUsers(ctx context.Context, ids []uuid.UUID, push protocol.Push) {
	p.mu.Lock()
	if p.pushed == nil {
		p.pushed = map[uuid.UUID]int{}
	}
	for _, id := range ids {
		p.pushed[id]++
	}
	p.mu.Unlock()
	p.Service.PushToUsers(ctx, ids, push)
}

// ── environment ───────────────────────────────────────────────────────────

type lkEnv struct {
	svc    *Service
	pool   *pgxpool.Pool
	org    *testsupport.Org
	frames *frameLog
	push   *pushLog
}

func newLK(t *testing.T) *lkEnv {
	t.Helper()
	pool := testsupport.DB(t)
	svc := NewService(pool, Config{LiveKitAPIKey: lkKey, LiveKitAPISecret: lkSecret})
	t.Cleanup(svc.Close)
	e := &lkEnv{svc: svc, pool: pool, org: testsupport.SeedOrg(t, pool), frames: &frameLog{}}
	e.push = &pushLog{Service: notifications.NewService(pool, nil, notifications.Config{})}
	svc.SetBroadcaster(e.frames)
	svc.SetNotifier(e.push)
	return e
}

// person seeds a named member of the organization with the given role.
func (e *lkEnv) person(t *testing.T, name, role string) uuid.UUID {
	t.Helper()
	id := testsupport.SeedUser(t, e.pool, e.org, strings.ToLower(strings.ReplaceAll(name, " ", "."))+"@example.test", role)
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET name = $1 WHERE id = $2`, name, id); err != nil {
		t.Fatalf("name %s: %v", name, err)
	}
	return id
}

// as is the signed-in router for one person of this organization.
func (e *lkEnv) as(t *testing.T, user uuid.UUID, roles ...string) *gin.Engine {
	t.Helper()
	return routerFor(t, e.svc, e.org.ID, user, roles...)
}

func (e *lkEnv) asOwner(t *testing.T) *gin.Engine {
	return e.as(t, e.org.OwnerID, "owner")
}

func routerFor(t *testing.T, svc *Service, org, user uuid.UUID, roles ...string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", org.String())
		c.Set("user_id", user.String())
		c.Set("user_roles", roles)
		c.Next()
	})
	RegisterRoutes(api, svc)
	return r
}

// webhookRouter has NO session middleware: LiveKit is not a signed-in user.
func (e *lkEnv) webhookRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	RegisterWebhook(api, e.svc)
	return r
}

// call makes a private call (no channel) by creator with the invitees.
func (e *lkEnv) call(t *testing.T, creator uuid.UUID, invitees ...uuid.UUID) *Call {
	t.Helper()
	ids := make([]string, 0, len(invitees))
	for _, i := range invitees {
		ids = append(ids, i.String())
	}
	call, err := e.svc.CreateCall(context.Background(), e.org.ID, creator, nil, "video", nil, ids)
	if err != nil {
		t.Fatalf("create call: %v", err)
	}
	return call
}

// ── plain HTTP helpers ────────────────────────────────────────────────────

func doJSON(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func wantCode(t *testing.T, w *httptest.ResponseRecorder, codes ...int) {
	t.Helper()
	for _, c := range codes {
		if w.Code == c {
			return
		}
	}
	t.Fatalf("want one of %v, got %d: %s", codes, w.Code, w.Body.String())
}

func is2xx(w *httptest.ResponseRecorder) bool { return w.Code >= 200 && w.Code < 300 }

// inner finds a value that is either at the top of an object or under key.
func inner(body []byte, key string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) == nil {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return body
}

// ── signed webhooks ───────────────────────────────────────────────────────

func sign(t *testing.T, key, secret string, body []byte) string {
	t.Helper()
	sum := sha256.Sum256(body)
	tok, err := auth.NewAccessToken(key, secret).
		SetValidFor(5 * time.Minute).
		SetSha256(base64.StdEncoding.EncodeToString(sum[:])).
		ToJWT()
	if err != nil {
		t.Fatalf("sign webhook: %v", err)
	}
	return tok
}

func rawWebhook(t *testing.T, r *gin.Engine, body []byte, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/livekit/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/webhook+json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// deliver sends one event the way LiveKit does: signed with the server's key.
func (e *lkEnv) deliver(t *testing.T, ev *livekit.WebhookEvent) *httptest.ResponseRecorder {
	t.Helper()
	body := marshalEvent(t, ev)
	return rawWebhook(t, e.webhookRouter(t), body, sign(t, lkKey, lkSecret, body))
}

func marshalEvent(t *testing.T, ev *livekit.WebhookEvent) []byte {
	t.Helper()
	body, err := protojson.Marshal(ev)
	if err != nil {
		t.Errorf("marshal webhook: %v", err)
	}
	return body
}

func (e *lkEnv) mustDeliver(t *testing.T, ev *livekit.WebhookEvent) {
	t.Helper()
	w := e.deliver(t, ev)
	if !is2xx(w) {
		t.Fatalf("webhook %s should be accepted, got %d: %s", ev.Event, w.Code, w.Body.String())
	}
}

func lkRoom(name string) *livekit.Room { return &livekit.Room{Name: name, Sid: "RM_" + name} }

func joinedEv(room string, user uuid.UUID, sid string, at time.Time) *livekit.WebhookEvent {
	return &livekit.WebhookEvent{
		Event: "participant_joined", Id: "EV_" + uuid.NewString(), CreatedAt: at.Unix(),
		Room:        lkRoom(room),
		Participant: &livekit.ParticipantInfo{Identity: user.String(), Sid: sid, JoinedAt: at.Unix()},
	}
}

func leftEv(room string, user uuid.UUID, sid string, at time.Time) *livekit.WebhookEvent {
	return &livekit.WebhookEvent{
		Event: "participant_left", Id: "EV_" + uuid.NewString(), CreatedAt: at.Unix(),
		Room:        lkRoom(room),
		Participant: &livekit.ParticipantInfo{Identity: user.String(), Sid: sid},
	}
}

func trackEv(event, room string, user uuid.UUID, sid string, source livekit.TrackSource, at time.Time) *livekit.WebhookEvent {
	return &livekit.WebhookEvent{
		Event: event, Id: "EV_" + uuid.NewString(), CreatedAt: at.Unix(),
		Room:        lkRoom(room),
		Participant: &livekit.ParticipantInfo{Identity: user.String(), Sid: sid},
		Track:       &livekit.TrackInfo{Sid: "TR_" + uuid.NewString()[:8], Type: livekit.TrackType_VIDEO, Source: source},
	}
}

func roomFinishedEv(room string, at time.Time) *livekit.WebhookEvent {
	return &livekit.WebhookEvent{Event: "room_finished", Id: "EV_" + uuid.NewString(), CreatedAt: at.Unix(), Room: lkRoom(room)}
}

// ── reading the log ───────────────────────────────────────────────────────

type logEvent struct {
	ID      string    `json:"id"`
	Type    string    `json:"type"`
	UserID  string    `json:"userId"`
	ActorID string    `json:"actorId"`
	At      time.Time `json:"at"`
}

type logSession struct {
	UserID   string     `json:"userId"`
	JoinedAt time.Time  `json:"joinedAt"`
	LeftAt   *time.Time `json:"leftAt"`
}

type callLog struct {
	Events   []logEvent   `json:"events"`
	Sessions []logSession `json:"sessions"`
}

func (l callLog) events(kind string, user uuid.UUID) []logEvent {
	out := []logEvent{}
	for _, e := range l.Events {
		if e.Type == kind && e.UserID == user.String() {
			out = append(out, e)
		}
	}
	return out
}

func (l callLog) sessions(user uuid.UUID) []logSession {
	out := []logSession{}
	for _, s := range l.Sessions {
		if s.UserID == user.String() {
			out = append(out, s)
		}
	}
	return out
}

func getLog(t *testing.T, r *gin.Engine, callID uuid.UUID) callLog {
	t.Helper()
	w := doJSON(t, r, http.MethodGet, "/api/calls/"+callID.String()+"/log", "")
	wantCode(t, w, http.StatusOK)
	var l callLog
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatalf("decode log %q: %v", w.Body.String(), err)
	}
	return l
}

func near(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= 2*time.Second
}

// ageCall moves everything recorded against a call into the past by d, in
// every table that has a call_id column, so a test can wait out a minute
// without sleeping. Deliberately schema-agnostic: it needs only the column
// name call_id, and shifts every timestamptz column it finds.
func ageCall(t *testing.T, pool *pgxpool.Pool, callID uuid.UUID, d time.Duration) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT c.table_name, c.column_name FROM information_schema.columns c
		JOIN information_schema.columns k ON k.table_name = c.table_name AND k.table_schema = c.table_schema
		JOIN information_schema.tables tb ON tb.table_name = c.table_name AND tb.table_schema = c.table_schema AND tb.table_type = 'BASE TABLE'
		WHERE c.table_schema = 'public' AND c.data_type = 'timestamp with time zone'
		  AND k.column_name = 'call_id'
		  AND c.table_name NOT IN ('calls', 'call_participants')`)
	if err != nil {
		t.Fatalf("find call tables: %v", err)
	}
	type col struct{ table, column string }
	var cols []col
	for rows.Next() {
		var c col
		if err := rows.Scan(&c.table, &c.column); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	rows.Close()
	if len(cols) == 0 {
		t.Fatal("no table with a call_id column and a timestamp exists yet (expected the knock requests and call_events tables)")
	}
	byTable := map[string][]string{}
	for _, c := range cols {
		byTable[c.table] = append(byTable[c.table], `"`+c.column+`" = "`+c.column+`" - ($2::float8 * interval '1 second')`)
	}
	for table, sets := range byTable {
		q := `UPDATE "` + table + `" SET ` + strings.Join(sets, ", ") + ` WHERE call_id = $1`
		if _, err := pool.Exec(ctx, q, callID, d.Seconds()); err != nil {
			t.Fatalf("age %s: %v", table, err)
		}
	}
}

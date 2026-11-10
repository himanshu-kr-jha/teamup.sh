package calendar

// Ticket 04. Everything goes over HTTP and SQL, the way production reaches the
// code; buildRouter and sweepReminders are the only places these tests touch the
// package's structure.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/notifications"
	"github.com/teamup/teamup/internal/testsupport"
)

func buildRouter(t *testing.T, pool *pgxpool.Pool, notifier *notifications.Service, org, user uuid.UUID, roles []string) *gin.Engine {
	t.Helper()
	svc := NewService(pool)
	svc.SetNotifier(notifier)
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

// sweepReminders is one pass of the job as a replica of its own would run it. Safe
// to call from a goroutine: a failure is reported without stopping the test there.
func sweepReminders(t *testing.T, pool *pgxpool.Pool, notifier *notifications.Service, now time.Time) int {
	t.Helper()
	svc := NewService(pool)
	svc.SetNotifier(notifier)
	sent, err := svc.SweepReminders(context.Background(), now)
	if err != nil {
		t.Errorf("sweep reminders: %v", err)
	}
	return sent
}

// ── fixture ───────────────────────────────────────────────────────────────

type env struct {
	pool     *pgxpool.Pool
	org      *testsupport.Org
	notifier *notifications.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testsupport.DB(t)
	return &env{pool: pool, org: testsupport.SeedOrg(t, pool),
		notifier: notifications.NewService(pool, nil, notifications.Config{})}
}

func (e *env) person(t *testing.T, name, role string) uuid.UUID {
	t.Helper()
	id := testsupport.SeedUser(t, e.pool, e.org, strings.ToLower(strings.ReplaceAll(name, " ", "."))+"@example.test", role)
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET name = $1 WHERE id = $2`, name, id); err != nil {
		t.Fatalf("name %s: %v", name, err)
	}
	return id
}

func (e *env) as(t *testing.T, user uuid.UUID, role string) *gin.Engine {
	t.Helper()
	return buildRouter(t, e.pool, e.notifier, e.org.ID, user, []string{role})
}

func (e *env) asOwner(t *testing.T) *gin.Engine { return e.as(t, e.org.OwnerID, "owner") }

func call(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func want(t *testing.T, w *httptest.ResponseRecorder, codes ...int) {
	t.Helper()
	for _, c := range codes {
		if w.Code == c {
			return
		}
	}
	t.Fatalf("want one of %v, got %d: %s", codes, w.Code, w.Body.String())
}

func is2xx(w *httptest.ResponseRecorder) bool { return w.Code >= 200 && w.Code < 300 }

// ── time ──────────────────────────────────────────────────────────────────

// day0 is midnight UTC three days ahead, so everything is in the future and
// nothing depends on what time the suite runs.
var day0 = time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 3)

func at(hour, minute int) time.Time {
	return day0.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func window() string {
	return "from=" + at(0, 0).Format(time.RFC3339) + "&to=" + at(23, 59).Format(time.RFC3339)
}

func near(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= 2*time.Second
}

// ── wire shapes (assumptions, see ticket 01 comments) ─────────────────────

type item struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`   // busy | focus | open
	Source   string    `json:"source"` // block | meeting | call | standing (only with authority)
	Title    string    `json:"title"`
	Details  string    `json:"details"`
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`
}

func (e *env) calendar(t *testing.T, r *gin.Engine, who uuid.UUID) (*httptest.ResponseRecorder, []item) {
	t.Helper()
	w := call(t, r, http.MethodGet, "/api/calendar?userId="+who.String()+"&"+window(), nil)
	var body struct {
		Items []item `json:"items"`
	}
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode calendar %q: %v", w.Body.String(), err)
		}
	}
	return w, body.Items
}

func (e *env) mustCalendar(t *testing.T, r *gin.Engine, who uuid.UUID) []item {
	t.Helper()
	w, items := e.calendar(t, r, who)
	want(t, w, http.StatusOK)
	return items
}

func find(items []item, title string) (item, bool) {
	for _, i := range items {
		if i.Title == title {
			return i, true
		}
	}
	return item{}, false
}

type block struct {
	ID string `json:"id"`
}

func (e *env) block(t *testing.T, as uuid.UUID, role, kind, title string, from, to time.Time) block {
	t.Helper()
	w := call(t, e.as(t, as, role), http.MethodPost, "/api/calendar/blocks", map[string]any{
		"kind": kind, "title": title, "startsAt": from.Format(time.RFC3339), "endsAt": to.Format(time.RFC3339)})
	want(t, w, http.StatusCreated)
	var b block
	_ = json.Unmarshal(inner(w.Body.Bytes(), "block"), &b)
	if b.ID == "" {
		t.Fatalf("a created block has an id: %s", w.Body.String())
	}
	return b
}

type conflict struct {
	UserID string `json:"userId"`
	Kind   string `json:"kind"`
}

type meeting struct {
	ID        string     `json:"id"`
	Conflicts []conflict `json:"conflicts"`
}

type meetingResult struct {
	code      int
	meeting   meeting
	conflicts []conflict
	raw       string
}

func meetingBody(title string, from, to time.Time, invitees ...uuid.UUID) map[string]any {
	ids := make([]string, 0, len(invitees))
	for _, i := range invitees {
		ids = append(ids, i.String())
	}
	return map[string]any{"title": title, "startsAt": from.Format(time.RFC3339), "endsAt": to.Format(time.RFC3339), "inviteeIds": ids}
}

func decodeMeeting(w *httptest.ResponseRecorder) meetingResult {
	res := meetingResult{code: w.Code, raw: w.Body.String()}
	if is2xx(w) {
		_ = json.Unmarshal(inner(w.Body.Bytes(), "meeting"), &res.meeting)
		var top struct {
			Conflicts []conflict `json:"conflicts"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &top)
		res.conflicts = top.Conflicts
	}
	return res
}

func (e *env) schedule(t *testing.T, as uuid.UUID, role, title string, from, to time.Time, invitees ...uuid.UUID) meetingResult {
	t.Helper()
	return decodeMeeting(call(t, e.as(t, as, role), http.MethodPost, "/api/meetings", meetingBody(title, from, to, invitees...)))
}

func (e *env) mustSchedule(t *testing.T, as uuid.UUID, role, title string, from, to time.Time, invitees ...uuid.UUID) meetingResult {
	t.Helper()
	res := e.schedule(t, as, role, title, from, to, invitees...)
	if res.code != http.StatusCreated || res.meeting.ID == "" {
		t.Fatalf("scheduling should succeed with 201, got %d: %s", res.code, res.raw)
	}
	return res
}

func inner(body []byte, key string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) == nil {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return body
}

// notificationTypes lists the types of every notification a person has.
func (e *env) notificationTypes(t *testing.T, who uuid.UUID) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT type FROM notifications WHERE user_id = $1 ORDER BY created_at`, who)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func countContaining(types []string, sub string) int {
	n := 0
	for _, s := range types {
		if strings.Contains(strings.ToLower(s), sub) {
			n++
		}
	}
	return n
}

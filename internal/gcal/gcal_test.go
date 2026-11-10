package gcal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/calendar"
	"github.com/teamup/teamup/internal/testsupport"
)

// fakeGoogle is Google as far as TeamUp can tell: a consent page's token
// endpoint, one person's primary calendar, and the revoke endpoint.
type fakeGoogle struct {
	mu        sync.Mutex
	server    *httptest.Server
	events    map[string]map[string]any
	next      int
	refreshOK bool
	revoked   []string
	writes    []string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	g := &fakeGoogle{events: map[string]map[string]any{}, refreshOK: true}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

func idToken(email string) string {
	part := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	return part(map[string]string{"alg": "none"}) + "." + part(map[string]string{"email": email}) + ".sig"
}

func (g *fakeGoogle) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.URL.Path == "/token":
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") == "refresh_token" && !g.refreshOK {
			reply(http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		reply(http.StatusOK, map[string]any{
			"access_token": "access-" + uuid.NewString(), "token_type": "Bearer", "expires_in": 1,
			"refresh_token": "refresh-secret-value", "id_token": idToken("alice@gmail.example"),
		})
	case r.URL.Path == "/revoke":
		form, _ := url.ParseQuery(string(body))
		g.revoked = append(g.revoked, form.Get("token"))
		w.WriteHeader(http.StatusOK)
	case r.URL.Path == "/calendar/v3/calendars/primary/events" && r.Method == http.MethodGet:
		items := []map[string]any{}
		for _, e := range g.events {
			items = append(items, e)
		}
		reply(http.StatusOK, map[string]any{"items": items})
	case r.URL.Path == "/calendar/v3/calendars/primary/events" && r.Method == http.MethodPost:
		var e map[string]any
		_ = json.Unmarshal(body, &e)
		g.next++
		id := "made" + string(rune('a'+g.next))
		e["id"] = id
		g.events[id] = e
		g.writes = append(g.writes, "POST "+id)
		reply(http.StatusOK, e)
	case strings.HasPrefix(r.URL.Path, "/calendar/v3/calendars/primary/events/"):
		id := strings.TrimPrefix(r.URL.Path, "/calendar/v3/calendars/primary/events/")
		existing, ok := g.events[id]
		if !ok {
			reply(http.StatusNotFound, map[string]any{})
			return
		}
		switch r.Method {
		case http.MethodPatch:
			var patch map[string]any
			_ = json.Unmarshal(body, &patch)
			for k, v := range patch {
				existing[k] = v
			}
			g.writes = append(g.writes, "PATCH "+id)
			reply(http.StatusOK, existing)
		case http.MethodDelete:
			delete(g.events, id)
			g.writes = append(g.writes, "DELETE "+id)
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		http.NotFound(w, r)
	}
}

func (g *fakeGoogle) put(id string, e map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e["id"] = id
	g.events[id] = e
}

// ours finds the event TeamUp put there for a meeting.
func (g *fakeGoogle) ours(callID uuid.UUID) map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.events {
		if props, ok := e["extendedProperties"].(map[string]any); ok {
			if private, ok := props["private"].(map[string]any); ok && private["teamupCallId"] == callID.String() {
				return e
			}
		}
	}
	return nil
}

func timed(start, end time.Time) map[string]any {
	return map[string]any{"start": map[string]any{"dateTime": start.Format(time.RFC3339)}, "end": map[string]any{"dateTime": end.Format(time.RFC3339)}}
}

func with(base map[string]any, extra map[string]any) map[string]any {
	for k, v := range extra {
		base[k] = v
	}
	return base
}

func connect(t *testing.T, s *Service, orgID, userID uuid.UUID) {
	t.Helper()
	address, nonce, err := s.ConnectURL(orgID, userID)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(address)
	if u.Query().Get("access_type") != "offline" || !strings.Contains(u.Query().Get("scope"), "calendar.events") {
		t.Fatalf("Google is asked for lasting access to events: %s", address)
	}
	if _, err := s.Complete(context.Background(), u.Query().Get("state"), "somebody else's browser", "code"); err == nil {
		t.Fatal("the way back is refused in a browser that did not start it")
	}
	if _, err := s.Complete(context.Background(), u.Query().Get("state"), nonce, "code"); err != nil {
		t.Fatal(err)
	}
}

func TestGoogleCalendarBothWays(t *testing.T) {
	pool := testsupport.DB(t)
	org := testsupport.SeedOrg(t, pool)
	alice := testsupport.SeedUser(t, pool, org, "alice@example.test", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob@example.test", "member")
	g := newFakeGoogle(t)
	ctx := context.Background()

	s, err := NewService(pool, Config{ClientID: "id", ClientSecret: "secret", TokenKey: "a-token-key-long-enough", BaseURL: "https://teamup.example", Endpoint: g.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	cal := calendar.NewService(pool)
	cal.OnMeetingsChanged(func(people []uuid.UUID) { s.Nudge(people...) })

	connect(t, s, org.ID, alice)
	st, _ := s.Status(ctx, alice)
	if !st.Connected || st.Email != "alice@gmail.example" {
		t.Fatalf("connected as her Google account: %+v", st)
	}
	var sealed []byte
	_ = pool.QueryRow(ctx, `SELECT token FROM google_calendar_links WHERE user_id = $1`, alice).Scan(&sealed)
	if bytes.Contains(sealed, []byte("refresh-secret-value")) {
		t.Error("the stored token is sealed, not readable from the database")
	}

	// Her Google Calendar, as it is.
	day := time.Now().Add(48 * time.Hour).Truncate(time.Hour)
	g.put("client", with(timed(day, day.Add(time.Hour)), map[string]any{"summary": "Client call", "htmlLink": "https://calendar.google.com/x"}))
	g.put("secret", with(timed(day.Add(2*time.Hour), day.Add(3*time.Hour)), map[string]any{"summary": "Doctor", "visibility": "private"}))
	g.put("holiday", map[string]any{"summary": "Holiday", "start": map[string]any{"date": day.Format("2006-01-02")}, "end": map[string]any{"date": day.AddDate(0, 0, 1).Format("2006-01-02")}})
	g.put("free", with(timed(day.Add(4*time.Hour), day.Add(5*time.Hour)), map[string]any{"summary": "Lunch", "transparency": "transparent"}))
	g.put("declined", with(timed(day.Add(5*time.Hour), day.Add(6*time.Hour)), map[string]any{"summary": "Optional", "attendees": []any{map[string]any{"self": true, "responseStatus": "declined"}}}))

	// A TeamUp meeting she is invited to.
	m, _, err := cal.ScheduleMeeting(ctx, org.ID, bob, "Planning", day.Add(6*time.Hour), day.Add(7*time.Hour), []uuid.UUID{alice})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(ctx, alice); err != nil {
		t.Fatal(err)
	}

	pushed := g.ours(m.ID)
	if pushed == nil || pushed["summary"] != "Planning" {
		t.Fatalf("the TeamUp meeting is in her Google Calendar: %+v", g.events)
	}
	if !strings.Contains(pushed["description"].(string), "https://teamup.example/calls/"+m.ID.String()) {
		t.Errorf("it says where to join: %v", pushed["description"])
	}

	items, err := cal.Calendar(ctx, org.ID, alice, alice, false, day.Add(-time.Hour), day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]calendar.Item{}
	googleCount := 0
	for _, it := range items {
		if it.Source == "google" {
			googleCount++
			titles[it.Title] = it
		}
	}
	if googleCount != 2 || titles["Client call"].Link == "" || titles["Doctor"].Kind != "busy" {
		t.Errorf("only her timed, busy, accepted Google meetings come in, and not TeamUp's own back again: %+v", items)
	}

	// Moved in TeamUp, moved in Google; cancelled, gone.
	if _, err := pool.Exec(ctx, `UPDATE calls SET scheduled_start = scheduled_start + interval '1 hour', scheduled_end = scheduled_end + interval '1 hour' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(ctx, alice); err != nil {
		t.Fatal(err)
	}
	moved := g.ours(m.ID)
	if moved["start"].(map[string]any)["dateTime"] != day.Add(7*time.Hour).UTC().Format(time.RFC3339) {
		t.Errorf("the move followed: %+v", moved)
	}
	before := len(g.writes)
	if err := s.Sync(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if len(g.writes) != before {
		t.Errorf("nothing changed, nothing is sent: %v", g.writes[before:])
	}
	if _, err := pool.Exec(ctx, `UPDATE calls SET status = 'cancelled' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if g.ours(m.ID) != nil {
		t.Error("a cancelled meeting comes out of her Google Calendar")
	}

	// Her manager reads her calendar, but not what Google says is private.
	var ownerID = org.OwnerID
	asOwner, err := cal.Calendar(ctx, org.ID, ownerID, alice, true, day.Add(-time.Hour), day.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range asOwner {
		if it.Title == "Doctor" || it.Link != "" {
			t.Errorf("somebody else sees no private title and no link into her Google account: %+v", it)
		}
	}

	// Google stops accepting the token: she is asked to connect again.
	g.mu.Lock()
	g.refreshOK = false
	g.mu.Unlock()
	time.Sleep(1100 * time.Millisecond) // the access token has expired
	_ = s.Sync(ctx, alice)
	if st, _ := s.Status(ctx, alice); !st.NeedsConsent {
		t.Errorf("she is told to connect again: %+v", st)
	}

	// Disconnecting forgets everything.
	g.mu.Lock()
	g.refreshOK = true
	g.mu.Unlock()
	connect(t, s, org.ID, alice)
	if err := s.Disconnect(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if len(g.revoked) == 0 {
		t.Error("Google is told to forget the token")
	}
	var left int
	_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM google_calendar_links WHERE user_id = $1) + (SELECT count(*) FROM google_calendar_events WHERE user_id = $1)`, alice).Scan(&left)
	if left != 0 {
		t.Errorf("nothing of her Google Calendar is kept, %d rows left", left)
	}
}

func TestWithoutSettingsGoogleCalendarIsSimplyUnavailable(t *testing.T) {
	s, err := NewService(nil, Config{ClientID: "id"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Enabled() {
		t.Error("no secret and no key: not available")
	}
	if _, _, err := s.ConnectURL(uuid.New(), uuid.New()); err == nil {
		t.Error("connecting is refused with a reason")
	}
	s.Nudge(uuid.New()) // does nothing, and does not block
}

package calendar

// Tickets 20, 22, 23 and 24: a meeting that opened early and emptied, what the
// calendar says about a standing meeting, calls nobody closed, and notifications
// and edits on meetings. Everything goes over HTTP as the other tests here do.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// asAt is the calendar as somebody reads it at a moment of the test's choosing.
func (e *env) asAt(t *testing.T, user uuid.UUID, role string, now time.Time) *gin.Engine {
	t.Helper()
	svc := NewService(e.pool)
	svc.SetNotifier(e.notifier)
	svc.SetClock(func() time.Time { return now })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", e.org.ID.String())
		c.Set("user_id", user.String())
		c.Set("user_roles", []string{role})
		c.Next()
	})
	RegisterRoutes(api, svc)
	return r
}

func (e *env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// ── ticket 20 ─────────────────────────────────────────────────────────────

// openEarlyAndEmpty is the organizer pressing Join at 09:40, alone, and leaving at
// 09:42, on a 10:00 meeting.
func (w *callWorld) openEarlyAndEmpty(t *testing.T, organizer uuid.UUID, id string) {
	t.Helper()
	callID := uuid.MustParse(id)
	want(t, call(t, w.callsAs(t, organizer, "member"), http.MethodPost, "/api/calls/"+id+"/token", nil), http.StatusOK)
	if err := w.svc.MarkCallEnded(context.Background(), w.org.ID, callID); err != nil {
		t.Fatal(err)
	}
	w.exec(t, `UPDATE call_participants SET joined_at = $3, left_at = $4 WHERE call_id = $1 AND user_id = $2`,
		callID, organizer, at(9, 40), at(9, 42))
}

func TestAMeetingThatOpenedEarlyAndEmptiedIsStillRemindedOnce(t *testing.T) {
	w := newCallWorld(t)
	organizer := w.person(t, "Organizer Orla", "member")
	bob := w.person(t, "Bob Builder", "member")
	id := w.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID
	w.openEarlyAndEmpty(t, organizer, id)

	if sent := sweepReminders(t, w.pool, w.notifier, at(9, 56)); sent != 2 {
		t.Errorf("Bob and the organizer still have to be there at 10:00, reminded %d", sent)
	}
	if got := countContaining(w.notificationTypes(t, bob), "reminder"); got != 1 {
		t.Errorf("Bob is reminded once, got %d", got)
	}
	if sent := sweepReminders(t, w.pool, w.notifier, at(9, 57)); sent != 0 {
		t.Errorf("once, got %d more", sent)
	}
}

func TestAMeetingThatEmptiedEarlyHoldsItsSlotUntilItsEnd(t *testing.T) {
	w := newCallWorld(t)
	organizer := w.person(t, "Organizer Orla", "member")
	bob := w.person(t, "Bob Builder", "member")
	id := w.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID
	w.openEarlyAndEmpty(t, organizer, id)

	during := w.mustCalendar(t, w.asAt(t, organizer, "member", at(9, 50)), organizer)
	got, ok := find(during, "Planning")
	if !ok || !near(got.StartsAt, at(10, 0)) || !near(got.EndsAt, at(11, 0)) {
		t.Errorf("still 10:00 to 11:00 for somebody who looked in early, got %+v (%v)", got, ok)
	}
	// Availability reads the same items.
	body := call(t, w.asAt(t, bob, "member", at(9, 50)), http.MethodGet,
		"/api/calendar/availability?userIds="+organizer.String()+"&from="+at(10, 0).Format(time.RFC3339)+"&to="+at(11, 0).Format(time.RFC3339), nil)
	want(t, body, http.StatusOK)
	if !strings.Contains(body.Body.String(), `"busy"`) {
		t.Errorf("the organizer is still taken at 10:00: %s", body.Body.String())
	}

	// Once its time is over it shows what really happened.
	after := w.mustCalendar(t, w.asAt(t, organizer, "member", at(11, 5)), organizer)
	got, ok = find(after, "Planning")
	if !ok || !near(got.StartsAt, at(9, 40)) || !near(got.EndsAt, at(9, 42)) {
		t.Errorf("afterwards it shows the real time, got %+v (%v)", got, ok)
	}
}

// ── ticket 22 ─────────────────────────────────────────────────────────────

func TestAnAttendedStandingMeetingOnYourCalendarNamesNobodyElse(t *testing.T) {
	e := newEnv(t)
	ann := e.person(t, "Ann Archer", "member")
	bob := e.person(t, "Bob Builder", "member")
	meetingID, callID := uuid.New(), uuid.New()
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	e.exec(t, `INSERT INTO standing_meetings (id, org_id, name, time_of_day, time_zone, weekdays, duration_minutes, created_by)
		VALUES ($1, $2, 'Morning scrum', '09:00', 'UTC', '{1,2,3,4,5,6,7}', 30, $3)`, meetingID, e.org.ID, e.org.OwnerID)
	e.exec(t, `INSERT INTO calls (id, org_id, room_name, title, call_type, status, created_by, started_at,
		scheduled_start, scheduled_end, standing_meeting_id, occurrence_date)
		VALUES ($1, $2, 'standing-x', 'Morning scrum', 'video', 'active', $3, $4, $4, $5, $6, $7::date)`,
		callID, e.org.ID, e.org.OwnerID, start, start.Add(30*time.Minute), meetingID, start.Format("2006-01-02"))
	for _, who := range []uuid.UUID{ann, bob} {
		e.exec(t, `INSERT INTO call_participants (call_id, user_id, role, joined_at) VALUES ($1, $2, 'participant', $3)`, callID, who, start)
	}

	w := call(t, e.as(t, ann, "member"), http.MethodGet, "/api/calendar?userId="+ann.String()+
		"&from="+start.Add(-time.Hour).Format(time.RFC3339)+"&to="+time.Now().UTC().Add(time.Hour).Format(time.RFC3339), nil)
	want(t, w, http.StatusOK)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range body.Items {
		if it["callId"] != callID.String() {
			continue
		}
		found = true
		if _, named := it["inviteeIds"]; named {
			t.Errorf("who else came is attendance's rule, got %v", it["inviteeIds"])
		}
		if _, named := it["organizerId"]; named {
			t.Errorf("an occurrence's creator is not an organizer, got %v", it["organizerId"])
		}
		// Ticket 31: and she cannot read its log, so she must not be offered it.
		if it["canReadLog"] != false {
			t.Errorf("Ann may not read an occurrence's log, got %v", it["canReadLog"])
		}
	}
	if !found {
		t.Fatalf("Ann came, so it is on her calendar: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), bob.String()) {
		t.Errorf("Bob is named somewhere: %s", w.Body.String())
	}
}

func TestAMeetingOnYourCalendarOffersItsLog(t *testing.T) {
	e := newEnv(t)
	ann := e.person(t, "Ann Archer", "member")
	e.mustSchedule(t, ann, "member", "Planning", at(10, 0), at(11, 0))
	w := call(t, e.as(t, ann, "member"), http.MethodGet, "/api/calendar?userId="+ann.String()+"&"+window(), nil)
	want(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"canReadLog":true`) {
		t.Errorf("somebody on a meeting reads its log: %s", w.Body.String())
	}
}

// ── ticket 23 (a) ─────────────────────────────────────────────────────────

func TestACallNobodyClosedDoesNotCoverTheCalendarOfSomebodyWhoNeverJoined(t *testing.T) {
	e := newEnv(t)
	host := e.person(t, "Host Hana", "member")
	rung := e.person(t, "Rung Rae", "member")
	id := uuid.New()
	started := time.Now().Add(-40 * 24 * time.Hour)
	e.exec(t, `INSERT INTO calls (id, org_id, room_name, call_type, status, created_by, started_at)
		VALUES ($1, $2, 'call-stale', 'video', 'active', $3, $4)`, id, e.org.ID, host, started)
	e.exec(t, `INSERT INTO call_participants (call_id, user_id, role) VALUES ($1, $2, 'participant')`, id, rung)

	w := call(t, e.as(t, host, "member"), http.MethodGet, "/api/calendar/availability?userIds="+rung.String()+
		"&from="+time.Now().UTC().Add(-48*time.Hour).Format(time.RFC3339)+"&to="+time.Now().UTC().Format(time.RFC3339), nil)
	want(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), `"busy"`) {
		t.Errorf("somebody rung once and never there is not busy for 40 days: %s", w.Body.String())
	}
}

// ── ticket 24 ─────────────────────────────────────────────────────────────

func TestALongMeetingTitleStillReachesEverybody(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	title := strings.Repeat("é", 100) + strings.Repeat("x", 200)
	id := e.mustSchedule(t, organizer, "member", title, at(10, 0), at(11, 0), bob).meeting.ID

	if got := countContaining(e.notificationTypes(t, bob), "invited"); got != 1 {
		t.Fatalf("Bob has to be told, got %v", e.notificationTypes(t, bob))
	}
	sweepReminders(t, e.pool, e.notifier, at(9, 56))
	if got := countContaining(e.notificationTypes(t, bob), "reminder"); got != 1 {
		t.Errorf("and reminded, got %v", e.notificationTypes(t, bob))
	}
	moved := map[string]any{"startsAt": at(12, 0).Format(time.RFC3339), "endsAt": at(13, 0).Format(time.RFC3339)}
	want(t, call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+id, moved), http.StatusOK)
	if got := countContaining(e.notificationTypes(t, bob), "moved"); got != 1 {
		t.Errorf("and told it moved, got %v", e.notificationTypes(t, bob))
	}
}

func TestAMeetingWithSomebodyDeactivatedOnItCanStillBeMoved(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	gone := e.person(t, "Gone Gus", "member")
	stay := e.person(t, "Stay Stan", "member")
	never := e.person(t, "Never Ned", "member")
	id := e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), gone, stay).meeting.ID
	e.exec(t, `UPDATE users SET is_active = false, deleted_at = NOW() WHERE id = ANY($1)`, []uuid.UUID{gone, never})

	// What the dialog sends: new times, and everybody it was showing.
	res := decodeMeeting(call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+id,
		meetingBody("Planning", at(12, 0), at(13, 0), gone, stay)))
	if res.code != http.StatusOK {
		t.Fatalf("moving it must work, got %d: %s", res.code, res.raw)
	}
	if got := countContaining(e.notificationTypes(t, stay), "moved"); got != 1 {
		t.Errorf("Stan is told, got %v", e.notificationTypes(t, stay))
	}
	if n := len(e.notificationTypes(t, gone)); n != 1 { // the original invitation, nothing since
		t.Errorf("nobody tells a person who has left, got %v", e.notificationTypes(t, gone))
	}
	if strings.Contains(res.raw, gone.String()) {
		t.Errorf("somebody who has left drops off the list: %s", res.raw)
	}

	// Somebody new who is not active is still refused.
	bad := call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+id,
		meetingBody("Planning", at(12, 0), at(13, 0), stay, never))
	want(t, bad, http.StatusBadRequest)
}

func TestSomebodyDeactivatedIsNotReminded(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	gone := e.person(t, "Gone Gus", "member")
	e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), gone)
	e.exec(t, `UPDATE users SET is_active = false, deleted_at = NOW() WHERE id = $1`, gone)
	sweepReminders(t, e.pool, e.notifier, at(9, 56))
	if got := countContaining(e.notificationTypes(t, gone), "reminder"); got != 0 {
		t.Errorf("got %v", e.notificationTypes(t, gone))
	}
}

func TestChangingOnlyTheEndDoesNotRemindAgain(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	id := e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID
	sweepReminders(t, e.pool, e.notifier, at(9, 56))
	want(t, call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+id,
		map[string]any{"endsAt": at(11, 30).Format(time.RFC3339)}), http.StatusOK)
	sweepReminders(t, e.pool, e.notifier, at(9, 57))
	if got := countContaining(e.notificationTypes(t, bob), "reminder"); got != 1 {
		t.Errorf("the start did not change, so once, got %d", got)
	}

	// A new start is a new reminder.
	want(t, call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+id, map[string]any{
		"startsAt": at(10, 30).Format(time.RFC3339)}), http.StatusOK)
	sweepReminders(t, e.pool, e.notifier, at(10, 26))
	if got := countContaining(e.notificationTypes(t, bob), "reminder"); got != 2 {
		t.Errorf("a moved start is reminded, got %d", got)
	}
}

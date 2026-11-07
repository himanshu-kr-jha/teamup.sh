package calendar

// How a scheduled meeting starts: joining opens it, and it rings nobody. Everybody
// was told when they were invited and reminded before the start. These run the
// calendar and the calls service together, because the meeting is written by one
// and opened by the other.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/calls"
	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

type sentMessages struct {
	mu   sync.Mutex
	sent map[string][]string
}

func (s *sentMessages) BroadcastToUser(userID string, msg *protocol.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent == nil {
		s.sent = map[string][]string{}
	}
	s.sent[userID] = append(s.sent[userID], msg.Type)
}

func (s *sentMessages) BroadcastToChannel(string, *protocol.Message, string) {}

// ringing is every frame that would make a phone ring, or tell a caller it is.
func (s *sentMessages) ringing() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for to, types := range s.sent {
		for _, kind := range types {
			if kind == protocol.TypeCallInvited || kind == "call.reach" {
				out = append(out, to+":"+kind)
			}
		}
	}
	return out
}

type devices struct {
	mu     sync.Mutex
	pushes int
}

func (d *devices) PushToUsers(context.Context, []uuid.UUID, protocol.Push) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pushes++
}

func (d *devices) Notify(context.Context, uuid.UUID, uuid.UUID, string, string, string, map[string]any) error {
	return nil
}

type noteRecorder struct{ notes int }

func (n *noteRecorder) RecordCallEvent(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) (uuid.UUID, error) {
	n.notes++
	return uuid.New(), nil
}

type callWorld struct {
	*env
	svc    *calls.Service
	frames *sentMessages
	phones *devices
	notes  *noteRecorder
}

func newCallWorld(t *testing.T) *callWorld {
	t.Helper()
	e := newEnv(t)
	w := &callWorld{env: e, frames: &sentMessages{}, phones: &devices{}, notes: &noteRecorder{}}
	w.svc = calls.NewService(e.pool, calls.Config{LiveKitAPIKey: "devkey", LiveKitAPISecret: "devsecret-devsecret-devsecret-0123456789"})
	w.svc.SetBroadcaster(w.frames)
	w.svc.SetNotifier(w.phones)
	w.svc.SetChatRecorder(w.notes)
	return w
}

func (w *callWorld) callsAs(t *testing.T, user uuid.UUID, role string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", w.org.ID.String())
		c.Set("user_id", user.String())
		c.Set("user_roles", []string{role})
		c.Next()
	})
	calls.RegisterRoutes(api, w.svc)
	return r
}

func (w *callWorld) status(t *testing.T, id uuid.UUID) string {
	t.Helper()
	call, err := w.svc.GetCall(context.Background(), w.org.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return call.Status
}

func TestAScheduledMeetingRingsNobodyWhenItStarts(t *testing.T) {
	w := newCallWorld(t)
	organizer := w.person(t, "Organizer Orla", "member")
	bob := w.person(t, "Bob Builder", "member")
	id := uuid.MustParse(w.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID)

	if got := w.status(t, id); got != "scheduled" {
		t.Fatalf("a meeting waits until somebody joins, got %q", got)
	}

	// The organizer opens it, which is the moment a call would have rung.
	token := call(t, w.callsAs(t, organizer, "member"), http.MethodPost, "/api/calls/"+id.String()+"/token", nil)
	want(t, token, http.StatusOK)
	var body struct {
		Token, RoomName string
	}
	if err := json.Unmarshal(token.Body.Bytes(), &body); err != nil || body.Token == "" || body.RoomName == "" {
		t.Fatalf("joining a meeting gives a token: %s", token.Body.String())
	}
	if got := w.status(t, id); got != "active" {
		t.Errorf("joining opens it, got %q", got)
	}

	// Bob's socket reconnects inside the ring window. That is what rings somebody
	// for a call they were away for, and it must not ring him for this.
	ctx := context.Background()
	pending, err := w.svc.PendingInvitationsFor(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("a meeting that opened is not a call waiting for somebody, got %+v", pending)
	}
	w.svc.DeliverPendingInvitations(ctx, bob)

	if got := w.frames.ringing(); len(got) != 0 {
		t.Errorf("nobody is rung for a meeting, got %v", got)
	}
	w.phones.mu.Lock()
	defer w.phones.mu.Unlock()
	if w.phones.pushes != 0 {
		t.Errorf("no phone is rung for a meeting, got %d pushes", w.phones.pushes)
	}
}

func TestAMeetingNobodyCameToIsNotAMissedCall(t *testing.T) {
	w := newCallWorld(t)
	organizer := w.person(t, "Organizer Orla", "member")
	bob := w.person(t, "Bob Builder", "member")
	id := uuid.MustParse(w.mustSchedule(t, organizer, "member", "One on one", at(10, 0), at(11, 0), bob).meeting.ID)

	want(t, call(t, w.callsAs(t, organizer, "member"), http.MethodPost, "/api/calls/"+id.String()+"/token", nil), http.StatusOK)
	w.svc.NoteIfMissed(context.Background(), id, "no_answer")
	if w.notes.notes != 0 {
		t.Errorf("two people on a meeting is not a private call to Bob's phone, got %d notes", w.notes.notes)
	}
}

func TestAnInviteeCannotEndOrDeclineAMeetingThatHasNotStarted(t *testing.T) {
	w := newCallWorld(t)
	organizer := w.person(t, "Organizer Orla", "member")
	bob := w.person(t, "Bob Builder", "member")
	id := w.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID

	r := w.callsAs(t, bob, "member")
	want(t, call(t, r, http.MethodPost, "/api/calls/"+id+"/end", nil), http.StatusConflict)
	want(t, call(t, r, http.MethodPost, "/api/calls/"+id+"/decline", nil), http.StatusConflict)
	if got := w.status(t, uuid.MustParse(id)); got != "scheduled" {
		t.Errorf("only the organizer or an admin cancels a meeting, got %q", got)
	}
	if _, ok := find(w.mustCalendar(t, w.as(t, bob, "member"), bob), "Planning"); !ok {
		t.Error("the meeting stays on Bob's calendar")
	}
}

func TestACancelledMeetingCannotBeJoinedAndIsNotListed(t *testing.T) {
	w := newCallWorld(t)
	organizer := w.person(t, "Organizer Orla", "member")
	bob := w.person(t, "Bob Builder", "member")
	id := w.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID
	want(t, call(t, w.as(t, organizer, "member"), http.MethodDelete, "/api/meetings/"+id, nil), http.StatusOK)

	r := w.callsAs(t, bob, "member")
	want(t, call(t, r, http.MethodPost, "/api/calls/"+id+"/token", nil), http.StatusConflict)
	if body := call(t, r, http.MethodGet, "/api/calls", nil).Body.String(); strings.Contains(body, id) {
		t.Errorf("a cancelled meeting never happened, so it is not history: %s", body)
	}
}

func TestAMeetingThatEmptiedReopensUntilItsEndAndNotAfter(t *testing.T) {
	w := newCallWorld(t)
	organizer := w.person(t, "Organizer Orla", "member")
	id := w.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0)).meeting.ID
	callID := uuid.MustParse(id)
	r := w.callsAs(t, organizer, "member")
	path := "/api/calls/" + id + "/token"

	now := at(10, 5)
	w.svc.SetClock(func() time.Time { return now })
	want(t, call(t, r, http.MethodPost, path, nil), http.StatusOK)
	if err := w.svc.MarkCallEnded(context.Background(), w.org.ID, callID); err != nil {
		t.Fatal(err)
	}

	// Everybody's connection dropped at once. It is still the meeting's time.
	now = at(10, 30)
	want(t, call(t, r, http.MethodPost, path, nil), http.StatusOK)
	if got := w.status(t, callID); got != "active" {
		t.Errorf("rejoining during its time reopens it, got %q", got)
	}
	if err := w.svc.MarkCallEnded(context.Background(), w.org.ID, callID); err != nil {
		t.Fatal(err)
	}

	now = at(11, 1)
	want(t, call(t, r, http.MethodPost, path, nil), http.StatusConflict)
}

// ── reminders across replicas ─────────────────────────────────────────────

func TestAReminderIsSentOnceWhenManyReplicasSweepAtOnce(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	const invitees, replicas = 12, 8
	people := make([]uuid.UUID, invitees)
	for i := range people {
		people[i] = e.person(t, fmt.Sprintf("Person %d", i), "member")
	}
	e.mustSchedule(t, organizer, "member", "All hands", at(10, 0), at(11, 0), people...)

	// A replica is a service of its own that shares nothing but the database.
	var wg sync.WaitGroup
	start := make(chan struct{})
	sent := make([]int, replicas)
	for i := range sent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			sent[i] = sweepReminders(t, e.pool, e.notifier, at(9, 56))
		}()
	}
	close(start)
	wg.Wait()

	total := 0
	for _, n := range sent {
		total += n
	}
	if total != invitees+1 {
		t.Errorf("one reminder per invitee and one for the organizer across all replicas: want %d, got %d (%v)", invitees+1, total, sent)
	}
	for i, id := range append([]uuid.UUID{organizer}, people...) {
		if n := countContaining(e.notificationTypes(t, id), "remind"); n != 1 {
			t.Errorf("person %d (0 is the organizer) got %d reminders", i, n)
		}
	}
}

func TestAMeetingMovedAwayAndBackIsRemindedAgain(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	m := e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting
	r := e.as(t, organizer, "member")

	sweepReminders(t, e.pool, e.notifier, at(9, 56))
	want(t, call(t, r, http.MethodPatch, "/api/meetings/"+m.ID, map[string]any{"startsAt": at(14, 0).Format(time.RFC3339), "endsAt": at(15, 0).Format(time.RFC3339)}), http.StatusOK)
	want(t, call(t, r, http.MethodPatch, "/api/meetings/"+m.ID, map[string]any{"startsAt": at(10, 0).Format(time.RFC3339), "endsAt": at(11, 0).Format(time.RFC3339)}), http.StatusOK)
	sweepReminders(t, e.pool, e.notifier, at(9, 57))
	if n := countContaining(e.notificationTypes(t, bob), "remind"); n != 2 {
		t.Errorf("the second time is a new time as far as Bob knows, got %d reminders", n)
	}
}

func TestChangingATitleDoesNotNotifyAsAMove(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	m := e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting

	want(t, call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+m.ID, map[string]any{"title": "Better title"}), http.StatusOK)
	if n := countContaining(e.notificationTypes(t, bob), "mov"); n != 0 {
		t.Errorf("nothing moved, got %d", n)
	}
}

func TestChangingWhoIsInvitedTellsThePeopleItChanges(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	stays := e.person(t, "Stays Sue", "member")
	goes := e.person(t, "Goes Gil", "member")
	joins := e.person(t, "Joins Jo", "member")
	m := e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), stays, goes).meeting

	want(t, call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+m.ID,
		map[string]any{"inviteeIds": []string{stays.String(), joins.String()}}), http.StatusOK)

	if n := countContaining(e.notificationTypes(t, joins), "invit"); n != 1 {
		t.Errorf("somebody added is invited, got %v", e.notificationTypes(t, joins))
	}
	if n := countContaining(e.notificationTypes(t, goes), "cancel"); n != 1 {
		t.Errorf("somebody removed is told, got %v", e.notificationTypes(t, goes))
	}
	if n := len(e.notificationTypes(t, stays)); n != 1 {
		t.Errorf("somebody it does not change hears nothing new, got %v", e.notificationTypes(t, stays))
	}
	if _, ok := find(e.mustCalendar(t, e.as(t, goes, "member"), goes), "Planning"); ok {
		t.Error("it is off the calendar of somebody removed")
	}
	if _, ok := find(e.mustCalendar(t, e.as(t, joins, "member"), joins), "Planning"); !ok {
		t.Error("it is on the calendar of somebody added")
	}
}

func TestAMeetingUnderWayCannotBeMovedOrCancelled(t *testing.T) {
	w := newCallWorld(t)
	organizer := w.person(t, "Organizer Orla", "member")
	id := w.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0)).meeting.ID
	want(t, call(t, w.callsAs(t, organizer, "member"), http.MethodPost, "/api/calls/"+id+"/token", nil), http.StatusOK)

	r := w.as(t, organizer, "member")
	want(t, call(t, r, http.MethodPatch, "/api/meetings/"+id, map[string]any{"title": "New"}), http.StatusConflict)
	want(t, call(t, r, http.MethodDelete, "/api/meetings/"+id, nil), http.StatusConflict)
}

// ── standing meetings on the calendar ─────────────────────────────────────

func (w *callWorld) standing(t *testing.T, weekdays []int) string {
	t.Helper()
	res := call(t, w.callsAs(t, w.org.OwnerID, "owner"), http.MethodPost, "/api/standing-meetings", map[string]any{
		"name": "Morning scrum", "timeOfDay": "09:30", "timeZone": "UTC", "weekdays": weekdays, "durationMinutes": 15,
	})
	want(t, res, http.StatusCreated)
	var created struct {
		StandingMeeting struct {
			ID string `json:"id"`
		} `json:"standingMeeting"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created.StandingMeeting.ID
}

func TestAStandingMeetingIsOnEverybodysCalendarAndCountsAsAConflict(t *testing.T) {
	w := newCallWorld(t)
	alice := w.person(t, "Alice Archer", "member")
	colleague := w.person(t, "Colleague Cal", "member")
	organizer := w.person(t, "Organizer Orla", "member")
	id := w.standing(t, []int{1, 2, 3, 4, 5, 6, 7})

	it, ok := find(w.mustCalendar(t, w.as(t, alice, "member"), alice), "Morning scrum")
	if !ok || it.Kind != "busy" || it.Source != "standing" || !near(it.StartsAt, at(9, 30)) || !near(it.EndsAt, at(9, 45)) {
		t.Fatalf("the scrum is on my calendar at its time, got %+v", it)
	}

	w2, _ := w.calendar(t, w.as(t, colleague, "member"), alice)
	if strings.Contains(w2.Body.String(), "Morning scrum") {
		t.Errorf("no titles without authority, even for this: %s", w2.Body.String())
	}

	res := w.mustSchedule(t, organizer, "member", "Clash", at(9, 40), at(10, 0), alice)
	found := false
	for _, c := range res.conflicts {
		if c.UserID == alice.String() && c.Kind == "meeting" {
			found = true
		}
	}
	if !found {
		t.Errorf("a standing meeting is a meeting, so it warns, got %+v", res.conflicts)
	}

	want(t, call(t, w.callsAs(t, w.org.OwnerID, "owner"), http.MethodDelete, "/api/standing-meetings/"+id, nil), http.StatusOK)
	if _, ok := find(w.mustCalendar(t, w.as(t, alice, "member"), alice), "Morning scrum"); ok {
		t.Error("an archived room is off the calendar")
	}
}

func TestAStandingMeetingOnlyAppearsOnTheDaysItRuns(t *testing.T) {
	w := newCallWorld(t)
	alice := w.person(t, "Alice Archer", "member")
	iso := int(at(0, 0).Weekday())
	if iso == 0 {
		iso = 7
	}
	other := iso%7 + 1
	w.standing(t, []int{other})

	if _, ok := find(w.mustCalendar(t, w.as(t, alice, "member"), alice), "Morning scrum"); ok {
		t.Error("it does not run on that weekday")
	}
}

func TestAttendingAStandingMeetingShowsTheRealTimeInsteadOfTheSlot(t *testing.T) {
	w := newCallWorld(t)
	alice := w.person(t, "Alice Archer", "member")
	id := w.standing(t, []int{1, 2, 3, 4, 5, 6, 7})

	w.svc.SetClock(func() time.Time { return at(9, 0) })
	res := call(t, w.callsAs(t, alice, "member"), http.MethodPost, "/api/standing-meetings/"+id+"/join", nil)
	want(t, res, http.StatusOK)
	var joined struct {
		CallID string `json:"callId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &joined); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(context.Background(), `
		INSERT INTO call_participants (call_id, user_id, joined_at, left_at) VALUES ($1, $2, $3, $4)
	`, joined.CallID, alice, at(9, 35), at(9, 50)); err != nil {
		t.Fatal(err)
	}

	items := w.mustCalendar(t, w.as(t, alice, "member"), alice)
	count := 0
	for _, it := range items {
		if it.Title == "Morning scrum" {
			count++
			if !near(it.StartsAt, at(9, 35)) || !near(it.EndsAt, at(9, 50)) {
				t.Errorf("what really happened replaces the slot, got %+v", it)
			}
		}
	}
	if count != 1 {
		t.Errorf("one entry for that day, got %d: %+v", count, items)
	}
}

// Nothing in another organization's standing meetings reaches this one's calendar.
func TestAnotherOrganizationsStandingMeetingIsNotOnMyCalendar(t *testing.T) {
	w := newCallWorld(t)
	w.standing(t, []int{1, 2, 3, 4, 5, 6, 7})
	other := testsupport.SeedOrg(t, w.pool)
	foreign := buildRouter(t, w.pool, w.notifier, other.ID, other.OwnerID, []string{"owner"})
	if items := w.mustCalendar(t, foreign, other.OwnerID); len(items) != 0 {
		t.Errorf("got %+v", items)
	}
}

// Everybody tends to arrive at the start of a meeting at once.
func TestEverybodyOpeningAMeetingAtTheSameMomentGetsIn(t *testing.T) {
	w := newCallWorld(t)
	organizer := w.person(t, "Organizer Orla", "member")
	const people = 8
	ids := make([]uuid.UUID, people)
	for i := range ids {
		ids[i] = w.person(t, fmt.Sprintf("Person %d", i), "member")
	}
	id := w.mustSchedule(t, organizer, "member", "All hands", at(10, 0), at(11, 0), ids...).meeting.ID

	routers := make([]*gin.Engine, people)
	for i := range ids {
		routers[i] = w.callsAs(t, ids[i], "member")
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, people)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes[i] = call(t, routers[i], http.MethodPost, "/api/calls/"+id+"/token", nil).Code
		}()
	}
	close(start)
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("person %d arrived with everybody else and got %d", i, code)
		}
	}
}

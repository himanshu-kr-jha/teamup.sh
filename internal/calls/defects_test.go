package calls

// Tickets 23, 25, 26, 27, 29, 31 and 32, reported by the independent reviewer and
// the browser tester. Each test is the reproduction written in the ticket.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"

	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

// fakeMedia stands in for LiveKit. onClose is the webhook LiveKit sends the moment a
// room is deleted, which can overtake whoever asked.
type fakeMedia struct {
	mu       sync.Mutex
	rooms    []string
	listErr  error
	onClose  func(room string)
	closed   []string
	removed  []string
	recorded []*livekit.RoomCompositeEgressRequest
	// present is who is in each room, as room/identity; sent is what was
	// delivered to a room.
	present map[string]bool
	sent    []sentData
}

type sentData struct {
	room, topic string
	data        []byte
}

func (f *fakeMedia) SendData(_ context.Context, room, topic string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentData{room, topic, data})
	return nil
}

func (f *fakeMedia) InRoom(_ context.Context, room, identity string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.present[room+"/"+identity], nil
}

func (f *fakeMedia) CloseRoom(_ context.Context, room string) error {
	f.mu.Lock()
	f.closed = append(f.closed, room)
	hook := f.onClose
	f.mu.Unlock()
	if hook != nil {
		hook(room)
	}
	return nil
}

func (f *fakeMedia) RoomNames(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rooms, f.listErr
}

func (f *fakeMedia) RemoveParticipant(_ context.Context, room, identity string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, room+"/"+identity)
	return nil
}

func (f *fakeMedia) StartRecording(_ context.Context, req *livekit.RoomCompositeEgressRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, req)
	return "EG_test", nil
}

func (f *fakeMedia) removals() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.removed...)
}

// lockedNotes is what the conversation was told, from whichever goroutine.
type lockedNotes struct {
	mu    sync.Mutex
	notes []string
}

func (n *lockedNotes) RecordCallEvent(_ context.Context, _, _, _ uuid.UUID, text string) (uuid.UUID, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notes = append(n.notes, text)
	return uuid.New(), nil
}

func (n *lockedNotes) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.notes)
}

func (n *lockedNotes) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string{}, n.notes...)
}

func (e *lkEnv) status(t *testing.T, id uuid.UUID) string {
	t.Helper()
	call, err := e.svc.GetCall(context.Background(), e.org.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return call.Status
}

func (e *lkEnv) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("never happened: %s", what)
}

// ── ticket 23 (c): hanging up against the webhook it causes ───────────────

func TestHangingUpSurvivesTheWebhookItCauses(t *testing.T) {
	e := newLK(t)
	media := &fakeMedia{}
	e.svc.media = media
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	media.onClose = func(room string) {
		if err := e.svc.HandleWebhook(context.Background(), roomFinishedEv(room, time.Now())); err != nil {
			t.Errorf("webhook: %v", err)
		}
	}
	w := doJSON(t, e.asOwner(t), http.MethodPost, "/api/calls/"+call.ID.String()+"/end", "")
	wantCode(t, w, http.StatusOK)
	if got := e.status(t, call.ID); got != "ended" {
		t.Errorf("got %q", got)
	}
}

func TestDecliningSaysDeclinedEvenIfTheWebhookArrivesFirst(t *testing.T) {
	e := newLK(t)
	media := &fakeMedia{}
	e.svc.media = media
	recorder := &lockedNotes{}
	e.svc.SetChatRecorder(recorder)
	invitee := e.person(t, "Invited Ivy", "member")
	call := e.call(t, e.org.OwnerID, invitee)
	media.onClose = func(room string) {
		if err := e.svc.HandleWebhook(context.Background(), roomFinishedEv(room, time.Now())); err != nil {
			t.Errorf("webhook: %v", err)
		}
	}

	w := doJSON(t, e.as(t, invitee, "member"), http.MethodPost, "/api/calls/"+call.ID.String()+"/decline", `{"reason":"declined"}`)
	wantCode(t, w, http.StatusOK)
	eventually(t, "the conversation says something", func() bool { return recorder.count() > 0 })
	time.Sleep(100 * time.Millisecond)
	if got := recorder.all(); len(got) != 1 || got[0] != "Call declined" {
		t.Errorf("one note, saying what happened, got %v", got)
	}
}

// ── ticket 23 (a): nothing ever closed a call whose webhook was lost ──────

func TestARoomThatIsGoneEndsACallWhoseWebhookWasLost(t *testing.T) {
	e := newLK(t)
	media := &fakeMedia{}
	e.svc.media = media
	ctx := context.Background()
	invitee := e.person(t, "Invited Ivy", "member")

	lost := e.call(t, e.org.OwnerID, invitee)
	stillThere := e.call(t, e.org.OwnerID, invitee)
	young := e.call(t, e.org.OwnerID, invitee)
	for _, c := range []*Call{lost, stillThere} {
		e.exec(t, `UPDATE calls SET started_at = NOW() - interval '1 hour', updated_at = NOW() - interval '1 hour' WHERE id = $1`, c.ID)
	}
	e.mustDeliver(t, joinedEv(lost.RoomName, invitee, "PA_1", time.Now().Add(-50*time.Minute)))
	media.rooms = []string{stillThere.RoomName}

	// LiveKit cannot be reached, which says nothing about any room.
	media.listErr = errors.New("connection refused")
	if _, err := e.svc.SweepVanishedRooms(ctx); err == nil {
		t.Error("the failure is reported")
	}
	if got := e.status(t, lost.ID); got != "active" {
		t.Fatalf("not knowing is not the same as the room being gone, got %q", got)
	}

	media.listErr = nil
	ended, err := e.svc.SweepVanishedRooms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ended != 1 {
		t.Errorf("one call, got %d", ended)
	}
	if got := e.status(t, lost.ID); got != "ended" {
		t.Errorf("its room is gone, got %q", got)
	}
	if got := e.status(t, stillThere.ID); got != "active" {
		t.Errorf("its room exists, got %q", got)
	}
	if got := e.status(t, young.ID); got != "active" {
		t.Errorf("a call just made has a person on the way with a token, got %q", got)
	}
	ss := getLog(t, e.asOwner(t), lost.ID).sessions(invitee)
	if len(ss) != 1 || ss[0].LeftAt == nil {
		t.Errorf("the session closes with the room, got %+v", ss)
	}

	// Another replica sweeping the same moment has nothing left to do.
	if again, err := e.svc.SweepVanishedRooms(ctx); err != nil || again != 0 {
		t.Errorf("got %d, %v", again, err)
	}
}

// ── ticket 25: a standing meeting across midnight ─────────────────────────

func TestASlotCrossingMidnightIsOneRoom(t *testing.T) {
	e := newLK(t)
	body := `{"name":"Night","timeOfDay":"23:30","timeZone":"UTC","weekdays":[1,2,3,4,5,6,7],"durationMinutes":60}`
	code, m := e.createStanding(t, e.org.OwnerID, "owner", body)
	if code != http.StatusCreated {
		t.Fatalf("got %d", code)
	}
	ann := e.person(t, "Ann Archer", "member")
	bob := e.person(t, "Bob Builder", "member")

	now := time.Date(2026, 11, 9, 23, 40, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return now })
	first := e.mustJoinStanding(t, ann, "member", m.ID)
	now = time.Date(2026, 11, 10, 0, 10, 0, 0, time.UTC)
	second := e.mustJoinStanding(t, bob, "member", m.ID)

	if second.RoomName != first.RoomName || second.Date != "2026-11-09" {
		t.Errorf("the same meeting is the same room: %+v and %+v", first, second)
	}
	// After it, the next one belongs to the new day.
	now = time.Date(2026, 11, 10, 0, 31, 0, 0, time.UTC)
	if third := e.mustJoinStanding(t, bob, "member", m.ID); third.Date != "2026-11-10" {
		t.Errorf("that slot is over, got %s", third.Date)
	}
}

func TestAnOccurrenceCrossingMidnightReopensForItsLinkAndCountsItsWholeLength(t *testing.T) {
	e := newLK(t)
	body := `{"name":"Night","timeOfDay":"23:30","timeZone":"UTC","weekdays":[1,2,3,4,5,6,7],"durationMinutes":120}`
	_, m := e.createStanding(t, e.org.OwnerID, "owner", body)
	ann := e.person(t, "Ann Archer", "member")
	e.exec(t, `UPDATE standing_meetings SET created_at = '2026-01-01' WHERE id = $1`, m.ID)
	e.exec(t, `UPDATE users SET created_at = '2026-01-01' WHERE org_id = $1`, e.org.ID)

	now := time.Date(2026, 11, 9, 23, 40, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return now })
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	callID := uuid.MustParse(j.CallID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", now))
	if err := e.svc.MarkCallEnded(context.Background(), e.org.ID, callID); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE call_sessions SET left_at = NULL WHERE call_id = $1`, callID)

	// 00:30 on the 10th: its slot runs to 01:30, so its link still opens it.
	now = time.Date(2026, 11, 10, 0, 30, 0, 0, time.UTC)
	w := doJSON(t, e.as(t, ann, "member"), http.MethodPost, "/api/calls/"+j.CallID+"/token", "")
	wantCode(t, w, http.StatusOK)
	if got := e.status(t, callID); got != "active" {
		t.Errorf("got %q", got)
	}

	// An open session counts to now, not to midnight.
	now = time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC)
	days := e.attendance(t, ann, "member", m.ID, "date=2026-11-09")
	if p, ok := days[0].person(ann); !ok || p.MinutesPresent != 80 {
		t.Errorf("23:40 to 01:00 is 80 minutes, got %+v", p)
	}

	// Once the slot has ended and the day has too, it does not come back.
	if err := e.svc.MarkCallEnded(context.Background(), e.org.ID, callID); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 11, 10, 1, 31, 0, 0, time.UTC)
	wantCode(t, doJSON(t, e.as(t, ann, "member"), http.MethodPost, "/api/calls/"+j.CallID+"/token", ""), http.StatusConflict)
}

func TestPressingJoinJustBeforeASlotJustAfterMidnightIsThatSlot(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Early", "00:05", "UTC")
	ann := e.person(t, "Ann Archer", "member")
	e.exec(t, `UPDATE standing_meetings SET created_at = '2026-01-01' WHERE id = $1`, m.ID)

	now := time.Date(2026, 11, 9, 23, 58, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return now })
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	if j.Date != "2026-11-10" {
		t.Fatalf("she is early for the 10th, got %s", j.Date)
	}
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", now))

	now = time.Date(2026, 11, 10, 0, 30, 0, 0, time.UTC)
	days := e.attendance(t, ann, "member", m.ID, "date=2026-11-10")
	p, ok := days[0].person(ann)
	if !ok || p.Status != "present" || p.MinutesLate != 0 {
		t.Errorf("present and not late, got %+v", p)
	}
	// Today's slot, the 9th, is untouched: a later join today would have been early.
	if days := e.attendance(t, ann, "member", m.ID, "date=2026-11-09"); len(days) == 1 {
		if p, ok := days[0].person(ann); ok && p.MinutesLate > 0 {
			t.Errorf("not 1433 minutes late on the 9th, got %+v", p)
		}
	}
}

// ── ticket 26: somebody deactivated ───────────────────────────────────────

func (e *lkEnv) deactivate(t *testing.T, who uuid.UUID) {
	t.Helper()
	e.exec(t, `UPDATE users SET is_active = false, deleted_at = NOW() WHERE id = $1`, who)
}

func TestSomebodyDeactivatedMayNotJoinAnything(t *testing.T) {
	e := newLK(t)
	ctx := context.Background()
	sockets := newRecordingBroadcaster()
	e.svc.SetBroadcaster(sockets)
	gone := e.person(t, "Gone Gus", "member")
	call := e.call(t, e.org.OwnerID, gone)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	occurrence := e.mustJoinStanding(t, e.org.OwnerID, "owner", m.ID)
	occurrenceID := uuid.MustParse(occurrence.CallID)
	e.deactivate(t, gone)

	for name, id := range map[string]uuid.UUID{"invited": call.ID, "standing": occurrenceID} {
		if ok, err := e.svc.MayJoin(ctx, e.org.ID, id, gone); err != nil || ok {
			t.Errorf("%s: may join = %v, %v", name, ok, err)
		}
		if ok, err := e.svc.IsMember(ctx, e.org.ID, id, gone); err != nil || ok {
			t.Errorf("%s: member = %v, %v", name, ok, err)
		}
	}

	// Their tab's socket lives on until it is closed, and asks for a fresh token.
	for _, room := range []string{call.RoomName, occurrence.RoomName} {
		raw, _ := json.Marshal(protocol.CallAcceptPayload{RoomName: room})
		e.svc.HandleCallAccept(ctx, &wsConn{userID: gone.String(), orgID: e.org.ID.String()},
			&protocol.Message{Type: protocol.TypeCallAccept, Payload: raw})
	}
	sockets.mu.Lock()
	defer sockets.mu.Unlock()
	if n := len(sockets.toUser[gone.String()]); n != 0 {
		t.Errorf("no grant for somebody who has left, got %d frames", n)
	}
}

func TestDeactivatingSomebodyTakesThemOutOfEveryRoomTheyAreIn(t *testing.T) {
	e := newLK(t)
	media := &fakeMedia{}
	e.svc.media = media
	gone := e.person(t, "Gone Gus", "member")
	stay := e.person(t, "Stay Stan", "member")
	inRoom := e.call(t, e.org.OwnerID, gone, stay)
	leftRoom := e.call(t, e.org.OwnerID, gone)
	now := time.Now()
	e.mustDeliver(t, joinedEv(inRoom.RoomName, gone, "PA_g", now))
	e.mustDeliver(t, joinedEv(inRoom.RoomName, stay, "PA_s", now))
	e.mustDeliver(t, joinedEv(leftRoom.RoomName, gone, "PA_g2", now))
	e.mustDeliver(t, leftEv(leftRoom.RoomName, gone, "PA_g2", now))

	e.deactivate(t, gone)
	e.svc.RemoveFromCalls(context.Background(), e.org.ID, gone)

	got := media.removals()
	if len(got) != 1 || got[0] != inRoom.RoomName+"/"+gone.String() {
		t.Errorf("out of the one room they were in, and nobody else: %v", got)
	}
}

// A token cannot be revoked, and its lifetime is deliberately left alone, so somebody
// who has left can still connect with one they already had. They do not stay.
func TestSomebodyDeactivatedConnectingWithAnOldTokenIsPutOutAndLogged(t *testing.T) {
	e := newLK(t)
	media := &fakeMedia{}
	e.svc.media = media
	gone := e.person(t, "Gone Gus", "member")
	stay := e.person(t, "Stay Stan", "member")
	call := e.call(t, e.org.OwnerID, gone, stay)
	e.deactivate(t, gone)

	now := time.Now()
	e.mustDeliver(t, joinedEv(call.RoomName, gone, "PA_g", now))
	e.mustDeliver(t, joinedEv(call.RoomName, stay, "PA_s", now))

	got := media.removals()
	if len(got) != 1 || got[0] != call.RoomName+"/"+gone.String() {
		t.Errorf("Gus goes and Stan stays: %v", got)
	}
	if n := len(getLog(t, e.asOwner(t), call.ID).events("joined", gone)); n != 1 {
		t.Errorf("it happened, so the trail says so, got %d", n)
	}
}

// ── ticket 27: false absences ─────────────────────────────────────────────

func TestAMeetingCreatedAfterTheDaysSlotMarksNobodyAbsent(t *testing.T) {
	e := newLK(t)
	created := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return created })
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	e.exec(t, `UPDATE standing_meetings SET created_at = $2 WHERE id = $1`, m.ID, created)
	e.exec(t, `UPDATE users SET created_at = '2026-01-01' WHERE org_id = $1`, e.org.ID)

	if days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-02"); len(days) != 0 {
		t.Errorf("the meeting did not exist at 09:00, got %+v", days)
	}
	// Tuesday's slot is after it was made.
	later := time.Date(2026, 3, 3, 9, 30, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return later })
	if days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-03"); len(days) != 1 {
		t.Errorf("the first slot after it was made counts, got %+v", days)
	}
}

func TestEditingTheWeekdaysDoesNotRewriteHistory(t *testing.T) {
	e := newLK(t)
	// Monday (1) only, from the start of the year.
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return created })
	code, m := e.createStanding(t, e.org.OwnerID, "owner", standingBody("Scrum", "09:00", "UTC", 1))
	if code != http.StatusCreated {
		t.Fatalf("got %d", code)
	}
	e.exec(t, `UPDATE standing_meetings SET created_at = $2 WHERE id = $1`, m.ID, created)
	e.exec(t, `UPDATE standing_meeting_schedules SET effective_at = $2 WHERE standing_meeting_id = $1`, m.ID, created)
	ann := e.person(t, "Ann Archer", "member")
	e.exec(t, `UPDATE users SET created_at = '2025-12-01' WHERE org_id = $1`, e.org.ID)

	// On Wednesday 4 March the admin swaps Monday for Saturday.
	edited := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return edited })
	wantCode(t, doJSON(t, e.asOwner(t), http.MethodPatch, "/api/standing-meetings/"+m.ID, `{"weekdays":[6]}`), http.StatusOK)

	read := func(date string) []attendanceDay {
		return e.attendance(t, ann, "member", m.ID, "date="+date)
	}
	if days := read("2026-02-28"); len(days) != 0 { // a Saturday before the edit
		t.Errorf("that Saturday it did not run, so nobody was absent, got %+v", days)
	}
	days := read("2026-03-02") // a Monday before the edit
	if len(days) != 1 {
		t.Fatalf("that Monday it did run, and nobody came, got %+v", days)
	}
	if p, ok := days[0].person(ann); !ok || p.Status != "absent" {
		t.Errorf("she was absent that Monday, got %+v", p)
	}
	later := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return later })
	if days := read("2026-03-07"); len(days) != 1 { // the first Saturday after
		t.Errorf("Saturdays count from now on, got %+v", days)
	}
	if days := read("2026-03-02"); len(days) != 1 {
		t.Errorf("still there, got %+v", days)
	}
}

func TestSomebodyEarlyDoesNotMakeEverybodyElseAbsentBeforeTheStart(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:30", "UTC")
	ann := e.person(t, "Ann Archer", "member")
	bob := e.person(t, "Bob Builder", "member")
	e.exec(t, `UPDATE standing_meetings SET created_at = '2026-01-01' WHERE id = $1`, m.ID)
	e.exec(t, `UPDATE users SET created_at = '2026-01-01' WHERE org_id = $1`, e.org.ID)

	now := time.Date(2026, 3, 2, 9, 5, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return now })
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", now))

	days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-02")
	if len(days) != 1 {
		t.Fatalf("got %+v", days)
	}
	if p, ok := days[0].person(ann); !ok || p.Status != "present" {
		t.Errorf("she is there, got %+v", p)
	}
	if p, ok := days[0].person(bob); ok {
		t.Errorf("it has not started, so Bob is not absent yet, got %+v", p)
	}

	now = time.Date(2026, 3, 2, 9, 31, 0, 0, time.UTC)
	days = e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-02")
	if p, ok := days[0].person(bob); !ok || p.Status != "absent" {
		t.Errorf("now he is, got %+v", p)
	}
}

// ── ticket 29: recording ──────────────────────────────────────────────────

func recordingEnv(t *testing.T) (*lkEnv, *fakeMedia) {
	t.Helper()
	e := newLK(t)
	media := &fakeMedia{}
	e.svc.media = media
	e.svc.cfg.StorageBucket = "recordings"
	return e, media
}

func TestAManagerWithNoLinkToAPrivateCallCannotRecordIt(t *testing.T) {
	e, media := recordingEnv(t)
	one := e.person(t, "One Ona", "member")
	stranger := e.person(t, "Other Manager", "manager")
	call := e.call(t, e.org.OwnerID, one)
	r := e.as(t, stranger, "manager")

	for method, path := range map[string]string{http.MethodPost: "/record", http.MethodDelete: "/record"} {
		w := doJSON(t, r, method, "/api/calls/"+call.ID.String()+path, "")
		wantCode(t, w, http.StatusNotFound)
	}
	if len(media.recorded) != 0 {
		t.Errorf("nothing was started, got %d", len(media.recorded))
	}
}

func TestAManagerOnTheCallAndAnAdminMayRecordIt(t *testing.T) {
	e, media := recordingEnv(t)
	boss := e.person(t, "Boss Bea", "manager")
	call := e.call(t, e.org.OwnerID, boss)
	w := doJSON(t, e.as(t, boss, "manager"), http.MethodPost, "/api/calls/"+call.ID.String()+"/record", "")
	wantCode(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"roomName"`) {
		t.Errorf("a member reads the call: %s", w.Body.String())
	}
	wantCode(t, doJSON(t, e.asOwner(t), http.MethodDelete, "/api/calls/"+call.ID.String()+"/record", ""), http.StatusOK)
	if len(media.recorded) != 1 {
		t.Errorf("got %d", len(media.recorded))
	}
}

func TestAManagerRecordingAStandingMeetingGetsOnlyWhatTheyMayRead(t *testing.T) {
	e, _ := recordingEnv(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	ann := e.person(t, "Ann Archer", "member")
	stranger := e.person(t, "Other Manager", "manager")
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", time.Now()))

	// Everybody may be in the room, so a manager may record it, but who came is
	// attendance's rule and they answer for nobody there.
	w := doJSON(t, e.as(t, stranger, "manager"), http.MethodPost, "/api/calls/"+j.CallID+"/record", "")
	wantCode(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), ann.String()) || strings.Contains(w.Body.String(), "participants") {
		t.Errorf("that is who came: %s", w.Body.String())
	}
}

func TestRecordingAReopenedCallDoesNotOverwriteTheFirstFile(t *testing.T) {
	e, media := recordingEnv(t)
	ctx := context.Background()
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	j := e.mustJoinStanding(t, e.org.OwnerID, "owner", m.ID)
	id := uuid.MustParse(j.CallID)

	if _, err := e.svc.StartRecording(ctx, e.org.ID, id); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.MarkCallEnded(ctx, e.org.ID, id); err != nil {
		t.Fatal(err)
	}
	e.mustJoinStanding(t, e.org.OwnerID, "owner", m.ID)
	time.Sleep(2 * time.Millisecond)
	if _, err := e.svc.StartRecording(ctx, e.org.ID, id); err != nil {
		t.Fatal(err)
	}
	if len(media.recorded) != 2 {
		t.Fatalf("got %d", len(media.recorded))
	}
	first := media.recorded[0].GetFile().GetFilepath()
	second := media.recorded[1].GetFile().GetFilepath()
	if first == second {
		t.Errorf("two recordings, two files: %s", first)
	}
	if want := "org-" + e.org.ID.String() + "/recordings/" + id.String() + "/"; !strings.HasPrefix(second, want) {
		t.Errorf("still under the organization's prefix: %s", second)
	}
}

// ── ticket 31: the log link ───────────────────────────────────────────────

func TestAttendanceSaysWhetherTheReaderMayOpenThatDaysLog(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	ann := e.person(t, "Ann Archer", "member")
	boss := e.person(t, "Boss Bea", "manager")
	bystander := e.person(t, "Bystander Bo", "manager")
	testsupport.SeedTeam(t, e.pool, e.org, "Anns", &boss, nil, ann)
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", time.Now()))

	read := func(who uuid.UUID, role string) bool {
		w := doJSON(t, e.as(t, who, role), http.MethodGet, "/api/standing-meetings/"+m.ID+"/attendance?date="+j.Date, "")
		wantCode(t, w, http.StatusOK)
		var body struct {
			Days []struct {
				CanReadLog bool `json:"canReadLog"`
			} `json:"days"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Days) != 1 {
			t.Fatalf("%v: %s", err, w.Body.String())
		}
		return body.Days[0].CanReadLog
	}
	if read(ann, "member") {
		t.Error("an employee is refused, so is not offered it")
	}
	if !read(boss, "manager") {
		t.Error("a manager over somebody who came reads it")
	}
	if read(bystander, "manager") {
		t.Error("a manager over nobody there is refused")
	}
	if !read(e.org.OwnerID, "owner") {
		t.Error("the owner reads it")
	}
}

func TestTheCallListAndTheCallSayWhetherYouMayOpenTheLog(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	ann := e.person(t, "Ann Archer", "member")
	ordinary := e.call(t, e.org.OwnerID, ann)
	j := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, ann, "PA_1", time.Now()))
	r := e.as(t, ann, "member")

	w := doJSON(t, r, http.MethodGet, "/api/calls", "")
	wantCode(t, w, http.StatusOK)
	var list struct {
		Calls []struct {
			ID         string `json:"id"`
			CanReadLog bool   `json:"canReadLog"`
		} `json:"calls"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range list.Calls {
		seen[c.ID] = c.CanReadLog
	}
	if readable, listed := seen[ordinary.ID.String()]; !listed || !readable {
		t.Errorf("a call she is on: listed %v, readable %v", listed, readable)
	}
	if readable, listed := seen[j.CallID]; !listed || readable {
		t.Errorf("a standing meeting she came to: listed %v, readable %v", listed, readable)
	}

	read := func(id string) bool {
		w := doJSON(t, r, http.MethodGet, "/api/calls/"+id, "")
		wantCode(t, w, http.StatusOK)
		var body struct {
			CanReadLog bool `json:"canReadLog"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.CanReadLog
	}
	if !read(ordinary.ID.String()) || read(j.CallID) {
		t.Errorf("one call yes, the standing one no")
	}
}

// ── ticket 32: the answerer's other tab ───────────────────────────────────

func TestWhoeverAnswersAKnockIsToldToo(t *testing.T) {
	w := newKnockWorld(t)
	req := w.mustKnock(t, w.knocker)
	w.frames.reset()
	wantCode(t, w.answer(t, "deny", w.host, "member", req.ID), http.StatusOK)

	resolved := 0
	for _, f := range w.frames.knockFrames(w.host) {
		if f.Type == protocol.TypeCallKnockResolved {
			resolved++
		}
	}
	if resolved != 1 {
		t.Errorf("their second tab is still showing the prompt, got %d frames", resolved)
	}
}

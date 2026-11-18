package calls

// Notes on or off in a call: who may pause and resume them, what is kept, and
// who is told.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// noteTaker stands in for the note-taker's side: whether it is set up, and
// what it was told.
type noteTaker struct {
	mu        sync.Mutex
	available bool
	everyCall bool
	told      []uuid.UUID
}

func (n *noteTaker) policy(context.Context, uuid.UUID) (bool, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.available, n.everyCall
}

func (n *noteTaker) changed(_ context.Context, _, callID uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.told = append(n.told, callID)
}

func (n *noteTaker) toldCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.told)
}

func notesOf(t *testing.T, body []byte) NotesState {
	t.Helper()
	var st NotesState
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return st
}

func (f *fakeMedia) notesSent() []NotesState {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []NotesState
	for _, s := range f.sent {
		if s.topic == MeetingNotesTopic {
			var st NotesState
			_ = json.Unmarshal(s.data, &st)
			out = append(out, st)
		}
	}
	return out
}

func TestAnybodyInTheCallPausesAndResumesTheNotesAndEverybodyIsTold(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	ravi := e.person(t, "Ravi Rao", "member")
	away := e.person(t, "Alma Away", "member")
	other := e.person(t, "Otto Other", "member")
	call := e.call(t, org, ravi, away)
	ctx := context.Background()
	e.exec(t, `UPDATE calls SET status = 'active' WHERE id = $1`, call.ID)
	media := &fakeMedia{present: map[string]bool{
		call.RoomName + "/" + org.String():  true,
		call.RoomName + "/" + ravi.String(): true,
	}}
	e.svc.media = media
	taker := &noteTaker{available: true, everyCall: true}
	e.svc.SetNotesHooks(taker.policy, taker.changed)
	hook := &joinHook{}
	e.svc.SetJoinHook(hook.joined)
	path := "/api/calls/" + call.ID.String() + "/notes"

	w := doJSON(t, e.as(t, ravi, "member"), http.MethodGet, "/api/notes-default", "")
	wantCode(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"available":true`) || !strings.Contains(w.Body.String(), `"everyCall":true`) {
		t.Errorf("the default for a new call: %s", w.Body.String())
	}
	w = doJSON(t, e.as(t, ravi, "member"), http.MethodGet, path, "")
	wantCode(t, w, http.StatusOK)
	if st := notesOf(t, w.Body.Bytes()); !st.On || !st.Available || len(st.Changes) != 0 {
		t.Errorf("on, by the organization's default: %+v", st)
	}

	// Ravi, not the host, pauses them.
	w = doJSON(t, e.as(t, ravi, "member"), http.MethodPut, path, `{"on":false}`)
	wantCode(t, w, http.StatusOK)
	st := notesOf(t, w.Body.Bytes())
	if st.On || len(st.Changes) != 1 || st.Changes[0].Name != "Ravi Rao" || st.Changes[0].On || st.Changes[0].UserID == nil || *st.Changes[0].UserID != ravi {
		t.Fatalf("paused by Ravi: %+v", st)
	}
	if taker.toldCount() != 1 {
		t.Errorf("the note-taker on this server was told: %d", taker.toldCount())
	}
	sent := media.notesSent()
	if len(sent) != 1 || sent[0].On || sent[0].Changes[0].Name != "Ravi Rao" || sent[0].Changes[0].UserID != nil {
		t.Errorf("the room was told who paused them, and given nobody's account: %+v", sent)
	}
	var kept *bool
	if err := e.pool.QueryRow(ctx, `SELECT notes_on FROM calls WHERE id = $1`, call.ID).Scan(&kept); err != nil || kept == nil || *kept {
		t.Errorf("kept with the call: %v %v", kept, err)
	}

	// The same again is no change.
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{"on":false}`), http.StatusOK)
	if len(media.notesSent()) != 1 || taker.toldCount() != 1 {
		t.Errorf("pausing paused notes changed something")
	}

	// Invited but not in the room; not invited at all; and a request that says nothing.
	wantCode(t, doJSON(t, e.as(t, away, "member"), http.MethodPut, path, `{"on":true}`), http.StatusForbidden)
	wantCode(t, doJSON(t, e.as(t, other, "member"), http.MethodPut, path, `{"on":true}`), http.StatusNotFound)
	wantCode(t, doJSON(t, e.as(t, other, "member"), http.MethodGet, path, ""), http.StatusNotFound)
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{}`), http.StatusBadRequest)

	// Olga resumes them: two people are connected, so the note-taker is called in
	// if it is not there yet.
	for _, id := range []uuid.UUID{org, ravi} {
		e.exec(t, `INSERT INTO call_sessions (call_id, user_id, participant_sid, joined_at) VALUES ($1, $2, $3, NOW())`, call.ID, id, "PA_"+id.String())
	}
	w = doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{"on":true}`)
	wantCode(t, w, http.StatusOK)
	if st := notesOf(t, w.Body.Bytes()); !st.On || len(st.Changes) != 2 || st.Changes[1].Name != "Olga Organizer" {
		t.Errorf("resumed by Olga: %+v", st)
	}
	if hook.count() != 1 {
		t.Errorf("the note-taker was asked to join: %d", hook.count())
	}

	// A guest in the meeting reads the same, and cannot change it.
	link, err := e.svc.SetGuestLink(ctx, e.org.ID, call.ID, org, false, GuestLinkChange{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	guest := e.guestRouter(t)
	at := "/api/guest/" + strings.TrimPrefix(link.Path, "/join/")
	w = guestJoin(t, guest, at, `{"name":"Pat Partner"}`, "203.0.113.5")
	wantCode(t, w, http.StatusOK)
	var join GuestJoin
	_ = json.Unmarshal(w.Body.Bytes(), &join)
	wantCode(t, guestRequest(t, guest, http.MethodGet, at+"/notes", ""), http.StatusForbidden)
	w = guestRequest(t, guest, http.MethodGet, at+"/notes", "", GuestKeyHeader, join.RejoinKey)
	wantCode(t, w, http.StatusOK)
	if st := notesOf(t, w.Body.Bytes()); !st.On || len(st.Changes) != 2 || st.Changes[0].Name != "Ravi Rao" || st.Changes[0].UserID != nil {
		t.Errorf("what a guest reads: %+v", st)
	}
	wantCode(t, guestRequest(t, guest, http.MethodPut, at+"/notes", `{"on":false}`, GuestKeyHeader, join.RejoinKey), http.StatusNotFound, http.StatusMethodNotAllowed)

	// Without a note-taker, there is nothing to turn on or off.
	taker.mu.Lock()
	taker.available = false
	taker.mu.Unlock()
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{"on":false}`), http.StatusConflict)

	// Once the call is over, neither.
	taker.mu.Lock()
	taker.available = true
	taker.mu.Unlock()
	e.exec(t, `UPDATE calls SET status = 'ended', ended_at = NOW() WHERE id = $1`, call.ID)
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{"on":false}`), http.StatusConflict)
}

func TestWhoeverMakesACallOrStandUpChoosesWhetherNotesAreTaken(t *testing.T) {
	e := newLK(t)
	taker := &noteTaker{available: true, everyCall: false}
	e.svc.SetNotesHooks(taker.policy, taker.changed)
	ctx := context.Background()
	notesOn := func(id string) *bool {
		t.Helper()
		var on *bool
		if err := e.pool.QueryRow(ctx, `SELECT notes_on FROM calls WHERE id = $1`, id).Scan(&on); err != nil {
			t.Fatal(err)
		}
		return on
	}
	made := func(body string) string {
		t.Helper()
		w := doJSON(t, e.asOwner(t), http.MethodPost, "/api/calls", body)
		wantCode(t, w, http.StatusCreated, http.StatusOK)
		var c struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &c)
		return c.ID
	}
	if on := notesOn(made(`{"callType":"video","takeNotes":true}`)); on == nil || !*on {
		t.Errorf("turned on when made: %v", on)
	}
	if on := notesOn(made(`{"callType":"video","takeNotes":false}`)); on == nil || *on {
		t.Errorf("turned off when made: %v", on)
	}
	if on := notesOn(made(`{"callType":"video"}`)); on != nil {
		t.Errorf("not chosen follows the organization's default: %v", *on)
	}
	w := doJSON(t, e.asOwner(t), http.MethodGet, "/api/notes-default", "")
	if !strings.Contains(w.Body.String(), `"everyCall":false`) {
		t.Errorf("only when asked: %s", w.Body.String())
	}

	// A stand-up's choice goes to each day's call.
	body, _ := json.Marshal(map[string]any{"name": "Daily", "timeOfDay": "09:30", "timeZone": "UTC", "weekdays": everyDay, "takeNotes": true})
	code, m := e.createStanding(t, e.org.OwnerID, "owner", string(body))
	if code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	w = doJSON(t, e.asOwner(t), http.MethodGet, "/api/standing-meetings", "")
	if !strings.Contains(w.Body.String(), `"takeNotes":true`) {
		t.Errorf("the stand-up keeps it: %s", w.Body.String())
	}
	day := e.mustJoinStanding(t, e.org.OwnerID, "owner", m.ID)
	if on := notesOn(day.CallID); on == nil || !*on {
		t.Errorf("the day's call has notes on: %v", on)
	}
}

func TestTurningTheNotesOnAndOffOverAndOverIsRefused(t *testing.T) {
	e := newLK(t)
	org := e.person(t, "Olga Organizer", "member")
	ravi := e.person(t, "Ravi Rao", "member")
	call := e.call(t, org, ravi)
	e.exec(t, `UPDATE calls SET status = 'active' WHERE id = $1`, call.ID)
	e.svc.media = &fakeMedia{present: map[string]bool{call.RoomName + "/" + ravi.String(): true, call.RoomName + "/" + org.String(): true}}
	taker := &noteTaker{available: true, everyCall: true}
	e.svc.SetNotesHooks(taker.policy, taker.changed)
	path := "/api/calls/" + call.ID.String() + "/notes"
	for i := 0; i < notesBurst; i++ {
		on := i%2 == 1
		wantCode(t, doJSON(t, e.as(t, ravi, "member"), http.MethodPut, path, `{"on":`+map[bool]string{true: "true", false: "false"}[on]+`}`), http.StatusOK)
	}
	wantCode(t, doJSON(t, e.as(t, ravi, "member"), http.MethodPut, path, `{"on":false}`), http.StatusTooManyRequests)
	// Somebody else in the call can still pause them.
	wantCode(t, doJSON(t, e.as(t, org, "member"), http.MethodPut, path, `{"on":false}`), http.StatusOK)
}

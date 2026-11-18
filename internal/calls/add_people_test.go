package calls

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

// A call that two people are in, with the media server saying so and the
// webhooks having arrived, as in production.
type talking struct {
	*lkEnv
	media      *fakeMedia
	asha, ravi uuid.UUID
	call       *Call
}

func newTalking(t *testing.T) *talking {
	t.Helper()
	e := newLK(t)
	media := &fakeMedia{present: map[string]bool{}}
	e.svc.media = media
	w := &talking{lkEnv: e, media: media}
	w.asha = e.person(t, "Asha Caller", "member")
	w.ravi = e.person(t, "Ravi Callee", "member")
	w.call = e.call(t, w.asha, w.ravi)
	w.enter(t, w.asha)
	w.enter(t, w.ravi)
	e.frames.reset()
	return w
}

// enter connects somebody to the call: a token, then LiveKit saying they joined.
func (w *talking) enter(t *testing.T, who uuid.UUID) {
	t.Helper()
	wantCode(t, doJSON(t, w.as(t, who), http.MethodPost, w.path("/token"), ""), http.StatusOK)
	w.mustDeliver(t, joinedEv(w.call.RoomName, who, "PA_"+who.String()[:8], time.Now()))
	w.media.mu.Lock()
	w.media.present[w.call.RoomName+"/"+who.String()] = true
	w.media.mu.Unlock()
}

func (w *talking) path(rest string) string { return "/api/calls/" + w.call.ID.String() + rest }

func (w *talking) ring(t *testing.T, by uuid.UUID, who ...uuid.UUID) (int, string) {
	t.Helper()
	ids := make([]string, 0, len(who))
	for _, id := range who {
		ids = append(ids, id.String())
	}
	body, _ := json.Marshal(map[string]any{"userIds": ids})
	res := doJSON(t, w.as(t, by), http.MethodPost, w.path("/ring"), string(body))
	return res.Code, res.Body.String()
}

// sent is the frames of a type that went to somebody.
func (e *lkEnv) sent(to uuid.UUID, kind string) []sentFrame {
	e.frames.mu.Lock()
	defer e.frames.mu.Unlock()
	out := []sentFrame{}
	for _, f := range e.frames.frames {
		if f.To == to.String() && f.Type == kind {
			out = append(out, f)
		}
	}
	return out
}

// outcomes is how rings for somebody ended, as one person was told.
func (e *lkEnv) outcomes(to, about uuid.UUID) []string {
	out := []string{}
	for _, f := range e.sent(to, "call.ring.result") {
		if f.Payload["userId"] == about.String() {
			out = append(out, f.Payload["outcome"].(string))
		}
	}
	return out
}

func TestRingingIntoACallFollowsTheRules(t *testing.T) {
	w := newTalking(t)
	ctx := context.Background()
	sam := w.person(t, "Sam Added", "member")
	zed := w.person(t, "Zed Outside", "member")
	gone := w.person(t, "Gwen Gone", "member")
	w.exec(t, `UPDATE users SET is_active = false WHERE id = $1`, gone)
	other := testsupport.SeedOrg(t, w.pool)
	stranger := testsupport.SeedUser(t, w.pool, other, "stranger@example.test", "member")

	// Somebody who is not in the call cannot ring people into it, even though
	// they could be invited to it.
	if code, body := w.ring(t, zed, sam); code != http.StatusForbidden {
		t.Errorf("somebody outside the call rang into it: %d %s", code, body)
	}
	// Nor can somebody invited to it who has not come in.
	w.exec(t, `INSERT INTO call_participants (call_id, user_id, role) VALUES ($1, $2, 'participant')`, w.call.ID, zed)
	if code, body := w.ring(t, zed, sam); code != http.StatusForbidden {
		t.Errorf("an invitee not in the call rang into it: %d %s", code, body)
	}

	for name, who := range map[string]uuid.UUID{
		"themselves":            w.asha,
		"a stranger":            stranger,
		"somebody deactivated":  gone,
		"somebody non-existent": uuid.New(),
	} {
		if code, body := w.ring(t, w.asha, who); code != http.StatusBadRequest {
			t.Errorf("ringing %s: want 400, got %d %s", name, code, body)
		}
	}
	if code, body := w.ring(t, w.asha, w.ravi); code != http.StatusConflict {
		t.Errorf("ringing somebody already in the call: want 409, got %d %s", code, body)
	}
	if code, body := w.ring(t, w.asha); code != http.StatusBadRequest {
		t.Errorf("ringing nobody: want 400, got %d %s", code, body)
	}
	if len(w.sent(stranger, "call.invited"))+len(w.sent(gone, "call.invited"))+len(w.sent(sam, "call.invited")) > 0 {
		t.Fatal("somebody was rung by a request that was refused")
	}

	// Somebody who is not the one who started the call can ring too.
	if code, body := w.ring(t, w.ravi, sam); code != http.StatusOK {
		t.Fatalf("ringing Sam in: %d %s", code, body)
	}
	invited := w.sent(sam, "call.invited")
	if len(invited) != 1 {
		t.Fatalf("Sam should be rung once, got %d", len(invited))
	}
	p := invited[0].Payload
	if p["ongoing"] != true || p["callerName"] != "Ravi Callee" || p["callId"] != w.call.ID.String() {
		t.Errorf("the ring should say Ravi is calling into this call: %v", p)
	}
	if with, _ := p["with"].([]any); len(with) != 1 || with[0] != "Asha Caller" {
		t.Errorf("the ring should say who else is in it, got %v", p["with"])
	}
	for _, who := range []uuid.UUID{w.asha, w.ravi} {
		if len(w.sent(who, "call.adding")) != 1 {
			t.Errorf("%s should see Sam being rung", w.svc.DisplayName(ctx, who))
		}
	}
	// Rung twice at once is refused; it is still ringing.
	if code, body := w.ring(t, w.asha, sam); code != http.StatusConflict {
		t.Errorf("ringing somebody already being rung: want 409, got %d %s", code, body)
	}

	// Rung in, Sam may come in.
	wantCode(t, doJSON(t, w.as(t, sam), http.MethodPost, w.path("/token"), ""), http.StatusOK)
	if got := w.outcomes(w.asha, sam); len(got) != 1 || got[0] != RingAnswered {
		t.Errorf("Asha should hear Sam picked up, got %v", got)
	}

	// A call that is over takes nobody.
	w.exec(t, `UPDATE calls SET status = 'ended' WHERE id = $1`, w.call.ID)
	if code, body := w.ring(t, w.asha, zed); code != http.StatusConflict {
		t.Errorf("ringing into a call that is over: want 409, got %d %s", code, body)
	}
}

// The heart of it: one more person not coming never ends a call for the people
// already talking, however their ring ends, and nobody is moved to a chat.
func TestADeclinedOrUnansweredAddedRingNeverEndsTheCall(t *testing.T) {
	w := newTalking(t)
	notes := &lockedNotes{}
	w.svc.SetChatRecorder(notes)
	w.svc.ringEvery = 0
	sam := w.person(t, "Sam Added", "member")

	// Turned down.
	if code, body := w.ring(t, w.asha, sam); code != http.StatusOK {
		t.Fatalf("ring: %d %s", code, body)
	}
	wantCode(t, doJSON(t, w.as(t, sam), http.MethodPost, w.path("/decline"), `{"reason":"declined"}`), http.StatusOK)
	if w.status(t, w.call.ID) != "active" {
		t.Fatal("Sam turning the ring down ended the call")
	}
	for _, who := range []uuid.UUID{w.asha, w.ravi} {
		if got := w.outcomes(who, sam); len(got) != 1 || got[0] != RingDeclined {
			t.Errorf("everybody in the call should hear Sam declined once, got %v", got)
		}
		for _, f := range w.sent(who, "call.ended") {
			t.Errorf("somebody in the call was told it ended: %v", f.Payload)
		}
		if len(w.sent(who, "call.missed")) > 0 {
			t.Error("somebody in the call was told it was missed")
		}
	}
	// The ring timing out on Sam's other tab changes nothing more.
	wantCode(t, doJSON(t, w.as(t, sam), http.MethodPost, w.path("/decline"), `{"reason":"no_answer"}`), http.StatusOK)
	if got := w.outcomes(w.asha, sam); len(got) != 1 {
		t.Errorf("one ring ends once, got %v", got)
	}

	// Rung again, and this time nobody picks up: it rings out on its own.
	w.svc.ringFor = 150 * time.Millisecond
	if code, body := w.ring(t, w.ravi, sam); code != http.StatusOK {
		t.Fatalf("ringing again somebody who said no: %d %s", code, body)
	}
	eventually(t, "the ring runs out", func() bool { return len(w.outcomes(w.ravi, sam)) == 2 })
	if got := w.outcomes(w.ravi, sam); len(got) != 2 || got[1] != RingNoAnswer {
		t.Errorf("Ravi should hear Sam did not answer, got %v", got)
	}
	if w.status(t, w.call.ID) != "active" {
		t.Fatal("a ring running out ended the call")
	}

	// The one who started the call left: the one still in it adds somebody who
	// does not pick up, and is not dropped either.
	w.mustDeliver(t, leftEv(w.call.RoomName, w.asha, "PA_"+w.asha.String()[:8], time.Now()))
	w.media.mu.Lock()
	delete(w.media.present, w.call.RoomName+"/"+w.asha.String())
	w.media.mu.Unlock()
	w.svc.ringFor = 45 * time.Second
	tom := w.person(t, "Tom Third", "member")
	if code, body := w.ring(t, w.ravi, tom); code != http.StatusOK {
		t.Fatalf("ring: %d %s", code, body)
	}
	wantCode(t, doJSON(t, w.as(t, tom), http.MethodPost, w.path("/decline"), `{"reason":"no_answer"}`), http.StatusOK)
	if w.status(t, w.call.ID) != "active" {
		t.Fatal("an added ring rung out ended the call for the one still in it")
	}
	if notes.count() > 0 {
		t.Errorf("a call that is going was written down as missed: %v", notes.all())
	}
}

func TestCancellingARingStopsItForEverybody(t *testing.T) {
	w := newTalking(t)
	sam := w.person(t, "Sam Added", "member")
	if code, body := w.ring(t, w.asha, sam); code != http.StatusOK {
		t.Fatalf("ring: %d %s", code, body)
	}
	cancel := w.path("/ring/" + sam.String())
	// Only somebody in the call.
	wantCode(t, doJSON(t, w.as(t, w.person(t, "Zed Outside", "member")), http.MethodDelete, cancel, ""), http.StatusForbidden)
	wantCode(t, doJSON(t, w.as(t, w.ravi), http.MethodDelete, cancel, ""), http.StatusOK)

	ended := w.sent(sam, "call.ended")
	if len(ended) != 1 || ended[0].Payload["reason"] != RingCancelled || ended[0].Payload["callId"] != w.call.ID.String() {
		t.Errorf("Sam's ring should stop, got %v", ended)
	}
	if got := w.outcomes(w.asha, sam); len(got) != 1 || got[0] != RingCancelled {
		t.Errorf("Asha should see the ring stopped, got %v", got)
	}
	if w.status(t, w.call.ID) != "active" {
		t.Fatal("cancelling a ring ended the call")
	}
	wantCode(t, doJSON(t, w.as(t, w.ravi), http.MethodDelete, cancel, ""), http.StatusConflict)
	pending, err := w.svc.PendingInvitationsFor(context.Background(), sam)
	if err != nil || len(pending) != 0 {
		t.Errorf("a cancelled ring is not waiting for Sam: %v %v", pending, err)
	}
}

func TestSomebodyBusyOrRungTooOftenIsNotRung(t *testing.T) {
	w := newTalking(t)
	sam := w.person(t, "Sam Busy", "member")
	// Sam is talking to somebody else.
	other := w.lkEnv.call(t, w.person(t, "Olga Other", "member"), sam)
	w.mustDeliver(t, joinedEv(other.RoomName, sam, "PA_sam_other", time.Now()))

	code, body := w.ring(t, w.asha, sam)
	if code != http.StatusOK {
		t.Fatalf("ring: %d %s", code, body)
	}
	var res struct {
		Rung []RungPerson `json:"rung"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil || len(res.Rung) != 1 || res.Rung[0].Status != RingBusy {
		t.Fatalf("Sam should be said to be busy: %s", body)
	}
	if len(w.sent(sam, "call.invited")) > 0 {
		t.Error("Sam was rung in another call")
	}
	if got := w.outcomes(w.ravi, sam); len(got) != 1 || got[0] != RingBusy {
		t.Errorf("Ravi should hear Sam is busy, got %v", got)
	}

	// Ringing and cancelling the same person over and over stops working.
	tom := w.person(t, "Tom Often", "member")
	for i := 0; i < ringRepeatLimit; i++ {
		if code, body := w.ring(t, w.asha, tom); code != http.StatusOK {
			t.Fatalf("ring %d: %d %s", i, code, body)
		}
		wantCode(t, doJSON(t, w.as(t, w.asha), http.MethodDelete, w.path("/ring/"+tom.String()), ""), http.StatusOK)
	}
	if code, body := w.ring(t, w.asha, tom); code != http.StatusTooManyRequests {
		t.Errorf("ringing Tom a sixth time in a few minutes: want 429, got %d %s", code, body)
	}
}

func TestIntoCallWording(t *testing.T) {
	cases := map[string]struct {
		kind string
		with []string
	}{
		"Calling you into a video call":                       {"video", nil},
		"Calling you into a call with Ravi":                   {"audio", []string{"Ravi"}},
		"Calling you into a video call with Ravi and Sam":     {"video", []string{"Ravi", "Sam"}},
		"Calling you into a call with Ravi, Sam and Tom":      {"audio", []string{"Ravi", "Sam", "Tom"}},
		"Calling you into a call with Ravi, Sam and 2 others": {"audio", []string{"Ravi", "Sam", "Tom", "Uma"}},
	}
	for want, c := range cases {
		if got := intoCallBody(c.kind, c.with); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

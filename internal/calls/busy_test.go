package calls

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Somebody in a call is not rung for another: the caller hears they are busy,
// the call ends, and the conversation says what happened.
func TestCallingSomebodyAlreadyInACallSaysTheyAreBusy(t *testing.T) {
	e := newLK(t)
	e.svc.media = &fakeMedia{}
	notes := &lockedNotes{}
	e.svc.SetChatRecorder(notes)
	ctx := context.Background()
	asha := e.person(t, "Asha Busy", "member")
	ravi := e.person(t, "Ravi Caller", "member")

	// Asha is talking to the owner.
	first := e.call(t, e.org.OwnerID, asha)
	e.mustDeliver(t, joinedEv(first.RoomName, asha, "PA_asha", time.Now()))

	second := e.call(t, ravi, asha)
	e.frames.reset()
	e.svc.Ring(ctx, second, ravi.String(), "Ravi Caller", []uuid.UUID{asha})

	eventually(t, "the call is over", func() bool { return e.status(t, second.ID) == "ended" })
	eventually(t, "the conversation says so", func() bool { return notes.count() > 0 })
	if got := notes.all(); got[0] != "Missed call, in another call" {
		t.Errorf("the note says %q", got[0])
	}

	var rang, toldBusy, ended bool
	e.frames.mu.Lock()
	for _, f := range e.frames.frames {
		switch {
		case f.To == asha.String() && f.Type == "call.invited":
			rang = true
		case f.To == ravi.String() && f.Type == "call.reach" && f.Payload["busy"] == true:
			toldBusy = true
		case f.To == ravi.String() && f.Type == "call.ended" && f.Payload["reason"] == "busy":
			ended = true
		}
	}
	e.frames.mu.Unlock()
	if rang {
		t.Error("Asha was rung in the middle of another call")
	}
	if !toldBusy || !ended {
		t.Errorf("Ravi should hear Asha is busy (reach %v) and why the call ended (%v)", toldBusy, ended)
	}

	loaded, err := e.svc.GetCall(ctx, e.org.ID, second.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.MissedReason == nil || *loaded.MissedReason != "busy" {
		t.Errorf("the calls list should say busy, got %v", loaded.MissedReason)
	}
}

func TestSomebodyWhoLeftTheirLastCallIsRungAgain(t *testing.T) {
	e := newLK(t)
	ctx := context.Background()
	asha := e.person(t, "Asha Free", "member")
	ravi := e.person(t, "Ravi Again", "member")

	first := e.call(t, e.org.OwnerID, asha)
	e.mustDeliver(t, joinedEv(first.RoomName, asha, "PA_asha", time.Now().Add(-time.Minute)))
	e.mustDeliver(t, leftEv(first.RoomName, asha, "PA_asha", time.Now()))

	second := e.call(t, ravi, asha)
	e.frames.reset()
	e.svc.Ring(ctx, second, ravi.String(), "Ravi Again", []uuid.UUID{asha})
	e.frames.mu.Lock()
	defer e.frames.mu.Unlock()
	for _, f := range e.frames.frames {
		if f.To == asha.String() && f.Type == "call.invited" {
			return
		}
	}
	t.Error("Asha is free and was not rung")
}

func TestDecliningAndRingingOutLeaveDifferentMarksInTheCallsList(t *testing.T) {
	e := newLK(t)
	e.svc.media = &fakeMedia{}
	e.svc.SetChatRecorder(&lockedNotes{})
	ivy := e.person(t, "Ivy Declines", "member")
	call := e.call(t, e.org.OwnerID, ivy)
	wantCode(t, doJSON(t, e.as(t, ivy, "member"), "POST", "/api/calls/"+call.ID.String()+"/decline", `{"reason":"declined"}`), 200)
	loaded, err := e.svc.GetCall(context.Background(), e.org.ID, call.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.MissedReason == nil || *loaded.MissedReason != "declined" {
		t.Errorf("want declined, got %v", loaded.MissedReason)
	}
}

type joinHook struct {
	mu    sync.Mutex
	calls []uuid.UUID
}

func (j *joinHook) joined(_ context.Context, _, callID uuid.UUID, _ string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, callID)
}

func (j *joinHook) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.calls)
}

// The note-taker comes when it is a meeting: two people in it together. One
// person waiting for an answer, however long, gets no minutes.
func TestTheNoteTakerIsCalledOnlyOnceTwoPeopleAreInTheCall(t *testing.T) {
	e := newLK(t)
	hook := &joinHook{}
	e.svc.SetJoinHook(hook.joined)
	ivy := e.person(t, "Ivy Answers", "member")
	call := e.call(t, e.org.OwnerID, ivy)

	e.mustDeliver(t, joinedEv(call.RoomName, e.org.OwnerID, "PA_owner", time.Now().Add(-10*time.Minute)))
	if hook.count() != 0 {
		t.Fatal("the note-taker came for one person waiting alone")
	}
	e.mustDeliver(t, joinedEv(call.RoomName, ivy, "PA_ivy", time.Now()))
	if hook.count() != 1 {
		t.Fatalf("the note-taker should come once the second person is in, called %d times", hook.count())
	}
}

func TestOnePersonAfterAnotherIsNotAMeeting(t *testing.T) {
	e := newLK(t)
	hook := &joinHook{}
	e.svc.SetJoinHook(hook.joined)
	ivy := e.person(t, "Ivy Later", "member")
	call := e.call(t, e.org.OwnerID, ivy)

	e.mustDeliver(t, joinedEv(call.RoomName, e.org.OwnerID, "PA_owner", time.Now().Add(-10*time.Minute)))
	e.mustDeliver(t, leftEv(call.RoomName, e.org.OwnerID, "PA_owner", time.Now().Add(-5*time.Minute)))
	e.mustDeliver(t, joinedEv(call.RoomName, ivy, "PA_ivy", time.Now()))
	if hook.count() != 0 {
		t.Error("two people who were never in the call together made a meeting")
	}
}

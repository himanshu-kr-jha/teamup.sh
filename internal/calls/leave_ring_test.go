package calls

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/leave"
	"github.com/teamup/teamup/internal/tasks"
)

func (e *lkEnv) onLeave(t *testing.T, person uuid.UUID, startsOn string, endsOn *string, part string) {
	t.Helper()
	svc := leave.NewService(e.pool, tasks.NewService(e.pool))
	if _, err := svc.Add(context.Background(), e.org.ID, leave.Actor{ID: person}, leave.Input{
		UserID: person, StartsOn: startsOn, EndsOn: endsOn, Part: part,
	}); err != nil {
		t.Fatalf("add leave: %v", err)
	}
}

func (e *lkEnv) zone(t *testing.T, person uuid.UUID, zone string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET timezone = $2 WHERE id = $1`, person, zone); err != nil {
		t.Fatal(err)
	}
}

// Somebody on leave is not rung: the caller hears so, with the day they are
// back, and the conversation keeps a note for when they are.
func TestCallingSomebodyOnLeaveSaysSoAndDoesNotRingThem(t *testing.T) {
	e := newLK(t)
	e.svc.media = &fakeMedia{}
	notes := &lockedNotes{}
	e.svc.SetChatRecorder(notes)
	ctx := context.Background()
	asha := e.person(t, "Asha Away", "member")
	ravi := e.person(t, "Ravi Caller", "member")

	// Her own today, in Kolkata, is what counts.
	e.zone(t, asha, "Asia/Kolkata")
	loc, _ := time.LoadLocation("Asia/Kolkata")
	today := time.Now().In(loc).Format("2006-01-02")
	last := time.Now().In(loc).AddDate(0, 0, 1).Format("2006-01-02")
	e.onLeave(t, asha, today, &last, leave.PartFull)

	call := e.call(t, ravi, asha)
	e.frames.reset()
	e.svc.Ring(ctx, call, ravi.String(), "Ravi Caller", []uuid.UUID{asha})

	eventually(t, "the call is over", func() bool { return e.status(t, call.ID) == "ended" })
	eventually(t, "the conversation says so", func() bool { return notes.count() > 0 })
	if got := notes.all(); got[0] != "Missed call, on leave" {
		t.Errorf("the note says %q", got[0])
	}
	back := time.Now().In(loc).AddDate(0, 0, 2).Format("2006-01-02")
	var rang, told, ended bool
	e.frames.mu.Lock()
	for _, f := range e.frames.frames {
		switch {
		case f.To == asha.String() && f.Type == "call.invited":
			rang = true
		case f.To == ravi.String() && f.Type == "call.reach" && f.Payload["onLeave"] == true && f.Payload["backOn"] == back:
			told = true
		case f.To == ravi.String() && f.Type == "call.ended" && f.Payload["reason"] == "on_leave":
			ended = true
		}
	}
	e.frames.mu.Unlock()
	if rang {
		t.Error("Asha was rung on her leave")
	}
	if !told || !ended {
		t.Errorf("Ravi should hear Asha is on leave until %s (reach %v) and why the call ended (%v)", back, told, ended)
	}
	loaded, err := e.svc.GetCall(ctx, e.org.ID, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.MissedReason == nil || *loaded.MissedReason != "on_leave" {
		t.Errorf("the calls list should say on leave, got %v", loaded.MissedReason)
	}
}

// Leave for the afternoon only keeps afternoon calls away, by her own clock.
func TestHalfADayOffOnlyKeepsCallsAwayInThatHalf(t *testing.T) {
	e := newLK(t)
	ctx := context.Background()
	asha := e.person(t, "Asha Half", "member")
	ravi := e.person(t, "Ravi Caller", "member")
	e.zone(t, asha, "Asia/Kolkata")
	loc, _ := time.LoadLocation("Asia/Kolkata")
	morning := time.Date(2026, 11, 16, 10, 0, 0, 0, loc)
	e.onLeave(t, asha, "2026-11-16", strPtr("2026-11-16"), leave.PartAfternoon)

	rang := func(at time.Time) bool {
		e.svc.SetClock(func() time.Time { return at })
		call := e.call(t, ravi, asha)
		e.frames.reset()
		e.svc.Ring(ctx, call, ravi.String(), "Ravi Caller", []uuid.UUID{asha})
		e.frames.mu.Lock()
		defer e.frames.mu.Unlock()
		for _, f := range e.frames.frames {
			if f.To == asha.String() && f.Type == "call.invited" {
				return true
			}
		}
		return false
	}
	if !rang(morning) {
		t.Error("in the morning she is working, and is rung")
	}
	if rang(morning.Add(5 * time.Hour)) {
		t.Error("at three in the afternoon she is off, and is not rung")
	}
}

func strPtr(s string) *string { return &s }

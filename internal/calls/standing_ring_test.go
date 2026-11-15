package calls

import (
	"context"
	"testing"
	"time"

	"github.com/teamup/teamup/internal/leave"
	"github.com/teamup/teamup/internal/tasks"
)

func (e *lkEnv) standingRings(t *testing.T) map[string]bool {
	t.Helper()
	rung := map[string]bool{}
	e.frames.mu.Lock()
	defer e.frames.mu.Unlock()
	for _, f := range e.frames.frames {
		if f.Type == "call.invited" && f.Payload["kind"] == "standing" {
			rung[f.To] = true
		}
	}
	return rung
}

func TestAStandUpRingsEverybodyItIsForAsItStartsOnce(t *testing.T) {
	e := newLK(t)
	ctx := context.Background()
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	if _, err := e.pool.Exec(ctx, `UPDATE standing_meetings SET created_at = '2026-01-01' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	ann := e.person(t, "Ann Archer", "member")
	bob := e.person(t, "Bob Away", "member")
	cat := e.person(t, "Cat Early", "member")
	e.onLeave(t, bob, "2026-03-02", strPtr("2026-03-02"), leave.PartFull)

	// Cat opened the room a few minutes early and is in it.
	early := time.Date(2026, 3, 2, 8, 57, 0, 0, time.UTC)
	e.svc.SetClock(func() time.Time { return early })
	j := e.mustJoinStanding(t, cat, "member", m.ID)
	e.mustDeliver(t, joinedEv(j.RoomName, cat, "PA_cat", early))

	e.frames.reset()
	if n, err := e.svc.SweepStandingRings(ctx, early); err != nil || n != 0 {
		t.Fatalf("before the start nobody is rung, got %d (%v)", n, err)
	}
	start := time.Date(2026, 3, 2, 9, 0, 20, 0, time.UTC)
	if _, err := e.svc.SweepStandingRings(ctx, start); err != nil {
		t.Fatal(err)
	}
	rung := e.standingRings(t)
	if !rung[ann.String()] || !rung[e.org.OwnerID.String()] {
		t.Errorf("Ann and the owner are rung, got %v", rung)
	}
	if rung[bob.String()] {
		t.Error("Bob is on leave and is not rung")
	}
	if rung[cat.String()] {
		t.Error("Cat is already there and is not rung")
	}

	// Once a day, however many sweeps or replicas.
	e.frames.reset()
	if n, _ := e.svc.SweepStandingRings(ctx, start.Add(30*time.Second)); n != 0 || len(e.standingRings(t)) != 0 {
		t.Errorf("rung once, got %d more", n)
	}
	// It rang into the day's one room, the one Cat is in.
	if got := e.callCount(t); got != 1 {
		t.Errorf("one occurrence, got %d calls", got)
	}
}

func TestAStandUpDoesNotRingLateOnAHolidayOrOnADayItDoesNotRun(t *testing.T) {
	e := newLK(t)
	ctx := context.Background()
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	if _, err := e.pool.Exec(ctx, `UPDATE standing_meetings SET created_at = '2026-01-01' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	e.person(t, "Ann Archer", "member")

	// Ten minutes in is too late to ring anybody into it.
	if n, _ := e.svc.SweepStandingRings(ctx, time.Date(2026, 3, 2, 9, 10, 0, 0, time.UTC)); n != 0 {
		t.Errorf("too late, got %d rung", n)
	}
	svc := leave.NewService(e.pool, tasks.NewService(e.pool))
	if _, err := svc.AddHoliday(ctx, e.org.ID, e.org.OwnerID, "2026-03-03", "Founders' day"); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.SweepStandingRings(ctx, time.Date(2026, 3, 3, 9, 1, 0, 0, time.UTC)); n != 0 {
		t.Errorf("a holiday, got %d rung", n)
	}
	if got := e.callCount(t); got != 0 {
		t.Errorf("no room was opened, got %d calls", got)
	}
}

// A day the ring opened and nobody answered is absent, not unknown: nobody asked
// to join, so no lost webhook can be hiding anybody.
func TestADayOnlyTheRingOpenedIsAbsentNotUnknown(t *testing.T) {
	e := newLK(t)
	ctx := context.Background()
	m := e.mustCreateStanding(t, "Morning scrum", "09:00", "UTC")
	for _, q := range []string{
		`UPDATE standing_meetings SET created_at = '2026-01-01'`,
		`UPDATE users SET created_at = '2026-01-01'`,
	} {
		if _, err := e.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	ann := e.person(t, "Ann Archer", "member")
	if _, err := e.pool.Exec(ctx, `UPDATE users SET created_at = '2026-01-01' WHERE id = $1`, ann); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 9, 0, 30, 0, time.UTC)
	if n, err := e.svc.SweepStandingRings(ctx, start); err != nil || n == 0 {
		t.Fatalf("rung, got %d (%v)", n, err)
	}
	e.svc.SetClock(func() time.Time { return start.Add(time.Hour) })
	days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-02")
	if len(days) != 1 {
		t.Fatalf("got %+v", days)
	}
	if p, ok := days[0].person(ann); !ok || p.Status != "absent" {
		t.Errorf("Ann never answered: absent, got %+v", p)
	}

	// Somebody asking for a way in and never arriving is lost webhooks again.
	e.mustJoinStanding(t, ann, "member", m.ID)
	days = e.attendance(t, e.org.OwnerID, "owner", m.ID, "date=2026-03-02")
	if p, _ := days[0].person(ann); p.Status != "unknown" {
		t.Errorf("asked to join and no connection reported: unknown, got %+v", p)
	}
}

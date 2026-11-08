package calendar

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type toldGuests struct {
	mu   sync.Mutex
	told []string
}

func (g *toldGuests) GuestsMeetingChanged(_ context.Context, _, callID uuid.UUID, cancelled bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	kind := "moved"
	if cancelled {
		kind = "cancelled"
	}
	g.told = append(g.told, callID.String()+" "+kind)
}

// People invited from outside hear when the meeting moves or is cancelled, and
// not when only its name or its members change.
func TestGuestsFromOutsideAreToldWhenAMeetingMovesOrIsCancelled(t *testing.T) {
	e := newEnv(t)
	org := e.person(t, "Olga Organizer", "member")
	svc := NewService(e.pool)
	guests := &toldGuests{}
	svc.SetGuestNotifier(guests)
	ctx := context.Background()
	start := time.Now().Add(24 * time.Hour).Truncate(time.Minute)
	m, _, err := svc.ScheduleMeeting(ctx, e.org.ID, org, "Pitch", start, start.Add(30*time.Minute), nil)
	if err != nil {
		t.Fatal(err)
	}
	renamed := "Pitch, round two"
	if _, _, err := svc.UpdateMeeting(ctx, e.org.ID, m.ID, org, false, meetingPatch{Title: &renamed}); err != nil {
		t.Fatal(err)
	}
	later := start.Add(time.Hour)
	end := later.Add(30 * time.Minute)
	if _, _, err := svc.UpdateMeeting(ctx, e.org.ID, m.ID, org, false, meetingPatch{StartsAt: &later, EndsAt: &end}); err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelMeeting(ctx, e.org.ID, m.ID, org, false); err != nil {
		t.Fatal(err)
	}
	want := []string{m.ID.String() + " moved", m.ID.String() + " cancelled"}
	if len(guests.told) != 2 || guests.told[0] != want[0] || guests.told[1] != want[1] {
		t.Errorf("told %v, want %v", guests.told, want)
	}
}

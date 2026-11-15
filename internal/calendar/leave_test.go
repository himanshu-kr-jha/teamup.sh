package calendar

import (
	"context"
	"testing"

	"github.com/teamup/teamup/internal/leave"
	"github.com/teamup/teamup/internal/tasks"
)

func TestAStandingMeetingIsOffYourCalendarOnYourLeaveAndOnAHoliday(t *testing.T) {
	w := newCallWorld(t)
	alice := w.person(t, "Alice Archer", "member")
	bob := w.person(t, "Bob Baker", "member")
	w.standing(t, []int{1, 2, 3, 4, 5, 6, 7})
	date := day0.Format("2006-01-02")

	svc := leave.NewService(w.pool, tasks.NewService(w.pool))
	ctx := context.Background()
	if _, err := svc.Add(ctx, w.org.ID, leave.Actor{ID: alice}, leave.Input{UserID: alice, StartsOn: date, EndsOn: &date}); err != nil {
		t.Fatal(err)
	}
	if _, ok := find(w.mustCalendar(t, w.as(t, alice, "member"), alice), "Morning scrum"); ok {
		t.Error("Alice is on leave, so the scrum is not on her calendar")
	}
	if _, ok := find(w.mustCalendar(t, w.as(t, bob, "member"), bob), "Morning scrum"); !ok {
		t.Error("Bob is not, so it still is on his")
	}

	if _, err := svc.AddHoliday(ctx, w.org.ID, w.org.OwnerID, date, "Founders' day"); err != nil {
		t.Fatal(err)
	}
	if _, ok := find(w.mustCalendar(t, w.as(t, bob, "member"), bob), "Morning scrum"); ok {
		t.Error("on a holiday it is on nobody's calendar")
	}
}

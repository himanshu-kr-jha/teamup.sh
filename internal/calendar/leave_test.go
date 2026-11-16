package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

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

// Leave and holidays are on the calendar itself, as days, for whoever may read it.
func TestLeaveAndHolidaysAreOnTheCalendarAsDays(t *testing.T) {
	w := newCallWorld(t)
	alice := w.person(t, "Alice Archer", "member")
	bob := w.person(t, "Bob Baker", "member")
	date := day0.Format("2006-01-02")
	ctx := context.Background()
	svc := leave.NewService(w.pool, tasks.NewService(w.pool))
	if _, err := svc.Add(ctx, w.org.ID, leave.Actor{ID: alice}, leave.Input{UserID: alice, StartsOn: date, EndsOn: &date, Part: leave.PartMorning}); err != nil {
		t.Fatal(err)
	}
	// Leave from long ago, with no end yet, still covers the day.
	long := day0.AddDate(0, -2, 0).Format("2006-01-02")
	if _, err := svc.Add(ctx, w.org.ID, leave.Actor{ID: bob}, leave.Input{UserID: bob, StartsOn: long}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddHoliday(ctx, w.org.ID, w.org.OwnerID, date, "Founders' day"); err != nil {
		t.Fatal(err)
	}

	type mark struct{ Kind, From, To, Part, Name string }
	days := func(viewer, who uuid.UUID) []mark {
		t.Helper()
		res := call(t, w.as(t, viewer, "member"), http.MethodGet, "/api/calendar?userId="+who.String()+"&"+window(), nil)
		want(t, res, http.StatusOK)
		var body struct{ Days []mark }
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Days
	}

	has := func(marks []mark, m mark) bool {
		for _, x := range marks {
			if x == m {
				return true
			}
		}
		return false
	}
	holiday := mark{Kind: "holiday", From: date, To: date, Name: "Founders' day"}
	got := days(alice, alice)
	if len(got) != 2 || !has(got, mark{Kind: "leave", From: date, To: date, Part: "morning"}) || !has(got, holiday) {
		t.Errorf("Alice's calendar has her morning off and the holiday, got %+v", got)
	}
	bobs := days(bob, bob)
	if len(bobs) != 2 || !has(bobs, mark{Kind: "leave", From: long, Part: "full"}) || !has(bobs, holiday) {
		t.Errorf("Bob's open-ended leave and the holiday, got %+v", bobs)
	}
	// Somebody who may not read Bob's calendar learns nothing of his leave from it.
	if res := call(t, w.as(t, alice, "member"), http.MethodGet, "/api/calendar?userId="+bob.String()+"&"+window(), nil); res.Code != http.StatusForbidden {
		t.Errorf("Alice reading Bob's calendar: %d", res.Code)
	}
}

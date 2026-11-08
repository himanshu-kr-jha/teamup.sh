package calendar

// A meeting that runs past its scheduled end shows as extended on the calendar of
// whoever was still in it: growing while it runs, and to when they left once it is
// over. Everybody else keeps the scheduled time.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

type extendedItem struct {
	Title           string     `json:"title"`
	StartsAt        time.Time  `json:"startsAt"`
	EndsAt          time.Time  `json:"endsAt"`
	ScheduledEndsAt *time.Time `json:"scheduledEndsAt"`
	Running         bool       `json:"running"`
}

func (e *env) meetingAt(t *testing.T, who uuid.UUID, now time.Time, title string) extendedItem {
	t.Helper()
	w := call(t, e.asAt(t, who, "member", now), http.MethodGet, "/api/calendar?userId="+who.String()+"&"+window(), nil)
	want(t, w, http.StatusOK)
	var body struct {
		Items []extendedItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	for _, it := range body.Items {
		if it.Title == title {
			return it
		}
	}
	t.Fatalf("%q is not on the calendar: %s", title, w.Body.String())
	return extendedItem{}
}

// running puts the meeting in progress since start, with these people connected from then.
func (e *env) running(t *testing.T, id string, start time.Time, people ...uuid.UUID) {
	t.Helper()
	e.exec(t, `UPDATE calls SET status = 'active', started_at = $2 WHERE id = $1`, id, start)
	for _, p := range people {
		e.exec(t, `UPDATE call_participants SET joined_at = $3, left_at = NULL WHERE call_id = $1 AND user_id = $2`, id, p, start)
	}
}

func (e *env) left(t *testing.T, id string, who uuid.UUID, at time.Time) {
	t.Helper()
	e.exec(t, `UPDATE call_participants SET left_at = $3 WHERE call_id = $1 AND user_id = $2`, id, who, at)
}

func (e *env) ended(t *testing.T, id string, at time.Time) {
	t.Helper()
	e.exec(t, `UPDATE calls SET status = 'ended', ended_at = $2 WHERE id = $1`, id, at)
}

func TestAMeetingStillRunningPastItsEndGrowsOnTheCalendarOfWhoeverIsInIt(t *testing.T) {
	e := newEnv(t)
	orla := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	id := e.mustSchedule(t, orla, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID
	e.running(t, id, at(10, 0), orla, bob)

	got := e.meetingAt(t, orla, at(11, 12), "Planning")
	if !near(got.StartsAt, at(10, 0)) || !near(got.EndsAt, at(11, 12)) || !got.Running {
		t.Errorf("10:00 until now, still running, got %+v", got)
	}
	if got.ScheduledEndsAt == nil || !near(*got.ScheduledEndsAt, at(11, 0)) {
		t.Errorf("it says it was scheduled until 11:00, got %v", got.ScheduledEndsAt)
	}

	// A little later it has grown.
	if later := e.meetingAt(t, orla, at(11, 20), "Planning"); !near(later.EndsAt, at(11, 20)) {
		t.Errorf("it grows with the clock, got %+v", later)
	}

	// Somebody looking for a slot sees Orla taken during the overrun.
	carol := e.person(t, "Carol Chen", "member")
	w := call(t, e.asAt(t, carol, "member", at(11, 12)), http.MethodGet,
		"/api/calendar/availability?userIds="+orla.String()+"&from="+at(11, 5).Format(time.RFC3339)+"&to="+at(11, 10).Format(time.RFC3339), nil)
	want(t, w, http.StatusOK)
	var avail struct {
		People []struct {
			Items []struct {
				Kind     string    `json:"kind"`
				StartsAt time.Time `json:"startsAt"`
				EndsAt   time.Time `json:"endsAt"`
			} `json:"items"`
		} `json:"people"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &avail); err != nil {
		t.Fatalf("decode availability %s: %v", w.Body.String(), err)
	}
	busy := false
	for _, p := range avail.People {
		for _, it := range p.Items {
			if it.Kind == "busy" && !it.StartsAt.After(at(11, 5)) && !it.EndsAt.Before(at(11, 10)) {
				busy = true
			}
		}
	}
	if !busy {
		t.Errorf("Orla is busy from 11:05 to 11:10: %s", w.Body.String())
	}
}

func TestAMeetingThatRanOverShowsUntilYouLeftOnceItIsOver(t *testing.T) {
	e := newEnv(t)
	orla := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	id := e.mustSchedule(t, orla, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID
	e.running(t, id, at(10, 0), orla, bob)
	e.left(t, id, bob, at(10, 55))
	e.left(t, id, orla, at(11, 20))
	e.ended(t, id, at(11, 20))

	got := e.meetingAt(t, orla, at(12, 0), "Planning")
	if !near(got.EndsAt, at(11, 20)) || got.Running || got.ScheduledEndsAt == nil || !near(*got.ScheduledEndsAt, at(11, 0)) {
		t.Errorf("Orla stayed until 11:20, 20 minutes over, got %+v", got)
	}

	// Bob left before the end, so for him it did not run over.
	if bobs := e.meetingAt(t, bob, at(12, 0), "Planning"); !near(bobs.EndsAt, at(10, 55)) || bobs.ScheduledEndsAt != nil {
		t.Errorf("Bob left at 10:55 and was not in the overrun, got %+v", bobs)
	}
}

func TestAMeetingThatEndedEarlyIsNotExtended(t *testing.T) {
	e := newEnv(t)
	orla := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	id := e.mustSchedule(t, orla, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID
	e.running(t, id, at(10, 0), orla, bob)
	e.left(t, id, orla, at(10, 40))
	e.left(t, id, bob, at(10, 40))
	e.ended(t, id, at(10, 40))

	if got := e.meetingAt(t, orla, at(12, 0), "Planning"); got.ScheduledEndsAt != nil || !near(got.EndsAt, at(10, 40)) {
		t.Errorf("finished at 10:40, nothing extended, got %+v", got)
	}
}

func TestAMeetingInProgressBeforeItsEndIsNotExtendedAndSomebodyWhoNeverCameKeepsTheSlot(t *testing.T) {
	e := newEnv(t)
	orla := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	id := e.mustSchedule(t, orla, "member", "Planning", at(10, 0), at(11, 0), bob).meeting.ID
	e.running(t, id, at(10, 0), orla)

	if got := e.meetingAt(t, orla, at(10, 30), "Planning"); !near(got.EndsAt, at(11, 0)) || got.ScheduledEndsAt != nil {
		t.Errorf("on time so far, got %+v", got)
	}
	if got := e.meetingAt(t, bob, at(11, 12), "Planning"); !near(got.StartsAt, at(10, 0)) || !near(got.EndsAt, at(11, 0)) || got.ScheduledEndsAt != nil {
		t.Errorf("Bob never came, so his calendar keeps 10:00 to 11:00, got %+v", got)
	}
}

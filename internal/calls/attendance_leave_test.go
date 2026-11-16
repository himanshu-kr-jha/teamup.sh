package calls

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/leave"
	"github.com/teamup/teamup/internal/tasks"
)

type leaveMark struct {
	Part        string `json:"part"`
	AfterTheDay bool   `json:"afterTheDay"`
	RecordedBy  *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"recordedBy"`
}

type attendeeWithLeave struct {
	UserID string     `json:"userId"`
	Status string     `json:"status"`
	Leave  *leaveMark `json:"leave"`
}

func (w *scrumWorld) leaveFor(t *testing.T, actor, person uuid.UUID, part string) {
	t.Helper()
	svc := leave.NewService(w.pool, tasks.NewService(w.pool))
	date := w.date
	if _, err := svc.Add(context.Background(), w.org.ID, leave.Actor{ID: actor}, leave.Input{
		UserID: person, StartsOn: date, EndsOn: &date, Part: part,
	}); err != nil {
		t.Fatalf("add leave: %v", err)
	}
}

func (w *scrumWorld) rawDay(t *testing.T) (string, []attendeeWithLeave) {
	t.Helper()
	w2 := doJSON(t, w.as(t, w.org.OwnerID, "owner"), "GET", "/api/standing-meetings/"+w.meeting.ID+"/attendance?date="+w.date, "")
	var body struct {
		Days []struct {
			Holiday string              `json:"holiday"`
			People  []attendeeWithLeave `json:"people"`
		} `json:"days"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w2.Body.String(), err)
	}
	if len(body.Days) != 1 {
		t.Fatalf("one day, got %s", w2.Body.String())
	}
	return body.Days[0].Holiday, body.Days[0].People
}

func find(people []attendeeWithLeave, id uuid.UUID) attendeeWithLeave {
	for _, p := range people {
		if p.UserID == id.String() {
			return p
		}
	}
	return attendeeWithLeave{}
}

func TestSomebodyOnLeaveIsOnLeaveNotAbsentAndLeaveAddedAfterwardsSaysSo(t *testing.T) {
	w := newScrumWorld(t)
	// The manager records Abe's leave now, after the 09:00 scrum.
	w.leaveFor(t, w.mgr, w.absent, leave.PartFull)
	// Pin "now" to an hour after the scrum: the database clock is the wall clock, and
	// before 09:00 in Kolkata the scrum has not yet happened.
	if _, err := w.pool.Exec(context.Background(),
		`UPDATE leave_changes SET created_at = $1 WHERE leave_id IN (SELECT id FROM leave_periods WHERE user_id = $2)`,
		w.start.Add(time.Hour), w.absent); err != nil {
		t.Fatal(err)
	}
	// Tia planned the morning off before: put her record before the meeting.
	w.leaveFor(t, w.tokenOnly, w.tokenOnly, leave.PartMorning)
	if _, err := w.pool.Exec(context.Background(),
		`UPDATE leave_changes SET created_at = $1 WHERE leave_id IN (SELECT id FROM leave_periods WHERE user_id = $2)`,
		w.start.Add(-48*time.Hour), w.tokenOnly); err != nil {
		t.Fatal(err)
	}
	// Lena came, and leave on a day you came changes nothing.
	w.leaveFor(t, w.late, w.late, leave.PartFull)

	_, people := w.rawDay(t)
	abe := find(people, w.absent)
	if abe.Status != "on_leave" || abe.Leave == nil || !abe.Leave.AfterTheDay || abe.Leave.RecordedBy == nil || abe.Leave.RecordedBy.ID != w.mgr.String() {
		t.Errorf("Abe is on leave, recorded by the manager after the scrum, got %+v %+v", abe, abe.Leave)
	}
	tia := find(people, w.tokenOnly)
	if tia.Status != "on_leave" || tia.Leave == nil || tia.Leave.AfterTheDay || tia.Leave.Part != "morning" {
		t.Errorf("Tia had the morning off, planned before, got %+v %+v", tia, tia.Leave)
	}
	if lena := find(people, w.late); lena.Status != "present" || lena.Leave != nil {
		t.Errorf("Lena came, so she is present, got %+v", lena)
	}
	if mia := find(people, w.mgr); mia.Status != "absent" {
		t.Errorf("nobody else changes, got %+v", mia)
	}
}

func TestAnAfternoonOffDoesNotExcuseAMorningMeeting(t *testing.T) {
	w := newScrumWorld(t)
	w.leaveFor(t, w.absent, w.absent, leave.PartAfternoon)
	if _, people := w.rawDay(t); find(people, w.absent).Status != "absent" {
		t.Errorf("the scrum is at 09:00, got %+v", find(people, w.absent))
	}
}

func TestOnAHolidayNobodyIsAbsentAndWhoeverCameIsPresent(t *testing.T) {
	w := newScrumWorld(t)
	svc := leave.NewService(w.pool, tasks.NewService(w.pool))
	if _, err := svc.AddHoliday(context.Background(), w.org.ID, w.org.OwnerID, w.date, "Founders' day"); err != nil {
		t.Fatal(err)
	}
	holiday, people := w.rawDay(t)
	if holiday != "Founders' day" {
		t.Errorf("the day names the holiday, got %q", holiday)
	}
	for _, p := range people {
		if p.Status == "absent" || p.Status == "on_leave" {
			t.Errorf("nobody is absent on a holiday, got %+v", p)
		}
	}
	if find(people, w.late).Status != "present" || find(people, w.absent).Status != "holiday" {
		t.Errorf("Lena came, Abe had the day off, got %+v", people)
	}
}

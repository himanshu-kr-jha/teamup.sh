package calls

// Ticket 03: standing meetings and daily attendance.
// Decisions 12 to 15 in .scratch/calendar-and-meetings/spec.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

type standingMeeting struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	TimeOfDay string `json:"timeOfDay"`
	TimeZone  string `json:"timeZone"`
	Weekdays  []int  `json:"weekdays"`
}

type standingJoin struct {
	CallID   string `json:"callId"`
	RoomName string `json:"roomName"`
	Token    string `json:"token"`
	Date     string `json:"date"`
}

func standingBody(name, timeOfDay, zone string, weekdays ...int) string {
	b, _ := json.Marshal(map[string]any{"name": name, "timeOfDay": timeOfDay, "timeZone": zone, "weekdays": weekdays})
	return string(b)
}

var everyDay = []int{1, 2, 3, 4, 5, 6, 7} // ISO weekdays, Monday is 1

func (e *lkEnv) createStanding(t *testing.T, as uuid.UUID, role, body string) (int, standingMeeting) {
	t.Helper()
	w := doJSON(t, e.as(t, as, role), http.MethodPost, "/api/standing-meetings", body)
	var m standingMeeting
	if is2xx(w) {
		if err := json.Unmarshal(inner(w.Body.Bytes(), "standingMeeting"), &m); err != nil || m.ID == "" {
			t.Fatalf("decode standing meeting %q: %v", w.Body.String(), err)
		}
	}
	return w.Code, m
}

func (e *lkEnv) mustCreateStanding(t *testing.T, name, timeOfDay, zone string) standingMeeting {
	t.Helper()
	code, m := e.createStanding(t, e.org.OwnerID, "owner", standingBody(name, timeOfDay, zone, everyDay...))
	if code != http.StatusCreated {
		t.Fatalf("the owner may create a standing meeting, got %d", code)
	}
	return m
}

func (e *lkEnv) joinStanding(t *testing.T, as uuid.UUID, role, id string) (int, standingJoin) {
	t.Helper()
	w := doJSON(t, e.as(t, as, role), http.MethodPost, "/api/standing-meetings/"+id+"/join", "")
	var j standingJoin
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
			t.Fatalf("decode join %q: %v", w.Body.String(), err)
		}
	}
	return w.Code, j
}

func (e *lkEnv) mustJoinStanding(t *testing.T, as uuid.UUID, role, id string) standingJoin {
	t.Helper()
	code, j := e.joinStanding(t, as, role, id)
	if code != http.StatusOK || j.CallID == "" || j.RoomName == "" {
		t.Fatalf("joining a standing meeting should return the occurrence, got %d %+v", code, j)
	}
	return j
}

func (e *lkEnv) callCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM calls WHERE org_id = $1`, e.org.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func today(zone string) string {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		panic(err)
	}
	return time.Now().In(loc).Format("2006-01-02")
}

// ── who manages them ──────────────────────────────────────────────────────

func TestOnlyAnAdminOrOwnerMayCreateEditOrArchiveAStandingMeeting(t *testing.T) {
	e := newLK(t)
	admin := e.person(t, "Admin Ada", "admin")
	manager := e.person(t, "Manager Max", "manager")
	member := e.person(t, "Member Mo", "member")
	body := standingBody("Morning scrum", "09:30", "Asia/Kolkata", 1, 2, 3, 4, 5)

	for name, c := range map[string]struct {
		id   uuid.UUID
		role string
	}{"manager": {manager, "manager"}, "member": {member, "member"}} {
		if code, _ := e.createStanding(t, c.id, c.role, body); code != http.StatusForbidden {
			t.Errorf("%s must not create, got %d", name, code)
		}
	}
	if n := e.callCount(t); n != 0 {
		t.Errorf("a refused create makes nothing, got %d calls", n)
	}

	code, byAdmin := e.createStanding(t, admin, "admin", body)
	if code != http.StatusCreated {
		t.Fatalf("an admin creates, got %d", code)
	}
	_, byOwner := e.createStanding(t, e.org.OwnerID, "owner", standingBody("Evening scrum", "17:30", "Asia/Kolkata", 1, 2, 3, 4, 5))
	if byOwner.ID == "" || byAdmin.Name != "Morning scrum" || byAdmin.TimeOfDay != "09:30" || byAdmin.TimeZone != "Asia/Kolkata" {
		t.Errorf("the meeting is stored as sent, got %+v / %+v", byAdmin, byOwner)
	}

	for name, who := range map[string]struct {
		id   uuid.UUID
		role string
	}{"manager": {manager, "manager"}, "member": {member, "member"}} {
		patch := doJSON(t, e.as(t, who.id, who.role), http.MethodPatch, "/api/standing-meetings/"+byAdmin.ID, `{"name":"Hijacked"}`)
		del := doJSON(t, e.as(t, who.id, who.role), http.MethodDelete, "/api/standing-meetings/"+byAdmin.ID, "")
		if patch.Code != http.StatusForbidden || del.Code != http.StatusForbidden {
			t.Errorf("%s must not edit or archive, got patch %d delete %d", name, patch.Code, del.Code)
		}
	}

	patch := doJSON(t, e.as(t, admin, "admin"), http.MethodPatch, "/api/standing-meetings/"+byAdmin.ID, `{"name":"Daily standup","timeOfDay":"10:00"}`)
	wantCode(t, patch, http.StatusOK)
	list := e.listStanding(t, member, "member")
	got := map[string]standingMeeting{}
	for _, m := range list {
		got[m.ID] = m
	}
	if got[byAdmin.ID].Name != "Daily standup" || got[byAdmin.ID].TimeOfDay != "10:00" {
		t.Errorf("an edit is visible to everybody, got %+v", got[byAdmin.ID])
	}
}

func (e *lkEnv) listStanding(t *testing.T, as uuid.UUID, role string) []standingMeeting {
	t.Helper()
	w := doJSON(t, e.as(t, as, role), http.MethodGet, "/api/standing-meetings", "")
	wantCode(t, w, http.StatusOK)
	var body struct {
		StandingMeetings []standingMeeting `json:"standingMeetings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list %q: %v", w.Body.String(), err)
	}
	return body.StandingMeetings
}

func TestAStandingMeetingIsValidated(t *testing.T) {
	e := newLK(t)
	for name, body := range map[string]string{
		"no name":            standingBody("", "09:30", "Asia/Kolkata", 1),
		"a time that is not": standingBody("Scrum", "25:61", "Asia/Kolkata", 1),
		"a zone that is not": standingBody("Scrum", "09:30", "Mars/Olympus_Mons", 1),
		"no weekdays":        standingBody("Scrum", "09:30", "Asia/Kolkata"),
		"a weekday of eight": standingBody("Scrum", "09:30", "Asia/Kolkata", 8),
		"a weekday of zero":  standingBody("Scrum", "09:30", "Asia/Kolkata", 0),
	} {
		if code, _ := e.createStanding(t, e.org.OwnerID, "owner", body); code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, code)
		}
	}
	if n := len(e.listStanding(t, e.org.OwnerID, "owner")); n != 0 {
		t.Errorf("nothing invalid was stored, got %d", n)
	}
}

func TestAnotherOrganizationNeverSeesOrJoinsOrReadsAStandingMeeting(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:30", "Asia/Kolkata")
	other := testsupport.SeedOrg(t, e.pool)
	r := routerFor(t, e.svc, other.ID, other.OwnerID, "owner")

	var list struct {
		StandingMeetings []standingMeeting `json:"standingMeetings"`
	}
	w := doJSON(t, r, http.MethodGet, "/api/standing-meetings", "")
	wantCode(t, w, http.StatusOK)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.StandingMeetings) != 0 {
		t.Errorf("another organization's rooms are invisible, got %+v", list.StandingMeetings)
	}
	for name, c := range map[string][2]string{
		"join":       {http.MethodPost, "/api/standing-meetings/" + m.ID + "/join"},
		"edit":       {http.MethodPatch, "/api/standing-meetings/" + m.ID},
		"archive":    {http.MethodDelete, "/api/standing-meetings/" + m.ID},
		"attendance": {http.MethodGet, "/api/standing-meetings/" + m.ID + "/attendance?date=" + today("Asia/Kolkata")},
	} {
		if got := doJSON(t, r, c[0], c[1], `{"name":"x"}`).Code; got != http.StatusNotFound {
			t.Errorf("%s from another organization: want 404, got %d", name, got)
		}
	}
}

// ── joining and occurrences ───────────────────────────────────────────────

func TestAnybodyInTheOrganizationJoinsWithoutKnocking(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:30", "Asia/Kolkata")
	for name, role := range map[string]string{"Member Mo": "member", "Manager Max": "manager", "Admin Ada": "admin"} {
		id := e.person(t, name, role)
		code, j := e.joinStanding(t, id, role, m.ID)
		if code != http.StatusOK || j.Token == "" || j.RoomName == "" {
			t.Errorf("%s joins straight away with a token, got %d %+v", name, code, j)
		}
	}
	if n := e.callCount(t); n != 1 {
		t.Errorf("everybody lands in one occurrence, got %d calls", n)
	}
}

func TestTwoPeopleJoiningAtTheSameMomentMakeOneOccurrence(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:30", "Asia/Kolkata")

	const people = 8
	ids := make([]uuid.UUID, people)
	for i := range ids {
		ids[i] = e.person(t, fmt.Sprintf("Racer %d", i), "member")
	}
	routers := make([]*gin.Engine, people)
	for i := range ids {
		routers[i] = e.as(t, ids[i], "member")
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]standingJoin, people)
	codes := make([]int, people)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			routers[i].ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/standing-meetings/"+m.ID+"/join", nil))
			codes[i] = w.Code
			_ = json.Unmarshal(w.Body.Bytes(), &results[i])
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range ids {
		if codes[i] != http.StatusOK {
			t.Fatalf("every simultaneous join succeeds, person %d got %d", i, codes[i])
		}
		if results[i].CallID != results[0].CallID {
			t.Errorf("everybody must land in the same occurrence: %s vs %s", results[i].CallID, results[0].CallID)
		}
	}
	if n := e.callCount(t); n != 1 {
		t.Errorf("one occurrence per day however many join at once, got %d calls", n)
	}
}

func TestAJoinLaterTheSameDayReusesTheOccurrenceEvenAfterTheRoomEmptied(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:30", "Asia/Kolkata")
	ann := e.person(t, "Ann Early", "member")
	bo := e.person(t, "Bo Late", "member")

	first := e.mustJoinStanding(t, ann, "member", m.ID)
	again := e.mustJoinStanding(t, ann, "member", m.ID)
	if again.CallID != first.CallID {
		t.Fatalf("the second join the same day is the same occurrence")
	}

	t0 := baseTime()
	e.mustDeliver(t, joinedEv(first.RoomName, ann, "PA_a", t0))
	e.mustDeliver(t, leftEv(first.RoomName, ann, "PA_a", t0.Add(10*time.Minute)))
	e.mustDeliver(t, roomFinishedEv(first.RoomName, t0.Add(10*time.Minute)))

	id := uuid.MustParse(first.CallID)
	call, err := e.svc.GetCall(context.Background(), e.org.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if call.Status != "ended" {
		t.Fatalf("the occurrence ends when its room empties, got status %q", call.Status)
	}

	late := e.mustJoinStanding(t, bo, "member", m.ID)
	if late.CallID != first.CallID {
		t.Errorf("a day has one record: joining later reopens it, got a new call %s", late.CallID)
	}
	call, _ = e.svc.GetCall(context.Background(), e.org.ID, id)
	if call.Status == "ended" {
		t.Errorf("the reopened occurrence is live again, got %q", call.Status)
	}
	if n := e.callCount(t); n != 1 {
		t.Errorf("still one record, got %d", n)
	}
}

func TestTheDateOfAnOccurrenceIsWorkedOutInTheMeetingsTimeZone(t *testing.T) {
	e := newLK(t)
	// UTC+14 and UTC-11 are 25 hours apart: at any instant they are on
	// different dates, which is the shape of the midnight bug.
	zones := []string{"Pacific/Kiritimati", "Pacific/Pago_Pago", "Asia/Kolkata", "UTC"}
	seen := map[string]string{}
	for _, zone := range zones {
		m := e.mustCreateStanding(t, "Scrum "+zone, "09:30", zone)
		before := today(zone)
		j := e.mustJoinStanding(t, e.org.OwnerID, "owner", m.ID)
		after := today(zone)
		if j.Date != before && j.Date != after {
			t.Errorf("%s: the occurrence date is the date in that zone (%s), got %s", zone, before, j.Date)
		}
		seen[zone] = j.Date
	}
	if seen["Pacific/Kiritimati"] == seen["Pacific/Pago_Pago"] {
		t.Errorf("the same instant is two different days in these zones, got %v", seen)
	}
}

func TestANewDayStartsANewOccurrenceAndKeepsTheOldOne(t *testing.T) {
	e := newLK(t)
	m := e.mustCreateStanding(t, "Morning scrum", "09:30", "UTC")
	ann := e.person(t, "Ann Archer", "member")

	yesterday := e.mustJoinStanding(t, ann, "member", m.ID)
	e.mustDeliver(t, joinedEv(yesterday.RoomName, ann, "PA_1", baseTime()))
	e.mustDeliver(t, leftEv(yesterday.RoomName, ann, "PA_1", baseTime().Add(5*time.Minute)))
	e.mustDeliver(t, roomFinishedEv(yesterday.RoomName, baseTime().Add(5*time.Minute)))
	moveOccurrenceToYesterday(t, e, uuid.MustParse(yesterday.CallID))

	todays := e.mustJoinStanding(t, ann, "member", m.ID)
	if todays.CallID == yesterday.CallID {
		t.Fatal("a new day is a new occurrence")
	}
	if n := e.callCount(t); n != 2 {
		t.Errorf("yesterday is kept, got %d calls", n)
	}

	// The history is still there.
	day := today("UTC")
	prev := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	got := e.attendance(t, e.org.OwnerID, "owner", m.ID, "from="+prev+"&to="+day)
	if len(got) != 2 {
		t.Fatalf("a range returns one entry per day with an occurrence, got %+v", got)
	}
}

// moveOccurrenceToYesterday shifts every date and timestamp on the calls row.
// The spec says an occurrence is one row in calls, so that is where its date
// lives; this fails loudly if it does not.
func moveOccurrenceToYesterday(t *testing.T, e *lkEnv, callID uuid.UUID) {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `
		SELECT column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'calls' AND data_type IN ('date', 'timestamp with time zone')`)
	if err != nil {
		t.Fatal(err)
	}
	var sets []string
	for rows.Next() {
		var col, typ string
		_ = rows.Scan(&col, &typ)
		if typ == "date" {
			sets = append(sets, fmt.Sprintf(`"%s" = ("%s" - interval '1 day')::date`, col, col))
		} else {
			sets = append(sets, fmt.Sprintf(`"%s" = "%s" - interval '1 day'`, col, col))
		}
	}
	rows.Close()
	if _, err := e.pool.Exec(context.Background(), `UPDATE calls SET `+strings.Join(sets, ", ")+` WHERE id = $1`, callID); err != nil {
		t.Fatalf("move occurrence: %v", err)
	}
}

// ── attendance ────────────────────────────────────────────────────────────

type attendee struct {
	UserID         string     `json:"userId"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	FirstJoinAt    *time.Time `json:"firstJoinAt"`
	LastLeaveAt    *time.Time `json:"lastLeaveAt"`
	MinutesPresent int        `json:"minutesPresent"`
	MinutesLate    int        `json:"minutesLate"`
}

type attendanceDay struct {
	Date           string     `json:"date"`
	ScheduledStart time.Time  `json:"scheduledStart"`
	People         []attendee `json:"people"`
}

func (d attendanceDay) person(id uuid.UUID) (attendee, bool) {
	for _, p := range d.People {
		if p.UserID == id.String() {
			return p, true
		}
	}
	return attendee{}, false
}

func (e *lkEnv) attendanceRaw(t *testing.T, as uuid.UUID, role, meeting, query string) (int, []attendanceDay) {
	t.Helper()
	w := doJSON(t, e.as(t, as, role), http.MethodGet, "/api/standing-meetings/"+meeting+"/attendance?"+query, "")
	var body struct {
		Days []attendanceDay `json:"days"`
	}
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode attendance %q: %v", w.Body.String(), err)
		}
	}
	return w.Code, body.Days
}

func (e *lkEnv) attendance(t *testing.T, as uuid.UUID, role, meeting, query string) []attendanceDay {
	t.Helper()
	code, days := e.attendanceRaw(t, as, role, meeting, query)
	if code != http.StatusOK {
		t.Fatalf("attendance: want 200, got %d", code)
	}
	return days
}

// scrumWorld is a morning scrum at 09:00 in Kolkata, held today, with people
// who were on time, late, early, away and so on.
type scrumWorld struct {
	*lkEnv
	meeting standingMeeting
	date    string
	start   time.Time // the scheduled start, an instant

	mgr, mgr2                                            uuid.UUID
	late, early, rejoiner, twoDevices, tokenOnly, absent uuid.UUID
	otherTeam, deactivated, joinedLater, nested          uuid.UUID
	backend, platform, sales                             uuid.UUID
}

func newScrumWorld(t *testing.T) *scrumWorld {
	t.Helper()
	e := newLK(t)
	w := &scrumWorld{lkEnv: e}
	w.meeting = e.mustCreateStanding(t, "Morning scrum", "09:00", "Asia/Kolkata")

	w.mgr = e.person(t, "Mia Manager", "manager")
	w.mgr2 = e.person(t, "Max Manager", "manager")
	w.late = e.person(t, "Late Lena", "member")
	w.early = e.person(t, "Early Eve", "member")
	w.rejoiner = e.person(t, "Rejoin Raj", "member")
	w.twoDevices = e.person(t, "Devices Dan", "member")
	w.tokenOnly = e.person(t, "Token Tia", "member")
	w.absent = e.person(t, "Absent Abe", "member")
	w.nested = e.person(t, "Nested Nia", "member")
	w.otherTeam = e.person(t, "Sales Sam", "member")
	w.deactivated = e.person(t, "Gone Gus", "member")
	w.joinedLater = e.person(t, "Newcomer Nat", "member")

	w.backend = testsupport.SeedTeam(t, e.pool, e.org, "Backend", &w.mgr, nil, w.late, w.early, w.rejoiner, w.absent, w.tokenOnly, w.deactivated, w.joinedLater)
	w.platform = testsupport.SeedTeam(t, e.pool, e.org, "Platform", nil, &w.backend, w.nested, w.twoDevices)
	w.sales = testsupport.SeedTeam(t, e.pool, e.org, "Sales", &w.mgr2, nil, w.otherTeam)

	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1`, w.deactivated); err != nil {
		t.Fatal(err)
	}
	// Joined the organization tomorrow, so was not part of it today.
	if _, err := e.pool.Exec(ctx, `UPDATE users SET created_at = NOW() + interval '2 days' WHERE id = $1`, w.joinedLater); err != nil {
		t.Fatal(err)
	}

	j := e.mustJoinStanding(t, w.late, "member", w.meeting.ID)
	w.date = j.Date
	loc, _ := time.LoadLocation("Asia/Kolkata")
	w.start, _ = time.ParseInLocation("2006-01-02 15:04", w.date+" 09:00", loc)
	room := j.RoomName
	at := func(minutes int) time.Time { return w.start.Add(time.Duration(minutes) * time.Minute) }
	// Reading it afterwards, whatever time of day the suite happens to run: before
	// the start, nobody is absent yet.
	e.svc.SetClock(func() time.Time { return w.start.Add(2 * time.Hour) })

	e.mustJoinStanding(t, w.tokenOnly, "member", w.meeting.ID) // a token, never connected

	// Lena: 7 minutes late, there for 30.
	e.mustDeliver(t, joinedEv(room, w.late, "PA_l", at(7)))
	e.mustDeliver(t, leftEv(room, w.late, "PA_l", at(37)))
	// Eve: early. Never negative late.
	e.mustDeliver(t, joinedEv(room, w.early, "PA_e", at(-5)))
	e.mustDeliver(t, leftEv(room, w.early, "PA_e", at(20)))
	// Raj: dropped and came back. 10 + 10 minutes, first 09:07, last 09:37.
	e.mustDeliver(t, joinedEv(room, w.rejoiner, "PA_r1", at(7)))
	e.mustDeliver(t, leftEv(room, w.rejoiner, "PA_r1", at(17)))
	e.mustDeliver(t, joinedEv(room, w.rejoiner, "PA_r2", at(27)))
	e.mustDeliver(t, leftEv(room, w.rejoiner, "PA_r2", at(37)))
	// Dan: phone and laptop overlapping, 09:00-09:20 and 09:10-09:30 is 30 minutes, not 40.
	e.mustDeliver(t, joinedEv(room, w.twoDevices, "PA_d1", at(0)))
	e.mustDeliver(t, leftEv(room, w.twoDevices, "PA_d1", at(20)))
	e.mustDeliver(t, joinedEv(room, w.twoDevices, "PA_d2", at(10)))
	e.mustDeliver(t, leftEv(room, w.twoDevices, "PA_d2", at(30)))
	// Nia and Sam simply came.
	e.mustDeliver(t, joinedEv(room, w.nested, "PA_n", at(2)))
	e.mustDeliver(t, leftEv(room, w.nested, "PA_n", at(12)))
	e.mustDeliver(t, joinedEv(room, w.otherTeam, "PA_s", at(1)))
	e.mustDeliver(t, leftEv(room, w.otherTeam, "PA_s", at(11)))
	return w
}

func (w *scrumWorld) day(t *testing.T, as uuid.UUID, role, extra string) attendanceDay {
	t.Helper()
	days := w.attendance(t, as, role, w.meeting.ID, "date="+w.date+extra)
	if len(days) != 1 || days[0].Date != w.date {
		t.Fatalf("a single date returns that one day, got %+v", days)
	}
	return days[0]
}

func TestAttendanceHasFirstJoinLastLeaveMinutesPresentAndMinutesLate(t *testing.T) {
	w := newScrumWorld(t)
	day := w.day(t, w.org.OwnerID, "owner", "")
	if !near(day.ScheduledStart, w.start) {
		t.Errorf("the scheduled start is 09:00 in the meeting's zone (%v), got %v", w.start, day.ScheduledStart)
	}
	at := func(m int) time.Time { return w.start.Add(time.Duration(m) * time.Minute) }

	for _, c := range []struct {
		name          string
		id            uuid.UUID
		first, last   int
		present, late int
	}{
		{"on a late arrival", w.late, 7, 37, 30, 7},
		{"early, never negative", w.early, -5, 20, 25, 0},
		{"dropped and rejoined", w.rejoiner, 7, 37, 20, 7},
		{"two overlapping devices", w.twoDevices, 0, 30, 30, 0},
	} {
		p, ok := day.person(c.id)
		if !ok || p.Status != "present" {
			t.Errorf("%s: should be present, got %+v", c.name, p)
			continue
		}
		if p.FirstJoinAt == nil || !near(*p.FirstJoinAt, at(c.first)) {
			t.Errorf("%s: first join, want %v got %v", c.name, at(c.first), p.FirstJoinAt)
		}
		if p.LastLeaveAt == nil || !near(*p.LastLeaveAt, at(c.last)) {
			t.Errorf("%s: last leave, want %v got %v", c.name, at(c.last), p.LastLeaveAt)
		}
		if p.MinutesPresent != c.present {
			t.Errorf("%s: minutes present, want %d got %d", c.name, c.present, p.MinutesPresent)
		}
		if p.MinutesLate != c.late {
			t.Errorf("%s: minutes late, want %d got %d", c.name, c.late, p.MinutesLate)
		}
	}
}

func TestAbsentIsDerivedFromEveryoneActiveWhoDidNotConnect(t *testing.T) {
	w := newScrumWorld(t)
	day := w.day(t, w.org.OwnerID, "owner", "")

	for name, id := range map[string]uuid.UUID{
		"somebody who never came":                 w.absent,
		"somebody who only got a token":           w.tokenOnly,
		"the manager, who is a person in the org": w.mgr,
	} {
		p, ok := day.person(id)
		if !ok || p.Status != "absent" || p.MinutesPresent != 0 || p.FirstJoinAt != nil {
			t.Errorf("%s should be listed as absent with no times, got %+v (listed %v)", name, p, ok)
		}
	}
	if _, ok := day.person(w.deactivated); ok {
		t.Error("a deactivated person is not expected anywhere")
	}
	if _, ok := day.person(w.joinedLater); ok {
		t.Error("somebody who was not in the organization on that date cannot be absent from it")
	}
}

func TestOnADayTheMeetingDoesNotRunNobodyIsAbsent(t *testing.T) {
	e := newLK(t)
	_ = e.person(t, "Somebody", "member")
	loc, _ := time.LoadLocation("Asia/Kolkata")
	iso := int(time.Now().In(loc).Weekday())
	if iso == 0 {
		iso = 7
	}
	var others []int
	for d := 1; d <= 7; d++ {
		if d != iso {
			others = append(others, d)
		}
	}
	code, m := e.createStanding(t, e.org.OwnerID, "owner", standingBody("Not today", "09:00", "Asia/Kolkata", others...))
	if code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	days := e.attendance(t, e.org.OwnerID, "owner", m.ID, "date="+today("Asia/Kolkata"))
	for _, d := range days {
		if len(d.People) != 0 {
			t.Errorf("the meeting is not held today, so nobody is absent from it, got %+v", d.People)
		}
	}
}

func TestWhoMayReadAttendanceAndWhatTheyGet(t *testing.T) {
	w := newScrumWorld(t)

	ids := func(d attendanceDay) map[string]bool {
		out := map[string]bool{}
		for _, p := range d.People {
			out[p.UserID] = true
		}
		return out
	}

	// Owner and admin: everybody.
	admin := w.person(t, "Admin Ada", "admin")
	for name, d := range map[string]attendanceDay{
		"owner": w.day(t, w.org.OwnerID, "owner", ""),
		"admin": w.day(t, admin, "admin", ""),
	} {
		got := ids(d)
		for label, id := range map[string]uuid.UUID{"backend": w.late, "nested": w.nested, "sales": w.otherTeam, "absent": w.absent} {
			if !got[id.String()] {
				t.Errorf("%s sees everybody, missing %s", name, label)
			}
		}
	}

	// Manager: the people they answer for, nested teams included, nobody else.
	m := ids(w.day(t, w.mgr, "manager", ""))
	for label, id := range map[string]uuid.UUID{"late": w.late, "absent": w.absent, "nested team": w.nested, "two devices": w.twoDevices} {
		if !m[id.String()] {
			t.Errorf("the manager answers for %s", label)
		}
	}
	for label, id := range map[string]uuid.UUID{"sales": w.otherTeam, "other manager": w.mgr2, "the owner": w.org.OwnerID} {
		if m[id.String()] {
			t.Errorf("the manager does not answer for %s", label)
		}
	}

	// Employee: only themselves, even when asking for another team.
	for _, extra := range []string{"", "&teamId=" + w.backend.String(), "&teamId=" + w.sales.String()} {
		d := w.day(t, w.late, "member", extra)
		if len(d.People) != 1 || d.People[0].UserID != w.late.String() || d.People[0].MinutesLate != 7 {
			t.Errorf("%q: an employee sees only themself, got %+v", extra, d.People)
		}
	}
	// Even somebody who was absent sees that about themself.
	if d := w.day(t, w.absent, "member", ""); len(d.People) != 1 || d.People[0].Status != "absent" {
		t.Errorf("an absent employee sees their own absence, got %+v", d.People)
	}
}

func TestAttendanceTeamFilterWorksLikeTheDashboard(t *testing.T) {
	w := newScrumWorld(t)

	d := w.day(t, w.org.OwnerID, "owner", "&teamId="+w.sales.String())
	if len(d.People) != 1 || d.People[0].UserID != w.otherTeam.String() {
		t.Errorf("an admin narrowing to Sales sees only Sales, got %+v", d.People)
	}
	d = w.day(t, w.mgr, "manager", "&teamId="+w.platform.String())
	for _, p := range d.People {
		if p.UserID == w.late.String() {
			t.Errorf("narrowing to the nested Platform team drops Backend-only people, got %+v", d.People)
		}
	}
	if len(d.People) != 2 {
		t.Errorf("Platform has two people, got %+v", d.People)
	}

	code, days := w.attendanceRaw(t, w.mgr, "manager", w.meeting.ID, "date="+w.date+"&teamId="+w.sales.String())
	if code != http.StatusForbidden && code != http.StatusNotFound {
		t.Errorf("a manager asking for a team they do not run: want 403 or 404, got %d", code)
	}
	if len(days) != 0 {
		t.Error("a refusal carries no data")
	}
}

func TestArchivingHidesTheRoomAndKeepsItsHistory(t *testing.T) {
	w := newScrumWorld(t)
	calls := w.callCount(t)

	member := w.late
	if code := doJSON(t, w.as(t, member, "member"), http.MethodDelete, "/api/standing-meetings/"+w.meeting.ID, "").Code; code != http.StatusForbidden {
		t.Errorf("a member cannot archive, got %d", code)
	}
	wantCode(t, doJSON(t, w.asOwner(t), http.MethodDelete, "/api/standing-meetings/"+w.meeting.ID, ""), http.StatusOK, http.StatusNoContent)

	for _, m := range w.listStanding(t, member, "member") {
		if m.ID == w.meeting.ID {
			t.Error("an archived room is hidden from the list")
		}
	}
	if code, _ := w.joinStanding(t, member, "member", w.meeting.ID); code != http.StatusNotFound && code != http.StatusGone {
		t.Errorf("an archived room cannot be joined, got %d", code)
	}
	if n := w.callCount(t); n != calls {
		t.Errorf("history is kept: %d calls before, %d after", calls, n)
	}

	day := w.day(t, w.org.OwnerID, "owner", "")
	if p, ok := day.person(w.late); !ok || p.MinutesPresent != 30 {
		t.Errorf("past attendance of an archived room is still readable, got %+v", p)
	}
}

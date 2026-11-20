package calendar

// A call on somebody's calendar is the time they were really in it, one block per
// stretch, from call_sessions. call_participants keeps one row per person per
// call, with their first join and their last leave, and a stand-up somebody left
// at 09:35 and looked into again at 20:17 is not a meeting from 08:30 to 20:17.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

type stretchItem struct {
	ID              string     `json:"id"`
	Source          string     `json:"source"`
	Title           string     `json:"title"`
	CallID          string     `json:"callId"`
	StartsAt        time.Time  `json:"startsAt"`
	EndsAt          time.Time  `json:"endsAt"`
	ScheduledEndsAt *time.Time `json:"scheduledEndsAt"`
	Running         bool       `json:"running"`
}

// blocksOf is everything titled so on somebody's calendar as they read it at now.
func (e *env) blocksOf(t *testing.T, who uuid.UUID, now time.Time, title string) []stretchItem {
	t.Helper()
	w := call(t, e.asAt(t, who, "member", now), http.MethodGet, "/api/calendar?userId="+who.String()+"&"+window(), nil)
	want(t, w, http.StatusOK)
	var body struct {
		Items []stretchItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	var out []stretchItem
	seen := map[string]bool{}
	for _, it := range body.Items {
		if seen[it.ID] {
			t.Errorf("two items share the id %q: %s", it.ID, w.Body.String())
		}
		seen[it.ID] = true
		if it.Title == title {
			out = append(out, it)
		}
	}
	return out
}

// span is one session, from and to minutes of day0; to < 0 is still connected.
type span struct{ fromH, fromM, toH, toM int }

func open(h, m int) span { return span{h, m, -1, 0} }

// sessions records somebody's connections the way LiveKit's webhooks do, and
// their call_participants row the way syncParticipant keeps it.
func (e *env) sessions(t *testing.T, callID, who uuid.UUID, spans ...span) {
	t.Helper()
	var first, last *time.Time
	stillIn := false
	for i, s := range spans {
		joined := at(s.fromH, s.fromM)
		var left *time.Time
		if s.toH >= 0 {
			l := at(s.toH, s.toM)
			left = &l
			if last == nil || l.After(*last) {
				last = &l
			}
		} else {
			stillIn = true
		}
		if first == nil || joined.Before(*first) {
			first = &joined
		}
		e.exec(t, `INSERT INTO call_sessions (call_id, user_id, participant_sid, joined_at, left_at) VALUES ($1, $2, $3, $4, $5)`,
			callID, who, "PA_"+who.String()[:8]+"_"+string(rune('a'+i)), joined, left)
	}
	if stillIn {
		last = nil
	}
	e.exec(t, `INSERT INTO call_participants (call_id, user_id, role, joined_at, left_at) VALUES ($1, $2, 'participant', $3, $4)
		ON CONFLICT (call_id, user_id) DO UPDATE SET joined_at = EXCLUDED.joined_at, left_at = EXCLUDED.left_at`,
		callID, who, first, last)
}

// standup is a daily 08:30 UTC stand-up and its occurrence on day0, ended at
// endedAt or still active when that is zero.
func (e *env) standup(t *testing.T, name string, endedAt time.Time) uuid.UUID {
	t.Helper()
	meetingID, callID := uuid.New(), uuid.New()
	e.exec(t, `INSERT INTO standing_meetings (id, org_id, name, time_of_day, time_zone, weekdays, duration_minutes, created_by, created_at)
		VALUES ($1, $2, $3, '08:30', 'UTC', '{1,2,3,4,5,6,7}', 30, $4, $5)`, meetingID, e.org.ID, name, e.org.OwnerID, day0.AddDate(0, 0, -7))
	status, ended := "active", (*time.Time)(nil)
	if !endedAt.IsZero() {
		status, ended = "ended", &endedAt
	}
	e.exec(t, `INSERT INTO calls (id, org_id, room_name, title, call_type, status, created_by, started_at, ended_at,
		scheduled_start, scheduled_end, standing_meeting_id, occurrence_date)
		VALUES ($1, $2, $3, $4, 'video', $5, $6, $7, $8, $7, $9, $10, $11::date)`,
		callID, e.org.ID, "standing-"+callID.String()[:8], name, status, e.org.OwnerID, at(8, 30), ended, at(9, 0),
		meetingID, day0.Format("2006-01-02"))
	return callID
}

// adHoc is a call somebody started at start, ended at endedAt or still active.
func (e *env) adHoc(t *testing.T, title string, start, endedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	status, ended := "active", (*time.Time)(nil)
	if !endedAt.IsZero() {
		status, ended = "ended", &endedAt
	}
	e.exec(t, `INSERT INTO calls (id, org_id, room_name, title, call_type, status, created_by, started_at, ended_at)
		VALUES ($1, $2, $3, $4, 'video', $5, $6, $7, $8)`, id, e.org.ID, "call-"+id.String()[:8], title, status, e.org.OwnerID, start, ended)
	return id
}

func wantBlocks(t *testing.T, got []stretchItem, spans ...[2]time.Time) {
	t.Helper()
	if len(got) != len(spans) {
		t.Fatalf("want %d blocks, got %d: %+v", len(spans), len(got), got)
	}
	for i, s := range spans {
		if !near(got[i].StartsAt, s[0]) || !near(got[i].EndsAt, s[1]) {
			t.Errorf("block %d: want %s to %s, got %s to %s", i,
				s[0].Format("15:04"), s[1].Format("15:04"), got[i].StartsAt.Format("15:04"), got[i].EndsAt.Format("15:04"))
		}
	}
}

func pair(fromH, fromM, toH, toM int) [2]time.Time {
	return [2]time.Time{at(fromH, fromM), at(toH, toM)}
}

func TestSomebodyWhoLooksIntoAStandUpLaterHasTwoBlocksNotOneAcrossTheDay(t *testing.T) {
	e := newEnv(t)
	ann := e.person(t, "Ann Archer", "member")
	callID := e.standup(t, "Morning", at(21, 5))
	e.sessions(t, callID, ann, span{8, 30, 9, 35}, span{20, 17, 20, 18})

	got := e.blocksOf(t, ann, at(22, 0), "Morning")
	wantBlocks(t, got, pair(8, 30, 9, 35), pair(20, 17, 20, 18))
	for _, b := range got {
		if b.Source != "standing" || b.CallID != callID.String() || b.Running {
			t.Errorf("each block is the occurrence she attended, over: %+v", b)
		}
	}
	// She attended, so the planned slot is not also there.
	if len(got) != 2 {
		t.Errorf("the 08:30 slot is replaced by what she really did: %+v", got)
	}
}

func TestTwoDevicesAtOnceAreOneBlock(t *testing.T) {
	e := newEnv(t)
	ann := e.person(t, "Ann Archer", "member")
	callID := e.adHoc(t, "Pairing", at(10, 0), at(10, 50))
	// Laptop, then the phone in companion mode, then the laptop closes first.
	e.sessions(t, callID, ann, span{10, 0, 10, 40}, span{10, 5, 10, 50})

	wantBlocks(t, e.blocksOf(t, ann, at(12, 0), "Pairing"), pair(10, 0, 10, 50))
}

func TestAReconnectIsNotALeaveButALongBreakIs(t *testing.T) {
	e := newEnv(t)
	ann := e.person(t, "Ann Archer", "member")
	callID := e.adHoc(t, "Review", at(10, 0), at(12, 0))
	// A reload at 10:20, back at 10:23; away from 10:45 to 11:30.
	e.sessions(t, callID, ann, span{10, 0, 10, 20}, span{10, 23, 10, 45}, span{11, 30, 12, 0})

	wantBlocks(t, e.blocksOf(t, ann, at(13, 0), "Review"), pair(10, 0, 10, 45), pair(11, 30, 12, 0))
}

func TestStillInACallRunsUntilNowAndOnlyThatStretchIsRunning(t *testing.T) {
	e := newEnv(t)
	ann := e.person(t, "Ann Archer", "member")
	callID := e.adHoc(t, "Incident", at(10, 0), time.Time{})
	e.sessions(t, callID, ann, span{10, 0, 10, 20}, open(10, 40))

	got := e.blocksOf(t, ann, at(11, 0), "Incident")
	wantBlocks(t, got, pair(10, 0, 10, 20), pair(10, 40, 11, 0))
	if got[0].Running || !got[1].Running {
		t.Errorf("only the stretch she is in now is running: %+v", got)
	}

	// A stand-up she is back in is running too.
	standup := e.standup(t, "Morning", time.Time{})
	e.sessions(t, standup, ann, span{8, 30, 9, 0}, open(10, 50))
	got = e.blocksOf(t, ann, at(11, 0), "Morning")
	wantBlocks(t, got, pair(8, 30, 9, 0), pair(10, 50, 11, 0))
	if got[0].Running || !got[1].Running {
		t.Errorf("only the stretch she is in now is running: %+v", got)
	}
}

func TestAMeetingOverShowsEachStretchAndOnlyTheOverrunIsExtended(t *testing.T) {
	e := newEnv(t)
	orla := e.person(t, "Organizer Orla", "member")
	id := uuid.MustParse(e.mustSchedule(t, orla, "member", "Planning", at(10, 0), at(11, 0)).meeting.ID)
	e.exec(t, `UPDATE calls SET status = 'ended', started_at = $2, ended_at = $3 WHERE id = $1`, id, at(10, 0), at(11, 15))
	e.exec(t, `DELETE FROM call_participants WHERE call_id = $1`, id)
	e.sessions(t, id, orla, span{10, 0, 10, 30}, span{10, 50, 11, 15})

	// Before its end it stands at its scheduled time.
	wantBlocks(t, e.blocksOf(t, orla, at(10, 45), "Planning"), pair(10, 0, 11, 0))

	got := e.blocksOf(t, orla, at(12, 0), "Planning")
	wantBlocks(t, got, pair(10, 0, 10, 30), pair(10, 50, 11, 15))
	if got[0].ScheduledEndsAt != nil {
		t.Errorf("the first stretch ended on time: %+v", got[0])
	}
	if got[1].ScheduledEndsAt == nil || !near(*got[1].ScheduledEndsAt, at(11, 0)) {
		t.Errorf("the second stretch ran past 11:00: %+v", got[1])
	}
}

func TestACallWithoutSessionsIsCappedRatherThanCoveringTheDay(t *testing.T) {
	e := newEnv(t)
	ann := e.person(t, "Ann Archer", "member")
	callID := e.standup(t, "Morning", at(21, 5))
	// What an old call, or one whose webhooks were lost, has: a first join and a
	// last leave, hours apart.
	e.exec(t, `INSERT INTO call_participants (call_id, user_id, role, joined_at, left_at) VALUES ($1, $2, 'participant', $3, $4)`,
		callID, ann, at(8, 30), at(20, 17))

	wantBlocks(t, e.blocksOf(t, ann, at(22, 0), "Morning"), pair(8, 30, 10, 30))

	// A short one is what it says.
	short := e.adHoc(t, "Catch-up", at(14, 0), at(14, 40))
	e.exec(t, `INSERT INTO call_participants (call_id, user_id, role, joined_at, left_at) VALUES ($1, $2, 'participant', $3, $4)`,
		short, ann, at(14, 0), at(14, 40))
	wantBlocks(t, e.blocksOf(t, ann, at(22, 0), "Catch-up"), pair(14, 0, 14, 40))
}

func TestStretchesMergeUpToTheGapAndCloseALostLeaveAtTheCallsEnd(t *testing.T) {
	t0 := time.Date(2030, 1, 7, 10, 0, 0, 0, time.UTC)
	m := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	left := func(min int) *time.Time { l := m(min); return &l }

	// Exactly the gap apart is one stretch; a minute more is two.
	if got := stretchesOf([]session{{m(0), left(20)}, {m(30), left(40)}}, false, nil, m(60)); len(got) != 1 {
		t.Errorf("ten minutes apart is one stretch, got %+v", got)
	}
	if got := stretchesOf([]session{{m(0), left(20)}, {m(31), left(40)}}, false, nil, m(60)); len(got) != 2 {
		t.Errorf("eleven minutes apart is two, got %+v", got)
	}
	// A session whose leave never came, on a call that ended, ends with it.
	ended := m(50)
	got := stretchesOf([]session{{m(0), nil}}, false, &ended, m(600))
	if len(got) != 1 || !got[0].end.Equal(ended) || got[0].open {
		t.Errorf("a lost leave ends with the call, got %+v", got)
	}
	// A device that stays on after another leaves keeps the stretch open.
	got = stretchesOf([]session{{m(0), left(30)}, {m(10), nil}}, true, nil, m(45))
	if len(got) != 1 || !got[0].end.Equal(m(45)) || !got[0].open {
		t.Errorf("still in on the second device, got %+v", got)
	}
	// Without sessions, a meeting planned longer than the cap keeps its length.
	s := fallbackStretch(m(0), left(600), false, nil, 3*time.Hour, m(700))
	if !s.end.Equal(m(180)) {
		t.Errorf("a three hour meeting is capped at three hours, got %v", s.end)
	}
}

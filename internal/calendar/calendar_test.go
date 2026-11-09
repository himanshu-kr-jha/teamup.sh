package calendar

// Ticket 04: calendar, blocks, scheduled meetings and reminders.
// Decisions 1 to 5 and 16 in .scratch/calendar-and-meetings/spec.md.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

// ── focus blocks and open slots ───────────────────────────────────────────

func TestSomebodyBlocksTimeForThemselvesAndFreesSlotsForMeetings(t *testing.T) {
	e := newEnv(t)
	alice := e.person(t, "Alice Archer", "member")

	e.block(t, alice, "member", "focus", "Deep work", at(9, 0), at(11, 0))
	e.block(t, alice, "member", "open", "Office hours", at(14, 0), at(15, 0))

	items := e.mustCalendar(t, e.as(t, alice, "member"), alice)
	focus, ok1 := find(items, "Deep work")
	open, ok2 := find(items, "Office hours")
	if !ok1 || !ok2 {
		t.Fatalf("both blocks are on my own calendar with their titles, got %+v", items)
	}
	if focus.Kind != "focus" || !near(focus.StartsAt, at(9, 0)) || !near(focus.EndsAt, at(11, 0)) {
		t.Errorf("focus block wrong: %+v", focus)
	}
	if open.Kind != "open" {
		t.Errorf("open slot wrong: %+v", open)
	}
}

func TestBlocksAreValidated(t *testing.T) {
	e := newEnv(t)
	alice := e.person(t, "Alice Archer", "member")
	r := e.as(t, alice, "member")

	good := func(m map[string]any) map[string]any {
		base := map[string]any{"kind": "focus", "title": "x", "startsAt": at(9, 0).Format(time.RFC3339), "endsAt": at(10, 0).Format(time.RFC3339)}
		for k, v := range m {
			if v == nil {
				delete(base, k)
			} else {
				base[k] = v
			}
		}
		return base
	}
	for name, body := range map[string]map[string]any{
		"ends before it starts":       good(map[string]any{"endsAt": at(8, 0).Format(time.RFC3339)}),
		"ends when it starts":         good(map[string]any{"endsAt": at(9, 0).Format(time.RFC3339)}),
		"absurdly long (thirty days)": good(map[string]any{"endsAt": at(9, 0).AddDate(0, 0, 30).Format(time.RFC3339)}),
		"a kind that does not exist":  good(map[string]any{"kind": "party"}),
		"no start":                    good(map[string]any{"startsAt": nil}),
		"no end":                      good(map[string]any{"endsAt": nil}),
		"a date that is not a date":   good(map[string]any{"startsAt": "tomorrow-ish"}),
	} {
		if w := call(t, r, http.MethodPost, "/api/calendar/blocks", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d: %s", name, w.Code, w.Body.String())
		}
	}
	if items := e.mustCalendar(t, r, alice); len(items) != 0 {
		t.Errorf("a refused block is not stored, got %+v", items)
	}
}

func TestOnlyTheOwnerOfABlockCanChangeOrRemoveIt(t *testing.T) {
	e := newEnv(t)
	alice := e.person(t, "Alice Archer", "member")
	nosy := e.person(t, "Nosy Ned", "member")
	boss := e.person(t, "Boss Bea", "manager")
	admin := e.person(t, "Admin Ada", "admin")
	testsupport.SeedTeam(t, e.pool, e.org, "Team", &boss, nil, alice)

	b := e.block(t, alice, "member", "focus", "Deep work", at(9, 0), at(11, 0))
	patch := map[string]any{"startsAt": at(13, 0).Format(time.RFC3339), "endsAt": at(14, 0).Format(time.RFC3339)}

	for name, who := range map[string]struct {
		id   uuid.UUID
		role string
	}{"a colleague": {nosy, "member"}, "the manager": {boss, "manager"}, "an org admin": {admin, "admin"}, "the owner": {e.org.OwnerID, "owner"}} {
		r := e.as(t, who.id, who.role)
		if w := call(t, r, http.MethodPatch, "/api/calendar/blocks/"+b.ID, patch); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Errorf("%s must not edit somebody's block, got %d", name, w.Code)
		}
		if w := call(t, r, http.MethodDelete, "/api/calendar/blocks/"+b.ID, nil); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Errorf("%s must not delete somebody's block, got %d", name, w.Code)
		}
	}
	items := e.mustCalendar(t, e.as(t, alice, "member"), alice)
	if it, ok := find(items, "Deep work"); !ok || !near(it.StartsAt, at(9, 0)) {
		t.Fatalf("nothing changed, got %+v", items)
	}

	r := e.as(t, alice, "member")
	want(t, call(t, r, http.MethodPatch, "/api/calendar/blocks/"+b.ID, patch), http.StatusOK)
	if it, _ := find(e.mustCalendar(t, r, alice), "Deep work"); !near(it.StartsAt, at(13, 0)) {
		t.Errorf("the owner moved it, got %+v", it)
	}
	if w := call(t, r, http.MethodPatch, "/api/calendar/blocks/"+b.ID, map[string]any{"endsAt": at(12, 0).Format(time.RFC3339)}); w.Code != http.StatusBadRequest {
		t.Errorf("an edit is validated too (end before start), got %d", w.Code)
	}
	want(t, call(t, r, http.MethodDelete, "/api/calendar/blocks/"+b.ID, nil), http.StatusOK, http.StatusNoContent)
	if _, ok := find(e.mustCalendar(t, r, alice), "Deep work"); ok {
		t.Error("deleted block is gone")
	}
}

// ── who sees what (decisions 4 and 64) ────────────────────────────────────

func TestVisibilityOnlyWithAuthority(t *testing.T) {
	e := newEnv(t)
	alice := e.person(t, "Alice Archer", "member")
	organizer := e.person(t, "Organizer Orla", "member")
	colleague := e.person(t, "Colleague Cal", "member")
	boss := e.person(t, "Boss Bea", "manager")
	otherBoss := e.person(t, "Other Boss Oz", "manager")
	admin := e.person(t, "Admin Ada", "admin")
	testsupport.SeedTeam(t, e.pool, e.org, "Alices", &boss, nil, alice)
	testsupport.SeedTeam(t, e.pool, e.org, "Others", &otherBoss, nil, organizer, colleague)

	e.block(t, alice, "member", "focus", "Deep work on payroll", at(9, 0), at(10, 0))
	e.block(t, alice, "member", "open", "Office hours secret", at(14, 0), at(15, 0))
	e.mustSchedule(t, organizer, "member", "Layoff planning", at(11, 0), at(12, 0), alice)
	seedPastCall(t, e, organizer, "Secret acquisition call", at(16, 0), at(17, 0), alice)
	titles := []string{"Deep work on payroll", "Office hours secret", "Layoff planning", "Secret acquisition call"}

	// Somebody without authority is refused the calendar outright, and the
	// refusal carries nothing of it.
	for _, c := range []struct {
		name string
		id   uuid.UUID
		role string
	}{{"a colleague", colleague, "member"}, {"another team's manager", otherBoss, "manager"}} {
		w, _ := e.calendar(t, e.as(t, c.id, c.role), alice)
		want(t, w, http.StatusForbidden)
		for _, title := range titles {
			if strings.Contains(w.Body.String(), title) {
				t.Errorf("%s must not see %q: %s", c.name, title, w.Body.String())
			}
		}
	}

	// Authority shows the detail: self, the manager above, an org admin, the owner.
	for _, c := range []struct {
		name string
		id   uuid.UUID
		role string
	}{{"alice", alice, "member"}, {"her manager", boss, "manager"}, {"an admin", admin, "admin"}, {"the owner", e.org.OwnerID, "owner"}} {
		items := e.mustCalendar(t, e.as(t, c.id, c.role), alice)
		for _, title := range titles {
			if _, ok := find(items, title); !ok {
				t.Errorf("%s should see %q, got %+v", c.name, title, items)
			}
		}
	}
}

func TestAnotherOrganizationsPeopleAndDataAreInvisible(t *testing.T) {
	e := newEnv(t)
	alice := e.person(t, "Alice Archer", "member")
	e.block(t, alice, "member", "focus", "Deep work", at(9, 0), at(10, 0))

	other := testsupport.SeedOrg(t, e.pool)
	foreign := buildRouter(t, e.pool, e.notifier, other.ID, other.OwnerID, []string{"owner"})

	if w, _ := e.calendar(t, foreign, alice); w.Code != http.StatusNotFound {
		t.Errorf("another organization's person: want 404, got %d: %s", w.Code, w.Body.String())
	}
	w := call(t, foreign, http.MethodGet, "/api/calendar/availability?userIds="+alice.String()+"&"+window(), nil)
	if w.Code == http.StatusOK && strings.Contains(w.Body.String(), alice.String()) {
		t.Errorf("availability must not reveal another organization's person: %s", w.Body.String())
	}
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Errorf("availability for a stranger: want 404 or an answer without them, got %d", w.Code)
	}
	// And a stranger cannot be invited.
	res := decodeMeeting(call(t, e.as(t, alice, "member"), http.MethodPost, "/api/meetings",
		meetingBody("Hello", at(9, 0), at(10, 0), other.OwnerID)))
	if res.code != http.StatusBadRequest && res.code != http.StatusNotFound && res.code != http.StatusUnprocessableEntity {
		t.Errorf("inviting somebody from another organization must be refused, got %d", res.code)
	}
	if items := e.mustCalendar(t, foreign, other.OwnerID); len(items) != 0 {
		t.Errorf("nothing of ours leaks onto their calendar, got %+v", items)
	}
}

func TestACalendarRangeIsValidated(t *testing.T) {
	e := newEnv(t)
	r := e.asOwner(t)
	id := e.org.OwnerID.String()
	for name, q := range map[string]string{
		"no range":       "userId=" + id,
		"to before from": "userId=" + id + "&from=" + at(10, 0).Format(time.RFC3339) + "&to=" + at(9, 0).Format(time.RFC3339),
		"a year":         "userId=" + id + "&from=" + at(0, 0).Format(time.RFC3339) + "&to=" + at(0, 0).AddDate(1, 0, 0).Format(time.RFC3339),
		"not a date":     "userId=" + id + "&from=soon&to=later",
		"not a user":     "userId=nobody&" + window(),
	} {
		if w := call(t, r, http.MethodGet, "/api/calendar?"+q, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d: %s", name, w.Code, w.Body.String())
		}
	}
	if w := call(t, r, http.MethodGet, "/api/calendar/availability?userIds="+id, nil); w.Code != http.StatusBadRequest {
		t.Errorf("availability without a range: want 400, got %d", w.Code)
	}
}

// ── calls on the calendar (decision 5) ────────────────────────────────────

// seedPastCall writes an unscheduled call that has already happened.
func seedPastCall(t *testing.T, e *env, creator uuid.UUID, title string, start, end time.Time, invitees ...uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO calls (id, org_id, room_name, title, call_type, status, started_at, ended_at, created_by)
		VALUES ($1, $2, $3, $4, 'video', 'ended', $5, $6, $7)`,
		id, e.org.ID, "call-"+id.String()[:8], title, start, end, creator); err != nil {
		t.Fatalf("seed call: %v", err)
	}
	for i, p := range append([]uuid.UUID{creator}, invitees...) {
		role := "participant"
		if i == 0 {
			role = "host"
		}
		if _, err := e.pool.Exec(ctx, `INSERT INTO call_participants (call_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, id, p, role); err != nil {
			t.Fatalf("seed participant: %v", err)
		}
	}
	return id
}

func TestAPastCallShowsOnEveryInvitesCalendarWithTheTimeTheyReallyWereInIt(t *testing.T) {
	e := newEnv(t)
	host := e.person(t, "Host Hana", "member")
	attended := e.person(t, "Attended Abe", "member")
	neverCame := e.person(t, "Never Came Nina", "member")
	uninvited := e.person(t, "Uninvited Uma", "member")

	id := seedPastCall(t, e, host, "Quick sync", at(10, 0), at(10, 30), attended, neverCame)
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE call_participants SET joined_at = $3, left_at = $4 WHERE call_id = $1 AND user_id = $2`,
		id, attended, at(10, 5), at(10, 20)); err != nil {
		t.Fatal(err)
	}

	got, ok := find(e.mustCalendar(t, e.as(t, attended, "member"), attended), "Quick sync")
	if !ok || !near(got.StartsAt, at(10, 5)) || !near(got.EndsAt, at(10, 20)) {
		t.Errorf("the person who attended sees when they were really in it (10:05-10:20), got %+v", got)
	}
	got, ok = find(e.mustCalendar(t, e.as(t, neverCame, "member"), neverCame), "Quick sync")
	if !ok || !near(got.StartsAt, at(10, 0)) || !near(got.EndsAt, at(10, 30)) {
		t.Errorf("an invitee who never connected sees the call's own time, got %+v", got)
	}
	if _, ok := find(e.mustCalendar(t, e.as(t, uninvited, "member"), uninvited), "Quick sync"); ok {
		t.Error("a call somebody was not invited to is not on their calendar")
	}
}

// ── scheduling meetings (decisions 2 and 3) ───────────────────────────────

func TestAScheduledMeetingLandsOnEveryInviteesCalendarWithoutThemDoingAnything(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	carol := e.person(t, "Carol Cook", "member")

	e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob, carol)
	for name, id := range map[string]uuid.UUID{"the organizer": organizer, "Bob": bob, "Carol": carol} {
		it, ok := find(e.mustCalendar(t, e.as(t, id, "member"), id), "Planning")
		if !ok || !near(it.StartsAt, at(10, 0)) || !near(it.EndsAt, at(11, 0)) {
			t.Errorf("%s: the meeting is on my calendar from this time to this time, got %+v", name, it)
		}
	}
}

func TestAMeetingIsValidated(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	r := e.as(t, organizer, "member")

	for name, body := range map[string]map[string]any{
		"no title":                    meetingBody("", at(10, 0), at(11, 0), bob),
		"ends before it starts":       meetingBody("Plan", at(11, 0), at(10, 0), bob),
		"ends when it starts":         meetingBody("Plan", at(10, 0), at(10, 0), bob),
		"longer than a sane meeting":  meetingBody("Plan", at(10, 0), at(10, 0).AddDate(0, 0, 3), bob),
		"somebody who is not a user":  {"title": "Plan", "startsAt": at(10, 0).Format(time.RFC3339), "endsAt": at(11, 0).Format(time.RFC3339), "inviteeIds": []string{"not-a-uuid"}},
		"somebody who does not exist": meetingBody("Plan", at(10, 0), at(11, 0), uuid.New()),
	} {
		res := decodeMeeting(call(t, r, http.MethodPost, "/api/meetings", body))
		if res.code != http.StatusBadRequest && res.code != http.StatusNotFound && res.code != http.StatusUnprocessableEntity {
			t.Errorf("%s: want a refusal, got %d: %s", name, res.code, res.raw)
		}
	}
	if items := e.mustCalendar(t, e.as(t, bob, "member"), bob); len(items) != 0 {
		t.Errorf("refused meetings are not stored, got %+v", items)
	}
}

func TestConflictsAreReportedButSchedulingStillSucceeds(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	busyFocus := e.person(t, "Focused Fay", "member")
	busyMeeting := e.person(t, "Meeting Moe", "member")
	openSlot := e.person(t, "Open Olga", "member")
	backToBack := e.person(t, "Backtoback Bo", "member")
	free := e.person(t, "Free Fred", "member")
	elsewhere := e.person(t, "Elsewhere Elle", "member")

	e.block(t, busyFocus, "member", "focus", "Secret deep work", at(10, 0), at(12, 0))
	e.mustSchedule(t, elsewhere, "member", "Secret other meeting", at(11, 0), at(12, 0), busyMeeting)
	e.block(t, openSlot, "member", "open", "Open", at(10, 0), at(18, 0))
	e.mustSchedule(t, elsewhere, "member", "Earlier meeting", at(10, 0), at(11, 0), backToBack)

	res := e.mustSchedule(t, organizer, "member", "Planning", at(11, 0), at(11, 30),
		busyFocus, busyMeeting, openSlot, backToBack, free)

	got := map[string]string{}
	for _, c := range res.conflicts {
		got[c.UserID] = c.Kind
	}
	if _, ok := got[busyFocus.String()]; !ok {
		t.Errorf("a focus block is a conflict, got %+v", res.conflicts)
	}
	if _, ok := got[busyMeeting.String()]; !ok {
		t.Errorf("another meeting is a conflict, got %+v", res.conflicts)
	}
	for name, id := range map[string]uuid.UUID{"an open slot": openSlot, "back to back": backToBack, "a free person": free} {
		if _, ok := got[id.String()]; ok {
			t.Errorf("%s is not a conflict, got %+v", name, res.conflicts)
		}
	}
	// The organizer has no authority over these people: no titles in the answer.
	for _, secret := range []string{"Secret deep work", "Secret other meeting"} {
		if strings.Contains(res.raw, secret) {
			t.Errorf("conflicts must not leak titles to somebody without authority: %s", res.raw)
		}
	}
	// It went ahead anyway.
	if _, ok := find(e.mustCalendar(t, e.as(t, busyFocus, "member"), busyFocus), "Planning"); !ok {
		t.Error("conflicts warn and do not refuse: the meeting is on the calendar")
	}
}

func TestOnlyTheOrganizerOrAnAdminMovesOrCancelsAMeeting(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	boss := e.person(t, "Boss Bea", "manager")
	admin := e.person(t, "Admin Ada", "admin")
	outsider := e.person(t, "Outsider Oz", "member")
	testsupport.SeedTeam(t, e.pool, e.org, "Bobs", &boss, nil, bob)

	m := e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting
	move := map[string]any{"startsAt": at(15, 0).Format(time.RFC3339), "endsAt": at(16, 0).Format(time.RFC3339)}

	for name, who := range map[string]struct {
		id   uuid.UUID
		role string
	}{"an invitee": {bob, "member"}, "the invitee's manager": {boss, "manager"}, "an outsider": {outsider, "member"}} {
		r := e.as(t, who.id, who.role)
		if w := call(t, r, http.MethodPatch, "/api/meetings/"+m.ID, move); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Errorf("%s must not move it, got %d", name, w.Code)
		}
		if w := call(t, r, http.MethodDelete, "/api/meetings/"+m.ID, nil); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Errorf("%s must not cancel it, got %d", name, w.Code)
		}
	}
	if it, _ := find(e.mustCalendar(t, e.as(t, bob, "member"), bob), "Planning"); !near(it.StartsAt, at(10, 0)) {
		t.Fatalf("nothing moved: %+v", it)
	}

	want(t, call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+m.ID, move), http.StatusOK)
	if it, _ := find(e.mustCalendar(t, e.as(t, bob, "member"), bob), "Planning"); !near(it.StartsAt, at(15, 0)) {
		t.Errorf("the organizer moved it, Bob sees the new time, got %+v", it)
	}
	want(t, call(t, e.as(t, admin, "admin"), http.MethodPatch, "/api/meetings/"+m.ID,
		map[string]any{"startsAt": at(17, 0).Format(time.RFC3339), "endsAt": at(18, 0).Format(time.RFC3339)}), http.StatusOK)
	if it, _ := find(e.mustCalendar(t, e.as(t, bob, "member"), bob), "Planning"); !near(it.StartsAt, at(17, 0)) {
		t.Errorf("an org admin moved it, got %+v", it)
	}
	if w := call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+m.ID,
		map[string]any{"endsAt": at(16, 0).Format(time.RFC3339)}); w.Code != http.StatusBadRequest {
		t.Errorf("moving is validated too, got %d", w.Code)
	}

	want(t, call(t, e.as(t, admin, "admin"), http.MethodDelete, "/api/meetings/"+m.ID, nil), http.StatusOK, http.StatusNoContent)
	for _, id := range []uuid.UUID{organizer, bob} {
		if _, ok := find(e.mustCalendar(t, e.as(t, id, "member"), id), "Planning"); ok {
			t.Error("a cancelled meeting is gone from every calendar")
		}
	}
}

func TestMovingAMeetingReportsTheNewConflicts(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	e.block(t, bob, "member", "focus", "Deep work", at(15, 0), at(17, 0))

	m := e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob)
	if len(m.conflicts) != 0 {
		t.Fatalf("no conflict at ten, got %+v", m.conflicts)
	}
	res := decodeMeeting(call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+m.meeting.ID,
		map[string]any{"startsAt": at(15, 30).Format(time.RFC3339), "endsAt": at(16, 0).Format(time.RFC3339)}))
	if res.code != http.StatusOK || len(res.conflicts) != 1 || res.conflicts[0].UserID != bob.String() {
		t.Errorf("moving onto a focus block warns, and still moves; got %d %+v", res.code, res.conflicts)
	}
}

// ── availability ──────────────────────────────────────────────────────────

func TestAvailabilityShowsBusyFocusAndOpenAndNeverATitle(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	carol := e.person(t, "Carol Cook", "member")
	e.block(t, bob, "member", "focus", "Bob private focus", at(9, 0), at(10, 0))
	e.block(t, bob, "member", "open", "Bob private open", at(14, 0), at(15, 0))
	e.mustSchedule(t, organizer, "member", "Private all hands", at(11, 0), at(12, 0), bob, carol)

	for _, c := range []struct {
		name string
		id   uuid.UUID
		role string
	}{{"a colleague", organizer, "member"}, {"the owner", e.org.OwnerID, "owner"}} {
		w := call(t, e.as(t, c.id, c.role), http.MethodGet,
			"/api/calendar/availability?userIds="+bob.String()+","+carol.String()+"&"+window(), nil)
		want(t, w, http.StatusOK)
		for _, secret := range []string{"Bob private focus", "Bob private open", "Private all hands"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Errorf("%s: availability is for scheduling, it carries no titles: %s", c.name, w.Body.String())
			}
		}
		for _, kind := range []string{`"focus"`, `"open"`, `"busy"`} {
			if !strings.Contains(w.Body.String(), kind) {
				t.Errorf("%s: availability should include %s: %s", c.name, kind, w.Body.String())
			}
		}
	}
}

// ── reminders and notifications (decision 16) ─────────────────────────────

func TestInvitedMovedAndCancelledEachNotifyTheInvitee(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")

	m := e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob).meeting
	if n := len(e.notificationTypes(t, bob)); n != 1 {
		t.Fatalf("Bob is told he is invited, got %v", e.notificationTypes(t, bob))
	}
	want(t, call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+m.ID,
		map[string]any{"startsAt": at(12, 0).Format(time.RFC3339), "endsAt": at(13, 0).Format(time.RFC3339)}), http.StatusOK)
	want(t, call(t, e.as(t, organizer, "member"), http.MethodDelete, "/api/meetings/"+m.ID, nil), http.StatusOK, http.StatusNoContent)

	types := e.notificationTypes(t, bob)
	if len(types) != 3 || countContaining(types, "invit") != 1 || countContaining(types, "mov") != 1 || countContaining(types, "cancel") != 1 {
		t.Errorf("invited, moved, cancelled: one notification each, got %v", types)
	}
	if got := e.notificationTypes(t, organizer); len(got) != 0 {
		t.Errorf("you are not notified about your own actions, got %v", got)
	}
}

func TestAReminderGoesOutFiveMinutesBeforeAndIsClaimedOnceEvenWithTwoReplicas(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")
	carol := e.person(t, "Carol Cook", "member")
	e.mustSchedule(t, organizer, "member", "Planning", at(10, 0), at(11, 0), bob, carol)

	if n := sweepReminders(t, e.pool, e.notifier, at(9, 30)); n != 0 {
		t.Errorf("half an hour before is too early, sent %d", n)
	}

	// Two replicas wake up at the same moment.
	var wg sync.WaitGroup
	sent := make([]int, 2)
	for i := range sent {
		wg.Add(1)
		go func() { defer wg.Done(); sent[i] = sweepReminders(t, e.pool, e.notifier, at(9, 56)) }()
	}
	wg.Wait()

	// The organizer has to be there as well, so they are reminded like anybody invited.
	for name, id := range map[string]uuid.UUID{"Orla": organizer, "Bob": bob, "Carol": carol} {
		if n := countContaining(e.notificationTypes(t, id), "remind"); n != 1 {
			t.Errorf("%s gets exactly one reminder, got %d (%v)", name, n, e.notificationTypes(t, id))
		}
	}
	if sent[0]+sent[1] != 3 {
		t.Errorf("the two sweeps together send one reminder each to two invitees and the organizer (3), got %v", sent)
	}
	if n := sweepReminders(t, e.pool, e.notifier, at(9, 58)); n != 0 {
		t.Errorf("already claimed, got %d more", n)
	}
}

func TestACancelledMeetingSendsNoReminderAndAMovedOneRemindsAgainAtTheNewTime(t *testing.T) {
	e := newEnv(t)
	organizer := e.person(t, "Organizer Orla", "member")
	bob := e.person(t, "Bob Builder", "member")

	cancelled := e.mustSchedule(t, organizer, "member", "Cancelled", at(10, 0), at(11, 0), bob).meeting
	want(t, call(t, e.as(t, organizer, "member"), http.MethodDelete, "/api/meetings/"+cancelled.ID, nil), http.StatusOK, http.StatusNoContent)
	moved := e.mustSchedule(t, organizer, "member", "Moved", at(10, 0), at(11, 0), bob).meeting

	sweepReminders(t, e.pool, e.notifier, at(9, 56))
	if n := countContaining(e.notificationTypes(t, bob), "remind"); n != 1 {
		t.Fatalf("only the live meeting is reminded, got %d (%v)", n, e.notificationTypes(t, bob))
	}

	want(t, call(t, e.as(t, organizer, "member"), http.MethodPatch, "/api/meetings/"+moved.ID,
		map[string]any{"startsAt": at(14, 0).Format(time.RFC3339), "endsAt": at(15, 0).Format(time.RFC3339)}), http.StatusOK)
	sweepReminders(t, e.pool, e.notifier, at(13, 56))
	if n := countContaining(e.notificationTypes(t, bob), "remind"); n != 2 {
		t.Errorf("a meeting that moved is reminded at its new time, got %d (%v)", n, e.notificationTypes(t, bob))
	}
}

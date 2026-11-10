package calendar

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Reported: work done in a meeting held outside TeamUp had nowhere to go. An
// entry is that, on your own calendar: its name and notes for you and your
// managers, busy time to anybody else, and a clash when a meeting is booked over it.
func TestAnEntryIsYourOwnRecordAndBusyToEverybodyElse(t *testing.T) {
	e := newEnv(t)
	alice := e.person(t, "Alice Archer", "member")
	bob := e.person(t, "Bob Builder", "member")
	r := e.as(t, alice, "member")

	w := call(t, r, http.MethodPost, "/api/calendar/blocks", map[string]any{
		"kind": "entry", "title": "Call with ACME", "details": "Agreed the pilot scope",
		"startsAt": at(10, 0).Format(time.RFC3339), "endsAt": at(11, 0).Format(time.RFC3339),
	})
	want(t, w, http.StatusCreated)

	items := e.mustCalendar(t, r, alice)
	entry, ok := find(items, "Call with ACME")
	if !ok || entry.Kind != "entry" || entry.Details != "Agreed the pilot scope" {
		t.Fatalf("the entry and its notes are on her calendar: %+v", items)
	}

	w = call(t, e.as(t, bob, "member"), http.MethodGet,
		"/api/calendar/availability?userIds="+alice.String()+"&"+window(), nil)
	want(t, w, http.StatusOK)
	for _, secret := range []string{"ACME", "pilot", `"entry"`} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("a colleague sees only that she is busy, found %s in %s", secret, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), `"busy"`) {
		t.Errorf("a colleague sees she is busy: %s", w.Body.String())
	}

	conflicts := e.mustSchedule(t, bob, "member", "Sync", at(10, 30), at(11, 30), alice).conflicts
	if len(conflicts) != 1 || conflicts[0].UserID != alice.String() || conflicts[0].Kind != "meeting" {
		t.Errorf("booking over an entry is a clash, as over a meeting: %+v", conflicts)
	}

	// An entry has to say what it is.
	w = call(t, r, http.MethodPost, "/api/calendar/blocks", map[string]any{
		"kind": "entry", "title": "  ",
		"startsAt": at(12, 0).Format(time.RFC3339), "endsAt": at(13, 0).Format(time.RFC3339),
	})
	want(t, w, http.StatusBadRequest)
}

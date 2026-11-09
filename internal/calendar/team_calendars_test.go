package calendar

// Ticket 57, decision 64: a manager opens the calendars of the people they answer
// for, read-only, and the picker offers exactly those.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

type pickerGroup struct {
	TeamID string `json:"teamId"`
	Name   string `json:"name"`
	People []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"people"`
}

func (e *env) picker(t *testing.T, who uuid.UUID, role string) []pickerGroup {
	t.Helper()
	w := call(t, e.as(t, who, role), http.MethodGet, "/api/calendar/people", nil)
	want(t, w, http.StatusOK)
	var body struct {
		Groups []pickerGroup `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Groups == nil {
		t.Fatalf("decode picker %q: %v", w.Body.String(), err)
	}
	return body.Groups
}

// names reads the picker as "Team: Name, Name" lines, which is what a person sees.
func names(groups []pickerGroup) []string {
	out := []string{}
	for _, g := range groups {
		people := []string{}
		for _, p := range g.People {
			people = append(people, p.Name)
		}
		out = append(out, g.Name+": "+strings.Join(people, ", "))
	}
	return out
}

func sameLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// teamWorld is Bea running Backend (Alice, and Ada who administers the
// organization) with Platform (Pat) nested beneath it, and Oz running Sales (Sam).
type teamWorld struct {
	*env
	bea, oz, alice, pat, sam, ada uuid.UUID
}

func newTeamWorld(t *testing.T) *teamWorld {
	t.Helper()
	e := newEnv(t)
	w := &teamWorld{env: e,
		bea:   e.person(t, "Bea Boss", "manager"),
		oz:    e.person(t, "Oz Other", "manager"),
		alice: e.person(t, "Alice Archer", "member"),
		pat:   e.person(t, "Pat Platform", "member"),
		sam:   e.person(t, "Sam Sales", "member"),
		ada:   e.person(t, "Ada Admin", "admin"),
	}
	backend := testsupport.SeedTeam(t, e.pool, e.org, "Backend", &w.bea, nil, w.alice, w.ada)
	testsupport.SeedTeam(t, e.pool, e.org, "Platform", nil, &backend, w.pat)
	testsupport.SeedTeam(t, e.pool, e.org, "Sales", &w.oz, nil, w.sam)
	return w
}

func TestWhoseCalendarSomebodyMayOpen(t *testing.T) {
	w := newTeamWorld(t)
	w.block(t, w.alice, "member", "focus", "Alice deep work", at(9, 0), at(10, 0))
	w.block(t, w.pat, "member", "focus", "Pat on call", at(9, 0), at(10, 0))
	w.block(t, w.sam, "member", "focus", "Sam prospecting", at(9, 0), at(10, 0))
	w.block(t, w.ada, "member", "focus", "Ada budget", at(9, 0), at(10, 0))
	w.mustSchedule(t, w.sam, "member", "Quarterly review", at(11, 0), at(12, 0), w.alice)

	for _, c := range []struct {
		name       string
		viewer     uuid.UUID
		role       string
		subject    uuid.UUID
		title      string
		mayOpen    bool
		whyRefused string
	}{
		{"yourself", w.alice, "member", w.alice, "Alice deep work", true, ""},
		{"a direct report", w.bea, "manager", w.alice, "Alice deep work", true, ""},
		{"a direct report's meeting somebody else organized", w.bea, "manager", w.alice, "Quarterly review", true, ""},
		{"somebody on a team nested under yours", w.bea, "manager", w.pat, "Pat on call", true, ""},
		{"anybody, with meetings.all", w.ada, "admin", w.sam, "Sam prospecting", true, ""},
		{"anybody, as the owner", w.org.OwnerID, "owner", w.pat, "Pat on call", true, ""},
		{"another team's person", w.bea, "manager", w.sam, "Sam prospecting", false, "not on a team Bea runs"},
		{"a colleague", w.alice, "member", w.pat, "Pat on call", false, "nobody's manager"},
		{"your own manager", w.alice, "member", w.bea, "", false, "authority runs down the chart, not up"},
		{"an administrator on your team", w.bea, "manager", w.ada, "Ada budget", false, "an administrator sits above the chart, as for tasks"},
	} {
		got, items := w.calendar(t, w.as(t, c.viewer, c.role), c.subject)
		if !c.mayOpen {
			if got.Code != http.StatusForbidden {
				t.Errorf("%s (%s): want 403, got %d: %s", c.name, c.whyRefused, got.Code, got.Body.String())
			}
			if c.title != "" && strings.Contains(got.Body.String(), c.title) {
				t.Errorf("%s: the refusal leaks %q: %s", c.name, c.title, got.Body.String())
			}
			continue
		}
		if got.Code != http.StatusOK {
			t.Errorf("%s: want 200, got %d: %s", c.name, got.Code, got.Body.String())
			continue
		}
		if it, ok := find(items, c.title); !ok || it.Source == "" {
			t.Errorf("%s: %q is there in full, got %+v", c.name, c.title, items)
		}
	}
}

func TestAReportsCalendarIsReadOnlyForTheirManager(t *testing.T) {
	w := newTeamWorld(t)
	b := w.block(t, w.alice, "member", "focus", "Alice deep work", at(9, 0), at(10, 0))
	other := w.block(t, w.pat, "member", "open", "Pat office hours", at(14, 0), at(15, 0))
	moved := map[string]any{"startsAt": at(13, 0).Format(time.RFC3339), "endsAt": at(14, 0).Format(time.RFC3339), "title": "Taken over"}

	for _, c := range []struct {
		name  string
		as    uuid.UUID
		role  string
		block string
	}{
		{"her manager", w.bea, "manager", b.ID},
		{"the manager of a nested team", w.bea, "manager", other.ID},
		{"an administrator", w.ada, "admin", b.ID},
		{"the owner", w.org.OwnerID, "owner", other.ID},
	} {
		r := w.as(t, c.as, c.role)
		if got := call(t, r, http.MethodPatch, "/api/calendar/blocks/"+c.block, moved); got.Code != http.StatusNotFound {
			t.Errorf("%s edits somebody else's block: want 404, got %d: %s", c.name, got.Code, got.Body.String())
		}
		if got := call(t, r, http.MethodDelete, "/api/calendar/blocks/"+c.block, nil); got.Code != http.StatusNotFound {
			t.Errorf("%s deletes somebody else's block: want 404, got %d: %s", c.name, got.Code, got.Body.String())
		}
	}

	// Both are where their owners put them, as they put them.
	for who, title := range map[uuid.UUID]string{w.alice: "Alice deep work", w.pat: "Pat office hours"} {
		items := w.mustCalendar(t, w.as(t, who, "member"), who)
		if it, ok := find(items, title); !ok || it.StartsAt.Hour() == 13 {
			t.Errorf("%q is untouched, got %+v", title, items)
		}
		if _, ok := find(items, "Taken over"); ok {
			t.Errorf("nothing was renamed: %+v", items)
		}
	}

	// A block made while looking at her calendar is the manager's own: the API
	// takes the owner from the session and has no way to name anybody else.
	w.block(t, w.bea, "manager", "focus", "Bea planning", at(16, 0), at(17, 0))
	if _, ok := find(w.mustCalendar(t, w.as(t, w.bea, "manager"), w.alice), "Bea planning"); ok {
		t.Error("a block lands on its maker's calendar only")
	}
}

func TestThePickerOffersExactlyTheCalendarsThatOpen(t *testing.T) {
	w := newTeamWorld(t)

	// Bea: her teams, each person once, without the administrator sitting on one.
	if got, wanted := names(w.picker(t, w.bea, "manager")), []string{"Backend: Alice Archer", "Platform: Pat Platform"}; !sameLines(got, wanted) {
		t.Errorf("Bea's picker: want %v, got %v", wanted, got)
	}
	if got := w.picker(t, w.bea, "manager"); got[0].TeamID == "" {
		t.Errorf("a team carries its id: %+v", got)
	}
	if got, wanted := names(w.picker(t, w.oz, "manager")), []string{"Sales: Sam Sales"}; !sameLines(got, wanted) {
		t.Errorf("Oz's picker: want %v, got %v", wanted, got)
	}
	// Nobody to offer, so the page shows no picker.
	for who, role := range map[uuid.UUID]string{w.alice: "member", w.pat: "member"} {
		if got := w.picker(t, who, role); len(got) != 0 {
			t.Errorf("somebody who runs no team gets an empty list, got %v", names(got))
		}
	}

	// Somebody on two of Bea's teams comes once; a deactivated person not at all.
	ctx := context.Background()
	gone := w.person(t, "Gil Gone", "member")
	if _, err := w.pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO team_members (team_id, user_id, joined_at)
		SELECT t.id, u.id, NOW() FROM teams t, users u
		WHERE t.org_id = $1 AND t.name = 'Platform' AND u.id IN ($2, $3)`, w.org.ID, w.alice, gone); err != nil {
		t.Fatal(err)
	}
	if got, wanted := names(w.picker(t, w.bea, "manager")), []string{"Backend: Alice Archer", "Platform: Pat Platform"}; !sameLines(got, wanted) {
		t.Errorf("Bea's picker after the change: want %v, got %v", wanted, got)
	}
	// A deactivated report's calendar still opens by its link.
	if got, _ := w.calendar(t, w.as(t, w.bea, "manager"), gone); got.Code != http.StatusOK {
		t.Errorf("a deactivated report's calendar: want 200, got %d", got.Code)
	}

	// An administrator: the teams they run, then everybody else active, and not
	// themself or another organization's people.
	other := testsupport.SeedOrg(t, w.pool)
	testsupport.SeedUser(t, w.pool, other, "outsider@example.test", "member")
	adaGroups := w.picker(t, w.ada, "admin")
	if len(adaGroups) != 1 || adaGroups[0].Name != OthersGroup || adaGroups[0].TeamID != "" {
		t.Fatalf("Ada runs no team, so one group of everybody: %v", names(adaGroups))
	}
	listed := strings.Join(names(adaGroups), "\n")
	for _, name := range []string{"Alice Archer", "Bea Boss", "Oz Other", "Pat Platform", "Sam Sales"} {
		if !strings.Contains(listed, name) {
			t.Errorf("an administrator is offered %s: %s", name, listed)
		}
	}
	for _, name := range []string{"Ada Admin", "Gil Gone", "outsider"} {
		if strings.Contains(listed, name) {
			t.Errorf("an administrator is not offered %s: %s", name, listed)
		}
	}

	// Every person offered opens, and every one refused is not offered.
	for _, who := range []struct {
		id   uuid.UUID
		role string
	}{{w.bea, "manager"}, {w.oz, "manager"}, {w.alice, "member"}, {w.ada, "admin"}} {
		offered := map[string]bool{}
		for _, g := range w.picker(t, who.id, who.role) {
			for _, p := range g.People {
				offered[p.ID] = true
			}
		}
		for _, subject := range []uuid.UUID{w.bea, w.oz, w.alice, w.pat, w.sam, w.ada} {
			if subject == who.id {
				continue
			}
			got, _ := w.calendar(t, w.as(t, who.id, who.role), subject)
			if offered[subject.String()] != (got.Code == http.StatusOK) {
				t.Errorf("%s about %s: offered %v, but the calendar answers %d", who.id, subject, offered[subject.String()], got.Code)
			}
		}
	}
}

package calls

// Standing meetings for chosen teams and people (migration 0045). The owner's
// decisions of 14 November 2026: teams include the teams nested beneath them,
// managers may make them for their own teams, people outside do not see them,
// and somebody let in at the door is in for that day only.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// team makes a team, optionally beneath another, managed by manager, with members.
func (e *lkEnv) team(t *testing.T, name string, parent *uuid.UUID, manager uuid.UUID, members ...uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO teams (org_id, name, parent_id, manager_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		e.org.ID, name, parent, manager).Scan(&id); err != nil {
		t.Fatalf("team %s: %v", name, err)
	}
	for _, m := range members {
		e.addToTeam(t, id, m)
	}
	return id
}

func (e *lkEnv) addToTeam(t *testing.T, team, person uuid.UUID) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO team_members (team_id, user_id) VALUES ($1, $2)`, team, person); err != nil {
		t.Fatal(err)
	}
}

func audienceBody(name string, everyone bool, teams, people []uuid.UUID) string {
	b, _ := json.Marshal(map[string]any{
		"name": name, "timeOfDay": "09:30", "timeZone": "Asia/Kolkata", "weekdays": everyDay,
		"everyone": everyone, "teamIds": teams, "personIds": people,
	})
	return string(b)
}

type listedStanding struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	InAudience bool   `json:"inAudience"`
	CanManage  bool   `json:"canManage"`
	Audience   struct {
		Everyone bool          `json:"everyone"`
		Teams    []AudienceRef `json:"teams"`
		People   []AudienceRef `json:"people"`
	} `json:"audience"`
}

func (e *lkEnv) listAudienced(t *testing.T, as uuid.UUID, role string) map[string]listedStanding {
	t.Helper()
	w := doJSON(t, e.as(t, as, role), http.MethodGet, "/api/standing-meetings", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var list []listedStanding
	if err := json.Unmarshal(inner(w.Body.Bytes(), "standingMeetings"), &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]listedStanding{}
	for _, m := range list {
		out[m.ID] = m
	}
	return out
}

// The cast: Max manages Backend, with API nested beneath it. Bea is on Backend,
// Ana on API, Fay on Frontend (managed by Flo), and Ada administers.
type cast struct {
	ada, max, flo, bea, ana, fay uuid.UUID
	backend, api, frontend       uuid.UUID
}

func newCast(t *testing.T, e *lkEnv) cast {
	t.Helper()
	c := cast{
		ada: e.person(t, "Admin Ada", "admin"),
		max: e.person(t, "Manager Max", "manager"),
		flo: e.person(t, "Manager Flo", "manager"),
		bea: e.person(t, "Backend Bea", "member"),
		ana: e.person(t, "Api Ana", "member"),
		fay: e.person(t, "Frontend Fay", "member"),
	}
	c.backend = e.team(t, "Backend", nil, c.max, c.bea)
	c.api = e.team(t, "API", &c.backend, c.max, c.ana)
	c.frontend = e.team(t, "Frontend", nil, c.flo, c.fay)
	return c
}

func TestAManagersStandUpIsForTheirTeamAndTheTeamsBeneathIt(t *testing.T) {
	e := newLK(t)
	c := newCast(t, e)
	code, m := e.createStanding(t, c.max, "manager", audienceBody("Backend stand-up", false, []uuid.UUID{c.backend}, nil))
	if code != http.StatusCreated {
		t.Fatalf("a manager may make a stand-up for their own team, got %d", code)
	}

	// In it: Max who made it, Bea on Backend, Ana on API beneath it.
	for name, who := range map[string]struct {
		id   uuid.UUID
		role string
	}{"max": {c.max, "manager"}, "bea": {c.bea, "member"}, "ana": {c.ana, "member"}} {
		got, ok := e.listAudienced(t, who.id, who.role)[m.ID]
		if !ok || !got.InAudience {
			t.Errorf("%s should see the stand-up and be in it: %+v", name, got)
		}
		if code, _ := e.joinStanding(t, who.id, who.role, m.ID); code != http.StatusOK {
			t.Errorf("%s should be able to join, got %d", name, code)
		}
	}
	if got := e.listAudienced(t, c.max, "manager")[m.ID]; !got.CanManage || len(got.Audience.Teams) != 1 || got.Audience.Teams[0].Name != "Backend" {
		t.Errorf("Max manages it and it names Backend: %+v", got)
	}

	// Not in it: Fay on Frontend does not even see it, or its attendance.
	if _, ok := e.listAudienced(t, c.fay, "member")[m.ID]; ok {
		t.Error("somebody outside the team must not see its stand-up")
	}
	if code, _ := e.joinStanding(t, c.fay, "member", m.ID); code != http.StatusNotFound {
		t.Errorf("somebody outside joining: want 404, got %d", code)
	}
	if w := doJSON(t, e.as(t, c.fay, "member"), http.MethodGet, "/api/standing-meetings/"+m.ID+"/attendance", ""); w.Code != http.StatusNotFound {
		t.Errorf("somebody outside reading attendance: want 404, got %d", w.Code)
	}

	// An admin sees it to manage it, but is not walked into it by rank.
	got, ok := e.listAudienced(t, c.ada, "admin")[m.ID]
	if !ok || got.InAudience || !got.CanManage {
		t.Errorf("the admin sees and manages it without being in it: %+v", got)
	}
	if code, _ := e.joinStanding(t, c.ada, "admin", m.ID); code != http.StatusNotFound {
		t.Errorf("an admin outside the audience joining: want 404, got %d", code)
	}
}

func TestWhoIsInItFollowsTheTeamAsItChanges(t *testing.T) {
	e := newLK(t)
	c := newCast(t, e)
	_, m := e.createStanding(t, c.max, "manager", audienceBody("Backend stand-up", false, []uuid.UUID{c.backend}, nil))
	if code, _ := e.joinStanding(t, c.fay, "member", m.ID); code != http.StatusNotFound {
		t.Fatalf("Fay is not on Backend yet, got %d", code)
	}
	e.addToTeam(t, c.backend, c.fay)
	if code, _ := e.joinStanding(t, c.fay, "member", m.ID); code != http.StatusOK {
		t.Errorf("Fay joined Backend, so its stand-up is hers too, got %d", code)
	}
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, c.backend, c.fay); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.joinStanding(t, c.fay, "member", m.ID); code != http.StatusNotFound {
		t.Errorf("Fay left Backend, so its stand-up is not hers any more, got %d", code)
	}
}

func TestAManagerChoosesOnlyFromTheirOwnPeople(t *testing.T) {
	e := newLK(t)
	c := newCast(t, e)
	for name, body := range map[string]string{
		"everybody":          audienceBody("All", true, nil, nil),
		"another's team":     audienceBody("Front", false, []uuid.UUID{c.frontend}, nil),
		"another's person":   audienceBody("Fay", false, nil, []uuid.UUID{c.fay}),
		"nobody":             audienceBody("Empty", false, nil, nil),
		"no audience at all": standingBody("Unsaid", "09:30", "Asia/Kolkata", everyDay...),
	} {
		code, _ := e.createStanding(t, c.max, "manager", body)
		want := http.StatusForbidden
		if name == "nobody" {
			want = http.StatusBadRequest
		}
		if code != want {
			t.Errorf("a manager choosing %s: want %d, got %d", name, want, code)
		}
	}
	// A specific person they manage, and a team nested beneath theirs, are fine.
	if code, _ := e.createStanding(t, c.max, "manager", audienceBody("Pair", false, []uuid.UUID{c.api}, []uuid.UUID{c.bea})); code != http.StatusCreated {
		t.Errorf("a manager's own nested team and own report, got %d", code)
	}
	// Somebody who manages nobody makes none.
	if code, _ := e.createStanding(t, c.bea, "member", audienceBody("Mine", false, nil, []uuid.UUID{c.bea})); code != http.StatusForbidden {
		t.Errorf("a member making a stand-up: want 403, got %d", code)
	}
	// What the dialog offers Max is exactly what he may choose.
	w := doJSON(t, e.as(t, c.max, "manager"), http.MethodGet, "/api/standing-audience-options", "")
	var opts AudienceOptions
	if err := json.Unmarshal(w.Body.Bytes(), &opts); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, r := range append(opts.Teams, opts.People...) {
		names = append(names, r.Name)
	}
	if opts.Everyone || strings.Join(names, ",") != "API,Backend,Api Ana,Backend Bea" {
		t.Errorf("Max is offered %v (everyone %v), want his two teams and two people", names, opts.Everyone)
	}
}

func TestOnlyItsMakerOrAnAdminChangesIt(t *testing.T) {
	e := newLK(t)
	c := newCast(t, e)
	_, m := e.createStanding(t, c.max, "manager", audienceBody("Backend stand-up", false, []uuid.UUID{c.backend}, nil))
	rename := `{"name":"Renamed"}`
	if w := doJSON(t, e.as(t, c.flo, "manager"), http.MethodPatch, "/api/standing-meetings/"+m.ID, rename); w.Code != http.StatusNotFound {
		t.Errorf("another manager, who cannot see it: want 404, got %d", w.Code)
	}
	if w := doJSON(t, e.as(t, c.bea, "member"), http.MethodPatch, "/api/standing-meetings/"+m.ID, rename); w.Code != http.StatusForbidden {
		t.Errorf("somebody in it but not its maker: want 403, got %d", w.Code)
	}
	if w := doJSON(t, e.as(t, c.max, "manager"), http.MethodPatch, "/api/standing-meetings/"+m.ID,
		audienceBody("Backend and Bea", false, []uuid.UUID{c.api}, []uuid.UUID{c.bea})); w.Code != http.StatusOK {
		t.Errorf("its maker changes who it is for: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(t, e.as(t, c.ada, "admin"), http.MethodPatch, "/api/standing-meetings/"+m.ID, audienceBody("Everybody now", true, nil, nil)); w.Code != http.StatusOK {
		t.Errorf("an admin may open it to everybody: %d %s", w.Code, w.Body.String())
	}
	if code, _ := e.joinStanding(t, c.fay, "member", m.ID); code != http.StatusOK {
		t.Errorf("for everybody now, Fay can join: %d", code)
	}
	if w := doJSON(t, e.as(t, c.bea, "member"), http.MethodDelete, "/api/standing-meetings/"+m.ID, ""); w.Code != http.StatusForbidden {
		t.Errorf("somebody in it archiving: want 403, got %d", w.Code)
	}
}

func TestAnAdminsMeetingWithoutAnAudienceIsForEverybodyAsBefore(t *testing.T) {
	e := newLK(t)
	c := newCast(t, e)
	m := e.mustCreateStanding(t, "All hands", "10:00", "Asia/Kolkata")
	if got := e.listAudienced(t, c.fay, "member")[m.ID]; !got.InAudience || !got.Audience.Everyone {
		t.Errorf("a meeting made without an audience is everybody's: %+v", got)
	}
}

func TestSomebodyLetInAtTheDoorIsInForThatDayOnly(t *testing.T) {
	e := newLK(t)
	c := newCast(t, e)
	_, m := e.createStanding(t, c.max, "manager", audienceBody("Backend stand-up", false, []uuid.UUID{c.backend}, nil))
	day := e.mustJoinStanding(t, c.bea, "member", m.ID)

	// Fay has the link to today's meeting: she may not walk in, so she knocks.
	fay := e.as(t, c.fay, "member")
	if w := doJSON(t, fay, http.MethodPost, "/api/calls/"+day.CallID+"/token", ""); w.Code == http.StatusOK {
		t.Fatal("somebody outside the audience must not get a token")
	}
	w := doJSON(t, fay, http.MethodPost, "/api/calls/"+day.CallID+"/knock", "")
	if !is2xx(w) {
		t.Fatalf("knock: %d %s", w.Code, w.Body.String())
	}
	var knock KnockRequest
	if err := json.Unmarshal(inner(w.Body.Bytes(), "request"), &knock); err != nil || knock.ID == uuid.Nil {
		if err2 := json.Unmarshal(w.Body.Bytes(), &knock); err2 != nil || knock.ID == uuid.Nil {
			t.Fatalf("decode knock %s", w.Body.String())
		}
	}

	// Somebody in the team answers the door; nobody was invited to a stand-up.
	bea := e.as(t, c.bea, "member")
	if w := doJSON(t, bea, http.MethodGet, "/api/calls/"+day.CallID+"/requests", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), knock.ID.String()) {
		t.Fatalf("Bea, in the team, sees who is at the door: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(t, bea, http.MethodPost, "/api/calls/"+day.CallID+"/requests/"+knock.ID.String()+"/admit", ""); !is2xx(w) {
		t.Fatalf("admit: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(t, fay, http.MethodPost, "/api/calls/"+day.CallID+"/token", ""); w.Code != http.StatusOK {
		t.Errorf("let in, Fay gets today's token: %d %s", w.Code, w.Body.String())
	}

	// That was today's call. The meeting itself is still not hers: not listed, and
	// tomorrow's call (another row) will not have her.
	if _, ok := e.listAudienced(t, c.fay, "member")[m.ID]; ok {
		t.Error("being let in once does not put the meeting on her list")
	}
	var otherDays int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM call_participants p JOIN calls c ON c.id = p.call_id
		WHERE c.standing_meeting_id = $1 AND p.user_id = $2 AND c.id <> $3`, m.ID, c.fay, day.CallID).Scan(&otherDays); err != nil {
		t.Fatal(err)
	}
	if otherDays != 0 {
		t.Errorf("Fay is admitted to %d other days", otherDays)
	}
	// Somebody outside the team cannot answer the door.
	if w := doJSON(t, e.as(t, c.flo, "manager"), http.MethodGet, "/api/calls/"+day.CallID+"/requests", ""); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Errorf("Flo, outside the team, reading the door: %d", w.Code)
	}
}

func TestOnlyTheAudienceIsAbsent(t *testing.T) {
	e := newLK(t)
	c := newCast(t, e)
	_, m := e.createStanding(t, c.max, "manager", audienceBody("Backend stand-up", false, []uuid.UUID{c.backend}, nil))
	e.mustJoinStanding(t, c.bea, "member", m.ID)
	w := doJSON(t, e.as(t, c.ada, "admin"), http.MethodGet, "/api/standing-meetings/"+m.ID+"/attendance?date="+today("Asia/Kolkata"), "")
	if w.Code != http.StatusOK {
		t.Fatalf("attendance: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "Frontend Fay") || strings.Contains(body, "Manager Flo") || strings.Contains(body, "Admin Ada") {
		t.Errorf("people the meeting is not for are listed: %s", body)
	}
	if !strings.Contains(body, "Api Ana") {
		t.Errorf("Ana, on the team beneath, is expected: %s", body)
	}
}

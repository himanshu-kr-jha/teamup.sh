package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/teamup/teamup/internal/testsupport"
)

// scrumBody is what a saved scrum is assumed to look like on the wire, either
// bare or wrapped in {"scrum": ...}.
type scrumBody struct {
	ID           string `json:"id"`
	TeamID       string `json:"teamId"`
	Notes        string `json:"notes"`
	CreatedAt    string `json:"createdAt"`
	Participants []struct {
		UserID string `json:"userId"`
		Name   string `json:"name"`
	} `json:"participants"`
	Entries []struct {
		UserID string `json:"userId"`
		Title  string `json:"title"`
		Status string `json:"status"`
	} `json:"entries"`
}

func decodeScrum(t *testing.T, body []byte) scrumBody {
	t.Helper()
	var s scrumBody
	if err := json.Unmarshal(unwrap(body, "scrum"), &s); err != nil {
		t.Fatalf("decode scrum %q: %v", body, err)
	}
	return s
}

func (s scrumBody) hasParticipant(id uuid.UUID) bool {
	for _, p := range s.Participants {
		if p.UserID == id.String() {
			return true
		}
	}
	return false
}

// saveScrum completes a scrum as the router's caller and returns its id.
func saveScrum(t *testing.T, r *gin.Engine, body string) string {
	t.Helper()
	w := send(t, r, http.MethodPost, "/api/scrums", body)
	mustStatus(t, w, http.StatusCreated)
	s := decodeScrum(t, w.Body.Bytes())
	if s.ID == "" {
		t.Fatalf("saved scrum has no id: %s", w.Body.String())
	}
	return s.ID
}

func listScrumIDs(t *testing.T, r *gin.Engine, query string) []string {
	t.Helper()
	w := getJSON(t, r, "/api/scrums"+query)
	mustStatus(t, w, http.StatusOK)
	var body struct {
		Scrums []struct {
			ID string `json:"id"`
		} `json:"scrums"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode scrum list %q: %v", w.Body.String(), err)
	}
	ids := []string{}
	for _, s := range body.Scrums {
		ids = append(ids, s.ID)
	}
	return ids
}

func sameIDs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestCompletingAScrumSnapshotsEveryoneIncludingPeopleWithNoWork(t *testing.T) {
	c := seedDash(t)
	c.work(t, c.alice, "Alice's task", "in_progress")
	c.work(t, c.pat, "Pat's task", "blocked")
	r := c.asManager(t, c.mgr)

	w := send(t, r, http.MethodPost, "/api/scrums", `{"teamId":"`+c.backend.String()+`","notes":"all quiet"}`)
	mustStatus(t, w, http.StatusCreated)
	saved := decodeScrum(t, w.Body.Bytes())

	read := getJSON(t, r, "/api/scrums/"+saved.ID)
	mustStatus(t, read, http.StatusOK)
	s := decodeScrum(t, read.Body.Bytes())

	if s.Notes != "all quiet" || s.TeamID != c.backend.String() {
		t.Errorf("notes and team should be kept, got %+v", s)
	}
	for name, id := range map[string]uuid.UUID{"alice": c.alice, "pat (nested team)": c.pat, "ann (no work)": c.ann} {
		if !s.hasParticipant(id) {
			t.Errorf("%s should be a participant: %s", name, read.Body.String())
		}
	}
	for name, id := range map[string]uuid.UUID{"sam": c.sam, "dee (deactivated)": c.dee, "quinn (org admin)": c.quinn} {
		if s.hasParticipant(id) {
			t.Errorf("%s must not be a participant", name)
		}
	}
	titles := map[string]string{}
	for _, e := range s.Entries {
		titles[e.Title] = e.Status
	}
	if titles["Alice's task"] != "in_progress" || titles["Pat's task"] != "blocked" {
		t.Errorf("entries should copy titles and statuses, got %v", titles)
	}
}

func TestAManagerCanSaveAScrumForEverybodyTheyAnswerFor(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)

	id := saveScrum(t, r, `{}`)
	s := decodeScrum(t, getJSON(t, r, "/api/scrums/"+id).Body.Bytes())
	if !s.hasParticipant(c.alice) || !s.hasParticipant(c.ann) || !s.hasParticipant(c.pat) || s.hasParticipant(c.sam) {
		t.Errorf("null team should mean everybody the caller answers for, got %+v", s.Participants)
	}
}

func TestASavedScrumNeverChangesAfterTheWorkDoes(t *testing.T) {
	c := seedDash(t)
	edited := c.work(t, c.alice, "Original title", "todo")
	removed := c.work(t, c.alice, "Soon to vanish", "in_progress")
	r := c.asManager(t, c.mgr)

	id := saveScrum(t, r, `{"teamId":"`+c.backend.String()+`"}`)
	before := getJSON(t, r, "/api/scrums/"+id)
	mustStatus(t, before, http.StatusOK)

	mustStatus(t, patchTask(t, r, edited.String(), `{"title":"Rewritten","status":"done"}`), http.StatusOK)
	mustStatus(t, deleteTask(t, r, removed.String()), http.StatusOK)
	if _, err := c.pool.Exec(context.Background(), `UPDATE users SET name = 'Alice Renamed' WHERE id = $1`, c.alice); err != nil {
		t.Fatalf("rename: %v", err)
	}

	after := getJSON(t, r, "/api/scrums/"+id)
	mustStatus(t, after, http.StatusOK)
	if before.Body.String() != after.Body.String() {
		t.Errorf("a saved scrum is a snapshot and must not move.\nbefore: %s\nafter:  %s",
			before.Body.String(), after.Body.String())
	}
}

func TestOnlyManagersAndAdminsMayCompleteAScrum(t *testing.T) {
	c := seedDash(t)

	if w := send(t, c.asMember(t, c.alice), http.MethodPost, "/api/scrums", `{}`); w.Code != http.StatusForbidden {
		t.Errorf("employee: want 403, got %d: %s", w.Code, w.Body.String())
	}
	if w := send(t, c.asMember(t, c.alice), http.MethodPost, "/api/scrums",
		`{"teamId":"`+c.backend.String()+`"}`); w.Code != http.StatusForbidden {
		t.Errorf("employee naming their own team: want 403, got %d: %s", w.Code, w.Body.String())
	}

	// A manager of another team is refused; the status is 403, or 404 if the
	// team is hidden from them, but never a saved scrum.
	w := send(t, c.asManager(t, c.mgr), http.MethodPost, "/api/scrums", `{"teamId":"`+c.sales.String()+`"}`)
	if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Errorf("manager of another team: want 403 or 404, got %d: %s", w.Code, w.Body.String())
	}
	if ids := listScrumIDs(t, c.asOwner(t), ""); len(ids) != 0 {
		t.Errorf("refused requests must not save anything, found %v", ids)
	}

	// An admin may save for any team and for the whole organization.
	saveScrum(t, c.asOwner(t), `{"teamId":"`+c.sales.String()+`"}`)
	saveScrum(t, c.asOwner(t), `{}`)
}

func TestWhoMayReadASavedScrum(t *testing.T) {
	c := seedDash(t)
	// The admin saves it, so the "holder" is the admin and the Backend manager
	// reads it only because they run the team.
	id := saveScrum(t, c.asOwner(t), `{"teamId":"`+c.backend.String()+`"}`)
	// Another manager saves one too, so the holder rule is exercised.
	heldByMgr2 := saveScrum(t, c.asManager(t, c.mgr2), `{"teamId":"`+c.sales.String()+`"}`)

	// No team on it, so the only claim to read it is having saved it.
	heldWithNoTeam := saveScrum(t, c.asManager(t, c.mgr), `{}`)

	elsewhere := testsupport.SeedOrg(t, c.pool)
	cases := []struct {
		name string
		r    *gin.Engine
		path string
		want int
	}{
		{"admin", c.asOwner(t), id, http.StatusOK},
		{"manager of the team", c.asManager(t, c.mgr), id, http.StatusOK},
		{"manager of a different team", c.asManager(t, c.mgr2), id, http.StatusNotFound},
		{"employee on the team", c.asMember(t, c.alice), id, http.StatusNotFound},
		{"admin of another organization", asUser(t, c.svc, elsewhere.ID, elsewhere.OwnerID, "owner"), id, http.StatusNotFound},
		{"holder who saved it", c.asManager(t, c.mgr2), heldByMgr2, http.StatusOK},
		{"holder of a scrum with no team", c.asManager(t, c.mgr), heldWithNoTeam, http.StatusOK},
		{"manager who did not hold a scrum with no team", c.asManager(t, c.mgr2), heldWithNoTeam, http.StatusNotFound},
		{"manager of another team than the one held", c.asManager(t, c.mgr), heldByMgr2, http.StatusNotFound},
		{"an invented id", c.asOwner(t), uuid.NewString(), http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := getJSON(t, tc.r, "/api/scrums/"+tc.path)
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestScrumListIsNewestFirstAndScopedToWhatTheCallerMayRead(t *testing.T) {
	c := seedDash(t)
	mgrFirst := saveScrum(t, c.asManager(t, c.mgr), `{"teamId":"`+c.backend.String()+`"}`)
	mgr2Only := saveScrum(t, c.asManager(t, c.mgr2), `{"teamId":"`+c.sales.String()+`"}`)
	mgrSecond := saveScrum(t, c.asManager(t, c.mgr), `{}`)

	if got := listScrumIDs(t, c.asManager(t, c.mgr), ""); !sameIDs(got, mgrSecond, mgrFirst) {
		t.Errorf("manager should see only their own, newest first: want [%s %s], got %v", mgrSecond, mgrFirst, got)
	}
	if got := listScrumIDs(t, c.asManager(t, c.mgr2), ""); !sameIDs(got, mgr2Only) {
		t.Errorf("the other manager should see only theirs: want [%s], got %v", mgr2Only, got)
	}
	if got := listScrumIDs(t, c.asOwner(t), ""); !sameIDs(got, mgrSecond, mgr2Only, mgrFirst) {
		t.Errorf("admin should see all, newest first: want [%s %s %s], got %v", mgrSecond, mgr2Only, mgrFirst, got)
	}
	if got := listScrumIDs(t, c.asOwner(t), "?limit=1"); !sameIDs(got, mgrSecond) {
		t.Errorf("limit=1 should return the newest only, got %v", got)
	}
	if got := listScrumIDs(t, c.asOwner(t), "?teamId="+c.sales.String()); !sameIDs(got, mgr2Only) {
		t.Errorf("teamId filter should narrow the list, got %v", got)
	}
	if got := listScrumIDs(t, c.asMember(t, c.alice), ""); len(got) != 0 {
		t.Errorf("an employee has nothing to read, got %v", got)
	}

	elsewhere := testsupport.SeedOrg(t, c.pool)
	if got := listScrumIDs(t, asUser(t, c.svc, elsewhere.ID, elsewhere.OwnerID, "owner"), ""); len(got) != 0 {
		t.Errorf("another organization must see nothing, got %v", got)
	}
}

func TestASavedScrumCannotBeEditedOrDeleted(t *testing.T) {
	c := seedDash(t)
	r := c.asOwner(t)
	id := saveScrum(t, r, `{"notes":"final"}`)

	for _, method := range []string{http.MethodPatch, http.MethodPut, http.MethodDelete} {
		w := send(t, r, method, "/api/scrums/"+id, `{"notes":"tampered"}`)
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: want 404 or 405, got %d: %s", method, w.Code, w.Body.String())
		}
	}
	s := decodeScrum(t, getJSON(t, r, "/api/scrums/"+id).Body.Bytes())
	if s.Notes != "final" {
		t.Errorf("the scrum was changed: %+v", s)
	}
}

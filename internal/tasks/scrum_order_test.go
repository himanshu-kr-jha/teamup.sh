package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── The scrum table's order, kept per viewer ──────────────────────────────

type orderBody struct {
	People []string            `json:"people"`
	Tasks  map[string][]string `json:"tasks"`
}

func getOrder(t *testing.T, r *gin.Engine, query string) orderBody {
	t.Helper()
	w := getJSON(t, r, "/api/scrums/order"+query)
	mustStatus(t, w, http.StatusOK)
	var o orderBody
	if err := json.Unmarshal(w.Body.Bytes(), &o); err != nil {
		t.Fatalf("decode order %q: %v", w.Body.String(), err)
	}
	return o
}

func idList(ids ...uuid.UUID) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = `"` + id.String() + `"`
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func strs(ids ...uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func putPeople(t *testing.T, r *gin.Engine, team *uuid.UUID, people ...uuid.UUID) []string {
	t.Helper()
	body := `{"people":` + idList(people...) + `}`
	if team != nil {
		body = `{"teamId":"` + team.String() + `","people":` + idList(people...) + `}`
	}
	w := send(t, r, http.MethodPut, "/api/scrums/order", body)
	mustStatus(t, w, http.StatusOK)
	var got struct {
		People []string `json:"people"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return got.People
}

func putTasks(t *testing.T, r *gin.Engine, person uuid.UUID, tasks ...uuid.UUID) *httpResult {
	t.Helper()
	w := send(t, r, http.MethodPut, "/api/scrums/order/tasks/"+person.String(), `{"tasks":`+idList(tasks...)+`}`)
	res := &httpResult{code: w.Code, body: w.Body.String()}
	if w.Code == http.StatusOK {
		var got struct {
			Tasks []string `json:"tasks"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", w.Body.String(), err)
		}
		res.tasks = got.Tasks
	}
	return res
}

type httpResult struct {
	code  int
	body  string
	tasks []string
}

func TestTheOrderOfPeopleIsKeptPerViewAndSurvivesAReload(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)

	if got := getOrder(t, r, ""); len(got.People) != 0 || len(got.Tasks) != 0 {
		t.Fatalf("nothing arranged yet should read as empty, got %+v", got)
	}

	saved := putPeople(t, r, nil, c.pat, c.ann, c.alice)
	if !sameIDs(saved, strs(c.pat, c.ann, c.alice)...) {
		t.Errorf("the order should be kept as given, got %v", saved)
	}
	// A fresh router is a fresh page load, on any device: the order is the server's.
	if got := getOrder(t, c.asManager(t, c.mgr), ""); !sameIDs(got.People, strs(c.pat, c.ann, c.alice)...) {
		t.Errorf("reload: want pat, ann, alice, got %v", got.People)
	}

	// The Backend view has an order of its own, and leaves the other one alone.
	putPeople(t, r, &c.backend, c.alice, c.pat)
	if got := getOrder(t, r, "?teamId="+c.backend.String()); !sameIDs(got.People, strs(c.alice, c.pat)...) {
		t.Errorf("backend view: want alice, pat, got %v", got.People)
	}
	if got := getOrder(t, r, ""); !sameIDs(got.People, strs(c.pat, c.ann, c.alice)...) {
		t.Errorf("the everybody view should be untouched, got %v", got.People)
	}

	// It can be changed any time.
	putPeople(t, r, nil, c.alice, c.pat, c.ann)
	if got := getOrder(t, r, ""); !sameIDs(got.People, strs(c.alice, c.pat, c.ann)...) {
		t.Errorf("rearranged: want alice, pat, ann, got %v", got.People)
	}
}

func TestTheOrderIsTheViewersOwnAndNobodyElseSeesIt(t *testing.T) {
	c := seedDash(t)
	putPeople(t, c.asManager(t, c.mgr), &c.backend, c.pat, c.alice, c.ann)

	// The admin looks at the same team and has arranged nothing.
	if got := getOrder(t, c.asOwner(t), "?teamId="+c.backend.String()); len(got.People) != 0 {
		t.Errorf("another viewer must not see somebody else's order, got %v", got.People)
	}
	putPeople(t, c.asOwner(t), &c.backend, c.ann, c.alice)
	if got := getOrder(t, c.asManager(t, c.mgr), "?teamId="+c.backend.String()); !sameIDs(got.People, strs(c.pat, c.alice, c.ann)...) {
		t.Errorf("the admin's order must not change the manager's, got %v", got.People)
	}
}

func TestAnOrderCanOnlyNamePeopleTheViewerCanSee(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)
	stranger := uuid.New()

	// Sam is on a team Mia does not run, Dee is deactivated, and the last id is nobody.
	saved := putPeople(t, r, nil, c.sam, c.alice, c.dee, c.alice, stranger, c.pat)
	if !sameIDs(saved, strs(c.alice, c.pat)...) {
		t.Errorf("only people Mia can see, once each, should be kept, got %v", saved)
	}
	if got := getOrder(t, r, ""); !sameIDs(got.People, strs(c.alice, c.pat)...) {
		t.Errorf("want alice, pat, got %v", got.People)
	}

	// A team that is not hers is not found, as on the dashboard.
	w := send(t, r, http.MethodPut, "/api/scrums/order", `{"teamId":"`+c.sales.String()+`","people":[]}`)
	mustStatus(t, w, http.StatusNotFound)
	mustStatus(t, getJSON(t, r, "/api/scrums/order?teamId="+c.sales.String()), http.StatusNotFound)

	// Somebody who later leaves the view drops out of it quietly.
	if _, err := c.pool.Exec(context.Background(), `DELETE FROM team_members WHERE user_id = $1`, c.pat); err != nil {
		t.Fatalf("remove pat: %v", err)
	}
	if got := getOrder(t, r, ""); !sameIDs(got.People, strs(c.alice)...) {
		t.Errorf("pat left, so only alice should be left in the order, got %v", got.People)
	}
}

func TestOnlySomebodyWhoRunsAScrumHasAnOrder(t *testing.T) {
	c := seedDash(t)
	alice := c.asMember(t, c.alice)
	mustStatus(t, getJSON(t, alice, "/api/scrums/order"), http.StatusForbidden)
	mustStatus(t, send(t, alice, http.MethodPut, "/api/scrums/order", `{"people":[]}`), http.StatusForbidden)
	if res := putTasks(t, alice, c.alice); res.code != http.StatusForbidden {
		t.Errorf("an employee arranging tasks: want 403, got %d: %s", res.code, res.body)
	}
	mustStatus(t, send(t, c.asManager(t, c.mgr), http.MethodPut, "/api/scrums/order", `{"people":["not-an-id"]}`), http.StatusBadRequest)
	mustStatus(t, send(t, c.asManager(t, c.mgr), http.MethodPut, "/api/scrums/order", `{}`), http.StatusBadRequest)
}

func TestTheOrderOfAPersonsTasksKeepsOnlyTheirOpenWork(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)
	first := c.work(t, c.alice, "First", "todo")
	second := c.work(t, c.alice, "Second", "blocked")
	done := c.work(t, c.alice, "Finished", "done")
	patsTask := c.work(t, c.pat, "Pat's", "todo")
	// Somebody else's task Alice was added to is part of her row too.
	other := c.insertTask(t, taskSpec{title: "Owner's", status: "in_progress"})
	c.watch(t, other, c.alice)

	res := putTasks(t, r, c.alice, other, second, done, patsTask, uuid.New(), second, first)
	if res.code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", res.code, res.body)
	}
	if !sameIDs(res.tasks, strs(other, second, first)...) {
		t.Errorf("only Alice's open work, once each, should be kept, got %v", res.tasks)
	}
	got := getOrder(t, c.asManager(t, c.mgr), "")
	if !sameIDs(got.Tasks[c.alice.String()], strs(other, second, first)...) {
		t.Errorf("reload: want other, second, first, got %v", got.Tasks[c.alice.String()])
	}
	// The same arrangement whatever team filter is on.
	got = getOrder(t, r, "?teamId="+c.backend.String())
	if !sameIDs(got.Tasks[c.alice.String()], strs(other, second, first)...) {
		t.Errorf("backend view: want the same task order, got %v", got.Tasks[c.alice.String()])
	}

	// Somebody Mia does not answer for is not found, and another viewer is unaffected.
	if res := putTasks(t, r, c.sam); res.code != http.StatusNotFound {
		t.Errorf("sam: want 404, got %d: %s", res.code, res.body)
	}
	if got := getOrder(t, c.asOwner(t), ""); len(got.Tasks) != 0 {
		t.Errorf("the admin has arranged nothing, got %v", got.Tasks)
	}
}

// ── My Lead ───────────────────────────────────────────────────────────────

type leadsDash struct {
	People []struct {
		UserID string `json:"userId"`
		Leads  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"leads"`
	} `json:"people"`
}

func leadsOf(t *testing.T, r *gin.Engine) map[string][]string {
	t.Helper()
	w := getJSON(t, r, "/api/tasks/dashboard")
	mustStatus(t, w, http.StatusOK)
	var d leadsDash
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	out := map[string][]string{}
	for _, p := range d.People {
		if p.Leads == nil {
			t.Fatalf("leads should be a list, never null: %s", w.Body.String())
		}
		names := []string{}
		for _, l := range p.Leads {
			names = append(names, l.Name)
		}
		out[p.UserID] = names
	}
	return out
}

func TestEveryPersonsLeadsAreTheManagersOfTheirTeamsButNeverThemselves(t *testing.T) {
	c := seedDash(t)
	// Alice is on Sales as well, and Mia sits on her own team.
	c.joinTeam(t, c.sales, c.alice)
	c.joinTeam(t, c.backend, c.mgr)

	leads := leadsOf(t, c.asOwner(t))
	if got := leads[c.alice.String()]; !sameIDs(got, "Mia Manager", "Max Manager") {
		t.Errorf("alice: want both managers, by team name, got %v", got)
	}
	if got := leads[c.ann.String()]; !sameIDs(got, "Mia Manager") {
		t.Errorf("ann: want Mia, got %v", got)
	}
	// Platform has no manager of its own.
	if got := leads[c.pat.String()]; len(got) != 0 {
		t.Errorf("pat: want nobody, got %v", got)
	}
	if got := leads[c.mgr.String()]; len(got) != 0 {
		t.Errorf("mia leads her own team and is not her own lead, got %v", got)
	}
	if got := leads[c.sam.String()]; !sameIDs(got, "Max Manager") {
		t.Errorf("sam: want Max, got %v", got)
	}
}

// ── A saved scrum keeps the order and the leads ───────────────────────────

type orderedScrum struct {
	Participants []struct {
		UserID   string   `json:"userId"`
		Position *int     `json:"position"`
		Leads    []string `json:"leads"`
	} `json:"participants"`
	Entries []struct {
		UserID   string `json:"userId"`
		TaskID   string `json:"taskId"`
		Status   string `json:"status"`
		AddedTo  bool   `json:"addedTo"`
		Position *int   `json:"position"`
	} `json:"entries"`
}

func readOrderedScrum(t *testing.T, r *gin.Engine, id string) orderedScrum {
	t.Helper()
	w := getJSON(t, r, "/api/scrums/"+id)
	mustStatus(t, w, http.StatusOK)
	var s orderedScrum
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return s
}

func TestASavedScrumKeepsTheHoldersOrderAndEachPersonsLeads(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)
	first := c.work(t, c.alice, "A first", "todo")
	second := c.work(t, c.alice, "B second", "in_progress")
	third := c.work(t, c.alice, "C third", "blocked")
	done := c.insertTask(t, taskSpec{title: "Done", assignee: &c.alice, status: "done", completedAt: ago(0)})
	added := c.insertTask(t, taskSpec{title: "Owner's", status: "todo"})
	c.watch(t, added, c.alice)

	// What the holder had on screen: Pat, then Alice, then Ann; Alice's third task
	// first and the one she was added to second. Her other two are not mentioned,
	// as for a task created while the dialog was open, so they go after.
	body := `{"teamId":"` + c.backend.String() + `","order":{"people":` + idList(c.pat, c.alice, c.ann) +
		`,"tasks":{"` + c.alice.String() + `":` + idList(third, added) + `}}}`
	id := saveScrum(t, r, body)
	s := readOrderedScrum(t, r, id)

	var people []string
	for i, p := range s.Participants {
		people = append(people, p.UserID)
		if p.Position == nil || *p.Position != i {
			t.Errorf("participant %d should be at position %d, got %v", i, i, p.Position)
		}
	}
	if !sameIDs(people, strs(c.pat, c.alice, c.ann)...) {
		t.Errorf("want pat, alice, ann, got %v", people)
	}
	for _, p := range s.Participants {
		want := []string{"Mia Manager"}
		if p.UserID == c.pat.String() {
			want = []string{}
		}
		if p.Leads == nil || !sameIDs(p.Leads, want...) {
			t.Errorf("leads of %s: want %v, got %v", p.UserID, want, p.Leads)
		}
	}

	var row []string
	for _, e := range s.Entries {
		if e.UserID != c.alice.String() {
			continue
		}
		if e.TaskID == done.String() {
			if e.Position != nil {
				t.Errorf("done work keeps no column, got %d", *e.Position)
			}
			continue
		}
		if e.Position == nil || *e.Position != len(row) {
			t.Errorf("task %s should be in column %d, got %v", e.TaskID, len(row), e.Position)
		}
		if (e.TaskID == added.String()) != e.AddedTo {
			t.Errorf("task %s: addedTo %v", e.TaskID, e.AddedTo)
		}
		row = append(row, e.TaskID)
	}
	if !sameIDs(row, strs(third, added, first, second)...) {
		t.Errorf("want third, added, then the rest by due date and title, got %v", row)
	}
}

func TestAScrumSavedBeforeTheTableReadsWithNoOrderAndNoLeads(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)
	c.work(t, c.alice, "Old work", "todo")
	id := saveScrum(t, r, `{}`)
	// What 0058 leaves on a scrum saved before it.
	for _, q := range []string{
		`UPDATE scrum_participants SET position = NULL, leads = NULL WHERE scrum_id = $1`,
		`UPDATE scrum_entries SET position = NULL WHERE scrum_id = $1`,
	} {
		if _, err := c.pool.Exec(context.Background(), q, id); err != nil {
			t.Fatalf("age the scrum: %v", err)
		}
	}
	w := getJSON(t, r, "/api/scrums/"+id)
	mustStatus(t, w, http.StatusOK)
	s := readOrderedScrum(t, r, id)
	if len(s.Participants) != 3 || len(s.Entries) != 1 {
		t.Fatalf("want 3 people and 1 task, got %s", w.Body.String())
	}
	for _, p := range s.Participants {
		if p.Position != nil || p.Leads != nil {
			t.Errorf("an old scrum has no order or leads to report, got %+v", p)
		}
	}
	if s.Entries[0].Position != nil {
		t.Errorf("an old entry has no column, got %v", *s.Entries[0].Position)
	}
}

func TestAnOrderThatIsTooLongIsRefused(t *testing.T) {
	c := seedDash(t)
	ids := make([]uuid.UUID, scrumOrderMaxPeople+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	r := c.asManager(t, c.mgr)
	mustStatus(t, send(t, r, http.MethodPut, "/api/scrums/order", `{"people":`+idList(ids...)+`}`), http.StatusBadRequest)
	mustStatus(t, send(t, r, http.MethodPost, "/api/scrums", `{"order":{"people":`+idList(ids...)+`}}`), http.StatusBadRequest)
}

package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type contribEntry struct {
	UserID    string `json:"userId"`
	Completed int    `json:"completed"`
	Involved  int    `json:"involved"`
	Open      int    `json:"open"`
}

// contribRange covers the whole test: from a month ago to tomorrow.
func contribRange() string {
	q := url.Values{}
	q.Set("from", time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339))
	q.Set("to", time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339))
	return q.Encode()
}

func getContributions(t *testing.T, r *gin.Engine, extra string) map[string]contribEntry {
	t.Helper()
	w := getJSON(t, r, "/api/tasks/contributions?"+contribRange()+extra)
	mustStatus(t, w, http.StatusOK)
	var body struct {
		People []contribEntry `json:"people"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode contributions %q: %v", w.Body.String(), err)
	}
	out := map[string]contribEntry{}
	for _, p := range body.People {
		out[p.UserID] = p
	}
	return out
}

type contribDetail struct {
	Completed []ref `json:"completed"`
	Involved  []ref `json:"involved"`
	Open      []ref `json:"open"`
}

func getContributionDetail(t *testing.T, r *gin.Engine, who uuid.UUID, rangeQuery string) *struct {
	code int
	body contribDetail
	raw  string
} {
	t.Helper()
	w := getJSON(t, r, "/api/tasks/contributions/"+who.String()+"?"+rangeQuery)
	out := &struct {
		code int
		body contribDetail
		raw  string
	}{code: w.Code, raw: w.Body.String()}
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out.body); err != nil {
			t.Fatalf("decode detail %q: %v", w.Body.String(), err)
		}
	}
	return out
}

func TestCompletedIsCountedInTheRangeAndCreditedToTheAssignee(t *testing.T) {
	c := seedDash(t)
	a, m := c.alice, c.mgr
	inRangeOwn := c.insertTask(t, taskSpec{title: "own", reporter: a, assignee: &a, status: "done", completedAt: ago(24 * time.Hour)})
	inRangeFiledByManager := c.insertTask(t, taskSpec{title: "filed by manager", reporter: m, assignee: &a, status: "done", completedAt: ago(48 * time.Hour)})
	c.insertTask(t, taskSpec{title: "too old", reporter: a, assignee: &a, status: "done", completedAt: ago(90 * 24 * time.Hour)})
	c.insertTask(t, taskSpec{title: "still open", reporter: a, assignee: &a, status: "in_progress"})
	c.insertTask(t, taskSpec{title: "removed", reporter: a, assignee: &a, status: "done", completedAt: ago(time.Hour), deleted: true})

	got := getContributions(t, c.asManager(t, c.mgr), "")
	if got[a.String()].Completed != 2 {
		t.Errorf("alice completed: want 2 (in range, either reporter), got %+v", got[a.String()])
	}
	if got[m.String()].Completed != 0 {
		t.Errorf("the reporter is not credited with the assignee's work, got %+v", got[m.String()])
	}
	if got[a.String()].Open != 1 {
		t.Errorf("alice open: want 1, got %+v", got[a.String()])
	}

	d := getContributionDetail(t, c.asManager(t, c.mgr), a, contribRange())
	if d.code != http.StatusOK {
		t.Fatalf("detail: want 200, got %d: %s", d.code, d.raw)
	}
	ids := refStrings(d.body.Completed)
	if len(ids) != 2 || !contains(ids, inRangeOwn) || !contains(ids, inRangeFiledByManager) {
		t.Errorf("detail should list the tasks behind the number, got %v", ids)
	}
}

func TestInvolvedCountsEveryWayOfBeingOnATaskOnceEach(t *testing.T) {
	c := seedDash(t)
	reporter := c.newMember(t, "rep", "Rita Reporter")
	assignee := c.newMember(t, "asg", "Abe Assignee")
	former := c.newMember(t, "frm", "Fay Former")
	watcher := c.newMember(t, "wat", "Walt Watcher")
	commenter := c.newMember(t, "com", "Cora Commenter")
	bystander := c.newMember(t, "by", "Ben Bystander")
	everything := c.newMember(t, "all", "Eve Everything")
	deletedOnly := c.newMember(t, "del", "Dan Deleted")
	for _, u := range []uuid.UUID{reporter, assignee, former, watcher, commenter, bystander, everything, deletedOnly} {
		c.joinTeam(t, c.backend, u)
	}

	task := c.insertTask(t, taskSpec{title: "shared work", reporter: reporter, assignee: &assignee, status: "in_progress"})
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := c.pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// The former assignee shows up in the activity trail: the task moved from
	// them to the current assignee.
	mustExec(`INSERT INTO task_activities (id, task_id, user_id, field, old_value, new_value, created_at)
		VALUES (gen_random_uuid(), $1, $2, 'assigneeId', $3, $4, NOW())`,
		task, c.mgr, former.String(), assignee.String())
	mustExec(`INSERT INTO task_watchers (task_id, user_id) VALUES ($1, $2)`, task, watcher)
	mustExec(`INSERT INTO task_comments (id, task_id, user_id, content, created_at)
		VALUES (gen_random_uuid(), $1, $2, 'looks good', NOW())`, task, commenter)

	// One person who is every one of those things on a single task counts once.
	solo := c.insertTask(t, taskSpec{title: "all roles", reporter: everything, assignee: &everything, status: "todo"})
	mustExec(`INSERT INTO task_activities (id, task_id, user_id, field, old_value, new_value, created_at)
		VALUES (gen_random_uuid(), $1, $2, 'assigneeId', $3, $3, NOW())`, solo, everything, everything.String())
	mustExec(`INSERT INTO task_watchers (task_id, user_id) VALUES ($1, $2)`, solo, everything)
	mustExec(`INSERT INTO task_comments (id, task_id, user_id, content, created_at)
		VALUES (gen_random_uuid(), $1, $2, 'one', NOW()), (gen_random_uuid(), $1, $2, 'two', NOW())`, solo, everything)

	// A soft-deleted task adds nothing, however involved somebody was.
	gone := c.insertTask(t, taskSpec{title: "removed", reporter: deletedOnly, assignee: &deletedOnly, status: "todo", deleted: true})
	mustExec(`INSERT INTO task_watchers (task_id, user_id) VALUES ($1, $2)`, gone, deletedOnly)

	got := getContributions(t, c.asManager(t, c.mgr), "")
	for name, id := range map[string]uuid.UUID{
		"reporter": reporter, "assignee": assignee, "former assignee": former,
		"watcher": watcher, "commenter": commenter, "person in every role": everything,
	} {
		if got[id.String()].Involved != 1 {
			t.Errorf("%s should be involved in exactly 1 task, got %+v", name, got[id.String()])
		}
	}
	if got[bystander.String()].Involved != 0 {
		t.Errorf("a bystander is not involved, got %+v", got[bystander.String()])
	}
	if got[deletedOnly.String()].Involved != 0 || got[deletedOnly.String()].Open != 0 {
		t.Errorf("soft-deleted tasks must not count, got %+v", got[deletedOnly.String()])
	}

	d := getContributionDetail(t, c.asManager(t, c.mgr), former, contribRange())
	if d.code != http.StatusOK || len(d.body.Involved) != 1 || string(d.body.Involved[0]) != task.String() {
		t.Errorf("detail for the former assignee should list the shared task, got %d %s", d.code, d.raw)
	}
}

func TestAnEmployeeSeesOnlyTheirOwnContribution(t *testing.T) {
	c := seedDash(t)
	c.insertTask(t, taskSpec{title: "alice done", reporter: c.alice, assignee: &c.alice, status: "done", completedAt: ago(time.Hour)})
	c.insertTask(t, taskSpec{title: "ann done", reporter: c.ann, assignee: &c.ann, status: "done", completedAt: ago(time.Hour)})
	r := c.asMember(t, c.alice)

	for _, extra := range []string{"", "&teamId=" + c.backend.String()} {
		got := getContributions(t, r, extra)
		if len(got) != 1 || got[c.alice.String()].Completed != 1 {
			t.Errorf("%q: employee should see only themself with 1 completed, got %+v", extra, got)
		}
	}

	if own := getContributionDetail(t, r, c.alice, contribRange()); own.code != http.StatusOK {
		t.Errorf("own detail: want 200, got %d: %s", own.code, own.raw)
	}
	other := getContributionDetail(t, r, c.ann, contribRange())
	if other.code != http.StatusNotFound {
		t.Errorf("somebody else's detail: want 404, got %d: %s", other.code, other.raw)
	}
	invented := getContributionDetail(t, r, uuid.New(), contribRange())
	if invented.code != other.code || invented.raw != other.raw {
		t.Errorf("a real colleague must look the same as an invented id: %d %s vs %d %s",
			other.code, other.raw, invented.code, invented.raw)
	}
}

func TestAManagerSeesOnlyTheirOwnPeopleContribution(t *testing.T) {
	c := seedDash(t)
	c.insertTask(t, taskSpec{title: "alice done", reporter: c.alice, assignee: &c.alice, status: "done", completedAt: ago(time.Hour)})
	c.insertTask(t, taskSpec{title: "sam done", reporter: c.sam, assignee: &c.sam, status: "done", completedAt: ago(time.Hour)})

	mine := getContributions(t, c.asManager(t, c.mgr), "")
	if _, ok := mine[c.alice.String()]; !ok {
		t.Errorf("alice should be listed for her manager, got %+v", mine)
	}
	if _, ok := mine[c.sam.String()]; ok {
		t.Error("sam is on another manager's team and must not be listed")
	}
	if _, ok := mine[c.quinn.String()]; ok {
		t.Error("an org admin is not one of the manager's people")
	}

	if d := getContributionDetail(t, c.asManager(t, c.mgr), c.alice, contribRange()); d.code != http.StatusOK {
		t.Errorf("own person's detail: want 200, got %d: %s", d.code, d.raw)
	}
	if d := getContributionDetail(t, c.asManager(t, c.mgr), c.sam, contribRange()); d.code != http.StatusNotFound {
		t.Errorf("another team's person: want 404, got %d: %s", d.code, d.raw)
	}

	all := getContributions(t, c.asOwner(t), "")
	if _, ok := all[c.sam.String()]; !ok {
		t.Errorf("an admin should see sam too, got %+v", all)
	}
}

// Deployed databases already hold "<nil>" from unassigning, and activity values are
// free text. None of that may break the numbers or credit anybody.
func TestActivityValuesThatAreNotIdsDoNotBreakContributions(t *testing.T) {
	c := seedDash(t)
	a := c.alice
	task := c.insertTask(t, taskSpec{title: "unassigned", reporter: c.mgr, assignee: nil, status: "todo"})
	for _, pair := range [][2]string{{a.String(), "<nil>"}, {"<nil>", "not-a-uuid"}} {
		if _, err := c.pool.Exec(context.Background(), `INSERT INTO task_activities (id, task_id, user_id, field, old_value, new_value, created_at)
			VALUES (gen_random_uuid(), $1, $2, 'assigneeId', $3, $4, NOW())`, task, c.mgr, pair[0], pair[1]); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got := getContributions(t, c.asManager(t, c.mgr), "")
	if got[a.String()].Involved != 1 {
		t.Errorf("alice was the assignee until the unassign, want 1 involved, got %+v", got[a.String()])
	}
	if got[c.ann.String()].Involved != 0 {
		t.Errorf("ann had nothing to do with it, got %+v", got[c.ann.String()])
	}
	d := getContributionDetail(t, c.asManager(t, c.mgr), a, contribRange())
	if d.code != http.StatusOK || len(d.body.Involved) != 1 {
		t.Errorf("detail should list the task, got %d %s", d.code, d.raw)
	}

	// And the route that writes these rows, end to end.
	assigned := c.work(t, c.ann, "taken off ann", "todo")
	mustStatus(t, patchTask(t, c.asManager(t, c.mgr), assigned.String(), `{"assigneeId":null}`), http.StatusOK)
	if got := getContributions(t, c.asManager(t, c.mgr), ""); got[c.ann.String()].Involved != 1 {
		t.Errorf("ann reported it and held it, once each is one task, got %+v", got[c.ann.String()])
	}
}

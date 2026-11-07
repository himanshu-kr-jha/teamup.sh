package tasks

// Ticket 05: tasks somebody was added to count.
// Decisions 17 and 18 in .scratch/calendar-and-meetings/spec.md.
//
// "Added to" means a row in task_watchers for somebody who is not the assignee.
// It is shown as its own bucket: dashboard tasks.addedTo and counts.addedTo,
// contributions addedTo, and the saved scrum's participants[].counts.addedTo.
// `completed` keeps meaning assigned to them.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// watch puts somebody on a task the way the table says, without the route, so a
// test can set up a state without depending on the notification side effect.
func (c *dashChart) watch(t *testing.T, task, who uuid.UUID) {
	t.Helper()
	if _, err := c.pool.Exec(context.Background(),
		`INSERT INTO task_watchers (task_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, task, who); err != nil {
		t.Fatalf("watch: %v", err)
	}
}

// addedDash reads the dashboard with the numbers this ticket adds.
type addedDash struct {
	Totals map[string]int `json:"totals"`
	People []struct {
		UserID string           `json:"userId"`
		Counts map[string]int   `json:"counts"`
		Tasks  map[string][]ref `json:"tasks"`
	} `json:"people"`
}

type addedPerson struct {
	counts  map[string]int
	buckets map[string][]string
}

func readAdded(t *testing.T, r *gin.Engine, query string) (map[string]addedPerson, map[string]int) {
	t.Helper()
	w := getJSON(t, r, "/api/tasks/dashboard"+query)
	mustStatus(t, w, http.StatusOK)
	var d addedDash
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode dashboard %q: %v", w.Body.String(), err)
	}
	out := map[string]addedPerson{}
	for _, p := range d.People {
		b := map[string][]string{}
		for name, refs := range p.Tasks {
			b[name] = refStrings(refs)
		}
		out[p.UserID] = addedPerson{counts: p.Counts, buckets: b}
	}
	return out, d.Totals
}

func (p addedPerson) in(bucket string, task uuid.UUID) bool { return contains(p.buckets[bucket], task) }

// ── the dashboard ─────────────────────────────────────────────────────────

func TestATaskSomebodyWasAddedToAppearsInTheirAddedToBucketForEveryViewer(t *testing.T) {
	c := seedDash(t)
	reporter, assignee := c.pat, c.ann
	task := c.insertTask(t, taskSpec{title: "Shared work", reporter: reporter, assignee: &assignee, status: "in_progress"})
	c.watch(t, task, c.alice)

	for name, r := range map[string]*gin.Engine{
		"alice herself": c.asMember(t, c.alice),
		"her manager":   c.asManager(t, c.mgr),
		"an admin":      c.asOwner(t),
	} {
		people, totals := readAdded(t, r, "")
		alice := people[c.alice.String()]
		if !alice.in("addedTo", task) || alice.counts["addedTo"] != 1 {
			t.Errorf("%s: the task is in alice's Added to bucket and counted once, got %v %v", name, alice.buckets["addedTo"], alice.counts)
		}
		if name != "alice herself" {
			if totals["addedTo"] < 1 {
				t.Errorf("%s: totals add up Added to, got %v", name, totals)
			}
		}
		// It is not alice's own work.
		for _, own := range []string{"pending", "ongoing", "blocked", "overdue", "completed"} {
			if alice.in(own, task) {
				t.Errorf("%s: an added-to task is not under alice's %s work", name, own)
			}
		}
	}
	// The assignee still has it as their own.
	people, _ := readAdded(t, c.asOwner(t), "")
	if !people[c.ann.String()].in("ongoing", task) || people[c.ann.String()].counts["addedTo"] != 0 {
		t.Errorf("the assignee has it as ongoing and not as added to, got %+v", people[c.ann.String()])
	}
}

func TestAnAssigneeWhoIsAlsoAWatcherAppearsOnceUnderTheirAssignedWork(t *testing.T) {
	c := seedDash(t)
	a := c.alice
	task := c.insertTask(t, taskSpec{title: "Mine and followed", reporter: c.mgr, assignee: &a, status: "todo"})
	c.watch(t, task, a)

	people, _ := readAdded(t, c.asManager(t, c.mgr), "")
	alice := people[a.String()]
	if !alice.in("pending", task) || alice.counts["pending"] != 1 {
		t.Errorf("assigned work shows under pending once, got %v %v", alice.buckets["pending"], alice.counts)
	}
	if alice.in("addedTo", task) || alice.counts["addedTo"] != 0 {
		t.Errorf("being the assignee and a watcher is not 'added to' as well, got %v %v", alice.buckets["addedTo"], alice.counts)
	}
}

func TestAddedToIsOpenWorkOnlyAndFinishedWorkIsNeverCountedTwice(t *testing.T) {
	c := seedDash(t)
	assignee := c.ann
	open := c.insertTask(t, taskSpec{title: "Open", reporter: c.mgr, assignee: &assignee, status: "blocked"})
	unassigned := c.insertTask(t, taskSpec{title: "Nobody has it", reporter: c.mgr, status: "todo"})
	done := c.insertTask(t, taskSpec{title: "Finished", reporter: c.mgr, assignee: &assignee, status: "done", completedAt: ago(2 * time.Hour)})
	gone := c.insertTask(t, taskSpec{title: "Removed", reporter: c.mgr, assignee: &assignee, status: "todo", deleted: true})
	for _, task := range []uuid.UUID{open, unassigned, done, gone} {
		c.watch(t, task, c.alice)
	}

	people, _ := readAdded(t, c.asManager(t, c.mgr), "")
	alice := people[c.alice.String()]
	if !alice.in("addedTo", open) || !alice.in("addedTo", unassigned) || alice.counts["addedTo"] != 2 {
		t.Errorf("open tasks, assigned to someone else or to nobody, count: want 2, got %v %v", alice.buckets["addedTo"], alice.counts)
	}
	if alice.in("addedTo", done) || alice.in("addedTo", gone) {
		t.Errorf("finished and removed tasks are not open work Added to, got %v", alice.buckets["addedTo"])
	}
	if alice.counts["completed"] != 0 || alice.in("completed", done) {
		t.Errorf("completed means assigned to them: a task they were only added to is not credited as completed, got %v", alice.counts)
	}
	if got := people[assignee.String()]; got.counts["completed"] != 1 || !got.in("completed", done) {
		t.Errorf("the assignee is credited once, got %v", got.counts)
	}
}

func TestRemovingAndReassigningMovesATaskBetweenTheBuckets(t *testing.T) {
	c := seedDash(t)
	manager := c.asManager(t, c.mgr)
	// Alice (assigned) and Ann (follower) are both on Backend, which mgr runs.
	task := c.work(t, c.alice, "Handover", "todo")
	c.watch(t, task, c.ann)

	people, _ := readAdded(t, manager, "")
	if !people[c.ann.String()].in("addedTo", task) || !people[c.alice.String()].in("pending", task) {
		t.Fatalf("setup: ann is added to, alice owns it, got %+v / %+v", people[c.ann.String()], people[c.alice.String()])
	}

	// Ann is given the task: it becomes her own work and is no longer 'added to'.
	mustStatus(t, patchTask(t, manager, task.String(), `{"assigneeId":"`+c.ann.String()+`"}`), http.StatusOK)
	people, _ = readAdded(t, manager, "")
	ann := people[c.ann.String()]
	if !ann.in("pending", task) || ann.in("addedTo", task) || ann.counts["addedTo"] != 0 || ann.counts["pending"] != 1 {
		t.Errorf("reassigned to the follower: it is hers once, under pending, got %v %v", ann.buckets, ann.counts)
	}

	// Alice watched it too: handing it away turns her ownership into 'added to'.
	c.watch(t, task, c.alice)
	people, _ = readAdded(t, manager, "")
	if alice := people[c.alice.String()]; !alice.in("addedTo", task) || alice.in("pending", task) {
		t.Errorf("the former assignee who still follows it is now 'added to', got %v", alice.buckets)
	}

	// Taking her off the task removes it from every bucket of hers.
	req := send(t, manager, http.MethodDelete, "/api/tasks/"+task.String()+"/watchers/"+c.alice.String(), "")
	if req.Code != http.StatusOK && req.Code != http.StatusNoContent {
		t.Fatalf("remove watcher: %d %s", req.Code, req.Body.String())
	}
	people, _ = readAdded(t, manager, "")
	if alice := people[c.alice.String()]; alice.in("addedTo", task) || alice.counts["addedTo"] != 0 {
		t.Errorf("removed from the task means no longer 'added to', got %v", alice.buckets)
	}
}

// ── contributions ─────────────────────────────────────────────────────────

func TestContributionsCountAddedToSeparatelyAndKeepCompletedMeaningAssigned(t *testing.T) {
	c := seedDash(t)
	assignee := c.ann
	open := c.insertTask(t, taskSpec{title: "Open", reporter: c.mgr, assignee: &assignee, status: "in_progress"})
	done := c.insertTask(t, taskSpec{title: "Done in range", reporter: c.mgr, assignee: &assignee, status: "done", completedAt: ago(24 * time.Hour)})
	old := c.insertTask(t, taskSpec{title: "Done long ago", reporter: c.mgr, assignee: &assignee, status: "done", completedAt: ago(90 * 24 * time.Hour)})
	own := c.insertTask(t, taskSpec{title: "Alice's own", reporter: c.mgr, assignee: &c.alice, status: "todo"})
	for _, task := range []uuid.UUID{open, done, old, own} {
		c.watch(t, task, c.alice)
	}

	w := getJSON(t, c.asManager(t, c.mgr), "/api/tasks/contributions?"+contribRange())
	mustStatus(t, w, http.StatusOK)
	var body struct {
		People []struct {
			UserID    string `json:"userId"`
			Completed int    `json:"completed"`
			Involved  int    `json:"involved"`
			Open      int    `json:"open"`
			AddedTo   int    `json:"addedTo"`
		} `json:"people"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var alice, ann *struct {
		UserID    string `json:"userId"`
		Completed int    `json:"completed"`
		Involved  int    `json:"involved"`
		Open      int    `json:"open"`
		AddedTo   int    `json:"addedTo"`
	}
	for i := range body.People {
		switch body.People[i].UserID {
		case c.alice.String():
			alice = &body.People[i]
		case c.ann.String():
			ann = &body.People[i]
		}
	}
	if alice == nil || ann == nil {
		t.Fatalf("both are listed: %s", w.Body.String())
	}
	// Added to: on it, not assigned, live in the range. The one from 90 days ago is out.
	if alice.AddedTo != 2 {
		t.Errorf("alice was added to the open task and the one done in range (not the old one, not her own): want 2, got %d", alice.AddedTo)
	}
	if alice.Completed != 0 {
		t.Errorf("a task they were added to is not credited as completed, got %d", alice.Completed)
	}
	if ann.Completed != 1 || ann.AddedTo != 0 {
		t.Errorf("the assignee is credited once and is not 'added to', got completed %d addedTo %d", ann.Completed, ann.AddedTo)
	}

	// The drill down lists the tasks behind the number.
	d := getJSON(t, c.asManager(t, c.mgr), "/api/tasks/contributions/"+c.alice.String()+"?"+contribRange())
	mustStatus(t, d, http.StatusOK)
	var detail struct {
		AddedTo   []ref `json:"addedTo"`
		Completed []ref `json:"completed"`
	}
	if err := json.Unmarshal(d.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	ids := refStrings(detail.AddedTo)
	if len(ids) != 2 || !contains(ids, open) || !contains(ids, done) || contains(ids, own) || contains(ids, old) {
		t.Errorf("detail.addedTo lists the two tasks, got %v", ids)
	}
	if len(detail.Completed) != 0 {
		t.Errorf("detail.completed stays assigned-only, got %v", refStrings(detail.Completed))
	}
}

// ── the saved scrum ───────────────────────────────────────────────────────

func TestASavedScrumRecordsAddedToAndDoesNotChangeWhenTheTaskLaterDoes(t *testing.T) {
	c := seedDash(t)
	assignee := c.ann
	task := c.insertTask(t, taskSpec{title: "Followed work", reporter: c.mgr, assignee: &assignee, status: "in_progress"})
	c.watch(t, task, c.alice)
	manager := c.asManager(t, c.mgr)

	id := saveScrum(t, manager, `{}`)

	read := func() (int, bool) {
		w := getJSON(t, manager, "/api/scrums/"+id)
		mustStatus(t, w, http.StatusOK)
		var s struct {
			Participants []struct {
				UserID string         `json:"userId"`
				Counts map[string]int `json:"counts"`
			} `json:"participants"`
			Entries []struct {
				UserID string `json:"userId"`
				TaskID string `json:"taskId"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(unwrap(w.Body.Bytes(), "scrum"), &s); err != nil {
			t.Fatalf("decode scrum %q: %v", w.Body.String(), err)
		}
		count := -1
		for _, p := range s.Participants {
			if p.UserID == c.alice.String() {
				count = p.Counts["addedTo"]
			}
		}
		found := false
		for _, e := range s.Entries {
			if e.UserID == c.alice.String() && e.TaskID == task.String() {
				found = true
			}
		}
		return count, found
	}
	count, found := read()
	if count != 1 {
		t.Errorf("the snapshot counts what alice was added to: want 1, got %d", count)
	}
	if !found {
		t.Error("the snapshot lists the task among alice's entries")
	}

	// A saved scrum never changes.
	if _, err := c.pool.Exec(context.Background(), `DELETE FROM task_watchers WHERE task_id = $1 AND user_id = $2`, task, c.alice); err != nil {
		t.Fatal(err)
	}
	if count, found = read(); count != 1 || !found {
		t.Errorf("the snapshot is a record of the day and does not follow later changes, got %d %v", count, found)
	}
}

// ── notifying the person who was added (decision 18) ──────────────────────

func TestBeingAddedToATaskNotifiesThePersonOnceAndNotAnExistingWatcherAgain(t *testing.T) {
	c := seedDash(t)
	notifier := &recordingNotifier{}
	c.svc.SetNotifier(notifier)
	manager := c.asManager(t, c.mgr)
	task := c.work(t, c.ann, "Shared", "todo")

	mustStatus(t, send(t, manager, http.MethodPost, "/api/tasks/"+task.String()+"/watchers",
		`{"userId":"`+c.alice.String()+`"}`), http.StatusOK)
	if notifier.count(c.alice) != 1 {
		t.Fatalf("the person added is told, got %d", notifier.count(c.alice))
	}
	if notifier.count(c.mgr) != 0 {
		t.Error("the person who added them is not told about their own action")
	}

	// Adding somebody who is already on the task is not 'being added'.
	mustStatus(t, send(t, manager, http.MethodPost, "/api/tasks/"+task.String()+"/watchers",
		`{"userId":"`+c.alice.String()+`"}`), http.StatusOK)
	if notifier.count(c.alice) != 1 {
		t.Errorf("an idempotent repeat must not notify again, got %d", notifier.count(c.alice))
	}

	// Following a task yourself needs no notification.
	mustStatus(t, send(t, c.asMember(t, c.pat), http.MethodPost, "/api/tasks/"+task.String()+"/watchers", ``), http.StatusOK)
	if notifier.count(c.pat) != 0 {
		t.Errorf("watching it yourself is not being added, got %d", notifier.count(c.pat))
	}
}

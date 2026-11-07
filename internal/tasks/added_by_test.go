package tasks

// Decision 38: only being added by somebody else counts as Added to. Adding yourself
// is following a task, and earns nothing.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
)

func (c *dashChart) addWatcherAs(t *testing.T, as, on uuid.UUID, task uuid.UUID) {
	t.Helper()
	body := ``
	if as != on {
		body = `{"userId":"` + on.String() + `"}`
	}
	r := c.asMember(t, as)
	if as == c.mgr {
		r = c.asManager(t, as)
	}
	mustStatus(t, send(t, r, http.MethodPost, "/api/tasks/"+task.String()+"/watchers", body), http.StatusOK)
}

func (c *dashChart) contributedAddedTo(t *testing.T, who uuid.UUID) int {
	t.Helper()
	w := getJSON(t, c.asManager(t, c.mgr), "/api/tasks/contributions?"+contribRange())
	mustStatus(t, w, http.StatusOK)
	var body struct {
		People []struct {
			UserID  string `json:"userId"`
			AddedTo int    `json:"addedTo"`
		} `json:"people"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, p := range body.People {
		if p.UserID == who.String() {
			return p.AddedTo
		}
	}
	t.Fatalf("%s is not listed: %s", who, w.Body.String())
	return 0
}

func (c *dashChart) scrumAddedTo(t *testing.T, who uuid.UUID) int {
	t.Helper()
	id := saveScrum(t, c.asManager(t, c.mgr), `{}`)
	w := getJSON(t, c.asManager(t, c.mgr), "/api/scrums/"+id)
	mustStatus(t, w, http.StatusOK)
	var s struct {
		Participants []struct {
			UserID string         `json:"userId"`
			Counts map[string]int `json:"counts"`
		} `json:"participants"`
	}
	if err := json.Unmarshal(unwrap(w.Body.Bytes(), "scrum"), &s); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.Participants {
		if p.UserID == who.String() {
			return p.Counts["addedTo"]
		}
	}
	t.Fatalf("%s is not in the scrum: %s", who, w.Body.String())
	return 0
}

func TestAddingYourselfToATaskIsNotAddedToAnywhere(t *testing.T) {
	c := seedDash(t)
	notifier := &recordingNotifier{}
	c.svc.SetNotifier(notifier)
	task := c.work(t, c.ann, "Interesting", "in_progress")

	c.addWatcherAs(t, c.alice, c.alice, task)

	if notifier.count(c.alice) != 0 {
		t.Errorf("nobody told her anything, got %d notifications", notifier.count(c.alice))
	}
	people, totals := readAdded(t, c.asManager(t, c.mgr), "")
	alice := people[c.alice.String()]
	if alice.in("addedTo", task) || alice.counts["addedTo"] != 0 || totals["addedTo"] != 0 {
		t.Errorf("the dashboard does not credit it, got %v %v, totals %v", alice.buckets["addedTo"], alice.counts, totals)
	}
	if got := c.contributedAddedTo(t, c.alice); got != 0 {
		t.Errorf("contribution does not credit it, got %d", got)
	}
	if got := c.scrumAddedTo(t, c.alice); got != 0 {
		t.Errorf("a saved scrum does not credit it, got %d", got)
	}

	// She is still on the task: following it is not undone.
	var on bool
	if err := c.pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM task_watchers WHERE task_id = $1 AND user_id = $2)`, task, c.alice).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Error("she follows it")
	}
}

func TestBeingAddedBySomebodyElseCountsEverywhere(t *testing.T) {
	c := seedDash(t)
	notifier := &recordingNotifier{}
	c.svc.SetNotifier(notifier)
	task := c.work(t, c.ann, "Shared", "in_progress")

	c.addWatcherAs(t, c.mgr, c.alice, task)

	if notifier.count(c.alice) != 1 {
		t.Errorf("she is told, got %d", notifier.count(c.alice))
	}
	people, _ := readAdded(t, c.asManager(t, c.mgr), "")
	if alice := people[c.alice.String()]; !alice.in("addedTo", task) || alice.counts["addedTo"] != 1 {
		t.Errorf("the dashboard credits it, got %v %v", alice.buckets["addedTo"], alice.counts)
	}
	if got := c.contributedAddedTo(t, c.alice); got != 1 {
		t.Errorf("contribution credits it, got %d", got)
	}
	if got := c.scrumAddedTo(t, c.alice); got != 1 {
		t.Errorf("a saved scrum credits it, got %d", got)
	}

	// Pressing Follow afterwards does not take that away.
	c.addWatcherAs(t, c.alice, c.alice, task)
	people, _ = readAdded(t, c.asManager(t, c.mgr), "")
	if alice := people[c.alice.String()]; !alice.in("addedTo", task) {
		t.Errorf("she was still added by somebody else, got %v", alice.buckets["addedTo"])
	}
}

func TestSomebodyAddingYouAfterYouFollowedItCountsAndTellsYou(t *testing.T) {
	c := seedDash(t)
	notifier := &recordingNotifier{}
	c.svc.SetNotifier(notifier)
	task := c.work(t, c.ann, "Shared", "in_progress")

	c.addWatcherAs(t, c.alice, c.alice, task)
	c.addWatcherAs(t, c.mgr, c.alice, task)

	if notifier.count(c.alice) != 1 {
		t.Errorf("that is being added, so she is told, got %d", notifier.count(c.alice))
	}
	people, _ := readAdded(t, c.asManager(t, c.mgr), "")
	if alice := people[c.alice.String()]; !alice.in("addedTo", task) {
		t.Errorf("and it counts, got %v", alice.buckets["addedTo"])
	}

	// Once. A repeat is nothing new.
	c.addWatcherAs(t, c.mgr, c.alice, task)
	if notifier.count(c.alice) != 1 {
		t.Errorf("once, got %d", notifier.count(c.alice))
	}
}

// Rows from before the column do not say who added them. Being added by somebody else
// has always notified, and adding yourself never has.
func TestExistingWatchersAreSortedByWhetherTheyWereEverNotified(t *testing.T) {
	c := seedDash(t)
	ctx := context.Background()
	task := c.work(t, c.ann, "Old", "in_progress")
	for _, who := range []uuid.UUID{c.alice, c.pat} {
		if _, err := c.pool.Exec(ctx, `INSERT INTO task_watchers (task_id, user_id, added_by) VALUES ($1, $2, NULL)`, task, who); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.pool.Exec(ctx, `
		INSERT INTO notifications (org_id, user_id, type, title, body, data)
		SELECT org_id, $1, 'task.added', 'You were added', 'x', jsonb_build_object('taskId', $2::text)
		FROM users WHERE id = $1
	`, c.alice, task.String()); err != nil {
		t.Fatal(err)
	}

	up, err := os.ReadFile("../../migrations/0023_task_watchers_added_by.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	people, _ := readAdded(t, c.asOwner(t), "")
	if alice := people[c.alice.String()]; !alice.in("addedTo", task) {
		t.Errorf("somebody told Alice she was added, got %v", alice.buckets["addedTo"])
	}
	if pat := people[c.pat.String()]; pat.in("addedTo", task) {
		t.Errorf("nobody ever told Pat anything, so he followed it, got %v", pat.buckets["addedTo"])
	}
}

package tasks

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
)

func newService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	return NewService(pool), pool
}

func createTask(t *testing.T, svc *Service, org *testsupport.Org, title string) *Task {
	t.Helper()
	return createTaskAs(t, svc, org, org.OwnerID, title)
}

// createTaskAs files a task in somebody else's name, so a test can be a person
// who did not report the task it is acting on.
func createTaskAs(t *testing.T, svc *Service, org *testsupport.Org, reporterID uuid.UUID, title string) *Task {
	t.Helper()
	task, err := svc.CreateTask(context.Background(), org.ID, reporterID,
		title, "", "", "", "", nil, nil, nil, nil, nil, false)
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return task
}

// ── Board and detail (ticket 07) ──────────────────────────────────────────

// CreateTask built a struct and never inserted, so a created task vanished.
func TestCreatedTaskIsReadableBack(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	created := createTask(t, svc, org, "Ship the thing")

	loaded, err := svc.GetTask(ctx, org.ID, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.Title != "Ship the thing" {
		t.Errorf("title: got %q", loaded.Title)
	}
	if loaded.Status != "todo" || loaded.Priority != "medium" || loaded.TaskType != "task" {
		t.Errorf("defaults not applied: %+v", loaded)
	}
	if loaded.ReporterName == "" {
		t.Error("reporter name should be loaded for rendering")
	}

	list, err := svc.ListTasks(ctx, org.ID, Filters{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("board should show the task, got %d", len(list))
	}
}

func TestListTasksIsEmptyForANewOrganization(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	list, err := svc.ListTasks(context.Background(), org.ID, Filters{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected no tasks, got %d", len(list))
	}
}

func TestListTasksFiltersByStatusAndSearch(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	a := createTask(t, svc, org, "Alpha task")
	createTask(t, svc, org, "Beta task")

	if _, err := svc.UpdateTask(ctx, org.ID, a.ID, org.OwnerID,
		map[string]interface{}{"status": "done"}, true); err != nil {
		t.Fatalf("update: %v", err)
	}

	done, err := svc.ListTasks(ctx, org.ID, Filters{Status: "done"})
	if err != nil {
		t.Fatalf("filter status: %v", err)
	}
	if len(done) != 1 || done[0].ID != a.ID {
		t.Errorf("status filter wrong: %+v", done)
	}

	found, err := svc.ListTasks(ctx, org.ID, Filters{Search: "beta"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) != 1 || found[0].Title != "Beta task" {
		t.Errorf("search should be case-insensitive on title: %+v", found)
	}
}

func TestTasksDoNotCrossOrganizations(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	theirTask, err := svc.CreateTask(ctx, theirs.ID, theirs.OwnerID, "Secret", "", "", "", "", nil, nil, nil, nil, nil, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.GetTask(ctx, mine.ID, theirTask.ID); err == nil {
		t.Error("a task from another organization must not be readable")
	}
	if err := svc.DeleteTask(ctx, mine.ID, theirTask.ID, mine.OwnerID, true); err == nil {
		t.Error("a task from another organization must not be deletable, not even by an admin")
	}
	list, _ := svc.ListTasks(ctx, mine.ID, Filters{})
	if len(list) != 0 {
		t.Errorf("board leaked another organization's tasks: %+v", list)
	}
}

func TestUpdateTaskAppliesOnlyWhitelistedFields(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Original")

	updated, err := svc.UpdateTask(ctx, org.ID, task.ID, org.OwnerID, map[string]interface{}{
		"title":    "Renamed",
		"priority": "high",
		// Must be ignored: reassigning the tenant would move the row.
		"orgId": other.ID.String(),
	}, true)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != "Renamed" || updated.Priority != "high" {
		t.Errorf("update not applied: %+v", updated)
	}
	if updated.OrgID != org.ID {
		t.Error("org_id must not be settable through a task update")
	}
}

func TestCompletingATaskStampsAndClearsCompletedAt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Finish me")

	done, err := svc.UpdateTask(ctx, org.ID, task.ID, org.OwnerID,
		map[string]interface{}{"status": "done"}, true)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.CompletedAt == nil {
		t.Error("completing a task should stamp completedAt")
	}

	reopened, err := svc.UpdateTask(ctx, org.ID, task.ID, org.OwnerID,
		map[string]interface{}{"status": "todo"}, true)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.CompletedAt != nil {
		t.Error("reopening a task should clear completedAt")
	}
}

func TestDeleteTaskRemovesItFromTheBoard(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Temporary")

	if err := svc.DeleteTask(ctx, org.ID, task.ID, org.OwnerID, false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetTask(ctx, org.ID, task.ID); err == nil {
		t.Error("a deleted task should not load")
	}
	list, _ := svc.ListTasks(ctx, org.ID, Filters{})
	if len(list) != 0 {
		t.Errorf("deleted task still on the board: %+v", list)
	}
	if err := svc.DeleteTask(ctx, org.ID, task.ID, org.OwnerID, false); err == nil {
		t.Error("deleting twice should report not-found")
	}
}

func TestReassigningATaskPersists(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "assignee@example.test", "member")
	task := createTask(t, svc, org, "Assign me")

	updated, err := svc.UpdateTask(ctx, org.ID, task.ID, org.OwnerID,
		map[string]interface{}{"assigneeId": mate.String()}, true)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if updated.AssigneeID == nil || *updated.AssigneeID != mate {
		t.Fatalf("assignee not persisted: %+v", updated.AssigneeID)
	}
	if updated.AssigneeName == "" {
		t.Error("assignee name should be loaded for rendering")
	}

	byAssignee, _ := svc.ListTasks(ctx, org.ID, Filters{AssigneeID: &mate})
	if len(byAssignee) != 1 {
		t.Errorf("assignee filter wrong: %+v", byAssignee)
	}
}

// ── Labels and activity (ticket 08) ───────────────────────────────────────

func TestLabelLifecycleOnATask(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Labelled")

	label, err := svc.CreateLabel(ctx, org.ID, "urgent", "#ff0000")
	if err != nil {
		t.Fatalf("create label: %v", err)
	}

	labels, err := svc.ListLabels(ctx, org.ID)
	if err != nil {
		t.Fatalf("list labels: %v", err)
	}
	if len(labels) != 1 || labels[0].Color != "#ff0000" {
		t.Errorf("label listing wrong: %+v", labels)
	}

	// Attaching twice is a no-op rather than an error.
	for i := 0; i < 2; i++ {
		if err := svc.AddLabel(ctx, org.ID, task.ID, label.ID); err != nil {
			t.Fatalf("add label (pass %d): %v", i, err)
		}
	}
	loaded, _ := svc.GetTask(ctx, org.ID, task.ID)
	if len(loaded.Labels) != 1 {
		t.Fatalf("expected one label on the task, got %d", len(loaded.Labels))
	}

	byLabel, _ := svc.ListTasks(ctx, org.ID, Filters{LabelID: &label.ID})
	if len(byLabel) != 1 {
		t.Errorf("label filter wrong: %+v", byLabel)
	}

	if err := svc.RemoveLabel(ctx, org.ID, task.ID, label.ID); err != nil {
		t.Fatalf("remove label: %v", err)
	}
	loaded, _ = svc.GetTask(ctx, org.ID, task.ID)
	if len(loaded.Labels) != 0 {
		t.Errorf("label not removed: %+v", loaded.Labels)
	}
}

func TestLabelFromAnotherOrganizationIsRefused(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Mine")

	foreign, err := svc.CreateLabel(ctx, other.ID, "theirs", "")
	if err != nil {
		t.Fatalf("create foreign label: %v", err)
	}
	if err := svc.AddLabel(ctx, org.ID, task.ID, foreign.ID); err == nil {
		t.Fatal("a label from another organization must not attach")
	}
}

func TestActivityTrailRecordsWhatChanged(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "actor@example.test", "member")
	task := createTask(t, svc, org, "Track me")

	time.Sleep(2 * time.Millisecond)
	if _, err := svc.UpdateTask(ctx, org.ID, task.ID, mate,
		map[string]interface{}{"status": "in_progress"}, true); err != nil {
		t.Fatalf("update: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.AddComment(ctx, org.ID, task.ID, mate, "on it", nil); err != nil {
		t.Fatalf("comment: %v", err)
	}

	activities, err := svc.GetActivities(ctx, org.ID, task.ID)
	if err != nil {
		t.Fatalf("activities: %v", err)
	}
	if len(activities) < 3 {
		t.Fatalf("expected creation, status change and comment, got %d", len(activities))
	}
	// Newest first.
	if activities[0].Field != "comment" {
		t.Errorf("activity should be reverse-chronological, newest is %q", activities[0].Field)
	}
	if activities[0].UserName == "" {
		t.Error("activity should name who did it")
	}

	var statusEntry *TaskActivity
	for i := range activities {
		if activities[i].Field == "status" {
			statusEntry = &activities[i]
		}
	}
	if statusEntry == nil {
		t.Fatal("status change not recorded")
	}
	if statusEntry.OldValue != "todo" || statusEntry.NewValue != "in_progress" {
		t.Errorf("status transition not captured: %q -> %q", statusEntry.OldValue, statusEntry.NewValue)
	}
}

// A no-op update should not litter the history.
func TestUnchangedFieldsDoNotCreateActivity(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Steady")

	before, _ := svc.GetActivities(ctx, org.ID, task.ID)
	if _, err := svc.UpdateTask(ctx, org.ID, task.ID, org.OwnerID,
		map[string]interface{}{"title": "Steady"}, true); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, _ := svc.GetActivities(ctx, org.ID, task.ID)

	if len(after) != len(before) {
		t.Errorf("setting a field to its current value should not record activity: %d -> %d",
			len(before), len(after))
	}
}

// fieldActivity reads the history of one field, oldest first.
func fieldActivity(t *testing.T, svc *Service, org *testsupport.Org, taskID uuid.UUID, field string) []TaskActivity {
	t.Helper()
	all, err := svc.GetActivities(context.Background(), org.ID, taskID)
	if err != nil {
		t.Fatalf("get activities: %v", err)
	}
	out := []TaskActivity{}
	for _, a := range all {
		if a.Field == field {
			out = append([]TaskActivity{a}, out...)
		}
	}
	return out
}

// Only title, status and a few others knew their prior value, so everything else
// looked changed on every save: a card drag wrote a history row each time.
func TestSavingTheSameValueRecordsNoHistoryForAnyField(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)
	r := asOwner(t, svc, org)
	parent := createTask(t, svc, org, "Parent")

	cases := []struct {
		name, field, first, again string
	}{
		{"position", "position", `5`, `5`},
		{"story points", "storyPoints", `3`, `3`},
		{"due date", "dueDate", `"2026-11-10T00:00:00Z"`, `"2026-11-10T00:00:00Z"`},
		{"due date, same instant in another zone", "dueDate", `"2026-11-10T00:00:00Z"`, `"2026-11-10T05:30:00+05:30"`},
		{"start date", "startDate", `"2026-11-01T09:00:00Z"`, `"2026-11-01T09:00:00Z"`},
		{"parent task", "parentTaskId", `"` + parent.ID.String() + `"`, `"` + parent.ID.String() + `"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := createTask(t, svc, org, "Dragged")
			body := func(value string) string { return `{"` + tc.field + `":` + value + `}` }
			mustStatus(t, patchTask(t, r, task.ID.String(), body(tc.first)), http.StatusOK)
			mustStatus(t, patchTask(t, r, task.ID.String(), body(tc.again)), http.StatusOK)
			if got := fieldActivity(t, svc, org, task.ID, tc.field); len(got) != 1 {
				t.Errorf("only the first save changed anything, got %d rows: %+v", len(got), got)
			}
		})
	}
}

func TestClearingAFieldRecordsNothingAsTheNewValue(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)
	r := asOwner(t, svc, org)

	task := createTask(t, svc, org, "Handed over")
	owner := org.OwnerID.String()
	mustStatus(t, patchTask(t, r, task.ID.String(), `{"assigneeId":"`+owner+`","dueDate":"2026-11-10T00:00:00Z"}`), http.StatusOK)
	mustStatus(t, patchTask(t, r, task.ID.String(), `{"assigneeId":null,"dueDate":null}`), http.StatusOK)

	assignee := fieldActivity(t, svc, org, task.ID, "assigneeId")
	if len(assignee) != 2 || assignee[1].OldValue != owner || assignee[1].NewValue != "" {
		t.Errorf("unassigning should record the old assignee and an empty new value, got %+v", assignee)
	}
	due := fieldActivity(t, svc, org, task.ID, "dueDate")
	if len(due) != 2 || due[1].OldValue != "2026-11-10T00:00:00Z" || due[1].NewValue != "" {
		t.Errorf("clearing a date should record the old date and an empty new value, got %+v", due)
	}

	// Clearing what is already empty is not a change.
	mustStatus(t, patchTask(t, r, task.ID.String(), `{"assigneeId":null,"dueDate":null,"storyPoints":null}`), http.StatusOK)
	if got := fieldActivity(t, svc, org, task.ID, "assigneeId"); len(got) != 2 {
		t.Errorf("clearing an empty assignee should record nothing, got %+v", got)
	}
	if got := fieldActivity(t, svc, org, task.ID, "storyPoints"); len(got) != 0 {
		t.Errorf("clearing empty story points should record nothing, got %+v", got)
	}
}

func TestDatesInHistoryAreRFC3339WhateverZoneTheyArrivedIn(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)
	r := asOwner(t, svc, org)

	task := createTask(t, svc, org, "Scheduled")
	mustStatus(t, patchTask(t, r, task.ID.String(), `{"dueDate":"2026-11-10T05:30:00+05:30"}`), http.StatusOK)
	mustStatus(t, patchTask(t, r, task.ID.String(), `{"dueDate":"2026-11-11T00:00:00Z"}`), http.StatusOK)

	due := fieldActivity(t, svc, org, task.ID, "dueDate")
	if len(due) != 2 {
		t.Fatalf("want 2 rows, got %+v", due)
	}
	if due[0].NewValue != "2026-11-10T00:00:00Z" {
		t.Errorf("first new value should be RFC3339 UTC, got %q", due[0].NewValue)
	}
	if due[1].OldValue != "2026-11-10T00:00:00Z" || due[1].NewValue != "2026-11-11T00:00:00Z" {
		t.Errorf("second row should be RFC3339 on both sides, got %+v", due[1])
	}
}

func TestCommentsRoundTrip(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Discuss")

	if _, err := svc.AddComment(ctx, org.ID, task.ID, org.OwnerID, "first thought", nil); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	comments, err := svc.ListComments(ctx, org.ID, task.ID)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) != 1 || comments[0].Content != "first thought" {
		t.Fatalf("comment not stored: %+v", comments)
	}
	if comments[0].UserName == "" {
		t.Error("comment should carry its author's name")
	}
}

func TestCommentOnAnotherOrganizationsTaskIsRefused(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	theirTask, _ := svc.CreateTask(ctx, theirs.ID, theirs.OwnerID, "Theirs", "", "", "", "", nil, nil, nil, nil, nil, false)
	if _, err := svc.AddComment(ctx, mine.ID, theirTask.ID, mine.OwnerID, "sneaky", nil); err == nil {
		t.Fatal("commenting across organizations must be refused")
	}
}

func TestGetTaskUnknownIsNotFound(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	if _, err := svc.GetTask(context.Background(), org.ID, uuid.New()); err == nil {
		t.Fatal("an unknown task must report not-found")
	}
}

// ── Watchers (ticket 07) ──────────────────────────────────────────────────
//
// The frontend already declared these endpoints; the server had none.

func TestWatchersLifecycle(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "watcher@example.test", "member")
	task := createTask(t, svc, org, "Watch me")

	for i := 0; i < 2; i++ {
		if _, err := svc.AddWatcher(ctx, org.ID, task.ID, mate, org.OwnerID); err != nil {
			t.Fatalf("add watcher (pass %d): %v", i, err)
		}
	}

	watchers, err := svc.ListWatchers(ctx, org.ID, task.ID)
	if err != nil {
		t.Fatalf("list watchers: %v", err)
	}
	if len(watchers) != 1 {
		t.Fatalf("watching twice must not duplicate, got %d", len(watchers))
	}
	if watchers[0].Name == "" {
		t.Error("a watcher should carry a name for rendering")
	}

	if err := svc.RemoveWatcher(ctx, org.ID, task.ID, mate); err != nil {
		t.Fatalf("remove watcher: %v", err)
	}
	watchers, _ = svc.ListWatchers(ctx, org.ID, task.ID)
	if len(watchers) != 0 {
		t.Errorf("watcher not removed: %+v", watchers)
	}
}

func TestWatcherFromAnotherOrganizationIsRefused(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Mine")

	if _, err := svc.AddWatcher(ctx, org.ID, task.ID, other.OwnerID, org.OwnerID); err == nil {
		t.Fatal("a user from another organization must not watch this task")
	}
}

// Watching is what makes a change reach someone who is neither the assignee
// nor the person making it.
func TestWatchersAreNotifiedOfChangesButNotTheirOwn(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	watcher := testsupport.SeedUser(t, pool, org, "follower@example.test", "member")
	task := createTask(t, svc, org, "Followed")

	notifier := &recordingNotifier{}
	svc.SetNotifier(notifier)

	if _, err := svc.AddWatcher(ctx, org.ID, task.ID, watcher, org.OwnerID); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if _, err := svc.AddWatcher(ctx, org.ID, task.ID, org.OwnerID, org.OwnerID); err != nil {
		t.Fatalf("watch as owner: %v", err)
	}

	if _, err := svc.UpdateTask(ctx, org.ID, task.ID, org.OwnerID,
		map[string]interface{}{"status": "in_progress"}, true); err != nil {
		t.Fatalf("update: %v", err)
	}

	if notifier.count(watcher) != 1 {
		t.Errorf("the watcher should have been notified, got %d", notifier.count(watcher))
	}
	if notifier.count(org.OwnerID) != 0 {
		t.Error("the person making the change must not be notified about it")
	}
}

type recordingNotifier struct {
	mu   sync.Mutex
	sent map[uuid.UUID]int
}

func (r *recordingNotifier) Notify(_ context.Context, _, userID uuid.UUID, _, _, _ string, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent == nil {
		r.sent = map[uuid.UUID]int{}
	}
	r.sent[userID]++
	return nil
}

func (r *recordingNotifier) count(userID uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent[userID]
}

// ── Who may assign work to whom ───────────────────────────────────────────

// Assignment runs down the org chart. Before this, anybody could assign work
// to anybody — including to their own manager.
func TestAManagerCanAssignToTheirOwnTeam(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	manager := testsupport.SeedUser(t, pool, org, "manager@example.test", "manager")
	report := testsupport.SeedUser(t, pool, org, "report@example.test", "member")
	testsupport.SeedTeam(t, pool, org, "photon", &manager, nil, report)

	task, err := svc.CreateTask(ctx, org.ID, manager, "Ship it", "", "", "", "",
		nil, &report, nil, nil, nil, false)
	if err != nil {
		t.Fatalf("a manager could not assign to their own report: %v", err)
	}
	if task.AssigneeID == nil || *task.AssigneeID != report {
		t.Errorf("wrong assignee: %+v", task.AssigneeID)
	}
}

func TestAMemberCannotAssignWorkUpwardsOrSideways(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	manager := testsupport.SeedUser(t, pool, org, "boss@example.test", "manager")
	alice := testsupport.SeedUser(t, pool, org, "alice@example.test", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob@example.test", "member")
	testsupport.SeedTeam(t, pool, org, "photon", &manager, nil, alice, bob)

	// Upwards.
	if _, err := svc.CreateTask(ctx, org.ID, alice, "Do my job", "", "", "", "",
		nil, &manager, nil, nil, nil, false); err == nil {
		t.Error("a member assigned work to their manager")
	}
	// Sideways.
	if _, err := svc.CreateTask(ctx, org.ID, alice, "You do it", "", "", "", "",
		nil, &bob, nil, nil, nil, false); err == nil {
		t.Error("a member assigned work to a peer")
	}
	// But always to themselves.
	if _, err := svc.CreateTask(ctx, org.ID, alice, "Mine", "", "", "", "",
		nil, &alice, nil, nil, nil, false); err != nil {
		t.Errorf("a member could not assign work to themselves: %v", err)
	}
}

func TestAManagerReachesSubTeamsButNotOtherTeams(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	head := testsupport.SeedUser(t, pool, org, "head@example.test", "manager")
	deep := testsupport.SeedUser(t, pool, org, "deep@example.test", "member")
	elsewhere := testsupport.SeedUser(t, pool, org, "elsewhere@example.test", "member")

	parent := testsupport.SeedTeam(t, pool, org, "engineering", &head, nil)
	testsupport.SeedTeam(t, pool, org, "platform", nil, &parent, deep)
	testsupport.SeedTeam(t, pool, org, "sales", nil, nil, elsewhere)

	if _, err := svc.CreateTask(ctx, org.ID, head, "Sub-team work", "", "", "", "",
		nil, &deep, nil, nil, nil, false); err != nil {
		t.Errorf("a manager could not reach a team beneath theirs: %v", err)
	}
	if _, err := svc.CreateTask(ctx, org.ID, head, "Not yours", "", "", "", "",
		nil, &elsewhere, nil, nil, nil, false); err == nil {
		t.Error("a manager assigned work outside their part of the chart")
	}
}

func TestAnOrgAdminCanAssignToAnybody(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	stranger := testsupport.SeedUser(t, pool, org, "stranger@example.test", "member")

	if _, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Top down", "", "", "", "",
		nil, &stranger, nil, nil, nil, true); err != nil {
		t.Errorf("an organization admin could not assign work: %v", err)
	}
}

func TestReassigningAnExistingTaskObeysTheSameRule(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	alice := testsupport.SeedUser(t, pool, org, "alice2@example.test", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob2@example.test", "member")

	task, err := svc.CreateTask(ctx, org.ID, alice, "Mine", "", "", "", "",
		nil, &alice, nil, nil, nil, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The rule cannot be dodged by creating first and reassigning after.
	if _, err := svc.UpdateTask(ctx, org.ID, task.ID, alice,
		map[string]interface{}{"assigneeId": bob.String()}, false); err == nil {
		t.Error("a member reassigned a task to a peer")
	}
}

func TestAssigningToSomebodyInAnotherOrganizationIsRefused(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	if _, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Cross tenant", "", "", "", "",
		nil, &theirs.OwnerID, nil, nil, nil, true); err == nil {
		t.Error("assigned a task across organizations")
	}
}

// An organization's owner or admin sits above the chart even when they are
// listed on a team, so their own manager cannot hand them work.
func TestAManagerCannotAssignToAnOrganizationAdminOnTheirTeam(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	manager := testsupport.SeedUser(t, pool, org, "mgr3@example.test", "manager")
	admin := testsupport.SeedUser(t, pool, org, "admin3@example.test", "admin")
	ordinary := testsupport.SeedUser(t, pool, org, "plain3@example.test", "member")

	// The owner and an admin both sit on the team the manager runs.
	testsupport.SeedTeam(t, pool, org, "photon", &manager, nil, org.OwnerID, admin, ordinary)

	if _, err := svc.CreateTask(ctx, org.ID, manager, "For the owner", "", "", "", "",
		nil, &org.OwnerID, nil, nil, nil, false); err == nil {
		t.Error("a manager assigned work to the organization owner")
	}
	if _, err := svc.CreateTask(ctx, org.ID, manager, "For the admin", "", "", "", "",
		nil, &admin, nil, nil, nil, false); err == nil {
		t.Error("a manager assigned work to an organization admin")
	}
	// The ordinary team member is still fair game.
	if _, err := svc.CreateTask(ctx, org.ID, manager, "For a report", "", "", "", "",
		nil, &ordinary, nil, nil, nil, false); err != nil {
		t.Errorf("a manager could not assign to an ordinary report: %v", err)
	}
}

// ── The manager's view of their team's work ───────────────────────────────

func TestTeamScopeShowsWhatTheManagersPeopleAreWorkingOn(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	manager := testsupport.SeedUser(t, pool, org, "lead@example.test", "manager")
	alice := testsupport.SeedUser(t, pool, org, "alice4@example.test", "member")
	stranger := testsupport.SeedUser(t, pool, org, "stranger4@example.test", "member")

	testsupport.SeedTeam(t, pool, org, "photon", &manager, nil, alice)
	testsupport.SeedTeam(t, pool, org, "other", nil, nil, stranger)

	if _, err := svc.CreateTask(ctx, org.ID, manager, "Alice's job", "", "", "", "",
		nil, &alice, nil, nil, nil, false); err != nil {
		t.Fatalf("assign to alice: %v", err)
	}
	if _, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Somebody else's job", "", "", "", "",
		nil, &stranger, nil, nil, nil, true); err != nil {
		t.Fatalf("assign to stranger: %v", err)
	}

	tasks, err := svc.ListTasks(ctx, org.ID, Filters{ManagedBy: &manager})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Alice's job" {
		t.Fatalf("team scope returned the wrong work: %+v", titlesOf(tasks))
	}
}

func TestTeamScopeReachesTeamsNestedUnderneath(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	lead := testsupport.SeedUser(t, pool, org, "lead5@example.test", "manager")
	intern := testsupport.SeedUser(t, pool, org, "intern5@example.test", "member")

	// An employee who runs a team of interns beneath their own.
	parent := testsupport.SeedTeam(t, pool, org, "engineering", &lead, nil)
	testsupport.SeedTeam(t, pool, org, "interns", nil, &parent, intern)

	if _, err := svc.CreateTask(ctx, org.ID, lead, "Intern work", "", "", "", "",
		nil, &intern, nil, nil, nil, false); err != nil {
		t.Fatalf("assign: %v", err)
	}

	tasks, err := svc.ListTasks(ctx, org.ID, Filters{ManagedBy: &lead})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("a sub-team's work is missing: %+v", titlesOf(tasks))
	}
}

func TestTeamScopeCoversEveryTeamSomebodyManagesAndCanNarrowToOne(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	manager := testsupport.SeedUser(t, pool, org, "two@example.test", "manager")
	first := testsupport.SeedUser(t, pool, org, "first@example.test", "member")
	second := testsupport.SeedUser(t, pool, org, "second@example.test", "member")

	teamA := testsupport.SeedTeam(t, pool, org, "alpha", &manager, nil, first)
	testsupport.SeedTeam(t, pool, org, "beta", &manager, nil, second)

	for _, who := range []uuid.UUID{first, second} {
		if _, err := svc.CreateTask(ctx, org.ID, manager, "job for "+who.String()[:4], "", "", "", "",
			nil, &who, nil, nil, nil, false); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}

	both, err := svc.ListTasks(ctx, org.ID, Filters{ManagedBy: &manager})
	if err != nil {
		t.Fatalf("list both: %v", err)
	}
	if len(both) != 2 {
		t.Errorf("managing two teams should show both: %+v", titlesOf(both))
	}

	// Narrowed to one of the two teams.
	justAlpha, err := svc.ListTasks(ctx, org.ID, Filters{ManagedBy: &manager, TeamID: &teamA})
	if err != nil {
		t.Fatalf("list alpha: %v", err)
	}
	if len(justAlpha) != 1 {
		t.Errorf("filtering to one team did not narrow: %+v", titlesOf(justAlpha))
	}

	// And narrowed to one person.
	justSecond, err := svc.ListTasks(ctx, org.ID, Filters{ManagedBy: &manager, AssigneeID: &second})
	if err != nil {
		t.Fatalf("list person: %v", err)
	}
	if len(justSecond) != 1 {
		t.Errorf("filtering to one person did not narrow: %+v", titlesOf(justSecond))
	}
}

func TestSomebodyWhoManagesNothingSeesNoTeamWork(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	plain := testsupport.SeedUser(t, pool, org, "plain6@example.test", "member")

	if _, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Not theirs", "", "", "", "",
		nil, &plain, nil, nil, nil, true); err != nil {
		t.Fatalf("assign: %v", err)
	}

	tasks, err := svc.ListTasks(ctx, org.ID, Filters{ManagedBy: &plain})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("somebody who manages nobody saw team work: %+v", titlesOf(tasks))
	}
}

func TestManagedTeamsListsEachTeamAndItsPeople(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	manager := testsupport.SeedUser(t, pool, org, "mt@example.test", "manager")
	alice := testsupport.SeedUser(t, pool, org, "mt-alice@example.test", "member")
	intern := testsupport.SeedUser(t, pool, org, "mt-intern@example.test", "member")
	outside := testsupport.SeedUser(t, pool, org, "mt-outside@example.test", "member")

	parent := testsupport.SeedTeam(t, pool, org, "engineering", &manager, nil, alice)
	testsupport.SeedTeam(t, pool, org, "interns", nil, &parent, intern)
	testsupport.SeedTeam(t, pool, org, "sales", nil, nil, outside)

	teams, err := svc.ManagedTeams(ctx, org.ID, manager)
	if err != nil {
		t.Fatalf("managed teams: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("teams: got %d want 2 (own plus nested): %+v", len(teams), teams)
	}
	seen := map[string][]string{}
	for _, team := range teams {
		for _, m := range team.Members {
			seen[team.Name] = append(seen[team.Name], m.Name)
		}
	}
	if len(seen["engineering"]) != 1 || len(seen["interns"]) != 1 {
		t.Errorf("wrong membership: %+v", seen)
	}
	for _, team := range teams {
		if team.Name == "sales" {
			t.Error("a team they do not manage appeared")
		}
	}
}

func titlesOf(tasks []Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.Title
	}
	return out
}

// "My tasks" is the work that is yours to do: assigned to you, or raised by you
// and not handed to anybody yet, or creating a task would make it disappear from
// your own view. Work you raised and handed to somebody else is theirs: it was
// in "My tasks" too, so a manager's own list filled up with their people's work.
func TestMyTasksIsTheWorkThatIsMineToDo(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	me := testsupport.SeedUser(t, pool, org, "me7@example.test", "manager")
	report := testsupport.SeedUser(t, pool, org, "them7@example.test", "member")
	testsupport.SeedTeam(t, pool, org, "mine7", &me, nil, report)

	if _, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Given to me", "", "", "", "",
		nil, &me, nil, nil, nil, true); err != nil {
		t.Fatalf("assign to me: %v", err)
	}
	if _, err := svc.CreateTask(ctx, org.ID, me, "Raised by me, unassigned", "", "", "", "",
		nil, nil, nil, nil, nil, false); err != nil {
		t.Fatalf("raised by me: %v", err)
	}
	if _, err := svc.CreateTask(ctx, org.ID, me, "Handed to my report", "", "", "", "",
		nil, &report, nil, nil, nil, false); err != nil {
		t.Fatalf("handed out: %v", err)
	}
	if _, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Nothing to do with me", "", "", "", "",
		nil, &org.OwnerID, nil, nil, nil, true); err != nil {
		t.Fatalf("someone else: %v", err)
	}

	mine, err := svc.ListTasks(ctx, org.ID, Filters{Mine: &me})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := titlesOf(mine); len(got) != 2 || !hasTitle(got, "Given to me") || !hasTitle(got, "Raised by me, unassigned") {
		t.Errorf("my tasks: got %v", got)
	}

	created, err := svc.ListTasks(ctx, org.ID, Filters{Mine: &me, MineAs: MineCreated})
	if err != nil {
		t.Fatalf("list created: %v", err)
	}
	if got := titlesOf(created); len(got) != 1 || got[0] != "Handed to my report" {
		t.Errorf("created by me: got %v", got)
	}
}

func hasTitle(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The create form should only offer people the org chart will accept, so
// nobody fills in a task and is refused on submit.
func TestAssignableUsersMatchesWhoMayActuallyBeAssigned(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	manager := testsupport.SeedUser(t, pool, org, "assignable-mgr@example.test", "manager")
	report := testsupport.SeedUser(t, pool, org, "assignable-report@example.test", "member")
	admin := testsupport.SeedUser(t, pool, org, "assignable-admin@example.test", "admin")
	outsider := testsupport.SeedUser(t, pool, org, "assignable-outsider@example.test", "member")

	testsupport.SeedTeam(t, pool, org, "photon", &manager, nil, report, admin)
	testsupport.SeedTeam(t, pool, org, "sales", nil, nil, outsider)

	people, err := svc.AssignableUsers(ctx, org.ID, manager, false)
	if err != nil {
		t.Fatalf("assignable: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, p := range people {
		got[p.UserID] = true
	}

	if !got[manager] {
		t.Error("you can always assign work to yourself")
	}
	if !got[report] {
		t.Error("a report on the managed team is missing")
	}
	if got[admin] {
		t.Error("an organization admin on the team was offered")
	}
	if got[outsider] {
		t.Error("somebody on another team was offered")
	}

	// And every name offered really is accepted.
	for _, p := range people {
		mayAssign, err := svc.MayAssign(ctx, org.ID, manager, p.UserID, false)
		if err != nil {
			t.Fatalf("may assign: %v", err)
		}
		if !mayAssign {
			t.Errorf("%s was offered but would be refused", p.Name)
		}
	}
}

func TestAnOrgAdminIsOfferedEverybody(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	somebody := testsupport.SeedUser(t, pool, org, "anyone@example.test", "member")

	people, err := svc.AssignableUsers(ctx, org.ID, org.OwnerID, true)
	if err != nil {
		t.Fatalf("assignable: %v", err)
	}
	var found bool
	for _, p := range people {
		if p.UserID == somebody {
			found = true
		}
	}
	if !found {
		t.Error("an organization admin should be able to assign to anybody")
	}
}

func TestSomebodyWhoManagesNothingIsOfferedOnlyThemselves(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	plain := testsupport.SeedUser(t, pool, org, "plain-assign@example.test", "member")
	testsupport.SeedUser(t, pool, org, "other-assign@example.test", "member")

	people, err := svc.AssignableUsers(ctx, org.ID, plain, false)
	if err != nil {
		t.Fatalf("assignable: %v", err)
	}
	if len(people) != 1 || people[0].UserID != plain {
		t.Errorf("expected only themselves, got %+v", people)
	}
}

// Being added to a task is being asked to pay attention to it, so it is listed
// for you under "Added to" — not only in the notification that told you about it.
func TestATaskYouWereAddedToIsInYourList(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	me := testsupport.SeedUser(t, pool, org, "added-to-task@example.test", "member")
	somebodyElse := testsupport.SeedUser(t, pool, org, "owner-of-task@example.test", "member")

	task, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Not mine at all", "", "", "", "",
		nil, &somebodyElse, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	before, err := svc.ListTasks(ctx, org.ID, Filters{Mine: &me, MineAs: MineAdded})
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("nothing should be mine yet: %+v", titlesOf(before))
	}

	if _, err := svc.AddWatcher(ctx, org.ID, task.ID, me, org.OwnerID); err != nil {
		t.Fatalf("add to task: %v", err)
	}

	after, err := svc.ListTasks(ctx, org.ID, Filters{Mine: &me, MineAs: MineAdded})
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(after) != 1 || after[0].ID != task.ID {
		t.Errorf("a task I was added to is missing: %+v", titlesOf(after))
	}
}

// And taking somebody off it takes it back out of their list.
func TestBeingRemovedFromATaskTakesItOutOfYourList(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	me := testsupport.SeedUser(t, pool, org, "removed-from-task@example.test", "member")

	task, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Briefly mine", "", "", "", "",
		nil, nil, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AddWatcher(ctx, org.ID, task.ID, me, org.OwnerID); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := svc.RemoveWatcher(ctx, org.ID, task.ID, me); err != nil {
		t.Fatalf("remove: %v", err)
	}

	mine, err := svc.ListTasks(ctx, org.ID, Filters{Mine: &me, MineAs: MineAdded})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mine) != 0 {
		t.Errorf("it should have gone: %+v", titlesOf(mine))
	}
}

// A task must not appear twice because you are both assigned to it and on it.
func TestATaskYouAreBothAssignedAndAddedToAppearsOnce(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	me := testsupport.SeedUser(t, pool, org, "both@example.test", "member")

	task, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Doubly mine", "", "", "", "",
		nil, &me, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AddWatcher(ctx, org.ID, task.ID, me, org.OwnerID); err != nil {
		t.Fatalf("add: %v", err)
	}

	mine, err := svc.ListTasks(ctx, org.ID, Filters{Mine: &me})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mine) != 1 {
		t.Errorf("listed %d times: %+v", len(mine), titlesOf(mine))
	}
}

// Adding and removing have to be symmetric: somebody who can put you on a task
// can take you off it again, or a mistake could only be undone by its victim.
func TestWhoeverCanAddYouToATaskCanTakeYouOff(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	manager := testsupport.SeedUser(t, pool, org, "sym-mgr@example.test", "manager")
	report := testsupport.SeedUser(t, pool, org, "sym-report@example.test", "member")
	testsupport.SeedTeam(t, pool, org, "photon", &manager, nil, report)

	task, err := svc.CreateTask(ctx, org.ID, manager, "Shared", "", "", "", "",
		nil, nil, nil, nil, nil, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The manager may put their report on it...
	mayAdd, err := svc.MayAssign(ctx, org.ID, manager, report, false)
	if err != nil {
		t.Fatalf("may assign: %v", err)
	}
	if !mayAdd {
		t.Fatal("a manager should be able to put their report on a task")
	}
	if _, err := svc.AddWatcher(ctx, org.ID, task.ID, report, org.OwnerID); err != nil {
		t.Fatalf("add: %v", err)
	}

	// ...and the same test governs taking them off again.
	if err := svc.RemoveWatcher(ctx, org.ID, task.ID, report); err != nil {
		t.Fatalf("remove: %v", err)
	}
	watchers, err := svc.ListWatchers(ctx, org.ID, task.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, w := range watchers {
		if w.UserID == report {
			t.Error("they are still on the task")
		}
	}
}

// ── Comment attachments ───────────────────────────────────────────────────

func seedTaskUpload(t *testing.T, pool *pgxpool.Pool, orgID, taskID, uploader uuid.UUID, filename string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO attachments (id, org_id, uploaded_by, category, ref_id, object_key,
		                         filename, content_type, size_bytes, created_at)
		VALUES (gen_random_uuid(), $1, $2, 'tasks', $3, $4, $5, 'image/png', 2048, NOW())
		RETURNING id
	`, orgID, uploader, taskID.String(), "key/"+uuid.New().String(), filename).Scan(&id); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
	return id
}

func TestACommentCarriesTheFilesSentWithIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	task, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "With a screenshot", "", "", "", "",
		nil, nil, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	upload := seedTaskUpload(t, pool, org.ID, task.ID, org.OwnerID, "screenshot.png")

	comment, err := svc.AddComment(ctx, org.ID, task.ID, org.OwnerID, "See attached",
		[]uuid.UUID{upload})
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if len(comment.Attachments) != 1 || comment.Attachments[0].Filename != "screenshot.png" {
		t.Fatalf("the file did not travel with the comment: %+v", comment.Attachments)
	}

	// And it is still there when the task is reopened.
	listed, err := svc.ListComments(ctx, org.ID, task.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || len(listed[0].Attachments) != 1 {
		t.Errorf("history lost the attachment: %+v", listed)
	}
	if listed[0].Attachments[0].ContentType != "image/png" {
		t.Errorf("the type is needed to know whether to show it: %q", listed[0].Attachments[0].ContentType)
	}
}

func TestAFileAloneIsAComment(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	task, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Wordless", "", "", "", "",
		nil, nil, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	upload := seedTaskUpload(t, pool, org.ID, task.ID, org.OwnerID, "solo.png")

	if _, err := svc.AddComment(ctx, org.ID, task.ID, org.OwnerID, "", []uuid.UUID{upload}); err != nil {
		t.Errorf("a file on its own should be a comment: %v", err)
	}
	if _, err := svc.AddComment(ctx, org.ID, task.ID, org.OwnerID, "   ", nil); err == nil {
		t.Error("an empty comment with no file was accepted")
	}
}

func TestACommentCannotClaimSomebodyElsesUpload(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "other-uploader@example.test", "member")

	task, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Not yours", "", "", "", "",
		nil, nil, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	theirs := seedTaskUpload(t, pool, org.ID, task.ID, other, "private.png")

	comment, err := svc.AddComment(ctx, org.ID, task.ID, org.OwnerID, "mine now", []uuid.UUID{theirs})
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if len(comment.Attachments) != 0 {
		t.Errorf("a comment claimed somebody else's upload: %+v", comment.Attachments)
	}
}

func TestACommentCannotClaimAnUploadFromAnotherTask(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	mine, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Here", "", "", "", "",
		nil, nil, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	elsewhere, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "There", "", "", "", "",
		nil, nil, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	upload := seedTaskUpload(t, pool, org.ID, elsewhere.ID, org.OwnerID, "elsewhere.png")

	comment, err := svc.AddComment(ctx, org.ID, mine.ID, org.OwnerID, "look", []uuid.UUID{upload})
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if len(comment.Attachments) != 0 {
		t.Errorf("a comment pulled in a file from another task: %+v", comment.Attachments)
	}
}

// ── What the board says this caller may do (issue #47) ────────────────────
//
// A manager's team board shows the work of an organisation admin who sits on
// their team as an ordinary engineer, because hiding somebody's work hides the
// work. What must never happen is the board and the write paths disagreeing:
// the list walked the managed subtree with no exclusions, HasAuthorityOver
// additionally excluded anybody who administers the organisation, and
// AssignableUsers ran a third copy of the rule. Three surfaces, three answers.
//
// The invariant pinned below is that for every (caller, task) pair the board's
// own answer and HasAuthorityOver — the one the write paths apply — agree.

type person struct {
	name       string
	id         uuid.UUID
	isOrgAdmin bool
}

type boardChart struct {
	org    *testsupport.Org
	people []person
}

// seedBoardChart is the smallest organisation that contains the disagreement:
// somebody who administers the organisation and also sits on a team as an
// ordinary member, the manager of that team, an ordinary member of it, and
// somebody on a team nobody in the chart manages.
func seedBoardChart(t *testing.T, pool *pgxpool.Pool) boardChart {
	t.Helper()
	org := testsupport.SeedOrg(t, pool)
	quinn := testsupport.SeedUser(t, pool, org, "quinn-admin@example.test", "admin")
	bob := testsupport.SeedUser(t, pool, org, "bob-manager@example.test", "manager")
	alice := testsupport.SeedUser(t, pool, org, "alice-backend@example.test", "member")
	sandra := testsupport.SeedUser(t, pool, org, "sandra-sales@example.test", "member")

	testsupport.SeedTeam(t, pool, org, "backend", &bob, nil, quinn, alice)
	testsupport.SeedTeam(t, pool, org, "sales", nil, nil, sandra)

	return boardChart{org: org, people: []person{
		{"the owner", org.OwnerID, true},
		{"quinn, an admin sitting on backend", quinn, true},
		{"bob, who manages backend", bob, false},
		{"alice, an engineer on backend", alice, false},
		{"sandra, over on sales", sandra, false},
	}}
}

// fileOwnWork gives everybody in the chart one task they filed and hold, so
// every (caller, task) pair in the property below is a pair of real people.
func (c boardChart) fileOwnWork(t *testing.T, svc *Service) {
	t.Helper()
	for _, p := range c.people {
		holder := p.id
		if _, err := svc.CreateTask(context.Background(), c.org.ID, p.id,
			p.name+"'s work", "", "", "", "", nil, &holder, nil, nil, nil, p.isOrgAdmin); err != nil {
			t.Fatalf("file work for %s: %v", p.name, err)
		}
	}
}

func TestTheBoardAndHasAuthorityOverNeverDisagree(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	c := seedBoardChart(t, pool)
	c.fileOwnWork(t, svc)

	// A second organisation, whose work must never reach this board and over
	// whose people nobody here has any authority at all.
	elsewhere := testsupport.SeedOrg(t, pool)
	if _, err := svc.CreateTask(ctx, elsewhere.ID, elsewhere.OwnerID, "Somebody else's tenant",
		"", "", "", "", nil, &elsewhere.OwnerID, nil, nil, nil, true); err != nil {
		t.Fatalf("seed the other organisation's work: %v", err)
	}

	for _, caller := range c.people {
		caller := caller
		t.Run(caller.name, func(t *testing.T) {
			board, err := svc.ListTasks(ctx, c.org.ID, Filters{
				Viewer:           &caller.id,
				ViewerIsOrgAdmin: caller.isOrgAdmin,
			})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(board) != len(c.people) {
				t.Fatalf("the board should show everybody's work, got %+v", titlesOf(board))
			}

			for _, task := range board {
				if task.OrgID != c.org.ID {
					t.Fatalf("another organisation's work reached the board: %q", task.Title)
				}
				if task.Can == nil {
					t.Fatalf("the board did not say what %s may do to %q", caller.name, task.Title)
				}

				wantDelete, err := svc.HasAuthorityOver(ctx, c.org.ID, caller.id, task.ReporterID, caller.isOrgAdmin)
				if err != nil {
					t.Fatalf("authority over the reporter of %q: %v", task.Title, err)
				}
				if task.Can.Delete != wantDelete {
					t.Errorf("%q: board says delete=%v, HasAuthorityOver over its reporter says %v",
						task.Title, task.Can.Delete, wantDelete)
				}

				// Nothing is being taken off anybody when the task is
				// unassigned, so there is nobody to need authority over.
				wantReassign := true
				if task.AssigneeID != nil {
					wantReassign, err = svc.HasAuthorityOver(ctx, c.org.ID, caller.id, *task.AssigneeID, caller.isOrgAdmin)
					if err != nil {
						t.Fatalf("authority over the assignee of %q: %v", task.Title, err)
					}
				}
				if task.Can.Reassign != wantReassign {
					t.Errorf("%q: board says reassign=%v, HasAuthorityOver over its assignee says %v",
						task.Title, task.Can.Reassign, wantReassign)
				}

				// A greyed card with no explanation is nearly as bad as the 403.
				blocked := !task.Can.Delete || !task.Can.Reassign
				if blocked && task.Can.Reason == "" {
					t.Errorf("%q is not actionable by %s and the board gives no reason",
						task.Title, caller.name)
				}
				if !blocked && task.Can.Reason != "" {
					t.Errorf("%q is fully actionable by %s yet carries the excuse %q",
						task.Title, caller.name, task.Can.Reason)
				}
			}
		})
	}

	// Authority does not cross the tenant boundary either: bob is a manager in
	// his own organisation and nobody at all in the other one.
	bob := c.people[2]
	foreign, err := svc.ListTasks(ctx, elsewhere.ID, Filters{Viewer: &bob.id})
	if err != nil {
		t.Fatalf("list the other organisation: %v", err)
	}
	if len(foreign) != 1 {
		t.Fatalf("expected the other organisation's single task, got %+v", titlesOf(foreign))
	}
	if foreign[0].Can == nil || foreign[0].Can.Delete || foreign[0].Can.Reassign {
		t.Errorf("a manager was given authority inside another organisation: %+v", foreign[0].Can)
	}
}

// The read-only decision, stated as a test: the task stays on the board and is
// labelled with a reason a person can read.
func TestAManagerSeesAnAdminsWorkOnTheirTeamBoardButCannotActOnIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	c := seedBoardChart(t, pool)
	c.fileOwnWork(t, svc)

	quinn, bob := c.people[1], c.people[2]

	board, err := svc.ListTasks(ctx, c.org.ID, Filters{ManagedBy: &bob.id, Viewer: &bob.id})
	if err != nil {
		t.Fatalf("team board: %v", err)
	}

	var quinns *Task
	for i := range board {
		if board[i].ReporterID == quinn.id {
			quinns = &board[i]
		}
	}
	if quinns == nil {
		t.Fatalf("quinn's work vanished from the team board: %+v", titlesOf(board))
	}
	if quinns.Can == nil {
		t.Fatal("the team board did not say what bob may do")
	}
	if quinns.Can.Delete || quinns.Can.Reassign {
		t.Errorf("the board offered bob an action the write path refuses: %+v", quinns.Can)
	}
	if !strings.Contains(quinns.Can.Reason, "organisation admin") {
		t.Errorf("the reason should say why, got %q", quinns.Can.Reason)
	}

	// And alice, an ordinary engineer on the same team, is still bob's to act on.
	alice := c.people[3]
	var alices *Task
	for i := range board {
		if board[i].ReporterID == alice.id {
			alices = &board[i]
		}
	}
	if alices == nil {
		t.Fatalf("alice's work is missing from her manager's board: %+v", titlesOf(board))
	}
	if alices.Can == nil || !alices.Can.Delete || !alices.Can.Reassign {
		t.Errorf("bob should be able to act on his own engineer's work: %+v", alices.Can)
	}
}

// The strongest form of the invariant: what the board promises is what the
// delete actually does, for every pair of people in the chart.
func TestTheBoardsDeleteFlagPredictsWhatDeletingDoes(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	c := seedBoardChart(t, pool)

	for _, caller := range c.people {
		for _, reporter := range c.people {
			task := createTaskAs(t, svc, c.org, reporter.id, reporter.name+" filed this")

			board, err := svc.ListTasks(ctx, c.org.ID, Filters{
				Viewer:           &caller.id,
				ViewerIsOrgAdmin: caller.isOrgAdmin,
			})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			var promised *Abilities
			for i := range board {
				if board[i].ID == task.ID {
					promised = board[i].Can
				}
			}
			if promised == nil {
				t.Fatalf("%s's board did not carry %q", caller.name, task.Title)
			}

			err = svc.DeleteTask(ctx, c.org.ID, task.ID, caller.id, caller.isOrgAdmin)
			if got := err == nil; got != promised.Delete {
				t.Errorf("%s deleting %s's task: board promised %v, the server said %v (%v)",
					caller.name, reporter.name, promised.Delete, got, err)
			}
			if err != nil {
				// Leave the board clean for the next pair.
				if err := svc.DeleteTask(ctx, c.org.ID, task.ID, c.org.OwnerID, true); err != nil {
					t.Fatalf("clean up %q: %v", task.Title, err)
				}
			}
		}
	}
}

// The mirror case. Somebody who administers the organisation may act on
// everybody, so a team board that showed them nothing was the same
// disagreement the other way up.
func TestAnAdminWhoManagesNoTeamStillHasATeamBoard(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	c := seedBoardChart(t, pool)
	c.fileOwnWork(t, svc)

	quinn := c.people[1]
	board, err := svc.ListTasks(ctx, c.org.ID, Filters{
		ManagedBy:        &quinn.id,
		Viewer:           &quinn.id,
		ViewerIsOrgAdmin: true,
	})
	if err != nil {
		t.Fatalf("team board: %v", err)
	}
	if len(board) != len(c.people) {
		t.Fatalf("an administrator's team board should reach everybody's work, got %+v", titlesOf(board))
	}
	for _, task := range board {
		if task.Can == nil || !task.Can.Delete || !task.Can.Reassign {
			t.Errorf("an administrator should be able to act on %q: %+v", task.Title, task.Can)
		}
	}

	// An ordinary member who manages nobody still sees nothing: the tab is
	// about the people you answer for, and they answer for none.
	sandra := c.people[4]
	empty, err := svc.ListTasks(ctx, c.org.ID, Filters{ManagedBy: &sandra.id, Viewer: &sandra.id})
	if err != nil {
		t.Fatalf("member team board: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("somebody who manages nobody saw team work: %+v", titlesOf(empty))
	}

	// And an administrator who does run a team keeps the board that means
	// something: widening theirs would drop the rest of the organisation onto a
	// tab labelled with their own team.
	alice := c.people[3]
	testsupport.SeedTeam(t, pool, c.org, "platform", &quinn.id, nil, alice.id)
	focused, err := svc.ListTasks(ctx, c.org.ID, Filters{
		ManagedBy:        &quinn.id,
		Viewer:           &quinn.id,
		ViewerIsOrgAdmin: true,
	})
	if err != nil {
		t.Fatalf("team board once they run a team: %v", err)
	}
	if len(focused) != 1 || focused[0].ReporterID != alice.id {
		t.Errorf("an administrator who runs a team should see that team, got %+v", titlesOf(focused))
	}
}

// Handing a task to somebody needs authority over them. Taking it off somebody
// needs the same, or the board's reassign flag would be promising something
// the write path does not honour.
func TestReassigningNeedsAuthorityOverWhoeverHoldsTheWork(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	c := seedBoardChart(t, pool)
	c.fileOwnWork(t, svc)

	quinn, bob, alice := c.people[1], c.people[2], c.people[3]

	board, _ := svc.ListTasks(ctx, c.org.ID, Filters{Viewer: &bob.id})
	var quinns, alices uuid.UUID
	for _, task := range board {
		if task.AssigneeID != nil && *task.AssigneeID == quinn.id {
			quinns = task.ID
		}
		if task.AssigneeID != nil && *task.AssigneeID == alice.id {
			alices = task.ID
		}
	}

	if _, err := svc.UpdateTask(ctx, c.org.ID, quinns, bob.id,
		map[string]interface{}{"assigneeId": alice.id.String()}, false); err == nil {
		t.Error("bob took work off an organisation admin")
	}
	// Off his own engineer, though, is exactly what a manager is for.
	if _, err := svc.UpdateTask(ctx, c.org.ID, alices, bob.id,
		map[string]interface{}{"assigneeId": bob.id.String()}, false); err != nil {
		t.Errorf("bob could not take his own engineer's work back: %v", err)
	}
}

// countingTracer counts every statement the driver sends, so a board that
// works out its abilities one card at a time is a failing test rather than
// something to be caught in review.
type countingTracer struct {
	mu sync.Mutex
	n  int
}

func (c *countingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return ctx
}

func (c *countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *countingTracer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Asking HasAuthorityOver per card would be fifty round trips for a fifty-row
// board. The whole page is resolved in one pass instead, and this pins it.
func TestABoardResolvesItsAbilitiesInOnePassNotOnePerCard(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	c := seedBoardChart(t, pool)

	const cards = 40
	for i := 0; i < cards; i++ {
		holder := c.people[i%len(c.people)]
		if _, err := svc.CreateTask(ctx, c.org.ID, holder.id, fmt.Sprintf("card %d", i),
			"", "", "", "", nil, &holder.id, nil, nil, nil, holder.isOrgAdmin); err != nil {
			t.Fatalf("seed card %d: %v", i, err)
		}
	}

	tracer := &countingTracer{}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = tracer
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open a traced pool onto the same database: %v", err)
	}
	defer traced.Close()

	// Bob is not an administrator, so this is the path that actually has to
	// consult the chart.
	bob := c.people[2]
	board, err := NewService(traced).ListTasks(ctx, c.org.ID, Filters{Viewer: &bob.id})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(board) != cards {
		t.Fatalf("expected %d cards, got %d", cards, len(board))
	}
	for _, task := range board {
		if task.Can == nil {
			t.Fatalf("a card came back without its abilities: %q", task.Title)
		}
	}

	// The rows, their labels, and the authority facts for everybody the page
	// mentions: three statements, whatever the size of the board.
	const budget = 4
	if got := tracer.count(); got > budget {
		t.Errorf("a %d-card board cost %d statements (budget %d): the abilities are being resolved per card",
			cards, got, budget)
	}
}

// The list is capped so a year of history is not sent on every refresh, and the
// cap must fall on old finished work: open work is what the board is for, however
// long it has been open.
func TestTheTaskListCapDropsOldFinishedWorkBeforeOpenWork(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	seed := func(org *testsupport.Org, count int, title, status, age string, completed bool) {
		t.Helper()
		completedAt := "NULL"
		if completed {
			completedAt = "NOW() - $6::interval"
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO tasks (org_id, reporter_id, title, status, created_at, completed_at)
			SELECT $1, $2, $3, $4, NOW() - $6::interval - (i * interval '1 second'), `+completedAt+`
			FROM generate_series(1, $5::int) i
		`, org.ID, org.OwnerID, title, status, count, age); err != nil {
			t.Fatalf("seed %s: %v", title, err)
		}
	}

	seed(org, 3, "ancient done", "done", "4 years", true)
	seed(org, 2, "old todo", "todo", "3 years", false)
	seed(org, 2, "old blocked", "blocked", "3 years", false)
	seed(org, 1, "old in progress", "in_progress", "3 years", false)
	seed(org, maxListedTasks, "recent done", "done", "1 day", true)

	list, err := svc.ListTasks(ctx, org.ID, Filters{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != maxListedTasks {
		t.Fatalf("listed %d tasks, want the cap of %d", len(list), maxListedTasks)
	}
	titles := map[string]int{}
	for _, task := range list {
		if task.OrgID != org.ID {
			t.Fatalf("a task from another organization came back: %+v", task)
		}
		titles[task.Title]++
	}
	for title, want := range map[string]int{"old todo": 2, "old blocked": 2, "old in progress": 1, "ancient done": 0} {
		if titles[title] != want {
			t.Errorf("%q: listed %d, want %d", title, titles[title], want)
		}
	}

	// A task finished yesterday is recent work, however long ago it was filed.
	if _, err := pool.Exec(ctx,
		`UPDATE tasks SET completed_at = NOW(), created_at = NOW() - interval '5 years', title = 'just finished'
		 WHERE id = (SELECT id FROM tasks WHERE org_id = $1 AND title = 'ancient done' LIMIT 1)`, org.ID); err != nil {
		t.Fatalf("finish task: %v", err)
	}
	list, err = svc.ListTasks(ctx, org.ID, Filters{Status: "done"})
	if err != nil {
		t.Fatalf("list done: %v", err)
	}
	found := false
	for _, task := range list {
		found = found || task.Title == "just finished"
	}
	if !found || len(list) != maxListedTasks {
		t.Errorf("listing done: %d tasks, recently finished one present: %v", len(list), found)
	}

	// With more open work than the cap, the newest of it is what stays.
	seed(other, 2, "oldest todo", "todo", "2 years", false)
	seed(other, maxListedTasks, "newer todo", "todo", "1 day", false)
	list, err = svc.ListTasks(ctx, other.ID, Filters{})
	if err != nil {
		t.Fatalf("list other: %v", err)
	}
	if len(list) != maxListedTasks {
		t.Fatalf("listed %d tasks, want the cap of %d", len(list), maxListedTasks)
	}
	for _, task := range list {
		if task.Title == "oldest todo" {
			t.Fatal("the cap kept the oldest open task over a newer one")
		}
	}
}

func TestTypingALabelAgainFindsTheOneAlreadyMade(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	first, err := svc.CreateLabel(ctx, org.ID, "  Front   end ", "#10b981")
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	if first.Name != "Front end" {
		t.Errorf("the name is tidied: %q", first.Name)
	}
	again, err := svc.CreateLabel(ctx, org.ID, "front end", "#ef4444")
	if err != nil {
		t.Fatalf("create it again: %v", err)
	}
	if again.ID != first.ID || again.Color != "#10b981" || again.Name != "Front end" {
		t.Errorf("the same label, as it was, not a second one or a new colour: %+v", again)
	}

	for _, bad := range []struct{ name, color string }{
		{"   ", ""},
		{strings.Repeat("x", MaxLabelName+1), ""},
		{"ok", "red"},
		{"ok", "#12345"},
	} {
		if _, err := svc.CreateLabel(ctx, org.ID, bad.name, bad.color); err == nil {
			t.Errorf("name %q colour %q should be refused", bad.name, bad.color)
		}
	}
	labels, _ := svc.ListLabels(ctx, org.ID)
	if len(labels) != 1 {
		t.Errorf("one label after all that, got %d", len(labels))
	}
}

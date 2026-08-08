package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
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
	task, err := svc.CreateTask(context.Background(), org.ID, org.OwnerID,
		title, "", "", "", "", nil, nil, nil, nil, nil)
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
		map[string]interface{}{"status": "done"}); err != nil {
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

	theirTask, err := svc.CreateTask(ctx, theirs.ID, theirs.OwnerID, "Secret", "", "", "", "", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.GetTask(ctx, mine.ID, theirTask.ID); err == nil {
		t.Error("a task from another organization must not be readable")
	}
	if err := svc.DeleteTask(ctx, mine.ID, theirTask.ID); err == nil {
		t.Error("a task from another organization must not be deletable")
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
	})
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
		map[string]interface{}{"status": "done"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.CompletedAt == nil {
		t.Error("completing a task should stamp completedAt")
	}

	reopened, err := svc.UpdateTask(ctx, org.ID, task.ID, org.OwnerID,
		map[string]interface{}{"status": "todo"})
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

	if err := svc.DeleteTask(ctx, org.ID, task.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetTask(ctx, org.ID, task.ID); err == nil {
		t.Error("a deleted task should not load")
	}
	list, _ := svc.ListTasks(ctx, org.ID, Filters{})
	if len(list) != 0 {
		t.Errorf("deleted task still on the board: %+v", list)
	}
	if err := svc.DeleteTask(ctx, org.ID, task.ID); err == nil {
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
		map[string]interface{}{"assigneeId": mate.String()})
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
		map[string]interface{}{"status": "in_progress"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.AddComment(ctx, org.ID, task.ID, mate, "on it"); err != nil {
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
		map[string]interface{}{"title": "Steady"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, _ := svc.GetActivities(ctx, org.ID, task.ID)

	if len(after) != len(before) {
		t.Errorf("setting a field to its current value should not record activity: %d -> %d",
			len(before), len(after))
	}
}

func TestCommentsRoundTrip(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Discuss")

	if _, err := svc.AddComment(ctx, org.ID, task.ID, org.OwnerID, "first thought"); err != nil {
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

	theirTask, _ := svc.CreateTask(ctx, theirs.ID, theirs.OwnerID, "Theirs", "", "", "", "", nil, nil, nil, nil, nil)
	if _, err := svc.AddComment(ctx, mine.ID, theirTask.ID, mine.OwnerID, "sneaky"); err == nil {
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

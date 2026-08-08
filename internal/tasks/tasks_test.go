package tasks

import (
	"context"
	"sync"
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
		map[string]interface{}{"title": "Steady"}, true); err != nil {
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

	theirTask, _ := svc.CreateTask(ctx, theirs.ID, theirs.OwnerID, "Theirs", "", "", "", "", nil, nil, nil, nil, nil, false)
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
		if err := svc.AddWatcher(ctx, org.ID, task.ID, mate); err != nil {
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

	if err := svc.AddWatcher(ctx, org.ID, task.ID, other.OwnerID); err == nil {
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

	if err := svc.AddWatcher(ctx, org.ID, task.ID, watcher); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if err := svc.AddWatcher(ctx, org.ID, task.ID, org.OwnerID); err != nil {
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

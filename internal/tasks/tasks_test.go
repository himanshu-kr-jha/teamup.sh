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

// "My tasks" has to include work somebody raised but has not handed out yet,
// otherwise creating a task makes it disappear from your own view.
func TestMyTasksCoversWorkAssignedToMeAndWorkIRaised(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	me := testsupport.SeedUser(t, pool, org, "me7@example.test", "member")
	somebody := testsupport.SeedUser(t, pool, org, "them7@example.test", "member")

	if _, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Given to me", "", "", "", "",
		nil, &me, nil, nil, nil, true); err != nil {
		t.Fatalf("assign to me: %v", err)
	}
	if _, err := svc.CreateTask(ctx, org.ID, me, "Raised by me, unassigned", "", "", "", "",
		nil, nil, nil, nil, nil, false); err != nil {
		t.Fatalf("raised by me: %v", err)
	}
	if _, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Nothing to do with me", "", "", "", "",
		nil, &somebody, nil, nil, nil, true); err != nil {
		t.Fatalf("someone else: %v", err)
	}

	mine, err := svc.ListTasks(ctx, org.ID, Filters{Involving: &me})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(mine) != 2 {
		t.Errorf("my tasks: got %+v", titlesOf(mine))
	}
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

// Being added to a task is being asked to pay attention to it, so it belongs
// in your list — not only in the notification that told you about it.
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

	before, err := svc.ListTasks(ctx, org.ID, Filters{Involving: &me})
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("nothing should be mine yet: %+v", titlesOf(before))
	}

	if err := svc.AddWatcher(ctx, org.ID, task.ID, me); err != nil {
		t.Fatalf("add to task: %v", err)
	}

	after, err := svc.ListTasks(ctx, org.ID, Filters{Involving: &me})
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
	if err := svc.AddWatcher(ctx, org.ID, task.ID, me); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := svc.RemoveWatcher(ctx, org.ID, task.ID, me); err != nil {
		t.Fatalf("remove: %v", err)
	}

	mine, err := svc.ListTasks(ctx, org.ID, Filters{Involving: &me})
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
	if err := svc.AddWatcher(ctx, org.ID, task.ID, me); err != nil {
		t.Fatalf("add: %v", err)
	}

	mine, err := svc.ListTasks(ctx, org.ID, Filters{Involving: &me})
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
	if err := svc.AddWatcher(ctx, org.ID, task.ID, report); err != nil {
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

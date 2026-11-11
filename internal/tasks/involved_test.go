package tasks

// Everybody a task concerns hears what happens to it: the assignee, whoever
// made it, and whoever was added to it. Nobody hears of their own doing, nor
// of one thing twice.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

type kindNotifier struct {
	mu   sync.Mutex
	sent map[uuid.UUID][]string
}

func (k *kindNotifier) Notify(_ context.Context, _, userID uuid.UUID, kind, _, _ string, _ map[string]any) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.sent == nil {
		k.sent = map[uuid.UUID][]string{}
	}
	k.sent[userID] = append(k.sent[userID], kind)
	return nil
}

func (k *kindNotifier) kinds(userID uuid.UUID) []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string{}, k.sent[userID]...)
}

func (k *kindNotifier) reset() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sent = nil
}

func sameKinds(t *testing.T, who string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s was told %v, want %v", who, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s was told %v, want %v", who, got, want)
			return
		}
	}
}

type involvedSetup struct {
	svc                           *Service
	notes                         *kindNotifier
	org                           *testsupport.Org
	owner, assignee, added, other uuid.UUID
	task                          uuid.UUID
}

// A task the owner made and gave to somebody, with a third person added to it.
func setupInvolved(t *testing.T) involvedSetup {
	t.Helper()
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	assignee := testsupport.SeedUser(t, pool, org, "asha@example.test", "member")
	added := testsupport.SeedUser(t, pool, org, "carol@example.test", "member")
	other := testsupport.SeedUser(t, pool, org, "dev@example.test", "member")
	task, err := svc.CreateTask(ctx, org.ID, org.OwnerID, "Write the report", "", "", "", "",
		nil, &assignee, nil, nil, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AddWatcher(ctx, org.ID, task.ID, added, org.OwnerID); err != nil {
		t.Fatalf("add: %v", err)
	}
	notes := &kindNotifier{}
	svc.SetNotifier(notes)
	return involvedSetup{svc: svc, notes: notes, org: org, owner: org.OwnerID, assignee: assignee, added: added, other: other, task: task.ID}
}

func TestAChangeIsToldToTheAssigneeTheCreatorAndWhoeverWasAdded(t *testing.T) {
	s := setupInvolved(t)
	ctx := context.Background()

	// The person added to it moves it on.
	if _, err := s.svc.UpdateTask(ctx, s.org.ID, s.task, s.added, map[string]interface{}{"status": "in_review"}, true); err != nil {
		t.Fatalf("update: %v", err)
	}
	sameKinds(t, "the assignee", s.notes.kinds(s.assignee), "task.updated")
	sameKinds(t, "the creator", s.notes.kinds(s.owner), "task.updated")
	sameKinds(t, "who made the change", s.notes.kinds(s.added))
	sameKinds(t, "somebody not on it", s.notes.kinds(s.other))
}

func TestDraggingACardUpItsColumnTellsNobody(t *testing.T) {
	s := setupInvolved(t)
	if _, err := s.svc.UpdateTask(context.Background(), s.org.ID, s.task, s.owner, map[string]interface{}{"position": 7}, true); err != nil {
		t.Fatalf("update: %v", err)
	}
	for name, who := range map[string]uuid.UUID{"assignee": s.assignee, "added": s.added} {
		sameKinds(t, name, s.notes.kinds(who))
	}
}

func TestWhoeverIsHandedATaskHearsThatAndNotAlsoThatItChanged(t *testing.T) {
	s := setupInvolved(t)
	if _, err := s.svc.UpdateTask(context.Background(), s.org.ID, s.task, s.owner, map[string]interface{}{"assigneeId": s.other.String()}, true); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	sameKinds(t, "the new assignee", s.notes.kinds(s.other), "task.assigned")
	sameKinds(t, "who was added", s.notes.kinds(s.added), "task.updated")
	sameKinds(t, "who reassigned it", s.notes.kinds(s.owner))
}

func TestACommentIsToldToEverybodyOnTheTaskAndAMentionOnlyOnce(t *testing.T) {
	s := setupInvolved(t)
	ctx := context.Background()

	if _, err := s.svc.AddComment(ctx, s.org.ID, s.task, s.assignee, "Draft is up, @carol please look", nil); err != nil {
		t.Fatalf("comment: %v", err)
	}
	sameKinds(t, "the creator", s.notes.kinds(s.owner), "task.comment")
	sameKinds(t, "who was named in it", s.notes.kinds(s.added), "task.mention")
	sameKinds(t, "the author", s.notes.kinds(s.assignee))
	sameKinds(t, "somebody not on it", s.notes.kinds(s.other))
}

func TestSomebodyOnATaskTwiceOverHearsOnce(t *testing.T) {
	s := setupInvolved(t)
	ctx := context.Background()
	// The assignee also follows it.
	if _, err := s.svc.AddWatcher(ctx, s.org.ID, s.task, s.assignee, s.assignee); err != nil {
		t.Fatalf("follow: %v", err)
	}
	s.notes.reset()
	if _, err := s.svc.UpdateTask(ctx, s.org.ID, s.task, s.added, map[string]interface{}{"priority": "high"}, true); err != nil {
		t.Fatalf("update: %v", err)
	}
	sameKinds(t, "the assignee", s.notes.kinds(s.assignee), "task.updated")
}

func TestACodeMoveIsToldToTheCreatorTooAndOnlyOnceToTheAssignee(t *testing.T) {
	s := setupInvolved(t)
	ctx := context.Background()
	if _, err := s.svc.AddWatcher(ctx, s.org.ID, s.task, s.assignee, s.assignee); err != nil {
		t.Fatalf("follow: %v", err)
	}
	s.notes.reset()
	moved, err := s.svc.MoveByCode(ctx, s.org.ID, s.task, "in_review", "github", nil, "Pull request opened")
	if err != nil || !moved {
		t.Fatalf("move: %v %v", moved, err)
	}
	sameKinds(t, "the assignee", s.notes.kinds(s.assignee), "task.updated")
	sameKinds(t, "the creator", s.notes.kinds(s.owner), "task.updated")
	sameKinds(t, "who was added", s.notes.kinds(s.added), "task.updated")
}

func TestDueTasksAreWhatIsMineToSeeThrough(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	me := testsupport.SeedUser(t, pool, org, "me@example.test", "member")
	other := testsupport.SeedUser(t, pool, org, "them@example.test", "member")
	day := time.Date(2026, 11, 20, 18, 29, 59, 0, time.UTC)
	later := day.Add(60 * 24 * time.Hour)

	newTask := func(title string, reporter uuid.UUID, assignee *uuid.UUID, due time.Time) uuid.UUID {
		t.Helper()
		task, err := svc.CreateTask(ctx, org.ID, reporter, title, "", "", "", "", nil, assignee, &due, nil, nil, true)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return task.ID
	}
	mine := newTask("Held by me", org.OwnerID, &me, day)
	made := newTask("Made by me, nobody holds it", me, nil, day)
	added := newTask("Added to", org.OwnerID, &other, day)
	if _, err := svc.AddWatcher(ctx, org.ID, added, me, org.OwnerID); err != nil {
		t.Fatalf("add: %v", err)
	}
	newTask("Made by me, somebody else holds it", me, &other, day)
	newTask("Not mine at all", org.OwnerID, &other, day)
	newTask("Mine but later", org.OwnerID, &me, later)

	got, err := svc.DueTasks(ctx, org.ID, me, day.Add(-24*time.Hour), day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	want := map[uuid.UUID]bool{mine: true, made: true, added: true}
	if len(got) != len(want) {
		t.Fatalf("got %d due tasks, want %d: %+v", len(got), len(want), got)
	}
	for _, task := range got {
		if !want[task.ID] {
			t.Errorf("%q should not be on my calendar", task.Title)
		}
	}

	if _, err := svc.DueTasks(ctx, org.ID, me, day, day.Add(100*24*time.Hour)); err == nil {
		t.Error("a read of over 92 days was allowed")
	}
}

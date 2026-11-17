package tasks

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/teamup/teamup/internal/testsupport"
)

// The owner's request of 17 November 2026: one list of every task that is
// somebody's, however it is: assigned to them, created by them for somebody
// else, or one they were added to. Each says which, and none is there twice.
func TestAllOfMineHasEveryKindAndSaysWhichEachIs(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	me := testsupport.SeedUser(t, pool, org, "all-of-mine@example.test", "member")
	other := testsupport.SeedUser(t, pool, org, "someone-else@example.test", "member")

	create := func(title string, reporter uuid.UUID, assignee *uuid.UUID) *Task {
		t.Helper()
		task, err := svc.CreateTask(ctx, org.ID, reporter, title, "", "", "", "", nil, assignee, nil, nil, nil, true)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return task
	}
	assigned := create("Given to me", org.OwnerID, &me)
	raised := create("Raised, not handed out", me, nil)
	handedOut := create("Handed to somebody", me, &other)
	followed := create("Following along", org.OwnerID, &other)
	both := create("Mine and followed", org.OwnerID, &me)
	create("Nothing to do with me", org.OwnerID, &other)
	for _, task := range []*Task{followed, both} {
		if _, err := svc.AddWatcher(ctx, org.ID, task.ID, me, org.OwnerID); err != nil {
			t.Fatalf("add to task: %v", err)
		}
	}

	all, err := svc.ListTasks(ctx, org.ID, Filters{Mine: &me, MineAs: MineAll})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, task := range all {
		if _, twice := got[task.Title]; twice {
			t.Errorf("%q is listed twice", task.Title)
		}
		got[task.Title] = task.Mine
	}
	want := map[string][]string{
		assigned.Title:  {MineIsAssigned},
		raised.Title:    {MineIsAssigned},
		handedOut.Title: {MineIsCreated},
		followed.Title:  {MineIsAdded},
		both.Title:      {MineIsAssigned, MineIsAdded},
	}
	if len(got) != len(want) {
		t.Errorf("want %d tasks, got %v", len(want), got)
	}
	for title, kinds := range want {
		if !slices.Equal(got[title], kinds) {
			t.Errorf("%q: want %v, got %v", title, kinds, got[title])
		}
	}

	// The single kinds still say how, too.
	created, err := svc.ListTasks(ctx, org.ID, Filters{Mine: &me, MineAs: MineCreated})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || !slices.Equal(created[0].Mine, []string{MineIsCreated}) {
		t.Errorf("created by me: %+v", created)
	}
	// Nobody asking for their own gets no marks.
	everything, err := svc.ListTasks(ctx, org.ID, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range everything {
		if task.Mine != nil {
			t.Errorf("%q is marked though nobody asked for their own", task.Title)
		}
	}
}

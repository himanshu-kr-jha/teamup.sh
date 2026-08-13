package org

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

// Two admins reorganising at the same moment: one files A under B while the
// other files B under A. Each move is legal on its own, and each cycle check
// run against the state before the other's write says so. If the check and the
// write are not one serialised unit, both commit and the tree closes into a
// loop.
//
// A cycle does not announce itself — every walk of the tree UNIONs, so nothing
// hangs. It quietly makes everyone in the loop everyone else's manager, which
// in this codebase is the gate on team membership and on task authority. That
// is a privilege inversion, so it is worth a concurrency test despite the
// awkwardness of writing one.
func TestConcurrentReparentsCannotFormACycle(t *testing.T) {
	pool := testsupport.DB(t)
	svc := NewService(pool)
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)

	// Repeated because the interleaving that produces the cycle is a race; a
	// single attempt would pass on an unfixed build most of the time.
	const rounds = 25
	for round := 0; round < rounds; round++ {
		a, err := svc.CreateTeam(ctx, org.ID, fmt.Sprintf("alpha-%d", round), "", nil, nil)
		if err != nil {
			t.Fatalf("round %d create alpha: %v", round, err)
		}
		b, err := svc.CreateTeam(ctx, org.ID, fmt.Sprintf("beta-%d", round), "", nil, nil)
		if err != nil {
			t.Fatalf("round %d create beta: %v", round, err)
		}

		aUnderB := &b.ID
		bUnderA := &a.ID

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = svc.UpdateTeam(ctx, org.ID, a.ID, nil, nil, nil, &aUnderB)
		}()
		go func() {
			defer wg.Done()
			_, _ = svc.UpdateTeam(ctx, org.ID, b.ID, nil, nil, nil, &bUnderA)
		}()
		wg.Wait()

		// At most one of them may have taken effect.
		var parentA, parentB *uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT parent_id FROM teams WHERE id = $1`, a.ID).Scan(&parentA); err != nil {
			t.Fatalf("round %d read alpha: %v", round, err)
		}
		if err := pool.QueryRow(ctx, `SELECT parent_id FROM teams WHERE id = $1`, b.ID).Scan(&parentB); err != nil {
			t.Fatalf("round %d read beta: %v", round, err)
		}
		if parentA != nil && *parentA == b.ID && parentB != nil && *parentB == a.ID {
			t.Fatalf("round %d: both moves committed — alpha and beta now point at each other,"+
				" so every member of either team manages every member of the other", round)
		}

		// Clean up so the next round starts from two fresh roots.
		nilParent := (*uuid.UUID)(nil)
		_, _ = svc.UpdateTeam(ctx, org.ID, a.ID, nil, nil, nil, &nilParent)
		_, _ = svc.UpdateTeam(ctx, org.ID, b.ID, nil, nil, nil, &nilParent)
		if err := svc.DeleteTeam(ctx, org.ID, a.ID); err != nil {
			t.Fatalf("round %d cleanup alpha: %v", round, err)
		}
		if err := svc.DeleteTeam(ctx, org.ID, b.ID); err != nil {
			t.Fatalf("round %d cleanup beta: %v", round, err)
		}
	}
}

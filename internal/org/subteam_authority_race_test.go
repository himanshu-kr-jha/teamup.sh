package org

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

// A lead moving a team inside their subtree at the same moment an
// administrator moves that team out of it.
//
// Authority here is not a property of the caller, it is a property of the tree
// — and the tree is what both of these are changing. Read on one connection
// and written on another, the lead's move is authorised against a shape that
// has already stopped being true: the administrator's move lands in the gap,
// and the lead's write then files a team they no longer run back underneath
// their own, silently pulling it back into their reach.
//
// Whichever way round the two are actually serialised, the answer is the same
// and it is checkable: the administrator's move is unconditional, so it either
// happens after the lead's and takes the team out, or happens before it and
// leaves the lead with no authority over the team at all. The team ends up
// where the administrator put it, every time. Anything else means the
// authority check was answered about a tree that no longer existed.
func TestALeadsMoveCannotOutrunTheTreeItWasAuthorisedAgainst(t *testing.T) {
	pool := testsupport.DB(t)
	svc := NewService(pool)
	ctx := context.Background()

	// SeedOrg brings the default roles with it, so AddMember's "member"
	// resolves without seedDefaultRoles.
	org := testsupport.SeedOrg(t, pool)

	lead, _, err := svc.AddMember(ctx, org.ID, "Lead", "lead@race.test", "member")
	if err != nil {
		t.Fatalf("add lead: %v", err)
	}
	leadActor := Actor{UserID: lead.ID}

	// Repeated because the interleaving that matters is a race; a single
	// attempt would pass against a check made outside the transaction most of
	// the time.
	const rounds = 25
	for round := 0; round < rounds; round++ {
		// elsewhere ── theirs        (nobody runs it)
		//   theirs is where the administrator sends the team
		// mine ──┬── moving          (the lead runs mine, and so all of this)
		//        └── destination
		elsewhere, err := svc.CreateTeam(ctx, org.ID, adminActor, fmt.Sprintf("elsewhere-%d", round), "", nil, nil)
		if err != nil {
			t.Fatalf("round %d create elsewhere: %v", round, err)
		}
		mine, err := svc.CreateTeam(ctx, org.ID, adminActor, fmt.Sprintf("mine-%d", round), "", nil, &lead.ID)
		if err != nil {
			t.Fatalf("round %d create mine: %v", round, err)
		}
		moving, err := svc.CreateTeam(ctx, org.ID, adminActor, fmt.Sprintf("moving-%d", round), "", &mine.ID, nil)
		if err != nil {
			t.Fatalf("round %d create moving: %v", round, err)
		}
		destination, err := svc.CreateTeam(ctx, org.ID, adminActor, fmt.Sprintf("destination-%d", round), "", &mine.ID, nil)
		if err != nil {
			t.Fatalf("round %d create destination: %v", round, err)
		}

		insideTheSubtree := &destination.ID
		outOfTheSubtree := &elsewhere.ID

		var wg sync.WaitGroup
		wg.Add(2)
		// The lead rearranges their own patch.
		go func() {
			defer wg.Done()
			_, _ = svc.UpdateTeam(ctx, org.ID, moving.ID, leadActor, nil, nil, nil, &insideTheSubtree)
		}()
		// The administrator takes the team away from them.
		go func() {
			defer wg.Done()
			_, _ = svc.UpdateTeam(ctx, org.ID, moving.ID, adminActor, nil, nil, nil, &outOfTheSubtree)
		}()
		wg.Wait()

		var parent *uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT parent_id FROM teams WHERE id = $1`, moving.ID).Scan(&parent); err != nil {
			t.Fatalf("round %d read moving: %v", round, err)
		}
		if parent == nil || *parent != elsewhere.ID {
			t.Fatalf("round %d: the administrator moved the team out of the lead's subtree, but it sits"+
				" under %v instead of %v — the lead's move was authorised against a tree that had already"+
				" changed, and pulled a team they no longer run back under their own",
				round, parent, elsewhere.ID)
		}

		// A manager of nothing relevant, to be sure the round really did end
		// with the team out of the lead's reach rather than merely re-parented.
		manages, err := svc.ManagesTeam(ctx, org.ID, moving.ID, lead.ID)
		if err != nil {
			t.Fatalf("round %d check management: %v", round, err)
		}
		if manages {
			t.Fatalf("round %d: the lead still runs a team the administrator took off them", round)
		}
	}
}

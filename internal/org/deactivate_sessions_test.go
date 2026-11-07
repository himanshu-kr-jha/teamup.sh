package org

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Removing somebody has to end what they already have open, or a person who has
// just been let go keeps signing back in on the refresh token in their browser.
func TestDeactivatingAMemberEndsTheirSessions(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Leavers Co", "leavers-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)
	leaver := addResetPerson(t, svc, pool, org.ID, "leaver@leavers.test", "member")
	stayer := addResetPerson(t, svc, pool, org.ID, "stayer@leavers.test", "member")

	if err := svc.DeactivateUser(ctx, org.ID, leaver.id); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	if n := liveSessions(t, pool, leaver.id); n != 0 {
		t.Errorf("leaver still has %d live sessions", n)
	}
	if n := liveSessions(t, pool, stayer.id); n != 1 {
		t.Errorf("colleague's sessions were touched: %d live, want 1", n)
	}
}

// A socket and a connection to a call room outlive the session that opened them, so
// whatever holds those has to be told.
func TestDeactivatingAMemberTellsWhateverHoldsTheirConnections(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, "Leavers Co", "leavers-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)
	leaver := addResetPerson(t, svc, pool, org.ID, "leaver@leavers.test", "member")

	var told []uuid.UUID
	svc.OnDeactivated(func(_ context.Context, orgID, userID uuid.UUID) {
		if orgID != org.ID {
			t.Errorf("org %s", orgID)
		}
		told = append(told, userID)
	})

	if err := svc.DeactivateUser(ctx, org.ID, uuid.New()); err == nil {
		t.Fatal("nobody by that id")
	}
	if len(told) != 0 {
		t.Errorf("nobody was deactivated, told %v", told)
	}
	if err := svc.DeactivateUser(ctx, org.ID, leaver.id); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if len(told) != 1 || told[0] != leaver.id {
		t.Errorf("told %v", told)
	}
}

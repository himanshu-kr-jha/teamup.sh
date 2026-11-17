package org

import (
	"context"
	"strings"
	"testing"

	"github.com/teamup/teamup/internal/testsupport"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Found making the tutorial videos on 17 November 2026: a team deleted, then
// made again with the same name, was refused with a server error, because the
// deleted team still held its name. A deleted team's name is free again, and
// a name a live team has is refused as a conflict that says so.
func TestADeletedTeamsNameCanBeUsedAgainAndALiveOneIsAConflict(t *testing.T) {
	pool := testsupport.DB(t)
	svc := NewService(pool)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	first, err := svc.CreateTeam(ctx, org.ID, adminActor, "Installations", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateTeam(ctx, org.ID, adminActor, "Installations", "", nil, nil)
	if apperrors.HTTPStatus(err) != 409 || !strings.HasSuffix(err.Error(), `there is already a team called "Installations"`) {
		t.Errorf("a live team's name: want a 409 that says so, got %v", err)
	}

	other, err := svc.CreateTeam(ctx, org.ID, adminActor, "Surveys", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	taken := "Installations"
	if _, err := svc.UpdateTeam(ctx, org.ID, other.ID, adminActor, &taken, nil, nil, nil); apperrors.HTTPStatus(err) != 409 {
		t.Errorf("renamed onto a live team's name: want 409, got %v", err)
	}

	if err := svc.DeleteTeam(ctx, org.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	again, err := svc.CreateTeam(ctx, org.ID, adminActor, "Installations", "", nil, nil)
	if err != nil {
		t.Fatalf("a deleted team's name is free again: %v", err)
	}
	if again.ID == first.ID {
		t.Error("a new team, not the deleted one")
	}
}

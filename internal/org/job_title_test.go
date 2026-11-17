package org

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The owner's request of 17 November 2026: somebody says what they do at the
// company, such as "AI/ML Engineer", and everyone in the organization sees it.
// It is tidied of stray spaces, can be cleared, and is at most 80 characters.
func TestACompanyRoleIsSavedTidiedAndShownToEveryone(t *testing.T) {
	w := newMailWorld(t)
	r := w.as(t, "admin")
	path := "/api/users/" + w.admin.ID.String()

	if got := send(t, r, http.MethodPatch, path, map[string]string{"jobTitle": "  AI/ML   Engineer "}); got.Code != http.StatusOK {
		t.Fatalf("saving a company role: %d %s", got.Code, got.Body.String())
	}
	listed := send(t, r, http.MethodGet, "/api/users", nil)
	var body struct {
		Users []User `json:"users"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Users) != 1 || body.Users[0].JobTitle != "AI/ML Engineer" {
		t.Fatalf("the list of members shows it, tidied: %+v", body.Users)
	}

	// Saving only a name leaves it as it was.
	if got := send(t, r, http.MethodPatch, path, map[string]string{"name": "Ada Admin"}); !strings.Contains(got.Body.String(), `"jobTitle":"AI/ML Engineer"`) {
		t.Errorf("a name alone keeps the company role: %s", got.Body.String())
	}
	if got := send(t, r, http.MethodPatch, path, map[string]string{"jobTitle": strings.Repeat("é", 81)}); got.Code != http.StatusBadRequest {
		t.Errorf("81 characters is too long: %d", got.Code)
	}
	if got := send(t, r, http.MethodPatch, path, map[string]string{"jobTitle": strings.Repeat("é", 80)}); got.Code != http.StatusOK {
		t.Errorf("80 characters, even two bytes each, fits: %d %s", got.Code, got.Body.String())
	}
	if got := send(t, r, http.MethodPatch, path, map[string]string{"jobTitle": ""}); !strings.Contains(got.Body.String(), `"jobTitle":""`) {
		t.Errorf("it can be cleared: %s", got.Body.String())
	}
}

// The member list says which teams each person is in, so it can be searched
// and filtered by team; a deleted team is not one of them.
func TestTheMemberListSaysWhichTeamsEachPersonIsIn(t *testing.T) {
	w := newMailWorld(t)
	ctx := t.Context()
	field, err := w.svc.CreateTeam(ctx, w.org, adminActor, "Field Service", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := w.svc.CreateTeam(ctx, w.org, adminActor, "Old team", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, team := range []uuid.UUID{field.ID, old.ID} {
		if err := w.svc.AddTeamMember(ctx, w.org, team, w.admin.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.svc.DeleteTeam(ctx, w.org, old.ID); err != nil {
		t.Fatal(err)
	}
	users, err := w.svc.ListUsers(ctx, w.org)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || len(users[0].Teams) != 1 || users[0].Teams[0] != "Field Service" {
		t.Fatalf("want only Field Service, got %+v", users)
	}
}

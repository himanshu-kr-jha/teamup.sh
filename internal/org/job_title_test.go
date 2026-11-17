package org

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
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

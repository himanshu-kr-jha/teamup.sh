package org

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// seedDefaultRoles mirrors what the setup wizard creates for a real
// organization, so AddMember's default of "member" resolves.
func seedDefaultRoles(t *testing.T, svc *Service, orgID uuid.UUID) {
	t.Helper()
	for _, name := range []string{"owner", "admin", "member"} {
		if _, err := svc.CreateRole(context.Background(), orgID, name, name, []string{"*"}); err != nil {
			t.Fatalf("seed role %s: %v", name, err)
		}
	}
}

// asRoles builds a router whose only middleware is a stand-in for the auth
// middleware, so the tests can say who is calling without minting real tokens.
func asRoles(t *testing.T, svc *Service, orgID string, roles ...string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_roles", roles)
		c.Next()
	})
	RegisterRoutes(api, svc)
	return r
}

func postMember(t *testing.T, r *gin.Engine, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAddMemberEndpointReturnsThePasswordOnce(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Endpoint Co", "endpoint-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	seedDefaultRoles(t, svc, org.ID)

	r := asRoles(t, svc, org.ID.String(), "owner")
	w := postMember(t, r, map[string]string{"name": "Ada", "email": "ada@endpoint.test"})
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	var got struct {
		User struct {
			Email              string `json:"email"`
			MustChangePassword bool   `json:"mustChangePassword"`
		} `json:"user"`
		TemporaryPassword string `json:"temporaryPassword"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.User.Email != "ada@endpoint.test" {
		t.Errorf("wrong member returned: %+v", got.User)
	}
	if !got.User.MustChangePassword {
		t.Error("a generated password has to be replaced on first sign-in")
	}
	if got.TemporaryPassword == "" {
		t.Error("no password returned — the administrator has nothing to pass on")
	}
}

func TestAddMemberEndpointRefusesANonAdministrator(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Locked Co", "locked-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	seedDefaultRoles(t, svc, org.ID)

	r := asRoles(t, svc, org.ID.String(), "member")
	w := postMember(t, r, map[string]string{"name": "Mallory", "email": "mallory@locked.test"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}

	// And no account was created as a side effect of being refused.
	users, err := svc.ListUsers(ctx, org.ID)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("refused caller still created %d user(s)", len(users))
	}
}

func TestAddMemberEndpointReportsADuplicateEmailAsAConflict(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Twice Co", "twice-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	seedDefaultRoles(t, svc, org.ID)

	r := asRoles(t, svc, org.ID.String(), "admin")
	if w := postMember(t, r, map[string]string{"name": "Ada", "email": "ada@twice.test"}); w.Code != http.StatusCreated {
		t.Fatalf("first add: want 201, got %d: %s", w.Code, w.Body.String())
	}

	// Not a 500 — the administrator has to be able to tell the difference
	// between "already there" and "the server broke".
	w := postMember(t, r, map[string]string{"name": "Ada Again", "email": "ada@twice.test"})
	if w.Code != http.StatusConflict {
		t.Fatalf("second add: want 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAddMemberEndpointRejectsAnAddressThatIsNotAnEmail(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Malformed Co", "malformed-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	r := asRoles(t, svc, org.ID.String(), "owner")
	w := postMember(t, r, map[string]string{"name": "Ada", "email": "not-an-email"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

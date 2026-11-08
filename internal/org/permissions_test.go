package org

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/testsupport"
)

// permissionWorld signs people in the way the real middleware does: their
// permissions are read from their roles in the database, so a role made up on
// the Roles screen is tested as it really works.
type permissionWorld struct {
	svc   *Service
	pool  *pgxpool.Pool
	org   *testsupport.Org
	authS *auth.Service
}

func newPermissionWorld(t *testing.T) *permissionWorld {
	t.Helper()
	svc, pool := newService(t)
	a, err := auth.NewService(pool, nil, auth.Config{Issuer: "teamup", Audience: "teamup-api",
		AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return &permissionWorld{svc: svc, pool: pool, org: testsupport.SeedOrg(t, pool), authS: a}
}

func (w *permissionWorld) as(t *testing.T, userID uuid.UUID) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		perms, err := w.authS.PermissionsOf(c.Request.Context(), userID)
		if err != nil {
			t.Fatalf("permissions: %v", err)
		}
		c.Set("org_id", w.org.ID.String())
		c.Set("user_id", userID.String())
		c.Set("user_permissions", perms)
		c.Next()
	})
	RegisterRoutes(api, w.svc)
	return r
}

func (w *permissionWorld) do(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (w *permissionWorld) roleWith(t *testing.T, name string, perms ...string) uuid.UUID {
	t.Helper()
	role, err := w.svc.CreateRole(context.Background(), w.org.ID, name, "", perms, []string{auth.Every})
	if err != nil {
		t.Fatalf("role %s: %v", name, err)
	}
	return role.ID
}

func (w *permissionWorld) give(t *testing.T, user, role uuid.UUID) {
	t.Helper()
	if err := w.svc.AssignRole(context.Background(), w.org.ID, user, role, w.org.OwnerID, []string{auth.Every}); err != nil {
		t.Fatal(err)
	}
}

func TestTheSeededRolesAreTheRealOnes(t *testing.T) {
	for _, d := range auth.DefaultRolePermissions() {
		var seeded []string
		if err := json.Unmarshal([]byte(testsupport.DefaultRolePermissions[d.Name]), &seeded); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(seeded, d.Permissions) {
			t.Errorf("%s: test fixtures %v, real %v", d.Name, seeded, d.Permissions)
		}
	}
}

// A role made up on the Roles screen does what its boxes say, and nothing
// else, whatever it is called.
func TestAMadeUpRoleDoesExactlyWhatItsPermissionsSay(t *testing.T) {
	w := newPermissionWorld(t)
	mo := testsupport.SeedUser(t, w.pool, w.org, "mo@example.test", "member")
	plain := testsupport.SeedUser(t, w.pool, w.org, "pia@example.test", "member")
	w.give(t, mo, w.roleWith(t, "Office manager", auth.PermOrgSettings, auth.PermMembers))

	asMo, asPlain := w.as(t, mo), w.as(t, plain)
	if got := w.do(t, asMo, http.MethodGet, "/api/org/mail", nil); got.Code != http.StatusOK {
		t.Errorf("settings: %d %s", got.Code, got.Body.String())
	}
	if got := w.do(t, asMo, http.MethodPost, "/api/users", map[string]string{"name": "New Person", "email": "new@example.test"}); got.Code != http.StatusCreated {
		t.Errorf("members: %d %s", got.Code, got.Body.String())
	}
	for _, refused := range []struct{ method, path string }{
		{http.MethodPost, "/api/roles"},
		{http.MethodPost, "/api/users/" + plain.String() + "/roles"},
	} {
		if got := w.do(t, asMo, refused.method, refused.path, map[string]string{"name": "x", "roleId": uuid.NewString()}); got.Code != http.StatusForbidden {
			t.Errorf("%s %s without roles.manage: %d", refused.method, refused.path, got.Code)
		}
	}
	if got := w.do(t, asPlain, http.MethodGet, "/api/org/mail", nil); got.Code != http.StatusForbidden {
		t.Errorf("a plain member: %d", got.Code)
	}

	// A built-in admin still has the run of it.
	admin := testsupport.SeedUser(t, w.pool, w.org, "ada@example.test", "admin")
	if got := w.do(t, w.as(t, admin), http.MethodGet, "/api/org/mail", nil); got.Code != http.StatusOK {
		t.Errorf("an admin: %d", got.Code)
	}
}

// Somebody who manages roles cannot make a role, or hand out one, that can do
// more than they can: otherwise managing roles would be owning the place.
func TestNobodyGrantsMoreThanTheyHold(t *testing.T) {
	w := newPermissionWorld(t)
	rita := testsupport.SeedUser(t, w.pool, w.org, "rita@example.test", "member")
	victim := testsupport.SeedUser(t, w.pool, w.org, "vic@example.test", "member")
	w.give(t, rita, w.roleWith(t, "Role keeper", auth.PermRoles, auth.PermTasksAll))
	asRita := w.as(t, rita)

	if got := w.do(t, asRita, http.MethodPost, "/api/roles", map[string]any{"name": "Escalate", "permissions": []string{auth.PermOrgSettings}}); got.Code != http.StatusForbidden {
		t.Errorf("granting what she lacks: %d %s", got.Code, got.Body.String())
	}
	if got := w.do(t, asRita, http.MethodPost, "/api/roles", map[string]any{"name": "Everything", "permissions": []string{"*"}}); got.Code != http.StatusBadRequest {
		t.Errorf("everything is not a permission to hand out: %d", got.Code)
	}
	if got := w.do(t, asRita, http.MethodPost, "/api/roles", map[string]any{"name": "Typo", "permissions": []string{"tasks.everything"}}); got.Code != http.StatusBadRequest {
		t.Errorf("an unknown permission: %d", got.Code)
	}
	got := w.do(t, asRita, http.MethodPost, "/api/roles", map[string]any{"name": "Task lead", "permissions": []string{auth.PermTasksAll}})
	if got.Code != http.StatusCreated {
		t.Fatalf("within what she holds: %d %s", got.Code, got.Body.String())
	}
	var lead Role
	_ = json.Unmarshal(got.Body.Bytes(), &lead)
	if got := w.do(t, asRita, http.MethodPost, "/api/users/"+victim.String()+"/roles", map[string]string{"roleId": lead.ID.String()}); got.Code != http.StatusOK {
		t.Errorf("handing out a role within reach: %d %s", got.Code, got.Body.String())
	}
	for _, role := range []string{"admin", "owner"} {
		if got := w.do(t, asRita, http.MethodPost, "/api/users/"+rita.String()+"/roles", map[string]string{"roleId": w.org.RoleIDs[role].String()}); got.Code != http.StatusForbidden {
			t.Errorf("making herself %s: %d", role, got.Code)
		}
	}
	if got := w.do(t, asRita, http.MethodDelete, "/api/users/"+w.org.OwnerID.String()+"/roles/"+w.org.RoleIDs["owner"].String(), nil); got.Code != http.StatusForbidden {
		t.Errorf("taking the owner's role away: %d", got.Code)
	}

	// The last owner stays an owner, even by their own hand.
	if err := w.svc.RemoveRole(context.Background(), w.org.ID, w.org.OwnerID, w.org.RoleIDs["owner"], []string{auth.Every}); err == nil {
		t.Errorf("the organization kept no owner")
	}
}

// Taking a role away takes its permissions away, without waiting for the
// person to sign in again.
func TestTakingARoleAwayTakesItsPermissionsAway(t *testing.T) {
	w := newPermissionWorld(t)
	mo := testsupport.SeedUser(t, w.pool, w.org, "mo@example.test", "member")
	role := w.roleWith(t, "Office manager", auth.PermOrgSettings)
	w.give(t, mo, role)
	if got := w.do(t, w.as(t, mo), http.MethodGet, "/api/org/mail", nil); got.Code != http.StatusOK {
		t.Fatalf("with the role: %d", got.Code)
	}
	if err := w.svc.RemoveRole(context.Background(), w.org.ID, mo, role, []string{auth.Every}); err != nil {
		t.Fatal(err)
	}
	if got := w.do(t, w.as(t, mo), http.MethodGet, "/api/org/mail", nil); got.Code != http.StatusForbidden {
		t.Errorf("without it: %d", got.Code)
	}
}

func TestThePermissionListIsServedForTheRolesScreen(t *testing.T) {
	w := newPermissionWorld(t)
	got := w.do(t, w.as(t, w.org.OwnerID), http.MethodGet, "/api/permissions", nil)
	var body struct {
		Permissions []auth.Permission `json:"permissions"`
	}
	_ = json.Unmarshal(got.Body.Bytes(), &body)
	if got.Code != http.StatusOK || len(body.Permissions) != len(auth.Catalog) || body.Permissions[0].Label == "" || body.Permissions[0].Area == "" {
		t.Errorf("the list: %d %+v", got.Code, body)
	}
}

// Managing members is not managing roles: a new member cannot be made an owner
// by somebody who could not make anybody one.
func TestAddingAMemberGivesNoRoleBeyondYourOwn(t *testing.T) {
	w := newPermissionWorld(t)
	hr := testsupport.SeedUser(t, w.pool, w.org, "hr@example.test", "member")
	w.give(t, hr, w.roleWith(t, "People team", auth.PermMembers))
	asHR := w.as(t, hr)
	if got := w.do(t, asHR, http.MethodPost, "/api/users", map[string]string{"name": "Evil", "email": "evil@example.test", "role": "owner"}); got.Code != http.StatusForbidden {
		t.Errorf("adding an owner: %d %s", got.Code, got.Body.String())
	}
	if got := w.do(t, asHR, http.MethodPost, "/api/users", map[string]string{"name": "Fine", "email": "fine@example.test", "role": "member"}); got.Code != http.StatusCreated {
		t.Errorf("adding a member: %d %s", got.Code, got.Body.String())
	}
	got := w.do(t, asHR, http.MethodPost, "/api/users/import", map[string]string{"csv": "name,email,role\nSneaky,sneaky@example.test,admin\nOk,ok@example.test,member"})
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "you cannot give the admin role") || !strings.Contains(got.Body.String(), `"created":1,`) {
		t.Errorf("importing an admin: %d %s", got.Code, got.Body.String())
	}
}

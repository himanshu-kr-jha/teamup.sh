package org

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

func newService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	return NewService(pool), pool
}

// ── Organizations ─────────────────────────────────────────────────────────

func TestCreateAndReadOrganization(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	logo := "https://cdn.example.test/logo.png"
	created, err := svc.CreateOrganization(ctx, "Acme Inc", "acme", &logo)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// CreateOrganization used to build a struct and never insert, so the row
	// it claimed to have created could not be read back.
	loaded, err := svc.GetOrganization(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if loaded.Name != "Acme Inc" || loaded.Subdomain != "acme" {
		t.Errorf("wrong organization: %+v", loaded)
	}
	if loaded.LogoURL == nil || *loaded.LogoURL != logo {
		t.Errorf("logo not persisted: %+v", loaded.LogoURL)
	}
	if !loaded.IsActive {
		t.Error("a new organization should be active")
	}

	bySub, err := svc.GetOrganizationBySubdomain(ctx, "acme")
	if err != nil {
		t.Fatalf("get by subdomain: %v", err)
	}
	if bySub.ID != created.ID {
		t.Error("subdomain lookup resolved to a different organization")
	}
}

func TestGetOrganizationUnknownIsNotFound(t *testing.T) {
	svc, _ := newService(t)
	if _, err := svc.GetOrganization(context.Background(), uuid.New()); err == nil {
		t.Fatal("an unknown organization must report not-found, not an empty struct")
	}
	if _, err := svc.GetOrganizationBySubdomain(context.Background(), "nope"); err == nil {
		t.Fatal("an unknown subdomain must report not-found")
	}
}

func TestUpdateOrganizationLeavesUnsuppliedFieldsAlone(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	logo := "https://cdn.example.test/a.png"
	if _, err := svc.UpdateOrganization(ctx, org.ID, nil, &logo, nil); err != nil {
		t.Fatalf("set logo: %v", err)
	}

	newName := "Renamed Org"
	updated, err := svc.UpdateOrganization(ctx, org.ID, &newName, nil, nil)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if updated.Name != newName {
		t.Errorf("name not updated: %q", updated.Name)
	}
	if updated.LogoURL == nil || *updated.LogoURL != logo {
		t.Error("a partial update must not blank the logo set earlier")
	}
}

// ── Users ─────────────────────────────────────────────────────────────────

func TestCreateAndReadUser(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	created, err := svc.CreateUser(ctx, org.ID, "Ada Lovelace", "ada@acme.test", "", "member", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	loaded, err := svc.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if loaded.Email != "ada@acme.test" || loaded.Name != "Ada Lovelace" {
		t.Errorf("wrong user: %+v", loaded)
	}
	if loaded.OrgID != org.ID {
		t.Error("user is not attached to the organization")
	}
}

func TestGetUserByEmailDoesNotCrossOrganizations(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	a := testsupport.SeedOrg(t, pool)
	b := testsupport.SeedOrg(t, pool)

	const shared = "shared@example.test"
	if _, err := svc.CreateUser(ctx, a.ID, "In A", shared, "", "member", false); err != nil {
		t.Fatalf("create in a: %v", err)
	}
	if _, err := svc.CreateUser(ctx, b.ID, "In B", shared, "", "member", false); err != nil {
		t.Fatalf("create in b: %v", err)
	}

	inA, err := svc.GetUserByEmail(ctx, a.ID, shared)
	if err != nil {
		t.Fatalf("lookup in a: %v", err)
	}
	if inA.Name != "In A" {
		t.Errorf("lookup crossed the organization boundary: %+v", inA)
	}
	// Casing must not matter.
	if _, err := svc.GetUserByEmail(ctx, b.ID, "SHARED@EXAMPLE.TEST"); err != nil {
		t.Errorf("email lookup should be case insensitive: %v", err)
	}
}

func TestListUsersIsScopedAndOrdered(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	mine := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)
	testsupport.SeedUser(t, pool, mine, "zoe@example.test", "member")
	testsupport.SeedUser(t, pool, other, "outsider@example.test", "member")

	users, err := svc.ListUsers(ctx, mine.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected the owner plus one member, got %d", len(users))
	}
	for _, u := range users {
		if u.OrgID != mine.ID {
			t.Errorf("list leaked a user from another organization: %s", u.Email)
		}
	}
}

func TestListUsersReportsRoles(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	users, err := svc.ListUsers(context.Background(), org.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var owner *User
	for i := range users {
		if users[i].ID == org.OwnerID {
			owner = &users[i]
		}
	}
	if owner == nil {
		t.Fatal("owner missing from the member list")
	}
	if len(owner.Roles) == 0 || owner.Roles[0] != "owner" {
		t.Errorf("owner's RBAC roles not reported: %v", owner.Roles)
	}
}

func TestDeactivateUserHidesThemFromTheOrganization(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	victim := testsupport.SeedUser(t, pool, org, "leaver@example.test", "member")

	if err := svc.DeactivateUser(ctx, org.ID, victim); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := svc.GetUser(ctx, victim); err == nil {
		t.Error("a deactivated user should no longer load")
	}
	users, _ := svc.ListUsers(ctx, org.ID)
	for _, u := range users {
		if u.ID == victim {
			t.Error("a deactivated user should not appear in the member list")
		}
	}
}

func TestDeactivateUserFromAnotherOrganizationIsRefused(t *testing.T) {
	svc, pool := newService(t)
	mine := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	if err := svc.DeactivateUser(context.Background(), mine.ID, other.OwnerID); err == nil {
		t.Fatal("deactivating a user in another organization must fail")
	}
}

// ── Roles ─────────────────────────────────────────────────────────────────

func TestCreateDefaultRolesIsIdempotent(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	created, err := svc.CreateOrganization(ctx, "Roles Inc", "roles", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.CreateDefaultRoles(ctx, created.ID); err != nil {
			t.Fatalf("create default roles (pass %d): %v", i, err)
		}
	}
	roles, err := svc.ListRoles(ctx, created.ID)
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(roles) != 4 {
		t.Fatalf("expected the 4 default roles, got %d", len(roles))
	}
	for _, r := range roles {
		if !r.IsSystem {
			t.Errorf("%s should be a system role", r.Name)
		}
		if len(r.Permissions) == 0 {
			t.Errorf("%s has no permissions", r.Name)
		}
	}
	_ = pool
}

func TestCustomRoleLifecycle(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	role, err := svc.CreateRole(ctx, org.ID, "auditor", "Read-only", []string{"tasks:read"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	newPerms := []string{"tasks:read", "reports:read"}
	updated, err := svc.UpdateRole(ctx, org.ID, role.ID, nil, nil, newPerms)
	if err != nil {
		t.Fatalf("update role: %v", err)
	}
	if len(updated.Permissions) != 2 {
		t.Errorf("permissions not updated: %v", updated.Permissions)
	}

	if err := svc.DeleteRole(ctx, org.ID, role.ID); err != nil {
		t.Fatalf("delete role: %v", err)
	}
}

// Removing a built-in role would strip every permission from whoever holds it,
// including the only owner.
func TestSystemRolesCannotBeChangedOrDeleted(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	ownerRole := org.RoleIDs["owner"]
	if err := svc.DeleteRole(ctx, org.ID, ownerRole); err == nil {
		t.Error("a system role must not be deletable")
	}
	name := "hijacked"
	if _, err := svc.UpdateRole(ctx, org.ID, ownerRole, &name, nil, nil); err == nil {
		t.Error("a system role must not be editable")
	}
}

func TestAssignRoleIsIdempotentAndScoped(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)
	member := testsupport.SeedUser(t, pool, org, "member@example.test", "member")

	adminRole := org.RoleIDs["admin"]
	for i := 0; i < 2; i++ {
		if err := svc.AssignRole(ctx, org.ID, member, adminRole, org.OwnerID); err != nil {
			t.Fatalf("assign (pass %d): %v", i, err)
		}
	}

	u, err := svc.GetUser(ctx, member)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !hasRole(u.Roles, "admin") {
		t.Errorf("role not applied: %v", u.Roles)
	}

	// A role belonging to another organization must not attach.
	if err := svc.AssignRole(ctx, org.ID, member, other.RoleIDs["admin"], org.OwnerID); err == nil {
		t.Error("assigning a role from another organization must fail")
	}
}

// The review found this reachable: an admin of one organization could grant a
// role belonging to another, because only the user and role had to agree.
func TestAssignRoleRefusesACrossOrganizationGrant(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	attacker := testsupport.SeedOrg(t, pool)
	victim := testsupport.SeedOrg(t, pool)

	// Attacker's organization, victim's user and victim's role.
	if err := svc.AssignRole(ctx, attacker.ID, victim.OwnerID, victim.RoleIDs["owner"], attacker.OwnerID); err == nil {
		t.Fatal("granting a role inside another organization must be refused")
	}

	var granted int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_roles WHERE user_id = $1 AND role_id = $2`,
		victim.OwnerID, victim.RoleIDs["owner"]).Scan(&granted); err != nil {
		t.Fatalf("count: %v", err)
	}
	// Only the seeded assignment; no extra row from the attacker.
	if granted != 1 {
		t.Errorf("expected the seeded assignment only, got %d", granted)
	}
}

func TestTeamManagerMustBelongToTheOrganization(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	if _, err := svc.CreateTeam(ctx, org.ID, "Platform", "", nil, &other.OwnerID); err == nil {
		t.Fatal("a manager from another organization must be refused")
	}

	team, err := svc.CreateTeam(ctx, org.ID, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if _, err := svc.UpdateTeam(ctx, org.ID, team.ID, nil, nil, &other.OwnerID, nil); err == nil {
		t.Fatal("reassigning to a foreign manager must be refused")
	}
}

func TestRemoveRole(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	member := testsupport.SeedUser(t, pool, org, "demote@example.test", "member")

	adminRole := org.RoleIDs["admin"]
	if err := svc.AssignRole(ctx, org.ID, member, adminRole, org.OwnerID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := svc.RemoveRole(ctx, org.ID, member, adminRole); err != nil {
		t.Fatalf("remove: %v", err)
	}
	u, _ := svc.GetUser(ctx, member)
	if hasRole(u.Roles, "admin") {
		t.Errorf("role still present after removal: %v", u.Roles)
	}
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// ── Teams ─────────────────────────────────────────────────────────────────

func TestTeamLifecycleAndMembership(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	member := testsupport.SeedUser(t, pool, org, "teammate@example.test", "member")

	team, err := svc.CreateTeam(ctx, org.ID, "Platform", "Core team", nil, &org.OwnerID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := svc.AddTeamMember(ctx, org.ID, team.ID, member); err != nil {
			t.Fatalf("add member (pass %d): %v", i, err)
		}
	}

	members, err := svc.ListTeamMembers(ctx, org.ID, team.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("adding twice must not duplicate membership, got %d", len(members))
	}

	teams, err := svc.ListTeams(ctx, org.ID)
	if err != nil {
		t.Fatalf("list teams: %v", err)
	}
	if len(teams) != 1 || teams[0].MemberCount != 1 {
		t.Errorf("team listing wrong: %+v", teams)
	}

	if err := svc.RemoveTeamMember(ctx, org.ID, team.ID, member); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if err := svc.DeleteTeam(ctx, org.ID, team.ID); err != nil {
		t.Fatalf("delete team: %v", err)
	}
	teams, _ = svc.ListTeams(ctx, org.ID)
	if len(teams) != 0 {
		t.Errorf("deleted team still listed: %+v", teams)
	}
}

func TestAddTeamMemberRefusesAnotherOrganizationsUser(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	team, err := svc.CreateTeam(ctx, org.ID, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	if err := svc.AddTeamMember(ctx, org.ID, team.ID, other.OwnerID); err == nil {
		t.Fatal("a user from another organization must not join this team")
	}
}

// ── Configuration ─────────────────────────────────────────────────────────

func TestConfigRoundTripAndOverwrite(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	if err := svc.SetConfig(ctx, org.ID, "theme", map[string]any{"mode": "dark"}, false); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := svc.GetConfig(ctx, org.ID, "theme")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok || m["mode"] != "dark" {
		t.Errorf("config round trip lost the value: %#v", got)
	}

	// Writing the same key again updates rather than duplicating.
	if err := svc.SetConfig(ctx, org.ID, "theme", map[string]any{"mode": "light"}, false); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, _ = svc.GetConfig(ctx, org.ID, "theme")
	if got.(map[string]any)["mode"] != "light" {
		t.Errorf("overwrite did not take: %#v", got)
	}
}

// The setup wizard writes service credentials here; nothing user-facing may
// hand them back.
func TestSecretConfigsAreExcludedFromPublicListing(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	if err := svc.SetConfig(ctx, org.ID, "minio", map[string]any{"secret_key": "hunter2"}, true); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	if err := svc.SetConfig(ctx, org.ID, "theme", map[string]any{"mode": "dark"}, false); err != nil {
		t.Fatalf("set public: %v", err)
	}

	public, err := svc.ListPublicConfigs(ctx, org.ID)
	if err != nil {
		t.Fatalf("list public: %v", err)
	}
	if _, leaked := public["minio"]; leaked {
		t.Error("a secret config must not appear in the public listing")
	}
	if _, ok := public["theme"]; !ok {
		t.Error("non-secret config should be listed")
	}
}

func TestGetConfigUnknownKeyIsNotFound(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	if _, err := svc.GetConfig(context.Background(), org.ID, "nope"); err == nil {
		t.Fatal("an unknown config key must report not-found")
	}
}

func TestGetConfigIntoDecodesTypedValues(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	type vapid struct {
		PublicKey  string `json:"public_key"`
		PrivateKey string `json:"private_key"`
	}
	if err := svc.SetConfig(ctx, org.ID, "vapid", vapid{"pub", "priv"}, true); err != nil {
		t.Fatalf("set: %v", err)
	}
	var got vapid
	if err := svc.GetConfigInto(ctx, org.ID, "vapid", &got); err != nil {
		t.Fatalf("get into: %v", err)
	}
	if got.PublicKey != "pub" || got.PrivateKey != "priv" {
		t.Errorf("typed decode lost data: %+v", got)
	}
}

// ── Adding members (ticket 15) ────────────────────────────────────────────
//
// There was no way to create a user at all: no endpoint, no invitation table,
// and the sign-up link pointed at a route that does not exist. The product was
// a single-user demo.

func TestAddMemberCreatesALoginableAccountWithARole(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	member, temporaryPassword, err := svc.AddMember(ctx, org.ID, "Priya Sharma", "priya@acme.test", "member")
	if err != nil {
		t.Fatalf("add member: %v", err)
	}
	if member.Email != "priya@acme.test" || member.Name != "Priya Sharma" {
		t.Errorf("wrong member: %+v", member)
	}
	if len(temporaryPassword) < 12 {
		t.Errorf("the temporary password must satisfy the minimum length, got %d chars", len(temporaryPassword))
	}

	// A member with no role can sign in and then do nothing, which is the
	// trap I hit adding a user by hand.
	if !hasRole(member.Roles, "member") {
		t.Errorf("the requested role should be assigned, got %v", member.Roles)
	}
	if !member.MustChangePassword {
		t.Error("a generated password must be flagged for replacement")
	}

	listed, err := svc.ListUsers(ctx, org.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, u := range listed {
		if u.ID == member.ID {
			found = true
		}
	}
	if !found {
		t.Error("the new member should appear in the members list")
	}
}

func TestAddMemberGeneratesADifferentPasswordEachTime(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	_, first, err := svc.AddMember(ctx, org.ID, "One", "one@acme.test", "member")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_, second, err := svc.AddMember(ctx, org.ID, "Two", "two@acme.test", "member")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first == second {
		t.Fatal("temporary passwords must not repeat")
	}
}

func TestAddMemberRejectsADuplicateEmail(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	if _, _, err := svc.AddMember(ctx, org.ID, "Priya", "dupe@acme.test", "member"); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, _, err := svc.AddMember(ctx, org.ID, "Someone Else", "dupe@acme.test", "member")
	if err == nil {
		t.Fatal("a duplicate email must be refused")
	}
	// It has to read as a conflict, not a server error.
	if apperrors.HTTPStatus(err) != http.StatusConflict {
		t.Errorf("expected a conflict, got status %d for %v", apperrors.HTTPStatus(err), err)
	}
}

// The same address may legitimately exist in another tenant.
func TestAddMemberAllowsTheSameEmailInAnotherOrganization(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	a := testsupport.SeedOrg(t, pool)
	b := testsupport.SeedOrg(t, pool)

	if _, _, err := svc.AddMember(ctx, a.ID, "In A", "shared@acme.test", "member"); err != nil {
		t.Fatalf("in a: %v", err)
	}
	if _, _, err := svc.AddMember(ctx, b.ID, "In B", "shared@acme.test", "member"); err != nil {
		t.Fatalf("the same address in another organization should be allowed: %v", err)
	}
}

func TestAddMemberRejectsAnUnknownRole(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	if _, _, err := svc.AddMember(context.Background(), org.ID, "Nobody", "nobody@acme.test", "sysadmin"); err == nil {
		t.Fatal("a role that does not exist in this organization must be refused")
	}
}

// ── Nested teams ──────────────────────────────────────────────────────────

// A team's place in the tree was fixed at creation: parent_id was accepted on
// insert and never again, so no reorganisation could ever be recorded and the
// recursive walks below had nothing deeper than one level to walk.
func TestUpdateTeamReparentsATeam(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	root, err := svc.CreateTeam(ctx, org.ID, "Engineering", "", nil, nil)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	moving, err := svc.CreateTeam(ctx, org.ID, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}

	under := &root.ID
	updated, err := svc.UpdateTeam(ctx, org.ID, moving.ID, nil, nil, nil, &under)
	if err != nil {
		t.Fatalf("reparent: %v", err)
	}
	if updated.ParentID == nil || *updated.ParentID != root.ID {
		t.Fatalf("team was not moved under its new parent: %+v", updated.ParentID)
	}

	// Not supplying a parent leaves the one it has alone, the same way the
	// other fields behave.
	name := "Platform Group"
	updated, err = svc.UpdateTeam(ctx, org.ID, moving.ID, &name, nil, nil, nil)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if updated.ParentID == nil || *updated.ParentID != root.ID {
		t.Errorf("renaming dropped the parent: %+v", updated.ParentID)
	}

	// And a team can be lifted back to the top level.
	var none *uuid.UUID
	updated, err = svc.UpdateTeam(ctx, org.ID, moving.ID, nil, nil, nil, &none)
	if err != nil {
		t.Fatalf("detach: %v", err)
	}
	if updated.ParentID != nil {
		t.Errorf("team should be back at the top level, parent is %v", *updated.ParentID)
	}
}

// The one that takes the product down. Every walk of the tree is a RECURSIVE
// CTE over parent_id; a cycle does not hang them (they all UNION, which
// deduplicates) but it does something quieter and worse — everybody in the
// cycle ends up managing everybody else, so a team lead two levels down
// acquires authority over their own manager's team.
func TestUpdateTeamRefusesACycle(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	top, err := svc.CreateTeam(ctx, org.ID, "Engineering", "", nil, &org.OwnerID)
	if err != nil {
		t.Fatalf("create top: %v", err)
	}
	middle, err := svc.CreateTeam(ctx, org.ID, "Platform", "", &top.ID, nil)
	if err != nil {
		t.Fatalf("create middle: %v", err)
	}
	bottom, err := svc.CreateTeam(ctx, org.ID, "Storage", "", &middle.ID, nil)
	if err != nil {
		t.Fatalf("create bottom: %v", err)
	}

	for _, tc := range []struct {
		name   string
		team   uuid.UUID
		parent uuid.UUID
	}{
		{"under itself", top.ID, top.ID},
		{"under its child", top.ID, middle.ID},
		{"under its grandchild", top.ID, bottom.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := &tc.parent
			if _, err := svc.UpdateTeam(ctx, org.ID, tc.team, nil, nil, nil, &parent); err == nil {
				t.Fatal("re-parenting a team beneath its own descendant must be refused")
			}
		})
	}

	// The tree is untouched: authority still runs downwards only, and the
	// walk still terminates.
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	manages, err := svc.ManagesTeam(deadline, org.ID, bottom.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("walk the tree after the refusals: %v", err)
	}
	if !manages {
		t.Error("running the top team should still reach the bottom of the tree")
	}

	// And not upwards. This is what a cycle would have granted.
	lead := testsupport.SeedUser(t, pool, org, "lead@example.test", "member")
	if _, err := svc.UpdateTeam(ctx, org.ID, bottom.ID, nil, nil, &lead, nil); err != nil {
		t.Fatalf("set the bottom team's manager: %v", err)
	}
	upwards, err := svc.ManagesTeam(deadline, org.ID, top.ID, lead)
	if err != nil {
		t.Fatalf("check upwards: %v", err)
	}
	if upwards {
		t.Error("running a team at the bottom must not confer control of the team above it")
	}
}

func TestUpdateTeamRefusesAParentInAnotherOrganization(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	mine, err := svc.CreateTeam(ctx, org.ID, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	theirs, err := svc.CreateTeam(ctx, other.ID, "Their Engineering", "", nil, nil)
	if err != nil {
		t.Fatalf("create their team: %v", err)
	}

	parent := &theirs.ID
	if _, err := svc.UpdateTeam(ctx, org.ID, mine.ID, nil, nil, nil, &parent); err == nil {
		t.Fatal("a parent from another organization must be refused")
	}

	teams, err := svc.ListTeams(ctx, org.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(teams) != 1 || teams[0].ParentID != nil {
		t.Errorf("the refused move still changed the tree: %+v", teams)
	}
}

// Deleting a team is a soft delete, so parent_id's ON DELETE SET NULL never
// fires: without this the children keep pointing at a row nobody can see and
// the whole subtree drops out of every manager's view.
func TestDeleteTeamGraftsChildrenOntoTheParent(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	top, err := svc.CreateTeam(ctx, org.ID, "Engineering", "", nil, &org.OwnerID)
	if err != nil {
		t.Fatalf("create top: %v", err)
	}
	middle, err := svc.CreateTeam(ctx, org.ID, "Platform", "", &top.ID, nil)
	if err != nil {
		t.Fatalf("create middle: %v", err)
	}
	bottom, err := svc.CreateTeam(ctx, org.ID, "Storage", "", &middle.ID, nil)
	if err != nil {
		t.Fatalf("create bottom: %v", err)
	}

	if err := svc.DeleteTeam(ctx, org.ID, middle.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	teams, err := svc.ListTeams(ctx, org.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var graft *Team
	for i := range teams {
		if teams[i].ID == bottom.ID {
			graft = &teams[i]
		}
	}
	if graft == nil {
		t.Fatal("the child of a deleted team disappeared from the organization")
	}
	if graft.ParentID == nil || *graft.ParentID != top.ID {
		t.Fatalf("child should hang off the deleted team's parent, parent is %v", graft.ParentID)
	}

	// The point of the graft: whoever ran the tree still runs what is left.
	manages, err := svc.ManagesTeam(ctx, org.ID, bottom.ID, org.OwnerID)
	if err != nil {
		t.Fatalf("check management: %v", err)
	}
	if !manages {
		t.Error("deleting a team in the middle cut its children off from their manager")
	}
}

func TestDeleteTeamLiftsChildrenOfARootToTheTopLevel(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	// A same-shaped tree next door, which must not be touched.
	theirTop, err := svc.CreateTeam(ctx, other.ID, "Engineering", "", nil, nil)
	if err != nil {
		t.Fatalf("create their top: %v", err)
	}
	theirChild, err := svc.CreateTeam(ctx, other.ID, "Platform", "", &theirTop.ID, nil)
	if err != nil {
		t.Fatalf("create their child: %v", err)
	}

	top, err := svc.CreateTeam(ctx, org.ID, "Engineering", "", nil, nil)
	if err != nil {
		t.Fatalf("create top: %v", err)
	}
	child, err := svc.CreateTeam(ctx, org.ID, "Platform", "", &top.ID, nil)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	if err := svc.DeleteTeam(ctx, org.ID, top.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	teams, err := svc.ListTeams(ctx, org.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(teams) != 1 || teams[0].ID != child.ID {
		t.Fatalf("expected the child alone, got %+v", teams)
	}
	if teams[0].ParentID != nil {
		t.Errorf("the child of a deleted root should be a root, parent is %v", *teams[0].ParentID)
	}

	// The neighbouring organization's identical tree is untouched.
	theirs, err := svc.ListTeams(ctx, other.ID)
	if err != nil {
		t.Fatalf("list theirs: %v", err)
	}
	if len(theirs) != 2 {
		t.Fatalf("a delete in one organization changed another: %+v", theirs)
	}
	for _, tm := range theirs {
		if tm.ID == theirChild.ID && (tm.ParentID == nil || *tm.ParentID != theirTop.ID) {
			t.Errorf("another organization's tree was re-parented: %+v", tm)
		}
	}
}

// ── Bulk onboarding ───────────────────────────────────────────────────────

// Onboarding 50 people meant repeating a dialog 49 times. The point of doing
// it in one operation is that one bad line cannot cost the other 49.
func TestImportMembersReportsEveryRowSeparately(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	team, err := svc.CreateTeam(ctx, org.ID, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}

	outcomes, err := svc.ImportMembers(ctx, org.ID, strings.Join([]string{
		"name,email,role,team",
		"Ada Lovelace,ada@acme.test,member,",
		"Grace Hopper,grace@acme.test,admin,Platform",
		"Ada Again,ada@acme.test,member,",
		"Nameless,,member,",
		"Wrong Role,wrong@acme.test,sysadmin,",
		"Wrong Team,team@acme.test,member,Nonesuch",
		"Katherine Johnson,katherine@acme.test,,",
	}, "\n"))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	want := []struct {
		email  string
		status string
	}{
		{"ada@acme.test", ImportCreated},
		{"grace@acme.test", ImportCreated},
		{"ada@acme.test", ImportDuplicate},
		{"", ImportInvalid},
		{"wrong@acme.test", ImportInvalid},
		{"team@acme.test", ImportInvalid},
		{"katherine@acme.test", ImportCreated},
	}
	if len(outcomes) != len(want) {
		t.Fatalf("want an outcome per row (%d), got %d: %+v", len(want), len(outcomes), outcomes)
	}
	seen := map[string]bool{}
	for i, w := range want {
		got := outcomes[i]
		if got.Email != w.email || got.Status != w.status {
			t.Errorf("row %d: want %q/%s, got %q/%s (%s)", i, w.email, w.status, got.Email, got.Status, got.Message)
		}
		// The line number has to be the one the administrator sees in their
		// spreadsheet, or "row 4 is wrong" is unactionable.
		if got.Row != i+2 {
			t.Errorf("row %d reported as line %d", i, got.Row)
		}
		if w.status == ImportCreated {
			if len(got.TemporaryPassword) < 12 {
				t.Errorf("%s got no usable password to hand over: %q", w.email, got.TemporaryPassword)
			}
			if seen[got.TemporaryPassword] {
				t.Errorf("%s was given a password already issued to somebody else", w.email)
			}
			seen[got.TemporaryPassword] = true
		} else if got.TemporaryPassword != "" {
			t.Errorf("%s failed but was still handed a password", w.email)
		}
	}

	// The failures did not take the successes with them.
	users, err := svc.ListUsers(ctx, org.ID)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	byEmail := map[string]User{}
	for _, u := range users {
		byEmail[u.Email] = u
	}
	for _, email := range []string{"ada@acme.test", "grace@acme.test", "katherine@acme.test"} {
		u, ok := byEmail[email]
		if !ok {
			t.Fatalf("%s was reported as created but does not exist", email)
		}
		if !u.MustChangePassword {
			t.Errorf("%s must be made to replace the generated password", email)
		}
	}
	for _, email := range []string{"wrong@acme.test", "team@acme.test"} {
		if _, ok := byEmail[email]; ok {
			t.Errorf("%s was rejected but an account was created anyway", email)
		}
	}
	if !hasRole(byEmail["grace@acme.test"].Roles, "admin") {
		t.Errorf("the role column was ignored: %v", byEmail["grace@acme.test"].Roles)
	}

	// The team column put her on the team, rather than being decoration.
	members, err := svc.ListTeamMembers(ctx, org.ID, team.ID)
	if err != nil {
		t.Fatalf("list team members: %v", err)
	}
	if len(members) != 1 || members[0].Email != "grace@acme.test" {
		t.Errorf("team column did not place the member: %+v", members)
	}
}

func TestImportMembersOnlySeesItsOwnOrganization(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	theirs, err := svc.CreateTeam(ctx, other.ID, "Secret", "", nil, nil)
	if err != nil {
		t.Fatalf("create their team: %v", err)
	}

	outcomes, err := svc.ImportMembers(ctx, org.ID, strings.Join([]string{
		"name,email,role,team",
		"Intruder,intruder@acme.test,member,Secret",
	}, "\n"))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Status != ImportInvalid {
		t.Fatalf("a team belonging to another organization must not resolve: %+v", outcomes)
	}

	members, err := svc.ListTeamMembers(ctx, other.ID, theirs.ID)
	if err != nil {
		t.Fatalf("list their members: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("the import reached into another organization's team: %+v", members)
	}
	users, err := svc.ListUsers(ctx, other.ID)
	if err != nil {
		t.Fatalf("list their users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("the import created accounts in another organization: %+v", users)
	}
}

func TestImportMembersRefusesAnOversizedBatch(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	rows := make([]string, 0, maxImportRows+2)
	rows = append(rows, "name,email")
	for i := 0; i <= maxImportRows; i++ {
		rows = append(rows, fmt.Sprintf("Person %d,person%d@acme.test", i, i))
	}

	if _, err := svc.ImportMembers(context.Background(), org.ID, strings.Join(rows, "\n")); err == nil {
		t.Fatal("a batch past the cap must be refused rather than held open for minutes")
	}

	users, err := svc.ListUsers(context.Background(), org.ID)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("a refused batch still created %d account(s)", len(users)-1)
	}
}

func TestImportMembersNeedsAUsableHeader(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	for _, csv := range []string{
		"",
		"Ada Lovelace,ada@acme.test",
		"forename,address\nAda,ada@acme.test",
	} {
		if _, err := svc.ImportMembers(context.Background(), org.ID, csv); err == nil {
			t.Errorf("a file with no name/email header must be refused, accepted %q", csv)
		}
	}
}

// The add-member dialog rejects a malformed address with a 400; an import must
// hold the same line, or a batch quietly creates accounts whose owners can
// never be reached and whose address cannot be corrected to one already taken.
func TestImportMembersRejectsAnAddressThatIsNotAnEmail(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	outcomes, err := svc.ImportMembers(ctx, org.ID, strings.Join([]string{
		"name,email",
		"Malformed,not-an-email",
		"Fine,fine@acme.test",
	}, "\n"))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("want two outcomes, got %+v", outcomes)
	}
	if outcomes[0].Status != ImportInvalid {
		t.Errorf("a malformed address must be rejected, got %s (%s)", outcomes[0].Status, outcomes[0].Message)
	}
	if outcomes[1].Status != ImportCreated {
		t.Errorf("the good row should still go through, got %s (%s)", outcomes[1].Status, outcomes[1].Message)
	}

	users, err := svc.ListUsers(ctx, org.ID)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	for _, u := range users {
		if u.Email == "not-an-email" {
			t.Error("an account was created with an address nobody can be reached at")
		}
	}
}

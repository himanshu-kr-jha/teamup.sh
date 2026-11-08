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

	"github.com/teamup/teamup/internal/testsupport"
)

// seedDefaultRoles mirrors what the setup wizard creates for a real
// organization, so AddMember's default of "member" resolves.
func seedDefaultRoles(t *testing.T, svc *Service, orgID uuid.UUID) {
	t.Helper()
	if err := svc.CreateDefaultRoles(context.Background(), orgID); err != nil {
		t.Fatalf("seed roles: %v", err)
	}
}

// asRoles builds a router whose only middleware is a stand-in for the auth
// middleware, so the tests can say who is calling without minting real tokens.
// The caller is nobody in particular, which is what a test about roles alone
// wants.
func asRoles(t *testing.T, svc *Service, orgID string, roles ...string) *gin.Engine {
	t.Helper()
	return asUser(t, svc, orgID, "00000000-0000-0000-0000-000000000001", roles...)
}

// asUser is asRoles for the tests that turn on which person is calling rather
// than which role they hold — team authority is read off the tree, so the
// caller has to be a real row in it.
func asUser(t *testing.T, svc *Service, orgID, userID string, roles ...string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
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

// ── Who may put somebody on a team ────────────────────────────────────────

func TestATeamsManagerCanAddSomebodyToIt(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Managed Co", "managed-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)

	manager, _, err := svc.AddMember(ctx, org.ID, "Manager", "manager@managed.test", "member")
	if err != nil {
		t.Fatalf("add manager: %v", err)
	}
	newcomer, _, err := svc.AddMember(ctx, org.ID, "Newcomer", "newcomer@managed.test", "member")
	if err != nil {
		t.Fatalf("add newcomer: %v", err)
	}

	team, err := svc.CreateTeam(ctx, org.ID, adminActor, "Squad", "", nil, &manager.ID)
	if err != nil {
		t.Fatalf("create team: %v", err)
	}

	manages, err := svc.ManagesTeam(ctx, org.ID, team.ID, manager.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !manages {
		t.Fatal("the person set as manager does not manage the team")
	}

	// Somebody else on the team does not get to decide who else is on it.
	stranger, err := svc.ManagesTeam(ctx, org.ID, team.ID, newcomer.ID)
	if err != nil {
		t.Fatalf("check stranger: %v", err)
	}
	if stranger {
		t.Error("an ordinary member was treated as running the team")
	}
}

func TestManagingATeamReachesTeamsBeneathIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Nested Co", "nested-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)

	head, _, err := svc.AddMember(ctx, org.ID, "Head", "head@nested.test", "member")
	if err != nil {
		t.Fatalf("add head: %v", err)
	}

	parent, err := svc.CreateTeam(ctx, org.ID, adminActor, "Engineering", "", nil, &head.ID)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	var childID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO teams (id, org_id, parent_id, name, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'Interns', NOW(), NOW()) RETURNING id
	`, org.ID, parent.ID).Scan(&childID); err != nil {
		t.Fatalf("create child: %v", err)
	}

	manages, err := svc.ManagesTeam(ctx, org.ID, childID, head.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !manages {
		t.Error("running a team should reach the teams beneath it")
	}
}

func TestManagingATeamDoesNotReachOtherTeams(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Separate Co", "separate-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)

	manager, _, err := svc.AddMember(ctx, org.ID, "Manager", "mgr@separate.test", "member")
	if err != nil {
		t.Fatalf("add manager: %v", err)
	}
	if _, err := svc.CreateTeam(ctx, org.ID, adminActor, "Theirs", "", nil, &manager.ID); err != nil {
		t.Fatalf("create their team: %v", err)
	}
	somebodyElses, err := svc.CreateTeam(ctx, org.ID, adminActor, "Sales", "", nil, nil)
	if err != nil {
		t.Fatalf("create other team: %v", err)
	}

	manages, err := svc.ManagesTeam(ctx, org.ID, somebodyElses.ID, manager.ID)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if manages {
		t.Error("running one team should not confer control of another")
	}
}

// ── Bulk onboarding ───────────────────────────────────────────────────────

func postImport(t *testing.T, r *gin.Engine, csv string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"csv": csv})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/users/import", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

type importResponse struct {
	Created  int `json:"created"`
	Failed   int `json:"failed"`
	Outcomes []struct {
		Row               int    `json:"row"`
		Email             string `json:"email"`
		Status            string `json:"status"`
		Message           string `json:"message"`
		TemporaryPassword string `json:"temporaryPassword"`
	} `json:"outcomes"`
}

func TestImportEndpointReturnsAPasswordForEveryNewMember(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Bulk Co", "bulk-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)

	r := asRoles(t, svc, org.ID.String(), "owner")
	w := postImport(t, r, "name,email,role\n"+
		"Ada,ada@bulk.test,member\n"+
		"Ada Again,ada@bulk.test,member\n"+
		"Grace,grace@bulk.test,admin\n")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var got importResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Created != 2 || got.Failed != 1 {
		t.Errorf("want 2 created and 1 failed, got %d/%d", got.Created, got.Failed)
	}
	if len(got.Outcomes) != 3 {
		t.Fatalf("want an outcome per row, got %d: %+v", len(got.Outcomes), got.Outcomes)
	}
	// The duplicate is reported, not fatal, and the rows around it went through.
	if got.Outcomes[1].Status != "duplicate" {
		t.Errorf("a repeated address should be reported as a duplicate: %+v", got.Outcomes[1])
	}
	for _, i := range []int{0, 2} {
		if got.Outcomes[i].TemporaryPassword == "" {
			t.Errorf("row %d was created with no password to hand over: %+v", i, got.Outcomes[i])
		}
	}
	if got.Outcomes[1].TemporaryPassword != "" {
		t.Error("a rejected row must not carry a password")
	}

	users, err := svc.ListUsers(ctx, org.ID)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("expected the two good rows to exist, got %d", len(users))
	}
}

func TestImportEndpointRefusesANonAdministrator(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Bulk Locked Co", "bulk-locked-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)

	r := asRoles(t, svc, org.ID.String(), "member")
	w := postImport(t, r, "name,email\nMallory,mallory@bulk.test\n")
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}

	users, err := svc.ListUsers(ctx, org.ID)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("a refused caller still created %d account(s)", len(users))
	}
}

func TestImportEndpointRejectsAFileItCannotUse(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Bulk Bad Co", "bulk-bad-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)

	r := asRoles(t, svc, org.ID.String(), "admin")
	// No header naming the columns: a 400 the administrator can act on, not a
	// 500 and not 40 accounts named after their column headings.
	if w := postImport(t, r, "Ada,ada@bulk.test\n"); w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Re-parenting over HTTP ────────────────────────────────────────────────

func patchTeam(t *testing.T, r *gin.Engine, id uuid.UUID, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/teams/"+id.String(), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestUpdateTeamEndpointMovesATeamAroundTheTree(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Tree Co", "tree-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)

	top, err := svc.CreateTeam(ctx, org.ID, adminActor, "Engineering", "", nil, nil)
	if err != nil {
		t.Fatalf("create top: %v", err)
	}
	child, err := svc.CreateTeam(ctx, org.ID, adminActor, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	var got struct {
		ParentID *string `json:"parentId"`
		Name     string  `json:"name"`
	}
	decode := func(w *httptest.ResponseRecorder) {
		t.Helper()
		got.ParentID = nil
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}

	r := asRoles(t, svc, org.ID.String(), "admin")

	w := patchTeam(t, r, child.ID, map[string]any{"parentId": top.ID.String()})
	if w.Code != http.StatusOK {
		t.Fatalf("reparent: want 200, got %d: %s", w.Code, w.Body.String())
	}
	decode(w)
	if got.ParentID == nil || *got.ParentID != top.ID.String() {
		t.Fatalf("team was not filed under its new parent: %+v", got.ParentID)
	}

	// A PATCH that does not mention the parent leaves it where it is.
	w = patchTeam(t, r, child.ID, map[string]any{"name": "Platform Group"})
	if w.Code != http.StatusOK {
		t.Fatalf("rename: want 200, got %d: %s", w.Code, w.Body.String())
	}
	decode(w)
	if got.ParentID == nil || *got.ParentID != top.ID.String() {
		t.Errorf("renaming a team moved it to the top level: %+v", got.ParentID)
	}

	// An explicitly empty parent lifts it back to the top level, which is the
	// case a plain optional field could not express.
	w = patchTeam(t, r, child.ID, map[string]any{"parentId": ""})
	if w.Code != http.StatusOK {
		t.Fatalf("detach: want 200, got %d: %s", w.Code, w.Body.String())
	}
	decode(w)
	if got.ParentID != nil {
		t.Errorf("team should be back at the top level, parent is %v", *got.ParentID)
	}
}

func TestUpdateTeamEndpointRefusesACycle(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Loop Co", "loop-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)

	top, err := svc.CreateTeam(ctx, org.ID, adminActor, "Engineering", "", nil, nil)
	if err != nil {
		t.Fatalf("create top: %v", err)
	}
	child, err := svc.CreateTeam(ctx, org.ID, adminActor, "Platform", "", &top.ID, nil)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	r := asRoles(t, svc, org.ID.String(), "admin")
	// Something an administrator can read and correct, not a 500.
	w := patchTeam(t, r, top.ID, map[string]any{"parentId": child.ID.String()})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}

	teams, err := svc.ListTeams(ctx, org.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, tm := range teams {
		if tm.ID == top.ID && tm.ParentID != nil {
			t.Errorf("the refused move was applied anyway: %+v", tm)
		}
	}
}

func TestUpdateTeamEndpointRefusesANonAdministrator(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Guarded Tree Co", "guarded-tree-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)

	top, err := svc.CreateTeam(ctx, org.ID, adminActor, "Engineering", "", nil, nil)
	if err != nil {
		t.Fatalf("create top: %v", err)
	}
	child, err := svc.CreateTeam(ctx, org.ID, adminActor, "Platform", "", nil, nil)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	r := asRoles(t, svc, org.ID.String(), "member")
	if w := patchTeam(t, r, child.ID, map[string]any{"parentId": top.ID.String()}); w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
}

func postTeamMember(t *testing.T, r *gin.Engine, teamID, userID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"userId": userID.String()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/teams/"+teamID.String()+"/members", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Every authority decision in tasks is read off team_members, so putting
// somebody on your team is what makes you their manager. A lead may do that for
// somebody nobody has yet and for somebody already in their chart, but not to
// their own boss, a peer, or a peer's reports.
//
//	Company     (boss runs it; the lead is on it)
//	├── Platform  (the lead runs it)
//	│   └── Storage  (subLead runs it)
//	└── Sales     (peer runs it)
func TestWhoALeadMayAddToTheirTeam(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedOrg(t, pool)

	user := func(name string) uuid.UUID {
		return testsupport.SeedUser(t, pool, org, name+"@example.test", "member")
	}
	lead, boss, peer, subLead := user("lead"), user("boss"), user("peer"), user("sublead")
	onStorage, onSales, unattached := user("onstorage"), user("onsales"), user("unattached")
	onBoth := user("onboth")

	company := testsupport.SeedTeam(t, pool, org, "Company", &boss, nil, lead)
	platform := testsupport.SeedTeam(t, pool, org, "Platform", &lead, &company)
	testsupport.SeedTeam(t, pool, org, "Storage", &subLead, &platform, onStorage, onBoth)
	testsupport.SeedTeam(t, pool, org, "Sales", &peer, &company, onSales, onBoth)

	asLead := asUser(t, svc, org.ID.String(), lead.String(), "member")
	asAdmin := asUser(t, svc, org.ID.String(), org.OwnerID.String(), "owner")

	cases := []struct {
		name string
		as   *gin.Engine
		who  uuid.UUID
		want int
	}{
		{"nobody has them yet", asLead, unattached, http.StatusOK},
		{"already on a team beneath", asLead, onStorage, http.StatusOK},
		{"runs a team beneath", asLead, subLead, http.StatusOK},
		{"already in the chart and on a team beside it", asLead, onBoth, http.StatusOK},
		{"the lead themselves", asLead, lead, http.StatusOK},
		{"their own boss, who sits on no team", asLead, boss, http.StatusForbidden},
		{"a peer, who sits on no team", asLead, peer, http.StatusForbidden},
		{"a peer's report", asLead, onSales, http.StatusForbidden},
		{"somebody in another organization", asLead, other.OwnerID, http.StatusNotFound},
		{"an administrator adding anybody", asAdmin, boss, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postTeamMember(t, tc.as, platform, tc.who)
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
			members, err := svc.ListTeamMembers(ctx, org.ID, platform)
			if err != nil {
				t.Fatalf("list members: %v", err)
			}
			added := false
			for _, m := range members {
				added = added || m.ID == tc.who
			}
			if added != (tc.want == http.StatusOK) {
				t.Errorf("on the team: %v, after a %d", added, tc.want)
			}
		})
	}
}

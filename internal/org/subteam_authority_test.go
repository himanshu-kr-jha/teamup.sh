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

// A team lead reshapes their own subtree and nothing else: they may open a new
// team underneath one they already run, and move a team they run to another
// place underneath one they run. Everything above and beside them stays an
// administrator's business.
//
// The interesting cases are all refusals, and they are what these tests are
// mostly made of. The tree is the same throughout:
//
//	Engineering              (nobody runs it)
//	├── Platform             (the lead runs it)
//	│   └── Storage
//	└── Sales                (nobody runs it)

type subteamOrg struct {
	org      *Organization
	lead     *User
	stranger *User

	engineering uuid.UUID
	platform    uuid.UUID
	storage     uuid.UUID
	sales       uuid.UUID

	// asAdmin and asLead are routers that call as an administrator and as the
	// lead respectively. The tree is built through asAdmin so that these tests
	// exercise the same path a real reorganisation takes.
	asAdmin *gin.Engine
	asLead  *gin.Engine
}

func postTeam(t *testing.T, r *gin.Engine, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/teams", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func deleteTeamHTTP(t *testing.T, r *gin.Engine, id uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/teams/"+id.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// createdTeamID reads the id back out of a 201, so a test can go on to use the
// team it just made.
func createdTeamID(t *testing.T, w *httptest.ResponseRecorder) uuid.UUID {
	t.Helper()
	if w.Code != http.StatusCreated {
		t.Fatalf("create team: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode created team: %v", err)
	}
	return got.ID
}

// parentOf reads a team's parent straight out of the listing, which is what
// every refusal test asserts on: a refused change must leave the tree alone.
func parentOf(t *testing.T, svc *Service, orgID, teamID uuid.UUID) *uuid.UUID {
	t.Helper()
	teams, err := svc.ListTeams(context.Background(), orgID)
	if err != nil {
		t.Fatalf("list teams: %v", err)
	}
	for _, tm := range teams {
		if tm.ID == teamID {
			return tm.ParentID
		}
	}
	t.Fatalf("team %s is not in the organization any more", teamID)
	return nil
}

func teamNamed(t *testing.T, svc *Service, orgID uuid.UUID, name string) (uuid.UUID, bool) {
	t.Helper()
	teams, err := svc.ListTeams(context.Background(), orgID)
	if err != nil {
		t.Fatalf("list teams: %v", err)
	}
	for _, tm := range teams {
		if tm.Name == name {
			return tm.ID, true
		}
	}
	return uuid.Nil, false
}

// seedSubteamOrg builds the tree above for one organization. Every test takes
// two of these, because authority is org-scoped and a rule that is only tested
// inside one tenant is not tested at all.
func seedSubteamOrg(t *testing.T, svc *Service, name, subdomain string) *subteamOrg {
	t.Helper()
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, name, subdomain, nil)
	if err != nil {
		t.Fatalf("create org %s: %v", name, err)
	}
	seedDefaultRoles(t, svc, org.ID)

	lead, _, err := svc.AddMember(ctx, org.ID, "Lead", "lead@"+subdomain+".test", "member")
	if err != nil {
		t.Fatalf("add lead: %v", err)
	}
	stranger, _, err := svc.AddMember(ctx, org.ID, "Stranger", "stranger@"+subdomain+".test", "member")
	if err != nil {
		t.Fatalf("add stranger: %v", err)
	}

	f := &subteamOrg{org: org, lead: lead, stranger: stranger}
	f.asAdmin = asUser(t, svc, org.ID.String(), uuid.New().String(), "admin")
	f.asLead = asUser(t, svc, org.ID.String(), lead.ID.String(), "member")

	f.engineering = createdTeamID(t, postTeam(t, f.asAdmin, map[string]any{"name": "Engineering"}))
	f.platform = createdTeamID(t, postTeam(t, f.asAdmin, map[string]any{
		"name": "Platform", "parentId": f.engineering.String(), "managerId": lead.ID.String(),
	}))
	f.storage = createdTeamID(t, postTeam(t, f.asAdmin, map[string]any{
		"name": "Storage", "parentId": f.platform.String(),
	}))
	f.sales = createdTeamID(t, postTeam(t, f.asAdmin, map[string]any{
		"name": "Sales", "parentId": f.engineering.String(),
	}))
	return f
}

// twoOrgs returns two identically shaped organizations. The second exists so
// that every refusal can also be checked across the tenant boundary, and so a
// rule that accidentally reads the whole teams table is caught.
func twoOrgs(t *testing.T, svc *Service) (*subteamOrg, *subteamOrg) {
	t.Helper()
	return seedSubteamOrg(t, svc, "Acme", "acme-subteams"),
		seedSubteamOrg(t, svc, "Globex", "globex-subteams")
}

// ── Creating ──────────────────────────────────────────────────────────────

func TestALeadCanOpenATeamUnderOneTheyRun(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	mine, theirs := twoOrgs(t, svc)

	w := postTeam(t, mine.asLead, map[string]any{
		"name": "Storage Tools", "parentId": mine.platform.String(),
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("a lead could not open a team under one they run: want 201, got %d: %s",
			w.Code, w.Body.String())
	}
	created := createdTeamID(t, w)

	if got := parentOf(t, svc, mine.org.ID, created); got == nil || *got != mine.platform {
		t.Errorf("the new team was not filed under Platform: %v", got)
	}

	// And they run what they just made — through the parent, without anybody
	// having to appoint them.
	manages, err := svc.ManagesTeam(ctx, mine.org.ID, created, mine.lead.ID)
	if err != nil {
		t.Fatalf("check management: %v", err)
	}
	if !manages {
		t.Error("a lead does not run the team they just opened underneath their own")
	}

	// The other organization is untouched, and its lead runs nothing here.
	if _, found := teamNamed(t, svc, theirs.org.ID, "Storage Tools"); found {
		t.Error("the team appeared in the other organization as well")
	}
	crossing, err := svc.ManagesTeam(ctx, mine.org.ID, created, theirs.lead.ID)
	if err != nil {
		t.Fatalf("check cross-tenant management: %v", err)
	}
	if crossing {
		t.Error("a lead of another organization was treated as running this team")
	}
}

func TestALeadMayRunTheTeamTheyOpenButMayNotHandItToSomebodyElse(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	// Naming themselves is the same authority they already have.
	if w := postTeam(t, mine.asLead, map[string]any{
		"name": "Storage Tools", "parentId": mine.platform.String(), "managerId": mine.lead.ID.String(),
	}); w.Code != http.StatusCreated {
		t.Fatalf("a lead could not name themselves: want 201, got %d: %s", w.Code, w.Body.String())
	}

	// Naming anybody else hands authority over the lead's own people to
	// somebody the lead has no standing to promote.
	w := postTeam(t, mine.asLead, map[string]any{
		"name": "Storage Ops", "parentId": mine.platform.String(), "managerId": mine.stranger.ID.String(),
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("a lead put somebody else in charge: want 403, got %d: %s", w.Code, w.Body.String())
	}
	if _, found := teamNamed(t, svc, mine.org.ID, "Storage Ops"); found {
		t.Error("the refused team was created anyway")
	}
}

func TestALeadCannotOpenATeamUnderOneTheyDoNotRun(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	for _, tc := range []struct {
		name   string
		parent uuid.UUID
	}{
		{"beside them", mine.sales},
		{"above them", mine.engineering},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := postTeam(t, mine.asLead, map[string]any{
				"name": "Trojan " + tc.name, "parentId": tc.parent.String(),
			})
			if w.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
			}
			if _, found := teamNamed(t, svc, mine.org.ID, "Trojan "+tc.name); found {
				t.Error("the refused team was created anyway")
			}
		})
	}
}

func TestALeadCannotOpenATeamAtTheTopLevel(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	// A root belongs to nobody's subtree, so opening one is not reshaping the
	// lead's own patch — it is adding a new branch to the organization.
	for _, body := range []map[string]any{
		{"name": "Skunkworks"},
		{"name": "Skunkworks", "parentId": ""},
		{"name": "Skunkworks", "parentId": nil},
	} {
		w := postTeam(t, mine.asLead, body)
		if w.Code != http.StatusForbidden {
			t.Fatalf("body %v: want 403, got %d: %s", body, w.Code, w.Body.String())
		}
	}
	if _, found := teamNamed(t, svc, mine.org.ID, "Skunkworks"); found {
		t.Error("a lead created a root team")
	}
}

func TestALeadCannotOpenATeamInAnotherOrganization(t *testing.T) {
	svc, _ := newService(t)
	mine, theirs := twoOrgs(t, svc)

	// Their Platform id, our session. The parent must be refused as belonging
	// to somebody else rather than resolved.
	w := postTeam(t, mine.asLead, map[string]any{
		"name": "Beachhead", "parentId": theirs.platform.String(),
	})
	if w.Code == http.StatusCreated {
		t.Fatalf("a lead planted a team in another organization: %s", w.Body.String())
	}
	if _, found := teamNamed(t, svc, theirs.org.ID, "Beachhead"); found {
		t.Error("the team landed in the other organization")
	}
	if _, found := teamNamed(t, svc, mine.org.ID, "Beachhead"); found {
		t.Error("the team was created locally with a foreign parent")
	}
}

// ── Moving ────────────────────────────────────────────────────────────────

func TestALeadCanMoveATeamWithinTheirOwnSubtree(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	// A second team under Platform, so there is somewhere inside the subtree
	// to move Storage to.
	tools := createdTeamID(t, postTeam(t, mine.asLead, map[string]any{
		"name": "Tools", "parentId": mine.platform.String(),
	}))

	w := patchTeam(t, mine.asLead, mine.storage, map[string]any{"parentId": tools.String()})
	if w.Code != http.StatusOK {
		t.Fatalf("a lead could not move a team inside their own subtree: want 200, got %d: %s",
			w.Code, w.Body.String())
	}
	if got := parentOf(t, svc, mine.org.ID, mine.storage); got == nil || *got != tools {
		t.Errorf("Storage was not moved under Tools: %v", got)
	}
}

func TestALeadCannotMoveATeamOutOfTheirOwnSubtree(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	// Moving Storage under Sales would put it out of the lead's reach for
	// good — they could not move it back — and hand their people to whoever
	// runs Sales. That is a reorganisation, not a reshaping of their own patch.
	for _, tc := range []struct {
		name      string
		newParent any
	}{
		{"under a team beside them", mine.sales.String()},
		{"under the team above them", mine.engineering.String()},
		{"up to the top level", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := patchTeam(t, mine.asLead, mine.storage, map[string]any{"parentId": tc.newParent})
			if w.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
			}
			if got := parentOf(t, svc, mine.org.ID, mine.storage); got == nil || *got != mine.platform {
				t.Errorf("the refused move was applied anyway; Storage now sits under %v", got)
			}
		})
	}
}

func TestALeadCannotMoveATeamTheyDoNotRun(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	// The mirror image of moving one out: pulling somebody else's team in.
	w := patchTeam(t, mine.asLead, mine.sales, map[string]any{"parentId": mine.platform.String()})
	if w.Code != http.StatusForbidden {
		t.Fatalf("a lead adopted a team they do not run: want 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := parentOf(t, svc, mine.org.ID, mine.sales); got == nil || *got != mine.engineering {
		t.Errorf("Sales was moved anyway; it now sits under %v", got)
	}
}

func TestALeadCannotMoveTheTopOfTheirOwnSubtree(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	// Platform is where the lead's authority starts. Every parent they are
	// entitled to choose is beneath Platform, so relocating Platform itself is
	// always somebody else's decision.
	w := patchTeam(t, mine.asLead, mine.platform, map[string]any{"parentId": mine.sales.String()})
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := parentOf(t, svc, mine.org.ID, mine.platform); got == nil || *got != mine.engineering {
		t.Errorf("Platform was moved anyway; it now sits under %v", got)
	}
}

func TestALeadCannotChangeWhoRunsAnExistingTeam(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	// Storage is inside the lead's subtree, so they may move and rename it —
	// but naming a manager displaces whoever holds the job, which is an
	// authority change rather than a reshaping.
	w := patchTeam(t, mine.asLead, mine.storage, map[string]any{"managerId": mine.lead.ID.String()})
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}

	teams, err := svc.ListTeams(context.Background(), mine.org.ID)
	if err != nil {
		t.Fatalf("list teams: %v", err)
	}
	for _, tm := range teams {
		if tm.ID == mine.storage && tm.ManagerID != nil {
			t.Errorf("Storage acquired a manager anyway: %v", *tm.ManagerID)
		}
	}
}

func TestALeadCanRenameATeamInsideTheirSubtreeButNotOutsideIt(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	if w := patchTeam(t, mine.asLead, mine.storage, map[string]any{"name": "Object Storage"}); w.Code != http.StatusOK {
		t.Fatalf("a lead could not rename a team they run: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, found := teamNamed(t, svc, mine.org.ID, "Object Storage"); !found {
		t.Error("the rename was reported as done but did not happen")
	}

	if w := patchTeam(t, mine.asLead, mine.sales, map[string]any{"name": "Revenue"}); w.Code != http.StatusForbidden {
		t.Fatalf("a lead renamed a team they do not run: want 403, got %d: %s", w.Code, w.Body.String())
	}
	if _, found := teamNamed(t, svc, mine.org.ID, "Revenue"); found {
		t.Error("the refused rename was applied anyway")
	}
}

func TestALeadCannotMoveATeamInAnotherOrganization(t *testing.T) {
	svc, _ := newService(t)
	mine, theirs := twoOrgs(t, svc)

	// Their Storage, our session, our Platform as the destination.
	w := patchTeam(t, mine.asLead, theirs.storage, map[string]any{"parentId": mine.platform.String()})
	if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Fatalf("want a refusal, got %d: %s", w.Code, w.Body.String())
	}
	if got := parentOf(t, svc, theirs.org.ID, theirs.storage); got == nil || *got != theirs.platform {
		t.Errorf("the other organization's tree was changed; their Storage now sits under %v", got)
	}

	// And the same in reverse: their lead cannot reach into ours.
	w = patchTeam(t, theirs.asLead, mine.storage, map[string]any{"parentId": theirs.platform.String()})
	if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Fatalf("reverse: want a refusal, got %d: %s", w.Code, w.Body.String())
	}
	if got := parentOf(t, svc, mine.org.ID, mine.storage); got == nil || *got != mine.platform {
		t.Errorf("our tree was changed by the other organization's lead; Storage sits under %v", got)
	}
}

// ── Deleting ──────────────────────────────────────────────────────────────

func TestALeadCannotDeleteATeam(t *testing.T) {
	svc, _ := newService(t)
	mine, _ := twoOrgs(t, svc)

	// Deleting grafts the children onto the deleted team's parent. Deleting
	// Platform would hand Storage to whoever runs Engineering, which is the
	// same giving-away that moving out refuses — so a lead does not delete.
	for _, target := range []uuid.UUID{mine.platform, mine.storage, mine.sales} {
		if w := deleteTeamHTTP(t, mine.asLead, target); w.Code != http.StatusForbidden {
			t.Fatalf("team %s: want 403, got %d: %s", target, w.Code, w.Body.String())
		}
	}

	teams, err := svc.ListTeams(context.Background(), mine.org.ID)
	if err != nil {
		t.Fatalf("list teams: %v", err)
	}
	if len(teams) != 4 {
		t.Errorf("a refused delete still removed something: %d teams left", len(teams))
	}
}

// ── Administrators ────────────────────────────────────────────────────────

func TestAnAdministratorStillReshapesTheWholeTree(t *testing.T) {
	svc, _ := newService(t)
	mine, theirs := twoOrgs(t, svc)

	// A root, which a lead may not open.
	root := createdTeamID(t, postTeam(t, mine.asAdmin, map[string]any{"name": "Facilities"}))
	if got := parentOf(t, svc, mine.org.ID, root); got != nil {
		t.Errorf("an administrator's root team was filed under %v", got)
	}

	// Somebody else in charge of a new team, which a lead may not do.
	appointed := createdTeamID(t, postTeam(t, mine.asAdmin, map[string]any{
		"name": "Reception", "parentId": root.String(), "managerId": mine.stranger.ID.String(),
	}))

	// A move across subtrees, which a lead may not make.
	if w := patchTeam(t, mine.asAdmin, mine.storage, map[string]any{"parentId": mine.sales.String()}); w.Code != http.StatusOK {
		t.Fatalf("admin move: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := parentOf(t, svc, mine.org.ID, mine.storage); got == nil || *got != mine.sales {
		t.Errorf("the administrator's move did not take effect: %v", got)
	}

	// A lift to the top level, and a change of manager on an existing team.
	if w := patchTeam(t, mine.asAdmin, mine.storage, map[string]any{"parentId": ""}); w.Code != http.StatusOK {
		t.Fatalf("admin detach: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := parentOf(t, svc, mine.org.ID, mine.storage); got != nil {
		t.Errorf("the administrator could not lift a team to the top level: %v", got)
	}
	if w := patchTeam(t, mine.asAdmin, mine.storage, map[string]any{"managerId": mine.stranger.ID.String()}); w.Code != http.StatusOK {
		t.Fatalf("admin set manager: want 200, got %d: %s", w.Code, w.Body.String())
	}

	// And a delete.
	if w := deleteTeamHTTP(t, mine.asAdmin, appointed); w.Code != http.StatusOK {
		t.Fatalf("admin delete: want 200, got %d: %s", w.Code, w.Body.String())
	}

	// None of which reached the other organization.
	if _, found := teamNamed(t, svc, theirs.org.ID, "Facilities"); found {
		t.Error("an administrator's new team appeared in the other organization")
	}
	if got := parentOf(t, svc, theirs.org.ID, theirs.storage); got == nil || *got != theirs.platform {
		t.Errorf("the other organization's tree moved: their Storage sits under %v", got)
	}
}

func TestAnAdministratorOfAnotherOrganizationReshapesNothingHere(t *testing.T) {
	svc, _ := newService(t)
	mine, theirs := twoOrgs(t, svc)

	// An administrator is only an administrator of their own tenant.
	if w := patchTeam(t, theirs.asAdmin, mine.storage, map[string]any{"parentId": theirs.platform.String()}); w.Code == http.StatusOK {
		t.Fatalf("an administrator moved another organization's team: %s", w.Body.String())
	}
	if got := parentOf(t, svc, mine.org.ID, mine.storage); got == nil || *got != mine.platform {
		t.Errorf("our Storage was moved by the other organization's administrator: %v", got)
	}
	if w := deleteTeamHTTP(t, theirs.asAdmin, mine.sales); w.Code == http.StatusOK {
		t.Fatalf("an administrator deleted another organization's team: %s", w.Body.String())
	}
	if _, found := teamNamed(t, svc, mine.org.ID, "Sales"); !found {
		t.Error("our Sales team was deleted by the other organization's administrator")
	}
}

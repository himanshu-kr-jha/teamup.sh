package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
)

// ── Shared fixture for the dashboard, scrum and contribution tests ────────

// dashChart is one organisation with two sides:
//
//	Backend   managed by mgr:  alice, ann (never has a task), quinn (org admin),
//	                           dee (deactivated); sub team Platform holds pat
//	Sales     managed by mgr2: sam
//
// The owner of the organisation is an org admin who runs no team.
type dashChart struct {
	svc  *Service
	pool *pgxpool.Pool
	org  *testsupport.Org

	mgr, mgr2                        uuid.UUID
	alice, ann, pat, quinn, dee, sam uuid.UUID
	backend, platform, sales         uuid.UUID
}

func seedDash(t *testing.T) *dashChart {
	t.Helper()
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)
	c := &dashChart{svc: svc, pool: pool, org: org}

	user := func(email, role, name string) uuid.UUID {
		id := testsupport.SeedUser(t, pool, org, email+"@example.test", role)
		if _, err := pool.Exec(context.Background(), `UPDATE users SET name = $1 WHERE id = $2`, name, id); err != nil {
			t.Fatalf("name %s: %v", email, err)
		}
		return id
	}
	c.mgr = user("mgr", "manager", "Mia Manager")
	c.mgr2 = user("mgr2", "manager", "Max Manager")
	c.alice = user("alice", "member", "Alice Archer")
	c.ann = user("ann", "member", "Ann Idle")
	c.pat = user("pat", "member", "Pat Platform")
	c.quinn = user("quinn", "admin", "Quinn Admin")
	c.dee = user("dee", "member", "Dee Deactivated")
	c.sam = user("sam", "member", "Sam Sales")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET is_active = false WHERE id = $1`, c.dee); err != nil {
		t.Fatalf("deactivate dee: %v", err)
	}

	c.backend = testsupport.SeedTeam(t, pool, org, "Backend", &c.mgr, nil, c.alice, c.ann, c.quinn, c.dee)
	c.platform = testsupport.SeedTeam(t, pool, org, "Platform", nil, &c.backend, c.pat)
	c.sales = testsupport.SeedTeam(t, pool, org, "Sales", &c.mgr2, nil, c.sam)
	return c
}

func (c *dashChart) asOwner(t *testing.T) *gin.Engine { return asOwner(t, c.svc, c.org) }
func (c *dashChart) asManager(t *testing.T, id uuid.UUID) *gin.Engine {
	return asUser(t, c.svc, c.org.ID, id, "manager")
}
func (c *dashChart) asMember(t *testing.T, id uuid.UUID) *gin.Engine {
	return asUser(t, c.svc, c.org.ID, id, "member")
}

// joinTeam puts an extra user on an existing team.
func (c *dashChart) joinTeam(t *testing.T, team, user uuid.UUID) {
	t.Helper()
	if _, err := c.pool.Exec(context.Background(),
		`INSERT INTO team_members (team_id, user_id, joined_at) VALUES ($1, $2, NOW()) ON CONFLICT DO NOTHING`,
		team, user); err != nil {
		t.Fatalf("join team: %v", err)
	}
}

// newMember seeds one more member of the organisation with a readable name.
func (c *dashChart) newMember(t *testing.T, email, name string) uuid.UUID {
	t.Helper()
	id := testsupport.SeedUser(t, c.pool, c.org, email+"@example.test", "member")
	if _, err := c.pool.Exec(context.Background(), `UPDATE users SET name = $1 WHERE id = $2`, name, id); err != nil {
		t.Fatalf("name %s: %v", email, err)
	}
	return id
}

// taskSpec describes a task row written straight to the table, so a test can
// choose dates without waiting for the clock.
type taskSpec struct {
	title       string
	reporter    uuid.UUID
	assignee    *uuid.UUID
	status      string
	due         *time.Time
	completedAt *time.Time
	deleted     bool
}

func (c *dashChart) insertTask(t *testing.T, s taskSpec) uuid.UUID {
	t.Helper()
	if s.status == "" {
		s.status = "todo"
	}
	if s.reporter == uuid.Nil {
		s.reporter = c.org.OwnerID
	}
	var deletedAt *time.Time
	if s.deleted {
		now := time.Now()
		deletedAt = &now
	}
	id := uuid.New()
	if _, err := c.pool.Exec(context.Background(), `
		INSERT INTO tasks (id, org_id, reporter_id, assignee_id, title, status, due_date, completed_at, deleted_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
	`, id, c.org.ID, s.reporter, s.assignee, s.title, s.status, s.due, s.completedAt, deletedAt); err != nil {
		t.Fatalf("insert task %q: %v", s.title, err)
	}
	return id
}

// work gives a person one task in the given status, held by them.
func (c *dashChart) work(t *testing.T, who uuid.UUID, title, status string) uuid.UUID {
	t.Helper()
	return c.insertTask(t, taskSpec{title: title, reporter: who, assignee: &who, status: status})
}

func ago(d time.Duration) *time.Time   { v := time.Now().Add(-d); return &v }
func ahead(d time.Duration) *time.Time { v := time.Now().Add(d); return &v }

// ── HTTP helpers ──────────────────────────────────────────────────────────

func send(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func mustStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("want %d, got %d: %s", want, w.Code, w.Body.String())
	}
}

// unwrap lets a response be either {"key": {...}} or the bare object.
func unwrap(body []byte, key string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) == nil {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return body
}

// ref reads a task reference that is either an id string or an object with an id.
type ref string

func (r *ref) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*r = ref(s)
		return nil
	}
	var o struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*r = ref(o.ID)
	return nil
}

func refStrings(rs []ref) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, string(r))
	}
	return out
}

func contains(list []string, want uuid.UUID) bool {
	for _, s := range list {
		if s == want.String() {
			return true
		}
	}
	return false
}

// ── Dashboard response ────────────────────────────────────────────────────

type dashCounts struct {
	Pending   int `json:"pending"`
	Ongoing   int `json:"ongoing"`
	Blocked   int `json:"blocked"`
	Overdue   int `json:"overdue"`
	Completed int `json:"completed"`
}

type dashEntry struct {
	UserID            string           `json:"userId"`
	Name              string           `json:"name"`
	Counts            dashCounts       `json:"counts"`
	Tasks             map[string][]ref `json:"tasks"`
	NewSinceLastScrum []ref            `json:"newSinceLastScrum"`
}

type dashboardBody struct {
	Scope string `json:"scope"`
	Teams []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"teams"`
	Totals      dashCounts  `json:"totals"`
	People      []dashEntry `json:"people"`
	LastScrumAt *time.Time  `json:"lastScrumAt"`
}

func (d dashboardBody) person(id uuid.UUID) (dashEntry, bool) {
	for _, p := range d.People {
		if p.UserID == id.String() {
			return p, true
		}
	}
	return dashEntry{}, false
}

func (d dashboardBody) ids() []string {
	out := []string{}
	for _, p := range d.People {
		out = append(out, p.UserID)
	}
	return out
}

func (e dashEntry) bucket(name string) []string { return refStrings(e.Tasks[name]) }

func getDashboard(t *testing.T, r *gin.Engine, query string) dashboardBody {
	t.Helper()
	w := getJSON(t, r, "/api/tasks/dashboard"+query)
	mustStatus(t, w, http.StatusOK)
	var d dashboardBody
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode dashboard %q: %v", w.Body.String(), err)
	}
	return d
}

// ── Dashboard tests (ticket 02) ───────────────────────────────────────────

func TestEmployeeDashboardIsSelfOnlyEvenWhenAskingForAnotherTeam(t *testing.T) {
	c := seedDash(t)
	mine := c.work(t, c.alice, "Alice's work", "todo")
	annsNeighbour := c.work(t, c.ann, "Ann's work", "todo")
	sams := c.work(t, c.sam, "Sam's work", "in_progress")

	r := c.asMember(t, c.alice)
	for _, q := range []string{"", "?teamId=" + c.sales.String(), "?teamId=" + c.backend.String()} {
		d := getDashboard(t, r, q)
		if d.Scope != "self" {
			t.Errorf("%q: employee scope should be self, got %q", q, d.Scope)
		}
		if len(d.People) != 1 || d.People[0].UserID != c.alice.String() {
			t.Fatalf("%q: employee should see only themself, got %v", q, d.ids())
		}
		got := d.People[0].bucket("pending")
		if !contains(got, mine) || contains(got, annsNeighbour) || contains(got, sams) {
			t.Errorf("%q: pending bucket should hold only alice's task, got %v", q, got)
		}
		for name, refs := range d.People[0].Tasks {
			for _, id := range refStrings(refs) {
				if id == annsNeighbour.String() || id == sams.String() {
					t.Errorf("%q: somebody else's task leaked into bucket %s", q, name)
				}
			}
		}
	}
}

func TestManagerDashboardCoversManagedAndNestedTeamsOnly(t *testing.T) {
	c := seedDash(t)
	c.work(t, c.alice, "Alice's work", "todo")
	c.work(t, c.pat, "Pat's work", "in_progress")
	c.work(t, c.sam, "Sam's work", "todo")
	c.work(t, c.dee, "Dee's old work", "todo")
	c.work(t, c.quinn, "Quinn's work", "todo")

	d := getDashboard(t, c.asManager(t, c.mgr), "")
	if d.Scope != "team" {
		t.Fatalf("manager scope should be team, got %q", d.Scope)
	}
	for _, who := range []struct {
		name string
		id   uuid.UUID
	}{{"alice", c.alice}, {"pat, on the nested team", c.pat}, {"ann, who has no tasks", c.ann}} {
		if _, ok := d.person(who.id); !ok {
			t.Errorf("%s should be listed, got %v", who.name, d.ids())
		}
	}
	for _, who := range []struct {
		name string
		id   uuid.UUID
	}{
		{"sam, on a team the manager does not run", c.sam},
		{"dee, who is deactivated", c.dee},
		{"quinn, an org admin sitting on the team", c.quinn},
	} {
		if _, ok := d.person(who.id); ok {
			t.Errorf("%s must not be listed", who.name)
		}
	}

	ann, _ := d.person(c.ann)
	if ann.Counts != (dashCounts{}) {
		t.Errorf("a person with no tasks should have all-zero counts, got %+v", ann.Counts)
	}

	listed := map[string]bool{}
	for _, tm := range d.Teams {
		listed[tm.ID] = true
	}
	if !listed[c.backend.String()] || listed[c.sales.String()] {
		t.Errorf("teams should offer Backend and not Sales, got %+v", d.Teams)
	}
}

func TestAdminWhoRunsNoTeamSeesTheWholeOrganization(t *testing.T) {
	c := seedDash(t)
	c.work(t, c.alice, "Alice's work", "todo")
	c.work(t, c.sam, "Sam's work", "todo")

	d := getDashboard(t, c.asOwner(t), "")
	if d.Scope != "org" {
		t.Fatalf("admin scope should be org, got %q", d.Scope)
	}
	for name, id := range map[string]uuid.UUID{
		"alice": c.alice, "ann": c.ann, "pat": c.pat, "sam": c.sam, "quinn": c.quinn, "mgr": c.mgr, "mgr2": c.mgr2,
	} {
		if _, ok := d.person(id); !ok {
			t.Errorf("%s should be listed for the organization view, got %v", name, d.ids())
		}
	}
	if _, ok := d.person(c.dee); ok {
		t.Error("a deactivated user must not be listed")
	}

	// Narrowing by team still works for an admin.
	narrow := getDashboard(t, c.asOwner(t), "?teamId="+c.sales.String())
	if _, ok := narrow.person(c.sam); !ok {
		t.Errorf("team filter should keep sam, got %v", narrow.ids())
	}
	if _, ok := narrow.person(c.alice); ok {
		t.Error("team filter Sales should drop alice")
	}
}

func TestDashboardBucketsAndCountsFollowTheVocabulary(t *testing.T) {
	c := seedDash(t)
	a := c.alice
	pendingFuture := c.insertTask(t, taskSpec{title: "pending", reporter: a, assignee: &a, status: "todo", due: ahead(48 * time.Hour)})
	pendingLate := c.insertTask(t, taskSpec{title: "pending late", reporter: a, assignee: &a, status: "todo", due: ago(48 * time.Hour)})
	doing := c.insertTask(t, taskSpec{title: "doing", reporter: a, assignee: &a, status: "in_progress"})
	review := c.insertTask(t, taskSpec{title: "review", reporter: a, assignee: &a, status: "in_review"})
	blocked := c.insertTask(t, taskSpec{title: "blocked", reporter: a, assignee: &a, status: "blocked"})
	doneLate := c.insertTask(t, taskSpec{title: "done after its due date", reporter: a, assignee: &a, status: "done",
		due: ago(72 * time.Hour), completedAt: ago(2 * time.Hour)})
	deleted := c.insertTask(t, taskSpec{title: "gone", reporter: a, assignee: &a, status: "todo", deleted: true})

	d := getDashboard(t, c.asManager(t, c.mgr), "")
	p, ok := d.person(a)
	if !ok {
		t.Fatalf("alice missing: %v", d.ids())
	}
	want := dashCounts{Pending: 2, Ongoing: 2, Blocked: 1, Overdue: 1, Completed: 1}
	if p.Counts != want {
		t.Errorf("counts: want %+v, got %+v", want, p.Counts)
	}
	for bucket, ids := range map[string][]uuid.UUID{
		"pending":   {pendingFuture, pendingLate},
		"ongoing":   {doing, review},
		"blocked":   {blocked},
		"overdue":   {pendingLate},
		"completed": {doneLate},
	} {
		got := p.bucket(bucket)
		if len(got) != len(ids) {
			t.Errorf("bucket %s: want %d tasks, got %v", bucket, len(ids), got)
		}
		for _, id := range ids {
			if !contains(got, id) {
				t.Errorf("bucket %s should hold task %s, got %v", bucket, id, got)
			}
		}
	}
	for name := range p.Tasks {
		if contains(p.bucket(name), deleted) {
			t.Errorf("a soft-deleted task showed up in bucket %s", name)
		}
	}
	if contains(p.bucket("overdue"), doneLate) {
		t.Error("finished work is never overdue")
	}
	if d.Totals.Pending != 2 || d.Totals.Blocked != 1 || d.Totals.Overdue != 1 {
		t.Errorf("totals should add up the people shown, got %+v", d.Totals)
	}
}

func TestANewPendingTaskAppearsOnTheNextRead(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)

	before := getDashboard(t, r, "")
	if p, _ := before.person(c.alice); p.Counts.Pending != 0 {
		t.Fatalf("expected alice to start with nothing pending, got %+v", p.Counts)
	}

	w := send(t, r, http.MethodPost, "/api/tasks",
		`{"title":"Fresh work","assigneeId":"`+c.alice.String()+`"}`)
	mustStatus(t, w, http.StatusCreated)
	var created Task
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}

	after := getDashboard(t, r, "")
	p, ok := after.person(c.alice)
	if !ok {
		t.Fatalf("alice missing: %v", after.ids())
	}
	if p.Counts.Pending != 1 || !contains(p.bucket("pending"), created.ID) {
		t.Errorf("new task should be pending straight away, got %+v %v", p.Counts, p.bucket("pending"))
	}
	if !contains(refStrings(p.NewSinceLastScrum), created.ID) {
		t.Errorf("new task should be listed as new since the last scrum, got %v", p.NewSinceLastScrum)
	}
}

func TestManagerCannotAskForATeamTheyDoNotManage(t *testing.T) {
	c := seedDash(t)
	sams := c.work(t, c.sam, "Sam's secret work", "todo")

	w := getJSON(t, c.asManager(t, c.mgr), "/api/tasks/dashboard?teamId="+c.sales.String())
	if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Fatalf("want 403 or 404, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), sams.String()) || strings.Contains(w.Body.String(), c.sam.String()) {
		t.Errorf("a refusal must not carry data: %s", w.Body.String())
	}
}

func TestDashboardDoneWorkIsSinceTheLastSavedScrum(t *testing.T) {
	c := seedDash(t)
	a := c.alice
	r := c.asManager(t, c.mgr)

	recent := c.insertTask(t, taskSpec{title: "finished yesterday", reporter: a, assignee: &a, status: "done", completedAt: ago(24 * time.Hour)})
	stale := c.insertTask(t, taskSpec{title: "finished last month", reporter: a, assignee: &a, status: "done", completedAt: ago(30 * 24 * time.Hour)})

	// No scrum yet: fall back to seven days.
	before := getDashboard(t, r, "")
	p, _ := before.person(a)
	if !contains(p.bucket("completed"), recent) || contains(p.bucket("completed"), stale) {
		t.Fatalf("without a scrum, done should mean the last 7 days, got %v", p.bucket("completed"))
	}
	if before.LastScrumAt != nil {
		t.Errorf("no scrum saved yet, lastScrumAt should be null, got %v", before.LastScrumAt)
	}

	mustStatus(t, send(t, r, http.MethodPost, "/api/scrums", `{}`), http.StatusCreated)

	// A task finished after the scrum, through the normal route.
	open := c.work(t, a, "finished after the scrum", "in_progress")
	mustStatus(t, patchTask(t, r, open.String(), `{"status":"done"}`), http.StatusOK)

	after := getDashboard(t, r, "")
	p, _ = after.person(a)
	done := p.bucket("completed")
	if contains(done, recent) {
		t.Errorf("work finished before the saved scrum should no longer be listed as done: %v", done)
	}
	if !contains(done, open) {
		t.Errorf("work finished after the saved scrum should be listed as done: %v", done)
	}
	if after.LastScrumAt == nil {
		t.Error("lastScrumAt should be set once a scrum is saved")
	}
}

func TestAnotherOrganizationCannotReachAnyOfThis(t *testing.T) {
	c := seedDash(t)
	alices := c.work(t, c.alice, "Alice's work", "todo")
	elsewhere := testsupport.SeedOrg(t, c.pool)
	r := asUser(t, c.svc, elsewhere.ID, elsewhere.OwnerID, "owner")

	d := getDashboard(t, r, "")
	if _, ok := d.person(c.alice); ok || len(d.People) != 1 || d.People[0].UserID != elsewhere.OwnerID.String() {
		t.Errorf("another organization should see only its own people, got %v", d.ids())
	}
	for _, path := range []string{
		"/api/tasks/dashboard?teamId=" + c.backend.String(),
		"/api/tasks/contributions?teamId=" + c.backend.String(),
		"/api/tasks/contributions/" + c.alice.String(),
	} {
		w := getJSON(t, r, path)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: want 404, got %d: %s", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), alices.String()) {
			t.Errorf("%s: leaked a task: %s", path, w.Body.String())
		}
	}
	mustStatus(t, send(t, r, http.MethodPost, "/api/scrums", `{"teamId":"`+c.backend.String()+`"}`), http.StatusNotFound)
}

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

	"github.com/teamup/teamup/internal/testsupport"
)

// asOwner builds a router whose only middleware stands in for the auth
// middleware, so a test can say who is calling without minting a real token.
func asOwner(t *testing.T, svc *Service, org *testsupport.Org) *gin.Engine {
	t.Helper()
	return asUser(t, svc, org.ID, org.OwnerID, "owner")
}

// asUser is the same router for somebody who is not the owner, so a test can
// call as a second member of the organization — or as a member of another one.
func asUser(t *testing.T, svc *Service, orgID, userID uuid.UUID, roles ...string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", orgID.String())
		c.Set("user_id", userID.String())
		c.Set("user_roles", roles)
		c.Next()
	})
	RegisterRoutes(api, svc)
	return r
}

func deleteTask(t *testing.T, r *gin.Engine, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/tasks/"+id, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func patchTask(t *testing.T, r *gin.Engine, id string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/tasks/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A value the column cannot hold used to reach the driver and come back as a
// 500 with a constraint message. Every one of these is the caller's mistake,
// so every one of them is a 400 that names the field.
func TestUpdateTaskRejectsValuesTheColumnCannotHold(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Original")
	r := asOwner(t, svc, org)

	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"date that is not a date", `{"dueDate":"not-a-date"}`, "dueDate"},
		{"start date that is not a date", `{"startDate":"not-a-date"}`, "startDate"},
		{"number where a title belongs", `{"title":7}`, "title"},
		{"title removed from a not-null column", `{"title":null}`, "title"},
		{"blank title", `{"title":"   "}`, "title"},
		{"title longer than the column", `{"title":"` + strings.Repeat("x", 501) + `"}`, "title"},
		{"number where a description belongs", `{"description":7}`, "description"},
		{"story points that are not a number", `{"storyPoints":"many"}`, "storyPoints"},
		{"fractional story points", `{"storyPoints":1.5}`, "storyPoints"},
		{"position that is not a number", `{"position":"first"}`, "position"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := patchTask(t, r, task.ID.String(), tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
			}
			var got struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode body %q: %v", w.Body.String(), err)
			}
			if !strings.Contains(got.Error, tc.field) {
				t.Errorf("error should name the field %q, got %q", tc.field, got.Error)
			}
			// A refused update must leave the row alone.
			after, err := svc.GetTask(context.Background(), org.ID, task.ID)
			if err != nil {
				t.Fatalf("reread task: %v", err)
			}
			if after.Title != "Original" {
				t.Errorf("refused update still changed the task: %+v", after)
			}
		})
	}
}

// The other half of the same fix: values the column can hold must still go
// through, including the nulls that clear a nullable column.
func TestUpdateTaskAcceptsWellFormedValues(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Original")
	r := asOwner(t, svc, org)

	due := "2030-01-02T03:04:05Z"
	w := patchTask(t, r, task.ID.String(),
		`{"title":"Renamed","description":"why","dueDate":"`+due+`","startDate":"`+due+`","storyPoints":3,"position":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	after, err := svc.GetTask(ctx, org.ID, task.ID)
	if err != nil {
		t.Fatalf("reread task: %v", err)
	}
	if after.Title != "Renamed" || after.Description != "why" {
		t.Errorf("title/description not applied: %+v", after)
	}
	if after.StoryPoints == nil || *after.StoryPoints != 3 {
		t.Errorf("storyPoints not applied: %+v", after.StoryPoints)
	}
	if after.Position != 2 {
		t.Errorf("position not applied: %d", after.Position)
	}
	want, _ := time.Parse(time.RFC3339, due)
	if after.DueDate == nil || !after.DueDate.Equal(want) {
		t.Errorf("dueDate not applied: %v", after.DueDate)
	}
	if after.StartDate == nil || !after.StartDate.Equal(want) {
		t.Errorf("startDate not applied: %v", after.StartDate)
	}

	if w := patchTask(t, r, task.ID.String(),
		`{"dueDate":null,"startDate":null,"storyPoints":null,"description":null}`); w.Code != http.StatusOK {
		t.Fatalf("clearing nullable fields: want 200, got %d: %s", w.Code, w.Body.String())
	}
	cleared, err := svc.GetTask(ctx, org.ID, task.ID)
	if err != nil {
		t.Fatalf("reread task: %v", err)
	}
	if cleared.DueDate != nil || cleared.StartDate != nil || cleared.StoryPoints != nil {
		t.Errorf("null should clear a nullable column: %+v", cleared)
	}
	if cleared.Description != "" {
		t.Errorf("null description should clear, got %q", cleared.Description)
	}
}

// Validation runs before the write, so a body that is part valid and part
// nonsense changes nothing at all.
func TestUpdateTaskRejectsTheWholeBodyWhenOneFieldIsBad(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Original")
	r := asOwner(t, svc, org)

	w := patchTask(t, r, task.ID.String(), `{"title":"Renamed","dueDate":"not-a-date"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	after, err := svc.GetTask(context.Background(), org.ID, task.ID)
	if err != nil {
		t.Fatalf("reread task: %v", err)
	}
	if after.Title != "Original" {
		t.Errorf("the valid half of a rejected body was applied: %q", after.Title)
	}
}

// Deleting used to need nothing but membership of the organization, so any
// member could make anybody else's task disappear from everybody's board.
// Authority over the person who filed it is what is required, and the caller
// who lacks it must be told 403 — while an id belonging to another tenant
// stays a 404, so a refusal never confirms that the task exists.
func TestDeletingATaskNeedsAuthorityOverTheReporter(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	reporter := testsupport.SeedUser(t, pool, org, "reporter@example.test", "member")
	bystander := testsupport.SeedUser(t, pool, org, "bystander@example.test", "member")
	manager := testsupport.SeedUser(t, pool, org, "manager@example.test", "manager")
	admin := testsupport.SeedUser(t, pool, org, "admin@example.test", "admin")
	testsupport.SeedTeam(t, pool, org, "Support", &manager, nil, reporter)

	elsewhere := testsupport.SeedOrg(t, pool)

	cases := []struct {
		name   string
		orgID  uuid.UUID
		userID uuid.UUID
		roles  []string
		want   int
	}{
		{"the person who filed it", org.ID, reporter, []string{"member"}, http.StatusOK},
		{"another member of the same organization", org.ID, bystander, []string{"member"}, http.StatusForbidden},
		{"the manager of the team they are on", org.ID, manager, []string{"manager"}, http.StatusOK},
		{"an administrator of the organization", org.ID, admin, []string{"admin"}, http.StatusOK},
		{"the owner of the organization", org.ID, org.OwnerID, []string{"owner"}, http.StatusOK},
		{"a member of another organization", elsewhere.ID, elsewhere.OwnerID, []string{"owner"}, http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := createTaskAs(t, svc, org, reporter, "Somebody else's work")
			r := asUser(t, svc, tc.orgID, tc.userID, tc.roles...)

			w := deleteTask(t, r, task.ID.String())
			if w.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}

			_, err := svc.GetTask(ctx, org.ID, task.ID)
			if tc.want == http.StatusOK && err == nil {
				t.Error("an allowed delete should take the task off the board")
			}
			if tc.want != http.StatusOK && err != nil {
				t.Errorf("a refused delete must leave the task alone: %v", err)
			}
		})
	}
}

// The 404 has to be the same 404 an invented id gets, or the status alone
// tells a caller in another organization that the task is real.
func TestADeleteFromAnotherOrganizationLooksLikeAnInventedID(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)
	task := createTask(t, svc, org, "Theirs")

	elsewhere := testsupport.SeedOrg(t, pool)
	r := asUser(t, svc, elsewhere.ID, elsewhere.OwnerID, "owner")

	real := deleteTask(t, r, task.ID.String())
	invented := deleteTask(t, r, uuid.NewString())

	if real.Code != http.StatusNotFound {
		t.Fatalf("another organization's task: want 404, got %d: %s", real.Code, real.Body.String())
	}
	if real.Code != invented.Code || real.Body.String() != invented.Body.String() {
		t.Errorf("a real task in another organization answered %d %s, an invented id answered %d %s",
			real.Code, real.Body.String(), invented.Code, invented.Body.String())
	}
	if _, err := svc.GetTask(context.Background(), org.ID, task.ID); err != nil {
		t.Errorf("the task was deleted from across the tenant boundary: %v", err)
	}
}

func getJSON(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// abilities mirrors the "can" object the API promises, so the test reads what
// a client would read rather than the struct the server happens to build.
type abilities struct {
	Edit     bool   `json:"edit"`
	Reassign bool   `json:"reassign"`
	Delete   bool   `json:"delete"`
	Reason   string `json:"reason"`
}

// Issue #47 end to end. The list showed a manager work they could not touch and
// said nothing about it, the delete answered 403 and the assignee list left the
// person out entirely — three surfaces, three answers. The board now tells the
// client what it may do, and this pins that answer to what the delete does.
func TestTheListAndTheDeleteGiveTheSameAnswer(t *testing.T) {
	svc, pool := newService(t)
	c := seedBoardChart(t, pool)
	quinn, bob, alice := c.people[1], c.people[2], c.people[3]

	quinns := createTaskAs(t, svc, c.org, quinn.id, "Filed by an organisation admin")
	alices := createTaskAs(t, svc, c.org, alice.id, "Filed by an engineer bob manages")

	r := asUser(t, svc, c.org.ID, bob.id, "manager")

	w := getJSON(t, r, "/api/tasks")
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var listed struct {
		Tasks []struct {
			ID  string     `json:"id"`
			Can *abilities `json:"can"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode board %q: %v", w.Body.String(), err)
	}
	if len(listed.Tasks) != 2 {
		t.Fatalf("expected both tasks on the board, got %s", w.Body.String())
	}

	promised := map[string]bool{}
	for _, task := range listed.Tasks {
		if task.Can == nil {
			t.Fatalf("the board did not tell the client what it may do: %s", w.Body.String())
		}
		promised[task.ID] = task.Can.Delete
		if !task.Can.Delete && task.Can.Reason == "" {
			t.Errorf("task %s came back read-only with no reason a person could read", task.ID)
		}
	}
	if promised[quinns.ID.String()] {
		t.Error("the board offered bob the delete of an organisation admin's task")
	}
	if !promised[alices.ID.String()] {
		t.Error("the board refused bob his own engineer's task")
	}

	// What the board promised is what the endpoint does.
	for _, task := range []*Task{quinns, alices} {
		want := http.StatusForbidden
		if promised[task.ID.String()] {
			want = http.StatusOK
		}
		if got := deleteTask(t, r, task.ID.String()); got.Code != want {
			t.Errorf("%q: board promised delete=%v so DELETE should be %d, got %d: %s",
				task.Title, promised[task.ID.String()], want, got.Code, got.Body.String())
		}
	}

	// And the detail page, which is where the Delete button actually lives, is
	// told the same thing.
	detail := getJSON(t, r, "/api/tasks/"+quinns.ID.String())
	if detail.Code != http.StatusOK {
		t.Fatalf("detail: want 200, got %d: %s", detail.Code, detail.Body.String())
	}
	var one struct {
		Can *abilities `json:"can"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &one); err != nil {
		t.Fatalf("decode detail %q: %v", detail.Body.String(), err)
	}
	if one.Can == nil || one.Can.Delete {
		t.Errorf("the detail page would have drawn a Delete button that 403s: %+v", one.Can)
	}
	if !strings.Contains(one.Can.Reason, "organisation admin") {
		t.Errorf("the reason should name the standing that blocks it, got %q", one.Can.Reason)
	}

	// Nothing of this crosses the tenant boundary: another organization's
	// caller cannot read the task, let alone what they may do to it.
	elsewhere := testsupport.SeedOrg(t, pool)
	foreign := getJSON(t, asUser(t, svc, elsewhere.ID, elsewhere.OwnerID, "owner"),
		"/api/tasks/"+quinns.ID.String())
	if foreign.Code != http.StatusNotFound {
		t.Errorf("another organization read the task: %d %s", foreign.Code, foreign.Body.String())
	}
}

// "No filter" and "a filter I could not read" used to be the same thing, so a
// typo got back a plausible and much wider answer than was asked for. An org
// administrator running no team got the whole organization from scope=team&teamId=all.
func TestAMalformedListFilterIsRefusedRatherThanIgnored(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)
	createTask(t, svc, org, "a real task")
	other := testsupport.SeedOrg(t, pool)
	createTask(t, svc, other, "somebody else's task")
	r := asOwner(t, svc, org)

	list := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/tasks"+query, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	for _, query := range []string{
		"?assigneeId=not-a-uuid",
		"?projectId=junk",
		"?labelId=junk",
		"?scope=team&teamId=all",
		"?teamId=all",
		"?scope=bogus",
		"?scope=all",
	} {
		t.Run(query, func(t *testing.T) {
			w := list(query)
			if w.Code != http.StatusBadRequest {
				t.Errorf("want 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	// What the web client actually sends still works, and stays in its own
	// organization.
	for _, query := range []string{"", "?scope=mine", "?scope=team", "?assigneeId=" + org.OwnerID.String()} {
		t.Run("still fine "+query, func(t *testing.T) {
			w := list(query)
			if w.Code != http.StatusOK {
				t.Errorf("want 200, got %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "somebody else's task") {
				t.Errorf("another organization's task came back: %s", w.Body.String())
			}
		})
	}
}

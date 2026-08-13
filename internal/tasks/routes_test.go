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

	"github.com/teamup/teamup/internal/testsupport"
)

// asOwner builds a router whose only middleware stands in for the auth
// middleware, so a test can say who is calling without minting a real token.
func asOwner(t *testing.T, svc *Service, org *testsupport.Org) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", org.ID.String())
		c.Set("user_id", org.OwnerID.String())
		c.Set("user_roles", []string{"owner"})
		c.Next()
	})
	RegisterRoutes(api, svc)
	return r
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

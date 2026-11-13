package org

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// asHolder is somebody signed in with exactly these permissions, as the auth
// middleware works them out from their roles.
func asHolder(svc *Service, orgID, userID uuid.UUID, perms ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("org_id", orgID.String())
		c.Set("user_id", userID.String())
		roles := []string{}
		for _, p := range perms {
			if p == "*" {
				roles = append(roles, "owner")
			}
		}
		c.Set("user_roles", roles)
		c.Set("user_permissions", perms)
		c.Next()
	})
	RegisterRoutes(api, svc)
	return r
}

func call(r *gin.Engine, method, path, body string) int {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// Managing members is not a way to act on somebody who can do more than you:
// deactivating the owner, or resetting an administrator's password and signing
// in as them, would hand over everything they hold.
func TestManagingMembersStopsAtWhoeverOutranksYou(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, "Rank Co", "rank-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)
	owner := addResetPerson(t, svc, pool, org.ID, "owner@rank.test", "owner")
	admin := addResetPerson(t, svc, pool, org.ID, "admin@rank.test", "admin")
	member := addResetPerson(t, svc, pool, org.ID, "member@rank.test", "member")
	hr := addResetPerson(t, svc, pool, org.ID, "hr@rank.test", "member")
	adminPerms, err := svc.heldBy(ctx, admin.id)
	if err != nil {
		t.Fatalf("admin permissions: %v", err)
	}

	type actor struct {
		name  string
		id    uuid.UUID
		perms []string
	}
	asAdmin := actor{"an admin", admin.id, adminPerms}
	asHR := actor{"somebody who may only manage members", hr.id, []string{"members.manage"}}

	refused := []struct {
		who    actor
		target resetPerson
	}{
		{asAdmin, owner},
		{asHR, owner},
		{asHR, admin},
	}
	for _, tc := range refused {
		path := "/api/users/" + tc.target.id.String()
		r := asHolder(svc, org.ID, tc.who.id, tc.who.perms...)
		t.Run(tc.who.name+" cannot reset", func(t *testing.T) {
			if code := call(r, http.MethodPost, path+"/reset-password", ""); code != http.StatusForbidden {
				t.Errorf("reset: got %d, want 403", code)
			}
			if !passwordWorks(t, pool, tc.target.id, tc.target.password) {
				t.Error("the password changed anyway")
			}
		})
		t.Run(tc.who.name+" cannot rename", func(t *testing.T) {
			if code := call(r, http.MethodPatch, path, `{"name":"Not Them"}`); code != http.StatusForbidden {
				t.Errorf("rename: got %d, want 403", code)
			}
		})
		t.Run(tc.who.name+" cannot deactivate", func(t *testing.T) {
			if code := call(r, http.MethodDelete, path, ""); code != http.StatusForbidden {
				t.Errorf("deactivate: got %d, want 403", code)
			}
			if liveSessions(t, pool, tc.target.id) != 1 {
				t.Error("their session ended anyway")
			}
		})
	}

	// What they are there for still works on somebody they outrank.
	r := asHolder(svc, org.ID, admin.id, adminPerms...)
	if code := call(r, http.MethodPost, "/api/users/"+member.id.String()+"/reset-password", ""); code != http.StatusOK {
		t.Errorf("an admin resetting a member: got %d", code)
	}
	if code := call(r, http.MethodDelete, "/api/users/"+member.id.String(), ""); code != http.StatusOK {
		t.Errorf("an admin deactivating a member: got %d", code)
	}
	// And an owner may act on another owner.
	second := addResetPerson(t, svc, pool, org.ID, "second@rank.test", "owner")
	r = asHolder(svc, org.ID, owner.id, "*")
	if code := call(r, http.MethodPost, "/api/users/"+second.id.String()+"/reset-password", ""); code != http.StatusOK {
		t.Errorf("an owner resetting an owner: got %d", code)
	}
}

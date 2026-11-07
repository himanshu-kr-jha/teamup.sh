package org

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func postReset(r *gin.Engine, userID uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/users/"+userID.String()+"/reset-password", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// resetPerson is somebody who can sign in: an account, the password they have
// now, and one live session.
type resetPerson struct {
	id       uuid.UUID
	password string
}

func addResetPerson(t *testing.T, svc *Service, pool *pgxpool.Pool, orgID uuid.UUID, email, role string) resetPerson {
	t.Helper()
	ctx := context.Background()
	u, password, err := svc.AddMember(ctx, orgID, email, email, role)
	if err != nil {
		t.Fatalf("add %s: %v", email, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET must_change_password = FALSE WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("settle %s: %v", email, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, session_id, token_hash, expires_at)
		VALUES ($1, $2, $3, NOW() + INTERVAL '1 day')
	`, u.ID, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatalf("session for %s: %v", email, err)
	}
	return resetPerson{id: u.ID, password: password}
}

func passwordWorks(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, password string) bool {
	t.Helper()
	var hash string
	if err := pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func liveSessions(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestResetPasswordIssuesATemporaryOneAndEndsTheirSessions(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Reset Co", "reset-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)
	member := addResetPerson(t, svc, pool, org.ID, "member@reset.test", "member")
	colleague := addResetPerson(t, svc, pool, org.ID, "colleague@reset.test", "member")

	w := postReset(asRoles(t, svc, org.ID.String(), "admin"), member.id)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		TemporaryPassword string `json:"temporaryPassword"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.TemporaryPassword) < temporaryPasswordLength {
		t.Errorf("temporary password is too short: %q", got.TemporaryPassword)
	}
	if !passwordWorks(t, pool, member.id, got.TemporaryPassword) {
		t.Error("the password shown to the administrator does not sign the member in")
	}
	if passwordWorks(t, pool, member.id, member.password) {
		t.Error("the old password still works")
	}
	if u, err := svc.GetUser(ctx, member.id); err != nil || !u.MustChangePassword {
		t.Errorf("the member must be made to change it: %+v, %v", u, err)
	}
	if n := liveSessions(t, pool, member.id); n != 0 {
		t.Errorf("%d session(s) survived the reset", n)
	}

	// Nobody else is touched.
	if !passwordWorks(t, pool, colleague.id, colleague.password) || liveSessions(t, pool, colleague.id) != 1 {
		t.Error("resetting one member changed another")
	}
}

func TestResetPasswordIsRefusedAndChangesNothing(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Guard Co", "guard-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	other, err := svc.CreateOrganization(ctx, "Other Co", "other-co", nil)
	if err != nil {
		t.Fatalf("create other org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)
	seedDefaultRoles(t, svc, other.ID)

	owner := addResetPerson(t, svc, pool, org.ID, "owner@guard.test", "owner")
	admin := addResetPerson(t, svc, pool, org.ID, "admin@guard.test", "admin")
	member := addResetPerson(t, svc, pool, org.ID, "member@guard.test", "member")
	outsider := addResetPerson(t, svc, pool, other.ID, "member@other.test", "member")

	cases := []struct {
		name   string
		caller resetPerson
		roles  []string
		target resetPerson
		want   int
	}{
		{"a member cannot reset a colleague", member, []string{"member"}, admin, http.StatusForbidden},
		{"an admin cannot reset an owner", admin, []string{"admin"}, owner, http.StatusForbidden},
		{"an administrator cannot skip the current password on their own account", admin, []string{"admin"}, admin, http.StatusBadRequest},
		{"an admin cannot see another organization's member", admin, []string{"admin"}, outsider, http.StatusNotFound},
		{"an owner cannot see another organization's member", owner, []string{"owner"}, outsider, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := asUser(t, svc, org.ID.String(), tc.caller.id.String(), tc.roles...)
			if w := postReset(r, tc.target.id); w.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
			if !passwordWorks(t, pool, tc.target.id, tc.target.password) {
				t.Error("a refused reset still changed the password")
			}
			if liveSessions(t, pool, tc.target.id) != 1 {
				t.Error("a refused reset still ended the session")
			}
		})
	}

	t.Run("an owner can reset another owner", func(t *testing.T) {
		second := addResetPerson(t, svc, pool, org.ID, "second@guard.test", "owner")
		r := asUser(t, svc, org.ID.String(), owner.id.String(), "owner")
		if w := postReset(r, second.id); w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("a deleted member is not found", func(t *testing.T) {
		gone := addResetPerson(t, svc, pool, org.ID, "gone@guard.test", "member")
		if err := svc.DeactivateUser(ctx, org.ID, gone.id); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
		r := asUser(t, svc, org.ID.String(), owner.id.String(), "owner")
		if w := postReset(r, gone.id); w.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
		}
	})
}

func TestADeletedMemberCanBeAddedBackWithTheSameEmail(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	org, err := svc.CreateOrganization(ctx, "Return Co", "return-co", nil)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	seedDefaultRoles(t, svc, org.ID)
	r := asRoles(t, svc, org.ID.String(), "admin")

	add := func() *httptest.ResponseRecorder {
		return postMember(t, r, map[string]string{"name": "Ada", "email": "ada@return.test"})
	}

	// Twice, because a person can leave and come back more than once.
	for round := 1; round <= 2; round++ {
		w := add()
		if w.Code != http.StatusCreated {
			t.Fatalf("round %d: want 201, got %d: %s", round, w.Code, w.Body.String())
		}
		var got struct {
			User struct {
				ID uuid.UUID `json:"id"`
			} `json:"user"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}

		// While they are here, the address is still theirs alone.
		if dup := add(); dup.Code != http.StatusConflict {
			t.Fatalf("round %d: a live duplicate should be 409, got %d: %s", round, dup.Code, dup.Body.String())
		}
		if err := svc.DeactivateUser(ctx, org.ID, got.User.ID); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
	}
}

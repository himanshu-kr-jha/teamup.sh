package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The welcome tour is shown until it is finished or skipped, once, on any
// device; and can be asked for again.
func TestTheWelcomeTourIsShownOnceAndCanBeShownAgain(t *testing.T) {
	pool := testDB(t)
	svc := testService(t, pool)
	ctx := context.Background()
	email := "tour-" + uuid.New().String()[:8] + "@acme.test"
	seedUser(t, pool, email, "their-password")
	user, err := svc.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	token := signIn(t, svc, user)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api/auth"), svc)

	completedAt := func() any {
		t.Helper()
		w := getAs(r, http.MethodGet, "/api/auth/me", token)
		var got struct {
			User map[string]any `json:"user"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		v, present := got.User["tourCompletedAt"]
		if !present {
			t.Fatal("tourCompletedAt is not in /me at all")
		}
		return v
	}
	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/tour", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if got := completedAt(); got != nil {
		t.Fatalf("a new person has not seen it yet: %v", got)
	}
	if code := post(`{"done":true}`); code != http.StatusOK {
		t.Fatalf("finishing it: %d", code)
	}
	if got := completedAt(); got == nil {
		t.Fatal("finished, it is not shown again")
	}
	if code := post(`{"done":false}`); code != http.StatusOK {
		t.Fatalf("asking for it again: %d", code)
	}
	if got := completedAt(); got != nil {
		t.Fatalf("asked for again, it is shown again: %v", got)
	}
}

// What's new opens by itself once after each update: the newest entry seen is
// kept for the person, only ever moves forward, and takes only an entry's id.
func TestWhatsNewRemembersTheNewestEntrySeen(t *testing.T) {
	pool := testDB(t)
	svc := testService(t, pool)
	ctx := context.Background()
	email := "news-" + uuid.New().String()[:8] + "@acme.test"
	seedUser(t, pool, email, "their-password")
	user, err := svc.GetUserByEmail(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	token := signIn(t, svc, user)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api/auth"), svc)

	seen := func() any {
		t.Helper()
		w := getAs(r, http.MethodGet, "/api/auth/me", token)
		var got struct {
			User map[string]any `json:"user"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		v, present := got.User["whatsNewSeen"]
		if !present {
			t.Fatal("whatsNewSeen is not in /me at all")
		}
		return v
	}
	post := func(id string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/whats-new", strings.NewReader(`{"seen":"`+id+`"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if got := seen(); got != nil {
		t.Fatalf("nothing seen yet: %v", got)
	}
	if code := post("2026-11-12"); code != http.StatusOK {
		t.Fatalf("seeing an entry: %d", code)
	}
	if got := seen(); got != "2026-11-12" {
		t.Fatalf("kept: %v", got)
	}
	// An older page still open somewhere does not bring the newest back.
	if code := post("2026-11-01"); code != http.StatusOK {
		t.Fatalf("an older entry: %d", code)
	}
	if got := seen(); got != "2026-11-12" {
		t.Errorf("never backwards: %v", got)
	}
	if code := post("2026-11-12-b"); code != http.StatusOK || seen() != "2026-11-12-b" {
		t.Errorf("a second update the same day moves it on")
	}
	for _, bad := range []string{"", "yesterday", "2026-11-12; DROP TABLE users", "2026-11-12-TOOLONGSUFFIX"} {
		if code := post(bad); code != http.StatusBadRequest {
			t.Errorf("%q should be refused, got %d", bad, code)
		}
	}
}

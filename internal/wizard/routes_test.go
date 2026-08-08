package wizard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func init() { gin.SetMode(gin.TestMode) }

// A service with a session store but no database: enough for every handler
// that only accumulates state, and it panics loudly if one reaches for the DB.
func testHandler() (*Handler, *memoryStore) {
	store := newMemoryStore()
	return NewHandler(&Service{sessions: store, logger: zerolog.Nop()}), store
}

func testRouter(h *Handler) *gin.Engine {
	r := gin.New()
	g := r.Group("/api/wizard")
	g.POST("/step1", h.Step1Organization)
	g.POST("/step2", h.Step2Admin)
	g.POST("/step3", h.Step3Auth)
	g.POST("/step4", h.Step4Services)
	g.POST("/step5", h.Step5Complete)
	return r
}

func postJSON(t *testing.T, r *gin.Engine, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookie {
			return c
		}
	}
	t.Fatalf("response did not set the %s cookie; wizard state cannot survive the next request", SessionCookie)
	return nil
}

// The regression that broke setup: each step returned 200 but nothing was kept,
// so step 5 finalized an empty form.
func TestStepsAccumulateStateAcrossRequests(t *testing.T) {
	h, store := testHandler()
	r := testRouter(h)

	w := postJSON(t, r, "/api/wizard/step1", map[string]any{
		"orgName": "Acme Inc", "orgSubdomain": "acme",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("step1: got %d, body %s", w.Code, w.Body.String())
	}
	cookie := sessionCookie(t, w)

	w = postJSON(t, r, "/api/wizard/step2", map[string]any{
		"adminName": "Ada Lovelace", "adminEmail": "ada@acme.test", "adminPassword": "correct-horse-battery",
	}, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("step2: got %d, body %s", w.Code, w.Body.String())
	}

	w = postJSON(t, r, "/api/wizard/step3", map[string]any{
		"oidcProvider": "custom", "customIssuer": "https://id.acme.test",
		"customClientId": "cid", "customClientSecret": "csecret",
	}, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("step3: got %d, body %s", w.Code, w.Body.String())
	}

	w = postJSON(t, r, "/api/wizard/step4", map[string]any{
		"minioEndpoint": "localhost:9000", "minioAccessKey": "minioadmin",
		"minioSecretKey": "minioadmin", "minioBucket": "teamup-files",
		"livekitUrl": "http://localhost:7880", "livekitApiKey": "devkey", "livekitApiSecret": "devsecret",
	}, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("step4: got %d, body %s", w.Code, w.Body.String())
	}

	state, _, err := store.Load(context.Background(), cookie.Value)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if err := state.Data.Validate(); err != nil {
		t.Fatalf("state after four steps should be complete, got: %v", err)
	}
	if state.Data.OrgName != "Acme Inc" || state.Data.AdminEmail != "ada@acme.test" {
		t.Errorf("state lost earlier steps: %+v", state.Data)
	}
}

// The frontend posts its whole form to every step; extra keys must be ignored
// rather than rejected or allowed to overwrite later steps.
func TestStepIgnoresUnrelatedFields(t *testing.T) {
	h, store := testHandler()
	r := testRouter(h)

	w := postJSON(t, r, "/api/wizard/step1", map[string]any{
		"orgName": "Acme Inc", "orgSubdomain": "acme",
		"adminEmail": "ada@acme.test", "minioBucket": "teamup-files", "smtpPort": 587,
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("step1: got %d, body %s", w.Code, w.Body.String())
	}

	state, _, _ := store.Load(context.Background(), sessionCookie(t, w).Value)
	if state.Data.AdminEmail != "" {
		t.Errorf("step1 must not absorb step2 fields, got adminEmail=%q", state.Data.AdminEmail)
	}
	if state.Data.OrgName != "Acme Inc" {
		t.Errorf("step1 did not record the org, got %+v", state.Data)
	}
}

func TestStep1RejectsBlankOrganization(t *testing.T) {
	h, _ := testHandler()
	r := testRouter(h)

	w := postJSON(t, r, "/api/wizard/step1", map[string]any{"orgName": "", "orgSubdomain": ""}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("blank org should be rejected, got %d body %s", w.Code, w.Body.String())
	}
}

func TestStep2RejectsShortPassword(t *testing.T) {
	h, _ := testHandler()
	r := testRouter(h)

	w := postJSON(t, r, "/api/wizard/step2", map[string]any{
		"adminName": "Ada", "adminEmail": "ada@acme.test", "adminPassword": "short",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("short password should be rejected, got %d", w.Code)
	}
}

// Finalizing early must be refused before any database work happens; the nil
// db in this fixture would panic if the handler got that far.
func TestStep5RefusesToFinalizeIncompleteSetup(t *testing.T) {
	h, _ := testHandler()
	r := testRouter(h)

	w := postJSON(t, r, "/api/wizard/step1", map[string]any{
		"orgName": "Acme Inc", "orgSubdomain": "acme",
	}, nil)
	cookie := sessionCookie(t, w)

	w = postJSON(t, r, "/api/wizard/step5", map[string]any{}, cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("finalizing with steps outstanding should fail, got %d body %s", w.Code, w.Body.String())
	}

	var resp struct {
		Error        string `json:"error"`
		MissingSteps []int  `json:"missingSteps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.MissingSteps) != 3 {
		t.Errorf("should report the 3 outstanding steps, got %v", resp.MissingSteps)
	}
}

func TestStep5WithNoSessionRefuses(t *testing.T) {
	h, _ := testHandler()
	r := testRouter(h)

	w := postJSON(t, r, "/api/wizard/step5", map[string]any{}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("finalizing without a session should fail, got %d", w.Code)
	}
}

// An id the server never issued must be discarded, not adopted. Otherwise an
// attacker can plant a cookie, let the operator fill in the wizard under it,
// then overwrite the admin step and finalize as owner.
func TestPlantedSessionIDIsRejected(t *testing.T) {
	h, store := testHandler()
	r := testRouter(h)

	planted := &http.Cookie{Name: SessionCookie, Value: "attacker-chosen-session-id"}
	w := postJSON(t, r, "/api/wizard/step1", map[string]any{
		"orgName": "Acme Inc", "orgSubdomain": "acme",
	}, planted)
	if w.Code != http.StatusOK {
		t.Fatalf("step1: got %d, body %s", w.Code, w.Body.String())
	}

	issued := sessionCookie(t, w)
	if issued.Value == planted.Value {
		t.Fatal("server adopted an attacker-supplied session id")
	}

	if _, found, _ := store.Load(context.Background(), planted.Value); found {
		t.Error("no state should be stored under the planted id")
	}
	state, found, _ := store.Load(context.Background(), issued.Value)
	if !found || state.Data.OrgName != "Acme Inc" {
		t.Errorf("state should live under the server-issued id, got found=%v %+v", found, state.Data)
	}
}

// nginx terminates TLS and proxies plaintext, so Request.TLS is nil in the
// standard deployment; without honouring the forwarded header the session
// cookie would never be marked Secure.
func TestSessionCookieIsSecureBehindTLSTerminatingProxy(t *testing.T) {
	h, _ := testHandler()
	r := testRouter(h)

	raw, _ := json.Marshal(map[string]any{"orgName": "Acme Inc", "orgSubdomain": "acme"})
	req := httptest.NewRequest(http.MethodPost, "/api/wizard/step1", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !sessionCookie(t, w).Secure {
		t.Error("cookie must be Secure when the request reached the proxy over HTTPS")
	}
}

func TestSessionCookieNotSecureOverPlainHTTP(t *testing.T) {
	h, _ := testHandler()
	r := testRouter(h)

	// A plain-HTTP dev instance must still work; marking the cookie Secure
	// there would make the browser drop it and break setup entirely.
	w := postJSON(t, r, "/api/wizard/step1", map[string]any{
		"orgName": "Acme Inc", "orgSubdomain": "acme",
	}, nil)
	if sessionCookie(t, w).Secure {
		t.Error("cookie must not be Secure on a plain-HTTP request")
	}
}

func TestSessionCookieIsHardened(t *testing.T) {
	h, _ := testHandler()
	r := testRouter(h)

	w := postJSON(t, r, "/api/wizard/step1", map[string]any{
		"orgName": "Acme Inc", "orgSubdomain": "acme",
	}, nil)
	c := sessionCookie(t, w)

	if !c.HttpOnly {
		t.Error("wizard session cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode && c.SameSite != http.SameSiteStrictMode {
		t.Errorf("wizard session cookie needs SameSite protection, got %v", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("cookie path should be /, got %q", c.Path)
	}
}

// The session id is the only thing standing between an anonymous caller and a
// half-configured instance, so it must not be guessable.
func TestSessionIDsAreUnique(t *testing.T) {
	h, _ := testHandler()
	r := testRouter(h)

	body := map[string]any{"orgName": "Acme Inc", "orgSubdomain": "acme"}
	first := sessionCookie(t, postJSON(t, r, "/api/wizard/step1", body, nil))
	second := sessionCookie(t, postJSON(t, r, "/api/wizard/step1", body, nil))

	if first.Value == second.Value {
		t.Error("each wizard session must get its own id")
	}
	if len(first.Value) < 32 {
		t.Errorf("session id looks too short to be unguessable: %d chars", len(first.Value))
	}
}

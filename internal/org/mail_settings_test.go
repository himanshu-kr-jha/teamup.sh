package org

// The mail server an administrator sets up after first-time setup, and what adding
// members does with it: the welcome email, and why it was not sent when it was not.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/mail"
)

// fakeMailer records what would have been sent, and fails as told.
type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	fail error
	// failAfter fails every send after this many have gone through, when set.
	failAfter int
	tests     []string
}

func (f *fakeMailer) Send(_ context.Context, _ uuid.UUID, msg mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil && len(f.sent) >= f.failAfter {
		return f.fail
	}
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeMailer) WelcomeMessage(orgName, name, email, temporaryPassword string) mail.Message {
	return mail.Message{To: email, Subject: "Welcome to " + orgName, Body: name + " " + temporaryPassword}
}

func (f *fakeMailer) Configured(context.Context, uuid.UUID) bool { return true }

func (f *fakeMailer) SendTest(_ context.Context, _ uuid.UUID, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.tests = append(f.tests, to)
	return nil
}

func withMailer(t *testing.T, svc *Service, orgID, userID string, mailer Mailer, roles ...string) *gin.Engine {
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
	RegisterRoutes(api, svc, func(h *Handler) { h.SetMailer(mailer) })
	return r
}

func send(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

type mailWorld struct {
	svc    *Service
	org    uuid.UUID
	admin  *User
	mailer *fakeMailer
}

func newMailWorld(t *testing.T) *mailWorld {
	t.Helper()
	svc, _ := newService(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, "Mail Co", "mail-co-"+uuid.NewString()[:8], nil)
	if err != nil {
		t.Fatal(err)
	}
	seedDefaultRoles(t, svc, org.ID)
	admin, _, err := svc.AddMember(ctx, org.ID, "Ada Admin", "ada@mail.test", "admin")
	if err != nil {
		t.Fatal(err)
	}
	return &mailWorld{svc: svc, org: org.ID, admin: admin, mailer: &fakeMailer{}}
}

func (w *mailWorld) as(t *testing.T, roles ...string) *gin.Engine {
	return withMailer(t, w.svc, w.org.String(), w.admin.ID.String(), w.mailer, roles...)
}

func TestAnAdministratorSetsUpMailAndThePasswordNeverComesBack(t *testing.T) {
	w := newMailWorld(t)
	r := w.as(t, "admin")

	got := send(t, r, http.MethodGet, "/api/org/mail", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"configured":false`) {
		t.Fatalf("nothing set up yet: %d %s", got.Code, got.Body.String())
	}

	saved := send(t, r, http.MethodPut, "/api/org/mail", map[string]any{
		"host": "smtp.gmail.com", "port": 587, "user": "team@example.com", "password": "app-password-1", "from": "TeamUp <team@example.com>",
	})
	if saved.Code != http.StatusOK {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	read := send(t, r, http.MethodGet, "/api/org/mail", nil)
	for _, body := range []string{saved.Body.String(), read.Body.String()} {
		if strings.Contains(body, "app-password-1") {
			t.Fatalf("the password came back: %s", body)
		}
		if !strings.Contains(body, `"passwordSet":true`) || !strings.Contains(body, `"configured":true`) {
			t.Errorf("want it saved and configured: %s", body)
		}
	}

	// Saving again with the password left blank keeps it.
	send(t, r, http.MethodPut, "/api/org/mail", map[string]any{
		"host": "smtp.gmail.com", "port": 465, "user": "team@example.com", "password": "", "from": "team@example.com",
	})
	var stored mail.Settings
	if err := w.svc.GetConfigInto(context.Background(), w.org, "smtp", &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Pass != "app-password-1" || stored.Port != 465 {
		t.Errorf("a blank password keeps the saved one, got %+v", stored)
	}

	// Pointed at another server, the saved password is not kept for it.
	send(t, r, http.MethodPut, "/api/org/mail", map[string]any{
		"host": "smtp.attacker.example", "port": 587, "user": "team@example.com", "password": "", "from": "team@example.com",
	})
	stored = mail.Settings{}
	if err := w.svc.GetConfigInto(context.Background(), w.org, "smtp", &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Pass != "" {
		t.Error("the saved password went with the new host")
	}

	if bad := send(t, r, http.MethodPut, "/api/org/mail", map[string]any{"host": "smtp.gmail.com", "from": "not an address"}); bad.Code != http.StatusBadRequest {
		t.Errorf("a from that is not an address is refused, got %d", bad.Code)
	}
}

func TestOnlyAnAdministratorTouchesMailSettings(t *testing.T) {
	w := newMailWorld(t)
	r := w.as(t, "member")
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/org/mail"}, {http.MethodPut, "/api/org/mail"}, {http.MethodPost, "/api/org/mail/test"},
	} {
		if got := send(t, r, c.method, c.path, map[string]any{}); got.Code != http.StatusForbidden {
			t.Errorf("%s %s as a member: %d", c.method, c.path, got.Code)
		}
	}
}

func TestATestEmailGoesToWhoeverAskedAndAFailureSaysWhy(t *testing.T) {
	w := newMailWorld(t)
	r := w.as(t, "admin")
	if got := send(t, r, http.MethodPost, "/api/org/mail/test", nil); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"sent":true`) || len(w.mailer.tests) != 1 || w.mailer.tests[0] != "ada@mail.test" {
		t.Fatalf("sent to the administrator: %d %s %v", got.Code, got.Body.String(), w.mailer.tests)
	}
	w.mailer.fail = errors.New("535 5.7.8 Username and Password not accepted")
	got := send(t, r, http.MethodPost, "/api/org/mail/test", nil)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"sent":false`) || !strings.Contains(got.Body.String(), `"problem":"the mail server did not take the message"`) {
		t.Errorf("a fixed sentence, not the server's words: %d %s", got.Code, got.Body.String())
	}
	w.mailer.fail = mail.ErrNotConfigured
	if got := send(t, r, http.MethodPost, "/api/org/mail/test", nil); got.Code != http.StatusConflict {
		t.Errorf("nothing saved: %d", got.Code)
	}
}

func TestAddingSomebodyEmailsThemOrSaysWhyNot(t *testing.T) {
	w := newMailWorld(t)
	r := w.as(t, "admin")

	ok := postMember(t, r, map[string]string{"name": "Bob", "email": "bob@mail.test"})
	if ok.Code != http.StatusCreated || !strings.Contains(ok.Body.String(), `"emailed":true`) {
		t.Fatalf("emailed: %d %s", ok.Code, ok.Body.String())
	}
	if len(w.mailer.sent) != 1 || w.mailer.sent[0].To != "bob@mail.test" {
		t.Errorf("Bob's welcome: %+v", w.mailer.sent)
	}

	w.mailer.fail, w.mailer.failAfter = errorsJoin(mail.ErrUnreachable, errors.New("could not reach smtp.example.com:587: i/o timeout")), 0
	failed := postMember(t, r, map[string]string{"name": "Cy", "email": "cy@mail.test"})
	var body struct {
		Emailed           bool   `json:"emailed"`
		MailProblem       string `json:"mailProblem"`
		TemporaryPassword string `json:"temporaryPassword"`
	}
	_ = json.Unmarshal(failed.Body.Bytes(), &body)
	if failed.Code != http.StatusCreated || body.Emailed || body.MailProblem != "could not connect to the mail server" || body.TemporaryPassword == "" {
		t.Errorf("Cy still exists, with a password to pass on and why it was not sent: %d %s", failed.Code, failed.Body.String())
	}

	w.mailer.fail = mail.ErrNotConfigured
	none := postMember(t, r, map[string]string{"name": "Di", "email": "di@mail.test"})
	if !strings.Contains(none.Body.String(), `"mailProblem":"not_configured"`) {
		t.Errorf("not set up: %s", none.Body.String())
	}
}

func TestImportingEmailsEverybodyAndStopsTryingOnceTheServerFails(t *testing.T) {
	w := newMailWorld(t)
	r := w.as(t, "admin")
	csv := "name,email\nEve,eve@mail.test\nFay,fay@mail.test\nGus,gus@mail.test\n"

	got := send(t, r, http.MethodPost, "/api/users/import", map[string]string{"csv": csv})
	if got.Code != http.StatusOK || strings.Count(got.Body.String(), `"emailed":true`) != 3 || len(w.mailer.sent) != 3 {
		t.Fatalf("all three emailed: %d %s", got.Code, got.Body.String())
	}

	w.mailer.fail, w.mailer.failAfter = errorsJoin(mail.ErrUnreachable, errors.New("could not reach smtp.example.com:587")), len(w.mailer.sent)+1
	csv = "name,email\nHal,hal@mail.test\nIvy,ivy@mail.test\nJo,jo@mail.test\n"
	got = send(t, r, http.MethodPost, "/api/users/import", map[string]string{"csv": csv})
	var body struct {
		Outcomes    []ImportOutcome `json:"outcomes"`
		MailProblem string          `json:"mailProblem"`
	}
	_ = json.Unmarshal(got.Body.Bytes(), &body)
	emailed := 0
	for _, o := range body.Outcomes {
		if o.Emailed {
			emailed++
		}
	}
	if emailed != 1 || body.MailProblem != "could not connect to the mail server" {
		t.Errorf("Hal is emailed, then the server fails and it is not tried again: %s", got.Body.String())
	}
	if len(w.mailer.sent) != 4 {
		t.Errorf("one more sent, then none: %d", len(w.mailer.sent))
	}
}

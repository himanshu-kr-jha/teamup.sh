package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"

	"github.com/teamup/teamup/internal/mail"
	"github.com/teamup/teamup/internal/testsupport"
)

// fakeMailer keeps what would have been sent, building messages as the real
// mail service does.
type fakeMailer struct {
	mu         sync.Mutex
	configured bool
	fail       bool
	sent       []mail.Message
	real       *mail.Service
}

func newFakeMailer() *fakeMailer {
	return &fakeMailer{configured: true, real: mail.NewService(nil, "https://teamup.example.test", zerolog.Nop())}
}

func (m *fakeMailer) Configured(context.Context, uuid.UUID) bool { return m.configured }
func (m *fakeMailer) Send(_ context.Context, _ uuid.UUID, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("connection refused")
	}
	m.sent = append(m.sent, msg)
	return nil
}
func (m *fakeMailer) PasswordResetMessage(orgName, name, email, token string, validFor time.Duration) mail.Message {
	return m.real.PasswordResetMessage(orgName, name, email, token, validFor)
}
func (m *fakeMailer) PasswordChangedMessage(orgName, name, email string) mail.Message {
	return m.real.PasswordChangedMessage(orgName, name, email)
}

func (m *fakeMailer) all() []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mail.Message(nil), m.sent...)
}

// lastToken is the token in the newest reset link sent.
func (m *fakeMailer) lastToken(t *testing.T) string {
	t.Helper()
	sent := m.all()
	for i := len(sent) - 1; i >= 0; i-- {
		if _, after, ok := strings.Cut(sent[i].Body, "/reset-password#token="); ok {
			return strings.Fields(after)[0]
		}
	}
	t.Fatalf("no reset link was sent, got %d messages", len(sent))
	return ""
}

type adminNote struct {
	to    []uuid.UUID
	kind  string
	title string
	data  map[string]any
}

type fakeNotifier struct {
	mu    sync.Mutex
	notes []adminNote
}

func (n *fakeNotifier) notify(_ context.Context, _ uuid.UUID, to []uuid.UUID, kind, title, _ string, data map[string]any) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.notes = append(n.notes, adminNote{to: to, kind: kind, title: title, data: data})
	return nil
}

func (n *fakeNotifier) all() []adminNote {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]adminNote(nil), n.notes...)
}

// resetBrowser is a browser whose server sends reset mail to a fake, doing the
// work before it answers so the test need not wait.
func resetBrowser(t *testing.T) (*browser, *fakeMailer, *fakeNotifier) {
	t.Helper()
	b := newBrowser(t)
	mailer, notes := newFakeMailer(), &fakeNotifier{}
	b.svc.SetPasswordResets(mailer, notes.notify)
	b.svc.runReset = func(work func(context.Context)) { work(context.Background()) }
	return b, mailer, notes
}

func (b *browser) forgot(email string) (int, string) {
	b.t.Helper()
	return b.send(http.MethodPost, "/api/auth/password/forgot", `{"email":"`+email+`"}`)
}

func (b *browser) signInWith(password string) int {
	b.t.Helper()
	code, _ := b.send(http.MethodPost, "/api/auth/login", `{"email":"`+b.email+`","password":"`+password+`"}`)
	return code
}

func TestAForgottenPasswordIsResetWithTheEmailedLinkOnce(t *testing.T) {
	b, mailer, _ := resetBrowser(t)
	// Signed in somewhere else, which the reset must end.
	b.login()

	code, body := b.forgot(strings.ToUpper(b.email))
	if code != http.StatusAccepted || !strings.Contains(body, "if there is an account") {
		t.Fatalf("asking for a link: %d %s", code, body)
	}
	sent := mailer.all()
	if len(sent) != 1 || sent[0].To != b.email || !strings.HasPrefix(sent[0].Subject, "Reset your ") {
		t.Fatalf("one reset email to the account, got %+v", sent)
	}
	if !strings.Contains(sent[0].Body, "https://teamup.example.test/reset-password#token=") || !strings.Contains(sent[0].Body, "30 minutes") {
		t.Errorf("the email carries the link and how long it works:\n%s", sent[0].Body)
	}
	token := mailer.lastToken(t)

	// Only a hash of the token is kept.
	var stored int
	if err := b.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM password_resets WHERE position($1::bytea in token_hash) > 0`, []byte(token)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Error("the token itself is in the database")
	}

	code, body = b.send(http.MethodPost, "/api/auth/password/reset/check", `{"token":"`+token+`"}`)
	if code != http.StatusOK || !strings.Contains(body, b.email) {
		t.Fatalf("checking the link: %d %s", code, body)
	}
	if code, _ := b.send(http.MethodPost, "/api/auth/password/reset", `{"token":"`+token+`","newPassword":"short"}`); code != http.StatusBadRequest {
		t.Errorf("a short password: %d", code)
	}
	if code, body := b.send(http.MethodPost, "/api/auth/password/reset", `{"token":"`+token+`","newPassword":"A-new-password-42"}`); code != http.StatusOK {
		t.Fatalf("resetting: %d %s", code, body)
	}

	// Signed out of where it was, by the browser too.
	if b.cookie("/api/auth/refresh", "rt") != "" {
		t.Error("the reset left the browser holding a refresh token")
	}
	var live int
	if err := b.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM refresh_tokens rt JOIN users u ON u.id = rt.user_id WHERE u.email = $1 AND rt.revoked_at IS NULL`, b.email).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Errorf("%d sessions still live after the reset", live)
	}
	if got := b.signInWith(logoutTestPassword); got != http.StatusUnauthorized {
		t.Errorf("the old password still signs in: %d", got)
	}
	if got := b.signInWith("A-new-password-42"); got != http.StatusOK {
		t.Errorf("the new password does not sign in: %d", got)
	}

	// Once only.
	code, body = b.send(http.MethodPost, "/api/auth/password/reset", `{"token":"`+token+`","newPassword":"Another-password-42"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "expired or has already been used") {
		t.Errorf("using the link twice: %d %s", code, body)
	}
	// And they are told it happened.
	sent = mailer.all()
	if len(sent) != 2 || !strings.Contains(sent[1].Subject, "password was changed") || sent[1].To != b.email {
		t.Errorf("a notice that the password changed, got %+v", sent)
	}
}

func TestNobodyLearnsWhetherThereIsAnAccount(t *testing.T) {
	b, mailer, notes := resetBrowser(t)
	_, known := b.forgot(b.email)
	code, unknown := b.forgot("nobody-" + uuid.NewString() + "@example.test")
	if code != http.StatusAccepted || unknown != known {
		t.Errorf("an unknown address is answered differently: %d %s, against %s", code, unknown, known)
	}
	if got := len(mailer.all()); got != 1 {
		t.Errorf("only the real account is emailed, got %d messages", got)
	}
	if len(notes.all()) != 0 {
		t.Error("nobody is asked about an address with no account")
	}
	if code, _ := b.forgot("not an address"); code != http.StatusBadRequest {
		t.Errorf("something that is not an address: %d", code)
	}
}

func TestOnlyTheNewestUnexpiredLinkWorks(t *testing.T) {
	b, mailer, _ := resetBrowser(t)
	b.forgot(b.email)
	first := mailer.lastToken(t)
	b.forgot(b.email)
	second := mailer.lastToken(t)
	if first == second {
		t.Fatal("two requests, one token")
	}
	if code, _ := b.send(http.MethodPost, "/api/auth/password/reset/check", `{"token":"`+first+`"}`); code != http.StatusBadRequest {
		t.Errorf("the older link still works: %d", code)
	}
	if _, err := b.pool.Exec(context.Background(),
		`UPDATE password_resets SET expires_at = NOW() - interval '1 second' WHERE used_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if code, _ := b.send(http.MethodPost, "/api/auth/password/reset", `{"token":"`+second+`","newPassword":"A-new-password-42"}`); code != http.StatusBadRequest {
		t.Errorf("an expired link still works: %d", code)
	}
	if code, _ := b.send(http.MethodPost, "/api/auth/password/reset/check", `{"token":"made-up"}`); code != http.StatusBadRequest {
		t.Errorf("a made-up link: %d", code)
	}
}

func TestChangingThePasswordAnotherWayRetiresTheLink(t *testing.T) {
	b, mailer, _ := resetBrowser(t)
	b.forgot(b.email)
	token := mailer.lastToken(t)
	user, err := b.svc.GetUserByEmail(context.Background(), b.email)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.svc.ChangePassword(context.Background(), user.ID, logoutTestPassword, "Remembered-it-42"); err != nil {
		t.Fatal(err)
	}
	if code, _ := b.send(http.MethodPost, "/api/auth/password/reset/check", `{"token":"`+token+`"}`); code != http.StatusBadRequest {
		t.Errorf("the link outlived a password change: %d", code)
	}
}

func TestAnAccountIsSentThreeLinksAnHourAtMost(t *testing.T) {
	b, mailer, _ := resetBrowser(t)
	for range 5 {
		if code, _ := b.forgot(b.email); code != http.StatusAccepted {
			t.Fatalf("asking: %d", code)
		}
	}
	if got := len(mailer.all()); got != resetRequestsAllowed {
		t.Errorf("sent %d links, want %d", got, resetRequestsAllowed)
	}
}

// Without mail, the people who can reset passwords are asked to, in the app.
func TestWithoutMailTheAdministratorsAreAsked(t *testing.T) {
	b, mailer, notes := resetBrowser(t)
	mailer.configured = false
	owner, err := b.svc.GetUserByEmail(context.Background(), b.email)
	if err != nil {
		t.Fatal(err)
	}
	org := &testsupport.Org{ID: owner.OrgID, OwnerID: owner.ID}
	memberEmail := "member-" + uuid.NewString() + "@example.test"
	member := testsupport.SeedUser(t, b.pool, org, memberEmail, "member")
	hash, _ := bcrypt.GenerateFromPassword([]byte("Member-pass-12345"), bcrypt.MinCost)
	if _, err := b.pool.Exec(context.Background(), `UPDATE users SET password_hash = $2 WHERE id = $1`, member, hash); err != nil {
		t.Fatal(err)
	}
	// A colleague who is no administrator is not asked.
	testsupport.SeedUser(t, b.pool, org, "colleague-"+uuid.NewString()+"@example.test", "member")

	if code, _ := b.forgot(memberEmail); code != http.StatusAccepted {
		t.Fatalf("asking: %d", code)
	}
	got := notes.all()
	if len(got) != 1 || len(got[0].to) != 1 || got[0].to[0] != owner.ID || got[0].kind != "password.reset_requested" ||
		got[0].data["userId"] != member.String() || !strings.Contains(got[0].title, "forgot their password") {
		t.Fatalf("the owner, and only the owner, is asked about the member, got %+v", got)
	}
	if len(mailer.all()) != 0 {
		t.Error("mail was sent without a mail server")
	}

	// Mail that is set up but fails asks them too, and leaves no working link.
	mailer.configured, mailer.fail = true, true
	b.forgot(memberEmail)
	if len(notes.all()) != 2 {
		t.Errorf("a failed send should ask the administrators, got %+v", notes.all())
	}
	var working int
	if err := b.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM password_resets WHERE user_id = $1 AND used_at IS NULL`, member).Scan(&working); err != nil {
		t.Fatal(err)
	}
	if working != 0 {
		t.Errorf("%d working links that were never delivered", working)
	}
}

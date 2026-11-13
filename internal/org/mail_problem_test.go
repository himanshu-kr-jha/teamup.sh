package org

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/teamup/teamup/internal/mail"
)

// The mail server's host and port are typed by the administrator, so what
// comes back when it fails is a fixed sentence: the server's own words, or
// "connection refused" against "i/o timeout", would turn the settings page into
// a way to probe the network.
func TestAMailFailureTellsTheAdministratorNothingTheServerSaid(t *testing.T) {
	w := newMailWorld(t)
	r := w.as(t, "admin")

	w.mailer.fail = errorsJoin(mail.ErrUnreachable, fmt.Errorf("could not reach 10.0.0.7:6379: dial tcp: connection refused, banner redis_version:7.2.4"))
	got := send(t, r, http.MethodPost, "/api/org/mail/test", nil)
	if body := got.Body.String(); got.Code != http.StatusOK || !strings.Contains(body, `"problem":"could not connect to the mail server"`) ||
		strings.Contains(body, "10.0.0.7") || strings.Contains(body, "redis_version") {
		t.Errorf("test email, unreachable: %d %s", got.Code, body)
	}

	w.mailer.fail = errorsJoin(mail.ErrSignIn, fmt.Errorf("smtp auth: 535 5.7.8 Username and Password not accepted"))
	got = send(t, r, http.MethodPost, "/api/org/mail/test", nil)
	if body := got.Body.String(); !strings.Contains(body, `"problem":"the mail server refused the sign-in"`) || strings.Contains(body, "535") {
		t.Errorf("test email, sign-in: %s", body)
	}

	w.mailer.fail, w.mailer.failAfter = errorsJoin(mail.ErrUnreachable, fmt.Errorf("could not reach 10.0.0.7:22: SSH-2.0-OpenSSH_9.6")), 0
	added := postMember(t, r, map[string]string{"name": "Pat", "email": "pat@mail.test"})
	if body := added.Body.String(); added.Code != http.StatusCreated || !strings.Contains(body, `"mailProblem":"could not connect to the mail server"`) || strings.Contains(body, "OpenSSH") {
		t.Errorf("welcome email: %d %s", added.Code, body)
	}
}

type joined struct{ kind, err error }

func (j *joined) Error() string   { return j.err.Error() }
func (j *joined) Unwrap() []error { return []error{j.kind, j.err} }

func errorsJoin(kind, err error) error { return &joined{kind, err} }

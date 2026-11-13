package mail

// The mail server's address is typed by an administrator, so it must not be a
// way to reach inside the network TeamUp runs in, and what goes back to the
// browser when sending fails must not describe what answered there.

import (
	"errors"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/teamup/teamup/internal/safedial"
)

// TestMain lets the package's fake SMTP servers on loopback be reached, as a
// self-hoster with a local relay would allow. Tests about refusing internal
// addresses clear the list themselves.
func TestMain(m *testing.M) {
	os.Setenv(safedial.EnvAllow, "127.0.0.1,::1")
	os.Exit(m.Run())
}

func TestMailIsNotSentToAnInternalAddress(t *testing.T) {
	t.Setenv(safedial.EnvAllow, "")
	for _, implicit := range []bool{false, true} {
		f, settings := startFake(t, implicit, !implicit, "secret")
		err := send(settings, Message{To: "ada@example.test", Subject: "Hi", Body: "hello"})
		if !errors.Is(err, safedial.ErrBlocked) || !errors.Is(err, ErrUnreachable) {
			t.Errorf("implicit TLS %v: want a blocked, unreachable error, got %v", implicit, err)
		}
		if f.authed || f.from != "" {
			t.Errorf("implicit TLS %v: the internal server was used", implicit)
		}
	}
}

func TestWhatTheAdministratorIsToldDoesNotDescribeTheServer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	down := send(Settings{Host: "127.0.0.1", Port: port, From: "a@example.test"}, Message{To: "b@example.test"})
	if got := Problem(down); got != "could not connect to the mail server" {
		t.Errorf("a server that is down: %q (from %v)", got, down)
	}

	_, settings := startFake(t, false, true, "secret")
	settings.Pass = "wrong"
	refused := send(settings, Message{To: "ada@example.test", Subject: "Hi", Body: "hello"})
	if got := Problem(refused); got != "the mail server refused the sign-in" {
		t.Errorf("a wrong password: %q (from %v)", got, refused)
	}

	if got := Problem(errors.New("550 5.1.1 <x@internal> no such user; banner: Postfix 3.7 on db-1:" + strconv.Itoa(port))); got != "the mail server did not take the message" {
		t.Errorf("anything else: %q", got)
	}
	if got := Problem(nil); got != "" {
		t.Errorf("no error: %q", got)
	}
}

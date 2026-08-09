package mail

import (
	"io"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func zerolog_() zerolog.Logger { return zerolog.New(io.Discard) }

// Header values are attacker-influenced (a member's name and address), so a
// newline in one must not be able to add headers of its own.
func TestHeadersCannotBeInjected(t *testing.T) {
	msg := compose("noreply@example.test", Message{
		To:      "victim@example.test\r\nBcc: everybody@example.test",
		Subject: "Welcome\r\nX-Injected: yes",
		Body:    "hello",
	})

	// What matters is that neither became a header of its own. Folded into
	// the value they are harmless — the address simply fails to resolve.
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, "Bcc:") {
			t.Error("a newline in the recipient added a Bcc header")
		}
		if strings.HasPrefix(line, "X-Injected:") {
			t.Error("a newline in the subject added a header")
		}
	}

	// Exactly one To header, on one line.
	headers, _, found := strings.Cut(msg, "\r\n\r\n")
	if !found {
		t.Fatal("no header/body separator")
	}
	var toLines int
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(line, "To:") {
			toLines++
		}
	}
	if toLines != 1 {
		t.Errorf("To headers: got %d want 1", toLines)
	}
}

// A line containing only a full stop ends the SMTP data phase, so it has to be
// escaped or the rest of the message is silently dropped.
func TestALoneFullStopIsEscaped(t *testing.T) {
	msg := compose("noreply@example.test", Message{
		To:      "someone@example.test",
		Subject: "Test",
		Body:    "first\n.\nlast",
	})

	if !strings.Contains(msg, "\r\n..\r\n") {
		t.Errorf("a lone full stop was not escaped:\n%s", msg)
	}
	if !strings.Contains(msg, "last") {
		t.Error("the rest of the message was lost")
	}
}

func TestTheWelcomeMessageSaysHowToSignInAndWithWhat(t *testing.T) {
	s := NewService(nil, "https://teamup.example.test/", zerolog_())
	msg := s.WelcomeMessage("Acme", "Ada", "ada@example.test", "temp-password-123")

	for _, want := range []string{
		"Ada",
		"Acme",
		"https://teamup.example.test/login",
		"ada@example.test",
		"temp-password-123",
	} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("the welcome message never mentions %q", want)
		}
	}
	if msg.To != "ada@example.test" {
		t.Errorf("addressed to %q", msg.To)
	}
	// They are told the password is temporary, so they do not file it away.
	if !strings.Contains(msg.Body, "replace it") {
		t.Error("the message does not say the password is temporary")
	}
}

// An installation with no mail server is a supported configuration, not a
// failure to be retried.
func TestSendingWithoutAServerReportsItIsNotConfigured(t *testing.T) {
	if err := send(Settings{}, Message{To: "a@b.test"}); err == nil {
		t.Error("sending with no host should fail")
	}
	if (Settings{Host: "mail.example.test"}).configured() {
		t.Error("a host with no from address is not configured")
	}
	if !(Settings{Host: "mail.example.test", From: "a@b.test"}).configured() {
		t.Error("host and from address is configured")
	}
}

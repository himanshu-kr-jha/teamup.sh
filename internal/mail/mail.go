// Package mail sends the few messages TeamUp needs to put in somebody's inbox.
//
// Settings come from the organization rather than the process, because the
// setup wizard is where they are entered. Sending is best effort by design: an
// installation with no mail server is a supported configuration, and the
// administrator is always shown the credentials on screen as well.
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// ConfigReader reads a stored organization setting. An interface so this
// package does not depend on org.
type ConfigReader interface {
	GetConfigInto(ctx context.Context, orgID uuid.UUID, key string, dst interface{}) error
}

// Settings is the shape stored under the "smtp" key by the setup wizard.
type Settings struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Pass string `json:"pass"`
	From string `json:"from"`
}

func (s Settings) configured() bool { return s.Host != "" && s.From != "" }

type Service struct {
	config ConfigReader
	logger zerolog.Logger
	// baseURL is where people should go to sign in.
	baseURL string
}

func NewService(config ConfigReader, baseURL string, logger zerolog.Logger) *Service {
	return &Service{config: config, baseURL: baseURL, logger: logger}
}

// ErrNotConfigured means this installation has no mail server, which is a
// normal state rather than a failure. Callers report it rather than retry.
var ErrNotConfigured = fmt.Errorf("no mail server is configured")

// Message is one email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Send delivers a message using the organization's own settings.
func (s *Service) Send(ctx context.Context, orgID uuid.UUID, msg Message) error {
	var settings Settings
	if err := s.config.GetConfigInto(ctx, orgID, "smtp", &settings); err != nil {
		return ErrNotConfigured
	}
	if !settings.configured() {
		return ErrNotConfigured
	}
	return send(settings, msg)
}

// Configured reports whether mail can be sent for this organization, so a
// caller can tell somebody up front rather than after the fact.
func (s *Service) Configured(ctx context.Context, orgID uuid.UUID) bool {
	var settings Settings
	if err := s.config.GetConfigInto(ctx, orgID, "smtp", &settings); err != nil {
		return false
	}
	return settings.configured()
}

func send(settings Settings, msg Message) error {
	port := settings.Port
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(settings.Host, strconv.Itoa(port))

	conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	// A server that stops responding must not hold the request open.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	client, err := smtp.NewClient(conn, settings.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: settings.Host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if settings.User != "" {
		if err := client.Auth(smtp.PlainAuth("", settings.User, settings.Pass, settings.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(settings.From); err != nil {
		return fmt.Errorf("sender rejected: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("recipient rejected: %w", err)
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("start message: %w", err)
	}
	if _, err := writer.Write([]byte(compose(settings.From, msg))); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish message: %w", err)
	}
	return client.Quit()
}

// compose builds the RFC 5322 message. The subject is encoded so a name with
// an accent in it does not arrive as mojibake, and header values are stripped
// of newlines so nothing in them can inject extra headers.
func compose(from string, msg Message) string {
	var b strings.Builder
	b.WriteString("From: " + sanitizeHeader(from) + "\r\n")
	b.WriteString("To: " + sanitizeHeader(msg.To) + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", sanitizeHeader(msg.Subject)) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	// A bare "." on its own line ends the data phase, so it has to be doubled.
	body := strings.ReplaceAll(msg.Body, "\r\n", "\n")
	for _, line := range strings.Split(body, "\n") {
		if line == "." {
			line = ".."
		}
		b.WriteString(line + "\r\n")
	}
	return b.String()
}

func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}

// WelcomeMessage is what a new member receives: how to get in, and enough about
// the platform to know what they have been given access to.
func (s *Service) WelcomeMessage(orgName, name, email, temporaryPassword string) Message {
	signIn := strings.TrimSuffix(s.baseURL, "/") + "/login"

	body := fmt.Sprintf(`Hello %s,

You have been added to %s on TeamUp, a private workspace your organization
runs itself for chat, calls and work tracking.

Sign in here:  %s

  Email     %s
  Password  %s

That password was generated for you. You will be asked to replace it with
one of your own the first time you sign in, and it will stop working then,
so there is no need to keep this message.

Once you are in you can:

  • Talk in channels, or one to one with anybody in the organization
  • Share files by attaching them to a message
  • Start or join a video call from any channel
  • See the work assigned to you, and comment on it

If you were not expecting this, tell whoever runs %s — the account exists
already and can be removed.
`, name, orgName, signIn, email, temporaryPassword, orgName)

	return Message{
		To:      email,
		Subject: fmt.Sprintf("Your %s account on TeamUp", orgName),
		Body:    body,
	}
}

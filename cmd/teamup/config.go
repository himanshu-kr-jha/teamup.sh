package main

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// minAuthSecretLength is what separates a key from a password somebody typed.
// setup.sh generates one at random and docs/DEPLOYMENT.md asks for 64 hex
// characters, so nothing this project produces is anywhere near the limit.
const minAuthSecretLength = 32

// Validate checks that the settings the server was handed can actually be
// used, and names the variable that cannot.
//
// `env:"...,required"` asserts only that a variable exists. Everything below
// was accepted as it stands and surfaced somewhere else entirely: a malformed
// TEAMUP_BASE_URL as an OIDC redirect the provider rejects and as broken links
// in outbound mail, a malformed database or Redis URL as a driver error at
// dial time that names no setting, TEAMUP_INSECURE_COOKIES as session tokens
// travelling in the clear on a production deployment.
//
// It runs at boot, before anything is dialled, so a misconfigured server dies
// on the spot rather than on the first request that needs the broken piece.
func (c Config) Validate() error {
	var problems []string
	report := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	// pgx and go-redis are the parsers that will be handed these strings a few
	// lines into runServer. Asking them now costs nothing, accepts every form
	// they accept — a keyword/value DSN as readily as a URL — and turns a
	// failure into a message with the variable's name on it.
	if _, err := pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		report("TEAMUP_DATABASE_URL is not a usable PostgreSQL connection string: %v", err)
	}
	if _, err := redis.ParseURL(c.RedisURL); err != nil {
		report("TEAMUP_REDIS_URL is not a usable Redis URL: %v", err)
	}

	if err := checkWebURL(c.BaseURL, "http", "https"); err != nil {
		report("TEAMUP_BASE_URL %v; it is the base for OIDC redirects and for the links in outbound email", err)
	}
	// Written as a websocket or an http address depending on who wrote it;
	// liveKitProxyTarget accepts either and silently gives up on anything else,
	// which takes the /livekit proxy and the LiveKit readiness check with it.
	if err := checkWebURL(c.LiveKitURL, "ws", "wss", "http", "https"); err != nil {
		report("TEAMUP_LIVEKIT_URL %v", err)
	}

	if err := checkHostPort(c.MinIOEndpoint); err != nil {
		report("TEAMUP_MINIO_ENDPOINT %v", err)
	}

	if err := checkPort(c.Port); err != nil {
		report("TEAMUP_PORT %v", err)
	}
	// The port only matters when there is a host to send mail to.
	if c.SMTPHost != "" && (c.SMTPPort < 1 || c.SMTPPort > 65535) {
		report("TEAMUP_SMTP_PORT %d is not a port number", c.SMTPPort)
	}

	// Optional, but a short one is worse than none: it looks like the stored
	// signing key is protected when it is guessable.
	if secret := strings.TrimSpace(c.AuthSecret); secret != "" && len(secret) < minAuthSecretLength {
		report("TEAMUP_AUTH_SECRET is %d characters; it wraps the stored signing key and needs at least %d (generate one with `openssl rand -hex 32`)",
			len(secret), minAuthSecretLength)
	}

	if c.InsecureCookies && c.Environment == "production" {
		problems = append(problems,
			"TEAMUP_INSECURE_COOKIES=true drops the Secure flag from session cookies, which sends them in the clear; it cannot be used with TEAMUP_ENV=production")
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

// checkWebURL requires an absolute URL with one of the given schemes. The
// error reads as the tail of a sentence beginning with the variable's name.
func checkWebURL(raw string, schemes ...string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("is not a URL: %v", err)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%q has no host; it must be an absolute URL such as %s://teamup.example.com", raw, schemes[0])
	}
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			return nil
		}
	}
	return fmt.Errorf("%q must use one of these schemes: %s", raw, strings.Join(schemes, ", "))
}

// checkHostPort validates an object storage endpoint. MinIO's client wants a
// bare host with an optional port — a scheme makes it fail with "Endpoint url
// cannot have fully qualified paths", which says nothing about which setting
// is at fault or that TEAMUP_MINIO_SECURE is where the scheme belongs.
func checkHostPort(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("is empty")
	}
	if strings.Contains(raw, "://") {
		return fmt.Errorf("%q must be a host and port with no scheme, such as minio:9000; use TEAMUP_MINIO_SECURE to choose TLS", raw)
	}
	if strings.Contains(raw, "/") {
		return fmt.Errorf("%q must be a host and port with no path", raw)
	}

	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		// No port at all is fine: the client falls back to 80 or 443.
		return nil
	}
	if host == "" {
		return fmt.Errorf("%q has no host", raw)
	}
	if err := checkPort(port); err != nil {
		return fmt.Errorf("%q: port %v", raw, err)
	}
	return nil
}

func checkPort(raw string) error {
	port, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("%q is not a number", raw)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%d is outside the range 1-65535", port)
	}
	return nil
}

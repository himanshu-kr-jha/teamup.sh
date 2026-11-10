// Package gcal keeps each person's Google Calendar and their TeamUp calendar
// in step: meetings in Google Calendar show on TeamUp as busy time, and TeamUp
// meetings are put into Google Calendar, moved and taken out again with them.
//
// Each person connects their own Google account once, with Google's consent
// screen. Their token is sealed under TEAMUP_GCAL_TOKEN_KEY before it is
// stored, so the database alone cannot read anybody's calendar.
package gcal

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/oauth2"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Config is what connecting to Google needs.
type Config struct {
	ClientID     string
	ClientSecret string
	// Seals stored tokens and signs the consent round trip. At least 16 characters.
	TokenKey string
	// Where TeamUp is reached, for the address Google sends people back to.
	BaseURL string
	// Replaces Google's addresses, for tests: the consent page, token and
	// revoke endpoints and the Calendar API all hang off it.
	Endpoint string
	// As auth's: drops Secure from the cookie, for plain-HTTP setups.
	InsecureCookies bool
}

// Enabled says whether everything Google needs has been set up.
func (c Config) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != "" && len(c.TokenKey) >= 16
}

// The scopes asked for: reading and writing events, and who the account is.
var scopes = []string{"openid", "email", "https://www.googleapis.com/auth/calendar.events"}

const (
	// How far back and ahead meetings are read from Google Calendar, and how
	// far ahead TeamUp meetings are put into it.
	lookBack  = 30 * 24 * time.Hour
	lookAhead = 120 * 24 * time.Hour
	// How long somebody has to finish Google's consent screen.
	stateLife = 15 * time.Minute
	// NonceCookie ties the consent round trip to the browser that began it.
	NonceCookie = "gcal_nonce"
	// CallbackPath is where Google sends people back to.
	CallbackPath = "/api/calendar/google/callback"
)

// Service connects people's Google Calendars and keeps them in step.
type Service struct {
	db        *pgxpool.Pool
	cfg       Config
	oauth     *oauth2.Config
	api       string
	revokeURL string
	aead      cipher.AEAD
	stateKey  []byte
	http      *http.Client
	logger    zerolog.Logger
	clock     func() time.Time
	nudges    chan uuid.UUID
	userLocks sync.Map
}

// NewService returns a service. With an incomplete Config it still answers,
// saying Google Calendar is not available.
func NewService(db *pgxpool.Pool, cfg Config) (*Service, error) {
	s := &Service{
		db:     db,
		cfg:    cfg,
		http:   &http.Client{Timeout: 30 * time.Second},
		logger: zerolog.Nop(),
		clock:  time.Now,
		nudges: make(chan uuid.UUID, 256),
	}
	if !cfg.Enabled() {
		return s, nil
	}
	authURL, tokenURL := "https://accounts.google.com/o/oauth2/v2/auth", "https://oauth2.googleapis.com/token"
	s.api, s.revokeURL = "https://www.googleapis.com/calendar/v3", "https://oauth2.googleapis.com/revoke"
	if e := strings.TrimRight(cfg.Endpoint, "/"); e != "" {
		authURL, tokenURL = e+"/o/oauth2/v2/auth", e+"/token"
		s.api, s.revokeURL = e+"/calendar/v3", e+"/revoke"
	}
	s.oauth = &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  strings.TrimRight(cfg.BaseURL, "/") + CallbackPath,
		Scopes:       scopes,
		Endpoint:     oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}
	sealKey, err := derive(cfg.TokenKey, "teamup google calendar tokens")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(sealKey)
	if err != nil {
		return nil, err
	}
	if s.aead, err = cipher.NewGCM(block); err != nil {
		return nil, err
	}
	if s.stateKey, err = derive(cfg.TokenKey, "teamup google calendar consent"); err != nil {
		return nil, err
	}
	return s, nil
}

func derive(secret, purpose string) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(secret), nil, []byte(purpose)), key); err != nil {
		return nil, fmt.Errorf("derive %s key: %w", purpose, err)
	}
	return key, nil
}

func (s *Service) SetLogger(l zerolog.Logger) { s.logger = l.With().Str("component", "gcal").Logger() }

// SetClock replaces what this service takes the time to be, for tests.
func (s *Service) SetClock(now func() time.Time) { s.clock = now }

// Enabled says whether people can connect a Google Calendar here.
func (s *Service) Enabled() bool { return s.oauth != nil }

// ── where somebody stands ─────────────────────────────────────────────────

// Status is what somebody's Google Calendar connection looks like to them.
type Status struct {
	// Available is false until an administrator has set Google up.
	Available    bool       `json:"available"`
	Connected    bool       `json:"connected"`
	Email        string     `json:"email,omitempty"`
	NeedsConsent bool       `json:"needsReconnect,omitempty"`
	LastSyncedAt *time.Time `json:"lastSyncedAt,omitempty"`
	LastError    string     `json:"lastError,omitempty"`
}

func (s *Service) Status(ctx context.Context, userID uuid.UUID) (Status, error) {
	st := Status{Available: s.Enabled()}
	var status string
	err := s.db.QueryRow(ctx, `
		SELECT google_email, status, last_synced_at, last_error FROM google_calendar_links WHERE user_id = $1
	`, userID).Scan(&st.Email, &status, &st.LastSyncedAt, &st.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("read google link: %w", err)
	}
	st.Connected = true
	st.NeedsConsent = status == "reconnect"
	return st, nil
}

// ── connecting ────────────────────────────────────────────────────────────

type consent struct {
	User  uuid.UUID `json:"u"`
	Org   uuid.UUID `json:"o"`
	Nonce string    `json:"n"`
	Until int64     `json:"e"`
}

// ConnectURL is Google's consent page for this person, and the nonce the
// browser must hold for the way back to be accepted.
func (s *Service) ConnectURL(orgID, userID uuid.UUID) (address, nonce string, err error) {
	if !s.Enabled() {
		return "", "", apperrors.New(apperrors.CodeUnavailable, "Google Calendar has not been set up here yet")
	}
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	nonce = base64.RawURLEncoding.EncodeToString(raw)
	payload, _ := json.Marshal(consent{User: userID, Org: orgID, Nonce: nonce, Until: s.clock().Add(stateLife).Unix()})
	body := base64.RawURLEncoding.EncodeToString(payload)
	state := body + "." + base64.RawURLEncoding.EncodeToString(s.sign(body))
	// Offline and consent, so Google hands over a refresh token every time:
	// without one the link dies with the first access token, an hour later.
	address = s.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.SetAuthURLParam("include_granted_scopes", "true"))
	return address, nonce, nil
}

func (s *Service) sign(body string) []byte {
	mac := hmac.New(sha256.New, s.stateKey)
	mac.Write([]byte(body))
	return mac.Sum(nil)
}

// readState checks the state Google handed back was made here, for the browser
// it came back to, and recently.
func (s *Service) readState(state, nonce string) (*consent, error) {
	bad := apperrors.New(apperrors.CodeBadRequest, "that link to Google has expired or was not started here; try connecting again")
	body, sig, ok := strings.Cut(state, ".")
	if !ok {
		return nil, bad
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.sign(body)) {
		return nil, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, bad
	}
	var c consent
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, bad
	}
	if s.clock().Unix() > c.Until || nonce == "" || !hmac.Equal([]byte(nonce), []byte(c.Nonce)) {
		return nil, bad
	}
	return &c, nil
}

// Complete finishes connecting: Google's code becomes a stored token for the
// person who asked, and their calendars are brought into step straight away.
func (s *Service) Complete(ctx context.Context, state, nonce, code string) (uuid.UUID, error) {
	if !s.Enabled() {
		return uuid.Nil, apperrors.New(apperrors.CodeUnavailable, "Google Calendar has not been set up here yet")
	}
	c, err := s.readState(state, nonce)
	if err != nil {
		return uuid.Nil, err
	}
	tok, err := s.oauth.Exchange(s.withClient(ctx), code)
	if err != nil {
		return uuid.Nil, fmt.Errorf("exchange google code: %w", err)
	}
	if tok.RefreshToken == "" {
		return uuid.Nil, apperrors.New(apperrors.CodeBadRequest, "Google did not allow TeamUp to stay connected; try again")
	}
	sealed, err := s.seal(tok)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO google_calendar_links (user_id, org_id, google_email, token, status, last_error)
		SELECT $1, $2, $3, $4, 'ok', '' FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
		ON CONFLICT (user_id) DO UPDATE SET google_email = EXCLUDED.google_email, token = EXCLUDED.token,
		    status = 'ok', last_error = '', updated_at = NOW()
	`, c.User, c.Org, emailOf(tok), sealed); err != nil {
		return uuid.Nil, fmt.Errorf("store google link: %w", err)
	}
	s.Nudge(c.User)
	return c.User, nil
}

// emailOf reads the account's address from the ID token. It came straight from
// Google's token endpoint over TLS, which is what makes it trustworthy without
// checking its signature (OpenID Connect Core 3.1.3.7).
func emailOf(tok *oauth2.Token) string {
	idToken, _ := tok.Extra("id_token").(string)
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(raw, &claims)
	return claims.Email
}

// Disconnect takes TeamUp out of somebody's Google account: the meetings it
// put there come out, Google is told to forget the token, and what was read
// from their calendar is dropped.
func (s *Service) Disconnect(ctx context.Context, userID uuid.UUID) error {
	if client, tok, err := s.client(ctx, userID); err == nil {
		rows, err := s.db.Query(ctx, `SELECT google_event_id FROM google_calendar_pushed WHERE user_id = $1`, userID)
		if err == nil {
			var ids []string
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				_, _, _ = s.call(ctx, client, http.MethodDelete, s.eventURL(id), nil)
			}
		}
		form := url.Values{"token": {tok.RefreshToken}}
		if req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.revokeURL, strings.NewReader(form.Encode())); err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if res, err := s.http.Do(req); err == nil {
				res.Body.Close()
			}
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, q := range []string{
		`DELETE FROM google_calendar_pushed WHERE user_id = $1`,
		`DELETE FROM google_calendar_events WHERE user_id = $1`,
		`DELETE FROM google_calendar_links WHERE user_id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			return fmt.Errorf("disconnect google: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ── tokens ────────────────────────────────────────────────────────────────

func (s *Service) seal(tok *oauth2.Token) ([]byte, error) {
	plain, err := json.Marshal(tok)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

func (s *Service) open(sealed []byte) (*oauth2.Token, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("stored google token is damaged")
	}
	plain, err := s.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, errors.New("stored google token cannot be read with this TEAMUP_GCAL_TOKEN_KEY")
	}
	var tok oauth2.Token
	if err := json.Unmarshal(plain, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func (s *Service) withClient(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, s.http)
}

// errReconnect means Google no longer accepts the token: the person has to
// connect again, which nothing done here can do for them.
var errReconnect = errors.New("Google Calendar needs to be connected again")

// client is an HTTP client acting as this person in Google, with a fresh
// access token, the refreshed token stored for next time.
func (s *Service) client(ctx context.Context, userID uuid.UUID) (*http.Client, *oauth2.Token, error) {
	if !s.Enabled() {
		return nil, nil, errors.New("google calendar is not set up")
	}
	var sealed []byte
	if err := s.db.QueryRow(ctx, `SELECT token FROM google_calendar_links WHERE user_id = $1`, userID).Scan(&sealed); err != nil {
		return nil, nil, err
	}
	stored, err := s.open(sealed)
	if err != nil {
		return nil, nil, err
	}
	fresh, err := s.oauth.TokenSource(s.withClient(ctx), stored).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && (re.ErrorCode == "invalid_grant" || re.Response != nil && re.Response.StatusCode == http.StatusUnauthorized) {
			return nil, nil, errReconnect
		}
		return nil, nil, fmt.Errorf("refresh google token: %w", err)
	}
	if fresh.AccessToken != stored.AccessToken {
		if fresh.RefreshToken == "" {
			fresh.RefreshToken = stored.RefreshToken
		}
		if again, err := s.seal(fresh); err == nil {
			_, _ = s.db.Exec(ctx, `UPDATE google_calendar_links SET token = $2, updated_at = NOW() WHERE user_id = $1`, userID, again)
		}
	}
	client := &http.Client{
		Timeout:   s.http.Timeout,
		Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(fresh), Base: s.http.Transport},
	}
	return client, fresh, nil
}

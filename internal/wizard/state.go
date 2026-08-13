package wizard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Step numbers as persisted in State.CompletedSteps. The UI shows a welcome
// screen first and a summary screen last, neither of which submits data, so
// these are the four steps that actually carry payloads.
const (
	StepOrganization = 1
	StepAdmin        = 2
	StepAuth         = 3
	StepServices     = 4
	StepComplete     = 5
)

// subdomainPattern mirrors the hostname_rfc1123 binding tag and the
// VARCHAR(63) column the value lands in.
var subdomainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// SessionTTL bounds how long a half-finished setup survives. Spec 09 §9.
const SessionTTL = 30 * time.Minute

// SessionCookie holds the opaque id pointing at server-side wizard state.
const SessionCookie = "teamup_wizard"

// State is the accumulated wizard progress for one browser session. It lives
// server-side because the payload (notably a base64 logo) can exceed the 4KB
// cookie limit, and because setup secrets should never ride in a cookie.
type State struct {
	CurrentStep    int               `json:"currentStep"`
	CompletedSteps map[int]bool      `json:"completedSteps"`
	Data           Data              `json:"data"`
	Errors         map[string]string `json:"errors"`
}

func NewState() *State {
	return &State{
		CurrentStep:    StepOrganization,
		CompletedSteps: map[int]bool{},
		Errors:         map[string]string{},
	}
}

// Step payloads. Each carries only the fields its screen collects, so applying
// one cannot blank out a field captured by an earlier step.

type OrganizationStep struct {
	OrgName      string `json:"orgName" binding:"required,min=2,max=100"`
	OrgSubdomain string `json:"orgSubdomain" binding:"required,hostname_rfc1123"`
	OrgLogo      string `json:"orgLogo,omitempty"`
}

type AdminStep struct {
	AdminName     string `json:"adminName" binding:"required"`
	AdminEmail    string `json:"adminEmail" binding:"required,email"`
	AdminPassword string `json:"adminPassword" binding:"required,min=12"`
}

type AuthStep struct {
	OIDCProvider       string `json:"oidcProvider" binding:"required,oneof=google entra custom"`
	GoogleClientID     string `json:"googleClientId,omitempty"`
	GoogleClientSecret string `json:"googleClientSecret,omitempty"`
	EntraClientID      string `json:"entraClientId,omitempty"`
	EntraClientSecret  string `json:"entraClientSecret,omitempty"`
	EntraTenantID      string `json:"entraTenantId,omitempty"`
	CustomIssuer       string `json:"customIssuer,omitempty"`
	CustomClientID     string `json:"customClientId,omitempty"`
	CustomClientSecret string `json:"customClientSecret,omitempty"`
}

type ServicesStep struct {
	MinIOEndpoint    string `json:"minioEndpoint" binding:"required"`
	MinIOAccessKey   string `json:"minioAccessKey" binding:"required"`
	MinIOSecretKey   string `json:"minioSecretKey" binding:"required"`
	MinIOBucket      string `json:"minioBucket" binding:"required"`
	LiveKitURL       string `json:"livekitUrl" binding:"required,url"`
	LiveKitAPIKey    string `json:"livekitApiKey" binding:"required"`
	LiveKitAPISecret string `json:"livekitApiSecret" binding:"required"`
	SMTPHost         string `json:"smtpHost,omitempty"`
	SMTPPort         int    `json:"smtpPort,omitempty"`
	SMTPUser         string `json:"smtpUser,omitempty"`
	SMTPPass         string `json:"smtpPass,omitempty"`
	SMTPFrom         string `json:"smtpFrom,omitempty"`
}

func (s *State) markComplete(step int) {
	if s.CompletedSteps == nil {
		s.CompletedSteps = map[int]bool{}
	}
	s.CompletedSteps[step] = true
	if next := step + 1; next > s.CurrentStep {
		s.CurrentStep = next
	}
}

func (s *State) ApplyOrganization(in OrganizationStep) {
	s.Data.OrgName = in.OrgName
	s.Data.OrgSubdomain = in.OrgSubdomain
	s.Data.OrgLogo = in.OrgLogo
	s.markComplete(StepOrganization)
}

func (s *State) ApplyAdmin(in AdminStep) {
	s.Data.AdminName = in.AdminName
	s.Data.AdminEmail = in.AdminEmail
	s.Data.AdminPassword = in.AdminPassword
	s.markComplete(StepAdmin)
}

func (s *State) ApplyAuth(in AuthStep) {
	s.Data.OIDCProvider = in.OIDCProvider
	s.Data.GoogleClientID = in.GoogleClientID
	s.Data.GoogleClientSecret = in.GoogleClientSecret
	s.Data.EntraClientID = in.EntraClientID
	s.Data.EntraClientSecret = in.EntraClientSecret
	s.Data.EntraTenantID = in.EntraTenantID
	s.Data.CustomIssuer = in.CustomIssuer
	s.Data.CustomClientID = in.CustomClientID
	s.Data.CustomClientSecret = in.CustomClientSecret
	s.markComplete(StepAuth)
}

func (s *State) ApplyServices(in ServicesStep) {
	s.Data.MinIOEndpoint = in.MinIOEndpoint
	s.Data.MinIOAccessKey = in.MinIOAccessKey
	s.Data.MinIOSecretKey = in.MinIOSecretKey
	s.Data.MinIOBucket = in.MinIOBucket
	s.Data.LiveKitURL = in.LiveKitURL
	s.Data.LiveKitAPIKey = in.LiveKitAPIKey
	s.Data.LiveKitAPISecret = in.LiveKitAPISecret
	s.Data.SMTPHost = in.SMTPHost
	s.Data.SMTPPort = in.SMTPPort
	s.Data.SMTPUser = in.SMTPUser
	s.Data.SMTPPass = in.SMTPPass
	s.Data.SMTPFrom = in.SMTPFrom
	s.markComplete(StepServices)
}

// MissingSteps lists the data-carrying steps still outstanding, so the client
// can be sent back to the right screen instead of finalizing a partial setup.
func (s *State) MissingSteps() []int {
	var missing []int
	for _, step := range []int{StepOrganization, StepAdmin, StepAuth, StepServices} {
		if !s.CompletedSteps[step] {
			missing = append(missing, step)
		}
	}
	return missing
}

// Validate is the last gate before anything is written to the database. The
// original code had none, which is how a wizard "completed" with every field
// blank and left the instance unusable.
func (d Data) Validate() error {
	var problems []string

	require := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	require("orgName", d.OrgName)
	require("orgSubdomain", d.OrgSubdomain)
	require("adminName", d.AdminName)
	require("adminEmail", d.AdminEmail)
	require("adminPassword", d.AdminPassword)
	require("oidcProvider", d.OIDCProvider)
	require("minioEndpoint", d.MinIOEndpoint)
	require("minioAccessKey", d.MinIOAccessKey)
	require("minioSecretKey", d.MinIOSecretKey)
	require("minioBucket", d.MinIOBucket)
	require("livekitUrl", d.LiveKitURL)
	require("livekitApiKey", d.LiveKitAPIKey)
	require("livekitApiSecret", d.LiveKitAPISecret)

	if n := len(strings.TrimSpace(d.OrgName)); n > 0 && (n < 2 || n > 100) {
		problems = append(problems, "orgName must be between 2 and 100 characters")
	}
	// The HTTP handlers enforce this through binding tags, but the unattended
	// TEAMUP_SETUP_* path reaches FinalizeSetup through Validate alone. Without
	// the check a malformed value fails later on the VARCHAR(63) column with a
	// raw driver error instead of a usable message.
	if d.OrgSubdomain != "" && !subdomainPattern.MatchString(d.OrgSubdomain) {
		problems = append(problems, "orgSubdomain must be 1-63 characters of lowercase letters, digits or hyphens, and may not start or end with a hyphen")
	}
	if d.AdminEmail != "" {
		if _, err := mail.ParseAddress(d.AdminEmail); err != nil {
			problems = append(problems, "adminEmail must be a valid email address")
		}
	}
	if d.AdminPassword != "" && len(d.AdminPassword) < 12 {
		problems = append(problems, "adminPassword must be at least 12 characters")
	}
	switch d.OIDCProvider {
	case "", "google", "entra", "custom":
	default:
		problems = append(problems, "oidcProvider must be one of google, entra, custom")
	}
	if d.LiveKitURL != "" {
		if u, err := url.Parse(d.LiveKitURL); err != nil || u.Scheme == "" || u.Host == "" {
			problems = append(problems, "livekitUrl must be an absolute URL")
		}
	}
	// A port is optional. This used to be `hostname_port`, which demanded one —
	// and a TLS-fronted deployment addresses object storage as a bare hostname
	// on 443, so the tag rejected exactly the configuration the production
	// compose file now ships. A port is still accepted for a direct MinIO.
	if d.MinIOEndpoint != "" {
		switch {
		case strings.Contains(d.MinIOEndpoint, "://"):
			problems = append(problems, "minioEndpoint must be a host with no scheme, such as storage.example.com or minio:9000")
		case strings.Contains(d.MinIOEndpoint, "/"):
			problems = append(problems, "minioEndpoint must be a host with no path")
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("setup data incomplete: %s", strings.Join(problems, "; "))
	}
	return nil
}

// sessionStore keeps wizard state between requests. Redis backs it in
// production; the in-memory implementation exists so the state machine can be
// tested without infrastructure.
// Load reports whether the id resolved to a stored session. Callers must treat
// a miss as "no session": adopting an unknown, client-supplied id would let an
// attacker plant a session id and then take over the setup it accumulates.
type sessionStore interface {
	Load(ctx context.Context, id string) (*State, bool, error)
	Save(ctx context.Context, id string, state *State, ttl time.Duration) (string, error)
	Delete(ctx context.Context, id string) error
}

func newSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type redisStore struct {
	client *redis.Client
}

func newRedisStore(client *redis.Client) *redisStore {
	return &redisStore{client: client}
}

func (r *redisStore) key(id string) string { return "wizard:session:" + id }

func (r *redisStore) Load(ctx context.Context, id string) (*State, bool, error) {
	if id == "" {
		return NewState(), false, nil
	}
	raw, err := r.client.Get(ctx, r.key(id)).Bytes()
	if err == redis.Nil {
		return NewState(), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	state := NewState()
	if err := json.Unmarshal(raw, state); err != nil {
		// A corrupt session should restart setup, not wedge it permanently.
		return NewState(), false, nil
	}
	if state.CompletedSteps == nil {
		state.CompletedSteps = map[int]bool{}
	}
	if state.Errors == nil {
		state.Errors = map[string]string{}
	}
	return state, true, nil
}

func (r *redisStore) Save(ctx context.Context, id string, state *State, ttl time.Duration) (string, error) {
	if id == "" {
		newID, err := newSessionID()
		if err != nil {
			return "", err
		}
		id = newID
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	if err := r.client.Set(ctx, r.key(id), raw, ttl).Err(); err != nil {
		return "", err
	}
	return id, nil
}

func (r *redisStore) Delete(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	return r.client.Del(ctx, r.key(id)).Err()
}

type memoryStore struct {
	mu    sync.Mutex
	items map[string][]byte
}

func newMemoryStore() *memoryStore {
	return &memoryStore{items: map[string][]byte{}}
}

func (m *memoryStore) Load(ctx context.Context, id string) (*State, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	raw, ok := m.items[id]
	if !ok {
		return NewState(), false, nil
	}
	state := NewState()
	if err := json.Unmarshal(raw, state); err != nil {
		return NewState(), false, nil
	}
	if state.CompletedSteps == nil {
		state.CompletedSteps = map[int]bool{}
	}
	if state.Errors == nil {
		state.Errors = map[string]string{}
	}
	return state, true, nil
}

func (m *memoryStore) Save(ctx context.Context, id string, state *State, ttl time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == "" {
		newID, err := newSessionID()
		if err != nil {
			return "", err
		}
		id = newID
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	m.items[id] = raw
	return id, nil
}

func (m *memoryStore) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, id)
	return nil
}

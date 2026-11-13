// Package integrations connects an organization's GitHub (and later
// Bitbucket) to TeamUp: which repositories make up which project, who is who
// there, and the pull requests and commits that name a task, so tasks move
// with the work and the right people are told.
package integrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/secretbox"
	"github.com/teamup/teamup/internal/tasks"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Config is where TeamUp is and what it seals secrets with.
type Config struct {
	BaseURL string
	// TEAMUP_AUTH_SECRET. Without it nothing can be connected: the app keys
	// GitHub hands over could only be stored readable.
	Secret          string
	InsecureCookies bool
	// GitHub's addresses, replaced in tests.
	GitHubWeb string
	GitHubAPI string
	// Bitbucket's, likewise.
	BitbucketWeb string
	BitbucketAPI string
}

// Notifier tells somebody something, in the app and on their phone.
type Notifier interface {
	Notify(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, data map[string]any) error
}

type Service struct {
	db       *pgxpool.Pool
	cfg      Config
	box      *secretbox.Box
	gh       *github
	tasks    *tasks.Service
	notifier Notifier
	logger   zerolog.Logger
}

func NewService(db *pgxpool.Pool, cfg Config, taskService *tasks.Service) *Service {
	if cfg.GitHubWeb == "" {
		cfg.GitHubWeb = "https://github.com"
	}
	if cfg.GitHubAPI == "" {
		cfg.GitHubAPI = "https://api.github.com"
	}
	if cfg.BitbucketWeb == "" {
		cfg.BitbucketWeb = "https://bitbucket.org"
	}
	if cfg.BitbucketAPI == "" {
		cfg.BitbucketAPI = "https://api.bitbucket.org"
	}
	s := &Service{db: db, cfg: cfg, gh: newGitHub(cfg.GitHubWeb, cfg.GitHubAPI), tasks: taskService, logger: zerolog.Nop()}
	if box, err := secretbox.New(cfg.Secret, "teamup code integrations"); err == nil {
		s.box = box
	}
	return s
}

func (s *Service) SetNotifier(n Notifier) { s.notifier = n }
func (s *Service) SetLogger(l zerolog.Logger) {
	s.logger = l.With().Str("component", "integrations").Logger()
}

// Available says whether anything can be connected on this server.
func (s *Service) Available() bool { return s.box != nil }

func (s *Service) base() string { return strings.TrimRight(s.cfg.BaseURL, "/") }

func randomKey() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ── connections ───────────────────────────────────────────────────────────

type connection struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	Provider     string
	Account      string
	AppID        string
	Installation string
	HookKey      string
	Status       string
	secrets      []byte
}

func (s *Service) loadConnection(ctx context.Context, where string, args ...any) (*connection, error) {
	var c connection
	err := s.db.QueryRow(ctx, `
		SELECT id, org_id, provider, account, app_id, installation, hook_key, status, secrets
		FROM code_connections WHERE `+where, args...).Scan(
		&c.ID, &c.OrgID, &c.Provider, &c.Account, &c.AppID, &c.Installation, &c.HookKey, &c.Status, &c.secrets)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load connection: %w", err)
	}
	return &c, nil
}

// hookKnown says whether a hook key belongs to a connection of that provider.
func (s *Service) hookKnown(ctx context.Context, provider, hookKey string) (bool, error) {
	var known bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM code_connections WHERE hook_key = $1 AND provider = $2)`, hookKey, provider).Scan(&known)
	return known, err
}

func (s *Service) githubSecrets(c *connection) (githubSecrets, error) {
	var sec githubSecrets
	if s.box == nil {
		return sec, secretbox.ErrNoSecret
	}
	err := s.box.Open(c.secrets, &sec)
	return sec, err
}

// installationToken is GitHub's leave to act in the organization's repositories.
func (s *Service) installationToken(ctx context.Context, c *connection) (string, error) {
	if c.Installation == "" {
		return "", apperrors.New(apperrors.CodeConflict, "the GitHub App is not installed yet")
	}
	sec, err := s.githubSecrets(c)
	if err != nil {
		return "", err
	}
	return s.gh.installationToken(ctx, sec, c.Installation)
}

// ConnectionStatus is what an admin sees of a provider.
type ConnectionStatus struct {
	Provider     string     `json:"provider"`
	Available    bool       `json:"available"`
	Status       string     `json:"status"` // none, pending, active, broken
	Account      string     `json:"account,omitempty"`
	AppURL       string     `json:"appUrl,omitempty"`
	InstallURL   string     `json:"installUrl,omitempty"`
	Repositories int        `json:"repositories"`
	LastError    string     `json:"lastError,omitempty"`
	ConnectedAt  *time.Time `json:"connectedAt,omitempty"`
	// For Bitbucket, made by hand: where its consumer sends people back to,
	// and what it must be allowed.
	CallbackURL string   `json:"callbackUrl,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

func (s *Service) Connections(ctx context.Context, orgID uuid.UUID) ([]ConnectionStatus, error) {
	out := []ConnectionStatus{}
	for _, provider := range []string{"github", "bitbucket"} {
		st := ConnectionStatus{Provider: provider, Available: s.Available(), Status: "none"}
		if provider == "bitbucket" {
			st.CallbackURL, st.Permissions = s.BitbucketCallbackURL(), BitbucketPermissions
		}
		var id uuid.UUID
		var sealed []byte
		var created time.Time
		err := s.db.QueryRow(ctx, `
			SELECT id, account, status, last_error, secrets, created_at,
			       (SELECT count(*) FROM code_repositories r WHERE r.connection_id = c.id AND r.present)
			FROM code_connections c WHERE org_id = $1 AND provider = $2
		`, orgID, provider).Scan(&id, &st.Account, &st.Status, &st.LastError, &sealed, &created, &st.Repositories)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			st.ConnectedAt = &created
			if s.box != nil && provider == "github" {
				var sec githubSecrets
				if s.box.Open(sealed, &sec) == nil && sec.Slug != "" {
					st.AppURL = s.cfg.GitHubWeb + "/apps/" + sec.Slug
					st.InstallURL = st.AppURL + "/installations/new"
				}
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// ── making the GitHub App (the manifest flow) ─────────────────────────────

// githubPermissions is what the app asks for. A manifest takes repository and
// organization permissions only: an account permission, such as reading
// people's email addresses, makes GitHub refuse the whole manifest.
var githubPermissions = map[string]string{
	"metadata":       "read",
	"contents":       "read",
	"pull_requests":  "read",
	"issues":         "read", // a pull request's conversation is its issue's comments
	"checks":         "read",
	"members":        "read",
	"administration": "write", // giving and taking away access to a repository
}

// githubEvents is what the app is sent. GitHub refuses an event the
// permissions above do not cover.
var githubEvents = []string{"pull_request", "pull_request_review", "push", "check_suite", "issue_comment", "member", "repository"}

type manifestState struct {
	Org     uuid.UUID `json:"o"`
	User    uuid.UUID `json:"u"`
	Nonce   string    `json:"n"`
	HookKey string    `json:"h"`
}

// ErrManifestExpired is a way back from GitHub that cannot be matched to a
// start in this browser. GitHub may have made the app all the same.
var ErrManifestExpired = apperrors.New(apperrors.CodeBadRequest, "that link has expired or was not started here; start connecting GitHub again")

// GitHubManifest begins making TeamUp's GitHub App on the admin's GitHub
// organization or personal account. The browser posts the manifest to
// GitHub, where the admin confirms; GitHub sends them back with a code that
// becomes the app's keys. Nothing has to be copied by hand.
//
// keep is what the browser holds on to (as a cookie) until it is back: the
// whole signed state, so the way back does not depend on GitHub returning it.
func (s *Service) GitHubManifest(ctx context.Context, orgID, userID uuid.UUID, ghOrg string) (action string, manifest string, keep string, err error) {
	if !s.Available() {
		return "", "", "", apperrors.New(apperrors.CodeUnavailable, "set TEAMUP_AUTH_SECRET on the server before connecting GitHub")
	}
	ghOrg = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(ghOrg, "https://github.com/"), "@"))
	ghOrg = strings.Trim(ghOrg, "/")
	if ghOrg == "" || strings.ContainsAny(ghOrg, " /?#") {
		return "", "", "", apperrors.New(apperrors.CodeBadRequest, "give the GitHub organization or account name, as in github.com/<name>")
	}
	if existing, err := s.loadConnection(ctx, `org_id = $1 AND provider = 'github'`, orgID); err == nil && existing.Status == "active" {
		return "", "", "", apperrors.New(apperrors.CodeConflict, "GitHub is connected already; disconnect it first to start again")
	}

	// A personal account has its own page for new apps; the organization
	// page does not exist for it.
	newAppPage := "/organizations/" + url.PathEscape(ghOrg) + "/settings/apps/new"
	switch kind, err := s.gh.accountType(ctx, ghOrg); {
	case isStatus(err, http.StatusNotFound):
		return "", "", "", apperrors.New(apperrors.CodeBadRequest, "GitHub has no organization or account named "+ghOrg)
	case err == nil && kind == "User":
		newAppPage = "/settings/apps/new"
	}

	var orgName string
	_ = s.db.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1`, orgID).Scan(&orgName)
	hookKey := randomKey()
	state, err := s.box.Token(manifestState{Org: orgID, User: userID, Nonce: randomKey(), HookKey: hookKey}, time.Hour)
	if err != nil {
		return "", "", "", err
	}
	name := "TeamUp " + orgName
	if len(name) > 34 {
		name = name[:34]
	}
	m := map[string]any{
		"name":        strings.TrimSpace(name),
		"url":         s.base(),
		"description": "Links pull requests and commits to TeamUp tasks, and tells people when their review is wanted.",
		"public":      false,
		"hook_attributes": map[string]any{
			"url":    s.base() + "/api/integrations/github/hook/" + hookKey,
			"active": true,
		},
		"redirect_url":        s.base() + "/api/integrations/github/created",
		"callback_urls":       []string{s.base() + "/api/integrations/github/identity/callback"},
		"setup_url":           s.base() + "/api/integrations/github/installed/" + hookKey,
		"setup_on_update":     true,
		"default_permissions": githubPermissions,
		"default_events":      githubEvents,
	}
	raw, _ := json.Marshal(m)
	action = s.cfg.GitHubWeb + newAppPage + "?state=" + url.QueryEscape(state)
	return action, string(raw), state, nil
}

// GitHubCreated takes GitHub's code for the app just made, swaps it for the
// app's keys, and stores them. Returns where to send the admin next: GitHub's
// page for installing the app on the account's repositories.
//
// kept is what GitHubManifest gave the browser. GitHub does not always hand
// the state back (a personal account's page has been seen to drop it), so the
// browser's copy is what counts; a state that does come back must match it.
func (s *Service) GitHubCreated(ctx context.Context, state, kept, code string) (string, error) {
	if !s.Available() {
		return "", secretbox.ErrNoSecret
	}
	var st manifestState
	if kept == "" || (state != "" && state != kept) || s.box.ReadToken(kept, &st) != nil {
		return "", ErrManifestExpired
	}
	if code == "" {
		return "", apperrors.New(apperrors.CodeBadRequest, "GitHub sent no code back")
	}
	var res struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
		PEM           string `json:"pem"`
		Owner         ghUser `json:"owner"`
	}
	if err := s.gh.do(ctx, "", http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", nil, &res); err != nil {
		return "", fmt.Errorf("finish making the GitHub App: %w", err)
	}
	sealed, err := s.box.Seal(githubSecrets{AppID: res.ID, Slug: res.Slug, ClientID: res.ClientID,
		ClientSecret: res.ClientSecret, WebhookSecret: res.WebhookSecret, PrivateKey: res.PEM})
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO code_connections (org_id, provider, account, app_id, hook_key, secrets, status, created_by)
		VALUES ($1, 'github', $2, $3, $4, $5, 'pending', $6)
		ON CONFLICT (org_id, provider) DO UPDATE SET account = EXCLUDED.account, app_id = EXCLUDED.app_id,
		    hook_key = EXCLUDED.hook_key, secrets = EXCLUDED.secrets, installation = '', status = 'pending',
		    last_error = '', created_by = EXCLUDED.created_by, updated_at = NOW()
	`, st.Org, res.Owner.Login, fmt.Sprint(res.ID), st.HookKey, sealed, st.User); err != nil {
		return "", fmt.Errorf("store github app: %w", err)
	}
	return s.cfg.GitHubWeb + "/apps/" + url.PathEscape(res.Slug) + "/installations/new", nil
}

// GitHubInstalled records where the app was installed, once GitHub says it
// was. The address names the connection; the installation is checked with
// GitHub as the app itself, so nobody can attach somebody else's.
func (s *Service) GitHubInstalled(ctx context.Context, hookKey, installation string) error {
	c, err := s.loadConnection(ctx, `hook_key = $1 AND provider = 'github'`, hookKey)
	if err != nil {
		return err
	}
	sec, err := s.githubSecrets(c)
	if err != nil {
		return err
	}
	appToken, err := s.gh.appJWT(sec)
	if err != nil {
		return err
	}
	var inst struct {
		ID      int64  `json:"id"`
		AppID   int64  `json:"app_id"`
		Account ghUser `json:"account"`
	}
	if err := s.gh.do(ctx, "Bearer "+appToken, http.MethodGet, "/app/installations/"+url.PathEscape(installation), nil, &inst); err != nil {
		return fmt.Errorf("check the installation with github: %w", err)
	}
	if inst.AppID != sec.AppID {
		return apperrors.New(apperrors.CodeForbidden, "that installation is not of this organization's app")
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE code_connections SET installation = $2, account = $3, status = 'active', last_error = '', updated_at = NOW()
		WHERE id = $1
	`, c.ID, fmt.Sprint(inst.ID), inst.Account.Login); err != nil {
		return err
	}
	c.Installation, c.Status = fmt.Sprint(inst.ID), "active"
	return s.syncRepositories(ctx, c)
}

// syncRepositories brings the list of repositories in line with what the
// installation can see now.
func (s *Service) syncRepositories(ctx context.Context, c *connection) error {
	token, err := s.installationToken(ctx, c)
	if err != nil {
		return err
	}
	repos, err := s.gh.installationRepos(ctx, token)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE code_repositories SET present = FALSE WHERE connection_id = $1`, c.ID); err != nil {
		return err
	}
	for _, r := range repos {
		if err := upsertRepo(ctx, tx, c, r.info()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type execer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// repoInfo is a repository as any provider describes it.
type repoInfo struct {
	ExternalID string
	FullName   string
	URL        string
	Private    bool
}

func (r ghRepo) info() repoInfo {
	address := r.HTMLURL
	if address == "" {
		address = "https://github.com/" + r.FullName
	}
	return repoInfo{ExternalID: fmt.Sprint(r.ID), FullName: r.FullName, URL: address, Private: r.Private}
}

func upsertRepo(ctx context.Context, db execer, c *connection, r repoInfo) error {
	var id uuid.UUID
	return db.QueryRow(ctx, `
		INSERT INTO code_repositories (org_id, connection_id, provider, external_id, full_name, html_url, private, present)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
		ON CONFLICT (connection_id, external_id) DO UPDATE SET full_name = EXCLUDED.full_name,
		    html_url = EXCLUDED.html_url, private = EXCLUDED.private, present = TRUE
		RETURNING id
	`, c.OrgID, c.ID, c.Provider, r.ExternalID, r.FullName, r.URL, r.Private).Scan(&id)
}

// SyncNow re-reads the repositories, for an admin who just changed which ones
// TeamUp may see.
func (s *Service) SyncNow(ctx context.Context, orgID uuid.UUID, provider string) error {
	c, err := s.loadConnection(ctx, `org_id = $1 AND provider = $2`, orgID, provider)
	if err != nil {
		return err
	}
	if provider == "bitbucket" {
		return s.syncBitbucketRepositories(ctx, c)
	}
	return s.syncRepositories(ctx, c)
}

// Disconnect forgets the connection and everything read through it. The app
// stays on GitHub until removed there; the admin is told where.
func (s *Service) Disconnect(ctx context.Context, orgID uuid.UUID, provider string) error {
	c, err := s.loadConnection(ctx, `org_id = $1 AND provider = $2`, orgID, provider)
	if err != nil {
		return err
	}
	s.gh.forget(c.Installation)
	if provider == "bitbucket" {
		s.removeBitbucketWebhook(ctx, c)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM code_identities WHERE org_id = $1 AND provider = $2`, orgID, provider); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM code_connections WHERE id = $1`, c.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ── repositories and projects ─────────────────────────────────────────────

type Repository struct {
	ID        uuid.UUID  `json:"id"`
	Provider  string     `json:"provider"`
	FullName  string     `json:"fullName"`
	URL       string     `json:"url"`
	Private   bool       `json:"private"`
	ProjectID *uuid.UUID `json:"projectId,omitempty"`
}

// Repositories lists what the organization's connections can see.
func (s *Service) Repositories(ctx context.Context, orgID uuid.UUID) ([]Repository, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, provider, full_name, html_url, private, project_id FROM code_repositories
		WHERE org_id = $1 AND present ORDER BY LOWER(full_name)
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Repository{}
	for rows.Next() {
		var r Repository
		if err := rows.Scan(&r.ID, &r.Provider, &r.FullName, &r.URL, &r.Private, &r.ProjectID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetProjectRepositories makes these the repositories of a project. A
// repository belongs to one project at a time, so one moved here leaves the
// project it was in.
func (s *Service) SetProjectRepositories(ctx context.Context, orgID, projectID uuid.UUID, repoIDs []uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
		projectID, orgID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return apperrors.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE code_repositories SET project_id = NULL WHERE org_id = $1 AND project_id = $2`, orgID, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE code_repositories SET project_id = $2 WHERE org_id = $1 AND id = ANY($3)`, orgID, projectID, repoIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

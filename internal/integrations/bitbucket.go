package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Bitbucket has nothing like GitHub's app manifest: an owner of the workspace
// makes an OAuth consumer in its settings and gives TeamUp the key and secret.
// TeamUp then acts as that owner, who alone may add the workspace's webhook,
// with a refresh token that turns over every time it is used.

// bitbucketSecrets is what a Bitbucket connection needs. Sealed when stored.
type bitbucketSecrets struct {
	Workspace     string    `json:"workspace"`
	Key           string    `json:"key"`
	Secret        string    `json:"secret"`
	WebhookSecret string    `json:"webhookSecret"`
	WebhookID     string    `json:"webhookId,omitempty"`
	AccessToken   string    `json:"accessToken,omitempty"`
	RefreshToken  string    `json:"refreshToken,omitempty"`
	Expires       time.Time `json:"expires,omitempty"`
}

// The events a workspace webhook is asked for.
var bitbucketEvents = []string{
	"repo:push", "repo:commit_status_created", "repo:commit_status_updated", "repo:created", "repo:deleted",
	"pullrequest:created", "pullrequest:updated", "pullrequest:approved", "pullrequest:unapproved",
	"pullrequest:changes_request_created", "pullrequest:changes_request_removed",
	"pullrequest:fulfilled", "pullrequest:rejected", "pullrequest:comment_created",
}

// BitbucketPermissions is what the consumer must be allowed, said the way
// Bitbucket's form says it.
var BitbucketPermissions = []string{
	"Account: Email, Read",
	"Workspace membership: Read",
	"Projects: Read",
	"Repositories: Read, Admin",
	"Pull requests: Read",
	"Webhooks: Read and write",
}

func (s *Service) bbWeb() string { return strings.TrimRight(s.cfg.BitbucketWeb, "/") }
func (s *Service) bbAPI() string { return strings.TrimRight(s.cfg.BitbucketAPI, "/") + "/2.0" }

// BitbucketCallbackURL is the address the consumer must send people back to.
func (s *Service) BitbucketCallbackURL() string {
	return s.base() + "/api/integrations/bitbucket/callback"
}

type bitbucketState struct {
	Purpose string    `json:"p"` // connect or identity
	Org     uuid.UUID `json:"o"`
	User    uuid.UUID `json:"u"`
	Nonce   string    `json:"n"`
}

var workspaceSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,61}$`)

// BitbucketStart keeps the consumer an admin made, and sends them to
// Bitbucket to sign in as an owner of the workspace.
func (s *Service) BitbucketStart(ctx context.Context, orgID, userID uuid.UUID, workspace, key, secret string) (string, string, error) {
	if !s.Available() {
		return "", "", apperrors.New(apperrors.CodeUnavailable, "set TEAMUP_AUTH_SECRET on the server before connecting Bitbucket")
	}
	workspace = strings.ToLower(strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(workspace), "https://"), "bitbucket.org/")), "/"))
	key, secret = strings.TrimSpace(key), strings.TrimSpace(secret)
	if !workspaceSlug.MatchString(workspace) {
		return "", "", apperrors.New(apperrors.CodeBadRequest, "give the workspace's name as in bitbucket.org/<workspace>")
	}
	if key == "" || secret == "" {
		return "", "", apperrors.New(apperrors.CodeBadRequest, "give the consumer's key and secret")
	}
	if existing, err := s.loadConnection(ctx, `org_id = $1 AND provider = 'bitbucket'`, orgID); err == nil && existing.Status == "active" {
		return "", "", apperrors.New(apperrors.CodeConflict, "Bitbucket is connected already; disconnect it first to start again")
	}
	sealed, err := s.box.Seal(bitbucketSecrets{Workspace: workspace, Key: key, Secret: secret, WebhookSecret: randomKey()})
	if err != nil {
		return "", "", err
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO code_connections (org_id, provider, account, hook_key, secrets, status, created_by)
		VALUES ($1, 'bitbucket', $2, $3, $4, 'pending', $5)
		ON CONFLICT (org_id, provider) DO UPDATE SET account = EXCLUDED.account, hook_key = EXCLUDED.hook_key,
		    secrets = EXCLUDED.secrets, status = 'pending', last_error = '', created_by = EXCLUDED.created_by, updated_at = NOW()
	`, orgID, workspace, randomKey(), sealed, userID); err != nil {
		return "", "", fmt.Errorf("store bitbucket consumer: %w", err)
	}
	return s.bitbucketAuthorize(orgID, userID, "connect", key)
}

func (s *Service) bitbucketAuthorize(orgID, userID uuid.UUID, purpose, key string) (string, string, error) {
	nonce := randomKey()
	state, err := s.box.Token(bitbucketState{Purpose: purpose, Org: orgID, User: userID, Nonce: nonce}, 15*time.Minute)
	if err != nil {
		return "", "", err
	}
	q := url.Values{"client_id": {key}, "response_type": {"code"}, "state": {state}}
	return s.bbWeb() + "/site/oauth2/authorize?" + q.Encode(), nonce, nil
}

// BitbucketIdentityURL is Bitbucket's page where somebody signs in to prove
// which Bitbucket account is theirs.
func (s *Service) BitbucketIdentityURL(ctx context.Context, orgID, userID uuid.UUID) (string, string, error) {
	c, err := s.loadConnection(ctx, `org_id = $1 AND provider = 'bitbucket' AND status = 'active'`, orgID)
	if err != nil {
		return "", "", apperrors.New(apperrors.CodeConflict, "your organization has not connected Bitbucket yet")
	}
	sec, err := s.bitbucketSecrets(c)
	if err != nil {
		return "", "", err
	}
	return s.bitbucketAuthorize(orgID, userID, "identity", sec.Key)
}

func (s *Service) bitbucketSecrets(c *connection) (bitbucketSecrets, error) {
	var sec bitbucketSecrets
	if s.box == nil {
		return sec, apperrors.New(apperrors.CodeUnavailable, "integrations are not set up on this server")
	}
	err := s.box.Open(c.secrets, &sec)
	return sec, err
}

func (s *Service) saveBitbucketSecrets(ctx context.Context, c *connection, sec bitbucketSecrets) error {
	sealed, err := s.box.Seal(sec)
	if err != nil {
		return err
	}
	c.secrets = sealed
	_, err = s.db.Exec(ctx, `UPDATE code_connections SET secrets = $2, updated_at = NOW() WHERE id = $1`, c.ID, sealed)
	return err
}

type bbToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

// bbToken asks Bitbucket for a token, with the consumer as the client.
func (s *Service) bbToken(ctx context.Context, sec bitbucketSecrets, form url.Values) (*bbToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.bbWeb()+"/site/oauth2/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(sec.Key, sec.Secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.gh.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach bitbucket: %w", err)
	}
	defer res.Body.Close()
	var tok bbToken
	if err := decodeJSON(res, &tok); err != nil {
		return nil, fmt.Errorf("read bitbucket's answer: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("bitbucket refused: %s %s", tok.Error, tok.Description)
	}
	return &tok, nil
}

// bbDo makes one call to Bitbucket's API with a bearer token.
func (s *Service) bbDo(ctx context.Context, token, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	address := path
	if !strings.HasPrefix(path, "http") {
		address = s.bbAPI() + path
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.gh.http.Do(req)
	if err != nil {
		return fmt.Errorf("reach bitbucket: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		text := string(raw)
		if len(text) > 300 {
			text = text[:300]
		}
		return &apiError{Status: res.StatusCode, Body: text}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// ownerToken is a fresh token for the owner who connected the workspace. Each
// refresh hands back a new refresh token and retires the old one, so the new
// one is stored before anything else is done.
func (s *Service) ownerToken(ctx context.Context, c *connection) (string, *bitbucketSecrets, error) {
	sec, err := s.bitbucketSecrets(c)
	if err != nil {
		return "", nil, err
	}
	if sec.AccessToken != "" && time.Now().Before(sec.Expires) {
		return sec.AccessToken, &sec, nil
	}
	if sec.RefreshToken == "" {
		return "", nil, apperrors.New(apperrors.CodeConflict, "Bitbucket is not signed in yet")
	}
	tok, err := s.bbToken(ctx, sec, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {sec.RefreshToken}})
	if err != nil {
		_, _ = s.db.Exec(ctx, `UPDATE code_connections SET status = 'broken', last_error = $2, updated_at = NOW() WHERE id = $1`,
			c.ID, "Bitbucket no longer accepts TeamUp's sign-in. Connect it again.")
		return "", nil, err
	}
	sec.AccessToken, sec.Expires = tok.AccessToken, time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second-5*time.Minute)
	if tok.RefreshToken != "" {
		sec.RefreshToken = tok.RefreshToken
	}
	if err := s.saveBitbucketSecrets(ctx, c, sec); err != nil {
		return "", nil, err
	}
	return sec.AccessToken, &sec, nil
}

// ── what Bitbucket sends and answers ──────────────────────────────────────

type bbLinks struct {
	HTML struct {
		Href string `json:"href"`
	} `json:"html"`
}

type bbAccount struct {
	UUID        string `json:"uuid"`
	AccountID   string `json:"account_id"`
	Nickname    string `json:"nickname"`
	DisplayName string `json:"display_name"`
}

func (a bbAccount) name() string { return firstNonEmpty(a.DisplayName, a.Nickname) }

type bbRepo struct {
	UUID      string  `json:"uuid"`
	FullName  string  `json:"full_name"`
	IsPrivate bool    `json:"is_private"`
	Links     bbLinks `json:"links"`
}

func (r bbRepo) info() repoInfo {
	address := r.Links.HTML.Href
	if address == "" {
		address = "https://bitbucket.org/" + r.FullName
	}
	return repoInfo{ExternalID: r.UUID, FullName: r.FullName, URL: address, Private: r.IsPrivate}
}

type bbBranch struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
}

type bbPull struct {
	ID           int         `json:"id"`
	Title        string      `json:"title"`
	Description  string      `json:"description"`
	State        string      `json:"state"`
	Draft        bool        `json:"draft"`
	Author       bbAccount   `json:"author"`
	Source       bbBranch    `json:"source"`
	Destination  bbBranch    `json:"destination"`
	Reviewers    []bbAccount `json:"reviewers"`
	Links        bbLinks     `json:"links"`
	CreatedOn    time.Time   `json:"created_on"`
	UpdatedOn    time.Time   `json:"updated_on"`
	Participants []struct {
		User     bbAccount `json:"user"`
		Role     string    `json:"role"`
		Approved bool      `json:"approved"`
		State    string    `json:"state"`
	} `json:"participants"`
}

func (p bbPull) info() pullInfo {
	out := pullInfo{Number: p.ID, Title: p.Title, Body: p.Description, URL: p.Links.HTML.Href,
		Head: p.Source.Branch.Name, Base: p.Destination.Branch.Name,
		AuthorLogin: p.Author.name(), AuthorKey: p.Author.UUID, OpenedAt: p.CreatedOn}
	updated := p.UpdatedOn
	switch strings.ToUpper(p.State) {
	case "MERGED":
		out.State, out.MergedAt, out.ClosedAt = "merged", &updated, &updated
	case "DECLINED", "SUPERSEDED":
		out.State, out.ClosedAt = "closed", &updated
	default:
		out.State = "open"
		if p.Draft {
			out.State = "draft"
		}
	}
	return out
}

// ── finishing the sign-in ─────────────────────────────────────────────────

// BitbucketDone takes Bitbucket's code: for the owner connecting the
// workspace, it checks they own it, adds the webhook and lists the
// repositories; for anybody else, it links their account. Returns the purpose
// and where to send them.
func (s *Service) BitbucketDone(ctx context.Context, state, nonce, code string) (string, error) {
	if !s.Available() {
		return "", apperrors.New(apperrors.CodeUnavailable, "integrations are not set up on this server")
	}
	var st bitbucketState
	if err := s.box.ReadToken(state, &st); err != nil || nonce == "" || nonce != st.Nonce {
		return "identity", apperrors.New(apperrors.CodeBadRequest, "that link has expired or was not started here; try again")
	}
	c, err := s.loadConnection(ctx, `org_id = $1 AND provider = 'bitbucket'`, st.Org)
	if err != nil {
		return st.Purpose, err
	}
	sec, err := s.bitbucketSecrets(c)
	if err != nil {
		return st.Purpose, err
	}
	tok, err := s.bbToken(ctx, sec, url.Values{"grant_type": {"authorization_code"}, "code": {code}})
	if err != nil {
		return st.Purpose, err
	}
	if st.Purpose == "identity" {
		return st.Purpose, s.linkBitbucket(ctx, st, tok.AccessToken)
	}

	var perm struct {
		Permission string `json:"permission"`
	}
	if err := s.bbDo(ctx, tok.AccessToken, http.MethodGet, "/user/workspaces/"+url.PathEscape(sec.Workspace)+"/permission", nil, &perm); err != nil {
		return st.Purpose, fmt.Errorf("check the workspace with bitbucket: %w", err)
	}
	if perm.Permission != "owner" {
		return st.Purpose, apperrors.New(apperrors.CodeForbidden, "only an owner of the Bitbucket workspace can connect it")
	}
	sec.AccessToken, sec.RefreshToken = tok.AccessToken, tok.RefreshToken
	sec.Expires = time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second - 5*time.Minute)

	var hook struct {
		UUID string `json:"uuid"`
	}
	if err := s.bbDo(ctx, tok.AccessToken, http.MethodPost, "/workspaces/"+url.PathEscape(sec.Workspace)+"/hooks", map[string]any{
		"description": "TeamUp",
		"url":         s.base() + "/api/integrations/bitbucket/hook/" + c.HookKey,
		"active":      true,
		"secret":      sec.WebhookSecret,
		"events":      bitbucketEvents,
	}, &hook); err != nil {
		return st.Purpose, fmt.Errorf("add the workspace webhook: %w", err)
	}
	sec.WebhookID = hook.UUID
	if err := s.saveBitbucketSecrets(ctx, c, sec); err != nil {
		return st.Purpose, err
	}
	if _, err := s.db.Exec(ctx, `UPDATE code_connections SET status = 'active', last_error = '', updated_at = NOW() WHERE id = $1`, c.ID); err != nil {
		return st.Purpose, err
	}
	c.Status = "active"
	return st.Purpose, s.syncBitbucketRepositories(ctx, c)
}

func (s *Service) linkBitbucket(ctx context.Context, st bitbucketState, token string) error {
	var me bbAccount
	if err := s.bbDo(ctx, token, http.MethodGet, "/user", nil, &me); err != nil {
		return err
	}
	var emails struct {
		Values []struct {
			Email       string `json:"email"`
			IsConfirmed bool   `json:"is_confirmed"`
		} `json:"values"`
	}
	_ = s.bbDo(ctx, token, http.MethodGet, "/user/emails", nil, &emails)
	confirmed := []string{}
	for _, e := range emails.Values {
		if e.IsConfirmed {
			confirmed = append(confirmed, strings.ToLower(e.Email))
		}
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO code_identities (user_id, org_id, provider, external_id, login, account_id, emails)
		VALUES ($1, $2, 'bitbucket', $3, $4, $5, $6)
		ON CONFLICT (user_id, provider) DO UPDATE SET external_id = EXCLUDED.external_id, login = EXCLUDED.login,
		    account_id = EXCLUDED.account_id, emails = EXCLUDED.emails, created_at = NOW()
	`, st.User, st.Org, me.UUID, firstNonEmpty(me.Nickname, me.DisplayName), me.AccountID, confirmed); err != nil {
		if strings.Contains(err.Error(), "code_identities_org_id_provider_external_id_key") {
			return apperrors.New(apperrors.CodeConflict, "that Bitbucket account is linked to somebody else here")
		}
		return err
	}
	// Work already recorded under this account becomes theirs.
	_, _ = s.db.Exec(ctx, `
		UPDATE code_commits c SET author_id = $1 FROM code_repositories r
		WHERE r.id = c.repository_id AND r.provider = 'bitbucket' AND c.org_id = $2 AND c.author_id IS NULL AND LOWER(c.author_email) = ANY($3)`,
		st.User, st.Org, confirmed)
	s.findAfterLinking(ctx, st.Org, st.User)
	return nil
}

// syncBitbucketRepositories brings the list in line with the workspace.
func (s *Service) syncBitbucketRepositories(ctx context.Context, c *connection) error {
	token, sec, err := s.ownerToken(ctx, c)
	if err != nil {
		return err
	}
	var repos []bbRepo
	next := "/repositories/" + url.PathEscape(sec.Workspace) + "?pagelen=100"
	for page := 0; next != "" && page < 50; page++ {
		var res struct {
			Values []bbRepo `json:"values"`
			Next   string   `json:"next"`
		}
		if err := s.bbDo(ctx, token, http.MethodGet, next, nil, &res); err != nil {
			return err
		}
		repos = append(repos, res.Values...)
		next = res.Next
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

// removeBitbucketWebhook takes TeamUp's webhook off the workspace, as the
// connection is forgotten. Best effort.
func (s *Service) removeBitbucketWebhook(ctx context.Context, c *connection) {
	token, sec, err := s.ownerToken(ctx, c)
	if err != nil || sec.WebhookID == "" {
		return
	}
	_ = s.bbDo(ctx, token, http.MethodDelete, "/workspaces/"+url.PathEscape(sec.Workspace)+"/hooks/"+url.PathEscape(sec.WebhookID), nil, nil)
}

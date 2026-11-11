package integrations

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// ── somebody's own GitHub account ─────────────────────────────────────────

type identityState struct {
	Org   uuid.UUID `json:"o"`
	User  uuid.UUID `json:"u"`
	Nonce string    `json:"n"`
}

// Identity is somebody's linked account on a provider.
type Identity struct {
	Provider  string    `json:"provider"`
	Login     string    `json:"login"`
	LinkedAt  time.Time `json:"linkedAt"`
	Available bool      `json:"available"`
}

// Identities lists the providers somebody could link, and what they have linked.
func (s *Service) Identities(ctx context.Context, orgID, userID uuid.UUID) ([]Identity, error) {
	out := []Identity{}
	rows, err := s.db.Query(ctx, `
		SELECT c.provider, COALESCE(i.login, ''), COALESCE(i.created_at, NOW())
		FROM code_connections c
		LEFT JOIN code_identities i ON i.provider = c.provider AND i.user_id = $2
		WHERE c.org_id = $1 AND c.status = 'active'
		ORDER BY c.provider
	`, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id Identity
		if err := rows.Scan(&id.Provider, &id.Login, &id.LinkedAt); err != nil {
			return nil, err
		}
		id.Available = true
		out = append(out, id)
	}
	return out, rows.Err()
}

// GitHubIdentityURL is GitHub's page where somebody signs in to prove which
// GitHub account is theirs. TeamUp keeps the account, not their token.
func (s *Service) GitHubIdentityURL(ctx context.Context, orgID, userID uuid.UUID) (string, string, error) {
	c, err := s.loadConnection(ctx, `org_id = $1 AND provider = 'github' AND status = 'active'`, orgID)
	if err != nil {
		return "", "", apperrors.New(apperrors.CodeConflict, "your organization has not connected GitHub yet")
	}
	sec, err := s.githubSecrets(c)
	if err != nil {
		return "", "", err
	}
	nonce := randomKey()
	state, err := s.box.Token(identityState{Org: orgID, User: userID, Nonce: nonce}, 15*time.Minute)
	if err != nil {
		return "", "", err
	}
	q := url.Values{
		"client_id":    {sec.ClientID},
		"redirect_uri": {s.base() + "/api/integrations/github/identity/callback"},
		"state":        {state},
		"allow_signup": {"false"},
	}
	return s.cfg.GitHubWeb + "/login/oauth/authorize?" + q.Encode(), nonce, nil
}

// GitHubIdentityDone swaps GitHub's code for who signed in, and links that
// account to the TeamUp person who started it.
func (s *Service) GitHubIdentityDone(ctx context.Context, state, nonce, code string) error {
	if !s.Available() {
		return apperrors.New(apperrors.CodeUnavailable, "integrations are not set up on this server")
	}
	var st identityState
	if err := s.box.ReadToken(state, &st); err != nil || nonce == "" || nonce != st.Nonce {
		return apperrors.New(apperrors.CodeBadRequest, "that link has expired or was not started here; try again")
	}
	c, err := s.loadConnection(ctx, `org_id = $1 AND provider = 'github'`, st.Org)
	if err != nil {
		return err
	}
	sec, err := s.githubSecrets(c)
	if err != nil {
		return err
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	form := url.Values{"client_id": {sec.ClientID}, "client_secret": {sec.ClientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.GitHubWeb+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := s.gh.http.Do(req)
	if err != nil {
		return fmt.Errorf("reach github: %w", err)
	}
	defer res.Body.Close()
	if err := decodeJSON(res, &tok); err != nil {
		return err
	}
	if tok.AccessToken == "" {
		return fmt.Errorf("github did not sign them in: %s", tok.Error)
	}
	auth := "token " + tok.AccessToken
	var me ghUser
	if err := s.gh.do(ctx, auth, http.MethodGet, "/user", nil, &me); err != nil {
		return err
	}
	var emails []struct {
		Email    string `json:"email"`
		Verified bool   `json:"verified"`
	}
	// Without leave to read them, commits are matched by login alone.
	_ = s.gh.do(ctx, auth, http.MethodGet, "/user/emails", nil, &emails)
	verified := []string{}
	for _, e := range emails {
		if e.Verified {
			verified = append(verified, strings.ToLower(e.Email))
		}
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO code_identities (user_id, org_id, provider, external_id, login, emails)
		VALUES ($1, $2, 'github', $3, $4, $5)
		ON CONFLICT (user_id, provider) DO UPDATE SET external_id = EXCLUDED.external_id, login = EXCLUDED.login,
		    emails = EXCLUDED.emails, created_at = NOW()
	`, st.User, st.Org, fmt.Sprint(me.ID), me.Login, verified)
	if err != nil {
		if strings.Contains(err.Error(), "code_identities_org_id_provider_external_id_key") {
			return apperrors.New(apperrors.CodeConflict, "that GitHub account is linked to somebody else here")
		}
		return err
	}
	_ = tag
	// Work already recorded under this account becomes theirs.
	_, _ = s.db.Exec(ctx, `UPDATE code_pull_requests SET author_id = $1 WHERE org_id = $2 AND LOWER(author_login) = LOWER($3) AND author_id IS NULL`, st.User, st.Org, me.Login)
	_, _ = s.db.Exec(ctx, `UPDATE code_commits SET author_id = $1 WHERE org_id = $2 AND author_id IS NULL AND (LOWER(author_login) = LOWER($3) OR LOWER(author_email) = ANY($4))`, st.User, st.Org, me.Login, verified)
	_, _ = s.db.Exec(ctx, `UPDATE code_pull_request_reviewers r SET user_id = $1 FROM code_pull_requests p WHERE p.id = r.pull_request_id AND p.org_id = $2 AND LOWER(r.login) = LOWER($3) AND r.user_id IS NULL`, st.User, st.Org, me.Login)
	s.findAfterLinking(ctx, st.Org, st.User)
	return nil
}

// Unlink forgets somebody's account on a provider.
func (s *Service) Unlink(ctx context.Context, userID uuid.UUID, provider string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM code_identities WHERE user_id = $1 AND provider = $2`, userID, provider)
	return err
}

// ── a task's code ─────────────────────────────────────────────────────────

type Person struct {
	Login  string     `json:"login"`
	UserID *uuid.UUID `json:"userId,omitempty"`
	Name   string     `json:"name,omitempty"`
}

type Reviewer struct {
	Person
	State string `json:"state"`
}

type PullRequest struct {
	ID         uuid.UUID  `json:"id"`
	Repository string     `json:"repository"`
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	State      string     `json:"state"`
	URL        string     `json:"url"`
	Branch     string     `json:"branch"`
	Checks     string     `json:"checks,omitempty"`
	Author     Person     `json:"author"`
	Reviewers  []Reviewer `json:"reviewers"`
	OpenedAt   time.Time  `json:"openedAt"`
	MergedAt   *time.Time `json:"mergedAt,omitempty"`
}

type Commit struct {
	Repository  string    `json:"repository"`
	SHA         string    `json:"sha"`
	Message     string    `json:"message"`
	URL         string    `json:"url"`
	Branch      string    `json:"branch"`
	Author      Person    `json:"author"`
	CommittedAt time.Time `json:"committedAt"`
}

type TaskCode struct {
	PullRequests []PullRequest `json:"pullRequests"`
	Commits      []Commit      `json:"commits"`
	// Whether anything is connected, so the panel can say how to start.
	Connected bool `json:"connected"`
}

// ForTask is the pull requests and commits that name a task.
func (s *Service) ForTask(ctx context.Context, orgID, taskID uuid.UUID) (*TaskCode, error) {
	out := &TaskCode{PullRequests: []PullRequest{}, Commits: []Commit{}}
	_ = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM code_connections WHERE org_id = $1 AND status = 'active')`, orgID).Scan(&out.Connected)
	rows, err := s.db.Query(ctx, `
		SELECT p.id, r.full_name, p.number, p.title, p.state, p.html_url, p.head_branch, p.checks,
		       p.author_login, p.author_id, COALESCE(u.name, ''), p.opened_at, p.merged_at
		FROM task_code_links l
		JOIN code_pull_requests p ON p.id = l.pull_request_id
		JOIN code_repositories r ON r.id = p.repository_id
		LEFT JOIN users u ON u.id = p.author_id
		JOIN tasks t ON t.id = l.task_id
		WHERE l.task_id = $1 AND t.org_id = $2
		ORDER BY p.opened_at DESC
	`, taskID, orgID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p PullRequest
		if err := rows.Scan(&p.ID, &p.Repository, &p.Number, &p.Title, &p.State, &p.URL, &p.Branch, &p.Checks,
			&p.Author.Login, &p.Author.UserID, &p.Author.Name, &p.OpenedAt, &p.MergedAt); err != nil {
			rows.Close()
			return nil, err
		}
		p.Reviewers = []Reviewer{}
		out.PullRequests = append(out.PullRequests, p)
	}
	rows.Close()
	for i := range out.PullRequests {
		rr, err := s.db.Query(ctx, `
			SELECT r.login, r.user_id, COALESCE(u.name, ''), r.state FROM code_pull_request_reviewers r
			LEFT JOIN users u ON u.id = r.user_id WHERE r.pull_request_id = $1 ORDER BY r.login
		`, out.PullRequests[i].ID)
		if err != nil {
			return nil, err
		}
		for rr.Next() {
			var rv Reviewer
			if err := rr.Scan(&rv.Login, &rv.UserID, &rv.Name, &rv.State); err == nil {
				out.PullRequests[i].Reviewers = append(out.PullRequests[i].Reviewers, rv)
			}
		}
		rr.Close()
	}
	crs, err := s.db.Query(ctx, `
		SELECT r.full_name, c.sha, c.message, c.html_url, c.branch, COALESCE(NULLIF(c.author_login, ''), c.author_name),
		       c.author_id, COALESCE(u.name, ''), c.committed_at
		FROM task_code_links l
		JOIN code_commits c ON c.id = l.commit_id
		JOIN code_repositories r ON r.id = c.repository_id
		LEFT JOIN users u ON u.id = c.author_id
		JOIN tasks t ON t.id = l.task_id
		WHERE l.task_id = $1 AND t.org_id = $2
		ORDER BY c.committed_at DESC LIMIT 100
	`, taskID, orgID)
	if err != nil {
		return nil, err
	}
	defer crs.Close()
	for crs.Next() {
		var c Commit
		if err := crs.Scan(&c.Repository, &c.SHA, &c.Message, &c.URL, &c.Branch, &c.Author.Login, &c.Author.UserID, &c.Author.Name, &c.CommittedAt); err != nil {
			return nil, err
		}
		out.Commits = append(out.Commits, c)
	}
	return out, crs.Err()
}

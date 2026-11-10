package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Access to a project is access to every repository in it, on GitHub or
// Bitbucket, given to the account the person linked there. Somebody asks; an
// administrator approves or declines; TeamUp makes the change on each
// repository and records it. Taking access away goes the same way.

var accessLevels = map[string]bool{"read": true, "write": true}

// ── what people see ───────────────────────────────────────────────────────

type AccessRequest struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   uuid.UUID  `json:"projectId"`
	ProjectName string     `json:"projectName"`
	UserID      uuid.UUID  `json:"userId"`
	UserName    string     `json:"userName"`
	Level       string     `json:"level"`
	Reason      string     `json:"reason"`
	Status      string     `json:"status"`
	Note        string     `json:"note,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	DecidedAt   *time.Time `json:"decidedAt,omitempty"`
}

type RepoAccess struct {
	Repository string `json:"repository"`
	Provider   string `json:"provider"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
}

type AccessGrant struct {
	ID        uuid.UUID    `json:"id"`
	ProjectID uuid.UUID    `json:"projectId"`
	UserID    uuid.UUID    `json:"userId"`
	UserName  string       `json:"userName"`
	Level     string       `json:"level"`
	GrantedBy string       `json:"grantedBy,omitempty"`
	GrantedAt time.Time    `json:"grantedAt"`
	Repos     []RepoAccess `json:"repositories"`
}

type AccessLogEntry struct {
	ID          uuid.UUID `json:"id"`
	ProjectName string    `json:"projectName,omitempty"`
	Subject     string    `json:"subject"`
	Actor       string    `json:"actor,omitempty"`
	Action      string    `json:"action"`
	Detail      string    `json:"detail,omitempty"`
	At          time.Time `json:"at"`
}

// ProjectAccess is who can reach a project's code, and who is asking. An
// administrator sees everybody; anybody else sees where they stand themself.
type ProjectAccess struct {
	Grants   []AccessGrant   `json:"grants"`
	Requests []AccessRequest `json:"requests"`
	// Providers of the project's repositories the caller has no linked account
	// for: access there cannot be given until they link one.
	Unlinked []string `json:"unlinked"`
}

func (s *Service) ProjectAccess(ctx context.Context, orgID, projectID, viewer uuid.UUID, admin bool) (*ProjectAccess, error) {
	if err := s.projectInOrg(ctx, orgID, projectID); err != nil {
		return nil, err
	}
	out := &ProjectAccess{Grants: []AccessGrant{}, Requests: []AccessRequest{}, Unlinked: []string{}}
	who := ""
	args := []any{projectID}
	if !admin {
		who = " AND g.user_id = $2"
		args = append(args, viewer)
	}
	rows, err := s.db.Query(ctx, `
		SELECT g.id, g.project_id, g.user_id, u.name, g.level, COALESCE(b.name, ''), g.granted_at
		FROM project_access_grants g JOIN users u ON u.id = g.user_id LEFT JOIN users b ON b.id = g.granted_by
		WHERE g.project_id = $1 AND g.revoked_at IS NULL`+who+` ORDER BY LOWER(u.name)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g AccessGrant
		if err := rows.Scan(&g.ID, &g.ProjectID, &g.UserID, &g.UserName, &g.Level, &g.GrantedBy, &g.GrantedAt); err != nil {
			rows.Close()
			return nil, err
		}
		g.Repos = []RepoAccess{}
		out.Grants = append(out.Grants, g)
	}
	rows.Close()
	for i := range out.Grants {
		rr, err := s.db.Query(ctx, `
			SELECT r.full_name, r.provider, a.status, a.detail FROM project_access_repos a
			JOIN code_repositories r ON r.id = a.repository_id WHERE a.grant_id = $1 ORDER BY LOWER(r.full_name)`, out.Grants[i].ID)
		if err != nil {
			return nil, err
		}
		for rr.Next() {
			var r RepoAccess
			if rr.Scan(&r.Repository, &r.Provider, &r.Status, &r.Detail) == nil {
				out.Grants[i].Repos = append(out.Grants[i].Repos, r)
			}
		}
		rr.Close()
	}
	reqWho := ""
	if !admin {
		reqWho = " AND r.requester_id = $2"
	}
	reqs, err := s.listRequests(ctx, `r.project_id = $1 AND r.status = 'pending'`+reqWho, args...)
	if err != nil {
		return nil, err
	}
	out.Requests = reqs
	prov, err := s.db.Query(ctx, `
		SELECT DISTINCT r.provider FROM code_repositories r
		WHERE r.project_id = $1 AND r.present
		  AND NOT EXISTS (SELECT 1 FROM code_identities i WHERE i.user_id = $2 AND i.provider = r.provider)`, projectID, viewer)
	if err != nil {
		return nil, err
	}
	defer prov.Close()
	for prov.Next() {
		var p string
		if prov.Scan(&p) == nil {
			out.Unlinked = append(out.Unlinked, p)
		}
	}
	return out, prov.Err()
}

func (s *Service) listRequests(ctx context.Context, where string, args ...any) ([]AccessRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.project_id, p.name, r.requester_id, u.name, r.level, r.reason, r.status, r.note, r.created_at, r.decided_at
		FROM project_access_requests r JOIN projects p ON p.id = r.project_id JOIN users u ON u.id = r.requester_id
		WHERE `+where+` ORDER BY r.created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessRequest{}
	for rows.Next() {
		var r AccessRequest
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.ProjectName, &r.UserID, &r.UserName, &r.Level, &r.Reason, &r.Status, &r.Note, &r.CreatedAt, &r.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PendingRequests is every request waiting for an administrator.
func (s *Service) PendingRequests(ctx context.Context, orgID uuid.UUID) ([]AccessRequest, error) {
	return s.listRequests(ctx, `r.org_id = $1 AND r.status = 'pending'`, orgID)
}

// AccessLog is the record, newest first.
func (s *Service) AccessLog(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, limit int) ([]AccessLogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
		SELECT l.id, COALESCE(p.name, ''), COALESCE(su.name, l.subject), COALESCE(a.name, ''), l.action, l.detail, l.created_at
		FROM project_access_log l
		LEFT JOIN projects p ON p.id = l.project_id
		LEFT JOIN users su ON su.id = l.subject_id
		LEFT JOIN users a ON a.id = l.actor_id
		WHERE l.org_id = $1 AND ($2::uuid IS NULL OR l.project_id = $2)
		ORDER BY l.created_at DESC LIMIT $3`, orgID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessLogEntry{}
	for rows.Next() {
		var e AccessLogEntry
		if err := rows.Scan(&e.ID, &e.ProjectName, &e.Subject, &e.Actor, &e.Action, &e.Detail, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) log(ctx context.Context, orgID uuid.UUID, projectID, subjectID, actorID *uuid.UUID, subject, action, detail string) {
	if _, err := s.db.Exec(ctx, `
		INSERT INTO project_access_log (org_id, project_id, subject_id, subject, actor_id, action, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, orgID, projectID, subjectID, subject, actorID, action, detail); err != nil {
		s.logger.Warn().Err(err).Msg("could not record access log")
	}
}

func (s *Service) projectInOrg(ctx context.Context, orgID, projectID uuid.UUID) error {
	var ok bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
		projectID, orgID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return apperrors.ErrNotFound
	}
	return nil
}

// ── asking ────────────────────────────────────────────────────────────────

// RequestAccess asks for read or write access to a project. Administrators
// are told.
func (s *Service) RequestAccess(ctx context.Context, orgID, projectID, userID uuid.UUID, level, reason string) (*AccessRequest, error) {
	if !accessLevels[level] {
		return nil, apperrors.New(apperrors.CodeBadRequest, "ask for read or write access")
	}
	if err := s.projectInOrg(ctx, orgID, projectID); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 2000 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "the reason is too long")
	}
	var held string
	err := s.db.QueryRow(ctx, `SELECT level FROM project_access_grants WHERE project_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		projectID, userID).Scan(&held)
	if err == nil && (held == level || held == "write") {
		return nil, apperrors.New(apperrors.CodeConflict, "you have that access already")
	}
	var id uuid.UUID
	err = s.db.QueryRow(ctx, `
		INSERT INTO project_access_requests (org_id, project_id, requester_id, level, reason)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, orgID, projectID, userID, level, reason).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, apperrors.New(apperrors.CodeConflict, "you have asked already; an administrator will answer")
	}
	if err != nil {
		return nil, fmt.Errorf("request access: %w", err)
	}
	reqs, err := s.listRequests(ctx, `r.id = $1`, id)
	if err != nil || len(reqs) == 0 {
		return nil, err
	}
	r := reqs[0]
	s.log(ctx, orgID, &projectID, &userID, &userID, "", "requested", level+access(reason))
	for _, admin := range s.admins(ctx, orgID) {
		if admin == userID || s.notifier == nil {
			continue
		}
		if err := s.notifier.Notify(ctx, orgID, admin, "access.requested",
			fmt.Sprintf("%s asks for %s access to %s", r.UserName, level, r.ProjectName), reason,
			map[string]any{"projectId": projectID.String(), "requestId": id.String()}); err != nil {
			s.logger.Warn().Err(err).Msg("access request notification failed")
		}
	}
	return &r, nil
}

func access(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

// admins is everybody who manages the organization's settings.
func (s *Service) admins(ctx context.Context, orgID uuid.UUID) []uuid.UUID {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT u.id FROM users u JOIN user_roles ur ON ur.user_id = u.id JOIN roles r ON r.id = ur.role_id
		WHERE u.org_id = $1 AND u.deleted_at IS NULL
		  AND (r.permissions::jsonb ? 'org.settings' OR r.permissions::jsonb ? '*')`, orgID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not find administrators")
		return nil
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// CancelRequest withdraws somebody's own request.
func (s *Service) CancelRequest(ctx context.Context, orgID, requestID, userID uuid.UUID) error {
	var projectID uuid.UUID
	err := s.db.QueryRow(ctx, `
		UPDATE project_access_requests SET status = 'cancelled', decided_at = NOW()
		WHERE id = $1 AND org_id = $2 AND requester_id = $3 AND status = 'pending' RETURNING project_id`,
		requestID, orgID, userID).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.ErrNotFound
	}
	if err != nil {
		return err
	}
	s.log(ctx, orgID, &projectID, &userID, &userID, "", "cancelled", "")
	return nil
}

// ── deciding ──────────────────────────────────────────────────────────────

// Decide approves or declines a request. Approving gives the access there and
// then, on every repository of the project.
func (s *Service) Decide(ctx context.Context, orgID, requestID, adminID uuid.UUID, approve bool, note string) (*AccessGrant, error) {
	status := "declined"
	if approve {
		status = "approved"
	}
	var r struct {
		project, user uuid.UUID
		level         string
	}
	err := s.db.QueryRow(ctx, `
		UPDATE project_access_requests SET status = $4, decided_by = $3, decided_at = NOW(), note = $5
		WHERE id = $1 AND org_id = $2 AND status = 'pending'
		RETURNING project_id, requester_id, level`, requestID, orgID, adminID, status, strings.TrimSpace(note)).Scan(&r.project, &r.user, &r.level)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.New(apperrors.CodeConflict, "that request was answered or withdrawn already")
	}
	if err != nil {
		return nil, err
	}
	var projectName string
	_ = s.db.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, r.project).Scan(&projectName)
	if !approve {
		s.log(ctx, orgID, &r.project, &r.user, &adminID, "", "declined", access(note))
		s.tellUser(ctx, orgID, r.user, "access.declined", fmt.Sprintf("Your request for %s access to %s was declined", r.level, projectName), note, r.project)
		return nil, nil
	}
	s.log(ctx, orgID, &r.project, &r.user, &adminID, "", "approved", r.level+access(note))
	return s.grant(ctx, orgID, r.project, r.user, adminID, r.level, &requestID, projectName)
}

// Grant gives somebody access without their asking.
func (s *Service) Grant(ctx context.Context, orgID, projectID, userID, adminID uuid.UUID, level string) (*AccessGrant, error) {
	if !accessLevels[level] {
		return nil, apperrors.New(apperrors.CodeBadRequest, "give read or write access")
	}
	if err := s.projectInOrg(ctx, orgID, projectID); err != nil {
		return nil, err
	}
	var inOrg bool
	_ = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`, userID, orgID).Scan(&inOrg)
	if !inOrg {
		return nil, apperrors.ErrNotFound
	}
	var projectName string
	_ = s.db.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, projectID).Scan(&projectName)
	return s.grant(ctx, orgID, projectID, userID, adminID, level, nil, projectName)
}

// grant records the access and makes it so on each repository. A grant
// replaces the one before it, so moving from read to write is one grant.
func (s *Service) grant(ctx context.Context, orgID, projectID, userID, adminID uuid.UUID, level string, requestID *uuid.UUID, projectName string) (*AccessGrant, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE project_access_grants SET revoked_at = NOW(), revoked_by = $3 WHERE project_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		projectID, userID, adminID); err != nil {
		return nil, err
	}
	var grantID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO project_access_grants (org_id, project_id, user_id, level, request_id, granted_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, orgID, projectID, userID, level, requestID, adminID).Scan(&grantID); err != nil {
		return nil, err
	}
	// Any other request of theirs for this project is answered by this.
	if _, err := tx.Exec(ctx, `UPDATE project_access_requests SET status = 'approved', decided_by = $3, decided_at = NOW()
		WHERE project_id = $1 AND requester_id = $2 AND status = 'pending'`, projectID, userID, adminID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	results := s.applyToRepos(ctx, orgID, projectID, userID, grantID, level, false)
	failed := 0
	for _, r := range results {
		if r.Status == "failed" {
			failed++
		}
	}
	summary := fmt.Sprintf("%s access on %d of %d repositories", level, len(results)-failed, len(results))
	s.log(ctx, orgID, &projectID, &userID, &adminID, "", "granted", summary)
	for _, r := range results {
		if r.Status == "failed" {
			s.log(ctx, orgID, &projectID, &userID, &adminID, "", "failed", r.Repository+": "+r.Detail)
		}
	}
	body := fmt.Sprintf("You can now %s %s's code.", map[string]string{"read": "read", "write": "read and change"}[level], projectName)
	if invited := countStatus(results, "invited"); invited > 0 {
		body += " GitHub has sent you an invitation to accept for each repository."
	}
	if failed > 0 {
		body += fmt.Sprintf(" %d repositories could not be opened to you yet; an administrator can see why.", failed)
	}
	s.tellUser(ctx, orgID, userID, "access.granted", fmt.Sprintf("You have %s access to %s", level, projectName), body, projectID)

	view, err := s.ProjectAccess(ctx, orgID, projectID, userID, true)
	if err != nil {
		return nil, err
	}
	for _, g := range view.Grants {
		if g.ID == grantID {
			return &g, nil
		}
	}
	return nil, nil
}

func countStatus(rs []RepoAccess, status string) int {
	n := 0
	for _, r := range rs {
		if r.Status == status {
			n++
		}
	}
	return n
}

// Revoke takes somebody's access away, on every repository.
func (s *Service) Revoke(ctx context.Context, orgID, grantID, adminID uuid.UUID) error {
	var g struct {
		project, user uuid.UUID
		level         string
	}
	err := s.db.QueryRow(ctx, `
		UPDATE project_access_grants SET revoked_at = NOW(), revoked_by = $3
		WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL RETURNING project_id, user_id, level`,
		grantID, orgID, adminID).Scan(&g.project, &g.user, &g.level)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.ErrNotFound
	}
	if err != nil {
		return err
	}
	results := s.applyToRepos(ctx, orgID, g.project, g.user, grantID, g.level, true)
	failed := countStatus(results, "failed")
	s.log(ctx, orgID, &g.project, &g.user, &adminID, "", "revoked", fmt.Sprintf("on %d of %d repositories", len(results)-failed, len(results)))
	for _, r := range results {
		if r.Status == "failed" {
			s.log(ctx, orgID, &g.project, &g.user, &adminID, "", "failed", "removing from "+r.Repository+": "+r.Detail)
		}
	}
	var projectName string
	_ = s.db.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, g.project).Scan(&projectName)
	s.tellUser(ctx, orgID, g.user, "access.revoked", "Your access to "+projectName+" was removed", "", g.project)
	if failed > 0 {
		return apperrors.New(apperrors.CodeConflict, fmt.Sprintf("access is withdrawn here, but %d repositories could not be changed; see the access log", failed))
	}
	return nil
}

func (s *Service) tellUser(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, projectID uuid.UUID) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.Notify(ctx, orgID, userID, kind, title, body, map[string]any{"projectId": projectID.String()}); err != nil {
		s.logger.Warn().Err(err).Msg("access notification failed")
	}
}

// ── on the providers ──────────────────────────────────────────────────────

// applyToRepos gives (or takes away) access on each repository of the
// project, as the account the person linked on its provider, and records how
// each went.
func (s *Service) applyToRepos(ctx context.Context, orgID, projectID, userID, grantID uuid.UUID, level string, remove bool) []RepoAccess {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.full_name, r.provider, r.connection_id, COALESCE(i.login, ''), COALESCE(i.external_id, '')
		FROM code_repositories r
		LEFT JOIN code_identities i ON i.user_id = $2 AND i.provider = r.provider
		WHERE r.project_id = $1 AND r.present ORDER BY LOWER(r.full_name)`, projectID, userID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not list a project's repositories")
		return nil
	}
	type target struct {
		repoID, connID    uuid.UUID
		name, provider    string
		login, externalID string
	}
	var targets []target
	for rows.Next() {
		var t target
		if rows.Scan(&t.repoID, &t.name, &t.provider, &t.connID, &t.login, &t.externalID) == nil {
			targets = append(targets, t)
		}
	}
	rows.Close()

	out := []RepoAccess{}
	for _, t := range targets {
		res := RepoAccess{Repository: t.name, Provider: t.provider}
		switch {
		case t.login == "" && t.externalID == "":
			res.Status = "failed"
			res.Detail = fmt.Sprintf("they have not linked a %s account in TeamUp", providerName(t.provider))
			if remove {
				res.Status, res.Detail = "revoked", "no linked account, so nothing to remove"
			}
		default:
			c, err := s.loadConnection(ctx, `id = $1 AND status = 'active'`, t.connID)
			if err != nil {
				res.Status, res.Detail = "failed", providerName(t.provider)+" is not connected"
				break
			}
			res.Status, res.Detail = s.applyOne(ctx, c, t.name, t.login, t.externalID, level, remove)
		}
		out = append(out, res)
		if _, err := s.db.Exec(ctx, `
			INSERT INTO project_access_repos (grant_id, repository_id, status, detail, updated_at) VALUES ($1, $2, $3, $4, NOW())
			ON CONFLICT (grant_id, repository_id) DO UPDATE SET status = EXCLUDED.status, detail = EXCLUDED.detail, updated_at = NOW()`,
			grantID, t.repoID, res.Status, res.Detail); err != nil {
			s.logger.Warn().Err(err).Msg("could not record repository access")
		}
	}
	return out
}

func providerName(p string) string {
	return map[string]string{"github": "GitHub", "bitbucket": "Bitbucket"}[p]
}

// applyOne changes one person's access to one repository.
func (s *Service) applyOne(ctx context.Context, c *connection, repo, login, externalID, level string, remove bool) (string, string) {
	owner, name, _ := strings.Cut(repo, "/")
	switch c.Provider {
	case "github":
		token, err := s.installationToken(ctx, c)
		if err != nil {
			return "failed", "GitHub would not let TeamUp in: " + err.Error()
		}
		path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/collaborators/" + url.PathEscape(login)
		if remove {
			if err := s.gh.do(ctx, "token "+token, http.MethodDelete, path, nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
				return "failed", plainError(err)
			}
			return "revoked", ""
		}
		var invitation struct {
			ID int64 `json:"id"`
		}
		err = s.gh.do(ctx, "token "+token, http.MethodPut, path, map[string]string{"permission": map[string]string{"read": "pull", "write": "push"}[level]}, &invitation)
		if err != nil {
			return "failed", plainError(err)
		}
		// GitHub answers with an invitation for somebody outside the
		// organization, and with nothing for a member it added there and then.
		if invitation.ID != 0 {
			return "invited", "waiting for them to accept GitHub's invitation"
		}
		return "granted", ""
	case "bitbucket":
		token, _, err := s.ownerToken(ctx, c)
		if err != nil {
			return "failed", "Bitbucket would not let TeamUp in: " + err.Error()
		}
		path := "/repositories/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/permissions-config/users/" + url.PathEscape(externalID)
		if remove {
			if err := s.bbDo(ctx, token, http.MethodDelete, path, nil, nil); err != nil && !isStatus(err, http.StatusNotFound) {
				return "failed", plainError(err)
			}
			return "revoked", ""
		}
		if err := s.bbDo(ctx, token, http.MethodPut, path, map[string]string{"permission": level}, nil); err != nil {
			if isStatus(err, http.StatusNotFound) || isStatus(err, http.StatusBadRequest) {
				return "failed", "they are not a member of the Bitbucket workspace yet; invite them in Bitbucket, then give access again"
			}
			return "failed", plainError(err)
		}
		return "granted", ""
	}
	return "failed", "unknown provider"
}

func plainError(err error) string {
	var e *apiError
	if errors.As(err, &e) {
		switch e.Status {
		case http.StatusForbidden:
			return "the connected account is not allowed to change who can use this repository"
		case http.StatusNotFound:
			return "the repository or the account was not found there"
		case http.StatusUnprocessableEntity:
			return "the provider refused the change: " + e.Body
		}
		return fmt.Sprintf("the provider answered %d", e.Status)
	}
	return err.Error()
}

// ── changes made on GitHub itself ─────────────────────────────────────────

// onMember records somebody given or refused a repository on GitHub directly,
// so the record here does not quietly go out of date.
func (s *Service) onMember(ctx context.Context, c *connection, body []byte) error {
	var e struct {
		Action     string `json:"action"`
		Member     ghUser `json:"member"`
		Sender     ghUser `json:"sender"`
		Repository ghRepo `json:"repository"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return err
	}
	// TeamUp's own changes come back as the app's; only a person's are news.
	if (e.Action != "added" && e.Action != "removed") || e.Sender.Type == "Bot" {
		return nil
	}
	var projectID *uuid.UUID
	var pid uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT project_id FROM code_repositories WHERE connection_id = $1 AND external_id = $2 AND project_id IS NOT NULL`,
		c.ID, fmt.Sprint(e.Repository.ID)).Scan(&pid); err == nil {
		projectID = &pid
	}
	subject := s.personFor(ctx, c.OrgID, "github", e.Member.Login, "")
	actor := s.personFor(ctx, c.OrgID, "github", e.Sender.Login, "")
	s.log(ctx, c.OrgID, projectID, subject, actor, "@"+e.Member.Login, "outside_"+e.Action,
		fmt.Sprintf("%s on GitHub by @%s, not through TeamUp", e.Repository.FullName, e.Sender.Login))
	return nil
}

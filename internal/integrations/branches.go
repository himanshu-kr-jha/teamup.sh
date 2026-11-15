package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Starting development on a task: TeamUp makes the task's branch on one of its
// project's repositories, from a base branch, and links it to the task. The
// branch is made by the organization's connection, but only for somebody who
// already has write access to that repository through the project; anybody else
// asks for it the way access is always asked for, and an administrator decides.

// ErrNeedsWriteAccess is somebody without write access to the repository asking
// for a branch on it.
var ErrNeedsWriteAccess = apperrors.New(apperrors.CodeForbidden,
	"you need write access to this project's repositories to start a branch; ask for it and an administrator will decide")

// ErrBranchNotAllowed is the connection itself not allowed to make branches:
// GitHub needs the app to have Contents: Read and write, Bitbucket the consumer
// to have Repositories: Write.
var ErrBranchNotAllowed = apperrors.New(apperrors.CodeConflict,
	"TeamUp is not allowed to create branches on this repository yet")

// TaskBranch is a branch started for a task from TeamUp.
type TaskBranch struct {
	ID         uuid.UUID `json:"id"`
	Repository string    `json:"repository"`
	Provider   string    `json:"provider"`
	Name       string    `json:"name"`
	Base       string    `json:"base"`
	URL        string    `json:"url"`
	By         Person    `json:"by"`
	CreatedAt  time.Time `json:"createdAt"`
}

// BranchName is the branch that links work to a task: its key and the first few
// words of its title, pho-31-fix-sign-in. The web app offers the same name.
func BranchName(key, title string) string {
	words := strings.Split(strings.Trim(nonBranch.ReplaceAllString(strings.ToLower(title), "-"), "-"), "-")
	if len(words) > 6 {
		words = words[:6]
	}
	name := strings.ToLower(key)
	if w := strings.Join(words, "-"); w != "" {
		name += "-" + w
	}
	return name
}

var nonBranch = regexp.MustCompile(`[^a-z0-9]+`)

// A base branch as somebody may type it: what git allows, less what would need escaping.
var baseBranch = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)

// MayBranch says whether somebody has write access to a repository through its
// project: a grant of write that reached that repository, given in TeamUp or
// found held there already.
func (s *Service) MayBranch(ctx context.Context, projectID, repoID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM project_access_grants g
			JOIN project_access_repos a ON a.grant_id = g.id
			WHERE g.project_id = $1 AND g.user_id = $3 AND g.revoked_at IS NULL AND g.level = 'write'
			  AND a.repository_id = $2 AND a.status IN ('granted', 'existing'))`, projectID, repoID, userID).Scan(&ok)
	return ok, err
}

// StartBranch makes the task's branch on one of its project's repositories,
// from base (the repository's default branch when empty), links it to the task
// and moves the task to in progress. A branch of that name there already is
// linked as it is, so pressing twice, or a branch pushed by hand, is no error.
func (s *Service) StartBranch(ctx context.Context, orgID, taskID, userID, repoID uuid.UUID, base string) (*TaskBranch, error) {
	task, err := s.tasks.GetTask(ctx, orgID, taskID)
	if err != nil {
		return nil, err
	}
	if task.ProjectID == nil || task.Key == "" {
		return nil, apperrors.New(apperrors.CodeConflict, "move the task into a project first; its branches go on that project's repositories")
	}
	base = strings.TrimSpace(base)
	if base != "" && !baseBranch.MatchString(base) {
		return nil, apperrors.New(apperrors.CodeValidation, "that is not a branch name")
	}

	var repo struct {
		connID   uuid.UUID
		provider string
		fullName string
		htmlURL  string
	}
	err = s.db.QueryRow(ctx, `
		SELECT connection_id, provider, full_name, html_url FROM code_repositories
		WHERE id = $1 AND org_id = $2 AND project_id = $3 AND present`, repoID, orgID, *task.ProjectID).
		Scan(&repo.connID, &repo.provider, &repo.fullName, &repo.htmlURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.New(apperrors.CodeNotFound, "that repository is not part of this task's project")
	}
	if err != nil {
		return nil, fmt.Errorf("find repository: %w", err)
	}
	may, err := s.MayBranch(ctx, *task.ProjectID, repoID, userID)
	if err != nil {
		return nil, err
	}
	if !may {
		return nil, ErrNeedsWriteAccess
	}
	c, err := s.loadConnection(ctx, `id = $1 AND status = 'active'`, repo.connID)
	if errors.Is(err, apperrors.ErrNotFound) {
		return nil, apperrors.New(apperrors.CodeConflict, "the connection to "+providerName(repo.provider)+" is not working; an administrator can reconnect it")
	}
	if err != nil {
		return nil, err
	}

	name := BranchName(task.Key, task.Title)
	var made string
	switch repo.provider {
	case "github":
		base, made, err = s.githubBranch(ctx, c, repo.fullName, repo.htmlURL, base, name)
	case "bitbucket":
		base, made, err = s.bitbucketBranch(ctx, c, repo.fullName, repo.htmlURL, base, name)
	default:
		err = fmt.Errorf("unknown provider %q", repo.provider)
	}
	if err != nil {
		return nil, err
	}

	b := &TaskBranch{Repository: repo.fullName, Provider: repo.provider, Name: name, Base: base, URL: made}
	err = s.db.QueryRow(ctx, `
		INSERT INTO task_branches (org_id, task_id, repository_id, name, base, html_url, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (repository_id, name) DO UPDATE SET task_id = EXCLUDED.task_id
		RETURNING id, created_at`, orgID, taskID, repoID, name, base, made, userID).Scan(&b.ID, &b.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("record branch: %w", err)
	}
	b.By = Person{UserID: &userID}
	_ = s.db.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, userID).Scan(&b.By.Name)
	if _, err := s.tasks.MoveByCode(ctx, orgID, taskID, "in_progress", repo.provider, &userID,
		"started a branch for it"); err != nil {
		s.logger.Warn().Err(err).Msg("could not move a task on starting its branch")
	}
	return b, nil
}

// notAllowed turns the provider refusing the connection itself into words an
// administrator can act on.
func notAllowed(err error) error {
	if isStatus(err, http.StatusForbidden) || isStatus(err, http.StatusUnauthorized) {
		return ErrBranchNotAllowed
	}
	return err
}

func (s *Service) githubBranch(ctx context.Context, c *connection, fullName, htmlURL, base, name string) (string, string, error) {
	token, err := s.installationToken(ctx, c)
	if err != nil {
		return "", "", err
	}
	auth := "token " + token
	repoPath := "/repos/" + fullName
	if base == "" {
		var r struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := s.gh.do(ctx, auth, http.MethodGet, repoPath, nil, &r); err != nil {
			return "", "", notAllowed(err)
		}
		base = r.DefaultBranch
	}
	link := htmlURL + "/tree/" + name
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err = s.gh.do(ctx, auth, http.MethodGet, repoPath+"/git/ref/heads/"+pathEscapeRef(base), nil, &ref)
	if isStatus(err, http.StatusNotFound) {
		return "", "", apperrors.New(apperrors.CodeNotFound, "there is no branch "+base+" on "+fullName)
	}
	if err != nil {
		return "", "", notAllowed(err)
	}
	err = s.gh.do(ctx, auth, http.MethodPost, repoPath+"/git/refs",
		map[string]string{"ref": "refs/heads/" + name, "sha": ref.Object.SHA}, nil)
	// 422: the branch is there already, made by hand or by an earlier press.
	if err != nil && !isStatus(err, http.StatusUnprocessableEntity) {
		return "", "", notAllowed(err)
	}
	return base, link, nil
}

func (s *Service) bitbucketBranch(ctx context.Context, c *connection, fullName, htmlURL, base, name string) (string, string, error) {
	token, _, err := s.ownerToken(ctx, c)
	if err != nil {
		return "", "", err
	}
	repoPath := "/repositories/" + fullName
	if base == "" {
		var r struct {
			MainBranch struct {
				Name string `json:"name"`
			} `json:"mainbranch"`
		}
		if err := s.bbDo(ctx, token, http.MethodGet, repoPath, nil, &r); err != nil {
			return "", "", notAllowed(err)
		}
		base = r.MainBranch.Name
	}
	link := htmlURL + "/branch/" + name
	// There already: linked as it is.
	if err := s.bbDo(ctx, token, http.MethodGet, repoPath+"/refs/branches/"+url.PathEscape(name), nil, nil); err == nil {
		return base, link, nil
	}
	var from struct {
		Target struct {
			Hash string `json:"hash"`
		} `json:"target"`
	}
	err = s.bbDo(ctx, token, http.MethodGet, repoPath+"/refs/branches/"+url.PathEscape(base), nil, &from)
	if isStatus(err, http.StatusNotFound) {
		return "", "", apperrors.New(apperrors.CodeNotFound, "there is no branch "+base+" on "+fullName)
	}
	if err != nil {
		return "", "", notAllowed(err)
	}
	err = s.bbDo(ctx, token, http.MethodPost, repoPath+"/refs/branches",
		map[string]any{"name": name, "target": map[string]string{"hash": from.Target.Hash}}, nil)
	if err != nil {
		return "", "", notAllowed(err)
	}
	return base, link, nil
}

// pathEscapeRef escapes each part of a branch name, keeping its slashes.
func pathEscapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// branchesFor is the branches started for a task, newest first.
func (s *Service) branchesFor(ctx context.Context, orgID, taskID uuid.UUID) ([]TaskBranch, error) {
	rows, err := s.db.Query(ctx, `
		SELECT b.id, r.full_name, r.provider, b.name, b.base, b.html_url, b.created_by, COALESCE(u.name, ''), b.created_at
		FROM task_branches b JOIN code_repositories r ON r.id = b.repository_id
		LEFT JOIN users u ON u.id = b.created_by
		WHERE b.task_id = $1 AND b.org_id = $2 ORDER BY b.created_at DESC`, taskID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskBranch{}
	for rows.Next() {
		var b TaskBranch
		if err := rows.Scan(&b.ID, &b.Repository, &b.Provider, &b.Name, &b.Base, &b.URL, &b.By.UserID, &b.By.Name, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

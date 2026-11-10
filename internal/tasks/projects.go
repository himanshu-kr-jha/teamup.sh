package tasks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Project groups work: its tasks are keyed with its key (PHO-12), and the
// repositories that make it up are where that work is done.
type Project struct {
	ID           uuid.UUID     `json:"id"`
	Name         string        `json:"name"`
	Key          string        `json:"key"`
	Description  string        `json:"description"`
	Repositories []ProjectRepo `json:"repositories"`
	OpenTasks    int           `json:"openTasks"`
	CreatedAt    time.Time     `json:"createdAt"`
}

type ProjectRepo struct {
	ID       uuid.UUID `json:"id"`
	Provider string    `json:"provider"`
	FullName string    `json:"fullName"`
	URL      string    `json:"url"`
}

// projectKey is two to ten capitals and digits, starting with a letter, as in
// PHO or BETA2. It is what people type in a branch name, so short and plain.
var projectKey = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

func (s *Service) ListProjects(ctx context.Context, orgID uuid.UUID) ([]Project, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.name, p.key, COALESCE(p.description, ''), p.created_at,
		       (SELECT count(*) FROM tasks t WHERE t.project_id = p.id AND t.deleted_at IS NULL AND t.status <> 'done')
		FROM projects p WHERE p.org_id = $1 AND p.deleted_at IS NULL ORDER BY LOWER(p.name)
	`, orgID)
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Key, &p.Description, &p.CreatedAt, &p.OpenTasks); err != nil {
			rows.Close()
			return nil, err
		}
		p.Repositories = []ProjectRepo{}
		out = append(out, p)
	}
	rows.Close()
	repos, err := s.db.Query(ctx, `
		SELECT project_id, id, provider, full_name, html_url FROM code_repositories
		WHERE org_id = $1 AND project_id IS NOT NULL AND present ORDER BY LOWER(full_name)
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer repos.Close()
	index := map[uuid.UUID]int{}
	for i, p := range out {
		index[p.ID] = i
	}
	for repos.Next() {
		var projectID uuid.UUID
		var r ProjectRepo
		if err := repos.Scan(&projectID, &r.ID, &r.Provider, &r.FullName, &r.URL); err != nil {
			return nil, err
		}
		if i, ok := index[projectID]; ok {
			out[i].Repositories = append(out[i].Repositories, r)
		}
	}
	return out, repos.Err()
}

func (s *Service) CreateProject(ctx context.Context, orgID, userID uuid.UUID, name, key, description string) (*Project, error) {
	name, key = strings.TrimSpace(name), strings.ToUpper(strings.TrimSpace(key))
	if name == "" || len(name) > 255 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "give the project a name")
	}
	if !projectKey.MatchString(key) {
		return nil, apperrors.New(apperrors.CodeBadRequest, "a key is 2 to 10 capital letters or digits, starting with a letter, like PHO")
	}
	if key == DefaultKeyPrefix {
		return nil, apperrors.New(apperrors.CodeBadRequest, key+" is used for tasks in no project; choose another key")
	}
	var p Project
	err := s.db.QueryRow(ctx, `
		INSERT INTO projects (org_id, name, key, description, lead_id) VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		RETURNING id, name, key, COALESCE(description, ''), created_at
	`, orgID, name, key, strings.TrimSpace(description), userID).Scan(&p.ID, &p.Name, &p.Key, &p.Description, &p.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, apperrors.New(apperrors.CodeConflict, "another project already uses the key "+key)
	}
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	p.Repositories = []ProjectRepo{}
	return &p, nil
}

// UpdateProject renames a project or changes what it says about itself. Its
// key stays: every task key and branch name already made with it would break.
func (s *Service) UpdateProject(ctx context.Context, orgID, id uuid.UUID, name, description *string) error {
	if name != nil && (strings.TrimSpace(*name) == "" || len(*name) > 255) {
		return apperrors.New(apperrors.CodeBadRequest, "give the project a name")
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE projects SET name = COALESCE($3, name), description = COALESCE($4, description), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, id, orgID, trimmed(name), trimmed(description))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

func trimmed(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

// ArchiveProject takes a project off the list. Its tasks keep their keys and
// stay where they are; its repositories are free to join another project.
func (s *Service) ArchiveProject(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `UPDATE projects SET deleted_at = NOW() WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	_, err = s.db.Exec(ctx, `UPDATE code_repositories SET project_id = NULL WHERE project_id = $1`, id)
	return err
}

// ── routes ────────────────────────────────────────────────────────────────

func registerProjectRoutes(rg *gin.RouterGroup, h *Handler) {
	rg.GET("/projects", h.ListProjects)
	rg.POST("/projects", h.CreateProject)
	rg.PATCH("/projects/:id", h.UpdateProject)
	rg.DELETE("/projects/:id", h.ArchiveProject)
}

func mayManageProjects(c *gin.Context) bool {
	if auth.GetUserFromContext(c).Can(auth.PermOrgSettings) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "only an administrator can manage projects"})
	return false
}

func (h *Handler) ListProjects(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	list, err := h.service.ListProjects(c.Request.Context(), cl.OrgID)
	if err != nil {
		httpx.Error(c, err, "failed to list projects")
		return
	}
	c.JSON(http.StatusOK, gin.H{"projects": list})
}

func (h *Handler) CreateProject(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok || !mayManageProjects(c) {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Key         string `json:"key"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	p, err := h.service.CreateProject(c.Request.Context(), cl.OrgID, cl.UserID, req.Name, req.Key, req.Description)
	if err != nil {
		httpx.Error(c, err, "failed to create project")
		return
	}
	c.JSON(http.StatusCreated, p)
}

func (h *Handler) UpdateProject(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok || !mayManageProjects(c) {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := h.service.UpdateProject(c.Request.Context(), cl.OrgID, id, req.Name, req.Description); err != nil {
		httpx.Error(c, err, "failed to update project")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ArchiveProject(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok || !mayManageProjects(c) {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if err := h.service.ArchiveProject(c.Request.Context(), cl.OrgID, id); err != nil {
		httpx.Error(c, err, "failed to archive project")
		return
	}
	c.Status(http.StatusNoContent)
}

package org

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

type Service struct {
	db     *pgxpool.Pool
	logger zerolog.Logger
}

type Organization struct {
	ID        uuid.UUID              `json:"id"`
	Name      string                 `json:"name"`
	Subdomain string                 `json:"subdomain"`
	LogoURL   *string                `json:"logoUrl,omitempty"`
	Settings  map[string]interface{} `json:"settings"`
	IsActive  bool                   `json:"isActive"`
	CreatedAt time.Time              `json:"createdAt"`
	UpdatedAt time.Time              `json:"updatedAt"`
}

type User struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"orgId"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	AvatarURL *string   `json:"avatarUrl,omitempty"`
	Role      string    `json:"role"`
	Roles     []string  `json:"roles"`
	IsAdmin   bool      `json:"isAdmin"`
	IsActive  bool      `json:"isActive"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Role struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"orgId"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	IsSystem    bool      `json:"isSystem"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Team struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"orgId"`
	ParentID    *uuid.UUID `json:"parentId,omitempty"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	ManagerID   *uuid.UUID `json:"managerId,omitempty"`
	MemberCount int        `json:"memberCount"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// ── Organizations ─────────────────────────────────────────────────────────

func (s *Service) CreateOrganization(ctx context.Context, name, subdomain string, logoURL *string) (*Organization, error) {
	org := &Organization{ID: uuid.New()}
	err := s.db.QueryRow(ctx, `
		INSERT INTO organizations (id, name, subdomain, logo_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		RETURNING id, name, subdomain, logo_url, settings, is_active, created_at, updated_at
	`, org.ID, name, subdomain, logoURL).Scan(
		&org.ID, &org.Name, &org.Subdomain, &org.LogoURL, &org.Settings,
		&org.IsActive, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create organization: %w", err)
	}
	return org, nil
}

const orgColumns = `id, name, subdomain, logo_url, settings, is_active, created_at, updated_at`

func scanOrg(row pgx.Row) (*Organization, error) {
	var o Organization
	err := row.Scan(&o.ID, &o.Name, &o.Subdomain, &o.LogoURL, &o.Settings,
		&o.IsActive, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return &o, nil
}

func (s *Service) GetOrganization(ctx context.Context, id uuid.UUID) (*Organization, error) {
	return scanOrg(s.db.QueryRow(ctx,
		`SELECT `+orgColumns+` FROM organizations WHERE id = $1 AND deleted_at IS NULL`, id))
}

func (s *Service) GetOrganizationBySubdomain(ctx context.Context, subdomain string) (*Organization, error) {
	return scanOrg(s.db.QueryRow(ctx,
		`SELECT `+orgColumns+` FROM organizations WHERE subdomain = $1 AND deleted_at IS NULL`, subdomain))
}

// UpdateOrganization applies only the fields that were supplied, so a partial
// PATCH cannot blank out the rest of the profile.
func (s *Service) UpdateOrganization(ctx context.Context, id uuid.UUID, name, logoURL *string, settings map[string]interface{}) (*Organization, error) {
	var settingsJSON []byte
	if settings != nil {
		var err error
		if settingsJSON, err = json.Marshal(settings); err != nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "invalid settings")
		}
	}
	return scanOrg(s.db.QueryRow(ctx, `
		UPDATE organizations SET
			name     = COALESCE($2, name),
			logo_url = COALESCE($3, logo_url),
			settings = COALESCE($4::jsonb, settings),
			updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING `+orgColumns, id, name, logoURL, settingsJSON))
}

// ── Users ─────────────────────────────────────────────────────────────────

// userColumns aggregates RBAC role names alongside the row, so callers see the
// roles a user actually holds rather than only the legacy single-role column.
const userColumns = `
	SELECT u.id, u.org_id, u.name, u.email, u.avatar_url, u.role,
	       COALESCE(u.is_admin, false), COALESCE(u.is_active, true),
	       u.created_at, u.updated_at,
	       COALESCE(ARRAY_REMOVE(ARRAY_AGG(r.name), NULL), '{}')
	FROM users u
	LEFT JOIN user_roles ur ON ur.user_id = u.id
	LEFT JOIN roles r ON r.id = ur.role_id
	WHERE u.deleted_at IS NULL AND %s
	GROUP BY u.id`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.OrgID, &u.Name, &u.Email, &u.AvatarURL, &u.Role,
		&u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt, &u.Roles)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (s *Service) CreateUser(ctx context.Context, orgID uuid.UUID, name, email, passwordHash string, role string, isAdmin bool) (*User, error) {
	id := uuid.New()
	_, err := s.db.Exec(ctx, `
		INSERT INTO users (id, org_id, name, email, password_hash, role, is_admin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, NOW(), NOW())
	`, id, orgID, name, email, passwordHash, role, isAdmin)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return s.GetUser(ctx, id)
}

func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, fmt.Sprintf(userColumns, "u.id = $1"), id))
}

// GetUserByEmail is scoped to one organization: the same address may exist in
// another tenant and must never resolve across the boundary.
func (s *Service) GetUserByEmail(ctx context.Context, orgID uuid.UUID, email string) (*User, error) {
	return scanUser(s.db.QueryRow(ctx,
		fmt.Sprintf(userColumns, "u.org_id = $1 AND LOWER(u.email) = LOWER($2)"), orgID, email))
}

func (s *Service) ListUsers(ctx context.Context, orgID uuid.UUID) ([]User, error) {
	rows, err := s.db.Query(ctx,
		fmt.Sprintf(userColumns, "u.org_id = $1")+" ORDER BY u.name", orgID)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.OrgID, &u.Name, &u.Email, &u.AvatarURL, &u.Role,
			&u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt, &u.Roles); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Service) UpdateUser(ctx context.Context, orgID, userID uuid.UUID, name, avatarURL *string) (*User, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE users SET name = COALESCE($3, name), avatar_url = COALESCE($4, avatar_url), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, userID, orgID, name, avatarURL)
	if err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperrors.ErrNotFound
	}
	return s.GetUser(ctx, userID)
}

// DeactivateUser is a soft delete: rows elsewhere reference the user, and an
// audit trail that loses its actor is worse than a disabled account.
func (s *Service) DeactivateUser(ctx context.Context, orgID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE users SET is_active = FALSE, deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, userID, orgID)
	if err != nil {
		return fmt.Errorf("deactivate user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

// ── Roles ─────────────────────────────────────────────────────────────────

var defaultRolePermissions = []struct {
	Name        string
	Permissions []string
}{
	{"owner", []string{"*"}},
	{"admin", []string{"org:manage", "users:manage", "roles:manage", "channels:manage", "tasks:manage", "calls:manage", "settings:manage"}},
	{"manager", []string{"channels:read", "channels:write", "tasks:manage", "calls:manage", "reports:read"}},
	{"member", []string{"channels:read", "channels:write", "tasks:read", "tasks:write:self", "calls:join"}},
}

func (s *Service) CreateDefaultRoles(ctx context.Context, orgID uuid.UUID) error {
	for _, r := range defaultRolePermissions {
		perms, err := json.Marshal(r.Permissions)
		if err != nil {
			return fmt.Errorf("encode %s permissions: %w", r.Name, err)
		}
		if _, err := s.db.Exec(ctx, `
			INSERT INTO roles (id, org_id, name, permissions, is_system, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, $2, $3, true, NOW(), NOW())
			ON CONFLICT (org_id, name) DO UPDATE SET permissions = EXCLUDED.permissions, updated_at = NOW()
		`, orgID, r.Name, perms); err != nil {
			return fmt.Errorf("create role %s: %w", r.Name, err)
		}
	}
	return nil
}

func (s *Service) ListRoles(ctx context.Context, orgID uuid.UUID) ([]Role, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, org_id, name, COALESCE(description, ''), permissions, COALESCE(is_system, false), created_at, updated_at
		FROM roles WHERE org_id = $1 ORDER BY is_system DESC, name
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()

	roles := []Role{}
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Name, &r.Description, &r.Permissions,
			&r.IsSystem, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

func (s *Service) CreateRole(ctx context.Context, orgID uuid.UUID, name, description string, permissions []string) (*Role, error) {
	if permissions == nil {
		permissions = []string{}
	}
	perms, err := json.Marshal(permissions)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "invalid permissions")
	}
	var r Role
	err = s.db.QueryRow(ctx, `
		INSERT INTO roles (id, org_id, name, description, permissions, is_system, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, NULLIF($3, ''), $4, false, NOW(), NOW())
		RETURNING id, org_id, name, COALESCE(description, ''), permissions, COALESCE(is_system, false), created_at, updated_at
	`, orgID, name, description, perms).Scan(&r.ID, &r.OrgID, &r.Name, &r.Description,
		&r.Permissions, &r.IsSystem, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create role: %w", err)
	}
	return &r, nil
}

func (s *Service) UpdateRole(ctx context.Context, orgID, roleID uuid.UUID, name, description *string, permissions []string) (*Role, error) {
	var perms []byte
	if permissions != nil {
		var err error
		if perms, err = json.Marshal(permissions); err != nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "invalid permissions")
		}
	}
	var r Role
	err := s.db.QueryRow(ctx, `
		UPDATE roles SET
			name = COALESCE($3, name),
			description = COALESCE($4, description),
			permissions = COALESCE($5::jsonb, permissions),
			updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND is_system = FALSE
		RETURNING id, org_id, name, COALESCE(description, ''), permissions, COALESCE(is_system, false), created_at, updated_at
	`, roleID, orgID, name, description, perms).Scan(&r.ID, &r.OrgID, &r.Name, &r.Description,
		&r.Permissions, &r.IsSystem, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Either it does not exist here, or it is a system role.
			return nil, apperrors.New(apperrors.CodeForbidden, "role cannot be modified")
		}
		return nil, fmt.Errorf("update role: %w", err)
	}
	return &r, nil
}

// DeleteRole refuses the four built-in roles: removing them would strip every
// permission from everyone holding one, including the only owner.
func (s *Service) DeleteRole(ctx context.Context, orgID, roleID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM roles WHERE id = $1 AND org_id = $2 AND is_system = FALSE`, roleID, orgID)
	if err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.New(apperrors.CodeForbidden, "role cannot be deleted")
	}
	return nil
}

// AssignRole is idempotent, and refuses to cross organization boundaries: the
// user and the role must belong to the same tenant.
func (s *Service) AssignRole(ctx context.Context, userID, roleID, assignedBy uuid.UUID) error {
	var assigner any
	if assignedBy != uuid.Nil {
		assigner = assignedBy
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, assigned_by, assigned_at)
		SELECT u.id, r.id, $3, NOW()
		FROM users u JOIN roles r ON r.org_id = u.org_id
		WHERE u.id = $1 AND r.id = $2 AND u.deleted_at IS NULL
		ON CONFLICT (user_id, role_id) DO NOTHING
	`, userID, roleID, assigner)
	if err != nil {
		return fmt.Errorf("assign role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either already assigned (fine) or the pair does not line up.
		var exists bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM user_roles WHERE user_id = $1 AND role_id = $2)`,
			userID, roleID).Scan(&exists); err != nil {
			return fmt.Errorf("verify role assignment: %w", err)
		}
		if !exists {
			return apperrors.New(apperrors.CodeNotFound, "user or role not found in this organization")
		}
	}
	return nil
}

func (s *Service) RemoveRole(ctx context.Context, orgID, userID, roleID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM user_roles ur
		USING users u, roles r
		WHERE ur.user_id = u.id AND ur.role_id = r.id
		  AND ur.user_id = $1 AND ur.role_id = $2
		  AND u.org_id = $3 AND r.org_id = $3
	`, userID, roleID, orgID)
	if err != nil {
		return fmt.Errorf("remove role: %w", err)
	}
	return nil
}

// ── Teams ─────────────────────────────────────────────────────────────────

func (s *Service) CreateTeam(ctx context.Context, orgID uuid.UUID, name, description string, parentID, managerID *uuid.UUID) (*Team, error) {
	var t Team
	err := s.db.QueryRow(ctx, `
		INSERT INTO teams (id, org_id, parent_id, name, description, manager_id, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, NULLIF($4, ''), $5, NOW(), NOW())
		RETURNING id, org_id, parent_id, name, COALESCE(description, ''), manager_id, created_at, updated_at
	`, orgID, parentID, name, description, managerID).Scan(
		&t.ID, &t.OrgID, &t.ParentID, &t.Name, &t.Description, &t.ManagerID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create team: %w", err)
	}
	return &t, nil
}

func (s *Service) ListTeams(ctx context.Context, orgID uuid.UUID) ([]Team, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id, t.org_id, t.parent_id, t.name, COALESCE(t.description, ''), t.manager_id,
		       COUNT(tm.user_id), t.created_at, t.updated_at
		FROM teams t
		LEFT JOIN team_members tm ON tm.team_id = t.id
		WHERE t.org_id = $1 AND t.deleted_at IS NULL
		GROUP BY t.id ORDER BY t.name
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()

	teams := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.OrgID, &t.ParentID, &t.Name, &t.Description,
			&t.ManagerID, &t.MemberCount, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

func (s *Service) UpdateTeam(ctx context.Context, orgID, teamID uuid.UUID, name, description *string, managerID *uuid.UUID) (*Team, error) {
	var t Team
	err := s.db.QueryRow(ctx, `
		UPDATE teams SET name = COALESCE($3, name), description = COALESCE($4, description),
		                 manager_id = COALESCE($5, manager_id), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
		RETURNING id, org_id, parent_id, name, COALESCE(description, ''), manager_id, created_at, updated_at
	`, teamID, orgID, name, description, managerID).Scan(
		&t.ID, &t.OrgID, &t.ParentID, &t.Name, &t.Description, &t.ManagerID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("update team: %w", err)
	}
	return &t, nil
}

func (s *Service) DeleteTeam(ctx context.Context, orgID, teamID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE teams SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`,
		teamID, orgID)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

// AddTeamMember is idempotent and refuses users from another organization.
func (s *Service) AddTeamMember(ctx context.Context, orgID, teamID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO team_members (team_id, user_id, joined_at)
		SELECT t.id, u.id, NOW()
		FROM teams t JOIN users u ON u.org_id = t.org_id
		WHERE t.id = $1 AND u.id = $2 AND t.org_id = $3
		  AND t.deleted_at IS NULL AND u.deleted_at IS NULL
		ON CONFLICT (team_id, user_id) DO NOTHING
	`, teamID, userID, orgID)
	if err != nil {
		return fmt.Errorf("add team member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM team_members WHERE team_id = $1 AND user_id = $2)`,
			teamID, userID).Scan(&exists); err != nil {
			return fmt.Errorf("verify team membership: %w", err)
		}
		if !exists {
			return apperrors.New(apperrors.CodeNotFound, "team or user not found in this organization")
		}
	}
	return nil
}

func (s *Service) RemoveTeamMember(ctx context.Context, orgID, teamID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM team_members tm USING teams t
		WHERE tm.team_id = t.id AND tm.team_id = $1 AND tm.user_id = $2 AND t.org_id = $3
	`, teamID, userID, orgID)
	if err != nil {
		return fmt.Errorf("remove team member: %w", err)
	}
	return nil
}

func (s *Service) ListTeamMembers(ctx context.Context, orgID, teamID uuid.UUID) ([]User, error) {
	rows, err := s.db.Query(ctx, `
		SELECT u.id, u.org_id, u.name, u.email, u.avatar_url, u.role,
		       COALESCE(u.is_admin, false), COALESCE(u.is_active, true), u.created_at, u.updated_at
		FROM team_members tm
		JOIN users u ON u.id = tm.user_id
		JOIN teams t ON t.id = tm.team_id
		WHERE tm.team_id = $1 AND t.org_id = $2 AND u.deleted_at IS NULL
		ORDER BY u.name
	`, teamID, orgID)
	if err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}
	defer rows.Close()

	members := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.OrgID, &u.Name, &u.Email, &u.AvatarURL, &u.Role,
			&u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan team member: %w", err)
		}
		members = append(members, u)
	}
	return members, rows.Err()
}

// ── Configuration ─────────────────────────────────────────────────────────

// SetConfig stores a JSON value under a key for the organization. is_secret
// marks credentials so reads can exclude them from anything user-facing.
func (s *Service) SetConfig(ctx context.Context, orgID uuid.UUID, key string, value interface{}, isSecret bool) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return apperrors.New(apperrors.CodeBadRequest, "config value is not serialisable")
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO org_configs (org_id, key, value, is_secret, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (org_id, key) DO UPDATE
		SET value = EXCLUDED.value, is_secret = EXCLUDED.is_secret, updated_at = NOW()
	`, orgID, key, raw, isSecret)
	if err != nil {
		return fmt.Errorf("set config %s: %w", key, err)
	}
	return nil
}

func (s *Service) GetConfig(ctx context.Context, orgID uuid.UUID, key string) (interface{}, error) {
	var raw []byte
	err := s.db.QueryRow(ctx,
		`SELECT value FROM org_configs WHERE org_id = $1 AND key = $2`, orgID, key).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("get config %s: %w", key, err)
	}
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", key, err)
	}
	return value, nil
}

// GetConfigInto decodes a stored config value into a typed destination.
func (s *Service) GetConfigInto(ctx context.Context, orgID uuid.UUID, key string, dst interface{}) error {
	var raw []byte
	err := s.db.QueryRow(ctx,
		`SELECT value FROM org_configs WHERE org_id = $1 AND key = $2`, orgID, key).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apperrors.ErrNotFound
		}
		return fmt.Errorf("get config %s: %w", key, err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("decode config %s: %w", key, err)
	}
	return nil
}

// ListPublicConfigs returns non-secret configuration only. Secrets are stored
// alongside ordinary settings, so anything user-facing must go through here.
func (s *Service) ListPublicConfigs(ctx context.Context, orgID uuid.UUID) (map[string]interface{}, error) {
	rows, err := s.db.Query(ctx,
		`SELECT key, value FROM org_configs WHERE org_id = $1 AND is_secret = FALSE`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list configs: %w", err)
	}
	defer rows.Close()

	out := map[string]interface{}{}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, fmt.Errorf("scan config: %w", err)
		}
		var value interface{}
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode config %s: %w", key, err)
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Service) IsSetupComplete(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM organizations WHERE deleted_at IS NULL`).Scan(&count)
	return count > 0, err
}

// FirstOrganizationID resolves the instance's organization. TeamUp is
// single-tenant per deployment, so there is exactly one.
func (s *Service) FirstOrganizationID(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM organizations WHERE deleted_at IS NULL ORDER BY created_at LIMIT 1`).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, apperrors.ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("resolve organization: %w", err)
	}
	return id, nil
}

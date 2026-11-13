package org

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	netmail "net/mail"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"

	"github.com/teamup/teamup/internal/auth"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

type Service struct {
	db       *pgxpool.Pool
	logger   zerolog.Logger
	enroller ChannelEnroller
	// Told when somebody is deactivated.
	onDeactivated   []func(ctx context.Context, orgID, userID uuid.UUID)
	onSessionsEnded []func(ctx context.Context, orgID, userID uuid.UUID)
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

	// True while the member is still on the password an administrator
	// generated for them.
	MustChangePassword bool `json:"mustChangePassword"`
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

// ChannelEnroller puts a new member into the channels that belong to the whole
// organization. An interface so org does not depend on the chat package.
type ChannelEnroller interface {
	EnrolInOpenChannels(ctx context.Context, orgID, userID uuid.UUID) error
}

func (s *Service) SetChannelEnroller(e ChannelEnroller) { s.enroller = e }

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// SetLogger wires real logging. Left at the zero value every diagnostic in
// this package was written to a nil writer and silently discarded — including
// the fail-closed paths that swallow an error and deny access.
func (s *Service) SetLogger(l zerolog.Logger) {
	s.logger = l.With().Str("component", "org").Logger()
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
	       u.created_at, u.updated_at, COALESCE(u.must_change_password, false),
	       COALESCE(ARRAY_REMOVE(ARRAY_AGG(r.name), NULL), '{}')
	FROM users u
	LEFT JOIN user_roles ur ON ur.user_id = u.id
	LEFT JOIN roles r ON r.id = ur.role_id
	WHERE u.deleted_at IS NULL AND %s
	GROUP BY u.id`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.OrgID, &u.Name, &u.Email, &u.AvatarURL, &u.Role,
		&u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt, &u.MustChangePassword, &u.Roles)
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
			&u.IsAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt, &u.MustChangePassword, &u.Roles); err != nil {
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
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE users SET is_active = FALSE, deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, userID, orgID)
	if err != nil {
		return fmt.Errorf("deactivate user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	// Removing somebody ends what they already have open.
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// What they have open elsewhere goes too. Revoking sessions stops HTTP at once,
	// but a socket and a connection to a room carry on by themselves.
	for _, notify := range s.onDeactivated {
		notify(ctx, orgID, userID)
	}
	return nil
}

// OnSessionsEnded adds something to run once somebody's sessions have all
// been ended without them being deactivated, as a password reset does.
func (s *Service) OnSessionsEnded(notify func(ctx context.Context, orgID, userID uuid.UUID)) {
	s.onSessionsEnded = append(s.onSessionsEnded, notify)
}

// OnDeactivated adds something to run once somebody has been deactivated. It cannot
// undo the deactivation, so it reports its own failures.
func (s *Service) OnDeactivated(notify func(ctx context.Context, orgID, userID uuid.UUID)) {
	s.onDeactivated = append(s.onDeactivated, notify)
}

// ── Roles ─────────────────────────────────────────────────────────────────

func (s *Service) CreateDefaultRoles(ctx context.Context, orgID uuid.UUID) error {
	for _, r := range auth.DefaultRolePermissions() {
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
		FROM roles WHERE org_id = $1
		ORDER BY is_system DESC, CASE name WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'manager' THEN 2 WHEN 'member' THEN 3 ELSE 4 END, lower(name)
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

// checkGrant refuses a permission that is not on the list, or that whoever is
// granting it does not hold: nobody can make a role more powerful than they
// are, which is what stops a role manager making themselves an owner.
func checkGrant(permissions, held []string) ([]string, error) {
	out := []string{}
	for _, p := range permissions {
		p = strings.TrimSpace(p)
		if p == "" || slices.Contains(out, p) {
			continue
		}
		if !auth.IsPermission(p) {
			return nil, apperrors.New(apperrors.CodeValidation, fmt.Sprintf("%q is not a permission", p))
		}
		if !auth.Grants(held, p) {
			return nil, apperrors.New(apperrors.CodeForbidden, fmt.Sprintf("you cannot grant %q, because you do not have it", p))
		}
		out = append(out, p)
	}
	return out, nil
}

// roleWithin refuses to let somebody hand out, take away or delete a role
// with more power than they have.
func (s *Service) roleWithin(ctx context.Context, orgID, roleID uuid.UUID, held []string) error {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT permissions FROM roles WHERE id = $1 AND org_id = $2`, roleID, orgID).Scan(&raw)
	if err == pgx.ErrNoRows {
		return apperrors.New(apperrors.CodeNotFound, "user or role not found in this organization")
	}
	if err != nil {
		return fmt.Errorf("read role: %w", err)
	}
	var perms []string
	_ = json.Unmarshal(raw, &perms)
	for _, p := range perms {
		if !auth.Grants(held, p) {
			return apperrors.New(apperrors.CodeForbidden, "that role can do more than you can")
		}
	}
	return nil
}

// heldBy is every permission somebody holds through their roles, as they are now.
func (s *Service) heldBy(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.permissions FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("read permissions: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var perms []string
		_ = json.Unmarshal(raw, &perms)
		for _, p := range perms {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out, rows.Err()
}

// MayActOn refuses to let somebody deactivate, reset or edit a member who can
// do something they cannot: managing members is not a way past the rule that
// nobody gets more power than they hold. Signing in as an administrator after
// resetting their password, or deactivating the owner, would be exactly that.
// Somebody in another organization is not found.
func (s *Service) MayActOn(ctx context.Context, orgID, targetID uuid.UUID, held []string) error {
	var targetOrg uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT org_id FROM users WHERE id = $1 AND deleted_at IS NULL`, targetID).Scan(&targetOrg)
	if err == pgx.ErrNoRows || (err == nil && targetOrg != orgID) {
		return apperrors.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read member: %w", err)
	}
	theirs, err := s.heldBy(ctx, targetID)
	if err != nil {
		return err
	}
	for _, p := range theirs {
		if !auth.Grants(held, p) {
			return apperrors.New(apperrors.CodeForbidden, "they can do more than you can, so only somebody who can do as much may change their account")
		}
	}
	return nil
}

// RoleNameWithin is roleWithin for a role given by name, as adding a member
// does. A name that is no role here is left for the caller to refuse.
func (s *Service) RoleNameWithin(ctx context.Context, orgID uuid.UUID, name string, held []string) error {
	if name == "" {
		return nil
	}
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT id FROM roles WHERE org_id = $1 AND name = $2`, orgID, name).Scan(&id); err != nil {
		return nil
	}
	return s.roleWithin(ctx, orgID, id, held)
}

// CreateRole makes a role with permissions from the list, each held by
// whoever makes it.
func (s *Service) CreateRole(ctx context.Context, orgID uuid.UUID, name, description string, permissions, held []string) (*Role, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return nil, apperrors.New(apperrors.CodeValidation, "a role needs a name of at most 60 characters")
	}
	permissions, err := checkGrant(permissions, held)
	if err != nil {
		return nil, err
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
		if strings.Contains(err.Error(), "duplicate key") {
			return nil, apperrors.New(apperrors.CodeConflict, "there is already a role with that name")
		}
		return nil, fmt.Errorf("create role: %w", err)
	}
	return &r, nil
}

// UpdateRole changes a role the organization made; the built-in ones stay as
// they are. Its permissions are checked as when it is made, and somebody may
// not change a role that can already do more than they can.
func (s *Service) UpdateRole(ctx context.Context, orgID, roleID uuid.UUID, name, description *string, permissions, held []string) (*Role, error) {
	if err := s.roleWithin(ctx, orgID, roleID, held); err != nil {
		return nil, err
	}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" || len(trimmed) > 60 {
			return nil, apperrors.New(apperrors.CodeValidation, "a role needs a name of at most 60 characters")
		}
		name = &trimmed
	}
	var perms []byte
	if permissions != nil {
		checked, err := checkGrant(permissions, held)
		if err != nil {
			return nil, err
		}
		if perms, err = json.Marshal(checked); err != nil {
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
func (s *Service) DeleteRole(ctx context.Context, orgID, roleID uuid.UUID, held []string) error {
	if err := s.roleWithin(ctx, orgID, roleID, held); err != nil {
		return err
	}
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

// AssignRole is idempotent and confined to one organization: the caller's, the
// target user's and the role's must all agree. Requiring only that the user and
// role agree with each other let an admin of one tenant grant a role inside
// another.
func (s *Service) AssignRole(ctx context.Context, orgID, userID, roleID, assignedBy uuid.UUID, held []string) error {
	if err := s.roleWithin(ctx, orgID, roleID, held); err != nil {
		return err
	}
	var assigner any
	if assignedBy != uuid.Nil {
		assigner = assignedBy
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, assigned_by, assigned_at)
		SELECT u.id, r.id, $3, NOW()
		FROM users u JOIN roles r ON r.org_id = u.org_id
		WHERE u.id = $1 AND r.id = $2 AND u.org_id = $4 AND u.deleted_at IS NULL
		ON CONFLICT (user_id, role_id) DO NOTHING
	`, userID, roleID, assigner, orgID)
	if err != nil {
		return fmt.Errorf("assign role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either already assigned (fine) or the pair does not line up.
		var exists bool
		if err := s.db.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM user_roles ur
				JOIN users u ON u.id = ur.user_id
				WHERE ur.user_id = $1 AND ur.role_id = $2 AND u.org_id = $3)`,
			userID, roleID, orgID).Scan(&exists); err != nil {
			return fmt.Errorf("verify role assignment: %w", err)
		}
		if !exists {
			return apperrors.New(apperrors.CodeNotFound, "user or role not found in this organization")
		}
	}
	return nil
}

func (s *Service) RemoveRole(ctx context.Context, orgID, userID, roleID uuid.UUID, held []string) error {
	if err := s.roleWithin(ctx, orgID, roleID, held); err != nil {
		return err
	}
	// The organization keeps at least one owner.
	var lastOwner bool
	if err := s.db.QueryRow(ctx, `
		SELECT r.name = 'owner' AND r.is_system AND (
		    SELECT COUNT(*) FROM user_roles ur2 JOIN users u ON u.id = ur2.user_id
		    WHERE ur2.role_id = r.id AND u.deleted_at IS NULL) <= 1
		FROM roles r WHERE r.id = $1 AND r.org_id = $2`, roleID, orgID).Scan(&lastOwner); err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("check owners: %w", err)
	}
	if lastOwner {
		return apperrors.New(apperrors.CodeConflict, "the organization must keep at least one owner")
	}
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

// Actor is who is asking to change the shape of the team tree.
//
// An organization's administrators reshape all of it. Anybody else reshapes
// only the subtree they already run: a team lead can open a team underneath
// one of theirs and move a team of theirs to another place underneath one of
// theirs, so that a reorganization inside one department does not have to
// queue behind an administrator. They cannot open a team at the top level,
// move a team out of their own reach, or touch anything above or beside them.
//
// The zero value is the least privilege there is — nobody, running nothing —
// so a caller that forgets to fill this in is refused rather than obeyed.
type Actor struct {
	UserID  uuid.UUID
	IsAdmin bool
}

func (s *Service) CreateTeam(ctx context.Context, orgID uuid.UUID, actor Actor, name, description string, parentID, managerID *uuid.UUID) (*Team, error) {
	if err := s.requireSameOrgUser(ctx, orgID, managerID); err != nil {
		return nil, err
	}
	if err := s.requireSameOrgTeam(ctx, orgID, parentID); err != nil {
		return nil, err
	}
	if err := requireManagerIsSelf(actor, managerID); err != nil {
		return nil, err
	}

	// The authority check and the insert are one unit, for the same reason
	// UpdateTeam's cycle check is: it is a question about the tree that the
	// write is about to join, and the tree moves. Checked on the pool and then
	// written, a lead can be told their parent is theirs, have that parent
	// moved out from under them, and still land the new team in a subtree that
	// is no longer theirs. Same per-organization lock, so a create and a move
	// cannot interleave inside each other.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("create team: %w", err)
	}
	defer tx.Rollback(ctx)

	if !actor.IsAdmin {
		if err := lockTeamTree(ctx, tx, orgID); err != nil {
			return nil, err
		}
		if err := s.requireMayNestUnder(ctx, tx, orgID, actor, parentID); err != nil {
			return nil, err
		}
	}

	var t Team
	err = tx.QueryRow(ctx, `
		INSERT INTO teams (id, org_id, parent_id, name, description, manager_id, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, NULLIF($4, ''), $5, NOW(), NOW())
		RETURNING id, org_id, parent_id, name, COALESCE(description, ''), manager_id, created_at, updated_at
	`, orgID, parentID, name, description, managerID).Scan(
		&t.ID, &t.OrgID, &t.ParentID, &t.Name, &t.Description, &t.ManagerID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create team: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
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

// ManagesTeam reports whether this person runs the team, directly or through
// a team above it. Running a team is what entitles somebody to decide who is
// on it, and to reshape what is underneath it, without making them an
// administrator of the whole organization.
func (s *Service) ManagesTeam(ctx context.Context, orgID, teamID, userID uuid.UUID) (bool, error) {
	return s.managesTeam(ctx, s.db, orgID, teamID, userID)
}

// managesTeam is the one rule, asked of a particular connection. A check that
// guards a write has to be asked of the transaction doing the write: the
// answer is read off the team tree, and the team tree is what is being
// changed.
func (s *Service) managesTeam(ctx context.Context, q querier, orgID, teamID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `
		WITH RECURSIVE managed AS (
			SELECT t.id FROM teams t
			WHERE t.manager_id = $3 AND t.org_id = $1 AND t.deleted_at IS NULL
			UNION
			SELECT child.id FROM teams child
			JOIN managed ON child.parent_id = managed.id
			WHERE child.deleted_at IS NULL
		)
		SELECT EXISTS (SELECT 1 FROM managed WHERE id = $2)
	`, orgID, teamID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check team management: %w", err)
	}
	return ok, nil
}

// UpdateTeam applies only the fields that were supplied.
//
// parentID is a pointer to a pointer because moving a team needs three cases,
// not two: nil leaves the team where it is, a pointer to nil lifts it to the
// top level, and a pointer to an id files it under that team. A plain
// *uuid.UUID could not say "make this a root" — which is why re-parenting was
// missing from the update path entirely.
func (s *Service) UpdateTeam(ctx context.Context, orgID, teamID uuid.UUID, actor Actor, name, description *string, managerID *uuid.UUID, parentID **uuid.UUID) (*Team, error) {
	if err := s.requireSameOrgUser(ctx, orgID, managerID); err != nil {
		return nil, err
	}
	// Naming a manager displaces whoever holds the job. A lead may run a team
	// they have just opened, but moving the job between people — even inside
	// their own subtree — is an administrator's decision.
	if !actor.IsAdmin && managerID != nil {
		return nil, apperrors.New(apperrors.CodeForbidden,
			"only an administrator can change who runs a team")
	}

	var newParent *uuid.UUID
	if parentID != nil {
		newParent = *parentID
		if err := s.requireSameOrgTeam(ctx, orgID, newParent); err != nil {
			return nil, err
		}
	}

	// A move needs the cycle check and the write to be one unit. Checked and
	// then written as separate statements, two admins reorganising at once can
	// each be told their move is fine — A under B, B under A — and both commit,
	// producing exactly the cycle the check exists to prevent. The consequence
	// is silent: every walk UNIONs so nothing hangs, and instead everyone in
	// the cycle becomes everyone else's manager.
	//
	// The lock is per organisation rather than per row because a cycle spans an
	// arbitrary set of teams, so there is no fixed set to lock. Moving a team is
	// a rare administrative act, so serialising it within one organisation costs
	// nothing. DeleteTeam takes a transaction for a weaker race already.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("update team: %w", err)
	}
	defer tx.Rollback(ctx)

	// A non-administrator needs the lock whatever they are changing, because
	// whatever they are changing is gated on a subtree read from this same
	// tree. An administrator needs it only for a move.
	if parentID != nil || !actor.IsAdmin {
		if err := lockTeamTree(ctx, tx, orgID); err != nil {
			return nil, err
		}
	}

	// Authority before shape, so that somebody with no business here learns
	// only that they have no business here, rather than what the tree looks
	// like.
	if err := s.requireRunsTeam(ctx, tx, orgID, actor, teamID,
		"you do not manage this team"); err != nil {
		return nil, err
	}
	if parentID != nil {
		if err := s.requireMayNestUnder(ctx, tx, orgID, actor, newParent); err != nil {
			return nil, err
		}
		if err := s.requireNoCycle(ctx, tx, orgID, teamID, newParent); err != nil {
			return nil, err
		}
	}

	var t Team
	err = tx.QueryRow(ctx, `
		UPDATE teams SET name = COALESCE($3, name), description = COALESCE($4, description),
		                 manager_id = COALESCE($5, manager_id),
		                 parent_id = CASE WHEN $6 THEN $7 ELSE parent_id END,
		                 updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
		RETURNING id, org_id, parent_id, name, COALESCE(description, ''), manager_id, created_at, updated_at
	`, teamID, orgID, name, description, managerID, parentID != nil, newParent).Scan(
		&t.ID, &t.OrgID, &t.ParentID, &t.Name, &t.Description, &t.ManagerID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("update team: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("update team: %w", err)
	}
	return &t, nil
}

// querier is whatever can run a query — the pool or a transaction. A check that
// guards a write has to run on the same connection as that write, or it is
// answering a question about a state the write will not see.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// lockTeamTree serialises everything that reads the shape of one
// organization's team tree in order to change it. Per organization rather than
// per row because the questions asked — is this a cycle, is this team mine —
// span an arbitrary set of teams, so there is no fixed set to lock. Reshaping
// a team tree is a rare administrative act, so serialising it within one
// organization costs nothing.
func lockTeamTree(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1))`, orgID.String()); err != nil {
		return fmt.Errorf("lock team tree: %w", err)
	}
	return nil
}

// requireRunsTeam refuses unless the actor runs this team. Administrators run
// everything; everybody else is asked of q, which must be the transaction
// doing the write — see managesTeam.
func (s *Service) requireRunsTeam(ctx context.Context, q querier, orgID uuid.UUID, actor Actor, teamID uuid.UUID, refusal string) error {
	if actor.IsAdmin {
		return nil
	}
	manages, err := s.managesTeam(ctx, q, orgID, teamID, actor.UserID)
	if err != nil {
		return err
	}
	if !manages {
		// The same refusal whether the team is somebody else's or does not
		// exist, so ids cannot be probed.
		return apperrors.New(apperrors.CodeForbidden, refusal)
	}
	return nil
}

// requireMayNestUnder decides where a team is allowed to be filed.
//
// A lead may only choose a parent they already run, which keeps the team
// inside their own subtree both before and after the change. Refusing the move
// out is the whole point: a team filed under somebody else is gone for good as
// far as the lead is concerned — they could not move it back — and it hands
// their people to whoever runs the destination, who never agreed to take them.
// That is a reorganization, not a lead reshaping their own patch, so it stays
// with the administrators.
//
// The top level is nobody's subtree, so a lead cannot put a team there either:
// a root is a new branch of the organization rather than a rearrangement of an
// existing one.
func (s *Service) requireMayNestUnder(ctx context.Context, q querier, orgID uuid.UUID, actor Actor, parentID *uuid.UUID) error {
	if actor.IsAdmin {
		return nil
	}
	if parentID == nil {
		return apperrors.New(apperrors.CodeForbidden,
			"only an administrator can put a team at the top level")
	}
	return s.requireRunsTeam(ctx, q, orgID, actor, *parentID,
		"you can only file a team under one you manage")
}

// requireManagerIsSelf lets a lead run the team they are opening and stops
// them handing it to anybody else. Appointing a third party would grant
// authority over the lead's own people to somebody the lead has no standing to
// promote; naming themselves grants nothing they do not already have through
// the parent. Leaving it unset is the ordinary case and works the same way.
func requireManagerIsSelf(actor Actor, managerID *uuid.UUID) error {
	if actor.IsAdmin || managerID == nil {
		return nil
	}
	if *managerID != actor.UserID {
		return apperrors.New(apperrors.CodeForbidden,
			"only an administrator can put somebody else in charge of a team")
	}
	return nil
}

// requireNoCycle refuses to file a team underneath itself or underneath one of
// its own descendants. parent_id has no database-level check for this, and the
// consequences are not loud: every walk of the tree UNIONs, so a cycle
// terminates rather than hanging, and instead quietly makes everybody in the
// cycle a manager of everybody else — a team lead below somebody gains control
// of the team above them. The walk below starts at the team itself, so
// re-parenting a team under itself is caught by the same query.
func (s *Service) requireNoCycle(ctx context.Context, q querier, orgID, teamID uuid.UUID, parentID *uuid.UUID) error {
	if parentID == nil {
		return nil
	}
	var loops bool
	if err := q.QueryRow(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT t.id FROM teams t
			WHERE t.id = $1 AND t.org_id = $3 AND t.deleted_at IS NULL
			UNION
			SELECT child.id FROM teams child
			JOIN subtree ON child.parent_id = subtree.id
			WHERE child.deleted_at IS NULL
		)
		SELECT EXISTS (SELECT 1 FROM subtree WHERE id = $2)
	`, teamID, *parentID, orgID).Scan(&loops); err != nil {
		return fmt.Errorf("check team ancestry: %w", err)
	}
	if loops {
		return apperrors.New(apperrors.CodeBadRequest,
			"a team cannot be moved underneath itself or one of the teams below it")
	}
	return nil
}

// DeleteTeam soft-deletes a team and grafts its children onto the parent it
// had.
//
// Grafting rather than refusing, deliberately. The delete is soft, so the
// ON DELETE SET NULL on parent_id never fires: without this the children go on
// pointing at a row that no query returns, which drops the entire subtree out
// of every manager's view and out of the org chart while leaving the teams
// themselves alive and assignable. Refusing would be safe but it makes the
// admin do the same re-parenting by hand before every reorganisation, and the
// tempting shortcut — deleting the children first — loses their membership.
// Grafting keeps every surviving team reachable from a root and preserves the
// management chain: whoever ran the deleted team's parent still runs what is
// left. A child of a root simply becomes a root itself.
func (s *Service) DeleteTeam(ctx context.Context, orgID, teamID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	defer tx.Rollback(ctx)

	// Locked, so a re-parent landing between the graft and the delete cannot
	// leave a child hanging off the deleted row.
	var grandparent *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT parent_id FROM teams
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
		FOR UPDATE
	`, teamID, orgID).Scan(&grandparent)
	if err != nil {
		if err == pgx.ErrNoRows {
			return apperrors.ErrNotFound
		}
		return fmt.Errorf("delete team: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE teams SET parent_id = $3, updated_at = NOW()
		WHERE parent_id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, teamID, orgID, grandparent); err != nil {
		return fmt.Errorf("reparent the children of a deleted team: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE teams SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, teamID, orgID); err != nil {
		return fmt.Errorf("delete team: %w", err)
	}

	return tx.Commit(ctx)
}

// requireSameOrgUser and requireSameOrgTeam guard the nullable foreign keys.
// The database constraints are global, so without these a team could be
// managed by — or nested under — a row belonging to another tenant.
func (s *Service) requireSameOrgUser(ctx context.Context, orgID uuid.UUID, userID *uuid.UUID) error {
	if userID == nil {
		return nil
	}
	var ok bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
		*userID, orgID).Scan(&ok); err != nil {
		return fmt.Errorf("check user organization: %w", err)
	}
	if !ok {
		return apperrors.New(apperrors.CodeNotFound, "user not found in this organization")
	}
	return nil
}

func (s *Service) requireSameOrgTeam(ctx context.Context, orgID uuid.UUID, teamID *uuid.UUID) error {
	if teamID == nil {
		return nil
	}
	var ok bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM teams WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
		*teamID, orgID).Scan(&ok); err != nil {
		return fmt.Errorf("check team organization: %w", err)
	}
	if !ok {
		return apperrors.New(apperrors.CodeNotFound, "team not found in this organization")
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

// AddTeamMemberAs is AddTeamMember on somebody's behalf. Authority over a person
// is read off team_members, so adding them is claiming them: a lead may only
// claim somebody with no ties outside their chart, or somebody already in it,
// and running a team is a tie even for somebody who sits on none. Otherwise a
// lead could put their own boss on their team and start managing them.
func (s *Service) AddTeamMemberAs(ctx context.Context, orgID, teamID uuid.UUID, actor Actor, userID uuid.UUID) error {
	if !actor.IsAdmin && actor.UserID != userID {
		var allowed bool
		err := s.db.QueryRow(ctx, `
			WITH RECURSIVE managed AS (
				SELECT t.id FROM teams t
				WHERE t.manager_id = $2 AND t.org_id = $1 AND t.deleted_at IS NULL
				UNION
				SELECT child.id FROM teams child
				JOIN managed ON child.parent_id = managed.id
				WHERE child.deleted_at IS NULL
			)
			SELECT EXISTS (
				SELECT 1 FROM team_members tm
				JOIN managed ON managed.id = tm.team_id
				WHERE tm.user_id = $3
			) OR NOT EXISTS (
				SELECT 1 FROM teams t
				WHERE t.org_id = $1 AND t.deleted_at IS NULL
				  AND t.id NOT IN (SELECT id FROM managed)
				  AND (t.manager_id = $3 OR EXISTS (
				      SELECT 1 FROM team_members tm
				      WHERE tm.team_id = t.id AND tm.user_id = $3))
			)
		`, orgID, actor.UserID, userID).Scan(&allowed)
		if err != nil {
			return fmt.Errorf("check who may be added: %w", err)
		}
		if !allowed {
			return apperrors.New(apperrors.CodeForbidden,
				"that person belongs to another team; ask an administrator to add them")
		}
	}
	return s.AddTeamMember(ctx, orgID, teamID, userID)
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

// ── Adding members ────────────────────────────────────────────────────────

// temporaryPasswordLength comfortably clears the 12-character minimum that
// applies everywhere else.
const temporaryPasswordLength = 16

// AddMember creates an account an administrator can hand to a colleague, and
// returns the generated password once — it is never stored in the clear and
// cannot be shown again.
//
// No email is sent: SMTP is optional in v1 and usually unconfigured, so an
// emailed invitation would fail silently on most installs. The administrator
// passes the password on however they already communicate.
func (s *Service) AddMember(ctx context.Context, orgID uuid.UUID, name, email, roleName string) (*User, string, error) {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" || email == "" {
		return nil, "", apperrors.New(apperrors.CodeBadRequest, "a name and email are required")
	}
	if roleName == "" {
		roleName = "member"
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)

	// The role has to exist in this organization, or the member signs in and
	// can do nothing — the trap of creating a user by hand.
	var roleID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM roles WHERE org_id = $1 AND name = $2`, orgID, roleName).Scan(&roleID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, "", apperrors.New(apperrors.CodeBadRequest, "that role does not exist in this organization")
		}
		return nil, "", fmt.Errorf("look up role: %w", err)
	}

	password, err := generateTemporaryPassword()
	if err != nil {
		return nil, "", fmt.Errorf("generate temporary password: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash temporary password: %w", err)
	}

	userID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO users (id, org_id, name, email, password_hash, role, is_admin,
		                   must_change_password, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, NOW(), NOW())
	`, userID, orgID, name, email, hash, roleName, roleName == "owner" || roleName == "admin")
	if err != nil {
		// (org_id, email) is unique among people who have not been deleted;
		// report it as a conflict rather than a server error so the form can
		// say something useful.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, "", apperrors.New(apperrors.CodeConflict, "somebody with that email is already in this organization")
		}
		return nil, "", fmt.Errorf("create member: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, assigned_at) VALUES ($1, $2, NOW())
		ON CONFLICT (user_id, role_id) DO NOTHING
	`, userID, roleID); err != nil {
		return nil, "", fmt.Errorf("assign role: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}

	// A new member belongs in the channels that belong to everybody, so the
	// organization's announcements reach them from their first day rather than
	// only once they happen to open the channel. Best effort: failing here must
	// not undo the account.
	if s.enroller != nil {
		if err := s.enroller.EnrolInOpenChannels(ctx, orgID, userID); err != nil {
			s.logger.Warn().Err(err).Msg("could not add new member to the organization's open channels")
		}
	}

	member, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	return member, password, nil
}

// ResetMemberPassword replaces a member's password with a generated one they
// have to change at their next sign-in, ends their sessions, and returns the
// password once. Only an owner may reset an owner: otherwise an admin could
// sign in as somebody who outranks them.
func (s *Service) ResetMemberPassword(ctx context.Context, orgID, userID uuid.UUID, callerIsOwner bool) (string, error) {
	target, err := s.GetUser(ctx, userID)
	if err != nil {
		return "", err
	}
	// Another organization's member is "not found", so ids cannot be probed.
	if target.OrgID != orgID {
		return "", apperrors.ErrNotFound
	}
	if !callerIsOwner && slices.Contains(target.Roles, "owner") {
		return "", apperrors.New(apperrors.CodeForbidden, "only an owner can reset an owner's password")
	}

	password, err := generateTemporaryPassword()
	if err != nil {
		return "", fmt.Errorf("generate temporary password: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash temporary password: %w", err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE users SET password_hash = $3, must_change_password = TRUE, updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, userID, orgID, hash)
	if err != nil {
		return "", fmt.Errorf("store temporary password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", apperrors.ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return "", fmt.Errorf("revoke sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	// What they had open is ended too, not only what they could open next.
	for _, notify := range s.onSessionsEnded {
		notify(ctx, orgID, userID)
	}
	return password, nil
}

// ── Bulk onboarding ───────────────────────────────────────────────────────

// maxImportRows caps one batch, and the number is set by the server's write
// timeout rather than by taste.
//
// Every row costs a bcrypt hash at the default cost, which cannot be
// parallelised away without handing the whole box to one request. A 200-row
// import was measured at 29.0s against the 30s WriteTimeout in cmd/teamup —
// 97% of the budget on an idle machine, so a slightly busier one loses the
// response. That is not a retryable failure: rows commit individually by
// design, so the accounts exist, must_change_password is set, and the only
// copy of those passwords was in the body that never arrived. Re-running
// reports every one of them as a duplicate.
//
// 50 rows is roughly 7s, which leaves room for a loaded box, and a company of
// 50-60 people onboards in one or two batches. Raising this means raising
// WriteTimeout with it, or moving the work off the request.
const maxImportRows = 50

// Outcomes of one imported row. A failure is per-row and reported, never fatal
// to the batch: an administrator pasting 50 colleagues in cannot be made to
// lose the other 49 because one address was mistyped.
const (
	ImportCreated   = "created"
	ImportDuplicate = "duplicate"
	ImportInvalid   = "invalid"
	ImportFailed    = "failed"
)

// ImportOutcome is what happened to a single line of the file. Row is the
// line number in the uploaded file, so it lines up with what the
// administrator sees in their spreadsheet.
type ImportOutcome struct {
	Row               int        `json:"row"`
	Name              string     `json:"name"`
	Email             string     `json:"email"`
	Status            string     `json:"status"`
	Message           string     `json:"message,omitempty"`
	TemporaryPassword string     `json:"temporaryPassword,omitempty"`
	UserID            *uuid.UUID `json:"userId,omitempty"`
	// Emailed says their details were sent to them.
	Emailed bool `json:"emailed"`
}

// ImportMembers creates an account for every row of a CSV and reports each one
// separately. It builds on AddMember rather than reimplementing it, so every
// imported account gets the same transaction, the same role assignment and the
// same enrolment in the organization's open channels as one added by hand.
//
// The error return is reserved for a file that cannot be used at all —
// unparseable, no usable header, or past the cap. Anything wrong with an
// individual row comes back as that row's outcome.
func (s *Service) ImportMembers(ctx context.Context, orgID uuid.UUID, held []string, file string) ([]ImportOutcome, error) {
	reader := csv.NewReader(strings.NewReader(file))
	// Tolerate ragged rows: a spreadsheet often drops the trailing empty
	// columns, and that is not a reason to reject somebody's whole team.
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "that file could not be read as CSV")
	}
	columns := map[string]int{}
	for i, name := range header {
		columns[strings.ToLower(strings.TrimSpace(name))] = i
	}
	nameCol, hasName := columns["name"]
	emailCol, hasEmail := columns["email"]
	if !hasName || !hasEmail {
		return nil, apperrors.New(apperrors.CodeBadRequest,
			"the first row has to name the columns, and must include name and email")
	}
	roleCol, hasRole := columns["role"]
	teamCol, hasTeam := columns["team"]

	type row struct {
		line                  int
		name, email, roleName string
		teamName              string
	}
	var rows []row
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "that file could not be read as CSV: "+err.Error())
		}
		line, _ := reader.FieldPos(0)
		field := func(idx int, present bool) string {
			if !present || idx >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[idx])
		}
		r := row{
			line:     line,
			name:     field(nameCol, true),
			email:    field(emailCol, true),
			roleName: field(roleCol, hasRole),
			teamName: field(teamCol, hasTeam),
		}
		// A wholly blank line is spreadsheet noise, not a person.
		if r.name == "" && r.email == "" && r.roleName == "" && r.teamName == "" {
			continue
		}
		if len(rows) >= maxImportRows {
			return nil, apperrors.New(apperrors.CodeBadRequest,
				fmt.Sprintf("that file has more than %d people in it; split it up", maxImportRows))
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "that file has no rows in it")
	}

	// One lookup for the whole batch, scoped to this organization: a team name
	// belonging to another tenant must not resolve, however it is spelled.
	teams, err := s.ListTeams(ctx, orgID)
	if err != nil {
		return nil, err
	}
	teamsByName := map[string]uuid.UUID{}
	for _, t := range teams {
		teamsByName[strings.ToLower(t.Name)] = t.ID
	}

	outcomes := make([]ImportOutcome, 0, len(rows))
	for _, r := range rows {
		outcome := ImportOutcome{Row: r.line, Name: r.name, Email: r.email}

		// The add-member form rejects a malformed address before it reaches
		// AddMember, which only insists on something non-empty. Checking here
		// keeps the two paths to the same standard rather than letting an
		// import create accounts nobody can be reached at.
		if r.email != "" {
			if _, err := netmail.ParseAddress(r.email); err != nil {
				outcome.Status = ImportInvalid
				outcome.Message = "that is not an email address"
				outcomes = append(outcomes, outcome)
				continue
			}
		}

		// The team is resolved before the account is created, so a row naming
		// a team that does not exist here is rejected whole rather than
		// leaving a member behind who is on nobody's team.
		var teamID uuid.UUID
		if r.teamName != "" {
			id, ok := teamsByName[strings.ToLower(r.teamName)]
			if !ok {
				outcome.Status = ImportInvalid
				outcome.Message = fmt.Sprintf("there is no team called %q here", r.teamName)
				outcomes = append(outcomes, outcome)
				continue
			}
			teamID = id
		}

		// Nobody gives a new member a role that can do more than they can.
		if err := s.RoleNameWithin(ctx, orgID, r.roleName, held); err != nil {
			outcome.Status = ImportInvalid
			outcome.Message = fmt.Sprintf("you cannot give the %s role", r.roleName)
			outcomes = append(outcomes, outcome)
			continue
		}
		member, password, err := s.AddMember(ctx, orgID, r.name, r.email, r.roleName)
		if err != nil {
			outcome.Status = importStatusFor(err)
			outcome.Message = apperrors.PublicMessage(err, "could not be added")
			outcomes = append(outcomes, outcome)
			continue
		}

		outcome.Status = ImportCreated
		outcome.TemporaryPassword = password
		outcome.UserID = &member.ID
		if teamID != uuid.Nil {
			// The account exists either way; say so rather than pretending the
			// row failed and inviting the administrator to run it again.
			if err := s.AddTeamMember(ctx, orgID, teamID, member.ID); err != nil {
				outcome.Message = fmt.Sprintf("added, but could not be put on %s: %v", r.teamName, err)
			}
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

func importStatusFor(err error) string {
	switch apperrors.GetCode(err) {
	case apperrors.CodeConflict:
		return ImportDuplicate
	case apperrors.CodeBadRequest, apperrors.CodeValidation, apperrors.CodeNotFound:
		return ImportInvalid
	default:
		return ImportFailed
	}
}

// generateTemporaryPassword builds a readable, unambiguous password. The
// alphabet omits characters that are easily confused when a password is read
// out or copied by hand.
func generateTemporaryPassword() (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, temporaryPasswordLength)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}

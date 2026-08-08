package org

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type Service struct {
	db     *pgxpool.Pool
	logger zerolog.Logger
}

type Organization struct {
	ID        uuid.UUID
	Name      string
	Subdomain string
	LogoURL   *string
	Settings  map[string]interface{}
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type User struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Name      string
	Email     string
	AvatarURL *string
	Role      string
	IsAdmin   bool
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Role struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	Name        string
	Description string
	Permissions []string
	IsSystem    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Team struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	ParentID    *uuid.UUID
	Name        string
	Description string
	ManagerID   *uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

func (s *Service) CreateOrganization(ctx context.Context, name, subdomain string, logoURL *string) (*Organization, error) {
	org := &Organization{
		ID:        uuid.New(),
		Name:      name,
		Subdomain: subdomain,
		LogoURL:   logoURL,
		Settings:  map[string]interface{}{},
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	// INSERT INTO organizations ...
	return org, nil
}

func (s *Service) GetOrganization(ctx context.Context, id uuid.UUID) (*Organization, error) {
	// SELECT * FROM organizations WHERE id = ...
	return nil, nil
}

func (s *Service) GetOrganizationBySubdomain(ctx context.Context, subdomain string) (*Organization, error) {
	// SELECT * FROM organizations WHERE subdomain = ...
	return nil, nil
}

func (s *Service) CreateUser(ctx context.Context, orgID uuid.UUID, name, email, passwordHash string, role string, isAdmin bool) (*User, error) {
	user := &User{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      name,
		Email:     email,
		Role:      role,
		IsAdmin:   isAdmin,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	// INSERT INTO users ...
	return user, nil
}

func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	// SELECT * FROM users WHERE id = ...
	return nil, nil
}

func (s *Service) GetUserByEmail(ctx context.Context, orgID uuid.UUID, email string) (*User, error) {
	// SELECT * FROM users WHERE org_id = ... AND email = ...
	return nil, nil
}

func (s *Service) CreateDefaultRoles(ctx context.Context, orgID uuid.UUID) error {
	roles := []Role{
		{ID: uuid.New(), OrgID: orgID, Name: "owner", Permissions: []string{"*"}, IsSystem: true},
		{ID: uuid.New(), OrgID: orgID, Name: "admin", Permissions: []string{"org:manage", "users:manage", "roles:manage", "channels:manage", "tasks:manage", "calls:manage", "settings:manage"}, IsSystem: true},
		{ID: uuid.New(), OrgID: orgID, Name: "manager", Permissions: []string{"channels:read", "channels:write", "tasks:manage", "calls:manage", "reports:read"}, IsSystem: true},
		{ID: uuid.New(), OrgID: orgID, Name: "member", Permissions: []string{"channels:read", "channels:write", "tasks:read", "tasks:write:self", "calls:join"}, IsSystem: true},
	}
	for _, r := range roles {
		// INSERT INTO roles ...
		_ = r
	}
	return nil
}

func (s *Service) AssignRole(ctx context.Context, userID, roleID, assignedBy uuid.UUID) error {
	// INSERT INTO user_roles ...
	return nil
}

func (s *Service) CreateTeam(ctx context.Context, orgID uuid.UUID, name, description string, parentID, managerID *uuid.UUID) (*Team, error) {
	team := &Team{
		ID:          uuid.New(),
		OrgID:       orgID,
		ParentID:    parentID,
		Name:        name,
		Description: description,
		ManagerID:   managerID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	// INSERT INTO teams ...
	return team, nil
}

func (s *Service) AddTeamMember(ctx context.Context, teamID, userID uuid.UUID) error {
	// INSERT INTO team_members ...
	return nil
}

func (s *Service) SetConfig(ctx context.Context, orgID uuid.UUID, key string, value interface{}, isSecret bool) error {
	// INSERT INTO org_configs (org_id, key, value, is_secret) VALUES ... ON CONFLICT (org_id, key) DO UPDATE SET value = EXCLUDED.value
	return nil
}

func (s *Service) GetConfig(ctx context.Context, orgID uuid.UUID, key string) (interface{}, error) {
	// SELECT value FROM org_configs WHERE org_id = ... AND key = ...
	return nil, nil
}

func (s *Service) IsSetupComplete(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&count)
	return count > 0, err
}

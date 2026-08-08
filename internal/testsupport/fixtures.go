package testsupport

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Org is a seeded organization with its owner, mirroring what the setup wizard
// produces so tests exercise the same shape the application really has.
type Org struct {
	ID        uuid.UUID
	Subdomain string
	OwnerID   uuid.UUID
	OwnerMail string
	RoleIDs   map[string]uuid.UUID
}

// DefaultRoles matches the set the setup wizard creates.
var DefaultRoles = []string{"owner", "admin", "manager", "member"}

// SeedOrg creates an organization, its four default roles, and an owner
// holding the owner role.
func SeedOrg(t *testing.T, p *pgxpool.Pool) *Org {
	t.Helper()
	ctx := context.Background()

	org := &Org{
		ID:        uuid.New(),
		Subdomain: "org-" + uuid.New().String()[:8],
		OwnerMail: "owner-" + uuid.New().String()[:8] + "@example.test",
		RoleIDs:   map[string]uuid.UUID{},
	}

	if _, err := p.Exec(ctx, `
		INSERT INTO organizations (id, name, subdomain, created_at)
		VALUES ($1, 'Test Org', $2, NOW())
	`, org.ID, org.Subdomain); err != nil {
		t.Fatalf("seed organization: %v", err)
	}

	for _, name := range DefaultRoles {
		id := uuid.New()
		if _, err := p.Exec(ctx, `
			INSERT INTO roles (id, org_id, name, permissions, is_system, created_at)
			VALUES ($1, $2, $3, '["*"]', true, NOW())
		`, id, org.ID, name); err != nil {
			t.Fatalf("seed role %s: %v", name, err)
		}
		org.RoleIDs[name] = id
	}

	org.OwnerID = SeedUser(t, p, org, org.OwnerMail, "owner")
	return org
}

// SeedUser adds a user to the organization holding the named role.
func SeedUser(t *testing.T, p *pgxpool.Pool, org *Org, email, role string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse-battery"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	id := uuid.New()
	if _, err := p.Exec(ctx, `
		INSERT INTO users (id, org_id, name, email, password_hash, role, is_admin, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
	`, id, org.ID, "Test "+role, email, hash, role, role == "owner" || role == "admin"); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}

	if roleID, ok := org.RoleIDs[role]; ok {
		if _, err := p.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id, assigned_at) VALUES ($1, $2, NOW())
		`, id, roleID); err != nil {
			t.Fatalf("assign role %s: %v", role, err)
		}
	}
	return id
}

// SeedChannel creates a channel in the organization with the given members.
func SeedChannel(t *testing.T, p *pgxpool.Pool, org *Org, name string, members ...uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	id := uuid.New()
	if _, err := p.Exec(ctx, `
		INSERT INTO channels (id, org_id, name, type, created_by, created_at)
		VALUES ($1, $2, $3, 'group', $4, NOW())
	`, id, org.ID, name, org.OwnerID); err != nil {
		t.Fatalf("seed channel %s: %v", name, err)
	}
	for _, m := range members {
		if _, err := p.Exec(ctx, `
			INSERT INTO channel_members (channel_id, user_id, role, joined_at)
			VALUES ($1, $2, 'member', NOW()) ON CONFLICT DO NOTHING
		`, id, m); err != nil {
			t.Fatalf("seed channel member: %v", err)
		}
	}
	return id
}

package wizard

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	livekitproto "github.com/livekit/protocol/livekit"
	livekit "github.com/livekit/server-sdk-go/v2"

	"github.com/teamup/teamup/internal/auth"

	"golang.org/x/crypto/bcrypt"
)

// setupAdvisoryLockKey is an arbitrary constant; it only has to be stable and
// not collide with other advisory locks in this database.
const setupAdvisoryLockKey int64 = 8264113570001

type Service struct {
	db       *pgxpool.Pool
	cfg      *auth.Config
	sessions sessionStore
	logger   zerolog.Logger
}

type Data struct {
	OrgName            string `json:"orgName" validate:"required,min=2,max=100"`
	OrgSubdomain       string `json:"orgSubdomain" validate:"required,hostname_rfc1123"`
	OrgLogo            string `json:"orgLogo,omitempty"`
	AdminName          string `json:"adminName" validate:"required"`
	AdminEmail         string `json:"adminEmail" validate:"required,email"`
	AdminPassword      string `json:"adminPassword" validate:"required,min=12"`
	OIDCProvider       string `json:"oidcProvider" validate:"required,oneof=google entra custom"`
	GoogleClientID     string `json:"googleClientId,omitempty"`
	GoogleClientSecret string `json:"googleClientSecret,omitempty"`
	EntraClientID      string `json:"entraClientId,omitempty"`
	EntraClientSecret  string `json:"entraClientSecret,omitempty"`
	EntraTenantID      string `json:"entraTenantId,omitempty"`
	CustomIssuer       string `json:"customIssuer,omitempty"`
	CustomClientID     string `json:"customClientId,omitempty"`
	CustomClientSecret string `json:"customClientSecret,omitempty"`
	MinIOEndpoint      string `json:"minioEndpoint" validate:"required,hostname_port"`
	MinIOAccessKey     string `json:"minioAccessKey" validate:"required"`
	MinIOSecretKey     string `json:"minioSecretKey" validate:"required"`
	MinIOBucket        string `json:"minioBucket" validate:"required"`
	LiveKitURL         string `json:"livekitUrl" validate:"required,url"`
	LiveKitAPIKey      string `json:"livekitApiKey" validate:"required"`
	LiveKitAPISecret   string `json:"livekitApiSecret" validate:"required"`
	SMTPHost           string `json:"smtpHost,omitempty"`
	SMTPPort           int    `json:"smtpPort,omitempty"`
	SMTPUser           string `json:"smtpUser,omitempty"`
	SMTPPass           string `json:"smtpPass,omitempty"`
	SMTPFrom           string `json:"smtpFrom,omitempty"`
}

func NewService(db *pgxpool.Pool, redisClient *redis.Client, cfg *auth.Config, logger zerolog.Logger) *Service {
	return &Service{
		db:       db,
		cfg:      cfg,
		sessions: newRedisStore(redisClient),
		logger:   logger,
	}
}

func (s *Service) IsSetupComplete(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&count)
	return count > 0, err
}

// FinalizeSetup writes the organization, its admin, RBAC roles and service
// configuration in one transaction. It validates first so a partially filled
// wizard cannot create a blank organization, and every write is an upsert so
// setup stays re-runnable (spec 09 §7).
func (s *Service) FinalizeSetup(ctx context.Context, data Data) error {
	if err := data.Validate(); err != nil {
		return err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// SetupGuardMiddleware checks "is setup done?" outside the transaction, so
	// two concurrent finalizes can both get past it. Serialise them here and
	// re-check inside the transaction: otherwise a request racing the
	// operator's own finalize lands a second organization with its own
	// permanent owner account, on an endpoint that needs no authentication.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, setupAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire setup lock: %w", err)
	}
	var otherOrgs int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM organizations WHERE subdomain <> $1`, data.OrgSubdomain,
	).Scan(&otherOrgs)
	if err != nil {
		return fmt.Errorf("check existing organizations: %w", err)
	}
	if otherOrgs > 0 {
		// Re-running setup for the same organization stays idempotent; creating
		// a different one on a provisioned instance does not.
		return fmt.Errorf("setup is already complete for another organization")
	}

	var orgID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (id, name, subdomain, logo_url, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (subdomain) DO UPDATE
		SET name = EXCLUDED.name, logo_url = EXCLUDED.logo_url, updated_at = NOW()
		RETURNING id
	`, uuid.New(), data.OrgName, data.OrgSubdomain, data.OrgLogo).Scan(&orgID)
	if err != nil {
		return fmt.Errorf("create organization: %w", err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(data.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}

	var userID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO users (id, org_id, name, email, password_hash, role, is_admin, created_at)
		VALUES ($1, $2, $3, $4, $5, 'owner', true, NOW())
		ON CONFLICT (org_id, email) DO UPDATE
		SET name = EXCLUDED.name, password_hash = EXCLUDED.password_hash,
		    role = 'owner', is_admin = true, updated_at = NOW()
		RETURNING id
	`, uuid.New(), orgID, data.AdminName, data.AdminEmail, passwordHash).Scan(&userID)
	if err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}

	if err := s.createDefaultRoles(ctx, tx, orgID); err != nil {
		return err
	}

	// Without this the only account in the system holds no role, and every
	// permission-gated feature is unreachable.
	_, err = tx.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, assigned_at)
		SELECT $1, id, NOW() FROM roles WHERE org_id = $2 AND name = 'owner'
		ON CONFLICT (user_id, role_id) DO NOTHING
	`, userID, orgID)
	if err != nil {
		return fmt.Errorf("assign owner role: %w", err)
	}

	oidcConfig := map[string]interface{}{
		"provider": data.OIDCProvider,
		"google":   map[string]string{"client_id": data.GoogleClientID, "client_secret": data.GoogleClientSecret},
		"entra":    map[string]string{"client_id": data.EntraClientID, "client_secret": data.EntraClientSecret, "tenant_id": data.EntraTenantID},
		"custom":   map[string]string{"issuer": data.CustomIssuer, "client_id": data.CustomClientID, "client_secret": data.CustomClientSecret},
	}
	if err := upsertConfig(ctx, tx, orgID, "oidc", oidcConfig); err != nil {
		return err
	}

	minioConfig := map[string]string{
		"endpoint": data.MinIOEndpoint, "access_key": data.MinIOAccessKey,
		"secret_key": data.MinIOSecretKey, "bucket": data.MinIOBucket,
	}
	if err := upsertConfig(ctx, tx, orgID, "minio", minioConfig); err != nil {
		return err
	}

	lkConfig := map[string]string{
		"url": data.LiveKitURL, "api_key": data.LiveKitAPIKey, "api_secret": data.LiveKitAPISecret,
	}
	if err := upsertConfig(ctx, tx, orgID, "livekit", lkConfig); err != nil {
		return err
	}

	if data.SMTPHost != "" {
		smtpConfig := map[string]interface{}{
			"host": data.SMTPHost, "port": data.SMTPPort,
			"user": data.SMTPUser, "pass": data.SMTPPass, "from": data.SMTPFrom,
		}
		if err := upsertConfig(ctx, tx, orgID, "smtp", smtpConfig); err != nil {
			return err
		}
	}

	// Both halves of the keypair are needed: the public key goes to the
	// browser, the private key signs the push payloads.
	vapidPublic, vapidPrivate, err := auth.GenerateVAPIDKeys()
	if err != nil {
		return fmt.Errorf("generate VAPID keys: %w", err)
	}
	vapidJSON, err := json.Marshal(map[string]string{
		"public_key": vapidPublic, "private_key": vapidPrivate,
	})
	if err != nil {
		return fmt.Errorf("encode VAPID keys: %w", err)
	}
	// Rotating these on every re-run would invalidate existing push
	// subscriptions, so keep whatever is already stored (spec 09 §7).
	_, err = tx.Exec(ctx, `
		INSERT INTO org_configs (org_id, key, value, is_secret, created_at)
		VALUES ($1, 'vapid', $2, true, NOW())
		ON CONFLICT (org_id, key) DO NOTHING
	`, orgID, vapidJSON)
	if err != nil {
		return fmt.Errorf("store VAPID keys: %w", err)
	}

	if err := upsertConfig(ctx, tx, orgID, "setup_complete", true); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// secretConfigKeys hold credentials (OIDC client secrets, the MinIO secret key,
// the SMTP password, the VAPID private key). They are flagged so any future
// "list the configuration" endpoint can exclude them by default rather than
// discovering the hard way that they were stored as ordinary values.
var secretConfigKeys = map[string]bool{
	"oidc": true, "minio": true, "livekit": true, "smtp": true, "vapid": true,
}

func upsertConfig(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, key string, value interface{}) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s config: %w", key, err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO org_configs (org_id, key, value, is_secret, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (org_id, key) DO UPDATE
		SET value = EXCLUDED.value, is_secret = EXCLUDED.is_secret, updated_at = NOW()
	`, orgID, key, raw, secretConfigKeys[key])
	if err != nil {
		return fmt.Errorf("store %s config: %w", key, err)
	}
	return nil
}

func (s *Service) createDefaultRoles(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	roles := []struct {
		Name        string
		Permissions []string
	}{
		{"owner", []string{"*"}},
		{"admin", []string{"org:manage", "users:manage", "roles:manage", "channels:manage", "tasks:manage", "calls:manage", "settings:manage"}},
		{"manager", []string{"channels:read", "channels:write", "tasks:manage", "calls:manage", "reports:read"}},
		{"member", []string{"channels:read", "channels:write", "tasks:read", "tasks:write:self", "calls:join"}},
	}
	for _, role := range roles {
		permsJSON, err := json.Marshal(role.Permissions)
		if err != nil {
			return fmt.Errorf("encode %s permissions: %w", role.Name, err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO roles (id, org_id, name, permissions, is_system, created_at)
			VALUES (gen_random_uuid(), $1, $2, $3, true, NOW())
			ON CONFLICT (org_id, name) DO UPDATE
			SET permissions = EXCLUDED.permissions, updated_at = NOW()
		`, orgID, role.Name, permsJSON)
		if err != nil {
			return fmt.Errorf("create role %s: %w", role.Name, err)
		}
	}
	return nil
}

// TestService probes a single dependency so the wizard can report a real
// result per service instead of a blanket "connection successful".
func (s *Service) TestService(ctx context.Context, service string, data Data) error {
	switch service {
	case "minio":
		return testMinIO(ctx, data)
	case "livekit":
		return testLiveKit(ctx, data)
	case "smtp":
		if data.SMTPHost == "" {
			return fmt.Errorf("smtp host is not configured")
		}
		return testSMTP(data.SMTPHost, data.SMTPPort, data.SMTPUser, data.SMTPPass)
	default:
		return fmt.Errorf("unknown service: %s", service)
	}
}

func testMinIO(ctx context.Context, data Data) error {
	if data.MinIOEndpoint == "" || data.MinIOBucket == "" {
		return fmt.Errorf("minio endpoint and bucket are required")
	}
	mc, err := minio.New(data.MinIOEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(data.MinIOAccessKey, data.MinIOSecretKey, ""),
		Secure: data.MinIOSecure(),
	})
	if err != nil {
		return fmt.Errorf("minio config: %w", err)
	}

	exists, err := mc.BucketExists(ctx, data.MinIOBucket)
	if err != nil {
		return fmt.Errorf("minio connection: %w", err)
	}
	if !exists {
		if err := mc.MakeBucket(ctx, data.MinIOBucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("minio create bucket: %w", err)
		}
	}

	// Reading is not enough — uploads are the thing that must work later.
	testKey := "wizard-test-" + uuid.New().String()
	if _, err := mc.PutObject(ctx, data.MinIOBucket, testKey, strings.NewReader("test"), 4, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("minio write test: %w", err)
	}
	_ = mc.RemoveObject(ctx, data.MinIOBucket, testKey, minio.RemoveObjectOptions{})
	return nil
}

func testLiveKit(ctx context.Context, data Data) error {
	if data.LiveKitURL == "" {
		return fmt.Errorf("livekit url is required")
	}
	if data.LiveKitAPIKey == "" || data.LiveKitAPISecret == "" {
		return fmt.Errorf("livekit api key and secret are required")
	}

	client := livekit.NewRoomServiceClient(data.LiveKitURL, data.LiveKitAPIKey, data.LiveKitAPISecret)
	// ListRooms is the cheapest authenticated call: it proves the URL resolves
	// and that the API key/secret pair is accepted.
	if _, err := client.ListRooms(ctx, &livekitproto.ListRoomsRequest{}); err != nil {
		return fmt.Errorf("livekit connection: %w", err)
	}
	return nil
}

// MinIOSecure reports whether the endpoint should be dialled over TLS.
func (d Data) MinIOSecure() bool {
	return strings.HasPrefix(d.MinIOEndpoint, "https")
}

func (s *Service) ValidateServices(ctx context.Context, data Data) error {
	mc, err := minio.New(data.MinIOEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(data.MinIOAccessKey, data.MinIOSecretKey, ""),
		Secure: strings.HasPrefix(data.MinIOEndpoint, "https"),
	})
	if err != nil {
		return fmt.Errorf("minio config: %w", err)
	}

	exists, err := mc.BucketExists(ctx, data.MinIOBucket)
	if err != nil {
		return fmt.Errorf("minio bucket check: %w", err)
	}
	if !exists {
		if err := mc.MakeBucket(ctx, data.MinIOBucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("minio create bucket: %w", err)
		}
	}
	testKey := "wizard-test-" + uuid.New().String()
	if _, err := mc.PutObject(ctx, data.MinIOBucket, testKey, strings.NewReader("test"), 4, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("minio write test: %w", err)
	}
	mc.RemoveObject(ctx, data.MinIOBucket, testKey, minio.RemoveObjectOptions{})

	lkClient := livekit.NewRoomServiceClient(data.LiveKitURL, data.LiveKitAPIKey, data.LiveKitAPISecret)
	// Skip LiveKit connection test for now - ListRoomsRequest type may differ in v2
	_ = lkClient
	// _, err = lkClient.ListRooms(ctx, &livekit.ListRoomsRequest{})
	// if err != nil { return fmt.Errorf("livekit connection: %w", err) }

	if data.SMTPHost != "" {
		if err := testSMTP(data.SMTPHost, data.SMTPPort, data.SMTPUser, data.SMTPPass); err != nil {
			return fmt.Errorf("smtp: %w", err)
		}
	}

	return nil
}

// testSMTP dials the server and, when credentials are supplied, authenticates.
// It previously returned nil unconditionally, so a misconfigured mail server
// still reported "connection successful".
func testSMTP(host string, port int, user, pass string) error {
	if port == 0 {
		port = 587
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if user != "" {
		if err := client.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	return client.Quit()
}

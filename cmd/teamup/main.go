package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/calendar"
	"github.com/teamup/teamup/internal/calls"
	"github.com/teamup/teamup/internal/chat"
	"github.com/teamup/teamup/internal/files"
	"github.com/teamup/teamup/internal/gcal"
	"github.com/teamup/teamup/internal/integrations"
	"github.com/teamup/teamup/internal/mail"
	"github.com/teamup/teamup/internal/notetaker"
	"github.com/teamup/teamup/internal/notifications"
	"github.com/teamup/teamup/internal/observability"
	"github.com/teamup/teamup/internal/org"
	"github.com/teamup/teamup/internal/presence"
	"github.com/teamup/teamup/internal/status"
	"github.com/teamup/teamup/internal/tasks"
	"github.com/teamup/teamup/internal/websocket"
	"github.com/teamup/teamup/internal/wizard"
	"github.com/teamup/teamup/pkg/protocol"
)

// `all:` walks the tree recursively and keeps files the default pattern skips
// (leading `_` or `.`). The previous `web/dist/**/*` pattern only reached one
// directory deep, because Go's embed globs treat `**` as an ordinary path
// element rather than a recursive wildcard.
//
//go:embed all:web/dist
var webFS embed.FS

type combinedService struct {
	chatService interface {
		HandleSendMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message)
		HandleEditMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message)
		HandleDeleteMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message)
		HandleTyping(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message)
		HandleRead(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message)
	}
	callsService interface {
		HandleCallInvite(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message)
		HandleCallAccept(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message)
		HandleCallDecline(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message)
		HandleCallSignaling(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message)
	}
}

func (s *combinedService) HandleSendMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	s.chatService.HandleSendMessage(ctx, conn, msg)
}

func (s *combinedService) HandleEditMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	s.chatService.HandleEditMessage(ctx, conn, msg)
}

func (s *combinedService) HandleDeleteMessage(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	s.chatService.HandleDeleteMessage(ctx, conn, msg)
}

func (s *combinedService) HandleTyping(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	s.chatService.HandleTyping(ctx, conn, msg)
}

func (s *combinedService) HandleRead(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	s.chatService.HandleRead(ctx, conn, msg)
}

func (s *combinedService) HandleCallInvite(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	s.callsService.HandleCallInvite(ctx, conn, msg)
}

func (s *combinedService) HandleCallAccept(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	s.callsService.HandleCallAccept(ctx, conn, msg)
}

func (s *combinedService) HandleCallDecline(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	s.callsService.HandleCallDecline(ctx, conn, msg)
}

func (s *combinedService) HandleCallSignaling(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	s.callsService.HandleCallSignaling(ctx, conn, msg)
}

// Version info (set via ldflags)
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// DatabaseConfig is everything a database migration needs, and nothing else.
//
// Migrations touch Postgres only, so `teamup migrate` parses this rather than
// the whole of Config. Asking for the full service configuration here refused
// to migrate from anywhere that legitimately has the database and none of the
// credentials: a pre-deploy job, an init container, and `make test-db`, which
// sets TEAMUP_DATABASE_URL and nothing more.
type DatabaseConfig struct {
	DatabaseURL string `env:"TEAMUP_DATABASE_URL,required"`
}

// Config is what the running server needs. Everything required here is
// required at boot deliberately: an instance missing a service credential
// should fail immediately and visibly, not at the first request that reaches
// for the missing piece.
type Config struct {
	// Database
	DatabaseConfig

	// Server
	Port        string `env:"TEAMUP_PORT" envDefault:"8080"`
	Environment string `env:"TEAMUP_ENV" envDefault:"development"`
	LogLevel    string `env:"TEAMUP_LOG_LEVEL" envDefault:"info"`

	// How long SIGTERM is given to finish in-flight requests and close
	// WebSocket sessions before the process leaves regardless. Keep it below
	// the orchestrator's own kill deadline — Kubernetes'
	// terminationGracePeriodSeconds, Docker's `stop_grace_period` — or the
	// drain is cut short by a SIGKILL instead of finishing.
	ShutdownGracePeriod time.Duration `env:"TEAMUP_SHUTDOWN_GRACE" envDefault:"30s"`

	// How many PostgreSQL connections this server holds. Zero uses
	// DefaultDBMaxConns; a pool_max_conns in the DSN wins over both.
	DBMaxConns int `env:"TEAMUP_DB_MAX_CONNS" envDefault:"25"`

	// Redis
	RedisURL string `env:"TEAMUP_REDIS_URL,required"`

	// MinIO
	MinIOEndpoint  string `env:"TEAMUP_MINIO_ENDPOINT,required"`
	MinIOAccessKey string `env:"TEAMUP_MINIO_ACCESS_KEY,required"`
	MinIOSecretKey string `env:"TEAMUP_MINIO_SECRET_KEY,required"`
	MinIOBucket    string `env:"TEAMUP_MINIO_BUCKET,required"`
	MinIOSecure    bool   `env:"TEAMUP_MINIO_SECURE" envDefault:"false"`

	// MinIOEndpoint is the address the BROWSER uses, because presigned URLs are
	// signed over the host and handed to the browser to call directly. When
	// object storage also sits on the internal network, this is the address the
	// server itself uses — a URL signed for one host cannot be replayed against
	// the other, so the two clients are kept separate.
	MinIOInternalEndpoint string `env:"TEAMUP_MINIO_INTERNAL_ENDPOINT"`
	MinIOInternalSecure   bool   `env:"TEAMUP_MINIO_INTERNAL_SECURE" envDefault:"false"`
	// Presigning is pure local computation only when the region is known;
	// otherwise the client makes a live call to discover it on every start.
	MinIORegion string `env:"TEAMUP_MINIO_REGION" envDefault:"us-east-1"`
	// The ceiling, in bytes. 5 GiB by default: with real multipart the old
	// 100 MiB cap existed only because a single PUT had to carry the whole
	// file, and a startup sharing screen recordings needs the room.
	MaxUploadBytes int64 `env:"TEAMUP_MAX_UPLOAD_BYTES" envDefault:"5368709120"`
	// Where a file stops going up in one PUT and starts going up in resumable
	// parts. Also what the reverse proxy's client_max_body_size has to cover,
	// since above it no single request is ever larger than one part.
	MultipartThresholdBytes int64 `env:"TEAMUP_MULTIPART_THRESHOLD_BYTES" envDefault:"16777216"`

	// LiveKit
	LiveKitURL       string `env:"TEAMUP_LIVEKIT_URL,required"`
	LiveKitAPIKey    string `env:"TEAMUP_LIVEKIT_API_KEY,required"`
	LiveKitAPISecret string `env:"TEAMUP_LIVEKIT_API_SECRET,required"`

	// How many people one call may hold: the size of the LiveKit room, and one
	// more than the number of channel members a channel call rings. See
	// calls.DefaultMaxCallParticipants for why this is not a media limit.
	MaxCallParticipants int `env:"TEAMUP_MAX_CALL_PARTICIPANTS" envDefault:"200"`

	// Auth
	AuthIssuer   string `env:"TEAMUP_AUTH_ISSUER" envDefault:"teamup"`
	AuthAudience string `env:"TEAMUP_AUTH_AUDIENCE" envDefault:"teamup-api"`
	// Encrypts the signing key where it is stored, so a database backup does
	// not carry the ability to mint access tokens. Optional — but once a
	// deployment has started with it, it has to keep being set, because the
	// stored key cannot be read back without it.
	AuthSecret string `env:"TEAMUP_AUTH_SECRET"`

	// Base URL for redirects
	BaseURL string `env:"TEAMUP_BASE_URL" envDefault:"http://localhost:8080"`

	// Hosts other than this server that may open a WebSocket, for setups where
	// the app is served separately (a Vite dev server, say). Same-origin always
	// works and needs no entry here.
	AllowedWSOrigins []string `env:"TEAMUP_ALLOWED_WS_ORIGINS" envSeparator:","`

	// Drops the Secure flag from auth cookies so a browser will keep them over
	// plain HTTP. Needed only to reach TeamUp from another machine on a local
	// network by address; anything on the open internet must leave this alone.
	InsecureCookies bool `env:"TEAMUP_INSECURE_COOKIES" envDefault:"false"`

	// OIDC Providers (configured via wizard)
	GoogleClientID     string `env:"TEAMUP_GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"TEAMUP_GOOGLE_CLIENT_SECRET"`
	EntraClientID      string `env:"TEAMUP_ENTRA_CLIENT_ID"`
	EntraClientSecret  string `env:"TEAMUP_ENTRA_CLIENT_SECRET"`
	EntraTenantID      string `env:"TEAMUP_ENTRA_TENANT_ID"`
	CustomIssuer       string `env:"TEAMUP_CUSTOM_ISSUER"`
	CustomClientID     string `env:"TEAMUP_CUSTOM_CLIENT_ID"`
	CustomClientSecret string `env:"TEAMUP_CUSTOM_CLIENT_SECRET"`

	// Google Calendar sync (optional). Its own OAuth client, apart from Google
	// sign-in: setting it up does not offer "Sign in with Google". The token key
	// seals each person's Google token where it is stored; once people have
	// connected, changing it means they connect again.
	GCalClientID     string `env:"TEAMUP_GCAL_CLIENT_ID"`
	GCalClientSecret string `env:"TEAMUP_GCAL_CLIENT_SECRET"`
	GCalTokenKey     string `env:"TEAMUP_GCAL_TOKEN_KEY"`
	// Replaces Google's addresses. For tests only.
	GCalEndpoint string `env:"TEAMUP_GCAL_ENDPOINT"`

	// Replace GitHub's addresses. For tests only.
	GitHubWebURL string `env:"TEAMUP_GITHUB_WEB_URL"`
	GitHubAPIURL string `env:"TEAMUP_GITHUB_API_URL"`
	// And Bitbucket's.
	BitbucketWebURL string `env:"TEAMUP_BITBUCKET_WEB_URL"`
	BitbucketAPIURL string `env:"TEAMUP_BITBUCKET_API_URL"`

	// SMTP (optional)
	SMTPHost     string `env:"TEAMUP_SMTP_HOST"`
	SMTPPort     int    `env:"TEAMUP_SMTP_PORT" envDefault:"587"`
	SMTPUser     string `env:"TEAMUP_SMTP_USER"`
	SMTPPassword string `env:"TEAMUP_SMTP_PASSWORD"`
	SMTPFrom     string `env:"TEAMUP_SMTP_FROM"`

	// Web Push (VAPID)
	VAPIDPublicKey  string `env:"TEAMUP_VAPID_PUBLIC_KEY"`
	VAPIDPrivateKey string `env:"TEAMUP_VAPID_PRIVATE_KEY"`
	VAPIDSubject    string `env:"TEAMUP_VAPID_SUBJECT" envDefault:"mailto:admin@teamup.local"`
	// PushServiceHosts replaces the browsers' push services as the only places
	// a push may be delivered to; tests point it at their fake.
	PushServiceHosts []string `env:"TEAMUP_PUSH_SERVICE_HOSTS" envSeparator:","`

	// Auto-setup (for CI/CD). Anything left unset falls back to the ordinary
	// runtime configuration above — see setupDataFromConfig.
	AutoSetup          bool   `env:"TEAMUP_AUTO_SETUP" envDefault:"false"`
	SetupOrgName       string `env:"TEAMUP_SETUP_ORG_NAME"`
	SetupOrgSubdomain  string `env:"TEAMUP_SETUP_ORG_SUBDOMAIN"`
	SetupAdminName     string `env:"TEAMUP_SETUP_ADMIN_NAME"`
	SetupAdminEmail    string `env:"TEAMUP_SETUP_ADMIN_EMAIL"`
	SetupAdminPassword string `env:"TEAMUP_SETUP_ADMIN_PASSWORD"`
	SetupOIDCProvider  string `env:"TEAMUP_SETUP_OIDC_PROVIDER"`

	SetupGoogleClientID     string `env:"TEAMUP_SETUP_GOOGLE_CLIENT_ID"`
	SetupGoogleClientSecret string `env:"TEAMUP_SETUP_GOOGLE_CLIENT_SECRET"`
	SetupEntraClientID      string `env:"TEAMUP_SETUP_ENTRA_CLIENT_ID"`
	SetupEntraClientSecret  string `env:"TEAMUP_SETUP_ENTRA_CLIENT_SECRET"`
	SetupEntraTenantID      string `env:"TEAMUP_SETUP_ENTRA_TENANT_ID"`
	SetupCustomIssuer       string `env:"TEAMUP_SETUP_CUSTOM_ISSUER"`
	SetupCustomClientID     string `env:"TEAMUP_SETUP_CUSTOM_CLIENT_ID"`
	SetupCustomClientSecret string `env:"TEAMUP_SETUP_CUSTOM_CLIENT_SECRET"`

	SetupMinIOEndpoint  string `env:"TEAMUP_SETUP_MINIO_ENDPOINT"`
	SetupMinIOAccessKey string `env:"TEAMUP_SETUP_MINIO_ACCESS_KEY"`
	SetupMinIOSecretKey string `env:"TEAMUP_SETUP_MINIO_SECRET_KEY"`
	SetupMinIOBucket    string `env:"TEAMUP_SETUP_MINIO_BUCKET"`

	SetupLiveKitURL       string `env:"TEAMUP_SETUP_LIVEKIT_URL"`
	SetupLiveKitAPIKey    string `env:"TEAMUP_SETUP_LIVEKIT_API_KEY"`
	SetupLiveKitAPISecret string `env:"TEAMUP_SETUP_LIVEKIT_API_SECRET"`
}

// AuthConfig returns the auth service configuration
func (c *Config) AuthConfig() auth.Config {
	return auth.Config{
		Issuer:         c.AuthIssuer,
		Audience:       c.AuthAudience,
		BaseURL:        c.BaseURL,
		GoogleClientID: c.GoogleClientID,
		GoogleSecret:   c.GoogleClientSecret,
		EntraClientID:  c.EntraClientID,
		EntraSecret:    c.EntraClientSecret,
		EntraTenantID:  c.EntraTenantID,
		CustomIssuer:   c.CustomIssuer,
		CustomClientID: c.CustomClientID,
		CustomSecret:   c.CustomClientSecret,
		AccessTokenTTL: 15 * time.Minute,
		// 30 days, per the OIDC decision: a week meant signing in again every
		// Monday morning.
		RefreshTokenTTL:  30 * 24 * time.Hour,
		SigningKeySecret: c.AuthSecret,
		InsecureCookies:  c.InsecureCookies,
	}
}

// CallsConfig returns the calls service configuration
func (c *Config) CallsConfig() calls.Config {
	return calls.Config{
		LiveKitURL:       c.LiveKitURL,
		LiveKitAPIKey:    c.LiveKitAPIKey,
		LiveKitAPISecret: c.LiveKitAPISecret,

		// LiveKit Egress writes recordings straight into the same object
		// storage the rest of the attachments live in.
		StorageEndpoint:  c.MinIOEndpoint,
		StorageAccessKey: c.MinIOAccessKey,
		StorageSecretKey: c.MinIOSecretKey,
		StorageBucket:    c.MinIOBucket,
		StorageSecure:    c.MinIOSecure,

		MaxCallParticipants: c.MaxCallParticipants,
	}
}

// FilesConfig returns the files service configuration
func (c *Config) FilesConfig() files.Config {
	return files.Config{
		Endpoint:         c.MinIOEndpoint,
		AccessKey:        c.MinIOAccessKey,
		SecretKey:        c.MinIOSecretKey,
		Bucket:           c.MinIOBucket,
		Secure:           c.MinIOSecure,
		InternalEndpoint: c.MinIOInternalEndpoint,
		InternalSecure:   c.MinIOInternalSecure,
		Region:           c.MinIORegion,
		MaxUploadBytes:   c.MaxUploadBytes,
		// Clamped inside the service as well; validated at boot so a bad value
		// is a start-up failure rather than a surprise on the first big file.
		MultipartThresholdBytes: c.MultipartThresholdBytes,
	}
}

// NotificationsConfig returns the notifications service configuration
func (c *Config) NotificationsConfig() notifications.Config {
	return notifications.Config{
		VAPIDPublicKey:   c.VAPIDPublicKey,
		VAPIDPrivateKey:  c.VAPIDPrivateKey,
		VAPIDSubject:     c.VAPIDSubject,
		PushServiceHosts: c.PushServiceHosts,
	}
}

func main() {
	// Root command
	rootCmd := &cobra.Command{
		Use:   "teamup",
		Short: "TeamUp - Self-hosted Collaboration Platform",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
	}

	// Server command
	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Start the HTTP server",
		RunE:  runServer,
	}

	// Migrate command
	migrateCmd := &cobra.Command{
		Use:   "migrate [up|down|down-all|version|force|create]",
		Short: "Database migrations",
		Long: `Database migrations.

  up                 Apply every pending migration.
  down               Roll back exactly one migration, the most recent.
                     Run it again for another step.
  down-all           Roll back every migration, dropping every table. Asks
                     for the database name first, and refuses outright when
                     TEAMUP_ENV=production.
  version            Print the recorded version and whether it is dirty.
  force <version>    Record which version is true, clearing a dirty mark left
                     by a migration that failed partway. Use "nil" for "no
                     migrations applied". This changes the bookkeeping only,
                     never the schema.
  create <name>      Scaffold the next numbered up/down migration pair.`,
		Args:          cobra.MinimumNArgs(1),
		RunE:          runMigrate,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Version command
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("TeamUp %s\n", version)
			fmt.Printf("Commit: %s\n", commit)
			fmt.Printf("Built:  %s\n", date)
		},
	}

	// Wizard command (for testing)
	wizardCmd := &cobra.Command{
		Use:   "wizard",
		Short: "Setup wizard utilities",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
	}

	healthcheckCmd := &cobra.Command{
		Use:           "healthcheck",
		Short:         "Exit non-zero unless the server on this machine is ready",
		RunE:          runHealthcheck,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.AddCommand(serverCmd, migrateCmd, versionCmd, wizardCmd, healthcheckCmd)

	if err := rootCmd.Execute(); err != nil {
		// Plain, on stderr. log.Fatal stamps a timestamp on the first line
		// only, which turns the multi-line recovery instructions a failed
		// migration prints into something that reads like a stack trace.
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func runServer(cmd *cobra.Command, args []string) error {
	// Load config
	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("config parse: %w", err)
	}
	// env.Parse only asserts that the required variables exist. Check the
	// values before anything is dialled, so an unusable setting is named here
	// rather than turning up as a driver error, a rejected OIDC redirect or a
	// session cookie sent in the clear.
	if err := cfg.Validate(); err != nil {
		return err
	}

	// Setup logger
	observability.Init("teamup", version, cfg.Environment)
	logger := observability.Logger(context.Background())

	logger.Info().
		Str("version", version).
		Str("commit", commit).
		Str("environment", cfg.Environment).
		Msg("Starting TeamUp")

	// Schedule for the CPU this process is allowed rather than the CPU the
	// node has, before anything starts a goroutine worth scheduling.
	limitGOMAXPROCSToCPUQuota(logger)

	// Connect to database. Sized explicitly: pgx's own default is
	// max(4, NumCPU), which on a CPU-limited container is four connections for
	// the whole application. See DefaultDBMaxConns.
	poolCfg, err := databasePoolConfig(cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	dbPool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return fmt.Errorf("database connect: %w", err)
	}
	defer dbPool.Close()
	logger.Info().
		Int32("db_max_conns", poolCfg.MaxConns).
		Int32("db_min_conns", poolCfg.MinConns).
		Msg("Database pool sized")

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dbPool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping: %w", err)
	}
	logger.Info().Msg("Database connected")

	// Connect to Redis
	redisClient, err := connectRedis(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis connect: %w", err)
	}
	defer redisClient.Close()
	logger.Info().Msg("Redis connected")

	// Initialize services
	authCfg := cfg.AuthConfig()
	// The signing key is loaded from (or written to) the database here, so
	// every replica signs with the same one and a restart does not sign
	// everybody out. Failing to get it is fatal on purpose.
	authService, err := auth.NewService(dbPool, redisClient, authCfg)
	if err != nil {
		return err
	}
	orgService := org.NewService(dbPool)
	chatService := chat.NewService(dbPool, redisClient)
	callsService := calls.NewService(dbPool, cfg.CallsConfig())
	filesService := files.NewService(cfg.FilesConfig())
	// The presigned-URL access checks are database-backed, so without this the
	// file endpoints fail closed.
	filesService.SetDB(dbPool)
	notificationsService := notifications.NewService(dbPool, redisClient, cfg.NotificationsConfig())
	presenceService := presence.NewService(redisClient)
	presenceService.SetDB(dbPool)
	tasksService := tasks.NewService(dbPool)
	calendarService := calendar.NewService(dbPool)
	wizardService := wizard.NewService(dbPool, redisClient, &authCfg, logger)

	// Without this every service logs to a nil writer, so the fail-closed
	// paths that deny access on error do so silently.
	orgService.SetLogger(logger)
	chatService.SetLogger(logger)
	callsService.SetLogger(logger)
	filesService.SetLogger(logger)
	notificationsService.SetLogger(logger)
	tasksService.SetLogger(logger)
	calendarService.SetLogger(logger)
	gcalService, err := gcal.NewService(dbPool, gcal.Config{
		ClientID:        cfg.GCalClientID,
		ClientSecret:    cfg.GCalClientSecret,
		TokenKey:        cfg.GCalTokenKey,
		BaseURL:         cfg.BaseURL,
		Endpoint:        cfg.GCalEndpoint,
		InsecureCookies: cfg.InsecureCookies,
	})
	if err != nil {
		return fmt.Errorf("google calendar: %w", err)
	}
	gcalService.SetLogger(logger)
	integrationsService := integrations.NewService(dbPool, integrations.Config{
		BaseURL:         cfg.BaseURL,
		Secret:          cfg.AuthSecret,
		InsecureCookies: cfg.InsecureCookies,
		GitHubWeb:       cfg.GitHubWebURL,
		GitHubAPI:       cfg.GitHubAPIURL,
		BitbucketWeb:    cfg.BitbucketWebURL,
		BitbucketAPI:    cfg.BitbucketAPIURL,
	}, tasksService)
	integrationsService.SetLogger(logger)
	// A meeting scheduled, moved or cancelled in TeamUp follows into Google.
	calendarService.OnMeetingsChanged(func(people []uuid.UUID) { gcalService.Nudge(people...) })

	// Create combined service for WebSocket hub
	combinedService := &combinedService{
		chatService:  chatService,
		callsService: callsService,
	}

	// Initialize WebSocket hub
	wsHub := websocket.NewHub(
		combinedService,
		presenceService,
		authService,
		logger,
	)
	go wsHub.Run()

	// Cross-process delivery, over the Redis this server already requires.
	//
	// Without it the hub's three maps are the whole world, and a second replica
	// does not share load, it splits the company in half — silently. With it a
	// broadcast reaches every session in the deployment, which is what makes a
	// rolling deploy possible at all: the old pod can drain while the new one
	// serves, instead of every user being dropped at once.
	//
	// Started before the listener opens, and waited on: a process that is
	// accepting sockets while deaf to the other replicas is exactly the failure
	// this replaces, so it is worth a moment at boot to know which it is. It is
	// not fatal — one replica that can reach Redis for everything else is still
	// a working single-replica deployment, and refusing to start would turn a
	// degraded rollout into an outage.
	fanout := websocket.NewFanout(redisClient, logger)
	wsHub.SetFanout(fanout)
	fanout.Start()
	fanoutCtx, cancelFanoutWait := context.WithTimeout(context.Background(), 10*time.Second)
	if err := fanout.WaitReady(fanoutCtx); err != nil {
		logger.Error().Err(err).
			Msg("WebSocket fan-out has not subscribed yet; until it does this instance cannot see the other replicas")
	} else {
		logger.Info().Str("node", wsHub.NodeID()).Msg("WebSocket fan-out subscribed")
	}
	cancelFanoutWait()

	// Set broadcasters for chat and calls services
	chatService.SetBroadcaster(wsHub)
	// Who a channel message reaches is resolved from membership at send time.
	wsHub.SetChannelRoster(chatService)
	// A call that arrived while somebody was away rings when they come back,
	// and the caller is told whether anybody is actually there.
	wsHub.SetCallInviteDeliverer(callsService)
	callsService.SetReachability(wsHub)
	callsService.SetChatRecorder(chatService)
	// A call has to ring the phone, not only a browser tab that happens to be
	// open, or somebody with the app closed is simply unreachable.
	callsService.SetNotifier(notificationsService)
	callsService.SetBroadcaster(wsHub)
	// A password changed or reset ends every session, and the sockets with them.
	authService.OnSessionsEnded(func(userID string) { wsHub.DisconnectUser(userID) })
	orgService.OnSessionsEnded(func(_ context.Context, _, userID uuid.UUID) { wsHub.DisconnectUser(userID.String()) })
	// Somebody deactivated keeps nothing open.
	orgService.OnDeactivated(func(ctx context.Context, orgID, userID uuid.UUID) {
		wsHub.DisconnectUser(userID.String())
		callsService.RemoveFromCalls(ctx, orgID, userID)
	})

	// Notifications reach the browser over the same socket, and the features
	// that raise them talk to the service through a narrow Notifier interface.
	notificationsService.SetBroadcaster(wsHub)
	chatService.SetNotifier(notificationsService)
	// Forwarding a file copies it into the conversation it goes to.
	chatService.SetObjectCopier(filesService)
	orgService.SetChannelEnroller(chatService)
	// Outgoing mail reads its settings from the organization, which is where
	// the setup wizard puts them.
	mailService := mail.NewService(orgService, cfg.BaseURL, logger)

	// The meeting note-taker joins every call while an administrator has it on,
	// and sends the minutes to whoever was there.
	notetakerService := notetaker.NewService(dbPool, orgService, notetaker.LiveKit{
		URL: cfg.LiveKitURL, APIKey: cfg.LiveKitAPIKey, APISecret: cfg.LiveKitAPISecret,
	}, cfg.BaseURL, logger)
	notetakerService.SetNotifier(notificationsService)
	notetakerService.SetMailer(mailService)
	callsService.SetJoinHook(notetakerService.PersonJoined)
	tasksService.SetNotifier(notificationsService)
	integrationsService.SetNotifier(notificationsService)
	calendarService.SetNotifier(notificationsService)
	// People from outside invited by email are emailed, and told when the
	// meeting moves or is cancelled.
	callsService.SetGuestMailer(mailService, cfg.BaseURL)
	calendarService.SetGuestNotifier(callsService)
	// A comment should appear for everybody following the task, not on their
	// next reload.
	tasksService.SetBroadcaster(wsHub)

	// Auto-setup if configured. It gets its own deadline: ctx above carries the
	// short database-ping timeout, which bcrypt hashing plus a multi-statement
	// transaction can outrun, turning a healthy start into a crash loop.
	if cfg.AutoSetup {
		setupCtx, cancelSetup := context.WithTimeout(context.Background(), 2*time.Minute)
		err := runAutoSetup(setupCtx, wizardService, cfg)
		cancelSetup()
		if err != nil {
			logger.Fatal().Err(err).Msg("Auto-setup failed")
		}
	}

	// The setup wizard generates and stores a VAPID keypair with the
	// organization. Prefer it over the environment, otherwise an instance
	// provisioned through the wizard has no working push keys at all.
	vapidCtx, cancelVAPID := context.WithTimeout(context.Background(), 10*time.Second)
	loadStoredVAPIDKeys(vapidCtx, orgService, notificationsService, logger)
	cancelVAPID()

	// Setup Gin router
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	// Only nginx in front, on the private network, says who the client is,
	// and it says so in X-Real-IP. Trusting every proxy let anybody write the
	// address recorded on their session with an X-Forwarded-For of their own.
	if err := router.SetTrustedProxies([]string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7"}); err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}
	router.RemoteIPHeaders = []string{"X-Real-IP"}
	router.Use(gin.Recovery())
	router.Use(limitBodies())
	// The policy is worked out once, from the page it protects; a missing page
	// is reported below, where the frontend is read for serving.
	page, _ := webFS.ReadFile("web/dist/index.html")
	router.Use(securityHeaders(contentSecurityPolicy(page, cfg.MinIOEndpoint, cfg.MinIOSecure, cfg.LiveKitURL, cfg.GitHubWebURL)))
	router.Use(observability.GinLogger(logger))
	router.Use(observability.GinMetrics())

	// Health endpoints (no auth). /healthz and /readyz are the canonical paths;
	// the /healthz/* forms are kept because nginx.conf and the production
	// compose healthcheck already probe them.
	//
	// Readiness covers every dependency a request can need, not only the two
	// the process refuses to boot without. An instance that cannot reach object
	// storage answers 500 to uploads, and one that cannot reach LiveKit answers
	// 500 to call creation; both must stay out of the load balancer until they
	// can. Liveness stays deliberately dependency-free — see LiveHandler.
	readyDeps := []observability.Dependency{
		observability.PingCheck("database", dbPool),
		observability.PingCheck("redis", redisPinger{redisClient}),
		observability.PingCheck("minio", filesService),
	}
	if target := liveKitProxyTarget(cfg.LiveKitURL); target != nil {
		readyDeps = append(readyDeps, observability.HTTPCheck("livekit", target.String()))
	}
	readiness := observability.NewReadiness(readyDeps...)
	readyHandler := readiness.Handler

	// The public status page reads the same checks, and keeps a day of them.
	statusService := status.NewService(func(ctx context.Context) map[string]bool {
		resp, _ := readiness.Evaluate(ctx)
		out := make(map[string]bool, len(resp.Checks))
		for name, check := range resp.Checks {
			out[name] = check.Status == "pass"
		}
		return out
	}, status.NewRedisStore(redisClient), logger)
	router.GET("/healthz", observability.LiveHandler)
	router.GET("/readyz", readyHandler)
	router.GET("/healthz/live", observability.LiveHandler)
	router.GET("/healthz/ready", readyHandler)
	router.GET("/metrics", observability.MetricsHandler)

	// Static files (embedded frontend)
	subFS, _ := fs.Sub(webFS, "web/dist")
	registerStatic(router, subFS)

	// SPA fallback: serve index.html for root and non-API routes. Read once at
	// startup — if the embedded frontend is missing, fail loudly here instead
	// of serving an empty 200 that looks like a broken app.
	indexHTML, err := webFS.ReadFile("web/dist/index.html")
	if err != nil {
		return fmt.Errorf("read embedded frontend (run `make build-frontend`): %w", err)
	}
	router.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	// API routes
	api := router.Group("/api")
	{
		// Auth routes (public)
		authGroup := api.Group("/auth")
		auth.RegisterRoutes(authGroup, authService)

		// Wizard routes. RegisterRoutes guards the mutating steps itself and
		// deliberately leaves /status readable so the frontend can decide
		// whether to show the wizard.
		wizardGroup := api.Group("/wizard")
		wizard.RegisterRoutes(wizardGroup, wizardService)

		// The status page and the organization's look are for anybody,
		// signed in or not.
		status.RegisterRoutes(api, statusService)
		org.RegisterPublicRoutes(api, orgService)
		// A meeting's guests have no account; the guest link is their credential.
		calls.RegisterGuestRoutes(api, callsService)

		// Protected routes
		protected := api.Group("/")
		protected.Use(authService.AuthMiddleware())
		{
			org.RegisterRoutes(protected, orgService, func(h *org.Handler) {
				h.SetMailer(mailService)
				h.SetLogger(logger)
			})
			chat.RegisterRoutes(protected, chatService)
			calls.RegisterRoutes(protected, callsService)
			files.RegisterRoutes(protected, filesService)
			notifications.RegisterRoutes(protected, notificationsService)
			presence.RegisterRoutes(protected, presenceService)
			tasks.RegisterRoutes(protected, tasksService)
			calendar.RegisterRoutes(protected, calendarService)
			gcal.RegisterRoutes(protected, api, gcalService)
			integrations.RegisterRoutes(protected, api, integrationsService)
			notetaker.RegisterRoutes(protected, notetakerService)
		}

		// LiveKit has no session. The signature on the request is its credential.
		calls.RegisterWebhook(api, callsService)
	}

	// WebSocket endpoint. Sessions are tracked so shutdown can close them; see
	// wsSessions for why http.Server cannot do it.
	sessions := newWSSessions()
	router.GET("/ws", sessions.Track(websocket.WSHandler(wsHub, authService, cfg.AllowedWSOrigins)))

	// LiveKit's signalling socket, served from this origin.
	//
	// A page served over HTTPS cannot open a plain ws:// connection to
	// LiveKit's own port, so behind TLS — a tunnel, or a reverse proxy — the
	// browser asks for /livekit here instead. Without this the app would need
	// nginx in front of it before a call could work at all.
	if target := liveKitProxyTarget(cfg.LiveKitURL); target != nil {
		proxy := httputil.NewSingleHostReverseProxy(target)
		director := proxy.Director
		proxy.Director = func(r *http.Request) {
			director(r)
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/livekit")
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			// LiveKit checks the Host it was reached on; passing the tunnel's
			// host through makes it reject the handshake.
			r.Host = target.Host
			// The call server needs its own token, not the person's session.
			r.Header.Del("Cookie")
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
			logger.Warn().Err(err).Msg("could not reach LiveKit")
			http.Error(w, "the call server is unavailable", http.StatusBadGateway)
		}
		router.Any("/livekit", gin.WrapH(proxy))
		router.Any("/livekit/*path", gin.WrapH(proxy))
	}

	// SPA fallback: serve index.html for non-API routes (only for paths without
	// file extensions). /wizard/* is deliberately NOT excluded — it is a
	// client-side route, and 404ing it made the setup wizard unreachable by
	// direct link or page refresh. The wizard's server endpoints all live
	// under /api/wizard/.
	router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") ||
			strings.HasPrefix(path, "/ws") ||
			strings.HasPrefix(path, "/healthz") ||
			strings.HasPrefix(path, "/readyz") ||
			strings.HasPrefix(path, "/metrics") ||
			strings.HasPrefix(path, "/auth/") ||
			strings.Contains(path, ".") { // Skip static files (have extensions)
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	// Start server
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Access people already hold on GitHub and Bitbucket is looked at again
	// now and then, so what TeamUp shows does not drift from what is there.
	findCtx, stopFinding := context.WithCancel(context.Background())
	defer stopFinding()
	go func() {
		first := time.NewTimer(time.Minute)
		defer first.Stop()
		select {
		case <-findCtx.Done():
			return
		case <-first.C:
			integrationsService.FindAllExistingAccess(findCtx)
		}
		ticker := time.NewTicker(integrations.FindEvery)
		defer ticker.Stop()
		for {
			select {
			case <-findCtx.Done():
				return
			case <-ticker.C:
				integrationsService.FindAllExistingAccess(findCtx)
			}
		}
	}()

	// Uploads that were staged and never sent would otherwise sit in object
	// storage for ever. Nothing else reclaims them.
	sweepCtx, stopSweep := context.WithCancel(context.Background())
	defer stopSweep()
	go func() {
		const (
			every   = time.Hour
			keepFor = 24 * time.Hour
		)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-sweepCtx.Done():
				return
			case <-ticker.C:
				removed, err := filesService.SweepAbandonedUploads(sweepCtx, keepFor)
				if err != nil {
					logger.Warn().Err(err).Msg("could not sweep abandoned uploads")
					continue
				}
				if removed > 0 {
					logger.Info().Int("count", removed).Msg("removed uploads that were never sent")
				}
			}
		}
	}()

	// Meeting reminders. Every replica sweeps, and claiming one is atomic.
	go calendarService.RunReminders(sweepCtx, 30*time.Second)
	// Everybody's Google Calendar, both ways, every few minutes and whenever a
	// meeting of theirs changes.
	go gcalService.Run(sweepCtx, 5*time.Minute)
	// A call whose room finished while nothing was listening. Every replica sweeps,
	// and ending a call is atomic.
	go callsService.RunVanishedRoomSweep(sweepCtx, 5*time.Minute)
	// What the status page shows for the last day. Every replica records four
	// times a minute, and a failure any of them saw wins.
	go statusService.Run(sweepCtx, 15*time.Second)
	// Meeting notes another server left off, on its way out or because it died:
	// carried on in the call, or written up if the call has ended.
	go notetakerService.RunResume(sweepCtx, 20*time.Second)

	// Graceful shutdown.
	//
	// The signal is armed before the listener opens, so a SIGTERM that arrives
	// during a slow start is not missed.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	serving := make(chan error, 1)
	go func() {
		logger.Info().Str("port", cfg.Port).Msg("Server starting")
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serving <- err
	}()

	select {
	case err := <-serving:
		// The listener died on its own — a bound port, say. Nothing to drain.
		if err != nil {
			return fmt.Errorf("server: %w", err)
		}
		logger.Info().Msg("Server stopped")
		return nil
	case sig := <-quit:
		logger.Info().
			Str("signal", sig.String()).
			Dur("grace", cfg.ShutdownGracePeriod).
			Msg("Shutting down server...")
	}

	// One deadline for the whole drain. Everything below runs inside it, and
	// runServer does not return — so the deferred dbPool.Close, redisClient.Close
	// and stopSweep do not run — until the drain is finished or the deadline has
	// passed. Handing srv.Shutdown to a goroutine nobody joined is what used to
	// tear the pool out from under in-flight requests a few milliseconds after
	// SIGTERM.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), cfg.ShutdownGracePeriod)
	defer cancelDrain()

	// WebSocket sessions first, and while the database and Redis are still
	// open: ending a session unregisters it from the hub, which writes the
	// user's presence and tells their colleagues they have gone.
	if closed := sessions.CloseAll(); closed > 0 {
		logger.Info().Int("sessions", closed).Msg("Closing WebSocket sessions")
	}

	shutdownErr := srv.Shutdown(drainCtx)

	// The note-taker saves what it has heard in each call and hands the call
	// to whichever server comes next, while the database is still open.
	notetakerService.Stop(drainCtx)
	// Calls still ringing stop being rung again while the database is open.
	callsService.Close()
	if shutdownErr != nil {
		logger.Error().Err(shutdownErr).Msg("Drain did not finish within the grace period")
	}

	// ListenAndServe has returned by now; take its error so nothing is lost.
	if err := <-serving; err != nil {
		logger.Error().Err(err).Msg("Server error during shutdown")
	}

	if err := sessions.Wait(drainCtx); err != nil {
		logger.Warn().Err(err).Msg("WebSocket sessions did not all close within the grace period")
	}

	// After the sessions, not before. Closing each one unregisters it, which
	// gives up its claim on the shared session registry and publishes the user
	// as offline once their last socket anywhere has gone — and that
	// publication has to have somewhere to go, or the colleagues still
	// connected to the other replicas keep seeing a green dot for somebody who
	// left with this pod. Close drains what is queued before it unsubscribes.
	if err := fanout.Close(drainCtx); err != nil {
		logger.Warn().Err(err).Msg("WebSocket fan-out did not shut down cleanly")
	}
	// And the reaper, which has nothing left to reap.
	wsHub.Stop()

	logger.Info().Msg("Server stopped")
	return shutdownErr
}

// firstNonEmpty picks the first value that was actually configured, letting an
// explicit TEAMUP_SETUP_* override win over the runtime default.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// setupDataFromConfig builds the wizard payload for an unattended install.
// Every field the schema requires is mapped here; the previous version filled
// in five of them and left the rest blank, which produced an organization with
// no storage, no video and an admin whose name violated a NOT NULL column.
func setupDataFromConfig(cfg Config) wizard.Data {
	return wizard.Data{
		OrgName:       cfg.SetupOrgName,
		OrgSubdomain:  cfg.SetupOrgSubdomain,
		AdminName:     firstNonEmpty(cfg.SetupAdminName, "Administrator"),
		AdminEmail:    cfg.SetupAdminEmail,
		AdminPassword: cfg.SetupAdminPassword,

		OIDCProvider:       cfg.SetupOIDCProvider,
		GoogleClientID:     firstNonEmpty(cfg.SetupGoogleClientID, cfg.GoogleClientID),
		GoogleClientSecret: firstNonEmpty(cfg.SetupGoogleClientSecret, cfg.GoogleClientSecret),
		EntraClientID:      firstNonEmpty(cfg.SetupEntraClientID, cfg.EntraClientID),
		EntraClientSecret:  firstNonEmpty(cfg.SetupEntraClientSecret, cfg.EntraClientSecret),
		EntraTenantID:      firstNonEmpty(cfg.SetupEntraTenantID, cfg.EntraTenantID),
		CustomIssuer:       firstNonEmpty(cfg.SetupCustomIssuer, cfg.CustomIssuer),
		CustomClientID:     firstNonEmpty(cfg.SetupCustomClientID, cfg.CustomClientID),
		CustomClientSecret: firstNonEmpty(cfg.SetupCustomClientSecret, cfg.CustomClientSecret),

		MinIOEndpoint:  firstNonEmpty(cfg.SetupMinIOEndpoint, cfg.MinIOEndpoint),
		MinIOAccessKey: firstNonEmpty(cfg.SetupMinIOAccessKey, cfg.MinIOAccessKey),
		MinIOSecretKey: firstNonEmpty(cfg.SetupMinIOSecretKey, cfg.MinIOSecretKey),
		MinIOBucket:    firstNonEmpty(cfg.SetupMinIOBucket, cfg.MinIOBucket),

		LiveKitURL:       firstNonEmpty(cfg.SetupLiveKitURL, cfg.LiveKitURL),
		LiveKitAPIKey:    firstNonEmpty(cfg.SetupLiveKitAPIKey, cfg.LiveKitAPIKey),
		LiveKitAPISecret: firstNonEmpty(cfg.SetupLiveKitAPISecret, cfg.LiveKitAPISecret),

		SMTPHost: cfg.SMTPHost,
		SMTPPort: cfg.SMTPPort,
		SMTPUser: cfg.SMTPUser,
		SMTPPass: cfg.SMTPPassword,
		SMTPFrom: cfg.SMTPFrom,
	}
}

// liveKitProxyTarget turns the configured LiveKit address into something to
// proxy to. The setting is written as a websocket or an http address depending
// on who wrote it; both mean the same host.
func liveKitProxyTarget(raw string) *url.URL {
	if raw == "" {
		return nil
	}
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" {
		return nil
	}
	switch target.Scheme {
	case "ws", "http":
		target.Scheme = "http"
	case "wss", "https":
		target.Scheme = "https"
	default:
		return nil
	}
	return target
}

// autoSetupService is the slice of the wizard the unattended path drives. An
// interface rather than *wizard.Service so what this function asks for — and
// in which order — can be tested without a database or an object store.
type autoSetupService interface {
	IsSetupComplete(ctx context.Context) (bool, error)
	FinalizeSetup(ctx context.Context, data wizard.Data) error
	EnsureObjectStorage(ctx context.Context, data wizard.Data) error
}

func runAutoSetup(ctx context.Context, wizardService autoSetupService, cfg Config) error {
	logger := observability.Logger(ctx)
	data := setupDataFromConfig(cfg)

	// Don't re-provision an instance that is already set up: that would reset
	// the admin password to the environment value on every restart.
	complete, err := wizardService.IsSetupComplete(ctx)
	if err != nil {
		return fmt.Errorf("check setup status: %w", err)
	}
	if complete {
		logger.Info().Msg("Auto-setup skipped: setup already complete")
		// The bucket is not part of what "setup complete" records: an instance
		// provisioned before anything created buckets, or one whose bucket was
		// deleted, has no other way back. Repair it on every boot — but do not
		// take an otherwise healthy instance down over it. /readyz reports
		// object storage separately, so it stays out of the load balancer
		// until the bucket is there.
		if err := wizardService.EnsureObjectStorage(ctx, data); err != nil {
			logger.Error().Err(err).Str("bucket", data.MinIOBucket).
				Msg("Could not create the object storage bucket; uploads will fail until it exists")
		}
		return nil
	}

	logger.Info().Msg("Running auto-setup...")

	if err := data.Validate(); err != nil {
		return fmt.Errorf("auto-setup environment is incomplete (%w); set the missing TEAMUP_SETUP_* variables or disable TEAMUP_AUTO_SETUP", err)
	}
	if err := wizardService.FinalizeSetup(ctx, data); err != nil {
		return err
	}

	logger.Info().
		Str("org", data.OrgName).
		Str("admin", data.AdminEmail).
		Msg("Auto-setup completed")
	return nil
}

func getContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".js":
		return "application/javascript"
	case ".css":
		return "text/css"
	case ".json", ".webmanifest":
		return "application/json"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".map":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func connectRedis(url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return client, nil
}

// redisPinger adapts go-redis, whose Ping returns a command rather than an
// error, to the plain Ping the readiness probe consults.
type redisPinger struct{ client *redis.Client }

func (p redisPinger) Ping(ctx context.Context) error { return p.client.Ping(ctx).Err() }

// loadStoredVAPIDKeys reads the keypair the setup wizard persisted with the
// organization and hands it to the notifications service. Single-tenant, so
// the first organization is the instance's organization.
func loadStoredVAPIDKeys(ctx context.Context, orgService *org.Service, notifier *notifications.Service, logger zerolog.Logger) {
	complete, err := orgService.IsSetupComplete(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("Could not check setup status for VAPID keys")
		return
	}
	if !complete {
		return
	}

	orgID, err := orgService.FirstOrganizationID(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("Could not resolve organization for VAPID keys")
		return
	}

	var keys struct {
		PublicKey  string `json:"public_key"`
		PrivateKey string `json:"private_key"`
	}
	if err := orgService.GetConfigInto(ctx, orgID, "vapid", &keys); err != nil {
		logger.Info().Msg("No stored VAPID keys; web push uses environment configuration")
		return
	}
	notifier.SetVAPIDKeys(keys.PublicKey, keys.PrivateKey)
	if notifier.HasVAPIDKeys() {
		logger.Info().Msg("Web push enabled using the organization's stored VAPID keys")
	}
}

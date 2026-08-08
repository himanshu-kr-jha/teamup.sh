package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/calls"
	"github.com/teamup/teamup/internal/chat"
	"github.com/teamup/teamup/internal/files"
	"github.com/teamup/teamup/internal/notifications"
	"github.com/teamup/teamup/internal/observability"
	"github.com/teamup/teamup/internal/org"
	"github.com/teamup/teamup/internal/presence"
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

type Config struct {
	// Server
	Port        string `env:"TEAMUP_PORT" envDefault:"8080"`
	Environment string `env:"TEAMUP_ENV" envDefault:"development"`
	LogLevel    string `env:"TEAMUP_LOG_LEVEL" envDefault:"info"`

	// Database
	DatabaseURL string `env:"TEAMUP_DATABASE_URL,required"`

	// Redis
	RedisURL string `env:"TEAMUP_REDIS_URL,required"`

	// MinIO
	MinIOEndpoint  string `env:"TEAMUP_MINIO_ENDPOINT,required"`
	MinIOAccessKey string `env:"TEAMUP_MINIO_ACCESS_KEY,required"`
	MinIOSecretKey string `env:"TEAMUP_MINIO_SECRET_KEY,required"`
	MinIOBucket    string `env:"TEAMUP_MINIO_BUCKET,required"`
	MinIOSecure    bool   `env:"TEAMUP_MINIO_SECURE" envDefault:"false"`

	// LiveKit
	LiveKitURL       string `env:"TEAMUP_LIVEKIT_URL,required"`
	LiveKitAPIKey    string `env:"TEAMUP_LIVEKIT_API_KEY,required"`
	LiveKitAPISecret string `env:"TEAMUP_LIVEKIT_API_SECRET,required"`

	// Auth
	AuthIssuer   string `env:"TEAMUP_AUTH_ISSUER" envDefault:"teamup"`
	AuthAudience string `env:"TEAMUP_AUTH_AUDIENCE" envDefault:"teamup-api"`
	AuthSecret   string `env:"TEAMUP_AUTH_SECRET"` // For HS256 fallback

	// Base URL for redirects
	BaseURL string `env:"TEAMUP_BASE_URL" envDefault:"http://localhost:8080"`

	// OIDC Providers (configured via wizard)
	GoogleClientID     string `env:"TEAMUP_GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"TEAMUP_GOOGLE_CLIENT_SECRET"`
	EntraClientID      string `env:"TEAMUP_ENTRA_CLIENT_ID"`
	EntraClientSecret  string `env:"TEAMUP_ENTRA_CLIENT_SECRET"`
	EntraTenantID      string `env:"TEAMUP_ENTRA_TENANT_ID"`
	CustomIssuer       string `env:"TEAMUP_CUSTOM_ISSUER"`
	CustomClientID     string `env:"TEAMUP_CUSTOM_CLIENT_ID"`
	CustomClientSecret string `env:"TEAMUP_CUSTOM_CLIENT_SECRET"`

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
		Issuer:          c.AuthIssuer,
		Audience:        c.AuthAudience,
		BaseURL:         c.BaseURL,
		GoogleClientID:  c.GoogleClientID,
		GoogleSecret:    c.GoogleClientSecret,
		EntraClientID:   c.EntraClientID,
		EntraSecret:     c.EntraClientSecret,
		EntraTenantID:   c.EntraTenantID,
		CustomIssuer:    c.CustomIssuer,
		CustomClientID:  c.CustomClientID,
		CustomSecret:    c.CustomClientSecret,
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	}
}

// CallsConfig returns the calls service configuration
func (c *Config) CallsConfig() calls.Config {
	return calls.Config{
		LiveKitURL:       c.LiveKitURL,
		LiveKitAPIKey:    c.LiveKitAPIKey,
		LiveKitAPISecret: c.LiveKitAPISecret,
	}
}

// FilesConfig returns the files service configuration
func (c *Config) FilesConfig() files.Config {
	return files.Config{
		Endpoint:  c.MinIOEndpoint,
		AccessKey: c.MinIOAccessKey,
		SecretKey: c.MinIOSecretKey,
		Bucket:    c.MinIOBucket,
		Secure:    c.MinIOSecure,
	}
}

// NotificationsConfig returns the notifications service configuration
func (c *Config) NotificationsConfig() notifications.Config {
	return notifications.Config{
		VAPIDPublicKey:  c.VAPIDPublicKey,
		VAPIDPrivateKey: c.VAPIDPrivateKey,
		VAPIDSubject:    c.VAPIDSubject,
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
		Use:   "migrate [up|down|version|force|create]",
		Short: "Database migrations",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runMigrate,
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

	rootCmd.AddCommand(serverCmd, migrateCmd, versionCmd, wizardCmd)

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func runServer(cmd *cobra.Command, args []string) error {
	// Load config
	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("config parse: %w", err)
	}

	// Setup logger
	observability.Init("teamup", version, cfg.Environment)
	logger := observability.Logger(context.Background())

	logger.Info().
		Str("version", version).
		Str("commit", commit).
		Str("environment", cfg.Environment).
		Msg("Starting TeamUp")

	// Connect to database
	dbPool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database connect: %w", err)
	}
	defer dbPool.Close()

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
	authService := auth.NewService(dbPool, redisClient, authCfg)
	orgService := org.NewService(dbPool)
	chatService := chat.NewService(dbPool, redisClient)
	callsService := calls.NewService(dbPool, cfg.CallsConfig())
	filesService := files.NewService(cfg.FilesConfig())
	notificationsService := notifications.NewService(dbPool, redisClient, cfg.NotificationsConfig())
	presenceService := presence.NewService(redisClient)
	tasksService := tasks.NewService(dbPool)
	wizardService := wizard.NewService(dbPool, redisClient, &authCfg, logger)

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

	// Set broadcasters for chat and calls services
	chatService.SetBroadcaster(wsHub)
	callsService.SetBroadcaster(wsHub)

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

	// Setup Gin router
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(observability.GinLogger(logger))
	router.Use(observability.GinMetrics())

	// Health endpoints (no auth). /healthz and /readyz are the canonical paths;
	// the /healthz/* forms are kept because nginx.conf and the production
	// compose healthcheck already probe them.
	readyHandler := observability.ReadyHandler(dbPool, redisClient)
	router.GET("/healthz", observability.LiveHandler)
	router.GET("/readyz", readyHandler)
	router.GET("/healthz/live", observability.LiveHandler)
	router.GET("/healthz/ready", readyHandler)
	router.GET("/metrics", observability.MetricsHandler)

	// Static files (embedded frontend)
	subFS, _ := fs.Sub(webFS, "web/dist")
	assetsFS, _ := fs.Sub(subFS, "assets")
	iconsFS, _ := fs.Sub(subFS, "icons")
	router.StaticFS("/assets", http.FS(assetsFS))
	router.StaticFS("/icons", http.FS(iconsFS))
	// Serve root-level static files using StaticFileFS
	router.StaticFileFS("/manifest.webmanifest", "manifest.webmanifest", http.FS(subFS))
	router.StaticFileFS("/sw.js", "sw.js", http.FS(subFS))
	router.StaticFileFS("/workbox-2eb6b95d.js", "workbox-2eb6b95d.js", http.FS(subFS))
	router.StaticFileFS("/workbox-2eb6b95d.js.map", "workbox-2eb6b95d.js.map", http.FS(subFS))
	router.StaticFileFS("/index.html", "index.html", http.FS(subFS))

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

		// Protected routes
		protected := api.Group("/")
		protected.Use(authService.AuthMiddleware())
		{
			org.RegisterRoutes(protected, orgService)
			chat.RegisterRoutes(protected, chatService)
			calls.RegisterRoutes(protected, callsService)
			files.RegisterRoutes(protected, filesService)
			notifications.RegisterRoutes(protected, notificationsService)
			presence.RegisterRoutes(protected, presenceService)
			tasks.RegisterRoutes(protected, tasksService)
		}
	}

	// WebSocket endpoint
	router.GET("/ws", websocket.WSHandler(wsHub, authService))

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

	// Graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		logger.Info().Msg("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error().Err(err).Msg("Server forced to shutdown")
		}
	}()

	logger.Info().Str("port", cfg.Port).Msg("Server starting")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}

	logger.Info().Msg("Server stopped")
	return nil
}

func runMigrate(cmd *cobra.Command, args []string) error {
	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("config parse: %w", err)
	}

	// Use absolute path for migrations in container, relative for local development
	migrationsPath := "file://migrations"
	if _, err := os.Stat("/migrations"); err == nil {
		migrationsPath = "file:///migrations"
	}

	m, err := migrate.New(migrationsPath, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}

	action := args[0]
	switch action {
	case "up":
		if err := m.Up(); err != nil && err != migrate.ErrNoChange {
			return fmt.Errorf("migrate up: %w", err)
		}
		fmt.Println("Migrations applied successfully")
	case "down":
		if err := m.Down(); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		fmt.Println("Migration rolled back")
	case "version":
		v, dirty, _ := m.Version()
		fmt.Printf("Version: %d, Dirty: %v\n", v, dirty)
	case "force":
		if len(args) < 2 {
			return fmt.Errorf("force requires version number")
		}
		version := args[1]
		// Parse version
		var v uint
		fmt.Sscanf(version, "%d", &v)
		if err := m.Force(int(v)); err != nil {
			return fmt.Errorf("migrate force: %w", err)
		}
		fmt.Printf("Forced version to %d\n", v)
	case "create":
		if len(args) < 2 {
			return fmt.Errorf("create requires migration name")
		}
		name := strings.Join(args[1:], "_")
		return createMigrationFiles(name)
	default:
		return fmt.Errorf("unknown action: %s", action)
	}
	return nil
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

func runAutoSetup(ctx context.Context, wizardService *wizard.Service, cfg Config) error {
	logger := observability.Logger(ctx)

	// Don't re-provision an instance that is already set up: that would reset
	// the admin password to the environment value on every restart.
	complete, err := wizardService.IsSetupComplete(ctx)
	if err != nil {
		return fmt.Errorf("check setup status: %w", err)
	}
	if complete {
		logger.Info().Msg("Auto-setup skipped: setup already complete")
		return nil
	}

	logger.Info().Msg("Running auto-setup...")

	data := setupDataFromConfig(cfg)
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

func createMigrationFiles(name string) error {
	timestamp := time.Now().Format("20060102150405")
	upFile := fmt.Sprintf("migrations/%s_%s.up.sql", timestamp, name)
	downFile := fmt.Sprintf("migrations/%s_%s.down.sql", timestamp, name)

	upContent := fmt.Sprintf("-- Migration: %s\n-- Created: %s\n\n", name, time.Now().Format(time.RFC3339))
	downContent := fmt.Sprintf("-- Rollback: %s\n-- Created: %s\n\n", name, time.Now().Format(time.RFC3339))

	if err := os.WriteFile(upFile, []byte(upContent), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(downFile, []byte(downContent), 0644); err != nil {
		return err
	}

	fmt.Printf("Created migration files:\n  %s\n  %s\n", upFile, downFile)
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

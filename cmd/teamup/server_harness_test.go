package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// The shutdown and readiness behaviour under test only exists once the process
// is a process: signal handling, the order deferred closes run in, and whether
// /readyz can still answer while a dependency is unreachable. None of that is
// observable from inside the test binary, so these tests build the server and
// drive the real thing over a socket.

const (
	harnessDBEnv = "TEAMUP_TEST_DATABASE_URL"

	// Auto-setup credentials. The password has to clear the wizard's
	// twelve-character minimum.
	harnessAdminEmail    = "shutdown-admin@teamup.test"
	harnessAdminPassword = "harness-password-1"
)

var (
	buildOnce sync.Once
	buildPath string
	buildErr  error
)

// serverBinary builds cmd/teamup once for the package and returns its path.
func serverBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "teamup-harness")
		if err != nil {
			buildErr = err
			return
		}
		buildPath = filepath.Join(dir, "teamup")
		cmd := exec.Command("go", "build", "-o", buildPath, ".")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build ./cmd/teamup: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("could not build the server: %v", buildErr)
	}
	return buildPath
}

var (
	cloneOnce sync.Once
	cloneDSN  string
	cloneErr  error
)

// harnessDatabaseURL returns a database of this package's own, cloned from the
// migrated template the way internal/testsupport does it.
//
// The clone matters: the server subprocess holds a connection pool open for as
// long as a test runs, and Postgres refuses to use a database as a CREATE
// DATABASE template while anything is connected to it. Pointing the server
// straight at the template would make every other package's clone fail
// whenever these tests happened to be running.
func harnessDatabaseURL(t *testing.T) string {
	t.Helper()
	templateDSN := os.Getenv(harnessDBEnv)
	if templateDSN == "" {
		t.Skipf("%s is not set", harnessDBEnv)
	}

	cloneOnce.Do(func() { cloneDSN, cloneErr = cloneTemplateDB(templateDSN) })
	if cloneErr != nil {
		t.Fatalf("prepare the harness database: %v", cloneErr)
	}
	return cloneDSN
}

func cloneTemplateDB(templateDSN string) (string, error) {
	cfg, err := pgx.ParseConfig(templateDSN)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", harnessDBEnv, err)
	}
	template := cfg.Database
	clone := template + "_server"

	// The administrative work happens on the maintenance database, so nothing
	// is connected to the template while it is being copied.
	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		return "", fmt.Errorf("connect maintenance database: %w", err)
	}
	defer admin.Close(context.Background())

	// FORCE drops a connection left behind by an interrupted run.
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, clone)); err != nil {
		return "", fmt.Errorf("drop %s: %w", clone, err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q TEMPLATE %q`, clone, template)); err != nil {
		return "", fmt.Errorf("clone %s from %s (is it migrated?): %w", clone, template, err)
	}

	return dsnWithDatabase(templateDSN, clone)
}

// dsnWithDatabase rewrites the database in a connection URL, leaving the
// credentials and parameters alone.
func dsnWithDatabase(dsn, database string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.Path = "/" + database
	return u.String(), nil
}

// freePort asks the kernel for a port and gives it straight back. The server
// binds it a moment later; nothing else on a test machine is racing for it.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type testServer struct {
	t       *testing.T
	cmd     *exec.Cmd
	baseURL string
	logs    *syncBuffer

	exited  chan struct{}
	exitErr error
}

type serverOptions struct {
	// env entries override or add to the harness defaults.
	env map[string]string
	// autoSetup provisions an org and an admin so the test can log in.
	autoSetup bool
	// waitReady polls /readyz instead of /healthz before returning. Tests that
	// deliberately break a dependency must not wait for readiness.
	waitReady bool
}

// startServer launches `teamup server` against the live development services
// and blocks until it answers on its port.
func startServer(t *testing.T, opts serverOptions) *testServer {
	t.Helper()

	bin := serverBinary(t)
	port := freePort(t)

	env := map[string]string{
		"TEAMUP_PORT":               port,
		"TEAMUP_ENV":                "production",
		"TEAMUP_LOG_LEVEL":          "info",
		"TEAMUP_DATABASE_URL":       harnessDatabaseURL(t),
		"TEAMUP_REDIS_URL":          envOr("TEAMUP_TEST_REDIS_URL", "redis://localhost:6379"),
		"TEAMUP_MINIO_ENDPOINT":     envOr("TEAMUP_TEST_MINIO_ENDPOINT", "localhost:9000"),
		"TEAMUP_MINIO_ACCESS_KEY":   "minioadmin",
		"TEAMUP_MINIO_SECRET_KEY":   "minioadmin",
		"TEAMUP_MINIO_BUCKET":       "teamup",
		"TEAMUP_MINIO_SECURE":       "false",
		"TEAMUP_LIVEKIT_URL":        envOr("TEAMUP_TEST_LIVEKIT_URL", "ws://localhost:7880"),
		"TEAMUP_LIVEKIT_API_KEY":    "devkey",
		"TEAMUP_LIVEKIT_API_SECRET": "devsecret",
		"TEAMUP_BASE_URL":           "http://127.0.0.1:" + port,
	}
	if opts.autoSetup {
		env["TEAMUP_AUTO_SETUP"] = "true"
		env["TEAMUP_SETUP_ORG_NAME"] = "Harness Org"
		env["TEAMUP_SETUP_ORG_SUBDOMAIN"] = "harness"
		env["TEAMUP_SETUP_ADMIN_NAME"] = "Harness Admin"
		env["TEAMUP_SETUP_ADMIN_EMAIL"] = harnessAdminEmail
		env["TEAMUP_SETUP_ADMIN_PASSWORD"] = harnessAdminPassword
		env["TEAMUP_SETUP_OIDC_PROVIDER"] = "custom"
	}
	for k, v := range opts.env {
		env[k] = v
	}

	cmd := exec.Command(bin, "server")
	cmd.Env = append(os.Environ(), flattenEnv(env)...)
	logs := &syncBuffer{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	// Its own process group, so a stray signal to the test's group cannot take
	// the server with it and the test alone decides when it dies.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	ts := &testServer{
		t:       t,
		cmd:     cmd,
		baseURL: "http://127.0.0.1:" + port,
		logs:    logs,
		exited:  make(chan struct{}),
	}
	go func() {
		ts.exitErr = cmd.Wait()
		close(ts.exited)
	}()
	t.Cleanup(ts.stop)

	probe := "/healthz"
	if opts.waitReady {
		probe = "/readyz"
	}
	ts.waitFor(probe, 90*time.Second)
	return ts
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func flattenEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func (s *testServer) waitFor(path string, timeout time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-s.exited:
			s.t.Fatalf("server exited during startup: %v\n%s", s.exitErr, s.logs.String())
		default:
		}
		resp, err := client.Get(s.baseURL + path)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("server never answered %s within %s\n%s", path, timeout, s.logs.String())
}

// get performs a plain GET and returns the status and body.
func (s *testServer) get(path string) (int, string) {
	s.t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(s.baseURL + path)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// login authenticates the auto-setup admin and returns the access-token cookie.
func (s *testServer) login() string {
	s.t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, harnessAdminEmail, harnessAdminPassword)
	resp, err := client.Post(s.baseURL+"/api/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		s.t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("login returned %d: %s\n%s", resp.StatusCode, payload, s.logs.String())
	}
	// Read Set-Cookie straight off the response: the auth cookies carry Secure,
	// which a cookie jar would refuse to store for a plain-HTTP test server.
	for _, c := range resp.Cookies() {
		if c.Name == "at" {
			return c.Value
		}
	}
	s.t.Fatalf("login did not set an access-token cookie")
	return ""
}

// terminate sends SIGTERM and reports how long the process took to leave.
func (s *testServer) terminate() (sent time.Time, elapsed time.Duration, exited bool) {
	s.t.Helper()
	sent = time.Now()
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		s.t.Fatalf("SIGTERM: %v", err)
	}
	select {
	case <-s.exited:
		return sent, time.Since(sent), true
	case <-time.After(2 * time.Minute):
		return sent, time.Since(sent), false
	}
}

func (s *testServer) stop() {
	if s.cmd.Process == nil {
		return
	}
	select {
	case <-s.exited:
		return
	default:
	}
	_ = s.cmd.Process.Signal(syscall.SIGKILL)
	select {
	case <-s.exited:
	case <-time.After(10 * time.Second):
	}
}

// slowBodyRequest starts a POST whose body is dribbled out over the given
// duration. The handler is inside ShouldBindJSON for that whole window, which
// is an in-flight request the drain is supposed to wait for — without needing
// a test-only slow endpoint in the production router.
func (s *testServer) slowBodyRequest(ctx context.Context, over time.Duration) <-chan slowResult {
	done := make(chan slowResult, 1)

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/auth/login", pr)
	if err != nil {
		done <- slowResult{err: err}
		return done
	}
	req.Header.Set("Content-Type", "application/json")

	go func() {
		// Headers and an opening fragment go immediately, so the handler is
		// entered and blocks reading the rest.
		_, _ = io.WriteString(pw, `{"email":"nobody@teamup.test","pass`)
		time.Sleep(over)
		_, _ = io.WriteString(pw, `word":"not-the-password"}`)
		_ = pw.Close()
	}()

	go func() {
		started := time.Now()
		client := &http.Client{Timeout: 90 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			done <- slowResult{err: err, took: time.Since(started)}
			return
		}
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
		done <- slowResult{status: resp.StatusCode, body: string(body), err: readErr, took: time.Since(started)}
	}()

	return done
}

type slowResult struct {
	status int
	body   string
	err    error
	took   time.Duration
}

package observability

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
)

var (
	// HTTP metrics
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_http_requests_total",
		Help: "Total HTTP requests",
	}, []string{"method", "path", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "teamup_http_request_duration_seconds",
		Help:    "HTTP request latency",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	httpRequestsInFlight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "teamup_http_requests_in_flight",
		Help: "Current in-flight HTTP requests",
	}, []string{"method", "path"})

	// WebSocket metrics
	wsConnectionsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "teamup_ws_connections_active",
		Help: "Active WebSocket connections",
	})
	wsMessagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_ws_messages_total",
		Help: "WebSocket messages",
	}, []string{"direction", "type"})
	wsErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_ws_errors_total",
		Help: "WebSocket errors",
	}, []string{"type"})

	// Database metrics
	dbQueriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_db_queries_total",
		Help: "Database queries",
	}, []string{"operation", "status"})
	dbQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "teamup_db_query_duration_seconds",
		Help: "DB query latency",
	}, []string{"operation"})

	// Business metrics
	activeUsers = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "teamup_active_users",
		Help: "Currently active users",
	}, []string{"org_id"})
	messagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_messages_total",
		Help: "Total messages sent",
	}, []string{"org_id"})
	callsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_calls_total",
		Help: "Total calls",
	}, []string{"org_id", "type"})
	tasksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "teamup_tasks_total",
		Help: "Total tasks",
	}, []string{"org_id", "status"})

	// System metrics
	goGoroutines = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "teamup_go_goroutines",
		Help: "Number of goroutines",
	})
	goMemStats = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "teamup_go_memstats",
		Help: "Go memory stats",
	}, []string{"type"})
)

// version of the running build, recorded by Init so the health responses can
// report which one answered.
var (
	versionMu sync.RWMutex
	version   = "dev"
)

func serviceVersion() string {
	versionMu.RLock()
	defer versionMu.RUnlock()
	return version
}

// Init initializes the global logger
func Init(serviceName, buildVersion, environment string) {
	versionMu.Lock()
	if buildVersion != "" {
		version = buildVersion
	}
	versionMu.Unlock()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	var output zerolog.ConsoleWriter
	if environment == "development" {
		output = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05.000"}
		log.Logger = zerolog.New(output).Level(zerolog.DebugLevel).With().Caller().Logger()
	} else {
		log.Logger = zerolog.New(os.Stdout).Level(zerolog.InfoLevel)
	}

	log.Logger = log.Logger.With().
		Str("service", serviceName).
		Str("version", serviceVersion()).
		Str("env", environment).
		Logger()

	// Start system metrics collector
	go startSystemMetricsCollector()
}

type ctxKey string

const (
	correlationIDKey ctxKey = "correlation_id"
	userIDKey        ctxKey = "user_id"
	orgIDKey         ctxKey = "org_id"
)

func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

func CorrelationID(ctx context.Context) string {
	if v := ctx.Value(correlationIDKey); v != nil {
		return v.(string)
	}
	return ""
}

func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

func UserID(ctx context.Context) string {
	if v := ctx.Value(userIDKey); v != nil {
		return v.(string)
	}
	return ""
}

func WithOrgID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, orgIDKey, id)
}

func OrgID(ctx context.Context) string {
	if v := ctx.Value(orgIDKey); v != nil {
		return v.(string)
	}
	return ""
}

func Logger(ctx context.Context) zerolog.Logger {
	l := log.Logger
	if cid := CorrelationID(ctx); cid != "" {
		l = l.With().Str("correlation_id", cid).Logger()
	}
	if uid := UserID(ctx); uid != "" {
		l = l.With().Str("user_id", uid).Logger()
	}
	if oid := OrgID(ctx); oid != "" {
		l = l.With().Str("org_id", oid).Logger()
	}
	return l
}

// GinLogger returns a Gin middleware for structured logging
func GinLogger(logger zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		status := c.Writer.Status()
		latency := time.Since(start)

		logger.Info().
			Str("method", method).
			Str("path", path).
			Int("status", status).
			Dur("latency", latency).
			Str("ip", c.ClientIP()).
			Msg("HTTP request")
	}
}

// GinMetrics returns a Gin middleware for Prometheus metrics
func GinMetrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := sanitizePath(c.Request.URL.Path)
		method := c.Request.Method

		httpRequestsInFlight.WithLabelValues(method, path).Inc()
		start := time.Now()

		c.Next()

		status := c.Writer.Status()
		duration := time.Since(start).Seconds()

		httpRequestsInFlight.WithLabelValues(method, path).Dec()
		httpRequestsTotal.WithLabelValues(method, path, string(rune(status))).Inc()
		httpRequestDuration.WithLabelValues(method, path).Observe(duration)
	}
}

func sanitizePath(path string) string {
	// Replace UUIDs and IDs with placeholders
	// This is a simplified version; in production use a proper path matcher
	return path
}

// MetricsHandler exposes Prometheus metrics
func MetricsHandler(c *gin.Context) {
	promhttp.Handler().ServeHTTP(c.Writer, c.Request)
}

// Health check types
type HealthResponse struct {
	Status    string                 `json:"status"`
	Checks    map[string]CheckResult `json:"checks"`
	Timestamp time.Time              `json:"timestamp"`
	Version   string                 `json:"version"`
}

type CheckResult struct {
	Status  string `json:"status"` // "pass" | "warn" | "fail"
	Message string `json:"message,omitempty"`
	Latency string `json:"latency,omitempty"`
}

type HealthChecker interface {
	Check(ctx context.Context) error
	Name() string
}

// LiveHandler - liveness probe (process alive)
//
// Deliberately answers from nothing but the process itself. A liveness probe
// that consulted a dependency would have Kubernetes restart pods over an
// outage a restart cannot fix; that judgement belongs to readiness, in
// readiness.go.
func LiveHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "alive"})
}

// HealthHandler - full health check
func HealthHandler(checkers ...HealthChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		checks := make(map[string]CheckResult)
		overall := "healthy"

		for _, checker := range checkers {
			start := time.Now()
			err := checker.Check(ctx)
			latency := time.Since(start)

			if err != nil {
				checks[checker.Name()] = CheckResult{
					Status:  "fail",
					Message: err.Error(),
					Latency: latency.String(),
				}
				overall = "unhealthy"
			} else {
				checks[checker.Name()] = CheckResult{
					Status:  "pass",
					Latency: latency.String(),
				}
			}
		}

		statusCode := http.StatusOK
		if overall == "unhealthy" {
			statusCode = http.StatusServiceUnavailable
		}

		c.JSON(statusCode, HealthResponse{
			Status:    overall,
			Checks:    checks,
			Timestamp: time.Now(),
			Version:   serviceVersion(),
		})
	}
}

// RecordWSConnection increments/decrements active WS connections
func RecordWSConnection(delta int) {
	if delta > 0 {
		wsConnectionsActive.Add(float64(delta))
	} else {
		wsConnectionsActive.Sub(float64(-delta))
	}
}

// RecordWSMessage records a WebSocket message
func RecordWSMessage(direction, msgType string) {
	wsMessagesTotal.WithLabelValues(direction, msgType).Inc()
}

// RecordWSError records a WebSocket error
func RecordWSError(msgType string) {
	wsErrorsTotal.WithLabelValues(msgType).Inc()
}

// RecordDBQuery records a database query
func RecordDBQuery(operation, status string, duration time.Duration) {
	dbQueriesTotal.WithLabelValues(operation, status).Inc()
	dbQueryDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

// RecordBusinessMetrics records business-level metrics
func RecordMessageSent(orgID string) {
	messagesTotal.WithLabelValues(orgID).Inc()
}

func RecordCallStarted(orgID, callType string) {
	callsTotal.WithLabelValues(orgID, callType).Inc()
}

func RecordTaskCreated(orgID, status string) {
	tasksTotal.WithLabelValues(orgID, status).Inc()
}

func UpdateActiveUsers(orgID string, count int) {
	activeUsers.WithLabelValues(orgID).Set(float64(count))
}

func startSystemMetricsCollector() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		goGoroutines.Set(float64(runtime.NumGoroutine()))
		goMemStats.WithLabelValues("alloc").Set(float64(ms.Alloc))
		goMemStats.WithLabelValues("sys").Set(float64(ms.Sys))
		goMemStats.WithLabelValues("heap_idle").Set(float64(ms.HeapIdle))
		goMemStats.WithLabelValues("heap_inuse").Set(float64(ms.HeapInuse))
	}
}

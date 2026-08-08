## Question

Define observability stack: structured JSON logging (zerolog/zap) with correlation IDs, `/healthz` + `/readyz` endpoints (liveness/readiness), Prometheus metrics (RED: rate/errors/duration for HTTP, WS, DB, LiveKit; custom: active users, calls, messages), Grafana dashboards (optional, Helm), and log sampling in high-traffic paths.

Status: claimed

## Answer

**Decision: Observability stack architecture defined below.**

### 1. Structured Logging (zerolog)

```go
// internal/observability/logging.go
package observability

import (
    "context"
    "os"
    "github.com/rs/zerolog"
    "github.com/rs/zerolog/log"
    "github.com/rs/zerolog/pkgerrors"
)

func Init(serviceName, version, environment string) {
    zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
    zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

    // Console output for dev, JSON for prod
    var output zerolog.ConsoleWriter
    if environment == "development" {
        output = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05.000"}
        log.Logger = zerolog.New(output).Level(zerolog.DebugLevel).With().Caller().Logger()
    } else {
        log.Logger = zerolog.New(os.Stdout).Level(zerolog.InfoLevel)
    }

    // Global fields
    log.Logger = log.Logger.With().
        Str("service", serviceName).
        Str("version", version).
        Str("env", environment).
        Logger()
}

// Context keys
type ctxKey string
const (
    correlationIDKey ctxKey = "correlation_id"
    userIDKey        ctxKey = "user_id"
    orgIDKey         ctxKey = "org_id"
)

// WithCorrelationID adds correlation ID to context
func WithCorrelationID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, correlationIDKey, id)
}

func CorrelationID(ctx context.Context) string {
    if v := ctx.Value(correlationIDKey); v != nil {
        return v.(string)
    }
    return ""
}

// Logger returns a contextual logger with correlation ID, user ID, org ID
func Logger(ctx context.Context) zerolog.Logger {
    l := log.Logger
    if cid := CorrelationID(ctx); cid != "" {
        l = l.With().Str("correlation_id", cid).Logger()
    }
    if uid := ctx.Value(userIDKey); uid != nil {
        l = l.With().Str("user_id", uid.(string)).Logger()
    }
    if oid := ctx.Value(orgIDKey); oid != nil {
        l = l.With().Str("org_id", oid.(string)).Logger()
    }
    return l
}

// HTTP middleware for correlation ID
func CorrelationMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        cid := r.Header.Get("X-Correlation-ID")
        if cid == "" { cid = uuid.New().String() }

        ctx := WithCorrelationID(r.Context(), cid)
        w.Header().Set("X-Correlation-ID", cid)

        // Log request
        start := time.Now()
        l := Logger(ctx).With().
            Str("method", r.Method).
            Str("path", r.URL.Path).
            Str("remote_addr", r.RemoteAddr).
            Logger()
        l.Info().Msg("request started")

        // Wrap response writer for status code
        rw := &responseWriter{ResponseWriter: w, statusCode: 200}
        next.ServeHTTP(rw, r.WithContext(ctx))

        l.Info().
            Int("status", rw.statusCode).
            Dur("duration_ms", time.Since(start)).
            Msg("request completed")
    })
}
```

**Log Levels:**
| Level | Use Case |
|-------|----------|
| `debug` | Detailed flow, SQL queries, WS frames |
| `info` | Request start/end, business events (user login, call started) |
| `warn` | Retryable errors, rate limits, slow queries (>500ms) |
| `error` | Failed requests, DB errors, external service failures |
| `fatal` | Startup failures (config, migrations) |

**Sampling (high-traffic paths):**
```go
// internal/observability/sampling.go
func NewSampler(rate float64) zerolog.LevelWriter {
    return zerolog.Sampler(&zerolog.BasicSampler{N: int(1/rate)})
}

// Usage: wrap logger for high-volume endpoints (WS messages, presence)
var wsLogger = log.Output(NewSampler(0.01)) // Log 1% of WS messages
```

---

### 2. Health Endpoints

```go
// internal/observability/health.go
type HealthChecker interface {
    Check(ctx context.Context) error
    Name() string
}

type HealthResponse struct {
    Status    string            `json:"status"` // "healthy" | "degraded" | "unhealthy"
    Checks    map[string]CheckResult `json:"checks"`
    Timestamp time.Time         `json:"timestamp"`
    Version   string            `json:"version"`
}

type CheckResult struct {
    Status  string `json:"status"` // "pass" | "warn" | "fail"
    Message string `json:"message,omitempty"`
    Latency string `json:"latency,omitempty"`
}

func (s *Server) HealthHandler(w http.ResponseWriter, r *http.Request) {
    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    checks := map[string]HealthChecker{
        "database":   s.dbHealth,
        "redis":      s.redisHealth,
        "minio":      s.minioHealth,
        "livekit":    s.livekitHealth,
    }

    results := make(map[string]CheckResult)
    overall := "healthy"

    for name, checker := range checks {
        start := time.Now()
        err := checker.Check(ctx)
        latency := time.Since(start)

        if err != nil {
            results[name] = CheckResult{Status: "fail", Message: err.Error(), Latency: latency.String()}
            overall = "unhealthy"
        } else {
            results[name] = CheckResult{Status: "pass", Latency: latency.String()}
        }
    }

    w.Header().Set("Content-Type", "application/json")
    if overall == "unhealthy" { w.WriteHeader(http.StatusServiceUnavailable) }
    json.NewEncoder(w).Encode(HealthResponse{
        Status: overall, Checks: results, Timestamp: time.Now(), Version: version,
    })
}

// Liveness: minimal (process alive)
func (s *Server) LiveHandler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
}

// Readiness: can serve traffic (dependencies healthy)
func (s *Server) ReadyHandler(w http.ResponseWriter, r *http.Request) {
    // Reuse health checks but with shorter timeout
    ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
    defer cancel()

    if err := s.dbHealth.Check(ctx); err != nil {
        http.Error(w, "not ready", http.StatusServiceUnavailable)
        return
    }
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}
```

**Kubernetes Probes:**
```yaml
# In Helm deployment.yaml
livenessProbe:
  httpGet: { path: /healthz/live, port: 8080 }
  initialDelaySeconds: 30
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 3

readinessProbe:
  httpGet: { path: /healthz/ready, port: 8080 }
  initialDelaySeconds: 10
  periodSeconds: 5
  timeoutSeconds: 3
  failureThreshold: 3
```

---

### 3. Prometheus Metrics (RED + Custom)

```go
// internal/observability/metrics.go
package observability

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    // ──────────────────────────────────────────────
    // HTTP (RED)
    // ──────────────────────────────────────────────
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

    // ──────────────────────────────────────────────
    // WebSocket (RED)
    // ──────────────────────────────────────────────
    wsConnectionsActive = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "teamup_ws_connections_active",
        Help: "Active WebSocket connections",
    })
    wsMessagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "teamup_ws_messages_total",
        Help: "WebSocket messages",
    }, []string{"direction", "type"}) // direction: sent|received
    wsMessageDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name: "teamup_ws_message_duration_seconds",
        Help: "WS message processing latency",
    }, []string{"type"})
    wsErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "teamup_ws_errors_total",
        Help: "WebSocket errors",
    }, []string{"type"})

    // ──────────────────────────────────────────────
    // Database (RED)
    // ──────────────────────────────────────────────
    dbQueriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "teamup_db_queries_total",
        Help: "Database queries",
    }, []string{"operation", "status"}) // operation: select|insert|update|delete
    dbQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name: "teamup_db_query_duration_seconds",
        Help: "DB query latency",
    }, []string{"operation"})
    dbPoolStats = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "teamup_db_pool",
        Help: "DB connection pool stats",
    }, []string{"state"}) // idle|in_use|wait_count

    // ──────────────────────────────────────────────
    // LiveKit
    // ──────────────────────────────────────────────
    livekitRoomsActive = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "teamup_livekit_rooms_active",
        Help: "Active LiveKit rooms",
    })
    livekitParticipantsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "teamup_livekit_participants_total",
        Help: "LiveKit participant events",
    }, []string{"event"}) // joined|left
    livekitRecordingDuration = promauto.NewHistogram(prometheus.HistogramOpts{
        Name: "teamup_livekit_recording_duration_seconds",
        Help: "Recording duration",
    })

    // ──────────────────────────────────────────────
    // Business Metrics (Custom)
    // ──────────────────────────────────────────────
    activeUsers = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "teamup_active_users",
        Help: "Currently active users (WS connected)",
    }, []string{"org_id"})
    totalUsers = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "teamup_total_users",
        Help: "Total registered users",
    }, []string{"org_id"})
    messagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "teamup_messages_total",
        Help: "Total messages sent",
    }, []string{"org_id", "type"}) // type: chat|call|task
    callsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "teamup_calls_total",
        Help: "Total calls",
    }, []string{"org_id", "type"}) // video|audio
    callDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name: "teamup_call_duration_seconds",
        Help: "Call duration",
    }, []string{"org_id"})
    tasksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "teamup_tasks_total",
        Help: "Tasks",
    }, []string{"org_id", "status"}) // todo|in_progress|done
    tasksByAssignee = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "teamup_tasks_by_assignee",
        Help: "Tasks per assignee",
    }, []string{"org_id", "user_id"})

    // ──────────────────────────────────────────────
    // System
    // ──────────────────────────────────────────────
    goGoroutines = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "teamup_go_goroutines",
        Help: "Number of goroutines",
    })
    goMemStats = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "teamup_go_memstats",
        Help: "Go memory stats",
    }, []string{"type"}) // alloc|sys|heap_idle|heap_inuse
)

// Middleware to record HTTP metrics
func HTTPMetricsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        path := sanitizePath(r.URL.Path) // /api/channels/123 → /api/channels/:id
        start := time.Now()
        rw := &responseWriter{ResponseWriter: w, statusCode: 200}

        httpRequestsInFlight.WithLabelValues(r.Method, path).Inc()
        defer httpRequestsInFlight.WithLabelValues(r.Method, path).Dec()

        next.ServeHTTP(rw, r)

        duration := time.Since(start).Seconds()
        httpRequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(rw.statusCode)).Inc()
        httpRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
    })
}

// Update system metrics periodically
func StartSystemMetricsCollector() {
    ticker := time.NewTicker(15 * time.Second)
    go func() {
        for range ticker.C {
            var ms runtime.MemStats
            runtime.ReadMemStats(&ms)
            goGoroutines.Set(float64(runtime.NumGoroutine()))
            goMemStats.WithLabelValues("alloc").Set(float64(ms.Alloc))
            goMemStats.WithLabelValues("sys").Set(float64(ms.Sys))
            goMemStats.WithLabelValues("heap_idle").Set(float64(ms.HeapIdle))
            goMemStats.WithLabelValues("heap_inuse").Set(float64(ms.HeapInuse))
        }
    }()
}
```

---

### 4. Metrics Endpoint

```go
// internal/observability/metrics_handler.go
func (s *Server) MetricsHandler(w http.ResponseWriter, r *http.Request) {
    // Prometheus handler
    promhttp.Handler().ServeHTTP(w, r)
}

// Protected in production (internal network only or auth)
```

---

### 5. Grafana Dashboards (Helm)

```yaml
# helm/teamup/templates/servicemonitor.yaml
{{- if .Values.monitoring.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "teamup.fullname" . }}
  labels: {{- include "teamup.labels" . | nindent 4 }}
spec:
  selector:
    matchLabels: {{- include "teamup.selectorLabels" . | nindent 6 }}
  endpoints:
  - port: http
    path: /metrics
    interval: 30s
{{- end }}
```

**Dashboards (JSON, deployed via ConfigMap):**
- **Overview**: Request rate, error rate, latency (p50/p95/p99), active connections
- **WebSocket**: Connections, messages/sec, message latency, errors by type
- **Database**: Query rate, latency, pool usage, slow queries
- **LiveKit**: Active rooms, participants, recordings, bandwidth
- **Business**: Active users, messages, calls, tasks by org
- **System**: Goroutines, memory, GC pauses, CPU

---

### 6. Distributed Tracing (OpenTelemetry - Optional v2)

```go
// internal/observability/tracing.go (v2)
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/sdk/resource"
    semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

func InitTracing(ctx context.Context, serviceName string) (*sdktrace.TracerProvider, error) {
    exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
    if err != nil { return nil, err }

    res, err := resource.New(ctx,
        resource.WithAttributes(semconv.ServiceName(serviceName)),
    )
    if err != nil { return nil, err }

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(res),
        sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))),
    )
    otel.SetTracerProvider(tp)
    return tp, nil
}
```

**v1 Decision:** Skip tracing — logs + metrics sufficient. Add in v2 when debugging distributed calls.

---

### 7. Alerting Rules (PrometheusRule)

```yaml
# helm/teamup/templates/prometheusrule.yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: {{ include "teamup.fullname" . }}-alerts
spec:
  groups:
  - name: teamup.rules
    rules:
    - alert: HighErrorRate
      expr: |
        sum(rate(teamup_http_requests_total{status=~"5.."}[5m]))
        / sum(rate(teamup_http_requests_total[5m])) > 0.05
      for: 5m
      labels: { severity: critical }
      annotations:
        summary: "High HTTP error rate (>5%)"
    - alert: HighLatency
      expr: histogram_quantile(0.95, rate(teamup_http_request_duration_seconds_bucket[5m])) > 1
      for: 5m
      labels: { severity: warning }
      annotations:
        summary: "P95 latency > 1s"
    - alert: TooManyGoroutines
      expr: teamup_go_goroutines > 10000
      for: 10m
      labels: { severity: warning }
      annotations:
        summary: "Goroutine leak suspected"
    - alert: DatabasePoolExhausted
      expr: teamup_db_pool{state="in_use"} / teamup_db_pool{state="idle"} > 0.9
      for: 5m
      labels: { severity: critical }
      annotations:
        summary: "DB connection pool near exhaustion"
    - alert: NoActiveConnections
      expr: teamup_ws_connections_active == 0
      for: 15m
      labels: { severity: info }
      annotations:
        summary: "No active WebSocket connections"
```

---

### 8. Log Aggregation (Loki - Optional)

```yaml
# docker-compose.yml (add to stack)
loki:
  image: grafana/loki:2.9
  ports: ["3100:3100"]
  volumes: [loki-data:/loki]

promtail:
  image: grafana/promtail:2.9
  volumes:
    - /var/log:/var/log
    - ./promtail-config.yml:/etc/promtail/config.yml
```

**v1 Decision:** Stdout JSON → collected by host logging agent (fluentd, vector, or cloud provider). No Loki in default Helm.

---

### 9. Open Questions (for later)

- **OpenTelemetry tracing** — v2
- **Loki/Grafana Logs** — v2
- **Custom business dashboards per org** — v2
- **SLO/SLI definitions** (99.9% availability, <200ms p95) — v2
- **Anomaly detection** (ML-based) — v3

Status: resolved

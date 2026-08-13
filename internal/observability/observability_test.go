package observability

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// resetHTTPMetrics drops every series the HTTP middleware has recorded so one
// test cannot see another's. The vectors are package globals registered on the
// default registry at init, so this is the only way to isolate them.
func resetHTTPMetrics() {
	httpRequestsTotal.Reset()
	httpRequestDuration.Reset()
	httpRequestsInFlight.Reset()
}

// metricsEngine builds the router under test: the metrics middleware in front
// of one parameterised route, which is the shape main.go wires up.
func metricsEngine() *gin.Engine {
	r := gin.New()
	r.Use(GinMetrics())
	r.GET("/api/tasks/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func get(t *testing.T, r *gin.Engine, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

// scrape returns the exposition text a Prometheus server would actually read
// off /metrics, so assertions are made against the bytes that ship rather than
// against an in-process view of them.
func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	MetricsHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics returned %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// series returns the label sets of every exposed series of the named metric,
// sorted, each rendered back as `key="value",...` so an unexpected label value
// appears verbatim in the failure message.
func series(t *testing.T, name string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(scrape(t), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		open := strings.IndexByte(line, '{')
		close := strings.LastIndexByte(line, '}')
		if open < 0 || close < open || line[:open] != name {
			continue
		}
		out = append(out, line[open+1:close])
	}
	sort.Strings(out)
	return out
}

// labelValues collects the distinct values a single label takes across every
// series of a metric.
func labelValues(t *testing.T, name, label string) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	for _, s := range series(t, name) {
		for _, pair := range strings.Split(s, ",") {
			k, v, ok := strings.Cut(pair, "=")
			if !ok || k != label {
				continue
			}
			v = strings.Trim(v, `"`)
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
}

// The status label has to be the decimal status code. Converting an int to a
// string by rune yields the character at that code point, which is not a
// status code and cannot be grouped, alerted on, or read by a human.
func TestGinMetricsLabelsStatusWithTheDecimalCode(t *testing.T) {
	resetHTTPMetrics()
	r := metricsEngine()

	if code := get(t, r, "/api/tasks/11111111-1111-1111-1111-111111111111"); code != http.StatusOK {
		t.Fatalf("matched route returned %d, want 200", code)
	}
	if code := get(t, r, "/api/nothing-here"); code != http.StatusNotFound {
		t.Fatalf("unmatched route returned %d, want 404", code)
	}

	got := labelValues(t, "teamup_http_requests_total", "status")
	want := []string{"200", "404"}
	if !equal(got, want) {
		t.Errorf("status labels = %q, want %q", got, want)
	}
}

// Every status code the server can return must survive the label round-trip,
// not just the two the happy path produces.
func TestGinMetricsStatusLabelForEveryCode(t *testing.T) {
	for _, status := range []int{200, 201, 204, 400, 401, 403, 404, 409, 422, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			resetHTTPMetrics()
			r := gin.New()
			r.Use(GinMetrics())
			r.GET("/x", func(c *gin.Context) { c.Status(status) })

			get(t, r, "/x")

			got := labelValues(t, "teamup_http_requests_total", "status")
			want := []string{fmt.Sprint(status)}
			if !equal(got, want) {
				t.Errorf("status labels = %q, want %q", got, want)
			}
		})
	}
}

// Two requests for two different tasks are two observations of one route, not
// two time series. Labelling with the raw URL gives Prometheus a new series per
// task id and the memory cost grows without bound.
func TestGinMetricsLabelsPathWithTheRouteTemplate(t *testing.T) {
	resetHTTPMetrics()
	r := metricsEngine()

	get(t, r, "/api/tasks/11111111-1111-1111-1111-111111111111")
	get(t, r, "/api/tasks/22222222-2222-2222-2222-222222222222")

	for _, name := range []string{
		"teamup_http_requests_total",
		"teamup_http_request_duration_seconds_count",
		"teamup_http_requests_in_flight",
	} {
		got := series(t, name)
		if len(got) != 1 {
			t.Errorf("%s has %d series after two requests to one route, want 1: %q", name, len(got), got)
			continue
		}
		if !strings.Contains(got[0], `path="/api/tasks/:id"`) {
			t.Errorf("%s series = %q, want path=\"/api/tasks/:id\"", name, got[0])
		}
	}

	// The single series must have counted both requests; collapsing the label
	// is only correct if nothing was dropped along the way.
	if got := counterValue(t, "teamup_http_requests_total"); got != 2 {
		t.Errorf("teamup_http_requests_total = %v, want 2", got)
	}
}

// An unmatched URL is attacker-controlled. If it reaches the label it is a
// remote cardinality bomb: a loop over random paths mints a series per request.
func TestGinMetricsCollapsesUnmatchedPaths(t *testing.T) {
	resetHTTPMetrics()
	r := metricsEngine()

	for i := 0; i < 25; i++ {
		if code := get(t, r, fmt.Sprintf("/%d-does-not-exist", i)); code != http.StatusNotFound {
			t.Fatalf("request %d returned %d, want 404", i, code)
		}
	}

	got := series(t, "teamup_http_requests_total")
	if len(got) != 1 {
		t.Fatalf("25 unmatched URLs produced %d series, want 1: %q", len(got), got)
	}
	for _, raw := range []string{"does-not-exist", "/0-", "/24-"} {
		if strings.Contains(got[0], raw) {
			t.Errorf("series %q echoes the requested URL back into the label", got[0])
		}
	}
	if got := counterValue(t, "teamup_http_requests_total"); got != 25 {
		t.Errorf("teamup_http_requests_total = %v, want 25", got)
	}
}

// The in-flight gauge shares the path label, so it must settle back to zero on
// the same series it incremented. A path computed twice from different sources
// would leak a permanently non-zero gauge.
func TestGinMetricsInFlightReturnsToZero(t *testing.T) {
	resetHTTPMetrics()
	r := metricsEngine()

	get(t, r, "/api/tasks/11111111-1111-1111-1111-111111111111")
	get(t, r, "/api/nothing-here")

	for _, line := range strings.Split(scrape(t), "\n") {
		if !strings.HasPrefix(line, "teamup_http_requests_in_flight{") {
			continue
		}
		if !strings.HasSuffix(line, " 0") {
			t.Errorf("in-flight gauge did not settle: %q", line)
		}
	}
}

// counterValue sums the values of every series of a counter metric.
func counterValue(t *testing.T, name string) float64 {
	t.Helper()
	var total float64
	for _, line := range strings.Split(scrape(t), "\n") {
		if !strings.HasPrefix(line, name+"{") {
			continue
		}
		var v float64
		if _, err := fmt.Sscanf(line[strings.LastIndexByte(line, '}')+1:], "%g", &v); err != nil {
			t.Fatalf("cannot read value from %q: %v", line, err)
		}
		total += v
	}
	return total
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

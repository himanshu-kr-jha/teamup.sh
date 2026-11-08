package status

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

type memoryStore struct {
	mu      sync.Mutex
	buckets map[int64]map[string]bool
	fail    error
}

func newMemoryStore() *memoryStore { return &memoryStore{buckets: map[int64]map[string]bool{}} }

func (m *memoryStore) Record(_ context.Context, minute int64, results map[string]bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.buckets[minute]
	if b == nil {
		b = map[string]bool{}
		m.buckets[minute] = b
	}
	for k, up := range results {
		if prev, ok := b[k]; ok && !prev {
			continue
		}
		b[k] = up
	}
	return nil
}

func (m *memoryStore) Load(_ context.Context, first, last int64) (map[int64]map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return nil, m.fail
	}
	out := map[int64]map[string]bool{}
	for k, v := range m.buckets {
		if k >= first && k <= last {
			out[k] = v
		}
	}
	return out, nil
}

// world is a clock you can move and services you can break.
type world struct {
	now   time.Time
	up    map[string]bool
	store *memoryStore
	svc   *Service
}

func newWorld() *world {
	w := &world{
		now:   time.Date(2026, 11, 8, 12, 0, 30, 0, time.UTC),
		up:    map[string]bool{"database": true, "redis": true, "minio": true, "livekit": true},
		store: newMemoryStore(),
	}
	w.svc = NewService(func(context.Context) map[string]bool {
		out := map[string]bool{}
		for k, v := range w.up {
			out[k] = v
		}
		return out
	}, w.store, zerolog.Nop())
	w.svc.now = func() time.Time { return w.now }
	w.svc.cacheTTL = 0
	return w
}

// minutes records once a minute for n minutes.
func (w *world) minutes(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := w.svc.RecordOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		w.now = w.now.Add(time.Minute)
	}
}

func row(t *testing.T, r Report, key string) ComponentReport {
	t.Helper()
	for _, c := range r.Components {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("no %s in %+v", key, r.Components)
	return ComponentReport{}
}

func TestEverythingUpIsOperationalWithFullUptime(t *testing.T) {
	w := newWorld()
	w.minutes(t, 90)
	r := w.svc.Report(context.Background())
	if r.Overall != "operational" || !r.HistoryAvailable {
		t.Fatalf("got %+v", r)
	}
	if len(r.Components) != len(Components) {
		t.Fatalf("every component reported: %d", len(r.Components))
	}
	calls := row(t, r, "livekit")
	if calls.Status != "operational" || calls.Uptime == nil || *calls.Uptime != 100 || len(calls.Incidents) != 0 {
		t.Errorf("calls: %+v", calls)
	}
	if len(calls.Hours) != 24 {
		t.Fatalf("a day of bars: %d", len(calls.Hours))
	}
	// Before anything was recorded there is nothing to show, not a green bar.
	if calls.Hours[0].Status != "none" || calls.Hours[23].Status != "up" {
		t.Errorf("first %+v last %+v", calls.Hours[0], calls.Hours[23])
	}
}

func TestAnOutageEarlierShowsAsAnIncidentWithItsLength(t *testing.T) {
	w := newWorld()
	w.minutes(t, 30)
	w.up["minio"] = false
	w.minutes(t, 12)
	w.up["minio"] = true
	w.minutes(t, 20)

	r := w.svc.Report(context.Background())
	if r.Overall != "operational" {
		t.Errorf("fine again now: %s", r.Overall)
	}
	files := row(t, r, "minio")
	if len(files.Incidents) != 1 || files.Incidents[0].Minutes != 12 || files.Incidents[0].End == nil {
		t.Fatalf("one finished 12 minute incident: %+v", files.Incidents)
	}
	if got := files.Incidents[0].End.Sub(files.Incidents[0].Start); got != 12*time.Minute {
		t.Errorf("from start to end: %v", got)
	}
	if *files.Uptime >= 100 || *files.Uptime < 80 {
		t.Errorf("uptime: %v", *files.Uptime)
	}
	partial := 0
	for _, h := range files.Hours {
		if h.Status == "partial" {
			partial++
		}
	}
	if partial == 0 {
		t.Errorf("the hour it happened in is partly down: %+v", files.Hours)
	}
	if row(t, r, "database").Incidents == nil || len(row(t, r, "database").Incidents) != 0 {
		t.Errorf("nothing else was down")
	}
}

func TestSomethingDownNowIsAnOngoingIncidentAndDegradesTheWhole(t *testing.T) {
	w := newWorld()
	w.minutes(t, 10)
	w.up["livekit"] = false
	w.minutes(t, 5)

	r := w.svc.Report(context.Background())
	calls := row(t, r, "livekit")
	if r.Overall != "degraded" || calls.Status != "outage" {
		t.Fatalf("overall %s calls %s", r.Overall, calls.Status)
	}
	if len(calls.Incidents) != 1 || calls.Incidents[0].End != nil || calls.Incidents[0].Minutes != 6 {
		t.Errorf("still going, 5 recorded minutes and this one: %+v", calls.Incidents)
	}
}

func TestTheDatabaseDownIsAnOutageOfEverything(t *testing.T) {
	w := newWorld()
	w.up["database"] = false
	if r := w.svc.Report(context.Background()); r.Overall != "outage" {
		t.Errorf("got %s", r.Overall)
	}
}

func TestOneReplicasFailureIsNotHiddenByAnothersPass(t *testing.T) {
	w := newWorld()
	minute := minuteOf(w.now)
	_ = w.store.Record(context.Background(), minute, map[string]bool{"redis": false})
	_ = w.store.Record(context.Background(), minute, map[string]bool{"redis": true})
	if b := w.store.buckets[minute]; b["redis"] {
		t.Errorf("a failure stays a failure: %+v", b)
	}
}

func TestAServiceThatIsNotSetUpIsNotReported(t *testing.T) {
	w := newWorld()
	delete(w.up, "livekit")
	for _, c := range w.svc.Report(context.Background()).Components {
		if c.Key == "livekit" {
			t.Errorf("calls are not set up here, so not shown")
		}
	}
}

func TestWithoutHistoryTheCurrentStateStillShows(t *testing.T) {
	w := newWorld()
	w.store.fail = errors.New("redis: connection refused")
	w.up["minio"] = false
	r := w.svc.Report(context.Background())
	if r.HistoryAvailable || row(t, r, "minio").Status != "outage" || r.Overall != "degraded" {
		t.Errorf("got %+v", r)
	}
}

func TestThePublicReportCarriesNoErrorDetails(t *testing.T) {
	w := newWorld()
	w.up["minio"] = false
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), w.svc)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	var got Report
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), `"message"`) || strings.Contains(rec.Body.String(), "9000") {
		t.Errorf("no error text: %s", rec.Body.String())
	}
}

func TestTheRedisStoreKeepsFailuresAndReadsTheWindowBack(t *testing.T) {
	url := os.Getenv("TEAMUP_TEST_REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379"
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("set TEAMUP_TEST_REDIS_URL to a reachable Redis: %v", err)
	}
	store := NewRedisStore(client)
	store.prefix = "teamup:status-test:" + uuid.NewString() + ":"

	if err := store.Record(ctx, 100, map[string]bool{"redis": false, "minio": true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, 100, map[string]bool{"redis": true, "minio": true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, 102, map[string]bool{"minio": true}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(ctx, 99, 103)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[100]["redis"] || !got[100]["minio"] || !got[102]["minio"] {
		t.Errorf("got %+v", got)
	}
	if ttl := client.TTL(ctx, store.key(100)).Val(); ttl <= Window {
		t.Errorf("kept past the window: %v", ttl)
	}
	client.Del(ctx, store.key(100), store.key(102))
}

func TestAnOutageOnlyAVisitorSawStaysOnRecord(t *testing.T) {
	w := newWorld()
	w.minutes(t, 5)
	// Down for less than a sampling interval, and seen on the page.
	w.up["minio"] = false
	w.svc.Report(context.Background())
	w.up["minio"] = true
	w.now = w.now.Add(time.Minute)

	files := row(t, w.svc.Report(context.Background()), "minio")
	if files.Status != "operational" || len(files.Incidents) != 1 || files.Incidents[0].End == nil {
		t.Errorf("up again, with the outage on record: %+v", files)
	}
}

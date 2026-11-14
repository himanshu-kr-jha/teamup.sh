package system

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// A /proc with known numbers.
func fakeProc(t *testing.T, stat string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"stat":    stat,
		"meminfo": "MemTotal:        4000000 kB\nMemFree:          100000 kB\nMemAvailable:    1000000 kB\n",
		"loadavg": "0.52 0.40 0.31 2/345 6789\n",
		"uptime":  "86400.50 160000.00\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const statA = "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 50 0 50 350 50 0 0 0 0 0\ncpu1 50 0 50 350 50 0 0 0 0 0\nintr 1\n"

// 1000 jiffies later: 300 working, 600 idle or waiting, 100 stolen, so 40% not idle.
const statB = "cpu  250 0 250 1200 200 0 0 100 0 0\ncpu0 1 0 1 1 1 0 0 0 0 0\ncpu1 1 0 1 1 1 0 0 0 0 0\n"

func TestReadsTheMachine(t *testing.T) {
	proc := fakeProc(t, statA)
	a, cores, err := readCPU(proc)
	if err != nil {
		t.Fatal(err)
	}
	if cores != 2 {
		t.Errorf("cores = %d, want 2", cores)
	}
	if err := os.WriteFile(filepath.Join(proc, "stat"), []byte(statB), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _, err := readCPU(proc)
	if err != nil {
		t.Fatal(err)
	}
	busy, steal, ok := cpuBetween(a, b)
	if !ok || busy != 40 || steal != 10 {
		t.Errorf("busy %v steal %v ok %v, want 40, 10, true (stolen time is not idle)", busy, steal, ok)
	}
	if _, _, ok := cpuBetween(b, a); ok {
		t.Error("a reading going backwards must not give a number")
	}

	total, avail, err := readMemory(proc)
	if err != nil || total != 4000000*1024 || avail != 1000000*1024 {
		t.Errorf("memory %d/%d %v: available is MemAvailable, not MemFree", total, avail, err)
	}
	if load, err := readLoad(proc); err != nil || load != 0.52 {
		t.Errorf("load %v %v", load, err)
	}
	if up, err := readUptime(proc); err != nil || up != 86400.5 {
		t.Errorf("uptime %v %v", up, err)
	}
	if total, free, err := readDisk(t.TempDir()); err != nil || total == 0 || free > total {
		t.Errorf("disk %d free %d %v", total, free, err)
	}
}

func TestWithoutProcItSaysSoRatherThanFailing(t *testing.T) {
	s := New(Deps{Proc: filepath.Join(t.TempDir(), "nothing"), Gatherer: prometheus.NewRegistry()})
	s.sample(context.Background())
	r, err := s.Report(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if r.Server.ProcAvailable || r.Server.CPUPercent != nil {
		t.Errorf("no /proc should leave the numbers empty, got %+v", r.Server)
	}
}

// The app's request counters, as observability registers them.
func registry() (*prometheus.Registry, *prometheus.CounterVec, *prometheus.HistogramVec) {
	reg := prometheus.NewRegistry()
	req := prometheus.NewCounterVec(prometheus.CounterOpts{Name: requestsMetric}, []string{"method", "path", "status"})
	dur := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: durationMetric, Buckets: prometheus.DefBuckets}, []string{"method", "path"})
	reg.MustRegister(req, dur)
	return reg, req, dur
}

func TestCountsRequestsErrorsAndHowLongTheyTook(t *testing.T) {
	reg, req, dur := registry()
	before, err := readCounters(reg)
	if err != nil {
		t.Fatal(err)
	}
	// 100 requests: 90 quick (20 ms), 10 slow (800 ms), 3 of them failed.
	for i := 0; i < 90; i++ {
		req.WithLabelValues("GET", "/api/channels", "200").Inc()
		dur.WithLabelValues("GET", "/api/channels").Observe(0.02)
	}
	for i := 0; i < 10; i++ {
		status := "200"
		if i < 3 {
			status = "502"
		}
		req.WithLabelValues("POST", "/api/files", status).Inc()
		dur.WithLabelValues("POST", "/api/files").Observe(0.8)
	}
	after, err := readCounters(reg)
	if err != nil {
		t.Fatal(err)
	}
	act := activityBetween(before, after)
	if act.Requests != 100 || act.Errors != 3 {
		t.Errorf("requests %d errors %d, want 100 and 3", act.Requests, act.Errors)
	}
	if act.P50ms == nil || *act.P50ms > 25 || *act.P50ms < 10 {
		t.Errorf("p50 = %v ms, want within the 10-25 ms bucket", act.P50ms)
	}
	if act.P95ms == nil || *act.P95ms < 500 || *act.P95ms > 1000 {
		t.Errorf("p95 = %v ms, want within the 500-1000 ms bucket", act.P95ms)
	}

	// The app restarted: the counters start again from nothing.
	reg2, req2, _ := registry()
	req2.WithLabelValues("GET", "/", "200").Inc()
	restarted, _ := readCounters(reg2)
	if got := activityBetween(after, restarted); got.Requests != 1 {
		t.Errorf("after a restart, %d requests, want the 1 since", got.Requests)
	}
	if got := activityBetween(after, after); got.P50ms != nil {
		t.Error("no requests, no answer time")
	}
}

func TestTheLiveNumbersComeFromTheLastFewMinutes(t *testing.T) {
	reg, req, dur := registry()
	clock := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	s := New(Deps{Proc: fakeProc(t, statA), Gatherer: reg})
	s.now = func() time.Time { return clock }
	s.sample(context.Background())
	for i := 0; i < 300; i++ {
		req.WithLabelValues("GET", "/", "200").Inc()
		dur.WithLabelValues("GET", "/").Observe(0.01)
	}
	clock = clock.Add(5 * time.Minute)
	s.sample(context.Background())

	r, err := s.Report(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if r.App.RequestsPerMinute == nil || *r.App.RequestsPerMinute != 60 {
		t.Errorf("requests per minute = %v, want 60", r.App.RequestsPerMinute)
	}
	if r.App.ErrorsLastHour == nil || *r.App.ErrorsLastHour != 0 {
		t.Errorf("errors in the last hour = %v, want 0", r.App.ErrorsLastHour)
	}
}

func f(v float64) *float64 { return &v }

func TestTheHistoryKeepsTheSpike(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	minutes := []point{
		{At: t0, CPU: f(10), Requests: 5, InCalls: 3},
		{At: t0.Add(time.Minute), CPU: f(95), Requests: 7, InCalls: 40},
		{At: t0.Add(2 * time.Minute), CPU: nil, Requests: 1, InCalls: 2},
		{At: t0.Add(5 * time.Minute), CPU: f(20), Requests: 2},
	}
	out := downsample(minutes, 5*time.Minute)
	if len(out) != 2 {
		t.Fatalf("got %d steps, want 2", len(out))
	}
	if *out[0].CPU != 95 || out[0].InCalls != 40 || out[0].Requests != 13 || out[0].Minutes != 3 {
		t.Errorf("first step %+v: want the busiest CPU and call, and the requests added up", out[0])
	}
	if !out[1].At.Equal(t0.Add(5 * time.Minute)) {
		t.Errorf("second step at %v", out[1].At)
	}
}

func TestWarnsNearALimit(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	yes, no := true, false
	db := int64(1)
	healthy := &Report{
		Server:  ServerNow{DiskTotal: 100, DiskFree: 50, MemoryTotal: 100, MemoryAvailable: 50, CPUPercent: f(20), StealPercent: f(0)},
		Calls:   CallsNow{Busiest: &Call{Title: "Stand-up", People: 12}},
		Storage: StorageNow{DatabaseBytes: &db, FilesReachable: true},
		Backup:  &BackupLast{FinishedAt: now.Add(-10 * time.Hour), Uploaded: &yes},
	}
	if w := warnings(healthy, now); len(w) != 0 {
		t.Errorf("a healthy server warned: %+v", w)
	}

	strained := *healthy
	strained.Server = ServerNow{DiskTotal: 100, DiskFree: 5, MemoryTotal: 100, MemoryAvailable: 12, CPUPercent: f(85), StealPercent: f(30)}
	strained.Calls = CallsNow{Busiest: &Call{Title: "All hands", People: 58}}
	strained.Storage = StorageNow{DatabaseBytes: &db, FilesReachable: false}
	strained.Backup = &BackupLast{FinishedAt: now.Add(-3 * 24 * time.Hour), Uploaded: &no}
	levels := map[string]int{}
	for _, w := range warnings(&strained, now) {
		levels[w.Level]++
	}
	// Disk 95%, files unreachable, the meeting of 58, a backup 3 days old: alarms.
	// Memory 88%, CPU 85%, steal 30%: warnings.
	if levels["alarm"] != 4 || levels["warn"] != 3 {
		t.Errorf("got %v, want 4 alarms and 3 warnings", levels)
	}

	notUploaded := *healthy
	notUploaded.Backup = &BackupLast{FinishedAt: now.Add(-time.Hour), Uploaded: &no}
	if w := warnings(&notUploaded, now); len(w) != 1 || w[0].Level != "alarm" {
		t.Errorf("a backup that did not reach the bucket: %+v", w)
	}
	never := *healthy
	never.Backup = nil
	if w := warnings(&never, now); len(w) != 1 || w[0].Level != "warn" {
		t.Errorf("no backup yet: %+v", w)
	}
}

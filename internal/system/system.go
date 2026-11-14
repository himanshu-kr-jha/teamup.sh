// Package system answers "how loaded is the server, and is anything close to
// a limit?" for the System tab an administrator sees.
//
// It reads what is already there: the machine from /proc, the app's own
// request counters from the metrics registry, people and calls from Redis and
// the database, and the backups' own record of themselves. Every 15 seconds it
// takes a reading; once a minute it keeps one in Redis for a day, so the page
// can draw the last 24 hours. Nothing here is public: the status page says
// whether TeamUp works, and this says how hard it is working, which is nobody
// else's business.
package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// Thresholds for the warnings. A meeting of 50 is what one 2-CPU server was
// measured to carry cleanly (the load test of 13 November 2026); 60 is where
// video began to suffer.
const (
	diskWarn, diskAlarm       = 80.0, 90.0
	memoryWarn, memoryAlarm   = 85.0, 95.0
	cpuWarn, cpuAlarm         = 80.0, 95.0
	stealWarn                 = 10.0
	meetingWarn, meetingAlarm = 40, 55
	backupLate                = 26 * time.Hour
	historyWindow             = 24 * time.Hour
	historyStep               = 5 * time.Minute
	keyPrefix                 = "teamup:system:"
)

// Deps are where the numbers come from, so tests can supply their own.
type Deps struct {
	DB       *pgxpool.Pool
	Redis    *redis.Client
	Gatherer prometheus.Gatherer
	// Connections counts the sockets open to this process. Without it the
	// metrics registry's gauge is read.
	Connections func() int
	// Online counts the people of an organization shown as online now.
	Online func(orgID string) (int, error)
	// Files says whether file storage is reachable, and whether it is an outside
	// bucket rather than storage on this server.
	Files func(ctx context.Context) (reachable, outside bool)
	// Proc is where /proc is, and Disk a path on the disk to measure.
	Proc, Disk string
	Logger     zerolog.Logger
}

// Service takes readings and builds the page.
type Service struct {
	d   Deps
	now func() time.Time

	mu         sync.Mutex
	lastCPU    *cpuTimes
	cpuBusy    *float64
	cpuSteal   *float64
	counters   []stamped // the last hour of counter readings, oldest first
	minute     int64
	minuteCPU  []float64
	minuteStl  []float64
	minuteFrom *appCounters
}

type stamped struct {
	at time.Time
	c  appCounters
}

// New builds a Service. Proc and Disk default to /proc and /.
func New(d Deps) *Service {
	if d.Proc == "" {
		d.Proc = "/proc"
	}
	if d.Disk == "" {
		d.Disk = "/"
	}
	if d.Gatherer == nil {
		d.Gatherer = prometheus.DefaultGatherer
	}
	return &Service{d: d, now: time.Now}
}

// Run takes a reading every interval until ctx is done.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		s.sample(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// point is one minute of the history.
type point struct {
	At          time.Time `json:"at"`
	CPU         *float64  `json:"cpu"`
	Steal       *float64  `json:"steal"`
	Memory      *float64  `json:"memory"`
	Disk        *float64  `json:"disk"`
	Connections float64   `json:"connections"`
	InCalls     int       `json:"inCalls"`
	Requests    uint64    `json:"requests"`
	Errors      uint64    `json:"errors"`
	P95ms       *float64  `json:"p95ms"`
	// Minutes is how many recorded minutes a step holds, so a step still under
	// way is averaged over what it has.
	Minutes int `json:"minutes"`
}

func (s *Service) sample(ctx context.Context) {
	now := s.now()
	cpu, _, cpuErr := readCPU(s.d.Proc)
	counters, cErr := readCounters(s.d.Gatherer)

	s.mu.Lock()
	if cpuErr == nil {
		if s.lastCPU != nil {
			if busy, steal, ok := cpuBetween(*s.lastCPU, cpu); ok {
				s.cpuBusy, s.cpuSteal = &busy, &steal
				s.minuteCPU = append(s.minuteCPU, busy)
				s.minuteStl = append(s.minuteStl, steal)
			}
		}
		s.lastCPU = &cpu
	}
	if cErr == nil {
		s.counters = append(s.counters, stamped{now, counters})
		for len(s.counters) > 1 && now.Sub(s.counters[0].at) > time.Hour+time.Minute {
			s.counters = s.counters[1:]
		}
	}

	// A new minute: the one before is finished and goes into the history.
	m := now.Unix() / 60
	var finished *point
	if s.minute != 0 && m != s.minute && cErr == nil {
		p := point{At: time.Unix(s.minute*60, 0).UTC(), CPU: mean(s.minuteCPU), Steal: mean(s.minuteStl)}
		if s.minuteFrom != nil {
			act := activityBetween(*s.minuteFrom, counters)
			p.Requests, p.Errors, p.P95ms = act.Requests, act.Errors, act.P95ms
		}
		p.Connections = s.connectionsFrom(counters)
		finished = &p
	}
	if m != s.minute {
		s.minute = m
		s.minuteCPU, s.minuteStl = nil, nil
		if cErr == nil {
			c := counters
			s.minuteFrom = &c
		}
	}
	s.mu.Unlock()

	if finished == nil || s.d.Redis == nil {
		return
	}
	if total, avail, err := readMemory(s.d.Proc); err == nil {
		v := pct(total-avail, total)
		finished.Memory = &v
	}
	if total, free, err := readDisk(s.d.Disk); err == nil {
		v := pct(total-free, total)
		finished.Disk = &v
	}
	if s.d.DB != nil {
		_ = s.d.DB.QueryRow(ctx, inCallsAllSQL).Scan(&finished.InCalls)
	}
	b, _ := json.Marshal(finished)
	key := keyPrefix + strconv.FormatInt(finished.At.Unix()/60, 10)
	if err := s.d.Redis.Set(ctx, key, b, historyWindow+2*time.Hour).Err(); err != nil && ctx.Err() == nil {
		s.d.Logger.Warn().Err(err).Msg("system: could not keep a reading")
	}
}

func (s *Service) connectionsFrom(c appCounters) float64 {
	if s.d.Connections != nil {
		return float64(s.d.Connections())
	}
	return c.sockets
}

func (s *Service) connections() float64 {
	if s.d.Connections != nil {
		return float64(s.d.Connections())
	}
	if c, err := readCounters(s.d.Gatherer); err == nil {
		return c.sockets
	}
	return 0
}

func mean(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	m := sum / float64(len(v))
	return &m
}

func pct(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return clampPct(100 * float64(part) / float64(whole))
}

// People in calls: a session that has started and not ended, in a call that
// is still going. The call's status keeps out a session whose leaving was never
// reported, which would otherwise count somebody in a call for ever.
const inCallsAllSQL = `
	SELECT COUNT(DISTINCT (s.call_id, s.user_id)) FROM call_sessions s
	JOIN calls c ON c.id = s.call_id AND c.status = 'active'
	WHERE s.joined_at IS NOT NULL AND s.left_at IS NULL`

// Report is the page.
type Report struct {
	CheckedAt time.Time   `json:"checkedAt"`
	Server    ServerNow   `json:"server"`
	People    PeopleNow   `json:"people"`
	Calls     CallsNow    `json:"calls"`
	App       AppNow      `json:"app"`
	Storage   StorageNow  `json:"storage"`
	Backup    *BackupLast `json:"backup"`
	Warnings  []Warning   `json:"warnings"`
}

// ServerNow is the machine. A nil number could not be read here.
type ServerNow struct {
	CPUPercent       *float64 `json:"cpuPercent"`
	StealPercent     *float64 `json:"stealPercent"`
	Cores            int      `json:"cores"`
	Load1            *float64 `json:"load1"`
	MemoryTotal      uint64   `json:"memoryTotal"`
	MemoryAvailable  uint64   `json:"memoryAvailable"`
	DiskTotal        uint64   `json:"diskTotal"`
	DiskFree         uint64   `json:"diskFree"`
	UptimeSeconds    *float64 `json:"uptimeSeconds"`
	ProcAvailable    bool     `json:"procAvailable"`
	ConnectionsTotal float64  `json:"connections"`
}

// PeopleNow is the organization's people.
type PeopleNow struct {
	Members     int `json:"members"`
	Online      int `json:"online"`
	ActiveToday int `json:"activeLastDay"`
}

// CallsNow is the organization's calls in progress.
type CallsNow struct {
	Active         int    `json:"active"`
	PeopleInCalls  int    `json:"peopleInCalls"`
	Busiest        *Call  `json:"busiest"`
	ComfortableMax int    `json:"comfortableMax"`
	Note           string `json:"note"`
}

// Call is one call in progress.
type Call struct {
	Title  string `json:"title"`
	People int    `json:"people"`
}

// AppNow is the app's own work: the last five minutes, and errors in the last hour.
type AppNow struct {
	RequestsPerMinute *float64 `json:"requestsPerMinute"`
	P50ms             *float64 `json:"p50ms"`
	P95ms             *float64 `json:"p95ms"`
	ErrorsLastHour    *uint64  `json:"errorsLastHour"`
	MeasuredMinutes   float64  `json:"measuredMinutes"`
}

// StorageNow is where things are kept.
type StorageNow struct {
	DatabaseBytes  *int64 `json:"databaseBytes"`
	FilesReachable bool   `json:"filesReachable"`
	FilesOutside   bool   `json:"filesOutside"`
}

// BackupLast is the newest nightly backup, as the backup recorded it.
type BackupLast struct {
	FinishedAt time.Time `json:"finishedAt"`
	SizeBytes  int64     `json:"sizeBytes"`
	// Uploaded is whether it reached the files bucket; nil when there is no
	// bucket and the backup stays on the server.
	Uploaded *bool  `json:"uploaded"`
	Problem  string `json:"problem,omitempty"`
}

// Warning is something close to, or past, a limit. Level is "warn" or "alarm".
type Warning struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// Report builds the page for one organization. The server's numbers are the
// whole server's; people, calls and the database are the organization's.
func (s *Service) Report(ctx context.Context, orgID uuid.UUID) (*Report, error) {
	r := &Report{CheckedAt: s.now().UTC(), Calls: CallsNow{ComfortableMax: 50,
		Note: "A server with 2 processors carries a meeting of about 50 with every camera on; above 60, video suffers."}}

	s.mu.Lock()
	r.Server.CPUPercent, r.Server.StealPercent = s.cpuBusy, s.cpuSteal
	r.App = s.appNow()
	s.mu.Unlock()

	if _, cores, err := readCPU(s.d.Proc); err == nil {
		r.Server.Cores = cores
		r.Server.ProcAvailable = true
	}
	if v, err := readLoad(s.d.Proc); err == nil {
		r.Server.Load1 = &v
	}
	if t, a, err := readMemory(s.d.Proc); err == nil {
		r.Server.MemoryTotal, r.Server.MemoryAvailable = t, a
	}
	if t, f, err := readDisk(s.d.Disk); err == nil {
		r.Server.DiskTotal, r.Server.DiskFree = t, f
	}
	if v, err := readUptime(s.d.Proc); err == nil {
		r.Server.UptimeSeconds = &v
	}
	r.Server.ConnectionsTotal = s.connections()

	if s.d.Online != nil {
		if n, err := s.d.Online(orgID.String()); err == nil {
			r.People.Online = n
		}
	}
	if s.d.Files != nil {
		r.Storage.FilesReachable, r.Storage.FilesOutside = s.d.Files(ctx)
	}
	if s.d.DB != nil {
		if err := s.fromDatabase(ctx, orgID, r); err != nil {
			return nil, err
		}
	}
	r.Warnings = warnings(r, s.now())
	return r, nil
}

// appNow is called with s.mu held.
func (s *Service) appNow() AppNow {
	var out AppNow
	if len(s.counters) < 2 {
		return out
	}
	last := s.counters[len(s.counters)-1]
	// Five minutes back, or as far as there is.
	from := s.counters[0]
	for _, c := range s.counters {
		if last.at.Sub(c.at) <= 5*time.Minute {
			from = c
			break
		}
	}
	mins := last.at.Sub(from.at).Minutes()
	if mins > 0 {
		act := activityBetween(from.c, last.c)
		rpm := float64(act.Requests) / mins
		out.RequestsPerMinute = &rpm
		out.P50ms, out.P95ms = act.P50ms, act.P95ms
		out.MeasuredMinutes = mins
	}
	hour := activityBetween(s.counters[0].c, last.c)
	out.ErrorsLastHour = &hour.Errors
	return out
}

func (s *Service) fromDatabase(ctx context.Context, orgID uuid.UUID, r *Report) error {
	var dbBytes int64
	err := s.d.DB.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(*) FROM users WHERE org_id = $1 AND deleted_at IS NULL AND COALESCE(is_active, true)),
		  (SELECT COUNT(DISTINCT t.user_id) FROM refresh_tokens t JOIN users u ON u.id = t.user_id
		    WHERE u.org_id = $1 AND t.created_at > NOW() - INTERVAL '24 hours'),
		  (SELECT COUNT(*) FROM calls WHERE org_id = $1 AND status = 'active'),
		  pg_database_size(current_database())
	`, orgID).Scan(&r.People.Members, &r.People.ActiveToday, &r.Calls.Active, &dbBytes)
	if err != nil {
		return fmt.Errorf("read people and calls: %w", err)
	}
	r.Storage.DatabaseBytes = &dbBytes

	rows, err := s.d.DB.Query(ctx, `
		SELECT COALESCE(NULLIF(c.title, ''), 'A call'), COUNT(DISTINCT s.user_id)
		FROM call_sessions s
		JOIN calls c ON c.id = s.call_id AND c.status = 'active' AND c.org_id = $1
		WHERE s.joined_at IS NOT NULL AND s.left_at IS NULL
		GROUP BY c.id, c.title
		ORDER BY 2 DESC
	`, orgID)
	if err != nil {
		return fmt.Errorf("read calls in progress: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c Call
		if err := rows.Scan(&c.Title, &c.People); err != nil {
			return fmt.Errorf("scan call: %w", err)
		}
		r.Calls.PeopleInCalls += c.People
		if r.Calls.Busiest == nil {
			busiest := c
			r.Calls.Busiest = &busiest
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var b BackupLast
	err = s.d.DB.QueryRow(ctx, `
		SELECT finished_at, size_bytes, uploaded, COALESCE(problem, '')
		FROM backup_runs ORDER BY finished_at DESC LIMIT 1
	`).Scan(&b.FinishedAt, &b.SizeBytes, &b.Uploaded, &b.Problem)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("read the last backup: %w", err)
	default:
		r.Backup = &b
	}
	return nil
}

func warnings(r *Report, now time.Time) []Warning {
	out := []Warning{}
	add := func(level, format string, args ...any) {
		out = append(out, Warning{Level: level, Text: fmt.Sprintf(format, args...)})
	}
	level := func(v, warn, alarm float64) string {
		switch {
		case v >= alarm:
			return "alarm"
		case v >= warn:
			return "warn"
		}
		return ""
	}
	if r.Server.DiskTotal > 0 {
		used := pct(r.Server.DiskTotal-r.Server.DiskFree, r.Server.DiskTotal)
		if l := level(used, diskWarn, diskAlarm); l != "" {
			add(l, "The disk is %.0f%% full. When it fills, nothing new can be saved.", used)
		}
	}
	if r.Server.MemoryTotal > 0 {
		used := pct(r.Server.MemoryTotal-r.Server.MemoryAvailable, r.Server.MemoryTotal)
		if l := level(used, memoryWarn, memoryAlarm); l != "" {
			add(l, "Memory is %.0f%% used. When it runs out, the call server is the first thing stopped.", used)
		}
	}
	if r.Server.CPUPercent != nil {
		if l := level(*r.Server.CPUPercent, cpuWarn, cpuAlarm); l != "" {
			add(l, "The processors are %.0f%% busy. Pages and calls slow down near 100%%.", *r.Server.CPUPercent)
		}
	}
	if r.Server.StealPercent != nil && *r.Server.StealPercent >= stealWarn {
		add("warn", "The cloud provider is holding back %.0f%% of the processors' time: a burstable server out of credit is slowed down.", *r.Server.StealPercent)
	}
	if b := r.Calls.Busiest; b != nil {
		switch {
		case b.People >= meetingAlarm:
			add("alarm", "%q has %d people. Above about 60, video starts to suffer on a server this size.", b.Title, b.People)
		case b.People >= meetingWarn:
			add("warn", "%q has %d people, close to the 50 a server this size carries comfortably.", b.Title, b.People)
		}
	}
	if r.Storage.DatabaseBytes != nil && !r.Storage.FilesReachable {
		add("alarm", "File storage cannot be reached: uploads and downloads fail.")
	}
	switch {
	case r.Backup == nil:
		add("warn", "No backup has been recorded yet. One should run every night.")
	case now.Sub(r.Backup.FinishedAt) > backupLate:
		add("alarm", "The last backup was %s ago. One should run every night.", roughly(now.Sub(r.Backup.FinishedAt)))
	case r.Backup.Uploaded != nil && !*r.Backup.Uploaded:
		add("alarm", "The last backup did not reach the files bucket, so it is only on this server.")
	}
	return out
}

func roughly(d time.Duration) string {
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}

// History is the last 24 hours in five-minute steps, oldest first. A step with
// nothing recorded is left out, and the page draws a gap.
func (s *Service) History(ctx context.Context) ([]point, error) {
	if s.d.Redis == nil {
		return []point{}, nil
	}
	now := s.now()
	first := now.Add(-historyWindow).Unix() / 60
	last := now.Unix() / 60
	keys := make([]string, 0, last-first+1)
	for m := first; m <= last; m++ {
		keys = append(keys, keyPrefix+strconv.FormatInt(m, 10))
	}
	vals, err := s.d.Redis.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	var minutes []point
	for _, v := range vals {
		str, ok := v.(string)
		if !ok {
			continue
		}
		var p point
		if json.Unmarshal([]byte(str), &p) == nil {
			minutes = append(minutes, p)
		}
	}
	return downsample(minutes, historyStep), nil
}

// downsample folds minutes into steps: the busiest minute's CPU, memory, disk,
// connections, people in calls and slowest answers, because a chart of averages
// hides the spike somebody is looking for, and the sum of requests and errors.
func downsample(minutes []point, step time.Duration) []point {
	out := []point{}
	for _, m := range minutes {
		at := m.At.Truncate(step)
		if len(out) == 0 || !out[len(out)-1].At.Equal(at) {
			m.At = at
			m.Minutes = 1
			out = append(out, m)
			continue
		}
		p := &out[len(out)-1]
		p.CPU, p.Steal = maxp(p.CPU, m.CPU), maxp(p.Steal, m.Steal)
		p.Memory, p.Disk, p.P95ms = maxp(p.Memory, m.Memory), maxp(p.Disk, m.Disk), maxp(p.P95ms, m.P95ms)
		if m.Connections > p.Connections {
			p.Connections = m.Connections
		}
		if m.InCalls > p.InCalls {
			p.InCalls = m.InCalls
		}
		p.Minutes++
		p.Requests += m.Requests
		p.Errors += m.Errors
	}
	return out
}

func maxp(a, b *float64) *float64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case *b > *a:
		return b
	}
	return a
}

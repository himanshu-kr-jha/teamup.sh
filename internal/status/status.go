// Package status answers "is something broken right now, and was it broken
// earlier?" for the public status page.
//
// Every replica checks the services TeamUp depends on once or twice a minute
// and writes what it saw into one bucket per minute. A failure written by any
// replica wins over a pass written by another, so a minute counts as an outage
// if anybody could not reach the service in it. A minute nobody wrote at all
// means no instance was running to look, which the page shows as such rather
// than pretending it was fine.
//
// What a check failed with is never part of the report. The page is public, and
// an error message carries host names and ports that are nobody's business.
package status

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

// Component is one thing the page reports on, under a name a member would use.
type Component struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Components are reported in this order. "app" is this process: it is up
// whenever there is anybody to answer.
var Components = []Component{
	{Key: "app", Name: "TeamUp", Description: "Opening the app and signing in"},
	{Key: "database", Name: "Messages and data", Description: "Chat, tasks, calendars and everything saved"},
	{Key: "redis", Name: "Live updates", Description: "New messages arriving without a refresh, and who is online"},
	{Key: "minio", Name: "File sharing", Description: "Sending, opening and downloading files"},
	{Key: "livekit", Name: "Calls", Description: "Voice and video calls and meetings"},
}

// Window is how far back the history goes.
const Window = 24 * time.Hour

// Prober reports each dependency as reachable or not, keyed like Components.
// A key it leaves out is not set up on this installation and is not reported.
type Prober func(ctx context.Context) map[string]bool

// Store keeps what was seen, one bucket per minute.
type Store interface {
	// Record merges results into the minute's bucket. A failure already there
	// stays a failure.
	Record(ctx context.Context, minute int64, results map[string]bool) error
	// Load returns the buckets from first to last minute inclusive. A minute
	// with no bucket is absent from the map.
	Load(ctx context.Context, first, last int64) (map[int64]map[string]bool, error)
}

// Service checks, records and reports.
type Service struct {
	probe  Prober
	store  Store
	logger zerolog.Logger
	now    func() time.Time

	// The report is public and costs a day of buckets to build, so it is
	// reused for a few seconds rather than rebuilt for every visitor.
	mu       sync.Mutex
	cached   *Report
	cachedAt time.Time
	cacheTTL time.Duration
}

// NewService builds a Service. store may be nil, in which case only the
// current state is reported.
func NewService(probe Prober, store Store, logger zerolog.Logger) *Service {
	return &Service{probe: probe, store: store, logger: logger, now: time.Now, cacheTTL: 10 * time.Second}
}

func minuteOf(t time.Time) int64 { return t.Unix() / 60 }

// check probes once, with "app" always up because something is answering.
func (s *Service) check(ctx context.Context) map[string]bool {
	results := s.probe(ctx)
	out := make(map[string]bool, len(results)+1)
	for k, v := range results {
		out[k] = v
	}
	out["app"] = true
	return out
}

// RecordOnce checks and writes the result into the current minute.
func (s *Service) RecordOnce(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	return s.store.Record(ctx, minuteOf(s.now()), s.check(ctx))
}

// Run records every interval until ctx is done. Twice a minute means a minute
// is not left empty by one slow check.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if err := s.RecordOnce(ctx); err != nil && ctx.Err() == nil {
			s.logger.Warn().Err(err).Msg("status: could not record a check")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Report is what the page shows.
type Report struct {
	// Overall is "operational", "degraded" (something is not working) or
	// "outage" (nothing that matters works).
	Overall          string            `json:"overall"`
	CheckedAt        time.Time         `json:"checkedAt"`
	HistoryAvailable bool              `json:"historyAvailable"`
	Components       []ComponentReport `json:"components"`
}

// ComponentReport is one row on the page.
type ComponentReport struct {
	Component
	// Status is "operational" or "outage".
	Status string `json:"status"`
	// Uptime is the share of recorded minutes it was up, as a percentage, or
	// absent when nothing was recorded.
	Uptime    *float64   `json:"uptime,omitempty"`
	Hours     []Hour     `json:"hours"`
	Incidents []Incident `json:"incidents"`
}

// Hour is one bar on the page, oldest first.
type Hour struct {
	Start time.Time `json:"start"`
	// Status is "up", "partial" (down for some of it), "down" (for all of the
	// recorded part) or "none" (nothing recorded).
	Status      string `json:"status"`
	DownMinutes int    `json:"downMinutes"`
}

// Incident is a run of minutes something was down.
type Incident struct {
	Start time.Time `json:"start"`
	// End is absent while it is still going on.
	End     *time.Time `json:"end,omitempty"`
	Minutes int        `json:"minutes"`
}

// Report builds the page, reusing a recent one.
func (s *Service) Report(ctx context.Context) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && s.now().Sub(s.cachedAt) < s.cacheTTL {
		return *s.cached
	}
	r := s.build(ctx)
	s.cached, s.cachedAt = &r, s.now()
	return r
}

func (s *Service) build(ctx context.Context) Report {
	now := s.now()
	current := s.check(ctx)
	// What a visitor saw is on the record too. Sampling alone misses an outage
	// shorter than the time between samples, and the moment somebody saw one
	// is exactly the one that must not vanish from the history.
	if s.store != nil {
		if err := s.store.Record(ctx, minuteOf(now), current); err != nil {
			s.logger.Debug().Err(err).Msg("status: could not record what a visitor saw")
		}
	}

	// Bars line up with the clock: the last one is the hour now under way.
	last := minuteOf(now)
	first := minuteOf(now.Truncate(time.Hour).Add(-Window + time.Hour))
	var buckets map[int64]map[string]bool
	historyAvailable := false
	if s.store != nil {
		loaded, err := s.store.Load(ctx, first, last)
		if err != nil {
			s.logger.Warn().Err(err).Msg("status: could not read the history")
		} else {
			buckets, historyAvailable = loaded, true
		}
	}

	report := Report{Overall: "operational", CheckedAt: now.UTC(), HistoryAvailable: historyAvailable}
	for _, c := range Components {
		up, known := current[c.Key]
		if !known {
			continue
		}
		row := ComponentReport{Component: c, Status: "operational", Hours: []Hour{}, Incidents: []Incident{}}
		if !up {
			row.Status = "outage"
			report.Overall = "degraded"
			if c.Key == "database" {
				report.Overall = "outage"
			}
		}
		if historyAvailable {
			row.Hours, row.Incidents, row.Uptime = history(c.Key, buckets, first, last, up)
		}
		report.Components = append(report.Components, row)
	}
	return report
}

// history folds the minutes into hourly bars, incidents newest first, and
// uptime. The minute being reported is taken from the current check, so an
// outage shows the moment it starts rather than at the next recording.
func history(key string, buckets map[int64]map[string]bool, first, last int64, upNow bool) ([]Hour, []Incident, *float64) {
	hours := make([]Hour, 0, 24)
	var incidents []Incident
	var open *Incident
	recorded, upMinutes := 0, 0

	closeOpen := func(at int64) {
		if open != nil {
			end := time.Unix(at*60, 0).UTC()
			open.End = &end
			incidents = append(incidents, *open)
			open = nil
		}
	}

	var bar *Hour
	recordedInBar := 0
	for m := first; m <= last; m++ {
		if (m-first)%60 == 0 {
			if bar != nil {
				hours = append(hours, finish(*bar, recordedInBar))
			}
			bar = &Hour{Start: time.Unix(m*60, 0).UTC()}
			recordedInBar = 0
		}

		up, seen := false, false
		if b, ok := buckets[m]; ok {
			up, seen = b[key]
		}
		if m == last {
			up, seen = upNow, true
		}
		if !seen {
			// Nothing to go on: an incident does not carry across a gap.
			closeOpen(m)
			continue
		}
		recorded++
		recordedInBar++
		if up {
			upMinutes++
			closeOpen(m)
			continue
		}
		bar.DownMinutes++
		if open == nil {
			open = &Incident{Start: time.Unix(m*60, 0).UTC()}
		}
		open.Minutes++
	}
	hours = append(hours, finish(*bar, recordedInBar))
	if open != nil {
		// Still down as of now.
		incidents = append(incidents, *open)
	}

	// Newest first.
	for i, j := 0, len(incidents)-1; i < j; i, j = i+1, j-1 {
		incidents[i], incidents[j] = incidents[j], incidents[i]
	}
	if incidents == nil {
		incidents = []Incident{}
	}
	var uptime *float64
	if recorded > 0 {
		u := float64(upMinutes) * 100 / float64(recorded)
		uptime = &u
	}
	return hours, incidents, uptime
}

func finish(h Hour, recorded int) Hour {
	switch {
	case recorded == 0:
		h.Status = "none"
	case h.DownMinutes == 0:
		h.Status = "up"
	case h.DownMinutes >= recorded:
		h.Status = "down"
	default:
		h.Status = "partial"
	}
	return h
}

// RegisterRoutes serves the report, without a session: the page is for when
// signing in is exactly what does not work.
func RegisterRoutes(rg *gin.RouterGroup, s *Service) {
	rg.GET("/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, s.Report(c.Request.Context()))
	})
}

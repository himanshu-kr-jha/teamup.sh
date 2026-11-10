package gcal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ── running ───────────────────────────────────────────────────────────────

// Nudge asks for these people's calendars to be brought into step soon, as
// when a meeting of theirs was scheduled, moved or cancelled. Never blocks:
// anybody it cannot queue is caught by the next regular sweep.
func (s *Service) Nudge(people ...uuid.UUID) {
	if !s.Enabled() {
		return
	}
	for _, p := range people {
		select {
		case s.nudges <- p:
		default:
		}
	}
}

// Run keeps everybody's calendars in step until ctx ends: everybody every
// `every`, and anybody nudged as soon as they are.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	if !s.Enabled() {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	s.SyncEverybody(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.SyncEverybody(ctx)
		case id := <-s.nudges:
			var linked bool
			if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM google_calendar_links WHERE user_id = $1 AND status = 'ok')`, id).Scan(&linked); err == nil && linked {
				_ = s.Sync(ctx, id)
			}
		}
	}
}

// SyncEverybody brings every connected calendar into step, one after another.
func (s *Service) SyncEverybody(ctx context.Context) {
	rows, err := s.db.Query(ctx, `SELECT user_id FROM google_calendar_links WHERE status = 'ok'`)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not list google calendar links")
		return
	}
	var people []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			people = append(people, id)
		}
	}
	rows.Close()
	for _, id := range people {
		if ctx.Err() != nil {
			return
		}
		_ = s.Sync(ctx, id)
	}
}

func (s *Service) lockFor(userID uuid.UUID) *sync.Mutex {
	m, _ := s.userLocks.LoadOrStore(userID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// Sync brings one person's calendars into step both ways and records how it went.
func (s *Service) Sync(ctx context.Context, userID uuid.UUID) error {
	lock := s.lockFor(userID)
	lock.Lock()
	defer lock.Unlock()

	err := s.sync(ctx, userID)
	switch {
	case errors.Is(err, errReconnect):
		_, _ = s.db.Exec(ctx, `UPDATE google_calendar_links SET status = 'reconnect', last_error = $2, updated_at = NOW() WHERE user_id = $1`,
			userID, "Google no longer accepts TeamUp's access. Connect again to carry on.")
	case err != nil:
		s.logger.Warn().Err(err).Str("user", userID.String()).Msg("google calendar sync failed")
		_, _ = s.db.Exec(ctx, `UPDATE google_calendar_links SET last_error = $2, updated_at = NOW() WHERE user_id = $1`,
			userID, "The last attempt to reach Google Calendar failed. It is tried again every few minutes.")
	default:
		_, _ = s.db.Exec(ctx, `UPDATE google_calendar_links SET last_synced_at = $2, last_error = '', updated_at = NOW() WHERE user_id = $1`,
			userID, s.clock())
	}
	return err
}

func (s *Service) sync(ctx context.Context, userID uuid.UUID) error {
	client, _, err := s.client(ctx, userID)
	if err != nil {
		return err
	}
	var orgID uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT org_id FROM google_calendar_links WHERE user_id = $1`, userID).Scan(&orgID); err != nil {
		return err
	}
	if err := s.push(ctx, client, orgID, userID); err != nil {
		return err
	}
	return s.pull(ctx, client, orgID, userID)
}

// ── Google into TeamUp ────────────────────────────────────────────────────

type googleTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

type googleEvent struct {
	ID           string     `json:"id,omitempty"`
	Status       string     `json:"status,omitempty"`
	Summary      string     `json:"summary,omitempty"`
	Description  string     `json:"description,omitempty"`
	Start        googleTime `json:"start"`
	End          googleTime `json:"end"`
	Transparency string     `json:"transparency,omitempty"`
	Visibility   string     `json:"visibility,omitempty"`
	HTMLLink     string     `json:"htmlLink,omitempty"`
	EventType    string     `json:"eventType,omitempty"`
	Attendees    []struct {
		Self           bool   `json:"self"`
		ResponseStatus string `json:"responseStatus"`
	} `json:"attendees,omitempty"`
	ExtendedProperties *struct {
		Private map[string]string `json:"private,omitempty"`
	} `json:"extendedProperties,omitempty"`
	Source *struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"source,omitempty"`
}

// fromTeamUp is a meeting TeamUp put there itself, already on the TeamUp calendar.
func (e googleEvent) fromTeamUp() bool {
	return e.ExtendedProperties != nil && e.ExtendedProperties.Private["teamupCallId"] != ""
}

// busy says whether the event takes up the person's time. A whole-day event
// (a holiday, a birthday) does not block meetings, nor one marked free, nor one
// they declined, nor where they say they are working from.
func (e googleEvent) busy() bool {
	if e.Status == "cancelled" || e.Transparency == "transparent" || e.Start.DateTime == "" || e.End.DateTime == "" {
		return false
	}
	if e.EventType == "workingLocation" {
		return false
	}
	for _, a := range e.Attendees {
		if a.Self && a.ResponseStatus == "declined" {
			return false
		}
	}
	return true
}

// pull replaces what is known of somebody's Google Calendar with what is there now.
func (s *Service) pull(ctx context.Context, client *http.Client, orgID, userID uuid.UUID) error {
	now := s.clock()
	q := url.Values{
		"singleEvents": {"true"},
		"orderBy":      {"startTime"},
		"timeMin":      {now.Add(-lookBack).UTC().Format(time.RFC3339)},
		"timeMax":      {now.Add(lookAhead).UTC().Format(time.RFC3339)},
		"maxResults":   {"250"},
	}
	var events []googleEvent
	for page := 0; page < 40; page++ {
		status, body, err := s.call(ctx, client, http.MethodGet, s.api+"/calendars/primary/events?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized {
			return errReconnect
		}
		if status != http.StatusOK {
			return fmt.Errorf("list google events: %d %s", status, trim(body))
		}
		var res struct {
			Items         []googleEvent `json:"items"`
			NextPageToken string        `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return fmt.Errorf("read google events: %w", err)
		}
		events = append(events, res.Items...)
		if res.NextPageToken == "" {
			break
		}
		q.Set("pageToken", res.NextPageToken)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM google_calendar_events WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear google events: %w", err)
	}
	for _, e := range events {
		if !e.busy() || e.fromTeamUp() {
			continue
		}
		start, err1 := time.Parse(time.RFC3339, e.Start.DateTime)
		end, err2 := time.Parse(time.RFC3339, e.End.DateTime)
		if err1 != nil || err2 != nil || !end.After(start) {
			continue
		}
		title := e.Summary
		if len(title) > 1000 {
			title = title[:1000]
		}
		private := e.Visibility == "private" || e.Visibility == "confidential"
		if _, err := tx.Exec(ctx, `
			INSERT INTO google_calendar_events (user_id, org_id, google_id, title, starts_at, ends_at, private, html_link)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (user_id, google_id) DO NOTHING
		`, userID, orgID, e.ID, title, start, end, private, e.HTMLLink); err != nil {
			return fmt.Errorf("store google event: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ── TeamUp into Google ────────────────────────────────────────────────────

type meeting struct {
	id         uuid.UUID
	title      string
	start, end time.Time
}

func (m meeting) fingerprint() string {
	return m.title + "|" + m.start.UTC().Format(time.RFC3339) + "|" + m.end.UTC().Format(time.RFC3339)
}

func (s *Service) eventURL(id string) string {
	return s.api + "/calendars/primary/events/" + url.PathEscape(id)
}

func (s *Service) eventFor(m meeting) googleEvent {
	join := strings.TrimRight(s.cfg.BaseURL, "/") + "/calls/" + m.id.String()
	e := googleEvent{
		Summary:     m.title,
		Description: "A TeamUp meeting. Join it here: " + join + "\n\nMoved or cancelled in TeamUp, it changes here too.",
		Start:       googleTime{DateTime: m.start.UTC().Format(time.RFC3339)},
		End:         googleTime{DateTime: m.end.UTC().Format(time.RFC3339)},
	}
	e.ExtendedProperties = &struct {
		Private map[string]string `json:"private,omitempty"`
	}{Private: map[string]string{"teamupCallId": m.id.String()}}
	e.Source = &struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}{Title: "TeamUp", URL: join}
	return e
}

// push puts this person's TeamUp meetings into their Google Calendar, changes
// the ones that moved, and takes out the ones cancelled or no longer theirs.
// Standing meetings are not sent: they repeat on TeamUp's own rules.
func (s *Service) push(ctx context.Context, client *http.Client, orgID, userID uuid.UUID) error {
	now := s.clock()
	rows, err := s.db.Query(ctx, `
		SELECT c.id, COALESCE(c.title, ''), c.scheduled_start, c.scheduled_end
		FROM calls c JOIN call_participants p ON p.call_id = c.id AND p.user_id = $2
		WHERE c.org_id = $1 AND c.standing_meeting_id IS NULL AND c.status <> 'cancelled'
		  AND c.scheduled_start IS NOT NULL AND c.scheduled_end IS NOT NULL
		  AND c.scheduled_end > $3 AND c.scheduled_start < $4
	`, orgID, userID, now.Add(-24*time.Hour), now.Add(lookAhead))
	if err != nil {
		return fmt.Errorf("list meetings to send: %w", err)
	}
	wanted := map[uuid.UUID]meeting{}
	for rows.Next() {
		var m meeting
		if err := rows.Scan(&m.id, &m.title, &m.start, &m.end); err != nil {
			rows.Close()
			return err
		}
		if m.title == "" {
			m.title = "Meeting"
		}
		wanted[m.id] = m
	}
	rows.Close()

	type sent struct {
		eventID, fingerprint string
	}
	already := map[uuid.UUID]sent{}
	rows, err = s.db.Query(ctx, `SELECT call_id, google_event_id, fingerprint FROM google_calendar_pushed WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		var x sent
		if err := rows.Scan(&id, &x.eventID, &x.fingerprint); err != nil {
			rows.Close()
			return err
		}
		already[id] = x
	}
	rows.Close()

	for id, m := range wanted {
		x, ok := already[id]
		if ok && x.fingerprint == m.fingerprint() {
			continue
		}
		body, _ := json.Marshal(s.eventFor(m))
		eventID := ""
		if ok {
			status, res, err := s.call(ctx, client, http.MethodPatch, s.eventURL(x.eventID)+"?sendUpdates=none", body)
			if err != nil {
				return err
			}
			switch {
			case status == http.StatusOK:
				eventID = x.eventID
			case status == http.StatusNotFound || status == http.StatusGone:
				// Deleted in Google by hand: put back below, as the meeting is still on.
			default:
				return fmt.Errorf("update google event: %d %s", status, trim(res))
			}
		}
		if eventID == "" {
			status, res, err := s.call(ctx, client, http.MethodPost, s.api+"/calendars/primary/events?sendUpdates=none", body)
			if err != nil {
				return err
			}
			if status != http.StatusOK && status != http.StatusCreated {
				return fmt.Errorf("create google event: %d %s", status, trim(res))
			}
			var created googleEvent
			if err := json.Unmarshal(res, &created); err != nil || created.ID == "" {
				return fmt.Errorf("read created google event: %v", err)
			}
			eventID = created.ID
		}
		if _, err := s.db.Exec(ctx, `
			INSERT INTO google_calendar_pushed (user_id, call_id, google_event_id, fingerprint) VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, call_id) DO UPDATE SET google_event_id = EXCLUDED.google_event_id, fingerprint = EXCLUDED.fingerprint
		`, userID, id, eventID, m.fingerprint()); err != nil {
			return err
		}
	}

	// Anything sent before and not wanted now: taken out if the meeting was
	// cancelled or this person is no longer in it. One that is merely past, or
	// far ahead, stays where it is.
	for id, x := range already {
		if _, ok := wanted[id]; ok {
			continue
		}
		var still bool
		if err := s.db.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM calls c JOIN call_participants p ON p.call_id = c.id AND p.user_id = $2
			               WHERE c.id = $1 AND c.status <> 'cancelled')
		`, id, userID).Scan(&still); err != nil {
			return err
		}
		if still {
			continue
		}
		status, res, err := s.call(ctx, client, http.MethodDelete, s.eventURL(x.eventID)+"?sendUpdates=none", nil)
		if err != nil {
			return err
		}
		if status >= 300 && status != http.StatusNotFound && status != http.StatusGone {
			return fmt.Errorf("remove google event: %d %s", status, trim(res))
		}
		if _, err := s.db.Exec(ctx, `DELETE FROM google_calendar_pushed WHERE user_id = $1 AND call_id = $2`, userID, id); err != nil {
			return err
		}
	}
	return nil
}

// ── talking to Google ─────────────────────────────────────────────────────

func (s *Service) call(ctx context.Context, client *http.Client, method, address string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("reach google calendar: %w", err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	return res.StatusCode, out, err
}

func trim(b []byte) string {
	if len(b) > 300 {
		b = b[:300]
	}
	return string(b)
}

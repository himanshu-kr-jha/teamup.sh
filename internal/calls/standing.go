package calls

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	// Zones are picked by an admin and worked out here, and the server image may
	// not carry the system's zone database.
	_ "time/tzdata"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/httpx"
	"github.com/teamup/teamup/internal/tasks"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

const (
	defaultStandingMinutes = 30
	maxStandingMinutes     = 8 * 60
	// A quarter is as far as one attendance read goes.
	maxAttendanceDays = 92
	// earlyForNextSlot is how early somebody pressing Join counts as being early for
	// a slot just after midnight, rather than late for today's. It is the calendar's
	// JOIN_EARLY_MINUTES.
	earlyForNextSlot = 30 * time.Minute
)

// StandingMeeting is a permanent room for the whole organization.
type StandingMeeting struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	TimeOfDay       string     `json:"timeOfDay"`
	TimeZone        string     `json:"timeZone"`
	Weekdays        []int16    `json:"weekdays"`
	DurationMinutes int        `json:"durationMinutes"`
	CreatedBy       *uuid.UUID `json:"createdBy,omitempty"`
	ArchivedAt      *time.Time `json:"archivedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

const standingColumns = `id, name, time_of_day, time_zone, weekdays, duration_minutes, created_by, archived_at, created_at`

func scanStanding(row pgx.Row) (*StandingMeeting, error) {
	var m StandingMeeting
	err := row.Scan(&m.ID, &m.Name, &m.TimeOfDay, &m.TimeZone, &m.Weekdays, &m.DurationMinutes, &m.CreatedBy, &m.ArchivedAt, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan standing meeting: %w", err)
	}
	return &m, nil
}

// runsOn says whether the meeting is held on this ISO weekday, Monday being 1.
func (m *StandingMeeting) runsOn(day time.Weekday) bool {
	iso := int16(day)
	if iso == 0 {
		iso = 7
	}
	for _, d := range m.Weekdays {
		if d == iso {
			return true
		}
	}
	return false
}

// slotOn is when the meeting starts and ends on a date, in its own zone.
func (m *StandingMeeting) slotOn(date time.Time) (time.Time, time.Time, error) {
	loc, err := time.LoadLocation(m.TimeZone)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("load zone: %w", err)
	}
	clock, err := time.Parse("15:04", m.TimeOfDay)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse time of day: %w", err)
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
	return start, start.Add(time.Duration(m.DurationMinutes) * time.Minute), nil
}

// occurrenceDay is which day of the meeting somebody means at this moment, as a
// time on that date in the meeting's zone. It is the day of the slot, and not simply
// today, because a slot can cross midnight and be one meeting on both sides of it:
// yesterday's while it is still running, tomorrow's when it is about to start, and
// otherwise today's.
func (m *StandingMeeting) occurrenceDay(now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(m.TimeZone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load zone: %w", err)
	}
	today := now.In(loc)
	yesterday, tomorrow := today.AddDate(0, 0, -1), today.AddDate(0, 0, 1)
	if m.runsOn(yesterday.Weekday()) {
		_, end, err := m.slotOn(yesterday)
		if err != nil {
			return time.Time{}, err
		}
		if now.Before(end) {
			return yesterday, nil
		}
	}
	if m.runsOn(tomorrow.Weekday()) {
		start, _, err := m.slotOn(tomorrow)
		if err != nil {
			return time.Time{}, err
		}
		if !start.After(now.Add(earlyForNextSlot)) {
			return tomorrow, nil
		}
	}
	return today, nil
}

// scheduleVersion is the meeting's schedule from one moment until the next.
type scheduleVersion struct {
	StandingMeeting // only its schedule is filled in
	effectiveAt     time.Time
	effectiveDate   string // that moment's date, in the zone of this schedule
}

// scheduleVersions is every schedule the meeting has had, oldest first. The first
// holds from when the meeting was created.
func (s *Service) scheduleVersions(ctx context.Context, m *StandingMeeting) ([]scheduleVersion, error) {
	rows, err := s.db.Query(ctx, `
		SELECT effective_at, time_of_day, time_zone, weekdays, duration_minutes
		FROM standing_meeting_schedules WHERE standing_meeting_id = $1
		ORDER BY effective_at, id
	`, m.ID)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	versions := []scheduleVersion{}
	for rows.Next() {
		var v scheduleVersion
		if err := rows.Scan(&v.effectiveAt, &v.TimeOfDay, &v.TimeZone, &v.Weekdays, &v.DurationMinutes); err != nil {
			return nil, fmt.Errorf("scan schedule: %w", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		versions = append(versions, scheduleVersion{StandingMeeting: *m})
	}
	versions[0].effectiveAt = m.CreatedAt
	for i := range versions {
		loc, err := time.LoadLocation(versions[i].TimeZone)
		if err != nil {
			return nil, fmt.Errorf("load zone: %w", err)
		}
		versions[i].effectiveDate = versions[i].effectiveAt.In(loc).Format("2006-01-02")
	}
	return versions, nil
}

// scheduleOn is the schedule in force on a date, if the meeting existed by then.
func scheduleOn(versions []scheduleVersion, date string) (*scheduleVersion, bool) {
	var found *scheduleVersion
	for i := range versions {
		if versions[i].effectiveDate <= date {
			found = &versions[i]
		}
	}
	return found, found != nil
}

// standingFields is what an admin may set. Each field is optional on an edit.
type standingFields struct {
	Name            *string `json:"name"`
	TimeOfDay       *string `json:"timeOfDay"`
	TimeZone        *string `json:"timeZone"`
	Weekdays        *[]int  `json:"weekdays"`
	DurationMinutes *int    `json:"durationMinutes"`
}

func badRequest(message string) error { return apperrors.New(apperrors.CodeBadRequest, message) }

// applyTo validates what was sent and writes it onto m.
func (f standingFields) applyTo(m *StandingMeeting) error {
	if f.Name != nil {
		m.Name = strings.TrimSpace(*f.Name)
	}
	if m.Name == "" || len(m.Name) > 255 {
		return badRequest("a standing meeting needs a name of at most 255 characters")
	}
	if f.TimeOfDay != nil {
		clock, err := time.Parse("15:04", *f.TimeOfDay)
		if err != nil {
			return badRequest("timeOfDay must be HH:MM")
		}
		m.TimeOfDay = clock.Format("15:04")
	}
	if m.TimeOfDay == "" {
		return badRequest("timeOfDay must be HH:MM")
	}
	if f.TimeZone != nil {
		m.TimeZone = *f.TimeZone
	}
	// LoadLocation reads "" as UTC and "Local" as wherever the server is, which is
	// the mistake a zone on the meeting exists to prevent.
	if m.TimeZone == "" || m.TimeZone == "Local" {
		return badRequest("timeZone must be an IANA zone")
	}
	if _, err := time.LoadLocation(m.TimeZone); err != nil {
		return badRequest("timeZone must be an IANA zone")
	}
	if f.Weekdays != nil {
		seen := map[int]bool{}
		days := []int16{}
		for _, d := range *f.Weekdays {
			if d < 1 || d > 7 {
				return badRequest("weekdays run from 1 for Monday to 7 for Sunday")
			}
			if !seen[d] {
				seen[d] = true
				days = append(days, int16(d))
			}
		}
		sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
		m.Weekdays = days
	}
	if len(m.Weekdays) == 0 {
		return badRequest("a standing meeting runs on at least one weekday")
	}
	if f.DurationMinutes != nil {
		m.DurationMinutes = *f.DurationMinutes
	}
	if m.DurationMinutes == 0 {
		m.DurationMinutes = defaultStandingMinutes
	}
	if m.DurationMinutes < 1 || m.DurationMinutes > maxStandingMinutes {
		return badRequest("durationMinutes must be between 1 and 480")
	}
	return nil
}

func (s *Service) CreateStandingMeeting(ctx context.Context, orgID, createdBy uuid.UUID, f standingFields) (*StandingMeeting, error) {
	m := &StandingMeeting{}
	if err := f.applyTo(m); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	created, err := scanStanding(tx.QueryRow(ctx, `
		INSERT INTO standing_meetings (org_id, name, time_of_day, time_zone, weekdays, duration_minutes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+standingColumns,
		orgID, m.Name, m.TimeOfDay, m.TimeZone, m.Weekdays, m.DurationMinutes, createdBy))
	if err != nil {
		return nil, err
	}
	if err := recordSchedule(ctx, tx, created, created.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return created, nil
}

func recordSchedule(ctx context.Context, tx pgx.Tx, m *StandingMeeting, at time.Time) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO standing_meeting_schedules (standing_meeting_id, effective_at, time_of_day, time_zone, weekdays, duration_minutes)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, m.ID, at, m.TimeOfDay, m.TimeZone, m.Weekdays, m.DurationMinutes); err != nil {
		return fmt.Errorf("record schedule: %w", err)
	}
	return nil
}

// sameSchedule says whether two versions of a meeting run at the same times.
func sameSchedule(a, b *StandingMeeting) bool {
	return a.TimeOfDay == b.TimeOfDay && a.TimeZone == b.TimeZone && a.DurationMinutes == b.DurationMinutes &&
		slices.Equal(a.Weekdays, b.Weekdays)
}

func (s *Service) ListStandingMeetings(ctx context.Context, orgID uuid.UUID) ([]StandingMeeting, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+standingColumns+` FROM standing_meetings
		WHERE org_id = $1 AND archived_at IS NULL ORDER BY time_of_day, name, id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list standing meetings: %w", err)
	}
	defer rows.Close()
	meetings := []StandingMeeting{}
	for rows.Next() {
		m, err := scanStanding(rows)
		if err != nil {
			return nil, err
		}
		meetings = append(meetings, *m)
	}
	return meetings, rows.Err()
}

func (s *Service) standingMeeting(ctx context.Context, orgID, id uuid.UUID) (*StandingMeeting, error) {
	return scanStanding(s.db.QueryRow(ctx,
		`SELECT `+standingColumns+` FROM standing_meetings WHERE id = $1 AND org_id = $2`, id, orgID))
}

// UpdateStandingMeeting changes the meeting from now on. A day already held keeps
// the start it had, so nobody becomes late in retrospect, and a day that ran under
// the old schedule still counts under it, so nobody becomes absent in retrospect.
func (s *Service) UpdateStandingMeeting(ctx context.Context, orgID, id uuid.UUID, f standingFields) (*StandingMeeting, error) {
	m, err := s.standingMeeting(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if m.ArchivedAt != nil {
		return nil, apperrors.ErrNotFound
	}
	before := *m
	if err := f.applyTo(m); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	updated, err := scanStanding(tx.QueryRow(ctx, `
		UPDATE standing_meetings
		SET name = $3, time_of_day = $4, time_zone = $5, weekdays = $6, duration_minutes = $7, updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND archived_at IS NULL
		RETURNING `+standingColumns,
		id, orgID, m.Name, m.TimeOfDay, m.TimeZone, m.Weekdays, m.DurationMinutes))
	if err != nil {
		return nil, err
	}
	if !sameSchedule(&before, updated) {
		if err := recordSchedule(ctx, tx, updated, s.clock()); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

// ArchiveStandingMeeting hides the room and keeps its history.
func (s *Service) ArchiveStandingMeeting(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE standing_meetings SET archived_at = $3, updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND archived_at IS NULL
	`, id, orgID, s.clock())
	if err != nil {
		return fmt.Errorf("archive standing meeting: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

// StandingJoin is what a person needs to walk into today's room.
type StandingJoin struct {
	CallID   uuid.UUID `json:"callId"`
	RoomName string    `json:"roomName"`
	Token    string    `json:"token"`
	Date     string    `json:"date"`
}

// JoinStandingMeeting finds or makes the occurrence running now, and hands out a
// token. The unique index is what makes two people arriving at once one row, and the
// same statement brings an occurrence back if its room emptied earlier in the day.
//
// The date is the date of the slot in the meeting's zone, wherever the server is.
func (s *Service) JoinStandingMeeting(ctx context.Context, orgID, id, userID uuid.UUID) (*StandingJoin, error) {
	m, err := s.standingMeeting(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if m.ArchivedAt != nil {
		return nil, apperrors.ErrNotFound
	}
	now := s.clock()
	day, err := m.occurrenceDay(now)
	if err != nil {
		return nil, err
	}
	start, end, err := m.slotOn(day)
	if err != nil {
		return nil, err
	}
	date := day.Format("2006-01-02")
	creator := userID
	if m.CreatedBy != nil {
		creator = *m.CreatedBy
	}

	out := &StandingJoin{Date: date}
	err = s.db.QueryRow(ctx, `
		INSERT INTO calls (org_id, room_name, title, call_type, status, started_at, created_by,
		                   scheduled_start, scheduled_end, standing_meeting_id, occurrence_date)
		VALUES ($1, $2, $3, 'video', 'active', $4, $5, $6, $7, $8, $9::date)
		ON CONFLICT (standing_meeting_id, occurrence_date) WHERE standing_meeting_id IS NOT NULL
		DO UPDATE SET status = 'active', ended_at = NULL, duration_seconds = NULL, updated_at = NOW()
		RETURNING id, room_name
	`, orgID, "standing-"+uuid.NewString(), m.Name, now, creator, start, end, id, date).Scan(&out.CallID, &out.RoomName)
	if err != nil {
		return nil, fmt.Errorf("find or make occurrence: %w", err)
	}
	if _, err := s.createRoom(ctx, out.RoomName); err != nil {
		s.logger.Warn().Err(err).Str("room", out.RoomName).Msg("could not create the room for a standing meeting")
	}

	out.Token, err = s.GetToken(ctx, out.RoomName, userID.String(), s.DisplayName(ctx, userID))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Attendee is one person on one day of a standing meeting.
type Attendee struct {
	UserID uuid.UUID `json:"userId"`
	Name   string    `json:"name"`
	// present or absent, or unknown when the day has no connections recorded at
	// all. See AttendanceDay.
	Status         string     `json:"status"`
	FirstJoinAt    *time.Time `json:"firstJoinAt"`
	LastLeaveAt    *time.Time `json:"lastLeaveAt"`
	MinutesPresent int        `json:"minutesPresent"`
	MinutesLate    int        `json:"minutesLate"`
}

// AttendanceDay is one date. AttendanceRecorded is false when people asked to join
// and no connection was ever reported: that is what webhooks not arriving looks
// like, and calling everybody absent on that evidence would be wrong, so they read
// as unknown instead.
type AttendanceDay struct {
	Date               string     `json:"date"`
	CallID             *uuid.UUID `json:"callId"`
	ScheduledStart     time.Time  `json:"scheduledStart"`
	AttendanceRecorded bool       `json:"attendanceRecorded"`
	// CanReadLog is whether whoever is reading may open this day's call log, so that
	// nobody is offered a button whose only answer is a refusal.
	CanReadLog bool       `json:"canReadLog"`
	People     []Attendee `json:"people"`
}

type occurrence struct {
	callID         uuid.UUID
	date           string
	scheduledStart time.Time
	scheduledEnd   *time.Time
	endedAt        *time.Time
}

// Attendance is decisions 14 and 15. Joined and left are stored; absent is worked
// out here from who was expected.
func (s *Service) Attendance(ctx context.Context, orgID, meetingID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID, from, to time.Time) ([]AttendanceDay, error) {
	m, err := s.standingMeeting(ctx, orgID, meetingID)
	if err != nil {
		return nil, err
	}
	scope, people, err := s.chart.AnswerableFor(ctx, orgID, callerID, isOrgAdmin, teamID)
	if err != nil {
		return nil, err
	}
	// Nobody needs permission to see themself, and a manager is on no team of their
	// own. A team filter asks for that team and nothing else.
	if teamID == nil && !containsID(people, callerID) {
		people = append(people, callerID)
	}
	loc, err := time.LoadLocation(m.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("load zone: %w", err)
	}

	type expected struct {
		name   string
		joined string // the date they joined the organization, in the meeting's zone
	}
	expect := make(map[uuid.UUID]expected, len(people))
	rows, err := s.db.Query(ctx, `
		SELECT id, name, created_at FROM users
		WHERE org_id = $1 AND id = ANY($2) AND deleted_at IS NULL AND COALESCE(is_active, true)
	`, orgID, people)
	if err != nil {
		return nil, fmt.Errorf("list expected people: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var name string
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &createdAt); err != nil {
			return nil, fmt.Errorf("scan expected person: %w", err)
		}
		expect[id] = expected{name: name, joined: createdAt.In(loc).Format("2006-01-02")}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	occurrences, err := s.occurrences(ctx, meetingID, from, to)
	if err != nil {
		return nil, err
	}
	sessions, err := s.occurrenceSessions(ctx, meetingID, from, to)
	if err != nil {
		return nil, err
	}

	versions, err := s.scheduleVersions(ctx, m)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	var archived string
	if m.ArchivedAt != nil {
		archived = m.ArchivedAt.In(loc).Format("2006-01-02")
	}

	days := []AttendanceDay{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		date := d.Format("2006-01-02")
		// As it was that day, because editing the weekdays must not rewrite history.
		schedule, existed := scheduleOn(versions, date)
		runs := existed && schedule.runsOn(d.Weekday())
		occ, held := occurrences[date]
		day := AttendanceDay{Date: date, People: []Attendee{}}
		switch {
		case held:
			day.CallID = &occ.callID
			day.ScheduledStart = occ.scheduledStart
			loaded := &CallModel{ID: occ.callID, StandingMeetingID: &meetingID}
			if day.CanReadLog, err = s.MayReadLog(ctx, orgID, loaded, callerID, isOrgAdmin); err != nil {
				return nil, err
			}
		case !runs || (archived != "" && date > archived):
			continue
		default:
			// It ran on the calendar and nobody pressed join: everybody was absent,
			// but only once it is due, and only if that schedule existed by then. A
			// meeting made at 14:00 has nobody absent from its 09:00 slot that morning.
			start, _, err := schedule.slotOn(d)
			if err != nil {
				return nil, err
			}
			if start.After(now) || start.Before(schedule.effectiveAt) {
				continue
			}
			day.ScheduledStart = start
		}

		byPerson := sessions[date]
		day.AttendanceRecorded = !held || len(byPerson) > 0
		endOfDay := time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc)
		// A slot running past midnight is still that day's.
		if held && occ.scheduledEnd != nil && occ.scheduledEnd.After(endOfDay) {
			endOfDay = *occ.scheduledEnd
		}
		for id, list := range byPerson {
			_, answers := expect[id]
			// Everybody who came is on the organization's record, even somebody
			// who has since left.
			if !answers && !(scope == tasks.ScopeOrg && teamID == nil) {
				continue
			}
			day.People = append(day.People, attendeeFrom(id, list, day.ScheduledStart, occ.endedAt, now, endOfDay))
		}
		// Somebody early has opened the room, but it has not started, so nobody else
		// is missing from it yet.
		if runs && !day.ScheduledStart.After(now) {
			absent := "absent"
			if !day.AttendanceRecorded {
				absent = "unknown"
			}
			for id, who := range expect {
				if _, came := byPerson[id]; !came && who.joined <= date {
					day.People = append(day.People, Attendee{UserID: id, Name: who.name, Status: absent})
				}
			}
		}
		sort.Slice(day.People, func(i, j int) bool {
			if day.People[i].Name != day.People[j].Name {
				return day.People[i].Name < day.People[j].Name
			}
			return day.People[i].UserID.String() < day.People[j].UserID.String()
		})
		days = append(days, day)
	}
	return days, nil
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// personSessions is one person's connections on one day.
type personSessions struct {
	name  string
	spans []sessionSpan
}

type sessionSpan struct {
	joined time.Time
	left   *time.Time
}

// attendeeFrom sums one person's day. A session still open is counted up to now,
// but never past the call's end or the end of that day, so a leave that never
// arrived cannot make somebody present for a week.
func attendeeFrom(id uuid.UUID, sessions personSessions, start time.Time, endedAt *time.Time, now, endOfDay time.Time) Attendee {
	a := Attendee{UserID: id, Name: sessions.name, Status: "present"}
	limit := now
	if endOfDay.Before(limit) {
		limit = endOfDay
	}
	if endedAt != nil && endedAt.Before(limit) {
		limit = *endedAt
	}
	spans := make([][2]time.Time, 0, len(sessions.spans))
	open := false
	for _, session := range sessions.spans {
		joined := session.joined
		if a.FirstJoinAt == nil || joined.Before(*a.FirstJoinAt) {
			a.FirstJoinAt = &joined
		}
		end := limit
		if session.left == nil {
			open = true
		} else {
			end = *session.left
			if a.LastLeaveAt == nil || end.After(*a.LastLeaveAt) {
				left := end
				a.LastLeaveAt = &left
			}
		}
		spans = append(spans, [2]time.Time{joined, end})
	}
	if open {
		a.LastLeaveAt = nil
	}
	a.MinutesPresent = int(mergedDuration(spans) / time.Minute)
	if late := a.FirstJoinAt.Sub(start); late > 0 {
		a.MinutesLate = int(late / time.Minute)
	}
	return a
}

func (s *Service) occurrences(ctx context.Context, meetingID uuid.UUID, from, to time.Time) (map[string]occurrence, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, occurrence_date::text, scheduled_start, scheduled_end, ended_at FROM calls
		WHERE standing_meeting_id = $1 AND occurrence_date BETWEEN $2::date AND $3::date
	`, meetingID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("list occurrences: %w", err)
	}
	defer rows.Close()
	out := map[string]occurrence{}
	for rows.Next() {
		var o occurrence
		if err := rows.Scan(&o.callID, &o.date, &o.scheduledStart, &o.scheduledEnd, &o.endedAt); err != nil {
			return nil, fmt.Errorf("scan occurrence: %w", err)
		}
		out[o.date] = o
	}
	return out, rows.Err()
}

func (s *Service) occurrenceSessions(ctx context.Context, meetingID uuid.UUID, from, to time.Time) (map[string]map[uuid.UUID]personSessions, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.occurrence_date::text, cs.user_id, u.name, cs.joined_at, cs.left_at
		FROM calls c
		JOIN call_sessions cs ON cs.call_id = c.id AND cs.joined_at IS NOT NULL
		JOIN users u ON u.id = cs.user_id
		WHERE c.standing_meeting_id = $1 AND c.occurrence_date BETWEEN $2::date AND $3::date
	`, meetingID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("list occurrence sessions: %w", err)
	}
	defer rows.Close()
	out := map[string]map[uuid.UUID]personSessions{}
	for rows.Next() {
		var date, name string
		var id uuid.UUID
		var span sessionSpan
		if err := rows.Scan(&date, &id, &name, &span.joined, &span.left); err != nil {
			return nil, fmt.Errorf("scan occurrence session: %w", err)
		}
		if out[date] == nil {
			out[date] = map[uuid.UUID]personSessions{}
		}
		p := out[date][id]
		p.name = name
		p.spans = append(p.spans, span)
		out[date][id] = p
	}
	return out, rows.Err()
}

func (h *Handler) requireAdmin(c *gin.Context) bool {
	if !isAdmin(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "only an organization admin can manage standing meetings"})
		return false
	}
	return true
}

func (h *Handler) ListStandingMeetings(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	meetings, err := h.service.ListStandingMeetings(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to list standing meetings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"standingMeetings": meetings})
}

func (h *Handler) CreateStandingMeeting(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok || !h.requireAdmin(c) {
		return
	}
	var f standingFields
	if err := c.ShouldBindJSON(&f); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, err := h.service.CreateStandingMeeting(c.Request.Context(), orgID, userID, f)
	if err != nil {
		httpx.Error(c, err, "failed to create standing meeting")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"standingMeeting": m})
}

func (h *Handler) UpdateStandingMeeting(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok || !h.requireAdmin(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var f standingFields
	if err := c.ShouldBindJSON(&f); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, err := h.service.UpdateStandingMeeting(c.Request.Context(), orgID, id, f)
	if err != nil {
		httpx.Error(c, err, "failed to update standing meeting")
		return
	}
	c.JSON(http.StatusOK, gin.H{"standingMeeting": m})
}

func (h *Handler) ArchiveStandingMeeting(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok || !h.requireAdmin(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.service.ArchiveStandingMeeting(c.Request.Context(), orgID, id); err != nil {
		httpx.Error(c, err, "failed to archive standing meeting")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "archived"})
}

func (h *Handler) JoinStandingMeeting(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	joined, err := h.service.JoinStandingMeeting(c.Request.Context(), orgID, id, userID)
	if err != nil {
		httpx.Error(c, err, "failed to join standing meeting")
		return
	}
	c.JSON(http.StatusOK, joined)
}

// attendanceRange reads ?date= or ?from=&to=, as dates. With neither it means today
// in the meeting's zone, which the client cannot be relied on to have worked out.
func (h *Handler) attendanceRange(c *gin.Context, orgID, meetingID uuid.UUID) (time.Time, time.Time, bool) {
	refuse := func(message string) (time.Time, time.Time, bool) {
		c.JSON(http.StatusBadRequest, gin.H{"error": message})
		return time.Time{}, time.Time{}, false
	}
	parse := func(raw string) (time.Time, error) { return time.Parse("2006-01-02", raw) }
	from, to := c.Query("from"), c.Query("to")
	if date := c.Query("date"); date != "" {
		from, to = date, date
	}
	if from == "" && to == "" {
		m, err := h.service.standingMeeting(c.Request.Context(), orgID, meetingID)
		if err != nil {
			httpx.Error(c, err, "failed to read attendance")
			return time.Time{}, time.Time{}, false
		}
		loc, err := time.LoadLocation(m.TimeZone)
		if err != nil {
			httpx.Error(c, err, "failed to read attendance")
			return time.Time{}, time.Time{}, false
		}
		today := h.service.clock().In(loc).Format("2006-01-02")
		from, to = today, today
	}
	start, err := parse(from)
	if err != nil {
		return refuse("from and date must be YYYY-MM-DD")
	}
	end, err := parse(to)
	if err != nil {
		return refuse("to must be YYYY-MM-DD")
	}
	if end.Before(start) {
		return refuse("to must not be before from")
	}
	if end.Sub(start) > maxAttendanceDays*24*time.Hour {
		return refuse("a range can cover at most 92 days")
	}
	return start, end, true
}

func (h *Handler) StandingMeetingAttendance(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var teamID *uuid.UUID
	if raw := c.Query("teamId"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid teamId"})
			return
		}
		teamID = &parsed
	}
	from, to, ok := h.attendanceRange(c, orgID, id)
	if !ok {
		return
	}
	days, err := h.service.Attendance(c.Request.Context(), orgID, id, userID, isAdmin(c), teamID, from, to)
	if err != nil {
		httpx.Error(c, err, "failed to read attendance")
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": days})
}

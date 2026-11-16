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
	"github.com/teamup/teamup/internal/leave"
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

// StandingMeeting is a permanent room: for the whole organization, or for chosen
// teams and people (standing_audience.go).
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
	// CameraRequired puts whoever joins and never turns their camera on in review
	// rather than present.
	CameraRequired bool `json:"cameraRequired"`
	// Audience is who it is for. InAudience and CanManage are for whoever asked:
	// whether they may join it, and change or archive it.
	Audience   *Audience `json:"audience,omitempty"`
	InAudience bool      `json:"inAudience"`
	CanManage  bool      `json:"canManage"`

	audienceKind string
}

const standingColumns = `id, name, time_of_day, time_zone, weekdays, duration_minutes, created_by, archived_at, created_at, audience, camera_required`

func scanStanding(row pgx.Row) (*StandingMeeting, error) {
	var m StandingMeeting
	err := row.Scan(&m.ID, &m.Name, &m.TimeOfDay, &m.TimeZone, &m.Weekdays, &m.DurationMinutes, &m.CreatedBy, &m.ArchivedAt, &m.CreatedAt, &m.audienceKind, &m.CameraRequired)
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

// standingFields is what its maker may set. Each field is optional on an edit;
// sending any of the audience's three replaces the audience with what was sent.
type standingFields struct {
	Name            *string      `json:"name"`
	TimeOfDay       *string      `json:"timeOfDay"`
	TimeZone        *string      `json:"timeZone"`
	Weekdays        *[]int       `json:"weekdays"`
	DurationMinutes *int         `json:"durationMinutes"`
	Everyone        *bool        `json:"everyone"`
	TeamIDs         *[]uuid.UUID `json:"teamIds"`
	PersonIDs       *[]uuid.UUID `json:"personIds"`
	CameraRequired  *bool        `json:"cameraRequired"`
}

// audienceSent is the audience asked for, if any part of it was sent.
func (f standingFields) audienceSent() (audienceChoice, bool) {
	if f.Everyone == nil && f.TeamIDs == nil && f.PersonIDs == nil {
		return audienceChoice{}, false
	}
	var c audienceChoice
	if f.Everyone != nil {
		c.everyone = *f.Everyone
	}
	if f.TeamIDs != nil {
		c.teams = *f.TeamIDs
	}
	if f.PersonIDs != nil {
		c.people = *f.PersonIDs
	}
	return c, true
}

func badRequest(message string) error { return apperrors.New(apperrors.CodeBadRequest, message) }

// applyTo validates what was sent and writes it onto m.
func (f standingFields) applyTo(m *StandingMeeting) error {
	if f.CameraRequired != nil {
		m.CameraRequired = *f.CameraRequired
	}
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

// CreateStandingMeeting makes a meeting. Saying nothing of the audience means
// everybody, as before audiences: an administrator may, and a manager is refused
// and has to choose their teams or people.
func (s *Service) CreateStandingMeeting(ctx context.Context, orgID uuid.UUID, a Actor, f standingFields) (*StandingMeeting, error) {
	may, err := s.MayMakeStanding(ctx, orgID, a)
	if err != nil {
		return nil, err
	}
	if !may {
		return nil, apperrors.New(apperrors.CodeForbidden, "only an organization admin or a team's manager can make a standing meeting")
	}
	m := &StandingMeeting{}
	if err := f.applyTo(m); err != nil {
		return nil, err
	}
	choice, sent := f.audienceSent()
	if !sent {
		choice.everyone = true
	}
	if err := s.checkAudience(ctx, orgID, a, choice, audienceChoice{}); err != nil {
		return nil, err
	}
	createdBy := a.ID
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	created, err := scanStanding(tx.QueryRow(ctx, `
		INSERT INTO standing_meetings (org_id, name, time_of_day, time_zone, weekdays, duration_minutes, created_by, camera_required)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+standingColumns,
		orgID, m.Name, m.TimeOfDay, m.TimeZone, m.Weekdays, m.DurationMinutes, createdBy, m.CameraRequired))
	if err != nil {
		return nil, err
	}
	if err := recordSchedule(ctx, tx, created, created.CreatedAt); err != nil {
		return nil, err
	}
	if err := saveAudience(ctx, tx, created.ID, choice); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.standingFor(ctx, orgID, created.ID, a)
}

// standingFor is one meeting as an actor sees it, audience and all.
func (s *Service) standingFor(ctx context.Context, orgID, id uuid.UUID, a Actor) (*StandingMeeting, error) {
	m, err := s.standingMeeting(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	list := []StandingMeeting{*m}
	if err := s.fillAudiences(ctx, list, a); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// visibleStanding is a meeting the actor may see: an administrator sees every
// one, anybody else those they are in or made. The rest are not found, so that a
// team's meeting is not even known of outside it.
func (s *Service) visibleStanding(ctx context.Context, orgID, id uuid.UUID, a Actor) (*StandingMeeting, error) {
	m, err := s.standingMeeting(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if mayManageStanding(m, a) {
		return m, nil
	}
	in, err := s.inStandingAudience(ctx, id, a.ID)
	if err != nil {
		return nil, err
	}
	if !in {
		return nil, apperrors.ErrNotFound
	}
	return m, nil
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

// ListStandingMeetings is the meetings a viewer may see: every one for an
// administrator, otherwise the ones they are in or made.
func (s *Service) ListStandingMeetings(ctx context.Context, orgID uuid.UUID, viewer Actor) ([]StandingMeeting, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+standingColumns+` FROM standing_meetings
		WHERE org_id = $1 AND archived_at IS NULL
		  AND ($2::boolean OR created_by = $3 OR standing_audience_includes(id, $3))
		ORDER BY time_of_day, name, id`, orgID, viewer.Admin, viewer.ID)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := s.fillAudiences(ctx, meetings, viewer); err != nil {
		return nil, err
	}
	return meetings, nil
}

func (s *Service) standingMeeting(ctx context.Context, orgID, id uuid.UUID) (*StandingMeeting, error) {
	return scanStanding(s.db.QueryRow(ctx,
		`SELECT `+standingColumns+` FROM standing_meetings WHERE id = $1 AND org_id = $2`, id, orgID))
}

// UpdateStandingMeeting changes the meeting from now on. A day already held keeps
// the start it had, so nobody becomes late in retrospect, and a day that ran under
// the old schedule still counts under it, so nobody becomes absent in retrospect.
func (s *Service) UpdateStandingMeeting(ctx context.Context, orgID, id uuid.UUID, a Actor, f standingFields) (*StandingMeeting, error) {
	m, err := s.visibleStanding(ctx, orgID, id, a)
	if err != nil {
		return nil, err
	}
	if m.ArchivedAt != nil {
		return nil, apperrors.ErrNotFound
	}
	if !mayManageStanding(m, a) {
		return nil, apperrors.New(apperrors.CodeForbidden, "only whoever made this meeting, or an organization admin, can change it")
	}
	choice, changeAudience := f.audienceSent()
	if changeAudience {
		kept, err := s.currentAudience(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := s.checkAudience(ctx, orgID, a, choice, kept); err != nil {
			return nil, err
		}
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
		SET name = $3, time_of_day = $4, time_zone = $5, weekdays = $6, duration_minutes = $7,
		    camera_required = $8, updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND archived_at IS NULL
		RETURNING `+standingColumns,
		id, orgID, m.Name, m.TimeOfDay, m.TimeZone, m.Weekdays, m.DurationMinutes, m.CameraRequired))
	if err != nil {
		return nil, err
	}
	if !sameSchedule(&before, updated) {
		if err := recordSchedule(ctx, tx, updated, s.clock()); err != nil {
			return nil, err
		}
	}
	if changeAudience {
		if err := saveAudience(ctx, tx, id, choice); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.standingFor(ctx, orgID, id, a)
}

// ArchiveStandingMeeting hides the room and keeps its history.
func (s *Service) ArchiveStandingMeeting(ctx context.Context, orgID, id uuid.UUID, a Actor) error {
	m, err := s.visibleStanding(ctx, orgID, id, a)
	if err != nil {
		return err
	}
	if !mayManageStanding(m, a) {
		return apperrors.New(apperrors.CodeForbidden, "only whoever made this meeting, or an organization admin, can archive it")
	}
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
	// Outside the audience it is not there at all, administrators included: rank
	// does not walk anybody into a meeting that is not theirs.
	in, err := s.inStandingAudience(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if !in {
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
	if out.CallID, out.RoomName, err = s.openOccurrence(ctx, orgID, m, creator, start, end, date, now, true); err != nil {
		return nil, err
	}

	out.Token, err = s.GetToken(ctx, out.RoomName, userID.String(), s.DisplayName(ctx, userID))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// openOccurrence finds or makes the day's call and opens its room. asked is
// whether somebody asked to join, as opposed to the start-of-meeting ring opening
// it: attendance reads a day nobody connected to as unknown only when somebody
// asked, since that is what webhooks not arriving looks like.
func (s *Service) openOccurrence(ctx context.Context, orgID uuid.UUID, m *StandingMeeting, creator uuid.UUID,
	start, end time.Time, date string, now time.Time, asked bool) (uuid.UUID, string, error) {
	var requested *time.Time
	if asked {
		requested = &now
	}
	var callID uuid.UUID
	var room string
	err := s.db.QueryRow(ctx, `
		INSERT INTO calls (org_id, room_name, title, call_type, status, started_at, created_by,
		                   scheduled_start, scheduled_end, standing_meeting_id, occurrence_date, join_requested_at,
		                   camera_required)
		VALUES ($1, $2, $3, 'video', 'active', $4, $5, $6, $7, $8, $9::date, $10, $11)
		ON CONFLICT (standing_meeting_id, occurrence_date) WHERE standing_meeting_id IS NOT NULL
		DO UPDATE SET status = 'active', ended_at = NULL, duration_seconds = NULL, updated_at = NOW(),
		              join_requested_at = COALESCE(calls.join_requested_at, EXCLUDED.join_requested_at)
		RETURNING id, room_name
	`, orgID, "standing-"+uuid.NewString(), m.Name, now, creator, start, end, m.ID, date, requested, m.CameraRequired).Scan(&callID, &room)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("find or make occurrence: %w", err)
	}
	if _, err := s.createRoom(ctx, room); err != nil {
		s.logger.Warn().Err(err).Str("room", room).Msg("could not create the room for a standing meeting")
	}
	return callID, room, nil
}

// markAskedToJoin records that somebody asked for a way into a standing meeting's
// day, by its link or by answering its ring.
func (s *Service) markAskedToJoin(ctx context.Context, callID uuid.UUID) {
	if _, err := s.db.Exec(ctx,
		`UPDATE calls SET join_requested_at = $2 WHERE id = $1 AND standing_meeting_id IS NOT NULL AND join_requested_at IS NULL`,
		callID, s.clock()); err != nil {
		s.logger.Warn().Err(err).Msg("could not record that somebody asked to join a standing meeting")
	}
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
	// Leave is set when the status is on_leave: what part of the day, and when and
	// by whom it was recorded, so leave added after the day always says so.
	Leave *AttendeeLeave `json:"leave,omitempty"`
	// CameraOn is set on a day that required cameras: whether they turned theirs on.
	CameraOn *bool `json:"cameraOn,omitempty"`
	// Field is the field work that counted for them (attendance_rules.go).
	Field *AttendeeField `json:"field,omitempty"`
	// Review is a reviewer's decision about this day, when there is one.
	Review *AttendeeReview `json:"review,omitempty"`
}

// AttendeeLeave is the leave that excuses somebody from one day.
type AttendeeLeave struct {
	Part       string        `json:"part"`
	RecordedAt time.Time     `json:"recordedAt"`
	RecordedBy *leave.Person `json:"recordedBy"`
	// AfterTheDay is recorded only once the meeting had already started.
	AfterTheDay bool `json:"afterTheDay"`
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
	CanReadLog bool `json:"canReadLog"`
	// Holiday names the organization's holiday on this date. Nobody is absent on
	// one, and whoever came anyway is still present.
	Holiday string `json:"holiday,omitempty"`
	// CameraRequired is the stand-up's camera rule as it was that day.
	CameraRequired bool       `json:"cameraRequired"`
	People         []Attendee `json:"people"`
}

type occurrence struct {
	callID         uuid.UUID
	date           string
	scheduledStart time.Time
	scheduledEnd   *time.Time
	endedAt        *time.Time
	// asked is whether anybody asked to join, rather than only the ring opening it.
	asked bool
	// cameraRequired is the stand-up's camera rule as it was that day.
	cameraRequired bool
}

// Attendance is decisions 14 and 15. Joined and left are stored; absent is worked
// out here from who was expected.
func (s *Service) Attendance(ctx context.Context, orgID, meetingID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID, from, to time.Time) ([]AttendanceDay, error) {
	// A meeting somebody may not see has no attendance for them either.
	m, err := s.visibleStanding(ctx, orgID, meetingID, Actor{ID: callerID, Admin: isOrgAdmin})
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
	// Only the meeting's audience is expected at it: somebody it is not for is
	// not absent from it.
	if m.audienceKind != "everyone" {
		rows, err := s.db.Query(ctx,
			`SELECT id FROM unnest($1::uuid[]) AS id WHERE standing_audience_includes($2, id)`, people, meetingID)
		if err != nil {
			return nil, fmt.Errorf("narrow to the audience: %w", err)
		}
		if people, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
			return nil, fmt.Errorf("scan audience: %w", err)
		}
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

	expectedIDs := make([]uuid.UUID, 0, len(expect))
	for id := range expect {
		expectedIDs = append(expectedIDs, id)
	}
	book, err := leave.Load(ctx, s.db, orgID, expectedIDs, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	extras, err := s.loadAttendanceExtras(ctx, orgID, meetingID, expectedIDs, from, to)
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
		day.Holiday, _ = book.Holiday(date)
		switch {
		case held:
			day.CallID = &occ.callID
			day.ScheduledStart = occ.scheduledStart
			day.CameraRequired = occ.cameraRequired
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
		// Nobody connected although somebody asked to join is webhooks not
		// arriving. A room only the ring opened, that nobody answered, is not.
		day.AttendanceRecorded = !held || len(byPerson) > 0 || !occ.asked
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
			slot := day.ScheduledStart.In(loc)
			for id, who := range expect {
				if _, came := byPerson[id]; came || who.joined > date {
					continue
				}
				// Leave and holidays are known whether or not the media server
				// reported anything, so they beat unknown as well as absent.
				a := Attendee{UserID: id, Name: who.name, Status: absent}
				if day.Holiday != "" {
					a.Status = "holiday"
				} else if l, on := book.On(id, date, slot); on {
					a.Status = "on_leave"
					a.Leave = &AttendeeLeave{Part: l.Part, RecordedAt: l.RecordedAt, RecordedBy: l.RecordedBy,
						AfterTheDay: l.RecordedAt.After(day.ScheduledStart)}
				}
				day.People = append(day.People, a)
			}
		}
		extras.apply(&day)
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
		SELECT id, occurrence_date::text, scheduled_start, scheduled_end, ended_at, join_requested_at IS NOT NULL, camera_required FROM calls
		WHERE standing_meeting_id = $1 AND occurrence_date BETWEEN $2::date AND $3::date
	`, meetingID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("list occurrences: %w", err)
	}
	defer rows.Close()
	out := map[string]occurrence{}
	for rows.Next() {
		var o occurrence
		if err := rows.Scan(&o.callID, &o.date, &o.scheduledStart, &o.scheduledEnd, &o.endedAt, &o.asked, &o.cameraRequired); err != nil {
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

// actor is whoever is asking, with whether they administer meetings.
func actor(c *gin.Context) (uuid.UUID, Actor, bool) {
	orgID, userID, ok := caller(c)
	return orgID, Actor{ID: userID, Admin: isAdmin(c)}, ok
}

func (h *Handler) ListStandingMeetings(c *gin.Context) {
	orgID, a, ok := actor(c)
	if !ok {
		return
	}
	meetings, err := h.service.ListStandingMeetings(c.Request.Context(), orgID, a)
	if err != nil {
		httpx.Error(c, err, "failed to list standing meetings")
		return
	}
	may, err := h.service.MayMakeStanding(c.Request.Context(), orgID, a)
	if err != nil {
		httpx.Error(c, err, "failed to list standing meetings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"standingMeetings": meetings, "canCreate": may})
}

// StandingAudienceOptions lists the teams and people the caller may make a
// standing meeting for.
func (h *Handler) StandingAudienceOptions(c *gin.Context) {
	orgID, a, ok := actor(c)
	if !ok {
		return
	}
	opts, err := h.service.StandingAudienceOptions(c.Request.Context(), orgID, a)
	if err != nil {
		httpx.Error(c, err, "failed to list who a standing meeting can be for")
		return
	}
	c.JSON(http.StatusOK, opts)
}

func (h *Handler) CreateStandingMeeting(c *gin.Context) {
	orgID, a, ok := actor(c)
	if !ok {
		return
	}
	var f standingFields
	if err := c.ShouldBindJSON(&f); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m, err := h.service.CreateStandingMeeting(c.Request.Context(), orgID, a, f)
	if err != nil {
		httpx.Error(c, err, "failed to create standing meeting")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"standingMeeting": m})
}

func (h *Handler) UpdateStandingMeeting(c *gin.Context) {
	orgID, a, ok := actor(c)
	if !ok {
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
	m, err := h.service.UpdateStandingMeeting(c.Request.Context(), orgID, id, a, f)
	if err != nil {
		httpx.Error(c, err, "failed to update standing meeting")
		return
	}
	c.JSON(http.StatusOK, gin.H{"standingMeeting": m})
}

func (h *Handler) ArchiveStandingMeeting(c *gin.Context) {
	orgID, a, ok := actor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.service.ArchiveStandingMeeting(c.Request.Context(), orgID, id, a); err != nil {
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
	// Reviewers read every stand-up's attendance, as whoever manages everybody's meetings does.
	days, err := h.service.Attendance(c.Request.Context(), orgID, id, userID, readsAllAttendance(c), teamID, from, to)
	if err != nil {
		httpx.Error(c, err, "failed to read attendance")
		return
	}
	c.JSON(http.StatusOK, gin.H{"days": days})
}

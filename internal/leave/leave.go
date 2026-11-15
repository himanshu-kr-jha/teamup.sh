// Package leave keeps dated leave and the organization's holidays, so that a day
// somebody was away on leave is never read as a day they did not turn up.
//
// Who may write leave: the person themself, whoever manages them on the chart,
// and an administrator. Nobody else sees it at all. A record is never deleted,
// only marked removed, and every change is kept with who made it, so leave added
// for a day already gone always says so.
package leave

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/tasks"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

const (
	dateLayout = "2006-01-02"
	// maxDays is one record at most a year long. Longer is a typo in a year.
	maxDays = 366
	// maxNote keeps a note a note.
	maxNote = 200
	// maxBack and maxAhead bound when leave may fall, as typo guards: far enough
	// back to correct last year's attendance, far enough ahead to plan.
	maxBack  = 400 * 24 * time.Hour
	maxAhead = 2 * 366 * 24 * time.Hour
	// maxRange is as far as one read goes: a year back and two ahead, the most
	// leave can span.
	maxRange = 1100
)

// The parts of a day leave can cover.
const (
	PartFull      = "full"
	PartMorning   = "morning"
	PartAfternoon = "afternoon"
)

// Notifier raises a notification. An interface so leave does not depend on the
// notifications package.
type Notifier interface {
	Notify(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, data map[string]any) error
}

// Service keeps leave and holidays.
type Service struct {
	db       *pgxpool.Pool
	chart    *tasks.Service
	notifier Notifier
	logger   zerolog.Logger
	clock    func() time.Time
}

// NewService makes the service. The chart is what says who manages whom.
func NewService(db *pgxpool.Pool, chart *tasks.Service) *Service {
	return &Service{db: db, chart: chart, clock: time.Now}
}

// SetNotifier wires notifications.
func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

// SetLogger wires the logger.
func (s *Service) SetLogger(l zerolog.Logger) { s.logger = l }

// SetClock replaces the clock, for tests.
func (s *Service) SetClock(now func() time.Time) { s.clock = now }

// Actor is whoever is asking. Admin is the right to manage members.
type Actor struct {
	ID    uuid.UUID
	Admin bool
}

// Person is somebody named on a record.
type Person struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Leave is one record.
type Leave struct {
	ID       uuid.UUID `json:"id"`
	UserID   uuid.UUID `json:"userId"`
	UserName string    `json:"userName"`
	StartsOn string    `json:"startsOn"`
	// EndsOn is nil for leave with no return date yet.
	EndsOn    *string   `json:"endsOn"`
	Part      string    `json:"part"`
	Note      string    `json:"note"`
	AddedBy   *Person   `json:"addedBy"`
	AddedAt   time.Time `json:"addedAt"`
	ChangedBy *Person   `json:"changedBy"`
	ChangedAt time.Time `json:"changedAt"`
}

// Change is one entry in a record's history: what it said after the change, or,
// for a removal, what it said when it went.
type Change struct {
	Action   string    `json:"action"`
	StartsOn string    `json:"startsOn"`
	EndsOn   *string   `json:"endsOn"`
	Part     string    `json:"part"`
	Note     string    `json:"note"`
	Actor    *Person   `json:"actor"`
	At       time.Time `json:"at"`
}

// Input is what a write sends.
type Input struct {
	UserID   uuid.UUID
	StartsOn string
	EndsOn   *string
	Part     string
	Note     string
}

func badRequest(message string) error { return apperrors.New(apperrors.CodeBadRequest, message) }

// ErrOverlaps is leave over days the person already has leave on.
var ErrOverlaps = apperrors.New(apperrors.CodeConflict, "this overlaps leave they already have; change that one instead")

func parseDate(raw, name string) (time.Time, error) {
	d, err := time.Parse(dateLayout, raw)
	if err != nil {
		return time.Time{}, badRequest(name + " must be a date like 2026-11-20")
	}
	return d, nil
}

// normalize checks an input and puts it in its one shape.
func (s *Service) normalize(in *Input) error {
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > maxNote {
		return badRequest("keep the note under 200 characters")
	}
	if in.Part == "" {
		in.Part = PartFull
	}
	if in.Part != PartFull && in.Part != PartMorning && in.Part != PartAfternoon {
		return badRequest("part must be full, morning or afternoon")
	}
	start, err := parseDate(in.StartsOn, "the first day")
	if err != nil {
		return err
	}
	now := s.clock().UTC()
	if start.Before(now.Add(-maxBack)) || start.After(now.Add(maxAhead)) {
		return badRequest("leave has to fall within a year back and two years ahead")
	}
	if in.EndsOn != nil {
		end, err := parseDate(*in.EndsOn, "the last day")
		if err != nil {
			return err
		}
		if end.Before(start) {
			return badRequest("the last day cannot be before the first")
		}
		if end.Sub(start) >= maxDays*24*time.Hour {
			return badRequest("one stretch of leave is at most a year; add another for the rest")
		}
	}
	if in.Part != PartFull && (in.EndsOn == nil || *in.EndsOn != in.StartsOn) {
		return badRequest("half a day is one date")
	}
	return nil
}

// inOrg is whether somebody is a current member of the organization.
func (s *Service) inOrg(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
		userID, orgID).Scan(&ok)
	return ok, err
}

// mayManage is the one rule: yourself, somebody you manage, or anybody if you
// administer members.
func (s *Service) mayManage(ctx context.Context, orgID uuid.UUID, a Actor, subject uuid.UUID) (bool, error) {
	in, err := s.inOrg(ctx, orgID, subject)
	if err != nil || !in {
		return false, err
	}
	return s.chart.HasAuthorityOver(ctx, orgID, a.ID, subject, a.Admin)
}

// People is everybody whose leave a can see and write: themself first, then
// whoever they answer for.
func (s *Service) People(ctx context.Context, orgID uuid.UUID, a Actor) ([]Person, error) {
	_, ids, err := s.chart.AnswerableFor(ctx, orgID, a.ID, a.Admin, nil)
	if err != nil {
		return nil, err
	}
	ids = append(ids, a.ID)
	rows, err := s.db.Query(ctx, `
		SELECT id, name FROM users
		WHERE org_id = $1 AND id = ANY($2) AND deleted_at IS NULL AND COALESCE(is_active, true)
		ORDER BY id = $3 DESC, name, id
	`, orgID, ids, a.ID)
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Person, error) {
		var p Person
		return p, row.Scan(&p.ID, &p.Name)
	})
}

const leaveColumns = `
	l.id, l.user_id, u.name, l.starts_on::text, l.ends_on::text, l.part, l.note,
	l.created_by, COALESCE(cb.name, ''), l.created_at, l.updated_by, COALESCE(ub.name, ''), l.updated_at`

const leaveFrom = `
	FROM leave_periods l
	JOIN users u ON u.id = l.user_id
	LEFT JOIN users cb ON cb.id = l.created_by
	LEFT JOIN users ub ON ub.id = l.updated_by`

func person(id *uuid.UUID, name string) *Person {
	if id == nil {
		return nil
	}
	return &Person{ID: *id, Name: name}
}

func scanLeave(row pgx.Row) (*Leave, error) {
	var l Leave
	var createdBy, updatedBy *uuid.UUID
	var createdName, updatedName string
	if err := row.Scan(&l.ID, &l.UserID, &l.UserName, &l.StartsOn, &l.EndsOn, &l.Part, &l.Note,
		&createdBy, &createdName, &l.AddedAt, &updatedBy, &updatedName, &l.ChangedAt); err != nil {
		return nil, err
	}
	l.AddedBy = person(createdBy, createdName)
	l.ChangedBy = person(updatedBy, updatedName)
	return &l, nil
}

// List is the leave a may see that touches the dates from to to, for one person
// or for everybody they may see.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, a Actor, from, to string, only *uuid.UUID) ([]Leave, error) {
	start, err := parseDate(from, "from")
	if err != nil {
		return nil, err
	}
	end, err := parseDate(to, "to")
	if err != nil {
		return nil, err
	}
	if end.Before(start) {
		return nil, badRequest("to cannot be before from")
	}
	if end.Sub(start) > maxRange*24*time.Hour {
		return nil, badRequest("one read covers at most 1100 days")
	}
	people, err := s.People(ctx, orgID, a)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(people))
	for _, p := range people {
		if only == nil || p.ID == *only {
			ids = append(ids, p.ID)
		}
	}
	if only != nil && len(ids) == 0 {
		// Not found rather than refused, so nobody can probe who exists.
		return nil, apperrors.ErrNotFound
	}
	rows, err := s.db.Query(ctx, `SELECT `+leaveColumns+leaveFrom+`
		WHERE l.org_id = $1 AND l.user_id = ANY($2) AND l.removed_at IS NULL
		  AND l.starts_on <= $4::date AND COALESCE(l.ends_on, 'infinity'::date) >= $3::date
		ORDER BY l.starts_on DESC, u.name, l.id
	`, orgID, ids, from, to)
	if err != nil {
		return nil, fmt.Errorf("list leave: %w", err)
	}
	defer rows.Close()
	out := []Leave{}
	for rows.Next() {
		l, err := scanLeave(rows)
		if err != nil {
			return nil, fmt.Errorf("scan leave: %w", err)
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// get reads one record that a may manage, or not found.
func (s *Service) get(ctx context.Context, q pgx.Tx, orgID uuid.UUID, a Actor, id uuid.UUID, lock bool) (*Leave, error) {
	query := `SELECT ` + leaveColumns + leaveFrom + ` WHERE l.id = $1 AND l.org_id = $2 AND l.removed_at IS NULL`
	if lock {
		query += ` FOR UPDATE OF l`
	}
	l, err := scanLeave(q.QueryRow(ctx, query, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read leave: %w", err)
	}
	ok, err := s.mayManage(ctx, orgID, a, l.UserID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperrors.ErrNotFound
	}
	return l, nil
}

// overlaps is whether the person has other leave on any of these days. Two half
// days of one date, a morning and an afternoon, do not overlap.
func overlaps(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, in Input, except *uuid.UUID) (bool, error) {
	var found bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM leave_periods
			WHERE org_id = $1 AND user_id = $2 AND removed_at IS NULL AND ($6::uuid IS NULL OR id <> $6)
			  AND starts_on <= COALESCE($4::date, 'infinity'::date)
			  AND COALESCE(ends_on, 'infinity'::date) >= $3::date
			  AND NOT (part <> 'full' AND $5 <> 'full' AND part <> $5)
		)
	`, orgID, in.UserID, in.StartsOn, in.EndsOn, in.Part, except).Scan(&found)
	return found, err
}

func record(ctx context.Context, tx pgx.Tx, orgID, leaveID, actor uuid.UUID, action string, l Input) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO leave_changes (leave_id, org_id, actor_id, action, starts_on, ends_on, part, note)
		VALUES ($1, $2, $3, $4, $5::date, $6::date, $7, $8)
	`, leaveID, orgID, actor, action, l.StartsOn, l.EndsOn, l.Part, l.Note)
	if err != nil {
		return fmt.Errorf("record leave change: %w", err)
	}
	return nil
}

// lockPerson serializes writes to one person's leave, so two writes at once
// cannot both pass the overlap check.
func lockPerson(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID); err != nil {
		return fmt.Errorf("lock person: %w", err)
	}
	return nil
}

// Add records leave for in.UserID.
func (s *Service) Add(ctx context.Context, orgID uuid.UUID, a Actor, in Input) (*Leave, error) {
	if err := s.normalize(&in); err != nil {
		return nil, err
	}
	ok, err := s.mayManage(ctx, orgID, a, in.UserID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperrors.ErrNotFound
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := s.insert(ctx, tx, orgID, a.ID, in)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.tell(ctx, orgID, a.ID, in.UserID, "added", in)
	return s.read(ctx, orgID, id)
}

func (s *Service) insert(ctx context.Context, tx pgx.Tx, orgID, actor uuid.UUID, in Input) (uuid.UUID, error) {
	if err := lockPerson(ctx, tx, in.UserID); err != nil {
		return uuid.Nil, err
	}
	clash, err := overlaps(ctx, tx, orgID, in, nil)
	if err != nil {
		return uuid.Nil, fmt.Errorf("check overlap: %w", err)
	}
	if clash {
		return uuid.Nil, ErrOverlaps
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO leave_periods (org_id, user_id, starts_on, ends_on, part, note, created_by, updated_by)
		VALUES ($1, $2, $3::date, $4::date, $5, $6, $7, $7)
		RETURNING id
	`, orgID, in.UserID, in.StartsOn, in.EndsOn, in.Part, in.Note, actor).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("add leave: %w", err)
	}
	return id, record(ctx, tx, orgID, id, actor, "added", in)
}

// read is one record, with no permission check: for answering a write that
// already passed one.
func (s *Service) read(ctx context.Context, orgID, id uuid.UUID) (*Leave, error) {
	l, err := scanLeave(s.db.QueryRow(ctx, `SELECT `+leaveColumns+leaveFrom+` WHERE l.id = $1 AND l.org_id = $2`, id, orgID))
	if err != nil {
		return nil, fmt.Errorf("read leave: %w", err)
	}
	return l, nil
}

// Change rewrites a record. Whose leave it is never changes.
func (s *Service) Change(ctx context.Context, orgID uuid.UUID, a Actor, id uuid.UUID, in Input) (*Leave, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := s.get(ctx, tx, orgID, a, id, true)
	if err != nil {
		return nil, err
	}
	in.UserID = current.UserID
	if err := s.normalize(&in); err != nil {
		return nil, err
	}
	if err := s.update(ctx, tx, orgID, a.ID, id, in); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.tell(ctx, orgID, a.ID, in.UserID, "changed", in)
	return s.read(ctx, orgID, id)
}

func (s *Service) update(ctx context.Context, tx pgx.Tx, orgID, actor, id uuid.UUID, in Input) error {
	if err := lockPerson(ctx, tx, in.UserID); err != nil {
		return err
	}
	clash, err := overlaps(ctx, tx, orgID, in, &id)
	if err != nil {
		return fmt.Errorf("check overlap: %w", err)
	}
	if clash {
		return ErrOverlaps
	}
	if _, err := tx.Exec(ctx, `
		UPDATE leave_periods SET starts_on = $2::date, ends_on = $3::date, part = $4, note = $5,
		       updated_by = $6, updated_at = NOW()
		WHERE id = $1
	`, id, in.StartsOn, in.EndsOn, in.Part, in.Note, actor); err != nil {
		return fmt.Errorf("change leave: %w", err)
	}
	return record(ctx, tx, orgID, id, actor, "changed", in)
}

// Remove marks a record removed. Its history stays.
func (s *Service) Remove(ctx context.Context, orgID uuid.UUID, a Actor, id uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := s.get(ctx, tx, orgID, a, id, true)
	if err != nil {
		return err
	}
	in := Input{UserID: current.UserID, StartsOn: current.StartsOn, EndsOn: current.EndsOn, Part: current.Part, Note: current.Note}
	if err := remove(ctx, tx, orgID, a.ID, id, in); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.tell(ctx, orgID, a.ID, in.UserID, "removed", in)
	return nil
}

func remove(ctx context.Context, tx pgx.Tx, orgID, actor, id uuid.UUID, in Input) error {
	if _, err := tx.Exec(ctx, `
		UPDATE leave_periods SET removed_at = NOW(), removed_by = $2, updated_by = $2, updated_at = NOW()
		WHERE id = $1
	`, id, actor); err != nil {
		return fmt.Errorf("remove leave: %w", err)
	}
	return record(ctx, tx, orgID, id, actor, "removed", in)
}

// History is every change to a record, oldest first. A removed record still has
// one, for whoever could see it.
func (s *Service) History(ctx context.Context, orgID uuid.UUID, a Actor, id uuid.UUID) ([]Change, error) {
	var owner uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT user_id FROM leave_periods WHERE id = $1 AND org_id = $2`, id, orgID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read leave: %w", err)
	}
	ok, err := s.mayManage(ctx, orgID, a, owner)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperrors.ErrNotFound
	}
	rows, err := s.db.Query(ctx, `
		SELECT c.action, c.starts_on::text, c.ends_on::text, c.part, c.note, c.actor_id, COALESCE(u.name, ''), c.created_at
		FROM leave_changes c LEFT JOIN users u ON u.id = c.actor_id
		WHERE c.leave_id = $1 ORDER BY c.created_at, c.id
	`, id)
	if err != nil {
		return nil, fmt.Errorf("list leave changes: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Change, error) {
		var c Change
		var actor *uuid.UUID
		var name string
		err := row.Scan(&c.Action, &c.StartsOn, &c.EndsOn, &c.Part, &c.Note, &actor, &name, &c.At)
		c.Actor = person(actor, name)
		return c, err
	})
}

// tell lets somebody know a manager or administrator wrote their leave. Their
// own writes need no telling.
func (s *Service) tell(ctx context.Context, orgID, actor, subject uuid.UUID, action string, in Input) {
	if s.notifier == nil || actor == subject {
		return
	}
	var name string
	if err := s.db.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, actor).Scan(&name); err != nil {
		name = "Somebody"
	}
	verb := map[string]string{"added": "added leave for you", "changed": "changed your leave", "removed": "removed your leave"}[action]
	data := map[string]any{"startsOn": in.StartsOn, "part": in.Part}
	if in.EndsOn != nil {
		data["endsOn"] = *in.EndsOn
	}
	if err := s.notifier.Notify(ctx, orgID, subject, "leave.changed", name+" "+verb, Describe(in.StartsOn, in.EndsOn, in.Part), data); err != nil {
		s.logger.Warn().Err(err).Msg("could not tell somebody about their leave")
	}
}

// Describe says which days a record covers, in words.
func Describe(startsOn string, endsOn *string, part string) string {
	day := func(raw string) string {
		d, err := time.Parse(dateLayout, raw)
		if err != nil {
			return raw
		}
		return d.Format("Mon 2 Jan 2006")
	}
	switch {
	case part == PartMorning:
		return day(startsOn) + ", morning"
	case part == PartAfternoon:
		return day(startsOn) + ", afternoon"
	case endsOn == nil:
		return "From " + day(startsOn) + ", no return date yet"
	case *endsOn == startsOn:
		return day(startsOn)
	default:
		return day(startsOn) + " to " + day(*endsOn)
	}
}

// ── leave from the availability picker ────────────────────────────────────

// covering is the person's own record that covers today, if any.
func covering(ctx context.Context, tx pgx.Tx, orgID, userID uuid.UUID, today string) (*Leave, error) {
	l, err := scanLeave(tx.QueryRow(ctx, `SELECT `+leaveColumns+leaveFrom+`
		WHERE l.org_id = $1 AND l.user_id = $2 AND l.removed_at IS NULL AND l.part = 'full'
		  AND l.starts_on <= $3::date AND COALESCE(l.ends_on, 'infinity'::date) >= $3::date
		ORDER BY l.starts_on LIMIT 1 FOR UPDATE OF l
	`, orgID, userID, today))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

// StartToday is somebody choosing "on leave" for themself. Leave already
// covering today is left as it is; otherwise they are on leave from today until
// the day before backOn, or with no return date when there is none.
func (s *Service) StartToday(ctx context.Context, orgID, userID uuid.UUID, today string, backOn *string) error {
	in := Input{UserID: userID, StartsOn: today, Part: PartFull}
	if backOn != nil {
		back, err := parseDate(*backOn, "back on")
		if err != nil {
			return err
		}
		start, err := parseDate(today, "today")
		if err != nil {
			return err
		}
		if !back.After(start) {
			return badRequest("back on has to be after today")
		}
		last := back.AddDate(0, 0, -1).Format(dateLayout)
		in.EndsOn = &last
	}
	if err := s.normalize(&in); err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockPerson(ctx, tx, userID); err != nil {
		return err
	}
	current, err := covering(ctx, tx, orgID, userID, today)
	if err != nil {
		return fmt.Errorf("find leave today: %w", err)
	}
	if current == nil {
		if _, err := s.insert(ctx, tx, orgID, userID, in); err != nil {
			return err
		}
	} else if backOn != nil && (current.EndsOn == nil || *current.EndsOn != *in.EndsOn) {
		// A new return date moves the end of the leave they are on.
		next := Input{UserID: userID, StartsOn: current.StartsOn, EndsOn: in.EndsOn, Part: PartFull, Note: current.Note}
		if err := s.normalize(&next); err != nil {
			return err
		}
		if err := s.update(ctx, tx, orgID, userID, current.ID, next); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// EndToday is somebody saying they are back: leave covering today ends
// yesterday, or goes, if it only started today.
func (s *Service) EndToday(ctx context.Context, orgID, userID uuid.UUID, today string) error {
	start, err := parseDate(today, "today")
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := covering(ctx, tx, orgID, userID, today)
	if err != nil {
		return fmt.Errorf("find leave today: %w", err)
	}
	if current == nil {
		return nil
	}
	in := Input{UserID: userID, StartsOn: current.StartsOn, EndsOn: current.EndsOn, Part: current.Part, Note: current.Note}
	if current.StartsOn >= today {
		if err := remove(ctx, tx, orgID, userID, current.ID, in); err != nil {
			return err
		}
	} else {
		yesterday := start.AddDate(0, 0, -1).Format(dateLayout)
		in.EndsOn = &yesterday
		if err := s.update(ctx, tx, orgID, userID, current.ID, in); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Today is the caller's date when it is believable, which is within a day of the
// server's, since nobody's clock is more than fourteen hours from UTC; otherwise
// the server's.
func Today(raw string, now time.Time) string {
	utc := now.UTC().Format(dateLayout)
	d, err := time.Parse(dateLayout, raw)
	if err != nil {
		return utc
	}
	server, _ := time.Parse(dateLayout, utc)
	if diff := d.Sub(server); diff > 24*time.Hour || diff < -24*time.Hour {
		return utc
	}
	return raw
}

// OnLeaveToday is who among the organization has leave covering today, a whole
// day or part of it, with the day they are back when it is known.
func (s *Service) OnLeaveToday(ctx context.Context, orgID uuid.UUID, today string) (map[uuid.UUID]*string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, (MAX(COALESCE(ends_on, 'infinity'::date)) + 1)::text FROM leave_periods
		WHERE org_id = $1 AND removed_at IS NULL
		  AND starts_on <= $2::date AND COALESCE(ends_on, 'infinity'::date) >= $2::date
		GROUP BY user_id
	`, orgID, today)
	if err != nil {
		return nil, fmt.Errorf("list leave today: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]*string{}
	for rows.Next() {
		var id uuid.UUID
		var back string
		if err := rows.Scan(&id, &back); err != nil {
			return nil, err
		}
		if back == "infinity" {
			out[id] = nil
		} else {
			out[id] = &back
		}
	}
	return out, rows.Err()
}

// ── what attendance and the calendar read ─────────────────────────────────

// Day is what leave says about one person on one date.
type Day struct {
	// Part is full, or the half of the day the leave covers.
	Part string
	// RecordedAt is since when this date has been on leave, and RecordedBy who
	// made it so: a record changed to take in a date counts from that change.
	RecordedAt time.Time
	RecordedBy *Person
}

// Covers is whether the leave covers a meeting starting at slot, read in the
// meeting's zone: a morning is before noon.
func (d Day) Covers(slot time.Time) bool {
	switch d.Part {
	case PartMorning:
		return slot.Hour() < 12
	case PartAfternoon:
		return slot.Hour() >= 12
	default:
		return true
	}
}

// Book is leave and holidays over some dates, for some people.
type Book struct {
	days     map[uuid.UUID]map[string][]Day
	holidays map[string]string
}

// On is the person's leave covering slot on date, if there is any.
func (b *Book) On(person uuid.UUID, date string, slot time.Time) (Day, bool) {
	if b == nil {
		return Day{}, false
	}
	for _, d := range b.days[person][date] {
		if d.Covers(slot) {
			return d, true
		}
	}
	return Day{}, false
}

// Holiday is the name of the organization's holiday on date, if it is one.
func (b *Book) Holiday(date string) (string, bool) {
	if b == nil {
		return "", false
	}
	name, ok := b.holidays[date]
	return name, ok
}

type span struct {
	start, end string // end is "" for no end
	part       string
}

func (sp span) covers(date string) bool {
	return sp.start <= date && (sp.end == "" || date <= sp.end)
}

// Load reads leave and holidays for these people over the dates from to to,
// inclusive. It answers for anybody asked about: whoever calls it has already
// decided who may see the result.
func Load(ctx context.Context, db *pgxpool.Pool, orgID uuid.UUID, people []uuid.UUID, from, to string) (*Book, error) {
	b := &Book{days: map[uuid.UUID]map[string][]Day{}, holidays: map[string]string{}}
	hrows, err := db.Query(ctx, `SELECT on_date::text, name FROM org_holidays WHERE org_id = $1 AND on_date BETWEEN $2::date AND $3::date`, orgID, from, to)
	if err != nil {
		return nil, fmt.Errorf("load holidays: %w", err)
	}
	for hrows.Next() {
		var date, name string
		if err := hrows.Scan(&date, &name); err != nil {
			hrows.Close()
			return nil, err
		}
		b.holidays[date] = name
	}
	hrows.Close()
	if err := hrows.Err(); err != nil {
		return nil, err
	}
	if len(people) == 0 {
		return b, nil
	}

	rows, err := db.Query(ctx, `
		SELECT l.id, l.user_id, l.part, c.starts_on::text, COALESCE(c.ends_on::text, ''), c.part, c.action = 'removed',
		       c.actor_id, COALESCE(u.name, ''), c.created_at
		FROM leave_periods l
		JOIN leave_changes c ON c.leave_id = l.id
		LEFT JOIN users u ON u.id = c.actor_id
		WHERE l.org_id = $1 AND l.user_id = ANY($2) AND l.removed_at IS NULL
		  AND l.starts_on <= $4::date AND COALESCE(l.ends_on, 'infinity'::date) >= $3::date
		ORDER BY l.id, c.created_at, c.id
	`, orgID, people, from, to)
	if err != nil {
		return nil, fmt.Errorf("load leave: %w", err)
	}
	defer rows.Close()
	type history struct {
		user    uuid.UUID
		part    string
		changes []struct {
			span    span
			removed bool
			by      *Person
			at      time.Time
		}
	}
	byLeave := map[uuid.UUID]*history{}
	var order []uuid.UUID
	for rows.Next() {
		var id, user uuid.UUID
		var part, start, end, changePart, name string
		var removed bool
		var actor *uuid.UUID
		var at time.Time
		if err := rows.Scan(&id, &user, &part, &start, &end, &changePart, &removed, &actor, &name, &at); err != nil {
			return nil, err
		}
		h := byLeave[id]
		if h == nil {
			h = &history{user: user, part: part}
			byLeave[id] = h
			order = append(order, id)
		}
		h.changes = append(h.changes, struct {
			span    span
			removed bool
			by      *Person
			at      time.Time
		}{span{start, end, changePart}, removed, person(actor, name), at})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	first, _ := time.Parse(dateLayout, from)
	last, _ := time.Parse(dateLayout, to)
	for _, id := range order {
		h := byLeave[id]
		final := h.changes[len(h.changes)-1].span
		for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
			date := d.Format(dateLayout)
			if !final.covers(date) {
				continue
			}
			// Since when this date has been covered without a break.
			var since *time.Time
			var by *Person
			for i := range h.changes {
				c := h.changes[i]
				if !c.removed && c.span.covers(date) {
					if since == nil {
						at := c.at
						since, by = &at, c.by
					}
				} else {
					since, by = nil, nil
				}
			}
			if since == nil {
				continue
			}
			if b.days[h.user] == nil {
				b.days[h.user] = map[string][]Day{}
			}
			b.days[h.user][date] = append(b.days[h.user][date], Day{Part: final.part, RecordedAt: *since, RecordedBy: by})
		}
	}
	return b, nil
}

// ── holidays ──────────────────────────────────────────────────────────────

// Holiday is a day the organization is off.
type Holiday struct {
	ID   uuid.UUID `json:"id"`
	Date string    `json:"date"`
	Name string    `json:"name"`
}

// Holidays lists the organization's holidays between two dates. Everybody in it
// may read them.
func (s *Service) Holidays(ctx context.Context, orgID uuid.UUID, from, to string) ([]Holiday, error) {
	if _, err := parseDate(from, "from"); err != nil {
		return nil, err
	}
	if _, err := parseDate(to, "to"); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, on_date::text, name FROM org_holidays
		WHERE org_id = $1 AND on_date BETWEEN $2::date AND $3::date ORDER BY on_date
	`, orgID, from, to)
	if err != nil {
		return nil, fmt.Errorf("list holidays: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Holiday, error) {
		var h Holiday
		return h, row.Scan(&h.ID, &h.Date, &h.Name)
	})
}

// ErrHolidayTaken is a second holiday on one date.
var ErrHolidayTaken = apperrors.New(apperrors.CodeConflict, "there is already a holiday on that date")

// AddHoliday marks a date off for everybody. The caller has checked the right.
func (s *Service) AddHoliday(ctx context.Context, orgID, actor uuid.UUID, date, name string) (*Holiday, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, badRequest("name the holiday")
	}
	if len([]rune(name)) > 120 {
		return nil, badRequest("keep the name under 120 characters")
	}
	d, err := parseDate(date, "the date")
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	if d.Before(now.Add(-maxBack)) || d.After(now.Add(maxAhead)) {
		return nil, badRequest("a holiday has to fall within a year back and two years ahead")
	}
	h := Holiday{Date: date, Name: name}
	err = s.db.QueryRow(ctx, `
		INSERT INTO org_holidays (org_id, on_date, name, created_by) VALUES ($1, $2::date, $3, $4)
		ON CONFLICT (org_id, on_date) DO NOTHING RETURNING id
	`, orgID, date, name, actor).Scan(&h.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrHolidayTaken
	}
	if err != nil {
		return nil, fmt.Errorf("add holiday: %w", err)
	}
	return &h, nil
}

// RemoveHoliday takes a holiday off the calendar.
func (s *Service) RemoveHoliday(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM org_holidays WHERE id = $1 AND org_id = $2`, id, orgID)
	if err != nil {
		return fmt.Errorf("remove holiday: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

// ── whether somebody is away right now ────────────────────────────────────

// OnLeaveAt is who among these people is on leave at that moment, by their own
// clock (users.timezone, which their browser keeps): a whole day, or the half of
// it the moment falls in, with the day they are back when it is known. It is
// what keeps a call, a ring or a reminder from reaching somebody on leave.
func OnLeaveAt(ctx context.Context, db *pgxpool.Pool, orgID uuid.UUID, people []uuid.UUID, at time.Time) (map[uuid.UUID]*string, error) {
	out := map[uuid.UUID]*string{}
	if len(people) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx, `
		WITH p AS (
			SELECT u.id, ($3::timestamptz AT TIME ZONE COALESCE(NULLIF(u.timezone, ''), 'UTC')) AS local
			FROM users u WHERE u.org_id = $1 AND u.id = ANY($2)
		)
		SELECT p.id, (MAX(COALESCE(l.ends_on, 'infinity'::date)) + 1)::text
		FROM p JOIN leave_periods l ON l.user_id = p.id AND l.org_id = $1 AND l.removed_at IS NULL
		WHERE l.starts_on <= p.local::date AND COALESCE(l.ends_on, 'infinity'::date) >= p.local::date
		  AND (l.part = 'full'
		       OR (l.part = 'morning' AND EXTRACT(HOUR FROM p.local) < 12)
		       OR (l.part = 'afternoon' AND EXTRACT(HOUR FROM p.local) >= 12))
		GROUP BY p.id
	`, orgID, people, at)
	if err != nil {
		return nil, fmt.Errorf("find who is on leave: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var back string
		if err := rows.Scan(&id, &back); err != nil {
			return nil, err
		}
		if back == "infinity" {
			out[id] = nil
		} else {
			b := back
			out[id] = &b
		}
	}
	return out, rows.Err()
}

// ValidZone is whether a time zone name is one the server knows, so that a
// stored zone can never break a query that reads it.
func ValidZone(name string) bool {
	if name == "" || len(name) > 50 {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

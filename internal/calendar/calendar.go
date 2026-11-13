// Package calendar is everybody's time: focus blocks and open slots, scheduled
// meetings, the calls they were in, and the organization's standing meetings.
//
// A meeting is a row in calls that has a scheduled start, so it is joined, logged
// and ended exactly like any other call. Nothing here rings anybody: a meeting
// opens when somebody asks for a token (see calls.openForJoin), and people find
// out through being invited and through the reminder.
package calendar

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	// Standing meetings are worked out in the zone an admin picked.
	_ "time/tzdata"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/tasks"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

const (
	// maxRange is as far as one read of a calendar goes.
	maxRange = 92 * 24 * time.Hour
	// maxBlock refuses a block that is a mistake rather than a holiday.
	maxBlock = 30 * 24 * time.Hour
	// maxMeeting is a sane meeting. Longer is a typo in a date.
	maxMeeting = 24 * time.Hour
	// maxPeople bounds one availability read and one meeting's invitees.
	maxPeople = 200
	// ReminderLead is how long before a meeting people are reminded.
	ReminderLead = 5 * time.Minute
	// ringWindow is how long a call tries to reach somebody, as calls.RingWindow.
	ringWindow = 45 * time.Second
)

// Notifier raises a notification. An interface so calendar does not depend on the
// notifications package.
type Notifier interface {
	Notify(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, data map[string]any) error
}

// Service answers for calendars and scheduling.
type Service struct {
	db       *pgxpool.Pool
	notifier Notifier
	logger   zerolog.Logger
	// The org chart lives with tasks; this reads it so there is one statement of
	// who outranks whom.
	chart *tasks.Service
	clock func() time.Time
	// guests is told when a meeting with guests from outside moves or ends.
	guests GuestNotifier
	// changed is told whose meetings were scheduled, moved or cancelled, so a
	// copy kept elsewhere (Google Calendar) can follow.
	changed func(people []uuid.UUID)
}

// OnMeetingsChanged is called with everybody a scheduled, moved or cancelled
// meeting concerns. It must not block.
func (s *Service) OnMeetingsChanged(f func(people []uuid.UUID)) { s.changed = f }

func (s *Service) meetingsChanged(people ...[]uuid.UUID) {
	if s.changed == nil {
		return
	}
	var all []uuid.UUID
	for _, p := range people {
		all = append(all, p...)
	}
	s.changed(all)
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db, chart: tasks.NewService(db), clock: time.Now}
}

func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

// GuestNotifier tells people from outside, invited by email, that a meeting
// moved or was cancelled.
type GuestNotifier interface {
	GuestsMeetingChanged(ctx context.Context, orgID, callID uuid.UUID, cancelled bool)
}

func (s *Service) SetGuestNotifier(g GuestNotifier) { s.guests = g }

func (s *Service) SetLogger(l zerolog.Logger) {
	s.logger = l.With().Str("component", "calendar").Logger()
}

// SetClock replaces what this service takes the time to be, for tests.
func (s *Service) SetClock(now func() time.Time) { s.clock = now }

// Item is one stretch of somebody's time. Kind and the times are for everybody in
// the organization, through availability and conflicts; the rest is detail, for
// them and whoever has authority over them.
type Item struct {
	ID     string `json:"id,omitempty"`
	Kind   string `json:"kind"` // busy, focus, open or entry
	Source string `json:"source,omitempty"`
	Title  string `json:"title,omitempty"`
	// What an entry says beyond its name. Only its owner and their managers read it.
	Details string `json:"details,omitempty"`
	// Where a meeting from Google Calendar opens there. Only for its owner.
	Link string `json:"link,omitempty"`
	// Marked private in Google Calendar: its name is for its owner alone.
	private           bool
	StartsAt          time.Time  `json:"startsAt"`
	EndsAt            time.Time  `json:"endsAt"`
	CallID            *uuid.UUID `json:"callId,omitempty"`
	StandingMeetingID *uuid.UUID `json:"standingMeetingId,omitempty"`
	OrganizerID       *uuid.UUID `json:"organizerId,omitempty"`
	InviteeIDs        []string   `json:"inviteeIds,omitempty"`
	Status            string     `json:"status,omitempty"`
	// ScheduledEndsAt is set only on a meeting that ran past its scheduled end
	// while this person was in it, so EndsAt is later than planned.
	ScheduledEndsAt *time.Time `json:"scheduledEndsAt,omitempty"`
	// Running says the meeting is still going and this person is still in it, so
	// EndsAt is now and will keep moving.
	Running bool `json:"running,omitempty"`
	// CanReadLog says whether the person reading this calendar may read this call's
	// log, so that nobody is offered a button whose only answer is a refusal.
	CanReadLog *bool `json:"canReadLog,omitempty"`
	// present is who has a row on the call. It decides the log's rule for a
	// standing meeting, and it is not for the wire: that list is attendance.
	present []uuid.UUID
}

// shape is the item as somebody without authority may see it.
func (i Item) shape() Item {
	kind := i.Kind
	// What somebody did is theirs to tell; to anybody else they were busy.
	if kind == "entry" {
		kind = "busy"
	}
	return Item{Kind: kind, StartsAt: i.StartsAt, EndsAt: i.EndsAt}
}

func overlaps(startA, endA, startB, endB time.Time) bool {
	return startA.Before(endB) && endA.After(startB)
}

// validateRange refuses a read of nothing or of far too much.
func validateRange(from, to time.Time) error {
	if !to.After(from) {
		return apperrors.New(apperrors.CodeBadRequest, "to must be after from")
	}
	if to.Sub(from) > maxRange {
		return apperrors.New(apperrors.CodeBadRequest, "a calendar read covers at most 92 days")
	}
	return nil
}

// Calendar is one person's items in a range, read in full: their own, or somebody
// they answer for (decision 64). Anybody else is refused, and finds a colleague's
// free time where it is needed, in Availability while scheduling.
//
// The detail is what the person themself sees, because a manager reads a report's
// calendar to know what their time goes to. It is still somebody else's calendar:
// nothing on it becomes the viewer's to change, which the write paths enforce by
// owner and organizer, not by this read.
func (s *Service) Calendar(ctx context.Context, orgID, viewerID, userID uuid.UUID, isOrgAdmin bool, from, to time.Time) ([]Item, error) {
	if err := validateRange(from, to); err != nil {
		return nil, err
	}
	var inOrg bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
		userID, orgID).Scan(&inOrg); err != nil {
		return nil, fmt.Errorf("find person: %w", err)
	}
	if !inOrg {
		return nil, apperrors.ErrNotFound
	}
	allowed, err := s.chart.HasAuthorityOver(ctx, orgID, viewerID, userID, isOrgAdmin)
	if err != nil {
		return nil, err
	}
	if !allowed {
		// Everybody in the organization can see this person is in it, so a 404
		// would hide nothing and only mislead.
		return nil, apperrors.New(apperrors.CodeForbidden, "you can only see your own calendar and those of the people you manage")
	}
	byPerson, err := s.loadItems(ctx, orgID, []uuid.UUID{userID}, from, to)
	if err != nil {
		return nil, err
	}
	items := byPerson[userID]
	for i := range items {
		// A manager reads what a report's time goes to, but not what Google
		// Calendar says is private, nor a way into the report's Google account.
		if items[i].Source == "google" && viewerID != userID {
			items[i].Link = ""
			if items[i].private {
				items[i].Title = ""
			}
		}
		if items[i].CallID != nil {
			readable, err := s.mayReadLog(ctx, orgID, viewerID, isOrgAdmin, items[i])
			if err != nil {
				return nil, err
			}
			items[i].CanReadLog = &readable
		}
	}
	return items, nil
}

// CalendarPerson is somebody whose calendar the viewer may open.
type CalendarPerson struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// CalendarGroup is one team of them, or, for an administrator, everybody else.
type CalendarGroup struct {
	TeamID *uuid.UUID       `json:"teamId,omitempty"`
	Name   string           `json:"name"`
	People []CalendarPerson `json:"people"`
}

// OthersGroup names the people an administrator may open who are on no team of
// theirs.
const OthersGroup = "Everybody else"

// Viewable is whose calendars, besides their own, this person may open: the
// people they answer for, by team and by the same rule as Calendar, and for
// somebody with meetings.all everybody else in the organization too. Only active
// people are offered; a deactivated person's calendar still opens by its link.
func (s *Service) Viewable(ctx context.Context, orgID, viewerID uuid.UUID, isOrgAdmin bool) ([]CalendarGroup, error) {
	teams, err := s.chart.Reports(ctx, orgID, viewerID)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, team := range teams {
		for _, m := range team.Members {
			ids = append(ids, m.UserID)
		}
	}
	active, err := s.activePeople(ctx, orgID, ids)
	if err != nil {
		return nil, err
	}
	isActive := make(map[uuid.UUID]bool, len(active))
	for _, id := range active {
		isActive[id] = true
	}

	groups := []CalendarGroup{}
	listed := map[uuid.UUID]bool{viewerID: true}
	for _, team := range teams {
		group := CalendarGroup{TeamID: &team.ID, Name: team.Name, People: []CalendarPerson{}}
		for _, m := range team.Members {
			if isActive[m.UserID] {
				listed[m.UserID] = true
				group.People = append(group.People, CalendarPerson{ID: m.UserID, Name: m.Name})
			}
		}
		if len(group.People) > 0 {
			groups = append(groups, group)
		}
	}
	if !isOrgAdmin {
		return groups, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, name FROM users
		WHERE org_id = $1 AND deleted_at IS NULL AND COALESCE(is_active, true)
		ORDER BY name, id
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	everybody, err := pgx.CollectRows(rows, pgx.RowToStructByPos[CalendarPerson])
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	rest := CalendarGroup{Name: OthersGroup, People: []CalendarPerson{}}
	for _, p := range everybody {
		if !listed[p.ID] {
			rest.People = append(rest.People, p)
		}
	}
	if len(rest.People) > 0 {
		groups = append(groups, rest)
	}
	return groups, nil
}

// mayReadLog is the call log's rule (decisions 11 and 27) for something already on
// a calendar this viewer may read in detail, which makes them a member of an
// ordinary call or somebody with authority over a member. A standing meeting has
// no members, so there it is an admin, or a manager over somebody who came.
func (s *Service) mayReadLog(ctx context.Context, orgID, viewerID uuid.UUID, isOrgAdmin bool, item Item) (bool, error) {
	if item.StandingMeetingID == nil || isOrgAdmin {
		return true, nil
	}
	return s.chart.AuthorityOverAny(ctx, orgID, viewerID, item.present)
}

// PersonAvailability is what scheduling needs to know of one person.
type PersonAvailability struct {
	UserID uuid.UUID `json:"userId"`
	Items  []Item    `json:"items"`
}

// Availability is for choosing a time, so it never carries a title, even for an
// admin. Somebody not in the organization is simply not in the answer.
func (s *Service) Availability(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID, from, to time.Time) ([]PersonAvailability, error) {
	if err := validateRange(from, to); err != nil {
		return nil, err
	}
	if len(userIDs) > maxPeople {
		return nil, apperrors.New(apperrors.CodeBadRequest, "too many people in one request")
	}
	inOrg, err := s.activePeople(ctx, orgID, userIDs)
	if err != nil {
		return nil, err
	}
	byPerson, err := s.loadItems(ctx, orgID, inOrg, from, to)
	if err != nil {
		return nil, err
	}
	people := make([]PersonAvailability, 0, len(inOrg))
	for _, id := range inOrg {
		items := make([]Item, 0, len(byPerson[id]))
		for _, item := range byPerson[id] {
			items = append(items, item.shape())
		}
		people = append(people, PersonAvailability{UserID: id, Items: items})
	}
	return people, nil
}

// activePeople keeps only the named people who are active in the organization,
// in the order given, once each.
func (s *Service) activePeople(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM users
		WHERE org_id = $1 AND id = ANY($2) AND deleted_at IS NULL AND COALESCE(is_active, true)
	`, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("resolve people: %w", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("resolve people: %w", err)
	}
	known := make(map[uuid.UUID]bool, len(found))
	for _, id := range found {
		known[id] = true
	}
	out := make([]uuid.UUID, 0, len(found))
	for _, id := range ids {
		if known[id] {
			known[id] = false
			out = append(out, id)
		}
	}
	return out, nil
}

// loadItems merges everything on these people's calendars, in a fixed number of
// queries however many people are asked about.
func (s *Service) loadItems(ctx context.Context, orgID uuid.UUID, people []uuid.UUID, from, to time.Time) (map[uuid.UUID][]Item, error) {
	out := make(map[uuid.UUID][]Item, len(people))
	if len(people) == 0 {
		return out, nil
	}
	if err := s.loadBlocks(ctx, orgID, people, from, to, out); err != nil {
		return nil, err
	}
	if err := s.loadGoogle(ctx, orgID, people, from, to, out); err != nil {
		return nil, err
	}
	attended, err := s.loadCalls(ctx, orgID, people, from, to, out)
	if err != nil {
		return nil, err
	}
	if err := s.loadStanding(ctx, orgID, people, from, to, attended, out); err != nil {
		return nil, err
	}
	for id := range out {
		items := out[id]
		sort.Slice(items, func(i, j int) bool {
			if !items[i].StartsAt.Equal(items[j].StartsAt) {
				return items[i].StartsAt.Before(items[j].StartsAt)
			}
			return items[i].ID < items[j].ID
		})
	}
	return out, nil
}

func (s *Service) loadBlocks(ctx context.Context, orgID uuid.UUID, people []uuid.UUID, from, to time.Time, out map[uuid.UUID][]Item) error {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, kind, title, details, starts_at, ends_at FROM calendar_blocks
		WHERE org_id = $1 AND user_id = ANY($2) AND starts_at < $4 AND ends_at > $3
	`, orgID, people, from, to)
	if err != nil {
		return fmt.Errorf("load blocks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, user uuid.UUID
		item := Item{Source: "block"}
		if err := rows.Scan(&id, &user, &item.Kind, &item.Title, &item.Details, &item.StartsAt, &item.EndsAt); err != nil {
			return fmt.Errorf("scan block: %w", err)
		}
		item.ID = id.String()
		out[user] = append(out[user], item)
	}
	return rows.Err()
}

// loadGoogle adds the meetings read from each person's Google Calendar.
func (s *Service) loadGoogle(ctx context.Context, orgID uuid.UUID, people []uuid.UUID, from, to time.Time, out map[uuid.UUID][]Item) error {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, google_id, title, starts_at, ends_at, private, html_link FROM google_calendar_events
		WHERE org_id = $1 AND user_id = ANY($2) AND starts_at < $4 AND ends_at > $3
	`, orgID, people, from, to)
	if err != nil {
		return fmt.Errorf("load google events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var user uuid.UUID
		var googleID string
		item := Item{Kind: "busy", Source: "google"}
		if err := rows.Scan(&user, &googleID, &item.Title, &item.StartsAt, &item.EndsAt, &item.private, &item.Link); err != nil {
			return fmt.Errorf("scan google event: %w", err)
		}
		item.ID = "google:" + googleID
		out[user] = append(out[user], item)
	}
	return rows.Err()
}

// loadCalls adds meetings and calls. It returns which standing meeting days each
// person really attended, because those replace the scheduled slot.
func (s *Service) loadCalls(ctx context.Context, orgID uuid.UUID, people []uuid.UUID, from, to time.Time, out map[uuid.UUID][]Item) (map[uuid.UUID]map[string]bool, error) {
	// The range here is padded, and the exact overlap is decided below once it is
	// known which times apply to the person.
	rows, err := s.db.Query(ctx, `
		SELECT p.user_id, c.id, COALESCE(c.title, ''), c.status, c.created_by, c.standing_meeting_id,
		       c.occurrence_date::text, c.scheduled_start, c.scheduled_end, c.started_at, c.ended_at,
		       p.joined_at, p.left_at,
		       ARRAY(SELECT i.user_id::text FROM call_participants i WHERE i.call_id = c.id ORDER BY i.user_id)
		FROM call_participants p
		JOIN calls c ON c.id = p.call_id
		WHERE c.org_id = $1 AND p.user_id = ANY($2) AND c.status <> 'cancelled'
		  AND COALESCE(p.joined_at, c.scheduled_start, c.started_at, c.created_at) < $4::timestamptz + interval '1 day'
		  AND COALESCE(p.left_at, c.ended_at, c.scheduled_end, NOW()) > $3::timestamptz - interval '1 day'
	`, orgID, people, from, to)
	if err != nil {
		return nil, fmt.Errorf("load calls: %w", err)
	}
	defer rows.Close()

	now := s.clock()
	attended := map[uuid.UUID]map[string]bool{}
	for rows.Next() {
		var (
			user, callID, organizer            uuid.UUID
			title, status                      string
			standingID                         *uuid.UUID
			occurrence                         *string
			scheduledStart, scheduledEnd       *time.Time
			startedAt, endedAt, joined, leftAt *time.Time
			invitees                           []string
		)
		if err := rows.Scan(&user, &callID, &title, &status, &organizer, &standingID, &occurrence,
			&scheduledStart, &scheduledEnd, &startedAt, &endedAt, &joined, &leftAt, &invitees); err != nil {
			return nil, fmt.Errorf("scan call: %w", err)
		}

		item := Item{ID: callID.String(), Kind: "busy", Title: title, CallID: &callID,
			StandingMeetingID: standingID, OrganizerID: &organizer, InviteeIDs: invitees, Status: status}
		switch {
		case standingID != nil:
			// Only somebody who came has a row here. Their real time replaces the slot.
			if joined == nil || occurrence == nil {
				continue
			}
			// That list of rows is who came, which is attendance's to tell (decisions
			// 15 and 27), and whoever pressed Join first organized nothing.
			item.present = textIDs(invitees)
			item.InviteeIDs, item.OrganizerID = nil, nil
			if attended[user] == nil {
				attended[user] = map[string]bool{}
			}
			attended[user][standingKey(*standingID, *occurrence)] = true
			item.Source = "standing"
			item.StartsAt, item.EndsAt = *joined, firstTime(now, leftAt, endedAt)
		case scheduledStart != nil:
			// A meeting stands at its scheduled time until that is over, and then
			// shows what really happened to whoever was really there. Its room
			// emptying earlier does not make it over: it comes back until its end
			// (decision 23), and somebody who looked in at 09:40 is still expected at
			// 10:00.
			item.Source = "meeting"
			item.StartsAt, item.EndsAt = *scheduledStart, *scheduledEnd
			if joined != nil && !now.Before(*scheduledEnd) {
				// Over, or running on past its end: what really happened to somebody
				// who was there. Still in it means until now, and it keeps growing.
				stillIn := status == "active" && leftAt == nil
				switch {
				case stillIn:
					item.StartsAt, item.EndsAt, item.Running = *joined, now, true
				case status == "ended" || leftAt != nil:
					item.StartsAt, item.EndsAt = *joined, firstTime(now, leftAt, endedAt)
				}
				if item.EndsAt.After(*scheduledEnd) {
					planned := *scheduledEnd
					item.ScheduledEndsAt = &planned
				}
			}
		default:
			item.Source = "call"
			switch {
			case joined != nil:
				item.StartsAt, item.EndsAt = *joined, firstTime(now, leftAt, endedAt)
			case startedAt != nil:
				// Somebody who never came was only ever being rung. A call nobody
				// closed must not make them busy from then until now.
				item.StartsAt, item.EndsAt = *startedAt, firstTime(now, endedAt)
				if rang := startedAt.Add(ringWindow); endedAt == nil && item.EndsAt.After(rang) {
					item.EndsAt = rang
				}
			default:
				continue
			}
		}
		if overlaps(item.StartsAt, item.EndsAt, from, to) {
			out[user] = append(out[user], item)
		}
	}
	return attended, rows.Err()
}

// textIDs reads ids that came out of the database as text.
func textIDs(raw []string) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(raw))
	for _, r := range raw {
		if id, err := uuid.Parse(r); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// firstTime is the first of these that is set, or the fallback.
func firstTime(fallback time.Time, times ...*time.Time) time.Time {
	for _, t := range times {
		if t != nil {
			return *t
		}
	}
	return fallback
}

func standingKey(id uuid.UUID, date string) string { return id.String() + "/" + date }

// loadStanding adds each standing meeting's slot on the days it runs since it was
// created, for everybody, unless the person really attended that day.
func (s *Service) loadStanding(ctx context.Context, orgID uuid.UUID, people []uuid.UUID, from, to time.Time, attended map[uuid.UUID]map[string]bool, out map[uuid.UUID][]Item) error {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, time_of_day, time_zone, weekdays, duration_minutes, created_at
		FROM standing_meetings WHERE org_id = $1 AND archived_at IS NULL
	`, orgID)
	if err != nil {
		return fmt.Errorf("load standing meetings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                uuid.UUID
			name, clock, zone string
			weekdays          []int16
			minutes           int
			createdAt         time.Time
		)
		if err := rows.Scan(&id, &name, &clock, &zone, &weekdays, &minutes, &createdAt); err != nil {
			return fmt.Errorf("scan standing meeting: %w", err)
		}
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return fmt.Errorf("load zone: %w", err)
		}
		timeOfDay, err := time.Parse("15:04", clock)
		if err != nil {
			return fmt.Errorf("parse time of day: %w", err)
		}
		first := createdAt.In(loc).Format("2006-01-02")
		// From the local date before the range to the one after it, because a slot
		// on either can overlap once the zone is taken into account.
		day := from.In(loc)
		for d := time.Date(day.Year(), day.Month(), day.Day()-1, 0, 0, 0, 0, loc); d.Before(to.Add(24 * time.Hour)); d = d.AddDate(0, 0, 1) {
			date := d.Format("2006-01-02")
			if date < first || !runsOn(weekdays, d.Weekday()) {
				continue
			}
			start := time.Date(d.Year(), d.Month(), d.Day(), timeOfDay.Hour(), timeOfDay.Minute(), 0, 0, loc)
			end := start.Add(time.Duration(minutes) * time.Minute)
			if !overlaps(start, end, from, to) {
				continue
			}
			for _, person := range people {
				if attended[person][standingKey(id, date)] {
					continue
				}
				standing := id
				out[person] = append(out[person], Item{
					ID: "standing:" + standingKey(id, date), Kind: "busy", Source: "standing", Title: name,
					StartsAt: start, EndsAt: end, StandingMeetingID: &standing,
				})
			}
		}
	}
	return rows.Err()
}

func runsOn(weekdays []int16, day time.Weekday) bool {
	iso := int16(day)
	if iso == 0 {
		iso = 7
	}
	for _, d := range weekdays {
		if d == iso {
			return true
		}
	}
	return false
}

// ── focus blocks and open slots ───────────────────────────────────────────

// Block is somebody's own time.
type Block struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	Details  string    `json:"details"`
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`
}

// maxDetails is what an entry may say beyond its name: notes from a meeting, not a document.
const maxDetails = 5000

func validateBlock(b *Block) error {
	if b.Kind != "focus" && b.Kind != "open" && b.Kind != "entry" {
		return apperrors.New(apperrors.CodeBadRequest, "kind must be focus, open or entry")
	}
	if len(b.Title) > 255 {
		return apperrors.New(apperrors.CodeBadRequest, "title is too long")
	}
	if b.Kind == "entry" && strings.TrimSpace(b.Title) == "" {
		return apperrors.New(apperrors.CodeBadRequest, "say what the entry is")
	}
	if b.Kind != "entry" {
		b.Details = ""
	}
	if len(b.Details) > maxDetails {
		return apperrors.New(apperrors.CodeBadRequest, "the details are too long")
	}
	if !b.EndsAt.After(b.StartsAt) {
		return apperrors.New(apperrors.CodeBadRequest, "a block must end after it starts")
	}
	if b.EndsAt.Sub(b.StartsAt) >= maxBlock {
		return apperrors.New(apperrors.CodeBadRequest, "a block must be shorter than thirty days")
	}
	return nil
}

func (s *Service) CreateBlock(ctx context.Context, orgID, userID uuid.UUID, b Block) (*Block, error) {
	if err := validateBlock(&b); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(ctx, `
		INSERT INTO calendar_blocks (org_id, user_id, kind, title, details, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id
	`, orgID, userID, b.Kind, b.Title, b.Details, b.StartsAt, b.EndsAt).Scan(&b.ID); err != nil {
		return nil, fmt.Errorf("create block: %w", err)
	}
	return &b, nil
}

// blockPatch holds what an edit sent. Empty fields keep what is stored.
type blockPatch struct {
	Kind, Title, Details *string
	StartsAt, EndsAt     *time.Time
}

// UpdateBlock is the owner's alone. Anybody else, an admin included, gets the same
// not-found as an id that does not exist.
func (s *Service) UpdateBlock(ctx context.Context, orgID, userID, id uuid.UUID, p blockPatch) (*Block, error) {
	var b Block
	err := s.db.QueryRow(ctx, `
		SELECT id, kind, title, details, starts_at, ends_at FROM calendar_blocks
		WHERE id = $1 AND org_id = $2 AND user_id = $3
	`, id, orgID, userID).Scan(&b.ID, &b.Kind, &b.Title, &b.Details, &b.StartsAt, &b.EndsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load block: %w", err)
	}
	if p.Kind != nil {
		b.Kind = *p.Kind
	}
	if p.Title != nil {
		b.Title = *p.Title
	}
	if p.Details != nil {
		b.Details = *p.Details
	}
	if p.StartsAt != nil {
		b.StartsAt = *p.StartsAt
	}
	if p.EndsAt != nil {
		b.EndsAt = *p.EndsAt
	}
	if err := validateBlock(&b); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE calendar_blocks SET kind = $2, title = $3, details = $4, starts_at = $5, ends_at = $6, updated_at = NOW()
		WHERE id = $1
	`, id, b.Kind, b.Title, b.Details, b.StartsAt, b.EndsAt); err != nil {
		return nil, fmt.Errorf("update block: %w", err)
	}
	return &b, nil
}

func (s *Service) DeleteBlock(ctx context.Context, orgID, userID, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM calendar_blocks WHERE id = $1 AND org_id = $2 AND user_id = $3`, id, orgID, userID)
	if err != nil {
		return fmt.Errorf("delete block: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

// ── scheduled meetings ────────────────────────────────────────────────────

// Meeting is a call scheduled ahead of time.
type Meeting struct {
	ID          uuid.UUID   `json:"id"`
	Title       string      `json:"title"`
	StartsAt    time.Time   `json:"startsAt"`
	EndsAt      time.Time   `json:"endsAt"`
	Status      string      `json:"status"`
	OrganizerID uuid.UUID   `json:"organizerId"`
	InviteeIDs  []uuid.UUID `json:"inviteeIds"`
}

// Conflict says somebody is taken at that time, and carries no title: the organizer
// may have no authority over them.
type Conflict struct {
	UserID uuid.UUID `json:"userId"`
	Kind   string    `json:"kind"` // focus or meeting
}

func validateMeeting(title string, start, end time.Time) error {
	if strings.TrimSpace(title) == "" || len(title) > 500 {
		return apperrors.New(apperrors.CodeBadRequest, "a meeting needs a title of at most 500 characters")
	}
	if !end.After(start) {
		return apperrors.New(apperrors.CodeBadRequest, "a meeting must end after it starts")
	}
	if end.Sub(start) > maxMeeting {
		return apperrors.New(apperrors.CodeBadRequest, "a meeting can be at most 24 hours")
	}
	return nil
}

// resolveInvitees refuses the whole list if anybody on it is not active in the
// organization, and takes the organizer off it: they are the host.
func (s *Service) resolveInvitees(ctx context.Context, orgID, organizer uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) > maxPeople {
		return nil, apperrors.New(apperrors.CodeBadRequest, "too many people on one meeting")
	}
	found, err := s.activePeople(ctx, orgID, ids)
	if err != nil {
		return nil, err
	}
	seen := make(map[uuid.UUID]bool, len(ids))
	unique := 0
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique++
		}
	}
	if len(found) != unique {
		return nil, apperrors.New(apperrors.CodeBadRequest, "everybody invited must be in this organization")
	}
	out := make([]uuid.UUID, 0, len(found))
	for _, id := range found {
		if id != organizer {
			out = append(out, id)
		}
	}
	return out, nil
}

// ScheduleMeeting puts a meeting on everybody's calendar. A conflict warns and does
// not refuse.
func (s *Service) ScheduleMeeting(ctx context.Context, orgID, organizer uuid.UUID, title string, start, end time.Time, inviteeIDs []uuid.UUID) (*Meeting, []Conflict, error) {
	title = strings.TrimSpace(title)
	if err := validateMeeting(title, start, end); err != nil {
		return nil, nil, err
	}
	invitees, err := s.resolveInvitees(ctx, orgID, organizer, inviteeIDs)
	if err != nil {
		return nil, nil, err
	}
	// Worked out before writing, so the meeting does not conflict with itself.
	conflicts, err := s.conflicts(ctx, orgID, append([]uuid.UUID{organizer}, invitees...), start, end, uuid.Nil)
	if err != nil {
		return nil, nil, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	m := &Meeting{ID: uuid.New(), Title: title, StartsAt: start, EndsAt: end, Status: "scheduled", OrganizerID: organizer, InviteeIDs: invitees}
	if _, err := tx.Exec(ctx, `
		INSERT INTO calls (id, org_id, room_name, title, call_type, status, created_by, scheduled_start, scheduled_end)
		VALUES ($1, $2, $3, $4, 'video', 'scheduled', $5, $6, $7)
	`, m.ID, orgID, "call-"+uuid.NewString(), title, organizer, start, end); err != nil {
		return nil, nil, fmt.Errorf("create meeting: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO call_participants (call_id, user_id, role) VALUES ($1, $2, 'host')`, m.ID, organizer); err != nil {
		return nil, nil, fmt.Errorf("add organizer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO call_participants (call_id, user_id, role)
		SELECT $1, invitee, 'participant' FROM UNNEST($2::uuid[]) AS invitee
	`, m.ID, invitees); err != nil {
		return nil, nil, fmt.Errorf("add invitees: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}

	s.notify(ctx, orgID, invitees, organizer, "meeting.invited", "You were invited to "+title, m)
	s.meetingsChanged([]uuid.UUID{organizer}, invitees)
	return m, conflicts, nil
}

// conflicts finds who is taken: an overlapping focus block or meeting, a standing
// meeting being one. An open slot is the opposite of a conflict, and touching end
// to start is not overlapping.
func (s *Service) conflicts(ctx context.Context, orgID uuid.UUID, people []uuid.UUID, start, end time.Time, except uuid.UUID) ([]Conflict, error) {
	byPerson, err := s.loadItems(ctx, orgID, people, start, end)
	if err != nil {
		return nil, err
	}
	out := []Conflict{}
	for _, person := range people {
		seen := map[string]bool{}
		for _, item := range byPerson[person] {
			if item.CallID != nil && *item.CallID == except {
				continue
			}
			kind := ""
			switch {
			case item.Kind == "focus":
				kind = "focus"
			// An entry is usually a meeting held somewhere else.
			case item.Source == "meeting" || item.Source == "standing" || item.Source == "google" || item.Kind == "entry":
				kind = "meeting"
			}
			if kind != "" && !seen[kind] {
				seen[kind] = true
				out = append(out, Conflict{UserID: person, Kind: kind})
			}
		}
	}
	return out, nil
}

// loadMeeting finds a meeting for changing it. Only its organizer or an org admin
// may: being invited, or managing somebody invited, is not enough.
func (s *Service) loadMeeting(ctx context.Context, orgID, id, actor uuid.UUID, isOrgAdmin bool) (*Meeting, error) {
	m := &Meeting{ID: id}
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(title, ''), scheduled_start, scheduled_end, status, created_by
		FROM calls
		WHERE id = $1 AND org_id = $2 AND scheduled_start IS NOT NULL AND standing_meeting_id IS NULL
	`, id, orgID).Scan(&m.Title, &m.StartsAt, &m.EndsAt, &m.Status, &m.OrganizerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load meeting: %w", err)
	}
	if m.OrganizerID != actor && !isOrgAdmin {
		return nil, apperrors.New(apperrors.CodeForbidden, "only the organizer or an admin can change a meeting")
	}
	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM call_participants WHERE call_id = $1 AND user_id <> $2 ORDER BY user_id`, id, m.OrganizerID)
	if err != nil {
		return nil, fmt.Errorf("load invitees: %w", err)
	}
	m.InviteeIDs, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("load invitees: %w", err)
	}
	return m, nil
}

// meetingPatch holds what an edit sent. Empty fields keep what is stored.
type meetingPatch struct {
	Title            *string
	StartsAt, EndsAt *time.Time
	InviteeIDs       *[]uuid.UUID
}

// errStarted is changing a meeting that is already under way or over.
var errStarted = apperrors.New(apperrors.CodeConflict, "this meeting has already started")

// UpdateMeeting moves or edits a meeting that has not started. A new start clears
// the claimed reminder, so the new time is reminded too. Changing only the end is
// no reason to remind anybody twice.
func (s *Service) UpdateMeeting(ctx context.Context, orgID, id, actor uuid.UUID, isOrgAdmin bool, p meetingPatch) (*Meeting, []Conflict, error) {
	m, err := s.loadMeeting(ctx, orgID, id, actor, isOrgAdmin)
	if err != nil {
		return nil, nil, err
	}
	if m.Status != "scheduled" {
		return nil, nil, errStarted
	}
	start, end := m.StartsAt, m.EndsAt
	if p.StartsAt != nil {
		start = *p.StartsAt
	}
	if p.EndsAt != nil {
		end = *p.EndsAt
	}
	moved := !start.Equal(m.StartsAt) || !end.Equal(m.EndsAt)
	title := m.Title
	if p.Title != nil {
		title = strings.TrimSpace(*p.Title)
	}
	if err := validateMeeting(title, start, end); err != nil {
		return nil, nil, err
	}

	before := m.InviteeIDs
	invitees := before
	if p.InviteeIDs != nil {
		if invitees, err = s.resolveEditedInvitees(ctx, orgID, m, *p.InviteeIDs); err != nil {
			return nil, nil, err
		}
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE calls
		SET title = $2, scheduled_start = $3, scheduled_end = $4, updated_at = NOW(),
		    reminded_for = CASE WHEN $5::boolean THEN NULL ELSE reminded_for END
		WHERE id = $1 AND status = 'scheduled'
	`, id, title, start, end, !start.Equal(m.StartsAt))
	if err != nil {
		return nil, nil, fmt.Errorf("update meeting: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, nil, errStarted
	}
	if p.InviteeIDs != nil {
		if _, err := tx.Exec(ctx,
			`DELETE FROM call_participants WHERE call_id = $1 AND user_id <> $2 AND NOT (user_id = ANY($3))`,
			id, m.OrganizerID, invitees); err != nil {
			return nil, nil, fmt.Errorf("remove invitees: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO call_participants (call_id, user_id, role)
			SELECT $1, invitee, 'participant' FROM UNNEST($2::uuid[]) AS invitee
			ON CONFLICT (call_id, user_id) DO NOTHING
		`, id, invitees); err != nil {
			return nil, nil, fmt.Errorf("add invitees: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}

	m.Title, m.StartsAt, m.EndsAt, m.InviteeIDs = title, start, end, invitees
	everybody := append([]uuid.UUID{m.OrganizerID}, invitees...)
	conflicts, err := s.conflicts(ctx, orgID, everybody, start, end, id)
	if err != nil {
		return nil, nil, err
	}

	added, stayed, removed := diffPeople(before, invitees)
	s.meetingsChanged([]uuid.UUID{m.OrganizerID}, invitees, removed)
	s.notify(ctx, orgID, added, actor, "meeting.invited", "You were invited to "+title, m)
	s.notify(ctx, orgID, removed, actor, "meeting.cancelled", title+" is off your calendar", m)
	if moved {
		// The organizer hears about it too when an admin moved their meeting.
		s.notify(ctx, orgID, append(stayed, m.OrganizerID), actor, "meeting.moved", title+" moved", m)
		if s.guests != nil {
			s.guests.GuestsMeetingChanged(ctx, orgID, id, false)
		}
	}
	return m, conflicts, nil
}

// resolveEditedInvitees is the list an edit sent. Somebody new has to be active,
// as when scheduling. Somebody already on the meeting who has since left drops off
// without a word, because the dialog sends back everybody it was showing, and the
// organizer has no way of knowing who that was: one person leaving the company must
// not stop a meeting from moving.
func (s *Service) resolveEditedInvitees(ctx context.Context, orgID uuid.UUID, m *Meeting, ids []uuid.UUID) ([]uuid.UUID, error) {
	onIt := make(map[uuid.UUID]bool, len(m.InviteeIDs))
	for _, id := range m.InviteeIDs {
		onIt[id] = true
	}
	var already, fresh []uuid.UUID
	for _, id := range ids {
		if onIt[id] {
			already = append(already, id)
		} else {
			fresh = append(fresh, id)
		}
	}
	added, err := s.resolveInvitees(ctx, orgID, m.OrganizerID, fresh)
	if err != nil {
		return nil, err
	}
	kept, err := s.activePeople(ctx, orgID, already)
	if err != nil {
		return nil, err
	}
	return append(kept, added...), nil
}

func diffPeople(before, after []uuid.UUID) (added, stayed, removed []uuid.UUID) {
	had := make(map[uuid.UUID]bool, len(before))
	for _, id := range before {
		had[id] = true
	}
	has := make(map[uuid.UUID]bool, len(after))
	for _, id := range after {
		has[id] = true
		if had[id] {
			stayed = append(stayed, id)
		} else {
			added = append(added, id)
		}
	}
	for _, id := range before {
		if !has[id] {
			removed = append(removed, id)
		}
	}
	return added, stayed, removed
}

// CancelMeeting takes a meeting off every calendar. Its row stays, so nothing about
// it is lost and a link to it says it was cancelled.
func (s *Service) CancelMeeting(ctx context.Context, orgID, id, actor uuid.UUID, isOrgAdmin bool) error {
	m, err := s.loadMeeting(ctx, orgID, id, actor, isOrgAdmin)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE calls SET status = 'cancelled', updated_at = NOW() WHERE id = $1 AND status = 'scheduled'`, id)
	if err != nil {
		return fmt.Errorf("cancel meeting: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errStarted
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE call_knock_requests SET status = 'ended', answered_at = $2
		WHERE call_id = $1 AND status = 'pending'
	`, id, s.clock()); err != nil {
		s.logger.Warn().Err(err).Msg("could not end requests on a cancelled meeting")
	}
	s.notify(ctx, orgID, append(m.InviteeIDs, m.OrganizerID), actor, "meeting.cancelled", m.Title+" was cancelled", m)
	s.meetingsChanged(m.InviteeIDs, []uuid.UUID{m.OrganizerID})
	if s.guests != nil {
		s.guests.GuestsMeetingChanged(ctx, orgID, id, true)
	}
	return nil
}

// notify tells people, leaving out whoever did it. A failed notification never
// undoes the scheduling.
func (s *Service) notify(ctx context.Context, orgID uuid.UUID, people []uuid.UUID, actor uuid.UUID, kind, title string, m *Meeting) {
	if s.notifier == nil {
		return
	}
	body := "On your calendar."
	if kind == "meeting.reminder" {
		body = "Starts in 5 minutes."
	}
	data := map[string]any{
		"callId":   m.ID.String(),
		"title":    m.Title,
		"startsAt": m.StartsAt.Format(time.RFC3339),
		"endsAt":   m.EndsAt.Format(time.RFC3339),
	}
	// Somebody who has left is told nothing, wherever the list came from.
	active, err := s.activePeople(ctx, orgID, people)
	if err != nil {
		s.logger.Warn().Err(err).Str("kind", kind).Msg("could not tell who to notify about a meeting")
		return
	}
	for _, person := range active {
		if person == actor {
			continue
		}
		if err := s.notifier.Notify(ctx, orgID, person, kind, title, body, data); err != nil {
			s.logger.Warn().Err(err).Str("kind", kind).Msg("could not notify about a meeting")
		}
	}
}

// SweepReminders sends what is due and returns how many went out.
//
// A meeting is claimed by the one UPDATE, which only one of several replicas can
// win for a given start, so a reminder goes out once. It is at-most-once: a replica
// dying between the claim and the send loses that reminder, which is better than
// everybody being told twice on every restart. A meeting already opened early is
// still reminded, since the others still need to know, and so is one whose room
// emptied again before its start: it comes back until its end (decision 23).
func (s *Service) SweepReminders(ctx context.Context, now time.Time) (int, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE calls SET reminded_for = scheduled_start
		WHERE status IN ('scheduled', 'active', 'ended') AND standing_meeting_id IS NULL
		  AND scheduled_start > $1 AND scheduled_start <= $2
		  AND reminded_for IS DISTINCT FROM scheduled_start
		RETURNING id, org_id, COALESCE(title, ''), scheduled_start, scheduled_end, created_by
	`, now, now.Add(ReminderLead))
	if err != nil {
		return 0, fmt.Errorf("claim reminders: %w", err)
	}
	type claimed struct {
		m     Meeting
		orgID uuid.UUID
	}
	var due []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.m.ID, &c.orgID, &c.m.Title, &c.m.StartsAt, &c.m.EndsAt, &c.m.OrganizerID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan reminder: %w", err)
		}
		due = append(due, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	sent := 0
	for _, c := range due {
		// Invited people and the organizer: the organizer has to be there as well.
		// UNION because the organizer is normally also a participant, and one
		// person gets one reminder.
		rows, err := s.db.Query(ctx,
			`SELECT p.user_id FROM (SELECT user_id FROM call_participants WHERE call_id = $1 UNION SELECT $2::uuid) p
			 JOIN users u ON u.id = p.user_id AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)`, c.m.ID, c.m.OrganizerID)
		if err != nil {
			return sent, fmt.Errorf("list people to remind: %w", err)
		}
		people, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return sent, fmt.Errorf("list people to remind: %w", err)
		}
		s.notify(ctx, c.orgID, people, uuid.Nil, "meeting.reminder", c.m.Title+" starts soon", &c.m)
		sent += len(people)
	}
	return sent, nil
}

// RunReminders sweeps until ctx ends. Every replica runs this; the claim is what
// keeps them from repeating each other.
func (s *Service) RunReminders(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.SweepReminders(ctx, s.clock()); err != nil && ctx.Err() == nil {
				s.logger.Warn().Err(err).Msg("could not send meeting reminders")
			}
		}
	}
}

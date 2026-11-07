package tasks

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// The level somebody answers at. The server decides it from who they are; a
// client can narrow it with a team filter and can never widen it.
const (
	scopeSelf = "self"
	scopeTeam = "team"
	scopeOrg  = "org"
)

// ScopeOrg is the level that sees the whole organization, for the packages that
// read AnswerableFor.
const ScopeOrg = scopeOrg

// AnswerableFor is who a caller answers for, and at what level, by the rule the
// dashboard follows. A team filter narrows it the same way too, and one outside
// the caller's own teams is not found.
func (s *Service) AnswerableFor(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID) (string, []uuid.UUID, error) {
	a, err := s.resolveAudience(ctx, orgID, callerID, isOrgAdmin, teamID)
	if err != nil {
		return "", nil, err
	}
	return a.Scope, a.ids(), nil
}

// doneFallbackWindow is how far back "done" reaches when no scrum has been saved
// for a scope yet, and always for somebody who only answers for themselves.
const doneFallbackWindow = 7 * 24 * time.Hour

// TeamRef names a team.
type TeamRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type audiencePerson struct {
	ID    uuid.UUID
	Name  string
	Teams []TeamRef
}

// audience is the people a caller answers for, and the teams they may narrow by.
type audience struct {
	Scope  string
	Teams  []TeamRef
	People []audiencePerson
}

func (a *audience) ids() []uuid.UUID {
	ids := make([]uuid.UUID, len(a.People))
	for i, p := range a.People {
		ids[i] = p.ID
	}
	return ids
}

func (a *audience) teamName(id uuid.UUID) string {
	for _, t := range a.Teams {
		if t.ID == id {
			return t.Name
		}
	}
	return ""
}

// answerableTeams lists the teams a caller answers for: every team for an
// organization admin, otherwise the ones they manage and those nested beneath.
func (s *Service) answerableTeams(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool) ([]TeamRef, error) {
	teams := []TeamRef{}
	if !isOrgAdmin {
		managed, err := s.ManagedTeams(ctx, orgID, callerID)
		if err != nil {
			return nil, err
		}
		for _, t := range managed {
			teams = append(teams, TeamRef{ID: t.ID, Name: t.Name})
		}
		return teams, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, name FROM teams WHERE org_id = $1 AND deleted_at IS NULL ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t TeamRef
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// resolveAudience answers "who does this caller answer for?" for the dashboard,
// scrums and contributions alike.
//
// Who outranks whom is not decided here: on a team scope the candidates are put
// through authorityFacts, the one statement of that rule. A team filter must be
// one of the caller's own teams and includes the teams nested beneath it. Somebody
// who answers only for themselves has nothing to narrow, so the filter is ignored.
func (s *Service) resolveAudience(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID) (*audience, error) {
	teams, err := s.answerableTeams(ctx, orgID, callerID, isOrgAdmin)
	if err != nil {
		return nil, err
	}

	a := &audience{Scope: scopeTeam, Teams: teams}
	switch {
	case isOrgAdmin:
		a.Scope = scopeOrg
	case len(teams) == 0:
		a.Scope = scopeSelf
		teamID = nil
	}

	roots := make([]uuid.UUID, 0, len(teams))
	for _, t := range teams {
		roots = append(roots, t.ID)
	}
	if teamID != nil {
		if a.teamName(*teamID) == "" {
			// Not found rather than refused, so a team id cannot be probed.
			return nil, apperrors.ErrNotFound
		}
		roots = []uuid.UUID{*teamID}
	}

	if a.Scope == scopeSelf {
		a.People, err = s.loadSelf(ctx, orgID, callerID)
	} else {
		a.People, err = s.loadTeamPeople(ctx, orgID, roots, a.Scope == scopeOrg && teamID == nil)
	}
	if err != nil {
		return nil, err
	}

	if a.Scope == scopeTeam {
		facts, err := s.authorityFacts(ctx, orgID, callerID, a.ids())
		if err != nil {
			return nil, err
		}
		kept := make([]audiencePerson, 0, len(a.People))
		for _, p := range a.People {
			if facts[p.ID].allows() {
				kept = append(kept, p)
			}
		}
		a.People = kept
	}
	return a, s.attachPersonTeams(ctx, orgID, a)
}

func (s *Service) loadSelf(ctx context.Context, orgID, callerID uuid.UUID) ([]audiencePerson, error) {
	p := audiencePerson{ID: callerID}
	err := s.db.QueryRow(ctx,
		`SELECT name FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`,
		callerID, orgID).Scan(&p.Name)
	if err == pgx.ErrNoRows {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load caller: %w", err)
	}
	return []audiencePerson{p}, nil
}

// loadTeamPeople lists the active people on the given teams and the teams nested
// beneath them, or everybody active in the organization.
func (s *Service) loadTeamPeople(ctx context.Context, orgID uuid.UUID, roots []uuid.UUID, everyone bool) ([]audiencePerson, error) {
	rows, err := s.db.Query(ctx, `
		WITH RECURSIVE reach AS (
			SELECT t.id FROM teams t
			WHERE t.org_id = $1 AND t.id = ANY($2) AND t.deleted_at IS NULL
			UNION
			SELECT child.id FROM teams child
			JOIN reach ON child.parent_id = reach.id
			WHERE child.org_id = $1 AND child.deleted_at IS NULL
		)
		SELECT u.id, u.name
		FROM users u
		WHERE u.org_id = $1 AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)
		  AND ($3::boolean OR EXISTS (
		      SELECT 1 FROM team_members tm
		      JOIN reach ON reach.id = tm.team_id
		      WHERE tm.user_id = u.id))
		ORDER BY u.name, u.id
	`, orgID, roots, everyone)
	if err != nil {
		return nil, fmt.Errorf("list people: %w", err)
	}
	defer rows.Close()

	people := []audiencePerson{}
	for rows.Next() {
		var p audiencePerson
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, fmt.Errorf("scan person: %w", err)
		}
		people = append(people, p)
	}
	return people, rows.Err()
}

// attachPersonTeams says which teams each person is on, in one query. A manager
// is told only about teams they answer for, not somebody's other memberships.
func (s *Service) attachPersonTeams(ctx context.Context, orgID uuid.UUID, a *audience) error {
	at := make(map[uuid.UUID]int, len(a.People))
	for i := range a.People {
		a.People[i].Teams = []TeamRef{}
		at[a.People[i].ID] = i
	}
	visible := make(map[uuid.UUID]bool, len(a.Teams))
	for _, t := range a.Teams {
		visible[t.ID] = true
	}

	rows, err := s.db.Query(ctx, `
		SELECT tm.user_id, t.id, t.name
		FROM team_members tm
		JOIN teams t ON t.id = tm.team_id
		WHERE tm.user_id = ANY($1) AND t.org_id = $2 AND t.deleted_at IS NULL
		ORDER BY t.name
	`, a.ids(), orgID)
	if err != nil {
		return fmt.Errorf("list person teams: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var userID uuid.UUID
		var t TeamRef
		if err := rows.Scan(&userID, &t.ID, &t.Name); err != nil {
			return fmt.Errorf("scan person team: %w", err)
		}
		if a.Scope != scopeTeam || visible[t.ID] {
			i := at[userID]
			a.People[i].Teams = append(a.People[i].Teams, t)
		}
	}
	return rows.Err()
}

// querier is what the pool and a transaction have in common, so a scrum can read
// the work it snapshots inside the transaction that saves it.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TaskSummary is what a dashboard needs of a task, without the description.
type TaskSummary struct {
	ID          uuid.UUID  `json:"id"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	Overdue     bool       `json:"overdue"`
	DueDate     *time.Time `json:"dueDate"`
	CompletedAt *time.Time `json:"completedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// summaryColumns selects what scanSummary reads, from a tasks table aliased t.
const summaryColumns = `t.id, t.title, t.status, t.priority,
	t.status <> 'done' AND t.due_date IS NOT NULL AND t.due_date < NOW(),
	t.due_date, t.completed_at, t.created_at`

func (t *TaskSummary) scanTargets() []any {
	return []any{&t.ID, &t.Title, &t.Status, &t.Priority, &t.Overdue, &t.DueDate, &t.CompletedAt, &t.CreatedAt}
}

// TaskCounts is how much work is in each bucket.
type TaskCounts struct {
	Pending   int `json:"pending"`
	Ongoing   int `json:"ongoing"`
	Blocked   int `json:"blocked"`
	Overdue   int `json:"overdue"`
	Completed int `json:"completed"`
	AddedTo   int `json:"addedTo"`
}

func (c *TaskCounts) add(other TaskCounts) {
	c.Pending += other.Pending
	c.Ongoing += other.Ongoing
	c.Blocked += other.Blocked
	c.Overdue += other.Overdue
	c.Completed += other.Completed
	c.AddedTo += other.AddedTo
}

// TaskBuckets files work by the spec's vocabulary. Overdue cuts across the others:
// a late pending task is in both Pending and Overdue. AddedTo stands apart: open
// tasks they are on and assigned to somebody else or to nobody, which is not their
// own work, so those are in no other bucket.
type TaskBuckets struct {
	Pending   []TaskSummary `json:"pending"`
	Ongoing   []TaskSummary `json:"ongoing"`
	Blocked   []TaskSummary `json:"blocked"`
	Overdue   []TaskSummary `json:"overdue"`
	Completed []TaskSummary `json:"completed"`
	AddedTo   []TaskSummary `json:"addedTo"`
}

func newTaskBuckets() *TaskBuckets {
	return &TaskBuckets{
		Pending:   []TaskSummary{},
		Ongoing:   []TaskSummary{},
		Blocked:   []TaskSummary{},
		Overdue:   []TaskSummary{},
		Completed: []TaskSummary{},
		AddedTo:   []TaskSummary{},
	}
}

func (b *TaskBuckets) file(t TaskSummary) {
	switch t.Status {
	case "todo":
		b.Pending = append(b.Pending, t)
	case "in_progress", "in_review":
		b.Ongoing = append(b.Ongoing, t)
	case "blocked":
		b.Blocked = append(b.Blocked, t)
	case "done":
		b.Completed = append(b.Completed, t)
	}
	if t.Overdue {
		b.Overdue = append(b.Overdue, t)
	}
}

func (b *TaskBuckets) counts() TaskCounts {
	return TaskCounts{
		Pending:   len(b.Pending),
		Ongoing:   len(b.Ongoing),
		Blocked:   len(b.Blocked),
		Overdue:   len(b.Overdue),
		Completed: len(b.Completed),
		AddedTo:   len(b.AddedTo),
	}
}

// lastScrumAt finds the last scrum saved for exactly this scope: that team, or the
// whole organization, or this manager's "everybody I answer for". Nobody saves a
// scrum for a self scope, so it has none.
func lastScrumAt(ctx context.Context, q querier, orgID, callerID uuid.UUID, scope string, teamID *uuid.UUID) (*time.Time, error) {
	var at *time.Time
	var err error
	switch {
	case scope == scopeSelf:
		return nil, nil
	case teamID != nil:
		err = q.QueryRow(ctx, `SELECT MAX(created_at) FROM scrums WHERE org_id = $1 AND team_id = $2`,
			orgID, *teamID).Scan(&at)
	case scope == scopeOrg:
		err = q.QueryRow(ctx, `SELECT MAX(created_at) FROM scrums
			WHERE org_id = $1 AND team_id IS NULL AND scope = 'org'`, orgID).Scan(&at)
	default:
		err = q.QueryRow(ctx, `SELECT MAX(created_at) FROM scrums
			WHERE org_id = $1 AND team_id IS NULL AND scope = 'team' AND held_by = $2`,
			orgID, callerID).Scan(&at)
	}
	if err != nil {
		return nil, fmt.Errorf("find last scrum: %w", err)
	}
	return at, nil
}

// doneSince is where "done" starts: the last saved scrum, not a rolling day, which
// would lose Friday's work on Monday.
func doneSince(last *time.Time) time.Time {
	if last != nil {
		return *last
	}
	return time.Now().Add(-doneFallbackWindow)
}

// loadWork reads everybody's open work and what they finished after since, in one
// query, newest completions first and then soonest due.
func loadWork(ctx context.Context, q querier, orgID uuid.UUID, people []uuid.UUID, since time.Time) (map[uuid.UUID]*TaskBuckets, error) {
	rows, err := q.Query(ctx, `
		SELECT t.assignee_id, `+summaryColumns+`
		FROM tasks t
		WHERE t.org_id = $1 AND t.assignee_id = ANY($2) AND t.deleted_at IS NULL
		  AND (t.status <> 'done' OR t.completed_at > $3)
		ORDER BY t.completed_at DESC NULLS LAST, t.due_date ASC NULLS LAST, t.created_at DESC, t.id
	`, orgID, people, since)
	if err != nil {
		return nil, fmt.Errorf("load work: %w", err)
	}
	defer rows.Close()

	work := make(map[uuid.UUID]*TaskBuckets, len(people))
	for _, id := range people {
		work[id] = newTaskBuckets()
	}
	for rows.Next() {
		var assignee uuid.UUID
		var t TaskSummary
		if err := rows.Scan(append([]any{&assignee}, t.scanTargets()...)...); err != nil {
			return nil, fmt.Errorf("scan work: %w", err)
		}
		work[assignee].file(t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	added, err := q.Query(ctx, `
		SELECT w.user_id, `+summaryColumns+`
		FROM task_watchers w
		JOIN tasks t ON t.id = w.task_id
		WHERE t.org_id = $1 AND w.user_id = ANY($2) AND t.deleted_at IS NULL
		  AND t.status <> 'done' AND t.assignee_id IS DISTINCT FROM w.user_id
		  AND w.added_by IS DISTINCT FROM w.user_id
		ORDER BY t.due_date ASC NULLS LAST, t.created_at DESC, t.id
	`, orgID, people)
	if err != nil {
		return nil, fmt.Errorf("load added-to work: %w", err)
	}
	defer added.Close()
	for added.Next() {
		var watcher uuid.UUID
		var t TaskSummary
		if err := added.Scan(append([]any{&watcher}, t.scanTargets()...)...); err != nil {
			return nil, fmt.Errorf("scan added-to work: %w", err)
		}
		work[watcher].AddedTo = append(work[watcher].AddedTo, t)
	}
	return work, added.Err()
}

// DashboardPerson is one person's summarised work.
type DashboardPerson struct {
	UserID            uuid.UUID    `json:"userId"`
	Name              string       `json:"name"`
	Teams             []TeamRef    `json:"teams"`
	Counts            TaskCounts   `json:"counts"`
	Tasks             *TaskBuckets `json:"tasks"`
	NewSinceLastScrum []uuid.UUID  `json:"newSinceLastScrum"`
}

// Dashboard is the task summary at the level the caller answers for.
type Dashboard struct {
	Scope       string            `json:"scope"`
	Teams       []TeamRef         `json:"teams"`
	Totals      TaskCounts        `json:"totals"`
	People      []DashboardPerson `json:"people"`
	LastScrumAt *time.Time        `json:"lastScrumAt"`
}

// Dashboard is computed live on each read. People with no work are listed too.
func (s *Service) Dashboard(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID) (*Dashboard, error) {
	a, err := s.resolveAudience(ctx, orgID, callerID, isOrgAdmin, teamID)
	if err != nil {
		return nil, err
	}
	last, err := lastScrumAt(ctx, s.db, orgID, callerID, a.Scope, teamID)
	if err != nil {
		return nil, err
	}
	since := doneSince(last)
	work, err := loadWork(ctx, s.db, orgID, a.ids(), since)
	if err != nil {
		return nil, err
	}

	d := &Dashboard{Scope: a.Scope, Teams: a.Teams, People: make([]DashboardPerson, 0, len(a.People)), LastScrumAt: last}
	if a.Scope == scopeSelf {
		d.Teams = []TeamRef{}
	}
	for _, p := range a.People {
		buckets := work[p.ID]
		entry := DashboardPerson{
			UserID: p.ID, Name: p.Name, Teams: p.Teams,
			Counts: buckets.counts(), Tasks: buckets, NewSinceLastScrum: []uuid.UUID{},
		}
		for _, open := range [][]TaskSummary{buckets.Pending, buckets.Ongoing, buckets.Blocked} {
			for _, t := range open {
				if t.CreatedAt.After(since) {
					entry.NewSinceLastScrum = append(entry.NewSinceLastScrum, t.ID)
				}
			}
		}
		d.Totals.add(entry.Counts)
		d.People = append(d.People, entry)
	}
	return d, nil
}

func isOrgAdmin(c *gin.Context) bool {
	return auth.GetUserFromContext(c).HasAnyRole("owner", "admin")
}

// optionalQueryUUID reads an optional id filter. A malformed one is refused rather
// than ignored, so a typo cannot quietly show the unfiltered list.
func optionalQueryUUID(c *gin.Context, key string) (*uuid.UUID, bool) {
	raw := c.Query(key)
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + key})
		return nil, false
	}
	return &id, true
}

func (h *Handler) Dashboard(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	teamID, ok := optionalQueryUUID(c, "teamId")
	if !ok {
		return
	}
	d, err := h.service.Dashboard(c.Request.Context(), cl.OrgID, cl.UserID, isOrgAdmin(c), teamID)
	if err != nil {
		httpx.Error(c, err, "failed to load dashboard")
		return
	}
	c.JSON(http.StatusOK, d)
}

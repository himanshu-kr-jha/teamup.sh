package tasks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// The scrum is a table, one row per person with their open tasks across it as
// Task 1, 2, 3. Whoever looks at it arranges both by hand, and the arrangement is
// theirs alone: it is kept per viewer, the people per view (a team filter, or
// everybody they answer for) and the tasks per person.
//
// What is stored is only ever a preference. The table is drawn from the live
// work, in the stored order where it says something and in the default order for
// anybody or anything it does not mention yet, so a stale entry costs nothing.

const (
	scrumOrderMaxPeople = 5000
	scrumOrderMaxTasks  = 2000
)

// ScrumOrder is how one viewer arranged the scrum table for one view: people top
// to bottom, and each person's tasks left to right.
type ScrumOrder struct {
	People []uuid.UUID               `json:"people"`
	Tasks  map[uuid.UUID][]uuid.UUID `json:"tasks"`
}

func (o *ScrumOrder) tooLong() bool {
	if o == nil {
		return false
	}
	if len(o.People) > scrumOrderMaxPeople || len(o.Tasks) > scrumOrderMaxPeople {
		return true
	}
	for _, ids := range o.Tasks {
		if len(ids) > scrumOrderMaxTasks {
			return true
		}
	}
	return false
}

// PersonRef names a person.
type PersonRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// keepKnown is ids in the order given, without repeats, and only those allowed.
func keepKnown(ids []uuid.UUID, allowed map[uuid.UUID]bool) []uuid.UUID {
	kept := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if allowed[id] && !seen[id] {
			seen[id] = true
			kept = append(kept, id)
		}
	}
	return kept
}

func (a *audience) idSet() map[uuid.UUID]bool {
	set := make(map[uuid.UUID]bool, len(a.People))
	for _, p := range a.People {
		set[p.ID] = true
	}
	return set
}

// scrumAudience is the audience of somebody who may run a scrum. Somebody who
// answers only for themselves has no scrum table to arrange.
func (s *Service) scrumAudience(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID) (*audience, error) {
	a, err := s.resolveAudience(ctx, orgID, callerID, isOrgAdmin, teamID)
	if err != nil {
		return nil, err
	}
	if a.Scope == scopeSelf {
		return nil, apperrors.New(apperrors.CodeForbidden, "only a team's manager or an organization admin runs a scrum")
	}
	return a, nil
}

// ScrumOrder is the caller's own arrangement for one view, limited to the people
// they can see in it now.
func (s *Service) ScrumOrder(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID) (*ScrumOrder, error) {
	a, err := s.scrumAudience(ctx, orgID, callerID, isOrgAdmin, teamID)
	if err != nil {
		return nil, err
	}
	visible := a.idSet()
	out := &ScrumOrder{People: []uuid.UUID{}, Tasks: map[uuid.UUID][]uuid.UUID{}}

	var people []uuid.UUID
	err = s.db.QueryRow(ctx, `
		SELECT people FROM scrum_people_orders
		WHERE org_id = $1 AND viewer_id = $2 AND team_id IS NOT DISTINCT FROM $3
	`, orgID, callerID, teamID).Scan(&people)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("load scrum people order: %w", err)
	}
	out.People = keepKnown(people, visible)

	rows, err := s.db.Query(ctx, `
		SELECT person_id, tasks FROM scrum_task_orders
		WHERE org_id = $1 AND viewer_id = $2 AND person_id = ANY($3)
	`, orgID, callerID, a.ids())
	if err != nil {
		return nil, fmt.Errorf("load scrum task orders: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var person uuid.UUID
		var tasks []uuid.UUID
		if err := rows.Scan(&person, &tasks); err != nil {
			return nil, fmt.Errorf("scan scrum task order: %w", err)
		}
		if len(tasks) > 0 {
			out.Tasks[person] = tasks
		}
	}
	return out, rows.Err()
}

// SetScrumPeopleOrder keeps the caller's order of people for one view. Anybody
// they cannot see in that view is dropped, not refused, so a person who left the
// team a moment ago does not turn a drag into an error.
func (s *Service) SetScrumPeopleOrder(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID, people []uuid.UUID) ([]uuid.UUID, error) {
	if len(people) > scrumOrderMaxPeople {
		return nil, apperrors.New(apperrors.CodeBadRequest, "too many people")
	}
	a, err := s.scrumAudience(ctx, orgID, callerID, isOrgAdmin, teamID)
	if err != nil {
		return nil, err
	}
	kept := keepKnown(people, a.idSet())
	if _, err := s.db.Exec(ctx, `
		INSERT INTO scrum_people_orders (org_id, viewer_id, team_id, people, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (viewer_id, team_id) DO UPDATE SET people = EXCLUDED.people, updated_at = NOW()
	`, orgID, callerID, teamID, kept); err != nil {
		return nil, fmt.Errorf("save scrum people order: %w", err)
	}
	return kept, nil
}

// SetScrumTaskOrder keeps the caller's order of one person's tasks. The person
// has to be somebody the caller answers for, and only their open work is kept.
func (s *Service) SetScrumTaskOrder(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, personID uuid.UUID, tasks []uuid.UUID) ([]uuid.UUID, error) {
	if len(tasks) > scrumOrderMaxTasks {
		return nil, apperrors.New(apperrors.CodeBadRequest, "too many tasks")
	}
	a, err := s.scrumAudience(ctx, orgID, callerID, isOrgAdmin, nil)
	if err != nil {
		return nil, err
	}
	if !a.idSet()[personID] {
		// Not found rather than refused, so who is where cannot be probed.
		return nil, apperrors.ErrNotFound
	}
	work, err := loadWork(ctx, s.db, orgID, []uuid.UUID{personID}, time.Now())
	if err != nil {
		return nil, err
	}
	open := map[uuid.UUID]bool{}
	for _, t := range work[personID].open() {
		open[t.ID] = true
	}
	kept := keepKnown(tasks, open)
	if _, err := s.db.Exec(ctx, `
		INSERT INTO scrum_task_orders (org_id, viewer_id, person_id, tasks, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (viewer_id, person_id) DO UPDATE SET tasks = EXCLUDED.tasks, updated_at = NOW()
	`, orgID, callerID, personID, kept); err != nil {
		return nil, fmt.Errorf("save scrum task order: %w", err)
	}
	return kept, nil
}

// open is everything still to do that the scrum table spreads across a row: their
// own work, then what they were added to. Overdue tasks are already in these.
func (b *TaskBuckets) open() []TaskSummary {
	own := make([]TaskSummary, 0, len(b.Pending)+len(b.Ongoing)+len(b.Blocked))
	own = append(append(append(own, b.Pending...), b.Ongoing...), b.Blocked...)
	added := append([]TaskSummary{}, b.AddedTo...)
	byDue(own)
	byDue(added)
	return append(own, added...)
}

// byDue is the default order of a row: soonest due first, undated last.
func byDue(tasks []TaskSummary) {
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i].DueDate, tasks[j].DueDate
		switch {
		case a != nil && b != nil && !a.Equal(*b):
			return a.Before(*b)
		case (a == nil) != (b == nil):
			return a != nil
		}
		return strings.ToLower(tasks[i].Title) < strings.ToLower(tasks[j].Title)
	})
}

// arranged puts items in the order given, then the rest in the order they came.
func arranged[T any](items []T, id func(T) uuid.UUID, order []uuid.UUID) []T {
	at := make(map[uuid.UUID]int, len(order))
	for i, o := range order {
		if _, dup := at[o]; !dup {
			at[o] = i
		}
	}
	out := append([]T{}, items...)
	sort.SliceStable(out, func(i, j int) bool {
		pi, iok := at[id(out[i])]
		pj, jok := at[id(out[j])]
		if iok && jok {
			return pi < pj
		}
		return iok && !jok
	})
	return out
}

// loadLeads is, for each person, the managers of every team they are on, not
// counting themselves: who they report to, as the scrum's My Lead column says.
func loadLeads(ctx context.Context, q querier, orgID uuid.UUID, people []uuid.UUID) (map[uuid.UUID][]PersonRef, error) {
	rows, err := q.Query(ctx, `
		SELECT tm.user_id, m.id, m.name
		FROM team_members tm
		JOIN teams t ON t.id = tm.team_id
		JOIN users m ON m.id = t.manager_id
		WHERE tm.user_id = ANY($1) AND t.org_id = $2 AND t.deleted_at IS NULL
		  AND m.org_id = $2 AND m.deleted_at IS NULL AND COALESCE(m.is_active, true)
		  AND m.id <> tm.user_id
		ORDER BY tm.user_id, t.name, m.name, m.id
	`, people, orgID)
	if err != nil {
		return nil, fmt.Errorf("load leads: %w", err)
	}
	defer rows.Close()
	leads := make(map[uuid.UUID][]PersonRef, len(people))
	for _, id := range people {
		leads[id] = []PersonRef{}
	}
	for rows.Next() {
		var person uuid.UUID
		var lead PersonRef
		if err := rows.Scan(&person, &lead.ID, &lead.Name); err != nil {
			return nil, fmt.Errorf("scan lead: %w", err)
		}
		dup := false
		for _, have := range leads[person] {
			dup = dup || have.ID == lead.ID
		}
		if !dup {
			leads[person] = append(leads[person], lead)
		}
	}
	return leads, rows.Err()
}

func leadNames(leads []PersonRef) []string {
	names := make([]string, len(leads))
	for i, l := range leads {
		names[i] = l.Name
	}
	return names
}

func (h *Handler) ScrumOrder(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	teamID, ok := optionalQueryUUID(c, "teamId")
	if !ok {
		return
	}
	order, err := h.service.ScrumOrder(c.Request.Context(), cl.OrgID, cl.UserID, isOrgAdmin(c), teamID)
	if err != nil {
		httpx.Error(c, err, "failed to load the scrum order")
		return
	}
	c.JSON(http.StatusOK, order)
}

func (h *Handler) SetScrumPeopleOrder(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	var req struct {
		TeamID *uuid.UUID  `json:"teamId"`
		People []uuid.UUID `json:"people" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "people must be a list of ids"})
		return
	}
	people, err := h.service.SetScrumPeopleOrder(c.Request.Context(), cl.OrgID, cl.UserID, isOrgAdmin(c), req.TeamID, req.People)
	if err != nil {
		httpx.Error(c, err, "failed to save the order")
		return
	}
	c.JSON(http.StatusOK, gin.H{"people": people})
}

func (h *Handler) SetScrumTaskOrder(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	personID, ok := pathUUID(c, "personId")
	if !ok {
		return
	}
	var req struct {
		Tasks []uuid.UUID `json:"tasks" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tasks must be a list of ids"})
		return
	}
	tasks, err := h.service.SetScrumTaskOrder(c.Request.Context(), cl.OrgID, cl.UserID, isOrgAdmin(c), personID, req.Tasks)
	if err != nil {
		httpx.Error(c, err, "failed to save the order")
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

package tasks

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// contributionDefaultRange is what a request with no from gets.
const contributionDefaultRange = 30 * 24 * time.Hour

// ContributionPerson is how much one person did and was part of in a range.
type ContributionPerson struct {
	UserID    uuid.UUID `json:"userId"`
	Name      string    `json:"name"`
	Teams     []TeamRef `json:"teams"`
	Completed int       `json:"completed"`
	Involved  int       `json:"involved"`
	Open      int       `json:"open"`
	AddedTo   int       `json:"addedTo"`
}

// Contributions is the per-person numbers for everybody the caller answers for.
type Contributions struct {
	Scope  string               `json:"scope"`
	Teams  []TeamRef            `json:"teams"`
	From   time.Time            `json:"from"`
	To     time.Time            `json:"to"`
	People []ContributionPerson `json:"people"`
}

// ContributionDetail is the tasks behind one person's numbers.
type ContributionDetail struct {
	UserID    uuid.UUID     `json:"userId"`
	Name      string        `json:"name"`
	From      time.Time     `json:"from"`
	To        time.Time     `json:"to"`
	Completed []TaskSummary `json:"completed"`
	Involved  []TaskSummary `json:"involved"`
	Open      []TaskSummary `json:"open"`
	AddedTo   []TaskSummary `json:"addedTo"`
}

// involvementCTE derives (user_id, task_id) pairs from existing data: they are or
// were the assignee, reported it, watch it or commented on it. UNION is what makes
// somebody who is all of those count once. A task belongs to the range if it was
// live during it: created by the end, and not finished before the start.
//
// Activity values are text, and "<nil>" for an unassign, so ids are compared as
// text rather than cast. Parameters: $1 org, $2 people, $3 from, $4 to.
const involvementCTE = `
	WITH live AS (
		SELECT t.id, t.assignee_id, t.reporter_id FROM tasks t
		WHERE t.org_id = $1 AND t.deleted_at IS NULL
		  AND t.created_at <= $4
		  AND (t.completed_at IS NULL OR t.completed_at >= $3)
	), involvement AS (
		SELECT live.assignee_id AS user_id, live.id AS task_id FROM live WHERE live.assignee_id = ANY($2::uuid[])
		UNION
		SELECT live.reporter_id, live.id FROM live WHERE live.reporter_id = ANY($2::uuid[])
		UNION
		SELECT w.user_id, w.task_id FROM task_watchers w
		JOIN live ON live.id = w.task_id WHERE w.user_id = ANY($2::uuid[])
		UNION
		SELECT c.user_id, c.task_id FROM task_comments c
		JOIN live ON live.id = c.task_id WHERE c.user_id = ANY($2::uuid[]) AND c.deleted_at IS NULL
		UNION
		SELECT p.user_id, a.task_id FROM task_activities a
		JOIN live ON live.id = a.task_id
		JOIN unnest($2::uuid[]) AS p(user_id) ON p.user_id::text IN (a.old_value, a.new_value)
		WHERE a.field = 'assigneeId'
	)`

// addedToJoin narrows a query following involvementCTE to the tasks somebody else
// put them on and they are not assigned to, in the same range. completed keeps meaning
// assigned, so one task is not credited as completed twice.
const addedToJoin = `
	FROM task_watchers w
	JOIN live ON live.id = w.task_id AND live.assignee_id IS DISTINCT FROM w.user_id
	JOIN tasks t ON t.id = w.task_id
	WHERE w.user_id = ANY($2::uuid[]) AND w.added_by IS DISTINCT FROM w.user_id`

// Contributions counts per concern across the whole set of people, not per person.
func (s *Service) Contributions(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID, from, to time.Time) (*Contributions, error) {
	a, err := s.resolveAudience(ctx, orgID, callerID, isOrgAdmin, teamID)
	if err != nil {
		return nil, err
	}
	out := &Contributions{Scope: a.Scope, Teams: a.Teams, From: from, To: to, People: make([]ContributionPerson, len(a.People))}
	if a.Scope == scopeSelf {
		out.Teams = []TeamRef{}
	}
	at := make(map[uuid.UUID]*ContributionPerson, len(a.People))
	for i, p := range a.People {
		out.People[i] = ContributionPerson{UserID: p.ID, Name: p.Name, Teams: p.Teams}
		at[p.ID] = &out.People[i]
	}

	rows, err := s.db.Query(ctx, `
		SELECT t.assignee_id,
		       COUNT(*) FILTER (WHERE t.status = 'done' AND t.completed_at >= $3 AND t.completed_at <= $4),
		       COUNT(*) FILTER (WHERE t.status <> 'done')
		FROM tasks t
		WHERE t.org_id = $1 AND t.assignee_id = ANY($2) AND t.deleted_at IS NULL
		GROUP BY t.assignee_id
	`, orgID, a.ids(), from, to)
	if err != nil {
		return nil, fmt.Errorf("count contributions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var completed, open int
		if err := rows.Scan(&id, &completed, &open); err != nil {
			return nil, fmt.Errorf("scan contributions: %w", err)
		}
		at[id].Completed, at[id].Open = completed, open
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	involved, err := s.db.Query(ctx, involvementCTE+`
		SELECT user_id, COUNT(*) FROM involvement GROUP BY user_id
	`, orgID, a.ids(), from, to)
	if err != nil {
		return nil, fmt.Errorf("count involvement: %w", err)
	}
	defer involved.Close()
	for involved.Next() {
		var id uuid.UUID
		var n int
		if err := involved.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan involvement: %w", err)
		}
		at[id].Involved = n
	}
	if err := involved.Err(); err != nil {
		return nil, err
	}

	added, err := s.db.Query(ctx, involvementCTE+`
		SELECT w.user_id, COUNT(*)`+addedToJoin+` GROUP BY w.user_id
	`, orgID, a.ids(), from, to)
	if err != nil {
		return nil, fmt.Errorf("count added-to: %w", err)
	}
	defer added.Close()
	for added.Next() {
		var id uuid.UUID
		var n int
		if err := added.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan added-to: %w", err)
		}
		at[id].AddedTo = n
	}
	return out, added.Err()
}

// ContributionDetail lists the tasks behind one person's numbers. Somebody the
// caller has no authority over reads exactly like somebody who does not exist.
func (s *Service) ContributionDetail(ctx context.Context, orgID, callerID, userID uuid.UUID, isOrgAdmin bool, from, to time.Time) (*ContributionDetail, error) {
	out := &ContributionDetail{UserID: userID, From: from, To: to,
		Completed: []TaskSummary{}, Involved: []TaskSummary{}, Open: []TaskSummary{}, AddedTo: []TaskSummary{}}
	err := s.db.QueryRow(ctx,
		`SELECT name FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`,
		userID, orgID).Scan(&out.Name)
	if err == pgx.ErrNoRows {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load person: %w", err)
	}
	allowed, err := s.HasAuthorityOver(ctx, orgID, callerID, userID, isOrgAdmin)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, apperrors.ErrNotFound
	}

	rows, err := s.db.Query(ctx, `
		SELECT `+summaryColumns+`
		FROM tasks t
		WHERE t.org_id = $1 AND t.assignee_id = $2 AND t.deleted_at IS NULL
		  AND (t.status <> 'done' OR (t.completed_at >= $3 AND t.completed_at <= $4))
		ORDER BY t.completed_at DESC NULLS LAST, t.due_date ASC NULLS LAST, t.created_at DESC, t.id
	`, orgID, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("list contribution tasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t TaskSummary
		if err := rows.Scan(t.scanTargets()...); err != nil {
			return nil, fmt.Errorf("scan contribution task: %w", err)
		}
		if t.Status == "done" {
			out.Completed = append(out.Completed, t)
		} else {
			out.Open = append(out.Open, t)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	involved, err := s.db.Query(ctx, involvementCTE+`
		SELECT `+summaryColumns+`
		FROM involvement i
		JOIN tasks t ON t.id = i.task_id
		ORDER BY t.created_at DESC, t.id
	`, orgID, []uuid.UUID{userID}, from, to)
	if err != nil {
		return nil, fmt.Errorf("list involvement: %w", err)
	}
	defer involved.Close()
	for involved.Next() {
		var t TaskSummary
		if err := involved.Scan(t.scanTargets()...); err != nil {
			return nil, fmt.Errorf("scan involvement task: %w", err)
		}
		out.Involved = append(out.Involved, t)
	}
	if err := involved.Err(); err != nil {
		return nil, err
	}

	added, err := s.db.Query(ctx, involvementCTE+`
		SELECT `+summaryColumns+addedToJoin+`
		ORDER BY t.created_at DESC, t.id
	`, orgID, []uuid.UUID{userID}, from, to)
	if err != nil {
		return nil, fmt.Errorf("list added-to: %w", err)
	}
	defer added.Close()
	for added.Next() {
		var t TaskSummary
		if err := added.Scan(t.scanTargets()...); err != nil {
			return nil, fmt.Errorf("scan added-to task: %w", err)
		}
		out.AddedTo = append(out.AddedTo, t)
	}
	return out, added.Err()
}

// contributionRange reads from and to, defaulting to the last thirty days.
func contributionRange(c *gin.Context) (time.Time, time.Time, bool) {
	to := time.Now().UTC()
	if raw := c.Query("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to"})
			return time.Time{}, time.Time{}, false
		}
		to = parsed
	}
	from := to.Add(-contributionDefaultRange)
	if raw := c.Query("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from"})
			return time.Time{}, time.Time{}, false
		}
		from = parsed
	}
	if from.After(to) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from must not be after to"})
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func (h *Handler) Contributions(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	teamID, ok := optionalQueryUUID(c, "teamId")
	if !ok {
		return
	}
	from, to, ok := contributionRange(c)
	if !ok {
		return
	}
	out, err := h.service.Contributions(c.Request.Context(), cl.OrgID, cl.UserID, isOrgAdmin(c), teamID, from, to)
	if err != nil {
		httpx.Error(c, err, "failed to load contributions")
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) ContributionDetail(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "userId")
	if !ok {
		return
	}
	from, to, ok := contributionRange(c)
	if !ok {
		return
	}
	out, err := h.service.ContributionDetail(c.Request.Context(), cl.OrgID, cl.UserID, userID, isOrgAdmin(c), from, to)
	if err != nil {
		httpx.Error(c, err, "failed to load contributions")
		return
	}
	c.JSON(http.StatusOK, out)
}

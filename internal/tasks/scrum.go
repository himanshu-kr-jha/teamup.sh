package tasks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

const (
	scrumNotesMaxLength = 5000
	scrumListDefault    = 20
	scrumListMax        = 100
)

// ScrumSummary is a saved scrum as listed in the history.
type ScrumSummary struct {
	ID               uuid.UUID  `json:"id"`
	TeamID           *uuid.UUID `json:"teamId"`
	TeamName         *string    `json:"teamName"`
	Scope            string     `json:"scope"`
	HeldByID         *uuid.UUID `json:"heldById"`
	HeldByName       string     `json:"heldByName"`
	Notes            string     `json:"notes"`
	DoneSince        time.Time  `json:"doneSince"`
	CreatedAt        time.Time  `json:"createdAt"`
	ParticipantCount int        `json:"participantCount"`
	EntryCount       int        `json:"entryCount"`
}

// ScrumParticipant is one person as they stood when the scrum was saved.
type ScrumParticipant struct {
	UserID uuid.UUID  `json:"userId"`
	Name   string     `json:"name"`
	Counts TaskCounts `json:"counts"`
	// Position is their row in the holder's table, from 0. Leads are the managers
	// of their teams. Both are null on a scrum saved before the table kept them.
	Position *int     `json:"position"`
	Leads    []string `json:"leads"`
}

// ScrumEntry is one task as it stood when the scrum was saved.
type ScrumEntry struct {
	UserID      uuid.UUID  `json:"userId"`
	TaskID      uuid.UUID  `json:"taskId"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	Overdue     bool       `json:"overdue"`
	DueDate     *time.Time `json:"dueDate"`
	CompletedAt *time.Time `json:"completedAt"`
	// AddedTo marks a task they were on and not assigned to when it was saved.
	AddedTo bool `json:"addedTo"`
	// Position is the task's column in the person's row, from 0, for open work
	// on a scrum that kept the order. Null for done work and older scrums.
	Position *int `json:"position"`
}

// Scrum is a saved scrum in full. It never changes once written.
type Scrum struct {
	ScrumSummary
	Participants []ScrumParticipant `json:"participants"`
	Entries      []ScrumEntry       `json:"entries"`
}

// SaveScrum writes the snapshot: everybody the caller answers for, with or without
// work, what each of them had on, who leads them, and the order the holder had the
// table in. Only somebody who answers for other people may hold one.
//
// The order is what the holder saw. Anybody or anything it does not mention, such
// as a task created a moment ago, goes after the rest, so nothing is left out.
func (s *Service) SaveScrum(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID, notes string, order *ScrumOrder) (*Scrum, error) {
	if len(notes) > scrumNotesMaxLength {
		return nil, apperrors.New(apperrors.CodeBadRequest, "notes are too long")
	}
	if order.tooLong() {
		return nil, apperrors.New(apperrors.CodeBadRequest, "that order is too long")
	}
	if order == nil {
		order = &ScrumOrder{}
	}
	a, err := s.resolveAudience(ctx, orgID, callerID, isOrgAdmin, teamID)
	if err != nil {
		return nil, err
	}
	if a.Scope == scopeSelf {
		return nil, apperrors.New(apperrors.CodeForbidden, "only a team's manager or an organization admin can complete a scrum")
	}
	var teamName *string
	if teamID != nil {
		name := a.teamName(*teamID)
		teamName = &name
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	last, err := lastScrumAt(ctx, tx, orgID, callerID, a.Scope, teamID)
	if err != nil {
		return nil, err
	}
	since := doneSince(last)

	// created_at is the transaction's start and the work is read after it, so
	// anything finished while this runs is at worst listed twice, never lost
	// between this scrum and the next.
	id := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO scrums (id, org_id, team_id, team_name, scope, held_by, held_by_name, notes, done_since, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, (SELECT name FROM users WHERE id = $6::uuid), $7, $8, NOW())
	`, id, orgID, teamID, teamName, a.Scope, callerID, notes, since); err != nil {
		return nil, fmt.Errorf("save scrum: %w", err)
	}

	work, err := loadWork(ctx, tx, orgID, a.ids(), since)
	if err != nil {
		return nil, err
	}
	leads, err := loadLeads(ctx, tx, orgID, a.ids())
	if err != nil {
		return nil, err
	}
	people := arranged(a.People, func(p audiencePerson) uuid.UUID { return p.ID }, order.People)
	if err := insertScrumRows(ctx, tx, id, people, work, leads, order.Tasks); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.loadScrum(ctx, orgID, id)
}

// insertScrumRows copies people in the order given, and each person's open work in
// the order taskOrder gives for them; done work keeps no position.
func insertScrumRows(ctx context.Context, tx pgx.Tx, scrumID uuid.UUID, people []audiencePerson, work map[uuid.UUID]*TaskBuckets,
	leads map[uuid.UUID][]PersonRef, taskOrder map[uuid.UUID][]uuid.UUID) error {
	participants := make([][]any, 0, len(people))
	entries := [][]any{}
	for row, p := range people {
		buckets := work[p.ID]
		n := buckets.counts()
		participants = append(participants, []any{scrumID, p.ID, p.Name, n.Pending, n.Ongoing, n.Blocked, n.Overdue, n.Completed, n.AddedTo,
			row, leadNames(leads[p.ID])})
		added := make(map[uuid.UUID]bool, len(buckets.AddedTo))
		for _, t := range buckets.AddedTo {
			added[t.ID] = true
		}
		// Overdue is left out: every overdue task is also in one of the others.
		open := arranged(buckets.open(), func(t TaskSummary) uuid.UUID { return t.ID }, taskOrder[p.ID])
		for col, t := range open {
			entries = append(entries, []any{scrumID, p.ID, t.ID, t.Title, t.Status, t.Priority, t.Overdue, t.DueDate, t.CompletedAt, added[t.ID], col})
		}
		for _, t := range buckets.Completed {
			entries = append(entries, []any{scrumID, p.ID, t.ID, t.Title, t.Status, t.Priority, t.Overdue, t.DueDate, t.CompletedAt, false, nil})
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"scrum_participants"},
		[]string{"scrum_id", "user_id", "name", "pending", "ongoing", "blocked", "overdue", "completed", "added_to", "position", "leads"},
		pgx.CopyFromRows(participants)); err != nil {
		return fmt.Errorf("save scrum participants: %w", err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"scrum_entries"},
		[]string{"scrum_id", "user_id", "task_id", "title", "status", "priority", "overdue", "due_date", "completed_at", "added_to", "position"},
		pgx.CopyFromRows(entries)); err != nil {
		return fmt.Errorf("save scrum entries: %w", err)
	}
	return nil
}

const scrumSummaryColumns = `s.id, s.team_id, s.team_name, s.scope, s.held_by, s.held_by_name, s.notes,
	s.done_since, s.created_at,
	(SELECT COUNT(*) FROM scrum_participants p WHERE p.scrum_id = s.id),
	(SELECT COUNT(*) FROM scrum_entries e WHERE e.scrum_id = s.id)`

func scanScrumSummary(row pgx.Row) (ScrumSummary, error) {
	var r ScrumSummary
	err := row.Scan(&r.ID, &r.TeamID, &r.TeamName, &r.Scope, &r.HeldByID, &r.HeldByName, &r.Notes,
		&r.DoneSince, &r.CreatedAt, &r.ParticipantCount, &r.EntryCount)
	// UTC so createdAt can go straight back as the before cursor in a URL.
	r.DoneSince, r.CreatedAt = r.DoneSince.UTC(), r.CreatedAt.UTC()
	return r, err
}

// mayReadScrum is the rule for who may read a saved scrum, with $1 the organization
// and $2 to $4 what scrumReaderArgs returns.
const mayReadScrum = `s.org_id = $1 AND ($2::boolean OR s.held_by = $3 OR s.team_id = ANY($4))`

// scrumReaderArgs resolves the caller's side of mayReadScrum: an organization admin,
// whoever held it, or the manager of its team.
func (s *Service) scrumReaderArgs(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool) ([]any, error) {
	teams, err := s.answerableTeams(ctx, orgID, callerID, isOrgAdmin)
	if err != nil {
		return nil, err
	}
	teamIDs := make([]uuid.UUID, 0, len(teams))
	for _, t := range teams {
		teamIDs = append(teamIDs, t.ID)
	}
	return []any{orgID, isOrgAdmin, callerID, teamIDs}, nil
}

// ListScrums returns the saved scrums this caller may read, newest first.
func (s *Service) ListScrums(ctx context.Context, orgID, callerID uuid.UUID, isOrgAdmin bool, teamID *uuid.UUID, before *time.Time, limit int) ([]ScrumSummary, error) {
	args, err := s.scrumReaderArgs(ctx, orgID, callerID, isOrgAdmin)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + scrumSummaryColumns + ` FROM scrums s WHERE ` + mayReadScrum
	if teamID != nil {
		args = append(args, *teamID)
		query += fmt.Sprintf(` AND s.team_id = $%d`, len(args))
	}
	if before != nil {
		args = append(args, *before)
		query += fmt.Sprintf(` AND s.created_at < $%d`, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY s.created_at DESC, s.id LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list scrums: %w", err)
	}
	defer rows.Close()

	scrums := []ScrumSummary{}
	for rows.Next() {
		r, err := scanScrumSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan scrum: %w", err)
		}
		scrums = append(scrums, r)
	}
	return scrums, rows.Err()
}

// GetScrum returns one saved scrum. Somebody who may not read it gets the same
// not-found as an id that does not exist, so ids cannot be probed.
func (s *Service) GetScrum(ctx context.Context, orgID, callerID, id uuid.UUID, isOrgAdmin bool) (*Scrum, error) {
	args, err := s.scrumReaderArgs(ctx, orgID, callerID, isOrgAdmin)
	if err != nil {
		return nil, err
	}
	var readable bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM scrums s WHERE `+mayReadScrum+` AND s.id = $5)`,
		append(args, id)...).Scan(&readable); err != nil {
		return nil, fmt.Errorf("check scrum: %w", err)
	}
	if !readable {
		return nil, apperrors.ErrNotFound
	}
	return s.loadScrum(ctx, orgID, id)
}

// loadScrum reads a scrum and its copied rows, in an order that only depends on
// what was copied, so the same scrum always reads back the same: the holder's
// order where it was kept, and by name and title before that.
func (s *Service) loadScrum(ctx context.Context, orgID, id uuid.UUID) (*Scrum, error) {
	summary, err := scanScrumSummary(s.db.QueryRow(ctx,
		`SELECT `+scrumSummaryColumns+` FROM scrums s WHERE s.id = $1 AND s.org_id = $2`, id, orgID))
	if err == pgx.ErrNoRows {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load scrum: %w", err)
	}
	out := &Scrum{ScrumSummary: summary, Participants: []ScrumParticipant{}, Entries: []ScrumEntry{}}

	rows, err := s.db.Query(ctx, `
		SELECT user_id, name, pending, ongoing, blocked, overdue, completed, added_to, position, leads
		FROM scrum_participants WHERE scrum_id = $1 ORDER BY position NULLS LAST, name, user_id`, id)
	if err != nil {
		return nil, fmt.Errorf("load scrum participants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p ScrumParticipant
		n := &p.Counts
		if err := rows.Scan(&p.UserID, &p.Name, &n.Pending, &n.Ongoing, &n.Blocked, &n.Overdue, &n.Completed, &n.AddedTo, &p.Position, &p.Leads); err != nil {
			return nil, fmt.Errorf("scan scrum participant: %w", err)
		}
		out.Participants = append(out.Participants, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	entries, err := s.db.Query(ctx, `
		SELECT user_id, task_id, title, status, priority, overdue, due_date, completed_at, added_to, position
		FROM scrum_entries WHERE scrum_id = $1 ORDER BY position NULLS LAST, title, task_id, user_id`, id)
	if err != nil {
		return nil, fmt.Errorf("load scrum entries: %w", err)
	}
	defer entries.Close()
	for entries.Next() {
		var e ScrumEntry
		if err := entries.Scan(&e.UserID, &e.TaskID, &e.Title, &e.Status, &e.Priority, &e.Overdue, &e.DueDate, &e.CompletedAt, &e.AddedTo, &e.Position); err != nil {
			return nil, fmt.Errorf("scan scrum entry: %w", err)
		}
		out.Entries = append(out.Entries, e)
	}
	return out, entries.Err()
}

func (h *Handler) SaveScrum(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	var req struct {
		TeamID *string `json:"teamId"`
		Notes  string  `json:"notes"`
		// The order the holder had the table in. Optional.
		Order *ScrumOrder `json:"order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	teamID, err := optionalUUID(req.TeamID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid teamId"})
		return
	}
	scrum, err := h.service.SaveScrum(c.Request.Context(), cl.OrgID, cl.UserID, isOrgAdmin(c), teamID, req.Notes, req.Order)
	if err != nil {
		httpx.Error(c, err, "failed to save scrum")
		return
	}
	c.JSON(http.StatusCreated, scrum)
}

func (h *Handler) ListScrums(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	teamID, ok := optionalQueryUUID(c, "teamId")
	if !ok {
		return
	}
	limit := scrumListDefault
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
			return
		}
		limit = min(parsed, scrumListMax)
	}
	var before *time.Time
	if raw := c.Query("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid before"})
			return
		}
		before = &parsed
	}
	scrums, err := h.service.ListScrums(c.Request.Context(), cl.OrgID, cl.UserID, isOrgAdmin(c), teamID, before, limit)
	if err != nil {
		httpx.Error(c, err, "failed to list scrums")
		return
	}
	c.JSON(http.StatusOK, gin.H{"scrums": scrums})
}

func (h *Handler) GetScrum(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	scrum, err := h.service.GetScrum(c.Request.Context(), cl.OrgID, cl.UserID, id, isOrgAdmin(c))
	if err != nil {
		httpx.Error(c, err, "scrum not found")
		return
	}
	c.JSON(http.StatusOK, scrum)
}

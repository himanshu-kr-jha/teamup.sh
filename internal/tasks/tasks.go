package tasks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	apperrors "github.com/teamup/teamup/pkg/errors"
	"github.com/teamup/teamup/pkg/protocol"
)

// Notifier raises a notification for a user. Kept as an interface so tasks
// does not depend on the notifications package.
type Notifier interface {
	Notify(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, data map[string]any) error
}

type Service struct {
	db          *pgxpool.Pool
	logger      zerolog.Logger
	notifier    Notifier
	broadcaster Broadcaster
}

func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

// Broadcaster delivers a message to somebody's open sessions. An interface so
// tasks does not depend on the websocket package.
type Broadcaster interface {
	BroadcastToUser(userID string, msg *protocol.Message)
}

// SetBroadcaster wires live updates. Without one, a comment still saves; it
// just does not appear until the page is next loaded.
func (s *Service) SetBroadcaster(b Broadcaster) { s.broadcaster = b }

// notifyAssignee tells someone they picked up work. Best effort: a failed
// notification must not fail the assignment that caused it, and nobody is
// notified about their own action.
func (s *Service) notifyAssignee(ctx context.Context, orgID, actorID uuid.UUID, task *Task) {
	if s.notifier == nil || task.AssigneeID == nil || *task.AssigneeID == actorID {
		return
	}
	if err := s.notifier.Notify(ctx, orgID, *task.AssigneeID, "task.assigned",
		"You were assigned a task", task.Title,
		map[string]any{"taskId": task.ID.String()}); err != nil {
		s.logger.Warn().Err(err).Msg("assignment notification failed")
	}
}

type Task struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"orgId"`
	ProjectID   *uuid.UUID `json:"projectId,omitempty"`
	ProjectName string     `json:"projectName,omitempty"`
	// Key names the task where it is talked about: PHO-12. A branch, commit or
	// pull request that says it is linked to the task.
	Key          string     `json:"key"`
	ReporterID   uuid.UUID  `json:"reporterId"`
	ReporterName string     `json:"reporterName,omitempty"`
	AssigneeID   *uuid.UUID `json:"assigneeId,omitempty"`
	AssigneeName string     `json:"assigneeName,omitempty"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Status       string     `json:"status"`
	Priority     string     `json:"priority"`
	TaskType     string     `json:"taskType"`
	StoryPoints  *int       `json:"storyPoints,omitempty"`
	DueDate      *time.Time `json:"dueDate,omitempty"`
	StartDate    *time.Time `json:"startDate,omitempty"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
	ParentTaskID *uuid.UUID `json:"parentTaskId,omitempty"`
	Position     int        `json:"position"`
	Labels       []Label    `json:"labels"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	// Can says what the caller who asked for this task may do to it. Nil when
	// nobody asked in particular — an internal read rather than a board.
	Can *Abilities `json:"can,omitempty"`
}

// Abilities is what one caller may do to one task.
type Abilities struct {
	Edit     bool   `json:"edit"`
	Reassign bool   `json:"reassign"`
	Delete   bool   `json:"delete"`
	Reason   string `json:"reason,omitempty"`
}

type Label struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"orgId"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"createdAt"`
}

type TaskComment struct {
	ID          uuid.UUID    `json:"id"`
	TaskID      uuid.UUID    `json:"taskId"`
	UserID      uuid.UUID    `json:"userId"`
	UserName    string       `json:"userName,omitempty"`
	Content     string       `json:"content"`
	Attachments []Attachment `json:"attachments"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   *time.Time   `json:"updatedAt,omitempty"`
}

// Attachment is a file shared on a comment.
type Attachment struct {
	ID          uuid.UUID `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	SizeBytes   int64     `json:"sizeBytes"`
	ObjectKey   string    `json:"objectKey"`
}

type TaskActivity struct {
	ID     uuid.UUID `json:"id"`
	TaskID uuid.UUID `json:"taskId"`
	// Nil when nobody in TeamUp is known to have done it, as when GitHub moved
	// the task for somebody who has not linked their account.
	UserID   *uuid.UUID `json:"userId,omitempty"`
	UserName string     `json:"userName,omitempty"`
	// github or bitbucket when the change came from there; empty for a person's own.
	Source    string    `json:"source,omitempty"`
	Field     string    `json:"field"`
	OldValue  string    `json:"oldValue"`
	NewValue  string    `json:"newValue"`
	CreatedAt time.Time `json:"createdAt"`
}

// Filters narrows a board query. Every field is optional.
type Filters struct {
	Status     string
	AssigneeID *uuid.UUID
	ProjectID  *uuid.UUID
	LabelID    *uuid.UUID
	Search     string
	// ManagedBy narrows the list to work assigned to somebody on a team this
	// user manages, so a manager can see what their people are doing.
	ManagedBy *uuid.UUID
	// TeamID narrows further to a single one of those teams.
	TeamID *uuid.UUID
	// Mine narrows to one person's tasks, in the sense MineAs says:
	//   "" (assigned): the work that is theirs to do — assigned to them, or
	//     raised by them and not handed to anybody yet, which would otherwise
	//     vanish from their list the moment they created it;
	//   "created": work they raised and handed to somebody else;
	//   "added": tasks they were put on to follow.
	// These were one list, so a manager's "My tasks" filled up with the work
	// they had handed out, under their own name.
	Mine   *uuid.UUID
	MineAs string
	// Viewer is who the board is being drawn for. Not a filter: it narrows
	// nothing, it decides what each returned task says about what this caller
	// may do to it.
	Viewer           *uuid.UUID
	ViewerIsOrgAdmin bool
}

// What Filters.MineAs may say, besides the default of assigned.
const (
	MineCreated = "created"
	MineAdded   = "added"
)

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// SetLogger wires real logging. Left at the zero value every diagnostic in
// this package was written to a nil writer and silently discarded — including
// the fail-closed paths that swallow an error and deny access.
func (s *Service) SetLogger(l zerolog.Logger) {
	s.logger = l.With().Str("component", "tasks").Logger()
}

// taskColumns keeps the reporter and assignee names alongside the row so a
// board can render without a lookup per card.
const taskColumns = `
	SELECT t.id, t.org_id, t.project_id, COALESCE(prj.name, ''), COALESCE(t.task_key, ''), t.reporter_id, rep.name,
	       t.assignee_id, COALESCE(asg.name, ''), t.title, COALESCE(t.description, ''),
	       t.status, t.priority, t.task_type, t.story_points,
	       t.due_date, t.start_date, t.completed_at, t.parent_task_id,
	       COALESCE(t.position, 0), t.created_at, t.updated_at
	FROM tasks t
	JOIN users rep ON rep.id = t.reporter_id
	LEFT JOIN users asg ON asg.id = t.assignee_id
	LEFT JOIN projects prj ON prj.id = t.project_id`

func scanTask(row pgx.Row) (*Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.OrgID, &t.ProjectID, &t.ProjectName, &t.Key, &t.ReporterID, &t.ReporterName,
		&t.AssigneeID, &t.AssigneeName, &t.Title, &t.Description,
		&t.Status, &t.Priority, &t.TaskType, &t.StoryPoints,
		&t.DueDate, &t.StartDate, &t.CompletedAt, &t.ParentTaskID,
		&t.Position, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	t.Labels = []Label{}
	return &t, nil
}

// maxListedTasks bounds one list response. The board refetches on focus and on
// a timer, so an organization's whole history must not come back every time.
// Far more than a team of 50 has open at once.
const maxListedTasks = 1000

func (s *Service) ListTasks(ctx context.Context, orgID uuid.UUID, f Filters) ([]Task, error) {
	where := []string{"t.org_id = $1", "t.deleted_at IS NULL"}
	args := []any{orgID}

	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.Status != "" && f.Status != "all" {
		add("t.status = $%d", f.Status)
	}
	if f.AssigneeID != nil {
		add("t.assignee_id = $%d", *f.AssigneeID)
	}
	if f.ProjectID != nil {
		add("t.project_id = $%d", *f.ProjectID)
	}
	if f.Search != "" {
		add("t.title ILIKE '%%' || $%d || '%%'", f.Search)
	}
	if f.LabelID != nil {
		add("EXISTS (SELECT 1 FROM task_labels tl WHERE tl.task_id = t.id AND tl.label_id = $%d)", *f.LabelID)
	}
	if f.Mine != nil {
		args = append(args, *f.Mine)
		n := len(args)
		switch f.MineAs {
		case MineCreated:
			where = append(where, fmt.Sprintf(
				`(t.reporter_id = $%d AND t.assignee_id IS NOT NULL AND t.assignee_id <> $%d)`, n, n))
		case MineAdded:
			where = append(where, fmt.Sprintf(
				`EXISTS (SELECT 1 FROM task_watchers tw WHERE tw.task_id = t.id AND tw.user_id = $%d)`, n))
		default:
			where = append(where, fmt.Sprintf(
				`(t.assignee_id = $%d OR (t.reporter_id = $%d AND t.assignee_id IS NULL))`, n, n))
		}
	}
	if f.ManagedBy != nil {
		// Walks down from every team this person manages, so a team lead who
		// also runs a sub-team of interns sees both.
		args = append(args, *f.ManagedBy, orgID)
		manager, org := len(args)-1, len(args)
		team := 0
		if f.TeamID != nil {
			args = append(args, *f.TeamID)
			team = len(args)
		}

		narrow := ""
		if team != 0 {
			narrow = fmt.Sprintf(" WHERE member.team_id = $%d", team)
		}
		clause := fmt.Sprintf(`t.assignee_id IN (
			WITH RECURSIVE managed AS (
				SELECT tm.id FROM teams tm
				WHERE tm.manager_id = $%d AND tm.org_id = $%d AND tm.deleted_at IS NULL
				UNION
				SELECT child.id FROM teams child
				JOIN managed ON child.parent_id = managed.id
				WHERE child.deleted_at IS NULL
			)
			SELECT member.user_id FROM team_members member
			JOIN managed ON managed.id = member.team_id%s)`, manager, org, narrow)

		// The mirror of the manager's problem. Somebody who administers the
		// organization manages nobody in the chart and may nonetheless act on
		// everybody, so this walk handed them an empty team board while every
		// write path let them act on every row that ought to have been on it.
		//
		// The decision: an administrator who runs no team falls back to the
		// whole organization, because the tab asks "what are the people I
		// answer for doing?" and they answer for everyone. An administrator who
		// does run teams already has a board that means something and keeps it
		// — widening theirs would put sales on a tab labelled with their own
		// team, and the team filter only offers teams they manage.
		if f.ViewerIsOrgAdmin {
			everybody := "t.assignee_id IS NOT NULL"
			if team != 0 {
				// They answer for every team, so they may narrow to any of them.
				everybody = fmt.Sprintf(`t.assignee_id IN (
					SELECT member.user_id FROM team_members member
					JOIN teams tm ON tm.id = member.team_id
					WHERE tm.org_id = $%d AND tm.id = $%d AND tm.deleted_at IS NULL)`, org, team)
			}
			clause = fmt.Sprintf(`(%s OR (%s AND NOT EXISTS (
				SELECT 1 FROM teams tm
				WHERE tm.manager_id = $%d AND tm.org_id = $%d AND tm.deleted_at IS NULL)))`,
				clause, everybody, manager, org)
		}
		where = append(where, clause)
	}

	// Choose which rows survive the cap first and order them for the board
	// afterwards: open work outranks finished work whatever its age, and within
	// each the most recent activity stays.
	args = append(args, maxListedTasks)
	query := taskColumns + ` WHERE t.id IN (
		SELECT t.id FROM tasks t WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY t.status = 'done', COALESCE(t.completed_at, t.created_at) DESC
		LIMIT $` + strconv.Itoa(len(args)) + `
	) ORDER BY t.position, t.created_at DESC`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.OrgID, &t.ProjectID, &t.ProjectName, &t.Key, &t.ReporterID, &t.ReporterName,
			&t.AssigneeID, &t.AssigneeName, &t.Title, &t.Description,
			&t.Status, &t.Priority, &t.TaskType, &t.StoryPoints,
			&t.DueDate, &t.StartDate, &t.CompletedAt, &t.ParentTaskID,
			&t.Position, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		t.Labels = []Label{}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tasks, err = s.attachLabels(ctx, tasks)
	if err != nil {
		return nil, err
	}
	if f.Viewer != nil {
		if err := s.attachAbilities(ctx, orgID, *f.Viewer, f.ViewerIsOrgAdmin, tasks); err != nil {
			return nil, err
		}
	}
	return tasks, nil
}

// AssignableUsers lists the people this caller may put work on: themselves,
// anybody on a team they manage, and — for an organization administrator —
// everybody. The create form offers these, so somebody does not fill in a task
// only to be refused on submit.
func (s *Service) AssignableUsers(ctx context.Context, orgID, userID uuid.UUID, isOrgAdmin bool) ([]TeamMemberT, error) {
	query := `
		SELECT u.id, u.name, u.email
		FROM users u
		WHERE u.org_id = $1 AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)
		ORDER BY u.name`
	args := []any{orgID}

	if !isOrgAdmin {
		query = `
			WITH RECURSIVE managed AS (
				SELECT t.id FROM teams t
				WHERE t.manager_id = $2 AND t.org_id = $1 AND t.deleted_at IS NULL
				UNION
				SELECT child.id FROM teams child
				JOIN managed ON child.parent_id = managed.id
				WHERE child.deleted_at IS NULL
			)
			SELECT u.id, u.name, u.email
			FROM users u
			WHERE u.org_id = $1 AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)
			  AND (
			    u.id = $2
			    OR (
			      EXISTS (SELECT 1 FROM team_members tm
			              JOIN managed ON managed.id = tm.team_id
			              WHERE tm.user_id = u.id)
			      -- Somebody who administers the organization is above the
			      -- chart even when they sit on a team.
			      AND NOT COALESCE(u.is_admin, false)
			      AND u.role NOT IN ('owner', 'admin')
			      AND NOT EXISTS (
			          SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id
			          WHERE ur.user_id = u.id AND (r.permissions ? '*' OR r.permissions ? 'tasks.all'))
			    )
			  )
			ORDER BY u.name`
		args = append(args, userID)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list assignable users: %w", err)
	}
	defer rows.Close()

	people := []TeamMemberT{}
	for rows.Next() {
		var m TeamMemberT
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email); err != nil {
			return nil, fmt.Errorf("scan assignable user: %w", err)
		}
		people = append(people, m)
	}
	return people, rows.Err()
}

// ManagedTeam is a team somebody runs, with the people on it. Backs the team
// view of the task board and the filter that narrows it to one person.
type ManagedTeam struct {
	ID      uuid.UUID     `json:"id"`
	Name    string        `json:"name"`
	Members []TeamMemberT `json:"members"`
}

type TeamMemberT struct {
	UserID uuid.UUID `json:"userId"`
	Name   string    `json:"name"`
	Email  string    `json:"email"`
}

// ManagedTeams lists the teams this person manages, each with its members,
// including teams nested beneath the ones they manage directly.
func (s *Service) ManagedTeams(ctx context.Context, orgID, userID uuid.UUID) ([]ManagedTeam, error) {
	rows, err := s.db.Query(ctx, `
		WITH RECURSIVE managed AS (
			SELECT t.id, t.name FROM teams t
			WHERE t.manager_id = $1 AND t.org_id = $2 AND t.deleted_at IS NULL
			UNION
			SELECT child.id, child.name FROM teams child
			JOIN managed ON child.parent_id = managed.id
			WHERE child.deleted_at IS NULL
		)
		SELECT managed.id, managed.name, u.id, u.name, u.email
		FROM managed
		LEFT JOIN team_members tm ON tm.team_id = managed.id
		LEFT JOIN users u ON u.id = tm.user_id AND u.deleted_at IS NULL
		ORDER BY managed.name, u.name
	`, userID, orgID)
	if err != nil {
		return nil, fmt.Errorf("list managed teams: %w", err)
	}
	defer rows.Close()

	teams := []ManagedTeam{}
	at := map[uuid.UUID]int{}
	for rows.Next() {
		var teamID uuid.UUID
		var teamName string
		var memberID *uuid.UUID
		var memberName, memberEmail *string
		if err := rows.Scan(&teamID, &teamName, &memberID, &memberName, &memberEmail); err != nil {
			return nil, fmt.Errorf("scan managed team: %w", err)
		}
		i, seen := at[teamID]
		if !seen {
			teams = append(teams, ManagedTeam{ID: teamID, Name: teamName, Members: []TeamMemberT{}})
			i = len(teams) - 1
			at[teamID] = i
		}
		// A team with nobody on it still belongs in the list.
		if memberID != nil {
			teams[i].Members = append(teams[i].Members, TeamMemberT{
				UserID: *memberID,
				Name:   derefOr(memberName, ""),
				Email:  derefOr(memberEmail, ""),
			})
		}
	}
	return teams, rows.Err()
}

// Reports is the people this person answers for, by team: everybody on a team
// they run or on one nested beneath it, kept to whom HasAuthorityOver allows, so
// a list built from it never offers somebody the rules then refuse (an
// administrator sitting on the team, or the manager themself). Each person comes
// once, under the first team that has them, and a team left empty is dropped.
func (s *Service) Reports(ctx context.Context, orgID, managerID uuid.UUID) ([]ManagedTeam, error) {
	teams, err := s.ManagedTeams(ctx, orgID, managerID)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, team := range teams {
		for _, m := range team.Members {
			ids = append(ids, m.UserID)
		}
	}
	facts, err := s.authorityFacts(ctx, orgID, managerID, ids)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]bool{managerID: true}
	out := []ManagedTeam{}
	for _, team := range teams {
		kept := []TeamMemberT{}
		for _, m := range team.Members {
			if seen[m.UserID] || !facts[m.UserID].allows() {
				continue
			}
			seen[m.UserID] = true
			kept = append(kept, m)
		}
		if len(kept) > 0 {
			out = append(out, ManagedTeam{ID: team.ID, Name: team.Name, Members: kept})
		}
	}
	return out, nil
}

func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// attachLabels loads the labels for a whole page in one query so a board does
// not issue a query per card.
func (s *Service) attachLabels(ctx context.Context, tasks []Task) ([]Task, error) {
	if len(tasks) == 0 {
		return tasks, nil
	}
	ids := make([]uuid.UUID, len(tasks))
	index := make(map[uuid.UUID]int, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
		index[t.ID] = i
	}

	rows, err := s.db.Query(ctx, `
		SELECT tl.task_id, l.id, l.org_id, l.name, COALESCE(l.color, '#6366f1'), l.created_at
		FROM task_labels tl JOIN labels l ON l.id = tl.label_id
		WHERE tl.task_id = ANY($1)
		ORDER BY l.name
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("load labels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var taskID uuid.UUID
		var l Label
		if err := rows.Scan(&taskID, &l.ID, &l.OrgID, &l.Name, &l.Color, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan label: %w", err)
		}
		if i, ok := index[taskID]; ok {
			tasks[i].Labels = append(tasks[i].Labels, l)
		}
	}
	return tasks, rows.Err()
}

func (s *Service) CreateTask(ctx context.Context, orgID, reporterID uuid.UUID, title, description, status, priority, taskType string, projectID, assigneeID *uuid.UUID, dueDate, startDate *time.Time, labels []string, isOrgAdmin bool) (*Task, error) {
	title = strings.TrimSpace(title)
	if !validTitle(title) {
		return nil, apperrors.New(apperrors.CodeBadRequest, "invalid title")
	}
	if assigneeID != nil {
		// Only UpdateTask used to confine the assignee to the organization, so
		// creating a task assigned to another tenant's user went straight
		// through.
		var exists bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
			*assigneeID, orgID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check assignee: %w", err)
		}
		if !exists {
			return nil, apperrors.New(apperrors.CodeNotFound, "assignee not found in this organization")
		}
		mayAssign, err := s.MayAssign(ctx, orgID, reporterID, *assigneeID, isOrgAdmin)
		if err != nil {
			return nil, err
		}
		if !mayAssign {
			return nil, apperrors.New(apperrors.CodeForbidden,
				"you can only assign work to yourself or to somebody on a team you manage")
		}
	}
	if status == "" {
		status = "todo"
	}
	if priority == "" {
		priority = "medium"
	}
	if taskType == "" {
		taskType = "task"
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	key, err := nextTaskKey(ctx, tx, orgID, projectID)
	if err != nil {
		return nil, err
	}

	id := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO tasks (id, org_id, project_id, reporter_id, assignee_id, title, description,
		                   status, priority, task_type, due_date, start_date, completed_at, created_at, updated_at, task_key)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, $11, $12,
		        CASE WHEN $13::boolean THEN NOW() END, NOW(), NOW(), $14)
	`, id, orgID, projectID, reporterID, assigneeID, title, description,
		status, priority, taskType, dueDate, startDate, status == "done", key)
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	for _, raw := range labels {
		labelID, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO task_labels (task_id, label_id)
			SELECT $1, l.id FROM labels l WHERE l.id = $2 AND l.org_id = $3
			ON CONFLICT DO NOTHING
		`, id, labelID, orgID); err != nil {
			return nil, fmt.Errorf("attach label: %w", err)
		}
	}

	if err := recordActivity(ctx, tx, id, reporterID, "created", "", title); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	created, err := s.GetTaskFor(ctx, orgID, id, reporterID, isOrgAdmin)
	if err != nil {
		return nil, err
	}
	s.notifyAssignee(ctx, orgID, reporterID, created)
	return created, nil
}

// DefaultKeyPrefix starts the key of a task in no project.
const DefaultKeyPrefix = "TASK"

// nextTaskKey hands out the next key for a task in this project, or in none.
// The counter row is locked by the update, so two tasks made at once in the
// same project never share a number.
func nextTaskKey(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, projectID *uuid.UUID) (string, error) {
	prefix := DefaultKeyPrefix
	if projectID != nil {
		err := tx.QueryRow(ctx, `SELECT key FROM projects WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`,
			*projectID, orgID).Scan(&prefix)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", apperrors.New(apperrors.CodeNotFound, "project not found")
		}
		if err != nil {
			return "", fmt.Errorf("find project: %w", err)
		}
	}
	var n int
	if err := tx.QueryRow(ctx, `
		INSERT INTO task_key_counters (org_id, prefix, last_number) VALUES ($1, $2, 1)
		ON CONFLICT (org_id, prefix) DO UPDATE SET last_number = task_key_counters.last_number + 1
		RETURNING last_number
	`, orgID, prefix).Scan(&n); err != nil {
		return "", fmt.Errorf("number task: %w", err)
	}
	return fmt.Sprintf("%s-%d", prefix, n), nil
}

// GetTask is organization-scoped: an id from another tenant reads as
// not-found rather than exposing the row.
func (s *Service) GetTask(ctx context.Context, orgID, id uuid.UUID) (*Task, error) {
	task, err := scanTask(s.db.QueryRow(ctx,
		taskColumns+` WHERE t.id = $1 AND t.org_id = $2 AND t.deleted_at IS NULL`, id, orgID))
	if err != nil {
		return nil, err
	}
	withLabels, err := s.attachLabels(ctx, []Task{*task})
	if err != nil {
		return nil, err
	}
	return &withLabels[0], nil
}

// GetTaskFor is GetTask with what this caller may do to the task attached. The
// detail page is where Delete actually lives, so it has to be told the same
// answer the board was given rather than finding out from a 403.
func (s *Service) GetTaskFor(ctx context.Context, orgID, id, viewerID uuid.UUID, isOrgAdmin bool) (*Task, error) {
	task, err := s.GetTask(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	page := []Task{*task}
	if err := s.attachAbilities(ctx, orgID, viewerID, isOrgAdmin, page); err != nil {
		return nil, err
	}
	return &page[0], nil
}

// updatableFields whitelists what a PATCH may touch, so a caller cannot smuggle
// org_id or reporter_id into the update.
var updatableFields = map[string]string{
	"title":        "title",
	"description":  "description",
	"status":       "status",
	"priority":     "priority",
	"taskType":     "task_type",
	"assigneeId":   "assignee_id",
	"dueDate":      "due_date",
	"startDate":    "start_date",
	"storyPoints":  "story_points",
	"position":     "position",
	"parentTaskId": "parent_task_id",
}

// titleMaxLength matches tasks.title, which is varchar(500). A longer title
// is the caller's mistake, not a server fault.
const titleMaxLength = 500

// validTitle is the one rule for a task's title, on create and on update.
func validTitle(title string) bool {
	return strings.TrimSpace(title) != "" && len(title) <= titleMaxLength
}

// allowedStatuses and allowedPriorities mirror the binding tags on create.
// PATCH takes a free-form map, so without these an update could set a status
// no board column matches.
var (
	allowedStatuses   = map[string]bool{"todo": true, "in_progress": true, "in_review": true, "done": true, "blocked": true}
	allowedPriorities = map[string]bool{"low": true, "medium": true, "high": true, "urgent": true}
	allowedTaskTypes  = map[string]bool{"task": true, "bug": true, "story": true, "epic": true, "subtask": true}
)

// validateUpdates checks the values, not just the field names. The whitelist
// alone let a caller write an assignee or parent task belonging to another
// organization, which then leaked that user's name back through the board.
func (s *Service) validateUpdates(ctx context.Context, orgID, actorID uuid.UUID, updates map[string]interface{}, isOrgAdmin bool) error {
	enum := func(field string, allowed map[string]bool) error {
		raw, ok := updates[field]
		if !ok {
			return nil
		}
		value, isString := raw.(string)
		if !isString || !allowed[value] {
			return apperrors.New(apperrors.CodeBadRequest, "invalid "+field)
		}
		return nil
	}
	if err := enum("status", allowedStatuses); err != nil {
		return err
	}
	if err := enum("priority", allowedPriorities); err != nil {
		return err
	}
	if err := enum("taskType", allowedTaskTypes); err != nil {
		return err
	}

	// Everything below type-checks a value against the column that will hold
	// it. Without this the raw JSON reached the driver and a caller's typo came
	// back as a 500 carrying a constraint or encoding message.
	if raw, ok := updates["title"]; ok {
		title, isString := raw.(string)
		if !isString || !validTitle(title) {
			return apperrors.New(apperrors.CodeBadRequest, "invalid title")
		}
	}
	if raw, ok := updates["description"]; ok && raw != nil {
		if _, isString := raw.(string); !isString {
			return apperrors.New(apperrors.CodeBadRequest, "invalid description")
		}
	}
	for _, field := range []string{"dueDate", "startDate"} {
		raw, ok := updates[field]
		if !ok || raw == nil {
			continue
		}
		text, isString := raw.(string)
		if !isString {
			return apperrors.New(apperrors.CodeBadRequest, "invalid "+field)
		}
		when, err := optionalTime(&text)
		if err != nil {
			return apperrors.New(apperrors.CodeBadRequest, "invalid "+field)
		}
		// Normalised so the driver receives a timestamp, never a string. An
		// empty string clears the column, as it does on create.
		if when == nil {
			updates[field] = nil
		} else {
			updates[field] = *when
		}
	}
	for _, field := range []string{"storyPoints", "position"} {
		raw, ok := updates[field]
		if !ok || raw == nil {
			continue
		}
		n, err := wholeNumber(raw)
		if err != nil {
			return apperrors.New(apperrors.CodeBadRequest, "invalid "+field)
		}
		updates[field] = n
	}

	// Nullable foreign keys are global in the schema, so they have to be
	// confined to this organization here.
	if raw, ok := updates["assigneeId"]; ok && raw != nil {
		id, err := parseUUIDValue(raw)
		if err != nil {
			return apperrors.New(apperrors.CodeBadRequest, "invalid assigneeId")
		}
		var exists bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
			id, orgID).Scan(&exists); err != nil {
			return fmt.Errorf("check assignee: %w", err)
		}
		if !exists {
			return apperrors.New(apperrors.CodeNotFound, "assignee not found in this organization")
		}
		mayAssign, err := s.MayAssign(ctx, orgID, actorID, id, isOrgAdmin)
		if err != nil {
			return err
		}
		if !mayAssign {
			return apperrors.New(apperrors.CodeForbidden,
				"you can only assign work to yourself or to somebody on a team you manage")
		}
		updates["assigneeId"] = id
	}
	if raw, ok := updates["parentTaskId"]; ok && raw != nil {
		id, err := parseUUIDValue(raw)
		if err != nil {
			return apperrors.New(apperrors.CodeBadRequest, "invalid parentTaskId")
		}
		var exists bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
			id, orgID).Scan(&exists); err != nil {
			return fmt.Errorf("check parent task: %w", err)
		}
		if !exists {
			return apperrors.New(apperrors.CodeNotFound, "parent task not found in this organization")
		}
		updates["parentTaskId"] = id
	}
	return nil
}

// MayAssign decides whether actor may put assignee on a task.
//
// Assignment runs down the org chart, never up or sideways: a team's manager
// may assign to anybody on that team or on a team beneath it, and anybody may
// assign work to themselves. Organization owners and admins are not bounded by
// the chart. Without this, any member could assign work to their manager.
func (s *Service) MayAssign(ctx context.Context, orgID, actorID, assigneeID uuid.UUID, isOrgAdmin bool) (bool, error) {
	return s.HasAuthorityOver(ctx, orgID, actorID, assigneeID, isOrgAdmin)
}

// authorityFact is what the org chart says about one person, seen from one
// actor. Everything in this package that asks "may I act on them?" reads the
// answer out of these two facts, so there is one statement of who outranks
// whom rather than a copy per surface.
type authorityFact struct {
	Name string
	// Administers: they own or administer the organization, so they sit above
	// the chart even when they are listed on a team as an ordinary member.
	Administers bool
	// Managed: they sit on a team the actor runs, or on one nested beneath it.
	Managed bool
}

func (f authorityFact) allows() bool { return !f.Administers && f.Managed }

// authorityFacts loads what the chart says about every named person, from
// actor's point of view, in a single round trip. A board asks about a whole
// page of cards at once: answering per card cost one query per row, which is
// fifty round trips for a fifty-row board.
//
// People it cannot place — not in this organization — are simply absent from
// the result, and every caller reads that as no authority.
func (s *Service) authorityFacts(ctx context.Context, orgID, actorID uuid.UUID, subjects []uuid.UUID) (map[uuid.UUID]authorityFact, error) {
	facts := make(map[uuid.UUID]authorityFact, len(subjects))
	if len(subjects) == 0 {
		return facts, nil
	}

	// The recursive term walks down from every team the actor manages,
	// following parent_id, so a team lead who also runs a sub-team of interns
	// reaches both.
	rows, err := s.db.Query(ctx, `
		WITH RECURSIVE managed AS (
			SELECT t.id
			FROM teams t
			WHERE t.org_id = $1 AND t.manager_id = $2 AND t.deleted_at IS NULL
			UNION
			SELECT child.id
			FROM teams child
			JOIN managed ON child.parent_id = managed.id
			WHERE child.org_id = $1 AND child.deleted_at IS NULL
		)
		SELECT u.id, u.name,
		       COALESCE(u.is_admin, false)
		           OR u.role IN ('owner', 'admin')
		           OR EXISTS (
		               SELECT 1 FROM user_roles ur
		               JOIN roles r ON r.id = ur.role_id
		               WHERE ur.user_id = u.id AND (r.permissions ? '*' OR r.permissions ? 'tasks.all')),
		       EXISTS (
		           SELECT 1 FROM team_members tm
		           JOIN managed ON managed.id = tm.team_id
		           WHERE tm.user_id = u.id)
		FROM users u
		WHERE u.org_id = $1 AND u.id = ANY($3)
	`, orgID, actorID, subjects)
	if err != nil {
		return nil, fmt.Errorf("check authority: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var fact authorityFact
		if err := rows.Scan(&id, &fact.Name, &fact.Administers, &fact.Managed); err != nil {
			return nil, fmt.Errorf("scan authority: %w", err)
		}
		facts[id] = fact
	}
	return facts, rows.Err()
}

// HasAuthorityOver answers whether actor may act on subject's behalf: they are
// the same person, actor manages a team subject sits on (directly or further
// down), or actor administers the organization. It is the one place this
// package decides who outranks whom, so assigning work, deleting somebody's
// task and drawing their card on a board cannot drift apart.
func (s *Service) HasAuthorityOver(ctx context.Context, orgID, actorID, subjectID uuid.UUID, isOrgAdmin bool) (bool, error) {
	if isOrgAdmin || actorID == subjectID {
		return true, nil
	}
	facts, err := s.authorityFacts(ctx, orgID, actorID, []uuid.UUID{subjectID})
	if err != nil {
		return false, err
	}
	// Somebody the chart cannot place is somebody nobody outranks. Nothing
	// reachable gets here — every caller looks its row up organization-scoped
	// first — and a refusal is the fail-closed answer if one ever does.
	return facts[subjectID].allows(), nil
}

// AuthorityOverAny answers whether actor outranks at least one of these people,
// by the same facts. An organization admin is the caller's own question to ask
// first; this reads only the chart.
func (s *Service) AuthorityOverAny(ctx context.Context, orgID, actorID uuid.UUID, subjects []uuid.UUID) (bool, error) {
	facts, err := s.authorityFacts(ctx, orgID, actorID, subjects)
	if err != nil {
		return false, err
	}
	for _, fact := range facts {
		if fact.allows() {
			return true, nil
		}
	}
	return false, nil
}

// attachAbilities tells each task what this caller may do to it, in one pass
// over the whole page. Every answer comes from the same authority facts the
// write paths consult, so a board cannot show an action the API will refuse —
// which is exactly what it used to do for an administrator sitting on somebody
// else's team as an ordinary member.
func (s *Service) attachAbilities(ctx context.Context, orgID, viewerID uuid.UUID, isOrgAdmin bool, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	if isOrgAdmin {
		for i := range tasks {
			tasks[i].Can = &Abilities{Edit: true, Reassign: true, Delete: true}
		}
		return nil
	}

	// Everybody the cards on this page are bound to: whoever filed each one and
	// whoever holds it. One query for the page, not one per card.
	people := make([]uuid.UUID, 0, 2*len(tasks))
	seen := make(map[uuid.UUID]bool, 2*len(tasks))
	remember := func(id uuid.UUID) {
		if !seen[id] {
			seen[id] = true
			people = append(people, id)
		}
	}
	for _, t := range tasks {
		remember(t.ReporterID)
		if t.AssigneeID != nil {
			remember(*t.AssigneeID)
		}
	}

	facts, err := s.authorityFacts(ctx, orgID, viewerID, people)
	if err != nil {
		return err
	}

	// over answers the one question, and names who to blame when the answer is
	// no — a greyed card with no explanation is nearly as bad as the 403.
	over := func(subject uuid.UUID) (bool, string) {
		if subject == viewerID {
			return true, ""
		}
		fact, known := facts[subject]
		switch {
		case !known:
			return false, "they are not in this organisation"
		case fact.Administers:
			return false, fact.Name + " is an organisation admin"
		case !fact.Managed:
			return false, fact.Name + " is not on a team you manage"
		}
		return true, ""
	}

	for i := range tasks {
		can := &Abilities{
			// Any member of the organization may change a task's own fields:
			// title, description, status, priority, dates. UpdateTask gates the
			// assignee and nothing else, so this is unconditionally true today.
			// It is carried anyway so the client reads one shape rather than a
			// flag plus an assumption, and so that tightening the rule later
			// has an obvious place to land.
			Edit: true,
		}

		// Deleting needs authority over whoever filed the task, exactly as
		// DeleteTask requires.
		can.Delete, can.Reason = over(tasks[i].ReporterID)

		// Reassigning needs authority over whoever currently holds the work.
		// Nothing is being taken off anybody when the task is unassigned.
		can.Reassign = true
		if tasks[i].AssigneeID != nil {
			mayMove, whyNot := over(*tasks[i].AssigneeID)
			can.Reassign = mayMove
			switch {
			case whyNot == "" || whyNot == can.Reason:
			case can.Reason == "":
				can.Reason = whyNot
			default:
				can.Reason += "; " + whyNot
			}
		}
		tasks[i].Can = can
	}
	return nil
}

// wholeNumber reads a value bound for an integer column. JSON decodes every
// number as a float64, so 1.5 would otherwise reach story_points and be
// silently truncated to 1 rather than refused.
func wholeNumber(raw interface{}) (int, error) {
	var f float64
	switch v := raw.(type) {
	case float64:
		f = v
	case int:
		f = float64(v)
	case int32:
		f = float64(v)
	case int64:
		f = float64(v)
	default:
		return 0, fmt.Errorf("not a number")
	}
	if f != math.Trunc(f) || f < math.MinInt32 || f > math.MaxInt32 {
		return 0, fmt.Errorf("not a whole 32-bit number")
	}
	return int(f), nil
}

func parseUUIDValue(raw interface{}) (uuid.UUID, error) {
	switch v := raw.(type) {
	case string:
		return uuid.Parse(v)
	case uuid.UUID:
		return v, nil
	default:
		return uuid.Nil, fmt.Errorf("not a uuid")
	}
}

// UpdateTask applies a partial change and records one activity entry per field
// that actually moved, so the task's history reflects what changed.
func (s *Service) UpdateTask(ctx context.Context, orgID, id, actorID uuid.UUID, updates map[string]interface{}, isOrgAdmin bool) (*Task, error) {
	before, err := s.GetTask(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if err := s.validateUpdates(ctx, orgID, actorID, updates, isOrgAdmin); err != nil {
		return nil, err
	}

	// Handing a task to somebody needs authority over them, which
	// validateUpdates has just checked. Taking it off whoever currently holds
	// it needs the same: without this half, a manager who may not delete an
	// organization admin's task could still quietly move it onto one of their
	// own reports, and the board's reassign flag would be promising something
	// the write path does not honour.
	if _, reassigning := updates["assigneeId"]; reassigning && before.AssigneeID != nil {
		mayTake, err := s.HasAuthorityOver(ctx, orgID, actorID, *before.AssigneeID, isOrgAdmin)
		if err != nil {
			return nil, err
		}
		if !mayTake {
			return nil, apperrors.New(apperrors.CodeForbidden,
				"you can only move work held by you or by somebody on a team you manage")
		}
	}

	sets := []string{}
	args := []any{id, orgID}
	for key, value := range updates {
		column, ok := updatableFields[key]
		if !ok {
			continue
		}
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if len(sets) == 0 {
		// Nothing to write, but the caller still gets the same shape back as
		// they would from a real change, abilities included.
		return s.GetTaskFor(ctx, orgID, id, actorID, isOrgAdmin)
	}
	// Completing a task stamps completed_at; reopening clears it. Saving done
	// over done keeps the first date, because tracking counts work by that date
	// and a later edit must not move it.
	if status, ok := updates["status"].(string); ok {
		if status == "done" {
			sets = append(sets, "completed_at = COALESCE(completed_at, NOW())")
		} else {
			sets = append(sets, "completed_at = NULL")
		}
	}
	sets = append(sets, "updated_at = NOW()")

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, fmt.Sprintf(
		`UPDATE tasks SET %s WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`,
		strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperrors.ErrNotFound
	}

	changed := map[string]bool{}
	for key, value := range updates {
		if _, ok := updatableFields[key]; !ok {
			continue
		}
		oldValue := beforeValue(before, key)
		newValue := activityValue(value)
		if oldValue == newValue {
			continue
		}
		changed[key] = true
		if err := recordActivity(ctx, tx, id, actorID, key, oldValue, newValue); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	after, err := s.GetTaskFor(ctx, orgID, id, actorID, isOrgAdmin)
	if err != nil {
		return nil, err
	}
	// Whoever was just handed the task hears that instead, and nobody hears of
	// a change that changed nothing.
	except := []uuid.UUID{actorID}
	if changed["assigneeId"] {
		s.notifyAssignee(ctx, orgID, actorID, after)
		if after.AssigneeID != nil {
			except = append(except, *after.AssigneeID)
		}
	}
	if what := describeChanges(changed, after); what != "" {
		s.notifyInvolved(ctx, orgID, id, "task.updated",
			s.personName(ctx, actorID)+" updated "+after.Title, what, except...)
	}
	return after, nil
}

// beforeValue is what a field holds now, written the way activityValue writes the
// incoming value, so comparing the two says whether anything changed.
func beforeValue(t *Task, field string) string {
	switch field {
	case "title":
		return t.Title
	case "description":
		return t.Description
	case "status":
		return t.Status
	case "priority":
		return t.Priority
	case "taskType":
		return t.TaskType
	case "assigneeId":
		return activityValue(t.AssigneeID)
	case "dueDate":
		return activityValue(t.DueDate)
	case "startDate":
		return activityValue(t.StartDate)
	case "storyPoints":
		return activityValue(t.StoryPoints)
	case "position":
		return activityValue(t.Position)
	case "parentTaskId":
		return activityValue(t.ParentTaskID)
	default:
		return ""
	}
}

// activityValue is how a value is written into the history: an empty field is the
// empty string, never "<nil>", and a time is RFC3339 in UTC, so one instant reads
// the same whichever zone it arrived in.
func activityValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	case *time.Time:
		if v == nil {
			return ""
		}
		return activityValue(*v)
	case *int:
		if v == nil {
			return ""
		}
		return activityValue(*v)
	case *uuid.UUID:
		if v == nil {
			return ""
		}
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func recordActivity(ctx context.Context, tx pgx.Tx, taskID, userID uuid.UUID, field, oldValue, newValue string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO task_activities (id, task_id, user_id, field, old_value, new_value, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NOW())
	`, taskID, userID, field, oldValue, newValue)
	if err != nil {
		return fmt.Errorf("record activity: %w", err)
	}
	return nil
}

// DeleteTask is a soft delete: comments, activity and attachments reference the
// task and an audit trail that loses its subject is worse than a hidden row.
//
// Deleting needs authority over the person who filed the task — themselves,
// whoever manages them through the team chart, or an organization owner or
// admin — the same ladder that decides who may assign work to whom. Membership
// of the organization used to be enough, so anybody could make anybody else's
// task disappear from everybody's board.
//
// The assignee is deliberately not on that list: being handed a piece of work
// is not authority to destroy the record of it, which is precisely how an
// unwanted bug report would be made to go away. An assignee can still close
// the task, and closing is written to its activity trail.
//
// (projects.lead_id would be a natural fifth answer, but nothing in the
// codebase reads or writes projects yet — no service, no route, no way to
// appoint a lead — so gating on it would be a branch no caller can reach.)
func (s *Service) DeleteTask(ctx context.Context, orgID, id, actorID uuid.UUID, isOrgAdmin bool) error {
	// Look the task up first, scoped to the organization: an id from another
	// tenant, or one already deleted, is not-found and never a refusal, so a
	// 403 cannot be used to discover that a task exists.
	var reporterID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		SELECT reporter_id FROM tasks
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, id, orgID).Scan(&reporterID); err != nil {
		if err == pgx.ErrNoRows {
			return apperrors.ErrNotFound
		}
		return fmt.Errorf("load task for delete: %w", err)
	}

	mayDelete, err := s.HasAuthorityOver(ctx, orgID, actorID, reporterID, isOrgAdmin)
	if err != nil {
		return err
	}
	if !mayDelete {
		return apperrors.New(apperrors.CodeForbidden,
			"only the person who filed this task, their manager, or an administrator may delete it")
	}

	tag, err := s.db.Exec(ctx, `
		UPDATE tasks SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL
	`, id, orgID)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

// AddComment writes the comment and its activity entry in one transaction:
// splitting them left a committed comment with no history when the second
// write failed, and returned an error for a comment that actually exists.
func (s *Service) AddComment(ctx context.Context, orgID, taskID, userID uuid.UUID, content string, attachmentIDs []uuid.UUID) (*TaskComment, error) {
	// A file on its own is a comment: "here is the screenshot" needs no words.
	if strings.TrimSpace(content) == "" && len(attachmentIDs) == 0 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "write something or attach a file")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var c TaskComment
	err = tx.QueryRow(ctx, `
		INSERT INTO task_comments (id, task_id, user_id, content, created_at)
		SELECT gen_random_uuid(), t.id, $2, $3, NOW()
		FROM tasks t WHERE t.id = $1 AND t.org_id = $4 AND t.deleted_at IS NULL
		RETURNING id, task_id, user_id, content, created_at, updated_at
	`, taskID, userID, content, orgID).Scan(
		&c.ID, &c.TaskID, &c.UserID, &c.Content, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("add comment: %w", err)
	}

	// Claim the uploads. Only files already filed against this task by this
	// person are eligible, so a comment cannot pull in somebody else's upload
	// or one from another task.
	c.Attachments = []Attachment{}
	if len(attachmentIDs) > 0 {
		rows, err := tx.Query(ctx, `
			UPDATE attachments
			SET comment_id = $1
			WHERE id = ANY($2)
			  AND uploaded_by = $3
			  AND category = 'tasks'
			  AND ref_id = $4
			  AND comment_id IS NULL
			  AND deleted_at IS NULL
			RETURNING id, filename, content_type, size_bytes, object_key
		`, c.ID, attachmentIDs, userID, taskID.String())
		if err != nil {
			return nil, fmt.Errorf("attach to comment: %w", err)
		}
		for rows.Next() {
			var a Attachment
			if err := rows.Scan(&a.ID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.ObjectKey); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan comment attachment: %w", err)
			}
			c.Attachments = append(c.Attachments, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	if err := recordActivity(ctx, tx, taskID, userID, "comment", "", content); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Everybody following the task sees it appear rather than finding it on
	// their next reload.
	s.announceComment(ctx, orgID, taskID, &c)
	mentioned := s.notifyCommentMentions(ctx, orgID, taskID, &c)
	s.notifyComment(ctx, orgID, taskID, &c, mentioned)
	return &c, nil
}

// commentMention matches @-handles as chat reads them (internal/chat): the
// part of an email before the @, or a name with the spaces taken out.
var commentMention = regexp.MustCompile(`@([A-Za-z0-9._-]+)`)

// notifyCommentMentions tells each person a comment names, once, whoever they
// are in the organization: a task can be read by anybody in it, so bringing
// somebody into the conversation needs no other step. Best effort, and the
// author is never told about themselves. It returns who was told, so they are
// not told a second time that a comment was written.
func (s *Service) notifyCommentMentions(ctx context.Context, orgID, taskID uuid.UUID, c *TaskComment) []uuid.UUID {
	if s.notifier == nil {
		return nil
	}
	var handles []string
	for _, m := range commentMention.FindAllStringSubmatch(c.Content, -1) {
		if h := strings.ToLower(strings.TrimRight(m[1], "._-")); h != "" {
			handles = append(handles, h)
		}
	}
	if len(handles) == 0 {
		return nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT u.id, t.title, COALESCE(a.name, 'Somebody')
		FROM users u
		JOIN tasks t ON t.id = $2 AND t.org_id = u.org_id
		LEFT JOIN users a ON a.id = $3
		WHERE u.org_id = $1 AND u.deleted_at IS NULL AND u.id <> $3
		  AND (REPLACE(LOWER(u.name), ' ', '') = ANY($4)
		       OR LOWER(split_part(u.email, '@', 1)) = ANY($4))
	`, orgID, taskID, c.UserID, handles)
	if err != nil {
		s.logger.Warn().Err(err).Msg("comment mention lookup failed")
		return nil
	}
	type named struct {
		id           uuid.UUID
		task, author string
	}
	var people []named
	for rows.Next() {
		var p named
		if err := rows.Scan(&p.id, &p.task, &p.author); err == nil {
			people = append(people, p)
		}
	}
	rows.Close()
	told := make([]uuid.UUID, 0, len(people))
	for _, p := range people {
		told = append(told, p.id)
		if err := s.notifier.Notify(ctx, orgID, p.id, "task.mention",
			p.author+" mentioned you on "+p.task, c.Content,
			map[string]any{"taskId": taskID.String(), "commentId": c.ID.String()}); err != nil {
			s.logger.Warn().Err(err).Msg("comment mention notification failed")
		}
	}
	return told
}

// announceComment tells everybody involved in a task that something was said.
// Best effort: a comment that was written must not fail because nobody could
// be told about it.
func (s *Service) announceComment(ctx context.Context, orgID, taskID uuid.UUID, c *TaskComment) {
	if s.broadcaster == nil {
		return
	}
	people, err := s.involvedIn(ctx, orgID, taskID, c.UserID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not work out who to tell about a comment")
		return
	}
	for _, userID := range people {
		s.broadcaster.BroadcastToUser(userID.String(), protocol.NewMessage("task.comment", map[string]any{
			"taskId":    taskID.String(),
			"commentId": c.ID.String(),
			"userName":  c.UserName,
		}))
	}
}

func (s *Service) ListComments(ctx context.Context, orgID, taskID uuid.UUID) ([]TaskComment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.task_id, c.user_id, u.name, c.content, c.created_at, c.updated_at
		FROM task_comments c
		JOIN tasks t ON t.id = c.task_id
		JOIN users u ON u.id = c.user_id
		WHERE c.task_id = $1 AND t.org_id = $2 AND c.deleted_at IS NULL
		ORDER BY c.created_at
	`, taskID, orgID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	comments := []TaskComment{}
	for rows.Next() {
		var c TaskComment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.UserID, &c.UserName, &c.Content,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan comment: %w", err)
		}
		c.Attachments = []Attachment{}
		comments = append(comments, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.attachToComments(ctx, comments)
}

// attachToComments loads every comment's files for a task in one query.
func (s *Service) attachToComments(ctx context.Context, comments []TaskComment) ([]TaskComment, error) {
	if len(comments) == 0 {
		return comments, nil
	}
	ids := make([]uuid.UUID, len(comments))
	at := make(map[uuid.UUID]int, len(comments))
	for i, c := range comments {
		ids[i] = c.ID
		at[c.ID] = i
	}

	rows, err := s.db.Query(ctx, `
		SELECT comment_id, id, filename, content_type, size_bytes, object_key
		FROM attachments
		WHERE comment_id = ANY($1) AND deleted_at IS NULL
		ORDER BY created_at
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("load comment attachments: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var commentID uuid.UUID
		var a Attachment
		if err := rows.Scan(&commentID, &a.ID, &a.Filename, &a.ContentType,
			&a.SizeBytes, &a.ObjectKey); err != nil {
			return nil, fmt.Errorf("scan comment attachment: %w", err)
		}
		if i, ok := at[commentID]; ok {
			comments[i].Attachments = append(comments[i].Attachments, a)
		}
	}
	return comments, rows.Err()
}

// ── Labels ────────────────────────────────────────────────────────────────

func (s *Service) ListLabels(ctx context.Context, orgID uuid.UUID) ([]Label, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, org_id, name, COALESCE(color, '#6366f1'), created_at
		FROM labels WHERE org_id = $1 ORDER BY name
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	defer rows.Close()

	labels := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.OrgID, &l.Name, &l.Color, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan label: %w", err)
		}
		labels = append(labels, l)
	}
	return labels, rows.Err()
}

// MaxLabelName is the longest name a label may have, in characters.
const MaxLabelName = 50

var labelColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// CreateLabel makes a label, or hands back the one the organization already has
// by that name, whatever its case, unchanged: typing a name somebody else made
// first must not recolour it for everybody.
func (s *Service) CreateLabel(ctx context.Context, orgID uuid.UUID, name, color string) (*Label, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return nil, apperrors.New(apperrors.CodeValidation, "a label needs a name")
	}
	if utf8.RuneCountInString(name) > MaxLabelName {
		return nil, apperrors.New(apperrors.CodeValidation, fmt.Sprintf("a label can be at most %d characters", MaxLabelName))
	}
	if color == "" {
		color = "#6366f1"
	}
	if !labelColor.MatchString(color) {
		return nil, apperrors.New(apperrors.CodeValidation, "a label's colour is written like #6366f1")
	}
	var l Label
	err := s.db.QueryRow(ctx, `
		SELECT id, org_id, name, COALESCE(color, '#6366f1'), created_at FROM labels
		WHERE org_id = $1 AND lower(name) = lower($2) ORDER BY created_at LIMIT 1
	`, orgID, name).Scan(&l.ID, &l.OrgID, &l.Name, &l.Color, &l.CreatedAt)
	if err == nil {
		return &l, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("find label: %w", err)
	}
	err = s.db.QueryRow(ctx, `
		INSERT INTO labels (id, org_id, name, color, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, NOW())
		ON CONFLICT (org_id, name) DO UPDATE SET name = labels.name
		RETURNING id, org_id, name, COALESCE(color, '#6366f1'), created_at
	`, orgID, name, color).Scan(&l.ID, &l.OrgID, &l.Name, &l.Color, &l.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create label: %w", err)
	}
	return &l, nil
}

// AddLabel is idempotent and refuses a label from another organization.
func (s *Service) AddLabel(ctx context.Context, orgID, taskID, labelID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO task_labels (task_id, label_id)
		SELECT t.id, l.id FROM tasks t JOIN labels l ON l.org_id = t.org_id
		WHERE t.id = $1 AND l.id = $2 AND t.org_id = $3 AND t.deleted_at IS NULL
		ON CONFLICT DO NOTHING
	`, taskID, labelID, orgID)
	if err != nil {
		return fmt.Errorf("add label: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM task_labels WHERE task_id = $1 AND label_id = $2)`,
			taskID, labelID).Scan(&exists); err != nil {
			return fmt.Errorf("verify label: %w", err)
		}
		if !exists {
			return apperrors.New(apperrors.CodeNotFound, "task or label not found in this organization")
		}
	}
	return nil
}

func (s *Service) RemoveLabel(ctx context.Context, orgID, taskID, labelID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM task_labels tl USING tasks t
		WHERE tl.task_id = t.id AND tl.task_id = $1 AND tl.label_id = $2 AND t.org_id = $3
	`, taskID, labelID, orgID)
	if err != nil {
		return fmt.Errorf("remove label: %w", err)
	}
	return nil
}

// GetActivities returns the task's history, most recent first.
func (s *Service) GetActivities(ctx context.Context, orgID, taskID uuid.UUID) ([]TaskActivity, error) {
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.task_id, a.user_id, COALESCE(u.name, ''), COALESCE(a.source, ''), a.field,
		       COALESCE(a.old_value, ''), COALESCE(a.new_value, ''), a.created_at
		FROM task_activities a
		JOIN tasks t ON t.id = a.task_id
		LEFT JOIN users u ON u.id = a.user_id
		WHERE a.task_id = $1 AND t.org_id = $2
		ORDER BY a.created_at DESC
	`, taskID, orgID)
	if err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	defer rows.Close()

	activities := []TaskActivity{}
	for rows.Next() {
		var a TaskActivity
		if err := rows.Scan(&a.ID, &a.TaskID, &a.UserID, &a.UserName, &a.Source, &a.Field,
			&a.OldValue, &a.NewValue, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		activities = append(activities, a)
	}
	return activities, rows.Err()
}

// ── Watchers ──────────────────────────────────────────────────────────────

// Watcher is someone following a task's changes.
type Watcher struct {
	UserID uuid.UUID `json:"userId"`
	Name   string    `json:"name"`
	Email  string    `json:"email"`
}

// AddWatcher is idempotent and refuses users from another organization. addedBy is
// who did it: yourself is only following a task, and somebody else is what Added to
// means (decision 38). It reports whether this call is what added them, because
// somebody already on it has not been added to anything, unless until now they were
// only following it themself.
func (s *Service) AddWatcher(ctx context.Context, orgID, taskID, userID, addedBy uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO task_watchers (task_id, user_id, added_by)
		SELECT t.id, u.id, $4 FROM tasks t JOIN users u ON u.org_id = t.org_id
		WHERE t.id = $1 AND u.id = $2 AND t.org_id = $3
		  AND t.deleted_at IS NULL AND u.deleted_at IS NULL
		ON CONFLICT (task_id, user_id) DO UPDATE SET added_by = EXCLUDED.added_by
		WHERE task_watchers.added_by = task_watchers.user_id
		  AND EXCLUDED.added_by <> task_watchers.user_id
	`, taskID, userID, orgID, addedBy)
	if err != nil {
		return false, fmt.Errorf("add watcher: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	var exists bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM task_watchers WHERE task_id = $1 AND user_id = $2)`,
		taskID, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("verify watcher: %w", err)
	}
	if !exists {
		return false, apperrors.New(apperrors.CodeNotFound, "task or user not found in this organization")
	}
	return false, nil
}

func (s *Service) RemoveWatcher(ctx context.Context, orgID, taskID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM task_watchers tw USING tasks t
		WHERE tw.task_id = t.id AND tw.task_id = $1 AND tw.user_id = $2 AND t.org_id = $3
	`, taskID, userID, orgID)
	if err != nil {
		return fmt.Errorf("remove watcher: %w", err)
	}
	return nil
}

func (s *Service) ListWatchers(ctx context.Context, orgID, taskID uuid.UUID) ([]Watcher, error) {
	rows, err := s.db.Query(ctx, `
		SELECT u.id, u.name, u.email
		FROM task_watchers tw
		JOIN tasks t ON t.id = tw.task_id
		JOIN users u ON u.id = tw.user_id
		WHERE tw.task_id = $1 AND t.org_id = $2 AND u.deleted_at IS NULL
		ORDER BY u.name
	`, taskID, orgID)
	if err != nil {
		return nil, fmt.Errorf("list watchers: %w", err)
	}
	defer rows.Close()

	watchers := []Watcher{}
	for rows.Next() {
		var w Watcher
		if err := rows.Scan(&w.UserID, &w.Name, &w.Email); err != nil {
			return nil, fmt.Errorf("scan watcher: %w", err)
		}
		watchers = append(watchers, w)
	}
	return watchers, rows.Err()
}

// NotifyAddedToTask tells somebody they were put on a task. Being added is not
// something you would otherwise notice.
func (s *Service) NotifyAddedToTask(ctx context.Context, orgID, taskID, userID, actorID uuid.UUID) {
	if s.notifier == nil || userID == actorID {
		return
	}
	var taskTitle, actorName string
	if err := s.db.QueryRow(ctx, `
		SELECT t.title, COALESCE(a.name, 'Somebody')
		FROM tasks t LEFT JOIN users a ON a.id = $2
		WHERE t.id = $1
	`, taskID, actorID).Scan(&taskTitle, &actorName); err != nil {
		s.logger.Warn().Err(err).Msg("task add notification lookup failed")
		return
	}
	if err := s.notifier.Notify(ctx, orgID, userID, "task.added",
		"You were added to "+taskTitle,
		actorName+" added you to "+taskTitle,
		map[string]any{"taskId": taskID.String()}); err != nil {
		s.logger.Warn().Err(err).Msg("task add notification failed")
	}
}

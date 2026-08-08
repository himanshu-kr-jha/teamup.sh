package tasks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Notifier raises a notification for a user. Kept as an interface so tasks
// does not depend on the notifications package.
type Notifier interface {
	Notify(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, data map[string]any) error
}

type Service struct {
	db       *pgxpool.Pool
	logger   zerolog.Logger
	notifier Notifier
}

func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

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
	ID           uuid.UUID  `json:"id"`
	OrgID        uuid.UUID  `json:"orgId"`
	ProjectID    *uuid.UUID `json:"projectId,omitempty"`
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
}

type Label struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"orgId"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"createdAt"`
}

type TaskComment struct {
	ID        uuid.UUID  `json:"id"`
	TaskID    uuid.UUID  `json:"taskId"`
	UserID    uuid.UUID  `json:"userId"`
	UserName  string     `json:"userName,omitempty"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

type TaskActivity struct {
	ID        uuid.UUID `json:"id"`
	TaskID    uuid.UUID `json:"taskId"`
	UserID    uuid.UUID `json:"userId"`
	UserName  string    `json:"userName,omitempty"`
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
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// taskColumns keeps the reporter and assignee names alongside the row so a
// board can render without a lookup per card.
const taskColumns = `
	SELECT t.id, t.org_id, t.project_id, t.reporter_id, rep.name,
	       t.assignee_id, COALESCE(asg.name, ''), t.title, COALESCE(t.description, ''),
	       t.status, t.priority, t.task_type, t.story_points,
	       t.due_date, t.start_date, t.completed_at, t.parent_task_id,
	       COALESCE(t.position, 0), t.created_at, t.updated_at
	FROM tasks t
	JOIN users rep ON rep.id = t.reporter_id
	LEFT JOIN users asg ON asg.id = t.assignee_id`

func scanTask(row pgx.Row) (*Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.OrgID, &t.ProjectID, &t.ReporterID, &t.ReporterName,
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

	query := taskColumns + " WHERE " + strings.Join(where, " AND ") +
		" ORDER BY t.position, t.created_at DESC"

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	tasks := []Task{}
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.OrgID, &t.ProjectID, &t.ReporterID, &t.ReporterName,
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
	return s.attachLabels(ctx, tasks)
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

func (s *Service) CreateTask(ctx context.Context, orgID, reporterID uuid.UUID, title, description, status, priority, taskType string, projectID, assigneeID *uuid.UUID, dueDate, startDate *time.Time, labels []string) (*Task, error) {
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

	id := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO tasks (id, org_id, project_id, reporter_id, assignee_id, title, description,
		                   status, priority, task_type, due_date, start_date, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9, $10, $11, $12, NOW(), NOW())
	`, id, orgID, projectID, reporterID, assigneeID, title, description,
		status, priority, taskType, dueDate, startDate)
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
	created, err := s.GetTask(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	s.notifyAssignee(ctx, orgID, reporterID, created)
	return created, nil
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

// UpdateTask applies a partial change and records one activity entry per field
// that actually moved, so the task's history reflects what changed.
func (s *Service) UpdateTask(ctx context.Context, orgID, id, actorID uuid.UUID, updates map[string]interface{}) (*Task, error) {
	before, err := s.GetTask(ctx, orgID, id)
	if err != nil {
		return nil, err
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
		return before, nil
	}
	// Completing a task stamps completed_at; reopening clears it.
	if status, ok := updates["status"].(string); ok {
		if status == "done" {
			sets = append(sets, "completed_at = NOW()")
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

	for key, value := range updates {
		if _, ok := updatableFields[key]; !ok {
			continue
		}
		oldValue := beforeValue(before, key)
		newValue := fmt.Sprintf("%v", value)
		if oldValue == newValue {
			continue
		}
		if err := recordActivity(ctx, tx, id, actorID, key, oldValue, newValue); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	after, err := s.GetTask(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if _, reassigned := updates["assigneeId"]; reassigned {
		s.notifyAssignee(ctx, orgID, actorID, after)
	}
	return after, nil
}

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
		if t.AssigneeID == nil {
			return ""
		}
		return t.AssigneeID.String()
	default:
		return ""
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
func (s *Service) DeleteTask(ctx context.Context, orgID, id uuid.UUID) error {
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

func (s *Service) AddComment(ctx context.Context, orgID, taskID, userID uuid.UUID, content string) (*TaskComment, error) {
	var c TaskComment
	err := s.db.QueryRow(ctx, `
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

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := recordActivity(ctx, tx, taskID, userID, "comment", "", content); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &c, nil
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
		comments = append(comments, c)
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

func (s *Service) CreateLabel(ctx context.Context, orgID uuid.UUID, name, color string) (*Label, error) {
	if color == "" {
		color = "#6366f1"
	}
	var l Label
	err := s.db.QueryRow(ctx, `
		INSERT INTO labels (id, org_id, name, color, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, NOW())
		ON CONFLICT (org_id, name) DO UPDATE SET color = EXCLUDED.color
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
		SELECT a.id, a.task_id, a.user_id, u.name, a.field,
		       COALESCE(a.old_value, ''), COALESCE(a.new_value, ''), a.created_at
		FROM task_activities a
		JOIN tasks t ON t.id = a.task_id
		JOIN users u ON u.id = a.user_id
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
		if err := rows.Scan(&a.ID, &a.TaskID, &a.UserID, &a.UserName, &a.Field,
			&a.OldValue, &a.NewValue, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan activity: %w", err)
		}
		activities = append(activities, a)
	}
	return activities, rows.Err()
}

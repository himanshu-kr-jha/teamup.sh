package tasks

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type Service struct {
	db     *pgxpool.Pool
	logger zerolog.Logger
}

type Task struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	ProjectID    *uuid.UUID
	ReporterID   uuid.UUID
	AssigneeID   *uuid.UUID
	Title        string
	Description  string
	Status       string
	Priority     string
	TaskType     string
	StoryPoints  *int
	DueDate      *time.Time
	StartDate    *time.Time
	CompletedAt  *time.Time
	ParentTaskID *uuid.UUID
	Position     int
	Labels       []Label
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Label struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Name      string
	Color     string
	CreatedAt time.Time
}

type TaskComment struct {
	ID        uuid.UUID
	TaskID    uuid.UUID
	UserID    uuid.UUID
	Content   string
	CreatedAt time.Time
	UpdatedAt *time.Time
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

func (s *Service) ListTasks(ctx context.Context, orgID uuid.UUID, filters map[string]interface{}) ([]Task, error) {
	// SELECT * FROM tasks WHERE org_id = ... AND deleted_at IS NULL ...
	return nil, nil
}

func (s *Service) CreateTask(ctx context.Context, orgID, reporterID uuid.UUID, title, description, status, priority, taskType string, projectID, assigneeID *uuid.UUID, dueDate, startDate *time.Time, labels []string) (*Task, error) {
	task := &Task{
		ID:          uuid.New(),
		OrgID:       orgID,
		ProjectID:   projectID,
		ReporterID:  reporterID,
		AssigneeID:  assigneeID,
		Title:       title,
		Description: description,
		Status:      status,
		Priority:    priority,
		TaskType:    taskType,
		DueDate:     dueDate,
		StartDate:   startDate,
		Position:    0,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	// INSERT INTO tasks ...
	return task, nil
}

func (s *Service) GetTask(ctx context.Context, id uuid.UUID) (*Task, error) {
	// SELECT * FROM tasks WHERE id = ...
	return nil, nil
}

func (s *Service) UpdateTask(ctx context.Context, id uuid.UUID, updates map[string]interface{}) error {
	// UPDATE tasks SET ... WHERE id = ...
	return nil
}

func (s *Service) DeleteTask(ctx context.Context, id uuid.UUID) error {
	// UPDATE tasks SET deleted_at = NOW() WHERE id = ...
	return nil
}

func (s *Service) AddComment(ctx context.Context, taskID, userID uuid.UUID, content string) (*TaskComment, error) {
	comment := &TaskComment{
		ID:        uuid.New(),
		TaskID:    taskID,
		UserID:    userID,
		Content:   content,
		CreatedAt: time.Now(),
	}
	// INSERT INTO task_comments ...
	return comment, nil
}

func (s *Service) AddLabel(ctx context.Context, taskID, labelID uuid.UUID) error {
	// INSERT INTO task_labels ...
	return nil
}

func (s *Service) RemoveLabel(ctx context.Context, taskID, labelID uuid.UUID) error {
	// DELETE FROM task_labels WHERE task_id = ... AND label_id = ...
	return nil
}

func (s *Service) ListLabels(ctx context.Context, orgID uuid.UUID) ([]Label, error) {
	// SELECT * FROM labels WHERE org_id = ...
	return nil, nil
}

func (s *Service) CreateLabel(ctx context.Context, orgID uuid.UUID, name, color string) (*Label, error) {
	label := &Label{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      name,
		Color:     color,
		CreatedAt: time.Now(),
	}
	// INSERT INTO labels ...
	return label, nil
}

func (s *Service) GetActivities(ctx context.Context, taskID uuid.UUID) ([]TaskActivity, error) {
	// SELECT * FROM task_activities WHERE task_id = ... ORDER BY created_at DESC
	return nil, nil
}

type TaskActivity struct {
	ID        uuid.UUID
	TaskID    uuid.UUID
	UserID    uuid.UUID
	Field     string
	OldValue  string
	NewValue  string
	CreatedAt time.Time
}

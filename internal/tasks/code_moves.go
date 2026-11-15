package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// statusRank orders the statuses work moves through. Blocked is off to one
// side: a blocked task is somebody's decision, and is never moved by code.
var statusRank = map[string]int{"todo": 0, "in_progress": 1, "in_review": 2, "done": 3}

// TaskByKey finds a task by the key a branch, commit or pull request names.
func (s *Service) TaskByKey(ctx context.Context, orgID uuid.UUID, key string) (*Task, error) {
	// Or by a key it had before it moved into a project.
	return scanTask(s.db.QueryRow(ctx,
		taskColumns+` WHERE t.org_id = $1 AND t.deleted_at IS NULL AND (UPPER(t.task_key) = UPPER($2)
		   OR t.id = (SELECT a.task_id FROM task_key_aliases a WHERE a.org_id = $1 AND UPPER(a.task_key) = UPPER($2)))`, orgID, key))
}

// MoveByCode moves a task forward because of something done in GitHub or
// Bitbucket: a pull request opened, sent for review or merged. Only ever
// forward, so a task somebody has already moved on, or blocked, stays where
// they put it. Recorded as the provider's doing, with the person behind it
// when they are known. Reports whether it moved.
func (s *Service) MoveByCode(ctx context.Context, orgID, taskID uuid.UUID, to, source string, actor *uuid.UUID, why string) (bool, error) {
	return s.moveByCode(ctx, orgID, taskID, to, source, actor, why, false)
}

// ReopenByCode is MoveByCode for a pull request just opened, which is new work
// on its tasks: a task already done goes back to where the pull request puts
// it (in progress, or in review), as well as forward from anywhere else.
// Blocked still stays where somebody put it. A commit does not reopen
// anything, because merging a pull request makes one that names the task.
func (s *Service) ReopenByCode(ctx context.Context, orgID, taskID uuid.UUID, to, source string, actor *uuid.UUID, why string) (bool, error) {
	return s.moveByCode(ctx, orgID, taskID, to, source, actor, why, true)
}

func (s *Service) moveByCode(ctx context.Context, orgID, taskID uuid.UUID, to, source string, actor *uuid.UUID, why string, reopen bool) (bool, error) {
	toRank, ok := statusRank[to]
	if !ok {
		return false, fmt.Errorf("cannot move a task to %q", to)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var from, title string
	err = tx.QueryRow(ctx, `SELECT status, title FROM tasks WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL FOR UPDATE`,
		taskID, orgID).Scan(&from, &title)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fromRank, known := statusRank[from]
	reopening := reopen && from == "done" && to != "done"
	if !known || (fromRank >= toRank && !reopening) {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks SET status = $2::varchar, updated_at = NOW(),
		       completed_at = CASE WHEN $2::varchar = 'done' THEN COALESCE(completed_at, NOW()) ELSE NULL END
		WHERE id = $1
	`, taskID, to); err != nil {
		return false, fmt.Errorf("move task: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_activities (id, task_id, user_id, field, old_value, new_value, source, created_at)
		VALUES (gen_random_uuid(), $1, $2, 'status', $3, $4, $5, NOW())
	`, taskID, actor, from, to, source); err != nil {
		return false, fmt.Errorf("record move: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}

	// Everybody on the task hears of it, and their boards refresh with it.
	var except []uuid.UUID
	if actor != nil {
		except = append(except, *actor)
	}
	headline := title + " moved to " + strings.ReplaceAll(to, "_", " ")
	if reopening {
		headline = title + " was reopened, " + strings.ReplaceAll(to, "_", " ")
	}
	s.notifyInvolved(ctx, orgID, taskID, "task.updated", headline, why, except...)
	return true, nil
}

// MoveToProject puts a task in a project, or another project, and gives it that
// project's key: research filed as TASK-7 becomes PHO-31 once it is Photon's
// work, keeping its comments, history, files and sub-tasks. The keys it had keep
// finding it, so a branch named task-7-... or a commit that says TASK-7 still
// links to it. Anybody who may edit the task may move it. Returns the task as
// it now is; moving it to the project it is in already changes nothing.
func (s *Service) MoveToProject(ctx context.Context, orgID, taskID, actorID, projectID uuid.UUID) (*Task, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current *uuid.UUID
	var oldKey string
	err = tx.QueryRow(ctx, `SELECT project_id, COALESCE(task_key, '') FROM tasks
		WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL FOR UPDATE`, taskID, orgID).Scan(&current, &oldKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find task: %w", err)
	}
	if current != nil && *current == projectID {
		return s.GetTask(ctx, orgID, taskID)
	}
	var projectName string
	err = tx.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL`,
		projectID, orgID).Scan(&projectName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperrors.New(apperrors.CodeNotFound, "project not found")
	}
	if err != nil {
		return nil, fmt.Errorf("find project: %w", err)
	}
	newKey, err := nextTaskKey(ctx, tx, orgID, &projectID)
	if err != nil {
		return nil, err
	}
	if oldKey != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO task_key_aliases (org_id, task_key, task_id) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, orgID, oldKey, taskID); err != nil {
			return nil, fmt.Errorf("keep the old key: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET project_id = $2, task_key = $3, updated_at = NOW() WHERE id = $1`,
		taskID, projectID, newKey); err != nil {
		return nil, fmt.Errorf("move task: %w", err)
	}
	// Read as "moved it to Photon as PHO-31, was TASK-7".
	if _, err := tx.Exec(ctx, `
		INSERT INTO task_activities (id, task_id, user_id, field, old_value, new_value, created_at)
		VALUES (gen_random_uuid(), $1, $2, 'project', $3, $4, NOW())
	`, taskID, actorID, oldKey, projectName+" as "+newKey); err != nil {
		return nil, fmt.Errorf("record move: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, orgID, taskID)
}

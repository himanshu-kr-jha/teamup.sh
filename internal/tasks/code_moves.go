package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// statusRank orders the statuses work moves through. Blocked is off to one
// side: a blocked task is somebody's decision, and is never moved by code.
var statusRank = map[string]int{"todo": 0, "in_progress": 1, "in_review": 2, "done": 3}

// TaskByKey finds a task by the key a branch, commit or pull request names.
func (s *Service) TaskByKey(ctx context.Context, orgID uuid.UUID, key string) (*Task, error) {
	return scanTask(s.db.QueryRow(ctx,
		taskColumns+` WHERE t.org_id = $1 AND UPPER(t.task_key) = UPPER($2) AND t.deleted_at IS NULL`, orgID, key))
}

// MoveByCode moves a task forward because of something done in GitHub or
// Bitbucket: a pull request opened, sent for review or merged. Only ever
// forward, so a task somebody has already moved on, or blocked, stays where
// they put it. Recorded as the provider's doing, with the person behind it
// when they are known. Reports whether it moved.
func (s *Service) MoveByCode(ctx context.Context, orgID, taskID uuid.UUID, to, source string, actor *uuid.UUID, why string) (bool, error) {
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
	if !known || fromRank >= toRank {
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
	s.notifyInvolved(ctx, orgID, taskID, "task.updated", title+" moved to "+strings.ReplaceAll(to, "_", " "), why, except...)
	return true, nil
}

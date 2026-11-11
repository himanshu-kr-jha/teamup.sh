package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// DueTask is a task as a calendar shows it: on the day it is due.
type DueTask struct {
	ID       uuid.UUID `json:"id"`
	Key      string    `json:"key"`
	Title    string    `json:"title"`
	Status   string    `json:"status"`
	Priority string    `json:"priority"`
	DueDate  time.Time `json:"dueDate"`
}

// maxDueRange matches the calendar's widest read, a month of whole weeks with room.
const maxDueRange = 92 * 24 * time.Hour

// DueTasks is the work due in a range that is this person's to see through:
// what they hold, what they made and nobody holds yet, and what they were
// added to or follow.
func (s *Service) DueTasks(ctx context.Context, orgID, userID uuid.UUID, from, to time.Time) ([]DueTask, error) {
	if !to.After(from) {
		return nil, apperrors.New(apperrors.CodeBadRequest, "to must be after from")
	}
	if to.Sub(from) > maxDueRange {
		return nil, apperrors.New(apperrors.CodeBadRequest, "a read of due tasks covers at most 92 days")
	}
	rows, err := s.db.Query(ctx, `
		SELECT t.id, COALESCE(t.task_key, ''), t.title, t.status, t.priority, t.due_date
		FROM tasks t
		WHERE t.org_id = $1 AND t.deleted_at IS NULL
		  AND t.due_date >= $3 AND t.due_date < $4
		  AND (t.assignee_id = $2
		       OR (t.reporter_id = $2 AND t.assignee_id IS NULL)
		       OR EXISTS (SELECT 1 FROM task_watchers w WHERE w.task_id = t.id AND w.user_id = $2))
		ORDER BY t.due_date, t.title
	`, orgID, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("due tasks: %w", err)
	}
	defer rows.Close()
	out := []DueTask{}
	for rows.Next() {
		var t DueTask
		if err := rows.Scan(&t.ID, &t.Key, &t.Title, &t.Status, &t.Priority, &t.DueDate); err != nil {
			return nil, fmt.Errorf("scan due task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

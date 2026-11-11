package tasks

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Everybody a task concerns hears what happens to it: whoever holds it,
// whoever made it, and whoever was added to it or follows it. Nobody is told
// about what they did themself, and nobody is told twice about one thing.

// involvedIn lists the people a task concerns, less the ones named in except.
func (s *Service) involvedIn(ctx context.Context, orgID, taskID uuid.UUID, except ...uuid.UUID) ([]uuid.UUID, error) {
	// Never nil: a NULL array would make the filter below drop everybody.
	skip := append([]uuid.UUID{}, except...)
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT u.id FROM (
			SELECT assignee_id AS person FROM tasks WHERE id = $1 AND org_id = $2
			UNION
			SELECT reporter_id FROM tasks WHERE id = $1 AND org_id = $2
			UNION
			SELECT w.user_id FROM task_watchers w JOIN tasks t ON t.id = w.task_id
			WHERE w.task_id = $1 AND t.org_id = $2
		) involved
		JOIN users u ON u.id = involved.person AND u.deleted_at IS NULL
		WHERE NOT (u.id = ANY($3))
	`, taskID, orgID, skip)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var people []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		people = append(people, id)
	}
	return people, rows.Err()
}

// notifyInvolved raises one notification for each person a task concerns.
// Best effort: whatever happened to the task has happened either way.
func (s *Service) notifyInvolved(ctx context.Context, orgID, taskID uuid.UUID, kind, title, body string, except ...uuid.UUID) {
	if s.notifier == nil {
		return
	}
	people, err := s.involvedIn(ctx, orgID, taskID, except...)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not work out who a task concerns")
		return
	}
	for _, person := range people {
		if err := s.notifier.Notify(ctx, orgID, person, kind, title, body,
			map[string]any{"taskId": taskID.String()}); err != nil {
			s.logger.Warn().Err(err).Str("kind", kind).Msg("task notification failed")
		}
	}
}

// personName is what a notification calls somebody.
func (s *Service) personName(ctx context.Context, id uuid.UUID) string {
	var name string
	if err := s.db.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, id).Scan(&name); err != nil || name == "" {
		return "Somebody"
	}
	return name
}

var statusWords = map[string]string{
	"todo": "To do", "in_progress": "In progress", "in_review": "In review", "done": "Done", "blocked": "Blocked",
}

// changeOrder is the order a change is described in, most telling first.
// Position is left out: dragging a card up a column changes nothing anybody
// needs to hear about.
var changeOrder = []string{"status", "assigneeId", "dueDate", "priority", "title", "description", "startDate", "storyPoints", "taskType", "parentTaskId"}

// describeChanges says in a line what changed on a task, or "" when nothing
// worth telling anybody did.
func describeChanges(changed map[string]bool, after *Task) string {
	var parts []string
	for _, key := range changeOrder {
		if !changed[key] {
			continue
		}
		switch key {
		case "status":
			word := statusWords[after.Status]
			if word == "" {
				word = after.Status
			}
			parts = append(parts, "status is now "+word)
		case "assigneeId":
			if after.AssigneeName != "" {
				parts = append(parts, "assigned to "+after.AssigneeName)
			} else {
				parts = append(parts, "nobody is assigned now")
			}
		case "dueDate":
			if after.DueDate == nil {
				parts = append(parts, "due date removed")
			} else {
				parts = append(parts, "due date changed")
			}
		case "priority":
			parts = append(parts, "priority is now "+after.Priority)
		case "title":
			parts = append(parts, "renamed")
		case "description":
			parts = append(parts, "description changed")
		case "startDate":
			parts = append(parts, "start date changed")
		case "storyPoints":
			parts = append(parts, "story points changed")
		case "taskType":
			parts = append(parts, "type is now "+after.TaskType)
		case "parentTaskId":
			parts = append(parts, "moved under another task")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	line := strings.Join(parts, ", ")
	return strings.ToUpper(line[:1]) + line[1:]
}

// notifyComment tells the people a task concerns that somebody wrote on it,
// less the author and anybody the comment named, who have heard already.
func (s *Service) notifyComment(ctx context.Context, orgID, taskID uuid.UUID, c *TaskComment, mentioned []uuid.UUID) {
	if s.notifier == nil {
		return
	}
	var title string
	if err := s.db.QueryRow(ctx, `SELECT title FROM tasks WHERE id = $1 AND org_id = $2`, taskID, orgID).Scan(&title); err != nil {
		s.logger.Warn().Err(err).Msg("comment notification lookup failed")
		return
	}
	body := strings.TrimSpace(c.Content)
	if body == "" {
		body = "Attached a file"
	}
	s.notifyInvolved(ctx, orgID, taskID, "task.comment",
		s.personName(ctx, c.UserID)+" commented on "+title, body,
		append([]uuid.UUID{c.UserID}, mentioned...)...)
}

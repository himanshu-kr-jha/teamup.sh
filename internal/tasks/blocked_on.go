package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// A blocked task says what it is blocked on: a short note, and optionally the
// person and the task it is waiting for. Only a blocked task has one. It is set
// when the task becomes blocked (with an empty note if the caller gave none, so
// older clients that only send a status keep working and the task reads "No
// reason given"), can be changed while it stays blocked, and is cleared when it
// moves on. Every change is written to the task's activity, so what it said is
// kept after it is cleared.

const blockedNoteMaxLength = 1000

// BlockedOn is what a blocked task is waiting for.
type BlockedOn struct {
	Note string           `json:"note"`
	User *PersonRef       `json:"user"`
	Task *BlockingTaskRef `json:"task"`
}

// BlockingTaskRef names the task another one waits on.
type BlockingTaskRef struct {
	ID    uuid.UUID `json:"id"`
	Title string    `json:"title"`
	Key   string    `json:"key,omitempty"`
}

// blockedRaw is how the columns read before they are made a BlockedOn.
type blockedRaw struct {
	note      *string
	userID    *uuid.UUID
	userName  *string
	taskID    *uuid.UUID
	taskTitle *string
	taskKey   *string
}

func (r blockedRaw) blockedOn(status string) *BlockedOn {
	if status != "blocked" {
		return nil
	}
	b := &BlockedOn{}
	if r.note != nil {
		b.Note = *r.note
	}
	if r.userID != nil && r.userName != nil {
		b.User = &PersonRef{ID: *r.userID, Name: *r.userName}
	}
	if r.taskID != nil && r.taskTitle != nil {
		b.Task = &BlockingTaskRef{ID: *r.taskID, Title: *r.taskTitle}
		if r.taskKey != nil {
			b.Task.Key = *r.taskKey
		}
	}
	return b
}

// blockedColumns reads a blocked task's reason, from a tasks table aliased t.
// Subqueries rather than joins, so any query over tasks can add it.
const blockedColumns = `t.blocked_note, t.blocked_by_user_id,
	(SELECT bu.name FROM users bu WHERE bu.id = t.blocked_by_user_id AND bu.deleted_at IS NULL),
	t.blocked_by_task_id,
	(SELECT bt.title FROM tasks bt WHERE bt.id = t.blocked_by_task_id AND bt.deleted_at IS NULL),
	(SELECT NULLIF(bt.task_key, '') FROM tasks bt WHERE bt.id = t.blocked_by_task_id AND bt.deleted_at IS NULL)`

func (r *blockedRaw) targets() []any {
	return []any{&r.note, &r.userID, &r.userName, &r.taskID, &r.taskTitle, &r.taskKey}
}

// blockedInput is a blockedOn value as a caller sends it.
type blockedInput struct {
	Note   string
	UserID *uuid.UUID
	TaskID *uuid.UUID
}

// parseBlockedOn reads {"note": "...", "userId": "...", "taskId": "..."}. A
// null means no reason.
func parseBlockedOn(raw any) (blockedInput, error) {
	var in blockedInput
	if raw == nil {
		return in, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return in, apperrors.New(apperrors.CodeBadRequest, "blockedOn must be an object")
	}
	if note, present := m["note"]; present && note != nil {
		text, isString := note.(string)
		if !isString {
			return in, apperrors.New(apperrors.CodeBadRequest, "invalid blockedOn note")
		}
		in.Note = strings.TrimSpace(text)
		if utf8.RuneCountInString(in.Note) > blockedNoteMaxLength {
			return in, apperrors.New(apperrors.CodeBadRequest, "that blocked-on note is too long")
		}
	}
	id := func(key string) (*uuid.UUID, error) {
		v, present := m[key]
		if !present || v == nil || v == "" {
			return nil, nil
		}
		text, isString := v.(string)
		if !isString {
			return nil, apperrors.New(apperrors.CodeBadRequest, "invalid blockedOn "+key)
		}
		parsed, err := uuid.Parse(text)
		if err != nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "invalid blockedOn "+key)
		}
		return &parsed, nil
	}
	var err error
	if in.UserID, err = id("userId"); err != nil {
		return in, err
	}
	if in.TaskID, err = id("taskId"); err != nil {
		return in, err
	}
	return in, nil
}

// checkBlockedOn makes sure the person and the task named are in this
// organization, and that a task is not waiting on itself.
func (s *Service) checkBlockedOn(ctx context.Context, orgID, taskID uuid.UUID, in blockedInput) error {
	if in.UserID != nil {
		var ok bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
			*in.UserID, orgID).Scan(&ok); err != nil {
			return fmt.Errorf("check blocked-on person: %w", err)
		}
		if !ok {
			return apperrors.New(apperrors.CodeBadRequest, "that person is not in this organization")
		}
	}
	if in.TaskID != nil {
		if *in.TaskID == taskID {
			return apperrors.New(apperrors.CodeBadRequest, "a task cannot be blocked on itself")
		}
		var ok bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL)`,
			*in.TaskID, orgID).Scan(&ok); err != nil {
			return fmt.Errorf("check blocked-on task: %w", err)
		}
		if !ok {
			return apperrors.New(apperrors.CodeBadRequest, "that task is not in this organization")
		}
	}
	return nil
}

// blockedPlan is what an update does to the reason: nothing, set it, or clear it.
type blockedPlan struct {
	set   bool
	clear bool
	in    blockedInput
}

// planBlockedOn decides, from the task as it was and the update asked for,
// what happens to its reason.
func (s *Service) planBlockedOn(ctx context.Context, orgID uuid.UUID, before *Task, updates map[string]any) (blockedPlan, error) {
	status := before.Status
	if next, ok := updates["status"].(string); ok {
		status = next
	}
	raw, given := updates["blockedOn"]
	switch {
	case status == "blocked" && given:
		in, err := parseBlockedOn(raw)
		if err != nil {
			return blockedPlan{}, err
		}
		if err := s.checkBlockedOn(ctx, orgID, before.ID, in); err != nil {
			return blockedPlan{}, err
		}
		return blockedPlan{set: true, in: in}, nil
	case status == "blocked" && before.Status != "blocked":
		// Blocked without a reason: an older client, or the API. It reads as
		// "No reason given" until somebody says.
		return blockedPlan{set: true}, nil
	case given && raw != nil:
		return blockedPlan{}, apperrors.New(apperrors.CodeBadRequest, "only a blocked task is blocked on something")
	case status != "blocked" && before.Status == "blocked":
		return blockedPlan{clear: true}, nil
	}
	return blockedPlan{}, nil
}

// apply adds the plan's columns to an UPDATE's SET list.
func (p blockedPlan) apply(sets []string, args []any) ([]string, []any) {
	switch {
	case p.set:
		args = append(args, p.in.Note, p.in.UserID, p.in.TaskID)
		n := len(args)
		sets = append(sets, fmt.Sprintf("blocked_note = $%d, blocked_by_user_id = $%d, blocked_by_task_id = $%d", n-2, n-1, n))
	case p.clear:
		sets = append(sets, "blocked_note = NULL, blocked_by_user_id = NULL, blocked_by_task_id = NULL")
	}
	return sets, args
}

// describeBlockedOn is the reason in words, for the task's activity: the note,
// then who and what it waits on.
func describeBlockedOn(b *BlockedOn) string {
	if b == nil {
		return ""
	}
	parts := []string{}
	if b.Note != "" {
		parts = append(parts, b.Note)
	}
	if b.User != nil {
		parts = append(parts, "waiting on "+b.User.Name)
	}
	if b.Task != nil {
		parts = append(parts, "waiting for "+b.Task.Title)
	}
	if len(parts) == 0 {
		return "No reason given"
	}
	return strings.Join(parts, "; ")
}

// setBlockedOn writes a reason straight onto a task that was just made blocked,
// as a new task is, with no activity of its own.
func (s *Service) setBlockedOn(ctx context.Context, orgID, taskID uuid.UUID, in blockedInput) error {
	_, err := s.db.Exec(ctx, `
		UPDATE tasks SET blocked_note = $3, blocked_by_user_id = $4, blocked_by_task_id = $5
		WHERE id = $1 AND org_id = $2 AND status = 'blocked' AND deleted_at IS NULL
	`, taskID, orgID, in.Note, in.UserID, in.TaskID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("set blocked on: %w", err)
	}
	return nil
}

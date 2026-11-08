package notetaker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// NotesItem is one meeting on the Meeting notes page.
type NotesItem struct {
	CallID      string    `json:"callId"`
	Title       string    `json:"title"`
	Date        time.Time `json:"date"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary,omitempty"`
	ActionItems int       `json:"actionItems"`
	Attendees   []string  `json:"attendees"`
	Error       string    `json:"error,omitempty"`
	// Attended is false for a meeting an administrator sees without having
	// been in it.
	Attended bool `json:"attended"`
}

// ListQuery is which meetings, newest first.
type ListQuery struct {
	// All is every meeting in the organization, for an administrator;
	// otherwise only the ones the caller was in.
	All    bool
	Search string
	Before *time.Time
	Limit  int
}

// List returns the meetings with notes that somebody may read, and whether
// there are older ones.
func (s *Service) List(ctx context.Context, orgID, userID uuid.UUID, isAdmin bool, q ListQuery) ([]NotesItem, bool, error) {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 30
	}
	all := q.All && isAdmin
	search := strings.TrimSpace(q.Search)
	rows, err := s.db.Query(ctx, `
		WITH m AS (
			SELECT n.call_id, n.status, n.minutes, COALESCE(n.error, '') AS error,
			       COALESCE(NULLIF(c.title, ''), ch.name, 'Call') AS title,
			       COALESCE(c.started_at, c.created_at) AS at,
			       EXISTS (SELECT 1 FROM call_participants p
			               WHERE p.call_id = n.call_id AND p.user_id = $2 AND p.joined_at IS NOT NULL) AS attended
			FROM meeting_notes n
			JOIN calls c ON c.id = n.call_id
			LEFT JOIN channels ch ON ch.id = c.channel_id
			WHERE n.org_id = $1
		)
		SELECT m.call_id, m.title, m.at, m.status, m.minutes, m.error, m.attended,
		       COALESCE((SELECT array_agg(u.name ORDER BY p.joined_at) FROM call_participants p
		                 JOIN users u ON u.id = p.user_id
		                 WHERE p.call_id = m.call_id AND p.joined_at IS NOT NULL), '{}')
		FROM m
		WHERE ($3 OR m.attended)
		  AND ($4::timestamptz IS NULL OR m.at < $4)
		  AND ($5 = '' OR m.title ILIKE '%' || $5 || '%' OR m.minutes->>'summary' ILIKE '%' || $5 || '%')
		ORDER BY m.at DESC, m.call_id
		LIMIT $6`,
		orgID, userID, all, q.Before, escapeLike(search), q.Limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list notes: %w", err)
	}
	defer rows.Close()
	out := []NotesItem{}
	for rows.Next() {
		var it NotesItem
		var id uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &it.Title, &it.Date, &it.Status, &raw, &it.Error, &it.Attended, &it.Attendees); err != nil {
			return nil, false, err
		}
		it.CallID = id.String()
		if len(raw) > 0 {
			var m Minutes
			if json.Unmarshal(raw, &m) == nil {
				it.Summary, it.ActionItems = m.Summary, len(m.ActionItems)
			}
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > q.Limit
	if more {
		out = out[:q.Limit]
	}
	return out, more, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ErrCannotRetry is minutes that are not failed: ready, still being taken or
// written, or with nothing to write them from.
var ErrCannotRetry = errors.New("only minutes that failed can be written again")

// Retry writes a call's minutes again from its saved transcript, with the
// settings as they are now, for somebody who may read them. It returns at once;
// the minutes arrive as they do after a call.
func (s *Service) Retry(ctx context.Context, orgID, callID, userID uuid.UUID, isAdmin bool) error {
	var status string
	err := s.db.QueryRow(ctx, `SELECT status FROM meeting_notes WHERE call_id = $1 AND org_id = $2`, callID, orgID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoNotes
	}
	if err != nil {
		return err
	}
	if ok, err := s.mayRead(ctx, callID, userID, isAdmin); err != nil {
		return err
	} else if !ok {
		return ErrNoNotes
	}
	set, err := s.Settings(ctx, orgID)
	if err != nil {
		return err
	}
	// Claimed, so two clicks write them once.
	tag, err := s.db.Exec(ctx, `UPDATE meeting_notes SET status = 'writing', error = NULL WHERE call_id = $1 AND status = 'failed'`, callID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCannotRetry
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		s.compose(ctx, orgID, callID, set, "")
	}()
	return nil
}

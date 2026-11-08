package chat

// History a day at a time.
//
// A conversation opens on the newest day that has anything in it, and scrolling
// up brings the day before that, whatever its date. A day is a day where the
// reader is, so the reader's time zone comes with the request. Asking again from
// a cursor inside a day returns the rest of that day, which is how a day busier
// than the cap is read in more than one go.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MaxDayMessages is the most one read returns, however busy the day.
const MaxDayMessages = 300

// GetMessagesForDay returns the top-level messages of the newest day, in loc,
// that has any before the cursor (or at all, without one), oldest first, and
// whether anything older remains.
func (s *Service) GetMessagesForDay(ctx context.Context, channelID uuid.UUID, before *time.Time, beforeID *uuid.UUID, loc *time.Location) ([]Message, bool, error) {
	if loc == nil {
		loc = time.UTC
	}
	const cursor = `($2::timestamptz IS NULL
		       OR (m.created_at, m.id) < ($2, COALESCE($3, '00000000-0000-0000-0000-000000000000'::uuid)))`

	var newest time.Time
	err := s.db.QueryRow(ctx, `
		SELECT m.created_at FROM messages m
		WHERE m.channel_id = $1 AND m.reply_to IS NULL AND `+cursor+`
		ORDER BY m.created_at DESC, m.id DESC LIMIT 1
	`, channelID, before, beforeID).Scan(&newest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []Message{}, false, nil
		}
		return nil, false, fmt.Errorf("find the newest day: %w", err)
	}
	local := newest.In(loc)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	rows, err := s.db.Query(ctx, `
		SELECT `+messageColumns+`
		FROM messages m
		JOIN users u ON u.id = m.user_id
		WHERE m.channel_id = $1
		  AND m.reply_to IS NULL
		  AND `+cursor+`
		  AND m.created_at >= $4
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $5
	`, channelID, before, beforeID, dayStart, MaxDayMessages)
	if err != nil {
		return nil, false, fmt.Errorf("get a day of messages: %w", err)
	}
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, false, err
	}
	reverse(messages)

	more := false
	if len(messages) > 0 {
		oldest := messages[0]
		if err := s.db.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM messages m
			WHERE m.channel_id = $1 AND m.reply_to IS NULL AND (m.created_at, m.id) < ($2, $3))
		`, channelID, oldest.CreatedAt, oldest.ID).Scan(&more); err != nil {
			return nil, false, fmt.Errorf("check for older messages: %w", err)
		}
	}
	hydrated, err := s.hydrate(ctx, channelID, messages)
	return hydrated, more, err
}

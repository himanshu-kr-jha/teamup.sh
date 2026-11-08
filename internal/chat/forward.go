package chat

// Forwarding passes a message on to other conversations, files and all, marked
// as forwarded.
//
// A file in chat is readable by the members of the conversation it was sent
// in, and the storage path is what says which conversation that is. Forwarding
// one therefore copies it, inside the object store, into each conversation it
// goes to: the people there can open their copy, and nobody gains a way into
// the conversation it came from.

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apperrors "github.com/teamup/teamup/pkg/errors"
)

// ObjectCopier copies a stored object to a new key without it leaving storage.
type ObjectCopier interface {
	CopyObject(ctx context.Context, srcKey, dstKey string) error
}

// MaxForwardTargets is how many conversations one forward can reach.
const MaxForwardTargets = 10

// ForwardMessage copies a message into each of the targets as a new message
// from userID, marked forwarded. It needs the person to be able to read the
// original and to post in every target.
func (s *Service) ForwardMessage(ctx context.Context, orgID, messageID, userID uuid.UUID, targets []uuid.UUID, isOrgAdmin bool) ([]*Message, error) {
	targets = distinct(targets)
	if len(targets) == 0 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "choose where to forward it")
	}
	if len(targets) > MaxForwardTargets {
		return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf("a message can be forwarded to at most %d conversations at once", MaxForwardTargets))
	}

	var sourceChannel uuid.UUID
	var content, kind, channelType string
	var deleted bool
	err := s.db.QueryRow(ctx, `
		SELECT m.channel_id, m.content, COALESCE(m.kind, 'user'), m.deleted_at IS NOT NULL, c.type
		FROM messages m JOIN channels c ON c.id = m.channel_id
		WHERE m.id = $1 AND m.org_id = $2 AND c.deleted_at IS NULL
	`, messageID, orgID).Scan(&sourceChannel, &content, &kind, &deleted, &channelType)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, fmt.Errorf("load message to forward: %w", err)
	}
	// Not being able to read it looks exactly like it not existing.
	if !isOpenToOrg(channelType) && !s.isMember(ctx, sourceChannel, userID) {
		return nil, apperrors.ErrNotFound
	}
	if deleted {
		return nil, apperrors.New(apperrors.CodeBadRequest, "that message was deleted")
	}
	if kind != "user" {
		return nil, apperrors.New(apperrors.CodeBadRequest, "only messages somebody wrote can be forwarded")
	}

	files, err := s.messageFiles(ctx, messageID)
	if err != nil {
		return nil, err
	}
	if len(files) > 0 && s.copier == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "file storage is not available, so files cannot be forwarded")
	}

	// Every target is checked before anything is sent, so a refusal for one
	// does not leave the others half done.
	for _, target := range targets {
		if err := s.AuthorizePost(ctx, orgID, target, userID, isOrgAdmin); err != nil {
			return nil, err
		}
	}

	sent := make([]*Message, 0, len(targets))
	for _, target := range targets {
		msg := &Message{
			ID:        uuid.New(),
			ChannelID: target,
			UserID:    userID,
			Content:   content,
			Forwarded: true,
			Reactions: map[string][]string{},
			CreatedAt: time.Now(),
		}

		copies := make([]Attachment, 0, len(files))
		for _, f := range files {
			dst := fmt.Sprintf("org-%s/chat/%s/%s-%s", orgID, target, uuid.NewString()[:8], path.Base(f.ObjectKey))
			if err := s.copier.CopyObject(ctx, f.ObjectKey, dst); err != nil {
				return sent, fmt.Errorf("copy %s for forwarding: %w", f.Filename, err)
			}
			c := f
			c.ObjectKey = dst
			c.ThumbnailKey = nil
			copies = append(copies, c)
		}

		if err := s.createMessage(ctx, msg); err != nil {
			return sent, err
		}
		msg.Attachments = []Attachment{}
		for _, c := range copies {
			if err := s.db.QueryRow(ctx, `
				INSERT INTO attachments (org_id, uploaded_by, category, ref_id, object_key, filename, content_type, size_bytes, message_id)
				VALUES ($1, $2, 'chat', $3, $4, $5, $6, $7, $8)
				RETURNING id
			`, orgID, userID, target.String(), c.ObjectKey, c.Filename, c.ContentType, c.SizeBytes, msg.ID).Scan(&c.ID); err != nil {
				return sent, fmt.Errorf("record forwarded file: %w", err)
			}
			msg.Attachments = append(msg.Attachments, c)
		}
		s.deliver(ctx, msg)
		sent = append(sent, msg)
	}
	return sent, nil
}

func (s *Service) messageFiles(ctx context.Context, messageID uuid.UUID) ([]Attachment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, filename, content_type, size_bytes, object_key, thumbnail_key
		FROM attachments WHERE message_id = $1 AND deleted_at IS NULL
		ORDER BY created_at
	`, messageID)
	if err != nil {
		return nil, fmt.Errorf("load files to forward: %w", err)
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.ObjectKey, &a.ThumbnailKey); err != nil {
			return nil, fmt.Errorf("scan file to forward: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func distinct(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	apperrors "github.com/teamup/teamup/pkg/errors"
	"github.com/teamup/teamup/pkg/protocol"
)

type Config struct {
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string
}

// Broadcaster delivers a live notification over the existing WebSocket.
type Broadcaster interface {
	BroadcastToUser(userID string, msg *protocol.Message)
}

type Service struct {
	db          *pgxpool.Pool
	redis       *redis.Client
	cfg         Config
	logger      zerolog.Logger
	broadcaster Broadcaster
}

func (s *Service) SetBroadcaster(b Broadcaster) { s.broadcaster = b }

// SetVAPIDKeys lets the keys stored with the organization at setup take
// precedence over the environment. Without this an instance provisioned
// through the wizard has no working keys unless TEAMUP_VAPID_* is also set.
func (s *Service) SetVAPIDKeys(publicKey, privateKey string) {
	if publicKey != "" && privateKey != "" {
		s.cfg.VAPIDPublicKey = publicKey
		s.cfg.VAPIDPrivateKey = privateKey
	}
}

// HasVAPIDKeys reports whether push can actually be signed.
func (s *Service) HasVAPIDKeys() bool {
	return s.cfg.VAPIDPublicKey != "" && s.cfg.VAPIDPrivateKey != ""
}

func (s *Service) VAPIDPublicKey() string { return s.cfg.VAPIDPublicKey }

type PushSubscription struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	UserID     uuid.UUID
	Endpoint   string
	P256DH     string
	Auth       string
	UserAgent  string
	DeviceName string
	CreatedAt  time.Time
}

type PushPayload struct {
	Title              string                 `json:"title"`
	Body               string                 `json:"body"`
	Icon               string                 `json:"icon,omitempty"`
	Badge              string                 `json:"badge,omitempty"`
	Image              string                 `json:"image,omitempty"`
	Tag                string                 `json:"tag,omitempty"`
	Renotify           bool                   `json:"renotify,omitempty"`
	Silent             bool                   `json:"silent,omitempty"`
	Data               map[string]interface{} `json:"data,omitempty"`
	Actions            []NotificationAction   `json:"actions,omitempty"`
	RequireInteraction bool                   `json:"requireInteraction,omitempty"`
	TTL                int                    `json:"ttl,omitempty"`
	Urgency            string                 `json:"urgency,omitempty"`
}

type NotificationAction struct {
	Action string `json:"action"`
	Title  string `json:"title"`
	Icon   string `json:"icon,omitempty"`
}

type Notification struct {
	ID         uuid.UUID       `json:"id"`
	OrgID      uuid.UUID       `json:"orgId"`
	UserID     uuid.UUID       `json:"userId"`
	Type       string          `json:"type"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	Data       json.RawMessage `json:"data"`
	ReadAt     *time.Time      `json:"readAt,omitempty"`
	ArchivedAt *time.Time      `json:"archivedAt,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
}

func NewService(db *pgxpool.Pool, redis *redis.Client, cfg Config) *Service {
	return &Service{
		db:    db,
		redis: redis,
		cfg:   cfg,
	}
}

// SetLogger wires real logging. Left at the zero value every diagnostic in
// this package was written to a nil writer and silently discarded — including
// the fail-closed paths that swallow an error and deny access.
func (s *Service) SetLogger(l zerolog.Logger) {
	s.logger = l.With().Str("component", "notifications").Logger()
}

// SaveSubscription stores a device's push endpoint. Re-subscribing from the
// same device updates the existing row rather than accumulating duplicates —
// browsers rotate keys and would otherwise leave dead endpoints behind.
func (s *Service) SaveSubscription(ctx context.Context, userID, orgID string, sub *PushSubscription) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return apperrors.New(apperrors.CodeBadRequest, "invalid user")
	}
	oid, err := uuid.Parse(orgID)
	if err != nil {
		return apperrors.New(apperrors.CodeBadRequest, "invalid organization")
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO push_subscriptions (id, org_id, user_id, endpoint, p256dh, auth, user_agent, device_name, last_used_at, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), NOW(), NOW())
		ON CONFLICT (user_id, endpoint) DO UPDATE
		SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
		    device_name = EXCLUDED.device_name, last_used_at = NOW()
	`, oid, uid, sub.Endpoint, sub.P256DH, sub.Auth, sub.UserAgent, sub.DeviceName)
	if err != nil {
		return fmt.Errorf("save push subscription: %w", err)
	}
	return nil
}

// DeleteSubscriptionByEndpoint unsubscribes one device.
func (s *Service) DeleteSubscriptionByEndpoint(ctx context.Context, userID, endpoint string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return apperrors.New(apperrors.CodeBadRequest, "invalid user")
	}
	_, err = s.db.Exec(ctx,
		`DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`, uid, endpoint)
	if err != nil {
		return fmt.Errorf("delete push subscription: %w", err)
	}
	return nil
}

func (s *Service) SendToUser(ctx context.Context, userID string, payload PushPayload) error {
	if !s.HasVAPIDKeys() {
		return nil // push not configured; in-app notification still stands
	}
	subs, err := s.getSubscriptions(ctx, userID)
	if err != nil {
		return err
	}

	// Bounded and synchronous with respect to the caller's context: the old
	// unbounded `go` per subscription leaked goroutines whenever the push
	// service hung, with no way to cancel them.
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, sub := range subs {
		wg.Add(1)
		sem <- struct{}{}
		go func(sub PushSubscription) {
			defer wg.Done()
			defer func() { <-sem }()
			s.sendToSubscription(ctx, sub, payload)
		}(sub)
	}
	wg.Wait()
	return nil
}

func (s *Service) sendToSubscription(ctx context.Context, sub PushSubscription, payload PushPayload) {
	pushSub := &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256DH,
			Auth:   sub.Auth,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		s.logger.Error().Err(err).Msg("encode push payload")
		return
	}

	resp, err := webpush.SendNotification(body, pushSub, &webpush.Options{
		VAPIDPublicKey:  s.cfg.VAPIDPublicKey,
		VAPIDPrivateKey: s.cfg.VAPIDPrivateKey,
		Subscriber:      s.cfg.VAPIDSubject,
		TTL:             3600,
		HTTPClient:      &http.Client{Timeout: 10 * time.Second},
	})
	if err != nil {
		s.logger.Warn().Err(err).Str("endpoint", sub.Endpoint).Msg("push delivery failed")
		return
	}
	// The body has to be drained and closed or every push leaks a connection.
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	// A dead endpoint answers 404/410 with no error, so checking only err
	// meant expired subscriptions were never pruned.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		if err := s.deleteSubscription(ctx, sub.ID); err != nil {
			s.logger.Warn().Err(err).Msg("could not prune expired push subscription")
		}
	}
}

func (s *Service) getSubscriptions(ctx context.Context, userID string) ([]PushSubscription, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "invalid user")
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, org_id, user_id, endpoint, p256dh, auth,
		       COALESCE(user_agent, ''), COALESCE(device_name, ''), created_at
		FROM push_subscriptions WHERE user_id = $1
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	defer rows.Close()

	subs := []PushSubscription{}
	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.ID, &p.OrgID, &p.UserID, &p.Endpoint, &p.P256DH, &p.Auth,
			&p.UserAgent, &p.DeviceName, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan push subscription: %w", err)
		}
		subs = append(subs, p)
	}
	return subs, rows.Err()
}

func (s *Service) deleteSubscription(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete push subscription: %w", err)
	}
	return nil
}

// CreateNotification stores the notification and pushes it live over the
// existing WebSocket, so the bell updates without a refresh.
func (s *Service) CreateNotification(ctx context.Context, notif *Notification) error {
	if notif.ID == uuid.Nil {
		notif.ID = uuid.New()
	}
	if len(notif.Data) == 0 {
		notif.Data = json.RawMessage(`{}`)
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO notifications (id, org_id, user_id, type, title, body, data, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		RETURNING created_at
	`, notif.ID, notif.OrgID, notif.UserID, notif.Type, notif.Title, notif.Body, notif.Data).
		Scan(&notif.CreatedAt)
	if err != nil {
		return fmt.Errorf("create notification: %w", err)
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(notif.UserID.String(), protocol.NewMessage(
			protocol.TypeNotificationNew, map[string]any{
				"id": notif.ID.String(), "type": notif.Type,
				"title": notif.Title, "body": notif.Body,
				"data": notif.Data, "createdAt": notif.CreatedAt,
			}))
	}
	return nil
}

// Notify is the convenience entry point used by the features that raise
// notifications: it records the in-app item and fans out a web push.
func (s *Service) Notify(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, data map[string]any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		raw = []byte(`{}`)
	}
	notif := &Notification{
		OrgID: orgID, UserID: userID, Type: kind,
		Title: title, Body: body, Data: raw,
	}
	if err := s.CreateNotification(ctx, notif); err != nil {
		return err
	}
	// Push is best effort: a missing subscription must not fail the action
	// that triggered the notification.
	if err := s.SendToUser(ctx, userID.String(), PushPayload{
		Title: title, Body: body, Tag: kind, Data: data,
	}); err != nil {
		s.logger.Warn().Err(err).Str("user", userID.String()).Msg("web push delivery failed")
	}
	return nil
}

func (s *Service) ListNotifications(ctx context.Context, userID string, unreadOnly bool, limit int) ([]Notification, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "invalid user")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, org_id, user_id, type, title, body, COALESCE(data, '{}'::jsonb),
		       read_at, archived_at, created_at
		FROM notifications
		WHERE user_id = $1 AND archived_at IS NULL AND ($2 = false OR read_at IS NULL)
		ORDER BY created_at DESC LIMIT $3
	`, uid, unreadOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	items := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.OrgID, &n.UserID, &n.Type, &n.Title, &n.Body,
			&n.Data, &n.ReadAt, &n.ArchivedAt, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		items = append(items, n)
	}
	return items, rows.Err()
}

func (s *Service) UnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM notifications
		WHERE user_id = $1 AND read_at IS NULL AND archived_at IS NULL
	`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count unread: %w", err)
	}
	return count, nil
}

// MarkRead is scoped to the recipient: a notification id must not be
// actionable by anyone else.
func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications SET read_at = NOW() WHERE id = $1 AND user_id = $2 AND read_at IS NULL`,
		id, userID)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.db.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM notifications WHERE id = $1 AND user_id = $2)`,
			id, userID).Scan(&exists); err != nil {
			return fmt.Errorf("verify notification: %w", err)
		}
		if !exists {
			return apperrors.ErrNotFound
		}
	}
	return nil
}

func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE notifications SET read_at = NOW() WHERE user_id = $1 AND read_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("mark all read: %w", err)
	}
	return nil
}

func (s *Service) Archive(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications SET archived_at = NOW() WHERE id = $1 AND user_id = $2 AND archived_at IS NULL`,
		id, userID)
	if err != nil {
		return fmt.Errorf("archive notification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

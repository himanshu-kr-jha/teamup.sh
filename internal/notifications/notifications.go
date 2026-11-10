package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	// How long the push service keeps trying a device that is off, in seconds.
	// Zero is an hour. Said to the push service, not put in the payload.
	TTL int `json:"-"`
	// How hard the push service tries to wake a device: "very-low", "low",
	// "normal" or "high". Empty is high. Said to the push service, not put in
	// the payload.
	Urgency string `json:"-"`
}

// defaultPushTTL is how long a push waits for a device that is off: a message
// an hour old is still worth seeing on the phone it was meant for.
const defaultPushTTL = 3600

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

// pushClient is the one HTTP client every push goes through.
//
// A client was built per push. That is not as costly as it looks — a client
// with no Transport uses http.DefaultTransport, which is shared and does pool
// connections — but DefaultTransport keeps only two idle connections per host,
// and the fan-out below sends to one host (a browser vendor's push service)
// many at a time. Everything past the second finishes, finds the idle pool
// full, and has its connection closed, so the next burst pays for the
// handshakes again. Sixty devices at three messages a second is enough to make
// that the dominant cost of sending a message.
//
// One client with a transport sized for the fan-out keeps the connections
// between bursts instead.
var pushClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   pushFanOut,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
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

	ttl := payload.TTL
	if ttl <= 0 {
		ttl = defaultPushTTL
	}
	// High unless said otherwise. Every push here puts something on the screen
	// that a person is waiting for, and an Android phone asleep in a pocket
	// holds back anything less until it next wakes up on its own, which can be
	// many minutes: a call that arrives then has long been missed.
	urgency := webpush.UrgencyHigh
	if payload.Urgency != "" {
		urgency = webpush.Urgency(payload.Urgency)
	}

	resp, err := webpush.SendNotification(body, pushSub, &webpush.Options{
		VAPIDPublicKey:  s.cfg.VAPIDPublicKey,
		VAPIDPrivateKey: s.cfg.VAPIDPrivateKey,
		Subscriber:      s.cfg.VAPIDSubject,
		TTL:             ttl,
		Urgency:         urgency,
		HTTPClient:      pushClient,
	})
	if err != nil {
		s.logger.Warn().Err(err).Str("endpoint", sub.Endpoint).Msg("push delivery failed")
		return
	}
	// The body has to be drained and closed or every push leaks a connection.
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	// Anything else that is not accepted is a push that never arrives, and
	// was until now dropped without a word: a key the push service refuses,
	// a payload it will not take.
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusGone {
		s.logger.Warn().Int("status", resp.StatusCode).Str("endpoint", sub.Endpoint).Msg("push refused by the push service")
	}

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

// subscriptionsFor loads the push subscriptions of several people in one
// statement.
//
// One query per recipient was a round trip per person before a single push had
// been sent — sixty of them for one message in a busy channel, all the same
// statement with a different id, all competing for the same connection pool as
// the traffic that people are waiting on.
func (s *Service) subscriptionsFor(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]PushSubscription, error) {
	byUser := map[uuid.UUID][]PushSubscription{}
	if len(userIDs) == 0 {
		return byUser, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, org_id, user_id, endpoint, p256dh, auth,
		       COALESCE(user_agent, ''), COALESCE(device_name, ''), created_at
		FROM push_subscriptions WHERE user_id = ANY($1)
	`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p PushSubscription
		if err := rows.Scan(&p.ID, &p.OrgID, &p.UserID, &p.Endpoint, &p.P256DH, &p.Auth,
			&p.UserAgent, &p.DeviceName, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan push subscription: %w", err)
		}
		byUser[p.UserID] = append(byUser[p.UserID], p)
	}
	return byUser, rows.Err()
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
	notif.Title = fitTitle(notif.Title)
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

// maxTitleRunes is the size of notifications.title. Titles are built from things
// people named, which can be longer: a task or a meeting may be 500 characters.
const maxTitleRunes = 255

// fitTitle shortens a title that would not fit. Over-long, the insert fails, and
// the person is told nothing at all about the thing they most needed to hear of.
func fitTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= maxTitleRunes {
		return title
	}
	return string(runes[:maxTitleRunes-1]) + "\u2026"
}

// Notify is the convenience entry point used by the features that raise
// notifications: it records the in-app item and fans out a web push.
// NotifyMany raises the same notification for a set of people in one
// statement. An announcement reaches everybody in the organization, so doing it
// one insert at a time cost a round trip per person.
func (s *Service) NotifyMany(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID, kind, title, body string, data map[string]any) error {
	if len(userIDs) == 0 {
		return nil
	}
	title = fitTitle(title)
	raw, err := json.Marshal(data)
	if err != nil {
		raw = []byte(`{}`)
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO notifications (id, org_id, user_id, type, title, body, data, created_at)
		SELECT gen_random_uuid(), $1, recipient, $2, $3, $4, $5, NOW()
		FROM UNNEST($6::uuid[]) AS recipient
	`, orgID, kind, title, body, raw, userIDs); err != nil {
		return fmt.Errorf("create notifications: %w", err)
	}

	// Push is best effort and slow enough that it must not hold up the post.
	go func() {
		pushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		s.deliverToUsers(pushCtx, userIDs, PushPayload{
			Title: title, Body: body, Tag: kind, Data: data,
		})
	}()
	return nil
}

// PushToUsers delivers to devices without recording anything in the bell.
//
// For the things that happen constantly — every message in every channel — an
// item per person per message would bury the notifications list under exactly
// the traffic the list exists to summarise. The conversation is already the
// record; this is only the tap on the shoulder.
//
// Best effort and detached: delivery takes seconds per device and must never
// hold up the thing that caused it.
func (s *Service) PushToUsers(ctx context.Context, userIDs []uuid.UUID, push protocol.Push) {
	if !s.HasVAPIDKeys() || len(userIDs) == 0 {
		return
	}
	payload := PushPayload{
		Title:              push.Title,
		Body:               push.Body,
		Tag:                push.Tag,
		Renotify:           push.Renotify,
		Data:               push.Data,
		RequireInteraction: push.RequireInteraction,
		TTL:                push.TTL,
		Urgency:            push.Urgency,
	}
	pushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	go func() {
		defer cancel()
		s.pushToUsers(pushCtx, userIDs, payload)
	}()
}

// pushFanOut bounds how many pushes are in flight at once. High enough that a
// channel's worth of devices goes out in a couple of waves rather than one at a
// time, low enough not to open an unbounded number of sockets to a push service
// that will rate-limit us for it.
const pushFanOut = 32

// pushToUsers is PushToUsers with the goroutine taken off, so it can be waited
// on — by a test, and by anything that needs delivery finished before it moves.
//
// Every device is reached, including those of somebody who has TeamUp open
// somewhere. People with a live socket used to be skipped on the grounds that
// the open page raises its own notification, but a socket is a person's, not a
// device's: a laptop left open on a desk silenced the phone in their pocket,
// and a phone app put in the background keeps its socket for minutes after it
// has stopped being able to show anything. Whether this device needs telling
// is for this device to decide, and its service worker does: it stays quiet
// when TeamUp is in front of the person, and the page in turn leaves to the
// push what the push will show (useDesktopNotifications).
func (s *Service) pushToUsers(ctx context.Context, userIDs []uuid.UUID, payload PushPayload) {
	s.deliverToUsers(ctx, userIDs, payload)
}

// deliverToUsers sends one payload to every device of every person named, with
// one subscription query for all of them and a bounded number of deliveries in
// flight.
func (s *Service) deliverToUsers(ctx context.Context, userIDs []uuid.UUID, payload PushPayload) {
	if !s.HasVAPIDKeys() || len(userIDs) == 0 {
		return
	}
	byUser, err := s.subscriptionsFor(ctx, userIDs)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not read push subscriptions")
		return
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, pushFanOut)
	for _, subs := range byUser {
		for _, sub := range subs {
			wg.Add(1)
			sem <- struct{}{}
			go func(sub PushSubscription) {
				defer wg.Done()
				defer func() { <-sem }()
				s.sendToSubscription(ctx, sub, payload)
			}(sub)
		}
	}
	wg.Wait()
}

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
	// Push is best effort and must not hold up the action that triggered it:
	// delivery can take seconds per device. Detached from the request context
	// so cancelling the HTTP request does not cancel delivery.
	go func() {
		pushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.SendToUser(pushCtx, userID.String(), PushPayload{
			Title: title, Body: body, Tag: kind, Data: data,
		}); err != nil {
			s.logger.Warn().Err(err).Str("user", userID.String()).Msg("web push delivery failed")
		}
	}()
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

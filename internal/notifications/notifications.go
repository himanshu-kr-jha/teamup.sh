package notifications

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

type Config struct {
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string
}

type Service struct {
	db     *pgxpool.Pool
	redis  *redis.Client
	cfg    Config
	logger zerolog.Logger
}

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
	ID         uuid.UUID
	OrgID      uuid.UUID
	UserID     uuid.UUID
	Type       string
	Title      string
	Body       string
	Data       json.RawMessage
	ReadAt     *time.Time
	ArchivedAt *time.Time
	CreatedAt  time.Time
}

func NewService(db *pgxpool.Pool, redis *redis.Client, cfg Config) *Service {
	return &Service{
		db:    db,
		redis: redis,
		cfg:   cfg,
	}
}

func (s *Service) SaveSubscription(ctx context.Context, userID, orgID string, sub *PushSubscription) error {
	return nil
}

func (s *Service) SendToUser(ctx context.Context, userID string, payload PushPayload) error {
	subs, err := s.getSubscriptions(ctx, userID)
	if err != nil {
		return err
	}

	for _, sub := range subs {
		go s.sendToSubscription(sub, payload)
	}
	return nil
}

func (s *Service) sendToSubscription(sub PushSubscription, payload PushPayload) {
	pushSub := &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256DH,
			Auth:   sub.Auth,
		},
	}

	body, _ := json.Marshal(payload)
	_, err := webpush.SendNotification(body, pushSub, &webpush.Options{
		VAPIDPublicKey:  s.cfg.VAPIDPublicKey,
		VAPIDPrivateKey: s.cfg.VAPIDPrivateKey,
		TTL:             3600,
	})
	if err != nil {
		// Check if error is related to expired/gone subscription
		errMsg := err.Error()
		if strings.Contains(errMsg, "expired") || strings.Contains(errMsg, "gone") {
			s.deleteSubscription(context.Background(), sub.ID)
		}
		return
	}
}

func (s *Service) getSubscriptions(ctx context.Context, userID string) ([]PushSubscription, error) {
	return nil, nil
}

func (s *Service) deleteSubscription(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (s *Service) CreateNotification(ctx context.Context, notif *Notification) error {
	return nil
}

func (s *Service) ListNotifications(ctx context.Context, userID string, unreadOnly bool, limit int) ([]Notification, error) {
	return nil, nil
}

func (s *Service) MarkRead(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	return nil
}

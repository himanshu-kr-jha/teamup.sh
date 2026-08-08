package presence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/websocket"
	"github.com/teamup/teamup/pkg/protocol"
)

type Service struct {
	redis  *redis.Client
	logger zerolog.Logger
}

func NewService(redis *redis.Client) *Service {
	return &Service{redis: redis}
}

func (s *Service) SetOnline(userID, orgID string) error {
	key := "presence:" + orgID + ":" + userID
	return s.redis.HSet(context.Background(), key, map[string]interface{}{
		"status":        "online",
		"last_activity": time.Now().Unix(),
	}).Err()
}

func (s *Service) SetOffline(userID, orgID string) error {
	key := "presence:" + orgID + ":" + userID
	return s.redis.HSet(context.Background(), key, map[string]interface{}{
		"status":        "offline",
		"last_activity": time.Now().Unix(),
	}).Err()
}

func (s *Service) UpdateStatus(userID, orgID string, status, customStatus, customEmoji string) error {
	key := "presence:" + orgID + ":" + userID
	return s.redis.HSet(context.Background(), key, map[string]interface{}{
		"status":              status,
		"custom_status":       customStatus,
		"custom_status_emoji": customEmoji,
		"last_activity":       time.Now().Unix(),
	}).Err()
}

func (s *Service) GetPresence(userID, orgID string) (*websocket.Presence, error) {
	key := "presence:" + orgID + ":" + userID
	data, err := s.redis.HGetAll(context.Background(), key).Result()
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	return &websocket.Presence{
		UserID:       userID,
		OrgID:        orgID,
		Status:       data["status"],
		CustomStatus: data["custom_status"],
		CustomEmoji:  data["custom_status_emoji"],
		LastActivity: parseTime(data["last_activity"]),
	}, nil
}

func (s *Service) GetAllOnline(orgID string) ([]websocket.Presence, error) {
	pattern := "presence:" + orgID + ":*"
	keys, err := s.redis.Keys(context.Background(), pattern).Result()
	if err != nil {
		return nil, err
	}

	var presences []websocket.Presence
	for _, key := range keys {
		data, err := s.redis.HGetAll(context.Background(), key).Result()
		if err != nil {
			continue
		}
		if data["status"] == "online" {
			// Extract userID from key (format: presence:orgID:userID)
			userID := key[len("presence:"+orgID+":"):]
			presences = append(presences, websocket.Presence{
				UserID:       userID,
				OrgID:        orgID,
				Status:       data["status"],
				CustomStatus: data["custom_status"],
				CustomEmoji:  data["custom_status_emoji"],
				LastActivity: parseTime(data["last_activity"]),
			})
		}
	}
	return presences, nil
}

func parseTime(s string) time.Time {
	var i int64
	_, _ = fmt.Sscanf(s, "%d", &i)
	return time.Unix(i, 0)
}

func (s *Service) HandlePresenceUpdate(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.PresenceUpdatePayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return
	}

	userID := conn.UserID()
	orgID := conn.OrgID()

	s.UpdateStatus(userID, orgID, payload.Status, payload.CustomStatus, payload.CustomStatusEmoji)
}

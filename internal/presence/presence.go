package presence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/websocket"
	"github.com/teamup/teamup/pkg/protocol"
)

type Service struct {
	db     *pgxpool.Pool
	redis  *redis.Client
	logger zerolog.Logger
	ttl    time.Duration
}

// presenceTTL is how long a presence entry survives without being refreshed.
//
// These entries used to be written with no expiry at all, so the only thing
// that ever removed one was Unregister — which only runs on a clean shutdown. A
// process that is killed, OOMs or is rescheduled left every user it had served
// showing online for ever, and nothing would ever correct it, because the only
// writer for that user was the process that died.
//
// The connection heartbeat refreshes this every thirty seconds, so the entry
// outlives three missed refreshes before disappearing. Long enough that a
// momentary Redis or scheduling hiccup does not blink somebody offline in front
// of their colleagues, short enough that a dead process stops lying within a
// minute or two.
const presenceTTL = 90 * time.Second

func (s *Service) expiry() time.Duration {
	if s.ttl > 0 {
		return s.ttl
	}
	return presenceTTL
}

func presenceKey(orgID, userID string) string {
	return "presence:" + orgID + ":" + userID
}

// writeEntry stores presence fields and sets the expiry in the same round trip,
// so an entry can never be left behind without one.
func (s *Service) writeEntry(ctx context.Context, key string, fields map[string]interface{}) error {
	pipe := s.redis.TxPipeline()
	pipe.HSet(ctx, key, fields)
	pipe.PExpire(ctx, key, s.expiry())
	_, err := pipe.Exec(ctx)
	return err
}

func NewService(redis *redis.Client) *Service {
	return &Service{redis: redis}
}

// SetDB wires the database. Availability — what somebody has chosen to say
// about themselves — is durable and lives on the user; whether they happen to
// be connected is transient and lives in Redis.
func (s *Service) SetDB(db *pgxpool.Pool) { s.db = db }

// Availability values somebody can choose for themselves. In a call and
// offline are not here: those are observed rather than chosen.
var choosableAvailability = map[string]bool{
	"available": true,
	"away":      true,
	"busy":      true,
	"on_leave":  true,
}

// Availability is what somebody has said about themselves.
type Availability struct {
	Status string     `json:"status"`
	Note   string     `json:"note,omitempty"`
	Until  *time.Time `json:"until,omitempty"`
}

// SetAvailability records a chosen status. It survives disconnecting, which is
// what makes "on leave" mean anything.
func (s *Service) SetAvailability(ctx context.Context, userID uuid.UUID, a Availability) error {
	if !choosableAvailability[a.Status] {
		return fmt.Errorf("unknown availability %q", a.Status)
	}
	// A note belongs to the status that explains it; changing status clears it
	// rather than leaving "back on Monday" attached to "available".
	_, err := s.db.Exec(ctx, `
		UPDATE users
		SET availability = $2, availability_note = NULLIF($3, ''), availability_until = $4,
		    updated_at = NOW()
		WHERE id = $1
	`, userID, a.Status, a.Note, a.Until)
	if err != nil {
		return fmt.Errorf("set availability: %w", err)
	}
	return nil
}

// GetAvailability reads back what somebody has chosen.
func (s *Service) GetAvailability(ctx context.Context, userID uuid.UUID) (Availability, error) {
	var a Availability
	var note *string
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(availability, 'available'), availability_note, availability_until
		FROM users WHERE id = $1
	`, userID).Scan(&a.Status, &note, &a.Until)
	if err != nil {
		return a, fmt.Errorf("get availability: %w", err)
	}
	if note != nil {
		a.Note = *note
	}
	return a, nil
}

// SetInCall marks somebody as being in a call, which shows differently from
// merely being busy. Transient: it goes when they disconnect.
func (s *Service) SetInCall(userID, orgID string, inCall bool) error {
	key := presenceKey(orgID, userID)
	if inCall {
		return s.writeEntry(context.Background(), key, map[string]interface{}{"in_call": "1"})
	}
	return s.redis.HDel(context.Background(), key, "in_call").Err()
}

// Effective is what to show for somebody: what they chose, corrected by what
// is actually true.
//
// The vocabulary is deliberately small — available, busy, away, on leave —
// because a dot has to be readable at a glance. Somebody who is not connected
// reads as away rather than offline: to a colleague those mean the same thing,
// which is "not right now". Being in a call reads as busy for the same reason:
// what matters is that they cannot talk, not why.
//
// Leave outranks everything, including being connected, because it is a
// statement about the days ahead rather than about this moment.
func Effective(chosen Availability, connected, inCall bool) string {
	if chosen.Status == "on_leave" {
		return "on_leave"
	}
	if inCall {
		return "busy"
	}
	if !connected {
		return "away"
	}
	if chosen.Status == "" {
		return "available"
	}
	return chosen.Status
}

func (s *Service) SetOnline(userID, orgID string) error {
	return s.writeEntry(context.Background(), presenceKey(orgID, userID), map[string]interface{}{
		"status":        "online",
		"last_activity": time.Now().Unix(),
	})
}

// Touch extends the expiry on somebody who is still connected.
//
// Called from the connection heartbeat. Without it the TTL that stops a dead
// process from claiming people are online would expire under people who really
// are, and they would blink offline every ninety seconds.
func (s *Service) Touch(userID, orgID string) error {
	ctx := context.Background()

	extended, err := s.redis.PExpire(ctx, presenceKey(orgID, userID), s.expiry()).Result()
	if err != nil {
		return err
	}
	if !extended {
		// The entry has already gone: Redis was restarted, or was unreachable
		// for longer than the TTL. Only a live connection is ever refreshed, so
		// this person is demonstrably here — re-establish rather than leave
		// them invisible until they happen to reconnect.
		return s.SetOnline(userID, orgID)
	}
	return nil
}

func (s *Service) SetOffline(userID, orgID string) error {
	return s.writeEntry(context.Background(), presenceKey(orgID, userID), map[string]interface{}{
		"status":        "offline",
		"last_activity": time.Now().Unix(),
	})
}

func (s *Service) UpdateStatus(userID, orgID string, status, customStatus, customEmoji string) error {
	return s.writeEntry(context.Background(), presenceKey(orgID, userID), map[string]interface{}{
		"status":              status,
		"custom_status":       customStatus,
		"custom_status_emoji": customEmoji,
		"last_activity":       time.Now().Unix(),
	})
}

func (s *Service) GetPresence(userID, orgID string) (*websocket.Presence, error) {
	data, err := s.redis.HGetAll(context.Background(), presenceKey(orgID, userID)).Result()
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

// PersonAvailability is what to show next to somebody's name.
type PersonAvailability struct {
	UserID uuid.UUID  `json:"userId"`
	Name   string     `json:"name"`
	Status string     `json:"status"`
	Note   string     `json:"note,omitempty"`
	Until  *time.Time `json:"until,omitempty"`
}

// ListOrgAvailability answers for everybody in the organization, not only the
// people currently connected: somebody on leave has to show as on leave, and
// that is precisely when they are not here.
func (s *Service) ListOrgAvailability(ctx context.Context, orgID uuid.UUID) ([]PersonAvailability, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, COALESCE(availability, 'available'), availability_note, availability_until
		FROM users
		WHERE org_id = $1 AND deleted_at IS NULL AND COALESCE(is_active, true)
		ORDER BY name
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list availability: %w", err)
	}
	defer rows.Close()

	people := []PersonAvailability{}
	chosen := map[uuid.UUID]Availability{}
	for rows.Next() {
		var p PersonAvailability
		var note *string
		if err := rows.Scan(&p.UserID, &p.Name, &p.Status, &note, &p.Until); err != nil {
			return nil, fmt.Errorf("scan availability: %w", err)
		}
		if note != nil {
			p.Note = *note
		}
		chosen[p.UserID] = Availability{Status: p.Status, Note: p.Note, Until: p.Until}
		people = append(people, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Whether each of them is actually here, and whether they are in a call.
	//
	// One pipeline rather than a hash read per person. This runs on every poll
	// from every open tab, so as a sequential loop its cost was one blocking
	// round trip per member of the organization — sixty waits to draw sixty
	// dots, all day. Pipelined it is one wait however large the company gets,
	// and Redis does the same work either way.
	pipe := s.redis.Pipeline()
	entries := make([]*redis.MapStringStringCmd, len(people))
	for i := range people {
		entries[i] = pipe.HGetAll(ctx, presenceKey(orgID.String(), people[i].UserID.String()))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		// Redis being unavailable should not hide who is on leave. Exec reports
		// the first command error, and each command is still asked below, so
		// there is nothing to do here but say so.
		s.logger.Debug().Err(err).Msg("presence lookup failed")
	}

	for i := range people {
		data, err := entries[i].Result()
		if err != nil && err != redis.Nil {
			s.logger.Debug().Err(err).Msg("presence lookup failed")
			data = nil
		}
		connected := data["status"] != "" && data["status"] != "offline"
		// Set either as a flag by the call itself, or as a status by a client
		// that reports it that way. Both mean the same thing.
		inCall := data["in_call"] == "1" || data["status"] == "in_call"
		people[i].Status = Effective(chosen[people[i].UserID], connected, inCall)
	}
	return people, nil
}

// GetAllOnline lists everybody in one organisation who is currently connected.
//
// It walks the keyspace with SCAN rather than KEYS. KEYS matches every key in
// the database in a single blocking operation, and Redis is single-threaded, so
// on a large keyspace it stalls every other command — including the ones on the
// request path — for as long as it takes. SCAN does the same work in small
// batches that yield between them.
//
// The prefix is the organisation's own, and the results are filtered to it
// again on the way out, so a key that somehow matched another tenant's could
// still not be returned here.
func (s *Service) GetAllOnline(orgID string) ([]websocket.Presence, error) {
	ctx := context.Background()
	prefix := presenceKey(orgID, "")

	var presences []websocket.Presence
	// SCAN guarantees only that a key present throughout is returned at least
	// once — it may be returned more than once — so the same person must not be
	// counted twice.
	seen := map[string]bool{}

	var cursor uint64
	for {
		keys, next, err := s.redis.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return nil, err
		}

		for _, key := range keys {
			userID, ok := strings.CutPrefix(key, prefix)
			if !ok || userID == "" || seen[userID] {
				continue
			}

			data, err := s.redis.HGetAll(ctx, key).Result()
			if err != nil {
				continue
			}
			if data["status"] != "online" {
				continue
			}

			seen[userID] = true
			presences = append(presences, websocket.Presence{
				UserID:       userID,
				OrgID:        orgID,
				Status:       data["status"],
				CustomStatus: data["custom_status"],
				CustomEmoji:  data["custom_status_emoji"],
				LastActivity: parseTime(data["last_activity"]),
			})
		}

		cursor = next
		if cursor == 0 {
			return presences, nil
		}
	}
}

func parseTime(s string) time.Time {
	var i int64
	_, _ = fmt.Sscanf(s, "%d", &i)
	return time.Unix(i, 0)
}

func (s *Service) HandlePresenceUpdate(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.PresenceUpdatePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	userID := conn.UserID()
	orgID := conn.OrgID()

	s.UpdateStatus(userID, orgID, payload.Status, payload.CustomStatus, payload.CustomStatusEmoji)
}

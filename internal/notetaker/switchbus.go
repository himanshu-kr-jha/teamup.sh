package notetaker

// Notes turned on or off on one server have to reach the note-taker on
// whichever server is taking them, at once: until it hears, it is still
// listening. The room is told too (calls.MeetingNotesTopic), and the
// note-taker hears that, but that message can fail to send, and the heartbeat
// that reads the switch again comes only every 15 seconds. So every server
// also says so over Redis, as the WebSocket fan-out already does between
// servers, and each one that holds the call's note-taker follows at once.
//
// Whatever is heard before it follows is still never sent to speech to text,
// kept or shown: the switch is written down before anybody is told, and every
// clip is checked against it (heardWhilePaused).

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// SwitchChannel is the Redis channel that says a call's notes were turned on
// or off. It carries only the call's id: the switch is read from the database.
const SwitchChannel = "teamup:notes:switch"

// UseRedis has this server tell the others, and hear from them, when notes are
// turned on or off. Without it, a single server is all there is.
func (s *Service) UseRedis(rdb *redis.Client) { s.redis = rdb }

// announceSwitch tells every other server that a call's notes changed.
func (s *Service) announceSwitch(ctx context.Context, callID uuid.UUID) {
	if s.redis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.redis.Publish(ctx, SwitchChannel, callID.String()).Err(); err != nil {
		s.logger.Warn().Err(err).Str("call", callID.String()).Msg("notetaker: could not tell the other servers notes were turned on or off")
	}
}

// ListenForSwitches follows notes turned on or off on other servers until ctx
// ends. go-redis subscribes again by itself after a dropped connection; what
// is said while it is down is caught by the heartbeat, and never kept.
func (s *Service) ListenForSwitches(ctx context.Context) {
	if s.redis == nil {
		return
	}
	sub := s.redis.Subscribe(ctx, SwitchChannel)
	defer sub.Close()
	messages := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-messages:
			if !ok {
				return
			}
			if id, err := uuid.Parse(m.Payload); err == nil {
				s.follow(id)
			}
		}
	}
}

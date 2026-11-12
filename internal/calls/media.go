package calls

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
)

// errNoMedia is asking LiveKit for something on an instance with no LiveKit.
var errNoMedia = errors.New("LiveKit is not configured")

// mediaServer is what the rules in this package ask of LiveKit, beyond handing out
// tokens. It is small so that those rules can be tested on what they ask for and
// when, which a real server cannot show: the order of two things in a few
// milliseconds, or a request failing.
type mediaServer interface {
	// CloseRoom ends a room for whoever is in it. No such room is not an error.
	CloseRoom(ctx context.Context, room string) error
	// RoomNames is every room that exists right now.
	RoomNames(ctx context.Context) ([]string, error)
	// RemoveParticipant takes somebody out of a room. Their not being there is not
	// an error.
	RemoveParticipant(ctx context.Context, room, identity string) error
	// StartRecording starts composing a room into a file and names the job.
	StartRecording(ctx context.Context, req *livekit.RoomCompositeEgressRequest) (string, error)
	// SendData delivers a packet from the server to everybody in a room, on a
	// topic. A room that does not exist is not an error.
	SendData(ctx context.Context, room, topic string, data []byte) error
	// InRoom says whether somebody is connected to a room now.
	InRoom(ctx context.Context, room, identity string) (bool, error)
}

// liveKit is the real server.
type liveKit struct{ s *Service }

func (l liveKit) CloseRoom(ctx context.Context, room string) error { return l.s.EndCall(ctx, room) }

func (l liveKit) RoomNames(ctx context.Context) ([]string, error) {
	if l.s.roomService == nil {
		return nil, errNoMedia
	}
	listed, err := l.s.roomService.ListRooms(ctx, &livekit.ListRoomsRequest{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(listed.Rooms))
	for _, room := range listed.Rooms {
		names = append(names, room.Name)
	}
	return names, nil
}

func (l liveKit) RemoveParticipant(ctx context.Context, room, identity string) error {
	if l.s.roomService == nil {
		return nil
	}
	_, err := l.s.roomService.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{Room: room, Identity: identity})
	return err
}

func (l liveKit) StartRecording(ctx context.Context, req *livekit.RoomCompositeEgressRequest) (string, error) {
	if l.s.egressService == nil {
		return "", errNoMedia
	}
	info, err := l.s.egressService.StartRoomCompositeEgress(ctx, req)
	if err != nil {
		return "", err
	}
	return info.EgressId, nil
}

func (l liveKit) SendData(ctx context.Context, room, topic string, data []byte) error {
	if l.s.roomService == nil {
		return errNoMedia
	}
	_, err := l.s.roomService.SendData(ctx, &livekit.SendDataRequest{
		Room: room, Data: data, Kind: livekit.DataPacket_RELIABLE, Topic: &topic,
	})
	if twirpNotFound(err) {
		return nil
	}
	return err
}

func (l liveKit) InRoom(ctx context.Context, room, identity string) (bool, error) {
	if l.s.roomService == nil {
		return false, errNoMedia
	}
	res, err := l.s.roomService.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: room})
	if twirpNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, p := range res.GetParticipants() {
		if p.GetIdentity() == identity && p.GetState() != livekit.ParticipantInfo_DISCONNECTED {
			return true, nil
		}
	}
	return false, nil
}

// twirpNotFound is LiveKit saying the room is not there.
func twirpNotFound(err error) bool {
	var te twirp.Error
	return errors.As(err, &te) && te.Code() == twirp.NotFound
}

// mediaOf is the server to ask.
func (s *Service) mediaOf() mediaServer {
	if s.media != nil {
		return s.media
	}
	return liveKit{s}
}

// vanishedRoomGrace is how long a call must have been left alone before its room
// being absent means anything. LiveKit makes a room on the first connection, so
// somebody holding a fresh token may simply not have arrived yet.
const vanishedRoomGrace = 10 * time.Minute

// SweepVanishedRooms ends calls whose room no longer exists, and returns how many.
//
// LiveKit retries a webhook only a few times, so a room_finished sent while this
// application was restarting for a deploy is gone, and nothing else would ever close
// that call. Everything is decided by the same code as the webhook, so whichever
// replica or webhook gets there first does the work and the rest find nothing to do.
// If LiveKit cannot be asked, nothing is known about any room and nothing changes.
func (s *Service) SweepVanishedRooms(ctx context.Context) (int, error) {
	names, err := s.mediaOf().RoomNames(ctx)
	if err != nil {
		return 0, fmt.Errorf("list rooms: %w", err)
	}
	now := s.clock()
	rows, err := s.db.Query(ctx, `
		SELECT id, org_id, created_by, status, scheduled_start IS NOT NULL, room_name
		FROM calls
		WHERE status = 'active' AND updated_at < $1 AND NOT (room_name = ANY($2))
	`, now.Add(-vanishedRoomGrace), names)
	if err != nil {
		return 0, fmt.Errorf("find calls with no room: %w", err)
	}
	type vanished struct {
		call webhookCall
		room string
	}
	calls, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (vanished, error) {
		var v vanished
		err := row.Scan(&v.call.ID, &v.call.OrgID, &v.call.CreatedBy, &v.call.Status, &v.call.Scheduled, &v.room)
		return v, err
	})
	if err != nil {
		return 0, fmt.Errorf("find calls with no room: %w", err)
	}

	ended := 0
	for _, v := range calls {
		did, err := s.finishRoom(ctx, &v.call, now)
		if err != nil {
			s.logger.Warn().Err(err).Str("room", v.room).Msg("could not end a call whose room is gone")
			continue
		}
		if did {
			ended++
		}
	}
	return ended, nil
}

// RunVanishedRoomSweep sweeps until ctx ends. Every replica runs this.
func (s *Service) RunVanishedRoomSweep(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.SweepVanishedRooms(ctx); err != nil && !errors.Is(err, errNoMedia) && ctx.Err() == nil {
				s.logger.Warn().Err(err).Msg("could not check for calls whose room is gone")
			}
		}
	}
}

// RemoveFromCalls takes somebody who has been deactivated out of every room they are
// connected to. A token cannot be revoked, and nothing else would make them leave. A
// failure is only logged: the deactivation itself has already happened.
func (s *Service) RemoveFromCalls(ctx context.Context, orgID, userID uuid.UUID) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT c.room_name
		FROM call_sessions cs JOIN calls c ON c.id = cs.call_id
		WHERE c.org_id = $1 AND cs.user_id = $2 AND cs.joined_at IS NOT NULL AND cs.left_at IS NULL
		  AND c.status = 'active'
	`, orgID, userID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not find the calls of somebody deactivated")
		return
	}
	rooms, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not find the calls of somebody deactivated")
		return
	}
	for _, room := range rooms {
		s.putOut(ctx, room, userID)
	}
}

func (s *Service) putOut(ctx context.Context, room string, userID uuid.UUID) {
	if err := s.mediaOf().RemoveParticipant(ctx, room, userID.String()); err != nil {
		s.logger.Warn().Err(err).Str("room", room).Msg("could not remove somebody deactivated from a room")
	}
}

// isActivePerson says whether somebody still works here. Sessions end at once when
// they stop, but a socket stays open and a LiveKit token stays good, so anything
// that reaches a call from either of those has to ask.
func (s *Service) isActivePerson(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	var active bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users
			WHERE id = $1 AND org_id = $2 AND deleted_at IS NULL AND COALESCE(is_active, true))
	`, userID, orgID).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("check person is active: %w", err)
	}
	return active, nil
}

package calls

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/tasks"
	"github.com/teamup/teamup/internal/websocket"
	apperrors "github.com/teamup/teamup/pkg/errors"
	"github.com/teamup/teamup/pkg/protocol"
)

type Config struct {
	LiveKitURL       string
	LiveKitAPIKey    string
	LiveKitAPISecret string

	// Recording destination. LiveKit's Egress service writes the finished file
	// straight to S3-compatible storage, so the media never passes through
	// this application.
	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageBucket    string
	StorageSecure    bool

	// MaxCallParticipants is the ceiling on one call: how large a LiveKit room
	// is created, and one more than how many people a channel call rings.
	// Zero means DefaultMaxCallParticipants.
	MaxCallParticipants int
}

// ErrCallOver is somebody asking to join a call that has ended and cannot reopen.
var ErrCallOver = apperrors.New(apperrors.CodeConflict, "this call has ended")

type Broadcaster interface {
	BroadcastToUser(userID string, msg *protocol.Message)
	BroadcastToChannel(channelID string, msg *protocol.Message, excludeUserID string)
}

type Service struct {
	reachable     Reachable
	chat          ChatRecorder
	notifier      Notifier
	db            *pgxpool.Pool
	cfg           Config
	logger        zerolog.Logger
	broadcaster   Broadcaster
	roomService   *lksdk.RoomServiceClient
	egressService *lksdk.EgressClient
	// The org chart lives with tasks; this reads it so there is one statement of
	// who outranks whom.
	chart *tasks.Service
	// joined is told when a person really connects to a call's room; the
	// note-taker uses it to join.
	joined func(ctx context.Context, orgID, callID uuid.UUID, roomName string)
	clock  func() time.Time
	// guestMail emails invitations to people from outside; baseURL starts
	// the links in them.
	guestMail GuestMailer
	baseURL   string
	// What this service asks of LiveKit, replaceable so that a test can stand in for
	// the server, and for the moment it answers. Nil means the real one.
	media mediaServer
}

// Serialised straight onto the wire by the create handler, so the tags have to
// match what the frontend reads.
type Call struct {
	ID              uuid.UUID  `json:"id"`
	OrgID           uuid.UUID  `json:"orgId"`
	ChannelID       *uuid.UUID `json:"channelId,omitempty"`
	RoomName        string     `json:"roomName"`
	LiveKitRoomSID  *string    `json:"-"`
	Title           *string    `json:"title,omitempty"`
	CallType        string     `json:"callType"`
	Status          string     `json:"status"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
	DurationSeconds *int       `json:"durationSeconds,omitempty"`
	RecordingURL    *string    `json:"recordingUrl,omitempty"`
	RecordingStatus *string    `json:"recordingStatus,omitempty"`
	CreatedBy       uuid.UUID  `json:"createdBy"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	// NotInvited counts channel members a call could not reach because a room
	// holds only so many. Response-only, so the caller can say so rather than
	// quietly ringing a subset.
	NotInvited int `json:"notInvited,omitempty"`
}

type CallParticipant struct {
	CallID     uuid.UUID  `json:"callId"`
	UserID     uuid.UUID  `json:"userId"`
	Role       string     `json:"role"`
	JoinedAt   *time.Time `json:"joinedAt,omitempty"`
	LeftAt     *time.Time `json:"leftAt,omitempty"`
	IsMuted    bool       `json:"isMuted"`
	IsCameraOn bool       `json:"isCameraOn"`
}

func NewService(db *pgxpool.Pool, cfg Config) *Service {
	s := &Service{
		db:    db,
		cfg:   cfg,
		chart: tasks.NewService(db),
		clock: time.Now,
	}

	// Initialize LiveKit room service client
	if cfg.LiveKitURL != "" && cfg.LiveKitAPIKey != "" && cfg.LiveKitAPISecret != "" {
		s.roomService = lksdk.NewRoomServiceClient(cfg.LiveKitURL, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)
		s.egressService = lksdk.NewEgressClient(cfg.LiveKitURL, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)
	}

	return s
}

// SetLogger wires real logging. Left at the zero value every diagnostic in
// this package was written to a nil writer and silently discarded — including
// the fail-closed paths that swallow an error and deny access.
func (s *Service) SetLogger(l zerolog.Logger) {
	s.logger = l.With().Str("component", "calls").Logger()
}

func (s *Service) SetBroadcaster(b Broadcaster) {
	s.broadcaster = b
}

// SetClock replaces what this service takes the time to be, so a test can cross
// a midnight rather than wait for one.
func (s *Service) SetClock(now func() time.Time) { s.clock = now }

// GenerateToken creates a LiveKit access token
func (s *Service) GenerateToken(ctx context.Context, roomName, userID, userName string, canPublish, canSubscribe bool) (string, error) {
	if s.cfg.LiveKitAPIKey == "" || s.cfg.LiveKitAPISecret == "" {
		return "", fmt.Errorf("LiveKit credentials not configured")
	}

	at := auth.NewAccessToken(s.cfg.LiveKitAPIKey, s.cfg.LiveKitAPISecret)
	at.SetIdentity(userID)
	at.SetName(userName)
	at.SetVideoGrant(&auth.VideoGrant{
		RoomJoin:       true,
		Room:           roomName,
		CanPublish:     &canPublish,
		CanSubscribe:   &canSubscribe,
		CanPublishData: &canPublish,
	})

	return at.ToJWT()
}

// CreateCall creates a new call room using LiveKit
func (s *Service) CreateCall(ctx context.Context, orgID, createdBy uuid.UUID, title *string, callType string, channelID *uuid.UUID, invitees []string) (*Call, error) {
	roomName := "call-" + uuid.New().String()[:8]

	liveKitRoomSID, err := s.createRoom(ctx, roomName)
	if err != nil {
		return nil, err
	}

	call := &Call{
		ID:             uuid.New(),
		OrgID:          orgID,
		ChannelID:      channelID,
		RoomName:       roomName,
		LiveKitRoomSID: liveKitRoomSID,
		Title:          title,
		CallType:       callType,
		Status:         "active",
		CreatedBy:      createdBy,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Persist the call: creating the LiveKit room alone left no record, so a
	// call could never be listed, joined by id, or seen in history.
	now := time.Now()
	call.StartedAt = &now
	if _, err := s.db.Exec(ctx, `
		INSERT INTO calls (id, org_id, channel_id, room_name, livekit_room_sid, title,
		                   call_type, status, started_at, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
	`, call.ID, call.OrgID, call.ChannelID, call.RoomName, call.LiveKitRoomSID, call.Title,
		call.CallType, call.Status, call.StartedAt, call.CreatedBy); err != nil {
		return nil, fmt.Errorf("persist call: %w", err)
	}

	// The creator hosts; invitees are recorded so the call can be shown to
	// them and so history knows who was asked.
	participants := append([]string{createdBy.String()}, invitees...)
	for i, invitee := range participants {
		userID, err := uuid.Parse(invitee)
		if err != nil {
			continue
		}
		role := "participant"
		if i == 0 {
			role = "host"
		}
		if _, err := s.db.Exec(ctx, `
			INSERT INTO call_participants (call_id, user_id, role)
			SELECT $1, u.id, $3 FROM users u WHERE u.id = $2 AND u.org_id = $4
			ON CONFLICT (call_id, user_id) DO NOTHING
		`, call.ID, userID, role, orgID); err != nil {
			return nil, fmt.Errorf("add call participant: %w", err)
		}
	}

	return call, nil
}

// createRoom asks LiveKit for a room. Asking for one that already exists returns
// it, so this is safe to repeat.
func (s *Service) createRoom(ctx context.Context, roomName string) (*string, error) {
	if s.roomService == nil {
		// Fallback: stub room SID
		stubSID := "stub-" + roomName
		return &stubSID, nil
	}
	room, err := s.roomService.CreateRoom(ctx, &livekit.CreateRoomRequest{
		Name:            roomName,
		EmptyTimeout:    300, // 5 minutes
		MaxParticipants: uint32(s.MaxParticipants()),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create LiveKit room: %w", err)
	}
	return &room.Sid, nil
}

// DefaultMaxCallParticipants is what a LiveKit room is created with unless the
// deployment says otherwise, and therefore how many people a call can hold.
//
// It was 50, which is below the size of the companies this product is for: a
// 60-person all-hands truncated its invitee list at 49 and LiveKit refused the
// 51st join, both without saying anything. LiveKit itself seats hundreds in a
// room, so the number here is not a media limit — it is a bound on runaway
// fan-out, since every invite is a database row and a WebSocket frame. 200
// clears an all-hands at this size several times over and still keeps one
// mis-click from ringing an unbounded crowd.
//
// Deployments larger than that set TEAMUP_MAX_CALL_PARTICIPANTS.
const DefaultMaxCallParticipants = 200

// MaxParticipants is the ceiling this deployment puts on a call.
func (s *Service) MaxParticipants() int {
	if s.cfg.MaxCallParticipants > 0 {
		return s.cfg.MaxCallParticipants
	}
	return DefaultMaxCallParticipants
}

// MaxChannelCallInvites bounds who gets rung when a call starts in a channel.
// Every member of a public channel is the whole organization, so without a
// bound one click in a busy channel rings everybody and writes a row each.
// One below the room ceiling, because the caller takes a seat too.
func (s *Service) MaxChannelCallInvites() int {
	return s.MaxParticipants() - 1
}

// ChannelMembersFor lists everybody in a channel apart from one person, which
// is who a channel call has to reach. Capped: it reports whether there were
// more so the caller can say so rather than silently reaching a subset.
func (s *Service) ChannelMembersFor(ctx context.Context, orgID, channelID, except uuid.UUID) ([]uuid.UUID, int, error) {
	var total int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM channel_members cm
		JOIN channels c ON c.id = cm.channel_id
		WHERE cm.channel_id = $1 AND c.org_id = $2 AND cm.user_id <> $3
	`, channelID, orgID, except).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count channel members for call: %w", err)
	}

	rows, err := s.db.Query(ctx, `
		SELECT cm.user_id
		FROM channel_members cm
		JOIN channels c ON c.id = cm.channel_id
		WHERE cm.channel_id = $1 AND c.org_id = $2 AND cm.user_id <> $3
		ORDER BY cm.joined_at
		LIMIT $4
	`, channelID, orgID, except, s.MaxChannelCallInvites())
	if err != nil {
		return nil, 0, fmt.Errorf("list channel members for call: %w", err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, 0, fmt.Errorf("scan channel member: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, total, rows.Err()
}

// IsChannelMember gates starting a call on a channel: a private channel's call
// belongs to its members, and a public channel's to the organization, which is
// the same test now that everybody is a member of an open channel.
func (s *Service) IsChannelMember(ctx context.Context, orgID, channelID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_members cm
			JOIN channels c ON c.id = cm.channel_id
			WHERE cm.channel_id = $1 AND cm.user_id = $2 AND c.org_id = $3
			  AND c.deleted_at IS NULL)
	`, channelID, userID, orgID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check channel membership for call: %w", err)
	}
	return ok, nil
}

// Ring tells each invitee that a call is waiting. The callee's client decides
// how long to keep ringing; the server just says who is calling and where.
//
// Runs off the request: minting a token and enqueuing a frame per invitee is
// work the caller should not wait on, and none of it affects whether the call
// was created.
func (s *Service) Ring(ctx context.Context, call *Call, callerID, callerName string, invitees []uuid.UUID) {
	// The list arrives from a request body — the create-call handler passes on
	// whatever ids it was given — so it is resolved against the call's own
	// organization first. A ring carries a LiveKit token, and a token issued to
	// somebody in another organization is a way into this one's room.
	//
	// Unlike the invite frame, this runs detached after the call already
	// exists, so there is no longer anybody to refuse to: strangers are dropped
	// and the genuine invitees still get their call.
	invitees, err := s.inviteesInOrg(ctx, call.OrgID, invitees)
	if err != nil {
		// Fail closed. Ringing an unresolved list is exactly the thing this
		// guards against.
		s.logger.Error().Err(err).Str("call", call.ID.String()).
			Msg("could not resolve call invitees; nobody was rung")
		return
	}

	// Devices first, and regardless of whether anything is open: somebody with
	// no tab is exactly who this reaches.
	s.ringDevices(ctx, call, callerName, invitees)

	if s.broadcaster == nil {
		return
	}
	for _, invitee := range invitees {
		// Their own name, so the panel they arrive on is labelled properly.
		token, err := s.GenerateToken(ctx, call.RoomName, invitee.String(), s.DisplayName(ctx, invitee), true, true)
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not mint a token for a call invitee")
			continue
		}
		payload := protocol.CallInvitedPayload{
			CallID:     call.ID.String(),
			RoomName:   call.RoomName,
			CallerID:   callerID,
			CallerName: callerName,
			CallType:   call.CallType,
			Token:      token,
		}
		if call.ChannelID != nil {
			payload.ChannelID = call.ChannelID.String()
		}
		s.broadcaster.BroadcastToUser(invitee.String(), protocol.NewMessage(
			protocol.TypeCallInvited, payload))

		// Tell whoever is calling whether that reached a phone or an empty
		// seat, so they see "ringing" or "calling" rather than guessing.
		s.broadcaster.BroadcastToUser(callerID, protocol.NewMessage("call.reach", map[string]any{
			"callId":    call.ID.String(),
			"userId":    invitee.String(),
			"reachable": s.isReachable(invitee.String()),
		}))
	}
}

// inviteesInOrg keeps only the people who really are in the organization.
//
// Nothing in an invitee list is trustworthy: the ids come off the wire, from a
// socket frame or a request body, and reaching one of them mints a LiveKit
// token for it. Every other cross-organization boundary here is enforced in
// the query, and this is the same idea — resolve who was named against the
// organization doing the naming before anybody is reached.
//
// Order is preserved and duplicates are dropped, so the result can be compared
// against a de-duplicated request to see whether anybody was rejected.
func (s *Service) inviteesInOrg(ctx context.Context, orgID uuid.UUID, invitees []uuid.UUID) ([]uuid.UUID, error) {
	if len(invitees) == 0 {
		return nil, nil
	}

	named := make([]string, 0, len(invitees))
	for _, id := range invitees {
		named = append(named, id.String())
	}
	rows, err := s.db.Query(ctx,
		`SELECT id FROM users WHERE org_id = $1 AND id = ANY($2::uuid[])`, orgID, named)
	if err != nil {
		return nil, fmt.Errorf("resolve call invitees: %w", err)
	}
	defer rows.Close()

	inOrg := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan call invitee: %w", err)
		}
		inOrg[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resolve call invitees: %w", err)
	}

	resolved := make([]uuid.UUID, 0, len(invitees))
	seen := map[uuid.UUID]bool{}
	for _, id := range invitees {
		if inOrg[id] && !seen[id] {
			seen[id] = true
			resolved = append(resolved, id)
		}
	}
	return resolved, nil
}

// Participants lists everybody on a call, optionally skipping one person.
func (s *Service) Participants(ctx context.Context, callID uuid.UUID, except string) ([]string, error) {
	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM call_participants WHERE call_id = $1`, callID)
	if err != nil {
		return nil, fmt.Errorf("list call participants: %w", err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan call participant: %w", err)
		}
		if id.String() == except {
			continue
		}
		ids = append(ids, id.String())
	}
	return ids, rows.Err()
}

// AnnounceCallOver tells everybody still ringing that the call has stopped, so
// a phone does not keep ringing after the caller has hung up or somebody has
// turned it down. Devices too, not only open tabs — a notification left on a
// lock screen is a phone still ringing as far as anybody can tell.
func (s *Service) AnnounceCallOver(ctx context.Context, call *CallModel, reason, declinedBy string, recipients []string) {
	// Whoever was being rung has the notification taken off their screen. Done
	// before the socket message and whatever the broadcaster is, because it is
	// the people with nothing open who would otherwise be left with a phone
	// apparently still ringing.
	ids := make([]uuid.UUID, 0, len(recipients))
	for _, userID := range recipients {
		if id, err := uuid.Parse(userID); err == nil {
			ids = append(ids, id)
		}
	}
	s.stopRinging(ctx, call.ID, ids)

	if s.broadcaster == nil {
		return
	}
	msg := protocol.NewMessage(protocol.TypeCallEnded, protocol.CallEndedPayload{
		CallID:     call.ID.String(),
		RoomName:   call.RoomName,
		Reason:     reason,
		DeclinedBy: declinedBy,
	})
	for _, userID := range recipients {
		s.broadcaster.BroadcastToUser(userID, msg)
	}
}

func (s *Service) GetToken(ctx context.Context, callID, userID, userName string) (string, error) {
	return s.GenerateToken(ctx, callID, userID, userName, true, true)
}

// StartRecording asks LiveKit's Egress service to composite the room and write
// the result into the organization's object storage. The file lands under the
// same org-scoped prefix as every other attachment, so the existing access
// checks apply to it unchanged.
//
// Retention is deliberately not implemented: the platform spec parks it as
// undecided, so recordings are kept until someone removes them.
func (s *Service) StartRecording(ctx context.Context, orgID, callID uuid.UUID) (*CallModel, error) {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return nil, err
	}
	if call.Status == "ended" {
		return nil, apperrors.New(apperrors.CodeBadRequest, "the call has already ended")
	}
	if call.RecordingStatus != nil && *call.RecordingStatus == "recording" {
		return nil, apperrors.New(apperrors.CodeConflict, "this call is already being recorded")
	}
	if s.media == nil && s.egressService == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "recording is not configured on this instance")
	}
	if s.cfg.StorageBucket == "" {
		return nil, apperrors.New(apperrors.CodeUnavailable, "recording storage is not configured")
	}

	// A meeting or an occurrence can reopen on the same row and be recorded again,
	// which must not replace the first file.
	objectKey := fmt.Sprintf("org-%s/recordings/%s/%s-%d.mp4", orgID, callID, callID, s.clock().UnixNano())
	egressID, err := s.mediaOf().StartRecording(ctx, &livekit.RoomCompositeEgressRequest{
		RoomName: call.RoomName,
		Layout:   "grid",
		Output: &livekit.RoomCompositeEgressRequest_File{
			File: &livekit.EncodedFileOutput{
				FileType: livekit.EncodedFileType_MP4,
				Filepath: objectKey,
				Output: &livekit.EncodedFileOutput_S3{
					S3: &livekit.S3Upload{
						AccessKey:      s.cfg.StorageAccessKey,
						Secret:         s.cfg.StorageSecretKey,
						Endpoint:       s.storageEndpointURL(),
						Bucket:         s.cfg.StorageBucket,
						ForcePathStyle: true,
						Region:         "us-east-1",
					},
				},
			},
		},
	})
	if err != nil {
		// Leave the call untouched: a failed start must surface a real error
		// rather than a silent success.
		return nil, fmt.Errorf("start recording: %w", err)
	}

	if _, err := s.db.Exec(ctx, `
		UPDATE calls SET recording_status = 'recording', recording_url = $3, updated_at = NOW()
		WHERE id = $1 AND org_id = $2
	`, callID, orgID, objectKey); err != nil {
		return nil, fmt.Errorf("record recording state: %w", err)
	}
	s.logger.Info().Str("call", callID.String()).Str("egress", egressID).Msg("recording started")

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToChannel(callID.String(), protocol.NewMessage(
			protocol.TypeCallSignalingRelay, map[string]any{"callId": callID.String(), "recording": true}), "")
	}
	return s.GetCall(ctx, orgID, callID)
}

// StopRecording finalises the file. Ending the call does this too, so a
// recording is never left dangling.
func (s *Service) StopRecording(ctx context.Context, orgID, callID uuid.UUID) error {
	call, err := s.GetCall(ctx, orgID, callID)
	if err != nil {
		return err
	}
	if call.RecordingStatus == nil || *call.RecordingStatus != "recording" {
		return nil
	}
	if s.egressService != nil {
		list, err := s.egressService.ListEgress(ctx, &livekit.ListEgressRequest{RoomName: call.RoomName})
		if err != nil {
			s.logger.Warn().Err(err).Msg("could not list egress to stop recording")
		} else {
			for _, info := range list.Items {
				if _, err := s.egressService.StopEgress(ctx, &livekit.StopEgressRequest{EgressId: info.EgressId}); err != nil {
					s.logger.Warn().Err(err).Str("egress", info.EgressId).Msg("stop egress failed")
				}
			}
		}
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE calls SET recording_status = 'completed', updated_at = NOW()
		WHERE id = $1 AND org_id = $2
	`, callID, orgID); err != nil {
		return fmt.Errorf("finalise recording: %w", err)
	}
	return nil
}

// storageEndpointURL gives Egress a scheme-qualified endpoint; the rest of the
// application stores it as a bare host:port for the MinIO client.
func (s *Service) storageEndpointURL() string {
	if s.cfg.StorageEndpoint == "" {
		return ""
	}
	if strings.HasPrefix(s.cfg.StorageEndpoint, "http://") || strings.HasPrefix(s.cfg.StorageEndpoint, "https://") {
		return s.cfg.StorageEndpoint
	}
	if s.cfg.StorageSecure {
		return "https://" + s.cfg.StorageEndpoint
	}
	return "http://" + s.cfg.StorageEndpoint
}

func (s *Service) EndCall(ctx context.Context, callID string) error {
	if s.roomService == nil {
		return nil
	}

	// Find room by name
	rooms, err := s.roomService.ListRooms(ctx, &livekit.ListRoomsRequest{})
	if err != nil {
		return err
	}

	for _, room := range rooms.Rooms {
		if room.Name == callID {
			_, err := s.roomService.DeleteRoom(ctx, &livekit.DeleteRoomRequest{Room: room.Name})
			return err
		}
	}

	return fmt.Errorf("room not found")
}

func (s *Service) HandleCallInvite(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.CallInvitePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	orgID, err := uuid.Parse(conn.OrgID())
	if err != nil {
		s.refuseInvite(conn)
		return
	}
	userID, err := uuid.Parse(conn.UserID())
	if err != nil {
		s.refuseInvite(conn)
		return
	}

	// Whoever is on the other end of this socket named these people, and the
	// shipped client never sends this frame at all — so the list is checked
	// against their own organization before a call exists, before anybody is
	// rung, and before a single token is minted.
	requested := make([]uuid.UUID, 0, len(payload.Invitees))
	seen := map[uuid.UUID]bool{}
	for _, raw := range payload.Invitees {
		id, err := uuid.Parse(raw)
		if err != nil {
			s.refuseInvite(conn)
			return
		}
		if !seen[id] {
			seen[id] = true
			requested = append(requested, id)
		}
	}
	invitees, err := s.inviteesInOrg(ctx, orgID, requested)
	if err != nil {
		s.logger.Error().Err(err).Msg("could not resolve the invitees on a call frame")
		s.refuseInvite(conn)
		return
	}

	// A frame naming somebody who is not in the caller's organization is
	// refused whole, rather than honoured for the part of it that checks out.
	//
	// Nothing has happened by this point — no call row, no room, no token — so
	// refusing leaves the world untouched, which is the direction to fail in
	// for a message that hands out media-server credentials. It is also the
	// clearer of the two failures: a caller who asked for three people and
	// silently got two sits in a room wondering why the third never picked up,
	// whereas a refusal says the invite did not happen. And there is no
	// legitimate partly-valid invite to preserve here — the shipped client
	// cannot produce one, so a frame like this is a bug or a probe either way.
	if len(invitees) != len(requested) {
		s.logger.Warn().
			Str("caller", userID.String()).Str("org", orgID.String()).
			Int("named", len(requested)).Int("resolved", len(invitees)).
			Msg("refused a call invite naming somebody outside the caller's organization")
		s.refuseInvite(conn)
		return
	}

	named := make([]string, 0, len(invitees))
	for _, id := range invitees {
		named = append(named, id.String())
	}

	call, err := s.CreateCall(ctx, orgID, userID, &payload.Title, payload.CallType, nil, named)
	if err != nil {
		return
	}

	callerToken, _ := s.GenerateToken(ctx, call.RoomName, conn.UserID(), "You", true, true)

	conn.Enqueue(protocol.NewMessage(protocol.TypeCallInvited, protocol.CallInvitedPayload{
		RoomName:   call.RoomName,
		CallerID:   conn.UserID(),
		CallerName: "You",
		CallType:   payload.CallType,
		Token:      callerToken,
	}))

	if s.broadcaster != nil {
		for _, invitee := range invitees {
			inviteeToken, _ := s.GenerateToken(ctx, call.RoomName, invitee.String(),
				s.DisplayName(ctx, invitee), true, true)
			s.broadcaster.BroadcastToUser(invitee.String(), protocol.NewMessage(protocol.TypeCallInvited, protocol.CallInvitedPayload{
				RoomName:   call.RoomName,
				CallerID:   conn.UserID(),
				CallerName: "Caller",
				CallType:   payload.CallType,
				Token:      inviteeToken,
			}))
		}
	}
}

// refuseInvite tells the caller their invite did not happen, so a client that
// has put up a "calling…" panel takes it down rather than waiting on a call
// that was never made.
//
// One reason for every refusal, deliberately: whether an id was malformed,
// belongs to nobody, or belongs to somebody in another organization is not
// something a caller is entitled to learn by asking.
func (s *Service) refuseInvite(conn websocket.WSConnection) {
	conn.Enqueue(protocol.NewMessage(protocol.TypeCallEnded, protocol.CallEndedPayload{
		Reason: "invalid_invitee",
	}))
}

// HandleCallAccept hands a join token to somebody answering a call. The room
// name comes from the client, so it decides nothing by itself: the same rules
// as POST /calls/:id/token apply, and a refusal is silent so that asking cannot
// be used to find out which rooms exist.
func (s *Service) HandleCallAccept(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.CallAcceptPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}
	userID, err := uuid.Parse(conn.UserID())
	if err != nil {
		return
	}
	orgID, err := uuid.Parse(conn.OrgID())
	if err != nil {
		return
	}

	// Only a live call is answered. A scheduled meeting does not ring, so there is
	// nothing to accept, and it opens through POST /calls/:id/token.
	call, err := scanCall(s.db.QueryRow(ctx,
		callColumns+` WHERE c.room_name = $1 AND c.org_id = $2 AND c.status = 'active'`,
		payload.RoomName, orgID))
	if err != nil {
		return
	}
	if mayJoin, err := s.MayJoin(ctx, orgID, call.ID, userID); err != nil || !mayJoin {
		return
	}

	token, _ := s.GenerateToken(ctx, call.RoomName, conn.UserID(), s.DisplayName(ctx, userID), true, true)
	s.notePickedUp(ctx, call, userID)

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(conn.UserID(), protocol.NewMessage(protocol.TypeCallInvited, protocol.CallInvitedPayload{
			CallID:   call.ID.String(),
			RoomName: call.RoomName,
			Token:    token,
		}))
	}
}

func (s *Service) HandleCallDecline(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.CallDeclinePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(conn.UserID(), protocol.NewMessage(protocol.TypeCallEnded, protocol.CallEndedPayload{
			RoomName: payload.RoomName,
			Duration: 0,
		}))
	}
}

func (s *Service) HandleCallSignaling(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.CallSignalingPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	// In a real implementation, this would relay signaling to LiveKit
	_ = payload
}

type ParticipantModel struct {
	UserID   uuid.UUID  `json:"userId"`
	UserName string     `json:"userName"`
	Role     string     `json:"role"`
	JoinedAt *time.Time `json:"joinedAt,omitempty"`
	LeftAt   *time.Time `json:"leftAt,omitempty"`
}

// Serialised straight onto the wire, so the tags have to match what the
// frontend reads.
type CallModel struct {
	ID              uuid.UUID  `json:"id"`
	OrgID           uuid.UUID  `json:"orgId"`
	ChannelID       *uuid.UUID `json:"channelId,omitempty"`
	RoomName        string     `json:"roomName"`
	LiveKitRoomSID  *string    `json:"-"`
	Title           *string    `json:"title,omitempty"`
	CallType        string     `json:"callType"`
	Status          string     `json:"status"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
	DurationSeconds *int       `json:"durationSeconds,omitempty"`
	RecordingURL    *string    `json:"recordingUrl,omitempty"`
	RecordingStatus *string    `json:"recordingStatus,omitempty"`
	// Set on a scheduled meeting and on one day of a standing meeting.
	ScheduledStart    *time.Time `json:"scheduledStart,omitempty"`
	ScheduledEnd      *time.Time `json:"scheduledEnd,omitempty"`
	StandingMeetingID *uuid.UUID `json:"standingMeetingId,omitempty"`
	OccurrenceDate    *string    `json:"occurrenceDate,omitempty"`
	// CanReadLog says whether whoever asked may read this call's log, so that nobody
	// is offered a button whose only answer is a refusal. Set for a reader, not stored.
	CanReadLog *bool `json:"canReadLog,omitempty"`

	CreatedBy        uuid.UUID          `json:"createdBy"`
	CreatedByName    string             `json:"createdByName"`
	ParticipantCount int                `json:"participantCount"`
	Participants     []ParticipantModel `json:"participants,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

const callColumns = `
	SELECT c.id, c.org_id, c.channel_id, c.room_name, c.livekit_room_sid, c.title,
	       c.call_type, c.status, c.started_at, c.ended_at, c.duration_seconds,
	       c.recording_url, c.recording_status,
	       c.scheduled_start, c.scheduled_end, c.standing_meeting_id, c.occurrence_date::text,
	       c.created_by, u.name, c.created_at, c.updated_at,
	       (SELECT COUNT(*) FROM call_participants p WHERE p.call_id = c.id)
	FROM calls c JOIN users u ON u.id = c.created_by`

func scanCall(row pgx.Row) (*CallModel, error) {
	var m CallModel
	err := row.Scan(&m.ID, &m.OrgID, &m.ChannelID, &m.RoomName, &m.LiveKitRoomSID, &m.Title,
		&m.CallType, &m.Status, &m.StartedAt, &m.EndedAt, &m.DurationSeconds,
		&m.RecordingURL, &m.RecordingStatus,
		&m.ScheduledStart, &m.ScheduledEnd, &m.StandingMeetingID, &m.OccurrenceDate,
		&m.CreatedBy, &m.CreatedByName, &m.CreatedAt, &m.UpdatedAt, &m.ParticipantCount)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// maxListedCalls bounds one list response; only the newest are kept.
const maxListedCalls = 1000

// ListCalls returns the organization's calls, most recent first, optionally
// narrowed by status or the channel the call belongs to.
// ListCalls returns the calls a person is entitled to see: ones they were part
// of, and ones belonging to a channel they are in. It used to filter on the
// organization alone, so a one-to-one call between two people was listed to
// everybody — who called whom, and when.
func (s *Service) ListCalls(ctx context.Context, orgID, viewerID uuid.UUID, status string, channelID *uuid.UUID) ([]CallModel, error) {
	args := []any{orgID, viewerID}
	where := []string{
		"c.org_id = $1",
		`(
		    EXISTS (SELECT 1 FROM call_participants p
		            WHERE p.call_id = c.id AND p.user_id = $2)
		    OR (c.channel_id IS NOT NULL AND EXISTS (
		            SELECT 1 FROM channel_members cm
		            WHERE cm.channel_id = c.channel_id AND cm.user_id = $2))
		)`,
	}
	if status != "" && status != "all" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("c.status = $%d", len(args)))
	} else {
		// A cancelled meeting never happened, so it is not history.
		where = append(where, "c.status <> 'cancelled'")
	}
	if channelID != nil {
		args = append(args, *channelID)
		where = append(where, fmt.Sprintf("c.channel_id = $%d", len(args)))
	}

	rows, err := s.db.Query(ctx,
		callColumns+" WHERE "+strings.Join(where, " AND ")+
			fmt.Sprintf(" ORDER BY c.created_at DESC LIMIT %d", maxListedCalls), args...)
	if err != nil {
		return nil, fmt.Errorf("list calls: %w", err)
	}
	defer rows.Close()

	calls := []CallModel{}
	for rows.Next() {
		m, err := scanCall(rows)
		if err != nil {
			return nil, fmt.Errorf("scan call: %w", err)
		}
		calls = append(calls, *m)
	}
	return calls, rows.Err()
}

// GetCall is organization-scoped and carries its participant list.
func (s *Service) GetCall(ctx context.Context, orgID, id uuid.UUID) (*CallModel, error) {
	call, err := scanCall(s.db.QueryRow(ctx,
		callColumns+" WHERE c.id = $1 AND c.org_id = $2", id, orgID))
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT p.user_id, u.name, COALESCE(p.role, 'participant'), p.joined_at, p.left_at
		FROM call_participants p JOIN users u ON u.id = p.user_id
		WHERE p.call_id = $1 ORDER BY p.joined_at NULLS LAST, u.name
	`, id)
	if err != nil {
		return nil, fmt.Errorf("list call participants: %w", err)
	}
	defer rows.Close()

	call.Participants = []ParticipantModel{}
	for rows.Next() {
		var p ParticipantModel
		if err := rows.Scan(&p.UserID, &p.UserName, &p.Role, &p.JoinedAt, &p.LeftAt); err != nil {
			return nil, fmt.Errorf("scan participant: %w", err)
		}
		call.Participants = append(call.Participants, p)
	}
	return call, rows.Err()
}

// MarkParticipantLeft records somebody turning a ring down. Connecting and leaving
// are not recorded here: those come from LiveKit, see webhook.go.
func (s *Service) MarkParticipantLeft(ctx context.Context, callID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE call_participants SET left_at = NOW() WHERE call_id = $1 AND user_id = $2`,
		callID, userID)
	if err != nil {
		return fmt.Errorf("mark left: %w", err)
	}
	return nil
}

// RingWindow is how long a call keeps trying to reach somebody. Past it, a
// call that was never answered is a missed one, and connecting no longer makes
// your phone ring for it.
const RingWindow = 45 * time.Second

// Reachable reports whether somebody has a session open. The caller is shown
// "ringing" when they do and "calling" when they do not — the difference
// between a phone making a noise somewhere and a message left in the air.
type Reachable interface {
	IsConnected(userID string) bool
}

// SetReachability wires the check. Without it everybody is treated as
// reachable, which is how this behaved before.
func (s *Service) SetReachability(r Reachable) { s.reachable = r }

// ChatRecorder writes a note into the conversation between two people. An
// interface so calls does not depend on the chat package.
type ChatRecorder interface {
	RecordCallEvent(ctx context.Context, orgID, callerID, calleeID uuid.UUID, text string) (uuid.UUID, error)
}

// SetChatRecorder wires where a missed call is written down.
func (s *Service) SetChatRecorder(c ChatRecorder) { s.chat = c }

// Notifier reaches somebody's devices, including ones with nothing open.
type Notifier interface {
	PushToUsers(ctx context.Context, userIDs []uuid.UUID, payload protocol.Push)
	Notify(ctx context.Context, orgID, userID uuid.UUID, kind, title, body string, data map[string]any) error
}

// SetNotifier wires how a phone is made to ring when nobody is looking.
func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

// SetJoinHook is told whenever a person connects to a call that is not over.
func (s *Service) SetJoinHook(f func(ctx context.Context, orgID, callID uuid.UUID, roomName string)) {
	s.joined = f
}

// ringDevices makes the phone itself ring, rather than only a browser tab that
// happens to still be open.
//
// A call is the one thing here that is worthless late: a message read an hour
// on is still the message, but a call nobody was told about is just a missed
// one. It holds the screen until it is dealt with, and its tag lets it be taken
// away again when the caller gives up.
func (s *Service) ringDevices(ctx context.Context, call *Call, callerName string, invitees []uuid.UUID) {
	if s.notifier == nil || len(invitees) == 0 {
		return
	}
	what := "video call"
	if call.CallType == "audio" {
		what = "call"
	}
	s.notifier.PushToUsers(ctx, invitees, protocol.Push{
		Title: callerName,
		Body:  "Incoming " + what,
		Tag:   "call-" + call.ID.String(),
		// It waits to be answered rather than sliding away unseen.
		RequireInteraction: true,
		Data: map[string]any{
			"kind":   "call.incoming",
			"url":    "/calls/" + call.ID.String(),
			"callId": call.ID.String(),
		},
	})
}

// stopRinging takes back a notification for a call that is over. Without it a
// phone that was in a pocket shows somebody calling long after they gave up.
func (s *Service) stopRinging(ctx context.Context, callID uuid.UUID, invitees []uuid.UUID) {
	if s.notifier == nil || len(invitees) == 0 {
		return
	}
	s.notifier.PushToUsers(ctx, invitees, protocol.Push{
		Title: "Call ended",
		Body:  "",
		Tag:   "call-" + callID.String(),
		Data:  map[string]any{"kind": "call.cancelled"},
	})
}

// NoteIfMissed writes "missed call" into the conversation when a one-to-one
// call ends without the other person ever picking up.
//
// Only for calls between two people. A call placed from a direct message is
// one of those even though it carries a channel, because a direct message is
// the conversation between exactly those two. A call to a real channel that
// nobody joined is not a missed call — it is a meeting nobody came to, and does
// not belong in anybody's private conversation.
func (s *Service) NoteIfMissed(ctx context.Context, callID uuid.UUID, reason string) {
	if s.chat == nil {
		return
	}

	var (
		orgID       uuid.UUID
		createdBy   uuid.UUID
		channelKind string
		answered    bool
		scheduled   bool
	)
	if err := s.db.QueryRow(ctx, `
		SELECT c.org_id, c.created_by, COALESCE(ch.type, ''), COALESCE(c.answered, false),
		       c.scheduled_start IS NOT NULL
		FROM calls c
		LEFT JOIN channels ch ON ch.id = c.channel_id
		WHERE c.id = $1
	`, callID).Scan(&orgID, &createdBy, &channelKind, &answered, &scheduled); err != nil {
		s.logger.Warn().Err(err).Msg("could not tell whether a call was answered")
		return
	}
	// A meeting nobody came to was never a call anybody missed.
	if answered || scheduled || (channelKind != "" && channelKind != "dm") {
		return
	}

	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM call_participants WHERE call_id = $1 AND user_id <> $2`,
		callID, createdBy)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not work out who was called")
		return
	}
	defer rows.Close()

	callees := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			callees = append(callees, id)
		}
	}
	// Exactly one other person: anything else is a group, not a private call.
	if len(callees) != 1 {
		return
	}

	channelID, err := s.chat.RecordCallEvent(ctx, orgID, createdBy, callees[0], noteFor(reason))
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not write a missed call into the conversation")
		return
	}

	// Whoever was calling is put into that conversation. A call that did not
	// happen almost always becomes a message, and making them go and find the
	// person again is the step nobody wants to take.
	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(createdBy.String(), protocol.NewMessage("call.missed", map[string]any{
			"callId":    callID.String(),
			"channelId": channelID.String(),
			"reason":    reason,
		}))
	}
}

// noteFor is what the conversation says happened. Turning somebody down and
// not being there are different things, and the person who was called knows
// which they did.
func noteFor(reason string) string {
	if reason == "declined" {
		return "Call declined"
	}
	return "Missed call"
}

func (s *Service) isReachable(userID string) bool {
	if s.reachable == nil {
		return true
	}
	return s.reachable.IsConnected(userID)
}

// PendingInvitation is a call still trying to reach somebody.
type PendingInvitation struct {
	CallID     uuid.UUID
	RoomName   string
	CallerID   uuid.UUID
	CallerName string
	CallType   string
	ChannelID  *uuid.UUID
}

// PendingInvitationsFor lists calls this person was invited to that are still
// within the ring window and which they have neither picked up nor turned down.
// Scheduled meetings and standing meetings never ring: people were told about
// them ahead of time, so joining one opens it and nobody's phone goes off.
//
// Somebody who was away when the phone rang should hear it when they get back,
// provided whoever called is still waiting.
func (s *Service) PendingInvitationsFor(ctx context.Context, userID uuid.UUID) ([]PendingInvitation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.room_name, c.created_by, COALESCE(u.name, u.email, ''), c.call_type, c.channel_id
		FROM call_participants p
		JOIN calls c ON c.id = p.call_id
		LEFT JOIN users u ON u.id = c.created_by
		WHERE p.user_id = $1
		  AND p.joined_at IS NULL
		  AND p.answered_at IS NULL
		  AND p.left_at IS NULL
		  AND c.status = 'active'
		  AND c.scheduled_start IS NULL
		  AND c.created_by <> $1
		  AND c.started_at > NOW() - $2::interval
	`, userID, RingWindow.String())
	if err != nil {
		return nil, fmt.Errorf("list pending invitations: %w", err)
	}
	defer rows.Close()

	pending := []PendingInvitation{}
	for rows.Next() {
		var p PendingInvitation
		if err := rows.Scan(&p.CallID, &p.RoomName, &p.CallerID, &p.CallerName,
			&p.CallType, &p.ChannelID); err != nil {
			return nil, fmt.Errorf("scan pending invitation: %w", err)
		}
		pending = append(pending, p)
	}
	return pending, rows.Err()
}

// DeliverPendingInvitations rings somebody for anything still waiting, and
// tells whoever is calling that their phone is now making a noise.
func (s *Service) DeliverPendingInvitations(ctx context.Context, userID uuid.UUID) {
	if s.broadcaster == nil {
		return
	}
	pending, err := s.PendingInvitationsFor(ctx, userID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not look for calls waiting on somebody")
		return
	}
	for _, p := range pending {
		token, err := s.GenerateToken(ctx, p.RoomName, userID.String(), s.DisplayName(ctx, userID), true, true)
		if err != nil {
			continue
		}
		payload := protocol.CallInvitedPayload{
			CallID:     p.CallID.String(),
			RoomName:   p.RoomName,
			CallerID:   p.CallerID.String(),
			CallerName: p.CallerName,
			CallType:   p.CallType,
			Token:      token,
		}
		if p.ChannelID != nil {
			payload.ChannelID = p.ChannelID.String()
		}
		s.broadcaster.BroadcastToUser(userID.String(), protocol.NewMessage(protocol.TypeCallInvited, payload))

		// The caller has been watching "calling"; it is ringing now.
		s.broadcaster.BroadcastToUser(p.CallerID.String(), protocol.NewMessage("call.reach", map[string]any{
			"callId":    p.CallID.String(),
			"userId":    userID.String(),
			"reachable": true,
		}))
	}
}

// notePickedUp records somebody asking for a token: the ring stops for them, and
// if they are not the caller this is no longer a missed call. It is deliberately
// not attendance, and deliberately does not wait for a webhook, so that missed
// calls and ringing keep working when webhooks are not arriving.
func (s *Service) notePickedUp(ctx context.Context, call *CallModel, userID uuid.UUID) {
	if _, err := s.db.Exec(ctx,
		`UPDATE call_participants SET answered_at = $3 WHERE call_id = $1 AND user_id = $2`,
		call.ID, userID, s.clock()); err != nil {
		s.logger.Warn().Err(err).Msg("could not record that somebody picked up")
	}
	if userID != call.CreatedBy {
		if err := s.MarkAnswered(ctx, call.ID); err != nil {
			s.logger.Warn().Err(err).Msg("could not record that a call was answered")
		}
	}
}

// MarkAnswered records that somebody other than the caller picked up, which is
// what separates a short call from a missed one once it is over.
func (s *Service) MarkAnswered(ctx context.Context, callID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE calls SET answered = TRUE WHERE id = $1`, callID)
	if err != nil {
		return fmt.Errorf("mark answered: %w", err)
	}
	return nil
}

// DisplayName is what to label somebody's panel with in a call. Falls back to
// the address only when there is no name, because a call full of email
// addresses tells you very little — and initials taken from one read as the
// domain rather than the person.
func (s *Service) DisplayName(ctx context.Context, userID uuid.UUID) string {
	var name, email string
	if err := s.db.QueryRow(ctx,
		`SELECT COALESCE(name, ''), COALESCE(email, '') FROM users WHERE id = $1`,
		userID).Scan(&name, &email); err != nil {
		s.logger.Warn().Err(err).Msg("could not load a name for a call participant")
		return ""
	}
	if name != "" {
		return name
	}
	return email
}

// MuteParticipant silences somebody's microphone in a call.
//
// A browser cannot do this: a participant only controls their own tracks, so
// asking somebody else to be quiet has to go through the media server. It mutes
// rather than removes, and it cannot unmute — turning a microphone back on is
// the owner's decision, and a server that could do it would be a server that
// can listen to a room at will.
func (s *Service) MuteParticipant(ctx context.Context, roomName, identity string) error {
	if s.roomService == nil {
		return apperrors.New(apperrors.CodeBadRequest, "calls are not configured on this installation")
	}

	participant, err := s.roomService.GetParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room: roomName, Identity: identity,
	})
	if err != nil {
		return apperrors.New(apperrors.CodeNotFound, "that person is not in this call")
	}

	var muted int
	for _, track := range participant.GetTracks() {
		if track.GetType() != livekit.TrackType_AUDIO || track.GetMuted() {
			continue
		}
		if _, err := s.roomService.MutePublishedTrack(ctx, &livekit.MuteRoomTrackRequest{
			Room: roomName, Identity: identity, TrackSid: track.GetSid(), Muted: true,
		}); err != nil {
			return fmt.Errorf("mute track: %w", err)
		}
		muted++
	}
	if muted == 0 {
		// Already quiet. Not an error: the caller wanted silence and has it.
		s.logger.Debug().Str("identity", identity).Msg("nothing to mute")
	}
	return nil
}

// StillOccupiedBySomebodyElse reports whether anybody other than this person is
// currently in the room.
//
// LiveKit is the authority: the database records who was invited and who
// joined, but nothing tells it when somebody closes their laptop, so it cannot
// answer this. Ending a call is only allowed when nobody else is in it, which
// is a question only the media server can settle.
//
// With no room service configured — the stub setup used in development — this
// reports false, because there is no room to be in.
func (s *Service) StillOccupiedBySomebodyElse(ctx context.Context, roomName, identity string) (bool, error) {
	if s.roomService == nil {
		return false, nil
	}
	res, err := s.roomService.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: roomName})
	if err != nil {
		return false, fmt.Errorf("list call participants: %w", err)
	}
	for _, participant := range res.GetParticipants() {
		if participant.GetIdentity() != identity {
			return true, nil
		}
	}
	return false, nil
}

// AnybodyLeftToAnswer reports whether the call still has somebody who was
// invited, has not joined and has not left. When nobody does, a call that was
// never picked up should stop rather than leave the caller sitting in an empty
// room.
func (s *Service) AnybodyLeftToAnswer(ctx context.Context, callID, callerID uuid.UUID) (bool, error) {
	var outstanding int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM call_participants
		WHERE call_id = $1 AND user_id <> $2 AND left_at IS NULL
	`, callID, callerID).Scan(&outstanding)
	if err != nil {
		return false, fmt.Errorf("count outstanding participants: %w", err)
	}
	return outstanding > 0, nil
}

// SomebodyJoined reports whether anybody other than the caller actually picked
// up, which is what separates "declined" from "nobody answered". Asking for a
// token counts as much as a connection LiveKit reported, or a call somebody is on
// would be dropped by another person's decline while webhooks are not arriving.
func (s *Service) SomebodyJoined(ctx context.Context, callID, callerID uuid.UUID) (bool, error) {
	var joined int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM call_participants
		WHERE call_id = $1 AND user_id <> $2 AND (joined_at IS NOT NULL OR answered_at IS NOT NULL)
	`, callID, callerID).Scan(&joined)
	if err != nil {
		return false, fmt.Errorf("count joined participants: %w", err)
	}
	return joined > 0, nil
}

// MarkCallEnded closes the record: without it a call stayed "active" forever
// and never appeared as history.
func (s *Service) MarkCallEnded(ctx context.Context, orgID, callID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE calls
		SET status = 'ended',
		    ended_at = NOW(),
		    duration_seconds = GREATEST(0, EXTRACT(EPOCH FROM (NOW() - COALESCE(started_at, created_at)))::int),
		    updated_at = NOW()
		WHERE id = $1 AND org_id = $2 AND status = 'active'
	`, callID, orgID)
	if err != nil {
		return fmt.Errorf("mark call ended: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	// A recording left running would never be finalised.
	if err := s.StopRecording(ctx, orgID, callID); err != nil {
		s.logger.Warn().Err(err).Str("call", callID.String()).Msg("could not finalise recording on call end")
	}

	// Anyone still connected has now left.
	if err := s.closeOpenSessions(ctx, callID, s.clock()); err != nil {
		return fmt.Errorf("close sessions: %w", err)
	}
	// Calls already running when attendance moved to webhooks have no sessions.
	if _, err := s.db.Exec(ctx,
		`UPDATE call_participants SET left_at = $2 WHERE call_id = $1 AND joined_at IS NOT NULL AND left_at IS NULL`,
		callID, s.clock()); err != nil {
		return fmt.Errorf("close participants: %w", err)
	}
	// A request cannot outlive its call.
	s.endPendingKnocks(ctx, callID)
	return nil
}

// IsParticipant reports whether the user was invited to, or has joined, a call.
func (s *Service) IsParticipant(ctx context.Context, callID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM call_participants WHERE call_id = $1 AND user_id = $2)`,
		callID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check call participation: %w", err)
	}
	return ok, nil
}

// MayJoin decides whether someone is entitled to a join token. Being in the
// organization is not enough: a private call between two people must not be
// joinable by everyone else who can list it.
//
// A call attached to a channel is open to that channel's members, which is how
// "join the standup" works without inviting people one by one. A day of a live
// standing meeting is open to everybody in the organization.
func (s *Service) MayJoin(ctx context.Context, orgID, callID, userID uuid.UUID) (bool, error) {
	member, err := s.IsMember(ctx, orgID, callID, userID)
	if err != nil || member {
		return member, err
	}

	var ok bool
	err = s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM calls c
			JOIN standing_meetings sm ON sm.id = c.standing_meeting_id AND sm.archived_at IS NULL
			JOIN users u ON u.org_id = c.org_id AND u.id = $3
			             AND u.deleted_at IS NULL AND COALESCE(u.is_active, true)
			WHERE c.id = $1 AND c.org_id = $2)
	`, callID, orgID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check standing meeting access: %w", err)
	}
	return ok, nil
}

// IsMember is the spec's member of a call: invited to it, admitted to it, or a
// member of its channel. Members answer knocks; being let in is wider than this
// only for a standing meeting, whose room is open without making anybody a member.
//
// Somebody deactivated is nobody's member, whatever rows they left behind.
func (s *Service) IsMember(ctx context.Context, orgID, callID, userID uuid.UUID) (bool, error) {
	active, err := s.isActivePerson(ctx, orgID, userID)
	if err != nil || !active {
		return false, err
	}
	invited, err := s.IsParticipant(ctx, callID, userID)
	if err != nil {
		return false, err
	}
	if invited {
		return true, nil
	}

	var ok bool
	err = s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM calls c
			JOIN channel_members cm ON cm.channel_id = c.channel_id
			WHERE c.id = $1 AND c.org_id = $2 AND cm.user_id = $3)
	`, callID, orgID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check call channel membership: %w", err)
	}
	return ok, nil
}

// reopenableDates is which days of a standing meeting may still come back: today in
// its zone, and whichever day its slot running now belongs to.
func (s *Service) reopenableDates(ctx context.Context, call *CallModel, now time.Time) ([]string, error) {
	if call.StandingMeetingID == nil {
		return nil, nil
	}
	m, err := s.standingMeeting(ctx, call.OrgID, *call.StandingMeetingID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(m.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("load zone: %w", err)
	}
	slot, err := m.occurrenceDay(now)
	if err != nil {
		return nil, err
	}
	return []string{now.In(loc).Format("2006-01-02"), slot.Format("2006-01-02")}, nil
}

// openForJoin brings a meeting or an occurrence to life when somebody comes to
// join it. This is how a scheduled meeting starts: it does not ring anybody,
// because everybody was told when they were invited and reminded before the
// start. An ended one comes back while its time is still running: a scheduled
// meeting until its end, an occurrence for the rest of its day. An ordinary call
// that has ended stays ended, which reports as ErrCallOver. An occurrence whose slot
// runs past midnight comes back until the end of that slot.
func (s *Service) openForJoin(ctx context.Context, call *CallModel) error {
	if call.Status == "active" {
		return nil
	}
	now := s.clock()
	dates, err := s.reopenableDates(ctx, call, now)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE calls c
		SET status = 'active', started_at = COALESCE(c.started_at, $2),
		    ended_at = NULL, duration_seconds = NULL, updated_at = NOW()
		WHERE c.id = $1 AND (
		    -- Somebody arriving at the same moment may have opened it already.
		    c.status IN ('scheduled', 'active')
		    OR (c.status = 'ended' AND c.standing_meeting_id IS NULL AND c.scheduled_end > $2)
		    OR (c.status = 'ended' AND EXISTS (
		        SELECT 1 FROM standing_meetings sm
		        WHERE sm.id = c.standing_meeting_id AND sm.archived_at IS NULL
		          AND c.occurrence_date::text = ANY($3::text[]))))
	`, call.ID, now, dates)
	if err != nil {
		return fmt.Errorf("open call: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrCallOver
	}
	// LiveKit would make the room on first connection anyway, without the size
	// limit, so a failure here is worth a warning and not a refused join.
	if _, err := s.createRoom(ctx, call.RoomName); err != nil {
		s.logger.Warn().Err(err).Str("room", call.RoomName).Msg("could not create the room for a meeting")
	}
	return nil
}

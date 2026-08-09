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
}

type Broadcaster interface {
	BroadcastToUser(userID string, msg *protocol.Message)
	BroadcastToChannel(channelID string, msg *protocol.Message, excludeUserID string)
}

type Service struct {
	db            *pgxpool.Pool
	cfg           Config
	logger        zerolog.Logger
	broadcaster   Broadcaster
	roomService   *lksdk.RoomServiceClient
	egressService *lksdk.EgressClient
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
		db:  db,
		cfg: cfg,
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

	var liveKitRoomSID *string

	if s.roomService != nil {
		room, err := s.roomService.CreateRoom(ctx, &livekit.CreateRoomRequest{
			Name:            roomName,
			EmptyTimeout:    300, // 5 minutes
			MaxParticipants: maxCallParticipants,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create LiveKit room: %w", err)
		}
		liveKitRoomSID = &room.Sid
	} else {
		// Fallback: stub room SID
		stubSID := "stub-" + roomName
		liveKitRoomSID = &stubSID
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

// maxCallParticipants is what a LiveKit room is created with, and therefore
// how many people a call can actually hold.
const maxCallParticipants = 50

// MaxChannelCallInvites bounds who gets rung when a call starts in a channel.
// Every member of a public channel is the whole organization, so without a
// bound one click in a busy channel rings everybody and writes a row each.
const MaxChannelCallInvites = maxCallParticipants - 1

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
	`, channelID, orgID, except, MaxChannelCallInvites)
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
	if s.broadcaster == nil {
		return
	}
	for _, invitee := range invitees {
		token, err := s.GenerateToken(ctx, call.RoomName, invitee.String(), "Participant", true, true)
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
	}
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
// turned it down.
func (s *Service) AnnounceCallOver(ctx context.Context, call *CallModel, reason, declinedBy string, recipients []string) {
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
	if s.egressService == nil {
		return nil, apperrors.New(apperrors.CodeUnavailable, "recording is not configured on this instance")
	}
	if s.cfg.StorageBucket == "" {
		return nil, apperrors.New(apperrors.CodeUnavailable, "recording storage is not configured")
	}

	objectKey := fmt.Sprintf("org-%s/recordings/%s/%s.mp4", orgID, callID, callID)
	res, err := s.egressService.StartRoomCompositeEgress(ctx, &livekit.RoomCompositeEgressRequest{
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
	s.logger.Info().Str("call", callID.String()).Str("egress", res.EgressId).Msg("recording started")

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
			_, err := s.roomService.DeleteRoom(ctx, &livekit.DeleteRoomRequest{Room: room.Sid})
			return err
		}
	}

	return fmt.Errorf("room not found")
}

func (s *Service) HandleCallInvite(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.CallInvitePayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return
	}

	orgID, _ := uuid.Parse(conn.OrgID())
	userID, _ := uuid.Parse(conn.UserID())

	call, err := s.CreateCall(ctx, orgID, userID, &payload.Title, payload.CallType, nil, payload.Invitees)
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
		for _, invitee := range payload.Invitees {
			inviteeToken, _ := s.GenerateToken(ctx, call.RoomName, invitee, "Participant", true, true)
			s.broadcaster.BroadcastToUser(invitee, protocol.NewMessage(protocol.TypeCallInvited, protocol.CallInvitedPayload{
				RoomName:   call.RoomName,
				CallerID:   conn.UserID(),
				CallerName: "Caller",
				CallType:   payload.CallType,
				Token:      inviteeToken,
			}))
		}
	}
}

func (s *Service) HandleCallAccept(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.CallAcceptPayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return
	}

	token, _ := s.GenerateToken(ctx, payload.RoomName, conn.UserID(), "Participant", true, true)

	if s.broadcaster != nil {
		s.broadcaster.BroadcastToUser(conn.UserID(), protocol.NewMessage(protocol.TypeCallInvited, protocol.CallInvitedPayload{
			RoomName: payload.RoomName,
			Token:    token,
		}))
	}
}

func (s *Service) HandleCallDecline(ctx context.Context, conn websocket.WSConnection, msg *protocol.Message) {
	var payload protocol.CallDeclinePayload
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
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
	payloadBytes, ok := msg.Payload.([]byte)
	if !ok {
		return
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
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
	       c.recording_url, c.recording_status, c.created_by, u.name,
	       c.created_at, c.updated_at,
	       (SELECT COUNT(*) FROM call_participants p WHERE p.call_id = c.id)
	FROM calls c JOIN users u ON u.id = c.created_by`

func scanCall(row pgx.Row) (*CallModel, error) {
	var m CallModel
	err := row.Scan(&m.ID, &m.OrgID, &m.ChannelID, &m.RoomName, &m.LiveKitRoomSID, &m.Title,
		&m.CallType, &m.Status, &m.StartedAt, &m.EndedAt, &m.DurationSeconds,
		&m.RecordingURL, &m.RecordingStatus, &m.CreatedBy, &m.CreatedByName,
		&m.CreatedAt, &m.UpdatedAt, &m.ParticipantCount)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// ListCalls returns the organization's calls, most recent first, optionally
// narrowed by status or the channel the call belongs to.
func (s *Service) ListCalls(ctx context.Context, orgID uuid.UUID, status string, channelID *uuid.UUID) ([]CallModel, error) {
	where := []string{"c.org_id = $1"}
	args := []any{orgID}
	if status != "" && status != "all" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("c.status = $%d", len(args)))
	}
	if channelID != nil {
		args = append(args, *channelID)
		where = append(where, fmt.Sprintf("c.channel_id = $%d", len(args)))
	}

	rows, err := s.db.Query(ctx,
		callColumns+" WHERE "+strings.Join(where, " AND ")+" ORDER BY c.created_at DESC", args...)
	if err != nil {
		return nil, fmt.Errorf("list calls: %w", err)
	}
	defer rows.Close()

	calls := []CallModel{}
	for rows.Next() {
		var m CallModel
		if err := rows.Scan(&m.ID, &m.OrgID, &m.ChannelID, &m.RoomName, &m.LiveKitRoomSID, &m.Title,
			&m.CallType, &m.Status, &m.StartedAt, &m.EndedAt, &m.DurationSeconds,
			&m.RecordingURL, &m.RecordingStatus, &m.CreatedBy, &m.CreatedByName,
			&m.CreatedAt, &m.UpdatedAt, &m.ParticipantCount); err != nil {
			return nil, fmt.Errorf("scan call: %w", err)
		}
		calls = append(calls, m)
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

// MarkParticipantJoined and MarkParticipantLeft keep the attendance record
// that the call detail page shows.
func (s *Service) MarkParticipantJoined(ctx context.Context, callID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO call_participants (call_id, user_id, role, joined_at)
		VALUES ($1, $2, 'participant', NOW())
		ON CONFLICT (call_id, user_id) DO UPDATE SET joined_at = COALESCE(call_participants.joined_at, NOW()), left_at = NULL
	`, callID, userID)
	if err != nil {
		return fmt.Errorf("mark joined: %w", err)
	}
	return nil
}

func (s *Service) MarkParticipantLeft(ctx context.Context, callID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE call_participants SET left_at = NOW() WHERE call_id = $1 AND user_id = $2`,
		callID, userID)
	if err != nil {
		return fmt.Errorf("mark left: %w", err)
	}
	return nil
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
		WHERE id = $1 AND org_id = $2 AND status <> 'ended'
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

	// Anyone still marked present has now left.
	if _, err := s.db.Exec(ctx,
		`UPDATE call_participants SET left_at = NOW() WHERE call_id = $1 AND joined_at IS NOT NULL AND left_at IS NULL`,
		callID); err != nil {
		return fmt.Errorf("close participants: %w", err)
	}
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
// "join the standup" works without inviting people one by one.
func (s *Service) MayJoin(ctx context.Context, orgID, callID, userID uuid.UUID) (bool, error) {
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

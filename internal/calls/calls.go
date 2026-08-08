package calls

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/websocket"
	"github.com/teamup/teamup/pkg/protocol"
)

type Config struct {
	LiveKitURL       string
	LiveKitAPIKey    string
	LiveKitAPISecret string
}

type Broadcaster interface {
	BroadcastToUser(userID string, msg *protocol.Message)
	BroadcastToChannel(channelID string, msg *protocol.Message, excludeUserID string)
}

type Service struct {
	db          *pgxpool.Pool
	cfg         Config
	logger      zerolog.Logger
	broadcaster Broadcaster
	roomService *lksdk.RoomServiceClient
}

type Call struct {
	ID              uuid.UUID
	OrgID           uuid.UUID
	ChannelID       *uuid.UUID
	RoomName        string
	LiveKitRoomSID  *string
	Title           *string
	CallType        string
	Status          string
	StartedAt       *time.Time
	EndedAt         *time.Time
	DurationSeconds *int
	RecordingURL    *string
	RecordingStatus *string
	CreatedBy       uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CallParticipant struct {
	CallID     uuid.UUID
	UserID     uuid.UUID
	Role       string
	JoinedAt   *time.Time
	LeftAt     *time.Time
	IsMuted    bool
	IsCameraOn bool
}

func NewService(db *pgxpool.Pool, cfg Config) *Service {
	s := &Service{
		db:  db,
		cfg: cfg,
	}

	// Initialize LiveKit room service client
	if cfg.LiveKitURL != "" && cfg.LiveKitAPIKey != "" && cfg.LiveKitAPISecret != "" {
		s.roomService = lksdk.NewRoomServiceClient(cfg.LiveKitURL, cfg.LiveKitAPIKey, cfg.LiveKitAPISecret)
	}

	return s
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
			MaxParticipants: 50,
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

	for _, invitee := range invitees {
		_, _ = uuid.Parse(invitee)
		// INSERT INTO call_participants ...
	}

	return call, nil
}

func (s *Service) GetToken(ctx context.Context, callID, userID, userName string) (string, error) {
	return s.GenerateToken(ctx, callID, userID, userName, true, true)
}

func (s *Service) StartRecording(ctx context.Context, callID string) error {
	// Recording is handled via EgressClient in LiveKit, not RoomServiceClient
	// For now, return not implemented
	return fmt.Errorf("recording not implemented - use EgressClient")
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

type CallModel struct {
	ID              uuid.UUID
	OrgID           uuid.UUID
	ChannelID       *uuid.UUID
	RoomName        string
	LiveKitRoomSID  *string
	Title           *string
	CallType        string
	Status          string
	StartedAt       *time.Time
	EndedAt         *time.Time
	DurationSeconds *int
	RecordingURL    *string
	RecordingStatus *string
	CreatedBy       uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (s *Service) ListCalls(ctx context.Context, orgID uuid.UUID, status string) ([]CallModel, error) {
	return nil, nil
}

func (s *Service) GetCall(ctx context.Context, id uuid.UUID) (*CallModel, error) {
	return nil, nil
}

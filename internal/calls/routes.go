package calls

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := NewHandler(service)

	rg.GET("/calls", h.ListCalls)
	rg.POST("/calls", h.CreateCall)
	rg.GET("/calls/:id", h.GetCall)
	rg.POST("/calls/:id/token", h.GetCallToken)
	rg.POST("/calls/:id/record", h.StartRecording)
	rg.POST("/calls/:id/end", h.EndCall)
	rg.POST("/calls/:id/decline", h.DeclineCall)
	rg.POST("/calls/:id/mute/:userId", h.MuteParticipant)
	rg.DELETE("/calls/:id/record", h.StopRecording)
}

func (h *Handler) ListCalls(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	viewerID, err := uuid.Parse(user.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	status := c.Query("status")

	var channelID *uuid.UUID
	if raw := c.Query("channelId"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			channelID = &id
		}
	}

	calls, err := h.service.ListCalls(c.Request.Context(), orgID, viewerID, status, channelID)
	if err != nil {
		httpx.Error(c, err, "failed to list calls")
		return
	}
	c.JSON(http.StatusOK, gin.H{"calls": calls})
}

func (h *Handler) CreateCall(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	userID, _ := uuid.Parse(user.UserID)

	var req struct {
		Title     *string  `json:"title"`
		ChannelID *string  `json:"channelId"`
		CallType  string   `json:"callType" binding:"required,oneof=video audio"`
		Invitees  []string `json:"invitees"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	var channelID *uuid.UUID
	invitees := req.Invitees
	notInvited := 0
	if req.ChannelID != nil {
		id, err := uuid.Parse(*req.ChannelID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channelId"})
			return
		}
		channelID = &id

		// A channel's call belongs to that channel. Anybody in the
		// organization is a member of a public channel, so this is what keeps
		// a private channel's call private.
		member, err := h.service.IsChannelMember(ctx, orgID, id, userID)
		if err != nil {
			httpx.Error(c, err, "failed to start call")
			return
		}
		if !member {
			c.JSON(http.StatusForbidden, gin.H{"error": "you are not a member of this channel"})
			return
		}

		// Everybody in the channel is called, so a caller does not have to
		// name them one at a time — up to what a room can hold.
		members, total, err := h.service.ChannelMembersFor(ctx, orgID, id, userID)
		if err != nil {
			httpx.Error(c, err, "failed to start call")
			return
		}
		notInvited = total - len(members)
		invitees = make([]string, 0, len(members))
		for _, m := range members {
			invitees = append(invitees, m.String())
		}
	}

	call, err := h.service.CreateCall(ctx, orgID, userID, req.Title, req.CallType, channelID, invitees)
	if err != nil {
		httpx.Error(c, err, "failed to create call")
		return
	}

	// Ring them. Without this a call existed but nobody was ever told.
	// Detached from the request: the caller should not wait on a token and a
	// frame per invitee, and the call is already made either way.
	inviteeIDs := make([]uuid.UUID, 0, len(invitees))
	for _, raw := range invitees {
		if id, err := uuid.Parse(raw); err == nil {
			inviteeIDs = append(inviteeIDs, id)
		}
	}
	ringCtx := context.WithoutCancel(ctx)
	name := callerName(user)
	go h.service.Ring(ringCtx, call, user.UserID, name, inviteeIDs)

	// Say so rather than quietly reaching a subset.
	call.NotInvited = notInvited

	c.JSON(http.StatusCreated, call)
}

// DeclineCall turns a call down and says so, so the caller finds out rather
// than watching it ring out.
func (h *Handler) DeclineCall(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, err := uuid.Parse(user.OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	userID, err := uuid.Parse(user.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	ctx := c.Request.Context()
	call, err := h.service.GetCall(ctx, orgID, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "call not found"})
		return
	}

	// Only somebody who was actually called can decline it.
	invited, err := h.service.IsParticipant(ctx, id, userID)
	if err != nil {
		httpx.Error(c, err, "failed to decline call")
		return
	}
	if !invited {
		c.JSON(http.StatusForbidden, gin.H{"error": "you were not called"})
		return
	}

	// A call that simply rang out is not the same as one somebody turned down,
	// and the caller should be told which.
	var req struct {
		Reason string `json:"reason" binding:"omitempty,oneof=declined no_answer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	reason := req.Reason
	if reason == "" {
		reason = "declined"
	}

	if err := h.service.MarkParticipantLeft(ctx, id, userID); err != nil {
		httpx.Error(c, err, "failed to decline call")
		return
	}

	// Tell the person who started it, not everybody: one person turning it
	// down does not end the call for the rest.
	go h.service.AnnounceCallOver(context.WithoutCancel(ctx), call, reason, callerName(user),
		[]string{call.CreatedBy.String()})

	// If nobody is left who might still pick up, the call is over. Otherwise
	// the caller waits in an empty room until they notice.
	waiting, err := h.service.AnybodyLeftToAnswer(ctx, id, call.CreatedBy)
	if err != nil {
		httpx.Error(c, err, "failed to decline call")
		return
	}
	if !waiting {
		joined, err := h.service.SomebodyJoined(ctx, id, call.CreatedBy)
		if err != nil {
			httpx.Error(c, err, "failed to decline call")
			return
		}
		// Only drop it if the call never actually got going.
		if !joined {
			if err := h.service.EndCall(ctx, call.RoomName); err != nil {
				h.service.logger.Warn().Err(err).Str("room", call.RoomName).Msg("LiveKit room teardown failed")
			}
			if err := h.service.MarkCallEnded(ctx, orgID, id); err != nil {
				httpx.Error(c, err, "failed to end call")
				return
			}
			go h.service.NoteIfMissed(context.WithoutCancel(ctx), id)
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": reason})
}

// MuteParticipant silences somebody else's microphone. Anybody in the call can
// do it — a room where only a host can quieten a stuck microphone is a room
// that stays unusable until the host notices.
//
// It only mutes. Nobody can turn somebody else's microphone back on.
func (h *Handler) MuteParticipant(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, err := uuid.Parse(user.OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	userID, err := uuid.Parse(user.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	target := c.Param("userId")

	ctx := c.Request.Context()
	call, err := h.service.GetCall(ctx, orgID, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "call not found"})
		return
	}

	// You have to be entitled to be in the call to quieten anybody in it.
	mayJoin, err := h.service.MayJoin(ctx, orgID, id, userID)
	if err != nil {
		httpx.Error(c, err, "failed to check call access")
		return
	}
	if !mayJoin && !user.HasAnyRole("owner", "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "you are not part of this call"})
		return
	}

	if err := h.service.MuteParticipant(ctx, call.RoomName, target); err != nil {
		httpx.Error(c, err, "failed to mute")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "muted"})
}

// callerName prefers something a callee will recognise over a bare id.
func callerName(user *auth.UserContext) string {
	if user.Email != "" {
		return user.Email
	}
	return "Somebody"
}

func (h *Handler) GetCall(c *gin.Context) {
	orgID, _ := uuid.Parse(auth.GetUserFromContext(c).OrgID)
	id, _ := uuid.Parse(c.Param("id"))
	call, err := h.service.GetCall(c.Request.Context(), orgID, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "call not found"})
		return
	}
	c.JSON(http.StatusOK, call)
}

// GetCallToken issues a LiveKit join grant. It previously signed a token for
// the raw call id — which is not the room name — without loading the call or
// checking that the caller belongs to the organization, so any authenticated
// user could mint a grant for any UUID-shaped room.
func (h *Handler) GetCallToken(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, err := uuid.Parse(user.OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	call, err := h.service.GetCall(c.Request.Context(), orgID, id)
	if err != nil {
		httpx.Error(c, err, "call not found")
		return
	}
	if call.Status == "ended" {
		c.JSON(http.StatusConflict, gin.H{"error": "this call has ended"})
		return
	}

	// Being in the organization is not enough: a call between two people must
	// not be joinable by anyone who can see it in the list.
	userID, err := uuid.Parse(user.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	mayJoin, err := h.service.MayJoin(c.Request.Context(), orgID, id, userID)
	if err != nil {
		httpx.Error(c, err, "failed to check call access")
		return
	}
	if !mayJoin {
		c.JSON(http.StatusForbidden, gin.H{"error": "you were not invited to this call"})
		return
	}

	// The name here is what labels their panel in the call, so it has to be
	// the person's name rather than their address — initials taken from an
	// address read as the domain rather than the person.
	displayName := h.service.DisplayName(c.Request.Context(), userID)
	if displayName == "" {
		displayName = user.Email
	}
	token, err := h.service.GetToken(c.Request.Context(), call.RoomName, user.UserID, displayName)
	if err != nil {
		httpx.Error(c, err, "failed to generate token")
		return
	}

	// Issuing a join grant is the point at which someone actually joins.
	if err := h.service.MarkParticipantJoined(c.Request.Context(), id, userID); err != nil {
		h.service.logger.Warn().Err(err).Msg("could not record call attendance")
	}
	// Somebody other than the caller picking up is what makes this not a
	// missed call, whatever happens to it afterwards.
	if userID != call.CreatedBy {
		if err := h.service.MarkAnswered(c.Request.Context(), id); err != nil {
			h.service.logger.Warn().Err(err).Msg("could not record that a call was answered")
		}
	}

	c.JSON(http.StatusOK, gin.H{"token": token, "roomName": call.RoomName})
}

func (h *Handler) StartRecording(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	if !user.HasAnyRole("owner", "admin", "manager") {
		c.JSON(http.StatusForbidden, gin.H{"error": "you may not record calls"})
		return
	}
	orgID, _ := uuid.Parse(user.OrgID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	call, err := h.service.StartRecording(c.Request.Context(), orgID, id)
	if err != nil {
		httpx.Error(c, err, "failed to start recording")
		return
	}
	c.JSON(http.StatusOK, call)
}

func (h *Handler) StopRecording(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	if !user.HasAnyRole("owner", "admin", "manager") {
		c.JSON(http.StatusForbidden, gin.H{"error": "you may not record calls"})
		return
	}
	orgID, _ := uuid.Parse(user.OrgID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.service.StopRecording(c.Request.Context(), orgID, id); err != nil {
		httpx.Error(c, err, "failed to stop recording")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "recording stopped"})
}

func (h *Handler) EndCall(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	id, _ := uuid.Parse(c.Param("id"))
	call, err := h.service.GetCall(c.Request.Context(), orgID, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "call not found"})
		return
	}

	// Ending a call drops everyone, so it needs the same entitlement as
	// joining — it previously had no check at all.
	userID, err := uuid.Parse(user.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	mayEnd, err := h.service.MayJoin(c.Request.Context(), orgID, id, userID)
	if err != nil {
		httpx.Error(c, err, "failed to check call access")
		return
	}
	if !mayEnd && !user.HasAnyRole("owner", "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "you are not part of this call"})
		return
	}

	// A call ends when the last person leaves, and not before. Nobody hangs up
	// on everybody else, so this closes the record only once the room is empty
	// apart from whoever is asking.
	occupied, err := h.service.StillOccupiedBySomebodyElse(c.Request.Context(), call.RoomName, user.UserID)
	if err != nil {
		httpx.Error(c, err, "failed to end call")
		return
	}
	if occupied {
		c.JSON(http.StatusConflict, gin.H{
			"error": "somebody else is still in this call; leave it instead",
		})
		return
	}

	if err := h.service.EndCall(c.Request.Context(), call.RoomName); err != nil {
		// The room may already be gone; the record still has to be closed.
		h.service.logger.Warn().Err(err).Str("room", call.RoomName).Msg("LiveKit room teardown failed")
	}
	if err := h.service.MarkCallEnded(c.Request.Context(), orgID, id); err != nil {
		httpx.Error(c, err, "failed to end call")
		return
	}

	// Anybody still being rung has to be told, or their phone keeps ringing
	// after the caller has already hung up.
	if others, err := h.service.Participants(c.Request.Context(), id, user.UserID); err == nil {
		go h.service.AnnounceCallOver(context.WithoutCancel(c.Request.Context()), call, "ended", "", others)
	}
	// Hanging up on a call nobody ever picked up leaves a missed call behind,
	// which is the only trace the other person gets if they were away.
	go h.service.NoteIfMissed(context.WithoutCancel(c.Request.Context()), id)

	c.JSON(http.StatusOK, gin.H{"message": "call ended"})
}

package calls

import (
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
	rg.DELETE("/calls/:id/record", h.StopRecording)
}

func (h *Handler) ListCalls(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	status := c.Query("status")

	var channelID *uuid.UUID
	if raw := c.Query("channelId"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			channelID = &id
		}
	}

	calls, err := h.service.ListCalls(c.Request.Context(), orgID, status, channelID)
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

	var channelID *uuid.UUID
	if req.ChannelID != nil {
		id, _ := uuid.Parse(*req.ChannelID)
		channelID = &id
	}

	call, err := h.service.CreateCall(c.Request.Context(), orgID, userID, req.Title, req.CallType, channelID, req.Invitees)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create call"})
		return
	}

	c.JSON(http.StatusCreated, call)
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

	token, err := h.service.GetToken(c.Request.Context(), call.RoomName, user.UserID, user.Email)
	if err != nil {
		httpx.Error(c, err, "failed to generate token")
		return
	}

	// Issuing a join grant is the point at which someone actually joins.
	if err := h.service.MarkParticipantJoined(c.Request.Context(), id, userID); err != nil {
		h.service.logger.Warn().Err(err).Msg("could not record call attendance")
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

	if err := h.service.EndCall(c.Request.Context(), call.RoomName); err != nil {
		// The room may already be gone; the record still has to be closed.
		h.service.logger.Warn().Err(err).Str("room", call.RoomName).Msg("LiveKit room teardown failed")
	}
	if err := h.service.MarkCallEnded(c.Request.Context(), orgID, id); err != nil {
		httpx.Error(c, err, "failed to end call")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "call ended"})
}

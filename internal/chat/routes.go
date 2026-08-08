package chat

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := NewHandler(service)

	rg.GET("/channels", h.ListChannels)
	rg.POST("/channels", h.CreateChannel)
	rg.GET("/channels/:id", h.GetChannel)
	rg.PATCH("/channels/:id", h.UpdateChannel)
	rg.DELETE("/channels/:id", h.DeleteChannel)
	rg.POST("/channels/:id/members", h.AddMember)
	rg.DELETE("/channels/:id/members/:userId", h.RemoveMember)

	rg.GET("/channels/:id/messages", h.ListMessages)
	rg.POST("/channels/:id/messages", h.SendMessage)
	rg.PATCH("/messages/:id", h.EditMessage)
	rg.DELETE("/messages/:id", h.DeleteMessage)
	rg.POST("/messages/:id/reactions", h.AddReaction)
	rg.DELETE("/messages/:id/reactions/:emoji", h.RemoveReaction)
}

func (h *Handler) ListChannels(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)

	channels, err := h.service.ListChannels(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list channels"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"channels": channels})
}

func (h *Handler) CreateChannel(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	userID, _ := uuid.Parse(user.UserID)

	var req struct {
		Name        *string `json:"name"`
		Type        string  `json:"type" binding:"required,oneof=dm group public announcement"`
		Description *string `json:"description"`
		TeamID      *string `json:"teamId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var teamID *uuid.UUID
	if req.TeamID != nil {
		id, _ := uuid.Parse(*req.TeamID)
		teamID = &id
	}

	channel, err := h.service.CreateChannel(c.Request.Context(), orgID, userID, req.Name, req.Type, req.Description, teamID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create channel"})
		return
	}

	c.JSON(http.StatusCreated, channel)
}

func (h *Handler) GetChannel(c *gin.Context) {
	channelID, _ := uuid.Parse(c.Param("id"))
	channel, err := h.service.GetChannel(c.Request.Context(), channelID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "channel not found"})
		return
	}
	c.JSON(http.StatusOK, channel)
}

func (h *Handler) UpdateChannel(c *gin.Context) {
	channelID, _ := uuid.Parse(c.Param("id"))
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		IsPrivate   *bool   `json:"isPrivate"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// UPDATE channels SET ...
	_ = channelID
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func (h *Handler) DeleteChannel(c *gin.Context) {
	channelID, _ := uuid.Parse(c.Param("id"))
	// UPDATE channels SET deleted_at = NOW(), is_archived = TRUE WHERE id = ...
	_ = channelID
	c.JSON(http.StatusOK, gin.H{"message": "archived"})
}

func (h *Handler) AddMember(c *gin.Context) {
	channelID, _ := uuid.Parse(c.Param("id"))
	userID, _ := uuid.Parse(c.Param("userId"))

	var req struct {
		Role string `json:"role" binding:"omitempty,oneof=owner admin member"`
	}
	if req.Role == "" {
		req.Role = "member"
	}

	if err := h.service.AddMember(c.Request.Context(), channelID, userID, req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "member added"})
}

func (h *Handler) RemoveMember(c *gin.Context) {
	channelID, _ := uuid.Parse(c.Param("id"))
	userID, _ := uuid.Parse(c.Param("userId"))
	// DELETE FROM channel_members WHERE channel_id = ... AND user_id = ...
	_ = channelID
	_ = userID
	c.JSON(http.StatusOK, gin.H{"message": "member removed"})
}

func (h *Handler) ListMessages(c *gin.Context) {
	channelID, _ := uuid.Parse(c.Param("id"))
	limit := 50
	if l := c.Query("limit"); l != "" {
		// parse limit
	}

	messages, err := h.service.GetMessages(c.Request.Context(), channelID, limit, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get messages"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": messages})
}

func (h *Handler) SendMessage(c *gin.Context) {
	channelID, _ := uuid.Parse(c.Param("id"))
	user := auth.GetUserFromContext(c)
	userID, _ := uuid.Parse(user.UserID)

	var req struct {
		Content     string   `json:"content" binding:"required"`
		ReplyTo     *string  `json:"replyTo"`
		Attachments []string `json:"attachments"` // object keys
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_ = channelID
	_ = userID
	// In real implementation, create message and broadcast via WS
	c.JSON(http.StatusCreated, gin.H{"message": "sent"})
}

func (h *Handler) EditMessage(c *gin.Context) {
	messageID, _ := uuid.Parse(c.Param("id"))
	var req struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_ = messageID
	// UPDATE messages SET content = ..., edited_at = NOW() WHERE id = ...
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func (h *Handler) DeleteMessage(c *gin.Context) {
	messageID, _ := uuid.Parse(c.Param("id"))
	// UPDATE messages SET deleted_at = NOW() WHERE id = ...
	_ = messageID
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *Handler) AddReaction(c *gin.Context) {
	messageID, _ := uuid.Parse(c.Param("id"))
	emoji := c.Param("emoji")
	user := auth.GetUserFromContext(c)
	userID, _ := uuid.Parse(user.UserID)
	// INSERT INTO message_reactions ...
	_ = messageID
	_ = emoji
	_ = userID
	c.JSON(http.StatusOK, gin.H{"message": "reaction added"})
}

func (h *Handler) RemoveReaction(c *gin.Context) {
	messageID, _ := uuid.Parse(c.Param("id"))
	emoji := c.Param("emoji")
	user := auth.GetUserFromContext(c)
	userID, _ := uuid.Parse(user.UserID)
	// DELETE FROM message_reactions WHERE message_id = ... AND user_id = ... AND emoji = ...
	_ = messageID
	_ = emoji
	_ = userID
	c.JSON(http.StatusOK, gin.H{"message": "reaction removed"})
}

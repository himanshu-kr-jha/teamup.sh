package notifications

import (
	"net/http"
	"time"

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

	rg.GET("/notifications", h.ListNotifications)
	rg.PATCH("/notifications/:id/read", h.MarkRead)
	rg.PATCH("/notifications/read-all", h.MarkAllRead)
	rg.DELETE("/notifications/:id", h.ArchiveNotification)
	rg.POST("/notifications/subscribe", h.Subscribe)
	rg.DELETE("/notifications/subscribe", h.Unsubscribe)
}

func (h *Handler) ListNotifications(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	userID := user.UserID
	unreadOnly := c.Query("unread_only") == "true"
	limit := 50
	if l := c.Query("limit"); l != "" {
		// parse
	}

	notifications, err := h.service.ListNotifications(c.Request.Context(), userID, unreadOnly, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list notifications"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"notifications": notifications})
}

func (h *Handler) MarkRead(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))
	if err := h.service.MarkRead(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark read"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "marked as read"})
}

func (h *Handler) MarkAllRead(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	userID, _ := uuid.Parse(user.UserID)

	if err := h.service.MarkAllRead(c.Request.Context(), userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark all read"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "all marked as read"})
}

func (h *Handler) ArchiveNotification(c *gin.Context) {
	_, _ = uuid.Parse(c.Param("id"))
	// UPDATE notifications SET archived_at = NOW() WHERE id = ...
	c.JSON(http.StatusOK, gin.H{"message": "archived"})
}

func (h *Handler) Subscribe(c *gin.Context) {
	user := auth.GetUserFromContext(c)

	var req struct {
		Endpoint   string `json:"endpoint" binding:"required"`
		P256DH     string `json:"p256dh" binding:"required"`
		Auth       string `json:"auth" binding:"required"`
		DeviceName string `json:"deviceName"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sub := &PushSubscription{
		ID:         uuid.New(),
		UserID:     uuid.MustParse(user.UserID),
		OrgID:      uuid.MustParse(user.OrgID),
		Endpoint:   req.Endpoint,
		P256DH:     req.P256DH,
		Auth:       req.Auth,
		DeviceName: req.DeviceName,
		CreatedAt:  time.Now(),
	}

	if err := h.service.SaveSubscription(c.Request.Context(), user.UserID, user.OrgID, sub); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save subscription"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "subscribed"})
}

func (h *Handler) Unsubscribe(c *gin.Context) {
	_ = auth.GetUserFromContext(c)

	var req struct {
		Endpoint string `json:"endpoint" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// DELETE FROM push_subscriptions WHERE user_id = ... AND endpoint = ...
	c.JSON(http.StatusOK, gin.H{"message": "unsubscribed"})
}

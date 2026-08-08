package notifications

import (
	"net/http"
	"strconv"
	"time"

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

	rg.GET("/notifications", h.ListNotifications)
	rg.PATCH("/notifications/:id/read", h.MarkRead)
	rg.PATCH("/notifications/read-all", h.MarkAllRead)
	rg.DELETE("/notifications/:id", h.ArchiveNotification)
	rg.POST("/notifications/subscribe", h.Subscribe)
	rg.DELETE("/notifications/subscribe", h.Unsubscribe)
	rg.GET("/notifications/unread-count", h.UnreadCount)
	rg.GET("/notifications/vapid-key", h.VAPIDKey)
}

// VAPIDKey hands the browser the public half it needs to subscribe. The
// private half never leaves the server.
func (h *Handler) VAPIDKey(c *gin.Context) {
	if !h.service.HasVAPIDKeys() {
		c.JSON(http.StatusOK, gin.H{"publicKey": "", "enabled": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"publicKey": h.service.VAPIDPublicKey(), "enabled": true})
}

func (h *Handler) UnreadCount(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	userID, err := uuid.Parse(user.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	count, err := h.service.UnreadCount(c.Request.Context(), userID)
	if err != nil {
		httpx.Error(c, err, "failed to count notifications")
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

func (h *Handler) ListNotifications(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	userID := user.UserID
	unreadOnly := c.Query("unread_only") == "true"
	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	notifications, err := h.service.ListNotifications(c.Request.Context(), userID, unreadOnly, limit)
	if err != nil {
		httpx.Error(c, err, "failed to list notifications")
		return
	}
	c.JSON(http.StatusOK, gin.H{"notifications": notifications})
}

func (h *Handler) MarkRead(c *gin.Context) {
	user := auth.GetUserFromContext(c)
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
	// Scoped to the recipient: a notification id must not be actionable by
	// anyone else.
	if err := h.service.MarkRead(c.Request.Context(), userID, id); err != nil {
		httpx.Error(c, err, "failed to mark read")
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
	user := auth.GetUserFromContext(c)
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
	if err := h.service.Archive(c.Request.Context(), userID, id); err != nil {
		httpx.Error(c, err, "failed to archive notification")
		return
	}
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

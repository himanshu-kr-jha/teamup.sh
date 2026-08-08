package presence

import (
	"net/http"

	"github.com/gin-gonic/gin"

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

	rg.GET("/presence/:userId", h.GetPresence)
	rg.PATCH("/presence", h.UpdatePresence)
	rg.GET("/presence/org/online", h.ListOnline)
}

func (h *Handler) GetPresence(c *gin.Context) {
	userID := c.Param("userId")
	user := auth.GetUserFromContext(c)
	orgID := user.OrgID

	presence, err := h.service.GetPresence(userID, orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get presence"})
		return
	}
	if presence == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "presence not found"})
		return
	}
	c.JSON(http.StatusOK, presence)
}

func (h *Handler) UpdatePresence(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID := user.OrgID
	userID := user.UserID

	var req struct {
		Status            string `json:"status" binding:"required,oneof=online away busy in_call offline"`
		CustomStatus      string `json:"customStatus"`
		CustomStatusEmoji string `json:"customStatusEmoji"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.UpdateStatus(userID, orgID, req.Status, req.CustomStatus, req.CustomStatusEmoji); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update presence"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "presence updated"})
}

func (h *Handler) ListOnline(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID := user.OrgID

	presences, err := h.service.GetAllOnline(orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list online users"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": presences})
}

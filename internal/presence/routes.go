package presence

import (
	"time"

	"github.com/google/uuid"
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
	rg.GET("/presence/availability", h.ListAvailability)
	rg.PUT("/presence/availability", h.SetAvailability)
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

	if err := validCustomStatus(req.CustomStatus, req.CustomStatusEmoji); err != nil {
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

// ListAvailability answers for everybody in the organization, which is what
// the chat sidebar shows beside each name.
func (h *Handler) ListAvailability(c *gin.Context) {
	orgID, err := uuid.Parse(auth.GetUserFromContext(c).OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	people, err := h.service.ListOrgAvailability(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list availability"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"people": people})
}

// SetAvailability records what somebody has chosen to say about themselves.
// Always about the caller: nobody sets anybody else's status.
func (h *Handler) SetAvailability(c *gin.Context) {
	userID, err := uuid.Parse(auth.GetUserFromContext(c).UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	var req struct {
		Status string     `json:"status" binding:"required,oneof=available away busy on_leave"`
		Note   string     `json:"note"`
		Until  *time.Time `json:"until"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Note) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keep the note under 200 characters"})
		return
	}

	if err := h.service.SetAvailability(c.Request.Context(), userID,
		Availability{Status: req.Status, Note: req.Note, Until: req.Until}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set availability"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "availability updated"})
}

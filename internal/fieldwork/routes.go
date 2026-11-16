package fieldwork

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
)

type Handler struct{ service *Service }

// RegisterRoutes mounts field work on the signed-in API.
func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := &Handler{service: service}
	rg.GET("/field-work/settings", h.GetSettings)
	rg.PUT("/field-work/settings", h.PutSettings)
	rg.GET("/field-work/checkins/mine", h.Mine)
	rg.POST("/field-work/checkins", h.CheckIn)
	rg.DELETE("/field-work/checkins/:id", h.Remove)
	rg.GET("/field-work/checkins/:id/photo", h.Photo)
}

func caller(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	u := auth.GetUserFromContext(c)
	orgID, err1 := uuid.Parse(u.OrgID)
	userID, err2 := uuid.Parse(u.UserID)
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

func (h *Handler) GetSettings(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	set, err := h.service.Settings(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to read field work settings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": set.Enabled, "mayChange": auth.GetUserFromContext(c).Can(auth.PermOrgSettings)})
}

func (h *Handler) PutSettings(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	if !auth.GetUserFromContext(c).Can(auth.PermOrgSettings) {
		c.JSON(http.StatusForbidden, gin.H{"error": "only somebody who changes organization settings can turn field work on or off"})
		return
	}
	var set Settings
	if err := c.ShouldBindJSON(&set); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "say whether field work is on"})
		return
	}
	if err := h.service.SetSettings(c.Request.Context(), orgID, set); err != nil {
		httpx.Error(c, err, "failed to save field work settings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": set.Enabled, "mayChange": true})
}

func (h *Handler) Mine(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	list, err := h.service.Mine(c.Request.Context(), orgID, userID, c.Query("today"))
	if err != nil {
		httpx.Error(c, err, "failed to read field work")
		return
	}
	c.JSON(http.StatusOK, gin.H{"checkins": list})
}

func (h *Handler) CheckIn(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	// A data: URL is a third larger than the photo it carries.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxPhotoBytes*2)
	var in Input
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "that photo is too large, or the request is not valid"})
		return
	}
	checkin, err := h.service.CheckIn(c.Request.Context(), orgID, userID, in)
	if err != nil {
		httpx.Error(c, err, "failed to save field work")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"checkin": checkin})
}

func (h *Handler) Remove(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.service.Remove(c.Request.Context(), orgID, userID, id, c.Query("today")); err != nil {
		httpx.Error(c, err, "failed to remove field work")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Photo(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	u := auth.GetUserFromContext(c)
	photo, kind, err := h.service.Photo(c.Request.Context(), orgID, id, Viewer{
		ID: userID, Reviewer: u.Can(auth.PermAttendance), Admin: u.Can(auth.PermMeetingsAll),
	})
	if err != nil {
		httpx.Error(c, err, "failed to read the photo")
		return
	}
	// Private to the people above, so not kept by anything in between.
	c.Header("Cache-Control", "private, max-age=3600")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, kind, photo)
}

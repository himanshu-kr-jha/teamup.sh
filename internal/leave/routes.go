package leave

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
)

// Handler is the HTTP face of leave and holidays.
type Handler struct {
	service *Service
}

// RegisterRoutes mounts everything on the signed-in group.
func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := &Handler{service: service}
	rg.GET("/leave/people", h.People)
	rg.GET("/leave", h.List)
	rg.POST("/leave", h.Add)
	rg.PATCH("/leave/:id", h.Change)
	rg.DELETE("/leave/:id", h.Remove)
	rg.GET("/leave/:id/history", h.History)
	rg.GET("/holidays", h.Holidays)
	rg.POST("/holidays", h.AddHoliday)
	rg.DELETE("/holidays/:id", h.RemoveHoliday)
}

func caller(c *gin.Context) (uuid.UUID, Actor, bool) {
	user := auth.GetUserFromContext(c)
	orgID, orgErr := uuid.Parse(user.OrgID)
	userID, userErr := uuid.Parse(user.UserID)
	if orgErr != nil || userErr != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return uuid.Nil, Actor{}, false
	}
	return orgID, Actor{ID: userID, Admin: user.Can(auth.PermMembers)}, true
}

func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return uuid.Nil, false
	}
	return id, true
}

// People is whose leave the caller may see and write, themself first.
func (h *Handler) People(c *gin.Context) {
	orgID, a, ok := caller(c)
	if !ok {
		return
	}
	people, err := h.service.People(c.Request.Context(), orgID, a)
	if err != nil {
		httpx.Error(c, err, "failed to list people")
		return
	}
	c.JSON(http.StatusOK, gin.H{"people": people})
}

// List is leave touching ?from=&to=, optionally for ?userId= alone.
func (h *Handler) List(c *gin.Context) {
	orgID, a, ok := caller(c)
	if !ok {
		return
	}
	var only *uuid.UUID
	if raw := c.Query("userId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userId"})
			return
		}
		only = &id
	}
	list, err := h.service.List(c.Request.Context(), orgID, a, c.Query("from"), c.Query("to"), only)
	if err != nil {
		httpx.Error(c, err, "failed to list leave")
		return
	}
	c.JSON(http.StatusOK, gin.H{"leave": list})
}

type leaveBody struct {
	UserID   *string `json:"userId"`
	StartsOn string  `json:"startsOn" binding:"required"`
	EndsOn   *string `json:"endsOn"`
	Part     string  `json:"part"`
	Note     string  `json:"note"`
}

func (b leaveBody) input() Input {
	return Input{StartsOn: b.StartsOn, EndsOn: b.EndsOn, Part: b.Part, Note: b.Note}
}

// Add records leave, for the caller unless userId names somebody else.
func (h *Handler) Add(c *gin.Context) {
	orgID, a, ok := caller(c)
	if !ok {
		return
	}
	var body leaveBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startsOn is required"})
		return
	}
	in := body.input()
	in.UserID = a.ID
	if body.UserID != nil && *body.UserID != "" {
		id, err := uuid.Parse(*body.UserID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userId"})
			return
		}
		in.UserID = id
	}
	l, err := h.service.Add(c.Request.Context(), orgID, a, in)
	if err != nil {
		httpx.Error(c, err, "failed to add leave")
		return
	}
	c.JSON(http.StatusCreated, l)
}

// Change rewrites a record.
func (h *Handler) Change(c *gin.Context) {
	orgID, a, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var body leaveBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "startsOn is required"})
		return
	}
	l, err := h.service.Change(c.Request.Context(), orgID, a, id, body.input())
	if err != nil {
		httpx.Error(c, err, "failed to change leave")
		return
	}
	c.JSON(http.StatusOK, l)
}

// Remove takes a record away, keeping its history.
func (h *Handler) Remove(c *gin.Context) {
	orgID, a, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.service.Remove(c.Request.Context(), orgID, a, id); err != nil {
		httpx.Error(c, err, "failed to remove leave")
		return
	}
	c.Status(http.StatusNoContent)
}

// History is every change to a record.
func (h *Handler) History(c *gin.Context) {
	orgID, a, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	changes, err := h.service.History(c.Request.Context(), orgID, a, id)
	if err != nil {
		httpx.Error(c, err, "failed to read the history")
		return
	}
	c.JSON(http.StatusOK, gin.H{"changes": changes})
}

// Holidays lists ?from=&to=.
func (h *Handler) Holidays(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	list, err := h.service.Holidays(c.Request.Context(), orgID, c.Query("from"), c.Query("to"))
	if err != nil {
		httpx.Error(c, err, "failed to list holidays")
		return
	}
	c.JSON(http.StatusOK, gin.H{"holidays": list})
}

func mayKeepHolidays(c *gin.Context) bool {
	if !auth.GetUserFromContext(c).Can(auth.PermOrgSettings) {
		c.JSON(http.StatusForbidden, gin.H{"error": "only an administrator keeps the holidays"})
		return false
	}
	return true
}

// AddHoliday marks a date off for the whole organization.
func (h *Handler) AddHoliday(c *gin.Context) {
	orgID, a, ok := caller(c)
	if !ok || !mayKeepHolidays(c) {
		return
	}
	var body struct {
		Date string `json:"date" binding:"required"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "date is required"})
		return
	}
	holiday, err := h.service.AddHoliday(c.Request.Context(), orgID, a.ID, body.Date, body.Name)
	if err != nil {
		httpx.Error(c, err, "failed to add the holiday")
		return
	}
	c.JSON(http.StatusCreated, holiday)
}

// RemoveHoliday takes one off.
func (h *Handler) RemoveHoliday(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok || !mayKeepHolidays(c) {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.service.RemoveHoliday(c.Request.Context(), orgID, id); err != nil {
		httpx.Error(c, err, "failed to remove the holiday")
		return
	}
	c.Status(http.StatusNoContent)
}

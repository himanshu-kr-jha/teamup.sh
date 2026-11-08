package calendar

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
)

// Handler is the HTTP face of the calendar.
type Handler struct {
	service *Service
}

// RegisterRoutes mounts everything on the signed-in group.
func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := &Handler{service: service}

	rg.GET("/calendar", h.Calendar)
	rg.GET("/calendar/availability", h.Availability)
	rg.POST("/calendar/blocks", h.CreateBlock)
	rg.PATCH("/calendar/blocks/:id", h.UpdateBlock)
	rg.DELETE("/calendar/blocks/:id", h.DeleteBlock)
	rg.POST("/meetings", h.ScheduleMeeting)
	rg.PATCH("/meetings/:id", h.UpdateMeeting)
	rg.DELETE("/meetings/:id", h.CancelMeeting)
}

func caller(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	user := auth.GetUserFromContext(c)
	orgID, orgErr := uuid.Parse(user.OrgID)
	userID, userErr := uuid.Parse(user.UserID)
	if orgErr != nil || userErr != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

func isOrgAdmin(c *gin.Context) bool {
	return auth.GetUserFromContext(c).Can(auth.PermMeetingsAll)
}

func refuse(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": message})
}

func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		refuse(c, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}

func queryRange(c *gin.Context) (time.Time, time.Time, bool) {
	from, err := time.Parse(time.RFC3339, c.Query("from"))
	if err != nil {
		refuse(c, "from must be an RFC 3339 time")
		return time.Time{}, time.Time{}, false
	}
	to, err := time.Parse(time.RFC3339, c.Query("to"))
	if err != nil {
		refuse(c, "to must be an RFC 3339 time")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// parseTime reads an optional time from a body. A value that is not one is
// refused rather than ignored.
func parseTime(raw *string) (*time.Time, bool) {
	if raw == nil {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, false
	}
	return &t, true
}

func parseIDs(raw []string) ([]uuid.UUID, bool) {
	ids := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}

func (h *Handler) Calendar(c *gin.Context) {
	orgID, viewer, ok := caller(c)
	if !ok {
		return
	}
	target := viewer
	if raw := c.Query("userId"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			refuse(c, "invalid userId")
			return
		}
		target = parsed
	}
	from, to, ok := queryRange(c)
	if !ok {
		return
	}
	items, err := h.service.Calendar(c.Request.Context(), orgID, viewer, target, isOrgAdmin(c), from, to)
	if err != nil {
		httpx.Error(c, err, "failed to load calendar")
		return
	}
	if items == nil {
		items = []Item{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Handler) Availability(c *gin.Context) {
	orgID, _, ok := caller(c)
	if !ok {
		return
	}
	raw := c.Query("userIds")
	if raw == "" {
		refuse(c, "userIds is required")
		return
	}
	ids, ok := parseIDs(strings.Split(raw, ","))
	if !ok {
		refuse(c, "invalid userIds")
		return
	}
	from, to, ok := queryRange(c)
	if !ok {
		return
	}
	people, err := h.service.Availability(c.Request.Context(), orgID, ids, from, to)
	if err != nil {
		httpx.Error(c, err, "failed to load availability")
		return
	}
	c.JSON(http.StatusOK, gin.H{"people": people})
}

func (h *Handler) CreateBlock(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	var req struct {
		Kind     string  `json:"kind"`
		Title    string  `json:"title"`
		StartsAt *string `json:"startsAt"`
		EndsAt   *string `json:"endsAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		refuse(c, "invalid request")
		return
	}
	start, okStart := parseTime(req.StartsAt)
	end, okEnd := parseTime(req.EndsAt)
	if !okStart || !okEnd || start == nil || end == nil {
		refuse(c, "startsAt and endsAt must be RFC 3339 times")
		return
	}
	b, err := h.service.CreateBlock(c.Request.Context(), orgID, userID,
		Block{Kind: req.Kind, Title: strings.TrimSpace(req.Title), StartsAt: *start, EndsAt: *end})
	if err != nil {
		httpx.Error(c, err, "failed to create block")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"block": b})
}

func (h *Handler) UpdateBlock(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req struct {
		Kind     *string `json:"kind"`
		Title    *string `json:"title"`
		StartsAt *string `json:"startsAt"`
		EndsAt   *string `json:"endsAt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		refuse(c, "invalid request")
		return
	}
	start, okStart := parseTime(req.StartsAt)
	end, okEnd := parseTime(req.EndsAt)
	if !okStart || !okEnd {
		refuse(c, "startsAt and endsAt must be RFC 3339 times")
		return
	}
	b, err := h.service.UpdateBlock(c.Request.Context(), orgID, userID, id,
		blockPatch{Kind: req.Kind, Title: req.Title, StartsAt: start, EndsAt: end})
	if err != nil {
		httpx.Error(c, err, "failed to update block")
		return
	}
	c.JSON(http.StatusOK, gin.H{"block": b})
}

func (h *Handler) DeleteBlock(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.service.DeleteBlock(c.Request.Context(), orgID, userID, id); err != nil {
		httpx.Error(c, err, "failed to delete block")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *Handler) ScheduleMeeting(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	var req struct {
		Title      string   `json:"title"`
		StartsAt   *string  `json:"startsAt"`
		EndsAt     *string  `json:"endsAt"`
		InviteeIDs []string `json:"inviteeIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		refuse(c, "invalid request")
		return
	}
	start, okStart := parseTime(req.StartsAt)
	end, okEnd := parseTime(req.EndsAt)
	if !okStart || !okEnd || start == nil || end == nil {
		refuse(c, "startsAt and endsAt must be RFC 3339 times")
		return
	}
	invitees, ok := parseIDs(req.InviteeIDs)
	if !ok {
		refuse(c, "invalid inviteeIds")
		return
	}
	m, conflicts, err := h.service.ScheduleMeeting(c.Request.Context(), orgID, userID, req.Title, *start, *end, invitees)
	if err != nil {
		httpx.Error(c, err, "failed to schedule meeting")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"meeting": m, "conflicts": conflicts})
}

func (h *Handler) UpdateMeeting(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req struct {
		Title      *string   `json:"title"`
		StartsAt   *string   `json:"startsAt"`
		EndsAt     *string   `json:"endsAt"`
		InviteeIDs *[]string `json:"inviteeIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		refuse(c, "invalid request")
		return
	}
	start, okStart := parseTime(req.StartsAt)
	end, okEnd := parseTime(req.EndsAt)
	if !okStart || !okEnd {
		refuse(c, "startsAt and endsAt must be RFC 3339 times")
		return
	}
	patch := meetingPatch{Title: req.Title, StartsAt: start, EndsAt: end}
	if req.InviteeIDs != nil {
		invitees, ok := parseIDs(*req.InviteeIDs)
		if !ok {
			refuse(c, "invalid inviteeIds")
			return
		}
		patch.InviteeIDs = &invitees
	}
	m, conflicts, err := h.service.UpdateMeeting(c.Request.Context(), orgID, id, userID, isOrgAdmin(c), patch)
	if err != nil {
		httpx.Error(c, err, "failed to update meeting")
		return
	}
	c.JSON(http.StatusOK, gin.H{"meeting": m, "conflicts": conflicts})
}

func (h *Handler) CancelMeeting(c *gin.Context) {
	orgID, userID, ok := caller(c)
	if !ok {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.service.CancelMeeting(c.Request.Context(), orgID, id, userID, isOrgAdmin(c)); err != nil {
		httpx.Error(c, err, "failed to cancel meeting")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "cancelled"})
}

package tasks

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := NewHandler(service)

	rg.GET("/tasks", h.ListTasks)
	rg.GET("/tasks/managed-teams", h.ManagedTeams)
	rg.GET("/tasks/assignable-users", h.AssignableUsers)
	rg.POST("/tasks", h.CreateTask)
	rg.GET("/tasks/:id", h.GetTask)
	rg.PATCH("/tasks/:id", h.UpdateTask)
	rg.DELETE("/tasks/:id", h.DeleteTask)
	rg.GET("/tasks/:id/comments", h.ListComments)
	rg.POST("/tasks/:id/comments", h.AddComment)
	rg.POST("/tasks/:id/labels", h.AddLabel)
	rg.DELETE("/tasks/:id/labels/:labelId", h.RemoveLabel)
	rg.GET("/tasks/:id/activities", h.GetActivities)
	rg.GET("/tasks/:id/watchers", h.ListWatchers)
	rg.POST("/tasks/:id/watchers", h.AddWatcher)
	rg.DELETE("/tasks/:id/watchers/:userId", h.RemoveWatcher)
	rg.GET("/labels", h.ListLabels)
	rg.POST("/labels", h.CreateLabel)
}

type caller struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
}

func callerFrom(c *gin.Context) (caller, bool) {
	u := auth.GetUserFromContext(c)
	orgID, err := uuid.Parse(u.OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return caller{}, false
	}
	userID, err := uuid.Parse(u.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return caller{}, false
	}
	return caller{OrgID: orgID, UserID: userID}, true
}

func pathUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + param})
		return uuid.Nil, false
	}
	return id, true
}

func queryUUID(c *gin.Context, key string) *uuid.UUID {
	raw := c.Query(key)
	if raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

func (h *Handler) ListTasks(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	filters := Filters{
		Status:     c.Query("status"),
		AssigneeID: queryUUID(c, "assigneeId"),
		ProjectID:  queryUUID(c, "projectId"),
		LabelID:    queryUUID(c, "labelId"),
		Search:     c.Query("search"),
		TeamID:     queryUUID(c, "teamId"),
		// Every card comes back saying what this caller may do to it. A board
		// that shows work somebody cannot act on is the product decision; a
		// board that does not say so is the bug.
		Viewer:           &cl.UserID,
		ViewerIsOrgAdmin: auth.GetUserFromContext(c).HasAnyRole("owner", "admin"),
	}
	// scope=team asks for the work of the people this caller manages. It is
	// always the caller — you cannot ask to see somebody else's reports.
	switch c.Query("scope") {
	case "team":
		filters.ManagedBy = &cl.UserID
	case "mine":
		filters.Involving = &cl.UserID
	}

	tasks, err := h.service.ListTasks(c.Request.Context(), cl.OrgID, filters)
	if err != nil {
		httpx.Error(c, err, "failed to list tasks")
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

// ManagedTeams answers with the teams the caller runs and who is on them, so
// the team view can offer a filter per person.
func (h *Handler) ManagedTeams(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	teams, err := h.service.ManagedTeams(c.Request.Context(), cl.OrgID, cl.UserID)
	if err != nil {
		httpx.Error(c, err, "failed to list managed teams")
		return
	}
	c.JSON(http.StatusOK, gin.H{"teams": teams})
}

// AssignableUsers answers with the people this caller may assign work to, so
// the create form only offers names that will be accepted.
func (h *Handler) AssignableUsers(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	people, err := h.service.AssignableUsers(c.Request.Context(), cl.OrgID, cl.UserID,
		auth.GetUserFromContext(c).HasAnyRole("owner", "admin"))
	if err != nil {
		httpx.Error(c, err, "failed to list assignable users")
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": people})
}

func (h *Handler) CreateTask(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	var req struct {
		Title       string   `json:"title" binding:"required"`
		Description string   `json:"description"`
		ProjectID   *string  `json:"projectId"`
		AssigneeID  *string  `json:"assigneeId"`
		Status      string   `json:"status" binding:"omitempty,oneof=todo in_progress in_review done blocked"`
		Priority    string   `json:"priority" binding:"omitempty,oneof=low medium high urgent"`
		TaskType    string   `json:"taskType" binding:"omitempty,oneof=task bug story epic subtask"`
		DueDate     *string  `json:"dueDate"`
		StartDate   *string  `json:"startDate"`
		Labels      []string `json:"labels"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	projectID, err := optionalUUID(req.ProjectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid projectId"})
		return
	}
	assigneeID, err := optionalUUID(req.AssigneeID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid assigneeId"})
		return
	}
	dueDate, err := optionalTime(req.DueDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dueDate"})
		return
	}
	startDate, err := optionalTime(req.StartDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid startDate"})
		return
	}

	task, err := h.service.CreateTask(c.Request.Context(), cl.OrgID, cl.UserID,
		req.Title, req.Description, req.Status, req.Priority, req.TaskType,
		projectID, assigneeID, dueDate, startDate, req.Labels,
		auth.GetUserFromContext(c).HasAnyRole("owner", "admin"))
	if err != nil {
		httpx.Error(c, err, "failed to create task")
		return
	}
	c.JSON(http.StatusCreated, task)
}

func optionalUUID(s *string) (*uuid.UUID, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func optionalTime(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (h *Handler) GetTask(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	task, err := h.service.GetTaskFor(c.Request.Context(), cl.OrgID, id, cl.UserID,
		auth.GetUserFromContext(c).HasAnyRole("owner", "admin"))
	if err != nil {
		httpx.Error(c, err, "task not found")
		return
	}
	c.JSON(http.StatusOK, task)
}

func (h *Handler) UpdateTask(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	task, err := h.service.UpdateTask(c.Request.Context(), cl.OrgID, id, cl.UserID, req,
		auth.GetUserFromContext(c).HasAnyRole("owner", "admin"))
	if err != nil {
		httpx.Error(c, err, "failed to update task")
		return
	}
	c.JSON(http.StatusOK, task)
}

func (h *Handler) DeleteTask(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteTask(c.Request.Context(), cl.OrgID, id, cl.UserID,
		auth.GetUserFromContext(c).HasAnyRole("owner", "admin")); err != nil {
		httpx.Error(c, err, "failed to delete task")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *Handler) ListComments(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	comments, err := h.service.ListComments(c.Request.Context(), cl.OrgID, id)
	if err != nil {
		httpx.Error(c, err, "failed to list comments")
		return
	}
	c.JSON(http.StatusOK, gin.H{"comments": comments})
}

func (h *Handler) AddComment(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		// Not required: a file on its own is a comment.
		Content       string   `json:"content"`
		AttachmentIDs []string `json:"attachmentIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	attachmentIDs := make([]uuid.UUID, 0, len(req.AttachmentIDs))
	for _, raw := range req.AttachmentIDs {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid attachmentIds"})
			return
		}
		attachmentIDs = append(attachmentIDs, parsed)
	}

	comment, err := h.service.AddComment(c.Request.Context(), cl.OrgID, id, cl.UserID,
		req.Content, attachmentIDs)
	if err != nil {
		httpx.Error(c, err, "failed to add comment")
		return
	}
	c.JSON(http.StatusCreated, comment)
}

func (h *Handler) AddLabel(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		LabelID string `json:"labelId" binding:"required,uuid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	labelID, err := uuid.Parse(req.LabelID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid labelId"})
		return
	}
	if err := h.service.AddLabel(c.Request.Context(), cl.OrgID, id, labelID); err != nil {
		httpx.Error(c, err, "failed to add label")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "label added"})
}

func (h *Handler) RemoveLabel(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	labelID, ok := pathUUID(c, "labelId")
	if !ok {
		return
	}
	if err := h.service.RemoveLabel(c.Request.Context(), cl.OrgID, id, labelID); err != nil {
		httpx.Error(c, err, "failed to remove label")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "label removed"})
}

func (h *Handler) GetActivities(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	activities, err := h.service.GetActivities(c.Request.Context(), cl.OrgID, id)
	if err != nil {
		httpx.Error(c, err, "failed to get activities")
		return
	}
	c.JSON(http.StatusOK, gin.H{"activities": activities})
}

func (h *Handler) ListLabels(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	labels, err := h.service.ListLabels(c.Request.Context(), cl.OrgID)
	if err != nil {
		httpx.Error(c, err, "failed to list labels")
		return
	}
	c.JSON(http.StatusOK, gin.H{"labels": labels})
}

func (h *Handler) CreateLabel(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	var req struct {
		Name  string `json:"name" binding:"required"`
		Color string `json:"color"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	label, err := h.service.CreateLabel(c.Request.Context(), cl.OrgID, req.Name, req.Color)
	if err != nil {
		httpx.Error(c, err, "failed to create label")
		return
	}
	c.JSON(http.StatusCreated, label)
}

func (h *Handler) ListWatchers(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	watchers, err := h.service.ListWatchers(c.Request.Context(), cl.OrgID, id)
	if err != nil {
		httpx.Error(c, err, "failed to list watchers")
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": watchers})
}

// AddWatcher defaults to the caller, so "follow this task" needs no body.
func (h *Handler) AddWatcher(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	// Following yourself is always allowed. Putting somebody else on a task
	// follows the org chart, exactly like assigning work to them — otherwise
	// any member could sign anybody up for every notification on any task.
	target := cl.UserID
	var req struct {
		UserID string `json:"userId" binding:"omitempty,uuid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.UserID != "" {
		parsed, err := uuid.Parse(req.UserID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userId"})
			return
		}
		target = parsed
	}

	ctx := c.Request.Context()
	if target != cl.UserID {
		mayAdd, err := h.service.MayAssign(ctx, cl.OrgID, cl.UserID, target,
			auth.GetUserFromContext(c).HasAnyRole("owner", "admin"))
		if err != nil {
			httpx.Error(c, err, "failed to add watcher")
			return
		}
		if !mayAdd {
			httpx.Error(c, apperrors.New(apperrors.CodeForbidden,
				"you can only add yourself or somebody on a team you manage"), "access denied")
			return
		}
	}

	if err := h.service.AddWatcher(ctx, cl.OrgID, id, target); err != nil {
		httpx.Error(c, err, "failed to add watcher")
		return
	}
	h.service.NotifyAddedToTask(ctx, cl.OrgID, id, target, cl.UserID)
	c.JSON(http.StatusOK, gin.H{"message": "watching"})
}

func (h *Handler) RemoveWatcher(c *gin.Context) {
	cl, ok := callerFrom(c)
	if !ok {
		return
	}
	id, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "userId")
	if !ok {
		return
	}
	// Symmetric with adding: you can always take yourself off, and you can take
	// off anybody you could have put on. Leaving this self-service only meant
	// somebody could be added to a task and then nobody but themselves could
	// undo it.
	ctx := c.Request.Context()
	if userID != cl.UserID {
		mayRemove, err := h.service.MayAssign(ctx, cl.OrgID, cl.UserID, userID,
			auth.GetUserFromContext(c).HasAnyRole("owner", "admin"))
		if err != nil {
			httpx.Error(c, err, "failed to remove watcher")
			return
		}
		if !mayRemove {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "you can only take yourself off, or somebody on a team you manage",
			})
			return
		}
	}
	if err := h.service.RemoveWatcher(ctx, cl.OrgID, id, userID); err != nil {
		httpx.Error(c, err, "failed to remove watcher")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "no longer watching"})
}

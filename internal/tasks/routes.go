package tasks

import (
	"net/http"
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

	rg.GET("/tasks", h.ListTasks)
	rg.POST("/tasks", h.CreateTask)
	rg.GET("/tasks/:id", h.GetTask)
	rg.PATCH("/tasks/:id", h.UpdateTask)
	rg.DELETE("/tasks/:id", h.DeleteTask)
	rg.GET("/tasks/:id/comments", h.ListComments)
	rg.POST("/tasks/:id/comments", h.AddComment)
	rg.POST("/tasks/:id/labels", h.AddLabel)
	rg.DELETE("/tasks/:id/labels/:labelId", h.RemoveLabel)
	rg.GET("/tasks/:id/activities", h.GetActivities)
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
	tasks, err := h.service.ListTasks(c.Request.Context(), cl.OrgID, Filters{
		Status:     c.Query("status"),
		AssigneeID: queryUUID(c, "assigneeId"),
		ProjectID:  queryUUID(c, "projectId"),
		LabelID:    queryUUID(c, "labelId"),
		Search:     c.Query("search"),
	})
	if err != nil {
		httpx.Error(c, err, "failed to list tasks")
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
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
		projectID, assigneeID, dueDate, startDate, req.Labels)
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
	task, err := h.service.GetTask(c.Request.Context(), cl.OrgID, id)
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
	task, err := h.service.UpdateTask(c.Request.Context(), cl.OrgID, id, cl.UserID, req)
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
	if err := h.service.DeleteTask(c.Request.Context(), cl.OrgID, id); err != nil {
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
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	comment, err := h.service.AddComment(c.Request.Context(), cl.OrgID, id, cl.UserID, req.Content)
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

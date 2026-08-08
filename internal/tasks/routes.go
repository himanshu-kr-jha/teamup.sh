package tasks

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

	rg.GET("/tasks", h.ListTasks)
	rg.POST("/tasks", h.CreateTask)
	rg.GET("/tasks/:id", h.GetTask)
	rg.PATCH("/tasks/:id", h.UpdateTask)
	rg.DELETE("/tasks/:id", h.DeleteTask)
	rg.POST("/tasks/:id/comments", h.AddComment)
	rg.POST("/tasks/:id/labels", h.AddLabel)
	rg.DELETE("/tasks/:id/labels/:labelId", h.RemoveLabel)
	rg.GET("/tasks/:id/activities", h.GetActivities)
}

func (h *Handler) ListTasks(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)

	filters := map[string]interface{}{
		"projectId":  c.Query("projectId"),
		"assigneeId": c.Query("assigneeId"),
		"status":     c.Query("status"),
		"labelIds":   c.QueryArray("labelIds"),
	}

	tasks, err := h.service.ListTasks(c.Request.Context(), orgID, filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list tasks"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

func (h *Handler) CreateTask(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	userID, _ := uuid.Parse(user.UserID)

	var req struct {
		Title       string   `json:"title" binding:"required"`
		Description string   `json:"description"`
		ProjectID   *string  `json:"projectId"`
		AssigneeID  *string  `json:"assigneeId"`
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

	var projectID, assigneeID *uuid.UUID
	if req.ProjectID != nil {
		id, _ := uuid.Parse(*req.ProjectID)
		projectID = &id
	}
	if req.AssigneeID != nil {
		id, _ := uuid.Parse(*req.AssigneeID)
		assigneeID = &id
	}

	var dueDate, startDate *time.Time
	if req.DueDate != nil {
		t, _ := time.Parse(time.RFC3339, *req.DueDate)
		dueDate = &t
	}
	if req.StartDate != nil {
		t, _ := time.Parse(time.RFC3339, *req.StartDate)
		startDate = &t
	}

	task, err := h.service.CreateTask(c.Request.Context(), orgID, userID, req.Title, req.Description, "todo", req.Priority, req.TaskType, projectID, assigneeID, dueDate, startDate, req.Labels)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create task"})
		return
	}

	c.JSON(http.StatusCreated, task)
}

func (h *Handler) GetTask(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))
	task, err := h.service.GetTask(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, task)
}

func (h *Handler) UpdateTask(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))

	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.UpdateTask(c.Request.Context(), id, req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update task"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "task updated"})
}

func (h *Handler) DeleteTask(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))
	if err := h.service.DeleteTask(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete task"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "task deleted"})
}

func (h *Handler) AddComment(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))
	user := auth.GetUserFromContext(c)
	userID, _ := uuid.Parse(user.UserID)

	var req struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	comment, err := h.service.AddComment(c.Request.Context(), id, userID, req.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add comment"})
		return
	}
	c.JSON(http.StatusCreated, comment)
}

func (h *Handler) AddLabel(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))
	labelID, _ := uuid.Parse(c.Param("labelId"))

	if err := h.service.AddLabel(c.Request.Context(), id, labelID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add label"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "label added"})
}

func (h *Handler) RemoveLabel(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))
	labelID, _ := uuid.Parse(c.Param("labelId"))

	if err := h.service.RemoveLabel(c.Request.Context(), id, labelID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove label"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "label removed"})
}

func (h *Handler) GetActivities(c *gin.Context) {
	id, _ := uuid.Parse(c.Param("id"))
	activities, err := h.service.GetActivities(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get activities"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"activities": activities})
}

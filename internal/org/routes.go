package org

import (
	"net/http"

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

	rg.GET("/org", h.GetOrganization)
	rg.PATCH("/org", h.UpdateOrganization)
	rg.GET("/users", h.ListUsers)
	rg.GET("/users/:id", h.GetUser)
	rg.PATCH("/users/:id", h.UpdateUser)
	rg.DELETE("/users/:id", h.DeleteUser)
	rg.GET("/roles", h.ListRoles)
	rg.POST("/roles", h.CreateRole)
	rg.PATCH("/roles/:id", h.UpdateRole)
	rg.DELETE("/roles/:id", h.DeleteRole)
	rg.POST("/users/:id/roles", h.AssignRole)
	rg.DELETE("/users/:id/roles/:roleId", h.RemoveRole)
	rg.GET("/teams", h.ListTeams)
	rg.POST("/teams", h.CreateTeam)
	rg.PATCH("/teams/:id", h.UpdateTeam)
	rg.DELETE("/teams/:id", h.DeleteTeam)
	rg.POST("/teams/:id/members", h.AddTeamMember)
	rg.DELETE("/teams/:id/members/:userId", h.RemoveTeamMember)
}

func (h *Handler) GetOrganization(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)

	org, err := h.service.GetOrganization(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get organization"})
		return
	}
	c.JSON(http.StatusOK, org)
}

func (h *Handler) UpdateOrganization(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)

	var req struct {
		Name     *string                `json:"name"`
		LogoURL  *string                `json:"logoUrl"`
		Settings map[string]interface{} `json:"settings"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_ = orgID
	// UPDATE organizations SET ...
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func (h *Handler) ListUsers(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	_ = orgID
	// SELECT * FROM users WHERE org_id = ...
	c.JSON(http.StatusOK, gin.H{"users": []interface{}{}})
}

func (h *Handler) GetUser(c *gin.Context) {
	userID, _ := uuid.Parse(c.Param("id"))
	u, err := h.service.GetUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, u)
}

func (h *Handler) UpdateUser(c *gin.Context) {
	userID, _ := uuid.Parse(c.Param("id"))
	_ = userID
	// UPDATE users SET ...
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func (h *Handler) DeleteUser(c *gin.Context) {
	userID, _ := uuid.Parse(c.Param("id"))
	_ = userID
	// UPDATE users SET deleted_at = NOW(), is_active = FALSE WHERE id = ...
	c.JSON(http.StatusOK, gin.H{"message": "deactivated"})
}

func (h *Handler) ListRoles(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	_ = orgID
	// SELECT * FROM roles WHERE org_id = ...
	c.JSON(http.StatusOK, gin.H{"roles": []interface{}{}})
}

func (h *Handler) CreateRole(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	_ = orgID

	var req struct {
		Name        string   `json:"name" binding:"required"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// INSERT INTO roles ...
	c.JSON(http.StatusCreated, gin.H{"message": "role created"})
}

func (h *Handler) UpdateRole(c *gin.Context) {
	roleID, _ := uuid.Parse(c.Param("id"))
	_ = roleID
	// UPDATE roles SET ...
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func (h *Handler) DeleteRole(c *gin.Context) {
	roleID, _ := uuid.Parse(c.Param("id"))
	_ = roleID
	// DELETE FROM roles WHERE id = ... AND is_system = FALSE
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *Handler) AssignRole(c *gin.Context) {
	userID, _ := uuid.Parse(c.Param("id"))
	roleID, _ := uuid.Parse(c.Param("roleId"))
	user := auth.GetUserFromContext(c)
	_ = user.UserID
	// assignerID, _ := uuid.Parse(user.UserID)

	if err := h.service.AssignRole(c.Request.Context(), userID, roleID, uuid.Nil); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to assign role"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role assigned"})
}

func (h *Handler) RemoveRole(c *gin.Context) {
	userID, _ := uuid.Parse(c.Param("id"))
	roleID, _ := uuid.Parse(c.Param("roleId"))
	// DELETE FROM user_roles WHERE user_id = ... AND role_id = ...
	_ = userID
	_ = roleID
	c.JSON(http.StatusOK, gin.H{"message": "role removed"})
}

func (h *Handler) ListTeams(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)
	_ = orgID
	// SELECT * FROM teams WHERE org_id = ...
	c.JSON(http.StatusOK, gin.H{"teams": []interface{}{}})
}

func (h *Handler) CreateTeam(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	orgID, _ := uuid.Parse(user.OrgID)

	var req struct {
		Name        string  `json:"name" binding:"required"`
		Description string  `json:"description"`
		ParentID    *string `json:"parentId"`
		ManagerID   *string `json:"managerId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var parentID, managerID *uuid.UUID
	if req.ParentID != nil {
		id, _ := uuid.Parse(*req.ParentID)
		parentID = &id
	}
	if req.ManagerID != nil {
		id, _ := uuid.Parse(*req.ManagerID)
		managerID = &id
	}
	_ = parentID
	_ = managerID

	team, err := h.service.CreateTeam(c.Request.Context(), orgID, req.Name, req.Description, parentID, managerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create team"})
		return
	}
	c.JSON(http.StatusCreated, team)
}

func (h *Handler) UpdateTeam(c *gin.Context) {
	teamID, _ := uuid.Parse(c.Param("id"))
	_ = teamID
	// UPDATE teams SET ...
	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func (h *Handler) DeleteTeam(c *gin.Context) {
	teamID, _ := uuid.Parse(c.Param("id"))
	_ = teamID
	// UPDATE teams SET deleted_at = NOW() WHERE id = ...
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *Handler) AddTeamMember(c *gin.Context) {
	teamID, _ := uuid.Parse(c.Param("id"))
	userID, _ := uuid.Parse(c.Param("userId"))
	if err := h.service.AddTeamMember(c.Request.Context(), teamID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "member added"})
}

func (h *Handler) RemoveTeamMember(c *gin.Context) {
	teamID, _ := uuid.Parse(c.Param("id"))
	userID, _ := uuid.Parse(c.Param("userId"))
	// DELETE FROM team_members WHERE team_id = ... AND user_id = ...
	_ = teamID
	_ = userID
	c.JSON(http.StatusOK, gin.H{"message": "member removed"})
}

package org

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/auth"
	"github.com/teamup/teamup/internal/httpx"
	"github.com/teamup/teamup/internal/mail"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Mailer posts a message on the organization's behalf. An interface so the
// handler can be built without one, which is what an installation with no mail
// server amounts to.
type Mailer interface {
	Send(ctx context.Context, orgID uuid.UUID, msg mail.Message) error
	WelcomeMessage(orgName, name, email, temporaryPassword string) mail.Message
	Configured(ctx context.Context, orgID uuid.UUID) bool
}

type Handler struct {
	service *Service
	mailer  Mailer
	logger  zerolog.Logger
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// SetMailer wires outgoing mail. Without it nothing is posted and the
// administrator passes credentials on themselves.
func (h *Handler) SetMailer(m Mailer) { h.mailer = m }

// SetLogger wires real logging for the paths that only warn.
func (h *Handler) SetLogger(l zerolog.Logger) { h.logger = l }

func RegisterRoutes(rg *gin.RouterGroup, service *Service, options ...func(*Handler)) {
	h := NewHandler(service)
	for _, option := range options {
		option(h)
	}

	rg.GET("/org", h.GetOrganization)
	rg.PATCH("/org", h.requireAdmin, h.UpdateOrganization)

	rg.GET("/users", h.ListUsers)
	rg.POST("/users", h.requireAdmin, h.AddMember)
	rg.POST("/users/import", h.requireAdmin, h.ImportMembers)
	rg.GET("/users/:id", h.GetUser)
	rg.PATCH("/users/:id", h.UpdateUser)
	rg.DELETE("/users/:id", h.requireAdmin, h.DeleteUser)

	rg.GET("/roles", h.ListRoles)
	rg.POST("/roles", h.requireAdmin, h.CreateRole)
	rg.PATCH("/roles/:id", h.requireAdmin, h.UpdateRole)
	rg.DELETE("/roles/:id", h.requireAdmin, h.DeleteRole)
	rg.POST("/users/:id/roles", h.requireAdmin, h.AssignRole)
	rg.DELETE("/users/:id/roles/:roleId", h.requireAdmin, h.RemoveRole)

	rg.GET("/teams", h.ListTeams)
	rg.POST("/teams", h.requireAdmin, h.CreateTeam)
	rg.PATCH("/teams/:id", h.requireAdmin, h.UpdateTeam)
	rg.DELETE("/teams/:id", h.requireAdmin, h.DeleteTeam)
	rg.GET("/teams/:id/members", h.ListTeamMembers)
	// Deciding who is on a team belongs to whoever runs it, as well as to the
	// organization's administrators.
	rg.POST("/teams/:id/members", h.requireTeamManager, h.AddTeamMember)
	rg.DELETE("/teams/:id/members/:userId", h.requireTeamManager, h.RemoveTeamMember)
}

// requireAdmin gates the mutations that change who can do what. Without it any
// member could grant themselves the owner role.
// requireTeamManager admits the organization's administrators and the person
// who runs this particular team. A manager needs to be able to put somebody on
// their own team without being given the run of the organization.
func (h *Handler) requireTeamManager(c *gin.Context) {
	if auth.GetUserFromContext(c).HasAnyRole("owner", "admin") {
		return
	}
	orgID, ok := callerOrg(c)
	if !ok {
		c.Abort()
		return
	}
	teamID, ok := pathUUID(c, "id")
	if !ok {
		c.Abort()
		return
	}
	userID, err := uuid.Parse(auth.GetUserFromContext(c).UserID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	manages, err := h.service.ManagesTeam(c.Request.Context(), orgID, teamID, userID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to check team access"})
		return
	}
	if !manages {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "you do not manage this team",
		})
		return
	}
}

func (h *Handler) requireAdmin(c *gin.Context) {
	user := auth.GetUserFromContext(c)
	if !user.HasAnyRole("owner", "admin") {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "administrator access required"})
		return
	}
	c.Next()
}

// callerOrg resolves the organization every request is scoped to.
func callerOrg(c *gin.Context) (uuid.UUID, bool) {
	user := auth.GetUserFromContext(c)
	orgID, err := uuid.Parse(user.OrgID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return uuid.Nil, false
	}
	return orgID, true
}

func pathUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid " + param})
		return uuid.Nil, false
	}
	return id, true
}

// ── Organization ──────────────────────────────────────────────────────────

func (h *Handler) GetOrganization(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	org, err := h.service.GetOrganization(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to load organization")
		return
	}
	c.JSON(http.StatusOK, org)
}

func (h *Handler) UpdateOrganization(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	var req struct {
		Name     *string                `json:"name"`
		LogoURL  *string                `json:"logoUrl"`
		Settings map[string]interface{} `json:"settings"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	org, err := h.service.UpdateOrganization(c.Request.Context(), orgID, req.Name, req.LogoURL, req.Settings)
	if err != nil {
		httpx.Error(c, err, "failed to update organization")
		return
	}
	c.JSON(http.StatusOK, org)
}

// ── Users ─────────────────────────────────────────────────────────────────

func (h *Handler) ListUsers(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	users, err := h.service.ListUsers(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to list users")
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// AddMember creates an account for a colleague and returns the generated
// password once. There was previously no way to add anybody at all.
func (h *Handler) AddMember(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	var req struct {
		Name  string `json:"name" binding:"required"`
		Email string `json:"email" binding:"required,email"`
		Role  string `json:"role" binding:"omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	member, temporaryPassword, err := h.service.AddMember(ctx, orgID, req.Name, req.Email, req.Role)
	if err != nil {
		httpx.Error(c, err, "failed to add member")
		return
	}

	// Post them their details. Best effort and reported back either way: an
	// installation with no mail server is a supported setup, and the password
	// is on screen regardless.
	emailed := h.sendWelcome(ctx, orgID, member, temporaryPassword)

	// The only time this password is ever visible: it is stored hashed and
	// cannot be recovered.
	c.JSON(http.StatusCreated, gin.H{
		"user":              member,
		"temporaryPassword": temporaryPassword,
		"emailed":           emailed,
	})
}

// maxImportBytes bounds what will be read off the wire. maxImportRows already
// caps the people; this caps the file, so a malformed upload cannot be
// streamed into memory forever. 200 rows of name, email, role and team is a
// few tens of kilobytes, so a megabyte is generous.
const maxImportBytes = 1 << 20

// ImportMembers creates accounts for a whole CSV at once and reports what
// happened to each row. Adding fifty colleagues by hand meant repeating the
// add-member dialog forty-nine times and copying forty-nine passwords out of
// it one at a time.
//
// A row that fails is reported in the response, not raised as an error: the
// batch is deliberately not all-or-nothing, because one mistyped address must
// not cost the other forty-nine accounts.
func (h *Handler) ImportMembers(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}

	var file string
	// A CSV posted as a file body, which is what curl and a spreadsheet export
	// produce, and the same thing wrapped in JSON, which is what the browser
	// sends after reading the file.
	if strings.HasPrefix(c.ContentType(), "text/csv") {
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxImportBytes))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "could not read the uploaded file"})
			return
		}
		file = string(raw)
	} else {
		var req struct {
			CSV string `json:"csv" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if len(req.CSV) > maxImportBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "that file is too large"})
			return
		}
		file = req.CSV
	}

	outcomes, err := h.service.ImportMembers(c.Request.Context(), orgID, file)
	if err != nil {
		httpx.Error(c, err, "failed to import members")
		return
	}

	created := 0
	for _, o := range outcomes {
		if o.Status == ImportCreated {
			created++
		}
	}

	// The passwords are in here and nowhere else: they are stored hashed and
	// cannot be recovered, so this response is the administrator's only copy.
	c.JSON(http.StatusOK, gin.H{
		"outcomes": outcomes,
		"created":  created,
		"failed":   len(outcomes) - created,
	})
}

// sendWelcome posts a new member their sign-in details, and says whether it
// managed to. Never fails the request: the account exists either way, and the
// administrator can pass the password on by hand.
func (h *Handler) sendWelcome(ctx context.Context, orgID uuid.UUID, member *User, temporaryPassword string) bool {
	if h.mailer == nil {
		return false
	}
	organization, err := h.service.GetOrganization(ctx, orgID)
	if err != nil {
		h.logger.Warn().Err(err).Msg("could not load the organization to write a welcome email")
		return false
	}
	msg := h.mailer.WelcomeMessage(organization.Name, member.Name, member.Email, temporaryPassword)
	if err := h.mailer.Send(ctx, orgID, msg); err != nil {
		if errors.Is(err, mail.ErrNotConfigured) {
			h.logger.Info().Msg("no mail server configured; the new member's password was not posted")
		} else {
			h.logger.Warn().Err(err).Msg("could not post the new member's password")
		}
		return false
	}
	return true
}

func (h *Handler) GetUser(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	u, err := h.service.GetUser(c.Request.Context(), userID)
	// Scoped deliberately: a user from another organization is "not found"
	// rather than forbidden, so ids cannot be probed.
	if err != nil || u.OrgID != orgID {
		httpx.Error(c, apperrors.ErrNotFound, "user not found")
		return
	}
	c.JSON(http.StatusOK, u)
}

func (h *Handler) UpdateUser(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "id")
	if !ok {
		return
	}

	caller := auth.GetUserFromContext(c)
	if caller.UserID != userID.String() && !caller.HasAnyRole("owner", "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "you may only edit your own profile"})
		return
	}

	var req struct {
		Name      *string `json:"name"`
		AvatarURL *string `json:"avatarUrl"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	u, err := h.service.UpdateUser(c.Request.Context(), orgID, userID, req.Name, req.AvatarURL)
	if err != nil {
		httpx.Error(c, err, "failed to update user")
		return
	}
	c.JSON(http.StatusOK, u)
}

func (h *Handler) DeleteUser(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	caller := auth.GetUserFromContext(c)
	if caller.UserID == userID.String() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "you cannot deactivate your own account"})
		return
	}
	if err := h.service.DeactivateUser(c.Request.Context(), orgID, userID); err != nil {
		httpx.Error(c, err, "failed to deactivate user")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deactivated"})
}

// ── Roles ─────────────────────────────────────────────────────────────────

func (h *Handler) ListRoles(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	roles, err := h.service.ListRoles(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to list roles")
		return
	}
	c.JSON(http.StatusOK, gin.H{"roles": roles})
}

func (h *Handler) CreateRole(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	var req struct {
		Name        string   `json:"name" binding:"required"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	role, err := h.service.CreateRole(c.Request.Context(), orgID, req.Name, req.Description, req.Permissions)
	if err != nil {
		httpx.Error(c, err, "failed to create role")
		return
	}
	c.JSON(http.StatusCreated, role)
}

func (h *Handler) UpdateRole(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	roleID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Name        *string  `json:"name"`
		Description *string  `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	role, err := h.service.UpdateRole(c.Request.Context(), orgID, roleID, req.Name, req.Description, req.Permissions)
	if err != nil {
		httpx.Error(c, err, "failed to update role")
		return
	}
	c.JSON(http.StatusOK, role)
}

func (h *Handler) DeleteRole(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	roleID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteRole(c.Request.Context(), orgID, roleID); err != nil {
		httpx.Error(c, err, "failed to delete role")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *Handler) AssignRole(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		RoleID string `json:"roleId" binding:"required,uuid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	roleID, err := uuid.Parse(req.RoleID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid roleId"})
		return
	}

	caller := auth.GetUserFromContext(c)
	assigner, _ := uuid.Parse(caller.UserID)

	if err := h.service.AssignRole(c.Request.Context(), orgID, userID, roleID, assigner); err != nil {
		httpx.Error(c, err, "failed to assign role")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role assigned"})
}

func (h *Handler) RemoveRole(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	roleID, ok := pathUUID(c, "roleId")
	if !ok {
		return
	}
	if err := h.service.RemoveRole(c.Request.Context(), orgID, userID, roleID); err != nil {
		httpx.Error(c, err, "failed to remove role")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role removed"})
}

// ── Teams ─────────────────────────────────────────────────────────────────

func (h *Handler) ListTeams(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	teams, err := h.service.ListTeams(c.Request.Context(), orgID)
	if err != nil {
		httpx.Error(c, err, "failed to list teams")
		return
	}
	c.JSON(http.StatusOK, gin.H{"teams": teams})
}

func (h *Handler) CreateTeam(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
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

	parentID, err := optionalUUID(req.ParentID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid parentId"})
		return
	}
	managerID, err := optionalUUID(req.ManagerID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid managerId"})
		return
	}

	team, err := h.service.CreateTeam(c.Request.Context(), orgID, req.Name, req.Description, parentID, managerID)
	if err != nil {
		httpx.Error(c, err, "failed to create team")
		return
	}
	c.JSON(http.StatusCreated, team)
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

func (h *Handler) UpdateTeam(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	teamID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		ManagerID   *string `json:"managerId"`
		ParentID    *string `json:"parentId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	managerID, err := optionalUUID(req.ManagerID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid managerId"})
		return
	}
	// Distinguish "parentId was not mentioned" from "parentId was cleared":
	// only the second should move the team to the top level.
	var parentRef **uuid.UUID
	if req.ParentID != nil {
		parentID, err := optionalUUID(req.ParentID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid parentId"})
			return
		}
		parentRef = &parentID
	}

	team, err := h.service.UpdateTeam(c.Request.Context(), orgID, teamID, req.Name, req.Description, managerID, parentRef)
	if err != nil {
		httpx.Error(c, err, "failed to update team")
		return
	}
	c.JSON(http.StatusOK, team)
}

func (h *Handler) DeleteTeam(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	teamID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteTeam(c.Request.Context(), orgID, teamID); err != nil {
		httpx.Error(c, err, "failed to delete team")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *Handler) ListTeamMembers(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	teamID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	members, err := h.service.ListTeamMembers(c.Request.Context(), orgID, teamID)
	if err != nil {
		httpx.Error(c, err, "failed to list team members")
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": members})
}

func (h *Handler) AddTeamMember(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	teamID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"userId" binding:"required,uuid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid userId"})
		return
	}
	if err := h.service.AddTeamMember(c.Request.Context(), orgID, teamID, userID); err != nil {
		httpx.Error(c, err, "failed to add member")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "member added"})
}

func (h *Handler) RemoveTeamMember(c *gin.Context) {
	orgID, ok := callerOrg(c)
	if !ok {
		return
	}
	teamID, ok := pathUUID(c, "id")
	if !ok {
		return
	}
	userID, ok := pathUUID(c, "userId")
	if !ok {
		return
	}
	if err := h.service.RemoveTeamMember(c.Request.Context(), orgID, teamID, userID); err != nil {
		httpx.Error(c, err, "failed to remove member")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "member removed"})
}

package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/httpx"
	"github.com/teamup/teamup/internal/observability"
)

type Handler struct {
	service *Service
	logger  zerolog.Logger
}

func NewHandler(service *Service) *Handler {
	return &Handler{
		service: service,
		logger:  observability.Logger(context.Background()).With().Str("component", "auth").Logger(),
	}
}

func RegisterRoutes(rg *gin.RouterGroup, service *Service) {
	h := NewHandler(service)

	// Signing in through an external provider (/login/:provider,
	// /callback/:provider, /link/:provider and /backchannel-logout) is not
	// registered, and its handlers are gone, because they were stubs: the
	// callback made up a user with random ids and gave it a real session, its
	// state check passed when both sides were empty, and the back-channel
	// logout trusted a token whose signature it never checked. They come back
	// only with real account linking, state and PKCE checks, and a verified
	// logout token. Providers and the linked_identities storage stay for that.
	rg.POST("/login", h.LoginWithPassword)
	rg.POST("/refresh", h.RefreshToken)
	rg.POST("/logout", h.Logout)

	// Protected routes (require auth)
	protected := rg.Group("/")
	protected.Use(service.AuthMiddleware())
	{
		protected.GET("/me", h.GetCurrentUser)
		protected.GET("/sessions", h.ListSessions)
		protected.POST("/password", h.ChangePassword)
		protected.POST("/tour", h.SetTour)
		protected.POST("/whats-new", h.SetWhatsNew)
		protected.DELETE("/sessions/:sessionId", h.RevokeSession)
	}
}

func (h *Handler) LoginWithPassword(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	if h.service.tooManyFailures(ctx, req.Email) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many wrong passwords for this account; try again in 15 minutes"})
		return
	}
	user, err := h.service.GetUserByEmail(ctx, req.Email)
	if err != nil {
		// As long as a wrong password takes, so the answer's timing does not
		// say whether there is an account at this address.
		spendCompareTime(req.Password)
		h.service.recordFailure(ctx, req.Email)
		h.logger.Info().Msg("sign-in refused: no such account")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	// Verify password
	if err := h.service.VerifyPassword(user, req.Password); err != nil {
		h.service.recordFailure(ctx, req.Email)
		h.logger.Info().Str("user_id", user.ID.String()).Msg("sign-in refused: wrong password")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	h.service.clearFailures(ctx, req.Email)

	// Generate token pair
	sessionID := uuid.New().String()
	accessToken, err := h.service.GenerateAccessToken(c.Request.Context(), user, sessionID)
	if err != nil {
		h.logger.Error().Err(err).Msg("Access token generation failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	refreshToken, err := h.service.generateAndStoreRefreshToken(c.Request.Context(), user, sessionID, c.Request.UserAgent(), c.ClientIP())
	if err != nil {
		h.logger.Error().Err(err).Msg("Refresh token generation failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	// Set cookies
	h.service.SetAuthCookies(c.Writer, &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(h.service.cfg.AccessTokenTTL.Seconds()),
		TokenType:    "Bearer",
	})

	perms, err := h.service.PermissionsOf(c.Request.Context(), user.ID)
	if err != nil {
		perms = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"user": user, "permissions": perms})
}

func (h *Handler) RefreshToken(c *gin.Context) {
	refreshToken, err := c.Cookie("rt")
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing refresh token"})
		return
	}

	tokens, err := h.service.RefreshAccessToken(c.Request.Context(), refreshToken, c.Request.UserAgent(), c.ClientIP())
	if err != nil {
		h.logger.Error().Err(err).Msg("Token refresh failed")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token refresh failed"})
		return
	}

	h.service.SetAuthCookies(c.Writer, tokens)
	// The tokens travel only in HttpOnly cookies, out of reach of page script;
	// repeating them here would hand them to any script that runs on the page.
	c.JSON(http.StatusOK, gin.H{"expiresIn": tokens.ExpiresIn})
}

// Logout is public on purpose: somebody whose access token has expired can still
// log out, so the session is found from the tokens on the request.
func (h *Handler) Logout(c *gin.Context) {
	refreshToken, _ := c.Cookie("rt")
	err := h.service.EndPresentedSession(c.Request.Context(), refreshToken, accessTokenFrom(c))
	h.service.ClearAuthCookies(c.Writer)
	if err != nil {
		h.logger.Error().Err(err).Msg("Logout could not revoke the session")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to end the session"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

// GetCurrentUser answers with the same user object login does. It used to
// return a snake_case subset with no id and no name, so reloading the page
// replaced the signed-in user with a half-empty object and anything keyed on
// user.id silently stopped matching.
func (h *Handler) GetCurrentUser(c *gin.Context) {
	userCtx := GetUserFromContext(c)
	userID, err := uuid.Parse(userCtx.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	user, err := h.service.getUser(c.Request.Context(), userID)
	if err != nil {
		httpx.Error(c, err, "failed to load current user")
		return
	}
	perms, err := h.service.PermissionsOf(c.Request.Context(), userID)
	if err != nil {
		httpx.Error(c, err, "failed to load current user")
		return
	}
	// The pages offer only what the server will allow; the server still checks.
	c.JSON(http.StatusOK, gin.H{"user": user, "permissions": perms})
}

// SetTour records that the caller finished or skipped the welcome tour, or, with
// done false, asks for it again on their next visit to any page.
func (h *Handler) SetTour(c *gin.Context) {
	userID, err := uuid.Parse(GetUserFromContext(c).UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	var req struct {
		Done bool `json:"done"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	at, err := h.service.SetTourCompleted(c.Request.Context(), userID, req.Done)
	if err != nil {
		httpx.Error(c, err, "failed to save the tour")
		return
	}
	c.JSON(http.StatusOK, gin.H{"tourCompletedAt": at})
}

// SetWhatsNew records the newest "What's new" entry the caller has seen.
func (h *Handler) SetWhatsNew(c *gin.Context) {
	userID, err := uuid.Parse(GetUserFromContext(c).UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	var req struct {
		Seen string `json:"seen"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	seen, err := h.service.SetWhatsNewSeen(c.Request.Context(), userID, req.Seen)
	if err != nil {
		httpx.Error(c, err, "failed to save what you have seen")
		return
	}
	c.JSON(http.StatusOK, gin.H{"whatsNewSeen": seen})
}

func (h *Handler) ListSessions(c *gin.Context) {
	userCtx := GetUserFromContext(c)
	userID, err := uuid.Parse(userCtx.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	sessions, err := h.service.ListSessions(c.Request.Context(), userID)
	if err != nil {
		h.logger.Error().Err(err).Msg("List sessions failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list sessions"})
		return
	}

	// Marking the caller's own session lets the UI label "this device" and
	// avoid offering to revoke it.
	type sessionView struct {
		Session
		Current bool `json:"current"`
	}
	out := make([]sessionView, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, sessionView{Session: sess, Current: sess.SessionID == userCtx.SessionID})
	}
	c.JSON(http.StatusOK, gin.H{"sessions": out})
}

// ChangePassword rotates the caller's own password. Nothing could rotate it
// before, so a password set by the setup wizard was permanent.
func (h *Handler) ChangePassword(c *gin.Context) {
	userCtx := GetUserFromContext(c)
	userID, err := uuid.Parse(userCtx.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}

	var req struct {
		CurrentPassword string `json:"currentPassword" binding:"required"`
		NewPassword     string `json:"newPassword" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.ChangePassword(c.Request.Context(), userID, req.CurrentPassword, req.NewPassword); err != nil {
		httpx.Error(c, err, "failed to change password")
		return
	}

	// Changing the password revoked every session, including this one, so the
	// browser must not be left holding credentials that no longer work.
	h.service.ClearAuthCookies(c.Writer)
	// Its live connections go too, rather than carrying on until their tokens run out.
	h.service.sessionsEnded(userCtx.UserID)
	c.JSON(http.StatusOK, gin.H{"message": "password changed, please sign in again"})
}

func (h *Handler) RevokeSession(c *gin.Context) {
	sessionID := c.Param("sessionId")
	userCtx := GetUserFromContext(c)

	if sessionID == userCtx.SessionID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot revoke current session"})
		return
	}

	// Scoped to the caller: a session id must not be revocable by anyone else.
	userID, err := uuid.Parse(userCtx.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
		return
	}
	if err := h.service.RevokeUserSession(c.Request.Context(), userID, sessionID); err != nil {
		httpx.Error(c, err, "failed to revoke session")
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "session revoked"})
}

// generateAndStoreRefreshToken creates and stores a refresh token
func (s *Service) generateAndStoreRefreshToken(ctx context.Context, user *User, sessionID, ua, ip string) (string, error) {
	_, rawToken, err := s.GenerateRefreshToken(ctx, user, sessionID, ua, ip)
	if err != nil {
		return "", err
	}
	return rawToken, nil
}

// User model (minimal)
type User struct {
	ID           uuid.UUID `json:"id"`
	OrgID        uuid.UUID `json:"orgId"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	Roles        []string  `json:"roles"`
	AuthProvider string    `json:"-"`
	AvatarURL    string    `json:"avatarUrl,omitempty"`
	IsActive     bool      `json:"-"`
	// Never serialized: this is the bcrypt hash used by VerifyPassword.
	PasswordHash string `json:"-"`
	// True while the user is still on a password an administrator generated.
	MustChangePassword bool `json:"mustChangePassword"`
	// TourCompletedAt is when they finished or skipped the tour shown at their
	// first sign-in; nil while it is still to be shown.
	TourCompletedAt *time.Time `json:"tourCompletedAt"`
	// WhatsNewSeen is the newest "What's new" entry they have seen; nil
	// until they have seen one.
	WhatsNewSeen *string `json:"whatsNewSeen"`
}

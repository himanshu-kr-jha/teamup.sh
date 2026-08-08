package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

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

	// Public routes
	rg.GET("/login/:provider", h.InitiateLogin)
	rg.POST("/login", h.LoginWithPassword)
	rg.GET("/callback/:provider", h.HandleCallback)
	rg.POST("/refresh", h.RefreshToken)
	rg.POST("/logout", h.Logout)
	rg.POST("/backchannel-logout", h.BackchannelLogout)

	// Protected routes (require auth)
	protected := rg.Group("/")
	protected.Use(service.AuthMiddleware())
	{
		protected.GET("/me", h.GetCurrentUser)
		protected.POST("/link/:provider", h.LinkAccount)
		protected.DELETE("/link/:provider", h.UnlinkAccount)
		protected.GET("/sessions", h.ListSessions)
		protected.POST("/password", h.ChangePassword)
		protected.DELETE("/sessions/:sessionId", h.RevokeSession)
	}
}

func (h *Handler) InitiateLogin(c *gin.Context) {
	providerName := c.Param("provider")
	provider, ok := h.service.GetProvider(providerName)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown provider"})
		return
	}

	// Generate PKCE
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		h.logger.Error().Err(err).Msg("PKCE generation failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Store PKCE verifier in cookie
	SetPKCECookie(c.Writer, verifier)

	// Generate state
	state := uuid.New().String()
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	// Build auth URL with PKCE
	url := provider.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	c.Redirect(http.StatusFound, url)
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

	user, err := h.service.GetUserByEmail(c.Request.Context(), req.Email)
	if err != nil {
		h.logger.Error().Err(err).Str("email", req.Email).Msg("User lookup failed")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	// Verify password
	if err := h.service.VerifyPassword(user, req.Password); err != nil {
		h.logger.Error().Err(err).Str("email", req.Email).Msg("Password verification failed")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

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

	c.JSON(http.StatusOK, gin.H{"user": user})
}

func (h *Handler) HandleCallback(c *gin.Context) {
	providerName := c.Param("provider")
	provider, ok := h.service.GetProvider(providerName)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown provider"})
		return
	}

	// Validate state
	state := c.Query("state")
	stateCookie, _ := c.Cookie("oauth_state")
	if state != stateCookie {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid state"})
		return
	}

	// Exchange code for tokens
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
		return
	}

	token, err := provider.Exchange(c.Request.Context(), code)
	if err != nil {
		h.logger.Error().Err(err).Str("provider", providerName).Msg("Token exchange failed")
		c.JSON(http.StatusBadRequest, gin.H{"error": "token exchange failed"})
		return
	}

	// Verify ID token
	idTokenRaw, ok := token.Extra("id_token").(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing id_token"})
		return
	}

	// Get user info
	userInfo, err := provider.UserInfo(c.Request.Context(), token)
	if err != nil {
		h.logger.Error().Err(err).Msg("UserInfo failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user info failed"})
		return
	}

	// Find or create user
	user, err := h.findOrCreateUser(c.Request.Context(), providerName, userInfo, token, idTokenRaw)
	if err != nil {
		h.logger.Error().Err(err).Msg("User lookup/creation failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user setup failed"})
		return
	}

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

	// Redirect to app
	c.Redirect(http.StatusFound, h.service.cfg.BaseURL+"/")
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
	c.JSON(http.StatusOK, tokens)
}

func (h *Handler) Logout(c *gin.Context) {
	userCtx := GetUserFromContext(c)

	// Revoke session
	if userCtx.SessionID != "" {
		h.service.RevokeSession(c.Request.Context(), userCtx.SessionID)
	}

	// Clear cookies
	h.service.ClearAuthCookies(c.Writer)

	// RP-Initiated Logout: redirect to provider
	if userCtx.AuthProvider != "" {
		provider, ok := h.service.GetProvider(userCtx.AuthProvider)
		if ok {
			idToken := userCtx.IDToken
			logoutURL := provider.EndSessionURL(idToken, h.service.cfg.BaseURL+"/logged-out")
			c.Redirect(http.StatusFound, logoutURL)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

func (h *Handler) BackchannelLogout(c *gin.Context) {
	logoutToken := c.PostForm("logout_token")
	if logoutToken == "" {
		c.Status(http.StatusBadRequest)
		return
	}

	// Parse logout token (simplified - in production validate signature)
	var claims struct {
		Subject string `json:"sub"`
		SID     string `json:"sid"` // Session ID
	}
	if err := parseLogoutToken(logoutToken, &claims); err != nil {
		h.logger.Error().Err(err).Msg("Invalid logout token")
		c.Status(http.StatusBadRequest)
		return
	}

	// Find user by provider subject
	// In production, look up by provider + subject
	// For now, just acknowledge
	c.Status(http.StatusOK)
}

func (h *Handler) GetCurrentUser(c *gin.Context) {
	userCtx := GetUserFromContext(c)
	c.JSON(http.StatusOK, gin.H{
		"user_id":       userCtx.UserID,
		"org_id":        userCtx.OrgID,
		"email":         userCtx.Email,
		"roles":         userCtx.Roles,
		"auth_provider": userCtx.AuthProvider,
	})
}

func (h *Handler) LinkAccount(c *gin.Context) {
	providerName := c.Param("provider")
	provider, ok := h.service.GetProvider(providerName)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown provider"})
		return
	}

	userCtx := GetUserFromContext(c)

	// Initiate OAuth flow for linking (similar to login but with user context)
	// Store user ID in state for callback
	verifier, challenge, _ := GeneratePKCE()
	SetPKCECookie(c.Writer, verifier)

	state := fmt.Sprintf("link:%s:%s", userCtx.UserID, uuid.New().String())
	http.SetCookie(c.Writer, &http.Cookie{
		Name: "oauth_state", Value: state, Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})

	url := provider.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	c.JSON(http.StatusOK, gin.H{"auth_url": url})
}

func (h *Handler) UnlinkAccount(c *gin.Context) {
	providerName := c.Param("provider")
	userCtx := GetUserFromContext(c)
	userID, _ := uuid.Parse(userCtx.UserID)

	// Check if user has other auth methods
	// In production, count linked identities
	// For now, allow unlinking
	if err := h.service.deleteLinkedIdentity(c.Request.Context(), userID, providerName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlink"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "account unlinked"})
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

func (h *Handler) findOrCreateUser(ctx context.Context, providerName string, userInfo *UserInfo, token *oauth2.Token, idToken string) (*User, error) {
	// In production:
	// 1. Check linked_identities for provider + subject
	// 2. If found, update tokens and return user
	// 3. If not found, check if email exists in users table
	// 4. If email exists, link identity
	// 4. If not, create new user with org (if first user, create org)

	// For now, return mock user
	return &User{
		ID:           uuid.New(),
		OrgID:        uuid.New(),
		Email:        userInfo.Email,
		Name:         userInfo.Name,
		Roles:        []string{"member"},
		AuthProvider: providerName,
	}, nil
}

// generateAndStoreRefreshToken creates and stores a refresh token
func (s *Service) generateAndStoreRefreshToken(ctx context.Context, user *User, sessionID, ua, ip string) (string, error) {
	_, rawToken, err := s.GenerateRefreshToken(ctx, user, sessionID, ua, ip)
	if err != nil {
		return "", err
	}
	return rawToken, nil
}

func parseLogoutToken(tokenString string, claims interface{}) error {
	// In production, validate JWT signature
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid token format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}

	return json.Unmarshal(payload, claims)
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
}

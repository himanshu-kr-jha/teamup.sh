package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"

	"github.com/teamup/teamup/internal/websocket"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

type Config struct {
	Issuer          string
	Audience        string
	BaseURL         string
	GoogleClientID  string
	GoogleSecret    string
	EntraClientID   string
	EntraSecret     string
	EntraTenantID   string
	CustomIssuer    string
	CustomClientID  string
	CustomSecret    string
	PrivateKey      *rsa.PrivateKey
	PublicKey       *rsa.PublicKey
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

type Service struct {
	db        *pgxpool.Pool
	redis     *redis.Client
	cfg       Config
	providers map[string]Provider
	verifier  *oidc.IDTokenVerifier
}

type Provider interface {
	Name() string
	AuthCodeURL(state string, opts ...oauth2.AuthCodeOption) string
	Exchange(ctx context.Context, code string) (*oauth2.Token, error)
	VerifyIDToken(ctx context.Context, rawIDToken string) (*oidc.IDToken, error)
	UserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error)
	EndSessionURL(idTokenHint, postLogoutRedirectURI string) string
	SupportsPKCE() bool
}

type UserInfo struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	GivenName     string
	FamilyName    string
	Picture       string
	Groups        []string
	Claims        map[string]any
}

type Claims struct {
	jwt.RegisteredClaims
	UserID       string   `json:"user_id"`
	OrgID        string   `json:"org_id"`
	Email        string   `json:"email"`
	Roles        []string `json:"roles"`
	SessionID    string   `json:"sid"`
	AuthProvider string   `json:"auth_provider,omitempty"`
	IDToken      string   `json:"id_token,omitempty"`
}

type RefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	SessionID string
	TokenHash string
	UserAgent string
	IPAddress string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

type LinkedIdentity struct {
	UserID       uuid.UUID
	Provider     string
	ProviderID   string
	Email        string
	AccessToken  string
	RefreshToken string
	IDToken      string
	UpdatedAt    time.Time
}

func NewService(db *pgxpool.Pool, redis *redis.Client, cfg Config) *Service {
	// Generate RSA keys if not provided
	if cfg.PrivateKey == nil {
		privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
		cfg.PrivateKey = privateKey
		cfg.PublicKey = &privateKey.PublicKey
	}

	s := &Service{
		db:        db,
		redis:     redis,
		cfg:       cfg,
		providers: make(map[string]Provider),
	}

	// Register providers
	if cfg.GoogleClientID != "" {
		s.providers["google"] = NewGoogleProvider(cfg)
	}
	if cfg.EntraClientID != "" {
		s.providers["entra"] = NewEntraProvider(cfg)
	}
	if cfg.CustomIssuer != "" {
		s.providers["custom"] = NewCustomProvider(cfg)
	}

	// Initialize OIDC verifier for custom provider
	if cfg.CustomIssuer != "" {
		ctx := context.Background()
		provider, err := oidc.NewProvider(ctx, cfg.CustomIssuer)
		if err == nil {
			s.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.CustomClientID})
		}
	}

	return s
}

func (s *Service) GetProvider(name string) (Provider, bool) {
	p, ok := s.providers[name]
	return p, ok
}

// GenerateAccessToken creates a short-lived JWT access token
func (s *Service) GenerateAccessToken(ctx context.Context, user *User, sessionID string) (string, error) {
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    s.cfg.Issuer,
			Audience:  []string{s.cfg.Audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.cfg.AccessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
		UserID:       user.ID.String(),
		OrgID:        user.OrgID.String(),
		Email:        user.Email,
		Roles:        user.Roles,
		SessionID:    sessionID,
		AuthProvider: user.AuthProvider,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(s.cfg.PrivateKey)
}

// GenerateRefreshToken creates a long-lived refresh token
func (s *Service) GenerateRefreshToken(ctx context.Context, user *User, ua, ip string) (*RefreshToken, string, error) {
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return nil, "", err
	}
	tokenString := base64.RawURLEncoding.EncodeToString(rawToken)

	hash, err := bcrypt.GenerateFromPassword([]byte(tokenString), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", err
	}

	sessionID := uuid.New().String()
	rt := &RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		SessionID: sessionID,
		TokenHash: string(hash),
		UserAgent: ua,
		IPAddress: ip,
		ExpiresAt: time.Now().Add(s.cfg.RefreshTokenTTL),
		CreatedAt: time.Now(),
	}

	if err := s.storeRefreshToken(ctx, rt); err != nil {
		return nil, "", err
	}

	return rt, tokenString, nil
}

// RefreshAccessToken rotates the refresh token and issues new access token
func (s *Service) RefreshAccessToken(ctx context.Context, refreshToken, ua, ip string) (*TokenPair, error) {
	rt, err := s.findRefreshTokenByHash(ctx, refreshToken)
	if err != nil || rt.RevokedAt != nil || time.Now().After(rt.ExpiresAt) {
		return nil, apperrors.ErrUnauthorized
	}

	// Revoke old token (rotation)
	now := time.Now()
	rt.RevokedAt = &now
	if err := s.updateRefreshToken(ctx, rt); err != nil {
		return nil, err
	}

	// Get user
	user, err := s.getUser(ctx, rt.UserID)
	if err != nil {
		return nil, err
	}

	// Generate new token pair
	newRT, newRawToken, err := s.GenerateRefreshToken(ctx, user, ua, ip)
	if err != nil {
		return nil, err
	}

	accessToken, err := s.GenerateAccessToken(ctx, user, newRT.SessionID)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: newRawToken,
		ExpiresIn:    int(s.cfg.AccessTokenTTL.Seconds()),
		TokenType:    "Bearer",
	}, nil
}

// ValidateAccessToken validates a JWT access token
func (s *Service) ValidateAccessToken(ctx context.Context, tokenString string) (*websocket.Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.cfg.PublicKey, nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, apperrors.New(apperrors.CodeUnauthorized, "invalid token")
	}

	// Check if session is revoked
	revoked, err := s.isSessionRevoked(ctx, claims.SessionID)
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, apperrors.New(apperrors.CodeUnauthorized, "session revoked")
	}

	// Convert to websocket.Claims
	return &websocket.Claims{
		UserID:       claims.UserID,
		OrgID:        claims.OrgID,
		Email:        claims.Email,
		Roles:        claims.Roles,
		SessionID:    claims.SessionID,
		AuthProvider: claims.AuthProvider,
		IDToken:      claims.IDToken,
	}, nil
}

// RevokeSession revokes a specific session
func (s *Service) RevokeSession(ctx context.Context, sessionID string) error {
	return s.revokeSessionTokens(ctx, sessionID)
}

// RevokeAllUserTokens revokes all sessions for a user
func (s *Service) RevokeAllUserTokens(ctx context.Context, userID uuid.UUID) error {
	return s.revokeAllUserTokens(ctx, userID)
}

// SetAuthCookies sets HttpOnly secure cookies for tokens
func (s *Service) SetAuthCookies(w http.ResponseWriter, tokens *TokenPair) {
	// Access token cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "at",
		Value:    tokens.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   tokens.ExpiresIn,
	})

	// Refresh token cookie (only sent to refresh endpoint)
	http.SetCookie(w, &http.Cookie{
		Name:     "rt",
		Value:    tokens.RefreshToken,
		Path:     "/api/auth/refresh",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.RefreshTokenTTL.Seconds()),
	})
}

// ClearAuthCookies clears auth cookies
func (s *Service) ClearAuthCookies(w http.ResponseWriter) {
	for _, name := range []string{"at", "rt"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
		})
	}
}

// AuthMiddleware validates access token from cookie
func (s *Service) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try cookie first
		accessToken, err := c.Cookie("at")
		if err != nil {
			// Try Authorization header
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				accessToken = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if accessToken == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		claims, err := s.ValidateAccessToken(c.Request.Context(), accessToken)
		if err != nil {
			// Try refresh
			if rt, err := c.Cookie("rt"); err == nil && rt != "" {
				if tokens, err := s.RefreshAccessToken(c.Request.Context(), rt, c.Request.UserAgent(), c.ClientIP()); err == nil {
					s.SetAuthCookies(c.Writer, tokens)
					claims, _ = s.ValidateAccessToken(c.Request.Context(), tokens.AccessToken)
				}
			}
			if claims == nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				return
			}
		}

		// Set user context
		c.Set("user_id", claims.UserID)
		c.Set("org_id", claims.OrgID)
		c.Set("user_roles", claims.Roles)
		c.Set("user_email", claims.Email)
		c.Set("session_id", claims.SessionID)
		c.Set("auth_provider", claims.AuthProvider)
		c.Set("id_token", claims.IDToken)

		c.Next()
	}
}

// GetUserFromContext extracts user info from Gin context
func GetUserFromContext(c *gin.Context) *UserContext {
	return &UserContext{
		UserID:       c.GetString("user_id"),
		OrgID:        c.GetString("org_id"),
		Roles:        c.GetStringSlice("user_roles"),
		Email:        c.GetString("user_email"),
		SessionID:    c.GetString("session_id"),
		AuthProvider: c.GetString("auth_provider"),
		IDToken:      c.GetString("id_token"),
	}
}

type UserContext struct {
	UserID       string
	OrgID        string
	Roles        []string
	Email        string
	SessionID    string
	AuthProvider string
	IDToken      string
}

func (u *UserContext) HasRole(role string) bool {
	for _, r := range u.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func (u *UserContext) HasAnyRole(roles ...string) bool {
	for _, r := range u.Roles {
		for _, allowed := range roles {
			if r == allowed {
				return true
			}
		}
	}
	return false
}

// TokenPair represents access + refresh token pair
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// PKCE helper functions
func GeneratePKCE() (codeVerifier, codeChallenge string, err error) {
	verifier := make([]byte, 32)
	if _, err := rand.Read(verifier); err != nil {
		return "", "", err
	}
	codeVerifier = base64.RawURLEncoding.EncodeToString(verifier)

	hash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge = base64.RawURLEncoding.EncodeToString(hash[:])
	return codeVerifier, codeChallenge, nil
}

func SetPKCECookie(w http.ResponseWriter, verifier string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "pkce_verifier",
		Value:    verifier,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600, // 10 minutes
	})
}

func GetAndClearPKCECookie(w http.ResponseWriter, r *http.Request) (string, error) {
	cookie, err := r.Cookie("pkce_verifier")
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "pkce_verifier",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return cookie.Value, nil
}

// Database operations (stubs - implement with actual queries)
func (s *Service) storeRefreshToken(ctx context.Context, rt *RefreshToken) error {
	// INSERT INTO refresh_tokens ...
	return nil
}

func (s *Service) findRefreshTokenByHash(ctx context.Context, tokenString string) (*RefreshToken, error) {
	// SELECT * FROM refresh_tokens WHERE token_hash = ...
	return nil, apperrors.ErrNotFound
}

func (s *Service) updateRefreshToken(ctx context.Context, rt *RefreshToken) error {
	// UPDATE refresh_tokens SET revoked_at = ... WHERE id = ...
	return nil
}

func (s *Service) revokeSessionTokens(ctx context.Context, sessionID string) error {
	// UPDATE refresh_tokens SET revoked_at = NOW() WHERE session_id = ...
	return nil
}

func (s *Service) revokeAllUserTokens(ctx context.Context, userID uuid.UUID) error {
	// UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = ...
	return nil
}

func (s *Service) isSessionRevoked(ctx context.Context, sessionID string) (bool, error) {
	// SELECT 1 FROM refresh_tokens WHERE session_id = ... AND revoked_at IS NULL
	return false, nil
}

func (s *Service) getUser(ctx context.Context, userID uuid.UUID) (*User, error) {
	// SELECT * FROM users WHERE id = ...
	return nil, apperrors.ErrNotFound
}

func (s *Service) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	// SELECT * FROM users WHERE email = ...
	return nil, apperrors.ErrNotFound
}

func (s *Service) VerifyPassword(user *User, password string) error {
	// In production, verify bcrypt hash from database
	// For now, return error since no password is stored
	return apperrors.New(apperrors.CodeUnauthorized, "password authentication not configured")
}

func (s *Service) getLinkedIdentity(ctx context.Context, provider, providerID string) (*LinkedIdentity, error) {
	// SELECT * FROM linked_identities WHERE provider = ... AND provider_id = ...
	return nil, apperrors.ErrNotFound
}

func (s *Service) upsertLinkedIdentity(ctx context.Context, li *LinkedIdentity) error {
	// INSERT ... ON CONFLICT ...
	return nil
}

func (s *Service) deleteLinkedIdentity(ctx context.Context, userID uuid.UUID, provider string) error {
	// DELETE FROM linked_identities WHERE user_id = ... AND provider = ...
	return nil
}

// GenerateVAPIDKeys generates a new VAPID key pair for Web Push
func GenerateVAPIDKeys() (publicKey, privateKey string, err error) {
	// Generate ECDSA P-256 key pair
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}

	// Encode public key (uncompressed format)
	publicKeyBytes := elliptic.Marshal(privKey.Curve, privKey.PublicKey.X, privKey.PublicKey.Y)
	publicKey = base64.RawURLEncoding.EncodeToString(publicKeyBytes)

	// Encode private key
	privateKeyBytes := privKey.D.Bytes()
	privateKey = base64.RawURLEncoding.EncodeToString(privateKeyBytes)

	return publicKey, privateKey, nil
}

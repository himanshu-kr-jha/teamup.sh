package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"

	"github.com/teamup/teamup/internal/websocket"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

type Config struct {
	Issuer         string
	Audience       string
	BaseURL        string
	GoogleClientID string
	GoogleSecret   string
	EntraClientID  string
	EntraSecret    string
	EntraTenantID  string
	CustomIssuer   string
	CustomClientID string
	CustomSecret   string
	// PrivateKey signs access tokens. Left nil — which is the normal case —
	// the service loads the deployment's stored key, or stores a new one on
	// first boot. PublicKey is always derived from it.
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
	// SigningKeySecret (TEAMUP_AUTH_SECRET) encrypts the stored signing key,
	// so a database backup does not carry the ability to mint tokens with it.
	// Optional, but once set it has to stay set: the key cannot be read back
	// without it.
	SigningKeySecret string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	// InsecureCookies drops the Secure flag from auth cookies.
	//
	// A browser will not store a Secure cookie sent over plain HTTP — except
	// on localhost, which it treats as trusted — so with this left on, signing
	// in from another machine over http://<address> fails silently: the
	// response looks fine and the cookie is simply discarded. Turning it off
	// is for testing over a local network and nowhere else.
	InsecureCookies bool
}

// cookieSecure reports whether to mark cookies Secure. Always true unless
// explicitly relaxed, so the safe setting is the one you get by default.
func (s *Service) cookieSecure() bool { return !s.cfg.InsecureCookies }

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
	// Carried in the token so enforcement costs no extra query per request.
	MustChangePassword bool `json:"must_change_password,omitempty"`
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

// NewService builds the auth service, loading the deployment's signing key
// from the database — and storing one there the first time — unless the caller
// supplied a key itself.
//
// It returns an error rather than carrying on with an improvised key: a key
// this process invented is one no other replica can verify, and the failure
// mode of getting that wrong is every request to every other pod coming back
// 401.
func NewService(db *pgxpool.Pool, redis *redis.Client, cfg Config) (*Service, error) {
	if cfg.PrivateKey == nil {
		if db == nil {
			return nil, errors.New("auth: no signing key configured and no database to load one from")
		}
		key, err := loadOrCreateSigningKey(context.Background(), db, cfg.SigningKeySecret)
		if err != nil {
			return nil, fmt.Errorf("auth: %w", err)
		}
		cfg.PrivateKey = key
	}
	// Always derived, never taken on trust, so a caller cannot hand over a
	// public key that does not belong to the private one.
	cfg.PublicKey = &cfg.PrivateKey.PublicKey

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

	return s, nil
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
		UserID:             user.ID.String(),
		OrgID:              user.OrgID.String(),
		Email:              user.Email,
		Roles:              user.Roles,
		SessionID:          sessionID,
		AuthProvider:       user.AuthProvider,
		MustChangePassword: user.MustChangePassword,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(s.cfg.PrivateKey)
}

// hashRefreshToken derives the stored lookup key for a refresh token.
//
// SHA-256 rather than bcrypt: bcrypt is salted, so the same token hashes
// differently every time and the row could never be found again — which is why
// the whole refresh path used to be unimplemented. Password stretching buys
// nothing here anyway, because the token is 256 bits of CSPRNG output rather
// than a guessable secret.
func hashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// GenerateRefreshToken creates a long-lived refresh token bound to sessionID.
// The caller passes the session id so the access token issued alongside it
// refers to the same session; otherwise access-token validation looks up a
// session that has no stored token and rejects every request.
func (s *Service) GenerateRefreshToken(ctx context.Context, user *User, sessionID, ua, ip string) (*RefreshToken, string, error) {
	rt, tokenString, err := newRefreshToken(user, sessionID, ua, ip, s.cfg.RefreshTokenTTL)
	if err != nil {
		return nil, "", err
	}
	if err := storeRefreshToken(ctx, s.db, rt); err != nil {
		return nil, "", err
	}
	return rt, tokenString, nil
}

// newRefreshToken mints a token without storing it, so rotation can write it
// inside the same transaction that retires the one being replaced.
func newRefreshToken(user *User, sessionID, ua, ip string, ttl time.Duration) (*RefreshToken, string, error) {
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return nil, "", err
	}
	tokenString := base64.RawURLEncoding.EncodeToString(rawToken)

	return &RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		SessionID: sessionID,
		TokenHash: hashRefreshToken(tokenString),
		UserAgent: ua,
		IPAddress: ip,
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(),
	}, tokenString, nil
}

// refreshRotationGrace is how long a refresh token that has just been rotated
// away still gets its holder an access token — on the session that replaced
// it, and never another refresh token.
//
// Requests arrive in bunches: a PWA refetches several queries when the window
// regains focus, and every client refreshes at once after a deploy. All of
// them present the same refresh token, one of them rotates it, and without
// this window the rest were answered with 401 and the user was thrown back to
// the sign-in screen. Wide enough to cover a burst, narrow enough that a
// stolen token is not a foothold.
const refreshRotationGrace = 30 * time.Second

// RefreshAccessToken rotates the refresh token and issues a new access token.
//
// The lookup takes a row lock, so concurrent callers presenting the same token
// are serialised: the first rotates it, and the ones behind it find the
// rotation already recorded and take the grace path rather than racing to
// rotate a token that is already spent.
func (s *Service) RefreshAccessToken(ctx context.Context, refreshToken, ua, ip string) (*TokenPair, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var (
		id         uuid.UUID
		userID     uuid.UUID
		expiresAt  time.Time
		revokedAt  *time.Time
		replacedBy *uuid.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, expires_at, revoked_at, replaced_by
		FROM refresh_tokens WHERE token_hash = $1
		FOR UPDATE
	`, hashRefreshToken(refreshToken)).Scan(&id, &userID, &expiresAt, &revokedAt, &replacedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperrors.ErrUnauthorized
		}
		return nil, err
	}
	if time.Now().After(expiresAt) {
		return nil, apperrors.ErrUnauthorized
	}

	user, err := scanUser(tx.QueryRow(ctx, fmt.Sprintf(userColumns, "u.id = $1"), userID))
	if err != nil {
		return nil, err
	}

	if revokedAt != nil {
		accessToken, err := s.accessTokenForReplacement(ctx, tx, user, replacedBy, *revokedAt)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		// No refresh token: the caller that won the rotation has already been
		// handed the replacement, and this one keeps the cookie it has until
		// that response lands.
		return &TokenPair{
			AccessToken: accessToken,
			ExpiresIn:   int(s.cfg.AccessTokenTTL.Seconds()),
			TokenType:   "Bearer",
		}, nil
	}

	// Rotation starts a new session so the previous access token stops being
	// accepted along with the refresh token it was paired with.
	newRT, newRawToken, err := newRefreshToken(user, uuid.New().String(), ua, ip, s.cfg.RefreshTokenTTL)
	if err != nil {
		return nil, err
	}
	if err := storeRefreshToken(ctx, tx, newRT); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = NOW(), replaced_by = $2 WHERE id = $1
	`, id, newRT.ID); err != nil {
		return nil, err
	}

	accessToken, err := s.GenerateAccessToken(ctx, user, newRT.SessionID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: newRawToken,
		ExpiresIn:    int(s.cfg.AccessTokenTTL.Seconds()),
		TokenType:    "Bearer",
	}, nil
}

// accessTokenForReplacement serves the caller that lost a rotation race: it
// hands back an access token on the session that replaced the one it
// presented. Anything else — a token revoked by signing out, one rotated away
// longer ago than the grace window, or a replacement that has itself been
// revoked — is refused, so this stays a fix for concurrency and not a way to
// keep using a token that was deliberately withdrawn.
func (s *Service) accessTokenForReplacement(ctx context.Context, tx pgx.Tx, user *User, replacedBy *uuid.UUID, revokedAt time.Time) (string, error) {
	if replacedBy == nil || time.Since(revokedAt) > refreshRotationGrace {
		return "", apperrors.ErrUnauthorized
	}
	var sessionID string
	err := tx.QueryRow(ctx, `
		SELECT session_id FROM refresh_tokens
		WHERE id = $1 AND revoked_at IS NULL AND expires_at > NOW()
	`, *replacedBy).Scan(&sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", apperrors.ErrUnauthorized
		}
		return "", err
	}
	return s.GenerateAccessToken(ctx, user, sessionID)
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
		UserID:             claims.UserID,
		OrgID:              claims.OrgID,
		Email:              claims.Email,
		Roles:              claims.Roles,
		SessionID:          claims.SessionID,
		AuthProvider:       claims.AuthProvider,
		IDToken:            claims.IDToken,
		MustChangePassword: claims.MustChangePassword,
		ExpiresAt:          claims.RegisteredClaims.ExpiresAt.Time,
	}, nil
}

// RevokeSession revokes a specific session
func (s *Service) RevokeSession(ctx context.Context, sessionID string) error {
	return s.revokeSessionTokens(ctx, sessionID)
}

// EndPresentedSession revokes the session behind whichever tokens a logging-out
// request carries. The refresh token is the one that matters: the access token
// may well have expired by the time somebody logs out, and that must not leave
// the session alive. The access token covers a browser still holding its refresh
// cookie on the old path, which is not sent here. Anything unrecognised is
// ignored, because logging out of nothing is not an error.
func (s *Service) EndPresentedSession(ctx context.Context, refreshToken, accessToken string) error {
	var sessions []string
	if refreshToken != "" {
		rt, err := s.findRefreshTokenByHash(ctx, refreshToken)
		switch {
		case err == nil:
			sessions = append(sessions, rt.SessionID)
		case !errors.Is(err, apperrors.ErrNotFound):
			return err
		}
	}
	if accessToken != "" {
		if claims, err := s.ValidateAccessToken(ctx, accessToken); err == nil {
			sessions = append(sessions, claims.SessionID)
		}
	}
	for _, sessionID := range sessions {
		if err := s.revokeSessionTokens(ctx, sessionID); err != nil {
			return err
		}
	}
	return nil
}

// RevokeAllUserTokens revokes all sessions for a user
func (s *Service) RevokeAllUserTokens(ctx context.Context, userID uuid.UUID) error {
	return s.revokeAllUserTokens(ctx, userID)
}

const (
	// refreshCookiePath covers the two routes that need the refresh token,
	// /refresh and /logout, and keeps it away from the rest of the API.
	refreshCookiePath = "/api/auth"
	// legacyRefreshCookiePath is where rt lived before it had to reach /logout.
	// Browsers signed in back then keep it there until told otherwise, so it can
	// go once a refresh token's lifetime has passed since that change.
	legacyRefreshCookiePath = "/api/auth/refresh"
)

// SetAuthCookies sets HttpOnly secure cookies for tokens
func (s *Service) SetAuthCookies(w http.ResponseWriter, tokens *TokenPair) {
	// Access token cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "at",
		Value:    tokens.AccessToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure(),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   tokens.ExpiresIn,
	})

	// A caller that lost a rotation race gets no refresh token, and must keep the
	// cookie it has rather than have it overwritten with nothing.
	if tokens.RefreshToken == "" {
		return
	}
	// Expired first, because a more specific path is sent first: left in place it
	// would shadow the cookie below, and the server would go on reading a token
	// that has already been rotated away.
	s.expireCookie(w, "rt", legacyRefreshCookiePath)
	http.SetCookie(w, &http.Cookie{
		Name:     "rt",
		Value:    tokens.RefreshToken,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   s.cookieSecure(),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.RefreshTokenTTL.Seconds()),
	})
}

// ClearAuthCookies expires the auth cookies. A browser only lets go of a cookie
// when it is expired on the path it was set with.
func (s *Service) ClearAuthCookies(w http.ResponseWriter) {
	s.expireCookie(w, "at", "/")
	s.expireCookie(w, "rt", refreshCookiePath)
	s.expireCookie(w, "rt", legacyRefreshCookiePath)
}

func (s *Service) expireCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cookieSecure(),
		SameSite: http.SameSiteStrictMode,
	})
}

// accessTokenFrom finds the access token on a request: the cookie, or failing
// that the Authorization header.
func accessTokenFrom(c *gin.Context) string {
	if token, err := c.Cookie("at"); err == nil {
		return token
	}
	if token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer "); ok {
		return token
	}
	return ""
}

// AuthMiddleware validates access token from cookie
func (s *Service) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		accessToken := accessTokenFrom(c)
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
		c.Set("must_change_password", claims.MustChangePassword)

		// What their roles let them do, as the roles are now. Failing closed:
		// a person whose permissions cannot be read gets none.
		if uid, err := uuid.Parse(claims.UserID); err == nil {
			perms, err := s.PermissionsOf(c.Request.Context(), uid)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "could not read your permissions"})
				return
			}
			c.Set("user_permissions", perms)
		}

		// Somebody still on an administrator-generated password may only look
		// at themselves, change it, or sign out. Enforced here rather than in
		// the interface so calling the API directly does not get around it.
		if claims.MustChangePassword && !isPasswordChangePath(c.Request.Method, c.FullPath()) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":              "set your own password before continuing",
				"mustChangePassword": true,
			})
			return
		}

		c.Next()
	}
}

// isPasswordChangePath lists what remains reachable while a password change is
// outstanding: enough to sign in, see who you are, change it, and leave.
func isPasswordChangePath(method, path string) bool {
	switch path {
	case "/api/auth/me", "/api/auth/password", "/api/auth/logout", "/api/auth/refresh":
		return true
	}
	return false
}

// GetUserFromContext extracts user info from Gin context
func GetUserFromContext(c *gin.Context) *UserContext {
	return &UserContext{
		UserID:             c.GetString("user_id"),
		OrgID:              c.GetString("org_id"),
		Roles:              c.GetStringSlice("user_roles"),
		Email:              c.GetString("user_email"),
		SessionID:          c.GetString("session_id"),
		AuthProvider:       c.GetString("auth_provider"),
		IDToken:            c.GetString("id_token"),
		MustChangePassword: c.GetBool("must_change_password"),
		Permissions:        permissionsFrom(c),
	}
}

// permissionsFrom is nil when nothing worked them out, which Can reads as the
// built-in roles' defaults.
func permissionsFrom(c *gin.Context) []string {
	v, ok := c.Get("user_permissions")
	if !ok {
		return nil
	}
	perms, _ := v.([]string)
	if perms == nil {
		perms = []string{}
	}
	return perms
}

type UserContext struct {
	UserID             string
	OrgID              string
	Roles              []string
	Email              string
	SessionID          string
	AuthProvider       string
	IDToken            string
	MustChangePassword bool
	// Permissions are what the person's roles let them do; see Can.
	Permissions []string
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

// The OIDC cookies below stay Secure unconditionally. Signing in through an
// external provider requires an HTTPS redirect URI anyway, so there is no
// plain-HTTP case for them to support.
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

// userColumns is shared by the by-id and by-email loads. Roles come from the
// RBAC join table; without them every permission check downstream sees an
// empty role list.
const userColumns = `
	SELECT u.id, u.org_id, u.email, u.name, COALESCE(u.avatar_url, ''),
	       COALESCE(u.password_hash, ''), COALESCE(u.is_active, true),
	       COALESCE(u.must_change_password, false), u.tour_completed_at, u.whats_new_seen,
	       COALESCE(ARRAY_REMOVE(ARRAY_AGG(r.name), NULL), '{}') AS roles
	FROM users u
	LEFT JOIN user_roles ur ON ur.user_id = u.id
	LEFT JOIN roles r ON r.id = ur.role_id
	WHERE u.deleted_at IS NULL AND %s
	GROUP BY u.id`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.OrgID, &u.Email, &u.Name, &u.AvatarURL,
		&u.PasswordHash, &u.IsActive, &u.MustChangePassword, &u.TourCompletedAt, &u.WhatsNewSeen, &u.Roles)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// whatsNewID is what a "What's new" entry is called: a date, perhaps with a
// suffix for a second update the same day.
var whatsNewID = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(-[a-z0-9]{1,10})?$`)

// SetWhatsNewSeen records the newest "What's new" entry this person has seen.
// It never goes backwards: an old page still open somewhere cannot make the
// newest entry show again.
func (s *Service) SetWhatsNewSeen(ctx context.Context, userID uuid.UUID, id string) (string, error) {
	if !whatsNewID.MatchString(id) {
		return "", apperrors.New(apperrors.CodeValidation, "not a What's new entry")
	}
	var seen string
	err := s.db.QueryRow(ctx, `
		UPDATE users SET whats_new_seen = CASE
			WHEN whats_new_seen IS NULL OR whats_new_seen < $2 THEN $2 ELSE whats_new_seen END
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING whats_new_seen`, userID, id).Scan(&seen)
	if err == pgx.ErrNoRows {
		return "", apperrors.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("set what's new: %w", err)
	}
	return seen, nil
}

// SetTourCompleted marks the welcome tour done for this person now, or clears it
// so it is shown again; it answers with what was stored.
func (s *Service) SetTourCompleted(ctx context.Context, userID uuid.UUID, done bool) (*time.Time, error) {
	var at *time.Time
	err := s.db.QueryRow(ctx, `
		UPDATE users SET tour_completed_at = CASE WHEN $2 THEN NOW() ELSE NULL END
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING tour_completed_at`, userID, done).Scan(&at)
	if err == pgx.ErrNoRows {
		return nil, apperrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("set tour: %w", err)
	}
	return at, nil
}

func (s *Service) getUser(ctx context.Context, userID uuid.UUID) (*User, error) {
	return scanUser(s.db.QueryRow(ctx, fmt.Sprintf(userColumns, "u.id = $1"), userID))
}

// GetUserByEmail looks the account up case-insensitively: browsers send
// whatever the user typed, and addresses are not case-sensitive in practice.
func (s *Service) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.db.QueryRow(ctx,
		fmt.Sprintf(userColumns, "LOWER(u.email) = LOWER($1)"), email))
}

// VerifyPassword compares against the bcrypt hash stored by the setup wizard.
func (s *Service) VerifyPassword(user *User, password string) error {
	if user == nil || user.PasswordHash == "" {
		// OIDC-only accounts have no password; refuse rather than treating an
		// empty hash as a match.
		return apperrors.New(apperrors.CodeUnauthorized, "password authentication not available for this account")
	}
	if !user.IsActive {
		return apperrors.New(apperrors.CodeUnauthorized, "account is disabled")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return apperrors.New(apperrors.CodeUnauthorized, "invalid credentials")
	}
	return nil
}

// execer is whatever can run a statement: the pool for ordinary writes, a
// transaction when rotation has to write and retire tokens together.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func storeRefreshToken(ctx context.Context, db execer, rt *RefreshToken) error {
	_, err := db.Exec(ctx, `
		INSERT INTO refresh_tokens (id, user_id, session_id, token_hash, user_agent, ip_address, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::inet, $7, $8)
	`, rt.ID, rt.UserID, rt.SessionID, rt.TokenHash, rt.UserAgent, rt.IPAddress, rt.ExpiresAt, rt.CreatedAt)
	return err
}

func (s *Service) findRefreshTokenByHash(ctx context.Context, tokenString string) (*RefreshToken, error) {
	var rt RefreshToken
	var ua, ip *string
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, session_id, token_hash, user_agent, host(ip_address), expires_at, revoked_at, created_at
		FROM refresh_tokens WHERE token_hash = $1
	`, hashRefreshToken(tokenString)).Scan(
		&rt.ID, &rt.UserID, &rt.SessionID, &rt.TokenHash, &ua, &ip,
		&rt.ExpiresAt, &rt.RevokedAt, &rt.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	if ua != nil {
		rt.UserAgent = *ua
	}
	if ip != nil {
		rt.IPAddress = *ip
	}
	return &rt, nil
}

func (s *Service) revokeSessionTokens(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE session_id = $1 AND revoked_at IS NULL`, sessionID)
	return err
}

func (s *Service) revokeAllUserTokens(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// isSessionRevoked backs access-token validation. A session with no live
// refresh token — revoked, expired, or never issued — is not usable, so an
// unknown session id reports revoked rather than allowing a forged token.
func (s *Service) isSessionRevoked(ctx context.Context, sessionID string) (bool, error) {
	var live int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM refresh_tokens
		WHERE session_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
	`, sessionID).Scan(&live)
	if err != nil {
		return true, err
	}
	return live == 0, nil
}

func (s *Service) getLinkedIdentity(ctx context.Context, provider, providerID string) (*LinkedIdentity, error) {
	var li LinkedIdentity
	var email, at, rtok, idt *string
	err := s.db.QueryRow(ctx, `
		SELECT user_id, provider, provider_id, email, access_token, refresh_token, id_token, updated_at
		FROM linked_identities WHERE provider = $1 AND provider_id = $2
	`, provider, providerID).Scan(&li.UserID, &li.Provider, &li.ProviderID,
		&email, &at, &rtok, &idt, &li.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, apperrors.ErrNotFound
		}
		return nil, err
	}
	for dst, src := range map[*string]*string{
		&li.Email: email, &li.AccessToken: at, &li.RefreshToken: rtok, &li.IDToken: idt,
	} {
		if src != nil {
			*dst = *src
		}
	}
	return &li, nil
}

func (s *Service) upsertLinkedIdentity(ctx context.Context, li *LinkedIdentity) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO linked_identities (user_id, provider, provider_id, email, access_token, refresh_token, id_token, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (user_id, provider) DO UPDATE
		SET provider_id = EXCLUDED.provider_id, email = EXCLUDED.email,
		    access_token = EXCLUDED.access_token, refresh_token = EXCLUDED.refresh_token,
		    id_token = EXCLUDED.id_token, updated_at = NOW()
	`, li.UserID, li.Provider, li.ProviderID, li.Email, li.AccessToken, li.RefreshToken, li.IDToken)
	return err
}

func (s *Service) deleteLinkedIdentity(ctx context.Context, userID uuid.UUID, provider string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM linked_identities WHERE user_id = $1 AND provider = $2`, userID, provider)
	return err
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

// MinPasswordLength matches what the setup wizard enforces, so a password
// cannot be weakened by rotating it.
const MinPasswordLength = 12

// ChangePassword rotates a user's password after proving they know the current
// one, and revokes their other sessions so a stolen token does not outlive the
// rotation.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	if len(newPassword) < MinPasswordLength {
		return apperrors.New(apperrors.CodeBadRequest,
			fmt.Sprintf("the new password must be at least %d characters", MinPasswordLength))
	}
	if currentPassword == newPassword {
		return apperrors.New(apperrors.CodeBadRequest, "the new password must be different")
	}

	user, err := s.getUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.VerifyPassword(user, currentPassword); err != nil {
		return apperrors.New(apperrors.CodeUnauthorized, "your current password is not correct")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE users SET password_hash = $2, must_change_password = FALSE, updated_at = NOW()
		WHERE id = $1
	`, userID, hash); err != nil {
		return fmt.Errorf("store new password: %w", err)
	}

	// Anything issued against the old password stops working.
	if err := s.revokeAllUserTokens(ctx, userID); err != nil {
		return fmt.Errorf("revoke sessions after password change: %w", err)
	}
	return nil
}

// Session describes an active sign-in, for the "where am I signed in" screen.
type Session struct {
	SessionID string    `json:"sessionId"`
	UserAgent string    `json:"userAgent"`
	IPAddress string    `json:"ipAddress"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// ListSessions returns the user's live sessions, newest first.
func (s *Service) ListSessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	rows, err := s.db.Query(ctx, `
		SELECT session_id, COALESCE(user_agent, ''), COALESCE(host(ip_address), ''), created_at, expires_at
		FROM refresh_tokens
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	sessions := []Session{}
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.SessionID, &sess.UserAgent, &sess.IPAddress,
			&sess.CreatedAt, &sess.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// RevokeUserSession revokes one session, but only if it belongs to the caller.
func (s *Service) RevokeUserSession(ctx context.Context, userID uuid.UUID, sessionID string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE refresh_tokens SET revoked_at = NOW()
		WHERE session_id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, sessionID, userID)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apperrors.ErrNotFound
	}
	return nil
}

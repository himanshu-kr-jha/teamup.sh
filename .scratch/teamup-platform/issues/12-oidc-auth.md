## Question

Design OIDC authentication flow: provider abstraction (Google, Entra ID, generic OIDC), PKCE for public clients, session management (JWT access token + refresh token rotation, HttpOnly secure cookies), role mapping (provider groups → app roles), account linking, and logout (RP-initiated + back-channel).

Status: claimed

## Answer

**Decision: OIDC authentication architecture defined below.**

### 1. Provider Abstraction

```go
// internal/auth/providers.go
package auth

import (
    "context"
    "golang.org/x/oauth2"
    "github.com/coreos/go-oidc/v3/oidc"
)

type Provider interface {
    Name() string                                    // "google", "entra", "custom"
    AuthCodeURL(state string, opts ...oauth2.AuthCodeOption) string
    Exchange(ctx context.Context, code string) (*oauth2.Token, error)
    VerifyIDToken(ctx context.Context, rawIDToken string) (*oidc.IDToken, error)
    UserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error)
    RevokeToken(ctx context.Context, token string) error
    EndSessionURL(idTokenHint string, postLogoutRedirectURI string) string
    SupportsPKCE() bool
    SupportsRefreshToken() bool
}

type UserInfo struct {
    Subject       string            `json:"sub"`
    Email         string            `json:"email"`
    EmailVerified bool              `json:"email_verified"`
    Name          string            `json:"name"`
    GivenName     string            `json:"given_name"`
    FamilyName    string            `json:"family_name"`
    Picture       string            `json:"picture"`
    Groups        []string          `json:"groups"`           // For role mapping
    Claims        map[string]any    `json:"-"`              // Raw claims
}
```

**Provider Registry:**
```go
var providers = map[string]Provider{
    "google":   NewGoogleProvider(),
    "entra":    NewEntraProvider(),
    "custom":   NewCustomProvider(),
}

func GetProvider(name string) (Provider, bool) {
    p, ok := providers[name]
    return p, ok
}
```

---

### 2. Google Provider

```go
// internal/auth/google.go
func NewGoogleProvider() *OIDCProvider {
    return &OIDCProvider{
        name: "google",
        oauth2Config: &oauth2.Config{
            ClientID:     cfg.GoogleClientID,
            ClientSecret: cfg.GoogleClientSecret,
            RedirectURL:  cfg.BaseURL + "/auth/callback/google",
            Scopes:       []string{"openid", "email", "profile"},
            Endpoint: oauth2.Endpoint{
                AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
                TokenURL: "https://oauth2.googleapis.com/token",
            },
        },
        oidcProvider: nil, // Lazy init
    }
}

// Google-specific: returns groups via custom claim or directory API
func (p *OIDCProvider) UserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error) {
    // First try ID token claims
    if idToken, ok := token.Extra("id_token").(string); ok {
        claims := parseIDToken(idToken)
        if groups := claims["groups"]; groups != nil {
            return &UserInfo{Groups: groups}, nil
        }
    }
    // Fallback: UserInfo endpoint
    return p.oidcProvider.UserInfo(ctx, oauth2.StaticTokenSource(token))
}
```

---

### 3. Entra ID (Azure AD) Provider

```go
// internal/auth/entra.go
func NewEntraProvider() *OIDCProvider {
    tenantID := cfg.EntraTenantID // "common" or specific tenant
    return &OIDCProvider{
        name: "entra",
        oauth2Config: &oauth2.Config{
            ClientID:     cfg.EntraClientID,
            ClientSecret: cfg.EntraClientSecret,
            RedirectURL:  cfg.BaseURL + "/auth/callback/entra",
            Scopes:       []string{"openid", "email", "profile", "GroupMember.Read.All"},
            Endpoint: oauth2.Endpoint{
                AuthURL:  "https://login.microsoftonline.com/" + tenantID + "/oauth2/v2.0/authorize",
                TokenURL: "https://login.microsoftonline.com/" + tenantID + "/oauth2/v2.0/token",
            },
        },
    }
}

// Entra ID: groups claim in ID token (if configured) or Microsoft Graph API
func (p *OIDCProvider) UserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error) {
    // Prefer ID token "groups" claim (requires Azure AD config)
    if idToken, ok := token.Extra("id_token").(string); ok {
        claims := parseIDToken(idToken)
        if groups := claims["groups"]; groups != nil {
            return &UserInfo{Groups: groups}, nil
        }
    }
    // Fallback: Microsoft Graph /me/memberOf
    client := graph.NewClient(token.AccessToken)
    groups, _ := client.GetUserGroups(ctx)
    return &UserInfo{Groups: groups}, nil
}
```

---

### 4. Custom OIDC Provider

```go
// internal/auth/custom.go
func NewCustomProvider() *OIDCProvider {
    return &OIDCProvider{
        name: "custom",
        oauth2Config: &oauth2.Config{
            ClientID:     cfg.CustomClientID,
            ClientSecret: cfg.CustomClientSecret,
            RedirectURL:  cfg.BaseURL + "/auth/callback/custom",
            Scopes:       []string{"openid", "email", "profile"},
            Endpoint: oauth2.Endpoint{
                AuthURL:  cfg.CustomIssuer + "/authorize",
                TokenURL: cfg.CustomIssuer + "/token",
                AuthStyle: oauth2.AuthStyleInParams,
            },
        },
        oidcProvider: nil, // Discovered from issuer
    }
}
```

---

### 5. PKCE for Public Clients (PWA)

```go
// internal/auth/pkce.go
import "github.com/zalando/go-keyring"

func GeneratePKCE() (codeVerifier, codeChallenge string, err error) {
    // code_verifier: 43-128 chars, unreserved
    verifier := make([]byte, 32)
    if _, err := rand.Read(verifier); err != nil { return "", "", err }
    codeVerifier = base64.RawURLEncoding.EncodeToString(verifier)

    // code_challenge = SHA256(code_verifier)
    hash := sha256.Sum256([]byte(codeVerifier))
    codeChallenge = base64.RawURLEncoding.EncodeToString(hash[:])
    return codeVerifier, codeChallenge, nil
}

// Store in secure HttpOnly cookie (short-lived)
func SetPKCECookie(w http.ResponseWriter, verifier string) {
    http.SetCookie(w, &http.Cookie{
        Name:     "pkce_verifier",
        Value:    verifier,
        Path:     "/",
        HttpOnly: true,
        Secure:   true,
        SameSite: http.SameSiteLaxMode,
        MaxAge:   600, // 10 min
    })
}

func GetAndClearPKCECookie(w http.ResponseWriter, r *http.Request) (string, error) {
    cookie, err := r.Cookie("pkce_verifier")
    if err != nil { return "", err }
    http.SetCookie(w, &http.Cookie{
        Name: "pkce_verifier", Value: "", Path: "/", MaxAge: -1,
        HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
    })
    return cookie.Value, nil
}

// Usage in auth handler:
func (h *Handler) InitiateLogin(w http.ResponseWriter, r *http.Request) {
    provider := h.getProvider(r.FormValue("provider"))
    verifier, challenge, _ := GeneratePKCE()
    SetPKCECookie(w, verifier)

    state := generateState()
    SetStateCookie(w, state)

    url := provider.AuthCodeURL(state,
        oauth2.SetAuthURLParam("code_challenge", challenge),
        oauth2.SetAuthURLParam("code_challenge_method", "S256"),
    )
    http.Redirect(w, r, url, http.StatusFound)
}

func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
    verifier, _ := GetAndClearPKCECookie(w, r)
    code := r.FormValue("code")
    state := r.FormValue("state")
    // Validate state...

    token, err := provider.Exchange(r.Context(), code, oauth2.SetAuthURLParam("code_verifier", verifier))
    // ...
}
```

---

### 6. Session Management (JWT + Refresh Token Rotation)

```go
// internal/auth/session.go
type TokenPair struct {
    AccessToken  string `json:"access_token"`
    RefreshToken string `json:"refresh_token"`
    ExpiresIn    int    `json:"expires_in"`      // seconds
    TokenType    string `json:"token_type"`      // "Bearer"
    IDToken      string `json:"id_token,omitempty"`
}

type Claims struct {
    jwt.RegisteredClaims
    UserID   string   `json:"user_id"`
    OrgID    string   `json:"org_id"`
    Email    string   `json:"email"`
    Roles    []string `json:"roles"`
    SessionID string  `json:"sid"`  // For refresh token rotation
}

// Access Token: Short-lived (15 min), JWT RS256
func (s *Service) GenerateAccessToken(ctx context.Context, user *User, sessionID string) (string, error) {
    claims := &Claims{
        RegisteredClaims: jwt.RegisteredClaims{
            Subject:   user.ID,
            Issuer:    s.cfg.Issuer,
            Audience:  []string{s.cfg.Audience},
            ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
            IssuedAt:  jwt.NewNumericDate(time.Now()),
            ID:        uuid.New().String(),
        },
        UserID:   user.ID,
        OrgID:    user.OrgID,
        Email:    user.Email,
        Roles:    user.Roles,
        SessionID: sessionID,
    }
    return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(s.privateKey)
}

// Refresh Token: Long-lived (30 days), opaque, stored hashed in DB
type RefreshToken struct {
    ID        UUID      `json:"id"`
    UserID    UUID      `json:"user_id"`
    SessionID string    `json:"session_id"`
    TokenHash string    `json:"token_hash"`  // bcrypt hash
    UserAgent string    `json:"user_agent"`
    IPAddress string    `json:"ip_address"`
    ExpiresAt time.Time `json:"expires_at"`
    RevokedAt *time.Time `json:"revoked_at,omitempty"`
    CreatedAt time.Time `json:"created_at"`
}

func (s *Service) GenerateRefreshToken(ctx context.Context, user *User, ua, ip string) (*RefreshToken, string, error) {
    // Generate random token
    rawToken := make([]byte, 32)
    rand.Read(rawToken)
    tokenString := base64.RawURLEncoding.EncodeToString(rawToken)

    // Hash for storage
    hash, _ := bcrypt.GenerateFromPassword([]byte(tokenString), bcrypt.DefaultCost)

    sessionID := uuid.New().String()
    rt := &RefreshToken{
        ID:        uuid.New(),
        UserID:    user.ID,
        SessionID: sessionID,
        TokenHash: string(hash),
        UserAgent: ua,
        IPAddress: ip,
        ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
        CreatedAt: time.Now(),
    }

    if err := s.db.InsertRefreshToken(ctx, rt); err != nil {
        return nil, "", err
    }
    return rt, tokenString, nil
}

// Refresh Token Rotation (RFC 6819)
func (s *Service) RefreshAccessToken(ctx context.Context, refreshToken, ua, ip string) (*TokenPair, error) {
    // 1. Find token by hash (scan - use index on user_id + partial token)
    rt, err := s.db.FindRefreshTokenByHash(ctx, refreshToken)
    if err != nil || rt.RevokedAt != nil || time.Now().After(rt.ExpiresAt) {
        return nil, ErrInvalidRefreshToken
    }

    // 2. Revoke old token (rotation)
    now := time.Now()
    rt.RevokedAt = &now
    if err := s.db.UpdateRefreshToken(ctx, rt); err != nil {
        return nil, err
    }

    // 3. Get user
    user, err := s.db.GetUser(ctx, rt.UserID)
    if err != nil { return nil, err }

    // 4. Generate new pair
    newRT, newRawToken, err := s.GenerateRefreshToken(ctx, user, ua, ip)
    if err != nil { return nil, err }

    accessToken, err := s.GenerateAccessToken(ctx, user, newRT.SessionID)
    if err != nil { return nil, err }

    return &TokenPair{
        AccessToken:  accessToken,
        RefreshToken: newRawToken,
        ExpiresIn:    15 * 60,
        TokenType:    "Bearer",
    }, nil
}

// Revoke all user sessions (logout everywhere)
func (s *Service) RevokeAllUserTokens(ctx context.Context, userID UUID) error {
    return s.db.RevokeAllRefreshTokens(ctx, userID)
}
```

---

### 7. Cookie-Based Session (HttpOnly, Secure)

```go
// internal/auth/cookies.go
func (s *Service) SetAuthCookies(w http.ResponseWriter, tokens *TokenPair) {
    // Access token: short-lived, HttpOnly, Secure, SameSite=Strict
    http.SetCookie(w, &http.Cookie{
        Name:     "at",
        Value:    tokens.AccessToken,
        Path:     "/",
        HttpOnly: true,
        Secure:   true,
        SameSite: http.SameSiteStrictMode,
        MaxAge:   int(tokens.ExpiresIn),
    })

    // Refresh token: HttpOnly, Secure, SameSite=Strict, longer expiry
    http.SetCookie(w, &http.Cookie{
        Name:     "rt",
        Value:    tokens.RefreshToken,
        Path:     "/api/auth/refresh",  // Only sent to refresh endpoint
        HttpOnly: true,
        Secure:   true,
        SameSite: http.SameSiteStrictMode,
        MaxAge:   30 * 24 * 60 * 60, // 30 days
    })
}

func (s *Service) ClearAuthCookies(w http.ResponseWriter) {
    for _, name := range []string{"at", "rt"} {
        http.SetCookie(w, &http.Cookie{
            Name: name, Value: "", Path: "/", MaxAge: -1,
            HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
        })
    }
}

// Middleware: extract access token from cookie
func AuthMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        cookie, err := r.Cookie("at")
        if err != nil {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }

        claims, err := s.ValidateAccessToken(r.Context(), cookie.Value)
        if err != nil {
            // Try refresh
            if rtCookie, _ := r.Cookie("rt"); rtCookie != nil {
                if tokens, err := s.RefreshAccessToken(r.Context(), rtCookie.Value, ...); err == nil {
                    s.SetAuthCookies(w, tokens)
                    claims, _ = s.ValidateAccessToken(r.Context(), tokens.AccessToken)
                }
            }
            if claims == nil {
                http.Error(w, "unauthorized", http.StatusUnauthorized)
                return
            }
        }

        ctx := WithUser(r.Context(), claims)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

---

### 8. Role Mapping (Provider Groups → App Roles)

```go
// internal/auth/role_mapping.go
type RoleMapping struct {
    Provider   string   `json:"provider"`   // "google", "entra", "custom"
    GroupClaim string   `json:"group_claim"` // Claim name for groups (default: "groups")
    Mappings   []GroupMapping `json:"mappings"`
}

type GroupMapping struct {
    ProviderGroup string `json:"provider_group"`  // Exact match or regex
    AppRole       string `json:"app_role"`        // "owner", "admin", "manager", "member"
    Priority      int    `json:"priority"`        // Higher = wins
}

// Default mappings (configurable per org)
var defaultMappings = map[string]RoleMapping{
    "google": {
        Provider: "google",
        Mappings: []GroupMapping{
            {ProviderGroup: "teamup-admins", AppRole: "admin", Priority: 100},
            {ProviderGroup: "teamup-managers", AppRole: "manager", Priority: 50},
        },
    },
    "entra": {
        Provider: "entra",
        GroupClaim: "groups", // Object IDs
        Mappings: []GroupMapping{
            {ProviderGroup: "a1b2c3d4-...", AppRole: "admin", Priority: 100}, // Admin group Object ID
        },
    },
}

func (s *Service) MapRoles(ctx context.Context, provider string, groups []string) []string {
    mapping := defaultMappings[provider]
    var roles []string

    for _, g := range groups {
        for _, m := range mapping.Mappings {
            if matchGroup(g, m.ProviderGroup) {
                roles = append(roles, m.AppRole)
            }
        }
    }

    // Always include 'member' as baseline
    if !slices.Contains(roles, "member") {
        roles = append(roles, "member")
    }
    return roles
}
```

---

### 9. Account Linking

```go
// internal/auth/linking.go
func (s *Service) LinkAccount(ctx context.Context, userID UUID, provider string, token *oauth2.Token, idToken string) error {
    // 1. Get user info from provider
    userInfo, err := s.providers[provider].UserInfo(ctx, token)
    if err != nil { return err }

    // 2. Check if this identity already linked to another user
    existing, err := s.db.GetLinkedIdentity(ctx, provider, userInfo.Subject)
    if err == nil && existing.UserID != userID {
        return ErrIdentityAlreadyLinked // Different user owns this identity
    }

    // 3. Store/link identity
    return s.db.UpsertLinkedIdentity(ctx, &LinkedIdentity{
        UserID:      userID,
        Provider:    provider,
        ProviderID:  userInfo.Subject,
        Email:       userInfo.Email,
        AccessToken: encrypt(token.AccessToken),   // Encrypted at rest
        RefreshToken: encrypt(token.RefreshToken),
        IDToken:     idToken,
        UpdatedAt:   time.Now(),
    })
}

func (s *Service) UnlinkAccount(ctx context.Context, userID UUID, provider string) error {
    // Prevent unlinking last auth method
    count, _ := s.db.CountLinkedIdentities(ctx, userID)
    if count <= 1 { return ErrCannotUnlinkLastMethod }
    return s.db.DeleteLinkedIdentity(ctx, userID, provider)
}
```

---

### 10. Logout (RP-Initiated + Back-Channel)

```go
// internal/auth/logout.go
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
    // 1. Get current session
    claims := GetUser(r.Context())

    // 2. Revoke refresh tokens (local)
    if claims.SessionID != "" {
        s.auth.RevokeSession(r.Context(), claims.SessionID)
    }
    // Or revoke all: s.auth.RevokeAllUserTokens(r.Context(), claims.UserID)

    // 3. Clear local cookies
    s.auth.ClearAuthCookies(w)

    // 4. RP-Initiated Logout: Redirect to provider's end_session_endpoint
    provider := h.getProvider(claims.AuthProvider)
    idToken := claims.IDToken // Stored in session/cookie

    logoutURL := provider.EndSessionURL(idToken, h.cfg.BaseURL+"/logged-out")

    http.Redirect(w, r, logoutURL, http.StatusFound)
}

// Back-Channel Logout (OIDC spec) - Provider notifies us of logout elsewhere
func (h *Handler) HandleBackchannelLogout(w http.ResponseWriter, r *http.Request) {
    // Parse logout token (JWT)
    logoutToken := r.FormValue("logout_token")
    claims, err := s.ValidateLogoutToken(r.Context(), logoutToken)
    if err != nil { http.Error(w, "invalid", 400); return }

    // Find user by subject/sub
    user, err := s.db.FindUserByProviderSubject(r.Context(), claims.Subject)
    if err != nil { return } // Silent ignore

    // Revoke all sessions for this user
    s.auth.RevokeAllUserTokens(r.Context(), user.ID)

    w.WriteHeader(http.StatusOK)
}
```

---

### 11. Security Hardening

| Threat | Mitigation |
|--------|------------|
| **Token theft** | Short access tokens (15m), refresh rotation, HttpOnly cookies |
| **CSRF** | PKCE for auth code flow, SameSite=Strict cookies, state parameter |
| **Replay** | Nonce in auth request, validated in ID token |
| **Token leakage in logs** | Never log tokens; sanitize in middleware |
| **Refresh token theft** | Rotation on each use, binding to UA/IP, revocation on suspicious activity |
| **Open redirect** | Validate `post_logout_redirect_uri` against allowlist |
| **PKCE downgrade** | Enforce S256, reject plain |

---

### 12. Open Questions (for later)

- **MFA/WebAuthn** (passkeys) — v2
- **Device trust** (certificate-based) — v2
- **Session management UI** (view/revoke devices) — v2
- **SCIM provisioning** (Entra ID, Okta) — v2
- **Passwordless login** (magic links) — v2

Status: resolved

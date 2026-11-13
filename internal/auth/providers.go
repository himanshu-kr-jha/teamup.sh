package auth

import (
	"context"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type OIDCProvider struct {
	name         string
	oauth2Config *oauth2.Config
	oidcProvider *oidc.Provider
	verifier     *oidc.IDTokenVerifier
}

func NewGoogleProvider(cfg Config) *OIDCProvider {
	return &OIDCProvider{
		name: "google",
		oauth2Config: &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleSecret,
			RedirectURL:  cfg.BaseURL + "/api/auth/callback/google",
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
				TokenURL: "https://oauth2.googleapis.com/token",
			},
		},
	}
}

func NewEntraProvider(cfg Config) *OIDCProvider {
	tenantID := cfg.EntraTenantID
	if tenantID == "" {
		tenantID = "common"
	}
	return &OIDCProvider{
		name: "entra",
		oauth2Config: &oauth2.Config{
			ClientID:     cfg.EntraClientID,
			ClientSecret: cfg.EntraSecret,
			RedirectURL:  cfg.BaseURL + "/api/auth/callback/entra",
			Scopes:       []string{"openid", "email", "profile", "GroupMember.Read.All"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://login.microsoftonline.com/" + tenantID + "/oauth2/v2.0/authorize",
				TokenURL: "https://login.microsoftonline.com/" + tenantID + "/oauth2/v2.0/token",
			},
		},
	}
}

func NewCustomProvider(cfg Config) *OIDCProvider {
	p := &OIDCProvider{
		name: "custom",
		oauth2Config: &oauth2.Config{
			ClientID:     cfg.CustomClientID,
			ClientSecret: cfg.CustomSecret,
			RedirectURL:  cfg.BaseURL + "/api/auth/callback/custom",
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:   cfg.CustomIssuer + "/authorize",
				TokenURL:  cfg.CustomIssuer + "/token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
	}

	// Initialize OIDC provider for discovery
	if cfg.CustomIssuer != "" {
		go func() {
			ctx := context.Background()
			provider, err := oidc.NewProvider(ctx, cfg.CustomIssuer)
			if err == nil {
				p.oidcProvider = provider
				p.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.CustomClientID})
			}
		}()
	}

	return p
}

func (p *OIDCProvider) Name() string {
	return p.name
}

func (p *OIDCProvider) AuthCodeURL(state string, opts ...oauth2.AuthCodeOption) string {
	return p.oauth2Config.AuthCodeURL(state, opts...)
}

func (p *OIDCProvider) Exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	return p.oauth2Config.Exchange(ctx, code)
}

func (p *OIDCProvider) VerifyIDToken(ctx context.Context, rawIDToken string) (*oidc.IDToken, error) {
	if p.verifier == nil {
		// Lazy init
		if p.oidcProvider == nil {
			provider, err := oidc.NewProvider(ctx, p.getIssuerURL())
			if err != nil {
				return nil, err
			}
			p.oidcProvider = provider
			p.verifier = provider.Verifier(&oidc.Config{ClientID: p.oauth2Config.ClientID})
		} else {
			p.verifier = p.oidcProvider.Verifier(&oidc.Config{ClientID: p.oauth2Config.ClientID})
		}
	}
	return p.verifier.Verify(ctx, rawIDToken)
}

func (p *OIDCProvider) getIssuerURL() string {
	switch p.name {
	case "google":
		return "https://accounts.google.com"
	case "entra":
		return "https://login.microsoftonline.com/" + p.oauth2Config.Endpoint.AuthURL
	default:
		return p.oauth2Config.Endpoint.AuthURL
	}
}

func (p *OIDCProvider) UserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error) {
	// Try ID token first
	if idToken, ok := token.Extra("id_token").(string); ok {
		verifiedToken, err := p.VerifyIDToken(ctx, idToken)
		if err == nil {
			var claims map[string]interface{}
			if err := verifiedToken.Claims(&claims); err == nil {
				return extractUserInfo(claims), nil
			}
		}
	}

	// Fallback to UserInfo endpoint
	if p.oidcProvider == nil {
		if err := p.initProvider(ctx); err != nil {
			return nil, err
		}
	}

	userInfo, err := p.oidcProvider.UserInfo(ctx, oauth2.StaticTokenSource(token))
	if err != nil {
		return nil, err
	}

	var claims map[string]interface{}
	if err := userInfo.Claims(&claims); err != nil {
		return nil, err
	}

	return extractUserInfo(claims), nil
}

func (p *OIDCProvider) initProvider(ctx context.Context) error {
	issuer := p.getIssuerURL()
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return err
	}
	p.oidcProvider = provider
	p.verifier = provider.Verifier(&oidc.Config{ClientID: p.oauth2Config.ClientID})
	return nil
}

func (p *OIDCProvider) EndSessionURL(idTokenHint, postLogoutRedirectURI string) string {
	endSessionEndpoint := p.getEndSessionEndpoint()
	if endSessionEndpoint == "" {
		return ""
	}

	params := make(map[string]string)
	if idTokenHint != "" {
		params["id_token_hint"] = idTokenHint
	}
	if postLogoutRedirectURI != "" {
		params["post_logout_redirect_uri"] = postLogoutRedirectURI
	}

	url := endSessionEndpoint + "?"
	first := true
	for k, v := range params {
		if !first {
			url += "&"
		}
		url += k + "=" + v
		first = false
	}
	return url
}

func (p *OIDCProvider) getEndSessionEndpoint() string {
	// Known endpoints; reading end_session_endpoint from the discovery
	// document would cover any provider, and belongs with real OIDC sign-in.
	switch p.name {
	case "google":
		return "https://accounts.google.com/Logout"
	case "entra":
		return "https://login.microsoftonline.com/common/oauth2/v2.0/logout"
	default:
		return ""
	}
}

func (p *OIDCProvider) SupportsPKCE() bool {
	return true
}

func (p *OIDCProvider) SupportsRefreshToken() bool {
	return true
}

func extractUserInfo(claims map[string]interface{}) *UserInfo {
	ui := &UserInfo{
		Claims: claims,
	}

	if sub, ok := claims["sub"].(string); ok {
		ui.Subject = sub
	}
	if email, ok := claims["email"].(string); ok {
		ui.Email = email
	}
	if emailVerified, ok := claims["email_verified"].(bool); ok {
		ui.EmailVerified = emailVerified
	}
	if name, ok := claims["name"].(string); ok {
		ui.Name = name
	}
	if givenName, ok := claims["given_name"].(string); ok {
		ui.GivenName = givenName
	}
	if familyName, ok := claims["family_name"].(string); ok {
		ui.FamilyName = familyName
	}
	if picture, ok := claims["picture"].(string); ok {
		ui.Picture = picture
	}
	if groups, ok := claims["groups"].([]interface{}); ok {
		for _, g := range groups {
			if gs, ok := g.(string); ok {
				ui.Groups = append(ui.Groups, gs)
			}
		}
	}

	return ui
}

// RoleMapping maps provider groups to app roles
type RoleMapping struct {
	Provider   string
	GroupClaim string
	Mappings   []GroupMapping
}

type GroupMapping struct {
	ProviderGroup string
	AppRole       string
	Priority      int
}

func MapRoles(provider string, groups []string, mappings map[string]RoleMapping) []string {
	mapping, ok := mappings[provider]
	if !ok {
		return []string{"member"}
	}

	var roles []string
	for _, g := range groups {
		for _, m := range mapping.Mappings {
			if matchGroup(g, m.ProviderGroup) {
				roles = append(roles, m.AppRole)
			}
		}
	}

	// Always include member as baseline
	if !contains(roles, "member") {
		roles = append(roles, "member")
	}

	return roles
}

func matchGroup(group, pattern string) bool {
	// Support exact match or prefix with *
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(group, prefix)
	}
	return group == pattern
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

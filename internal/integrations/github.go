package integrations

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// githubSecrets is what GitHub handed over when the app was made for an
// organization. Sealed before it is stored.
type githubSecrets struct {
	AppID         int64  `json:"appId"`
	Slug          string `json:"slug"`
	ClientID      string `json:"clientId"`
	ClientSecret  string `json:"clientSecret"`
	WebhookSecret string `json:"webhookSecret"`
	PrivateKey    string `json:"pem"`
}

// github talks to GitHub as one app, and as that app's installation.
type github struct {
	web, api string
	http     *http.Client

	mu     sync.Mutex
	tokens map[string]cachedToken // installation id -> token
}

type cachedToken struct {
	value string
	until time.Time
}

func newGitHub(web, api string) *github {
	return &github{
		web:    strings.TrimRight(web, "/"),
		api:    strings.TrimRight(api, "/"),
		http:   &http.Client{Timeout: 30 * time.Second},
		tokens: map[string]cachedToken{},
	}
}

// appJWT proves to GitHub that a request comes from the app itself.
func (g *github) appJWT(s githubSecrets) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(s.PrivateKey))
	if err != nil {
		return "", fmt.Errorf("read the app's private key: %w", err)
	}
	return signAppJWT(key, s.AppID)
}

func signAppJWT(key *rsa.PrivateKey, appID int64) (string, error) {
	now := time.Now()
	// A minute back for clock drift; GitHub refuses anything over ten minutes.
	return jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": fmt.Sprint(appID),
	}).SignedString(key)
}

// installationToken is the app acting in the organization it is installed on.
// They last an hour; one is kept until a few minutes before that.
func (g *github) installationToken(ctx context.Context, s githubSecrets, installation string) (string, error) {
	g.mu.Lock()
	if t, ok := g.tokens[installation]; ok && time.Now().Before(t.until) {
		g.mu.Unlock()
		return t.value, nil
	}
	g.mu.Unlock()

	appToken, err := g.appJWT(s)
	if err != nil {
		return "", err
	}
	var res struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := g.do(ctx, "Bearer "+appToken, http.MethodPost, "/app/installations/"+url.PathEscape(installation)+"/access_tokens", nil, &res); err != nil {
		return "", err
	}
	g.mu.Lock()
	g.tokens[installation] = cachedToken{value: res.Token, until: res.ExpiresAt.Add(-5 * time.Minute)}
	g.mu.Unlock()
	return res.Token, nil
}

func (g *github) forget(installation string) {
	g.mu.Lock()
	delete(g.tokens, installation)
	g.mu.Unlock()
}

// apiError is GitHub saying no, with its status kept so callers can tell a
// missing thing from a refusal.
type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("github answered %d: %s", e.Status, e.Body) }

func isStatus(err error, status int) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == status
}

// do makes one call to GitHub's API. path is relative to the API, or a full address.
func (g *github) do(ctx context.Context, auth, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	address := path
	if !strings.HasPrefix(path, "http") {
		address = g.api + path
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("reach github: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		text := string(raw)
		if len(text) > 300 {
			text = text[:300]
		}
		return &apiError{Status: res.StatusCode, Body: text}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// ── what GitHub sends and answers ─────────────────────────────────────────

type ghUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

type ghRepo struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
	Private  bool   `json:"private"`
}

type ghPull struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	State     string     `json:"state"`
	Draft     bool       `json:"draft"`
	Merged    bool       `json:"merged"`
	HTMLURL   string     `json:"html_url"`
	User      ghUser     `json:"user"`
	CreatedAt time.Time  `json:"created_at"`
	MergedAt  *time.Time `json:"merged_at"`
	ClosedAt  *time.Time `json:"closed_at"`
	Head      struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	RequestedReviewers []ghUser `json:"requested_reviewers"`
}

// state is the one word TeamUp keeps: open, draft, merged or closed.
func (p ghPull) state() string {
	switch {
	case p.Merged || p.MergedAt != nil:
		return "merged"
	case p.State == "closed":
		return "closed"
	case p.Draft:
		return "draft"
	default:
		return "open"
	}
}

// installationRepos lists every repository the installation can see.
func (g *github) installationRepos(ctx context.Context, token string) ([]ghRepo, error) {
	var all []ghRepo
	for page := 1; page <= 50; page++ {
		var res struct {
			Repositories []ghRepo `json:"repositories"`
		}
		if err := g.do(ctx, "token "+token, http.MethodGet, fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), nil, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Repositories...)
		if len(res.Repositories) < 100 {
			break
		}
	}
	return all, nil
}

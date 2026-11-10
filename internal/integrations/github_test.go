package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/tasks"
	"github.com/teamup/teamup/internal/testsupport"
)

// fakeGitHub is GitHub as TeamUp sees it: the manifest conversion, the app's
// installation, its repositories, and people signing in to link accounts.
type fakeGitHub struct {
	server *httptest.Server
	mu     sync.Mutex
	// collaborator changes asked for, as "PUT acme/photon-web alice-gh push"
	collaborators []string
	key           *rsa.PrivateKey
	users         map[string]struct {
		id     int64
		login  string
		emails []string
	} // by OAuth code
}

const webhookSecret = "webhook-secret-from-github"

func newFakeGitHub(t *testing.T) *fakeGitHub {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGitHub{key: key, users: map[string]struct {
		id     int64
		login  string
		emails []string
	}{
		"alice-code": {101, "alice-gh", []string{"alice@home.example"}},
		"bob-code":   {102, "bob-gh", nil},
	}}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	appOnly := func() bool {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return &g.key.PublicKey, nil })
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/repos/") && strings.Contains(r.URL.Path, "/collaborators/") {
		if r.Header.Get("Authorization") != "token installation-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
		var body struct {
			Permission string `json:"permission"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		g.collaborators = append(g.collaborators, strings.TrimSpace(fmt.Sprintf("%s %s/%s %s %s", r.Method, parts[0], parts[1], parts[3], body.Permission)))
		g.mu.Unlock()
		// Somebody outside the organization is sent an invitation; a member is added.
		if r.Method == http.MethodPut && parts[3] == "alice-gh" {
			w.WriteHeader(http.StatusCreated)
			reply(map[string]any{"id": 99})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/app-manifests/") && r.Method == http.MethodPost:
		der := x509.MarshalPKCS1PrivateKey(g.key)
		reply(map[string]any{"id": 4242, "slug": "teamup-acme", "client_id": "Iv1.client", "client_secret": "client-secret",
			"webhook_secret": webhookSecret, "pem": string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})),
			"owner": map[string]any{"login": "acme"}})
	case r.URL.Path == "/app/installations/77" && r.Method == http.MethodGet:
		if appOnly() {
			reply(map[string]any{"id": 77, "app_id": 4242, "account": map[string]any{"login": "acme"}})
		}
	case r.URL.Path == "/app/installations/77/access_tokens":
		if appOnly() {
			reply(map[string]any{"token": "installation-token", "expires_at": "2099-01-01T00:00:00Z"})
		}
	case r.URL.Path == "/installation/repositories":
		if r.Header.Get("Authorization") != "token installation-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		reply(map[string]any{"repositories": []map[string]any{
			{"id": 1, "full_name": "acme/photon-web", "html_url": "https://github.com/acme/photon-web", "private": true},
			{"id": 2, "full_name": "acme/photon-api", "html_url": "https://github.com/acme/photon-api", "private": true},
		}})
	case r.URL.Path == "/login/oauth/access_token":
		_ = r.ParseForm()
		if _, ok := g.users[r.Form.Get("code")]; !ok || r.Form.Get("client_secret") != "client-secret" {
			reply(map[string]any{"error": "bad_verification_code"})
			return
		}
		reply(map[string]any{"access_token": "user-" + r.Form.Get("code")})
	case r.URL.Path == "/user" || r.URL.Path == "/user/emails":
		code := strings.TrimPrefix(r.Header.Get("Authorization"), "token user-")
		u, ok := g.users[code]
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/user" {
			reply(map[string]any{"id": u.id, "login": u.login})
			return
		}
		list := []map[string]any{}
		for _, e := range u.emails {
			list = append(list, map[string]any{"email": e, "verified": true})
		}
		reply(list)
	default:
		http.NotFound(w, r)
	}
}

type told struct {
	who  uuid.UUID
	kind string
	what string
}

type recorder struct {
	mu   sync.Mutex
	sent []told
}

func (r *recorder) Notify(_ context.Context, _, userID uuid.UUID, kind, title, _ string, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, told{userID, kind, title})
	return nil
}

func (r *recorder) count(who uuid.UUID, kind string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, t := range r.sent {
		if t.who == who && t.kind == kind {
			n++
		}
	}
	return n
}

func sign(body []byte) string {
	m := hmac.New(sha256.New, []byte(webhookSecret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestGitHubFromConnectingToAFinishedTask(t *testing.T) {
	pool := testsupport.DB(t)
	org := testsupport.SeedOrg(t, pool)
	alice := testsupport.SeedUser(t, pool, org, "alice@acme.example", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob@acme.example", "member")
	ctx := context.Background()
	g := newFakeGitHub(t)

	taskService := tasks.NewService(pool)
	told := &recorder{}
	taskService.SetNotifier(told)
	s := NewService(pool, Config{BaseURL: "https://teamup.example", Secret: "a-server-secret-long-enough",
		GitHubWeb: g.server.URL, GitHubAPI: g.server.URL}, taskService)
	s.SetNotifier(told)
	s.SetLogger(zerolog.New(zerolog.NewTestWriter(t)))

	// The admin makes the app on GitHub and installs it.
	action, manifest, nonce, err := s.GitHubManifest(ctx, org.ID, org.OwnerID, "https://github.com/acme")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(action, g.server.URL+"/organizations/acme/settings/apps/new?state=") {
		t.Fatalf("the manifest goes to the organization's new-app page: %s", action)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(manifest), &m)
	hook := m["hook_attributes"].(map[string]any)["url"].(string)
	hookKey := hook[strings.LastIndex(hook, "/")+1:]
	state, _ := url.Parse(action)
	if _, err := s.GitHubCreated(ctx, state.Query().Get("state"), "another browser", "code"); err == nil {
		t.Fatal("the way back is refused in a browser that did not start it")
	}
	next, err := s.GitHubCreated(ctx, state.Query().Get("state"), nonce, "code")
	if err != nil {
		t.Fatal(err)
	}
	if next != g.server.URL+"/apps/teamup-acme/installations/new" {
		t.Errorf("then on to installing it: %s", next)
	}
	if err := s.GitHubInstalled(ctx, hookKey, "77"); err != nil {
		t.Fatal(err)
	}
	repos, _ := s.Repositories(ctx, org.ID)
	if len(repos) != 2 {
		t.Fatalf("the installation's repositories are listed: %+v", repos)
	}

	// Photon is two repositories, and its tasks are keyed PHO.
	project, err := taskService.CreateProject(ctx, org.ID, org.OwnerID, "Photon", "pho", "")
	if err != nil || project.Key != "PHO" {
		t.Fatalf("project: %+v %v", project, err)
	}
	if err := s.SetProjectRepositories(ctx, org.ID, project.ID, []uuid.UUID{repos[0].ID, repos[1].ID}); err != nil {
		t.Fatal(err)
	}
	task, err := taskService.CreateTask(ctx, org.ID, org.OwnerID, "Fix sign-in", "", "", "", "", &project.ID, &alice, nil, nil, nil, true)
	if err != nil || task.Key != "PHO-1" {
		t.Fatalf("the first Photon task is PHO-1: %+v %v", task, err)
	}
	second, _ := taskService.CreateTask(ctx, org.ID, org.OwnerID, "Rate limits", "", "", "", "", &project.ID, &alice, nil, nil, nil, true)
	if second.Key != "PHO-2" {
		t.Fatalf("then PHO-2: %s", second.Key)
	}

	// Each links their GitHub account.
	for _, who := range []struct {
		id   uuid.UUID
		code string
	}{{alice, "alice-code"}, {bob, "bob-code"}} {
		address, nonce, err := s.GitHubIdentityURL(ctx, org.ID, who.id)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(address)
		if err := s.GitHubIdentityDone(ctx, u.Query().Get("state"), nonce, who.code); err != nil {
			t.Fatal(err)
		}
	}

	event := func(kind, delivery string, payload any) error {
		body, _ := json.Marshal(payload)
		return s.GitHubEvent(ctx, hookKey, kind, delivery, sign(body), body)
	}
	repo := map[string]any{"id": 1, "full_name": "acme/photon-web", "html_url": "https://github.com/acme/photon-web", "private": true}
	pull := func(state string, merged bool) map[string]any {
		return map[string]any{"number": 7, "title": "Fix sign-in", "body": "", "state": state, "merged": merged,
			"html_url": "https://github.com/acme/photon-web/pull/7", "user": map[string]any{"login": "alice-gh"},
			"head": map[string]any{"ref": "pho-1-fix-sign-in"}, "base": map[string]any{"ref": "main"},
			"requested_reviewers": []any{map[string]any{"login": "bob-gh"}}}
	}

	// Somebody else's event, or a forged one, is refused.
	body, _ := json.Marshal(map[string]any{"action": "opened"})
	if err := s.GitHubEvent(ctx, hookKey, "pull_request", "x", "sha256=00", body); err != ErrBadSignature {
		t.Errorf("a forged event is refused: %v", err)
	}

	// Opened for review: PHO-1 (named by its branch) is in review, and Bob is asked.
	if err := event("pull_request", "d1", map[string]any{"action": "opened", "pull_request": pull("open", false), "repository": repo, "sender": map[string]any{"login": "alice-gh"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := taskService.GetTask(ctx, org.ID, task.ID); got.Status != "in_review" {
		t.Errorf("a pull request sent for review moves the task to review: %s", got.Status)
	}
	if told.count(bob, "code.review_requested") != 1 {
		t.Errorf("Bob is told his review is wanted: %+v", told.sent)
	}
	// GitHub sends it again: nobody is told twice.
	_ = event("pull_request", "d1", map[string]any{"action": "opened", "pull_request": pull("open", false), "repository": repo, "sender": map[string]any{"login": "alice-gh"}})
	if told.count(bob, "code.review_requested") != 1 {
		t.Errorf("a repeated delivery is acted on once")
	}

	// Bob approves; Alice hears. The checks fail; Alice hears that too.
	_ = event("pull_request_review", "d2", map[string]any{"action": "submitted", "review": map[string]any{"state": "approved", "user": map[string]any{"login": "bob-gh"}}, "pull_request": pull("open", false), "repository": repo})
	if told.count(alice, "code.review") != 1 {
		t.Errorf("Alice is told of Bob's approval: %+v", told.sent)
	}
	_ = event("check_suite", "d3", map[string]any{"action": "completed", "check_suite": map[string]any{"conclusion": "failure", "pull_requests": []any{map[string]any{"number": 7}}}, "repository": repo})
	if told.count(alice, "code.checks_failed") != 1 {
		t.Errorf("Alice is told her checks failed")
	}

	// A commit naming PHO-2, by Alice's own email: PHO-2 is under way, and the commit is hers.
	_ = event("push", "d4", map[string]any{"ref": "refs/heads/pho-2-limits", "repository": repo, "commits": []any{map[string]any{
		"id": "abc123", "message": "Start on rate limits", "url": "https://github.com/acme/photon-web/commit/abc123", "timestamp": "2026-11-10T10:00:00Z",
		"author": map[string]any{"name": "Alice", "email": "alice@home.example", "username": ""}}}})
	if got, _ := taskService.GetTask(ctx, org.ID, second.ID); got.Status != "in_progress" {
		t.Errorf("a commit on PHO-2's branch puts it in progress: %s", got.Status)
	}

	// Merged: PHO-1 is done.
	merged := pull("closed", true)
	merged["merged_at"] = "2026-11-10T12:00:00Z"
	_ = event("pull_request", "d5", map[string]any{"action": "closed", "pull_request": merged, "repository": repo, "sender": map[string]any{"login": "bob-gh"}})
	if got, _ := taskService.GetTask(ctx, org.ID, task.ID); got.Status != "done" {
		t.Errorf("merging finishes the task: %s", got.Status)
	}

	code, err := s.ForTask(ctx, org.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(code.PullRequests) != 1 || code.PullRequests[0].State != "merged" || code.PullRequests[0].Author.UserID == nil ||
		*code.PullRequests[0].Author.UserID != alice || len(code.PullRequests[0].Reviewers) != 1 || code.PullRequests[0].Reviewers[0].State != "approved" ||
		code.PullRequests[0].Checks != "failing" {
		t.Errorf("the task shows its pull request, who made it and who approved it: %+v", code.PullRequests)
	}
	commits, _ := s.ForTask(ctx, org.ID, second.ID)
	if len(commits.Commits) != 1 || commits.Commits[0].Author.UserID == nil || *commits.Commits[0].Author.UserID != alice {
		t.Errorf("PHO-2 shows Alice's commit: %+v", commits.Commits)
	}

	// Code never moves a task backwards, nor a blocked one at all.
	blocked, _ := taskService.CreateTask(ctx, org.ID, org.OwnerID, "Waiting", "", "blocked", "", "", &project.ID, nil, nil, nil, nil, true)
	_ = event("push", "d6", map[string]any{"ref": "refs/heads/main", "repository": repo, "commits": []any{map[string]any{
		"id": "def456", "message": fmt.Sprintf("Touch %s and PHO-1", blocked.Key), "timestamp": "2026-11-10T13:00:00Z", "author": map[string]any{"username": "bob-gh"}}}})
	if got, _ := taskService.GetTask(ctx, org.ID, blocked.ID); got.Status != "blocked" {
		t.Errorf("a blocked task stays blocked: %s", got.Status)
	}
	if got, _ := taskService.GetTask(ctx, org.ID, task.ID); got.Status != "done" {
		t.Errorf("a finished task is not reopened by a later commit: %s", got.Status)
	}
}

func TestKeysAreFoundWhereverTheyAreWritten(t *testing.T) {
	got := KeysIn("PHO-12: fix login", "feature/pho-12-login", "see BOS-3 and pho-12", "UTF-8 x", "nothing here")
	want := []string{"PHO-12", "BOS-3", "UTF-8"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v (UTF-8 is found but names no task, so links nothing)", got, want)
	}
}

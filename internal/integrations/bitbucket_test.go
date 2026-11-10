package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/tasks"
	"github.com/teamup/teamup/internal/testsupport"
)

// fakeBitbucket is Bitbucket as TeamUp sees it: the consumer's token endpoint,
// the workspace (with who owns it), its webhook and repositories, and people.
type fakeBitbucket struct {
	server *httptest.Server
	mu     sync.Mutex
	owner  bool
	hooks  []map[string]any
	// refresh tokens still good; each refresh retires the one used.
	refresh map[string]bool
	next    int
}

var bbPeople = map[string]bbAccount{
	"owner-code": {UUID: "{owner}", AccountID: "557058:owner", Nickname: "olivia", DisplayName: "Olivia Owner"},
	"alice-code": {UUID: "{alice}", AccountID: "557058:alice", Nickname: "alice-bb", DisplayName: "Alice A"},
	"bob-code":   {UUID: "{bob}", AccountID: "557058:bob", Nickname: "bob-bb", DisplayName: "Bob B"},
}

func newFakeBitbucket(t *testing.T) *fakeBitbucket {
	b := &fakeBitbucket{owner: true, refresh: map[string]bool{}}
	b.server = httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(b.server.Close)
	return b
}

func (b *fakeBitbucket) serve(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	who := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer access-")
	switch {
	case r.URL.Path == "/site/oauth2/access_token":
		key, secret, _ := r.BasicAuth()
		_ = r.ParseForm()
		if key != "consumer-key" || secret != "consumer-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			reply(map[string]any{"error": "invalid_client"})
			return
		}
		code := r.Form.Get("code")
		if r.Form.Get("grant_type") == "refresh_token" {
			if !b.refresh[r.Form.Get("refresh_token")] {
				w.WriteHeader(http.StatusBadRequest)
				reply(map[string]any{"error": "invalid_grant"})
				return
			}
			delete(b.refresh, r.Form.Get("refresh_token"))
			code = "owner-code"
		}
		b.next++
		rt := "refresh-" + strings.Repeat("x", b.next)
		b.refresh[rt] = true
		// expires at once, so every use refreshes
		reply(map[string]any{"access_token": "access-" + code, "refresh_token": rt, "expires_in": 1})
	case r.URL.Path == "/2.0/user/workspaces/acme/permission":
		perm := "member"
		if b.owner && who == "owner-code" {
			perm = "owner"
		}
		reply(map[string]any{"permission": perm})
	case r.URL.Path == "/2.0/workspaces/acme/hooks" && r.Method == http.MethodPost:
		var hook map[string]any
		_ = json.NewDecoder(r.Body).Decode(&hook)
		hook["uuid"] = "{hook}"
		b.hooks = append(b.hooks, hook)
		reply(hook)
	case r.URL.Path == "/2.0/workspaces/acme/hooks/{hook}" && r.Method == http.MethodDelete:
		b.hooks = nil
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/2.0/repositories/acme":
		reply(map[string]any{"values": []map[string]any{
			{"uuid": "{r1}", "full_name": "acme/cortex", "is_private": true, "links": map[string]any{"html": map[string]any{"href": "https://bitbucket.org/acme/cortex"}}},
		}})
	case r.URL.Path == "/2.0/user":
		if p, ok := bbPeople[who]; ok {
			reply(p)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	case r.URL.Path == "/2.0/user/emails":
		values := []map[string]any{}
		if who == "alice-code" {
			values = append(values, map[string]any{"email": "Alice@Home.example", "is_confirmed": true})
		}
		reply(map[string]any{"values": values})
	default:
		http.NotFound(w, r)
	}
}

func TestBitbucketFromConnectingToAFinishedTask(t *testing.T) {
	pool := testsupport.DB(t)
	org := testsupport.SeedOrg(t, pool)
	alice := testsupport.SeedUser(t, pool, org, "alice@acme.example", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob@acme.example", "member")
	ctx := context.Background()
	bb := newFakeBitbucket(t)

	taskService := tasks.NewService(pool)
	told := &recorder{}
	taskService.SetNotifier(told)
	s := NewService(pool, Config{BaseURL: "https://teamup.example", Secret: "a-server-secret-long-enough",
		BitbucketWeb: bb.server.URL, BitbucketAPI: bb.server.URL}, taskService)
	s.SetNotifier(told)
	s.SetLogger(zerolog.New(zerolog.NewTestWriter(t)))

	signIn := func(address, nonce, code string) (string, error) {
		u, _ := url.Parse(address)
		if u.Query().Get("client_id") != "consumer-key" {
			t.Fatalf("Bitbucket is asked with the consumer's key: %s", address)
		}
		return s.BitbucketDone(ctx, u.Query().Get("state"), nonce, code)
	}

	// Somebody who does not own the workspace cannot connect it.
	address, nonce, err := s.BitbucketStart(ctx, org.ID, org.OwnerID, "https://bitbucket.org/acme/", "consumer-key", "consumer-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signIn(address, nonce, "alice-code"); err == nil {
		t.Fatal("a workspace member who is not its owner is refused")
	}
	// Its owner can.
	address, nonce, _ = s.BitbucketStart(ctx, org.ID, org.OwnerID, "acme", "consumer-key", "consumer-secret")
	if purpose, err := signIn(address, nonce, "owner-code"); err != nil || purpose != "connect" {
		t.Fatalf("the owner connects it: %s %v", purpose, err)
	}
	if len(bb.hooks) != 1 || !strings.HasPrefix(bb.hooks[0]["url"].(string), "https://teamup.example/api/integrations/bitbucket/hook/") {
		t.Fatalf("a workspace webhook pointing at TeamUp is added: %+v", bb.hooks)
	}
	hook := bb.hooks[0]["url"].(string)
	hookKey := hook[strings.LastIndex(hook, "/")+1:]
	secret := bb.hooks[0]["secret"].(string)
	repos, _ := s.Repositories(ctx, org.ID)
	if len(repos) != 1 || repos[0].Provider != "bitbucket" || repos[0].FullName != "acme/cortex" {
		t.Fatalf("the workspace's repositories are listed: %+v", repos)
	}
	// Tokens turn over; a refresh after the first works, which only a stored new refresh token allows.
	if err := s.SyncNow(ctx, org.ID, "bitbucket"); err != nil {
		t.Fatalf("listing again refreshes the owner's sign-in: %v", err)
	}
	if err := s.SyncNow(ctx, org.ID, "bitbucket"); err != nil {
		t.Fatalf("and again, with the refresh token handed back last time: %v", err)
	}

	project, _ := taskService.CreateProject(ctx, org.ID, org.OwnerID, "Cortex", "COR", "")
	_ = s.SetProjectRepositories(ctx, org.ID, project.ID, []uuid.UUID{repos[0].ID})
	task, _ := taskService.CreateTask(ctx, org.ID, org.OwnerID, "Search ranking", "", "", "", "", &project.ID, &alice, nil, nil, nil, true)

	for _, who := range []struct {
		id   uuid.UUID
		code string
	}{{alice, "alice-code"}, {bob, "bob-code"}} {
		address, nonce, err := s.BitbucketIdentityURL(ctx, org.ID, who.id)
		if err != nil {
			t.Fatal(err)
		}
		if purpose, err := signIn(address, nonce, who.code); err != nil || purpose != "identity" {
			t.Fatalf("linking: %s %v", purpose, err)
		}
	}

	event := func(kind, delivery string, payload any) error {
		raw, _ := json.Marshal(payload)
		return s.BitbucketEvent(ctx, hookKey, kind, delivery, signWith(secret, raw), raw)
	}
	repo := map[string]any{"uuid": "{r1}", "full_name": "acme/cortex", "is_private": true, "links": map[string]any{"html": map[string]any{"href": "https://bitbucket.org/acme/cortex"}}}
	account := func(p bbAccount) map[string]any {
		return map[string]any{"uuid": p.UUID, "account_id": p.AccountID, "nickname": p.Nickname, "display_name": p.DisplayName}
	}
	pull := func(state string, reviewers ...bbAccount) map[string]any {
		list := []any{}
		for _, r := range reviewers {
			list = append(list, account(r))
		}
		return map[string]any{"id": 3, "title": "COR-1 better ranking", "state": state, "author": account(bbPeople["alice-code"]),
			"source": map[string]any{"branch": map[string]any{"name": "feature/ranking"}}, "destination": map[string]any{"branch": map[string]any{"name": "main"}},
			"reviewers": list, "links": map[string]any{"html": map[string]any{"href": "https://bitbucket.org/acme/cortex/pull-requests/3"}},
			"created_on": "2026-11-10T09:00:00Z", "updated_on": "2026-11-10T09:00:00Z"}
	}

	body, _ := json.Marshal(map[string]any{})
	if err := s.BitbucketEvent(ctx, hookKey, "pullrequest:created", "x", "sha256=00", body); err != ErrBadSignature {
		t.Errorf("a forged event is refused: %v", err)
	}

	// Opened with nobody to review: in progress.
	_ = event("pullrequest:created", "e1", map[string]any{"actor": account(bbPeople["alice-code"]), "pullrequest": pull("OPEN"), "repository": repo})
	if got, _ := taskService.GetTask(ctx, org.ID, task.ID); got.Status != "in_progress" {
		t.Errorf("opened, the task is in progress: %s", got.Status)
	}
	// Bob added as reviewer: Bitbucket only says the pull request was updated.
	_ = event("pullrequest:updated", "e2", map[string]any{"actor": account(bbPeople["alice-code"]), "pullrequest": pull("OPEN", bbPeople["bob-code"]), "repository": repo})
	if told.count(bob, "code.review_requested") != 1 {
		t.Errorf("Bob is told his review is wanted: %+v", told.sent)
	}
	if got, _ := taskService.GetTask(ctx, org.ID, task.ID); got.Status != "in_review" {
		t.Errorf("waiting for review: %s", got.Status)
	}
	// Updated again with Bob still there: he is not told twice.
	_ = event("pullrequest:updated", "e3", map[string]any{"actor": account(bbPeople["alice-code"]), "pullrequest": pull("OPEN", bbPeople["bob-code"]), "repository": repo})
	if told.count(bob, "code.review_requested") != 1 {
		t.Errorf("an unchanged reviewer is asked once")
	}
	// Bob approves; the build fails; Alice hears both.
	_ = event("pullrequest:approved", "e4", map[string]any{"actor": account(bbPeople["bob-code"]), "approval": map[string]any{"user": account(bbPeople["bob-code"])}, "pullrequest": pull("OPEN", bbPeople["bob-code"]), "repository": repo})
	if told.count(alice, "code.review") != 1 {
		t.Errorf("Alice is told of the approval: %+v", told.sent)
	}
	_ = event("repo:commit_status_updated", "e5", map[string]any{"commit_status": map[string]any{"state": "FAILED", "refname": "feature/ranking"}, "repository": repo})
	if told.count(alice, "code.checks_failed") != 1 {
		t.Errorf("Alice is told the build failed")
	}
	// Bob mentions Alice in a comment.
	_ = event("pullrequest:comment_created", "e6", map[string]any{"actor": account(bbPeople["bob-code"]),
		"comment": map[string]any{"content": map[string]any{"raw": "@{557058:alice} nearly there"}}, "pullrequest": pull("OPEN"), "repository": repo})
	if told.count(alice, "code.mention") != 1 {
		t.Errorf("Alice is told she was mentioned")
	}
	// A commit by Alice's email lands on the branch.
	_ = event("repo:push", "e7", map[string]any{"repository": repo, "push": map[string]any{"changes": []any{map[string]any{
		"new": map[string]any{"type": "branch", "name": "feature/ranking"},
		"commits": []any{map[string]any{"hash": "aaa111", "message": "COR-1 tune weights", "date": "2026-11-10T10:00:00Z",
			"links":  map[string]any{"html": map[string]any{"href": "https://bitbucket.org/acme/cortex/commits/aaa111"}},
			"author": map[string]any{"raw": "Alice <alice@home.example>"}}}}}}})
	// Merged: done.
	_ = event("pullrequest:fulfilled", "e8", map[string]any{"actor": account(bbPeople["bob-code"]), "pullrequest": pull("MERGED", bbPeople["bob-code"]), "repository": repo})
	if got, _ := taskService.GetTask(ctx, org.ID, task.ID); got.Status != "done" {
		t.Errorf("merged, the task is done: %s", got.Status)
	}

	code, _ := s.ForTask(ctx, org.ID, task.ID)
	if len(code.PullRequests) != 1 || code.PullRequests[0].State != "merged" || code.PullRequests[0].Author.UserID == nil ||
		*code.PullRequests[0].Author.UserID != alice || len(code.PullRequests[0].Reviewers) != 1 ||
		code.PullRequests[0].Reviewers[0].State != "approved" || code.PullRequests[0].Reviewers[0].UserID == nil {
		t.Errorf("the task shows the pull request as Bitbucket has it: %+v", code.PullRequests)
	}
	if len(code.Commits) != 1 || code.Commits[0].Author.UserID == nil || *code.Commits[0].Author.UserID != alice {
		t.Errorf("Alice's commit, matched by her confirmed address: %+v", code.Commits)
	}

	// Disconnecting takes the webhook off the workspace.
	if err := s.Disconnect(ctx, org.ID, "bitbucket"); err != nil {
		t.Fatal(err)
	}
	if len(bb.hooks) != 0 {
		t.Errorf("the webhook is removed")
	}
}

func signWith(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

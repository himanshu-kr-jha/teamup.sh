package integrations

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/tasks"
	"github.com/teamup/teamup/internal/testsupport"
)

// Reported: access to the company's repositories was given and taken away on
// GitHub and Bitbucket by hand, with no record of who asked or who agreed.
func TestAccessIsAskedForApprovedGivenOnEveryRepositoryAndTakenAway(t *testing.T) {
	pool := testsupport.DB(t)
	org := testsupport.SeedOrg(t, pool)
	alice := testsupport.SeedUser(t, pool, org, "alice@acme.example", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob@acme.example", "member")
	ctx := context.Background()
	gh := newFakeGitHub(t)
	bb := newFakeBitbucket(t)

	taskService := tasks.NewService(pool)
	told := &recorder{}
	s := NewService(pool, Config{BaseURL: "https://teamup.example", Secret: "a-server-secret-long-enough",
		GitHubWeb: gh.server.URL, GitHubAPI: gh.server.URL, BitbucketWeb: bb.server.URL, BitbucketAPI: bb.server.URL}, taskService)
	s.SetNotifier(told)
	s.SetLogger(zerolog.New(zerolog.NewTestWriter(t)))

	// GitHub, and Bitbucket, connected.
	action, manifest, nonce, err := s.GitHubManifest(ctx, org.ID, org.OwnerID, "acme")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(manifest), &m)
	hook := m["hook_attributes"].(map[string]any)["url"].(string)
	ghHook := hook[strings.LastIndex(hook, "/")+1:]
	u, _ := url.Parse(action)
	if _, err := s.GitHubCreated(ctx, u.Query().Get("state"), nonce, "code"); err != nil {
		t.Fatal(err)
	}
	if err := s.GitHubInstalled(ctx, ghHook, "77"); err != nil {
		t.Fatal(err)
	}
	address, nonce, _ := s.BitbucketStart(ctx, org.ID, org.OwnerID, "acme", "consumer-key", "consumer-secret")
	u, _ = url.Parse(address)
	if _, err := s.BitbucketDone(ctx, u.Query().Get("state"), nonce, "owner-code"); err != nil {
		t.Fatal(err)
	}

	// Photon is photon-web on GitHub and cortex on Bitbucket.
	project, _ := taskService.CreateProject(ctx, org.ID, org.OwnerID, "Photon", "PHO", "")
	repos, _ := s.Repositories(ctx, org.ID)
	var ids []uuid.UUID
	for _, r := range repos {
		if r.FullName == "acme/photon-web" || r.FullName == "acme/cortex" {
			ids = append(ids, r.ID)
		}
	}
	if err := s.SetProjectRepositories(ctx, org.ID, project.ID, ids); err != nil {
		t.Fatal(err)
	}

	// Alice links both accounts; Bob only GitHub.
	link := func(user uuid.UUID, provider, code string) {
		var address, nonce string
		var err error
		if provider == "github" {
			address, nonce, err = s.GitHubIdentityURL(ctx, org.ID, user)
		} else {
			address, nonce, err = s.BitbucketIdentityURL(ctx, org.ID, user)
		}
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(address)
		if provider == "github" {
			err = s.GitHubIdentityDone(ctx, u.Query().Get("state"), nonce, code)
		} else {
			_, err = s.BitbucketDone(ctx, u.Query().Get("state"), nonce, code)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	link(alice, "github", "alice-code")
	link(alice, "bitbucket", "alice-code")
	link(bob, "github", "bob-code")

	// Alice asks for write access. The owner, who administers, is told.
	req, err := s.RequestAccess(ctx, org.ID, project.ID, alice, "write", "Fixing the sign-in")
	if err != nil {
		t.Fatal(err)
	}
	if told.count(org.OwnerID, "access.requested") != 1 {
		t.Errorf("administrators are told of the request: %+v", told.sent)
	}
	if _, err := s.RequestAccess(ctx, org.ID, project.ID, alice, "write", "again"); err == nil {
		t.Error("one open request per person and project")
	}
	mine, _ := s.ProjectAccess(ctx, org.ID, project.ID, bob, false)
	if len(mine.Requests) != 0 {
		t.Errorf("somebody else sees only their own requests: %+v", mine.Requests)
	}
	if !slices.Contains(mine.Unlinked, "bitbucket") {
		t.Errorf("Bob is told he has no Bitbucket account linked: %+v", mine.Unlinked)
	}

	// Approved: GitHub invites her (she is outside the GitHub organization),
	// Bitbucket gives her the repository there and then.
	grant, err := s.Decide(ctx, org.ID, req.ID, org.OwnerID, true, "")
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, r := range grant.Repos {
		status[r.Repository] = r.Status
	}
	if status["acme/photon-web"] != "invited" || status["acme/cortex"] != "granted" {
		t.Errorf("each repository says how it went: %+v", grant.Repos)
	}
	if !slices.Contains(gh.collaborators, "PUT acme/photon-web alice-gh push") || !slices.Contains(bb.permissions, "PUT acme/cortex {alice} write") {
		t.Errorf("the providers were asked: github %v, bitbucket %v", gh.collaborators, bb.permissions)
	}
	if told.count(alice, "access.granted") != 1 {
		t.Errorf("Alice is told she has access")
	}

	// Bob asks to read, and is declined; then an administrator gives him read
	// access anyway. Bitbucket cannot, as he has no account linked there.
	bobReq, _ := s.RequestAccess(ctx, org.ID, project.ID, bob, "read", "")
	if _, err := s.Decide(ctx, org.ID, bobReq.ID, org.OwnerID, false, "Not this quarter"); err != nil {
		t.Fatal(err)
	}
	if told.count(bob, "access.declined") != 1 {
		t.Errorf("Bob is told he was declined")
	}
	if _, err := s.Decide(ctx, org.ID, bobReq.ID, org.OwnerID, true, ""); err == nil {
		t.Error("a request is answered once")
	}
	bobGrant, err := s.Grant(ctx, org.ID, project.ID, bob, org.OwnerID, "read")
	if err != nil {
		t.Fatal(err)
	}
	bobStatus := map[string]RepoAccess{}
	for _, r := range bobGrant.Repos {
		bobStatus[r.Repository] = r
	}
	if bobStatus["acme/photon-web"].Status != "granted" || bobStatus["acme/cortex"].Status != "failed" ||
		!strings.Contains(bobStatus["acme/cortex"].Detail, "not linked a Bitbucket account") {
		t.Errorf("GitHub gives Bob read; Bitbucket says why it cannot: %+v", bobGrant.Repos)
	}

	// Alice's access is taken away, on both.
	if err := s.Revoke(ctx, org.ID, grant.ID, org.OwnerID); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(gh.collaborators, "DELETE acme/photon-web alice-gh") || !slices.Contains(bb.permissions, "DELETE acme/cortex {alice}") {
		t.Errorf("both providers are told: github %v, bitbucket %v", gh.collaborators, bb.permissions)
	}

	// Somebody added on GitHub directly is recorded; TeamUp's own changes are not news.
	send := func(payload any) {
		raw, _ := json.Marshal(payload)
		if err := s.GitHubEvent(ctx, ghHook, "member", uuid.NewString(), sign(raw), raw); err != nil {
			t.Fatal(err)
		}
	}
	repo := map[string]any{"id": 1, "full_name": "acme/photon-web"}
	send(map[string]any{"action": "added", "member": map[string]any{"login": "bob-gh"}, "sender": map[string]any{"login": "teamup-acme[bot]", "type": "Bot"}, "repository": repo})
	send(map[string]any{"action": "added", "member": map[string]any{"login": "stranger"}, "sender": map[string]any{"login": "some-admin", "type": "User"}, "repository": repo})

	entries, err := s.AccessLog(ctx, org.ID, &project.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for i := len(entries) - 1; i >= 0; i-- {
		actions = append(actions, entries[i].Action)
	}
	want := []string{"requested", "approved", "granted", "requested", "declined", "granted", "failed", "revoked", "outside_added"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Errorf("the record, in order:\n got %v\nwant %v", actions, want)
	}
	view, _ := s.ProjectAccess(ctx, org.ID, project.ID, org.OwnerID, true)
	if len(view.Grants) != 1 || view.Grants[0].UserID != bob || view.Grants[0].Level != "read" {
		t.Errorf("only Bob's read access is left: %+v", view.Grants)
	}
}

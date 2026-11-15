package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/tasks"
	"github.com/teamup/teamup/internal/testsupport"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

// Reported: research and bug reports were filed with no project, and once they
// became a project's work a second task was made for it, so the story was split
// across two tickets and the branch was named by hand. A task now moves into its
// project, keeping its old key, and starts its own branch, for whoever may push
// to the repository.
func TestATaskMovesIntoAProjectAndStartsItsBranch(t *testing.T) {
	pool := testsupport.DB(t)
	org := testsupport.SeedOrg(t, pool)
	alice := testsupport.SeedUser(t, pool, org, "alice@acme.example", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob@acme.example", "member")
	ctx := context.Background()
	gh := newFakeGitHub(t)
	bb := newFakeBitbucket(t)

	taskService := tasks.NewService(pool)
	s := NewService(pool, Config{BaseURL: "https://teamup.example", Secret: "a-server-secret-long-enough",
		GitHubWeb: gh.server.URL, GitHubAPI: gh.server.URL, BitbucketWeb: bb.server.URL, BitbucketAPI: bb.server.URL}, taskService)
	s.SetNotifier(&recorder{})
	s.SetLogger(zerolog.New(zerolog.NewTestWriter(t)))

	action, manifest, nonce, err := s.GitHubManifest(ctx, org.ID, org.OwnerID, "acme")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(manifest), &m)
	hook := m["hook_attributes"].(map[string]any)["url"].(string)
	u, _ := url.Parse(action)
	if _, err := s.GitHubCreated(ctx, u.Query().Get("state"), nonce, "code"); err != nil {
		t.Fatal(err)
	}
	if err := s.GitHubInstalled(ctx, hook[strings.LastIndex(hook, "/")+1:], "77"); err != nil {
		t.Fatal(err)
	}
	address, nonce, _ := s.BitbucketStart(ctx, org.ID, org.OwnerID, "acme", "consumer-key", "consumer-secret")
	u, _ = url.Parse(address)
	if _, err := s.BitbucketDone(ctx, u.Query().Get("state"), nonce, "owner-code"); err != nil {
		t.Fatal(err)
	}
	project, _ := taskService.CreateProject(ctx, org.ID, org.OwnerID, "Photon", "PHO", "")
	repos, _ := s.Repositories(ctx, org.ID)
	byName := map[string]uuid.UUID{}
	for _, r := range repos {
		byName[r.FullName] = r.ID
	}
	web, cortex := byName["acme/photon-web"], byName["acme/cortex"]
	if err := s.SetProjectRepositories(ctx, org.ID, project.ID, []uuid.UUID{web, cortex}); err != nil {
		t.Fatal(err)
	}
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

	// Research, filed with no project.
	task, err := taskService.CreateTask(ctx, org.ID, bob, "Research flaky sign-in", "", "", "", "", nil, &bob, nil, nil, nil, false)
	if err != nil || !strings.HasPrefix(task.Key, tasks.DefaultKeyPrefix+"-") {
		t.Fatalf("a task with no project has the plain key: %+v %v", task, err)
	}
	oldKey := task.Key
	if _, err := s.StartBranch(ctx, org.ID, task.ID, bob, web, ""); err == nil || !strings.Contains(err.Error(), "project first") {
		t.Fatalf("a task with no project has no repositories to branch on: %v", err)
	}

	// It is Photon's work now, and takes Photon's key.
	moved, err := taskService.MoveToProject(ctx, org.ID, task.ID, bob, project.ID)
	if err != nil || moved.Key != "PHO-1" || moved.ProjectID == nil || *moved.ProjectID != project.ID {
		t.Fatalf("moved into Photon as PHO-1: %+v %v", moved, err)
	}
	for _, key := range []string{"PHO-1", oldKey, strings.ToLower(oldKey)} {
		if found, err := taskService.TaskByKey(ctx, org.ID, key); err != nil || found.ID != task.ID {
			t.Errorf("%s still finds the task: %+v %v", key, found, err)
		}
	}
	var history string
	_ = pool.QueryRow(ctx, `SELECT old_value || ' -> ' || new_value FROM task_activities WHERE task_id = $1 AND field = 'project'`, task.ID).Scan(&history)
	if history != oldKey+" -> Photon as PHO-1" {
		t.Errorf("the move is in its history: %q", history)
	}
	if again, err := taskService.MoveToProject(ctx, org.ID, task.ID, bob, project.ID); err != nil || again.Key != "PHO-1" {
		t.Errorf("moving it where it is changes nothing: %+v %v", again, err)
	}

	// Bob may not push to Photon yet.
	if _, err := s.StartBranch(ctx, org.ID, task.ID, bob, web, ""); !errors.Is(err, ErrNeedsWriteAccess) {
		t.Fatalf("no write access, no branch: %v", err)
	}
	// Read access is not enough either.
	readGrant, err := s.Grant(ctx, org.ID, project.ID, bob, org.OwnerID, "read")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartBranch(ctx, org.ID, task.ID, bob, web, ""); !errors.Is(err, ErrNeedsWriteAccess) {
		t.Fatalf("read access is not enough: %v", err)
	}
	if err := s.Revoke(ctx, org.ID, readGrant.ID, org.OwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(ctx, org.ID, project.ID, bob, org.OwnerID, "write"); err != nil {
		t.Fatal(err)
	}

	branch, err := s.StartBranch(ctx, org.ID, task.ID, bob, web, "")
	if err != nil {
		t.Fatal(err)
	}
	if branch.Name != "pho-1-research-flaky-sign-in" || branch.Base != "main" || branch.Repository != "acme/photon-web" ||
		branch.URL != "https://github.com/acme/photon-web/tree/pho-1-research-flaky-sign-in" {
		t.Errorf("the branch, from the default branch: %+v", branch)
	}
	if !slices.Contains(gh.branches, "acme/photon-web pho-1-research-flaky-sign-in from sha-main") {
		t.Errorf("made on GitHub: %v", gh.branches)
	}
	if now, _ := taskService.GetTask(ctx, org.ID, task.ID); now.Status != "in_progress" {
		t.Errorf("starting its branch puts it in progress: %s", now.Status)
	}
	// Pressed again, or made by hand already: linked as it is.
	if _, err := s.StartBranch(ctx, org.ID, task.ID, bob, web, ""); err != nil {
		t.Errorf("a branch there already is no error: %v", err)
	}
	code, _ := s.ForTask(ctx, org.ID, task.ID)
	if len(code.Branches) != 1 || code.Branches[0].By.Name == "" {
		t.Errorf("the task shows its branch once, and who started it: %+v", code.Branches)
	}
	if _, err := s.StartBranch(ctx, org.ID, task.ID, bob, web, "no-such-branch"); !isCode(err, apperrors.CodeNotFound) {
		t.Errorf("a base that is not there is said so: %v", err)
	}
	if _, err := s.StartBranch(ctx, org.ID, task.ID, bob, web, "bad base; rm -rf"); !isCode(err, apperrors.CodeValidation) {
		t.Errorf("a base that is no branch name is refused: %v", err)
	}

	// Alice has write access too, but has not accepted GitHub's invitation yet,
	// so she may branch on Bitbucket, where she is in, and not on GitHub.
	if _, err := s.Grant(ctx, org.ID, project.ID, alice, org.OwnerID, "write"); err != nil {
		t.Fatal(err)
	}
	second, _ := taskService.CreateTask(ctx, org.ID, alice, "Cache tokens", "", "", "", "", &project.ID, &alice, nil, nil, nil, false)
	if _, err := s.StartBranch(ctx, org.ID, second.ID, alice, web, ""); !errors.Is(err, ErrNeedsWriteAccess) {
		t.Errorf("an invitation not yet accepted is not access: %v", err)
	}
	onBB, err := s.StartBranch(ctx, org.ID, second.ID, alice, cortex, "")
	if err != nil || onBB.Base != "master" || !slices.Contains(bb.branches, "acme/cortex pho-2-cache-tokens from hash-master") {
		t.Fatalf("made on Bitbucket from its main branch: %+v %v %v", onBB, err, bb.branches)
	}

	// An app made before branches came in may only read the code.
	gh.mu.Lock()
	gh.readOnlyContents = true
	gh.mu.Unlock()
	third, _ := taskService.CreateTask(ctx, org.ID, bob, "Retry budget", "", "", "", "", &project.ID, &bob, nil, nil, nil, false)
	if _, err := s.StartBranch(ctx, org.ID, third.ID, bob, web, ""); !errors.Is(err, ErrBranchNotAllowed) {
		t.Errorf("the app not allowed to make branches is said so: %v", err)
	}

	// A repository outside the task's project is not offered.
	other, _ := taskService.CreateProject(ctx, org.ID, org.OwnerID, "Boson", "BOS", "")
	elsewhere, _ := taskService.CreateTask(ctx, org.ID, bob, "Boson thing", "", "", "", "", &other.ID, &bob, nil, nil, nil, false)
	if _, err := s.StartBranch(ctx, org.ID, elsewhere.ID, bob, web, ""); !isCode(err, apperrors.CodeNotFound) {
		t.Errorf("only the project's own repositories: %v", err)
	}
}

func TestBranchNamesMatchWhatTheWebAppOffers(t *testing.T) {
	for _, c := range []struct{ key, title, want string }{
		{"PHO-12", "Fix sign-in", "pho-12-fix-sign-in"},
		{"PHO-12", "  Why does the API return 500?? on /login  ", "pho-12-why-does-the-api-return-500"},
		{"TASK-3", "", "task-3"},
		{"BOS-1", "Ünïcode & only", "bos-1-n-code-only"},
	} {
		if got := BranchName(c.key, c.title); got != c.want {
			t.Errorf("BranchName(%q, %q) = %q, want %q", c.key, c.title, got, c.want)
		}
	}
}

func isCode(err error, code string) bool {
	var e *apperrors.AppError
	return errors.As(err, &e) && e.Code == code
}

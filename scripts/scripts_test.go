// Package scripts holds the repository's shell scripts; this tests the ones
// other tooling depends on.
package scripts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockRun runs with-test-lock.sh with a lock of its own, so the test never
// waits on a real test run.
func lockRun(ctx context.Context, lock string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "./with-test-lock.sh", args...)
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TEAMUP_TESTS_LOCK") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "TEAMUP_TESTS_LOCK="+lock)
	return cmd
}

func needFlock(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock is not installed")
	}
}

// Two runs at once dropped each other's test database: the second waits for
// the first.
func TestTwoRunsUsingTheTestServicesTakeTurns(t *testing.T) {
	needFlock(t)
	lock := filepath.Join(t.TempDir(), "tests.lock")
	log := filepath.Join(t.TempDir(), "log")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, name := range []string{"a", "b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			script := "echo start-" + name + " >> " + log + "; sleep 0.4; echo end-" + name + " >> " + log
			if out, err := lockRun(ctx, lock, "sh", "-c", script).CombinedOutput(); err != nil {
				t.Errorf("%s: %v %s", name, err, out)
			}
		}(name)
	}
	wg.Wait()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(raw))
	if len(lines) != 4 || strings.TrimPrefix(lines[0], "start-") != strings.TrimPrefix(lines[1], "end-") {
		t.Errorf("the runs overlapped: %v", lines)
	}
}

// make check holds the lock and runs make test-go, which asks for it again:
// that must go straight on, not wait for itself.
func TestTheLockIsTakenAgainFromInsideWithoutWaiting(t *testing.T) {
	needFlock(t)
	lock := filepath.Join(t.TempDir(), "tests.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := lockRun(ctx, lock, "./with-test-lock.sh", "./with-test-lock.sh", "sh", "-c", "echo inside").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "inside" {
		t.Fatalf("nested: %v %q", err, out)
	}
}

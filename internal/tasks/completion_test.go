package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// completedAtOf reads the stored date directly, so the test checks the column
// the tracking numbers are built on and not a field of the response.
func completedAtOf(t *testing.T, c *dashChart, id string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := c.pool.QueryRow(context.Background(), `SELECT completed_at FROM tasks WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("read completed_at: %v", err)
	}
	return at
}

func createViaAPI(t *testing.T, c *dashChart, body string) Task {
	t.Helper()
	w := send(t, c.asOwner(t), http.MethodPost, "/api/tasks", body)
	mustStatus(t, w, http.StatusCreated)
	var task Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode created task: %v", err)
	}
	return task
}

func TestSavingDoneTwiceKeepsTheFirstCompletionDate(t *testing.T) {
	c := seedDash(t)
	task := createViaAPI(t, c, `{"title":"Ship it"}`)
	r := c.asOwner(t)

	mustStatus(t, patchTask(t, r, task.ID.String(), `{"status":"done"}`), http.StatusOK)
	first := completedAtOf(t, c, task.ID.String())
	if first == nil {
		t.Fatal("moving to done should stamp completed_at")
	}

	// Push the stored date into the past so a rewrite cannot hide behind the
	// clock having barely moved.
	backdated := first.Add(-48 * time.Hour)
	if _, err := c.pool.Exec(context.Background(), `UPDATE tasks SET completed_at = $1 WHERE id = $2`, backdated, task.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	mustStatus(t, patchTask(t, r, task.ID.String(), `{"status":"done"}`), http.StatusOK)
	second := completedAtOf(t, c, task.ID.String())
	if second == nil || !second.Equal(backdated) {
		t.Errorf("saving done again must keep the first completion date %v, got %v", backdated, second)
	}
}

func TestMovingDoneBackToTodoClearsTheCompletionDate(t *testing.T) {
	c := seedDash(t)
	task := createViaAPI(t, c, `{"title":"Reopened"}`)
	r := c.asOwner(t)

	mustStatus(t, patchTask(t, r, task.ID.String(), `{"status":"done"}`), http.StatusOK)
	if completedAtOf(t, c, task.ID.String()) == nil {
		t.Fatal("precondition: done should stamp completed_at")
	}
	mustStatus(t, patchTask(t, r, task.ID.String(), `{"status":"todo"}`), http.StatusOK)
	if at := completedAtOf(t, c, task.ID.String()); at != nil {
		t.Errorf("reopening should clear completed_at, got %v", at)
	}
}

func TestCreatingATaskAsDoneStampsTheCompletionDate(t *testing.T) {
	c := seedDash(t)
	task := createViaAPI(t, c, `{"title":"Already finished","status":"done"}`)

	at := completedAtOf(t, c, task.ID.String())
	if at == nil {
		t.Fatal("a task created as done must have completed_at")
	}
	if time.Since(*at) > time.Minute || time.Until(*at) > time.Minute {
		t.Errorf("completed_at should be about now, got %v", at)
	}
	if task.CompletedAt == nil {
		t.Error("the response should carry completedAt too")
	}
}

func TestCreatingATaskRefusesBlankAndOverlongTitles(t *testing.T) {
	c := seedDash(t)
	r := c.asOwner(t)

	cases := []struct{ name, title string }{
		{"blank", "   "},
		{"empty", ""},
		{"501 characters", strings.Repeat("x", 501)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := func() int {
				var n int
				if err := c.pool.QueryRow(context.Background(),
					`SELECT count(*) FROM tasks WHERE org_id = $1`, c.org.ID).Scan(&n); err != nil {
					t.Fatalf("count: %v", err)
				}
				return n
			}
			was := rows()
			body, _ := json.Marshal(map[string]string{"title": tc.title})
			w := send(t, r, http.MethodPost, "/api/tasks", string(body))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
			}
			if now := rows(); now != was {
				t.Errorf("a refused create must not write a row, found %d new", now-was)
			}
		})
	}

	ok := strings.Repeat("x", 500)
	body, _ := json.Marshal(map[string]string{"title": ok})
	mustStatus(t, send(t, r, http.MethodPost, "/api/tasks", string(body)), http.StatusCreated)
}

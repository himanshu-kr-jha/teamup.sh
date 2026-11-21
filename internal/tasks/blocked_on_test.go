package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

// ── What a blocked task is blocked on ─────────────────────────────────────

type blockedBody struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	BlockedOn *struct {
		Note string `json:"note"`
		User *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"user"`
		Task *struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"task"`
	} `json:"blockedOn"`
}

func readBlocked(t *testing.T, r *gin.Engine, id uuid.UUID) blockedBody {
	t.Helper()
	w := getJSON(t, r, "/api/tasks/"+id.String())
	mustStatus(t, w, http.StatusOK)
	var b blockedBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return b
}

type activityRow struct {
	Field    string `json:"field"`
	OldValue string `json:"oldValue"`
	NewValue string `json:"newValue"`
}

func blockedActivity(t *testing.T, r *gin.Engine, id uuid.UUID) []activityRow {
	t.Helper()
	w := getJSON(t, r, "/api/tasks/"+id.String()+"/activities")
	mustStatus(t, w, http.StatusOK)
	var body struct {
		Activities []activityRow `json:"activities"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	out := []activityRow{}
	for _, a := range body.Activities {
		if a.Field == "blockedOn" {
			out = append(out, a)
		}
	}
	return out
}

func TestMarkingATaskBlockedKeepsWhatItIsBlockedOnAndClearsItWhenItMovesOn(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)
	task := c.work(t, c.alice, "Ship the importer", "in_progress")
	vendor := c.work(t, c.pat, "Vendor sends the keys", "todo")

	w := patchTask(t, r, task.String(), `{"status":"blocked","blockedOn":{"note":"  Waiting for the API keys  ","userId":"`+
		c.pat.String()+`","taskId":"`+vendor.String()+`"}}`)
	mustStatus(t, w, http.StatusOK)
	got := readBlocked(t, r, task)
	if got.Status != "blocked" || got.BlockedOn == nil {
		t.Fatalf("want a blocked task with a reason, got %s", w.Body.String())
	}
	if got.BlockedOn.Note != "Waiting for the API keys" {
		t.Errorf("the note is kept, trimmed: got %q", got.BlockedOn.Note)
	}
	if got.BlockedOn.User == nil || got.BlockedOn.User.Name != "Pat Platform" {
		t.Errorf("the person is named: got %+v", got.BlockedOn.User)
	}
	if got.BlockedOn.Task == nil || got.BlockedOn.Task.Title != "Vendor sends the keys" {
		t.Errorf("the task is named: got %+v", got.BlockedOn.Task)
	}

	// It can be changed while the task stays blocked.
	mustStatus(t, patchTask(t, r, task.String(), `{"blockedOn":{"note":"Keys promised for Monday"}}`), http.StatusOK)
	got = readBlocked(t, r, task)
	if got.BlockedOn == nil || got.BlockedOn.Note != "Keys promised for Monday" || got.BlockedOn.User != nil || got.BlockedOn.Task != nil {
		t.Errorf("an edit replaces the reason, got %+v", got.BlockedOn)
	}

	// Moving on clears it, and the history keeps what it said.
	mustStatus(t, patchTask(t, r, task.String(), `{"status":"in_progress"}`), http.StatusOK)
	if got := readBlocked(t, r, task); got.BlockedOn != nil {
		t.Errorf("a task that is not blocked has no reason, got %+v", got.BlockedOn)
	}
	var cleared int
	if err := c.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM tasks WHERE id = $1 AND blocked_note IS NULL AND blocked_by_user_id IS NULL AND blocked_by_task_id IS NULL`,
		task).Scan(&cleared); err != nil || cleared != 1 {
		t.Errorf("the columns should be cleared, got %d (%v)", cleared, err)
	}
	history := blockedActivity(t, r, task)
	if len(history) != 3 {
		t.Fatalf("want three blocked-on entries (set, changed, cleared), got %+v", history)
	}
	// Whatever order the history is read in, each reason is in it in words.
	saw := map[string]bool{}
	for _, a := range history {
		saw[a.NewValue] = true
		saw["was:"+a.OldValue] = true
	}
	if !saw["Waiting for the API keys; waiting on Pat Platform; waiting for Vendor sends the keys"] || !saw["Keys promised for Monday"] ||
		!saw["was:Keys promised for Monday"] {
		t.Errorf("the history should keep each reason in words, got %+v", history)
	}
}

func TestBlockingWithoutAReasonStillWorksAndReadsAsNoReason(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)
	task := c.work(t, c.alice, "Old client", "todo")

	mustStatus(t, patchTask(t, r, task.String(), `{"status":"blocked"}`), http.StatusOK)
	got := readBlocked(t, r, task)
	if got.BlockedOn == nil || got.BlockedOn.Note != "" || got.BlockedOn.User != nil || got.BlockedOn.Task != nil {
		t.Errorf("a status alone blocks with an empty reason, got %+v", got.BlockedOn)
	}
	// A task blocked before reasons existed has a NULL note and reads the same.
	old := c.work(t, c.alice, "Blocked long ago", "blocked")
	if got := readBlocked(t, r, old); got.BlockedOn == nil || got.BlockedOn.Note != "" {
		t.Errorf("an old blocked task reads as no reason, got %+v", got.BlockedOn)
	}
}

func TestABlockedOnReasonIsCheckedBeforeItIsKept(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)
	task := c.work(t, c.alice, "Checked", "todo")
	elsewhere := testsupport.SeedOrg(t, c.pool)
	stranger := testsupport.SeedUser(t, c.pool, elsewhere, "stranger@example.test", "member")

	for name, body := range map[string]string{
		"a reason on a task that is not blocked": `{"blockedOn":{"note":"why"}}`,
		"a person from another organization":     `{"status":"blocked","blockedOn":{"userId":"` + stranger.String() + `"}}`,
		"a task that does not exist":             `{"status":"blocked","blockedOn":{"taskId":"` + uuid.NewString() + `"}}`,
		"the task itself":                        `{"status":"blocked","blockedOn":{"taskId":"` + task.String() + `"}}`,
		"a note that is not text":                `{"status":"blocked","blockedOn":{"note":7}}`,
		"not an object":                          `{"status":"blocked","blockedOn":"because"}`,
	} {
		if w := patchTask(t, r, task.String(), body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d: %s", name, w.Code, w.Body.String())
		}
	}
	if got := readBlocked(t, r, task); got.Status != "todo" {
		t.Errorf("a refused update changes nothing, got status %s", got.Status)
	}
}

func TestATaskCreatedBlockedCanSayWhatItIsBlockedOn(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)
	w := send(t, r, http.MethodPost, "/api/tasks",
		`{"title":"Born blocked","status":"blocked","assigneeId":"`+c.alice.String()+`","blockedOn":{"note":"Needs a licence","userId":"`+c.mgr.String()+`"}}`)
	mustStatus(t, w, http.StatusCreated)
	var created blockedBody
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.BlockedOn == nil || created.BlockedOn.Note != "Needs a licence" || created.BlockedOn.User == nil {
		t.Errorf("want the reason on the new task, got %s", w.Body.String())
	}
	w = send(t, r, http.MethodPost, "/api/tasks", `{"title":"Not blocked","blockedOn":{"note":"why"}}`)
	mustStatus(t, w, http.StatusBadRequest)
}

func TestTheDashboardAndASavedScrumSayWhatEachBlockedTaskIsBlockedOn(t *testing.T) {
	c := seedDash(t)
	r := c.asManager(t, c.mgr)
	stuck := c.work(t, c.alice, "Stuck", "todo")
	mustStatus(t, patchTask(t, r, stuck.String(), `{"status":"blocked","blockedOn":{"note":"Waiting on legal","userId":"`+c.mgr.String()+`"}}`), http.StatusOK)
	c.work(t, c.alice, "Moving", "in_progress")

	w := getJSON(t, r, "/api/tasks/dashboard?teamId="+c.backend.String())
	mustStatus(t, w, http.StatusOK)
	var d struct {
		People []struct {
			UserID string `json:"userId"`
			Tasks  struct {
				Blocked []blockedBody `json:"blocked"`
				Ongoing []blockedBody `json:"ongoing"`
			} `json:"tasks"`
		} `json:"people"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, p := range d.People {
		if p.UserID != c.alice.String() {
			continue
		}
		if len(p.Tasks.Blocked) != 1 || p.Tasks.Blocked[0].BlockedOn == nil || p.Tasks.Blocked[0].BlockedOn.Note != "Waiting on legal" {
			t.Errorf("the blocked bucket carries the reason, got %+v", p.Tasks.Blocked)
		}
		if len(p.Tasks.Ongoing) != 1 || p.Tasks.Ongoing[0].BlockedOn != nil {
			t.Errorf("work that is not blocked has no reason, got %+v", p.Tasks.Ongoing)
		}
	}

	id := saveScrum(t, r, `{"teamId":"`+c.backend.String()+`"}`)
	read := getJSON(t, r, "/api/scrums/"+id)
	mustStatus(t, read, http.StatusOK)
	var s struct {
		Entries []struct {
			Title     string `json:"title"`
			BlockedOn *struct {
				Note      string  `json:"note"`
				UserName  *string `json:"userName"`
				TaskTitle *string `json:"taskTitle"`
			} `json:"blockedOn"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, e := range s.Entries {
		switch e.Title {
		case "Stuck":
			if e.BlockedOn == nil || e.BlockedOn.Note != "Waiting on legal" || e.BlockedOn.UserName == nil || *e.BlockedOn.UserName != "Mia Manager" || e.BlockedOn.TaskTitle != nil {
				t.Errorf("the saved scrum keeps the reason as it was, got %+v", e.BlockedOn)
			}
		case "Moving":
			if e.BlockedOn != nil {
				t.Errorf("work that is not blocked keeps no reason, got %+v", e.BlockedOn)
			}
		}
	}

	// Changing the reason later does not touch the saved scrum.
	mustStatus(t, patchTask(t, r, stuck.String(), `{"blockedOn":{"note":"Legal said yes"}}`), http.StatusOK)
	again := getJSON(t, r, "/api/scrums/"+id)
	if again.Body.String() != read.Body.String() {
		t.Errorf("a saved scrum must not move when the reason does")
	}
}

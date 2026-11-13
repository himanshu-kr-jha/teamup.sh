package tasks

import (
	"context"
	"testing"

	"github.com/teamup/teamup/internal/testsupport"
)

// "%" or "_" in the search box is looked for as itself, not as a pattern that
// matches every task.
func TestTaskSearchTreatsWildcardsAsPlainCharacters(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	createTask(t, svc, org, "Raise coverage to 90%")
	createTask(t, svc, org, "Rename snake_case fields")
	createTask(t, svc, org, `Fix C:\temp paths`)
	createTask(t, svc, org, "Plain task")

	for text, want := range map[string]string{"%": "Raise coverage to 90%", "_": "Rename snake_case fields", `\`: `Fix C:\temp paths`} {
		found, err := svc.ListTasks(ctx, org.ID, Filters{Search: text})
		if err != nil {
			t.Fatalf("search %q: %v", text, err)
		}
		if len(found) != 1 || found[0].Title != want {
			titles := []string{}
			for _, f := range found {
				titles = append(titles, f.Title)
			}
			t.Errorf("search %q: want only %q, got %v", text, want, titles)
		}
	}
}

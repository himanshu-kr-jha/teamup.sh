package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/teamup/teamup/internal/testsupport"
)

// Channel history is read on every channel open, every socket reconnect and
// every tab focus, and it deliberately does not filter deleted_at: a deleted
// message keeps its place so replies still have a parent — see
// TestADeletedMessageKeepsItsPlaceInHistory.
//
// The only index over (channel_id, created_at) used to carry
// `WHERE deleted_at IS NULL`, and a partial index cannot serve a query that
// does not exclude the same rows. So the predicate that makes history correct
// was the one that made the index unusable, and at 100k messages the query was
// a parallel sequential scan of the whole table.
//
// The fix belongs in the index rather than the query, and this is what that
// means: an index leading with channel_id and created_at that excludes nothing.
func TestChannelHistoryHasAnIndexItCanActuallyUse(t *testing.T) {
	pool := testsupport.DB(t)

	rows, err := pool.Query(context.Background(), `
		SELECT indexname, indexdef FROM pg_indexes
		WHERE schemaname = current_schema() AND tablename = 'messages'
	`)
	if err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	defer rows.Close()

	var usable []string
	var partialOnly []string
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		if !strings.Contains(def, "(channel_id, created_at") {
			continue
		}
		if strings.Contains(def, " WHERE ") {
			partialOnly = append(partialOnly, name+": "+def)
			continue
		}
		usable = append(usable, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}

	if len(usable) == 0 {
		t.Errorf("no index on messages(channel_id, created_at) that history can use;"+
			" every candidate is partial and therefore skipped by the planner: %v", partialOnly)
	}
}

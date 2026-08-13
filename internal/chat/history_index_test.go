package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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

// A keyset cursor has to be total, or a page boundary landing inside a group of
// equal keys steps over the rest of that group and the reader is never told
// history went missing.
//
// Being straight about the likelihood: the send path sets created_at explicitly
// from Go's time.Now() and writes one row per statement, so this is NOT the
// transaction-timestamp collision it first looks like. Postgres truncates to
// microseconds, so two messages must commit inside the same microsecond AND
// land on a page boundary. At sixty people that is effectively never.
//
// The test forces the collision directly rather than pretending it is common.
// It is here because (created_at, id) is strictly more correct than created_at
// alone, the client already tiebreaks on id for exactly this reason, and a
// cursor that can silently drop rows is not worth keeping for the sake of one
// column in an ORDER BY.
func TestPagingDoesNotSkipMessagesSharingATimestamp(t *testing.T) {
	pool := testsupport.DB(t)
	svc := NewService(pool, nil)
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	// One explicit timestamp for every row — the collision the cursor has to
	// survive, constructed directly rather than hoped for.
	const total = 6
	shared := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < total; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO messages (org_id, channel_id, user_id, content, created_at)
			 VALUES ($1,$2,$3,$4,$5)`,
			org.ID, ch, org.OwnerID, fmt.Sprintf("simultaneous %d", i), shared); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	var stamps int
	if err := pool.QueryRow(ctx,
		`SELECT count(DISTINCT created_at) FROM messages WHERE channel_id = $1`, ch).Scan(&stamps); err != nil {
		t.Fatalf("count timestamps: %v", err)
	}
	if stamps != 1 {
		t.Fatalf("precondition: wanted one shared timestamp, got %d — this test is not exercising the boundary", stamps)
	}

	// Walk the whole history two at a time, exactly as a scrolling client does.
	seen := map[string]bool{}
	var beforeAt *time.Time
	var beforeID *uuid.UUID
	for page := 0; page < total; page++ {
		got, err := svc.GetMessages(ctx, ch, 2, beforeAt, beforeID)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(got) == 0 {
			break
		}
		for _, m := range got {
			seen[m.ID.String()] = true
		}
		oldest := got[0]
		beforeAt, beforeID = &oldest.CreatedAt, &oldest.ID
	}

	if len(seen) != total {
		t.Errorf("paging reached %d of %d messages — the rest share the boundary"+
			" timestamp and were stepped over", len(seen), total)
	}
}

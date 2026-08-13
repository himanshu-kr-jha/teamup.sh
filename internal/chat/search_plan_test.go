package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/teamup/teamup/internal/testsupport"
)

// searchCorpus is enough messages for the table's statistics to be real. How
// large it has to be before a scan looks expensive is a property of the data,
// not of the query, so the test below settles that question a different way —
// see the planner settings it makes.
const searchCorpus = 2000

// The fuzzy branch of search was `word_similarity($2, m.content) >= 0.4`.
// pg_trgm only accelerates its own operators, so a bare function comparison is
// not indexable — and one unindexable branch in an OR chain forces the whole
// chain to a filter over every message in the organization, both GIN indexes
// left unused.
//
// This asks the planner directly rather than trusting the shape of the SQL.
func TestFuzzySearchCanUseTheTrigramIndex(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	// A corpus of ordinary chatter, plus a handful of messages carrying a word
	// nothing else does — which is what a search term normally is.
	if _, err := pool.Exec(ctx, `
		INSERT INTO messages (id, org_id, channel_id, user_id, content, created_at, kind)
		SELECT gen_random_uuid(), $1, $2, $3,
		       'standup notes for the release train, item ' || i,
		       NOW() - (i || ' seconds')::interval, 'user'
		FROM generate_series(1, $4) i
	`, org.ID, ch, org.OwnerID, searchCorpus); err != nil {
		t.Fatalf("seed corpus: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO messages (id, org_id, channel_id, user_id, content, created_at, kind)
		SELECT gen_random_uuid(), $1, $2, $3,
		       'the vestibulum migration ran clean on shard ' || i,
		       NOW() - (i || ' seconds')::interval, 'user'
		FROM generate_series(1, 20) i
	`, org.ID, ch, org.OwnerID); err != nil {
		t.Fatalf("seed needles: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE messages`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		fmt.Sprintf("SET LOCAL pg_trgm.word_similarity_threshold = %v", searchTrigramThreshold)); err != nil {
		t.Fatalf("set threshold: %v", err)
	}

	// And: use an index if there is one. On any table small enough to seed in
	// a test a scan really is the cheapest plan, so left to itself the planner
	// would choose one whatever the predicate looked like, and the test would
	// pass either way. Asked to avoid a scan, the only remaining reason to see
	// one is that no index path exists for this predicate at all — which is
	// precisely the defect: pg_trgm accelerates its own operators, and
	// `word_similarity(a, b) >= 0.4` is a comparison of a function's result.
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}

	// The approximate branch on its own. Asked as part of the whole query the
	// answer would depend on join order and on how the other two branches
	// happen to cost out on this particular corpus; asked directly it is the
	// question itself — can the planner reach these rows through the trigram
	// index, or must it look at every message?
	//
	// The term is a typo, so no whole word and no substring matches it: only
	// trigram similarity can find these rows.
	rows, err := tx.Query(ctx,
		"EXPLAIN SELECT m.id FROM messages m WHERE "+strings.ReplaceAll(searchFuzzyPredicate, "$2", "$1"),
		"vestibulm")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}
	rows.Close()

	if !strings.Contains(plan.String(), "idx_messages_content_trgm") {
		t.Errorf("the approximate branch cannot use the trigram index, so every"+
			" message is examined — and one unindexable branch takes the whole OR"+
			" chain down with it:\n%s", plan.String())
	}
	if strings.Contains(plan.String(), "Seq Scan on messages") {
		t.Errorf("the approximate branch sequentially scans the messages table:\n%s", plan.String())
	}

	// And the query still answers: a superset from the index, narrowed by the
	// comparison, must be the same rows as before.
	results, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "vestibulm"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Error("the typo found nothing")
	}
	for _, r := range results {
		if !strings.Contains(r.Content, "vestibulum") {
			t.Errorf("unrelated message matched: %q", r.Content)
		}
	}
}

// The threshold is a setting on the connection, and connections are pooled.
// Set without LOCAL it would leak to whatever ran next on that connection —
// including another organization's query.
func TestSearchDoesNotLeaveItsThresholdOnTheConnection(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID,
		"the deployment finished cleanly", nil, nil, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "deploment"}); err != nil {
		t.Fatalf("search: %v", err)
	}

	// Every connection in the pool must still be at the default. Asked enough
	// times to reach more than the one the search happened to use.
	for i := 0; i < 10; i++ {
		var setting string
		if err := pool.QueryRow(ctx,
			`SELECT current_setting('pg_trgm.word_similarity_threshold')`).Scan(&setting); err != nil {
			t.Fatalf("read setting: %v", err)
		}
		if setting != "0.6" {
			t.Fatalf("a pooled connection was left at word_similarity_threshold=%s, not the default 0.6", setting)
		}
	}
}

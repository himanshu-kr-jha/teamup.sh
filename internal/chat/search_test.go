package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

// Message search had no route at all: the frontend declared an endpoint that
// was never built. The schema already carries the generated tsvector column.
func TestSearchFindsMatchingMessages(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	for _, body := range []string{
		"the deployment pipeline is broken again",
		"lunch is at noon",
		"we should document the deployment steps",
	} {
		if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, body, nil, nil, false); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	results, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "deployment"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected both deployment messages, got %d", len(results))
	}
	for _, r := range results {
		if r.ChannelName == "" || r.UserName == "" {
			t.Errorf("a result needs its channel and author to be useful: %+v", r)
		}
		if r.Highlight == "" {
			t.Errorf("a result should carry a highlighted snippet: %+v", r)
		}
	}
}

// Stemming: "documented" should find "document".
func TestSearchMatchesWordStems(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "we should document the steps", nil, nil, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	results, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "documenting"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("stemmed search should match, got %d results", len(results))
	}
}

// The searcher must only ever see channels they can read.
func TestSearchOnlyReturnsReadableChannels(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedUser(t, pool, org, "outsider@example.test", "member")

	private := testsupport.SeedChannel(t, pool, org, "private", org.OwnerID)
	if _, err := svc.SendMessage(ctx, org.ID, private, org.OwnerID, "confidential runway numbers", nil, nil, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	mine, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "runway"})
	if err != nil {
		t.Fatalf("search as member: %v", err)
	}
	if len(mine) != 1 {
		t.Errorf("a member should find their own channel's message, got %d", len(mine))
	}

	theirs, err := svc.SearchMessages(ctx, org.ID, outsider, SearchQuery{Text: "runway"})
	if err != nil {
		t.Fatalf("search as outsider: %v", err)
	}
	if len(theirs) != 0 {
		t.Errorf("a non-member must not find a private channel's messages, got %d", len(theirs))
	}
}

func TestSearchIncludesPublicChannelsForNonMembers(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	outsider := testsupport.SeedUser(t, pool, org, "reader@example.test", "member")

	name := "announcements"
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create public: %v", err)
	}
	if _, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "all hands on friday", nil, nil, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	results, err := svc.SearchMessages(ctx, org.ID, outsider, SearchQuery{Text: "hands"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("public channels should be searchable organization-wide, got %d", len(results))
	}
}

func TestSearchNeverCrossesOrganizations(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	mine := testsupport.SeedOrg(t, pool)
	theirs := testsupport.SeedOrg(t, pool)

	ch := testsupport.SeedChannel(t, pool, theirs, "theirs", theirs.OwnerID)
	if _, err := svc.SendMessage(ctx, theirs.ID, ch, theirs.OwnerID, "their secret plans", nil, nil, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	results, err := svc.SearchMessages(ctx, mine.ID, mine.OwnerID, SearchQuery{Text: "secret"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("search leaked another organization's messages: %+v", results)
	}
}

func TestSearchCanBeNarrowedToAChannelAndAuthor(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	mate := testsupport.SeedUser(t, pool, org, "mate@example.test", "member")
	a := testsupport.SeedChannel(t, pool, org, "alpha", org.OwnerID, mate)
	b := testsupport.SeedChannel(t, pool, org, "beta", org.OwnerID, mate)

	if _, err := svc.SendMessage(ctx, org.ID, a, org.OwnerID, "release notes for alpha", nil, nil, false); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if _, err := svc.SendMessage(ctx, org.ID, b, mate, "release notes for beta", nil, nil, false); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	inA, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "release", ChannelID: &a})
	if err != nil {
		t.Fatalf("channel filter: %v", err)
	}
	if len(inA) != 1 || inA[0].ChannelID != a {
		t.Errorf("channel filter wrong: %+v", inA)
	}

	byMate, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "release", UserID: &mate})
	if err != nil {
		t.Fatalf("author filter: %v", err)
	}
	if len(byMate) != 1 || byMate[0].UserID != mate {
		t.Errorf("author filter wrong: %+v", byMate)
	}
}

func TestSearchExcludesDeletedMessages(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	msg, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "regrettable thing", nil, nil, false)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.DeleteMessage(ctx, msg.ID, org.OwnerID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	results, _ := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "regrettable"})
	if len(results) != 0 {
		t.Errorf("a deleted message must not be searchable, got %d", len(results))
	}
}

func TestSearchWithEmptyQueryReturnsNothing(t *testing.T) {
	svc, pool := newService(t)
	org := testsupport.SeedOrg(t, pool)

	results, err := svc.SearchMessages(context.Background(), org.ID, org.OwnerID, SearchQuery{Text: "   "})
	if err != nil {
		t.Fatalf("empty search should not error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("an empty query should match nothing, got %d", len(results))
	}
}

func TestSearchIsPaged(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	for i := 0; i < 5; i++ {
		if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "repeated keyword here", nil, nil, false); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	page, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "keyword", Limit: 2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page) != 2 {
		t.Errorf("limit ignored, got %d", len(page))
	}

	second, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "keyword", Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("offset search: %v", err)
	}
	if len(second) != 2 {
		t.Errorf("offset page wrong size: %d", len(second))
	}
	if len(page) > 0 && len(second) > 0 && page[0].ID == second[0].ID {
		t.Error("the second page should not repeat the first")
	}
	_ = uuid.Nil
}

// ts_headline does not escape the content it highlights, so HTML delimiters
// would hand user-authored markup to the browser verbatim.
func TestSearchHighlightDoesNotEmitHTML(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID,
		`<script>alert("xss")</script> deployment notes`, nil, nil, false); err != nil {
		t.Fatalf("seed: %v", err)
	}

	results, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "deployment"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if strings.Contains(results[0].Highlight, "<mark>") {
		t.Error("highlights must not be HTML")
	}
	if !strings.Contains(results[0].Highlight, HighlightStart) {
		t.Errorf("expected the sentinel delimiter, got %q", results[0].Highlight)
	}
}

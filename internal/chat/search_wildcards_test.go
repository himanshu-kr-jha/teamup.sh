package chat

import (
	"context"
	"testing"

	"github.com/teamup/teamup/internal/testsupport"
)

// A search box is not a pattern language: "%" or "_" typed into it is looked
// for as itself. Unescaped, "%" matched every message and every file, which
// is both wrong and a full scan on demand.
func TestSearchTreatsWildcardsAsPlainCharacters(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)

	for _, body := range []string{"we hit 100% coverage", "lunch is at noon", "snake_case everywhere"} {
		if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, body, nil, nil, false); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}
	seedSharedFile(t, pool, org.ID, ch, org.OwnerID, "growth-50%.pdf")
	seedSharedFile(t, pool, org.ID, ch, org.OwnerID, "holiday-photos.zip")

	for text, want := range map[string]int{"%": 1, "_": 1} {
		got, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: text})
		if err != nil {
			t.Fatalf("search %q: %v", text, err)
		}
		if len(got) != want {
			t.Errorf("messages matching %q: want %d, got %d", text, want, len(got))
		}
	}
	files, err := svc.SearchFiles(ctx, org.ID, org.OwnerID, SearchQuery{Text: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Filename != "growth-50%.pdf" {
		t.Errorf("files matching %%: %+v", files)
	}
}

// Paging deep into a search makes the database sort and skip that many rows
// on every request, so how far one can page is bounded.
func TestSearchCannotPageArbitrarilyDeep(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "deployment", nil, nil, false); err != nil {
		t.Fatal(err)
	}
	seedSharedFile(t, pool, org.ID, ch, org.OwnerID, "deployment.pdf")

	if maxSearchOffset > 10_000 {
		t.Errorf("the offset limit is %d", maxSearchOffset)
	}
	msgs, err := svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "deployment", Offset: 1 << 30})
	if err != nil || len(msgs) != 0 {
		t.Errorf("messages past the limit: %v %v", msgs, err)
	}
	files, err := svc.SearchFiles(ctx, org.ID, org.OwnerID, SearchQuery{Text: "deployment", Offset: 1 << 30})
	if err != nil || len(files) != 0 {
		t.Errorf("files past the limit: %v %v", files, err)
	}
	msgs, err = svc.SearchMessages(ctx, org.ID, org.OwnerID, SearchQuery{Text: "deployment"})
	if err != nil || len(msgs) != 1 {
		t.Errorf("the first page still works: %v %v", msgs, err)
	}
}

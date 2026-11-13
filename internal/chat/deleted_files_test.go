package chat

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/teamup/teamup/internal/testsupport"
)

// A message deleted with a file in it takes the file with it: nobody else in
// the conversation, then or later, finds it in the history or a search.
func TestADeletedMessagesFilesGoWithIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	colleague := testsupport.SeedUser(t, pool, org, "colleague-"+uuid.NewString()[:8]+"@example.test", "member")
	name := "files-" + uuid.NewString()[:8]
	ch, err := svc.CreateChannel(ctx, org.ID, org.OwnerID, &name, "public", nil, nil)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	upload := seedUpload(t, pool, org.ID, ch.ID, org.OwnerID, "salaries.xlsx")
	msg, err := svc.SendMessage(ctx, org.ID, ch.ID, org.OwnerID, "oops, wrong channel", nil, []uuid.UUID{upload}, false)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := svc.DeleteMessage(ctx, msg.ID, org.OwnerID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	history, err := svc.GetMessages(ctx, ch.ID, 50, nil, nil)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, m := range history {
		if len(m.Attachments) > 0 {
			t.Errorf("the deleted message still carries %s", m.Attachments[0].ObjectKey)
		}
	}
	found, err := svc.SearchFiles(ctx, org.ID, colleague, SearchQuery{Text: "salaries"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("search still finds %s", found[0].Filename)
	}
	var live int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM attachments WHERE id = $1 AND deleted_at IS NULL`, upload).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Error("the file is still live")
	}
}

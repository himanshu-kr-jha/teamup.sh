package tasks

import (
	"context"
	"testing"
)

// Reported: there was no way to bring somebody into a task's conversation.
// Naming them in a comment, as in chat, now tells them.
func TestNamingSomebodyInACommentTellsThem(t *testing.T) {
	c := seedDash(t)
	notifier := &recordingNotifier{}
	c.svc.SetNotifier(notifier)
	task := c.work(t, c.ann, "Quarterly report", "todo")
	ctx := context.Background()

	if _, err := c.svc.AddComment(ctx, c.org.ID, task, c.ann, "@alice and @AliceArcher, can you check this? Not @nobody.", nil); err != nil {
		t.Fatal(err)
	}
	if got := notifier.count(c.alice); got != 1 {
		t.Errorf("alice is told once, however many ways she is named: got %d", got)
	}

	if _, err := c.svc.AddComment(ctx, c.org.ID, task, c.ann, "Note to self, @ann.", nil); err != nil {
		t.Fatal(err)
	}
	if got := notifier.count(c.ann); got != 0 {
		t.Errorf("naming yourself tells nobody: got %d", got)
	}

	if _, err := c.svc.AddComment(ctx, c.org.ID, task, c.ann, "email alice@example.test about it", nil); err != nil {
		t.Fatal(err)
	}
	if got := notifier.count(c.alice); got != 1 {
		t.Errorf("an email address is not a mention: got %d", got)
	}
}

package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/testsupport"
	apperrors "github.com/teamup/teamup/pkg/errors"
)

type fakeCopier struct {
	mu     sync.Mutex
	copied [][2]string
	fail   error
}

func (f *fakeCopier) CopyObject(_ context.Context, src, dst string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.copied = append(f.copied, [2]string{src, dst})
	return nil
}

func seedFile(t *testing.T, svc *Service, org *testsupport.Org, channel, uploader uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	key := "org-" + org.ID.String() + "/chat/" + channel.String() + "/20261108T000000Z-" + name
	if err := svc.db.QueryRow(context.Background(), `
		INSERT INTO attachments (org_id, uploaded_by, category, ref_id, object_key, filename, content_type, size_bytes)
		VALUES ($1, $2, 'chat', $3, $4, $5, 'application/pdf', 1234) RETURNING id
	`, org.ID, uploader, channel.String(), key, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

type forwardWorld struct {
	svc            *Service
	org            *testsupport.Org
	alice, bob     uuid.UUID
	source, target uuid.UUID
	copier         *fakeCopier
}

func newForwardWorld(t *testing.T) *forwardWorld {
	svc, pool := newService(t)
	svc.SetLogger(zerolog.Nop())
	org := testsupport.SeedOrg(t, pool)
	alice := testsupport.SeedUser(t, pool, org, "alice@example.com", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob@example.com", "member")
	w := &forwardWorld{
		svc: svc, org: org, alice: alice, bob: bob,
		source: testsupport.SeedChannel(t, pool, org, "source", org.OwnerID, alice),
		target: testsupport.SeedChannel(t, pool, org, "target", alice, bob),
		copier: &fakeCopier{},
	}
	svc.SetObjectCopier(w.copier)
	return w
}

func TestAForwardedMessageArrivesMarkedWithItsFilesCopiedIntoTheNewConversation(t *testing.T) {
	w := newForwardWorld(t)
	ctx := context.Background()
	file := seedFile(t, w.svc, w.org, w.source, w.org.OwnerID, "plan.pdf")
	orig, err := w.svc.SendMessage(ctx, w.org.ID, w.source, w.org.OwnerID, "the plan", nil, []uuid.UUID{file}, false)
	if err != nil {
		t.Fatal(err)
	}

	sent, err := w.svc.ForwardMessage(ctx, w.org.ID, orig.ID, w.alice, []uuid.UUID{w.target, w.target}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("a target named twice is sent to once: %d", len(sent))
	}
	got := sent[0]
	if !got.Forwarded || got.Content != "the plan" || got.UserID != w.alice || got.ChannelID != w.target {
		t.Errorf("forwarded by alice into target: %+v", got)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Filename != "plan.pdf" {
		t.Fatalf("the file went too: %+v", got.Attachments)
	}
	copied := got.Attachments[0].ObjectKey
	if !strings.HasPrefix(copied, "org-"+w.org.ID.String()+"/chat/"+w.target.String()+"/") || copied == orig.Attachments[0].ObjectKey {
		t.Errorf("a copy filed under the new conversation, so its members can open it: %s", copied)
	}
	if len(w.copier.copied) != 1 || w.copier.copied[0][0] != orig.Attachments[0].ObjectKey {
		t.Errorf("copied from the original: %+v", w.copier.copied)
	}

	// Read back as everybody sees it.
	history, err := w.svc.GetMessages(ctx, w.target, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	last := history[len(history)-1]
	if !last.Forwarded || len(last.Attachments) != 1 || last.Attachments[0].ObjectKey != copied {
		t.Errorf("history: %+v", last)
	}
	// The original is untouched.
	src, _ := w.svc.GetMessages(ctx, w.source, 10, nil, nil)
	if src[len(src)-1].Forwarded || len(src[len(src)-1].Attachments) != 1 {
		t.Errorf("original: %+v", src[len(src)-1])
	}
}

func TestForwardingNeedsToReadTheOriginalAndPostInEveryTarget(t *testing.T) {
	w := newForwardWorld(t)
	ctx := context.Background()
	orig, err := w.svc.SendMessage(ctx, w.org.ID, w.source, w.org.OwnerID, "private", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	// Bob is not in the source conversation: as if it did not exist.
	if _, err := w.svc.ForwardMessage(ctx, w.org.ID, orig.ID, w.bob, []uuid.UUID{w.target}, false); !errors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("bob cannot read it: %v", err)
	}

	// Alice may read it, but one of her targets is not hers: nothing is sent.
	elsewhere := testsupport.SeedChannel(t, w.svc.db, w.org, "elsewhere", w.bob)
	if _, err := w.svc.ForwardMessage(ctx, w.org.ID, orig.ID, w.alice, []uuid.UUID{w.target, elsewhere}, false); err == nil {
		t.Errorf("not a member of one target")
	}
	history, _ := w.svc.GetMessages(ctx, w.target, 10, nil, nil)
	if len(history) != 0 {
		t.Errorf("nothing half sent: %d", len(history))
	}

	if _, err := w.svc.ForwardMessage(ctx, w.org.ID, orig.ID, w.alice, nil, false); err == nil {
		t.Errorf("nowhere to send it")
	}
	many := make([]uuid.UUID, MaxForwardTargets+1)
	for i := range many {
		many[i] = uuid.New()
	}
	if _, err := w.svc.ForwardMessage(ctx, w.org.ID, orig.ID, w.alice, many, false); err == nil {
		t.Errorf("too many at once")
	}
}

func TestADeletedMessageCannotBeForwarded(t *testing.T) {
	w := newForwardWorld(t)
	ctx := context.Background()
	orig, err := w.svc.SendMessage(ctx, w.org.ID, w.source, w.alice, "oops", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.svc.DeleteMessage(ctx, orig.ID, w.alice); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ForwardMessage(ctx, w.org.ID, orig.ID, w.alice, []uuid.UUID{w.target}, false); err == nil {
		t.Errorf("deleted")
	}
}

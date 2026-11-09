package chat

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/testsupport"
)

func TestEachMemberChoosesWhatAConversationInterruptsThemWith(t *testing.T) {
	svc, pool := newService(t)
	svc.SetLogger(zerolog.Nop())
	n := &recordingNotifier{}
	svc.SetNotifier(n)
	org := testsupport.SeedOrg(t, pool)
	alice := testsupport.SeedUser(t, pool, org, "alice@example.com", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob@example.com", "member")
	outsider := testsupport.SeedUser(t, pool, org, "carol@example.com", "member")
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, alice, bob)
	ctx := context.Background()

	// Everybody starts hearing about everything.
	list, err := svc.ListChannels(ctx, org.ID, alice)
	if err != nil {
		t.Fatal(err)
	}
	if got := findChannel(t, list, ch).NotifyLevel; got == nil || *got != "all" {
		t.Fatalf("a member starts at all: %v", got)
	}

	if err := svc.SetNotifyLevel(ctx, ch, alice, "none"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetNotifyLevel(ctx, ch, bob, "mentions"); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.ListChannels(ctx, org.ID, alice)
	if got := findChannel(t, list, ch).NotifyLevel; got == nil || *got != "none" {
		t.Fatalf("alice chose none: %v", got)
	}
	// It is hers alone.
	list, _ = svc.ListChannels(ctx, org.ID, bob)
	if got := findChannel(t, list, ch).NotifyLevel; got == nil || *got != "mentions" {
		t.Fatalf("bob chose mentions: %v", got)
	}

	// Nothing means nothing, mentions included; mentions still reach Bob.
	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "@alice and @bob, a look?", nil, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := n.mentioned(); len(got) != 1 || got[0] != bob {
		t.Errorf("only bob is told he was mentioned: %v", got)
	}

	// Not a choice: an unknown level, or somebody who is not in the conversation.
	if err := svc.SetNotifyLevel(ctx, ch, alice, "loud"); err == nil {
		t.Error("an unknown level is refused")
	}
	if err := svc.SetNotifyLevel(ctx, ch, outsider, "none"); err == nil {
		t.Error("somebody outside the conversation has nothing to set")
	}
}

func findChannel(t *testing.T, list []Channel, id uuid.UUID) Channel {
	t.Helper()
	for _, c := range list {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("channel %s is not in the list", id)
	return Channel{}
}

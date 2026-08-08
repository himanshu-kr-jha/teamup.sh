package notifications

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

// recordingBroadcaster captures what would have gone out over the WebSocket.
type recordingBroadcaster struct {
	mu   sync.Mutex
	sent map[string]int
}

func (r *recordingBroadcaster) BroadcastToUser(userID string, msg *protocol.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent == nil {
		r.sent = map[string]int{}
	}
	r.sent[userID]++
}

func (r *recordingBroadcaster) count(userID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent[userID]
}

func newService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	return NewService(pool, nil, Config{}), pool
}

func TestNotificationIsStoredAndCounted(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	if err := svc.Notify(ctx, org.ID, org.OwnerID, "task.assigned",
		"You were assigned a task", "Ship it", map[string]any{"taskId": "abc"}); err != nil {
		t.Fatalf("notify: %v", err)
	}

	items, err := svc.ListNotifications(ctx, org.OwnerID.String(), false, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("notification not stored, got %d", len(items))
	}
	if items[0].Title != "You were assigned a task" || items[0].Type != "task.assigned" {
		t.Errorf("wrong notification: %+v", items[0])
	}

	count, err := svc.UnreadCount(ctx, org.OwnerID)
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 1 {
		t.Errorf("unread count: got %d want 1", count)
	}
}

// A new notification must reach an open tab without a refresh.
func TestNotificationIsBroadcastLive(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	bc := &recordingBroadcaster{}
	svc.SetBroadcaster(bc)

	if err := svc.Notify(ctx, org.ID, org.OwnerID, "chat.mention", "Mentioned", "hi", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if bc.count(org.OwnerID.String()) != 1 {
		t.Error("notification should be pushed over the socket")
	}
}

func TestMarkReadAndMarkAllRead(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	for i := 0; i < 3; i++ {
		if err := svc.Notify(ctx, org.ID, org.OwnerID, "task.assigned", "T", "b", nil); err != nil {
			t.Fatalf("notify: %v", err)
		}
	}
	items, _ := svc.ListNotifications(ctx, org.OwnerID.String(), false, 50)
	if len(items) != 3 {
		t.Fatalf("setup: expected 3, got %d", len(items))
	}

	if err := svc.MarkRead(ctx, org.OwnerID, items[0].ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	count, _ := svc.UnreadCount(ctx, org.OwnerID)
	if count != 2 {
		t.Errorf("after one read expected 2 unread, got %d", count)
	}

	unread, _ := svc.ListNotifications(ctx, org.OwnerID.String(), true, 50)
	if len(unread) != 2 {
		t.Errorf("unread-only listing wrong: %d", len(unread))
	}

	if err := svc.MarkAllRead(ctx, org.OwnerID); err != nil {
		t.Fatalf("mark all read: %v", err)
	}
	count, _ = svc.UnreadCount(ctx, org.OwnerID)
	if count != 0 {
		t.Errorf("expected everything read, got %d unread", count)
	}
}

// A notification id must not be actionable by anyone but its recipient.
func TestMarkReadIsScopedToTheRecipient(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "nosy@example.test", "member")

	if err := svc.Notify(ctx, org.ID, org.OwnerID, "task.assigned", "T", "b", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	items, _ := svc.ListNotifications(ctx, org.OwnerID.String(), false, 50)

	if err := svc.MarkRead(ctx, other, items[0].ID); err == nil {
		t.Fatal("another user must not be able to mark this notification read")
	}
	count, _ := svc.UnreadCount(ctx, org.OwnerID)
	if count != 1 {
		t.Error("the notification should still be unread for its owner")
	}
}

func TestNotificationsAreOnlyVisibleToTheirRecipient(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	other := testsupport.SeedUser(t, pool, org, "other@example.test", "member")

	if err := svc.Notify(ctx, org.ID, org.OwnerID, "task.assigned", "T", "b", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	items, err := svc.ListNotifications(ctx, other.String(), false, 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("another user should see nothing, got %d", len(items))
	}
}

func TestArchiveRemovesFromTheList(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	if err := svc.Notify(ctx, org.ID, org.OwnerID, "task.assigned", "T", "b", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	items, _ := svc.ListNotifications(ctx, org.OwnerID.String(), false, 50)

	if err := svc.Archive(ctx, org.OwnerID, items[0].ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	items, _ = svc.ListNotifications(ctx, org.OwnerID.String(), false, 50)
	if len(items) != 0 {
		t.Errorf("archived notification still listed: %+v", items)
	}
}

// ── Push subscriptions (ticket 11) ────────────────────────────────────────

func TestPushSubscriptionUpsertsPerDevice(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	sub := &PushSubscription{
		Endpoint: "https://push.example.test/abc",
		P256DH:   "key-1", Auth: "auth-1", DeviceName: "Laptop",
	}
	if err := svc.SaveSubscription(ctx, org.OwnerID.String(), org.ID.String(), sub); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Browsers rotate keys; the same endpoint must update rather than
	// accumulate a dead duplicate.
	sub.P256DH = "key-2"
	if err := svc.SaveSubscription(ctx, org.OwnerID.String(), org.ID.String(), sub); err != nil {
		t.Fatalf("resave: %v", err)
	}

	subs, err := svc.getSubscriptions(ctx, org.OwnerID.String())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected one subscription, got %d", len(subs))
	}
	if subs[0].P256DH != "key-2" {
		t.Errorf("subscription not refreshed: %q", subs[0].P256DH)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	const endpoint = "https://push.example.test/gone"
	if err := svc.SaveSubscription(ctx, org.OwnerID.String(), org.ID.String(), &PushSubscription{
		Endpoint: endpoint, P256DH: "k", Auth: "a",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := svc.DeleteSubscriptionByEndpoint(ctx, org.OwnerID.String(), endpoint); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	subs, _ := svc.getSubscriptions(ctx, org.OwnerID.String())
	if len(subs) != 0 {
		t.Errorf("subscription should be gone, got %d", len(subs))
	}
}

// The wizard stores a keypair with the organization; it must win over the
// environment, and push must be inert rather than broken without one.
func TestVAPIDKeysFromStorageTakePrecedence(t *testing.T) {
	svc, _ := newService(t)

	if svc.HasVAPIDKeys() {
		t.Fatal("a service with no configuration should report push as unavailable")
	}
	svc.SetVAPIDKeys("public", "private")
	if !svc.HasVAPIDKeys() || svc.VAPIDPublicKey() != "public" {
		t.Error("stored keys should be adopted")
	}
	// Empty values must not wipe working keys.
	svc.SetVAPIDKeys("", "")
	if !svc.HasVAPIDKeys() {
		t.Error("empty keys must not clear the configured ones")
	}
}

func TestSendToUserIsInertWithoutKeys(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	// No VAPID keys: the in-app notification still stands and push is skipped
	// rather than erroring the action that triggered it.
	if err := svc.SendToUser(ctx, org.OwnerID.String(), PushPayload{Title: "t"}); err != nil {
		t.Fatalf("push without keys should be a no-op, got: %v", err)
	}
}

func TestListNotificationsRejectsAnInvalidUser(t *testing.T) {
	svc, _ := newService(t)
	if _, err := svc.ListNotifications(context.Background(), "not-a-uuid", false, 10); err == nil {
		t.Fatal("an invalid user id must be rejected")
	}
	if _, err := svc.UnreadCount(context.Background(), uuid.Nil); err != nil {
		t.Logf("unread count for nil uuid: %v", err)
	}
}

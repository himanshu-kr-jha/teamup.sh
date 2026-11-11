package notifications

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"context"
	"github.com/teamup/teamup/internal/auth"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// An announcement reaches everybody in the organization, so the notifications
// are written in one statement rather than one round trip per person.
func TestNotifyManyWritesOneRowPerRecipient(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	second := testsupport.SeedUser(t, pool, org, "many-b@example.test", "member")
	third := testsupport.SeedUser(t, pool, org, "many-c@example.test", "member")

	recipients := []uuid.UUID{org.OwnerID, second, third}
	if err := svc.NotifyMany(ctx, org.ID, recipients, "chat.announcement",
		"All hands", "Friday at three", map[string]any{"channelId": "abc"}); err != nil {
		t.Fatalf("notify many: %v", err)
	}

	for _, who := range recipients {
		var count int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND type = 'chat.announcement'`,
			who).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 1 {
			t.Errorf("recipient %s got %d notifications, want 1", who, count)
		}
	}
}

func TestNotifyManyWithNobodyToTellIsHarmless(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	if err := svc.NotifyMany(ctx, org.ID, nil, "chat.announcement", "Nobody", "there", nil); err != nil {
		t.Errorf("notifying nobody should be a no-op: %v", err)
	}
}

// realSubscription builds keys a push service would have handed the browser, so
// the encryption in delivery is exercised rather than stubbed.
func realSubscription(t *testing.T, endpoint string) *PushSubscription {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate subscription key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	return &PushSubscription{
		Endpoint: endpoint,
		P256DH:   base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

// The whole delivery path, as far as it can be taken without a real push
// service: a stored subscription is signed for, encrypted, and posted to the
// endpoint the browser gave us.
func TestAPushIsDeliveredToTheEndpointTheBrowserGaveUs(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	received := make(chan *http.Request, 1)
	var body []byte
	push := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		received <- r
		w.WriteHeader(http.StatusCreated)
	}))
	defer push.Close()

	public, private, err := auth.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate VAPID keys: %v", err)
	}
	svc.SetVAPIDKeys(public, private)

	if err := svc.SaveSubscription(ctx, org.OwnerID.String(), org.ID.String(),
		realSubscription(t, push.URL+"/send")); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := svc.SendToUser(ctx, org.OwnerID.String(), PushPayload{
		Title: "QA Bob", Body: "are you around?", Tag: "chat-1",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case r := <-received:
		// Signed as us, so the push service will accept it at all.
		if !strings.HasPrefix(r.Header.Get("Authorization"), "vapid") {
			t.Errorf("a push has to be signed, got %q", r.Header.Get("Authorization"))
		}
		// Encrypted to the subscription's key: the push service must not be
		// able to read what it is carrying.
		if r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("payload should be encrypted, got %q", r.Header.Get("Content-Encoding"))
		}
		if bytes.Contains(body, []byte("are you around?")) {
			t.Error("the message went out in the clear")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nothing was delivered")
	}
}

// A subscription the browser has thrown away answers 410, and keeping it means
// writing to it forever.
func TestADeadSubscriptionIsForgotten(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)

	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer gone.Close()

	public, private, err := auth.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate VAPID keys: %v", err)
	}
	svc.SetVAPIDKeys(public, private)

	if err := svc.SaveSubscription(ctx, org.OwnerID.String(), org.ID.String(),
		realSubscription(t, gone.URL+"/send")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := svc.SendToUser(ctx, org.OwnerID.String(), PushPayload{Title: "x", Body: "y"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	subs, _ := svc.getSubscriptions(ctx, org.OwnerID.String())
	if len(subs) != 0 {
		t.Errorf("a dead subscription should be dropped, got %d", len(subs))
	}
}

// A title is built from something people named, which may be longer than the
// column. Too long, the insert failed and the person was told nothing at all.
func TestALongTitleIsShortenedRatherThanLosingTheNotification(t *testing.T) {
	pool := testsupport.DB(t)
	org := testsupport.SeedOrg(t, pool)
	svc := NewService(pool, nil, Config{})
	ctx := context.Background()
	long := strings.Repeat("é", 400)

	if err := svc.Notify(ctx, org.ID, org.OwnerID, "task.added", long, "body", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if err := svc.NotifyMany(ctx, org.ID, []uuid.UUID{org.OwnerID}, "task.added", long, "body", nil); err != nil {
		t.Fatalf("notify many: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT title FROM notifications WHERE user_id = $1`, org.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	titles, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(titles) != 2 {
		t.Fatalf("both are there, got %d", len(titles))
	}
	for _, title := range titles {
		runes := []rune(title)
		if len(runes) != 255 || runes[254] != '…' || !utf8.ValidString(title) {
			t.Errorf("255 characters ending in an ellipsis, cut on a character: %d, %q", len(runes), title)
		}
	}
}

func TestUnreadTasksAreTheTasksWithSomethingUnreadAndOpeningOneReadsIt(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()
	org := testsupport.SeedOrg(t, pool)
	live := &recordingBroadcaster{}
	svc.SetBroadcaster(live)
	first, second := uuid.New(), uuid.New()
	for _, n := range []struct {
		kind string
		data map[string]any
	}{
		{"task.updated", map[string]any{"taskId": first.String()}},
		{"task.comment", map[string]any{"taskId": first.String()}},
		{"task.assigned", map[string]any{"taskId": second.String()}},
		{"meeting.invited", map[string]any{"callId": "x"}},
	} {
		if err := svc.Notify(ctx, org.ID, org.OwnerID, n.kind, "t", "b", n.data); err != nil {
			t.Fatalf("notify: %v", err)
		}
	}

	ids, err := svc.UnreadTasks(ctx, org.OwnerID)
	if err != nil {
		t.Fatalf("unread tasks: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("want the two tasks, got %v", ids)
	}

	before := live.count(org.OwnerID.String())
	if err := svc.MarkTaskRead(ctx, org.OwnerID, first); err != nil {
		t.Fatalf("mark task read: %v", err)
	}
	if live.count(org.OwnerID.String()) != before+1 {
		t.Error("other tabs were not told the task was read")
	}
	ids, _ = svc.UnreadTasks(ctx, org.OwnerID)
	if len(ids) != 1 || ids[0] != second.String() {
		t.Errorf("after reading the first task, want only the second, got %v", ids)
	}
	count, _ := svc.UnreadCount(ctx, org.OwnerID)
	if count != 2 {
		t.Errorf("the meeting and the second task should still be unread, got %d", count)
	}

	// Reading it again changes nothing, and tells nobody.
	before = live.count(org.OwnerID.String())
	if err := svc.MarkTaskRead(ctx, org.OwnerID, first); err != nil {
		t.Fatalf("mark again: %v", err)
	}
	if live.count(org.OwnerID.String()) != before {
		t.Error("a read that read nothing was announced")
	}
}

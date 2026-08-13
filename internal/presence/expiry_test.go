package presence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// person returns ids for somebody nobody else in the suite is using, and
// removes their key afterwards.
func person(t *testing.T, client *redis.Client) (userID, orgID string) {
	t.Helper()

	userID, orgID = uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		client.Del(context.Background(), "presence:"+orgID+":"+userID)
	})
	return userID, orgID
}

// The presence hashes were written with no expiry at all, so the only thing
// that ever removed one was Unregister on a clean shutdown. A process that is
// killed, OOMs, or is rescheduled leaves every user it served showing online
// for ever — there is no other writer that would ever correct it.
func TestPresenceKeysCarryAnExpiry(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	userID, orgID := person(t, client)

	if err := svc.SetOnline(userID, orgID); err != nil {
		t.Fatalf("set online: %v", err)
	}

	ttl, err := client.PTTL(context.Background(), "presence:"+orgID+":"+userID).Result()
	if err != nil {
		t.Fatalf("read ttl: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("the presence key has no expiry (ttl %v): a process that dies leaves this user online for ever", ttl)
	}
}

// The point of the expiry: an entry whose owner stops refreshing it goes away
// by itself.
func TestPresenceDisappearsWhenNobodyRefreshesIt(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	svc.ttl = 300 * time.Millisecond
	userID, orgID := person(t, client)

	if err := svc.SetOnline(userID, orgID); err != nil {
		t.Fatalf("set online: %v", err)
	}

	got, err := svc.GetPresence(userID, orgID)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if got == nil {
		t.Fatal("they should be online immediately after connecting")
	}

	// Nobody refreshes: this is the process having been killed.
	time.Sleep(700 * time.Millisecond)

	got, err = svc.GetPresence(userID, orgID)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if got != nil {
		t.Errorf("they are still shown as %q long after the process serving them stopped", got.Status)
	}
}

// Somebody who is still connected must not be expired out from under them. The
// heartbeat the writer already sends every thirty seconds is what refreshes it.
func TestRefreshingKeepsSomebodyWhoIsStillHere(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	svc.ttl = 600 * time.Millisecond
	userID, orgID := person(t, client)

	if err := svc.SetOnline(userID, orgID); err != nil {
		t.Fatalf("set online: %v", err)
	}

	// Two refreshes inside one lifetime, as a live connection would.
	for i := 0; i < 2; i++ {
		time.Sleep(300 * time.Millisecond)
		if err := svc.Touch(userID, orgID); err != nil {
			t.Fatalf("touch: %v", err)
		}
	}

	// Past the original expiry, but refreshed throughout.
	got, err := svc.GetPresence(userID, orgID)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if got == nil {
		t.Fatal("somebody who is still connected was expired")
	}
	if got.Status != "online" {
		t.Errorf("status: got %q want online", got.Status)
	}
}

// Refreshing must not resurrect somebody. If the entry has already gone the
// user is still connected, so it is re-established rather than left missing —
// but it must never invent presence for a key that was deliberately removed
// and an owner that is no longer connected.
func TestRefreshingReestablishesAnEntryThatAlreadyExpired(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)
	userID, orgID := person(t, client)

	if err := svc.Touch(userID, orgID); err != nil {
		t.Fatalf("touch: %v", err)
	}

	got, err := svc.GetPresence(userID, orgID)
	if err != nil {
		t.Fatalf("read presence: %v", err)
	}
	if got == nil {
		t.Fatal("a connection that is still here should have its entry re-established")
	}
	ttl, err := client.PTTL(context.Background(), "presence:"+orgID+":"+userID).Result()
	if err != nil {
		t.Fatalf("read ttl: %v", err)
	}
	if ttl <= 0 {
		t.Errorf("the re-established key has no expiry (ttl %v)", ttl)
	}
}

// GetAllOnline scans the keyspace for one organisation's keys. Whichever way it
// walks Redis, it must never answer with somebody else's tenant.
func TestGetAllOnlineOnlySeesItsOwnOrganisation(t *testing.T) {
	client := testRedis(t)
	svc := NewService(client)

	mine, orgA := person(t, client)
	theirs, orgB := person(t, client)

	if err := svc.SetOnline(mine, orgA); err != nil {
		t.Fatalf("set online: %v", err)
	}
	if err := svc.SetOnline(theirs, orgB); err != nil {
		t.Fatalf("set online: %v", err)
	}

	online, err := svc.GetAllOnline(orgA)
	if err != nil {
		t.Fatalf("get all online: %v", err)
	}

	found := false
	for _, p := range online {
		if p.UserID == theirs {
			t.Errorf("another organisation's user leaked into the roster: %s", p.UserID)
		}
		if p.OrgID != orgA {
			t.Errorf("a presence from org %q was returned for org %q", p.OrgID, orgA)
		}
		if p.UserID == mine {
			found = true
		}
	}
	if !found {
		t.Errorf("our own online user was not returned: %v", online)
	}
}

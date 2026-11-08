package chat

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/teamup/teamup/internal/testsupport"
	"github.com/teamup/teamup/pkg/protocol"
)

type recordingNotifier struct {
	mu    sync.Mutex
	users []uuid.UUID
}

func (r *recordingNotifier) Notify(_ context.Context, _ uuid.UUID, userID uuid.UUID, kind, _, _ string, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if kind == "chat.mention" {
		r.users = append(r.users, userID)
	}
	return nil
}

func (r *recordingNotifier) NotifyMany(context.Context, uuid.UUID, []uuid.UUID, string, string, string, map[string]any) error {
	return nil
}

func (r *recordingNotifier) PushToUsers(context.Context, []uuid.UUID, protocol.Push) {}

func (r *recordingNotifier) mentioned() []uuid.UUID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uuid.UUID(nil), r.users...)
}

func TestSomebodyIsMentionedByUsernameOrByNameAndOnlyThem(t *testing.T) {
	svc, pool := newService(t)
	svc.SetLogger(zerolog.Nop())
	n := &recordingNotifier{}
	svc.SetNotifier(n)
	org := testsupport.SeedOrg(t, pool)
	alice := testsupport.SeedUser(t, pool, org, "alice@example.com", "member")
	bob := testsupport.SeedUser(t, pool, org, "bob.smith@example.com", "member")
	outsider := testsupport.SeedUser(t, pool, org, "carol@example.com", "member")
	_ = outsider
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID, alice, bob)
	ctx := context.Background()

	if _, err := svc.SendMessage(ctx, org.ID, ch, org.OwnerID, "thanks @alice, and @Bob.Smith. Also @carol.", nil, nil, false); err != nil {
		t.Fatal(err)
	}
	got := n.mentioned()
	want := map[uuid.UUID]bool{alice: true, bob: true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Errorf("alice and bob, by username, case and a full stop aside; carol is not in the channel: %v", got)
	}
}

func TestHistoryComesADayAtATimeInTheReadersTimeZone(t *testing.T) {
	svc, pool := newService(t)
	svc.SetLogger(zerolog.Nop())
	org := testsupport.SeedOrg(t, pool)
	ch := testsupport.SeedChannel(t, pool, org, "general", org.OwnerID)
	ctx := context.Background()
	kolkata, _ := time.LoadLocation("Asia/Kolkata")

	at := func(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, kolkata) }
	post := func(when time.Time, text string) {
		if _, err := pool.Exec(ctx, `INSERT INTO messages (org_id, channel_id, user_id, content, created_at) VALUES ($1, $2, $3, $4, $5)`,
			org.ID, ch, org.OwnerID, text, when); err != nil {
			t.Fatal(err)
		}
	}
	post(at(1, 10), "first of november")
	post(at(5, 9), "fifth, morning")
	post(at(5, 23), "fifth, late")
	// 01:00 on the 6th in Kolkata is still the 5th in UTC.
	post(at(6, 1), "sixth, small hours")
	post(at(6, 12), "sixth, noon")

	texts := func(ms []Message) []string {
		out := []string{}
		for _, m := range ms {
			out = append(out, m.Content)
		}
		return out
	}

	day, more, err := svc.GetMessagesForDay(ctx, ch, nil, nil, kolkata)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(day); len(got) != 2 || got[0] != "sixth, small hours" || got[1] != "sixth, noon" || !more {
		t.Fatalf("the newest day, in Kolkata: %v more=%v", got, more)
	}

	day, more, _ = svc.GetMessagesForDay(ctx, ch, &day[0].CreatedAt, &day[0].ID, kolkata)
	if got := texts(day); len(got) != 2 || got[0] != "fifth, morning" || !more {
		t.Fatalf("the day before: %v", got)
	}

	// Days with nothing in them are skipped.
	day, more, _ = svc.GetMessagesForDay(ctx, ch, &day[0].CreatedAt, &day[0].ID, kolkata)
	if got := texts(day); len(got) != 1 || got[0] != "first of november" || more {
		t.Fatalf("back to the first, and nothing older: %v more=%v", got, more)
	}

	empty := testsupport.SeedChannel(t, pool, org, "empty", org.OwnerID)
	day, more, err = svc.GetMessagesForDay(ctx, empty, nil, nil, kolkata)
	if err != nil || len(day) != 0 || more {
		t.Errorf("an empty conversation: %v %v %v", day, more, err)
	}
}

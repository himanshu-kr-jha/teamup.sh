package presence

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/teamup/teamup/internal/testsupport"
)

// orgHeadcount is the size this product is built for. The roster is fetched on
// every poll by every open tab, so its cost has to be counted per person in the
// organization, not per person connected.
const orgHeadcount = 60

// roundTrips counts how many times the client goes to Redis and waits: once
// per single command, and once per pipeline however many commands it carries.
// That is the number that matters — the loop's cost was latency multiplied by
// the size of the organization, not the work Redis did.
type roundTrips struct {
	mu sync.Mutex
	n  int
}

func (h *roundTrips) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}

func (h *roundTrips) record() {
	h.mu.Lock()
	h.n++
	h.mu.Unlock()
}

func (h *roundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *roundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.record()
		return next(ctx, cmd)
	}
}

func (h *roundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.record()
		return next(ctx, cmds)
	}
}

// The availability roster read one Redis hash per person in a sequential loop,
// so a 60-person organization cost 60 blocking round trips — every poll, from
// every open tab, to render a column of dots.
func TestTheAvailabilityRosterReadsRedisInOneRoundTrip(t *testing.T) {
	pool := testsupport.DB(t)
	client := testRedis(t)
	trips := &roundTrips{}
	client.AddHook(trips)

	svc := NewService(client)
	svc.SetDB(pool)
	ctx := context.Background()

	org := testsupport.SeedOrg(t, pool)
	people := []uuid.UUID{org.OwnerID}
	for i := 0; i < orgHeadcount-1; i++ {
		people = append(people,
			testsupport.SeedUser(t, pool, org, fmt.Sprintf("roster%d@example.test", i), "member"))
	}

	// A second organization, whose presence must neither be read nor reported.
	theirs := testsupport.SeedOrg(t, pool)
	theirMember := testsupport.SeedUser(t, pool, theirs, "theirs@example.test", "member")

	// Half of them connected, one of those in a call, so the answer is not
	// uniform and a batched read has something to get wrong.
	inCall := people[1]
	for i, id := range people {
		if i%2 == 1 {
			fields := map[string]interface{}{"status": "online"}
			if id == inCall {
				fields["in_call"] = "1"
			}
			if err := svc.writeEntry(ctx, presenceKey(org.ID.String(), id.String()), fields); err != nil {
				t.Fatalf("seed presence: %v", err)
			}
		}
	}
	// The other organization's owner is connected too.
	if err := svc.writeEntry(ctx, presenceKey(theirs.ID.String(), theirs.OwnerID.String()),
		map[string]interface{}{"status": "online"}); err != nil {
		t.Fatalf("seed foreign presence: %v", err)
	}
	t.Cleanup(func() {
		for _, id := range people {
			client.Del(context.Background(), presenceKey(org.ID.String(), id.String()))
		}
		client.Del(context.Background(), presenceKey(theirs.ID.String(), theirs.OwnerID.String()))
	})

	before := trips.count()
	roster, err := svc.ListOrgAvailability(ctx, org.ID)
	if err != nil {
		t.Fatalf("list availability: %v", err)
	}
	used := trips.count() - before

	if len(roster) != orgHeadcount {
		t.Fatalf("roster: got %d people want %d", len(roster), orgHeadcount)
	}

	// One batch, however many people are in it. Two is allowed only because a
	// pipeline that is split by the client is still one wait per batch; what is
	// forbidden is a number that grows with the organization.
	if used > 2 {
		t.Errorf("reading presence for %d people took %d Redis round trips, want one batch",
			orgHeadcount, used)
	}

	// Batching must not change the answer.
	status := map[uuid.UUID]string{}
	for _, p := range roster {
		status[p.UserID] = p.Status
		if p.UserID == theirs.OwnerID || p.UserID == theirMember {
			t.Errorf("another organization's member %s appeared in the roster", p.UserID)
		}
	}
	for i, id := range people {
		want := "away"
		if i%2 == 1 {
			want = "available"
		}
		if id == inCall {
			want = "busy"
		}
		if got := status[id]; got != want {
			t.Errorf("person %d: status %q want %q", i, got, want)
		}
	}
}

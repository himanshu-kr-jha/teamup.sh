package status

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore keeps one hash per minute, shared by every replica.
type RedisStore struct {
	client *redis.Client
	prefix string
}

// NewRedisStore stores under "teamup:status:".
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client, prefix: "teamup:status:"}
}

func (r *RedisStore) key(minute int64) string { return r.prefix + strconv.FormatInt(minute, 10) }

// keep is a little longer than the window, so the oldest bar is never half gone.
const keep = Window + 2*time.Hour

// Record writes a pass only where nothing is written yet, and a failure always,
// so one replica's failure is not hidden by another's pass.
func (r *RedisStore) Record(ctx context.Context, minute int64, results map[string]bool) error {
	key := r.key(minute)
	pipe := r.client.Pipeline()
	for component, up := range results {
		if up {
			pipe.HSetNX(ctx, key, component, "1")
		} else {
			pipe.HSet(ctx, key, component, "0")
		}
	}
	pipe.Expire(ctx, key, keep)
	_, err := pipe.Exec(ctx)
	return err
}

// Load reads every minute in one round trip.
func (r *RedisStore) Load(ctx context.Context, first, last int64) (map[int64]map[string]bool, error) {
	pipe := r.client.Pipeline()
	cmds := make(map[int64]*redis.MapStringStringCmd, last-first+1)
	for m := first; m <= last; m++ {
		cmds[m] = pipe.HGetAll(ctx, r.key(m))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	out := make(map[int64]map[string]bool)
	for m, cmd := range cmds {
		fields, err := cmd.Result()
		if err != nil || len(fields) == 0 {
			continue
		}
		bucket := make(map[string]bool, len(fields))
		for k, v := range fields {
			bucket[k] = v == "1"
		}
		out[m] = bucket
	}
	return out, nil
}

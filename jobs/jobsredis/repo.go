package jobsredis

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/frostgrove/vv/jobs"
	"github.com/redis/go-redis/v9"
)

type repository struct {
	client redis.UniversalClient
	base   string
}

type storedEntry struct {
	ID               string
	Definition       string
	Codec            string
	Version          jobs.SchemaVersion
	Priority         int
	State            jobs.InvocationState
	ReadyAt          time.Time
	RecordSize       int
	Record           []byte
	LeaseToken       []byte
	LeaseIncarnation [jobs.WorkerIncarnationBytes]byte
	LeaseUntil       time.Time
	Intents          []string
	ExcludedBinding  string
	ExcludedBuild    string
}

var releaseLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

func newRepository(client redis.UniversalClient, base string) repository {
	return repository{client: client, base: base}
}

func (r repository) prepare(ctx context.Context) error {
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("jobsredis: ping: %w", err)
	}
	key := r.base + ":format"
	created, err := r.client.SetNX(ctx, key, FormatVersion, 0).Result()
	if err != nil {
		return fmt.Errorf("jobsredis: prepare: %w", err)
	}
	if created {
		return nil
	}
	version, err := r.client.Get(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("jobsredis: format: %w", err)
	}
	if version != FormatVersion {
		return fmt.Errorf("%w: found %q, expected %q", ErrFormatMismatch, version, FormatVersion)
	}
	return nil
}

func (r repository) lock(ctx context.Context, token []byte, ttl time.Duration) (func(), error) {
	encoded := hex.EncodeToString(token)
	key := r.base + ":mutation-lock"
	for {
		acquired, err := r.client.SetNX(ctx, key, encoded, ttl).Result()
		if err != nil {
			return nil, fmt.Errorf("jobsredis: acquire lock: %w", err)
		}
		if acquired {
			return func() {
				releaseCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
				defer cancel()
				_, _ = releaseLockScript.Run(releaseCtx, r.client, []string{key}, encoded).Result()
			}, nil
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (r repository) now(ctx context.Context) (time.Time, error) {
	value, err := r.client.Time(ctx).Result()
	if err != nil {
		return time.Time{}, fmt.Errorf("jobsredis: server time: %w", err)
	}
	value = value.Round(0).UTC()
	if value.IsZero() || value.Year() < 1 || value.Year() > 9999 {
		return time.Time{}, jobs.ErrInvalid
	}
	return value, nil
}

func (r repository) entry(ctx context.Context, id string) (storedEntry, bool, error) {
	encoded, err := r.client.Get(ctx, r.entryKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return storedEntry{}, false, nil
	}
	if err != nil {
		return storedEntry{}, false, fmt.Errorf("jobsredis: read delivery: %w", err)
	}
	var entry storedEntry
	if err := json.Unmarshal(encoded, &entry); err != nil {
		return storedEntry{}, false, fmt.Errorf("jobsredis: decode delivery metadata: %w", err)
	}
	if entry.ID != id || len(entry.Record) == 0 || entry.RecordSize < 1 {
		return storedEntry{}, false, fmt.Errorf("jobsredis: %w: invalid delivery metadata", jobs.ErrInvalid)
	}
	return entry, true, nil
}

func (r repository) intent(ctx context.Context, intent string) (storedEntry, bool, error) {
	id, err := r.client.Get(ctx, r.intentKey(intent)).Result()
	if errors.Is(err, redis.Nil) {
		return storedEntry{}, false, nil
	}
	if err != nil {
		return storedEntry{}, false, fmt.Errorf("jobsredis: read intent: %w", err)
	}
	entry, found, err := r.entry(ctx, id)
	if err != nil || found {
		return entry, found, err
	}
	if err := r.client.Del(ctx, r.intentKey(intent)).Err(); err != nil {
		return storedEntry{}, false, fmt.Errorf("jobsredis: remove stale intent: %w", err)
	}
	return storedEntry{}, false, nil
}

func (r repository) priorities(ctx context.Context) ([]int, error) {
	values, err := r.client.ZRange(ctx, r.prioritiesKey(), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("jobsredis: read priorities: %w", err)
	}
	result := make([]int, 0, len(values))
	for _, value := range values {
		priority, parseErr := strconv.Atoi(value)
		if parseErr != nil || priority < 1 || priority > jobs.MaximumPriority {
			return nil, fmt.Errorf("jobsredis: %w: invalid priority index", jobs.ErrInvalid)
		}
		result = append(result, priority)
	}
	return result, nil
}

func (r repository) readyIDs(ctx context.Context, priority int, definition string, now time.Time, count int64) ([]string, error) {
	values, err := r.client.ZRangeByScore(ctx, r.readyKey(priority, definition), &redis.ZRangeBy{
		Min:    "-inf",
		Max:    strconv.FormatInt(now.UnixMilli(), 10),
		Offset: 0,
		Count:  count,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("jobsredis: read ready deliveries: %w", err)
	}
	return values, nil
}

func (r repository) recoveryIDs(ctx context.Context, incarnation jobs.WorkerIncarnation, now time.Time, limit int64) ([]string, error) {
	expired, err := r.client.ZRangeByScore(ctx, r.leasedKey(), &redis.ZRangeBy{
		Min: "-inf", Max: strconv.FormatInt(now.UnixMilli(), 10), Offset: 0, Count: limit + 1,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("jobsredis: read expired leases: %w", err)
	}
	owned, err := r.client.ZRange(ctx, r.incarnationKey(incarnation.Bytes()), 0, limit).Result()
	if err != nil {
		return nil, fmt.Errorf("jobsredis: read worker leases: %w", err)
	}
	seen := make(map[string]struct{}, len(expired)+len(owned))
	result := make([]string, 0, len(expired)+len(owned))
	for _, values := range [][]string{expired, owned} {
		for _, id := range values {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result, nil
}

func (r repository) save(ctx context.Context, previous *storedEntry, current *storedEntry) error {
	previousIntents := make([]string, 0)
	if previous != nil {
		for _, intent := range previous.Intents {
			id, err := r.client.Get(ctx, r.intentKey(intent)).Result()
			if err == nil && id == previous.ID {
				previousIntents = append(previousIntents, intent)
			} else if err != nil && !errors.Is(err, redis.Nil) {
				return fmt.Errorf("jobsredis: inspect intent: %w", err)
			}
		}
	}
	_, err := r.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		if previous != nil {
			pipe.ZRem(ctx, r.readyKey(previous.Priority, previous.Definition), previous.ID)
			pipe.ZRem(ctx, r.leasedKey(), previous.ID)
			if previous.LeaseIncarnation != [jobs.WorkerIncarnationBytes]byte{} {
				pipe.ZRem(ctx, r.incarnationKey(previous.LeaseIncarnation), previous.ID)
			}
			for _, intent := range previousIntents {
				pipe.Del(ctx, r.intentKey(intent))
			}
		}
		if current == nil {
			if previous != nil {
				pipe.Del(ctx, r.entryKey(previous.ID))
				pipe.SRem(ctx, r.deliveriesKey(), previous.ID)
			}
			return nil
		}
		encoded, encodeErr := json.Marshal(current)
		if encodeErr != nil {
			return fmt.Errorf("jobsredis: encode delivery metadata: %w", encodeErr)
		}
		pipe.Set(ctx, r.entryKey(current.ID), encoded, 0)
		pipe.SAdd(ctx, r.deliveriesKey(), current.ID)
		pipe.ZAdd(ctx, r.prioritiesKey(), redis.Z{Score: float64(current.Priority), Member: strconv.Itoa(current.Priority)})
		if len(current.LeaseToken) > 0 {
			pipe.ZAdd(ctx, r.leasedKey(), redis.Z{Score: float64(current.LeaseUntil.UnixMilli()), Member: current.ID})
			pipe.ZAdd(ctx, r.incarnationKey(current.LeaseIncarnation), redis.Z{Score: float64(current.LeaseUntil.UnixMilli()), Member: current.ID})
		} else if current.State == jobs.InvocationQueued && !current.ReadyAt.IsZero() {
			pipe.ZAdd(ctx, r.readyKey(current.Priority, current.Definition), redis.Z{Score: float64(current.ReadyAt.UnixMilli()), Member: current.ID})
		}
		for _, intent := range current.Intents {
			pipe.Set(ctx, r.intentKey(intent), current.ID, 0)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("jobsredis: persist delivery: %w", err)
	}
	return nil
}

func (r repository) entryKey(id string) string {
	return r.base + ":delivery:" + id
}

func (r repository) intentKey(intent string) string {
	return r.base + ":intent:" + intent
}

func (r repository) readyKey(priority int, definition string) string {
	return r.base + ":ready:" + strconv.Itoa(priority) + ":" + definition
}

func (r repository) prioritiesKey() string {
	return r.base + ":priorities"
}

func (r repository) leasedKey() string {
	return r.base + ":leased"
}

func (r repository) deliveriesKey() string {
	return r.base + ":deliveries"
}

func (r repository) incarnationKey(value [jobs.WorkerIncarnationBytes]byte) string {
	return r.base + ":incarnation:" + hex.EncodeToString(value[:])
}

func sameLease(entry storedEntry, lease jobs.LeaseRef) bool {
	return entry.ID == lease.InvocationID().String() && bytes.Equal(entry.LeaseToken, lease.DriverToken())
}

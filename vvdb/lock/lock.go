package lock

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/frostgrove/vv/crud"
)

type Policy struct {
	Timeout time.Duration
	Retries int
}

// Locks is bound to one source because an advisory lock is scoped to one database: the identity of
// a critical section is the pair of the database and the key, never the key alone. Two Locks over
// two databases do not exclude each other even on an identical Key, and two over the same database
// do, across pools and processes.
type Locks struct {
	source  crud.Source
	backend backend
	policy  Policy
}

func For(source crud.Source, policy Policy) (*Locks, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: no source", ErrDialectUnsupported)
	}
	resolved, err := backendFor(source.Dialect())
	if err != nil {
		return nil, err
	}
	if policy.Retries < 1 {
		policy.Retries = 1
	}
	return &Locks{source: source, backend: resolved, policy: policy}, nil
}

func (this *Locks) Take(ctx context.Context, exec crud.Executor, guards ...Guard) error {
	if len(guards) == 0 {
		return nil
	}
	if !crud.IsTransaction(exec) {
		return ErrNoTransaction
	}
	if this.policy.Timeout > 0 {
		statement, args := this.backend.timeout(this.policy.Timeout)
		if _, err := exec.Exec(ctx, statement, args...); err != nil {
			return fmt.Errorf("lock: setting the lock timeout: %w", err)
		}
	}
	for _, guard := range ordered(guards) {
		statement, args := this.backend.take(guard)
		if _, err := exec.Exec(ctx, statement, args...); err != nil {
			return fmt.Errorf("lock: acquiring %d: %w", int64(guard.Key), err)
		}
	}
	return nil
}

func (this *Locks) TryTake(ctx context.Context, exec crud.Executor, guard Guard) (bool, error) {
	if !crud.IsTransaction(exec) {
		return false, ErrNoTransaction
	}
	statement, args := this.backend.try(guard)
	rows, err := exec.Query(ctx, statement, args...)
	if err != nil {
		return false, fmt.Errorf("lock: trying %d: %w", int64(guard.Key), err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, fmt.Errorf("lock: trying %d: %w", int64(guard.Key), err)
		}
		return false, fmt.Errorf("lock: trying %d: %w", int64(guard.Key), errNoRow)
	}
	var taken bool
	if err := rows.Scan(&taken); err != nil {
		return false, fmt.Errorf("lock: trying %d: %w", int64(guard.Key), err)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("lock: trying %d: %w", int64(guard.Key), err)
	}
	return taken, nil
}

// Guarded runs fn in a transaction of its own that holds every guard for as long as it lasts, and
// runs the whole thing again when the engine answers with a failure a second attempt fixes.
//
// Retrying here rather than handing the failure back is what [[D-139]] narrowed [[D-040]] to
// allow: the transaction is opened here and nobody else can see it, so replaying it is a retry
// rather than a second attempt at somebody else's broken transaction.
func (this *Locks) Guarded(ctx context.Context, guards []Guard, fn func(ctx context.Context) error) error {
	return this.Retry(ctx, func(ctx context.Context) error {
		return crud.InNewTx(ctx, this.source, func(ctx context.Context) error {
			executor, bound, err := crud.SourceBoundExecutorFor(ctx, this.source)
			if err != nil {
				// InNewTx has just opened a transaction on this very source, so a scope failure here
				// names which of mismatch, invalid session or missing source the binding hit.
				// Reporting it as ErrNoTransaction hid all three behind the one thing it is not.
				return fmt.Errorf("lock: resolving the transaction just opened: %w", err)
			}
			if !bound {
				return ErrNoTransaction
			}
			if err := this.Take(ctx, executor, guards...); err != nil {
				return err
			}
			return fn(ctx)
		})
	})
}

func (this *Locks) Retry(ctx context.Context, fn func(ctx context.Context) error) error {
	var last error
	for attempt := 1; attempt <= this.policy.Retries; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if !this.Retryable(err) {
			return err
		}
		last = err
		if attempt == this.policy.Retries {
			break
		}
		if !sleep(ctx, backoff(retryBase, this.policy.Timeout, attempt)) {
			return ctx.Err()
		}
	}
	return last
}

// Guards are taken in one deterministic order — ascending key, and an exclusive request before a
// shared one on the same key — so that two callers asking for the same pair cannot take them in
// opposite orders and deadlock. Which order it is does not matter and is not observable: every
// guard is held before fn runs. Duplicates collapse to the strongest request, because taking the
// same key twice in one transaction is redundant and asking for it both ways means exclusive.
func ordered(guards []Guard) []Guard {
	sorted := append(make([]Guard, 0, len(guards)), guards...)
	sort.SliceStable(sorted, func(left, right int) bool {
		if sorted[left].Key != sorted[right].Key {
			return sorted[left].Key < sorted[right].Key
		}
		return !sorted[left].Shared && sorted[right].Shared
	})
	kept := make([]Guard, 0, len(sorted))
	for _, guard := range sorted {
		if len(kept) > 0 && kept[len(kept)-1].Key == guard.Key {
			continue
		}
		kept = append(kept, guard)
	}
	return kept
}

const retryBase = 50 * time.Millisecond

func backoff(base, max time.Duration, attempt int) time.Duration {
	wait := base
	for i := 1; i < attempt && wait < max; i++ {
		wait *= 2
	}
	if max > 0 && wait > max {
		wait = max
	}
	return wait
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

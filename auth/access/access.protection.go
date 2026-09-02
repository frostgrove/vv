package access

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frostgrove/vv/errs"
)

type Attempt struct {
	Subject SubjectType

	Identifier string

	IP string
}

type AttemptOutcome string

const (
	AttemptSucceeded AttemptOutcome = "succeeded"

	AttemptFailed AttemptOutcome = "failed"

	AttemptRefused AttemptOutcome = "refused"
)

type AttemptLimiter interface {
	Admit(ctx context.Context, attempt Attempt) error

	Record(ctx context.Context, attempt Attempt, outcome AttemptOutcome) error
}

type AttemptObserver interface {
	AttemptObserved(ctx context.Context, attempt Attempt, outcome AttemptOutcome)
}

type Protection struct {
	Limiter AttemptLimiter

	Observer AttemptObserver
}

const (
	CodeTooManyAttempts errs.Code = "too_many_attempts"

	CodeOverloaded errs.Code = "overloaded"
)

func TooManyAttempts() error {
	return errs.Retryable().
		Code(CodeTooManyAttempts).
		Message("too many sign-in attempts from here; wait before trying again").
		Entity("Credential").Op("Login").Fault()
}

func Overloaded() error {
	return errs.Retryable().
		Code(CodeOverloaded).
		Message("the service is busy; try again").
		Entity("Credential").Fault()
}

func (this *Deps) admit(ctx context.Context, attempt Attempt) error {
	if this.protection.Limiter == nil {
		return nil
	}
	if err := this.protection.Limiter.Admit(ctx, attempt); err != nil {
		this.observe(ctx, attempt, AttemptRefused)
		return err
	}
	return nil
}

func (this *Deps) recordAttempt(ctx context.Context, attempt Attempt, outcome AttemptOutcome) {
	if this.protection.Limiter != nil {
		if err := this.protection.Limiter.Record(ctx, attempt, outcome); err != nil {
			this.Log.WarnContext(ctx, "could not record a sign-in attempt",
				slog.String("subject_type", string(attempt.Subject)), slog.Any("err", err))
		}
	}
	this.observe(ctx, attempt, outcome)
}

func (this *Deps) observe(ctx context.Context, attempt Attempt, outcome AttemptOutcome) {
	if this.protection.Observer == nil {
		return
	}
	this.protection.Observer.AttemptObserved(ctx, attempt, outcome)
}

type AttemptPolicy struct {
	MaxPerIdentifier int

	MaxPerIP int

	Window time.Duration

	LockFor time.Duration

	Now func() time.Time
}

const (
	DefaultAttemptsPerIdentifier = 10
	DefaultAttemptsPerIP         = 50
	DefaultAttemptWindow         = 15 * time.Minute
	DefaultAttemptLock           = 15 * time.Minute
)

func (this AttemptPolicy) withDefaults() AttemptPolicy {
	if this.MaxPerIdentifier <= 0 {
		this.MaxPerIdentifier = DefaultAttemptsPerIdentifier
	}
	if this.MaxPerIP <= 0 {
		this.MaxPerIP = DefaultAttemptsPerIP
	}
	if this.Window <= 0 {
		this.Window = DefaultAttemptWindow
	}
	if this.LockFor <= 0 {
		this.LockFor = DefaultAttemptLock
	}
	if this.Now == nil {
		this.Now = time.Now
	}
	return this
}

type MemoryLimiter struct {
	policy AttemptPolicy

	mu       sync.Mutex
	counters map[string]*attemptCounter
}

type attemptCounter struct {
	failures int
	opened   time.Time
	lockedTo time.Time
}

func NewMemoryLimiter(policy AttemptPolicy) *MemoryLimiter {
	return &MemoryLimiter{policy: policy.withDefaults(), counters: map[string]*attemptCounter{}}
}

func DefaultMemoryLimiter() *MemoryLimiter { return NewMemoryLimiter(AttemptPolicy{}) }

var _ AttemptLimiter = (*MemoryLimiter)(nil)

const memoryLimiterPruneAt = 4096

func (this *MemoryLimiter) Admit(_ context.Context, attempt Attempt) error {
	now := this.policy.Now()

	this.mu.Lock()
	defer this.mu.Unlock()
	this.prune(now)
	for key, ceiling := range this.keys(attempt) {
		counter := this.counters[key]
		if counter == nil {
			continue
		}
		if now.Before(counter.lockedTo) {
			return TooManyAttempts()
		}
		if now.Sub(counter.opened) < this.policy.Window && counter.failures >= ceiling {
			counter.lockedTo = now.Add(this.policy.LockFor)
			return TooManyAttempts()
		}
	}
	return nil
}

func (this *MemoryLimiter) Record(_ context.Context, attempt Attempt, outcome AttemptOutcome) error {
	now := this.policy.Now()

	this.mu.Lock()
	defer this.mu.Unlock()
	for key, ceiling := range this.keys(attempt) {
		if outcome == AttemptSucceeded {
			if key == identifierKey(attempt) {
				delete(this.counters, key)
			}
			continue
		}
		if outcome != AttemptFailed {
			continue
		}
		counter := this.counters[key]
		if counter == nil || now.Sub(counter.opened) >= this.policy.Window {
			counter = &attemptCounter{opened: now}
			this.counters[key] = counter
		}
		counter.failures++
		if counter.failures >= ceiling {
			counter.lockedTo = now.Add(this.policy.LockFor)
		}
	}
	return nil
}

func (this *MemoryLimiter) keys(attempt Attempt) map[string]int {
	keys := make(map[string]int, 2)
	if attempt.Identifier != "" {
		keys[identifierKey(attempt)] = this.policy.MaxPerIdentifier
	}
	if attempt.IP != "" {
		keys["ip\x00"+attempt.IP] = this.policy.MaxPerIP
	}
	return keys
}

func identifierKey(attempt Attempt) string {
	return "id\x00" + string(attempt.Subject) + "\x00" + attempt.Identifier
}

func (this *MemoryLimiter) prune(now time.Time) {
	if len(this.counters) < memoryLimiterPruneAt {
		return
	}
	for key, counter := range this.counters {
		if now.Sub(counter.opened) >= this.policy.Window && !now.Before(counter.lockedTo) {
			delete(this.counters, key)
		}
	}
}

type BulkheadHasher struct {
	inner   Hasher
	permits chan struct{}
	queue   int64
	waiting atomic.Int64
}

var _ Hasher = (*BulkheadHasher)(nil)

func NewBulkhead(inner Hasher, permits, queue int) *BulkheadHasher {
	if inner == nil {
		panic("access: NewBulkhead needs a Hasher to stand in front of")
	}
	if permits < 1 {
		permits = 1
	}
	if queue < 0 {
		queue = 0
	}
	return &BulkheadHasher{inner: inner, permits: make(chan struct{}, permits), queue: int64(queue)}
}

func Bulkhead(inner Hasher) *BulkheadHasher {
	permits := runtime.NumCPU()
	if permits > 4 {
		permits = 4
	}
	return NewBulkhead(inner, permits, permits*8)
}

func (this *BulkheadHasher) Unwrap() Hasher { return this.inner }

func (this *BulkheadHasher) Hash(password string) (string, error) {
	if err := this.enter(); err != nil {
		return "", err
	}
	defer this.leave()
	return this.inner.Hash(password)
}

func (this *BulkheadHasher) Verify(password, encoded string) (bool, error) {
	if err := this.enter(); err != nil {
		return false, err
	}
	defer this.leave()
	return this.inner.Verify(password, encoded)
}

func (this *BulkheadHasher) enter() error {
	select {
	case this.permits <- struct{}{}:
		return nil
	default:
	}
	if this.waiting.Add(1) > this.queue {
		this.waiting.Add(-1)
		return Overloaded()
	}
	this.permits <- struct{}{}
	this.waiting.Add(-1)
	return nil
}

func (this *BulkheadHasher) leave() { <-this.permits }

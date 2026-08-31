package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

type transientBlockingCodec struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type transientSignalingTimer struct {
	channel <-chan time.Time
	entered chan struct{}
	once    sync.Once
}

type transientCancelClock struct {
	cancel context.CancelFunc
	armed  atomic.Bool
}

func (this *transientCancelClock) Now() time.Time {
	if this.armed.Load() {
		this.cancel()
		panic("clock")
	}
	return time.Unix(1_900_000_000, 0).UTC()
}

func (*transientCancelClock) NewTimer(time.Duration) Timer {
	return &reviewTimer{channel: make(chan time.Time)}
}

type transientContextKey struct{}

type transientContextObserver struct {
	key    transientContextKey
	values chan any
}

func (this *transientContextObserver) Observe(ctx context.Context, event Event) {
	if event.Operation == LoadOperation {
		this.values <- ctx.Value(this.key)
	}
}

func (this *transientSignalingTimer) C() <-chan time.Time {
	this.once.Do(func() { close(this.entered) })
	return this.channel
}

func (*transientSignalingTimer) Stop() bool { return true }

type transientChargedStringCodec struct {
	charge func(string) int64
}

func (*transientChargedStringCodec) ID() string          { return "transient-charged-string" }
func (*transientChargedStringCodec) Schema() ValueSchema { return 1 }

func (*transientChargedStringCodec) Encode(value string, limit ValueLimit) ([]byte, error) {
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return []byte(value), nil
}

func (*transientChargedStringCodec) Decode(value []byte, limit ValueLimit) (string, error) {
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return "", ErrTooLarge
	}
	return string(bytes.Clone(value)), nil
}

func (this *transientChargedStringCodec) DecodeCharge(value string) int64 {
	return this.charge(value)
}

type transientInlineValue [4096]byte

type transientInlineKey [8192]byte

type transientMassiveInlineValue [256 << 10]byte

type transientInlineCodec struct{}

func (transientInlineCodec) ID() string          { return "transient-inline" }
func (transientInlineCodec) Schema() ValueSchema { return 1 }
func (transientInlineCodec) Encode(transientInlineValue, ValueLimit) ([]byte, error) {
	return []byte{1}, nil
}
func (transientInlineCodec) Decode([]byte, ValueLimit) (transientInlineValue, error) {
	return transientInlineValue{}, nil
}

type transientMassiveInlineCodec struct{}

func (transientMassiveInlineCodec) ID() string          { return "transient-massive-inline" }
func (transientMassiveInlineCodec) Schema() ValueSchema { return 1 }
func (transientMassiveInlineCodec) Encode(transientMassiveInlineValue, ValueLimit) ([]byte, error) {
	return []byte{1}, nil
}
func (transientMassiveInlineCodec) Decode([]byte, ValueLimit) (transientMassiveInlineValue, error) {
	return transientMassiveInlineValue{}, nil
}

type transientHugeJSON struct {
	Padding [4096]byte `json:"-"`
}

type transientHugeWireCodec struct{}

func (transientHugeWireCodec) ID() string          { return "trusted-json" }
func (transientHugeWireCodec) Schema() ValueSchema { return 1 }
func (transientHugeWireCodec) Encode([]transientHugeJSON, ValueLimit) ([]byte, error) {
	return []byte(`[{}]`), nil
}
func (transientHugeWireCodec) Decode([]byte, ValueLimit) ([]transientHugeJSON, error) {
	return nil, ErrInvalid
}

var transientJSONDecodeCalls atomic.Int64

func (*transientHugeJSON) UnmarshalJSON([]byte) error {
	transientJSONDecodeCalls.Add(1)
	return nil
}

type transientJSONValue struct {
	Name string
}

type transientTimeValue struct {
	At       time.Time
	Optional *time.Time
	History  []time.Time
}

type transientRecursiveJSON struct {
	Value int
	Next  *transientRecursiveJSON
}

type TransientPromotedJSON5 struct {
	Padding [4096]byte `json:"-"`
	X       int
}

type TransientPromotedJSON4 struct {
	Padding [4096]byte `json:"-"`
	*TransientPromotedJSON5
}

type TransientPromotedJSON3 struct {
	Padding [4096]byte `json:"-"`
	*TransientPromotedJSON4
}

type TransientPromotedJSON2 struct {
	Padding [4096]byte `json:"-"`
	*TransientPromotedJSON3
}

type TransientPromotedJSON1 struct {
	Padding [4096]byte `json:"-"`
	*TransientPromotedJSON2
}

type transientPromotedJSONRoot struct {
	*TransientPromotedJSON1
}

type transientJSONDiamond30 int
type transientJSONDiamond29 struct{ A, B *transientJSONDiamond30 }
type transientJSONDiamond28 struct{ A, B *transientJSONDiamond29 }
type transientJSONDiamond27 struct{ A, B *transientJSONDiamond28 }
type transientJSONDiamond26 struct{ A, B *transientJSONDiamond27 }
type transientJSONDiamond25 struct{ A, B *transientJSONDiamond26 }
type transientJSONDiamond24 struct{ A, B *transientJSONDiamond25 }
type transientJSONDiamond23 struct{ A, B *transientJSONDiamond24 }
type transientJSONDiamond22 struct{ A, B *transientJSONDiamond23 }
type transientJSONDiamond21 struct{ A, B *transientJSONDiamond22 }
type transientJSONDiamond20 struct{ A, B *transientJSONDiamond21 }
type transientJSONDiamond19 struct{ A, B *transientJSONDiamond20 }
type transientJSONDiamond18 struct{ A, B *transientJSONDiamond19 }
type transientJSONDiamond17 struct{ A, B *transientJSONDiamond18 }
type transientJSONDiamond16 struct{ A, B *transientJSONDiamond17 }
type transientJSONDiamond15 struct{ A, B *transientJSONDiamond16 }
type transientJSONDiamond14 struct{ A, B *transientJSONDiamond15 }
type transientJSONDiamond13 struct{ A, B *transientJSONDiamond14 }
type transientJSONDiamond12 struct{ A, B *transientJSONDiamond13 }
type transientJSONDiamond11 struct{ A, B *transientJSONDiamond12 }
type transientJSONDiamond10 struct{ A, B *transientJSONDiamond11 }
type transientJSONDiamond9 struct{ A, B *transientJSONDiamond10 }
type transientJSONDiamond8 struct{ A, B *transientJSONDiamond9 }
type transientJSONDiamond7 struct{ A, B *transientJSONDiamond8 }
type transientJSONDiamond6 struct{ A, B *transientJSONDiamond7 }
type transientJSONDiamond5 struct{ A, B *transientJSONDiamond6 }
type transientJSONDiamond4 struct{ A, B *transientJSONDiamond5 }
type transientJSONDiamond3 struct{ A, B *transientJSONDiamond4 }
type transientJSONDiamond2 struct{ A, B *transientJSONDiamond3 }
type transientJSONDiamond1 struct{ A, B *transientJSONDiamond2 }
type transientJSONDiamond0 struct{ A, B *transientJSONDiamond1 }

type TransientJSONSCCA1 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCB1
	*TransientJSONSCCB2
}

type TransientJSONSCCB1 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCA1
}

type TransientJSONSCCA2 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCB2
	*TransientJSONSCCB3
}

type TransientJSONSCCB2 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCA2
}

type TransientJSONSCCA3 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCB3
	*TransientJSONSCCB4
}

type TransientJSONSCCB3 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCA3
}

type TransientJSONSCCA4 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCB4
	*TransientJSONSCCB5
}

type TransientJSONSCCB4 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCA4
}

type TransientJSONSCCA5 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCB5
	X int
}

type TransientJSONSCCB5 struct {
	Padding [4096]byte `json:"-"`
	*TransientJSONSCCA5
}

type transientJSONSCCRoot struct {
	SeedA5 *TransientJSONSCCA5 `json:"-"`
	SeedA4 *TransientJSONSCCA4 `json:"-"`
	SeedA3 *TransientJSONSCCA3 `json:"-"`
	SeedA2 *TransientJSONSCCA2 `json:"-"`
	SeedA1 *TransientJSONSCCA1 `json:"-"`
	*TransientJSONSCCB1
}

type transientEmbeddedElement struct {
	Padding [4096]byte `json:"-"`
}

type transientEmbeddedBase struct {
	Values []transientEmbeddedElement
}

type transientEmbeddedValue struct {
	transientEmbeddedBase
}

type transientJSONHook struct{}

var transientHookDecodeCalls atomic.Int64

func (*transientJSONHook) UnmarshalJSON([]byte) error {
	transientHookDecodeCalls.Add(1)
	return nil
}

type transientTextHook string

func (*transientTextHook) UnmarshalText([]byte) error { return nil }

type transientJSONMarshalHook struct{}

var transientMarshalCalls atomic.Int64

func (transientJSONMarshalHook) MarshalJSON() ([]byte, error) {
	transientMarshalCalls.Add(1)
	return make([]byte, 1<<20), nil
}

type transientTextMarshalHook string

func (*transientTextMarshalHook) MarshalText() ([]byte, error) {
	transientMarshalCalls.Add(1)
	return make([]byte, 1<<20), nil
}

type transientIsZeroHook int

func (transientIsZeroHook) IsZero() bool {
	transientMarshalCalls.Add(1)
	return false
}

type transientNonemptyInterface interface {
	TransientMethod()
}

type transientSerialCodec struct {
	entered chan struct{}
	release chan struct{}
	active  atomic.Int64
	maximum atomic.Int64
	calls   atomic.Int64
}

func (*transientSerialCodec) ID() string          { return "transient-serial" }
func (*transientSerialCodec) Schema() ValueSchema { return 1 }

func (*transientSerialCodec) Encode(value []byte, limit ValueLimit) ([]byte, error) {
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return bytes.Clone(value), nil
}

func (this *transientSerialCodec) Decode(value []byte, limit ValueLimit) ([]byte, error) {
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	active := this.active.Add(1)
	for maximum := this.maximum.Load(); active > maximum && !this.maximum.CompareAndSwap(maximum, active); maximum = this.maximum.Load() {
	}
	this.calls.Add(1)
	this.entered <- struct{}{}
	<-this.release
	this.active.Add(-1)
	return bytes.Clone(value), nil
}

func (*transientSerialCodec) DecodeCharge(value []byte) int64 { return int64(len(value)) }

type transientSignalContext struct {
	context.Context
	calls  atomic.Int64
	second chan struct{}
	once   sync.Once
}

func (this *transientSignalContext) Done() <-chan struct{} {
	if this.calls.Add(1) >= 2 {
		this.once.Do(func() { close(this.second) })
	}
	return this.Context.Done()
}

func (*transientBlockingCodec) ID() string          { return "transient-blocking" }
func (*transientBlockingCodec) Schema() ValueSchema { return 1 }

func (*transientBlockingCodec) Encode(value []byte, limit ValueLimit) ([]byte, error) {
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return bytes.Clone(value), nil
}

func (this *transientBlockingCodec) Decode(value []byte, limit ValueLimit) ([]byte, error) {
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	this.once.Do(func() { close(this.started) })
	<-this.release
	return bytes.Clone(value), nil
}

func TestTransientPolicyFailsFast(t *testing.T) {
	policy := coordinationPolicy()
	policy.MaxValueBytes = 64
	policy.MaxBatchResultBytes = 256
	policy.MaxTransientBytes = 0
	normalized, err := normalizePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := transientPlanFor(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.MaxTransientBytes != plan.minimum {
		t.Fatalf("default transient bytes = %d, want %d", normalized.MaxTransientBytes, plan.minimum)
	}

	policy.MaxTransientBytes = plan.minimum - 1
	if _, err := normalizePolicy(policy); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undersized transient budget error = %v", err)
	}

	policy.MaxTransientBytes = plan.minimum
	policy.TransientSaturation = WaitForTransient(0)
	if _, err := normalizePolicy(policy); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded transient wait error = %v", err)
	}
}

func TestDisabledTransientOptionsStillValidate(t *testing.T) {
	tests := []Profile{
		Disabled.With(TransientSaturation(TransientSaturationPolicy{})),
		Disabled.With(TransientSaturation(WaitForTransient(0))),
		Disabled.With(TransientSaturation(TransientSaturationPolicy{mode: 99})),
		Disabled.With(MaxTransientBytes(1)),
	}
	for _, profile := range tests {
		if _, err := profile.Build(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("disabled profile error = %v", err)
		}
	}
}

func TestMaxTransientBytesRemainsAnOrderIndependentHardCap(t *testing.T) {
	for _, profile := range []Profile{
		Hot.With(MaxTransientBytes(1<<20), MaxValueBytes(32<<20)),
		Hot.With(MaxValueBytes(32<<20), MaxTransientBytes(1<<20)),
	} {
		if _, err := profile.Build(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("undersized explicit cap error = %v", err)
		}
	}
	const capBytes = int64(512 << 20)
	for _, profile := range []Profile{
		Hot.With(MaxTransientBytes(capBytes), MaxValueBytes(32<<20)),
		Hot.With(MaxValueBytes(32<<20), MaxTransientBytes(capBytes)),
	} {
		policy, err := profile.Build()
		if err != nil {
			t.Fatal(err)
		}
		if policy.MaxTransientBytes != capBytes || !policy.transientExplicit || policy.transientDefaulted {
			t.Fatalf("explicit cap state = (%d, %t, %t)", policy.MaxTransientBytes, policy.transientExplicit, policy.transientDefaulted)
		}
	}
}

func TestDirectPublicTransientCapMutationRemainsHard(t *testing.T) {
	defaultPolicy, err := Hot.Build()
	if err != nil {
		t.Fatal(err)
	}
	base, err := transientPlanFor(defaultPolicy)
	if err != nil {
		t.Fatal(err)
	}
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	build := func(policy Policy, purpose string) (*Cache[string, transientMassiveInlineValue], error) {
		return New(Runtime{
			LoaderTimeout:  coordinationTestTimeout,
			BackendTimeout: coordinationTestTimeout,
			CleanupTimeout: coordinationTestTimeout,
		}, newActivationBackend(purpose), Global[string](MustNamespace("tests", "unit", purpose, 1)), keys, transientMassiveInlineCodec{}, policy)
	}
	mutated := defaultPolicy
	mutated.MaxTransientBytes = base.minimum - 1
	if _, err := build(mutated, "transient-mutated-cap"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mutated public cap error = %v", err)
	}
	untouched, err := build(defaultPolicy, "transient-default-cap")
	if err != nil {
		t.Fatal(err)
	}
	if untouched.inner.Load().policy.MaxTransientBytes <= defaultPolicy.MaxTransientBytes ||
		untouched.inner.Load().policy.MaxTransientBytes != untouched.inner.Load().policy.transientResolved {
		t.Fatalf("typed default cap = (%d, %d), original %d", untouched.inner.Load().policy.MaxTransientBytes, untouched.inner.Load().policy.transientResolved, defaultPolicy.MaxTransientBytes)
	}
	sameValue := defaultPolicy
	sameValue.MaxTransientBytes = defaultPolicy.MaxTransientBytes
	if _, err := build(sameValue, "transient-same-default-cap"); err != nil {
		t.Fatalf("observationally unchanged default error = %v", err)
	}
	explicitProfile := Hot.With(MaxTransientBytes(defaultPolicy.MaxTransientBytes))
	explicit, err := explicitProfile.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build(explicit, "transient-explicit-default-cap"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("explicit same-value cap error = %v", err)
	}
}

func TestDisabledDescriptorPublishesEnforcedBounds(t *testing.T) {
	policy, err := Disabled.Build()
	if err != nil {
		t.Fatal(err)
	}
	description := describePolicy(policy)
	if !description.Disabled || description.MaxKeyBytes != policy.MaxKeyBytes || description.MaxValueBytes != policy.MaxValueBytes ||
		description.MaxValueDepth != policy.MaxValueDepth || description.MaxFlights != policy.MaxFlights ||
		description.MaxBatchKeys != policy.MaxBatchKeys || description.MaxBatchKeyBytes != policy.MaxBatchKeyBytes ||
		description.MaxBatchResultBytes != policy.MaxBatchResultBytes || description.MaxTransientBytes != policy.MaxTransientBytes ||
		description.MaxTransientWaiters != policy.MaxTransientWaiters || description.ReservedTransientBytes == 0 ||
		description.TransientSaturation != policy.TransientSaturation.mode || description.TransientWait != policy.TransientSaturation.timeout {
		t.Fatalf("disabled description = %+v", description)
	}
}

func TestDisabledLoaderUsesValueBlindTimedContract(t *testing.T) {
	t.Run("results and values", func(t *testing.T) {
		clock := newReviewClock()
		observer := &transientContextObserver{key: transientContextKey{}, values: make(chan any, 4)}
		instance := newDisabledTransientStringCache(t, Runtime{
			Clock:          clock,
			Observer:       observer,
			LoaderTimeout:  5 * time.Second,
			BackendTimeout: time.Second,
			CleanupTimeout: time.Second,
		})
		caller := context.WithValue(context.Background(), observer.key, "principal")
		result, err := instance.Resolve(caller, "found", func(ctx context.Context, _ string) (LoadResult[string], error) {
			if value := ctx.Value(observer.key); value != nil {
				t.Fatalf("disabled loader value = %#v", value)
			}
			deadline, ok := ctx.Deadline()
			if !ok || !deadline.Equal(clock.now.Add(5*time.Second)) {
				t.Fatalf("disabled loader deadline = %v, %t", deadline, ok)
			}
			return Present("value"), nil
		})
		if err != nil || result.State != Loaded || result.Value != "value" {
			t.Fatalf("disabled found = (%+v, %v)", result, err)
		}
		if value := receiveTransientValue(t, observer.values); value != nil {
			t.Fatalf("disabled found observer value = %#v", value)
		}
		result, err = instance.Resolve(caller, "absent", func(context.Context, string) (LoadResult[string], error) {
			return Absent[string](), nil
		})
		if err != nil || result.State != Negative || result.Value != "" {
			t.Fatalf("disabled absent = (%+v, %v)", result, err)
		}
		if value := receiveTransientValue(t, observer.values); value != nil {
			t.Fatalf("disabled absent observer value = %#v", value)
		}
		loaderErr := errors.New("disabled loader error")
		result, err = instance.Resolve(caller, "error", func(context.Context, string) (LoadResult[string], error) {
			return LoadResult[string]{}, loaderErr
		})
		if !errors.Is(err, ErrLoader) || !errors.Is(err, loaderErr) || result != (Result[string]{}) {
			t.Fatalf("disabled error = (%+v, %v)", result, err)
		}
		if value := receiveTransientValue(t, observer.values); value != nil {
			t.Fatalf("disabled error observer value = %#v", value)
		}
		result, err = instance.Resolve(caller, "invalid", func(context.Context, string) (LoadResult[string], error) {
			return LoadResult[string]{}, nil
		})
		if !errors.Is(err, ErrInvalid) || result != (Result[string]{}) {
			t.Fatalf("disabled invalid = (%+v, %v)", result, err)
		}
		if value := receiveTransientValue(t, observer.values); value != nil {
			t.Fatalf("disabled invalid observer value = %#v", value)
		}
		waitCacheQuiescent(t, instance)
	})

	t.Run("fake clock timeout", func(t *testing.T) {
		clock := newReviewClock()
		observer := &transientContextObserver{key: transientContextKey{}, values: make(chan any, 1)}
		instance := newDisabledTransientStringCache(t, Runtime{
			Clock:          clock,
			Observer:       observer,
			LoaderTimeout:  2 * time.Second,
			BackendTimeout: time.Second,
			CleanupTimeout: time.Second,
		})
		result, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[string], error) {
			clock.Advance(2 * time.Second)
			return Present("late"), nil
		})
		if !errors.Is(err, ErrLoader) || !errors.Is(err, context.DeadlineExceeded) || result != (Result[string]{}) {
			t.Fatalf("disabled timeout = (%+v, %v)", result, err)
		}
		if value := receiveTransientValue(t, observer.values); value != nil {
			t.Fatalf("disabled timeout observer value = %#v", value)
		}
		waitCacheQuiescent(t, instance)
	})

	t.Run("earlier caller deadline", func(t *testing.T) {
		clock := newReviewClock()
		instance := newDisabledTransientStringCache(t, Runtime{
			Clock:          clock,
			LoaderTimeout:  5 * time.Second,
			BackendTimeout: time.Second,
			CleanupTimeout: time.Second,
		})
		callerDeadline := clock.now.Add(time.Second)
		ctx, cancel := context.WithDeadline(context.Background(), callerDeadline)
		defer cancel()
		result, err := instance.Resolve(ctx, "key", func(ctx context.Context, _ string) (LoadResult[string], error) {
			deadline, ok := ctx.Deadline()
			if !ok || !deadline.Equal(callerDeadline) {
				t.Fatalf("disabled caller deadline = %v, %t", deadline, ok)
			}
			return Present("value"), nil
		})
		if err != nil || result.State != Loaded || result.Value != "value" {
			t.Fatalf("disabled caller deadline result = (%+v, %v)", result, err)
		}
		expiredDeadline := clock.now.Add(time.Second)
		expiredCtx, expiredCancel := context.WithDeadline(context.Background(), expiredDeadline)
		defer expiredCancel()
		result, err = instance.Resolve(expiredCtx, "expired", func(context.Context, string) (LoadResult[string], error) {
			clock.Advance(time.Second)
			return Present("late"), nil
		})
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrLoader) || result != (Result[string]{}) {
			t.Fatalf("disabled caller deadline expiry = (%+v, %v)", result, err)
		}
		waitCacheQuiescent(t, instance)
	})

	t.Run("caller cancellation", func(t *testing.T) {
		clock := newReviewClock()
		instance := newDisabledTransientStringCache(t, Runtime{
			Clock:          clock,
			LoaderTimeout:  5 * time.Second,
			BackendTimeout: time.Second,
			CleanupTimeout: time.Second,
		})
		key := transientContextKey{}
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "request-body"))
		entered := make(chan struct{})
		done := resolveAsync(ctx, instance, "key", func(ctx context.Context, _ string) (LoadResult[string], error) {
			if value := ctx.Value(key); value != nil {
				t.Fatalf("disabled canceled loader value = %#v", value)
			}
			close(entered)
			<-ctx.Done()
			return LoadResult[string]{}, ctx.Err()
		})
		receiveTransientSignal(t, entered)
		cancel()
		outcome := receiveResolve(t, done)
		if !errors.Is(outcome.err, context.Canceled) || errors.Is(outcome.err, ErrLoader) || outcome.result != (Result[string]{}) {
			t.Fatalf("disabled cancellation = (%+v, %v)", outcome.result, outcome.err)
		}
		waitCacheQuiescent(t, instance)
	})

	t.Run("caller cancellation beats clock failure", func(t *testing.T) {
		for _, disabled := range []bool{false, true} {
			name := "enabled"
			if disabled {
				name = "disabled"
			}
			t.Run(name, func(t *testing.T) {
				clock := &transientCancelClock{}
				observer := &reviewObserver{}
				runtime := Runtime{
					Clock:          clock,
					Observer:       observer,
					LoaderTimeout:  5 * time.Second,
					BackendTimeout: time.Second,
					CleanupTimeout: time.Second,
				}
				var instance *Cache[string, string]
				if disabled {
					instance = newDisabledTransientStringCache(t, runtime)
				} else {
					instance = newTransientRuntimeStringCache(t, runtime, newCoordinationBackend(), "cancel-clock-enabled")
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				clock.cancel = cancel
				var calls atomic.Int64
				result, err := instance.Resolve(ctx, "key", func(context.Context, string) (LoadResult[string], error) {
					calls.Add(1)
					clock.armed.Store(true)
					return Present("value"), nil
				})
				if !errors.Is(err, context.Canceled) || errors.Is(err, ErrLoader) || result != (Result[string]{}) || calls.Load() != 1 {
					t.Fatalf("cancel versus clock = (%+v, %v), calls %d", result, err, calls.Load())
				}
				waitCacheQuiescent(t, instance)
				if events := transientOperationEvents(observer, LoadOperation); events != 1 {
					t.Fatalf("cancel versus clock load events = %d", events)
				}
			})
		}
	})

	t.Run("loader context failure emits one load", func(t *testing.T) {
		clock := newReviewClock()
		clock.SetTimers(func(time.Duration) Timer { return nil })
		observer := &transientContextObserver{key: transientContextKey{}, values: make(chan any, 2)}
		instance := newDisabledTransientStringCache(t, Runtime{
			Clock:          clock,
			Observer:       observer,
			LoaderTimeout:  5 * time.Second,
			BackendTimeout: time.Second,
			CleanupTimeout: time.Second,
		})
		ctx := context.WithValue(context.Background(), observer.key, "principal")
		var calls atomic.Int64
		result, err := instance.Resolve(ctx, "key", func(context.Context, string) (LoadResult[string], error) {
			calls.Add(1)
			return Present("value"), nil
		})
		if !errors.Is(err, ErrInvalid) || result != (Result[string]{}) || calls.Load() != 0 {
			t.Fatalf("disabled loader context failure = (%+v, %v), calls %d", result, err, calls.Load())
		}
		if value := receiveTransientValue(t, observer.values); value != nil {
			t.Fatalf("disabled loader context observer value = %#v", value)
		}
		select {
		case value := <-observer.values:
			t.Fatalf("second disabled loader context event = %#v", value)
		default:
		}
		waitCacheQuiescent(t, instance)
	})
}

func TestBuiltInProfilesPublishAttainableFlightCapacityWithoutHiddenRaise(t *testing.T) {
	for _, test := range []struct {
		profile Profile
		flights int
		bytes   int64
	}{
		{profile: Hot, flights: 1, bytes: 256 << 20},
		{profile: Warm, flights: 1, bytes: 256 << 20},
		{profile: Durable, flights: 2, bytes: 512 << 20},
	} {
		policy, err := test.profile.Build()
		if err != nil {
			t.Fatal(err)
		}
		plan, err := transientPlanFor(policy)
		if err != nil {
			t.Fatal(err)
		}
		if policy.MaxFlights != test.flights || policy.MaxTransientBytes != test.bytes || policy.transientResolved != test.bytes || plan.minimum > test.bytes {
			t.Fatalf("profile %s = flights %d, bytes %d, resolved %d, minimum %d", test.profile.Name(), policy.MaxFlights, policy.MaxTransientBytes, policy.transientResolved, plan.minimum)
		}
	}
}

func TestTransientWaiterPolicyIsIndependentAndDescribed(t *testing.T) {
	base, err := Hot.Build()
	if err != nil {
		t.Fatal(err)
	}
	if base.MaxTransientWaiters != 64 {
		t.Fatalf("default transient waiters = %d", base.MaxTransientWaiters)
	}
	for _, count := range []int{1, 128} {
		policy, err := Hot.With(MaxTransientWaiters(count)).Build()
		if err != nil {
			t.Fatal(err)
		}
		plan, err := transientPlanFor(policy)
		if err != nil {
			t.Fatal(err)
		}
		want, ok := multiplyTransientBytes(int64(count), transientAdmissionSlotCharge)
		if !ok || plan.reserved != want || policy.MaxTransientBytes < plan.minimum {
			t.Fatalf("waiter policy %d = reserve %d, cap %d, minimum %d", count, plan.reserved, policy.MaxTransientBytes, plan.minimum)
		}
		description := describePolicy(policy)
		if description.MaxTransientWaiters != count || description.ReservedTransientBytes != want {
			t.Fatalf("waiter description %d = %+v", count, description)
		}
	}
	for _, profile := range []Profile{
		Hot.With(MaxTransientWaiters(0)),
		Hot.With(MaxTransientWaiters(MaximumTransientWaiters + 1)),
	} {
		if _, err := profile.Build(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid waiter policy error = %v", err)
		}
	}
}

func TestTransientWaiterOptionsPreserveExplicitCapOrder(t *testing.T) {
	const capBytes = int64(512 << 20)
	for _, profile := range []Profile{
		Hot.With(MaxTransientBytes(capBytes), MaxTransientWaiters(2)),
		Hot.With(MaxTransientWaiters(2), MaxTransientBytes(capBytes)),
	} {
		policy, err := profile.Build()
		if err != nil {
			t.Fatal(err)
		}
		if policy.MaxTransientBytes != capBytes || policy.MaxTransientWaiters != 2 || !policy.transientExplicit {
			t.Fatalf("ordered waiter policy = %+v", policy)
		}
	}
	policy, err := Hot.With(TransientSaturation(RejectTransient()), MaxTransientWaiters(3)).Build()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := transientPlanFor(policy)
	if err != nil {
		t.Fatal(err)
	}
	description := describePolicy(policy)
	if plan.reserved != 0 || description.MaxTransientWaiters != 3 || description.ReservedTransientBytes != 0 {
		t.Fatalf("reject waiter policy = (%+v, %+v)", plan, description)
	}
}

func TestTypedAutomaticDeclarationsResolveAdmissionReserveBeforeActivation(t *testing.T) {
	target := Auto[transientInlineKey, transientInlineValue](Hot)
	automatic := target.automatic.Load()
	if automatic == nil {
		t.Fatal("automatic declaration is missing")
	}
	perSlot := transientAdmissionSlotCharge + int64(reflect.TypeFor[transientInlineKey]().Size()) + int64(reflect.TypeFor[transientInlineValue]().Size())
	wantReserve, ok := multiplyTransientBytes(int64(automatic.policy.MaxTransientWaiters), perSlot)
	if !ok {
		t.Fatal("typed admission reserve overflowed")
	}
	description := target.Describe()
	if automatic.transientPlan.reserved != wantReserve || description.Policy.ReservedTransientBytes != wantReserve ||
		description.Policy.MaxTransientBytes != automatic.policy.MaxTransientBytes {
		t.Fatalf("automatic typed policy = (%+v, %+v)", automatic, description.Policy)
	}
	keys := MustKeyFunc(KeyVersion(1), func(key transientInlineKey, _ KeyLimit) ([]byte, error) {
		return []byte{key[0]}, nil
	})
	definition, err := Define(target, DefinitionSpec[transientInlineKey, transientInlineValue]{
		Name: "typed-auto",
		Namespace: NamespaceTemplate{
			Purpose:    "typed-auto",
			Generation: 1,
		},
		Scope:  GlobalPlan[transientInlineKey](),
		Keys:   keys,
		Values: transientInlineCodec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	declared := definition.Describe()
	if declared.Policy.ReservedTransientBytes != wantReserve || declared.Policy.MaxTransientBytes != automatic.policy.MaxTransientBytes {
		t.Fatalf("declared typed policy = %+v", declared.Policy)
	}
	base, err := Hot.Build()
	if err != nil {
		t.Fatal(err)
	}
	untyped, err := transientPlanFor(base)
	if err != nil {
		t.Fatal(err)
	}
	explicit := Hot.With(MaxTransientBytes(untyped.minimum))
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("typed explicit cap did not fail during Auto")
		}
	}()
	_ = Auto[transientInlineKey, transientInlineValue](explicit)
}

func TestTransientPlanUsesOwnershipHandoffFormulas(t *testing.T) {
	policy := transientBatchPolicy(t)
	plan, err := transientPlanFor(policy)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatal(err)
	}
	wantEncode := 2*int64(envelope) + 2*int64(policy.MaxValueBytes) + jsonSafeRuntimeBytes
	wantBuild := wantEncode + int64(policy.MaxValueBytes) + transientFlightFixedCharge
	if plan.encode != wantEncode || plan.build != wantBuild {
		t.Fatalf("transient formulas = encode %d build %d, want %d and %d", plan.encode, plan.build, wantEncode, wantBuild)
	}
	wantLookup := int64(envelope) + 4*int64(policy.MaxValueBytes) + jsonSafeRuntimeBytes
	wantBatchScratch := 4*int64(policy.MaxValueBytes) + jsonSafeRuntimeBytes
	if plan.lookup != wantLookup || plan.batchScratch != wantBatchScratch {
		t.Fatalf("codec runtime formulas = lookup %d batch scratch %d, want %d and %d", plan.lookup, plan.batchScratch, wantLookup, wantBatchScratch)
	}
	if plan.resolve != plan.waiter+plan.lookup+plan.build {
		t.Fatalf("resolve charge = %d, want %d", plan.resolve, plan.waiter+plan.lookup+plan.build)
	}
}

func TestTransientPlanChargesLargeDecodePeakWithoutFixedScratchSubstitution(t *testing.T) {
	policy := coordinationPolicy()
	policy.MaxValueBytes = 64 << 20
	policy.MaxBatchResultBytes = 256 << 20
	policy.MaxBatchKeys = 1
	policy.MaxBatchKeyBytes = policy.MaxKeyBytes
	policy.MaxFlights = 1
	policy.MaxTransientBytes = 0
	policy.TransientSaturation = RejectTransient()
	policy, err := normalizePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := transientPlanFor(policy)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatal(err)
	}
	wantLookup := int64(envelope) + 4*int64(policy.MaxValueBytes) + jsonSafeRuntimeBytes
	wantBatchScratch := 4*int64(policy.MaxValueBytes) + jsonSafeRuntimeBytes
	if plan.lookup != wantLookup || plan.batchScratch != wantBatchScratch || plan.lookup <= int64(envelope)+3*int64(policy.MaxValueBytes)+jsonSafeRuntimeBytes {
		t.Fatalf("large decode plan = lookup %d batch %d, want %d and %d", plan.lookup, plan.batchScratch, wantLookup, wantBatchScratch)
	}
}

func TestTypedBatchAllocationsFailFast(t *testing.T) {
	policy := transientTestPolicy(t)
	plan, err := transientPlanFor(policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.MaxTransientBytes = plan.minimum
	policy.transientDefaulted = false
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	_, err = New(Runtime{
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, newCoordinationBackend(), Global[string](MustNamespace("tests", "unit", "transient-inline", 1)), keys, transientInlineCodec{}, policy)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("typed transient error = %v", err)
	}
}

func TestTypedPlanChargesInlineKeysValuesAndJoinedCallers(t *testing.T) {
	policy := transientTestPolicy(t)
	base, err := transientPlanFor(policy)
	if err != nil {
		t.Fatal(err)
	}
	typed, err := typedTransientPlan[transientInlineKey, transientInlineValue](policy, base)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, _ := transientTypeBytes[transientInlineKey]()
	valueBytes, _ := transientTypeBytes[transientInlineValue]()
	resultBytes, _ := transientTypeBytes[Result[transientInlineValue]]()
	if typed.key < base.key+keyBytes || typed.lookup < base.lookup+keyBytes+resultBytes ||
		typed.encode < base.encode+keyBytes+valueBytes || typed.build < base.build+keyBytes+valueBytes ||
		typed.waiter < base.waiter+keyBytes || typed.resolve != typed.waiter+typed.lookup+typed.build {
		t.Fatalf("typed plan = %+v, base = %+v", typed, base)
	}
	flightMaximum, err := typed.flightCapacity(policy.MaxFlights)
	if err != nil {
		t.Fatal(err)
	}
	owner := typed.build + max(typed.waiter, typed.retained)
	steady := int64(policy.MaxFlights) * owner
	wantFlightMaximum := max(steady, int64(policy.MaxFlights-1)*owner+typed.resolve, steady+typed.background)
	if typed.minimum < flightMaximum || flightMaximum != wantFlightMaximum {
		t.Fatalf("flight minimum = %d, want %d, plan = %+v", flightMaximum, wantFlightMaximum, typed)
	}
	policy.MaxTransientBytes = base.minimum
	policy.transientDefaulted = false
	policy.transientExplicit = true
	keys := MustKeyFunc(KeyVersion(1), func(transientInlineKey, KeyLimit) ([]byte, error) { return []byte{1}, nil })
	_, err = New(Runtime{
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, newCoordinationBackend(), Global[transientInlineKey](MustNamespace("tests", "unit", "transient-inline-key", 1)), keys, transientInlineCodec{}, policy)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("typed explicit cap error = %v", err)
	}
}

func TestTypedTransientMinimumAddsAdmissionReserveExactlyOnce(t *testing.T) {
	for _, saturation := range []TransientSaturationPolicy{RejectTransient(), WaitForTransient(time.Second)} {
		policy := coordinationPolicy()
		policy.MaxValueBytes = 64
		policy.MaxBatchResultBytes = 256
		policy.MaxTransientWaiters = 3
		policy.MaxTransientBytes = 1 << 40
		policy.TransientSaturation = saturation
		policy.transientExplicit = true
		policy, err := normalizePolicy(policy)
		if err != nil {
			t.Fatal(err)
		}
		base, err := transientPlanFor(policy)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := typedTransientPlan[transientInlineKey, transientInlineValue](policy, base)
		if err != nil {
			t.Fatal(err)
		}
		batchMaximum, err := plan.batch(policy.MaxBatchKeys, int64(policy.MaxBatchResultBytes))
		if err != nil {
			t.Fatal(err)
		}
		flightMaximum, err := plan.flightCapacity(policy.MaxFlights)
		if err != nil {
			t.Fatal(err)
		}
		peak := maxTransientBytes(plan.key, plan.lookupOperation, plan.putOperation, plan.forgetOperation, plan.build, plan.waiter, plan.background, plan.resolve, batchMaximum, flightMaximum)
		want, ok := addTransientBytes(peak, plan.reserved)
		if !ok || plan.minimum != want {
			t.Fatalf("typed minimum = %d, want %d, reserve %d", plan.minimum, want, plan.reserved)
		}
		if saturation.mode == RejectTransientMode && plan.reserved != 0 {
			t.Fatalf("reject reserve = %d", plan.reserved)
		}
		policy.MaxTransientBytes = want
		resolved, exact, err := resolveTypedPolicy[transientInlineKey, transientInlineValue](policy)
		if err != nil || resolved.MaxTransientBytes != want || exact.minimum != want {
			t.Fatalf("exact typed cap = policy %d, minimum %d, error %v", resolved.MaxTransientBytes, exact.minimum, err)
		}
	}
}

func TestJSONPreflightRejectsInlineExpansionBeforeDestinationDecode(t *testing.T) {
	transientJSONDecodeCalls.Store(0)
	codec := TrustedJSON[[]transientHugeJSON](1)
	value, err := codec.Decode([]byte(`[{}]`), ValueLimit{MaxBytes: 4096, MaxDecodedBytes: 4096, MaxDepth: 16})
	if !errors.Is(err, ErrTooLarge) || value != nil {
		t.Fatalf("json decode = (%v, %v)", value, err)
	}
	if calls := transientJSONDecodeCalls.Load(); calls != 0 {
		t.Fatalf("destination decode calls = %d", calls)
	}
}

func TestJSONDecodeDoesNotStartWithoutTransientAdmission(t *testing.T) {
	transientJSONDecodeCalls.Store(0)
	policy := transientBatchPolicy(t)
	policy.TransientSaturation = RejectTransient()
	backend := newCoordinationBackend()
	instance := newTransientTypedCache(t, backend, TrustedJSON[[]transientHugeJSON](1), policy, "transient-json-admission")
	core := instance.inner.Load()
	encoded, _, _, err := encodeEnvelope(core.runtime, transientHugeWireCodec{}, core.valueDescriptor, core.policy, Present([]transientHugeJSON{{}}))
	if err != nil {
		t.Fatal(err)
	}
	address := cacheAddress(t, instance, "key")
	if err := backend.store(context.Background(), address, encoded); err != nil {
		t.Fatal(err)
	}
	held, ok := core.transient.tryAcquire(core.policy.MaxTransientBytes)
	if !ok {
		t.Fatal("full transient admission failed")
	}
	result, err := instance.Lookup(context.Background(), "key")
	if !errors.Is(err, ErrSaturated) || result.State != 0 || result.Value != nil {
		t.Fatalf("lookup without admission = (%+v, %v)", result, err)
	}
	if calls := transientJSONDecodeCalls.Load(); calls != 0 {
		t.Fatalf("destination decode calls = %d", calls)
	}
	held.release()
	result, err = instance.Lookup(context.Background(), "key")
	if !errors.Is(err, ErrCorrupt) || result.State != 0 || result.Value != nil {
		t.Fatalf("preflighted lookup = (%+v, %v)", result, err)
	}
	if calls := transientJSONDecodeCalls.Load(); calls != 0 {
		t.Fatalf("destination decode calls after preflight = %d", calls)
	}
}

func TestJSONRejectsCustomDecodeHooksUnlessExplicitlyTrusted(t *testing.T) {
	if err := validCodec(JSON[transientJSONHook](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("root codec error = %v", err)
	}
	if err := validCodec(JSON[struct{ Value transientJSONHook }](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reachable codec error = %v", err)
	}
	if err := validCodec(JSON[transientTextHook](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("text codec error = %v", err)
	}
	if err := validCodec(JSON[*****transientJSONHook](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deep hook codec error = %v", err)
	}
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	_, err := New(Runtime{
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, newCoordinationBackend(), Global[string](MustNamespace("tests", "unit", "transient-safe-json", 1)), keys, JSON[transientJSONHook](1), transientTestPolicy(t))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("safe default boot error = %v", err)
	}
	transientHookDecodeCalls.Store(0)
	codec := TrustedJSON[transientJSONHook](1)
	value, err := codec.Decode([]byte(`{}`), ValueLimit{MaxBytes: 64, MaxDecodedBytes: 64, MaxDepth: 4})
	if err != nil || value != (transientJSONHook{}) || transientHookDecodeCalls.Load() != 1 {
		t.Fatalf("trusted decode = (%+v, %v), calls = %d", value, err, transientHookDecodeCalls.Load())
	}
	if codec.ID() != "trusted-json" || codec.Schema() != 1 {
		t.Fatalf("trusted descriptor = (%q, %d)", codec.ID(), codec.Schema())
	}
}

func TestJSONRejectsCustomEncodeHooksBeforeInvocation(t *testing.T) {
	transientMarshalCalls.Store(0)
	if err := validCodec(JSON[transientJSONMarshalHook](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("marshal codec error = %v", err)
	}
	if err := validCodec(JSON[struct{ Value transientJSONMarshalHook }](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nested marshal codec error = %v", err)
	}
	if err := validCodec(JSON[*****transientJSONMarshalHook](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deep marshal codec error = %v", err)
	}
	if err := validCodec(JSON[transientTextMarshalHook](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("text marshal codec error = %v", err)
	}
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	_, err := New(Runtime{
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, newCoordinationBackend(), Global[string](MustNamespace("tests", "unit", "transient-safe-marshal", 1)), keys, JSON[transientJSONMarshalHook](1), transientTestPolicy(t))
	if !errors.Is(err, ErrInvalid) || transientMarshalCalls.Load() != 0 {
		t.Fatalf("marshal boot = (%v, calls %d)", err, transientMarshalCalls.Load())
	}
	codec := JSON[any](1)
	encoded, err := codec.Encode(any(transientJSONMarshalHook{}), ValueLimit{MaxBytes: 64, MaxDecodedBytes: 64, MaxDepth: 4})
	if !errors.Is(err, ErrInvalid) || encoded != nil || transientMarshalCalls.Load() != 0 {
		t.Fatalf("dynamic marshal encode = (%q, %v), calls %d", encoded, err, transientMarshalCalls.Load())
	}
	text := transientTextMarshalHook("value")
	encoded, err = codec.Encode(any(&text), ValueLimit{MaxBytes: 64, MaxDecodedBytes: 64, MaxDepth: 4})
	if !errors.Is(err, ErrInvalid) || encoded != nil || transientMarshalCalls.Load() != 0 {
		t.Fatalf("dynamic text encode = (%q, %v), calls %d", encoded, err, transientMarshalCalls.Load())
	}
}

func TestJSONEncodePreflightBoundsValuesBeforeEncoder(t *testing.T) {
	requireSafeJSONRuntime(t)
	limit := ValueLimit{MaxBytes: 64, MaxDecodedBytes: 64, MaxDepth: 8}
	cases := []struct {
		name   string
		encode func() ([]byte, error)
	}{
		{name: "string", encode: func() ([]byte, error) { return JSON[string](1).Encode(strings.Repeat("x", 1<<20), limit) }},
		{name: "slice", encode: func() ([]byte, error) { return JSON[[]int](1).Encode(make([]int, 1<<20), limit) }},
		{name: "map", encode: func() ([]byte, error) {
			value := make(map[int]int, 4096)
			for index := range 4096 {
				value[index] = index
			}
			return JSON[map[int]int](1).Encode(value, limit)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := test.encode()
			if !errors.Is(err, ErrTooLarge) || encoded != nil {
				t.Fatalf("encode = (%q, %v)", encoded, err)
			}
		})
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if encoded, err := JSON[map[string]any](1).Encode(cycle, ValueLimit{MaxBytes: 4096, MaxDecodedBytes: 4096, MaxDepth: 8}); !errors.Is(err, ErrInvalid) || encoded != nil {
		t.Fatalf("cycle encode = (%q, %v)", encoded, err)
	}
	encoded, err := JSON[string](1).Encode("x", ValueLimit{MaxBytes: 3, MaxDecodedBytes: 512, MaxDepth: 1})
	if err != nil || string(encoded) != `"x"` {
		t.Fatalf("exact encode = (%q, %v)", encoded, err)
	}
}

func TestJSONEncodePreflightBoundsStringScanningByWireLimit(t *testing.T) {
	value := strings.Repeat("x", 1<<20)
	charge, scanned, ok := jsonStringBytesLimited(value, 3)
	if ok || charge != 0 || scanned != 0 {
		t.Fatalf("limited string scan = charge %d, scanned %d, ok %t", charge, scanned, ok)
	}
	limit := ValueLimit{MaxBytes: 3, MaxDecodedBytes: 1 << 20, MaxDepth: 4}
	if err := preflightJSONEncode(reflect.ValueOf(value), limit, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large string preflight error = %v", err)
	}
	if err := preflightJSONEncode(reflect.ValueOf(map[string]int{value: 1}), limit, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large map key preflight error = %v", err)
	}
}

func TestJSONEncodePreflightChargesEveryIgnoredStructFieldScan(t *testing.T) {
	fields := make([]reflect.StructField, 300)
	for index := range fields {
		fields[index] = reflect.StructField{
			Name: fmt.Sprintf("Field%03d", index),
			Type: reflect.TypeFor[struct{}](),
			Tag:  `json:"-"`,
		}
	}
	valueType := reflect.StructOf(fields)
	values := reflect.MakeSlice(reflect.SliceOf(valueType), 300, 300)
	limit := ValueLimit{MaxBytes: 4096, MaxDecodedBytes: 4096, MaxDepth: 4}
	if err := preflightJSONEncode(values, limit, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("wide ignored value preflight error = %v", err)
	}
}

func TestSafeJSONRuntimeModeIsExplicit(t *testing.T) {
	safe := JSON[string](1)
	err := validCodec(safe)
	if safeJSONRuntimeSupported {
		if err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, ErrInvalid) {
		t.Fatalf("jsonv2 safe codec error = %v", err)
	}
	trusted := TrustedJSON[string](1)
	if err := validCodec(trusted); err != nil || trusted.ID() != "trusted-json" {
		t.Fatalf("trusted codec = (%q, %v)", trusted.ID(), err)
	}
	encoded, err := trusted.Encode("value", ValueLimit{MaxBytes: 64, MaxDecodedBytes: 64, MaxDepth: 2})
	if err != nil || string(encoded) != `"value"` {
		t.Fatalf("trusted encode = (%q, %v)", encoded, err)
	}
}

func TestJSONValueLimitsAreHonestForSafeAndTrustedCodecs(t *testing.T) {
	codecs := []Codec[string]{TrustedJSON[string](1)}
	if safeJSONRuntimeSupported {
		codecs = append(codecs, JSON[string](1))
	}
	value := strings.Repeat("<", 20)
	want := `"` + strings.Repeat(`\u003c`, 20) + `"`
	for _, codec := range codecs {
		profile := codec.(jsonCodec[string])
		decodedMinimum := int(profile.root + 2*max(profile.inline, int64(16)) + int64(len(value)))
		if len(want) <= decodedMinimum {
			t.Fatalf("wire %d does not exceed decoded limit %d", len(want), decodedMinimum)
		}
		if encoded, err := codec.Encode(value, ValueLimit{MaxBytes: len(want), MaxDecodedBytes: decodedMinimum - 1, MaxDepth: 2}); !errors.Is(err, ErrTooLarge) || encoded != nil {
			t.Fatalf("decoded bound encode for %s = (%q, %v)", codec.ID(), encoded, err)
		}
		encoded, err := codec.Encode(value, ValueLimit{MaxBytes: len(want), MaxDecodedBytes: decodedMinimum, MaxDepth: 2})
		if err != nil || string(encoded) != want {
			t.Fatalf("exact decoded bound encode for %s = (%q, %v)", codec.ID(), encoded, err)
		}
		decoded, err := codec.Decode(encoded, ValueLimit{MaxBytes: len(want), MaxDecodedBytes: decodedMinimum, MaxDepth: 2})
		if err != nil || decoded != value {
			t.Fatalf("separate wire decode for %s = (%q, %v)", codec.ID(), decoded, err)
		}
		limit := ValueLimit{MaxBytes: len(want), MaxDecodedBytes: decodedMinimum, MaxDepth: jsonSafeMaximumDepth + 1}
		if encoded, err := codec.Encode(value, limit); !errors.Is(err, ErrInvalid) || encoded != nil {
			t.Fatalf("depth encode for %s = (%q, %v)", codec.ID(), encoded, err)
		}
		if decoded, err := codec.Decode([]byte(want), limit); !errors.Is(err, ErrInvalid) || decoded != "" {
			t.Fatalf("depth decode for %s = (%q, %v)", codec.ID(), decoded, err)
		}
	}
	if encoded, err := String(1).Encode("value", ValueLimit{MaxBytes: 64, MaxDecodedBytes: 64, MaxDepth: jsonSafeMaximumDepth + 1}); err != nil || string(encoded) != "value" {
		t.Fatalf("non-JSON depth = (%q, %v)", encoded, err)
	}
}

func TestSafeJSONRejectsUnsupportedStaticShapes(t *testing.T) {
	checks := []error{
		validCodec(JSON[any](1)),
		validCodec(JSON[transientNonemptyInterface](1)),
		validCodec(JSON[func()](1)),
		validCodec(JSON[chan int](1)),
		validCodec(JSON[complex128](1)),
		validCodec(JSON[map[float64]int](1)),
	}
	for index, err := range checks {
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsupported shape %d error = %v", index, err)
		}
	}
}

func TestSafeJSONRejectsIsZeroBeforeInvocation(t *testing.T) {
	transientMarshalCalls.Store(0)
	codec := JSON[struct {
		Value transientIsZeroHook `json:",omitzero"`
	}](1)
	if err := validCodec(codec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("IsZero codec error = %v", err)
	}
	if encoded, err := codec.Encode(struct {
		Value transientIsZeroHook `json:",omitzero"`
	}{Value: 1}, ValueLimit{MaxBytes: 64, MaxDecodedBytes: 4096, MaxDepth: 2}); !errors.Is(err, ErrInvalid) || encoded != nil {
		t.Fatalf("IsZero encode = (%q, %v)", encoded, err)
	}
	if calls := transientMarshalCalls.Load(); calls != 0 {
		t.Fatalf("IsZero calls = %d", calls)
	}
}

func TestJSONDashTagRequiresExactRawTag(t *testing.T) {
	requireSafeJSONRuntime(t)
	type namedDash struct {
		Value string `json:"-,omitempty"`
	}
	codec := JSON[namedDash](1)
	value := namedDash{Value: "value"}
	want := `{"-":"value"}`
	if encoded, err := codec.Encode(value, ValueLimit{MaxBytes: len(want) - 1, MaxDecodedBytes: 4096, MaxDepth: 2}); !errors.Is(err, ErrTooLarge) || encoded != nil {
		t.Fatalf("named dash boundary = (%q, %v)", encoded, err)
	}
	encoded, err := codec.Encode(value, ValueLimit{MaxBytes: len(want), MaxDecodedBytes: 4096, MaxDepth: 2})
	if err != nil || string(encoded) != want {
		t.Fatalf("named dash encode = (%q, %v)", encoded, err)
	}
	transientMarshalCalls.Store(0)
	if err := validCodec(JSON[struct {
		Value transientIsZeroHook `json:"-,omitzero"`
	}](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("named dash IsZero error = %v", err)
	}
	if calls := transientMarshalCalls.Load(); calls != 0 {
		t.Fatalf("named dash IsZero calls = %d", calls)
	}
	if err := validCodec(JSON[struct {
		Value transientJSONMarshalHook `json:"-"`
	}](1)); err != nil {
		t.Fatalf("exact dash ignored hook error = %v", err)
	}
}

func TestJSONEncodePreflightMatchesEscapingAndScalarBounds(t *testing.T) {
	requireSafeJSONRuntime(t)
	codec := JSON[string](1)
	value := "<>&"
	want := `"\u003c\u003e\u0026"`
	if encoded, err := codec.Encode(value, ValueLimit{MaxBytes: len(want) - 1, MaxDecodedBytes: 4096, MaxDepth: 1}); !errors.Is(err, ErrTooLarge) || encoded != nil {
		t.Fatalf("HTML underbound = (%q, %v)", encoded, err)
	}
	encoded, err := codec.Encode(value, ValueLimit{MaxBytes: len(want), MaxDecodedBytes: 4096, MaxDepth: 1})
	if err != nil || string(encoded) != want {
		t.Fatalf("HTML encode = (%q, %v)", encoded, err)
	}
	number, err := JSON[json.Number](1).Encode(json.Number(""), ValueLimit{MaxBytes: 1, MaxDecodedBytes: 4096, MaxDepth: 1})
	if err != nil || string(number) != "0" {
		t.Fatalf("empty number encode = (%q, %v)", number, err)
	}
	if encoded, err := JSON[float64](1).Encode(1e20, ValueLimit{MaxBytes: 21, MaxDecodedBytes: 4096, MaxDepth: 1}); !errors.Is(err, ErrTooLarge) || encoded != nil {
		t.Fatalf("float conservative bound = (%q, %v)", encoded, err)
	}
}

func TestJSONEncodePreflightUsesFallbackForInvalidTags(t *testing.T) {
	requireSafeJSONRuntime(t)
	type tagged struct {
		FallbackName string `json:"bad\\name"`
	}
	codec := JSON[tagged](1)
	value := tagged{FallbackName: "value"}
	encoded, err := codec.Encode(value, ValueLimit{MaxBytes: 4096, MaxDecodedBytes: 4096, MaxDepth: 2})
	if err != nil || string(encoded) != `{"FallbackName":"value"}` {
		t.Fatalf("fallback tag encode = (%q, %v)", encoded, err)
	}
	if encoded, err := codec.Encode(value, ValueLimit{MaxBytes: len(encoded) - 1, MaxDecodedBytes: 4096, MaxDepth: 2}); !errors.Is(err, ErrTooLarge) || encoded != nil {
		t.Fatalf("fallback tag boundary = (%q, %v)", encoded, err)
	}
}

func TestJSONEncodeOnlyProducesWireAdmissibleForDeclaredDecodeLimit(t *testing.T) {
	requireSafeJSONRuntime(t)
	type ignored struct {
		Padding [4096]byte `json:"-"`
	}
	for _, codec := range []Codec[ignored]{JSON[ignored](1), TrustedJSON[ignored](1)} {
		if encoded, err := codec.Encode(ignored{}, ValueLimit{MaxBytes: 64, MaxDecodedBytes: 64, MaxDepth: 2}); !errors.Is(err, ErrTooLarge) || encoded != nil {
			t.Fatalf("ignored inline encode for %s = (%q, %v)", codec.ID(), encoded, err)
		}
	}
	type nested struct {
		Values []map[string]int
	}
	codec := JSON[nested](1).(jsonCodec[nested])
	value := nested{Values: []map[string]int{{"x": 1}, {"y": 2}}}
	large := ValueLimit{MaxBytes: 4096, MaxDecodedBytes: 1 << 20, MaxDepth: 8}
	encoded, err := codec.Encode(value, large)
	if err != nil {
		t.Fatal(err)
	}
	minimum := 1
	maximum := large.MaxDecodedBytes
	for minimum < maximum {
		middle := minimum + (maximum-minimum)/2
		if err := preflightJSONDecode(encoded, middle, large.MaxDepth, codec.root, codec.inline, codec.mapLike, codec.objectBytes, codec.chargeErr); err == nil {
			maximum = middle
		} else {
			minimum = middle + 1
		}
	}
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: minimum - 1, MaxDepth: large.MaxDepth}
	if encoded, err := codec.Encode(value, limit); !errors.Is(err, ErrTooLarge) || encoded != nil {
		t.Fatalf("nested inadmissible encode = (%q, %v), limit %d", encoded, err, minimum-1)
	}
	limit.MaxDecodedBytes = minimum
	encoded, err = codec.Encode(value, limit)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(encoded, limit)
	if err != nil || !reflect.DeepEqual(decoded, value) {
		t.Fatalf("nested round trip = (%+v, %v), limit %d", decoded, err, minimum)
	}
}

func TestJSONScannerMatchesStandardGrammar(t *testing.T) {
	if !safeJSONRuntimeSupported {
		t.Skip("safe JSON scanner is disabled")
	}
	cases := [][]byte{
		[]byte(`null`),
		[]byte(" \t\r\n[true,false,{\"x\":-1.25e+3}] \n"),
		[]byte(`{"a":"\ud83d\ude00","a":"\ud800"}`),
		{'"', 0xff, '"'},
		[]byte(``),
		[]byte(`01`),
		[]byte(`[1,]`),
		[]byte(`{"x":}`),
		[]byte(`true false`),
		[]byte(`"\u12xz"`),
	}
	for _, encoded := range cases {
		got := scanJSON(encoded, 16, nil) == nil
		want := json.Valid(encoded) && utf8.Valid(encoded)
		if got != want {
			t.Fatalf("scanner parity for %q = %t, want %t", encoded, got, want)
		}
	}
}

func FuzzJSONScannerMatchesStandardGrammar(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`null`),
		[]byte(`{"key":[1,true,"value"]}`),
		{'"', 0xff, '"'},
		[]byte(`[1,]`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if !safeJSONRuntimeSupported || len(encoded) > 512 {
			t.Skip()
		}
		got := scanJSON(encoded, jsonSafeMaximumDepth, nil) == nil
		want := json.Valid(encoded) && utf8.Valid(encoded)
		if got != want {
			t.Fatalf("scanner parity for %q = %t, want %t", encoded, got, want)
		}
	})
}

func TestJSONScannerBoundsLargeTokensAndReplacementText(t *testing.T) {
	requireSafeJSONRuntime(t)
	stringCodec := JSON[string](1).(jsonCodec[string])
	invalid := []byte{'"', 0xff, '"'}
	if value, err := stringCodec.Decode(invalid, ValueLimit{MaxBytes: len(invalid), MaxDecodedBytes: 4096, MaxDepth: 1}); !errors.Is(err, ErrCorrupt) || value != "" {
		t.Fatalf("invalid UTF-8 = (%q, %v)", value, err)
	}
	inline := max(stringCodec.inline, int64(16))
	minimum := stringCodec.root + 2*inline + 3
	escaped := []byte(`"\ud800"`)
	if value, err := stringCodec.Decode(escaped, ValueLimit{MaxBytes: len(escaped), MaxDecodedBytes: int(minimum - 1), MaxDepth: 1}); !errors.Is(err, ErrTooLarge) || value != "" {
		t.Fatalf("escaped replacement underbound = (%q, %v)", value, err)
	}
	value, err := stringCodec.Decode(escaped, ValueLimit{MaxBytes: len(escaped), MaxDecodedBytes: int(minimum), MaxDepth: 1})
	if err != nil || value != "�" {
		t.Fatalf("escaped replacement decode = (%q, %v)", value, err)
	}
	largeString := []byte(`"` + strings.Repeat("x", 1<<20) + `"`)
	if value, err := stringCodec.Decode(largeString, ValueLimit{MaxBytes: len(largeString), MaxDecodedBytes: 4096, MaxDepth: 1}); !errors.Is(err, ErrTooLarge) || value != "" {
		t.Fatalf("large string = (%d, %v)", len(value), err)
	}
	largeNumber := []byte(strings.Repeat("9", 1<<20))
	if value, err := JSON[json.Number](1).Decode(largeNumber, ValueLimit{MaxBytes: len(largeNumber), MaxDecodedBytes: 4096, MaxDepth: 1}); !errors.Is(err, ErrTooLarge) || value != "" {
		t.Fatalf("large number = (%q, %v)", value, err)
	}
}

func TestJSONScannerRejectsInvalidUTF8UnknownStructKey(t *testing.T) {
	requireSafeJSONRuntime(t)
	type value struct {
		Known int
	}
	const keyBytes = 1 << 20
	encoded := make([]byte, 0, keyBytes+6)
	encoded = append(encoded, '{', '"')
	for range keyBytes {
		encoded = append(encoded, 0xff)
	}
	encoded = append(encoded, '"', ':', '1', '}')
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: len(encoded) + 4096, MaxDepth: 2}
	decoded, err := JSON[value](1).Decode(encoded, limit)
	if !errors.Is(err, ErrCorrupt) || decoded != (value{}) {
		t.Fatalf("invalid unknown key = (%+v, %v)", decoded, err)
	}
	if decoded, err := TrustedJSON[value](1).Decode(encoded, limit); !errors.Is(err, ErrCorrupt) || decoded != (value{}) {
		t.Fatalf("trusted invalid unknown key = (%+v, %v)", decoded, err)
	}
}

func TestJSONTypeProfilerRejectsExcessivePointerDepth(t *testing.T) {
	value := reflect.TypeFor[int]()
	for range jsonTypeMaximumDepth + 1 {
		value = reflect.PointerTo(value)
	}
	if _, err := newJSONTypeProfiler(false).charge(value); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("deep type error = %v", err)
	}
}

func TestJSONRejectsTimeShapesBeforeDecode(t *testing.T) {
	if err := validCodec(JSON[time.Time](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("time codec error = %v", err)
	}
	codec := JSON[[]time.Time](1)
	if err := validCodec(codec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nested time codec error = %v", err)
	}
	value, err := codec.Decode([]byte(`["2026-08-31T12:34:56+00:01"]`), ValueLimit{MaxBytes: 128, MaxDecodedBytes: 4096, MaxDepth: 4})
	if !errors.Is(err, ErrInvalid) || value != nil {
		t.Fatalf("odd-offset slice decode = (%+v, %v)", value, err)
	}
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	_, err = New(Runtime{
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, newCoordinationBackend(), Global[string](MustNamespace("tests", "unit", "transient-time-json", 1)), keys, JSON[transientTimeValue](1), transientBatchPolicy(t))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("time cache boot error = %v", err)
	}
}

func TestRFC3339UTCIsBoundedAndCanonical(t *testing.T) {
	codec := RFC3339UTC(1)
	if err := validCodec(codec); err != nil || codec.ID() != "time-rfc3339-utc" || codec.Schema() != 1 {
		t.Fatalf("codec descriptor = (%q, %d, %v)", codec.ID(), codec.Schema(), err)
	}
	source := time.Date(2026, time.August, 31, 12, 34, 56, 123400000, time.FixedZone("odd", 61*60))
	want := source.UTC()
	limit := ValueLimit{MaxBytes: 64, MaxDecodedBytes: 64, MaxDepth: 1}
	encoded, err := codec.Encode(source, limit)
	if err != nil || string(encoded) != want.Format(time.RFC3339Nano) || encoded[len(encoded)-1] != 'Z' {
		t.Fatalf("time encode = (%q, %v)", encoded, err)
	}
	decoded, err := codec.Decode(encoded, limit)
	if err != nil || !decoded.Equal(want) || decoded.Location() != time.UTC {
		t.Fatalf("time decode = (%+v, %v)", decoded, err)
	}
	if charge := codec.(DecodeCharger[time.Time]).DecodeCharge(decoded); charge != 0 {
		t.Fatalf("time decode charge = %d", charge)
	}
	short := []byte("2026-08-31T12:34:56Z")
	inline := int(reflect.TypeFor[time.Time]().Size())
	tight := ValueLimit{MaxBytes: 30, MaxDecodedBytes: inline, MaxDepth: 1}
	tightEncoded, err := codec.Encode(source, tight)
	if err != nil || len(tightEncoded) <= tight.MaxDecodedBytes {
		t.Fatalf("separate time wire bound = (%q, %v), decoded %d", tightEncoded, err, tight.MaxDecodedBytes)
	}
	if tightDecoded, err := codec.Decode(tightEncoded, tight); err != nil || !tightDecoded.Equal(want) {
		t.Fatalf("separate time decode = (%+v, %v)", tightDecoded, err)
	}
	if _, err := codec.Decode(short, ValueLimit{MaxBytes: len(short), MaxDecodedBytes: inline - 1, MaxDepth: 1}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("inline bound error = %v", err)
	}
	invalid := [][]byte{
		[]byte("2026-08-31T12:34:56+00:01"),
		[]byte(`"2026-08-31T12:34:56Z"`),
		[]byte("2026-08-31T12:34:56.100Z"),
		[]byte("2026-08-31t12:34:56Z"),
	}
	for _, encoded := range invalid {
		if _, err := codec.Decode(encoded, limit); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("noncanonical %q error = %v", encoded, err)
		}
	}
	if _, err := codec.Encode(time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC), limit); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported year error = %v", err)
	}
	instance := newTransientTypedCache(t, newCoordinationBackend(), codec, transientBatchPolicy(t), "transient-rfc3339-utc")
	if err := instance.Put(context.Background(), "key", source); err != nil {
		t.Fatal(err)
	}
	result, err := instance.Lookup(context.Background(), "key")
	if err != nil || result.State != Hit || !result.Value.Equal(want) || result.Value.Location() != time.UTC {
		t.Fatalf("time lookup = (%+v, %v)", result, err)
	}
}

func TestJSONPreflightChargesDeepPointerScalarStorage(t *testing.T) {
	requireSafeJSONRuntime(t)
	pointerBytes, err := transientTypeBytes[*int]()
	if err != nil {
		t.Fatal(err)
	}
	intBytes, err := transientTypeBytes[int]()
	if err != nil {
		t.Fatal(err)
	}
	codec := JSON[*****int](1).(jsonCodec[*****int])
	expectedInline := pointerBytes*5 + intBytes
	if codec.root != pointerBytes || codec.inline != expectedInline {
		t.Fatalf("pointer profile = (root %d, inline %d), want (%d, %d)", codec.root, codec.inline, pointerBytes, expectedInline)
	}
	encoded := []byte("7")
	minimum := codec.root + 2*codec.inline + int64(len(encoded))
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(minimum - 1), MaxDepth: 1}
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value != nil {
		t.Fatalf("undercharged pointer decode = (%v, %v)", value, err)
	}
	limit.MaxDecodedBytes = int(minimum)
	value, err := codec.Decode(encoded, limit)
	if err != nil || value == nil || *value == nil || **value == nil || ***value == nil || ****value == nil || *****value != 7 {
		t.Fatalf("pointer decode = (%v, %v)", value, err)
	}
}

func TestTrustedJSONPreflightChargesDeepPointerTimeStorage(t *testing.T) {
	pointerBytes, err := transientTypeBytes[*time.Time]()
	if err != nil {
		t.Fatal(err)
	}
	timeBytes, err := transientTypeBytes[time.Time]()
	if err != nil {
		t.Fatal(err)
	}
	codec := TrustedJSON[*****time.Time](1).(jsonCodec[*****time.Time])
	expectedInline := pointerBytes*5 + timeBytes
	if codec.root != pointerBytes || codec.inline != expectedInline {
		t.Fatalf("time pointer profile = (root %d, inline %d), want (%d, %d)", codec.root, codec.inline, pointerBytes, expectedInline)
	}
	encoded := []byte(`"2026-08-31T12:34:56Z"`)
	minimum := codec.root + 2*codec.inline + int64(len(encoded)-2)
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(minimum - 1), MaxDepth: 1}
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value != nil {
		t.Fatalf("undercharged time decode = (%v, %v)", value, err)
	}
	limit.MaxDecodedBytes = int(minimum)
	value, err := codec.Decode(encoded, limit)
	want := time.Date(2026, time.August, 31, 12, 34, 56, 0, time.UTC)
	if err != nil || value == nil || *value == nil || **value == nil || ***value == nil || ****value == nil || !(*****value).Equal(want) {
		t.Fatalf("time pointer decode = (%v, %v)", value, err)
	}
}

func TestJSONTypeProfileTerminatesRecursiveShapes(t *testing.T) {
	requireSafeJSONRuntime(t)
	profiler := newJSONTypeProfiler(false)
	profile, err := profiler.charge(reflect.TypeFor[*transientRecursiveJSON]())
	if err != nil || profile.maximum == 0 || profiler.visits != 3 || len(profiler.nodes) != 3 || profiler.components != 3 || profiler.hookVisits != 3 {
		t.Fatalf("recursive profile = (%+v, %v), visits = %d, nodes = %d, components = %d, hooks = %d", profile, err, profiler.visits, len(profiler.nodes), profiler.components, profiler.hookVisits)
	}
	codec := JSON[*transientRecursiveJSON](1)
	if err := validCodec(codec); err != nil {
		t.Fatal(err)
	}
	value, err := codec.Decode([]byte(`{"Value":1,"Next":{"Value":2}}`), ValueLimit{MaxBytes: 128, MaxDecodedBytes: 4096, MaxDepth: 4})
	if err != nil || value == nil || value.Value != 1 || value.Next == nil || value.Next.Value != 2 {
		t.Fatalf("recursive decode = (%+v, %v)", value, err)
	}
}

func TestJSONPreflightChargesPromotedPointerTargets(t *testing.T) {
	requireSafeJSONRuntime(t)
	codec := JSON[transientPromotedJSONRoot](1).(jsonCodec[transientPromotedJSONRoot])
	rootBytes := int64(reflect.TypeFor[transientPromotedJSONRoot]().Size())
	pointerBytes := int64(reflect.TypeFor[*int]().Size())
	targets := []reflect.Type{
		reflect.TypeFor[TransientPromotedJSON1](),
		reflect.TypeFor[TransientPromotedJSON2](),
		reflect.TypeFor[TransientPromotedJSON3](),
		reflect.TypeFor[TransientPromotedJSON4](),
		reflect.TypeFor[TransientPromotedJSON5](),
	}
	allocationFloor := rootBytes
	expectedInline := rootBytes + pointerBytes*int64(len(targets))
	for _, target := range targets {
		allocationFloor += int64(target.Size())
		expectedInline += int64(target.Size())
	}
	if codec.root != rootBytes || codec.inline != expectedInline || codec.inline < allocationFloor {
		t.Fatalf("promoted profile = (root %d, inline %d), want (%d, %d), allocation floor %d", codec.root, codec.inline, rootBytes, expectedInline, allocationFloor)
	}
	encoded := []byte(`{"X":1}`)
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(allocationFloor - 1), MaxDepth: 2}
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value.TransientPromotedJSON1 != nil {
		t.Fatalf("undercharged promoted decode = (%+v, %v)", value, err)
	}
	minimum := codec.root + 4*codec.inline + 1
	limit.MaxDecodedBytes = int(minimum - 1)
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value.TransientPromotedJSON1 != nil {
		t.Fatalf("near-boundary promoted decode = (%+v, %v)", value, err)
	}
	limit.MaxDecodedBytes = int(minimum)
	value, err := codec.Decode(encoded, limit)
	if err != nil || value.TransientPromotedJSON1 == nil || value.TransientPromotedJSON2 == nil || value.TransientPromotedJSON3 == nil || value.TransientPromotedJSON4 == nil || value.TransientPromotedJSON5 == nil || value.X != 1 {
		t.Fatalf("promoted decode = (%+v, %v)", value, err)
	}
}

func TestJSONTypeProfilerIsSCCSafeAcrossSeedOrder(t *testing.T) {
	requireSafeJSONRuntime(t)
	profiler := newJSONTypeProfiler(false)
	profile, err := profiler.charge(reflect.TypeFor[transientJSONSCCRoot]())
	rootBytes := int64(reflect.TypeFor[transientJSONSCCRoot]().Size())
	pointerBytes := int64(reflect.TypeFor[*int]().Size())
	targets := []reflect.Type{
		reflect.TypeFor[TransientJSONSCCA1](),
		reflect.TypeFor[TransientJSONSCCB1](),
		reflect.TypeFor[TransientJSONSCCA2](),
		reflect.TypeFor[TransientJSONSCCB2](),
		reflect.TypeFor[TransientJSONSCCA3](),
		reflect.TypeFor[TransientJSONSCCB3](),
		reflect.TypeFor[TransientJSONSCCA4](),
		reflect.TypeFor[TransientJSONSCCB4](),
		reflect.TypeFor[TransientJSONSCCA5](),
		reflect.TypeFor[TransientJSONSCCB5](),
	}
	allocationFloor := rootBytes
	expectedInline := rootBytes + pointerBytes*int64(len(targets))
	for _, target := range targets {
		allocationFloor += int64(target.Size())
		expectedInline += int64(target.Size())
	}
	if err != nil || profile.maximum != expectedInline || profile.maximum < allocationFloor || profiler.visits != 22 || len(profiler.nodes) != 22 || profiler.components != 7 || profiler.hookVisits != 22 {
		t.Fatalf("SCC profile = (%+v, %v), want inline %d, floor %d, visits %d, nodes %d, components %d, hooks %d", profile, err, expectedInline, allocationFloor, profiler.visits, len(profiler.nodes), profiler.components, profiler.hookVisits)
	}
	codec := JSON[transientJSONSCCRoot](1).(jsonCodec[transientJSONSCCRoot])
	if codec.inline != expectedInline {
		t.Fatalf("SCC codec inline = %d, want %d", codec.inline, expectedInline)
	}
	encoded := []byte(`{"X":1}`)
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(allocationFloor - 1), MaxDepth: 2}
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value.TransientJSONSCCB1 != nil {
		t.Fatalf("undercharged SCC decode = (%+v, %v)", value, err)
	}
	minimum := codec.root + 4*codec.inline + 1
	limit.MaxDecodedBytes = int(minimum)
	value, err := codec.Decode(encoded, limit)
	if err != nil || value.TransientJSONSCCB1 == nil {
		t.Fatalf("SCC decode root = (%+v, %v)", value, err)
	}
	b1 := value.TransientJSONSCCB1
	a1 := b1.TransientJSONSCCA1
	if a1 == nil || a1.TransientJSONSCCB2 == nil {
		t.Fatalf("SCC decode A1 = (%+v, %v)", value, err)
	}
	b2 := a1.TransientJSONSCCB2
	a2 := b2.TransientJSONSCCA2
	if a2 == nil || a2.TransientJSONSCCB3 == nil {
		t.Fatalf("SCC decode A2 = (%+v, %v)", value, err)
	}
	b3 := a2.TransientJSONSCCB3
	a3 := b3.TransientJSONSCCA3
	if a3 == nil || a3.TransientJSONSCCB4 == nil {
		t.Fatalf("SCC decode A3 = (%+v, %v)", value, err)
	}
	b4 := a3.TransientJSONSCCB4
	a4 := b4.TransientJSONSCCA4
	if a4 == nil || a4.TransientJSONSCCB5 == nil {
		t.Fatalf("SCC decode A4 = (%+v, %v)", value, err)
	}
	b5 := a4.TransientJSONSCCB5
	a5 := b5.TransientJSONSCCA5
	if a5 == nil || a5.X != 1 {
		t.Fatalf("SCC decode A5 = (%+v, %v)", value, err)
	}
}

func TestJSONTypeProfilerMemoizesDiamondDAG(t *testing.T) {
	const depth = 30
	profiler := newJSONTypeProfiler(false)
	profile, err := profiler.charge(reflect.TypeFor[transientJSONDiamond0]())
	expectedVisits := depth*2 + 1
	if err != nil || profile.maximum == 0 || profiler.visits != expectedVisits || len(profiler.nodes) != expectedVisits || profiler.components != expectedVisits || profiler.hookVisits != expectedVisits {
		t.Fatalf("diamond profile = (%+v, %v), visits = %d/%d, nodes = %d, components = %d, hooks = %d", profile, err, profiler.visits, expectedVisits, len(profiler.nodes), profiler.components, profiler.hookVisits)
	}
}

func TestJSONTypeProfilerClassifiesDeepPointersLinearly(t *testing.T) {
	value := reflect.TypeFor[int]()
	const depth = 512
	for range depth {
		value = reflect.PointerTo(value)
	}
	profiler := newJSONTypeProfiler(false)
	profile, err := profiler.charge(value)
	expected := depth + 1
	pointerBytes := int64(reflect.TypeFor[*int]().Size())
	intBytes := int64(reflect.TypeFor[int]().Size())
	if err != nil || profile.maximum != pointerBytes*depth+intBytes || profiler.visits != expected || len(profiler.nodes) != expected || profiler.components != expected || profiler.hookVisits != expected {
		t.Fatalf("deep pointer profile = (%+v, %v), visits %d/%d, nodes %d, components %d, hooks %d", profile, err, profiler.visits, expected, len(profiler.nodes), profiler.components, profiler.hookVisits)
	}
}

func TestJSONTypeProfilerRejectsConstructorWorkBeyondFixedCap(t *testing.T) {
	fields := make([]reflect.StructField, 2048)
	for index := range fields {
		fields[index] = reflect.StructField{Name: fmt.Sprintf("F%d", index), Type: reflect.TypeFor[int]()}
	}
	broad := reflect.StructOf(fields)
	profiler := newJSONTypeProfiler(false)
	if _, err := profiler.charge(broad); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("broad profiler error = %v", err)
	}
	if profiler.edges > jsonTypeMaximumEdges || len(profiler.nodes) > jsonTypeMaximumNodes || profiler.workText > jsonTypeMaximumTextBytes {
		t.Fatalf("profiler caps = edges %d, nodes %d, text %d", profiler.edges, len(profiler.nodes), profiler.workText)
	}
}

func TestJSONPreflightTraversesUnexportedAnonymousEmbeddings(t *testing.T) {
	requireSafeJSONRuntime(t)
	codec := JSON[transientEmbeddedValue](1)
	value, err := codec.Decode([]byte(`{"Values":[{}]}`), ValueLimit{MaxBytes: 1024, MaxDecodedBytes: 1024, MaxDepth: 8})
	if !errors.Is(err, ErrTooLarge) || value.Values != nil {
		t.Fatalf("embedded decode = (%+v, %v)", value, err)
	}
}

func TestJSONPreflightChargesNumberText(t *testing.T) {
	requireSafeJSONRuntime(t)
	encoded := []byte("123456789012345678901234567890")
	codec := JSON[json.Number](1).(jsonCodec[json.Number])
	inline := codec.inline
	if inline < 16 {
		inline = 16
	}
	minimum := int(codec.root + 2*inline + int64(len(encoded)))
	if _, err := codec.Decode(encoded, ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: minimum - 1, MaxDepth: 2}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("undercharged number error = %v", err)
	}
	value, err := codec.Decode(encoded, ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: minimum, MaxDepth: 2})
	if err != nil || value.String() != string(encoded) {
		t.Fatalf("number decode = (%q, %v)", value, err)
	}
}

func TestJSONPreflightChargesEmptyRootMap(t *testing.T) {
	requireSafeJSONRuntime(t)
	codec := JSON[map[string]int](1).(jsonCodec[map[string]int])
	inline := max(codec.inline, int64(16))
	minimum := codec.root + 2*inline + codec.objectBytes
	encoded := []byte(`{}`)
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(minimum - 1), MaxDepth: 1}
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value != nil {
		t.Fatalf("undercharged map decode = (%+v, %v)", value, err)
	}
	limit.MaxDecodedBytes = int(minimum)
	value, err := codec.Decode(encoded, limit)
	if err != nil || value == nil || len(value) != 0 {
		t.Fatalf("map decode = (%+v, %v)", value, err)
	}
}

func TestJSONPreflightChargesEveryEmptyMapInSlice(t *testing.T) {
	requireSafeJSONRuntime(t)
	codec := JSON[[]map[string]int](1).(jsonCodec[[]map[string]int])
	inline := max(codec.inline, int64(16))
	const objects = 3
	minimum := codec.root + int64(objects+1)*2*inline + objects*codec.objectBytes
	encoded := []byte(`[{},{},{}]`)
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(minimum - 1), MaxDepth: 2}
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value != nil {
		t.Fatalf("undercharged map slice decode = (%+v, %v)", value, err)
	}
	limit.MaxDecodedBytes = int(minimum)
	value, err := codec.Decode(encoded, limit)
	if err != nil || len(value) != objects || value[0] == nil || value[1] == nil || value[2] == nil {
		t.Fatalf("map slice decode = (%+v, %v)", value, err)
	}
}

func TestJSONPreflightDoesNotChargeMapEntriesForStructFields(t *testing.T) {
	requireSafeJSONRuntime(t)
	type value struct {
		X int
	}
	codec := JSON[value](1).(jsonCodec[value])
	inline := max(codec.inline, int64(16))
	minimum := codec.root + 4*inline + 1
	encoded := []byte(`{"X":1}`)
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(minimum), MaxDepth: 2}
	decoded, err := codec.Decode(encoded, limit)
	if err != nil || decoded.X != 1 {
		t.Fatalf("struct decode = (%+v, %v), limit %d", decoded, err, minimum)
	}
}

func TestJSONPreflightChargesEmptyObjectDecodedIntoAny(t *testing.T) {
	if err := validCodec(JSON[any](1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("safe interface error = %v", err)
	}
	codec := TrustedJSON[any](1).(jsonCodec[any])
	inline := max(codec.inline, int64(16))
	minimum := codec.root + 2*inline + codec.objectBytes
	encoded := []byte(`{}`)
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(minimum - 1), MaxDepth: 1}
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value != nil {
		t.Fatalf("undercharged any decode = (%+v, %v)", value, err)
	}
	limit.MaxDecodedBytes = int(minimum)
	value, err := codec.Decode(encoded, limit)
	object, ok := value.(map[string]any)
	if err != nil || !ok || object == nil || len(object) != 0 {
		t.Fatalf("any decode = (%+v, %v)", value, err)
	}
}

func TestJSONPreflightChargesFirstMapGroupStorage(t *testing.T) {
	requireSafeJSONRuntime(t)
	codec := JSON[map[string][128]byte](1).(jsonCodec[map[string][128]byte])
	inline := max(codec.inline, int64(16))
	keyCharge := int64(1) + jsonMapEntryBytes
	minimum := codec.root + 4*inline + codec.objectBytes + keyCharge
	encoded := []byte(`{"x":null}`)
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(minimum - 1), MaxDepth: 2}
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value != nil {
		t.Fatalf("undercharged group decode = (%+v, %v)", value, err)
	}
	limit.MaxDecodedBytes = int(minimum)
	value, err := codec.Decode(encoded, limit)
	if err != nil || value == nil || len(value) != 1 {
		t.Fatalf("group decode = (%+v, %v)", value, err)
	}
}

func TestJSONPreflightChargesFirstGroupForEveryMap(t *testing.T) {
	requireSafeJSONRuntime(t)
	codec := JSON[[]map[string][128]byte](1).(jsonCodec[[]map[string][128]byte])
	inline := max(codec.inline, int64(16))
	const objects = 2
	keyCharge := int64(1) + jsonMapEntryBytes
	tokens := int64(1 + objects*2)
	minimum := codec.root + tokens*2*inline + objects*(codec.objectBytes+keyCharge)
	encoded := []byte(`[{"x":null},{"x":null}]`)
	limit := ValueLimit{MaxBytes: len(encoded), MaxDecodedBytes: int(minimum - 1), MaxDepth: 3}
	if value, err := codec.Decode(encoded, limit); !errors.Is(err, ErrTooLarge) || value != nil {
		t.Fatalf("undercharged repeated group decode = (%+v, %v)", value, err)
	}
	limit.MaxDecodedBytes = int(minimum)
	value, err := codec.Decode(encoded, limit)
	if err != nil || len(value) != objects || value[0] == nil || value[1] == nil {
		t.Fatalf("repeated group decode = (%+v, %v)", value, err)
	}
}

func TestBatchDecodedChargeIsCumulativeAndAllOrError(t *testing.T) {
	requireSafeJSONRuntime(t)
	policy := transientBatchPolicy(t)
	jsonCache := newTransientTypedCache(t, newCoordinationBackend(), JSON[transientJSONValue](1), policy, "transient-json-batch")
	for _, key := range []string{"first", "second"} {
		if err := jsonCache.Put(context.Background(), key, transientJSONValue{Name: key}); err != nil {
			t.Fatal(err)
		}
	}
	results, err := jsonCache.LookupMany(context.Background(), []string{"first", "second"})
	if !errors.Is(err, ErrTooLarge) || results != nil {
		t.Fatalf("json batch = (%#v, %v)", results, err)
	}

	stringCache := newTransientTypedCache(t, newCoordinationBackend(), String(1), policy, "transient-string-batch")
	for _, key := range []string{"first", "second"} {
		if err := stringCache.Put(context.Background(), key, key); err != nil {
			t.Fatal(err)
		}
	}
	stringResults, err := stringCache.LookupMany(context.Background(), []string{"first", "second"})
	if err != nil || len(stringResults) != 2 || stringResults[0].Value != "first" || stringResults[1].Value != "second" {
		t.Fatalf("exact batch = (%#v, %v)", stringResults, err)
	}
}

func TestBatchDecodeChargerRejectsInvalidChargesAndAllowsZero(t *testing.T) {
	tests := []struct {
		name    string
		charge  func(string) int64
		wantErr error
	}{
		{name: "negative", charge: func(string) int64 { return -1 }, wantErr: ErrCorrupt},
		{name: "over limit", charge: func(string) int64 { return 4097 }, wantErr: ErrCorrupt},
		{name: "panic", charge: func(string) int64 { panic("charge") }, wantErr: ErrCorrupt},
		{name: "zero", charge: func(string) int64 { return 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := transientBatchPolicy(t)
			codec := &transientChargedStringCodec{charge: test.charge}
			instance := newTransientTypedCache(t, newCoordinationBackend(), codec, policy, "transient-charge")
			if err := instance.Put(context.Background(), "key", "value"); err != nil {
				t.Fatal(err)
			}
			results, err := instance.LookupMany(context.Background(), []string{"key"})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) || results != nil {
					t.Fatalf("charged batch = (%#v, %v)", results, err)
				}
				return
			}
			if err != nil || len(results) != 1 || results[0].Value != "value" || results[0].State != Hit {
				t.Fatalf("zero charged batch = (%#v, %v)", results, err)
			}
		})
	}
}

func TestLookupDecodeChargerRunsExactlyOnceAndRejectsInvalidCharges(t *testing.T) {
	tests := []struct {
		name    string
		charge  int64
		panic   bool
		wantErr error
	}{
		{name: "exact", charge: 5},
		{name: "zero", charge: 0},
		{name: "negative", charge: -1, wantErr: ErrCorrupt},
		{name: "over limit", charge: 4097, wantErr: ErrCorrupt},
		{name: "panic", panic: true, wantErr: ErrCorrupt},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			codec := &transientChargedStringCodec{charge: func(string) int64 {
				calls.Add(1)
				if test.panic {
					panic("charge")
				}
				return test.charge
			}}
			instance := newTransientTypedCache(t, newCoordinationBackend(), codec, transientBatchPolicy(t), "transient-single-charge")
			if err := instance.Put(context.Background(), "key", "value"); err != nil {
				t.Fatal(err)
			}
			result, err := instance.Lookup(context.Background(), "key")
			if calls.Load() != 1 {
				t.Fatalf("charger calls = %d", calls.Load())
			}
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) || result != (Result[string]{}) {
					t.Fatalf("invalid charged lookup = (%+v, %v)", result, err)
				}
				return
			}
			if err != nil || result.State != Hit || result.Value != "value" {
				t.Fatalf("charged lookup = (%+v, %v)", result, err)
			}
		})
	}
}

func TestBatchDecodeChargerSkipsMissAndNegative(t *testing.T) {
	var calls atomic.Int64
	codec := &transientChargedStringCodec{charge: func(string) int64 {
		calls.Add(1)
		panic("unexpected charge")
	}}
	policy := transientTestPolicy(t)
	policy.Freshness = Expiring(time.Second, time.Second)
	policy.Retention = ExpireAfter(10 * time.Second)
	policy.Negative = CacheAbsenceFor(5 * time.Second)
	clock := newReviewClock()
	instance := newTransientClockCache(t, newCoordinationBackend(), codec, policy, clock, "transient-miss-charge")
	if err := instance.Put(context.Background(), "expired", "value"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(3 * time.Second)
	results, err := instance.LookupMany(context.Background(), []string{"expired"})
	if err != nil || len(results) != 1 || results[0].State != Miss {
		t.Fatalf("expired batch = (%+v, %v)", results, err)
	}
	result, err := instance.Resolve(context.Background(), "negative", func(context.Context, string) (LoadResult[string], error) {
		return Absent[string](), nil
	})
	if err != nil || result.State != Negative {
		t.Fatalf("negative resolve = (%+v, %v)", result, err)
	}
	results, err = instance.LookupMany(context.Background(), []string{"negative"})
	if err != nil || len(results) != 1 || results[0].State != Negative {
		t.Fatalf("negative batch = (%+v, %v)", results, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("charger calls = %d", calls.Load())
	}
}

func TestTransientPlanRejectsOverflow(t *testing.T) {
	policy := coordinationPolicy()
	policy.MaxValueBytes = math.MaxInt / 2
	policy.MaxBatchResultBytes = math.MaxInt/2 + 1024
	policy.MaxTransientBytes = math.MaxInt64
	_, err := normalizePolicy(policy)
	if math.MaxInt == math.MaxInt64 {
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("overflow error = %v", err)
		}
	} else if err != nil {
		t.Fatalf("32-bit policy error = %v", err)
	}
	if _, ok := addTransientBytes(math.MaxInt64, 1); ok {
		t.Fatal("overflowing addition succeeded")
	}
	if _, ok := multiplyTransientBytes(math.MaxInt64, 2); ok {
		t.Fatal("overflowing multiplication succeeded")
	}
	policy = coordinationPolicy()
	policy.MaxFlights = math.MaxInt
	policy.MaxTransientBytes = math.MaxInt64
	_, err = normalizePolicy(policy)
	if math.MaxInt == math.MaxInt64 {
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("waiter overflow error = %v", err)
		}
	} else if err != nil {
		t.Fatalf("32-bit waiter policy error = %v", err)
	}
}

func TestMaxValueProfileOverrideKeepsDependentBudgetsSafe(t *testing.T) {
	profiles := []Profile{
		Hot.With(MaxValueBytes(64 << 20)),
		Warm.With(StaleBehavior(ServeWhileRefreshing)),
	}
	for _, profile := range profiles {
		policy, err := profile.Build()
		if err != nil {
			t.Fatal(err)
		}
		plan, err := transientPlanFor(policy)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := maxEnvelopeBytes(policy)
		if err != nil {
			t.Fatal(err)
		}
		if policy.MaxBatchResultBytes < envelope || policy.MaxTransientBytes < plan.minimum {
			t.Fatalf("dependent budgets = batch %d, transient %d, want >= %d and >= %d", policy.MaxBatchResultBytes, policy.MaxTransientBytes, envelope, plan.minimum)
		}
	}
}

func TestTransientBudgetWaitIsCancelableAndReleasesCapacity(t *testing.T) {
	budget := newTransientBudget(10, 0, 1)
	held, ok := budget.tryAcquire(10)
	if !ok {
		t.Fatal("initial admission failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := budget.acquire(ctx, newReviewClock(), WaitForTransient(time.Hour), 1)
		done <- err
	}()
	waitTransientWaiters(t, budget, 1)
	cancel()
	if err := receiveTransientError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission error = %v", err)
	}
	if used, waiters := budget.snapshot(); used != 10 || waiters != 0 {
		t.Fatalf("budget after cancellation = (%d, %d)", used, waiters)
	}

	woken := make(chan *transientLease, 1)
	go func() {
		lease, _ := budget.acquire(context.Background(), newReviewClock(), WaitForTransient(time.Hour), 5)
		woken <- lease
	}()
	waitTransientWaiters(t, budget, 1)
	held.release()
	lease := receiveTransientLease(t, woken)
	if lease == nil {
		t.Fatal("released capacity did not admit waiter")
	}
	lease.release()
	if used, waiters := budget.snapshot(); used != 0 || waiters != 0 {
		t.Fatalf("budget after release = (%d, %d)", used, waiters)
	}
}

func TestTransientBudgetUsesInjectedBoundedTimer(t *testing.T) {
	budget := newTransientBudget(1, 0, 1)
	held, ok := budget.tryAcquire(1)
	if !ok {
		t.Fatal("initial admission failed")
	}
	timerChannel := make(chan time.Time, 1)
	clock := newReviewClock()
	clock.SetTimers(func(time.Duration) Timer { return &reviewTimer{channel: timerChannel} })
	done := make(chan error, 1)
	go func() {
		_, err := budget.acquire(context.Background(), clock, WaitForTransient(time.Second), 1)
		done <- err
	}()
	waitTransientWaiters(t, budget, 1)
	timerChannel <- time.Now()
	if err := receiveTransientError(t, done); !errors.Is(err, ErrSaturated) {
		t.Fatalf("timed admission error = %v", err)
	}
	held.release()
	if used, waiters := budget.snapshot(); used != 0 || waiters != 0 {
		t.Fatalf("budget after timeout = (%d, %d)", used, waiters)
	}
}

func TestTransientAdmissionGateBoundsTimerCreation(t *testing.T) {
	budget := newTransientBudget(8, 0, 1)
	held, ok := budget.tryAcquire(8)
	if !ok {
		t.Fatal("initial admission failed")
	}
	clock := newReviewClock()
	timerChannel := make(chan time.Time)
	var timers atomic.Int64
	clock.SetTimers(func(time.Duration) Timer {
		timers.Add(1)
		return &reviewTimer{channel: timerChannel}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := budget.acquire(ctx, clock, WaitForTransient(time.Hour), 1)
		done <- err
	}()
	waitTransientWaiters(t, budget, 1)
	if lease, err := budget.acquire(context.Background(), clock, WaitForTransient(time.Hour), 1); !errors.Is(err, ErrSaturated) || lease != nil {
		t.Fatalf("full gate admission = (%v, %v)", lease, err)
	}
	if timers.Load() != 1 {
		t.Fatalf("timer creations = %d", timers.Load())
	}
	cancel()
	if err := receiveTransientError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("gated cancellation error = %v", err)
	}
	held.release()
	if used, waiters := budget.snapshot(); used != 0 || waiters != 0 {
		t.Fatalf("gate terminal stats = (%d, %d)", used, waiters)
	}
}

func TestTransientAdmissionTimerFailureAndCancellationReleaseGateOnce(t *testing.T) {
	budget := newTransientBudget(8, 0, 1)
	held, ok := budget.tryAcquire(8)
	if !ok {
		t.Fatal("initial admission failed")
	}
	invalidClock := newReviewClock()
	invalidClock.SetTimers(func(time.Duration) Timer { return nil })
	if lease, err := budget.acquire(context.Background(), invalidClock, WaitForTransient(time.Hour), 1); !errors.Is(err, ErrInvalid) || lease != nil {
		t.Fatalf("invalid timer admission = (%v, %v)", lease, err)
	}
	validClock := newReviewClock()
	validClock.SetTimers(func(time.Duration) Timer { return &reviewTimer{channel: make(chan time.Time)} })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := budget.acquire(ctx, validClock, WaitForTransient(time.Hour), 1)
		done <- err
	}()
	waitTransientWaiters(t, budget, 1)
	cancel()
	if err := receiveTransientError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission = %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() {
		_, err := budget.acquire(ctx, validClock, WaitForTransient(time.Hour), 1)
		done <- err
	}()
	waitTransientWaiters(t, budget, 1)
	cancel()
	if err := receiveTransientError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("second canceled admission = %v", err)
	}
	held.release()
	if used, waiters := budget.snapshot(); used != 0 || waiters != 0 || len(budget.slots) != 0 {
		t.Fatalf("gate terminal state = (%d, %d, %d)", used, waiters, len(budget.slots))
	}
}

func TestTransientAdmissionReservationReducesUsableCapacity(t *testing.T) {
	budget := newTransientBudget(32, 8, 1)
	if lease, ok := budget.tryAcquire(25); ok || lease != nil {
		t.Fatalf("oversized usable admission = (%v, %t)", lease, ok)
	}
	lease, ok := budget.tryAcquire(24)
	if !ok || lease == nil {
		t.Fatal("usable capacity admission failed")
	}
	lease.release()
	if used, waiters := budget.snapshot(); used != 0 || waiters != 0 {
		t.Fatalf("reservation terminal stats = (%d, %d)", used, waiters)
	}
}

func TestTimedContextCancelJoinsWatcherTeardown(t *testing.T) {
	clock := newReviewClock()
	stopEntered := make(chan struct{})
	releaseStop := make(chan struct{})
	clock.SetTimers(func(time.Duration) Timer {
		return &reviewTimer{channel: make(chan time.Time), stop: func() {
			close(stopEntered)
			<-releaseStop
		}}
	})
	var watchers atomic.Int64
	_, cancel, err := newTimedContext(context.Background(), clock, time.Hour, &watchers)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		cancel()
		close(done)
	}()
	receiveTransientSignal(t, stopEntered)
	select {
	case <-done:
		t.Fatal("cancel returned before timer teardown")
	default:
	}
	if watchers.Load() != 1 {
		t.Fatalf("watchers during teardown = %d", watchers.Load())
	}
	close(releaseStop)
	receiveTransientSignal(t, done)
	if watchers.Load() != 0 {
		t.Fatalf("watchers after teardown = %d", watchers.Load())
	}
	for range 100 {
		_, cancel, err := newTimedContext(context.Background(), newReviewClock(), time.Hour, &watchers)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
	}
	if watchers.Load() != 0 {
		t.Fatalf("rapid watcher terminal count = %d", watchers.Load())
	}
}

func TestTransientAdmissionReusesOneDeadlineAcrossStateChanges(t *testing.T) {
	budget := newTransientBudget(1, 0, 1)
	held, ok := budget.tryAcquire(1)
	if !ok {
		t.Fatal("initial admission failed")
	}
	defer held.release()
	clock := newReviewClock()
	timerChannel := make(chan time.Time, 1)
	var timers atomic.Int64
	clock.SetTimers(func(time.Duration) Timer {
		timers.Add(1)
		return &reviewTimer{channel: timerChannel}
	})
	admission := transientAdmission{}
	defer admission.close(budget)
	for index := 0; index < 2; index++ {
		wake := make(chan struct{})
		close(wake)
		lease, err := budget.acquireUntilAdmission(context.Background(), clock, WaitForTransient(time.Second), 1, wake, &admission)
		if !errors.Is(err, errTransientStateChanged) || lease != nil {
			t.Fatalf("state change %d = (%v, %v)", index, lease, err)
		}
	}
	timerChannel <- time.Now()
	held.release()
	lease, err := budget.acquireUntilAdmission(context.Background(), clock, WaitForTransient(time.Second), 1, nil, &admission)
	if !errors.Is(err, ErrSaturated) || lease != nil {
		t.Fatalf("shared deadline error = %v", err)
	}
	if timers.Load() != 1 {
		t.Fatalf("timers = %d", timers.Load())
	}
	if used, waiters := budget.snapshot(); used != 0 || waiters != 0 {
		t.Fatalf("shared deadline stats = (%d, %d)", used, waiters)
	}
}

func TestTransientAdmissionRejectsDeadlineAtSuccessfulHandoff(t *testing.T) {
	budget := newTransientBudget(1, 0, 0)
	timerChannel := make(chan time.Time, 1)
	timer := &transientSignalingTimer{channel: timerChannel, entered: make(chan struct{})}
	admission := transientAdmission{timer: timer}
	defer admission.close(budget)
	budget.mu.Lock()
	done := make(chan error, 1)
	go func() {
		lease, err := budget.acquireUntilAdmission(context.Background(), newReviewClock(), WaitForTransient(time.Hour), 1, nil, &admission)
		if lease != nil {
			lease.release()
		}
		done <- err
	}()
	receiveTransientSignal(t, timer.entered)
	timerChannel <- time.Now()
	budget.mu.Unlock()
	if err := receiveTransientError(t, done); !errors.Is(err, ErrSaturated) {
		t.Fatalf("handoff deadline error = %v", err)
	}
	if used, waiters := budget.snapshot(); used != 0 || waiters != 0 {
		t.Fatalf("handoff deadline stats = (%d, %d)", used, waiters)
	}
}

func TestFlightDeadlineRejectsProbeGrowBeforeRegistration(t *testing.T) {
	instance := newTransientCache(t, newCoordinationBackend(), Bytes(1), transientTestPolicy(t))
	core := instance.inner.Load()
	address, _, err := core.transientAddress(context.Background(), "key", LoadOperation)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := core.transient.acquire(context.Background(), core.runtime.Clock, core.policy.TransientSaturation, core.transientPlan.background)
	if err != nil {
		t.Fatal(err)
	}
	reservation := resolveReservation{lease: probe, weight: core.transientPlan.background}
	state := core.acquireState(address)
	timerChannel := make(chan time.Time, 1)
	timer := &transientSignalingTimer{channel: timerChannel, entered: make(chan struct{})}
	type outcome struct {
		member *flightMember
		grown  bool
		err    error
	}
	done := make(chan outcome, 1)
	core.transient.mu.Lock()
	go func() {
		core.coord.mu.Lock()
		member, grown, err := core.prepareProbedFlightLocked(context.Background(), address, state, false, timer, &reservation)
		core.coord.mu.Unlock()
		done <- outcome{member: member, grown: grown, err: err}
	}()
	select {
	case <-timer.entered:
	case <-time.After(coordinationTestTimeout):
		core.transient.mu.Unlock()
		t.Fatal("flight handoff did not reach budget lock")
	}
	timerChannel <- time.Now()
	core.transient.mu.Unlock()
	var got outcome
	select {
	case got = <-done:
	case <-time.After(coordinationTestTimeout):
		t.Fatal("flight handoff did not terminate")
	}
	if got.member != nil || got.grown || !errors.Is(got.err, ErrSaturated) || reservation.weight != core.transientPlan.background {
		t.Fatalf("flight handoff = (%v, %t, %v), reservation %d", got.member, got.grown, got.err, reservation.weight)
	}
	core.coord.mu.Lock()
	registered := state.member != nil || core.coord.activeFlights != 0
	core.coord.mu.Unlock()
	if registered {
		t.Fatal("expired flight was registered")
	}
	if used, waiters := core.transient.snapshot(); used != core.transientPlan.background || waiters != 0 {
		t.Fatalf("flight handoff budget = (%d, %d)", used, waiters)
	}
	core.releaseState(address, state)
	reservation.release()
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("flight handoff terminal stats = %+v", stats)
	}
}

func TestResolveRestartDoesNotReuseExpiredTransientAdmission(t *testing.T) {
	const admissionTimeout = 317 * time.Millisecond
	backend := newCoordinationBackend()
	clock := newReviewClock()
	admissionTimers := make(chan chan time.Time, 2)
	clock.SetTimers(func(duration time.Duration) Timer {
		channel := make(chan time.Time, 1)
		if duration == admissionTimeout {
			admissionTimers <- channel
		}
		return &reviewTimer{channel: channel}
	})
	policy := transientTestPolicy(t)
	policy.TransientSaturation = WaitForTransient(admissionTimeout)
	instance := newTransientClockCache(t, backend, Bytes(1), policy, clock, "transient-admission-restart")
	core := instance.inner.Load()
	usable := core.policy.MaxTransientBytes - core.transientPlan.reserved
	held, ok := core.transient.tryAcquire(usable - core.transientPlan.key)
	if !ok {
		t.Fatal("transient holder admission failed")
	}
	loaderStarted := make(chan struct{})
	loaderRelease := make(chan struct{})
	done := resolveAsync(context.Background(), instance, "key", func(context.Context, string) (LoadResult[[]byte], error) {
		close(loaderStarted)
		<-loaderRelease
		return Present([]byte("old")), nil
	})
	waitStats(t, instance, func(stats LocalStats) bool { return stats.TransientWaiters == 1 })
	var expired chan time.Time
	select {
	case expired = <-admissionTimers:
	case <-time.After(coordinationTestTimeout):
		t.Fatal("transient admission timer was not created")
	}
	held.release()
	receiveTransientSignal(t, loaderStarted)
	expired <- time.Now()
	if err := instance.Put(context.Background(), "key", []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	close(loaderRelease)
	outcome := receiveResolve(t, done)
	if outcome.err != nil || outcome.result.State != Hit || !bytes.Equal(outcome.result.Value, []byte("replacement")) {
		t.Fatalf("restarted resolve = (%+v, %v)", outcome.result, outcome.err)
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("restart terminal stats = %+v", stats)
	}
}

func TestResolveAdmitsWeightedBuildBeforeStartingLoader(t *testing.T) {
	backend := newCoordinationBackend()
	policy := transientTestPolicy(t)
	policy.TransientSaturation = RejectTransient()
	instance := newTransientCache(t, backend, Bytes(1), policy)
	started := make(chan struct{})
	release := make(chan struct{})
	first := resolveAsync(context.Background(), instance, "first", func(context.Context, string) (LoadResult[[]byte], error) {
		close(started)
		<-release
		return Present([]byte("first")), nil
	})
	receiveTransientSignal(t, started)
	core := instance.inner.Load()
	used, _ := core.transient.snapshot()
	reserve, ok := core.transient.tryAcquire(core.policy.MaxTransientBytes - used)
	if !ok {
		t.Fatal("remaining transient admission failed")
	}
	var secondCalls atomic.Int64
	second, err := instance.Resolve(context.Background(), "second", func(context.Context, string) (LoadResult[[]byte], error) {
		secondCalls.Add(1)
		return Present([]byte("second")), nil
	})
	if !errors.Is(err, ErrSaturated) || second.State != 0 || second.Value != nil {
		t.Fatalf("second resolve = (%+v, %v)", second, err)
	}
	if secondCalls.Load() != 0 {
		t.Fatalf("second loader calls = %d", secondCalls.Load())
	}
	reserve.release()
	close(release)
	outcome := receiveResolve(t, first)
	if outcome.err != nil || outcome.result.State != Loaded || !bytes.Equal(outcome.result.Value, []byte("first")) {
		t.Fatalf("first resolve = (%+v, %v)", outcome.result, outcome.err)
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats.TransientBytes != 0 || stats.TransientWaiters != 0 {
		t.Fatalf("transient stats after resolve = %+v", stats)
	}
}

func TestResolveAccountsSnapshotUntilLastDecode(t *testing.T) {
	backend := newCoordinationBackend()
	policy := transientTestPolicy(t)
	codec := &transientBlockingCodec{started: make(chan struct{}), release: make(chan struct{})}
	instance := newTransientCache(t, backend, codec, policy)
	done := resolveAsync(context.Background(), instance, "key", func(context.Context, string) (LoadResult[[]byte], error) {
		return Present([]byte("value")), nil
	})
	receiveTransientSignal(t, codec.started)
	stats := instance.Stats()
	want := instance.inner.Load().transientPlan.build + instance.inner.Load().transientPlan.waiter
	if stats.TransientBytes != want {
		t.Fatalf("transient bytes during decode = %d, want %d", stats.TransientBytes, want)
	}
	close(codec.release)
	outcome := receiveResolve(t, done)
	if outcome.err != nil || outcome.result.State != Loaded || !bytes.Equal(outcome.result.Value, []byte("value")) {
		t.Fatalf("resolve = (%+v, %v)", outcome.result, outcome.err)
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats.TransientBytes != 0 || stats.TransientWaiters != 0 {
		t.Fatalf("transient stats after decode = %+v", stats)
	}
}

func TestResolveJoinsExistingFlightWithoutAnotherLookupAdmission(t *testing.T) {
	backend := newCoordinationBackend()
	policy := transientTestPolicy(t)
	policy.TransientSaturation = RejectTransient()
	instance := newTransientCache(t, backend, Bytes(1), policy)
	started := make(chan struct{})
	release := make(chan struct{})
	first := resolveAsync(context.Background(), instance, "shared", func(context.Context, string) (LoadResult[[]byte], error) {
		close(started)
		<-release
		return Present([]byte("value")), nil
	})
	receiveTransientSignal(t, started)
	var duplicateCalls atomic.Int64
	second := resolveAsync(context.Background(), instance, "shared", func(context.Context, string) (LoadResult[[]byte], error) {
		duplicateCalls.Add(1)
		return Present([]byte("duplicate")), nil
	})
	waitAddressState(t, instance, "shared", func(state *addressState) bool {
		return state.member != nil && state.member.waiters == 2
	})
	close(release)
	for _, outcome := range []resolveOutcome[[]byte]{receiveResolve(t, first), receiveResolve(t, second)} {
		if outcome.err != nil || outcome.result.State != Loaded || !bytes.Equal(outcome.result.Value, []byte("value")) {
			t.Fatalf("shared resolve = (%+v, %v)", outcome.result, outcome.err)
		}
	}
	if duplicateCalls.Load() != 0 {
		t.Fatalf("duplicate loader calls = %d", duplicateCalls.Load())
	}
	waitCacheQuiescent(t, instance)
}

func TestKeyAndBatchStagesAdmitBeforeCallbacksAndRouting(t *testing.T) {
	backend := newCoordinationBackend()
	observer := &reviewObserver{}
	policy := transientTestPolicy(t)
	policy.TransientSaturation = RejectTransient()
	var keyCalls atomic.Int64
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) {
		keyCalls.Add(1)
		return []byte(key), nil
	})
	instance, err := New(Runtime{
		Observer:       observer,
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, backend, Global[string](MustNamespace("tests", "unit", "transient-key-stage", 1)), keys, Bytes(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	core := instance.inner.Load()
	held, ok := core.transient.tryAcquire(core.policy.MaxTransientBytes)
	if !ok {
		t.Fatal("full transient admission failed")
	}
	if _, err := instance.Lookup(context.Background(), "lookup"); !errors.Is(err, ErrSaturated) {
		t.Fatalf("lookup error = %v", err)
	}
	if err := instance.Put(context.Background(), "put", []byte("value")); !errors.Is(err, ErrSaturated) {
		t.Fatalf("put error = %v", err)
	}
	if err := instance.Forget(context.Background(), "forget"); !errors.Is(err, ErrSaturated) {
		t.Fatalf("forget error = %v", err)
	}
	var loaderCalls atomic.Int64
	if _, err := instance.Resolve(context.Background(), "resolve", func(context.Context, string) (LoadResult[[]byte], error) {
		loaderCalls.Add(1)
		return Present([]byte("value")), nil
	}); !errors.Is(err, ErrSaturated) {
		t.Fatalf("resolve error = %v", err)
	}
	if results, err := instance.LookupMany(context.Background(), []string{"batch"}); !errors.Is(err, ErrSaturated) || results != nil {
		t.Fatalf("batch = (%#v, %v)", results, err)
	}
	if calls := keyCalls.Load(); calls != 0 {
		t.Fatalf("key callback calls = %d", calls)
	}
	if calls := loaderCalls.Load(); calls != 0 {
		t.Fatalf("loader calls = %d", calls)
	}
	if events := transientOperationEvents(observer, LoadOperation); events != 0 {
		t.Fatalf("load events before admission = %d", events)
	}
	held.release()
	if stats := instance.Stats(); stats.TransientBytes != 0 || stats.TransientWaiters != 0 {
		t.Fatalf("transient stats = %+v", stats)
	}
}

func TestOperationsAdmitBeforeCoordinationStateCreation(t *testing.T) {
	policy := transientTestPolicy(t)
	policy.TransientSaturation = RejectTransient()
	instance := newTransientCache(t, newCoordinationBackend(), Bytes(1), policy)
	core := instance.inner.Load()
	held, ok := core.transient.tryAcquire(core.policy.MaxTransientBytes - core.transientPlan.key)
	if !ok {
		t.Fatal("operation reservation failed")
	}
	if _, err := instance.Lookup(context.Background(), "lookup"); !errors.Is(err, ErrSaturated) {
		t.Fatalf("lookup error = %v", err)
	}
	if err := instance.Put(context.Background(), "put", []byte("value")); !errors.Is(err, ErrSaturated) {
		t.Fatalf("put error = %v", err)
	}
	if err := instance.Forget(context.Background(), "forget"); !errors.Is(err, ErrSaturated) {
		t.Fatalf("forget error = %v", err)
	}
	var loads atomic.Int64
	if _, err := instance.Resolve(context.Background(), "resolve", func(context.Context, string) (LoadResult[[]byte], error) {
		loads.Add(1)
		return Present([]byte("value")), nil
	}); !errors.Is(err, ErrSaturated) {
		t.Fatalf("resolve error = %v", err)
	}
	stats := instance.Stats()
	if stats.CoordinationEntries != 0 || stats.CoordinationWaiters != 0 || stats.TimedContextWatchers != 0 || loads.Load() != 0 {
		t.Fatalf("pre-coordination stats = %+v, loads %d", stats, loads.Load())
	}
	held.release()
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("operation terminal stats = %+v", stats)
	}
}

func TestCoordinationWaitersRetainOperationLeases(t *testing.T) {
	backend := newCoordinationBackend()
	instance := newTransientCache(t, backend, Bytes(1), transientTestPolicy(t))
	if err := instance.Put(context.Background(), "key", []byte("value")); err != nil {
		t.Fatal(err)
	}
	deleteEntered := make(chan struct{})
	releaseDelete := make(chan struct{})
	backend.setDeleteHook(func(ctx context.Context, address Address, call int) error {
		close(deleteEntered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-releaseDelete:
			return backend.remove(ctx, address)
		}
	})
	forgetDone := make(chan error, 1)
	go func() { forgetDone <- instance.Forget(context.Background(), "key") }()
	receiveTransientSignal(t, deleteEntered)
	lookupDone := make(chan error, 1)
	go func() {
		_, err := instance.Lookup(context.Background(), "key")
		lookupDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		stats := instance.Stats()
		want := instance.inner.Load().transientPlan.forgetOperation + instance.inner.Load().transientPlan.lookupOperation
		if stats.CoordinationWaiters == 1 && stats.TransientBytes == want && stats.TimedContextWatchers == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("coordination lease stats = %+v, want bytes %d", stats, want)
		}
		runtime.Gosched()
	}
	close(releaseDelete)
	if err := receiveTransientError(t, forgetDone); err != nil {
		t.Fatal(err)
	}
	if err := receiveTransientError(t, lookupDone); err != nil {
		t.Fatal(err)
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("coordination terminal stats = %+v", stats)
	}
}

func TestSameKeyJoinStillRequiresKeyStageAdmission(t *testing.T) {
	backend := newCoordinationBackend()
	policy := transientTestPolicy(t)
	policy.TransientSaturation = RejectTransient()
	var keyCalls atomic.Int64
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) {
		keyCalls.Add(1)
		return []byte(key), nil
	})
	instance, err := New(Runtime{
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, backend, Global[string](MustNamespace("tests", "unit", "transient-same-key-stage", 1)), keys, Bytes(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	first := resolveAsync(context.Background(), instance, "key", func(context.Context, string) (LoadResult[[]byte], error) {
		close(started)
		<-release
		return Present([]byte("value")), nil
	})
	receiveTransientSignal(t, started)
	core := instance.inner.Load()
	used, _ := core.transient.snapshot()
	reserve, ok := core.transient.tryAcquire(core.policy.MaxTransientBytes - used)
	if !ok {
		t.Fatal("remaining transient admission failed")
	}
	result, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[[]byte], error) {
		return Present([]byte("unexpected")), nil
	})
	if !errors.Is(err, ErrSaturated) || result.State != 0 || result.Value != nil {
		t.Fatalf("same-key resolve = (%+v, %v)", result, err)
	}
	if calls := keyCalls.Load(); calls != 1 {
		t.Fatalf("key callback calls = %d", calls)
	}
	reserve.release()
	close(release)
	outcome := receiveResolve(t, first)
	if outcome.err != nil || outcome.result.State != Loaded {
		t.Fatalf("first resolve = (%+v, %v)", outcome.result, outcome.err)
	}
	waitCacheQuiescent(t, instance)
}

func TestForegroundWaitersDecodeSeriallyUnderOneLease(t *testing.T) {
	backend := newCoordinationBackend()
	policy := transientTestPolicy(t)
	codec := &transientSerialCodec{entered: make(chan struct{}, 3), release: make(chan struct{}, 3)}
	instance := newTransientCache(t, backend, codec, policy)
	loaderEntered := make(chan struct{})
	releaseLoader := make(chan struct{})
	loader := func(context.Context, string) (LoadResult[[]byte], error) {
		close(loaderEntered)
		<-releaseLoader
		return Present([]byte("value")), nil
	}
	outcomes := []<-chan resolveOutcome[[]byte]{resolveAsync(context.Background(), instance, "key", loader)}
	receiveTransientSignal(t, loaderEntered)
	for index := 0; index < 2; index++ {
		outcomes = append(outcomes, resolveAsync(context.Background(), instance, "key", loader))
	}
	waitStats(t, instance, func(stats LocalStats) bool { return stats.FlightWaiters == 3 })
	close(releaseLoader)
	for index := 0; index < 3; index++ {
		receiveTransientSignal(t, codec.entered)
		codec.release <- struct{}{}
	}
	for _, done := range outcomes {
		outcome := receiveResolve(t, done)
		if outcome.err != nil || outcome.result.State != Loaded || !bytes.Equal(outcome.result.Value, []byte("value")) {
			t.Fatalf("serialized resolve = (%+v, %v)", outcome.result, outcome.err)
		}
	}
	if calls, maximum := codec.calls.Load(), codec.maximum.Load(); calls != 3 || maximum != 1 {
		t.Fatalf("decode calls = %d, maximum concurrency = %d", calls, maximum)
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("stats after serialized decode = %+v", stats)
	}
}

func TestFastJoinUsesTypedWaiterBudgetForLargeInlineKeys(t *testing.T) {
	policy := coordinationPolicy()
	policy.MaxKeyBytes = 64
	policy.MaxValueBytes = 64
	policy.MaxFlights = 4
	policy.MaxBatchKeys = 4
	policy.MaxBatchKeyBytes = 256
	policy.MaxBatchResultBytes = 256
	policy.MaxTransientBytes = 0
	policy.TransientSaturation = RejectTransient()
	keys := MustKeyFunc(KeyVersion(1), func(key transientInlineKey, _ KeyLimit) ([]byte, error) { return []byte{key[0]}, nil })
	instance, err := New(Runtime{
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, newCoordinationBackend(), Global[transientInlineKey](MustNamespace("tests", "unit", "transient-inline-waiters", 1)), keys, Bytes(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	var key transientInlineKey
	key[0] = 1
	started := make(chan struct{})
	release := make(chan struct{})
	loader := func(context.Context, transientInlineKey) (LoadResult[[]byte], error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return Present([]byte("value")), nil
	}
	outcomes := []<-chan resolveOutcome[[]byte]{resolveAsync(context.Background(), instance, key, loader)}
	receiveTransientSignal(t, started)
	for index := 0; index < policy.MaxFlights-1; index++ {
		outcomes = append(outcomes, resolveAsync(context.Background(), instance, key, loader))
	}
	core := instance.inner.Load()
	want := core.transientPlan.build + int64(policy.MaxFlights)*core.transientPlan.waiter
	stats := waitStats(t, instance, func(stats LocalStats) bool {
		return stats.FlightWaiters == policy.MaxFlights && stats.TransientBytes == want
	})
	if stats.TransientBytes != want {
		t.Fatalf("joined transient bytes = %d, want %d", stats.TransientBytes, want)
	}
	close(release)
	for _, done := range outcomes {
		outcome := receiveResolve(t, done)
		if outcome.err != nil || outcome.result.State != Loaded || !bytes.Equal(outcome.result.Value, []byte("value")) {
			t.Fatalf("joined resolve = (%+v, %v)", outcome.result, outcome.err)
		}
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("joined terminal stats = %+v", stats)
	}
}

func TestFastJoinProbeBoundsCallersBeforeCoordinationLock(t *testing.T) {
	const transientTimeout = 457 * time.Millisecond
	policy := coordinationPolicy()
	policy.MaxKeyBytes = 64
	policy.MaxValueBytes = 64
	policy.MaxBatchKeys = 2
	policy.MaxBatchKeyBytes = 128
	policy.MaxBatchResultBytes = 256
	policy.MaxFlights = 1
	policy.MaxTransientBytes = 0
	policy.MaxTransientWaiters = 2
	policy.TransientSaturation = WaitForTransient(transientTimeout)
	clock := newReviewClock()
	var timers atomic.Int64
	clock.SetTimers(func(duration time.Duration) Timer {
		if duration == transientTimeout {
			timers.Add(1)
		}
		return &reviewTimer{channel: make(chan time.Time)}
	})
	keys := MustKeyFunc(KeyVersion(1), func(key transientInlineKey, _ KeyLimit) ([]byte, error) { return []byte{key[0]}, nil })
	instance, err := New(Runtime{
		Clock:          clock,
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, newCoordinationBackend(), Global[transientInlineKey](MustNamespace("tests", "unit", "transient-probe-gate", 1)), keys, Bytes(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	core := instance.inner.Load()
	usable := core.policy.MaxTransientBytes - core.transientPlan.reserved
	held, ok := core.transient.tryAcquire(usable - core.transientPlan.background)
	if !ok {
		t.Fatal("probe holder admission failed")
	}
	core.coord.mu.Lock()
	type probeOutcome struct {
		result Result[[]byte]
		err    error
	}
	outcomes := make(chan probeOutcome, 4)
	cancels := make([]context.CancelFunc, 4)
	var loaderCalls atomic.Int64
	launch := func(index int) {
		ctx, cancel := context.WithCancel(context.Background())
		cancels[index] = cancel
		var key transientInlineKey
		key[0] = byte(index + 1)
		go func() {
			result, err := instance.Resolve(ctx, key, func(context.Context, transientInlineKey) (LoadResult[[]byte], error) {
				loaderCalls.Add(1)
				return Present([]byte("unexpected")), nil
			})
			outcomes <- probeOutcome{result: result, err: err}
		}()
	}
	launch(0)
	deadline := time.NewTimer(coordinationTestTimeout)
	defer deadline.Stop()
	for {
		used, waiters := core.transient.snapshot()
		if used == usable && waiters == 0 {
			break
		}
		select {
		case <-deadline.C:
			core.coord.mu.Unlock()
			t.Fatalf("first probe did not reach coordination: used %d, waiters %d", used, waiters)
		default:
			runtime.Gosched()
		}
	}
	for index := 1; index < 4; index++ {
		launch(index)
	}
	waitTransientWaiters(t, core.transient, policy.MaxTransientWaiters)
	var received []probeOutcome
	select {
	case outcome := <-outcomes:
		received = append(received, outcome)
		if !errors.Is(outcome.err, ErrSaturated) {
			core.coord.mu.Unlock()
			t.Fatalf("overflow probe = (%+v, %v)", outcome.result, outcome.err)
		}
	case <-time.After(coordinationTestTimeout):
		core.coord.mu.Unlock()
		t.Fatal("overflow probe did not reject")
	}
	if timers.Load() != int64(policy.MaxTransientWaiters) || len(core.coord.states) != 0 {
		core.coord.mu.Unlock()
		t.Fatalf("bounded probe state = timers %d, coordination entries %d", timers.Load(), len(core.coord.states))
	}
	for _, cancel := range cancels {
		cancel()
	}
	core.coord.mu.Unlock()
	for len(received) < 4 {
		select {
		case outcome := <-outcomes:
			received = append(received, outcome)
		case <-time.After(coordinationTestTimeout):
			t.Fatal("probe caller did not terminate")
		}
	}
	saturated := 0
	canceled := 0
	for _, outcome := range received {
		switch {
		case errors.Is(outcome.err, ErrSaturated):
			saturated++
		case errors.Is(outcome.err, context.Canceled):
			canceled++
		default:
			t.Fatalf("probe outcome = (%+v, %v)", outcome.result, outcome.err)
		}
	}
	if saturated != 1 || canceled != 3 || loaderCalls.Load() != 0 {
		t.Fatalf("probe terminals = saturated %d, canceled %d, loads %d", saturated, canceled, loaderCalls.Load())
	}
	held.release()
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("probe terminal stats = %+v", stats)
	}
}

func TestTransientDefaultMakesConfiguredFlightGroupsAttainable(t *testing.T) {
	policy := coordinationPolicy()
	policy.MaxValueBytes = 64
	policy.MaxBatchResultBytes = 256
	policy.MaxFlights = 3
	policy.FlightSaturation = Reject()
	policy.MaxTransientBytes = 0
	policy.TransientSaturation = RejectTransient()
	instance := newTransientCache(t, newCoordinationBackend(), Bytes(1), policy)
	started := make(chan string, policy.MaxFlights)
	release := make(chan struct{})
	loader := func(_ context.Context, key string) (LoadResult[[]byte], error) {
		started <- key
		<-release
		return Present([]byte(key)), nil
	}
	outcomes := make([]<-chan resolveOutcome[[]byte], 0, policy.MaxFlights)
	for index := 0; index < policy.MaxFlights; index++ {
		key := fmt.Sprintf("key-%d", index)
		outcomes = append(outcomes, resolveAsync(context.Background(), instance, key, loader))
		select {
		case got := <-started:
			if got != key {
				t.Fatalf("started key = %q, want %q", got, key)
			}
		case <-time.After(coordinationTestTimeout):
			t.Fatal("configured flight did not start")
		}
	}
	core := instance.inner.Load()
	stats := instance.Stats()
	want := int64(policy.MaxFlights) * (core.transientPlan.build + core.transientPlan.waiter)
	if stats.ActiveFlights != policy.MaxFlights || stats.FlightWaiters != policy.MaxFlights || stats.TransientBytes != want {
		t.Fatalf("configured flight stats = %+v, want transient %d", stats, want)
	}
	var overflowCalls atomic.Int64
	result, err := instance.Resolve(context.Background(), "overflow", func(context.Context, string) (LoadResult[[]byte], error) {
		overflowCalls.Add(1)
		return Present([]byte("overflow")), nil
	})
	if !errors.Is(err, ErrSaturated) || result.State != 0 || result.Value != nil || overflowCalls.Load() != 0 {
		t.Fatalf("overflow flight = (%+v, %v), calls %d", result, err, overflowCalls.Load())
	}
	close(release)
	for _, outcome := range outcomes {
		resolved := receiveResolve(t, outcome)
		if resolved.err != nil || resolved.result.State != Loaded {
			t.Fatalf("configured flight = (%+v, %v)", resolved.result, resolved.err)
		}
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("configured flight terminal stats = %+v", stats)
	}
}

func TestFlightSaturationBeforeRegistrationDoesNotEmitLoad(t *testing.T) {
	policy := transientTestPolicy(t)
	policy.MaxFlights = 1
	policy.FlightSaturation = Reject()
	policy.MaxTransientBytes = 0
	observer := &reviewObserver{}
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		Observer:       observer,
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, newCoordinationBackend(), Global[string](MustNamespace("tests", "unit", "transient-flight-event", 1)), keys, Bytes(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	first := resolveAsync(context.Background(), instance, "first", func(context.Context, string) (LoadResult[[]byte], error) {
		close(started)
		<-release
		return Present([]byte("first")), nil
	})
	receiveTransientSignal(t, started)
	var secondCalls atomic.Int64
	result, err := instance.Resolve(context.Background(), "second", func(context.Context, string) (LoadResult[[]byte], error) {
		secondCalls.Add(1)
		return Present([]byte("second")), nil
	})
	if !errors.Is(err, ErrSaturated) || result.State != 0 || result.Value != nil || secondCalls.Load() != 0 {
		t.Fatalf("flight saturation = (%+v, %v), calls %d", result, err, secondCalls.Load())
	}
	if events := transientOperationEvents(observer, LoadOperation); events != 0 {
		t.Fatalf("pre-registration flight events = %d", events)
	}
	close(release)
	outcome := receiveResolve(t, first)
	if outcome.err != nil || outcome.result.State != Loaded {
		t.Fatalf("registered flight = (%+v, %v)", outcome.result, outcome.err)
	}
	waitCacheQuiescent(t, instance)
	if events := transientOperationEvents(observer, LoadOperation); events != 1 {
		t.Fatalf("registered flight events = %d", events)
	}
}

func TestTransientDefaultMakesStaleFlightOwnersAttainable(t *testing.T) {
	policy := coordinationPolicy()
	policy.Freshness = Expiring(time.Second, 10*time.Second)
	policy.Retention = ExpireAfter(20 * time.Second)
	policy.Stale = ServeOnLoaderError
	policy.MaxFlights = 2
	policy.FlightSaturation = Reject()
	policy.MaxTransientBytes = 0
	policy.TransientSaturation = RejectTransient()
	clock := newReviewClock()
	instance := newTransientClockCache(t, newCoordinationBackend(), Bytes(1), policy, clock, "transient-stale-flight-capacity")
	keys := []string{"first", "second"}
	for _, key := range keys {
		if err := instance.Put(context.Background(), key, []byte("old-"+key)); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(2 * time.Second)
	started := make(chan string, len(keys))
	release := make(chan struct{})
	outcomes := make([]<-chan resolveOutcome[[]byte], 0, len(keys))
	for _, key := range keys {
		outcomes = append(outcomes, resolveAsync(context.Background(), instance, key, func(_ context.Context, key string) (LoadResult[[]byte], error) {
			started <- key
			<-release
			return LoadResult[[]byte]{}, errors.New("loader failed")
		}))
	}
	seen := make(map[string]bool, len(keys))
	for range keys {
		select {
		case key := <-started:
			seen[key] = true
		case <-time.After(coordinationTestTimeout):
			t.Fatal("stale flight did not start")
		}
	}
	if len(seen) != len(keys) {
		t.Fatalf("started stale flights = %v", seen)
	}
	core := instance.inner.Load()
	want := int64(len(keys)) * (core.transientPlan.build + core.transientPlan.retained)
	stats := instance.Stats()
	if stats.ActiveFlights != len(keys) || stats.FlightWaiters != len(keys) || stats.TransientBytes != want {
		t.Fatalf("stale flight owners = %+v, want transient %d", stats, want)
	}
	close(release)
	for index, done := range outcomes {
		outcome := receiveResolve(t, done)
		wantValue := []byte("old-" + keys[index])
		if outcome.err != nil || outcome.result.State != Stale || !bytes.Equal(outcome.result.Value, wantValue) {
			t.Fatalf("stale flight %d = (%+v, %v)", index, outcome.result, outcome.err)
		}
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("stale flight terminal stats = %+v", stats)
	}
}

func TestDecodeTokenWaitIsExactlyCancelable(t *testing.T) {
	instance := newTransientCache(t, newCoordinationBackend(), Bytes(1), transientTestPolicy(t))
	core := instance.inner.Load()
	lease, ok := core.transient.tryAcquire(core.transientPlan.build)
	if !ok {
		t.Fatal("build admission failed")
	}
	groupContext, groupCancel := context.WithCancel(context.Background())
	member := &flightMember{
		done:        make(chan struct{}),
		group:       &flightGroup{cancel: groupCancel},
		waiters:     1,
		snapshot:    resultSnapshot{state: Loaded, payload: []byte("value")},
		transient:   lease,
		finished:    true,
		observed:    true,
		decodeToken: make(chan struct{}, 1),
	}
	close(member.done)
	core.coord.mu.Lock()
	core.coord.flightWaiters = 1
	core.coord.mu.Unlock()
	base, cancel := context.WithCancel(groupContext)
	ctx := &transientSignalContext{Context: base, second: make(chan struct{})}
	done := make(chan resolveOutcome[[]byte], 1)
	go func() {
		result, err := core.awaitMember(ctx, member)
		done <- resolveOutcome[[]byte]{result: result, err: err}
	}()
	receiveTransientSignal(t, ctx.second)
	cancel()
	outcome := receiveResolve(t, done)
	if !errors.Is(outcome.err, context.Canceled) || outcome.result.State != 0 || outcome.result.Value != nil {
		t.Fatalf("canceled decode token = (%+v, %v)", outcome.result, outcome.err)
	}
	if used, waiters := core.transient.snapshot(); used != 0 || waiters != 0 {
		t.Fatalf("transient after token cancellation = (%d, %d)", used, waiters)
	}
	if stats := instance.Stats(); stats.FlightWaiters != 0 {
		t.Fatalf("flight waiters after cancellation = %+v", stats)
	}
}

func TestBackgroundLeaseCoversSynchronousObserver(t *testing.T) {
	backend := newCoordinationBackend()
	observer := &blockingLoadObserver{entered: make(chan struct{}), release: make(chan struct{})}
	policy := transientTestPolicy(t)
	policy.Freshness = Expiring(time.Second, 10*time.Second)
	policy.Retention = ExpireAfter(20 * time.Second)
	policy.Stale = ServeWhileRefreshing
	policy.MaxTransientBytes = 0
	policy.transientDefaulted = false
	clock := newReviewClock()
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		Clock:          clock,
		Observer:       observer,
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, backend, Global[string](MustNamespace("tests", "unit", "transient-observer", 1)), keys, Bytes(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Put(context.Background(), "key", []byte("old")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	result, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[[]byte], error) {
		return Present([]byte("new")), nil
	})
	if err != nil || result.State != Stale {
		t.Fatalf("stale resolve = (%+v, %v)", result, err)
	}
	receiveTransientSignal(t, observer.entered)
	stats := instance.Stats()
	if stats.TransientBytes != instance.inner.Load().transientPlan.build || stats.ActiveFlights != 1 {
		t.Fatalf("observer transient stats = %+v", stats)
	}
	close(observer.release)
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("stats after observer = %+v", stats)
	}
}

func TestFlightContextsDetachCallerValuesAcrossJoinLoadWriteAndObserve(t *testing.T) {
	key := transientContextKey{}
	loaderValues := make(chan any, 1)
	backendValues := make(chan any, 1)
	observerValues := make(chan any, 1)
	observer := &transientContextObserver{key: key, values: observerValues}
	backend := newCoordinationBackend()
	backend.setPutHook(func(ctx context.Context, address Address, value []byte, expiry Expiry, _ int) error {
		backendValues <- ctx.Value(key)
		return backend.store(ctx, address, value)
	})
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		Observer:       observer,
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, backend, Global[string](MustNamespace("tests", "unit", "transient-context-detach", 1)), keys, Bytes(1), transientTestPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var loads atomic.Int64
	loader := func(ctx context.Context, _ string) (LoadResult[[]byte], error) {
		loads.Add(1)
		loaderValues <- ctx.Value(key)
		close(started)
		<-release
		return Present([]byte("value")), nil
	}
	firstCtx := context.WithValue(context.Background(), key, "first-principal")
	secondCtx := context.WithValue(context.Background(), key, "second-principal")
	first := resolveAsync(firstCtx, instance, "key", loader)
	receiveTransientSignal(t, started)
	second := resolveAsync(secondCtx, instance, "key", loader)
	waitStats(t, instance, func(stats LocalStats) bool { return stats.FlightWaiters == 2 })
	close(release)
	for _, done := range []<-chan resolveOutcome[[]byte]{first, second} {
		outcome := receiveResolve(t, done)
		if outcome.err != nil || outcome.result.State != Loaded || !bytes.Equal(outcome.result.Value, []byte("value")) {
			t.Fatalf("detached flight result = (%+v, %v)", outcome.result, outcome.err)
		}
	}
	if value := receiveTransientValue(t, loaderValues); value != nil {
		t.Fatalf("loader caller value = %#v", value)
	}
	if value := receiveTransientValue(t, backendValues); value != nil {
		t.Fatalf("flight backend caller value = %#v", value)
	}
	if value := receiveTransientValue(t, observerValues); value != nil {
		t.Fatalf("load observer caller value = %#v", value)
	}
	if loads.Load() != 1 {
		t.Fatalf("detached flight loads = %d", loads.Load())
	}
	waitCacheQuiescent(t, instance)
}

func TestCleanupContextDetachesCallerValues(t *testing.T) {
	key := transientContextKey{}
	backend := newCoordinationBackend()
	instance := newTransientCache(t, backend, Bytes(1), transientTestPolicy(t))
	if err := instance.Put(context.Background(), "key", []byte("value")); err != nil {
		t.Fatal(err)
	}
	deleteValues := make(chan any, 1)
	backend.setDeleteHook(func(ctx context.Context, address Address, _ int) error {
		deleteValues <- ctx.Value(key)
		return backend.remove(ctx, address)
	})
	ctx := context.WithValue(context.Background(), key, "request-body")
	if err := instance.Forget(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	if value := receiveTransientValue(t, deleteValues); value != nil {
		t.Fatalf("cleanup caller value = %#v", value)
	}
	waitCacheQuiescent(t, instance)
}

func TestFlightWaitTimerTeardownPrecedesReservationRelease(t *testing.T) {
	const flightTimeout = 419 * time.Millisecond
	backend := newCoordinationBackend()
	clock := newReviewClock()
	stopEntered := make(chan struct{})
	releaseStop := make(chan struct{})
	flightTimerCreated := make(chan struct{}, 1)
	clock.SetTimers(func(duration time.Duration) Timer {
		if duration != flightTimeout {
			return &reviewTimer{channel: make(chan time.Time)}
		}
		flightTimerCreated <- struct{}{}
		return &reviewTimer{channel: make(chan time.Time), stop: func() {
			close(stopEntered)
			<-releaseStop
		}}
	})
	policy := transientTestPolicy(t)
	policy.MaxFlights = 1
	policy.FlightSaturation = WaitBounded(flightTimeout)
	instance := newTransientClockCache(t, backend, Bytes(1), policy, clock, "transient-flight-timer-lifetime")
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	first := resolveAsync(context.Background(), instance, "first", func(context.Context, string) (LoadResult[[]byte], error) {
		close(firstStarted)
		<-firstRelease
		return Present([]byte("first")), nil
	})
	receiveTransientSignal(t, firstStarted)
	secondStarted := make(chan struct{})
	second := resolveAsync(context.Background(), instance, "second", func(context.Context, string) (LoadResult[[]byte], error) {
		close(secondStarted)
		return Present([]byte("second")), nil
	})
	receiveTransientSignal(t, flightTimerCreated)
	waitStats(t, instance, func(stats LocalStats) bool { return stats.CoordinationWaiters == 1 })
	close(firstRelease)
	receiveTransientSignal(t, stopEntered)
	firstOutcome := receiveResolve(t, first)
	if firstOutcome.err != nil || firstOutcome.result.State != Loaded {
		t.Fatalf("first resolve = (%+v, %v)", firstOutcome.result, firstOutcome.err)
	}
	select {
	case <-secondStarted:
		t.Fatal("second loader started before timer teardown")
	default:
	}
	core := instance.inner.Load()
	stats := instance.Stats()
	want := core.transientPlan.build + core.transientPlan.background
	if stats.TransientBytes != want || stats.ActiveFlights != 1 || stats.FlightWaiters != 1 {
		t.Fatalf("timer teardown stats = %+v, want transient %d", stats, want)
	}
	close(releaseStop)
	receiveTransientSignal(t, secondStarted)
	secondOutcome := receiveResolve(t, second)
	if secondOutcome.err != nil || secondOutcome.result.State != Loaded {
		t.Fatalf("second resolve = (%+v, %v)", secondOutcome.result, secondOutcome.err)
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("timer teardown terminal stats = %+v", stats)
	}
}

func TestSlowServeWhileRefreshingPropagatesExpiredFlightAdmission(t *testing.T) {
	const flightTimeout = 443 * time.Millisecond
	clock := newReviewClock()
	flightTimers := make(chan chan time.Time, 1)
	clock.SetTimers(func(duration time.Duration) Timer {
		channel := make(chan time.Time, 1)
		if duration == flightTimeout {
			flightTimers <- channel
		}
		return &reviewTimer{channel: channel}
	})
	policy := transientTestPolicy(t)
	policy.Freshness = Expiring(time.Second, 10*time.Second)
	policy.Retention = ExpireAfter(20 * time.Second)
	policy.Stale = ServeWhileRefreshing
	policy.MaxFlights = 1
	policy.FlightSaturation = WaitBounded(flightTimeout)
	policy.MaxTransientBytes = 0
	observer := &reviewObserver{}
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		Clock:          clock,
		Observer:       observer,
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, newCoordinationBackend(), Global[string](MustNamespace("tests", "unit", "transient-slow-swr-deadline", 1)), keys, Bytes(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Put(context.Background(), "key", []byte("old")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	core := instance.inner.Load()
	usable := core.policy.MaxTransientBytes - core.transientPlan.reserved
	held, ok := core.transient.tryAcquire(usable - core.transientPlan.background)
	if !ok {
		t.Fatal("slow-path holder admission failed")
	}
	var loaderCalls atomic.Int64
	done := resolveAsync(context.Background(), instance, "key", func(context.Context, string) (LoadResult[[]byte], error) {
		loaderCalls.Add(1)
		return Present([]byte("unexpected")), nil
	})
	waitTransientWaiters(t, core.transient, 1)
	core.coord.mu.Lock()
	core.coord.activeFlights++
	core.coord.mu.Unlock()
	held.release()
	var timer chan time.Time
	select {
	case timer = <-flightTimers:
	case <-time.After(coordinationTestTimeout):
		t.Fatal("slow-path flight timer was not created")
	}
	waitStats(t, instance, func(stats LocalStats) bool { return stats.CoordinationWaiters == 1 })
	core.coord.mu.Lock()
	core.coord.activeFlights--
	core.signalCapacityLocked()
	timer <- time.Now()
	core.coord.mu.Unlock()
	outcome := receiveResolve(t, done)
	if !errors.Is(outcome.err, ErrSaturated) || outcome.result.State != 0 || outcome.result.Value != nil || loaderCalls.Load() != 0 {
		t.Fatalf("slow-path expired admission = (%+v, %v), calls %d", outcome.result, outcome.err, loaderCalls.Load())
	}
	if events := transientOperationEvents(observer, LoadOperation); events != 0 {
		t.Fatalf("slow-path pre-registration load events = %d", events)
	}
	waitCacheQuiescent(t, instance)
}

func TestSlowResolveReadsBackendBeforeJoiningNewMember(t *testing.T) {
	readFailure := errors.New("remote read failed")
	for _, test := range []struct {
		name      string
		seed      bool
		readError error
	}{
		{name: "remote hit", seed: true},
		{name: "propagated read failure", readError: readFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newCoordinationBackend()
			policy := transientTestPolicy(t)
			policy.MaxFlights = 1
			policy.MaxTransientBytes = 1 << 30
			policy.ReadFailure = Propagate
			instance := newTransientCache(t, backend, Bytes(1), policy)
			if test.seed {
				if err := instance.Put(context.Background(), "key", []byte("remote")); err != nil {
					t.Fatal(err)
				}
			}
			address := cacheAddress(t, instance, "key")
			backend.mu.Lock()
			backend.getHook = func(ctx context.Context, got Address, _ ReadLimit, call int) ([]byte, bool, error) {
				if call == 1 {
					return nil, false, nil
				}
				if test.readError != nil {
					return nil, false, test.readError
				}
				return backend.load(ctx, got)
			}
			backend.mu.Unlock()
			core := instance.inner.Load()
			usable := core.policy.MaxTransientBytes - core.transientPlan.reserved
			held, ok := core.transient.tryAcquire(usable - core.transientPlan.background)
			if !ok {
				t.Fatal("slow parity holder admission failed")
			}
			var loaderCalls atomic.Int64
			done := resolveAsync(context.Background(), instance, "key", func(context.Context, string) (LoadResult[[]byte], error) {
				loaderCalls.Add(1)
				return Present([]byte("local")), nil
			})
			waitTransientWaiters(t, core.transient, 1)
			memberLease, ok := held.split(core.transientPlan.build)
			if !ok {
				held.release()
				t.Fatal("member lease split failed")
			}
			state := core.acquireState(address)
			core.coord.mu.Lock()
			member := core.registerFlightLocked(address, state, false, memberLease)
			core.coord.mu.Unlock()
			core.releaseState(address, state)
			held.release()
			outcome := receiveResolve(t, done)
			if test.readError != nil {
				if !errors.Is(outcome.err, test.readError) || outcome.result.State != 0 || outcome.result.Value != nil {
					t.Fatalf("slow read failure = (%+v, %v)", outcome.result, outcome.err)
				}
			} else if outcome.err != nil || outcome.result.State != Hit || !bytes.Equal(outcome.result.Value, []byte("remote")) {
				t.Fatalf("slow remote hit = (%+v, %v)", outcome.result, outcome.err)
			}
			if loaderCalls.Load() != 0 {
				t.Fatalf("slow parity loader calls = %d", loaderCalls.Load())
			}
			member.group.cancel()
			core.finishFlight(member, resultSnapshot{}, errSuperseded)
			waitCacheQuiescent(t, instance)
		})
	}
}

func TestResolveRestartUsesFreshFlightWaitTimer(t *testing.T) {
	const flightTimeout = 431 * time.Millisecond
	backend := newCoordinationBackend()
	clock := newReviewClock()
	flightTimers := make(chan chan time.Time, 4)
	clock.SetTimers(func(duration time.Duration) Timer {
		channel := make(chan time.Time, 1)
		if duration == flightTimeout {
			flightTimers <- channel
		}
		return &reviewTimer{channel: channel}
	})
	policy := transientTestPolicy(t)
	policy.MaxFlights = 1
	policy.FlightSaturation = WaitBounded(flightTimeout)
	policy.MaxTransientBytes = 1 << 30
	instance := newTransientClockCache(t, backend, Bytes(1), policy, clock, "transient-flight-timer-restart")
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	first := resolveAsync(context.Background(), instance, "first", func(context.Context, string) (LoadResult[[]byte], error) {
		close(firstStarted)
		<-firstRelease
		return Present([]byte("first")), nil
	})
	receiveTransientSignal(t, firstStarted)
	targetStarted := make(chan struct{})
	targetRelease := make(chan struct{})
	var targetCalls atomic.Int64
	target := resolveAsync(context.Background(), instance, "target", func(context.Context, string) (LoadResult[[]byte], error) {
		if targetCalls.Add(1) == 1 {
			close(targetStarted)
			<-targetRelease
			return Present([]byte("superseded")), nil
		}
		return Present([]byte("fresh")), nil
	})
	var expired chan time.Time
	select {
	case expired = <-flightTimers:
	case <-time.After(coordinationTestTimeout):
		t.Fatal("first flight timer was not created")
	}
	waitStats(t, instance, func(stats LocalStats) bool { return stats.CoordinationWaiters == 1 })
	close(firstRelease)
	receiveTransientSignal(t, targetStarted)
	firstOutcome := receiveResolve(t, first)
	if firstOutcome.err != nil || firstOutcome.result.State != Loaded {
		t.Fatalf("first blocker = (%+v, %v)", firstOutcome.result, firstOutcome.err)
	}
	expired <- time.Now()
	targetAddress := cacheAddress(t, instance, "target")
	restartGet := make(chan struct{})
	releaseRestartGet := make(chan struct{})
	var targetReads atomic.Int64
	backend.mu.Lock()
	backend.getHook = func(ctx context.Context, address Address, _ ReadLimit, _ int) ([]byte, bool, error) {
		if address == targetAddress && targetReads.Add(1) == 1 {
			close(restartGet)
			<-releaseRestartGet
		}
		return backend.load(ctx, address)
	}
	backend.mu.Unlock()
	if err := instance.Forget(context.Background(), "target"); err != nil {
		t.Fatal(err)
	}
	close(targetRelease)
	receiveTransientSignal(t, restartGet)
	waitStats(t, instance, func(stats LocalStats) bool { return stats.ActiveFlights == 0 })
	secondStarted := make(chan struct{})
	secondRelease := make(chan struct{})
	second := resolveAsync(context.Background(), instance, "second", func(context.Context, string) (LoadResult[[]byte], error) {
		close(secondStarted)
		<-secondRelease
		return Present([]byte("second")), nil
	})
	receiveTransientSignal(t, secondStarted)
	close(releaseRestartGet)
	select {
	case <-flightTimers:
	case outcome := <-target:
		t.Fatalf("target reused expired timer: (%+v, %v)", outcome.result, outcome.err)
	case <-time.After(coordinationTestTimeout):
		t.Fatal("fresh flight timer was not created")
	}
	waitStats(t, instance, func(stats LocalStats) bool { return stats.CoordinationWaiters == 1 })
	close(secondRelease)
	secondOutcome := receiveResolve(t, second)
	if secondOutcome.err != nil || secondOutcome.result.State != Loaded {
		t.Fatalf("second blocker = (%+v, %v)", secondOutcome.result, secondOutcome.err)
	}
	targetOutcome := receiveResolve(t, target)
	if targetOutcome.err != nil || targetOutcome.result.State != Loaded || !bytes.Equal(targetOutcome.result.Value, []byte("fresh")) {
		t.Fatalf("target restart = (%+v, %v)", targetOutcome.result, targetOutcome.err)
	}
	if targetCalls.Load() != 2 {
		t.Fatalf("target loader calls = %d", targetCalls.Load())
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("fresh timer terminal stats = %+v", stats)
	}
}

func TestBackgroundFinishReleasesBuildAfterCapacitySignal(t *testing.T) {
	instance := newTransientCache(t, newCoordinationBackend(), Bytes(1), transientTestPolicy(t))
	core := instance.inner.Load()
	address := cacheAddress(t, instance, "key")
	state := core.acquireState(address)
	lease, ok := core.transient.tryAcquire(core.transientPlan.build)
	if !ok {
		t.Fatal("build admission failed")
	}
	core.coord.mu.Lock()
	member := core.registerFlightLocked(address, state, true, lease)
	core.coord.mu.Unlock()
	core.transient.mu.Lock()
	done := make(chan struct{})
	go func() {
		core.finishFlight(member, resultSnapshot{state: Negative}, nil)
		close(done)
	}()
	deadline := time.NewTimer(coordinationTestTimeout)
	defer deadline.Stop()
	for {
		core.coord.mu.Lock()
		active := core.coord.activeFlights
		core.coord.mu.Unlock()
		if active == 0 {
			break
		}
		select {
		case <-deadline.C:
			core.transient.mu.Unlock()
			t.Fatal("background flight did not signal capacity")
		default:
			runtime.Gosched()
		}
	}
	if core.transient.used != core.transientPlan.build {
		core.transient.mu.Unlock()
		t.Fatalf("build bytes after capacity signal = %d, want %d", core.transient.used, core.transientPlan.build)
	}
	select {
	case <-done:
		core.transient.mu.Unlock()
		t.Fatal("background finish returned before build release")
	default:
	}
	core.transient.mu.Unlock()
	receiveTransientSignal(t, done)
	core.releaseState(address, state)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("background finish terminal stats = %+v", stats)
	}
}

func TestCoordinationWaitersTrackMutationBarrierAndCancellation(t *testing.T) {
	for _, cancelPut := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "cancel"}[cancelPut], func(t *testing.T) {
			backend := newCoordinationBackend()
			instance := newTransientCache(t, backend, Bytes(1), transientTestPolicy(t))
			if err := instance.Put(context.Background(), "key", []byte("old")); err != nil {
				t.Fatal(err)
			}
			deleteEntered := make(chan struct{})
			releaseDelete := make(chan struct{})
			backend.setDeleteHook(func(ctx context.Context, address Address, call int) error {
				close(deleteEntered)
				<-releaseDelete
				return backend.remove(ctx, address)
			})
			forgetDone := make(chan error, 1)
			go func() { forgetDone <- instance.Forget(context.Background(), "key") }()
			receiveTransientSignal(t, deleteEntered)
			putContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			putDone := make(chan error, 1)
			go func() { putDone <- instance.Put(putContext, "key", []byte("new")) }()
			waitStats(t, instance, func(stats LocalStats) bool { return stats.CoordinationWaiters == 1 })
			if cancelPut {
				cancel()
				if err := receiveTransientError(t, putDone); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled put error = %v", err)
				}
				waitStats(t, instance, func(stats LocalStats) bool { return stats.CoordinationWaiters == 0 })
			} else {
				select {
				case err := <-putDone:
					t.Fatalf("put crossed invalidation early: %v", err)
				default:
				}
			}
			close(releaseDelete)
			if err := receiveTransientError(t, forgetDone); err != nil {
				t.Fatal(err)
			}
			if !cancelPut {
				if err := receiveTransientError(t, putDone); err != nil {
					t.Fatal(err)
				}
			}
			waitCacheQuiescent(t, instance)
			if stats := instance.Stats(); stats != (LocalStats{}) {
				t.Fatalf("stats after mutation barrier = %+v", stats)
			}
		})
	}
}

func TestFlightReservationIsNotSplitAfterGenerationChange(t *testing.T) {
	instance := newTransientCache(t, newCoordinationBackend(), Bytes(1), transientTestPolicy(t))
	core := instance.inner.Load()
	address, _, err := core.transientAddress(context.Background(), "key", LoadOperation)
	if err != nil {
		t.Fatal(err)
	}
	state := core.acquireState(address)
	defer core.releaseState(address, state)
	core.coord.mu.Lock()
	generation := state.generation
	state.generation++
	core.signalStateLocked(state)
	core.coord.mu.Unlock()
	lease, err := core.transient.acquire(context.Background(), core.runtime.Clock, core.policy.TransientSaturation, core.transientPlan.resolve)
	if err != nil {
		t.Fatal(err)
	}
	reservation := resolveReservation{lease: lease, weight: core.transientPlan.resolve}
	member, admitted, err := core.prepareFlight(context.Background(), address, state, generation, false, nil, &reservation)
	if err != nil || admitted || member != nil || reservation.weight != core.transientPlan.resolve {
		t.Fatalf("stale generation admission = (%v, %t, %v), reservation = %d", member, admitted, err, reservation.weight)
	}
	if used, _ := core.transient.snapshot(); used != core.transientPlan.resolve {
		t.Fatalf("reservation bytes = %d, want %d", used, core.transientPlan.resolve)
	}
	reservation.release()
	if used, waiters := core.transient.snapshot(); used != 0 || waiters != 0 {
		t.Fatalf("generation terminal stats = (%d, %d)", used, waiters)
	}
}

func TestServeWhileRefreshingSharesAggregateBudget(t *testing.T) {
	backend := newCoordinationBackend()
	policy := transientTestPolicy(t)
	policy.Freshness = Expiring(time.Second, 10*time.Second)
	policy.Retention = ExpireAfter(20 * time.Second)
	policy.Stale = ServeWhileRefreshing
	policy.MaxTransientBytes = 0
	policy, err := normalizePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	clock := newReviewClock()
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		Clock:          clock,
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, backend, Global[string](MustNamespace("tests", "unit", "transient-swr", 1)), keys, Bytes(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Put(context.Background(), "key", []byte("old")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	started := make(chan struct{})
	release := make(chan struct{})
	result, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[[]byte], error) {
		close(started)
		<-release
		return Present([]byte("new")), nil
	})
	if err != nil || result.State != Stale || !bytes.Equal(result.Value, []byte("old")) {
		t.Fatalf("stale resolve = (%+v, %v)", result, err)
	}
	receiveTransientSignal(t, started)
	result, err = instance.Lookup(context.Background(), "key")
	if err != nil || result.State != Stale || !bytes.Equal(result.Value, []byte("old")) {
		t.Fatalf("lookup during refresh = (%+v, %v)", result, err)
	}
	close(release)
	waitCacheQuiescent(t, instance)
	result, err = instance.Lookup(context.Background(), "key")
	if err != nil || result.State != Hit || !bytes.Equal(result.Value, []byte("new")) {
		t.Fatalf("lookup after refresh = (%+v, %v)", result, err)
	}
}

func TestBackgroundRefreshCanBeJoinedAfterStaleExpiry(t *testing.T) {
	backend := newCoordinationBackend()
	policy := transientTestPolicy(t)
	policy.Freshness = Expiring(time.Second, 2*time.Second)
	policy.Retention = ExpireAfter(10 * time.Second)
	policy.Stale = ServeWhileRefreshing
	clock := newReviewClock()
	instance := newTransientClockCache(t, backend, Bytes(1), policy, clock, "transient-background-join")
	if err := instance.Put(context.Background(), "key", []byte("old")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	started := make(chan struct{})
	release := make(chan struct{})
	stale, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[[]byte], error) {
		close(started)
		<-release
		return Present([]byte("new")), nil
	})
	if err != nil || stale.State != Stale || !bytes.Equal(stale.Value, []byte("old")) {
		t.Fatalf("stale resolve = (%+v, %v)", stale, err)
	}
	receiveTransientSignal(t, started)
	clock.Advance(2 * time.Second)
	var duplicateCalls atomic.Int64
	joined := resolveAsync(context.Background(), instance, "key", func(context.Context, string) (LoadResult[[]byte], error) {
		duplicateCalls.Add(1)
		return Present([]byte("duplicate")), nil
	})
	waitStats(t, instance, func(stats LocalStats) bool { return stats.FlightWaiters == 1 })
	close(release)
	outcome := receiveResolve(t, joined)
	if outcome.err != nil || outcome.result.State != Loaded || !bytes.Equal(outcome.result.Value, []byte("new")) {
		t.Fatalf("background join = (%+v, %v)", outcome.result, outcome.err)
	}
	if duplicateCalls.Load() != 0 {
		t.Fatalf("duplicate loader calls = %d", duplicateCalls.Load())
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("background join stats = %+v", stats)
	}
}

func TestBackgroundLookupRaceRetriesWithCombinedReservation(t *testing.T) {
	backend := newCoordinationBackend()
	policy := transientTestPolicy(t)
	policy.Freshness = Expiring(time.Second, 2*time.Second)
	policy.Retention = ExpireAfter(10 * time.Second)
	policy.Stale = ServeWhileRefreshing
	clock := newReviewClock()
	instance := newTransientClockCache(t, backend, Bytes(1), policy, clock, "transient-background-race")
	if err := instance.Put(context.Background(), "key", []byte("old")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	started := make(chan struct{})
	releaseBackground := make(chan struct{})
	stale, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[[]byte], error) {
		close(started)
		<-releaseBackground
		return LoadResult[[]byte]{}, errors.New("refresh failed")
	})
	if err != nil || stale.State != Stale {
		t.Fatalf("stale resolve = (%+v, %v)", stale, err)
	}
	receiveTransientSignal(t, started)
	clock.Advance(2 * time.Second)
	getEntered := make(chan struct{})
	releaseGet := make(chan struct{})
	var getOnce sync.Once
	backend.mu.Lock()
	backend.getHook = func(ctx context.Context, address Address, _ ReadLimit, _ int) ([]byte, bool, error) {
		getOnce.Do(func() { close(getEntered) })
		<-releaseGet
		return backend.load(ctx, address)
	}
	backend.mu.Unlock()
	var recoveryCalls atomic.Int64
	recovered := resolveAsync(context.Background(), instance, "key", func(context.Context, string) (LoadResult[[]byte], error) {
		recoveryCalls.Add(1)
		return Present([]byte("recovered")), nil
	})
	receiveTransientSignal(t, getEntered)
	close(releaseBackground)
	waitStats(t, instance, func(stats LocalStats) bool { return stats.ActiveFlights == 0 })
	close(releaseGet)
	outcome := receiveResolve(t, recovered)
	if outcome.err != nil || outcome.result.State != Loaded || !bytes.Equal(outcome.result.Value, []byte("recovered")) {
		t.Fatalf("lookup race = (%+v, %v)", outcome.result, outcome.err)
	}
	if recoveryCalls.Load() != 1 {
		t.Fatalf("recovery loader calls = %d", recoveryCalls.Load())
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("lookup race stats = %+v", stats)
	}
}

func TestDecodedStaleFallbackRemainsChargedThroughLoader(t *testing.T) {
	policy := transientTestPolicy(t)
	policy.Freshness = Expiring(time.Second, 10*time.Second)
	policy.Retention = ExpireAfter(20 * time.Second)
	policy.Stale = ServeOnLoaderError
	clock := newReviewClock()
	instance := newTransientClockCache(t, newCoordinationBackend(), Bytes(1), policy, clock, "transient-stale-fallback")
	if err := instance.Put(context.Background(), "key", []byte("old")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Second)
	started := make(chan struct{})
	release := make(chan struct{})
	done := resolveAsync(context.Background(), instance, "key", func(context.Context, string) (LoadResult[[]byte], error) {
		close(started)
		<-release
		return LoadResult[[]byte]{}, errors.New("loader failed")
	})
	receiveTransientSignal(t, started)
	core := instance.inner.Load()
	stats := instance.Stats()
	want := core.transientPlan.build + core.transientPlan.retained
	if stats.TransientBytes != want || stats.FlightWaiters != 1 {
		t.Fatalf("stale fallback stats = %+v, want transient %d", stats, want)
	}
	close(release)
	outcome := receiveResolve(t, done)
	if outcome.err != nil || outcome.result.State != Stale || !bytes.Equal(outcome.result.Value, []byte("old")) {
		t.Fatalf("stale fallback = (%+v, %v)", outcome.result, outcome.err)
	}
	waitCacheQuiescent(t, instance)
	if stats := instance.Stats(); stats != (LocalStats{}) {
		t.Fatalf("stale fallback terminal stats = %+v", stats)
	}
}

func TestTransientBudgetConcurrentTerminalPaths(t *testing.T) {
	budget := newTransientBudget(32, 0, 0)
	var group sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		worker := worker
		group.Add(1)
		go func() {
			defer group.Done()
			for iteration := 0; iteration < 100; iteration++ {
				weight := int64((worker+iteration)%8 + 1)
				lease, err := budget.acquire(context.Background(), newReviewClock(), RejectTransient(), weight)
				if err == nil {
					lease.release()
				} else if !errors.Is(err, ErrSaturated) {
					t.Errorf("admission error = %v", err)
					return
				}
			}
		}()
	}
	group.Wait()
	if used, waiters := budget.snapshot(); used != 0 || waiters != 0 {
		t.Fatalf("budget after stress = (%d, %d)", used, waiters)
	}
}

func transientTestPolicy(t *testing.T) Policy {
	t.Helper()
	policy := coordinationPolicy()
	policy.MaxValueBytes = 64
	policy.MaxBatchResultBytes = 256
	policy.MaxTransientBytes = 0
	policy.TransientSaturation = WaitForTransient(coordinationTestTimeout)
	normalized, err := normalizePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}

func transientBatchPolicy(t *testing.T) Policy {
	t.Helper()
	policy := coordinationPolicy()
	policy.MaxValueBytes = 4096
	policy.MaxBatchResultBytes = 5000
	policy.MaxTransientBytes = 0
	policy.TransientSaturation = WaitForTransient(coordinationTestTimeout)
	policy.transientDefaulted = false
	normalized, err := normalizePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}

func requireSafeJSONRuntime(t *testing.T) {
	t.Helper()
	if !safeJSONRuntimeSupported {
		t.Skip("safe JSON is unavailable in this runtime mode")
	}
}

func transientOperationEvents(observer *reviewObserver, operation Operation) int {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	count := 0
	for _, event := range observer.events {
		if event.Operation == operation {
			count++
		}
	}
	return count
}

func newDisabledTransientStringCache(t *testing.T, runtime Runtime) *Cache[string, string] {
	t.Helper()
	policy, err := Disabled.Build()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := New[string, string](runtime, nil, Scope[string]{}, nil, nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func newTransientRuntimeStringCache(t *testing.T, runtime Runtime, backend Backend, purpose string) *Cache[string, string] {
	t.Helper()
	policy := coordinationPolicy()
	policy.MaxTransientBytes = 0
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(runtime, backend, Global[string](MustNamespace("tests", "unit", purpose, 1)), keys, String(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func newTransientCache(t *testing.T, backend Backend, codec Codec[[]byte], policy Policy) *Cache[string, []byte] {
	return newTransientTypedCache(t, backend, codec, policy, "transient")
}

func newTransientTypedCache[V any](t *testing.T, backend Backend, codec Codec[V], policy Policy, purpose string) *Cache[string, V] {
	t.Helper()
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, backend, Global[string](MustNamespace("tests", "unit", purpose, 1)), keys, codec, policy)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func newTransientClockCache[V any](t *testing.T, backend Backend, codec Codec[V], policy Policy, clock Clock, purpose string) *Cache[string, V] {
	t.Helper()
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		Clock:          clock,
		LoaderTimeout:  coordinationTestTimeout,
		BackendTimeout: coordinationTestTimeout,
		CleanupTimeout: coordinationTestTimeout,
	}, backend, Global[string](MustNamespace("tests", "unit", purpose, 1)), keys, codec, policy)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func waitStats[K, V any](t *testing.T, instance *Cache[K, V], predicate func(LocalStats) bool) LocalStats {
	t.Helper()
	deadline := time.NewTimer(coordinationTestTimeout)
	defer deadline.Stop()
	for {
		stats := instance.Stats()
		if predicate(stats) {
			return stats
		}
		select {
		case <-deadline.C:
			t.Fatalf("stats did not reach expected state: %+v", stats)
		default:
			runtime.Gosched()
		}
	}
}

func waitTransientWaiters(t *testing.T, budget *transientBudget, want int) {
	t.Helper()
	deadline := time.NewTimer(coordinationTestTimeout)
	defer deadline.Stop()
	for {
		_, waiters := budget.snapshot()
		if waiters == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("transient waiters = %d, want %d", waiters, want)
		default:
			runtime.Gosched()
		}
	}
}

func receiveTransientError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(coordinationTestTimeout):
		t.Fatal("timed out waiting for transient error")
		return nil
	}
}

func receiveTransientLease(t *testing.T, result <-chan *transientLease) *transientLease {
	t.Helper()
	select {
	case lease := <-result:
		return lease
	case <-time.After(coordinationTestTimeout):
		t.Fatal("timed out waiting for transient lease")
		return nil
	}
}

func receiveTransientSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(coordinationTestTimeout):
		t.Fatal("timed out waiting for transient signal")
	}
}

func receiveTransientValue(t *testing.T, values <-chan any) any {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(coordinationTestTimeout):
		t.Fatal("timed out waiting for transient value")
		return nil
	}
}

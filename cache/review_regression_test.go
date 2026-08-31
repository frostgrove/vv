package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type reviewClock struct {
	mu       sync.Mutex
	now      time.Time
	panicNow bool
	sample   chan struct{}
	timers   func(time.Duration) Timer
}

func newReviewClock() *reviewClock {
	return &reviewClock{now: time.Unix(1_900_000_000, 0).UTC()}
}

func (clock *reviewClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	if clock.panicNow {
		panic("clock")
	}
	if clock.sample != nil {
		close(clock.sample)
		clock.sample = nil
	}
	return clock.now
}

func (clock *reviewClock) NewTimer(duration time.Duration) Timer {
	clock.mu.Lock()
	factory := clock.timers
	clock.mu.Unlock()
	if factory != nil {
		return factory(duration)
	}
	return &reviewTimer{channel: make(chan time.Time)}
}

func (clock *reviewClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

func (clock *reviewClock) PanicNow() {
	clock.mu.Lock()
	clock.panicNow = true
	clock.mu.Unlock()
}

func (clock *reviewClock) ArmSample(signal chan struct{}) {
	clock.mu.Lock()
	clock.sample = signal
	clock.mu.Unlock()
}

func (clock *reviewClock) SetTimers(factory func(time.Duration) Timer) {
	clock.mu.Lock()
	clock.timers = factory
	clock.mu.Unlock()
}

type reviewTimer struct {
	channel <-chan time.Time
	stop    func()
}

func (timer *reviewTimer) C() <-chan time.Time {
	return timer.channel
}

func (timer *reviewTimer) Stop() bool {
	if timer.stop != nil {
		timer.stop()
	}
	return true
}

type reviewPanicChannelTimer struct{}

func (*reviewPanicChannelTimer) C() <-chan time.Time {
	panic("timer channel")
}

func (*reviewPanicChannelTimer) Stop() bool {
	return true
}

type reviewPanicRandom struct{}

func (reviewPanicRandom) Uint64() uint64 {
	panic("random")
}

type reviewCodec struct {
	encode func(string, ValueLimit) ([]byte, error)
	decode func([]byte, ValueLimit) (string, error)
}

func (*reviewCodec) ID() string {
	return "review-string"
}

func (*reviewCodec) Schema() ValueSchema {
	return 1
}

func (codec *reviewCodec) Encode(value string, limit ValueLimit) ([]byte, error) {
	if codec.encode != nil {
		return codec.encode(value, limit)
	}
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return []byte(value), nil
}

func (codec *reviewCodec) Decode(value []byte, limit ValueLimit) (string, error) {
	if codec.decode != nil {
		return codec.decode(value, limit)
	}
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return "", ErrTooLarge
	}
	return string(bytes.Clone(value)), nil
}

func reviewPolicy() Policy {
	return Policy{
		Freshness:           Expiring(time.Hour, time.Hour),
		Retention:           ExpireAfter(3 * time.Hour),
		Negative:            NoNegativeCaching(),
		Jitter:              NoJitter(),
		MaxKeyBytes:         256,
		MaxValueBytes:       4 << 10,
		MaxValueDepth:       16,
		MaxFlights:          8,
		FlightSaturation:    WaitBounded(time.Hour),
		Stale:               RefreshBlocking,
		LastWaiter:          CancelLoader,
		MaxBatchKeys:        16,
		MaxBatchKeyBytes:    4 << 10,
		MaxBatchResultBytes: 64 << 10,
		ReadFailure:         Propagate,
		WriteFailure:        Propagate,
		InvalidateFailure:   Propagate,
		Corruption:          RefuseCorrupt,
		profile:             "review",
	}
}

func newReviewCache(t *testing.T, clock Clock, random Random, backend Backend, codec Codec[string], policy Policy) *Cache[string, string] {
	t.Helper()
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) {
		return []byte(key), nil
	})
	instance, err := New(Runtime{
		Clock:          clock,
		Random:         random,
		LoaderTimeout:  time.Hour,
		BackendTimeout: time.Hour,
		CleanupTimeout: time.Hour,
	}, backend, Global[string](MustNamespace("review", "test", "regression", 1)), keys, codec, policy)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestReviewPutStagesEncodingWithoutBlockingReadsAndFencesLoader(t *testing.T) {
	clock := newReviewClock()
	backend := newCoordinationBackend()
	entered := make(chan struct{})
	releaseEncode := make(chan struct{})
	codec := &reviewCodec{}
	codec.encode = func(value string, limit ValueLimit) ([]byte, error) {
		if value == "explicit" {
			close(entered)
			<-releaseEncode
		}
		if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
			return nil, ErrTooLarge
		}
		return []byte(value), nil
	}
	policy := reviewPolicy()
	policy.Freshness = Expiring(time.Second, time.Second)
	policy.Retention = ExpireAfter(3 * time.Second)
	policy.LastWaiter = FinishLoader
	instance := newReviewCache(t, clock, nil, backend, codec, policy)
	if err := instance.Put(context.Background(), "key", "old"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(3 * time.Second)
	putDone := make(chan error, 1)
	go func() {
		putDone <- instance.Put(context.Background(), "key", "explicit")
	}()
	waitSignal(t, entered, "explicit encoding")
	lookupDone := make(chan resolveOutcome[string], 1)
	go func() {
		result, err := instance.Lookup(context.Background(), "key")
		lookupDone <- resolveOutcome[string]{result: result, err: err}
	}()
	lookup := receiveResolve(t, lookupDone)
	if lookup.err != nil || lookup.result.State != Miss {
		t.Fatalf("lookup during encoding = %+v, err = %v", lookup.result, lookup.err)
	}
	resolveCtx, cancelResolve := context.WithCancel(context.Background())
	var loaderCalls atomic.Int32
	backend.mu.Lock()
	backend.getHook = func(context.Context, Address, ReadLimit, int) ([]byte, bool, error) {
		cancelResolve()
		return nil, false, nil
	}
	backend.mu.Unlock()
	resolved := resolveAsync(resolveCtx, instance, "key", func(context.Context, string) (LoadResult[string], error) {
		loaderCalls.Add(1)
		return Present("loader"), nil
	})
	resolveResult := receiveResolve(t, resolved)
	if !errors.Is(resolveResult.err, context.Canceled) {
		t.Fatalf("resolve error = %v", resolveResult.err)
	}
	backend.mu.Lock()
	backend.getHook = nil
	backend.mu.Unlock()
	close(releaseEncode)
	if err := receiveError(t, putDone); err != nil {
		t.Fatal(err)
	}
	waitCacheQuiescent(t, instance)
	if loaderCalls.Load() != 0 {
		t.Fatalf("loader calls = %d", loaderCalls.Load())
	}
	result, err := instance.Lookup(context.Background(), "key")
	if err != nil || result.Value != "explicit" || result.State != Hit {
		t.Fatalf("final lookup = %+v, err = %v", result, err)
	}
	_, puts, _ := backend.calls()
	if puts != 2 {
		t.Fatalf("backend puts = %d", puts)
	}
	assertQuiescent(t, instance)
}

func TestReviewFailedLaterPutDoesNotEraseEarlierStagedPut(t *testing.T) {
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	codec := &reviewCodec{}
	codec.encode = func(value string, limit ValueLimit) ([]byte, error) {
		switch value {
		case "first":
			close(firstEntered)
			<-releaseFirst
		case "second":
			close(secondEntered)
			return nil, ErrInvalid
		}
		if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
			return nil, ErrTooLarge
		}
		return []byte(value), nil
	}
	instance := newReviewCache(t, newReviewClock(), nil, newCoordinationBackend(), codec, reviewPolicy())
	firstDone := make(chan error, 1)
	go func() { firstDone <- instance.Put(context.Background(), "key", "first") }()
	waitSignal(t, firstEntered, "first staged encoding")
	core, err := instance.core()
	if err != nil {
		t.Fatal(err)
	}
	address := cacheAddress(t, instance, "key")
	core.coord.mu.Lock()
	staged := core.coord.states[address].stagedMutation
	core.coord.mu.Unlock()
	if staged == 0 {
		t.Fatal("first mutation was not staged")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- instance.Put(context.Background(), "key", "second") }()
	waitAddressState(t, instance, "key", func(state *addressState) bool {
		return state.refs >= 2 && state.stagedMutation == staged
	})
	close(releaseFirst)
	if err := receiveError(t, firstDone); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, secondEntered, "second staged encoding")
	if err := receiveError(t, secondDone); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second Put() error = %v", err)
	}
	result, err := instance.Lookup(context.Background(), "key")
	if err != nil || result.Value != "first" || result.State != Hit {
		t.Fatalf("final lookup = %+v, error = %v", result, err)
	}
	waitCacheQuiescent(t, instance)
}

func TestReviewRelativeExpiryAccountsForDelayedClaim(t *testing.T) {
	for _, test := range []struct {
		name       string
		claimDelay time.Duration
		wantWrites int
		wantTTL    time.Duration
	}{
		{name: "remaining lifetime", claimDelay: 4 * time.Second, wantWrites: 2, wantTTL: 6 * time.Second},
		{name: "expired before claim", claimDelay: 10 * time.Second, wantWrites: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newReviewClock()
			backend := newCoordinationBackend()
			firstWrite := make(chan struct{})
			releaseFirst := make(chan struct{})
			var expiryMu sync.Mutex
			var explicitExpiry Expiry
			backend.setPutHook(func(ctx context.Context, address Address, value []byte, expiry Expiry, call int) error {
				switch call {
				case 1:
					close(firstWrite)
					<-releaseFirst
					return backend.store(ctx, address, value)
				case 2:
					expiryMu.Lock()
					explicitExpiry = expiry
					expiryMu.Unlock()
					return backend.store(ctx, address, value)
				default:
					return unexpectedCall("put", call)
				}
			})
			encodeEntered := make(chan struct{})
			releaseEncode := make(chan struct{})
			envelopeSampled := make(chan struct{})
			codec := &reviewCodec{}
			codec.encode = func(value string, limit ValueLimit) ([]byte, error) {
				if value == "explicit" {
					close(encodeEntered)
					<-releaseEncode
					clock.ArmSample(envelopeSampled)
				}
				if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
					return nil, ErrTooLarge
				}
				return []byte(value), nil
			}
			policy := reviewPolicy()
			policy.Freshness = Expiring(10*time.Second, 0)
			policy.Retention = ExpireAfter(10 * time.Second)
			instance := newReviewCache(t, clock, nil, backend, codec, policy)
			firstDone := make(chan error, 1)
			go func() {
				firstDone <- instance.Put(context.Background(), "key", "first")
			}()
			waitSignal(t, firstWrite, "first claimed write")
			explicitDone := make(chan error, 1)
			go func() {
				explicitDone <- instance.Put(context.Background(), "key", "explicit")
			}()
			waitSignal(t, encodeEntered, "delayed encoding")
			clock.Advance(2 * time.Second)
			close(releaseEncode)
			waitSignal(t, envelopeSampled, "envelope clock sample")
			clock.Advance(test.claimDelay)
			close(releaseFirst)
			if err := receiveError(t, firstDone); err != nil {
				t.Fatal(err)
			}
			if err := receiveError(t, explicitDone); err != nil {
				t.Fatal(err)
			}
			_, puts, _ := backend.calls()
			if puts != test.wantWrites {
				t.Fatalf("backend puts = %d, want %d", puts, test.wantWrites)
			}
			if test.wantWrites == 2 {
				expiryMu.Lock()
				got := explicitExpiry
				expiryMu.Unlock()
				if got.Mode != RelativeExpiry || got.RetainFor != test.wantTTL {
					t.Fatalf("explicit expiry = %+v, want %v", got, test.wantTTL)
				}
				result, err := instance.Lookup(context.Background(), "key")
				if err != nil || result.Value != "explicit" || result.State != Hit {
					t.Fatalf("final lookup = %+v, err = %v", result, err)
				}
			}
			assertQuiescent(t, instance)
		})
	}
}

func TestReviewServeOnLoaderErrorOnlyCatchesLoaderFailures(t *testing.T) {
	loaderErr := errors.New("loader failed")
	codecErr := errors.New("codec failed")
	backendErr := errors.New("backend put failed")
	tests := []struct {
		name      string
		configure func(*reviewClock, *coordinationBackend, *reviewCodec)
		load      func(*reviewClock) (LoadResult[string], error)
		wantError error
		wantStale bool
	}{
		{
			name: "loader failure",
			load: func(*reviewClock) (LoadResult[string], error) {
				return LoadResult[string]{}, loaderErr
			},
			wantStale: true,
		},
		{
			name: "invalid presence",
			load: func(*reviewClock) (LoadResult[string], error) {
				return LoadResult[string]{}, nil
			},
			wantError: ErrInvalid,
		},
		{
			name: "codec failure",
			configure: func(_ *reviewClock, _ *coordinationBackend, codec *reviewCodec) {
				codec.encode = func(value string, _ ValueLimit) ([]byte, error) {
					if value == "codec-error" {
						return nil, codecErr
					}
					return []byte(value), nil
				}
			},
			load: func(*reviewClock) (LoadResult[string], error) {
				return Present("codec-error"), nil
			},
			wantError: ErrInvalid,
		},
		{
			name: "backend put failure",
			configure: func(_ *reviewClock, backend *coordinationBackend, _ *reviewCodec) {
				backend.setPutHook(func(context.Context, Address, []byte, Expiry, int) error {
					return backendErr
				})
			},
			load: func(*reviewClock) (LoadResult[string], error) {
				return Present("new"), nil
			},
			wantError: ErrBackend,
		},
		{
			name: "runtime clock failure",
			load: func(clock *reviewClock) (LoadResult[string], error) {
				clock.PanicNow()
				return Present("new"), nil
			},
			wantError: ErrInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := newReviewClock()
			backend := newCoordinationBackend()
			codec := &reviewCodec{}
			policy := reviewPolicy()
			policy.Stale = ServeOnLoaderError
			instance := newReviewCache(t, clock, nil, backend, codec, policy)
			if err := instance.Put(context.Background(), "key", "stale"); err != nil {
				t.Fatal(err)
			}
			clock.Advance(90 * time.Minute)
			if test.configure != nil {
				test.configure(clock, backend, codec)
			}
			result, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[string], error) {
				return test.load(clock)
			})
			if test.wantStale {
				if err != nil || result.Value != "stale" || result.State != Stale {
					t.Fatalf("resolve = %+v, err = %v", result, err)
				}
			} else if !errors.Is(err, test.wantError) || result.State != 0 {
				t.Fatalf("resolve = %+v, err = %v, want %v", result, err, test.wantError)
			}
			assertQuiescent(t, instance)
		})
	}
}

func TestReviewPanickingRuntimeCallbacksReleaseCore(t *testing.T) {
	t.Run("clock now", func(t *testing.T) {
		clock := newReviewClock()
		instance := newReviewCache(t, clock, nil, newCoordinationBackend(), &reviewCodec{}, reviewPolicy())
		clock.PanicNow()
		if err := instance.Put(context.Background(), "key", "value"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("put error = %v", err)
		}
		assertQuiescent(t, instance)
	})
	t.Run("random", func(t *testing.T) {
		policy := reviewPolicy()
		policy.Jitter = SubtractUpTo(time.Second)
		instance := newReviewCache(t, newReviewClock(), reviewPanicRandom{}, newCoordinationBackend(), &reviewCodec{}, policy)
		if err := instance.Put(context.Background(), "key", "value"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("put error = %v", err)
		}
		assertQuiescent(t, instance)
	})
	t.Run("clock new timer", func(t *testing.T) {
		clock := newReviewClock()
		clock.SetTimers(func(time.Duration) Timer {
			panic("new timer")
		})
		instance := newReviewCache(t, clock, nil, newCoordinationBackend(), &reviewCodec{}, reviewPolicy())
		if _, err := instance.Lookup(context.Background(), "key"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("lookup error = %v", err)
		}
		assertQuiescent(t, instance)
	})
	t.Run("timer channel", func(t *testing.T) {
		clock := newReviewClock()
		clock.SetTimers(func(time.Duration) Timer {
			return &reviewPanicChannelTimer{}
		})
		instance := newReviewCache(t, clock, nil, newCoordinationBackend(), &reviewCodec{}, reviewPolicy())
		if _, err := instance.Lookup(context.Background(), "key"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("lookup error = %v", err)
		}
		assertQuiescent(t, instance)
	})
	t.Run("timer stop", func(t *testing.T) {
		clock := newReviewClock()
		stopped := make(chan struct{})
		clock.SetTimers(func(time.Duration) Timer {
			return &reviewTimer{channel: make(chan time.Time), stop: func() {
				close(stopped)
				panic("timer stop")
			}}
		})
		instance := newReviewCache(t, clock, nil, newCoordinationBackend(), &reviewCodec{}, reviewPolicy())
		result, err := instance.Lookup(context.Background(), "key")
		if err != nil || result.State != Miss {
			t.Fatalf("lookup = %+v, err = %v", result, err)
		}
		waitSignal(t, stopped, "panicking timer stop")
		assertQuiescent(t, instance)
	})
}

func TestReviewDisabledLookupManyStillEnforcesBounds(t *testing.T) {
	policy, err := Disabled.Build()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := New[string, string](Runtime{}, nil, Scope[string]{}, nil, nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, policy.MaxBatchKeys)
	results, err := instance.LookupMany(context.Background(), keys)
	if err != nil || len(results) != len(keys) {
		t.Fatalf("bounded lookup many results = %d, err = %v", len(results), err)
	}
	for index, result := range results {
		if result.State != Miss {
			t.Fatalf("result %d = %+v", index, result)
		}
	}
	results, err = instance.LookupMany(context.Background(), make([]string, policy.MaxBatchKeys+1))
	if !errors.Is(err, ErrTooLarge) || results != nil {
		t.Fatalf("oversized lookup many results = %#v, err = %v", results, err)
	}
}

func TestReviewDisabledLoaderPanicIsClassified(t *testing.T) {
	policy, err := Disabled.Build()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := New[string, string](Runtime{}, nil, Scope[string]{}, nil, nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	result, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[string], error) {
		panic("secret-disabled-loader-marker")
	})
	if !errors.Is(err, ErrLoader) || result != (Result[string]{}) {
		t.Fatalf("Resolve() result=%+v error=%v", result, err)
	}
	if strings.Contains(err.Error(), "secret-disabled-loader-marker") {
		t.Fatalf("Resolve() exposed panic payload: %v", err)
	}
}

type reviewDescribedBackend struct {
	*coordinationBackend
	description BackendDescription
}

func (backend *reviewDescribedBackend) DescribeBackend() BackendDescription {
	return backend.description
}

func reviewProcessBackend(name string) *reviewDescribedBackend {
	return &reviewDescribedBackend{
		coordinationBackend: newCoordinationBackend(),
		description: BackendDescription{
			Name:              name,
			Topology:          ProcessBackend,
			ExpiryClock:       ProcessExpiryClock,
			MaxItemBytes:      32 << 20,
			RelativeExpiry:    true,
			MaxRelativeExpiry: 24 * time.Hour,
			CapacityBounded:   true,
		},
	}
}

func reviewSharedBackend(name string) *reviewDescribedBackend {
	return &reviewDescribedBackend{
		coordinationBackend: newCoordinationBackend(),
		description: BackendDescription{
			Name:              name,
			Topology:          SharedBackend,
			ExpiryClock:       ServerExpiryClock,
			MaxItemBytes:      32 << 20,
			RelativeExpiry:    true,
			MaxRelativeExpiry: 24 * time.Hour,
		},
	}
}

func reviewDefinition(t *testing.T, name, purpose string, profile Profile, provider ProviderID) (*Cache[string, string], *Definition[string, string]) {
	t.Helper()
	target := Auto[string, string](profile)
	definition, err := Define(target, DefinitionSpec[string, string]{
		Name:      name,
		Namespace: NamespaceTemplate{Purpose: purpose, Generation: 1},
		Scope:     GlobalPlan[string](),
		Keys: MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) {
			return []byte(key), nil
		}),
		Values:   String(ValueSchema(1)),
		Provider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	return target, definition
}

func TestReviewActivationSupportsCrossKindAndPerProviderTopology(t *testing.T) {
	bound := 3 * time.Second
	sharedSkew, err := BoundedClockSkew(bound)
	if err != nil {
		t.Fatal(err)
	}
	memory, memoryDefinition := reviewDefinition(t, "memory-cache", "memory-values", Hot, "")
	shared, sharedDefinition := reviewDefinition(t, "shared-cache", "shared-values", Hot, "redis")
	err = Activate(context.Background(), ActivationSpec{
		Application: "review",
		Environment: "test",
		Runtime: Runtime{
			Clock:          newReviewClock(),
			LoaderTimeout:  time.Hour,
			BackendTimeout: time.Hour,
			CleanupTimeout: time.Hour,
		},
		Sets: []Set{MustSet(memoryDefinition, sharedDefinition)},
		Providers: []Provider{
			{ID: "memory", Resource: "process-resource", Kind: MemoryProviderKind, Backend: reviewProcessBackend("memory")},
			{ID: "redis", Resource: "shared-resource", Kind: RedisProviderKind, Backend: reviewSharedBackend("redis"), ClockSkew: sharedSkew},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	memoryDescriptor := memory.Describe()
	if memoryDescriptor.ProviderKind != MemoryProviderKind || memoryDescriptor.ProviderID != "memory" || memoryDescriptor.ResourceID != "process-resource" ||
		memoryDescriptor.ClockSkew.Mode != SingleProcessSkew || memoryDescriptor.ClockSkew.Bound != 0 {
		t.Fatalf("memory descriptor = %+v", memoryDescriptor)
	}
	sharedDescriptor := shared.Describe()
	if sharedDescriptor.Profile != Hot.Name() || sharedDescriptor.ProviderKind != RedisProviderKind || sharedDescriptor.ProviderID != "redis" ||
		sharedDescriptor.ResourceID != "shared-resource" || sharedDescriptor.ClockSkew.Mode != BoundedSharedSkew || sharedDescriptor.ClockSkew.Bound != bound {
		t.Fatalf("shared descriptor = %+v", sharedDescriptor)
	}
}

func TestReviewActivationRejectsClockTopologyMismatch(t *testing.T) {
	sharedSkew, err := BoundedClockSkew(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		backend Backend
		skew    SkewPolicy
	}{
		{name: "process with bounded skew", backend: reviewProcessBackend("process"), skew: sharedSkew},
		{name: "shared without bounded skew", backend: reviewSharedBackend("shared")},
		{name: "shared with process clock", backend: reviewSharedBackend("shared"), skew: SingleProcessClock()},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, definition := reviewDefinition(t, "topology-cache", "topology-values", Hot, "provider")
			err := Activate(context.Background(), ActivationSpec{
				Application: "review",
				Environment: "test",
				Sets:        []Set{MustSet(definition)},
				Providers: []Provider{{
					ID:        "provider",
					Kind:      RedisProviderKind,
					Backend:   test.backend,
					ClockSkew: test.skew,
				}},
			})
			if !errors.Is(err, ErrInvalid) || target.Describe().Activated {
				t.Fatalf("activation error = %v, descriptor = %+v", err, target.Describe())
			}
		})
	}
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	if _, err := New(Runtime{ClockSkew: sharedSkew}, reviewProcessBackend("process"), Global[string](MustNamespace("review", "test", "process-mismatch", 1)), keys, String(1), reviewPolicy()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("process New error = %v", err)
	}
	if _, err := New(Runtime{ClockSkew: SingleProcessClock()}, reviewSharedBackend("shared"), Global[string](MustNamespace("review", "test", "shared-mismatch", 1)), keys, String(1), reviewPolicy()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("shared New error = %v", err)
	}
}

func TestReviewActivationScopesNamespaceCollisionByResource(t *testing.T) {
	sharedSkew, err := BoundedClockSkew(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("same resource refused", func(t *testing.T) {
		first, firstDefinition := reviewDefinition(t, "first-cache", "same-values", Hot, "first-provider")
		second, secondDefinition := reviewDefinition(t, "second-cache", "same-values", Hot, "second-provider")
		err := Activate(context.Background(), ActivationSpec{
			Application: "review",
			Environment: "test",
			Sets:        []Set{MustSet(firstDefinition, secondDefinition)},
			Providers: []Provider{
				{ID: "first-provider", Resource: "shared-resource", Kind: RedisProviderKind, Backend: reviewSharedBackend("first"), ClockSkew: sharedSkew},
				{ID: "second-provider", Resource: "shared-resource", Kind: PostgreSQLProviderKind, Backend: reviewSharedBackend("second"), ClockSkew: sharedSkew},
			},
		})
		if !errors.Is(err, ErrInvalid) || !stringsContain(fmt.Sprint(err), "shares a physical namespace") {
			t.Fatalf("activation error = %v", err)
		}
		if first.Describe().Activated || second.Describe().Activated {
			t.Fatal("namespace collision published caches")
		}
	})
	t.Run("separate resources allowed", func(t *testing.T) {
		first, firstDefinition := reviewDefinition(t, "first-cache", "same-values", Hot, "first-provider")
		second, secondDefinition := reviewDefinition(t, "second-cache", "same-values", Hot, "second-provider")
		err := Activate(context.Background(), ActivationSpec{
			Application: "review",
			Environment: "test",
			Sets:        []Set{MustSet(firstDefinition, secondDefinition)},
			Providers: []Provider{
				{ID: "first-provider", Resource: "first-resource", Kind: RedisProviderKind, Backend: reviewSharedBackend("first"), ClockSkew: sharedSkew},
				{ID: "second-provider", Resource: "second-resource", Kind: PostgreSQLProviderKind, Backend: reviewSharedBackend("second"), ClockSkew: sharedSkew},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !first.Describe().Activated || !second.Describe().Activated {
			t.Fatalf("descriptors = %+v, %+v", first.Describe(), second.Describe())
		}
	})
}

func stringsContain(value, fragment string) bool {
	return len(fragment) <= len(value) && bytes.Contains([]byte(value), []byte(fragment))
}

type reviewLimitBatchBackend struct {
	description BackendDescription
}

func (backend *reviewLimitBatchBackend) DescribeBackend() BackendDescription {
	return backend.description
}

func (*reviewLimitBatchBackend) Get(context.Context, Address, ReadLimit) ([]byte, bool, error) {
	return nil, false, nil
}

func (*reviewLimitBatchBackend) Put(context.Context, Address, []byte, Expiry) error {
	return nil
}

func (*reviewLimitBatchBackend) Delete(context.Context, Address) error {
	return nil
}

func (*reviewLimitBatchBackend) GetMany(context.Context, []Address, BatchReadLimit) (map[Address][]byte, error) {
	return nil, fmt.Errorf("driver limit: %w", ErrTooLarge)
}

type reviewObserver struct {
	mu     sync.Mutex
	events []Event
}

type blockingLoadObserver struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (observer *blockingLoadObserver) Observe(_ context.Context, event Event) {
	if event.Operation != LoadOperation {
		return
	}
	observer.once.Do(func() { close(observer.entered) })
	<-observer.release
}

func (observer *reviewObserver) Observe(_ context.Context, event Event) {
	observer.mu.Lock()
	observer.events = append(observer.events, event)
	observer.mu.Unlock()
}

func (observer *reviewObserver) Last() Event {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.events) == 0 {
		return Event{}
	}
	return observer.events[len(observer.events)-1]
}

func (observer *reviewObserver) Count(operation Operation, outcome Outcome) int {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	count := 0
	for _, event := range observer.events {
		if event.Operation == operation && event.Outcome == outcome {
			count++
		}
	}
	return count
}

func TestReviewBlockingObserverConsumesAFlightSlot(t *testing.T) {
	observer := &blockingLoadObserver{entered: make(chan struct{}), release: make(chan struct{})}
	released := false
	defer func() {
		if !released {
			close(observer.release)
		}
	}()
	policy := reviewPolicy()
	policy.MaxFlights = 1
	policy.FlightSaturation = Reject()
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(Runtime{
		Clock:          newReviewClock(),
		Observer:       observer,
		LoaderTimeout:  time.Hour,
		BackendTimeout: time.Hour,
		CleanupTimeout: time.Hour,
	}, newCoordinationBackend(), Global[string](MustNamespace("review", "test", "observer-flight", 1)), keys, String(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	first := resolveAsync(context.Background(), instance, "first", func(context.Context, string) (LoadResult[string], error) {
		return Present("value"), nil
	})
	waitSignal(t, observer.entered, "blocking load observer")
	assertStringResult(t, receiveResolve(t, first), "value", Loaded)
	var secondCalls atomic.Int32
	second, err := instance.Resolve(context.Background(), "second", func(context.Context, string) (LoadResult[string], error) {
		secondCalls.Add(1)
		return Present("second"), nil
	})
	if !errors.Is(err, ErrSaturated) || second != (Result[string]{}) || secondCalls.Load() != 0 {
		t.Fatalf("second result=%+v error=%v loader calls=%d", second, err, secondCalls.Load())
	}
	close(observer.release)
	released = true
	waitCacheQuiescent(t, instance)
}

func TestReviewBatchReaderTooLargeStaysLimitUnderCorruptAsMiss(t *testing.T) {
	policy := reviewPolicy()
	policy.Corruption = CorruptAsMiss
	policy.ReadFailure = AsMiss
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatal(err)
	}
	backend := &reviewLimitBatchBackend{description: BackendDescription{
		Name:              "limit-batch",
		Topology:          ProcessBackend,
		ExpiryClock:       ProcessExpiryClock,
		MaxItemBytes:      maximum,
		RelativeExpiry:    true,
		MaxRelativeExpiry: 24 * time.Hour,
	}}
	observer := &reviewObserver{}
	runtime := Runtime{
		Clock:          newReviewClock(),
		Observer:       observer,
		LoaderTimeout:  time.Hour,
		BackendTimeout: time.Hour,
		CleanupTimeout: time.Hour,
	}
	keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
	instance, err := New(runtime, backend, Global[string](MustNamespace("review", "test", "batch-limit", 1)), keys, String(1), policy)
	if err != nil {
		t.Fatal(err)
	}
	results, err := instance.LookupMany(context.Background(), []string{"key"})
	if !errors.Is(err, ErrTooLarge) || results != nil {
		t.Fatalf("lookup many results = %#v, err = %v", results, err)
	}
	event := observer.Last()
	if event.Operation != LookupManyOperation || event.Outcome != ErrorOutcome || event.Reason != LimitReason {
		t.Fatalf("event = %+v", event)
	}
}

type reviewSnapshotBatchBackend struct {
	*coordinationBackend
	batchCalls int
}

func (backend *reviewSnapshotBatchBackend) GetMany(ctx context.Context, addresses []Address, _ BatchReadLimit) (map[Address][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.batchCalls++
	result := make(map[Address][]byte, len(addresses))
	for _, address := range addresses {
		if value, found := backend.values[address]; found {
			result[address] = bytes.Clone(value)
		}
	}
	return result, nil
}

func (backend *reviewSnapshotBatchBackend) BatchCalls() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.batchCalls
}

type reviewBatchOutcome struct {
	results []Result[string]
	err     error
}

func TestReviewLookupManyDiscardsDecodeObsoletedByMutation(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutate    func(*Cache[string, string]) error
		wantValue string
		wantState State
	}{
		{
			name: "put",
			mutate: func(instance *Cache[string, string]) error {
				return instance.Put(context.Background(), "key", "new")
			},
			wantValue: "new",
			wantState: Hit,
		},
		{
			name: "forget",
			mutate: func(instance *Cache[string, string]) error {
				return instance.Forget(context.Background(), "key")
			},
			wantState: Miss,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			decodeEntered := make(chan struct{})
			releaseDecode := make(chan struct{})
			var decodeMu sync.Mutex
			decodeCalls := 0
			codec := &reviewCodec{}
			codec.decode = func(value []byte, limit ValueLimit) (string, error) {
				decodeMu.Lock()
				decodeCalls++
				call := decodeCalls
				decodeMu.Unlock()
				if call == 1 {
					close(decodeEntered)
					<-releaseDecode
				}
				if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
					return "", ErrTooLarge
				}
				return string(bytes.Clone(value)), nil
			}
			backend := &reviewSnapshotBatchBackend{coordinationBackend: newCoordinationBackend()}
			observer := &reviewObserver{}
			clock := newReviewClock()
			keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
			instance, err := New(Runtime{
				Clock:          clock,
				Observer:       observer,
				LoaderTimeout:  time.Hour,
				BackendTimeout: time.Hour,
				CleanupTimeout: time.Hour,
			}, backend, Global[string](MustNamespace("review", "test", "batch-obsolete-"+test.name, 1)), keys, codec, reviewPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if err := instance.Put(context.Background(), "key", "old"); err != nil {
				t.Fatal(err)
			}
			lookupDone := make(chan reviewBatchOutcome, 1)
			go func() {
				results, err := instance.LookupMany(context.Background(), []string{"key"})
				lookupDone <- reviewBatchOutcome{results: results, err: err}
			}()
			waitSignal(t, decodeEntered, "batch decode")
			mutationDone := make(chan error, 1)
			go func() {
				mutationDone <- test.mutate(instance)
			}()
			if err := receiveError(t, mutationDone); err != nil {
				t.Fatal(err)
			}
			close(releaseDecode)
			var outcome reviewBatchOutcome
			select {
			case outcome = <-lookupDone:
			case <-time.After(coordinationTestTimeout):
				t.Fatal("lookup many did not finish")
			}
			if outcome.err != nil || len(outcome.results) != 1 || outcome.results[0].State != test.wantState || outcome.results[0].Value != test.wantValue {
				t.Fatalf("lookup many = %#v, err = %v", outcome.results, outcome.err)
			}
			if calls := backend.BatchCalls(); calls != 2 {
				t.Fatalf("batch reads = %d", calls)
			}
			if completes := observer.Count(LookupManyOperation, CompleteOutcome); completes != 1 {
				t.Fatalf("lookup many complete events = %d", completes)
			}
			assertQuiescent(t, instance)
		})
	}
}

func TestReviewServeWhileRefreshingHonorsGlobalSaturationPolicy(t *testing.T) {
	for _, test := range []struct {
		name      string
		policy    FlightSaturationPolicy
		wantError error
		wantStale bool
		wantWait  bool
	}{
		{name: "reject", policy: Reject(), wantError: ErrSaturated},
		{name: "serve stale", policy: ServeStale(), wantStale: true},
		{name: "wait bounded", policy: WaitBounded(time.Hour), wantStale: true, wantWait: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newReviewClock()
			policy := reviewPolicy()
			policy.MaxFlights = 1
			policy.Stale = ServeWhileRefreshing
			policy.FlightSaturation = test.policy
			instance := newReviewCache(t, clock, nil, newCoordinationBackend(), &reviewCodec{}, policy)
			if err := instance.Put(context.Background(), "first", "old-first"); err != nil {
				t.Fatal(err)
			}
			if err := instance.Put(context.Background(), "second", "old-second"); err != nil {
				t.Fatal(err)
			}
			clock.Advance(90 * time.Minute)
			firstEntered := make(chan struct{})
			releaseFirst := make(chan struct{})
			first, err := instance.Resolve(context.Background(), "first", func(context.Context, string) (LoadResult[string], error) {
				close(firstEntered)
				<-releaseFirst
				return Present("new-first"), nil
			})
			if err != nil || first.Value != "old-first" || first.State != Stale {
				t.Fatalf("first resolve = %+v, err = %v", first, err)
			}
			waitSignal(t, firstEntered, "first background refresh")
			secondEntered := make(chan struct{})
			secondDone := resolveAsync(context.Background(), instance, "second", func(context.Context, string) (LoadResult[string], error) {
				close(secondEntered)
				return Present("new-second"), nil
			})
			if test.wantWait {
				select {
				case outcome := <-secondDone:
					t.Fatalf("wait bounded returned before capacity: %+v, err = %v", outcome.result, outcome.err)
				default:
				}
			}
			if !test.wantWait {
				outcome := receiveResolve(t, secondDone)
				if test.wantError != nil {
					if !errors.Is(outcome.err, test.wantError) || outcome.result.State != 0 {
						t.Fatalf("second resolve = %+v, err = %v", outcome.result, outcome.err)
					}
				} else if outcome.err != nil || outcome.result.Value != "old-second" || outcome.result.State != Stale {
					t.Fatalf("second resolve = %+v, err = %v", outcome.result, outcome.err)
				}
				assertNotSignaled(t, secondEntered, "second loader")
			}
			close(releaseFirst)
			if test.wantWait {
				waitSignal(t, secondEntered, "second background refresh")
				outcome := receiveResolve(t, secondDone)
				if outcome.err != nil || outcome.result.Value != "old-second" || outcome.result.State != Stale {
					t.Fatalf("second resolve = %+v, err = %v", outcome.result, outcome.err)
				}
			}
			waitCacheQuiescent(t, instance)
			assertQuiescent(t, instance)
		})
	}
}

type reviewBackendWrapper struct {
	backend   Backend
	next      Backend
	panicNext bool
}

func (wrapper *reviewBackendWrapper) Get(ctx context.Context, address Address, limit ReadLimit) ([]byte, bool, error) {
	return wrapper.backend.Get(ctx, address, limit)
}

func (wrapper *reviewBackendWrapper) Put(ctx context.Context, address Address, value []byte, expiry Expiry) error {
	return wrapper.backend.Put(ctx, address, value, expiry)
}

func (wrapper *reviewBackendWrapper) Delete(ctx context.Context, address Address) error {
	return wrapper.backend.Delete(ctx, address)
}

func (wrapper *reviewBackendWrapper) Next() Backend {
	if wrapper.panicNext {
		panic("next")
	}
	return wrapper.next
}

func reviewRequiredBatchDefinition(t *testing.T, provider ProviderID) (*Cache[string, string], *Definition[string, string]) {
	t.Helper()
	target := Auto[string, string](Hot)
	definition, err := Define(target, DefinitionSpec[string, string]{
		Name:      "required-batch",
		Namespace: NamespaceTemplate{Purpose: "required-batch-values", Generation: 1},
		Scope:     GlobalPlan[string](),
		Keys: MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) {
			return []byte(key), nil
		}),
		Values:   String(1),
		Provider: provider,
		Requires: []Capability{BatchReadCapability},
	})
	if err != nil {
		t.Fatal(err)
	}
	return target, definition
}

func TestReviewBackendWrapperTraversalIsBoundedAndPanicSafe(t *testing.T) {
	base := &reviewLimitBatchBackend{description: reviewProcessBackend("wrapped").description}
	inner := &reviewBackendWrapper{backend: base, next: base}
	outer := &reviewBackendWrapper{backend: inner, next: inner}
	if description, ok := BackendDescriptionOf(outer); !ok || description.Name != "wrapped" {
		t.Fatalf("description = %+v, ok = %t", description, ok)
	}
	if reader, ok := BatchReaderOf(outer); !ok || reader != base || !Supports(outer, BatchReadCapability) {
		t.Fatalf("batch reader = %T, ok = %t", reader, ok)
	}
	first := &reviewBackendWrapper{backend: base}
	second := &reviewBackendWrapper{backend: base}
	first.next = second
	second.next = first
	if _, ok := BackendDescriptionOf(first); ok {
		t.Fatal("cycle exposed a backend description")
	}
	if _, ok := BatchReaderOf(first); ok || Supports(first, BatchReadCapability) {
		t.Fatal("cycle exposed a batch capability")
	}
	panicking := &reviewBackendWrapper{backend: base, panicNext: true}
	if _, ok := BackendDescriptionOf(panicking); ok {
		t.Fatal("panicking wrapper exposed a backend description")
	}
	if _, ok := BatchReaderOf(panicking); ok || Supports(panicking, BatchReadCapability) {
		t.Fatal("panicking wrapper exposed a batch capability")
	}
}

func TestReviewRequiredBatchCapabilityFailsOrActivatesAtBoot(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		target, definition := reviewRequiredBatchDefinition(t, "provider")
		backend := reviewProcessBackend("without-batch")
		err := Activate(context.Background(), ActivationSpec{
			Application: "review",
			Environment: "test",
			Sets:        []Set{MustSet(definition)},
			Providers: []Provider{{
				ID:      "provider",
				Kind:    MemoryProviderKind,
				Backend: backend,
			}},
		})
		if !errors.Is(err, ErrInvalid) || !stringsContain(fmt.Sprint(err), "lacks capability") || target.Describe().Activated {
			t.Fatalf("activation error = %v, descriptor = %+v", err, target.Describe())
		}
	})
	t.Run("wrapped", func(t *testing.T) {
		target, definition := reviewRequiredBatchDefinition(t, "provider")
		base := &reviewLimitBatchBackend{description: reviewProcessBackend("with-batch").description}
		wrapper := &reviewBackendWrapper{backend: base, next: base}
		err := Activate(context.Background(), ActivationSpec{
			Application: "review",
			Environment: "test",
			Sets:        []Set{MustSet(definition)},
			Providers: []Provider{{
				ID:      "provider",
				Kind:    MemoryProviderKind,
				Backend: wrapper,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if descriptor := target.Describe(); !descriptor.Activated || len(descriptor.Requires) != 1 || descriptor.Requires[0] != BatchReadCapability {
			t.Fatalf("descriptor = %+v", descriptor)
		}
	})
}

func TestReviewDependencyErrorsNeverExposeSecretMaterial(t *testing.T) {
	const secret = "review-secret-marker-7d64ce"
	assertSafe := func(t *testing.T, err error, kind error) {
		t.Helper()
		if !errors.Is(err, kind) || stringsContain(fmt.Sprint(err), secret) {
			t.Fatalf("error = %q, want %v without marker", err, kind)
		}
	}
	t.Run("key codec", func(t *testing.T) {
		keys := MustKeyFunc(KeyVersion(1), func(string, KeyLimit) ([]byte, error) {
			return nil, errors.New(secret)
		})
		instance, err := New(Runtime{}, newCoordinationBackend(), Global[string](MustNamespace("review", "test", "secret-key", 1)), keys, String(1), reviewPolicy())
		if err != nil {
			t.Fatal(err)
		}
		_, err = instance.Lookup(context.Background(), "key")
		assertSafe(t, err, ErrInvalid)
	})
	t.Run("partitioner", func(t *testing.T) {
		keys := MustKeyFunc(KeyVersion(1), func(key string, _ KeyLimit) ([]byte, error) { return []byte(key), nil })
		scope := Partitioned(MustNamespace("review", "test", "secret-partition", 1), func(string, KeyLimit) ([]byte, error) {
			return nil, errors.New(secret)
		})
		instance, err := New(Runtime{}, newCoordinationBackend(), scope, keys, String(1), reviewPolicy())
		if err != nil {
			t.Fatal(err)
		}
		_, err = instance.Lookup(context.Background(), "key")
		assertSafe(t, err, ErrInvalid)
	})
	t.Run("codec encode", func(t *testing.T) {
		codec := &reviewCodec{encode: func(string, ValueLimit) ([]byte, error) { return nil, errors.New(secret) }}
		instance := newReviewCache(t, newReviewClock(), nil, newCoordinationBackend(), codec, reviewPolicy())
		assertSafe(t, instance.Put(context.Background(), "key", "value"), ErrInvalid)
	})
	t.Run("codec decode", func(t *testing.T) {
		codec := &reviewCodec{}
		instance := newReviewCache(t, newReviewClock(), nil, newCoordinationBackend(), codec, reviewPolicy())
		if err := instance.Put(context.Background(), "key", "value"); err != nil {
			t.Fatal(err)
		}
		codec.decode = func([]byte, ValueLimit) (string, error) { return "", errors.New(secret) }
		_, err := instance.Lookup(context.Background(), "key")
		assertSafe(t, err, ErrCorrupt)
	})
	t.Run("backend get", func(t *testing.T) {
		backend := newCoordinationBackend()
		backend.getHook = func(context.Context, Address, ReadLimit, int) ([]byte, bool, error) {
			return nil, false, errors.New(secret)
		}
		instance := newReviewCache(t, newReviewClock(), nil, backend, &reviewCodec{}, reviewPolicy())
		_, err := instance.Lookup(context.Background(), "key")
		assertSafe(t, err, ErrBackend)
	})
	t.Run("backend put", func(t *testing.T) {
		backend := newCoordinationBackend()
		backend.setPutHook(func(context.Context, Address, []byte, Expiry, int) error { return errors.New(secret) })
		instance := newReviewCache(t, newReviewClock(), nil, backend, &reviewCodec{}, reviewPolicy())
		assertSafe(t, instance.Put(context.Background(), "key", "value"), ErrBackend)
	})
}

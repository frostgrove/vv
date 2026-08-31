package cache

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type policyTestClock struct {
	now time.Time
}

func (this *policyTestClock) Now() time.Time {
	return this.now
}

func (this *policyTestClock) NewTimer(time.Duration) Timer {
	return policyTestTimer{channel: make(chan time.Time)}
}

type policyTestTimer struct {
	channel chan time.Time
}

func (this policyTestTimer) C() <-chan time.Time {
	return this.channel
}

func (policyTestTimer) Stop() bool {
	return true
}

type policyTestRandom struct {
	value uint64
}

func (this policyTestRandom) Uint64() uint64 {
	return this.value
}

type policyTestCodec struct {
	encode func([]byte, ValueLimit) ([]byte, error)
	decode func([]byte, ValueLimit) ([]byte, error)
}

func (policyTestCodec) ID() string {
	return "policy-test"
}

func (policyTestCodec) Schema() ValueSchema {
	return 1
}

func (this policyTestCodec) Encode(value []byte, limit ValueLimit) ([]byte, error) {
	if this.encode != nil {
		return this.encode(value, limit)
	}
	return bytes.Clone(value), nil
}

func (this policyTestCodec) Decode(encoded []byte, limit ValueLimit) ([]byte, error) {
	if this.decode != nil {
		return this.decode(encoded, limit)
	}
	return bytes.Clone(encoded), nil
}

type capabilityTestBackend struct {
	description BackendDescription
}

func (this *capabilityTestBackend) DescribeBackend() BackendDescription {
	return this.description
}

func (*capabilityTestBackend) Get(context.Context, Address, ReadLimit) ([]byte, bool, error) {
	return nil, false, nil
}

func (*capabilityTestBackend) Put(context.Context, Address, []byte, Expiry) error {
	return nil
}

func (*capabilityTestBackend) Delete(context.Context, Address) error {
	return nil
}

func TestPolicyRejectsArithmeticOverflow(t *testing.T) {
	t.Run("fresh and stale duration", func(t *testing.T) {
		policy := newCacheTestPolicy(64)
		policy.Freshness = Expiring(time.Duration(math.MaxInt64), time.Nanosecond)
		policy.Retention = ExpireAfter(time.Duration(math.MaxInt64))
		if err := validatePolicy(policy); !errors.Is(err, ErrInvalid) {
			t.Fatalf("validatePolicy() error = %v", err)
		}
	})

	t.Run("maximum envelope size", func(t *testing.T) {
		policy := newCacheTestPolicy(64)
		policy.MaxValueBytes = math.MaxInt
		policy.MaxBatchResultBytes = math.MaxInt
		if _, err := maxEnvelopeBytes(policy); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("maxEnvelopeBytes() error = %v", err)
		}
		if err := validatePolicy(policy); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("validatePolicy() error = %v", err)
		}
	})

	t.Run("batch must fit envelope", func(t *testing.T) {
		policy := newCacheTestPolicy(64)
		policy.MaxBatchResultBytes = policy.MaxValueBytes
		if err := validatePolicy(policy); !errors.Is(err, ErrInvalid) {
			t.Fatalf("validatePolicy() error = %v", err)
		}
	})
}

func TestPolicyRejectsExplicitZeroAndNoOpModes(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Policy)
	}{
		{name: "zero negative duration", change: func(policy *Policy) { policy.Negative = CacheAbsenceFor(0) }},
		{name: "zero jitter duration", change: func(policy *Policy) { policy.Jitter = SubtractUpTo(0) }},
		{name: "always fresh jitter", change: func(policy *Policy) {
			policy.Freshness = AlwaysFresh("reviewed")
			policy.Jitter = SubtractUpTo(time.Second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := newCacheTestPolicy(64)
			test.change(&policy)
			if err := validatePolicy(policy); !errors.Is(err, ErrInvalid) {
				t.Fatalf("validatePolicy() error = %v", err)
			}
		})
	}
}

func TestEnvelopeRejectsUnrepresentableClock(t *testing.T) {
	policy := newCacheTestPolicy(64)
	codec := policyTestCodec{}
	descriptor := mustCodecDescriptor(t, codec)
	tests := []struct {
		name string
		now  time.Time
	}{
		{name: "outside envelope timestamp range", now: time.Date(2500, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{name: "deadline addition overflows", now: time.Unix(0, math.MaxInt64-int64(time.Minute)).UTC()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, payload, expiry, err := encodeEnvelope(
				newCacheTestRuntime(test.now),
				codec,
				descriptor,
				policy,
				Present([]byte("value")),
			)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("encodeEnvelope() error = %v", err)
			}
			if encoded != nil || payload != nil || expiry != (Expiry{}) {
				t.Fatalf("encodeEnvelope() returned data after rejecting the clock")
			}
		})
	}
}

func TestEnvelopeUsesCurrentPolicyCaps(t *testing.T) {
	started := time.Unix(1_900_000_000, 0).UTC()
	codec := policyTestCodec{}
	descriptor := mustCodecDescriptor(t, codec)
	storedPolicy := newCacheTestPolicy(64)
	storedPolicy.Freshness = Expiring(10*time.Minute, 10*time.Minute)
	storedPolicy.Retention = ExpireAfter(30 * time.Minute)
	storedPolicy.Negative = CacheAbsenceFor(10 * time.Minute)
	stored, _, _, err := encodeEnvelope(
		newCacheTestRuntime(started),
		codec,
		descriptor,
		storedPolicy,
		Present([]byte("value")),
	)
	if err != nil {
		t.Fatalf("encodeEnvelope() error = %v", err)
	}

	currentPolicy := newCacheTestPolicy(64)
	currentPolicy.Freshness = Expiring(2*time.Minute, 3*time.Minute)
	currentPolicy.Retention = ExpireAfter(5 * time.Minute)
	currentPolicy.Negative = CacheAbsenceFor(2 * time.Minute)
	tests := []struct {
		name  string
		now   time.Time
		state State
	}{
		{name: "last fresh instant", now: started.Add(2*time.Minute - time.Nanosecond), state: Hit},
		{name: "exact fresh boundary", now: started.Add(2 * time.Minute), state: Stale},
		{name: "last stale instant", now: started.Add(5*time.Minute - time.Nanosecond), state: Stale},
		{name: "exact retention boundary", now: started.Add(5 * time.Minute), state: Miss},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, _, err := decodeEnvelope(stored, newCacheTestRuntime(test.now), codec, descriptor, currentPolicy)
			if err != nil {
				t.Fatalf("decodeEnvelope() error = %v", err)
			}
			if result.State != test.state {
				t.Fatalf("state = %v, want %v", result.State, test.state)
			}
		})
	}

	negative, _, _, err := encodeEnvelope(
		newCacheTestRuntime(started),
		codec,
		descriptor,
		storedPolicy,
		Absent[[]byte](),
	)
	if err != nil {
		t.Fatalf("encode negative envelope: %v", err)
	}
	for _, test := range []struct {
		name      string
		policy    Policy
		now       time.Time
		wantState State
	}{
		{name: "shortened negative window", policy: currentPolicy, now: started.Add(2*time.Minute - time.Nanosecond), wantState: Negative},
		{name: "exact negative boundary", policy: currentPolicy, now: started.Add(2 * time.Minute), wantState: Miss},
		{name: "negative caching disabled", policy: withoutNegativeCaching(currentPolicy), now: started, wantState: Miss},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, _, err := decodeEnvelope(negative, newCacheTestRuntime(test.now), codec, descriptor, test.policy)
			if err != nil {
				t.Fatalf("decodeEnvelope() error = %v", err)
			}
			if result.State != test.wantState {
				t.Fatalf("state = %v, want %v", result.State, test.wantState)
			}
		})
	}
}

func TestEnvelopeClockSkewExactBoundaries(t *testing.T) {
	started := time.Unix(1_900_000_000, 0).UTC()
	bound := 5 * time.Second
	skew, err := BoundedClockSkew(bound)
	if err != nil {
		t.Fatalf("BoundedClockSkew() error = %v", err)
	}
	policy := newCacheTestPolicy(64)
	policy.Freshness = Expiring(10*time.Second, 10*time.Second)
	policy.Retention = ExpireAfter(20 * time.Second)
	policy.Negative = NoNegativeCaching()
	codec := policyTestCodec{}
	descriptor := mustCodecDescriptor(t, codec)
	stored, _, _, err := encodeEnvelope(newCacheTestRuntime(started), codec, descriptor, policy, Present([]byte("value")))
	if err != nil {
		t.Fatalf("encodeEnvelope() error = %v", err)
	}

	tests := []struct {
		name  string
		now   time.Time
		state State
	}{
		{name: "last fresh instant", now: started.Add(5*time.Second - time.Nanosecond), state: Hit},
		{name: "fresh boundary includes skew", now: started.Add(5 * time.Second), state: Stale},
		{name: "last stale instant", now: started.Add(15*time.Second - time.Nanosecond), state: Stale},
		{name: "retention boundary includes skew", now: started.Add(15 * time.Second), state: Miss},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := newCacheTestRuntime(test.now)
			runtime.ClockSkew = skew
			result, _, err := decodeEnvelope(stored, runtime, codec, descriptor, policy)
			if err != nil {
				t.Fatalf("decodeEnvelope() error = %v", err)
			}
			if result.State != test.state {
				t.Fatalf("state = %v, want %v", result.State, test.state)
			}
		})
	}

	t.Run("writer at exact skew bound", func(t *testing.T) {
		future, _, _, err := encodeEnvelope(
			newCacheTestRuntime(started.Add(bound)),
			codec,
			descriptor,
			policy,
			Present([]byte("value")),
		)
		if err != nil {
			t.Fatalf("encodeEnvelope() error = %v", err)
		}
		runtime := newCacheTestRuntime(started)
		runtime.ClockSkew = skew
		result, _, err := decodeEnvelope(future, runtime, codec, descriptor, policy)
		if err != nil {
			t.Fatalf("decodeEnvelope() error = %v", err)
		}
		if result.State != Hit {
			t.Fatalf("state = %v, want %v", result.State, Hit)
		}
	})

	t.Run("writer beyond skew bound", func(t *testing.T) {
		future, _, _, err := encodeEnvelope(
			newCacheTestRuntime(started.Add(bound+time.Nanosecond)),
			codec,
			descriptor,
			policy,
			Present([]byte("value")),
		)
		if err != nil {
			t.Fatalf("encodeEnvelope() error = %v", err)
		}
		runtime := newCacheTestRuntime(started)
		runtime.ClockSkew = skew
		if _, _, err := decodeEnvelope(future, runtime, codec, descriptor, policy); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("decodeEnvelope() error = %v", err)
		}
	})
}

func TestEnvelopeContainsCustomCodecFailures(t *testing.T) {
	started := time.Unix(1_900_000_000, 0).UTC()
	policy := newCacheTestPolicy(4)
	runtime := newCacheTestRuntime(started)

	t.Run("encoded output over limit", func(t *testing.T) {
		codec := policyTestCodec{encode: func([]byte, ValueLimit) ([]byte, error) {
			return make([]byte, policy.MaxValueBytes+1), nil
		}}
		descriptor := mustCodecDescriptor(t, codec)
		if _, _, _, err := encodeEnvelope(runtime, codec, descriptor, policy, Present([]byte("x"))); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("encodeEnvelope() error = %v", err)
		}
	})

	t.Run("encode panic", func(t *testing.T) {
		codec := policyTestCodec{encode: func([]byte, ValueLimit) ([]byte, error) {
			panic("encode")
		}}
		descriptor := mustCodecDescriptor(t, codec)
		if _, _, _, err := encodeEnvelope(runtime, codec, descriptor, policy, Present([]byte("x"))); err == nil {
			t.Fatal("encodeEnvelope() error = nil")
		}
	})

	t.Run("decode panic", func(t *testing.T) {
		codec := policyTestCodec{decode: func([]byte, ValueLimit) ([]byte, error) {
			panic("decode")
		}}
		descriptor := mustCodecDescriptor(t, codec)
		stored, _, _, err := encodeEnvelope(runtime, codec, descriptor, policy, Present([]byte("x")))
		if err != nil {
			t.Fatalf("encodeEnvelope() error = %v", err)
		}
		if _, _, err := decodeEnvelope(stored, runtime, codec, descriptor, policy); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("decodeEnvelope() error = %v", err)
		}
	})

	t.Run("decode limit error", func(t *testing.T) {
		codec := policyTestCodec{decode: func([]byte, ValueLimit) ([]byte, error) {
			return nil, ErrTooLarge
		}}
		descriptor := mustCodecDescriptor(t, codec)
		stored, _, _, err := encodeEnvelope(runtime, codec, descriptor, policy, Present([]byte("x")))
		if err != nil {
			t.Fatalf("encodeEnvelope() error = %v", err)
		}
		if _, _, err := decodeEnvelope(stored, runtime, codec, descriptor, policy); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("decodeEnvelope() error = %v", err)
		}
	})
}

func TestPhysicalExpiryModes(t *testing.T) {
	started := time.Unix(1_900_000_000, 0).UTC()
	codec := policyTestCodec{}
	descriptor := mustCodecDescriptor(t, codec)
	policy := newCacheTestPolicy(64)
	policy.Retention = ExpireAfter(20 * time.Minute)
	policy.Negative = CacheAbsenceFor(3 * time.Minute)

	_, _, foundExpiry, err := encodeEnvelope(newCacheTestRuntime(started), codec, descriptor, policy, Present([]byte("value")))
	if err != nil {
		t.Fatalf("encode found envelope: %v", err)
	}
	if foundExpiry.Mode != RelativeExpiry || foundExpiry.RetainFor != 20*time.Minute || !foundExpiry.deadline.Equal(started.Add(20*time.Minute)) {
		t.Fatalf("found expiry = %+v", foundExpiry)
	}
	if err := validExpiry(foundExpiry); err != nil {
		t.Fatalf("validExpiry(found) error = %v", err)
	}

	_, _, negativeExpiry, err := encodeEnvelope(newCacheTestRuntime(started), codec, descriptor, policy, Absent[[]byte]())
	if err != nil {
		t.Fatalf("encode negative envelope: %v", err)
	}
	if negativeExpiry.Mode != RelativeExpiry || negativeExpiry.RetainFor != 3*time.Minute || !negativeExpiry.deadline.Equal(started.Add(3*time.Minute)) {
		t.Fatalf("negative expiry = %+v", negativeExpiry)
	}

	capacityPolicy := policy
	capacityPolicy.Retention = CapacityBoundedRetention()
	capacityPolicy.Negative = NoNegativeCaching()
	_, _, capacityExpiry, err := encodeEnvelope(newCacheTestRuntime(started), codec, descriptor, capacityPolicy, Present([]byte("value")))
	if err != nil {
		t.Fatalf("encode capacity envelope: %v", err)
	}
	if capacityExpiry != (Expiry{Mode: CapacityOnlyExpiry}) {
		t.Fatalf("capacity expiry = %+v", capacityExpiry)
	}
}

func TestBackendCapabilityMatrix(t *testing.T) {
	finite := newCacheTestPolicy(64)
	capacity := finite
	capacity.Retention = CapacityBoundedRetention()
	capacity.Negative = NoNegativeCaching()
	capacityNegative := capacity
	capacityNegative.Negative = CacheAbsenceFor(2 * time.Minute)
	processRelative := BackendDescription{
		Name:              "process-relative",
		Topology:          ProcessBackend,
		ExpiryClock:       ProcessExpiryClock,
		MaxItemBytes:      1 << 20,
		RelativeExpiry:    true,
		MaxRelativeExpiry: 24 * time.Hour,
		CapacityBounded:   true,
	}
	sharedRelative := processRelative
	sharedRelative.Name = "shared-relative"
	sharedRelative.Topology = SharedBackend
	sharedRelative.ExpiryClock = ServerExpiryClock
	processCapacity := processRelative
	processCapacity.Name = "process-capacity"
	processCapacity.RelativeExpiry = false
	processCapacity.MaxRelativeExpiry = 0
	sharedCapacity := processCapacity
	sharedCapacity.Name = "shared-capacity"
	sharedCapacity.Topology = SharedBackend
	sharedCapacity.ExpiryClock = ServerExpiryClock
	boundedSkew, err := BoundedClockSkew(2 * time.Second)
	if err != nil {
		t.Fatalf("BoundedClockSkew() error = %v", err)
	}
	processRuntime := newCacheTestRuntime(time.Unix(1_900_000_000, 0).UTC())
	sharedRuntime := processRuntime
	sharedRuntime.ClockSkew = boundedSkew

	tests := []struct {
		name        string
		description BackendDescription
		policy      Policy
		runtime     Runtime
		wantError   error
	}{
		{name: "process relative", description: processRelative, policy: finite, runtime: processRuntime},
		{name: "shared relative", description: sharedRelative, policy: finite, runtime: sharedRuntime},
		{name: "process capacity", description: processCapacity, policy: capacity, runtime: processRuntime},
		{name: "shared needs explicit skew", description: sharedRelative, policy: finite, runtime: processRuntime, wantError: ErrInvalid},
		{name: "process refuses shared skew", description: processRelative, policy: finite, runtime: sharedRuntime, wantError: ErrInvalid},
		{name: "shared capacity rejected", description: sharedCapacity, policy: capacity, runtime: sharedRuntime, wantError: ErrInvalid},
		{name: "finite needs relative expiry", description: processCapacity, policy: finite, runtime: processRuntime, wantError: ErrInvalid},
		{name: "negative needs relative expiry", description: processCapacity, policy: capacityNegative, runtime: processRuntime, wantError: ErrInvalid},
		{
			name: "retention exceeds expiry range",
			description: func() BackendDescription {
				value := processRelative
				value.MaxRelativeExpiry = time.Minute
				return value
			}(),
			policy: finite, runtime: processRuntime, wantError: ErrInvalid,
		},
		{
			name: "negative exceeds expiry range",
			description: func() BackendDescription {
				value := processRelative
				value.MaxRelativeExpiry = time.Minute
				return value
			}(),
			policy: capacityNegative, runtime: processRuntime, wantError: ErrInvalid,
		},
		{
			name: "topology and clock disagree",
			description: func() BackendDescription {
				value := processRelative
				value.ExpiryClock = ServerExpiryClock
				return value
			}(),
			policy: finite, runtime: processRuntime, wantError: ErrInvalid,
		},
		{
			name: "item limit too small",
			description: func() BackendDescription {
				value := processRelative
				value.MaxItemBytes = 1
				return value
			}(),
			policy: finite, runtime: processRuntime, wantError: ErrTooLarge,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &capabilityTestBackend{description: test.description}
			cache, err := New(
				test.runtime,
				backend,
				Global[string](MustNamespace("app", "test", "capability", 1)),
				cacheTestKeyCodec(),
				Bytes(1),
				test.policy,
			)
			if test.wantError == nil {
				if err != nil {
					t.Fatalf("New() error = %v", err)
				}
				if cache == nil {
					t.Fatal("New() cache = nil")
				}
				return
			}
			if !errors.Is(err, test.wantError) {
				t.Fatalf("New() error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func newCacheTestPolicy(maxValueBytes int) Policy {
	policy := hotDefaults()
	policy.Freshness = Expiring(10*time.Minute, 5*time.Minute)
	policy.Retention = ExpireAfter(20 * time.Minute)
	policy.Negative = CacheAbsenceFor(2 * time.Minute)
	policy.Jitter = NoJitter()
	policy.MaxKeyBytes = 128
	policy.MaxValueBytes = maxValueBytes
	policy.MaxValueDepth = 16
	policy.MaxBatchKeys = 16
	policy.MaxBatchKeyBytes = 2 << 10
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		panic(err)
	}
	policy.MaxBatchResultBytes = maximum
	policy.ReadFailure = Propagate
	policy.WriteFailure = Propagate
	policy.InvalidateFailure = Propagate
	policy.Corruption = RefuseCorrupt
	return policy
}

func newCacheTestRuntime(now time.Time) Runtime {
	return Runtime{
		Clock:     &policyTestClock{now: now},
		ClockSkew: SingleProcessClock(),
		Random:    policyTestRandom{},
	}
}

func cacheTestKeyCodec() KeyCodec[string] {
	return MustKeyFunc(1, func(key string, limit KeyLimit) ([]byte, error) {
		if len(key) == 0 || len(key) > limit.MaxBytes {
			return nil, ErrTooLarge
		}
		return []byte(key), nil
	})
}

func mustCodecDescriptor[V any](t *testing.T, codec Codec[V]) codecDescriptor {
	t.Helper()
	descriptor, err := describeCodec(codec)
	if err != nil {
		t.Fatalf("describeCodec() error = %v", err)
	}
	return descriptor
}

func withoutNegativeCaching(policy Policy) Policy {
	policy.Negative = NoNegativeCaching()
	return policy
}

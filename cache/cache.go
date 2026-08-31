package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const defaultLoaderTimeout = 30 * time.Second

type Cache[K, V any] struct {
	inner        atomic.Pointer[cacheCore[K, V]]
	automatic    atomic.Pointer[automaticDeclaration]
	definition   atomic.Pointer[Definition[K, V]]
	activationMu sync.Mutex
}

type cacheIdentity struct {
	marker byte
}

type cacheCore[K, V any] struct {
	runtime            Runtime
	backend            Backend
	scope              Scope[K]
	keys               KeyCodec[K]
	keyVersion         KeyVersion
	values             Codec[V]
	valueDescriptor    codecDescriptor
	policy             Policy
	name               string
	maxEnvelopeBytes   int
	backendDescription BackendDescription
	identity           *cacheIdentity
	providerKind       ProviderKind
	providerID         ProviderID
	resourceID         ResourceID
	requires           []Capability
	activation         *activationGate

	coord coordination
}

type coordination struct {
	mu              sync.Mutex
	states          map[Address]*addressState
	activeFlights   int
	activeWrites    int
	capacityChanged chan struct{}
}

type addressState struct {
	refs           int
	generation     uint64
	stagedMutation uint64
	writeActive    bool
	invalidating   bool
	changed        chan struct{}
	member         *flightMember
}

func New[K, V any](runtime Runtime, backend Backend, scope Scope[K], keys KeyCodec[K], values Codec[V], policy Policy) (*Cache[K, V], error) {
	normalizedPolicy, err := normalizePolicy(policy)
	if err != nil {
		return nil, err
	}
	runtime, err = normalizeRuntime(runtime, defaultLoaderTimeout)
	if err != nil {
		return nil, err
	}
	core := &cacheCore[K, V]{runtime: runtime, policy: normalizedPolicy, identity: &cacheIdentity{}}
	core.coord.states = make(map[Address]*addressState)
	core.coord.capacityChanged = make(chan struct{})
	if !normalizedPolicy.disabled {
		if err := core.configure(backend, scope, keys, values); err != nil {
			return nil, err
		}
	} else if core.runtime.ClockSkew.mode == 0 {
		core.runtime.ClockSkew = SingleProcessClock()
	}
	result := &Cache[K, V]{}
	result.inner.Store(core)
	return result, nil
}

func (this *cacheCore[K, V]) configure(backend Backend, scope Scope[K], keys KeyCodec[K], values Codec[V]) error {
	if nilInterface(backend) || !scope.valid() || nilInterface(keys) {
		return failure("build cache", fmt.Errorf("%w: backend, scope, and key codec are required", ErrInvalid))
	}
	keyVersion, err := describeKeyCodec(keys)
	if err != nil {
		return failure("build cache", err)
	}
	if keyVersion == 0 {
		return failure("build cache", fmt.Errorf("%w: key version is zero", ErrInvalid))
	}
	valueDescriptor, err := describeCodec(values)
	if err != nil {
		return failure("build cache", err)
	}
	if err := validateRuntimePolicy(this.runtime, this.policy); err != nil {
		return failure("build cache", err)
	}
	description, ok := BackendDescriptionOf(backend)
	if !ok {
		return failure("build cache", fmt.Errorf("%w: backend description is required", ErrInvalid))
	}
	switch description.Topology {
	case ProcessBackend:
		if this.runtime.ClockSkew.mode == 0 {
			this.runtime.ClockSkew = SingleProcessClock()
		}
		if this.runtime.ClockSkew.mode != SingleProcessSkew {
			return failure("build cache", fmt.Errorf("%w: process backend requires a process clock policy", ErrInvalid))
		}
	case SharedBackend:
		if this.runtime.ClockSkew.mode != BoundedSharedSkew {
			return failure("build cache", fmt.Errorf("%w: shared backend requires bounded clock skew", ErrInvalid))
		}
	}
	maximumEnvelopeBytes, err := maxEnvelopeBytes(this.policy)
	if err != nil {
		return failure("build cache", err)
	}
	if maximumEnvelopeBytes > description.MaxItemBytes {
		return failure("build cache", fmt.Errorf("%w: cache envelope exceeds backend item limit", ErrTooLarge))
	}
	if this.policy.Retention.capacityOnly {
		if description.Topology != ProcessBackend || !description.CapacityBounded {
			return failure("build cache", fmt.Errorf("%w: capacity-only retention requires a bounded backend", ErrInvalid))
		}
	} else if !description.RelativeExpiry {
		return failure("build cache", fmt.Errorf("%w: backend does not support relative expiry", ErrInvalid))
	}
	if this.policy.Negative.duration > 0 && !description.RelativeExpiry {
		return failure("build cache", fmt.Errorf("%w: negative caching requires relative expiry", ErrInvalid))
	}
	maximumRelativeExpiry := this.policy.Retention.expiresAfter
	if this.policy.Retention.capacityOnly {
		maximumRelativeExpiry = this.policy.Negative.duration
	}
	if maximumRelativeExpiry > 0 && maximumRelativeExpiry > description.MaxRelativeExpiry {
		return failure("build cache", fmt.Errorf("%w: cache retention exceeds backend expiry range", ErrInvalid))
	}
	this.backend = backend
	this.scope = scope
	this.keys = keys
	this.keyVersion = keyVersion
	this.values = values
	this.valueDescriptor = valueDescriptor
	this.name = scope.namespace.purpose
	this.maxEnvelopeBytes = maximumEnvelopeBytes
	this.backendDescription = description
	return nil
}

func describeKeyCodec[K any](codec KeyCodec[K]) (version KeyVersion, err error) {
	defer func() {
		if recover() != nil {
			version = 0
			err = fmt.Errorf("%w: key codec descriptor panicked", ErrInvalid)
		}
	}()
	return codec.Version(), nil
}

func (this *Cache[K, V]) core() (*cacheCore[K, V], error) {
	if this == nil {
		return nil, failure("use cache", ErrNotActivated)
	}
	core := this.inner.Load()
	if core == nil {
		return nil, failure("use cache", ErrNotActivated)
	}
	if core.activation != nil && !core.activation.committed.Load() {
		return nil, failure("use cache", ErrNotActivated)
	}
	return core, nil
}

func (this *Cache[K, V]) Stats() LocalStats {
	core, err := this.core()
	if err != nil {
		return LocalStats{}
	}
	core.coord.mu.Lock()
	defer core.coord.mu.Unlock()
	return LocalStats{
		CoordinationEntries: len(core.coord.states),
		ActiveFlights:       core.coord.activeFlights,
		ActiveWrites:        core.coord.activeWrites,
	}
}

func (this *cacheCore[K, V]) observe(ctx context.Context, event Event) {
	event.Cache = this.name
	defer func() { _ = recover() }()
	this.runtime.Observer.Observe(ctx, event)
}

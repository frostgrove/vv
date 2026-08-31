package cache

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

type batchProbeBackend struct {
	description BackendDescription
	getMany     func(context.Context, []Address, BatchReadLimit) (map[Address][]byte, error)
	addresses   []Address
	limit       BatchReadLimit
	calls       int
}

func (this *batchProbeBackend) DescribeBackend() BackendDescription {
	return this.description
}

func (*batchProbeBackend) Get(context.Context, Address, ReadLimit) ([]byte, bool, error) {
	return nil, false, nil
}

func (*batchProbeBackend) Put(context.Context, Address, []byte, Expiry) error {
	return nil
}

func (*batchProbeBackend) Delete(context.Context, Address) error {
	return nil
}

func (this *batchProbeBackend) GetMany(ctx context.Context, addresses []Address, limit BatchReadLimit) (map[Address][]byte, error) {
	this.calls++
	this.addresses = append([]Address(nil), addresses...)
	this.limit = limit
	if this.getMany == nil {
		return map[Address][]byte{}, nil
	}
	return this.getMany(ctx, addresses, limit)
}

type fallbackProbeBackend struct {
	description BackendDescription
	values      map[Address][]byte
	addresses   []Address
	limits      []ReadLimit
}

func (this *fallbackProbeBackend) DescribeBackend() BackendDescription {
	return this.description
}

func (this *fallbackProbeBackend) Get(_ context.Context, address Address, limit ReadLimit) ([]byte, bool, error) {
	this.addresses = append(this.addresses, address)
	this.limits = append(this.limits, limit)
	value, ok := this.values[address]
	return value, ok, nil
}

func (*fallbackProbeBackend) Put(context.Context, Address, []byte, Expiry) error {
	return nil
}

func (*fallbackProbeBackend) Delete(context.Context, Address) error {
	return nil
}

type batchRecordingObserver struct {
	events []Event
}

func (this *batchRecordingObserver) Observe(_ context.Context, event Event) {
	this.events = append(this.events, event)
}

func (this *batchRecordingObserver) last(operation Operation) (Event, bool) {
	for index := len(this.events) - 1; index >= 0; index-- {
		if this.events[index].Operation == operation {
			return this.events[index], true
		}
	}
	return Event{}, false
}

type countingBatchCodec struct {
	decodes int
}

func (*countingBatchCodec) ID() string {
	return "counting-bytes"
}

func (*countingBatchCodec) Schema() ValueSchema {
	return 1
}

func (*countingBatchCodec) Encode(value []byte, limit ValueLimit) ([]byte, error) {
	if len(value) > limit.MaxBytes || len(value) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	return bytes.Clone(value), nil
}

func (this *countingBatchCodec) Decode(encoded []byte, limit ValueLimit) ([]byte, error) {
	if len(encoded) > limit.MaxBytes || len(encoded) > limit.MaxDecodedBytes {
		return nil, ErrTooLarge
	}
	this.decodes++
	return bytes.Clone(encoded), nil
}

func TestBatchReaderReceivesExactLimitsAndUniqueAddresses(t *testing.T) {
	policy := newCacheTestPolicy(64)
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatalf("maxEnvelopeBytes() error = %v", err)
	}
	policy.MaxBatchResultBytes = maximum * 2
	backend := newBatchProbeBackend(policy)
	cache, _ := newBatchTestCache(t, backend, Bytes(1), policy, &batchRecordingObserver{})

	results, err := cache.LookupMany(context.Background(), []string{"first", "second", "first"})
	if err != nil {
		t.Fatalf("LookupMany() error = %v", err)
	}
	if backend.calls != 1 {
		t.Fatalf("GetMany() calls = %d, want 1", backend.calls)
	}
	if len(backend.addresses) != 2 {
		t.Fatalf("GetMany() addresses = %d, want 2", len(backend.addresses))
	}
	if backend.addresses[0] == backend.addresses[1] {
		t.Fatal("GetMany() received duplicate addresses")
	}
	wantLimit := BatchReadLimit{
		MaxItems:      2,
		MaxItemBytes:  maximum,
		MaxTotalBytes: int64(policy.MaxBatchResultBytes),
	}
	if backend.limit != wantLimit {
		t.Fatalf("GetMany() limit = %+v, want %+v", backend.limit, wantLimit)
	}
	for index, result := range results {
		if result.State != Miss {
			t.Fatalf("result %d state = %v, want %v", index, result.State, Miss)
		}
	}
}

func TestBatchReaderRejectsUnexpectedAndOversizedResponses(t *testing.T) {
	tests := []struct {
		name     string
		keys     []string
		response func([]Address, BatchReadLimit) map[Address][]byte
	}{
		{
			name: "unexpected address",
			keys: []string{"key"},
			response: func(_ []Address, _ BatchReadLimit) map[Address][]byte {
				return map[Address][]byte{{KeyDigest: [32]byte{1}}: {1}}
			},
		},
		{
			name: "oversized item",
			keys: []string{"key"},
			response: func(addresses []Address, limit BatchReadLimit) map[Address][]byte {
				return map[Address][]byte{addresses[0]: make([]byte, limit.MaxItemBytes+1)}
			},
		},
		{
			name: "oversized aggregate",
			keys: []string{"first", "second"},
			response: func(addresses []Address, limit BatchReadLimit) map[Address][]byte {
				size := int(limit.MaxTotalBytes/2) + 1
				return map[Address][]byte{
					addresses[0]: make([]byte, size),
					addresses[1]: make([]byte, size),
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := newCacheTestPolicy(64)
			backend := newBatchProbeBackend(policy)
			backend.getMany = func(_ context.Context, addresses []Address, limit BatchReadLimit) (map[Address][]byte, error) {
				return test.response(addresses, limit), nil
			}
			cache, _ := newBatchTestCache(t, backend, Bytes(1), policy, &batchRecordingObserver{})
			results, err := cache.LookupMany(context.Background(), test.keys)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("LookupMany() error = %v", err)
			}
			if results != nil {
				t.Fatalf("LookupMany() results = %#v, want nil", results)
			}
		})
	}
}

func TestBatchDuplicateFanoutChargesEveryMaterialization(t *testing.T) {
	policy := newCacheTestPolicy(64)
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatalf("maxEnvelopeBytes() error = %v", err)
	}
	policy.MaxBatchResultBytes = maximum
	backend := newBatchProbeBackend(policy)
	observer := &batchRecordingObserver{}
	codec := &countingBatchCodec{}
	cache, core := newBatchTestCache(t, backend, codec, policy, observer)
	address := batchTestAddress(t, core, "duplicate")
	value := bytes.Repeat([]byte{7}, policy.MaxValueBytes)
	encoded := batchTestEnvelope(t, core, value)
	if len(encoded)*2 <= policy.MaxBatchResultBytes {
		t.Fatalf("test envelope does not exercise duplicate charging: envelope=%d limit=%d", len(encoded), policy.MaxBatchResultBytes)
	}
	backend.getMany = func(context.Context, []Address, BatchReadLimit) (map[Address][]byte, error) {
		return map[Address][]byte{address: encoded}, nil
	}

	results, err := cache.LookupMany(context.Background(), []string{"duplicate", "duplicate"})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("LookupMany() error = %v", err)
	}
	if results != nil {
		t.Fatalf("LookupMany() results = %#v, want nil", results)
	}
	if backend.calls != 1 || len(backend.addresses) != 1 {
		t.Fatalf("GetMany() calls = %d, addresses = %d", backend.calls, len(backend.addresses))
	}
	if codec.decodes != 1 {
		t.Fatalf("Decode() calls = %d, want 1 before budget rejection", codec.decodes)
	}
}

func TestBatchDuplicateFanoutReadsOnceAndDecodesIndependently(t *testing.T) {
	policy := newCacheTestPolicy(64)
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatalf("maxEnvelopeBytes() error = %v", err)
	}
	policy.MaxBatchResultBytes = maximum * 2
	backend := newBatchProbeBackend(policy)
	observer := &batchRecordingObserver{}
	codec := &countingBatchCodec{}
	cache, core := newBatchTestCache(t, backend, codec, policy, observer)
	address := batchTestAddress(t, core, "duplicate")
	value := bytes.Repeat([]byte{7}, policy.MaxValueBytes)
	encoded := batchTestEnvelope(t, core, value)
	backend.getMany = func(context.Context, []Address, BatchReadLimit) (map[Address][]byte, error) {
		return map[Address][]byte{address: encoded}, nil
	}

	results, err := cache.LookupMany(context.Background(), []string{"duplicate", "duplicate"})
	if err != nil {
		t.Fatalf("LookupMany() error = %v", err)
	}
	if backend.calls != 1 || len(backend.addresses) != 1 {
		t.Fatalf("GetMany() calls = %d, addresses = %d", backend.calls, len(backend.addresses))
	}
	if codec.decodes != 2 {
		t.Fatalf("Decode() calls = %d, want 2", codec.decodes)
	}
	if len(results) != 2 || results[0].State != Hit || results[1].State != Hit {
		t.Fatalf("LookupMany() results = %#v", results)
	}
	results[0].Value[0] = 99
	if results[1].Value[0] != 7 {
		t.Fatal("duplicate results share mutable decoded storage")
	}
	event, ok := observer.last(LookupManyOperation)
	if !ok {
		t.Fatal("lookup_many event was not observed")
	}
	if event.EncodedBytes != int64(len(encoded)*2) {
		t.Fatalf("encoded bytes = %d, want %d", event.EncodedBytes, len(encoded)*2)
	}
	if event.PayloadBytes != int64(len(value)*2) {
		t.Fatalf("payload bytes = %d, want %d", event.PayloadBytes, len(value)*2)
	}
}

func TestFallbackBatchChargesIncrementally(t *testing.T) {
	policy := newCacheTestPolicy(64)
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatalf("maxEnvelopeBytes() error = %v", err)
	}
	policy.MaxBatchResultBytes = maximum
	backend := &fallbackProbeBackend{
		description: batchTestDescription(policy),
		values:      make(map[Address][]byte),
	}
	cache, core := newBatchTestCache(t, backend, Bytes(1), policy, &batchRecordingObserver{})
	keys := []string{"first", "second", "third"}
	itemSize := maximum/2 + 1
	for _, key := range keys {
		backend.values[batchTestAddress(t, core, key)] = make([]byte, itemSize)
	}

	results, err := cache.LookupMany(context.Background(), keys)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("LookupMany() error = %v", err)
	}
	if results != nil {
		t.Fatalf("LookupMany() results = %#v, want nil", results)
	}
	if len(backend.addresses) != 2 {
		t.Fatalf("Get() calls = %d, want 2", len(backend.addresses))
	}
	for index, limit := range backend.limits {
		if limit != (ReadLimit{MaxBytes: maximum}) {
			t.Fatalf("Get() limit %d = %+v", index, limit)
		}
	}
}

func newBatchProbeBackend(policy Policy) *batchProbeBackend {
	return &batchProbeBackend{description: batchTestDescription(policy)}
}

func batchTestDescription(policy Policy) BackendDescription {
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		panic(err)
	}
	return BackendDescription{
		Name:              "batch-probe",
		Topology:          ProcessBackend,
		ExpiryClock:       ProcessExpiryClock,
		MaxItemBytes:      maximum,
		RelativeExpiry:    true,
		MaxRelativeExpiry: 24 * time.Hour,
	}
}

func newBatchTestCache(
	t *testing.T,
	backend Backend,
	codec Codec[[]byte],
	policy Policy,
	observer Observer,
) (*Cache[string, []byte], *cacheCore[string, []byte]) {
	t.Helper()
	runtime := newCacheTestRuntime(time.Unix(1_900_000_000, 0).UTC())
	runtime.Observer = observer
	cache, err := New(
		runtime,
		backend,
		Global[string](MustNamespace("app", "test", "batch", 1)),
		cacheTestKeyCodec(),
		codec,
		policy,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	core, err := cache.core()
	if err != nil {
		t.Fatalf("core() error = %v", err)
	}
	return cache, core
}

func batchTestAddress(t *testing.T, core *cacheCore[string, []byte], key string) Address {
	t.Helper()
	address, _, err := addressOf(core.scope, core.keys, core.keyVersion, key, core.policy.MaxKeyBytes)
	if err != nil {
		t.Fatalf("addressOf() error = %v", err)
	}
	return address
}

func batchTestEnvelope(t *testing.T, core *cacheCore[string, []byte], value []byte) []byte {
	t.Helper()
	encoded, _, _, err := encodeEnvelope(core.runtime, core.values, core.valueDescriptor, core.policy, Present(value))
	if err != nil {
		t.Fatalf("encodeEnvelope() error = %v", err)
	}
	return encoded
}

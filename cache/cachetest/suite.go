package cachetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
)

type Harness struct {
	Backend            cache.Backend
	Runtime            cache.Runtime
	Advance            func(time.Duration) error
	Close              func() error
	Capacity           *Capacity
	VerifyCancellation func(*testing.T)
	runID              string
}

type Capacity struct {
	MaxEntries             int
	BytePressureValueBytes int
}

const maximumItemProbeBytes = 8 << 20

type Factory func(*testing.T) Harness

func Run(t *testing.T, factory Factory) {
	if factory == nil {
		t.Fatal("cachetest: factory is nil")
	}
	t.Run("backend_values", func(t *testing.T) { runBackendValues(t, openHarness(t, factory)) })
	t.Run("backend_expiry", func(t *testing.T) { runBackendExpiry(t, openHarness(t, factory)) })
	t.Run("backend_cancellation", func(t *testing.T) { runBackendCancellation(t, openHarness(t, factory)) })
	t.Run("driver_cancellation", func(t *testing.T) { runDriverCancellation(t, openHarness(t, factory)) })
	t.Run("backend_batch", func(t *testing.T) { runBackendBatch(t, openHarness(t, factory)) })
	t.Run("backend_limits", func(t *testing.T) { runBackendLimits(t, openHarness(t, factory)) })
	t.Run("typed_values", func(t *testing.T) { runTypedValues(t, openHarness(t, factory)) })
	t.Run("typed_batch", func(t *testing.T) { runTypedBatch(t, openHarness(t, factory)) })
	t.Run("key_bounds", func(t *testing.T) { runKeyBounds(t, openHarness(t, factory)) })
	t.Run("codec_bounds", func(t *testing.T) { runCodecBounds(t, openHarness(t, factory)) })
	t.Run("transient_accounting", func(t *testing.T) { runTransientAccounting(t, openHarness(t, factory)) })
	t.Run("negative_and_stale", func(t *testing.T) { runNegativeAndStale(t, openHarness(t, factory)) })
	t.Run("jitter_and_skew", func(t *testing.T) { runJitterAndSkew(t, openHarness(t, factory)) })
	t.Run("stale_policies", func(t *testing.T) { runStalePolicies(t, openHarness(t, factory)) })
	t.Run("singleflight_and_saturation", func(t *testing.T) { runSingleflightAndSaturation(t, openHarness(t, factory)) })
	t.Run("flight_saturation_policies", func(t *testing.T) { runFlightSaturationPolicies(t, openHarness(t, factory)) })
	t.Run("shared_waiter_cancellation", func(t *testing.T) { runSharedWaiterCancellation(t, openHarness(t, factory)) })
	t.Run("waiter_cancellation", func(t *testing.T) { runWaiterCancellation(t, openHarness(t, factory)) })
	t.Run("loader_timeout", func(t *testing.T) { runLoaderTimeout(t, openHarness(t, factory)) })
	t.Run("failure_policies", func(t *testing.T) { runFailurePolicies(t, openHarness(t, factory)) })
	t.Run("mutation_fences", func(t *testing.T) { runMutationFences(t, openHarness(t, factory)) })
	t.Run("partitioning", func(t *testing.T) { runPartitioning(t, openHarness(t, factory)) })
	t.Run("corruption_and_bounds", func(t *testing.T) { runCorruptionAndBounds(t, openHarness(t, factory)) })
}

func openHarness(t *testing.T, factory Factory) Harness {
	t.Helper()
	harness := factory(t)
	if nilValue(harness.Backend) || nilValue(harness.Runtime.Clock) || nilValue(harness.Runtime.Random) || harness.Advance == nil || harness.VerifyCancellation == nil {
		t.Fatal("cachetest: factory returned an incomplete harness")
	}
	description, ok := cache.BackendDescriptionOf(harness.Backend)
	if !ok {
		t.Fatal("cachetest: backend description is invalid")
	}
	if harness.Capacity != nil {
		if !description.CapacityBounded || harness.Capacity.MaxEntries < 2 || harness.Capacity.MaxEntries > 1024 || harness.Capacity.BytePressureValueBytes < 1 || harness.Capacity.BytePressureValueBytes > description.MaxItemBytes || harness.Capacity.BytePressureValueBytes > maximumItemProbeBytes {
			t.Fatal("cachetest: backend capacity probe is invalid")
		}
	}
	var identity [12]byte
	if _, err := rand.Read(identity[:]); err != nil {
		t.Fatalf("cachetest: run identity: %v", err)
	}
	harness.runID = hex.EncodeToString(identity[:])
	if harness.Close != nil {
		t.Cleanup(func() {
			if err := harness.Close(); err != nil {
				t.Errorf("cachetest: close: %v", err)
			}
		})
	}
	return harness
}

func runBackendValues(t *testing.T, harness Harness) {
	ctx := context.Background()
	address := suiteAddress(harness, 1)
	expiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}
	original := []byte("first")
	if err := harness.Backend.Put(ctx, address, original, expiry); err != nil {
		t.Fatal(err)
	}
	original[0] = 'x'
	value, found, err := harness.Backend.Get(ctx, address, cache.ReadLimit{MaxBytes: 64})
	if err != nil || !found || string(value) != "first" {
		t.Fatalf("first get = %q, %v, %v", value, found, err)
	}
	value[0] = 'y'
	value, found, err = harness.Backend.Get(ctx, address, cache.ReadLimit{MaxBytes: 64})
	if err != nil || !found || string(value) != "first" {
		t.Fatalf("owned get = %q, %v, %v", value, found, err)
	}
	if err := harness.Backend.Put(ctx, address, []byte("oversized"), expiry); err != nil {
		t.Fatal(err)
	}
	if value, found, err := harness.Backend.Get(ctx, address, cache.ReadLimit{MaxBytes: 2}); !errors.Is(err, cache.ErrTooLarge) || found || value != nil {
		t.Fatalf("bounded get = %q, %v, %v", value, found, err)
	}
	if err := harness.Backend.Delete(ctx, address); err != nil {
		t.Fatal(err)
	}
	if err := harness.Backend.Delete(ctx, address); err != nil {
		t.Fatal(err)
	}
	if value, found, err := harness.Backend.Get(ctx, address, cache.ReadLimit{MaxBytes: 64}); err != nil || found || value != nil {
		t.Fatalf("deleted get = %q, %v, %v", value, found, err)
	}
}

func runBackendExpiry(t *testing.T, harness Harness) {
	ctx := context.Background()
	address := suiteAddress(harness, 2)
	if err := harness.Backend.Put(ctx, address, []byte("live"), cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	advance(t, harness, 10*time.Second-time.Nanosecond)
	if value, found, err := harness.Backend.Get(ctx, address, cache.ReadLimit{MaxBytes: 64}); err != nil || !found || string(value) != "live" {
		t.Fatalf("before boundary = %q, %v, %v", value, found, err)
	}
	advance(t, harness, time.Nanosecond)
	if value, found, err := harness.Backend.Get(ctx, address, cache.ReadLimit{MaxBytes: 64}); err != nil || found || value != nil {
		t.Fatalf("at boundary = %q, %v, %v", value, found, err)
	}
}

func runBackendCancellation(t *testing.T, harness Harness) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	address := suiteAddress(harness, 3)
	expiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}
	if _, _, err := harness.Backend.Get(ctx, address, cache.ReadLimit{MaxBytes: 64}); !errors.Is(err, context.Canceled) {
		t.Fatalf("get error = %v", err)
	}
	if err := harness.Backend.Put(ctx, address, []byte("value"), expiry); !errors.Is(err, context.Canceled) {
		t.Fatalf("put error = %v", err)
	}
	if err := harness.Backend.Delete(ctx, address); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete error = %v", err)
	}
}

func runDriverCancellation(t *testing.T, harness Harness) {
	harness.VerifyCancellation(t)
}

func runBackendBatch(t *testing.T, harness Harness) {
	reader, ok := cache.BatchReaderOf(harness.Backend)
	if !ok {
		t.Skip("backend has no batch capability")
	}
	ctx := context.Background()
	expiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}
	addresses := []cache.Address{suiteAddress(harness, 4), suiteAddress(harness, 5)}
	for index, address := range addresses {
		if err := harness.Backend.Put(ctx, address, bytes.Repeat([]byte{byte(index + 1)}, 2), expiry); err != nil {
			t.Fatal(err)
		}
	}
	values, err := reader.GetMany(ctx, []cache.Address{addresses[0], addresses[1], addresses[0]}, cache.BatchReadLimit{MaxItems: 3, MaxItemBytes: 4, MaxTotalBytes: 8})
	if err != nil || len(values) != 2 || len(values[addresses[0]]) != 2 || len(values[addresses[1]]) != 2 {
		t.Fatalf("batch = %v, %v", values, err)
	}
	values[addresses[0]][0] = 9
	value, found, err := harness.Backend.Get(ctx, addresses[0], cache.ReadLimit{MaxBytes: 4})
	if err != nil || !found || value[0] != 1 {
		t.Fatalf("batch ownership = %v, %v, %v", value, found, err)
	}
	values, err = reader.GetMany(ctx, addresses, cache.BatchReadLimit{MaxItems: 2, MaxItemBytes: 2, MaxTotalBytes: 2})
	if !errors.Is(err, cache.ErrTooLarge) || values != nil {
		t.Fatalf("bounded batch = %v, %v", values, err)
	}
}

func runBackendLimits(t *testing.T, harness Harness) {
	description, _ := cache.BackendDescriptionOf(harness.Backend)
	if description.MaxItemBytes <= maximumItemProbeBytes {
		oversized := make([]byte, description.MaxItemBytes+1)
		if err := harness.Backend.Put(context.Background(), suiteIndexedAddress(harness, 1000), oversized, cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}); !errors.Is(err, cache.ErrTooLarge) {
			t.Fatalf("oversized backend put = %v", err)
		}
	}
	if harness.Capacity == nil {
		return
	}
	ctx := context.Background()
	expiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}
	addresses := make([]cache.Address, harness.Capacity.MaxEntries+1)
	for index := range addresses {
		addresses[index] = suiteIndexedAddress(harness, 1100+index)
		if err := harness.Backend.Put(ctx, addresses[index], []byte{byte(index)}, expiry); err != nil {
			t.Fatal(err)
		}
	}
	found := 0
	for _, address := range addresses {
		_, present, err := harness.Backend.Get(ctx, address, cache.ReadLimit{MaxBytes: description.MaxItemBytes})
		if err != nil {
			t.Fatal(err)
		}
		if present {
			found++
		}
	}
	if found != harness.Capacity.MaxEntries {
		t.Fatalf("entry capacity retained %d values, want %d", found, harness.Capacity.MaxEntries)
	}
	if _, present, err := harness.Backend.Get(ctx, addresses[0], cache.ReadLimit{MaxBytes: description.MaxItemBytes}); err != nil || present {
		t.Fatalf("entry capacity oldest value = %v, %v", present, err)
	}
	for _, address := range addresses {
		if err := harness.Backend.Delete(ctx, address); err != nil {
			t.Fatal(err)
		}
	}
	left := suiteIndexedAddress(harness, 1200)
	right := suiteIndexedAddress(harness, 1201)
	value := bytes.Repeat([]byte{1}, harness.Capacity.BytePressureValueBytes)
	if err := harness.Backend.Put(ctx, left, value, expiry); err != nil {
		t.Fatal(err)
	}
	if err := harness.Backend.Put(ctx, right, value, expiry); err != nil {
		t.Fatal(err)
	}
	if _, present, err := harness.Backend.Get(ctx, left, cache.ReadLimit{MaxBytes: description.MaxItemBytes}); err != nil || present {
		t.Fatalf("byte capacity oldest value = %v, %v", present, err)
	}
	if got, present, err := harness.Backend.Get(ctx, right, cache.ReadLimit{MaxBytes: description.MaxItemBytes}); err != nil || !present || !bytes.Equal(got, value) {
		t.Fatalf("byte capacity newest value = %d, %v, %v", len(got), present, err)
	}
}

func runTypedValues(t *testing.T, harness Harness) {
	typed := newStringCache(t, harness, nil)
	ctx := context.Background()
	result, err := typed.Lookup(ctx, "empty")
	if err != nil || result.State != cache.Miss {
		t.Fatalf("miss = %+v, %v", result, err)
	}
	if err := typed.Put(ctx, "empty", ""); err != nil {
		t.Fatal(err)
	}
	result, err = typed.Lookup(ctx, "empty")
	if err != nil || result.State != cache.Hit || result.Value != "" {
		t.Fatalf("empty hit = %+v, %v", result, err)
	}
	if err := typed.Put(ctx, "empty", "updated"); err != nil {
		t.Fatal(err)
	}
	result, err = typed.Lookup(ctx, "empty")
	if err != nil || result.State != cache.Hit || result.Value != "updated" {
		t.Fatalf("updated hit = %+v, %v", result, err)
	}
	if err := typed.Forget(ctx, "empty"); err != nil {
		t.Fatal(err)
	}
	result, err = typed.Lookup(ctx, "empty")
	if err != nil || result.State != cache.Miss {
		t.Fatalf("forgotten lookup = %+v, %v", result, err)
	}
}

func runTypedBatch(t *testing.T, harness Harness) {
	typed := newStringCache(t, harness, nil)
	ctx := context.Background()
	if err := typed.Put(ctx, "first", "a"); err != nil {
		t.Fatal(err)
	}
	if err := typed.Put(ctx, "second", ""); err != nil {
		t.Fatal(err)
	}
	results, err := typed.LookupMany(ctx, []string{"second", "missing", "first", "second"})
	if err != nil || len(results) != 4 {
		t.Fatalf("batch results = %+v, %v", results, err)
	}
	if results[0].State != cache.Hit || results[0].Value != "" || results[1].State != cache.Miss || results[2].State != cache.Hit || results[2].Value != "a" || results[3].State != cache.Hit || results[3].Value != "" {
		t.Fatalf("batch order = %+v", results)
	}
	keys := make([]string, 17)
	for index := range keys {
		keys[index] = fmt.Sprintf("key-%d", index)
	}
	if results, err := typed.LookupMany(ctx, keys); !errors.Is(err, cache.ErrTooLarge) || results != nil {
		t.Fatalf("bounded keys = %+v, %v", results, err)
	}

	bounded := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.MaxBatchResultBytes = 500
	})
	for _, key := range []string{"left", "right"} {
		if err := bounded.Put(ctx, key, strings.Repeat(key, 40)); err != nil {
			t.Fatal(err)
		}
	}
	if results, err := bounded.LookupMany(ctx, []string{"left", "right"}); !errors.Is(err, cache.ErrTooLarge) || results != nil {
		t.Fatalf("bounded results = %+v, %v", results, err)
	}
}

func runKeyBounds(t *testing.T, harness Harness) {
	typed := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.MaxKeyBytes = 4
		policy.MaxBatchKeyBytes = 5
	})
	if result, err := typed.Lookup(context.Background(), "12345"); !errors.Is(err, cache.ErrTooLarge) || result.State != 0 {
		t.Fatalf("oversized key = %+v, %v", result, err)
	}
	if results, err := typed.LookupMany(context.Background(), []string{"123", "456"}); !errors.Is(err, cache.ErrTooLarge) || results != nil {
		t.Fatalf("oversized key batch = %+v, %v", results, err)
	}
}

type nestedValue struct {
	Next *nestedValue `json:"next,omitempty"`
}

type expansiveValue struct {
	Padding [256 << 10]byte `json:"-"`
}

type expansiveWireCodec struct{}

func (expansiveWireCodec) ID() string                { return "json" }
func (expansiveWireCodec) Schema() cache.ValueSchema { return cache.ValueSchema(1) }

func (expansiveWireCodec) Encode(value []expansiveValue, limit cache.ValueLimit) ([]byte, error) {
	if len(value) != 1 || limit.MaxBytes < 4 {
		return nil, cache.ErrTooLarge
	}
	return []byte(`[{}]`), nil
}

func (expansiveWireCodec) Decode([]byte, cache.ValueLimit) ([]expansiveValue, error) {
	return nil, cache.ErrInvalid
}

func runCodecBounds(t *testing.T, harness Harness) {
	ctx := context.Background()
	controller := MustController(harness.Backend)
	harness.Backend = controller.Backend()
	futureWriter := newTypedCache(t, harness, "future-schema", cache.String(cache.ValueSchema(2)), nil)
	futureReader := newTypedCache(t, harness, "future-schema", cache.String(cache.ValueSchema(1)), nil)
	if err := futureWriter.Put(ctx, "key", "future"); err != nil {
		t.Fatal(err)
	}
	if result, err := futureReader.Lookup(ctx, "key"); !errors.Is(err, cache.ErrCorrupt) || result.State != 0 {
		t.Fatalf("future schema = %+v, %v", result, err)
	}
	address, ok := lastAddress(controller.Records(), PutOperation)
	if !ok {
		t.Fatal("future envelope address was not recorded")
	}
	description, _ := cache.BackendDescriptionOf(harness.Backend)
	encoded, found, err := harness.Backend.Get(ctx, address, cache.ReadLimit{MaxBytes: description.MaxItemBytes})
	if err != nil || !found || len(encoded) < sha256.Size+10 {
		t.Fatalf("future envelope source = %d, %v, %v", len(encoded), found, err)
	}
	binary.BigEndian.PutUint16(encoded[8:10], 2)
	checksum := sha256.Sum256(encoded[:len(encoded)-sha256.Size])
	copy(encoded[len(encoded)-sha256.Size:], checksum[:])
	if err := harness.Backend.Put(ctx, address, encoded, cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if result, err := futureWriter.Lookup(ctx, "key"); !errors.Is(err, cache.ErrCorrupt) || result.State != 0 {
		t.Fatalf("future envelope = %+v, %v", result, err)
	}

	largeWriter := newTypedCache(t, harness, "encoded-bound", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.MaxValueBytes = 512
	})
	largeReader := newTypedCache(t, harness, "encoded-bound", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.MaxValueBytes = 32
	})
	if err := largeWriter.Put(ctx, "key", strings.Repeat("x", 64)); err != nil {
		t.Fatal(err)
	}
	if result, err := largeReader.Lookup(ctx, "key"); !errors.Is(err, cache.ErrCorrupt) || result.State != 0 {
		t.Fatalf("encoded bound = %+v, %v", result, err)
	}

	if safeJSONRuntimeSupported {
		depthWriter := newTypedCache(t, harness, "depth-bound", cache.JSON[nestedValue](cache.ValueSchema(1)), func(policy *cache.Policy) {
			policy.MaxValueDepth = 8
		})
		depthReader := newTypedCache(t, harness, "depth-bound", cache.JSON[nestedValue](cache.ValueSchema(1)), func(policy *cache.Policy) {
			policy.MaxValueDepth = 1
		})
		deep := nestedValue{Next: &nestedValue{Next: &nestedValue{}}}
		if err := depthWriter.Put(ctx, "key", deep); err != nil {
			t.Fatal(err)
		}
		if result, err := depthReader.Lookup(ctx, "key"); !errors.Is(err, cache.ErrCorrupt) || result.State != 0 {
			t.Fatalf("decoded depth = %+v, %v", result, err)
		}

		expansionWriter := newTypedCache(t, harness, "decoded-bound", expansiveWireCodec{}, nil)
		expansionReader := newTypedCache(t, harness, "decoded-bound", cache.JSON[[]expansiveValue](cache.ValueSchema(1)), nil)
		if err := expansionWriter.Put(ctx, "key", []expansiveValue{{}}); err != nil {
			t.Fatal(err)
		}
		if result, err := expansionReader.Lookup(ctx, "key"); !errors.Is(err, cache.ErrCorrupt) || result.State != 0 {
			t.Fatalf("decoded expansion = %+v, %v", result, err)
		}
	}
}

func runTransientAccounting(t *testing.T, harness Harness) {
	runTransientSaturation(t, harness, cache.RejectTransient(), "reject")
	runTransientSaturation(t, harness, cache.WaitForTransient(time.Hour), "cancel")
	runTransientSaturation(t, harness, cache.WaitForTransient(time.Hour), "wake")
}

func runTransientSaturation(t *testing.T, harness Harness, saturation cache.TransientSaturationPolicy, mode string) {
	operation := GetOperation
	if _, ok := cache.BatchReaderOf(harness.Backend); ok {
		operation = GetManyOperation
	}
	controller := MustController(harness.Backend)
	harness.Backend = controller.Backend()
	typed := newTypedCache(t, harness, "transient-"+mode, cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.MaxBatchKeys = 1
		policy.MaxBatchKeyBytes = policy.MaxKeyBytes
		policy.MaxBatchResultBytes = 4096
		policy.MaxTransientBytes = 0
		policy.TransientSaturation = saturation
	})
	type outcome struct {
		results []cache.Result[string]
		err     error
	}
	pauses := []*Pause{controller.MustPauseNext(operation)}
	defer func() {
		for _, pause := range pauses {
			pause.Release()
		}
	}()
	holderDone := make(chan outcome, 64)
	go func() {
		results, err := typed.LookupMany(context.Background(), []string{"holder-0"})
		holderDone <- outcome{results: results, err: err}
	}()
	waitForPause(t, pauses[0], "the first transient capacity holder")
	stats := typed.Stats()
	policy := typed.Describe().Policy
	limit := policy.MaxTransientBytes - policy.ReservedTransientBytes
	charge := stats.TransientBytes
	if charge <= 0 || charge > limit {
		t.Fatalf("transient peak = %d, limit %d", stats.TransientBytes, limit)
	}
	holderCount := int(limit / charge)
	if holderCount < 1 || holderCount > cap(holderDone) {
		t.Fatalf("transient holder count = %d, charge %d, limit %d", holderCount, charge, limit)
	}
	for index := 1; index < holderCount; index++ {
		pause := controller.MustPauseNext(operation)
		pauses = append(pauses, pause)
		key := "holder-" + strconv.Itoa(index)
		go func() {
			results, err := typed.LookupMany(context.Background(), []string{key})
			holderDone <- outcome{results: results, err: err}
		}()
		waitForPause(t, pause, "a transient capacity holder")
		expected := int64(index+1) * charge
		if used := typed.Stats().TransientBytes; used != expected {
			t.Fatalf("transient holder usage = %d, want %d", used, expected)
		}
	}
	saturated := int64(holderCount) * charge
	if limit-saturated >= charge {
		t.Fatalf("transient residual = %d, charge %d", limit-saturated, charge)
	}
	switch mode {
	case "reject":
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		secondDone := make(chan outcome, 1)
		go func() {
			results, err := typed.LookupMany(ctx, []string{"second"})
			secondDone <- outcome{results: results, err: err}
		}()
		second := receive(t, secondDone, "the rejected transient contender")
		if !errors.Is(second.err, cache.ErrSaturated) || second.results != nil {
			t.Fatalf("transient reject = %+v, %v", second.results, second.err)
		}
	case "cancel":
		ctx, cancel := context.WithCancel(context.Background())
		secondDone := make(chan outcome, 1)
		go func() {
			results, err := typed.LookupMany(ctx, []string{"second"})
			secondDone <- outcome{results: results, err: err}
		}()
		waitUntil(t, "the transient waiter", func() bool {
			stats := typed.Stats()
			return stats.TransientWaiters == 1 && stats.TransientBytes == saturated
		})
		cancel()
		second := receive(t, secondDone, "the cancelled transient waiter")
		if !errors.Is(second.err, context.Canceled) || second.results != nil {
			t.Fatalf("transient waiter cancellation = %+v, %v", second.results, second.err)
		}
		waitUntil(t, "the transient waiter cleanup", func() bool {
			return typed.Stats().TransientWaiters == 0
		})
	case "wake":
		secondDone := make(chan outcome, 1)
		go func() {
			results, err := typed.LookupMany(context.Background(), []string{"second"})
			secondDone <- outcome{results: results, err: err}
		}()
		waitUntil(t, "the transient waiter", func() bool {
			stats := typed.Stats()
			return stats.TransientWaiters == 1 && stats.TransientBytes == saturated
		})
		pauses[0].Release()
		second := receive(t, secondDone, "the admitted transient waiter")
		if second.err != nil || len(second.results) != 1 || second.results[0].State != cache.Miss {
			t.Fatalf("transient waiter wake = %+v, %v", second.results, second.err)
		}
	default:
		t.Fatalf("transient saturation mode = %q", mode)
	}
	for _, pause := range pauses {
		pause.Release()
	}
	for index := 0; index < holderCount; index++ {
		holder := receive(t, holderDone, "a transient capacity holder to finish")
		if holder.err != nil || len(holder.results) != 1 || holder.results[0].State != cache.Miss {
			t.Fatalf("transient capacity holder = %+v, %v", holder.results, holder.err)
		}
	}
	waitForQuiescence(t, typed)
}

func runNegativeAndStale(t *testing.T, harness Harness) {
	typed := newStringCache(t, harness, nil)
	ctx := context.Background()
	var absentCalls atomic.Int32
	absent := func(context.Context, string) (cache.LoadResult[string], error) {
		absentCalls.Add(1)
		return cache.Absent[string](), nil
	}
	result, err := typed.Resolve(ctx, "absent", absent)
	if err != nil || result.State != cache.Negative {
		t.Fatalf("negative load = %+v, %v", result, err)
	}
	result, err = typed.Resolve(ctx, "absent", absent)
	if err != nil || result.State != cache.Negative || absentCalls.Load() != 1 {
		t.Fatalf("negative hit = %+v, %d, %v", result, absentCalls.Load(), err)
	}
	advance(t, harness, time.Second)
	result, err = typed.Resolve(ctx, "absent", absent)
	if err != nil || result.State != cache.Negative || absentCalls.Load() != 2 {
		t.Fatalf("negative expiry = %+v, %d, %v", result, absentCalls.Load(), err)
	}

	loaderFailure := errors.New("private loader payload")
	var failedCalls atomic.Int32
	failed := func(context.Context, string) (cache.LoadResult[string], error) {
		failedCalls.Add(1)
		return cache.LoadResult[string]{}, loaderFailure
	}
	for range 2 {
		if _, err := typed.Resolve(ctx, "failure", failed); !errors.Is(err, cache.ErrLoader) || strings.Contains(err.Error(), loaderFailure.Error()) {
			t.Fatalf("loader error = %v", err)
		}
	}
	if failedCalls.Load() != 2 {
		t.Fatalf("loader calls = %d", failedCalls.Load())
	}

	if err := typed.Put(ctx, "stale", "value"); err != nil {
		t.Fatal(err)
	}
	advance(t, harness, 2*time.Second)
	result, err = typed.Lookup(ctx, "stale")
	if err != nil || result.State != cache.Stale || result.Value != "value" {
		t.Fatalf("stale lookup = %+v, %v", result, err)
	}
	advance(t, harness, 3*time.Second)
	result, err = typed.Lookup(ctx, "stale")
	if err != nil || result.State != cache.Miss {
		t.Fatalf("expired lookup = %+v, %v", result, err)
	}
}

func runJitterAndSkew(t *testing.T, harness Harness) {
	bound := runtimeSkewBound(t, harness, "jitter")
	description, _ := cache.BackendDescriptionOf(harness.Backend)
	jitterHarness := harness
	jitterHarness.Runtime.Random = NewRandom(uint64(time.Second - time.Nanosecond))
	jittered := newTypedCache(t, jitterHarness, "jitter", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.Freshness = cache.Expiring(bound+2*time.Second, 2*time.Second)
		policy.Retention = cache.ExpireAfter(bound + 5*time.Second)
		policy.Jitter = cache.SubtractUpTo(time.Second)
	})
	if err := jittered.Put(context.Background(), "key", "value"); err != nil {
		t.Fatal(err)
	}
	advance(t, harness, time.Second)
	if result, err := jittered.Lookup(context.Background(), "key"); err != nil || result.State != cache.Hit {
		t.Fatalf("jitter before boundary = %+v, %v", result, err)
	}
	advance(t, harness, time.Nanosecond)
	if result, err := jittered.Lookup(context.Background(), "key"); err != nil || result.State != cache.Stale {
		t.Fatalf("jitter at boundary = %+v, %v", result, err)
	}
	advance(t, harness, 3*time.Second-time.Nanosecond)
	if result, err := jittered.Lookup(context.Background(), "key"); err != nil || result.State != cache.Miss {
		t.Fatalf("jitter stale maximum = %+v, %v", result, err)
	}

	if description.Topology != cache.SharedBackend {
		return
	}
	skewed := newTypedCache(t, harness, "skew", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.Freshness = cache.Expiring(bound+2*time.Second, 2*time.Second)
		policy.Retention = cache.ExpireAfter(bound + 5*time.Second)
		policy.Jitter = cache.NoJitter()
	})
	if err := skewed.Put(context.Background(), "key", "value"); err != nil {
		t.Fatal(err)
	}
	if result, err := skewed.Lookup(context.Background(), "key"); err != nil || result.State != cache.Hit {
		t.Fatalf("skew before boundary = %+v, %v", result, err)
	}
	advance(t, harness, 2*time.Second)
	if result, err := skewed.Lookup(context.Background(), "key"); err != nil || result.State != cache.Stale {
		t.Fatalf("skew at boundary = %+v, %v", result, err)
	}
	advance(t, harness, 2*time.Second)
	if result, err := skewed.Lookup(context.Background(), "key"); err != nil || result.State != cache.Miss {
		t.Fatalf("skew stale maximum = %+v, %v", result, err)
	}
}

func runStalePolicies(t *testing.T, harness Harness) {
	bound := runtimeSkewBound(t, harness, "stale-policies")
	ctx := context.Background()
	serveOnError := newTypedCache(t, harness, "serve-on-error", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.Freshness = cache.Expiring(bound+2*time.Second, 2*time.Second)
		policy.Retention = cache.ExpireAfter(bound + 5*time.Second)
		policy.Stale = cache.ServeOnLoaderError
	})
	if err := serveOnError.Put(ctx, "key", "stale"); err != nil {
		t.Fatal(err)
	}
	advance(t, harness, 2*time.Second)
	var failedCalls atomic.Int32
	failingLoader := func(context.Context, string) (cache.LoadResult[string], error) {
		failedCalls.Add(1)
		return cache.LoadResult[string]{}, errors.New("private stale loader failure")
	}
	result, err := serveOnError.Resolve(ctx, "key", failingLoader)
	if err != nil || result.State != cache.Stale || result.Value != "stale" {
		t.Fatalf("serve-on-error stale = %+v, %v", result, err)
	}
	advance(t, harness, 2*time.Second)
	result, err = serveOnError.Resolve(ctx, "key", failingLoader)
	if !errors.Is(err, cache.ErrLoader) || result.State != 0 || failedCalls.Load() != 2 {
		t.Fatalf("serve-on-error expiry = %+v, calls %d, %v", result, failedCalls.Load(), err)
	}
	waitForQuiescence(t, serveOnError)

	refreshing := newTypedCache(t, harness, "serve-while-refreshing", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.Freshness = cache.Expiring(bound+2*time.Second, 2*time.Second)
		policy.Retention = cache.ExpireAfter(bound + 5*time.Second)
		policy.Stale = cache.ServeWhileRefreshing
	})
	if err := refreshing.Put(ctx, "key", "stale"); err != nil {
		t.Fatal(err)
	}
	advance(t, harness, 2*time.Second)
	loaderStarted := make(chan struct{})
	loaderRelease := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(loaderRelease)
		}
	}()
	type outcome struct {
		result cache.Result[string]
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := refreshing.Resolve(ctx, "key", func(context.Context, string) (cache.LoadResult[string], error) {
			close(loaderStarted)
			<-loaderRelease
			return cache.Present("refreshed"), nil
		})
		done <- outcome{result: result, err: err}
	}()
	stale := receive(t, done, "stale-while-refresh result")
	if stale.err != nil || stale.result.State != cache.Stale || stale.result.Value != "stale" {
		t.Fatalf("stale-while-refresh = %+v, %v", stale.result, stale.err)
	}
	receive(t, loaderStarted, "stale-while-refresh loader")
	if stats := refreshing.Stats(); stats.ActiveFlights != 1 || stats.FlightWaiters != 0 {
		t.Fatalf("stale-while-refresh coordination = %+v", stats)
	}
	close(loaderRelease)
	released = true
	waitForQuiescence(t, refreshing)
	result, err = refreshing.Lookup(ctx, "key")
	if err != nil || result.State != cache.Hit || result.Value != "refreshed" {
		t.Fatalf("stale-while-refresh completion = %+v, %v", result, err)
	}

	blocking := newTypedCache(t, harness, "refresh-blocking", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.Freshness = cache.Expiring(bound+2*time.Second, 2*time.Second)
		policy.Retention = cache.ExpireAfter(bound + 5*time.Second)
		policy.Stale = cache.RefreshBlocking
	})
	if err := blocking.Put(ctx, "key", "stale"); err != nil {
		t.Fatal(err)
	}
	advance(t, harness, 2*time.Second)
	blockingStarted := make(chan struct{})
	blockingRelease := make(chan struct{})
	blockingReleased := false
	defer func() {
		if !blockingReleased {
			close(blockingRelease)
		}
	}()
	blockingDone := make(chan outcome, 1)
	go func() {
		result, err := blocking.Resolve(ctx, "key", func(context.Context, string) (cache.LoadResult[string], error) {
			close(blockingStarted)
			<-blockingRelease
			return cache.Present("refreshed"), nil
		})
		blockingDone <- outcome{result: result, err: err}
	}()
	receive(t, blockingStarted, "blocking refresh loader")
	if stats := blocking.Stats(); stats.ActiveFlights != 1 || stats.FlightWaiters != 1 {
		t.Fatalf("blocking refresh coordination = %+v", stats)
	}
	select {
	case completed := <-blockingDone:
		t.Fatalf("blocking refresh completed before loader: %+v, %v", completed.result, completed.err)
	default:
	}
	close(blockingRelease)
	blockingReleased = true
	completed := receive(t, blockingDone, "blocking refresh result")
	if completed.err != nil || completed.result.State != cache.Loaded || completed.result.Value != "refreshed" {
		t.Fatalf("blocking refresh = %+v, %v", completed.result, completed.err)
	}
	waitForQuiescence(t, blocking)
	result, err = blocking.Lookup(ctx, "key")
	if err != nil || result.State != cache.Hit || result.Value != "refreshed" {
		t.Fatalf("blocking refresh stored = %+v, %v", result, err)
	}
}

func runSingleflightAndSaturation(t *testing.T, harness Harness) {
	typed := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.MaxFlights = 1
		policy.FlightSaturation = cache.Reject()
	})
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := typed.Resolve(context.Background(), "first", func(context.Context, string) (cache.LoadResult[string], error) {
			close(started)
			<-release
			return cache.Present("value"), nil
		})
		firstDone <- err
	}()
	receive(t, started, "the saturated loader to start")
	if _, err := typed.Resolve(context.Background(), "second", func(context.Context, string) (cache.LoadResult[string], error) {
		return cache.Present("unexpected"), nil
	}); !errors.Is(err, cache.ErrSaturated) {
		t.Fatalf("saturation error = %v", err)
	}
	close(release)
	if err := receive(t, firstDone, "the saturated loader to finish"); err != nil {
		t.Fatal(err)
	}
	waitForQuiescence(t, typed)

	var calls atomic.Int32
	joinedStarted := make(chan struct{})
	joinedRelease := make(chan struct{})
	results := make(chan cache.Result[string], 2)
	errors := make(chan error, 2)
	loader := func(context.Context, string) (cache.LoadResult[string], error) {
		if calls.Add(1) == 1 {
			close(joinedStarted)
		}
		<-joinedRelease
		return cache.Present("shared"), nil
	}
	go resolveInto(typed, "joined", loader, results, errors)
	receive(t, joinedStarted, "the shared loader to start")
	go resolveInto(typed, "joined", loader, results, errors)
	waitUntil(t, "the second shared waiter", func() bool {
		return typed.Stats().FlightWaiters == 2
	})
	close(joinedRelease)
	for range 2 {
		result := receive(t, results, "a shared result")
		if err := receive(t, errors, "a shared error"); err != nil || result.Value != "shared" {
			t.Fatalf("joined result = %+v, %v", result, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("joined loader calls = %d", calls.Load())
	}
	waitForQuiescence(t, typed)
}

func runLoaderTimeout(t *testing.T, harness Harness) {
	harness.Runtime.LoaderTimeout = 3 * time.Second
	typed := newStringCache(t, harness, nil)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := typed.Resolve(context.Background(), "timeout", func(ctx context.Context, _ string) (cache.LoadResult[string], error) {
			close(started)
			<-ctx.Done()
			return cache.LoadResult[string]{}, ctx.Err()
		})
		done <- err
	}()
	receive(t, started, "the timed loader to start")
	advance(t, harness, 3*time.Second)
	if err := receive(t, done, "the timed loader to stop"); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, cache.ErrLoader) {
		t.Fatalf("loader timeout = %v", err)
	}
	waitForQuiescence(t, typed)
}

func runFlightSaturationPolicies(t *testing.T, harness Harness) {
	bound := runtimeSkewBound(t, harness, "flight-saturation")
	waiting := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.MaxFlights = 1
		policy.FlightSaturation = cache.WaitBounded(time.Second)
	})
	heldStarted := make(chan struct{})
	heldRelease := make(chan struct{})
	heldDone := make(chan error, 1)
	go func() {
		_, err := waiting.Resolve(context.Background(), "held", func(ctx context.Context, _ string) (cache.LoadResult[string], error) {
			close(heldStarted)
			select {
			case <-heldRelease:
				return cache.Present("held"), nil
			case <-ctx.Done():
				return cache.LoadResult[string]{}, ctx.Err()
			}
		})
		heldDone <- err
	}()
	receive(t, heldStarted, "the flight capacity holder")
	waitingDone := make(chan error, 1)
	go func() {
		_, err := waiting.Resolve(context.Background(), "waiting", func(context.Context, string) (cache.LoadResult[string], error) {
			return cache.Present("unexpected"), nil
		})
		waitingDone <- err
	}()
	waitUntil(t, "the bounded flight waiter", func() bool {
		return waiting.Stats().CoordinationWaiters == 1
	})
	advance(t, harness, time.Second)
	if err := receive(t, waitingDone, "the bounded flight waiter"); !errors.Is(err, cache.ErrSaturated) {
		t.Fatalf("bounded flight waiter = %v", err)
	}
	close(heldRelease)
	if err := receive(t, heldDone, "the flight capacity holder to finish"); err != nil {
		t.Fatal(err)
	}
	waitForQuiescence(t, waiting)

	waking := newTypedCache(t, harness, "flight-wake", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.MaxFlights = 1
		policy.FlightSaturation = cache.WaitBounded(time.Hour)
	})
	wakeHolderStarted := make(chan struct{})
	wakeHolderRelease := make(chan struct{})
	wakeHolderReleased := false
	defer func() {
		if !wakeHolderReleased {
			close(wakeHolderRelease)
		}
	}()
	type outcome struct {
		result cache.Result[string]
		err    error
	}
	wakeHolderDone := make(chan outcome, 1)
	go func() {
		result, err := waking.Resolve(context.Background(), "holder", func(context.Context, string) (cache.LoadResult[string], error) {
			close(wakeHolderStarted)
			<-wakeHolderRelease
			return cache.Present("holder"), nil
		})
		wakeHolderDone <- outcome{result: result, err: err}
	}()
	receive(t, wakeHolderStarted, "the wake flight holder")
	wakeWaiterStarted := make(chan struct{})
	wakeWaiterDone := make(chan outcome, 1)
	go func() {
		result, err := waking.Resolve(context.Background(), "waiter", func(context.Context, string) (cache.LoadResult[string], error) {
			close(wakeWaiterStarted)
			return cache.Present("waiter"), nil
		})
		wakeWaiterDone <- outcome{result: result, err: err}
	}()
	waitUntil(t, "the releasable flight waiter", func() bool {
		return waking.Stats().CoordinationWaiters == 1
	})
	close(wakeHolderRelease)
	wakeHolderReleased = true
	holder := receive(t, wakeHolderDone, "the wake flight holder to finish")
	if holder.err != nil || holder.result.State != cache.Loaded || holder.result.Value != "holder" {
		t.Fatalf("wake flight holder = %+v, %v", holder.result, holder.err)
	}
	receive(t, wakeWaiterStarted, "the released flight waiter loader")
	waiter := receive(t, wakeWaiterDone, "the released flight waiter")
	if waiter.err != nil || waiter.result.State != cache.Loaded || waiter.result.Value != "waiter" {
		t.Fatalf("released flight waiter = %+v, %v", waiter.result, waiter.err)
	}
	waitForQuiescence(t, waking)

	stale := newTypedCache(t, harness, "serve-stale-saturation", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.Freshness = cache.Expiring(bound+time.Second, 5*time.Second)
		policy.MaxFlights = 1
		policy.FlightSaturation = cache.ServeStale()
		policy.Stale = cache.RefreshBlocking
	})
	if err := stale.Put(context.Background(), "stale", "cached"); err != nil {
		t.Fatal(err)
	}
	advance(t, harness, 2*time.Second)
	capacityStarted := make(chan struct{})
	capacityRelease := make(chan struct{})
	capacityDone := make(chan error, 1)
	go func() {
		_, err := stale.Resolve(context.Background(), "capacity", func(ctx context.Context, _ string) (cache.LoadResult[string], error) {
			close(capacityStarted)
			select {
			case <-capacityRelease:
				return cache.Present("capacity"), nil
			case <-ctx.Done():
				return cache.LoadResult[string]{}, ctx.Err()
			}
		})
		capacityDone <- err
	}()
	receive(t, capacityStarted, "the stale capacity holder")
	var staleLoaderCalls atomic.Int32
	result, err := stale.Resolve(context.Background(), "stale", func(context.Context, string) (cache.LoadResult[string], error) {
		staleLoaderCalls.Add(1)
		return cache.Present("unexpected"), nil
	})
	if err != nil || result.State != cache.Stale || result.Value != "cached" || staleLoaderCalls.Load() != 0 {
		t.Fatalf("stale saturation = %+v, calls %d, %v", result, staleLoaderCalls.Load(), err)
	}
	close(capacityRelease)
	if err := receive(t, capacityDone, "the stale capacity holder to finish"); err != nil {
		t.Fatal(err)
	}
	waitForQuiescence(t, stale)
}

func runSharedWaiterCancellation(t *testing.T, harness Harness) {
	typed := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.LastWaiter = cache.CancelLoader
	})
	started := make(chan struct{})
	release := make(chan struct{})
	loaderCanceled := make(chan struct{})
	loaderContext := make(chan context.Context, 1)
	var calls atomic.Int32
	loader := func(ctx context.Context, _ string) (cache.LoadResult[string], error) {
		if calls.Add(1) == 1 {
			loaderContext <- ctx
			close(started)
		}
		select {
		case <-release:
			return cache.Present("shared"), nil
		case <-ctx.Done():
			close(loaderCanceled)
			return cache.LoadResult[string]{}, ctx.Err()
		}
	}
	type outcome struct {
		result cache.Result[string]
		err    error
	}
	firstDone := make(chan outcome, 1)
	go func() {
		result, err := typed.Resolve(context.Background(), "shared-cancel", loader)
		firstDone <- outcome{result: result, err: err}
	}()
	receive(t, started, "the shared cancellation loader")
	sharedLoaderCtx := receive(t, loaderContext, "the shared loader context")
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	secondDone := make(chan outcome, 1)
	go func() {
		result, err := typed.Resolve(secondCtx, "shared-cancel", loader)
		secondDone <- outcome{result: result, err: err}
	}()
	waitUntil(t, "the cancellable shared waiter", func() bool {
		return typed.Stats().FlightWaiters == 2
	})
	cancelSecond()
	second := receive(t, secondDone, "the shared waiter cancellation")
	if !errors.Is(second.err, context.Canceled) {
		t.Fatalf("shared waiter cancellation = %+v, %v", second.result, second.err)
	}
	waitUntil(t, "the surviving shared waiter", func() bool {
		stats := typed.Stats()
		return stats.ActiveFlights == 1 && stats.FlightWaiters == 1
	})
	if err := sharedLoaderCtx.Err(); err != nil {
		t.Fatalf("one cancelled waiter cancelled the shared loader: %v", err)
	}
	select {
	case <-loaderCanceled:
		t.Fatal("one cancelled waiter stopped a loader still needed by another waiter")
	default:
	}
	close(release)
	first := receive(t, firstDone, "the surviving shared waiter result")
	if first.err != nil || first.result.Value != "shared" || first.result.State != cache.Loaded || calls.Load() != 1 {
		t.Fatalf("surviving shared waiter = %+v, calls %d, %v", first.result, calls.Load(), first.err)
	}
	waitForQuiescence(t, typed)
}

func runWaiterCancellation(t *testing.T, harness Harness) {
	observer := NewObserver()
	harness.Runtime.Observer = observer
	cancelCache := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.LastWaiter = cache.CancelLoader
	})
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	loaderStopped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := cancelCache.Resolve(ctx, "cancel", func(ctx context.Context, _ string) (cache.LoadResult[string], error) {
			close(started)
			<-ctx.Done()
			close(loaderStopped)
			return cache.LoadResult[string]{}, ctx.Err()
		})
		done <- err
	}()
	receive(t, started, "the cancellable loader to start")
	cancel()
	if err := receive(t, done, "the cancelled waiter to stop"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter = %v", err)
	}
	receive(t, loaderStopped, "the cancelled loader to stop")
	waitCtx, stopWait := context.WithTimeout(context.Background(), testTimeout)
	defer stopWait()
	if _, err := observer.Wait(waitCtx, 2); err != nil {
		t.Fatal(err)
	}
	waitForQuiescence(t, cancelCache)

	controller := MustController(harness.Backend)
	harness.Backend = controller.Backend()
	finishCache := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.LastWaiter = cache.FinishLoader
	})
	finishCtx, finishCancel := context.WithCancel(context.Background())
	finishStarted := make(chan struct{})
	finishRelease := make(chan struct{})
	finishLoaderCanceled := make(chan struct{})
	finishLoaderContext := make(chan context.Context, 1)
	waiterDone := make(chan error, 1)
	go func() {
		_, err := finishCache.Resolve(finishCtx, "finish", func(ctx context.Context, _ string) (cache.LoadResult[string], error) {
			finishLoaderContext <- ctx
			close(finishStarted)
			select {
			case <-finishRelease:
				return cache.Present("completed"), nil
			case <-ctx.Done():
				close(finishLoaderCanceled)
				return cache.LoadResult[string]{}, ctx.Err()
			}
		})
		waiterDone <- err
	}()
	receive(t, finishStarted, "the detached loader to start")
	detachedLoaderCtx := receive(t, finishLoaderContext, "the detached loader context")
	putPause := controller.MustPauseNext(PutOperation)
	finishCancel()
	if err := receive(t, waiterDone, "the detached waiter to stop"); !errors.Is(err, context.Canceled) {
		t.Fatalf("detached waiter = %v", err)
	}
	if err := detachedLoaderCtx.Err(); err != nil {
		t.Fatalf("FinishLoader cancelled the detached loader: %v", err)
	}
	select {
	case <-finishLoaderCanceled:
		t.Fatal("FinishLoader cancelled the detached loader")
	default:
	}
	close(finishRelease)
	waitForPause(t, putPause, "the detached loader write")
	putPause.Release()
	waitForQuiescence(t, finishCache)
	result, err := finishCache.Lookup(context.Background(), "finish")
	if err != nil || result.State != cache.Hit || result.Value != "completed" {
		t.Fatalf("detached completion = %+v, %v", result, err)
	}
}

func runFailurePolicies(t *testing.T, harness Harness) {
	controller := MustController(harness.Backend)
	harness.Backend = controller.Backend()
	typed := newStringCache(t, harness, nil)
	ctx := context.Background()
	private := errors.New("tenant-raw-key-value")
	controller.MustFailNext(GetOperation, private)
	if _, err := typed.Lookup(ctx, "raw-key"); !errors.Is(err, cache.ErrBackend) || strings.Contains(err.Error(), private.Error()) || strings.Contains(err.Error(), "raw-key") {
		t.Fatalf("read failure = %v", err)
	}
	controller.MustFailNext(PutOperation, private)
	if err := typed.Put(ctx, "raw-key", "raw-value"); !errors.Is(err, cache.ErrBackend) || strings.Contains(err.Error(), private.Error()) || strings.Contains(err.Error(), "raw-value") {
		t.Fatalf("write failure = %v", err)
	}
	controller.MustFailNext(DeleteOperation, private)
	if err := typed.Forget(ctx, "raw-key"); !errors.Is(err, cache.ErrBackend) || strings.Contains(err.Error(), private.Error()) || strings.Contains(err.Error(), "raw-key") {
		t.Fatalf("delete failure = %v", err)
	}
	waitForQuiescence(t, typed)

	readAsMiss := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.ReadFailure = cache.AsMiss
	})
	controller.MustFailNext(GetOperation, private)
	if result, err := readAsMiss.Lookup(ctx, "read-as-miss"); err != nil || result.State != cache.Miss {
		t.Fatalf("read-as-miss = %+v, %v", result, err)
	}
	ignoreWrite := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.WriteFailure = cache.Ignore
	})
	controller.MustFailNext(PutOperation, private)
	if err := ignoreWrite.Put(ctx, "ignored-write", "value"); err != nil {
		t.Fatalf("ignored write = %v", err)
	}
	if result, err := ignoreWrite.Lookup(ctx, "ignored-write"); err != nil || result.State != cache.Miss {
		t.Fatalf("ignored write lookup = %+v, %v", result, err)
	}
	ignoreDelete := newStringCache(t, harness, func(policy *cache.Policy) {
		policy.InvalidateFailure = cache.Ignore
	})
	if err := ignoreDelete.Put(ctx, "ignored-delete", "value"); err != nil {
		t.Fatal(err)
	}
	controller.MustFailNext(DeleteOperation, private)
	if err := ignoreDelete.Forget(ctx, "ignored-delete"); err != nil {
		t.Fatalf("ignored delete = %v", err)
	}
	if result, err := ignoreDelete.Lookup(ctx, "ignored-delete"); err != nil || result.State != cache.Hit || result.Value != "value" {
		t.Fatalf("ignored delete lookup = %+v, %v", result, err)
	}
}

func runPartitioning(t *testing.T, harness Harness) {
	controller := MustController(harness.Backend)
	harness.Backend = controller.Backend()
	type key struct {
		tenant string
		id     string
	}
	keys := cache.MustKeyFunc(cache.KeyVersion(1), func(value key, limit cache.KeyLimit) ([]byte, error) {
		if len(value.id) > limit.MaxBytes {
			return nil, cache.ErrTooLarge
		}
		return []byte(value.id), nil
	})
	namespace := suiteNamespace(harness, "partitioned", cache.Generation(1))
	scope := cache.Partitioned(namespace, func(value key, limit cache.KeyLimit) ([]byte, error) {
		if len(value.tenant) > limit.MaxBytes {
			return nil, cache.ErrTooLarge
		}
		return []byte(value.tenant), nil
	})
	typed, err := cache.New(harness.Runtime, harness.Backend, scope, keys, cache.String(cache.ValueSchema(1)), suitePolicyFor(t, harness, "partitioned"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	recordsBeforeMissing := len(controller.Records())
	missingTenant, missingTenantErr := typed.Lookup(ctx, key{id: "same-id"})
	if !errors.Is(missingTenantErr, cache.ErrTooLarge) || missingTenant.State != 0 || len(controller.Records()) != recordsBeforeMissing {
		t.Fatalf("missing tenant = %+v, records %d/%d, %v", missingTenant, len(controller.Records()), recordsBeforeMissing, missingTenantErr)
	}
	first := key{tenant: "tenant-a", id: "same-id"}
	second := key{tenant: "tenant-b", id: "same-id"}
	if err := typed.Put(ctx, first, "a"); err != nil {
		t.Fatal(err)
	}
	if err := typed.Put(ctx, second, "b"); err != nil {
		t.Fatal(err)
	}
	left, leftErr := typed.Lookup(ctx, first)
	right, rightErr := typed.Lookup(ctx, second)
	if leftErr != nil || rightErr != nil || left.Value != "a" || right.Value != "b" || left.State != cache.Hit || right.State != cache.Hit {
		t.Fatalf("partitioned results = %+v/%v, %+v/%v", left, leftErr, right, rightErr)
	}

	stringKeys := cache.MustKeyFunc(cache.KeyVersion(1), func(value string, limit cache.KeyLimit) ([]byte, error) {
		if len(value) > limit.MaxBytes {
			return nil, cache.ErrTooLarge
		}
		return []byte(value), nil
	})
	firstGeneration, err := cache.New(
		harness.Runtime,
		harness.Backend,
		cache.Global[string](suiteNamespace(harness, "generation", cache.Generation(1))),
		stringKeys,
		cache.String(cache.ValueSchema(1)),
		suitePolicyFor(t, harness, "generation-one"),
	)
	if err != nil {
		t.Fatal(err)
	}
	secondGeneration, err := cache.New(
		harness.Runtime,
		harness.Backend,
		cache.Global[string](suiteNamespace(harness, "generation", cache.Generation(2))),
		stringKeys,
		cache.String(cache.ValueSchema(1)),
		suitePolicyFor(t, harness, "generation-two"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstGeneration.Put(ctx, "same", "old"); err != nil {
		t.Fatal(err)
	}
	if err := secondGeneration.Put(ctx, "same", "new"); err != nil {
		t.Fatal(err)
	}
	oldResult, oldErr := firstGeneration.Lookup(ctx, "same")
	newResult, newErr := secondGeneration.Lookup(ctx, "same")
	if oldErr != nil || newErr != nil || oldResult.Value != "old" || newResult.Value != "new" {
		t.Fatalf("generation results = %+v/%v, %+v/%v", oldResult, oldErr, newResult, newErr)
	}
	leftNamespace := newTypedCache(t, harness, "namespace-left", cache.String(cache.ValueSchema(1)), nil)
	rightNamespace := newTypedCache(t, harness, "namespace-right", cache.String(cache.ValueSchema(1)), nil)
	if err := leftNamespace.Put(ctx, "same", "left"); err != nil {
		t.Fatal(err)
	}
	if err := rightNamespace.Put(ctx, "same", "right"); err != nil {
		t.Fatal(err)
	}
	leftNamespaceResult, leftNamespaceErr := leftNamespace.Lookup(ctx, "same")
	rightNamespaceResult, rightNamespaceErr := rightNamespace.Lookup(ctx, "same")
	if leftNamespaceErr != nil || rightNamespaceErr != nil || leftNamespaceResult.Value != "left" || rightNamespaceResult.Value != "right" {
		t.Fatalf("namespace results = %+v/%v, %+v/%v", leftNamespaceResult, leftNamespaceErr, rightNamespaceResult, rightNamespaceErr)
	}
}

func runMutationFences(t *testing.T, harness Harness) {
	controller := MustController(harness.Backend)
	harness.Backend = controller.Backend()
	typed := newStringCache(t, harness, nil)
	ctx := context.Background()
	oldPutPause := controller.MustPauseNext(PutOperation)
	oldPutDone := make(chan error, 1)
	go func() { oldPutDone <- typed.Put(ctx, "ordered", "old") }()
	waitForPause(t, oldPutPause, "the old write")
	deletePause := controller.MustPauseNext(DeleteOperation)
	forgetDone := make(chan error, 1)
	go func() { forgetDone <- typed.Forget(ctx, "ordered") }()
	waitUntil(t, "the delete blocked behind the old write", func() bool {
		stats := typed.Stats()
		return stats.ActiveWrites == 1 && stats.CoordinationWaiters == 1
	})
	if deletePause.HasEntered() {
		t.Fatal("delete reached backend during the old write")
	}
	oldPutPause.Release()
	if err := receive(t, oldPutDone, "the old write to finish"); err != nil {
		t.Fatal(err)
	}
	waitForPause(t, deletePause, "the ordered delete")
	newPutPause := controller.MustPauseNext(PutOperation)
	newPutDone := make(chan error, 1)
	go func() { newPutDone <- typed.Put(ctx, "ordered", "new") }()
	waitUntil(t, "the write blocked behind delete", func() bool {
		stats := typed.Stats()
		return stats.ActiveWrites == 1 && stats.CoordinationWaiters == 1
	})
	if newPutPause.HasEntered() {
		t.Fatal("new put reached backend during delete")
	}
	deletePause.Release()
	if err := receive(t, forgetDone, "the ordered delete to finish"); err != nil {
		t.Fatal(err)
	}
	waitForPause(t, newPutPause, "the new write")
	newPutPause.Release()
	if err := receive(t, newPutDone, "the new write to finish"); err != nil {
		t.Fatal(err)
	}
	result, err := typed.Lookup(ctx, "ordered")
	if err != nil || result.State != cache.Hit || result.Value != "new" {
		t.Fatalf("ordered mutation = %+v, %v", result, err)
	}

	loaderStarted := make(chan struct{})
	loaderRelease := make(chan struct{})
	resolveCtx, resolveCancel := context.WithTimeout(context.Background(), testTimeout)
	defer resolveCancel()
	type resolvedOutcome struct {
		result cache.Result[string]
		err    error
	}
	resolveDone := make(chan resolvedOutcome, 1)
	var loaderCalls atomic.Int32
	go func() {
		result, err := typed.Resolve(resolveCtx, "resolved", func(context.Context, string) (cache.LoadResult[string], error) {
			if loaderCalls.Add(1) == 1 {
				close(loaderStarted)
				timer := time.NewTimer(testTimeout)
				defer timer.Stop()
				select {
				case <-loaderRelease:
				case <-timer.C:
					return cache.LoadResult[string]{}, context.DeadlineExceeded
				}
				return cache.Present("obsolete"), nil
			}
			return cache.Present("fresh"), nil
		})
		resolveDone <- resolvedOutcome{result: result, err: err}
	}()
	receive(t, loaderStarted, "the obsolete loader to start")
	resolveDeletePause := controller.MustPauseNext(DeleteOperation)
	resolveForgetDone := make(chan error, 1)
	go func() { resolveForgetDone <- typed.Forget(ctx, "resolved") }()
	waitForPause(t, resolveDeletePause, "the resolve delete")
	resolveDeletePause.Release()
	if err := receive(t, resolveForgetDone, "the resolve delete to finish"); err != nil {
		t.Fatal(err)
	}
	putsBeforeRelease := countOperation(controller.Records(), PutOperation)
	close(loaderRelease)
	outcome := receive(t, resolveDone, "the superseded resolve to retry")
	if outcome.err != nil || outcome.result.State != cache.Loaded || outcome.result.Value != "fresh" || loaderCalls.Load() != 2 {
		t.Fatalf("superseded resolve = %+v, calls %d, %v", outcome.result, loaderCalls.Load(), outcome.err)
	}
	if puts := countOperation(controller.Records(), PutOperation); puts != putsBeforeRelease+1 {
		t.Fatalf("superseded resolve wrote %d backend values, want 1", puts-putsBeforeRelease)
	}
	waitForQuiescence(t, typed)
	result, err = typed.Lookup(ctx, "resolved")
	if err != nil || result.State != cache.Hit || result.Value != "fresh" {
		t.Fatalf("resolved mutation = %+v, %v", result, err)
	}
}

func runCorruptionAndBounds(t *testing.T, harness Harness) {
	controller := MustController(harness.Backend)
	harness.Backend = controller.Backend()
	typed := newStringCache(t, harness, nil)
	ctx := context.Background()
	if err := typed.Put(ctx, "corrupt", "value"); err != nil {
		t.Fatal(err)
	}
	address, ok := lastAddress(controller.Records(), PutOperation)
	if !ok {
		t.Fatal("put address was not recorded")
	}
	if err := harness.Backend.Put(ctx, address, []byte("not-an-envelope"), cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, err := typed.Lookup(ctx, "corrupt"); !errors.Is(err, cache.ErrCorrupt) {
		t.Fatalf("corrupt lookup = %v", err)
	}

	controller.Reset()
	corruptAsMiss := newTypedCache(t, harness, "corrupt-as-miss", cache.String(cache.ValueSchema(1)), func(policy *cache.Policy) {
		policy.Corruption = cache.CorruptAsMiss
	})
	if err := corruptAsMiss.Put(ctx, "key", "old"); err != nil {
		t.Fatal(err)
	}
	address, ok = lastAddress(controller.Records(), PutOperation)
	if !ok {
		t.Fatal("corrupt-as-miss address was not recorded")
	}
	if err := harness.Backend.Put(ctx, address, []byte("not-an-envelope"), cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if result, err := corruptAsMiss.Lookup(ctx, "key"); err != nil || result.State != cache.Miss {
		t.Fatalf("corrupt-as-miss lookup = %+v, %v", result, err)
	}
	var regenerationCalls atomic.Int32
	result, err := corruptAsMiss.Resolve(ctx, "key", func(context.Context, string) (cache.LoadResult[string], error) {
		regenerationCalls.Add(1)
		return cache.Present("regenerated"), nil
	})
	if err != nil || result.State != cache.Loaded || result.Value != "regenerated" || regenerationCalls.Load() != 1 {
		t.Fatalf("corrupt-as-miss resolve = %+v, calls %d, %v", result, regenerationCalls.Load(), err)
	}
	waitForQuiescence(t, corruptAsMiss)
	if result, err := corruptAsMiss.Lookup(ctx, "key"); err != nil || result.State != cache.Hit || result.Value != "regenerated" {
		t.Fatalf("corrupt-as-miss replacement = %+v, %v", result, err)
	}

	controller.Reset()
	if err := typed.Put(ctx, "bounded", "value"); err != nil {
		t.Fatal(err)
	}
	address, ok = lastAddress(controller.Records(), PutOperation)
	if !ok {
		t.Fatal("bounded address was not recorded")
	}
	description, _ := cache.BackendDescriptionOf(harness.Backend)
	oversizedLength := 2048
	if description.MaxItemBytes < oversizedLength {
		oversizedLength = description.MaxItemBytes
	}
	if oversizedLength <= 512 {
		t.Skip("backend test item limit is too small for an oversized envelope probe")
	}
	if err := harness.Backend.Put(ctx, address, make([]byte, oversizedLength), cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if _, err := typed.Lookup(ctx, "bounded"); !errors.Is(err, cache.ErrCorrupt) {
		t.Fatalf("oversized lookup = %v", err)
	}
}

func newStringCache(t *testing.T, harness Harness, change func(*cache.Policy)) *cache.Cache[string, string] {
	return newTypedCache(t, harness, "strings", cache.String(cache.ValueSchema(1)), change)
}

func newTypedCache[V any](t *testing.T, harness Harness, purpose string, values cache.Codec[V], change func(*cache.Policy)) *cache.Cache[string, V] {
	t.Helper()
	policy := suitePolicyFor(t, harness, purpose)
	if change != nil {
		change(&policy)
	}
	keys := cache.MustKeyFunc(cache.KeyVersion(1), func(value string, limit cache.KeyLimit) ([]byte, error) {
		if len(value) == 0 || len(value) > limit.MaxBytes {
			return nil, cache.ErrTooLarge
		}
		return []byte(value), nil
	})
	namespace := suiteNamespace(harness, purpose, cache.Generation(1))
	typed, err := cache.New(harness.Runtime, harness.Backend, cache.Global[string](namespace), keys, values, policy)
	if err != nil {
		t.Fatal(err)
	}
	return typed
}

func suitePolicy(t *testing.T) cache.Policy {
	t.Helper()
	policy, err := cache.Hot.Build()
	if err != nil {
		t.Fatal(err)
	}
	policy.Freshness = cache.Expiring(2*time.Second, 3*time.Second)
	policy.Retention = cache.ExpireAfter(8 * time.Second)
	policy.Negative = cache.CacheAbsenceFor(time.Second)
	policy.Jitter = cache.NoJitter()
	policy.MaxKeyBytes = 256
	policy.MaxValueBytes = 256
	policy.MaxValueDepth = 8
	policy.MaxFlights = 2
	policy.FlightSaturation = cache.WaitBounded(time.Second)
	policy.Stale = cache.RefreshBlocking
	policy.LastWaiter = cache.CancelLoader
	policy.MaxBatchKeys = 16
	policy.MaxBatchKeyBytes = 4096
	policy.MaxBatchResultBytes = 4096
	policy.ReadFailure = cache.Propagate
	policy.WriteFailure = cache.Propagate
	policy.InvalidateFailure = cache.Propagate
	policy.Corruption = cache.RefuseCorrupt
	return policy
}

func suitePolicyFor(t *testing.T, harness Harness, purpose string) cache.Policy {
	t.Helper()
	policy := suitePolicy(t)
	bound := runtimeSkewBound(t, harness, purpose)
	if bound > time.Duration(math.MaxInt64)-8*time.Second {
		t.Fatalf("clock skew bound = %s", bound)
	}
	policy.Freshness = cache.Expiring(bound+2*time.Second, 3*time.Second)
	policy.Retention = cache.ExpireAfter(bound + 8*time.Second)
	policy.Negative = cache.CacheAbsenceFor(bound + time.Second)
	return policy
}

func runtimeSkewBound(t *testing.T, harness Harness, purpose string) time.Duration {
	t.Helper()
	keys := cache.MustKeyFunc(cache.KeyVersion(1), func(value string, limit cache.KeyLimit) ([]byte, error) {
		if len(value) == 0 || len(value) > limit.MaxBytes {
			return nil, cache.ErrTooLarge
		}
		return []byte(value), nil
	})
	probe, err := cache.New(
		harness.Runtime,
		harness.Backend,
		cache.Global[string](suiteNamespace(harness, "clock-"+purpose, cache.Generation(1))),
		keys,
		cache.String(cache.ValueSchema(1)),
		suitePolicy(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := probe.Describe()
	description, _ := cache.BackendDescriptionOf(harness.Backend)
	if descriptor.ClockSkew.Bound < 0 ||
		(description.Topology == cache.SharedBackend && (descriptor.ClockSkew.Mode != cache.BoundedSharedSkew || descriptor.ClockSkew.Bound <= 0)) ||
		(description.Topology != cache.SharedBackend && (descriptor.ClockSkew.Mode != cache.SingleProcessSkew || descriptor.ClockSkew.Bound != 0)) {
		t.Fatalf("clock skew = %+v for topology %d", descriptor.ClockSkew, description.Topology)
	}
	return descriptor.ClockSkew.Bound
}

func resolveInto(typed *cache.Cache[string, string], key string, loader cache.Loader[string, string], results chan<- cache.Result[string], failures chan<- error) {
	result, err := typed.Resolve(context.Background(), key, loader)
	results <- result
	failures <- err
}

func advance(t *testing.T, harness Harness, duration time.Duration) {
	t.Helper()
	if err := harness.Advance(duration); err != nil {
		t.Fatal(err)
	}
}

func suiteAddress(harness Harness, value byte) cache.Address {
	namespace := sha256.Sum256([]byte("cachetest:" + harness.runID))
	key := sha256.Sum256(append([]byte(harness.runID+":"), value))
	return cache.Address{NamespaceDigest: namespace, KeyDigest: key}
}

func suiteIndexedAddress(harness Harness, value int) cache.Address {
	namespace := sha256.Sum256([]byte("cachetest:" + harness.runID))
	key := sha256.Sum256([]byte(harness.runID + ":" + strconv.Itoa(value)))
	return cache.Address{NamespaceDigest: namespace, KeyDigest: key}
}

func suiteNamespace(harness Harness, purpose string, generation cache.Generation) cache.Namespace {
	return cache.MustNamespace("cachetest", "test", purpose+"-"+harness.runID, generation)
}

func lastAddress(records []Record, operation Operation) (cache.Address, bool) {
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].Operation == operation {
			return records[index].Address, true
		}
	}
	return cache.Address{}, false
}

func countOperation(records []Record, operation Operation) int {
	count := 0
	for _, record := range records {
		if record.Operation == operation {
			count++
		}
	}
	return count
}

func waitForQuiescence(t *testing.T, typed *cache.Cache[string, string]) {
	t.Helper()
	var stats cache.LocalStats
	waitUntil(t, "cache quiescence", func() bool {
		stats = typed.Stats()
		return stats == (cache.LocalStats{})
	})
}

package cache

import (
	"context"
	"sync"
	"testing"
)

type interceptedBackend struct {
	*seamBackend
	mu    sync.Mutex
	calls int
	get   func(context.Context, Address, ReadLimit, int) ([]byte, bool, error)
}

func (this *interceptedBackend) Get(ctx context.Context, address Address, limit ReadLimit) ([]byte, bool, error) {
	this.mu.Lock()
	this.calls++
	call := this.calls
	hook := this.get
	this.mu.Unlock()
	if hook != nil {
		return hook(ctx, address, limit, call)
	}
	return this.seamBackend.Get(ctx, address, limit)
}

func (this *interceptedBackend) interceptGet(hook func(context.Context, Address, ReadLimit, int) ([]byte, bool, error)) {
	this.mu.Lock()
	this.get = hook
	this.mu.Unlock()
}

func newInterceptedMemoCache(t *testing.T) (*Cache[string, []byte], *interceptedBackend) {
	t.Helper()
	policy := newCacheTestPolicy(64)
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatalf("maxEnvelopeBytes() error = %v", err)
	}
	policy.MaxBatchResultBytes = maximum * 4
	backend := &interceptedBackend{seamBackend: newSeamBackend(policy)}
	cache, _ := newBatchTestCache(t, backend, Bytes(1), policy, &batchRecordingObserver{})
	return cache, backend
}

func overwriteDuringFirstRead(t *testing.T, cache *Cache[string, []byte], backend *interceptedBackend, key string, value []byte) func() {
	t.Helper()
	var writeErr error
	backend.interceptGet(func(ctx context.Context, address Address, limit ReadLimit, call int) ([]byte, bool, error) {
		superseded, found, err := backend.seamBackend.Get(ctx, address, limit)
		if call == 1 {
			writeErr = cache.Put(context.Background(), key, value)
		}
		return superseded, found, err
	})
	return func() {
		t.Helper()
		if writeErr != nil {
			t.Fatalf("the concurrent write failed: %v", writeErr)
		}
	}
}

func TestALookupDiscardedByAConcurrentWriteIsNotWhatTheMemoAnswersNext(t *testing.T) {
	cache, backend := newInterceptedMemoCache(t)
	memo := mustMemo(t)
	defer memo.Close()
	ctx := WithMemo(context.Background(), memo)
	if err := cache.Put(context.Background(), "key", []byte("before")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	assertWritten := overwriteDuringFirstRead(t, cache, backend, "key", []byte("after"))

	raced, err := cache.Lookup(ctx, "key")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	assertWritten()
	repeated, err := cache.Lookup(ctx, "key")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	if string(raced.Value) != "after" {
		t.Fatalf("the retried lookup returned %q, want the value the write left", raced.Value)
	}
	if string(repeated.Value) != "after" {
		t.Fatalf("the second lookup went backwards in time and returned %q from the memo", repeated.Value)
	}
}

func TestALookupManyDiscardedByAConcurrentWriteAsksTheBackendAgain(t *testing.T) {
	cache, backend := newInterceptedMemoCache(t)
	memo := mustMemo(t)
	defer memo.Close()
	ctx := WithMemo(context.Background(), memo)
	if err := cache.Put(context.Background(), "key", []byte("before")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	assertWritten := overwriteDuringFirstRead(t, cache, backend, "key", []byte("after"))

	results, err := cache.LookupMany(ctx, []string{"key"})
	if err != nil {
		t.Fatalf("LookupMany() error = %v", err)
	}
	assertWritten()

	if len(results) != 1 || string(results[0].Value) != "after" {
		t.Fatalf("LookupMany() answered %q from the read its own coordination discarded", results[0].Value)
	}
}

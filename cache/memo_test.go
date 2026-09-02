package cache

import (
	"context"
	"errors"
	"testing"
)

func newMemoTestCache(t *testing.T) (*Cache[string, []byte], *seamBackend) {
	t.Helper()
	policy := newCacheTestPolicy(64)
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatalf("maxEnvelopeBytes() error = %v", err)
	}
	policy.MaxBatchResultBytes = maximum * 4
	backend := newSeamBackend(policy)
	cache, _ := newBatchTestCache(t, backend, Bytes(1), policy, &batchRecordingObserver{})
	return cache, backend
}

func mustMemo(t *testing.T) *Memo {
	t.Helper()
	memo, err := NewMemo(MemoLimit{MaxEntries: 8, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewMemo() error = %v", err)
	}
	return memo
}

func TestASecondLookupInOneExecutionNeverReachesTheBackend(t *testing.T) {
	cache, backend := newMemoTestCache(t)
	memo := mustMemo(t)
	defer memo.Close()
	ctx := WithMemo(context.Background(), memo)
	if err := cache.Put(ctx, "key", []byte("value")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	first, err := cache.Lookup(ctx, "key")
	if err != nil || first.State != Hit {
		t.Fatalf("Lookup() = %v, %v", first.State, err)
	}
	reads := backend.reads()
	second, err := cache.Lookup(ctx, "key")
	if err != nil || second.State != Hit || string(second.Value) != "value" {
		t.Fatalf("Lookup() = %v, %q, %v", second.State, second.Value, err)
	}

	if backend.reads() != reads {
		t.Fatalf("backend reads = %d, want the memoized answer to stand at %d", backend.reads(), reads)
	}
	if stats := memo.Stats(); stats.Entries != 1 || stats.Hits != 1 {
		t.Fatalf("memo stats = %+v", stats)
	}
}

func TestAMemoNeverRemembersABackendMiss(t *testing.T) {
	cache, backend := newMemoTestCache(t)
	memo := mustMemo(t)
	defer memo.Close()
	ctx := WithMemo(context.Background(), memo)

	if result, err := cache.Lookup(ctx, "absent"); err != nil || result.State != Miss {
		t.Fatalf("Lookup() = %v, %v", result.State, err)
	}
	reads := backend.reads()
	if result, err := cache.Lookup(ctx, "absent"); err != nil || result.State != Miss {
		t.Fatalf("Lookup() = %v, %v", result.State, err)
	}

	if backend.reads() == reads {
		t.Fatal("a backend miss was memoized, so a concurrent write would stay invisible")
	}
	if stats := memo.Stats(); stats.Entries != 0 {
		t.Fatalf("memo stats = %+v, want nothing remembered", stats)
	}
}

func TestAConfirmedAbsenceIsMemoizedUnlikeAMiss(t *testing.T) {
	cache, backend := newMemoTestCache(t)
	memo := mustMemo(t)
	defer memo.Close()
	ctx := WithMemo(context.Background(), memo)
	absent := func(context.Context, string) (LoadResult[[]byte], error) { return Absent[[]byte](), nil }

	if result, err := cache.Resolve(ctx, "gone", absent); err != nil || result.State != Negative {
		t.Fatalf("Resolve() = %v, %v", result.State, err)
	}
	if result, err := cache.Lookup(ctx, "gone"); err != nil || result.State != Negative {
		t.Fatalf("Lookup() = %v, %v", result.State, err)
	}
	reads := backend.reads()
	if result, err := cache.Lookup(ctx, "gone"); err != nil || result.State != Negative {
		t.Fatalf("Lookup() = %v, %v", result.State, err)
	}

	if backend.reads() != reads {
		t.Fatalf("backend reads = %d, want the memoized absence to stand at %d", backend.reads(), reads)
	}
}

func TestAWriteInsideAnExecutionDropsWhatTheMemoHeld(t *testing.T) {
	for name, mutate := range map[string]func(context.Context, *Cache[string, []byte]) error{
		"put": func(ctx context.Context, cache *Cache[string, []byte]) error {
			return cache.Put(ctx, "key", []byte("second"))
		},
		"forget": func(ctx context.Context, cache *Cache[string, []byte]) error {
			return cache.Forget(ctx, "key")
		},
		"resolve": func(ctx context.Context, cache *Cache[string, []byte]) error {
			_, err := cache.Resolve(ctx, "key", func(context.Context, string) (LoadResult[[]byte], error) {
				return Present([]byte("second")), nil
			})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			cache, backend := newMemoTestCache(t)
			memo := mustMemo(t)
			defer memo.Close()
			ctx := WithMemo(context.Background(), memo)
			if err := cache.Put(ctx, "key", []byte("first")); err != nil {
				t.Fatalf("Put() error = %v", err)
			}
			if _, err := cache.Lookup(ctx, "key"); err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}

			if err := mutate(ctx, cache); err != nil {
				t.Fatalf("mutation error = %v", err)
			}
			reads := backend.reads()
			result, err := cache.Lookup(ctx, "key")
			if err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}

			if backend.reads() == reads {
				t.Fatal("the lookup after the mutation answered from a memo the mutation should have dropped")
			}
			if name == "put" && string(result.Value) != "second" {
				t.Fatalf("Lookup() value = %q, want the value the mutation left", result.Value)
			}
		})
	}
}

func TestAClosedMemoIsANoOpBarrier(t *testing.T) {
	cache, backend := newMemoTestCache(t)
	memo := mustMemo(t)
	ctx := WithMemo(context.Background(), memo)
	if err := cache.Put(ctx, "key", []byte("value")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, err := cache.Lookup(ctx, "key"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	memo.Close()
	memo.Close()
	reads := backend.reads()
	if _, err := cache.Lookup(ctx, "key"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	if backend.reads() == reads {
		t.Fatal("a closed memo still answered a lookup")
	}
	if stats := memo.Stats(); !stats.Closed || stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("memo stats = %+v, want an emptied closed memo", stats)
	}
}

func TestAMemoRefusesWhatItCannotHold(t *testing.T) {
	if _, err := NewMemo(MemoLimit{MaxEntries: 0, MaxBytes: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewMemo() error = %v", err)
	}
	if _, err := NewMemo(MemoLimit{MaxEntries: MaxMemoEntries + 1, MaxBytes: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewMemo() error = %v", err)
	}
	cache, backend := newMemoTestCache(t)
	memo, err := NewMemo(MemoLimit{MaxEntries: 1, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("NewMemo() error = %v", err)
	}
	defer memo.Close()
	ctx := WithMemo(context.Background(), memo)
	for _, key := range []string{"first", "second"} {
		if err := cache.Put(ctx, key, []byte(key)); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
		if _, err := cache.Lookup(ctx, key); err != nil {
			t.Fatalf("Lookup() error = %v", err)
		}
	}
	reads := backend.reads()

	if _, err := cache.Lookup(ctx, "second"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	if backend.reads() == reads {
		t.Fatal("the memo held more entries than its bound admits")
	}
	if stats := memo.Stats(); stats.Entries != 1 || stats.Refused == 0 {
		t.Fatalf("memo stats = %+v", stats)
	}
}

func TestALookupManyAsksTheBackendOnlyForWhatTheMemoLacks(t *testing.T) {
	cache, backend := newMemoTestCache(t)
	memo := mustMemo(t)
	defer memo.Close()
	ctx := WithMemo(context.Background(), memo)
	for _, key := range []string{"first", "second"} {
		if err := cache.Put(ctx, key, []byte(key)); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
	}
	if _, err := cache.Lookup(ctx, "first"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	reads := backend.reads()

	results, err := cache.LookupMany(ctx, []string{"first", "second", "first"})
	if err != nil {
		t.Fatalf("LookupMany() error = %v", err)
	}

	if backend.reads() != reads+1 {
		t.Fatalf("backend reads = %d, want one read for the key the memo lacked", backend.reads()-reads)
	}
	if len(results) != 3 || string(results[0].Value) != "first" || string(results[1].Value) != "second" || string(results[2].Value) != "first" {
		t.Fatalf("LookupMany() results = %+v", results)
	}
}

func TestACacheWithoutAMemoInItsContextIsUnchanged(t *testing.T) {
	cache, backend := newMemoTestCache(t)
	ctx := context.Background()
	if err := cache.Put(ctx, "key", []byte("value")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if _, err := cache.Lookup(ctx, "key"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	reads := backend.reads()
	if _, err := cache.Lookup(ctx, "key"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	if backend.reads() != reads+1 {
		t.Fatal("a lookup without a memo skipped the backend")
	}
	if MemoFrom(ctx) != nil {
		t.Fatal("MemoFrom invented a memo")
	}
}

type nilBatchBackend struct {
	*seamBackend
}

func (*nilBatchBackend) GetMany(context.Context, []Address, BatchReadLimit) (map[Address][]byte, error) {
	return nil, nil
}

func TestAMemoMergesIntoABackendAnswerThatWasNil(t *testing.T) {
	policy := newCacheTestPolicy(64)
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatalf("maxEnvelopeBytes() error = %v", err)
	}
	policy.MaxBatchResultBytes = maximum * 4
	backend := &nilBatchBackend{seamBackend: newSeamBackend(policy)}
	cache, _ := newBatchTestCache(t, backend, Bytes(1), policy, &batchRecordingObserver{})
	memo := mustMemo(t)
	defer memo.Close()
	ctx := WithMemo(context.Background(), memo)
	if err := cache.Put(ctx, "key", []byte("value")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, err := cache.Lookup(ctx, "key"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	results, err := cache.LookupMany(ctx, []string{"key", "other"})
	if err != nil {
		t.Fatalf("LookupMany() error = %v", err)
	}

	if results[0].State != Hit || string(results[0].Value) != "value" || results[1].State != Miss {
		t.Fatalf("LookupMany() results = %+v", results)
	}
}

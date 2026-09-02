package cache

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func newResolveManyCache(t *testing.T) (*Cache[string, []byte], *seamBackend) {
	t.Helper()
	policy := newCacheTestPolicy(64)
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatalf("maxEnvelopeBytes() error = %v", err)
	}
	policy.MaxBatchResultBytes = maximum * 8
	backend := newSeamBackend(policy)
	cache, _ := newBatchTestCache(t, backend, Bytes(1), policy, &batchRecordingObserver{})
	return cache, backend
}

func echoBatchLoader(calls *[][]string) BatchLoader[string, []byte] {
	return func(_ context.Context, keys []string) ([]LoadResult[[]byte], error) {
		*calls = append(*calls, append([]string(nil), keys...))
		results := make([]LoadResult[[]byte], len(keys))
		for index, key := range keys {
			results[index] = Present([]byte("loaded:" + key))
		}
		return results, nil
	}
}

func TestResolveManyKeepsInputOrderAndAsksForEachAddressOnce(t *testing.T) {
	cache, _ := newResolveManyCache(t)
	calls := make([][]string, 0, 1)

	results, err := cache.ResolveMany(context.Background(), []string{"b", "a", "b"}, echoBatchLoader(&calls))
	if err != nil {
		t.Fatalf("ResolveMany() error = %v", err)
	}

	if len(calls) != 1 || len(calls[0]) != 2 || calls[0][0] != "b" || calls[0][1] != "a" {
		t.Fatalf("loader saw %v, want one call for b and a in first-seen order", calls)
	}
	want := []string{"loaded:b", "loaded:a", "loaded:b"}
	for index, result := range results {
		if result.State != Loaded || string(result.Value) != want[index] {
			t.Fatalf("result %d = %v %q, want %s", index, result.State, result.Value, want[index])
		}
	}
}

func TestResolveManyAsksTheLoaderOnlyForWhatTheCacheLacks(t *testing.T) {
	cache, _ := newResolveManyCache(t)
	if err := cache.Put(context.Background(), "cached", []byte("stored")); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	calls := make([][]string, 0, 1)

	results, err := cache.ResolveMany(context.Background(), []string{"cached", "fresh"}, echoBatchLoader(&calls))
	if err != nil {
		t.Fatalf("ResolveMany() error = %v", err)
	}

	if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != "fresh" {
		t.Fatalf("loader saw %v, want only the missing key", calls)
	}
	if results[0].State != Hit || string(results[0].Value) != "stored" {
		t.Fatalf("cached result = %v %q", results[0].State, results[0].Value)
	}
	if results[1].State != Loaded || string(results[1].Value) != "loaded:fresh" {
		t.Fatalf("loaded result = %v %q", results[1].State, results[1].Value)
	}
}

func TestResolveManyLeavesWhatItLoadedInTheCache(t *testing.T) {
	cache, backend := newResolveManyCache(t)
	calls := make([][]string, 0, 1)

	if _, err := cache.ResolveMany(context.Background(), []string{"one", "two"}, echoBatchLoader(&calls)); err != nil {
		t.Fatalf("ResolveMany() error = %v", err)
	}

	if backend.stored() != 2 {
		t.Fatalf("backend holds %d entries, want the two the batch loaded", backend.stored())
	}
	results, err := cache.LookupMany(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("LookupMany() error = %v", err)
	}
	for index, result := range results {
		if result.State != Hit {
			t.Fatalf("result %d state = %v, want a hit", index, result.State)
		}
	}
}

func TestResolveManyRefusesTheWholeBatchWhenTheLoaderFails(t *testing.T) {
	tests := map[string]BatchLoader[string, []byte]{
		"error": func(context.Context, []string) ([]LoadResult[[]byte], error) {
			return nil, errors.New("upstream is down")
		},
		"short answer": func(_ context.Context, keys []string) ([]LoadResult[[]byte], error) {
			return make([]LoadResult[[]byte], len(keys)-1), nil
		},
		"unset presence": func(_ context.Context, keys []string) ([]LoadResult[[]byte], error) {
			return make([]LoadResult[[]byte], len(keys)), nil
		},
		"panic": func(context.Context, []string) ([]LoadResult[[]byte], error) {
			panic("loader")
		},
	}
	for name, load := range tests {
		t.Run(name, func(t *testing.T) {
			cache, backend := newResolveManyCache(t)

			results, err := cache.ResolveMany(context.Background(), []string{"one", "two"}, load)

			if err == nil || results != nil {
				t.Fatalf("ResolveMany() = %+v, %v, want a refusal", results, err)
			}
			if backend.writes() != 0 {
				t.Fatalf("backend writes = %d, want nothing written", backend.writes())
			}
		})
	}
}

func TestResolveManyWritesNothingWhenTheAnswersExceedTheBatchBudget(t *testing.T) {
	policy := newCacheTestPolicy(64)
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil {
		t.Fatalf("maxEnvelopeBytes() error = %v", err)
	}
	policy.MaxBatchResultBytes = maximum + 1
	backend := newSeamBackend(policy)
	cache, _ := newBatchTestCache(t, backend, Bytes(1), policy, &batchRecordingObserver{})
	load := func(_ context.Context, keys []string) ([]LoadResult[[]byte], error) {
		results := make([]LoadResult[[]byte], len(keys))
		for index := range keys {
			results[index] = Present(make([]byte, policy.MaxValueBytes))
		}
		return results, nil
	}

	results, err := cache.ResolveMany(context.Background(), []string{"one", "two"}, load)

	if !errors.Is(err, ErrTooLarge) || results != nil {
		t.Fatalf("ResolveMany() = %+v, %v, want a refusal on the cumulative bound", results, err)
	}
	if backend.writes() != 0 {
		t.Fatalf("backend writes = %d, want the bound checked before the first write", backend.writes())
	}
}

func TestResolveManyRefusesMoreKeysThanTheBatchAdmits(t *testing.T) {
	cache, _ := newResolveManyCache(t)
	calls := make([][]string, 0)
	keys := make([]string, 0, 64)
	for index := 0; index < 64; index++ {
		keys = append(keys, fmt.Sprintf("key-%d", index))
	}

	if _, err := cache.ResolveMany(context.Background(), keys, echoBatchLoader(&calls)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ResolveMany() error = %v, want a refusal", err)
	}
	if len(calls) != 0 {
		t.Fatalf("loader was called %d times for a refused batch", len(calls))
	}
}

func TestResolveManyTurnsAConfirmedAbsenceIntoANegativeResult(t *testing.T) {
	cache, _ := newResolveManyCache(t)

	results, err := cache.ResolveMany(context.Background(), []string{"gone"}, func(context.Context, []string) ([]LoadResult[[]byte], error) {
		return []LoadResult[[]byte]{Absent[[]byte]()}, nil
	})
	if err != nil {
		t.Fatalf("ResolveMany() error = %v", err)
	}

	if len(results) != 1 || results[0].State != Negative {
		t.Fatalf("ResolveMany() results = %+v, want one negative", results)
	}
	cached, err := cache.Lookup(context.Background(), "gone")
	if err != nil || cached.State != Negative {
		t.Fatalf("Lookup() = %v, %v, want the negative to have been stored", cached.State, err)
	}
}

func TestResolveManyOnAnEmptyOrInvalidCallAnswersWithoutTheLoader(t *testing.T) {
	cache, _ := newResolveManyCache(t)
	calls := make([][]string, 0)

	results, err := cache.ResolveMany(context.Background(), nil, echoBatchLoader(&calls))
	if err != nil || len(results) != 0 {
		t.Fatalf("ResolveMany() = %+v, %v", results, err)
	}
	if _, err := cache.ResolveMany(context.Background(), []string{"key"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ResolveMany() error = %v, want a refusal for a missing loader", err)
	}
	if len(calls) != 0 {
		t.Fatalf("loader was called %d times", len(calls))
	}
}

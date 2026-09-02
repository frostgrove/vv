package cache

import (
	"context"
	"errors"
	"testing"
)

type namingObserver struct {
	name   string
	seen   *[]string
	panics bool
}

func (this *namingObserver) Observe(_ context.Context, _ Event) {
	*this.seen = append(*this.seen, this.name)
	if this.panics {
		panic("observer")
	}
}

func TestEveryComposedObserverRunsInOrderThroughAPanickingChild(t *testing.T) {
	seen := make([]string, 0, 3)
	observer, err := Observers(
		&namingObserver{name: "first", seen: &seen},
		&namingObserver{name: "second", seen: &seen, panics: true},
		&namingObserver{name: "third", seen: &seen},
	)
	if err != nil {
		t.Fatalf("Observers() error = %v", err)
	}

	observer.Observe(context.Background(), Event{Operation: LookupOperation, Outcome: MissOutcome})

	if len(seen) != 3 || seen[0] != "first" || seen[1] != "second" || seen[2] != "third" {
		t.Fatalf("observers ran as %v, want first, second, third", seen)
	}
}

func TestAnObserverFanOutRefusesMoreChildrenThanItAdmits(t *testing.T) {
	seen := make([]string, 0)
	children := make([]Observer, 0, MaxObservers+1)
	for index := 0; index <= MaxObservers; index++ {
		children = append(children, &namingObserver{name: "child", seen: &seen})
	}

	if _, err := Observers(children...); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Observers() error = %v, want a refusal", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustObservers accepted an over-limit list")
		}
	}()
	MustObservers(children...)
}

func TestAnObserverFanOutSkipsAbsentChildren(t *testing.T) {
	seen := make([]string, 0, 1)
	var absent *namingObserver
	observer := MustObservers(absent, nil, &namingObserver{name: "only", seen: &seen})

	observer.Observe(context.Background(), Event{Operation: PutOperation, Outcome: StoredOutcome})

	if len(seen) != 1 || seen[0] != "only" {
		t.Fatalf("observers ran as %v, want only", seen)
	}
}

func TestAComposedObserverReachesTheCacheRuntime(t *testing.T) {
	seen := make([]string, 0, 2)
	policy := newCacheTestPolicy(64)
	backend := newSeamBackend(policy)
	cache, _ := newBatchTestCache(t, backend, Bytes(1), policy, MustObservers(
		&namingObserver{name: "first", seen: &seen, panics: true},
		&namingObserver{name: "second", seen: &seen},
	))

	if _, err := cache.Lookup(context.Background(), "absent"); err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	if len(seen) != 2 || seen[1] != "second" {
		t.Fatalf("observers ran as %v, want both", seen)
	}
}

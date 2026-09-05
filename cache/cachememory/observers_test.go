package cachememory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
)

type namingMemoryObserver struct {
	name   string
	seen   *[]string
	panics bool
}

func (this *namingMemoryObserver) Observe(_ context.Context, _ cachememory.Event) {
	*this.seen = append(*this.seen, this.name)
	if this.panics {
		panic("memory observer")
	}
}

func TestEveryComposedMemoryObserverRunsInOrderThroughAPanickingChild(t *testing.T) {
	seen := make([]string, 0, 3)
	observer, err := cachememory.Observers(
		&namingMemoryObserver{name: "first", seen: &seen},
		&namingMemoryObserver{name: "second", seen: &seen, panics: true},
		&namingMemoryObserver{name: "third", seen: &seen},
	)
	if err != nil {
		t.Fatalf("Observers() error = %v", err)
	}

	observer.Observe(context.Background(), cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.MissOutcome})

	if len(seen) != 3 || seen[0] != "first" || seen[1] != "second" || seen[2] != "third" {
		t.Fatalf("observers ran as %v, want first, second, third", seen)
	}
}

func TestAMemoryObserverFanOutRefusesMoreChildrenThanItAdmits(t *testing.T) {
	seen := make([]string, 0)
	children := make([]cachememory.Observer, 0, cachememory.MaxObservers+1)
	for index := 0; index <= cachememory.MaxObservers; index++ {
		children = append(children, &namingMemoryObserver{name: "child", seen: &seen})
	}

	if _, err := cachememory.Observers(children...); !errors.Is(err, cache.ErrTooLarge) {
		t.Fatalf("Observers() error = %v, want a refusal", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustObservers accepted an over-limit list")
		}
	}()
	cachememory.MustObservers(children...)
}

func TestAMemoryObserverFanOutSkipsAbsentChildren(t *testing.T) {
	seen := make([]string, 0, 1)
	var absent *namingMemoryObserver
	observer := cachememory.MustObservers(absent, nil, &namingMemoryObserver{name: "only", seen: &seen})

	observer.Observe(context.Background(), cachememory.Event{Operation: cachememory.PutOperation, Outcome: cachememory.StoredOutcome})

	if len(seen) != 1 || seen[0] != "only" {
		t.Fatalf("observers ran as %v, want only", seen)
	}
}

package jobs

import (
	"context"
	"errors"
	"testing"
)

type namingWorkerObserver struct {
	name   string
	seen   *[]string
	panics bool
}

func (this *namingWorkerObserver) Observe(_ context.Context, _ WorkerEvent) {
	*this.seen = append(*this.seen, this.name)
	if this.panics {
		panic("worker observer")
	}
}

func TestEveryComposedWorkerObserverRunsInOrderThroughAPanickingChild(t *testing.T) {
	seen := make([]string, 0, 3)
	observer, err := WorkerObservers(
		&namingWorkerObserver{name: "first", seen: &seen},
		&namingWorkerObserver{name: "second", seen: &seen, panics: true},
		&namingWorkerObserver{name: "third", seen: &seen},
	)
	if err != nil {
		t.Fatalf("WorkerObservers() error = %v", err)
	}

	observer.Observe(context.Background(), WorkerEvent{})

	if len(seen) != 3 || seen[0] != "first" || seen[1] != "second" || seen[2] != "third" {
		t.Fatalf("observers ran as %v, want first, second, third", seen)
	}
}

func TestAWorkerObserverFanOutRefusesMoreChildrenThanItAdmits(t *testing.T) {
	seen := make([]string, 0)
	children := make([]WorkerObserver, 0, MaxWorkerObservers+1)
	for index := 0; index <= MaxWorkerObservers; index++ {
		children = append(children, &namingWorkerObserver{name: "child", seen: &seen})
	}

	if _, err := WorkerObservers(children...); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("WorkerObservers() error = %v, want a refusal", err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustWorkerObservers accepted an over-limit list")
		}
	}()
	MustWorkerObservers(children...)
}

func TestAWorkerObserverFanOutSkipsAbsentChildren(t *testing.T) {
	seen := make([]string, 0, 1)
	var absent *namingWorkerObserver
	observer := MustWorkerObservers(absent, nil, &namingWorkerObserver{name: "only", seen: &seen})

	observer.Observe(context.Background(), WorkerEvent{})

	if len(seen) != 1 || seen[0] != "only" {
		t.Fatalf("observers ran as %v, want only", seen)
	}
}

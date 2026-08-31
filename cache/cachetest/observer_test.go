package cachetest

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/cache"
)

func TestObserverRecordsWaitsAndResets(t *testing.T) {
	observer := NewObserver()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := observer.Wait(ctx, 2)
		done <- err
	}()
	observer.Observe(context.Background(), cacheEvent(1))
	observer.Observe(context.Background(), cacheEvent(2))
	if err := receive(t, done, "observer events"); err != nil {
		t.Fatal(err)
	}
	events := observer.Events()
	if len(events) != 2 || events[0].Items != 1 || events[1].Items != 2 {
		t.Fatalf("events = %+v", events)
	}
	observer.Reset()
	if len(observer.Events()) != 0 {
		t.Fatalf("events after reset = %+v", observer.Events())
	}
}

func TestObserverWaitCancellation(t *testing.T) {
	observer := &Observer{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := observer.Wait(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v", err)
	}
}

func cacheEvent(items int) cache.Event {
	return cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome, Items: items}
}

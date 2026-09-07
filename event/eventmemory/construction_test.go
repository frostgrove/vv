package eventmemory_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

func TestEveryNumberAStoreAndItsLogTakeIsBoundedAtBothEnds(t *testing.T) {
	t.Run("a store writes to a log, and a spec that names none is refused", func(t *testing.T) {
		if _, err := eventmemory.New(eventmemory.Spec{}); !errors.Is(err, event.ErrWrongStore) {
			t.Fatalf("a store over no log at all answered %v", err)
		}
	})

	t.Run("a number the log takes is refused below zero and above the kernel's ceiling", func(t *testing.T) {
		for _, refused := range []struct {
			what string
			spec eventmemory.LogSpec
		}{
			{"a payload bound below zero", eventmemory.LogSpec{MaxPayload: -1}},
			{"a key bound below zero", eventmemory.LogSpec{MaxKey: -1}},
			{"a payload bound above the kernel's ceiling", eventmemory.LogSpec{MaxPayload: event.MaxPayloadBytes + 1}},
			{"a key bound above the kernel's ceiling", eventmemory.LogSpec{MaxKey: event.MaxKeyBytes + 1}},
		} {
			if _, err := eventmemory.NewLog(refused.spec); !errors.Is(err, event.ErrWrongStore) {
				t.Fatalf("a log over %s answered %v, and a bound the kernel would refuse at the door makes every write through it unreadable", refused.what, err)
			}
		}
		limits := newStore(t, eventmemory.Spec{
			Log: newLog(t, eventmemory.LogSpec{MaxPayload: event.MaxPayloadBytes, MaxKey: event.MaxKeyBytes}),
		}).Limits()
		if limits.MaxPayload != event.MaxPayloadBytes || limits.MaxKey != event.MaxKeyBytes {
			t.Fatalf("a log at exactly the two ceilings publishes %d and %d, so the refusals above are not about the ceiling", limits.MaxPayload, limits.MaxKey)
		}
	})

	t.Run("a number the store takes is refused below zero and above the kernel's ceiling", func(t *testing.T) {
		log := newLog(t, eventmemory.LogSpec{MaxPayload: event.MaxResidentBytes / event.MaxPageCount})
		for _, refused := range []struct {
			what string
			spec eventmemory.Spec
		}{
			{"a batch bound below zero", eventmemory.Spec{Log: log, MaxBatch: -1}},
			{"a stream page below zero", eventmemory.Spec{Log: log, StreamPage: -1}},
			{"a read cap below zero", eventmemory.Spec{Log: log, MaxRead: -1}},
			{"a batch bound above the kernel's ceiling", eventmemory.Spec{Log: log, MaxBatch: event.MaxBatchCount + 1}},
			{"a stream page above the kernel's ceiling", eventmemory.Spec{Log: log, StreamPage: event.MaxPageCount + 1}},
			{"a read cap above the kernel's ceiling", eventmemory.Spec{Log: log, MaxRead: event.MaxPageCount + 1}},
		} {
			if _, err := eventmemory.New(refused.spec); !errors.Is(err, event.ErrWrongStore) {
				t.Fatalf("a store over %s answered %v, so it was built over a number the kernel refuses at the door it is bound through, with every read and every append behind it", refused.what, err)
			}
		}
		limits := newStore(t, eventmemory.Spec{
			Log:        log,
			MaxBatch:   event.MaxBatchCount,
			StreamPage: event.MaxPageCount,
			MaxRead:    event.MaxPageCount,
		}).Limits()
		if limits.MaxBatch != event.MaxBatchCount || limits.StreamPage != event.MaxPageCount || limits.MaxRead != event.MaxPageCount {
			t.Fatalf("a store at exactly the three ceilings publishes %+v, so the refusals above are not about the ceiling", limits)
		}
	})

	t.Run("a number left at zero is the store's own default and one the caller set is published as it was set", func(t *testing.T) {
		byDefault := newStore(t, eventmemory.Spec{Log: newLog(t, eventmemory.LogSpec{})}).Limits()
		want := event.Limits{MaxPayload: 64 << 10, MaxBatch: 64, MaxKey: 512, StreamPage: 256, MaxRead: 256}
		if byDefault != want {
			t.Fatalf("a log and a store with nothing set publish %+v where the defaults are %+v", byDefault, want)
		}
		chosen := newStore(t, eventmemory.Spec{
			Log:        newLog(t, eventmemory.LogSpec{MaxPayload: 8192, MaxKey: 128}),
			MaxBatch:   7,
			StreamPage: 9,
			MaxRead:    11,
		}).Limits()
		if asked := (event.Limits{MaxPayload: 8192, MaxBatch: 7, MaxKey: 128, StreamPage: 9, MaxRead: 11}); chosen != asked {
			t.Fatalf("a store asked for %+v publishes %+v, and the kernel enforces what it publishes rather than what it was asked", asked, chosen)
		}
	})
}

func TestAStoreIsBuiltPerRequestOverALogThatOutlivesIt(t *testing.T) {
	ctx := context.Background()
	log := newLog(t, eventmemory.LogSpec{})
	stream := streamOf("catalog.product", "acme/A-17")
	const requests = 200

	time.Sleep(time.Millisecond)
	before := runtime.NumGoroutine()

	for request := range requests {
		store := newStore(t, eventmemory.Spec{Log: log})
		appendTo(t, ctx, store, stream, event.Version(request), fmt.Sprintf("request %d", request))
		if err := store.Close(); err != nil {
			t.Fatalf("closing the store of request %d answered %v", request, err)
		}
	}

	if running := settled(before); running > before {
		t.Fatalf("%d goroutines are running where %d were before %d stores were constructed, so a constructor started something the caller can neither see nor stop",
			running, before, requests)
	}

	page := drainStream(t, ctx, newStore(t, eventmemory.Spec{Log: log}), stream)
	if len(page) != requests {
		t.Fatalf("a store built after %d per-request stores had written reads %d events, and it looked the history up in no other way than being built over the log",
			requests, len(page))
	}
	for index, envelope := range page {
		if envelope.Version != event.Version(index)+1 || string(envelope.Payload) != fmt.Sprintf("request %d", index) {
			t.Fatalf("the event at version %d reads %q, so a store closing took the history of the requests before it with it",
				envelope.Version, envelope.Payload)
		}
	}
}

func settled(want int) int {
	for range 200 {
		running := runtime.NumGoroutine()
		if running <= want {
			return running
		}
		time.Sleep(time.Millisecond)
	}
	return runtime.NumGoroutine()
}

package eventmemory_test

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

func TestManyWritersLeaveOneDenseHistoryAndAMonotoneLog(t *testing.T) {
	ctx := context.Background()
	log := newLog(t, eventmemory.LogSpec{})
	store := newStore(t, eventmemory.Spec{Log: log, StreamPage: 8, MaxRead: 8, Clock: ticking(decided, time.Millisecond)})
	streams := []event.Stream{streamOf("orders.order", "acme/one"), streamOf("orders.order", "acme/two"), streamOf("orders.order", "acme/three")}

	const writers, rounds, attempts = 6, 20, 500
	type record struct {
		admitted int
		failure  error
	}
	written := make([]record, writers)

	var released, working sync.WaitGroup
	released.Add(1)
	for writer := range writers {
		working.Add(1)
		go func() {
			defer working.Done()
			released.Wait()
			for round := range rounds {
				stream := streams[(writer+round)%len(streams)]
				request := event.AppendRequest{Stream: stream, Records: records(fmt.Sprintf("writer %d round %d", writer, round))}
				for attempt := 0; ; attempt++ {
					if attempt == attempts {
						written[writer].failure = fmt.Errorf("writer %d gave up on %v after %d refusals", writer, stream, attempt)
						return
					}
					at, err := endOf(ctx, store, stream)
					if err != nil {
						written[writer].failure = fmt.Errorf("writer %d could not read %v: %w", writer, stream, err)
						return
					}
					request.Expected = at
					refused, err := appendAsWriter(ctx, store, request, writer%2 == 0)
					if err != nil {
						written[writer].failure = err
						return
					}
					if refused {
						runtime.Gosched()
						continue
					}
					written[writer].admitted++
					break
				}
			}
		}()
	}

	stop := make(chan struct{})
	var reading sync.WaitGroup
	var tiled atomic.Int64
	var readerFailure error
	reading.Add(1)
	go func() {
		defer reading.Done()
		cursor := event.Cursor("")
		var highest event.Position
		for {
			page, next, err := store.ReadAll(ctx, cursor)
			if err != nil {
				readerFailure = fmt.Errorf("a reader tiling the log while it was written to was refused: %w", err)
				return
			}
			for _, envelope := range page {
				if envelope.Position <= highest {
					readerFailure = fmt.Errorf("a reader that had already been handed position %d was then handed %d, so an event became visible below one it had checkpointed past",
						highest, envelope.Position)
					return
				}
				highest = envelope.Position
			}
			tiled.Add(int64(len(page)))
			cursor = next
			if len(page) > 0 {
				continue
			}
			select {
			case <-stop:
				return
			default:
				runtime.Gosched()
			}
		}
	}()

	released.Done()
	working.Wait()
	close(stop)
	reading.Wait()

	admitted := 0
	for writer, got := range written {
		if got.failure != nil {
			t.Fatalf("%v", got.failure)
		}
		if got.admitted != rounds {
			t.Fatalf("writer %d landed %d of its %d decisions", writer, got.admitted, rounds)
		}
		admitted += got.admitted
	}
	if readerFailure != nil {
		t.Fatalf("%v", readerFailure)
	}
	if tiled.Load() == 0 {
		t.Fatalf("the reader tiling the log while the writers ran saw nothing, so what it asserted about positions was asserted over an empty log")
	}

	global := drainLog(t, ctx, store)
	if len(global) != admitted {
		t.Fatalf("the log holds %d events where %d appends were admitted, so a write was lost or one was published twice", len(global), admitted)
	}

	var highest event.Position
	for _, envelope := range global {
		if envelope.Position <= highest {
			t.Fatalf("the log holds position %d after %d, so two concurrent commits took one position or reordered", envelope.Position, highest)
		}
		highest = envelope.Position
	}

	perStream := 0
	for _, stream := range streams {
		page := drainStream(t, ctx, store, stream)
		perStream += len(page)
		for index, envelope := range page {
			if envelope.Version != event.Version(index)+1 {
				t.Fatalf("%v holds versions %v after concurrent writers, so a version was skipped or reused", stream, versionsOf(page))
			}
			if envelope.Stream != stream {
				t.Fatalf("%v holds an envelope of %v", stream, envelope.Stream)
			}
		}
		if !slices.Equal(payloadsOf(page), payloadsOfStreamIn(global, stream)) {
			t.Fatalf("%v reads as %v of its own and as %v of the log, so one stream is ordered differently by the two reads",
				stream, payloadsOf(page), payloadsOfStreamIn(global, stream))
		}
	}
	if perStream != len(global) {
		t.Fatalf("the three streams hold %d events between them and the log holds %d, so an event is visible to one read and not the other", perStream, len(global))
	}

	decisions := payloadsOf(global)
	for writer := range writers {
		for round := range rounds {
			decided := fmt.Sprintf("writer %d round %d", writer, round)
			if count := countOf(decisions, decided); count != 1 {
				t.Fatalf("%q was committed %d times", decided, count)
			}
		}
	}
}

func appendAsWriter(ctx context.Context, store *eventmemory.Store, request event.AppendRequest, transactional bool) (bool, error) {
	if !transactional {
		return store.Append(ctx, request) != nil, nil
	}
	tx, err := store.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("beginning a transaction was refused: %w", err)
	}
	if store.Append(eventmemory.WithTransaction(ctx, tx), request) != nil {
		if err := tx.Rollback(ctx); err != nil {
			return true, fmt.Errorf("rolling back a transaction nothing was staged in was refused: %w", err)
		}
		return true, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("committing an append the store had admitted was refused: %w", err)
	}
	return false, nil
}

func endOf(ctx context.Context, store *eventmemory.Store, stream event.Stream) (event.Version, error) {
	var at event.Version
	for range drainCeiling {
		page, err := store.ReadStream(ctx, stream, at)
		if err != nil {
			return 0, err
		}
		if len(page) == 0 {
			return at, nil
		}
		at += event.Version(len(page))
	}
	return 0, fmt.Errorf("reading %v page by page never reached the end", stream)
}

func payloadsOfStreamIn(page []event.Envelope, stream event.Stream) []string {
	var held []string
	for _, envelope := range page {
		if envelope.Stream == stream {
			held = append(held, string(envelope.Payload))
		}
	}
	return held
}

func countOf(held []string, wanted string) int {
	count := 0
	for _, one := range held {
		if one == wanted {
			count++
		}
	}
	return count
}

package projection_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

var errReadModelAway = errors.New("the read model is unreachable")

// Every page a handler was handed, in order, so a case reads what the second
// attempt got rather than what the framework says it sent.
type delivered struct {
	mutex    sync.Mutex
	attempts []int
	pages    [][]string
}

func (this *delivered) took(batch projection.Batch) int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.attempts = append(this.attempts, batch.Attempt)
	this.pages = append(this.pages, payloadsOf(batch.Envelopes))
	return len(this.attempts)
}

func (this *delivered) page(index int) []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if index >= len(this.pages) {
		return nil
	}
	return this.pages[index]
}

// Whether an envelope was ever handed to the handler on its own, which is the
// isolation pass's shape: the whole-page attempt that DISCOVERS a permanent
// failure carries every envelope of the page and rolls back as one, and what the
// queue promises is that from the moment the failure is known, an envelope
// behind a parked one is never delivered again.
func (this *delivered) redelivered(payload string) bool {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for _, page := range this.pages {
		if len(page) == 1 && page[0] == payload {
			return true
		}
	}
	return false
}

// Whether an envelope ever reached the handler at all, which is what a park
// promises for a sequence the queue was already holding.
func (this *delivered) reached(payload string) bool {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for _, page := range this.pages {
		for _, held := range page {
			if held == payload {
				return true
			}
		}
	}
	return false
}

func (this *delivered) count() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return len(this.attempts)
}

// The delays are the spec's, doubling and capped, and they are proved by the
// ticker rather than slept through. The ownership control beside it is the one
// that would otherwise pass vacuously: a handler is granted the page it holds,
// so a framework handing the same slice back applies the second attempt over
// what the first left behind and advances past events nobody ever saw.
func TestARetryReAppliesTheLogsOwnPageAfterABackoff(t *testing.T) {
	t.Run("the same page, after First and then twice First", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one", "two")

		var seen delivered
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			if seen.took(batch) <= 2 {
				return errReadModelAway
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		running(t, newProjection(t, spec))

		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		for _, expected := range []struct {
			attempt int
			delay   time.Duration
		}{{1, 250 * time.Millisecond}, {2, 500 * time.Millisecond}} {
			stand.ticks.expect(t, expected.delay, "the backoff after a retryable handler failure")
			retrying := stand.observer.await(t, "it is retrying", func(state projection.State) bool {
				return state.Phase == projection.PhaseRetrying && state.Attempt == expected.attempt+1
			})
			if !errors.Is(retrying.Err, errReadModelAway) {
				t.Fatalf("attempt %d was published with %v, where a retry carries the handler's own failure", expected.attempt, retrying.Err)
			}
			if row := stand.row(t, "orders"); !row.Fresh() {
				t.Fatalf("the checkpoint is at advance %d after attempt %d failed, and a projection advances only over a page it finished with", row.Advance, expected.attempt)
			}
			if reads := stand.read.reads.Load(); reads != 1 {
				t.Fatalf("%d reads were issued by attempt %d, and a retry re-applies the page it holds rather than reading again", reads, expected.attempt)
			}
			stand.ticks.fire(t)
		}

		stand.observer.await(t, "the page applied", func(state projection.State) bool {
			return state.Progress.Applied == 2
		})
		if seen.count() != 3 {
			t.Fatalf("the handler was called %d times where two failures and one success are three", seen.count())
		}
		for attempt := range 3 {
			if page := seen.page(attempt); !same(page, []string{"one", "two"}) {
				t.Fatalf("attempt %d was handed %v where the log's page is one, two", attempt+1, page)
			}
		}
	})

	t.Run("the control: a handler that rewrites the page it was granted", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one", "two", "three")

		var seen delivered
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			if seen.took(batch) == 1 {
				batch.Envelopes[0], batch.Envelopes[2] = batch.Envelopes[2], batch.Envelopes[0]
				for _, envelope := range batch.Envelopes {
					for index := range envelope.Payload {
						envelope.Payload[index] = 'x'
					}
				}
				batch.Envelopes = batch.Envelopes[:1]
				return errReadModelAway
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		running(t, newProjection(t, spec))

		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		stand.ticks.expect(t, 250*time.Millisecond, "the backoff after a retryable handler failure")
		stand.ticks.fire(t)

		stand.observer.await(t, "the page applied", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})
		if rows := stand.model.rows(); !same(rows, []string{"one", "two", "three"}) {
			t.Fatalf("the second attempt applied %v, so it was handed the page the first attempt left behind", rows)
		}
		row := stand.row(t, "orders")
		if row.Progress.Highest != 3 {
			t.Fatalf("the advance the applied attempt saved reaches position %d where the log answered three envelopes, so the checkpoint passes events nobody applied", row.Progress.Highest)
		}
	})
}

// A redelivery after a restart is a new delivery rather than a retry, so it
// arrives at Attempt 1 again, and what makes it safe is the identity it carries
// rather than anything the framework remembers.
func TestARedeliveryCarriesTheSameIdentitiesInMemoryOfItsOwn(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "one", "two")

	var first delivered
	arrived := make(chan struct{}, 1)
	dying := stand.spec("orders", projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
		first.took(batch)
		for _, envelope := range batch.Envelopes {
			for index := range envelope.Payload {
				envelope.Payload[index] = 'x'
			}
		}
		arrived <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}))
	stop, returned := running(t, newProjection(t, dying))
	<-arrived
	stop()
	if err := returns(t, returned); !errors.Is(err, context.Canceled) {
		t.Fatalf("a projection cancelled inside its handler returned %v", err)
	}
	if row := stand.row(t, "orders"); !row.Fresh() {
		t.Fatalf("the checkpoint is at advance %d after a process that died inside the handler", row.Advance)
	}

	var second delivered
	resumed := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
		second.took(batch)
		stand.model.write(payloadsOf(batch.Envelopes)...)
		return nil
	}))
	running(t, newProjection(t, resumed))
	stand.observer.await(t, "the redelivery applied", func(state projection.State) bool {
		return state.Progress.Applied == 2
	})

	if first.attempts[0] != 1 || second.attempts[0] != 1 {
		t.Fatalf("the two deliveries arrived at attempts %d and %d, and a redelivery after a restart is a new delivery rather than a retry", first.attempts[0], second.attempts[0])
	}
	if !same(second.page(0), []string{"one", "two"}) {
		t.Fatalf("the redelivery was handed %v, so the second delivery reads bytes the first one wrote into", second.page(0))
	}
	if rows := stand.model.rows(); !same(rows, []string{"one", "two"}) {
		t.Fatalf("the redelivered page applied as %v", rows)
	}
}

// Sequence granularity is what a park buys, so the assertion is what the page
// kept: a page-granular policy loses up to a page of good events for one corrupt
// payload and passes every test that only counts halts, and an envelope-granular
// one applies the next event of the order whose first one failed.
func TestAPermanentFailureHaltsAndParkSequenceIsSequenceGranular(t *testing.T) {
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)
	refuses := func(payload string) func(context.Context, projection.Batch) error {
		return func(_ context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if string(envelope.Payload) == payload {
					return permanent
				}
			}
			return nil
		}
	}

	t.Run("under Halt the advance stands still", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one", "two", "three")

		refuse := refuses("two")
		spec := stand.spec("orders", projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
			if err := refuse(ctx, batch); err != nil {
				return err
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		running(t, newProjection(t, spec))

		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, event.ErrPayload) {
			t.Fatalf("a permanent handler failure halted with %v, where the handler's own failure travels on the halt", halted.Err)
		}
		if count := stand.observer.counted(projection.PhaseHalted); count != 1 {
			t.Fatalf("the halt was published %d times, and it is published once", count)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the checkpoint advanced to %d over a page nothing applied", row.Advance)
		}
		if rows := stand.model.rows(); len(rows) != 0 {
			t.Fatalf("%v was applied by a page that failed as a whole", rows)
		}
	})

	t.Run("under ParkSequence the page loses one sequence and no more", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")
		stand.append(t, "b", "two")
		stand.append(t, "c", "three")

		held := newPark()
		refuse := refuses("two")
		spec := stand.spec("orders", projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
			if err := refuse(ctx, batch); err != nil {
				return err
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		running(t, newProjection(t, stand.parking(spec, held)))

		stand.observer.await(t, "the page was accounted for", func(state projection.State) bool {
			return state.Progress.Quarantined == 1
		})
		if rows := stand.model.rows(); !same(rows, []string{"one", "three"}) {
			t.Fatalf("the isolation pass applied %v, where every envelope but the refused one is of another sequence and applies", rows)
		}
		took := held.letters(identityOf(t, "orders", projection.Ungenerated, projection.Whole()), sequenceOf("b"))
		if len(took) != 1 {
			t.Fatalf("the park holds %d letters for one envelope the handler refused", len(took))
		}
		if string(took[0].Envelope.Payload) != "two" || took[0].Sequencer != "by-stream" || !errors.Is(took[0].Cause, event.ErrPayload) {
			t.Fatalf("the park was handed %+v, where a letter carries the sequencer that named it, the envelope and the cause", took[0])
		}
		row := stand.row(t, "orders")
		if row.Advance != 1 {
			t.Fatalf("the checkpoint is at advance %d, and a page whose failure was parked advances it exactly once", row.Advance)
		}
		if row.Progress.Quarantined != 1 || row.Progress.Applied != 2 {
			t.Fatalf("the row records %d quarantined and %d applied, where one envelope of three was parked", row.Progress.Quarantined, row.Progress.Applied)
		}
		if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
			t.Fatal("a parked envelope halted the projection")
		}
	})

	t.Run("the control: a park that refuses halts instead of skipping", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")
		stand.append(t, "b", "two")

		held := newPark()
		held.refuses = errors.New("the park table is unreachable")
		refuse := refuses("two")
		spec := stand.spec("orders", projection.HandlerFunc(refuse))
		running(t, newProjection(t, stand.parking(spec, held)))

		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, projection.ErrHalted) {
			t.Fatalf("a park that refused answered %v", halted.Err)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the checkpoint advanced to %d over an envelope nothing recorded", row.Advance)
		}
	})

	t.Run("the control: a retryable failure inside the isolation pass parks nothing", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")
		stand.append(t, "b", "two")

		held := newPark()
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			if len(batch.Envelopes) > 1 {
				return permanent
			}
			if string(batch.Envelopes[0].Payload) == "two" {
				return errReadModelAway
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		running(t, newProjection(t, stand.parking(spec, held)))

		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		stand.ticks.expect(t, 250*time.Millisecond, "the backoff after the isolation pass met a retryable failure")
		retrying := stand.observer.await(t, "it is retrying", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying
		})
		if !errors.Is(retrying.Err, errReadModelAway) {
			t.Fatalf("the isolation pass returned the page to retrying with %v", retrying.Err)
		}
		if written := held.written.Load(); written != 0 {
			t.Fatalf("the park took %d letters over a failure a database that went away produced, and a retryable failure is not a corrupt payload", written)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the checkpoint advanced to %d over a page the isolation pass did not finish", row.Advance)
		}
	})
}

// What Ready measures is the backoff this projection has already waited in the
// streak it is inside, so one failure between two successes never reports
// unhealthy and a streak that outlasts Tolerate always does.
func TestASingleFailureFollowedByASuccessNeverReportsUnhealthy(t *testing.T) {
	ctx := context.Background()

	t.Run("one failure and then a success", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")

		var seen delivered
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			if seen.took(batch) == 1 {
				return errReadModelAway
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		spec.Tolerate = time.Second
		spec.Backoff = projection.Backoff{First: 250 * time.Millisecond, Max: 250 * time.Millisecond}
		held := newProjection(t, spec)
		running(t, held)

		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		stand.ticks.expect(t, 250*time.Millisecond, "the backoff after the failure")
		if err := held.Ready(ctx); err != nil {
			t.Fatalf("one failed attempt reported unhealthy with %v, and readiness must not flap on one transient error", err)
		}
		stand.ticks.fire(t)
		stand.observer.await(t, "the page applied", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})
		if err := held.Ready(ctx); err != nil {
			t.Fatalf("a projection that failed once and then applied reported %v", err)
		}
	})

	t.Run("a streak that outlasts Tolerate", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")

		spec := stand.spec("orders", projection.HandlerFunc(func(context.Context, projection.Batch) error {
			return errReadModelAway
		}))
		spec.Tolerate = time.Second
		spec.Backoff = projection.Backoff{First: 250 * time.Millisecond, Max: 250 * time.Millisecond}
		spec.Attempts = 100
		held := newProjection(t, spec)
		running(t, held)

		// The k-th backoff this projection waits out is followed by attempt k+1,
		// whose failure publishes attempt k+2 — so waiting for that state is what
		// makes the accumulated backoff a number the case can read rather than
		// one it races.
		waitedOut := func(beat int) {
			t.Helper()
			stand.ticks.expect(t, 250*time.Millisecond, "the backoff")
			stand.ticks.fire(t)
			stand.observer.await(t, "the next attempt failed", func(state projection.State) bool {
				return state.Phase == projection.PhaseRetrying && state.Attempt == beat+2
			})
		}
		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		for beat := range 3 {
			waitedOut(beat + 1)
		}
		if err := held.Ready(ctx); err != nil {
			t.Fatalf("750ms of backoff against a tolerance of a second reported %v", err)
		}
		for beat := range 2 {
			waitedOut(beat + 4)
		}
		err := held.Ready(ctx)
		if err == nil {
			t.Fatal("a projection that has waited 1.25s of backoff against a tolerance of a second reported healthy")
		}
		if !errors.Is(err, errReadModelAway) {
			t.Fatalf("the readiness answer is %v and does not carry what the projection is retrying over", err)
		}
	})
}

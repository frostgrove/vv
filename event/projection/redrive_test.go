package projection_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

// The operator's half, wired the way a redrive is: its own unit of work, on its
// own goroutine, over the same queue the loop writes to and the same read model
// the loop applies into.
func redriving(t *testing.T, held *stand, queue *park, destination *transaction, handler projection.Handler) *projection.Redrive {
	t.Helper()
	held.park = queue
	built, err := projection.NewRedrive(projection.RedriveSpec{
		Identity:    identityOf(t, "orders", projection.Ungenerated, projection.Whole()),
		Handler:     handler,
		Park:        queue,
		Unit:        held.operating(destination),
		Destination: readModel{pool: held},
	})
	if err != nil {
		t.Fatalf("a well-formed redrive was refused: %v", err)
	}
	return built
}

// Letters written the way the loop writes them, so a case that is about the
// redrive alone starts from the queue a failing page would have left.
func parked(t *testing.T, held *park, of projection.Identity, key string, payloads ...string) {
	t.Helper()
	envelopes := make([]event.Envelope, 0, len(payloads))
	for index, payload := range payloads {
		envelopes = append(envelopes, event.Envelope{
			Stream:   event.Stream{Family: "orders", Key: event.Key(key)},
			Version:  event.Version(index + 1),
			Position: event.Position(index + 1),
			Type:     "orders.placed",
			Payload:  []byte(payload),
		})
	}
	held.seed(of, "by-stream", sequenceOf(key), envelopes...)
}

// A sequence is a queue and not a set: its letters go back in insert order, one
// unit each, and what records the drain is the queue rather than the checkpoint
// — the fence admits one writer and the loop owns it.
func TestARedriveDrainsASequenceInInsertOrderAndTouchesNoCheckpoint(t *testing.T) {
	ctx := context.Background()

	t.Run("three letters, in order, and not one save", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "b", "B1")
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A2", "A3", "A4")

		one := &transaction{named: "one resource"}
		running(t, newProjection(t, stand.parking(stand.spec("orders", applies(stand.model)), held)))
		stand.observer.await(t, "the loop is following a queue that is holding one sequence", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded && state.Parked == 1
		})
		stand.ticks.expect(t, time.Second, "the poll the loop follows on")

		saves := stand.points.saves.Load()
		var seen delivered
		drained, err := redriving(t, stand, held, one, projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			seen.took(batch)
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		})).Sequence(ctx, sequenceOf("a"))
		if err != nil {
			t.Fatalf("draining a sequence whose cause was fixed answered %v", err)
		}
		if drained.Applied != 3 || drained.Left != 0 || drained.Cause != nil {
			t.Fatalf("the redrive answered %+v where three letters applied and none was left", drained)
		}
		if rows := stand.model.rows(); !same(rows, []string{"B1", "A2", "A3", "A4"}) {
			t.Fatalf("the read model holds %v, where the redrive applies the letters of a in the order they were parked", rows)
		}
		for attempt := range 3 {
			if page := seen.page(attempt); len(page) != 1 {
				t.Fatalf("the redrive handed the handler %d envelopes at once, and a redrive is one unit per letter", len(page))
			}
		}
		if written := stand.points.saves.Load(); written != saves {
			t.Fatalf("the redrive issued %d checkpoint saves, and the fence belongs to the loop", written-saves)
		}
		if letters := held.letters(of, sequenceOf("a")); len(letters) != 0 {
			t.Fatalf("the queue still holds %d letters of a sequence that drained", len(letters))
		}

		stand.ticks.fire(t)
		stand.observer.await(t, "the loop noticed the drain", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing && state.Parked == 0
		})
	})

	t.Run("the control: a sequence nobody parked answers nothing applied and no error", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		one := &transaction{named: "one resource"}

		drained, err := redriving(t, stand, held, one, applies(stand.model)).Sequence(ctx, sequenceOf("a"))
		if err != nil {
			t.Fatalf("draining an empty queue answered %v, and an operator whose drain found nothing has not failed", err)
		}
		if drained.Applied != 0 || drained.Cause != nil {
			t.Fatalf("draining an empty queue answered %+v", drained)
		}
	})
}

// The causal-order guarantee on the retry path: the letters behind the one that
// failed again stay where they are, so nothing of that sequence is applied over
// a read model that never received what came before it.
func TestARedriveStopsAtTheFirstLetterThatFailsAgain(t *testing.T) {
	ctx := context.Background()
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)
	refusing := func(payload string) projection.HandlerFunc {
		return func(_ context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if string(envelope.Payload) == payload {
					return permanent
				}
			}
			return nil
		}
	}

	t.Run("A2 applies, A3 is requeued, A4 is untouched", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A2", "A3", "A4")
		before := held.letters(of, sequenceOf("a"))

		var seen delivered
		drained, err := redriving(t, stand, held, &transaction{named: "one resource"}, projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
			seen.took(batch)
			if err := refusing("A3")(ctx, batch); err != nil {
				return err
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		})).Sequence(ctx, sequenceOf("a"))
		if err != nil {
			t.Fatalf("a redrive whose letter failed again answered the error %v, and the redrive did not fail — the letter did", err)
		}
		if drained.Applied != 1 || drained.Left != 2 || !errors.Is(drained.Cause, event.ErrPayload) {
			t.Fatalf("the redrive answered %+v where one letter applied, two were left and the cause is the letter's own", drained)
		}
		if seen.redelivered("A4") {
			t.Fatal("A4 reached the handler behind a letter that failed again, which is the ordering the queue exists for broken on its own retry path")
		}
		letters := held.letters(of, sequenceOf("a"))
		if len(letters) != 2 || string(letters[0].Envelope.Payload) != "A3" || string(letters[1].Envelope.Payload) != "A4" {
			t.Fatalf("the queue holds %d letters and does not begin at A3, so the stop did not leave the sequence where it was", len(letters))
		}
		if !errors.Is(letters[0].Cause, event.ErrPayload) || letters[0].Attempt <= before[1].Attempt {
			t.Fatalf("the requeued letter is %+v, where a letter that failed again carries its new cause and a raised attempt", letters[0])
		}
		if rows := stand.model.rows(); !same(rows, []string{"A2"}) {
			t.Fatalf("the read model holds %v where only the letter before the blocker applied", rows)
		}
	})

	t.Run("the control: the same sequence with its cause fixed drains fully", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A2", "A3", "A4")

		drained, err := redriving(t, stand, held, &transaction{named: "one resource"}, applies(stand.model)).Sequence(ctx, sequenceOf("a"))
		if err != nil || drained.Applied != 3 || drained.Left != 0 {
			t.Fatalf("the same sequence with nothing failing answered %+v and %v, so the stop above is the failure's and not the redrive's", drained, err)
		}
	})
}

// A loop that always picked the same failing sequence would starve every other
// one, and one that picked at random would lose the fairness the rotation buys.
func TestARedriveRotatesByLeastRecentlyTried(t *testing.T) {
	ctx := context.Background()
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	t.Run("three sequences, one of them failing", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A1")
		parked(t, held, of, "b", "B1")
		parked(t, held, of, "c", "C1")

		redrive := redriving(t, stand, held, &transaction{named: "one resource"}, projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			if batch.Envelopes[0].Stream.Key == "a" {
				return permanent
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		var taken []string
		for range 4 {
			drained, err := redrive.Any(ctx)
			if err != nil {
				t.Fatalf("a rotation answered %v", err)
			}
			taken = append(taken, drained.Sequence)
		}
		if !same(taken, []string{sequenceOf("a"), sequenceOf("b"), sequenceOf("c"), sequenceOf("a")}) {
			t.Fatalf("four rotations took %v, where the failing sequence is retried once per rotation rather than every call", taken)
		}
		if rows := stand.model.rows(); !same(rows, []string{"B1", "C1"}) {
			t.Fatalf("the read model holds %v, where the two fixable sequences drained", rows)
		}
	})

	t.Run("the control: one failing sequence comes back every time", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A1")

		redrive := redriving(t, stand, held, &transaction{named: "one resource"}, projection.HandlerFunc(func(context.Context, projection.Batch) error {
			return permanent
		}))
		for range 3 {
			drained, err := redrive.Any(ctx)
			if err != nil || drained.Sequence != sequenceOf("a") {
				t.Fatalf("a rotation over one sequence answered %+v and %v, so the rotation above is visible only when there is something to rotate", drained, err)
			}
		}
	})
}

// A claim is what makes a redrive exclusive, and an exclusion that is only a read
// is none: two callers would take the same least-recently-tried sequence, load
// the same letters, and apply from the first, so the third lands before the
// second finished.
func TestTwoGatedRedrivesNeverProcessOneSequence(t *testing.T) {
	ctx := context.Background()

	t.Run("two callers, gated so the contention is caused", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		for _, key := range []string{"a", "b", "c"} {
			parked(t, held, of, key, key+"1", key+"2")
		}

		arrived := make(chan struct{}, 2)
		release := make(chan struct{})
		held.gate = func() {
			arrived <- struct{}{}
			<-release
		}

		var applied sync.Map
		var twice []string
		var guard sync.Mutex
		redrive := redriving(t, stand, held, &transaction{named: "one resource"}, projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			payload := string(batch.Envelopes[0].Payload)
			if _, seen := applied.LoadOrStore(payload, true); seen {
				guard.Lock()
				twice = append(twice, payload)
				guard.Unlock()
			}
			return nil
		}))

		answers := make(chan projection.Retried, 2)
		failures := make(chan error, 2)
		for range 2 {
			go func() {
				drained, err := redrive.Any(ctx)
				failures <- err
				answers <- drained
			}()
		}
		for range 2 {
			select {
			case <-arrived:
			case <-time.After(settle):
				t.Fatal("both callers never reached the claim, so the contention was never caused")
			}
		}
		close(release)

		var taken []string
		for range 2 {
			if err := <-failures; err != nil {
				t.Fatalf("a gated redrive answered %v", err)
			}
			taken = append(taken, (<-answers).Sequence)
		}
		if taken[0] == taken[1] {
			t.Fatalf("both callers took %q, and a claim that admits two callers is a read with a longer name", taken[0])
		}
		guard.Lock()
		defer guard.Unlock()
		if len(twice) != 0 {
			t.Fatalf("%v were applied twice by two operators draining at once", twice)
		}
		if outstanding := held.claims(of); len(outstanding) != 0 {
			t.Fatalf("the grants %v were never released, and a sequence nobody released is undrainable until the application's clock expires it", outstanding)
		}
	})

	t.Run("a handler that panics releases the grant on its way out", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A1")

		redrive := redriving(t, stand, held, &transaction{named: "one resource"}, projection.HandlerFunc(func(context.Context, projection.Batch) error {
			panic("the read model's driver panicked")
		}))
		func() {
			defer func() {
				if recovered := recover(); recovered == nil {
					t.Error("a handler that panicked inside a redrive did not reach the operator, and a redrive runs on the operator's own goroutine")
				}
			}()
			_, _ = redrive.Any(ctx)
		}()
		if outstanding := held.claims(of); len(outstanding) != 0 {
			t.Fatalf("the grants %v outlived a panic, so the sequence is undrainable until the application's clock expires them", outstanding)
		}
	})

	t.Run("the control: one caller alone drains the sequence", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A1", "A2")

		drained, err := redriving(t, stand, held, &transaction{named: "one resource"}, applies(stand.model)).Any(ctx)
		if err != nil || drained.Applied != 2 {
			t.Fatalf("one caller alone answered %+v and %v, so the claim costs the single-operator case something", drained, err)
		}
	})
}

// The failure two simultaneous Claims cannot see. A grant expires by the
// application's clock, so it travels on every write it authorises: the Evict of
// the second letter is refused, its whole unit rolls back, and the caller that
// lost the sequence issues no Touch and no Release over a grant it does not
// hold.
func TestAnExpiredClaimAppliesEvictsAndReleasesNothing(t *testing.T) {
	ctx := context.Background()

	t.Run("expired between letters and re-granted", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A2", "A3", "A4")

		rows := &ledger{}
		var applied []string
		var second projection.Claim
		redrive := redriving(t, stand, held, &transaction{named: "one resource"}, projection.HandlerFunc(func(ctx context.Context, batch projection.Batch) error {
			payload := string(batch.Envelopes[0].Payload)
			applied = append(applied, payload)
			if payload == "A3" {
				held.expire(of, sequenceOf("a"))
				granted, found, err := held.Claim(ctx, of, sequenceOf("a"))
				if err != nil || !found {
					t.Errorf("the sequence could not be re-granted after its claim expired: %v", err)
				}
				second = granted
			}
			rows.write(ctx, payload)
			return nil
		}))

		drained, err := redrive.Sequence(ctx, sequenceOf("a"))
		if !errors.Is(err, projection.ErrClaimLost) {
			t.Fatalf("a redrive whose claim expired mid-drain answered %v, and a lost claim is the operation failing rather than the letter failing", err)
		}
		if drained.Applied != 1 || drained.Cause != nil {
			t.Fatalf("the redrive answered %+v, where it carries what it applied before the loss and no letter's cause", drained)
		}
		if !same(applied, []string{"A2", "A3"}) {
			t.Fatalf("the handler was called for %v, where the loss is discovered at the eviction of A3", applied)
		}
		if got := rows.rows(); !same(got, []string{"A2"}) {
			t.Fatalf("the read model holds %v, where A3's unit rolled back with the eviction its grant no longer authorised", got)
		}
		letters := held.letters(of, sequenceOf("a"))
		if len(letters) != 2 || string(letters[0].Envelope.Payload) != "A3" {
			t.Fatalf("the queue holds %d letters and does not begin at A3, so the letter the loser applied was evicted after all", len(letters))
		}
		if letters[0].Attempt != 1 {
			t.Fatalf("the first letter is at attempt %d, and a caller that lost its grant issues no Touch over a sequence it does not hold", letters[0].Attempt)
		}
		if outstanding := held.claims(of); len(outstanding) != 1 || outstanding[0] != second.Token {
			t.Fatalf("the grants outstanding are %v where the second caller's %q is the one that survives — a loser that released would free the winner's sequence", outstanding, second.Token)
		}
		if released := held.released.Load(); released != 0 {
			t.Fatalf("Release was called %d times over a grant this caller had already lost, and the suppression is this package's: nothing in the Redriver contract makes Release compare the token, and against the idempotent \"delete the row for this sequence\" the call would free the winner's sequence mid-drain", released)
		}
	})

	t.Run("the control: the same run with no expiry drains three and releases once", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A2", "A3", "A4")

		rows := &ledger{}
		drained, err := redriving(t, stand, held, &transaction{named: "one resource"}, appliesInto(rows)).Sequence(ctx, sequenceOf("a"))
		if err != nil || drained.Applied != 3 {
			t.Fatalf("the identical run with no expiry answered %+v and %v, so the refusal above is the expiry's and not the fixture's", drained, err)
		}
		if got := rows.rows(); !same(got, []string{"A2", "A3", "A4"}) {
			t.Fatalf("the read model holds %v where three letters drained in order", got)
		}
		if outstanding := held.claims(of); len(outstanding) != 0 {
			t.Fatalf("the grants %v outlived a drain that finished", outstanding)
		}
		if released := held.released.Load(); released != 1 {
			t.Fatalf("Release was called %d times over a drain that finished, where a claim is taken once and given back once — so the zero above is the loss and not the fixture", released)
		}
	})
}

// The ordering a letter's place in its sequence encodes is not the ordering
// another key would give it, and a park is keyed by the projection and its
// generation with the partition dropped — so a redrive is generation-wide.
func TestARedriveNamingAnotherSequencerOrAPartitionIsRefused(t *testing.T) {
	ctx := context.Background()

	t.Run("letters parked under another sequencer", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		held.seed(of, "by-order", "order-7", event.Envelope{
			Stream:   event.Stream{Family: "orders", Key: "a"},
			Position: 1,
			Payload:  []byte("A2"),
		})

		var called int
		redrive, err := projection.NewRedrive(projection.RedriveSpec{
			Identity:    of,
			Handler:     projection.HandlerFunc(func(context.Context, projection.Batch) error { called++; return nil }),
			Sequencer:   projection.SequenceBy("by-customer", func(event.Envelope) string { return "customer-1" }),
			Park:        held,
			Unit:        stand.operating(&transaction{named: "one resource"}),
			Destination: readModel{pool: stand},
		})
		if err != nil {
			t.Fatalf("a well-formed redrive was refused: %v", err)
		}
		stand.park = held
		drained, err := redrive.Sequence(ctx, "order-7")
		if !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("a redrive across a sequencer change answered %v", err)
		}
		for _, named := range []string{"by-order", "by-customer"} {
			if !strings.Contains(err.Error(), named) {
				t.Fatalf("the refusal reads %q and does not name %s, so an operator cannot tell which key the queue was built under", err, named)
			}
		}
		if called != 0 {
			t.Fatalf("the handler was called %d times before the refusal", called)
		}
		if drained.Applied != 0 {
			t.Fatalf("the refused redrive reports %d applied", drained.Applied)
		}
		if letters := held.letters(of, "order-7"); len(letters) != 1 {
			t.Fatalf("the queue holds %d letters after a refusal that applied nothing", len(letters))
		}
	})

	t.Run("an identity carrying a partition", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		_, err := projection.NewRedrive(projection.RedriveSpec{
			Identity:    identityOf(t, "orders", projection.Ungenerated, partitionOf(t, 1, 3)),
			Handler:     applies(stand.model),
			Park:        held,
			Unit:        stand.operating(&transaction{named: "one resource"}),
			Destination: readModel{pool: stand},
		})
		if !errors.Is(err, projection.ErrTopology) {
			t.Fatalf("a redrive over a partitioned identity answered %v, and a park is keyed by the projection and its generation", err)
		}
		if !strings.Contains(err.Error(), "1.3") {
			t.Fatalf("the refusal reads %q and does not name the partition", err)
		}
	})

	t.Run("the control: the matching sequencer proceeds", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", projection.Ungenerated, projection.Whole())
		parked(t, held, of, "a", "A2")

		drained, err := redriving(t, stand, held, &transaction{named: "one resource"}, applies(stand.model)).Sequence(ctx, sequenceOf("a"))
		if err != nil || drained.Applied != 1 {
			t.Fatalf("a redrive naming the sequencer the letters were parked under answered %+v and %v", drained, err)
		}
	})
}

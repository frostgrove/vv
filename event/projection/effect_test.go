package projection_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

// The application's own effect sink. Its writes are staged in the unit of work
// the stand's Unit opened and land only when that unit commits, which is what a
// durable write inside the caller's transaction is — over a plain Go slice "the
// unit committed" and "the unit rolled back" hold the same rows, so a case about
// a rollback could not tell them apart.
//
// calls counts every entry and held records only what landed, so a case can say
// which of the three suppressors answered: a suppressed delivery makes no call
// at all, and a rolled-back one makes the call and lands nothing.
type sink struct {
	mutex sync.Mutex
	held  []projection.Effect

	calls   atomic.Int64
	outside atomic.Int64

	refuses error
}

func (this *sink) Stage(ctx context.Context, effect projection.Effect) error {
	this.calls.Add(1)
	this.mutex.Lock()
	refuses := this.refuses
	this.mutex.Unlock()
	if refuses != nil {
		return refuses
	}
	work := unitOfWorkIn(ctx)
	if work == nil {
		this.outside.Add(1)
		this.landed(effect)
		return nil
	}
	work.landing = append(work.landing, func() { this.landed(effect) })
	return nil
}

func (this *sink) landed(effect projection.Effect) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.held = append(this.held, effect)
}

func (this *sink) staged() []projection.Effect {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]projection.Effect(nil), this.held...)
}

// Every envelope that reached the sink, in the order it reached it, flattened
// across the deliveries: what an effect is owed for is an envelope, so most
// cases ask this and the ones about the split per delivery ask pages().
func (this *sink) payloads() []string {
	var held []string
	for _, effect := range this.staged() {
		held = append(held, payloadsOf(effect.Envelopes)...)
	}
	return held
}

func (this *sink) pages() [][]string {
	var held [][]string
	for _, effect := range this.staged() {
		held = append(held, payloadsOf(effect.Envelopes))
	}
	return held
}

func (this *sink) refusing(err error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.refuses = err
}

func samePages(left, right [][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !same(left[index], right[index]) {
			return false
		}
	}
	return true
}

// The one wiring Effects constructs at, and the whole of what a case has to say
// to get it: one transaction for the checkpoint store and the read model, and a
// sink that stages inside it.
func (this *stand) staging(spec projection.Spec, effects projection.Effects) projection.Spec {
	one := &transaction{named: "one resource"}
	spec = this.inUnit(spec, one, one, nil)
	spec.Effects = effects
	return spec
}

// A rebuild is a spec with the capability nil — there is nothing to withhold
// because there is nothing to hold — and the live generation beside it is the
// same spec with a sink. The pair is the whole of ES-06's headline, and the
// control is what separates the two suppressors from each other: with the row
// moved and the rebuild restarted CARRYING a sink, the rebuild stages and the
// live generation, still holding its own sink, stages nothing.
func TestTheLiveGenerationStagesAndTheRebuildDoesNot(t *testing.T) {
	ctx := context.Background()
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "A1", "A2")
	stand.append(t, "b", "B1")

	row := owning("orders", 1)
	live := &sink{}
	spec := stand.staging(stand.spec("orders", applies(stand.model)), live)
	spec.Generation, spec.Generations = 1, row
	running(t, newProjection(t, spec))
	stand.observer.await(t, "generation 1 applied the whole log", func(state projection.State) bool {
		return state.Identity.Generation() == 1 && state.Progress.Applied == 3
	})
	if staged := live.payloads(); !same(staged, []string{"A1", "A2", "B1"}) {
		t.Fatalf("the live generation staged %v, where an effect is owed for every envelope it applied past its barrier", staged)
	}

	reads := row.reads.Load()
	rebuilt := &model{}
	beside := newTicks()
	rebuild := stand.staging(stand.spec("orders", applies(rebuilt)), nil)
	rebuild.Generation, rebuild.Generations, rebuild.Ticks = 2, row, beside.Ticks
	cancel, returned := running(t, newProjection(t, rebuild))
	stand.observer.await(t, "generation 2 applied the whole log", func(state projection.State) bool {
		return state.Identity.Generation() == 2 && state.Progress.Applied == 3
	})

	if rows := rebuilt.rows(); !same(rows, []string{"A1", "A2", "B1"}) {
		t.Fatalf("the rebuild applied %v, so it is not the same log the live generation drained and the pair proves nothing", rows)
	}
	if staged := live.payloads(); !same(staged, []string{"A1", "A2", "B1"}) {
		t.Fatalf("the sink holds %v after the rebuild drained, where a rebuild with no capability stages nothing at all", staged)
	}
	if reads != row.reads.Load() {
		t.Fatalf("the rebuild read the ownership row %d times, where a nil Effects is the first suppressor and costs no round trip", row.reads.Load()-reads)
	}

	t.Run("the control: the row moves and the rebuild carries a sink", func(t *testing.T) {
		cancel()
		returns(t, returned)
		if err := row.Activate(ctx, "orders", 1, 2); err != nil {
			t.Fatalf("moving the read target from generation 1 to 2 answered %v", err)
		}
		stand.append(t, "a", "A4")

		arrived := &sink{}
		restarted := stand.staging(stand.spec("orders", applies(rebuilt)), arrived)
		restarted.Generation, restarted.Generations, restarted.Ticks = 2, row, beside.Ticks
		running(t, newProjection(t, restarted))
		stand.observer.await(t, "generation 2 applied the event that arrived after the cutover", func(state projection.State) bool {
			return state.Identity.Generation() == 2 && state.Progress.Applied == 4
		})

		stand.ticks.fire(t)
		stand.observer.await(t, "generation 1 applied the event that arrived after the cutover", func(state projection.State) bool {
			return state.Identity.Generation() == 1 && state.Progress.Applied == 4
		})

		if staged := arrived.payloads(); !same(staged, []string{"A4"}) {
			t.Fatalf("the arriving generation staged %v, where the row names it and it owns every envelope it applies", staged)
		}
		if staged := live.payloads(); !same(staged, []string{"A1", "A2", "B1"}) {
			t.Fatalf("the retiring generation staged %v, and it holds a sink — so the capability is what was measured above rather than the ownership row", staged)
		}
		if rows := stand.model.rows(); !same(rows, []string{"A1", "A2", "B1", "A4"}) {
			t.Fatalf("the retiring generation applied %v, and a generation that stopped staging goes on applying and goes on advancing", rows)
		}
	})
}

// The reference's own dual-write window, refused rather than documented: an
// effect staged outside the unit that commits the advance is a second write, and
// the outage that takes the sink away advances the checkpoint over an event
// nothing will ever send.
func TestEffectsAreRefusedBesideAfterApply(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})

	refused := stand.spec("orders", applies(stand.model))
	refused.Effects = &sink{}
	built, err := projection.New(refused)
	if err == nil {
		t.Fatal("a spec staging effects outside a unit of work was accepted, and what it assembles is the dual write the capability exists to close")
	}
	if built != nil {
		t.Fatal("a refused spec answered a projection as well as an error")
	}
	if !errors.Is(err, projection.ErrSpec) {
		t.Fatalf("Effects beside AfterApply was refused as %v, which is not the class a spec that cannot be assembled carries", err)
	}
	if !strings.Contains(err.Error(), "AfterApply") {
		t.Fatalf("the refusal reads %q and does not name the mode it is about", err)
	}

	t.Run("the control: the same sink at InUnit", func(t *testing.T) {
		if _, err := projection.New(stand.staging(stand.spec("orders", applies(stand.model)), &sink{})); err != nil {
			t.Fatalf("the same sink at the one tier it constructs at was refused: %v", err)
		}
	})
}

// The split is per envelope and never per page, and both halves of that are
// defects: staging the whole page double-fires over history the prior
// generation covered, and staging none of it loses the first effects past the
// barrier.
func TestAStraddlingPageStagesExactlyTheEnvelopesPastTheBarrier(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "P1", "P2", "P3", "P4", "P5")

	var seen delivered
	live := &sink{}
	spec := stand.staging(stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
		seen.took(batch)
		stand.model.write(payloadsOf(batch.Envelopes)...)
		return nil
	})), live)
	spec.EffectsAfter = 3
	running(t, newProjection(t, spec))
	stand.observer.await(t, "the page was applied", func(state projection.State) bool {
		return state.Progress.Applied == 5
	})

	if pages := live.pages(); !samePages(pages, [][]string{{"P4", "P5"}}) {
		t.Fatalf("the sink took %v, where a page running from below the barrier to above it owes an effect for exactly the envelopes past it", pages)
	}
	if rows := stand.model.rows(); !same(rows, []string{"P1", "P2", "P3", "P4", "P5"}) {
		t.Fatalf("the handler applied %v, and a barrier gates the effect and never the page", rows)
	}
	if page := seen.page(0); len(page) != 5 {
		t.Fatalf("the handler was given %d envelopes, where the whole page reaches it whatever the barrier suppresses", len(page))
	}

	t.Run("the control: a page entirely at or below the barrier calls nothing at all", func(t *testing.T) {
		held := newStand(t, eventmemory.Spec{})
		held.append(t, "a", "P1", "P2", "P3")

		quiet := &sink{}
		spec := held.staging(held.spec("orders", applies(held.model)), quiet)
		spec.EffectsAfter = 9
		running(t, newProjection(t, spec))
		held.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})
		if calls := quiet.calls.Load(); calls != 0 {
			t.Fatalf("the sink was called %d times for a page with nothing past the barrier, and a delivery with nothing to stage does not call Stage at all — not even with an empty slice", calls)
		}
	})

	t.Run("the control: a page entirely past the barrier stages all of it", func(t *testing.T) {
		held := newStand(t, eventmemory.Spec{})
		held.append(t, "a", "P1", "P2", "P3")

		everything := &sink{}
		running(t, newProjection(t, held.staging(held.spec("orders", applies(held.model)), everything)))
		held.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})
		if pages := everything.pages(); !samePages(pages, [][]string{{"P1", "P2", "P3"}}) {
			t.Fatalf("the sink took %v for a page with no barrier over it, so the arm above measured a sink that stages nothing", pages)
		}
	})
}

// The interaction neither appendix asks about and that has no defensible
// default: a page holding a parked envelope and an applied one. Staging the
// parked one tells the world about a state the read model refused; dropping it
// loses that effect for ever, because the loop advanced over that position and
// will never see it again. So it goes where the envelope goes — into the letter.
func TestAParkedEnvelopeIsNotStagedAndIsNotLost(t *testing.T) {
	permanent := fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)

	t.Run("the applied one is staged and the parked one travels", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		held := newPark()
		live := &sink{}
		spec := stand.parking(stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if string(envelope.Payload) == "A2" {
					return permanent
				}
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		})), held)
		spec.Effects = live
		running(t, newProjection(t, spec))
		stand.observer.await(t, "the first page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})

		stand.append(t, "a", "A2")
		stand.append(t, "b", "B1")
		stand.ticks.fire(t)
		state := stand.observer.await(t, "the whole log was read over a queue that is holding something", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded
		})

		if state.Progress.Quarantined != 1 {
			t.Fatalf("the projection reports %d quarantined envelopes, where A2 is the one that failed", state.Progress.Quarantined)
		}
		if pages := live.pages(); !samePages(pages, [][]string{{"A1"}, {"B1"}}) {
			t.Fatalf("the sink took %v, where the page A2 B1 owes an effect for B1 alone — A2 never reached the read model, so telling the world about it is the inversion the capability exists to prevent", pages)
		}
		if outside := live.outside.Load(); outside != 0 {
			t.Fatalf("%d stages were made outside the unit of work, and an effect that does not roll back with the advance is sent again on every rollback this pass can take", outside)
		}
		letters := held.letters(identityOf(t, "orders", projection.Ungenerated, projection.Whole()), sequenceOf("a"))
		if len(letters) != 1 || string(letters[0].Envelope.Payload) != "A2" {
			t.Fatalf("the queue holds %d letters, and an effect that is not staged now is not lost either — the letter is what carries it", len(letters))
		}
	})

	// The other applier, and it is the one a degraded projection runs on every
	// page for as long as the queue holds anything: the page is not delivered
	// whole, each envelope is asked whether its sequence is parked, and the ones
	// that are go to the queue WITHOUT REACHING THE HANDLER. An effect is owed
	// for what the handler applied, so it is owed for what is left and for
	// nothing else — the same rule as the arm above, reached by the other path.
	t.Run("a later page meets a sequence the queue already holds", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		held := newPark()
		live := &sink{}
		spec := stand.parking(stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			for _, envelope := range batch.Envelopes {
				if string(envelope.Payload) == "A2" {
					return permanent
				}
			}
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		})), held)
		spec.Effects = live
		running(t, newProjection(t, spec))
		stand.observer.await(t, "the first page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})

		stand.append(t, "a", "A2")
		stand.append(t, "b", "B1")
		stand.ticks.fire(t)
		stand.observer.await(t, "the queue holds the sequence a", func(state projection.State) bool {
			return state.Phase == projection.PhaseDegraded
		})

		stand.append(t, "a", "A3")
		stand.append(t, "c", "C1")
		stand.ticks.fire(t)
		state := stand.observer.await(t, "the page behind the queue was delivered", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})

		if state.Progress.Quarantined != 2 {
			t.Fatalf("the projection reports %d quarantined envelopes, where A3 went to the queue behind A2 without reaching the handler", state.Progress.Quarantined)
		}
		if pages := live.pages(); !samePages(pages, [][]string{{"A1"}, {"B1"}, {"C1"}}) {
			t.Fatalf("the sink took %v, where the page A3 C1 owes an effect for C1 alone — A3 never reached the handler at all, so an effect for it is one the read model never earned", pages)
		}
		if rows := stand.model.rows(); !same(rows, []string{"A1", "B1", "C1"}) {
			t.Fatalf("the read model holds %v, and what the sink was owed above is what this list holds", rows)
		}
		if outside := live.outside.Load(); outside != 0 {
			t.Fatalf("%d stages were made outside the unit of work, and an effect that does not roll back with the advance is sent again on every rollback this pass can take", outside)
		}
		letters := held.letters(identityOf(t, "orders", projection.Ungenerated, projection.Whole()), sequenceOf("a"))
		if len(letters) != 2 || string(letters[1].Envelope.Payload) != "A3" {
			t.Fatalf("the queue holds %d letters of a, where A3 travels behind A2 carrying its own effect", len(letters))
		}
	})

	t.Run("the control: the same page with nothing parked stages both", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		held := newPark()
		live := &sink{}
		spec := stand.parking(stand.spec("orders", applies(stand.model)), held)
		spec.Effects = live
		running(t, newProjection(t, spec))
		stand.observer.await(t, "the first page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})

		stand.append(t, "a", "A2")
		stand.append(t, "b", "B1")
		stand.ticks.fire(t)
		stand.observer.await(t, "the second page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})

		stand.append(t, "a", "A3")
		stand.append(t, "c", "C1")
		stand.ticks.fire(t)
		stand.observer.await(t, "the third page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 5
		})

		if pages := live.pages(); !samePages(pages, [][]string{{"A1"}, {"A2", "B1"}, {"A3", "C1"}}) {
			t.Fatalf("the sink took %v for pages nothing parked, so the two exclusions above are the barrier's or the handler's rather than the queue's", pages)
		}
	})
}

// The other end of the same rule: the effect a letter carries is staged by the
// redrive that applies it, in the transaction that applies and evicts it, and an
// eviction stages nothing at all — a skip is an operator saying the event will
// never be applied.
func TestARedriveStagesWhatItAppliesAndAnEvictionStagesNothing(t *testing.T) {
	ctx := context.Background()

	t.Run("what it applies, one unit per letter", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", 1, projection.Whole())
		parked(t, held, of, "a", "A2", "A3")

		live := &sink{}
		row := owning("orders", 1)
		drained, err := draining(t, stand, held, of, applies(stand.model), live, row).Sequence(ctx, sequenceOf("a"))
		if err != nil {
			t.Fatalf("draining a sequence whose cause was fixed answered %v", err)
		}
		if drained.Applied != 2 || drained.Left != 0 {
			t.Fatalf("the redrive answered %+v where two letters applied and none was left", drained)
		}
		if pages := live.pages(); !samePages(pages, [][]string{{"A2"}, {"A3"}}) {
			t.Fatalf("the sink took %v, where one unit per letter is one effect per letter — an effect the loop never staged because the envelope never reached the loop's handler", pages)
		}
		if outside := live.outside.Load(); outside != 0 {
			t.Fatalf("%d stages were made outside the letter's unit, and the apply, the effect and the eviction are one transaction or a crash between them leaves two of the three", outside)
		}
		for _, effect := range live.staged() {
			if effect.Attempt != 2 {
				t.Fatalf("the effect was staged at attempt %d, where a letter parked at attempt 1 is redriven at 2", effect.Attempt)
			}
			if effect.Identity != of {
				t.Fatalf("the effect was staged under %q, where a redrive drains a whole generation and stages under the identity it drains", effect.Identity)
			}
		}
	})

	t.Run("an evicted letter's effect is never staged", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", 1, projection.Whole())
		parked(t, held, of, "a", "A2", "A3")
		held.skip(of, sequenceOf("a"))

		live := &sink{}
		row := owning("orders", 1)
		drained, err := draining(t, stand, held, of, applies(stand.model), live, row).Sequence(ctx, sequenceOf("a"))
		if err != nil {
			t.Fatalf("draining a sequence an operator had already skipped from answered %v", err)
		}
		if drained.Applied != 1 {
			t.Fatalf("the redrive applied %d letters where one was evicted before it ran", drained.Applied)
		}
		if pages := live.pages(); !samePages(pages, [][]string{{"A3"}}) {
			t.Fatalf("the sink took %v, and an effect for an event that will never be applied is the inversion the capability exists to prevent", pages)
		}
	})

	t.Run("a unit that does not commit leaves the letter parked and stages nothing", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", 1, projection.Whole())
		parked(t, held, of, "a", "A2", "A3")

		live := &sink{}
		row := owning("orders", 1)
		stand.interrupted = errors.New("the connection went away on commit")
		drained, err := draining(t, stand, held, of, applies(stand.model), live, row).Sequence(ctx, sequenceOf("a"))
		if err == nil {
			t.Fatal("a redrive whose unit did not commit answered no error, so nothing tells an operator the drain did not happen")
		}
		if drained.Applied != 0 {
			t.Fatalf("the redrive answered %d applied over a unit that did not commit", drained.Applied)
		}
		if letters := held.letters(of, sequenceOf("a")); len(letters) != 2 {
			t.Fatalf("the queue holds %d letters after a unit that did not commit, where both are still parked", len(letters))
		}
		if staged := live.payloads(); len(staged) != 0 {
			t.Fatalf("the sink holds %v after a unit that did not commit, and an effect that survives the rollback is one the next redrive sends a second time", staged)
		}
		if calls := live.calls.Load(); calls != 1 {
			t.Fatalf("the sink was called %d times, and what makes the arm above about a rollback rather than about a suppressor is that the call was made", calls)
		}
	})

	t.Run("the control: the same redrive with no sink", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", 1, projection.Whole())
		parked(t, held, of, "a", "A2", "A3")

		row := owning("orders", 1)
		drained, err := draining(t, stand, held, of, applies(stand.model), nil, row).Sequence(ctx, sequenceOf("a"))
		if err != nil {
			t.Fatalf("draining a sequence through a redrive with no sink answered %v", err)
		}
		if drained.Applied != 2 {
			t.Fatalf("the redrive applied %d letters where a spec with no capability applies every one of them", drained.Applied)
		}
		if rows := stand.model.rows(); !same(rows, []string{"A2", "A3"}) {
			t.Fatalf("the read model holds %v, so the drain above was not the same drain", rows)
		}
		if reads := row.reads.Load(); reads != 0 {
			t.Fatalf("the ownership row was read %d times by a redrive with no sink, and a nil Effects is the first suppressor and costs no round trip", reads)
		}
	})

	t.Run("the control: a redrive whose generation does not own the row", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", 1, projection.Whole())
		parked(t, held, of, "a", "A2", "A3")

		live := &sink{}
		row := owning("orders", 2)
		drained, err := draining(t, stand, held, of, applies(stand.model), live, row).Sequence(ctx, sequenceOf("a"))
		if err != nil {
			t.Fatalf("draining a sequence of a retired generation answered %v", err)
		}
		if drained.Applied != 2 {
			t.Fatalf("the redrive applied %d letters, where a retired generation still drains its own queue into its own read model", drained.Applied)
		}
		if calls := live.calls.Load(); calls != 0 {
			t.Fatalf("the sink was called %d times by a generation the row does not name, and the operator's half is gated by the same three suppressors the loop is", calls)
		}
		if reads := row.reads.Load(); reads != 2 {
			t.Fatalf("the ownership row was read %d times over two letters, where each letter's unit asks it once", reads)
		}
	})

	// The third field the loop has, and the one the redrive refuses rather than
	// honours. A letter is in the queue because a loop parked it, a loop parks
	// only under ParkSequence, and New refuses that beside a barrier — so a
	// barrier here can only suppress an effect that is owed, for ever.
	t.Run("a barrier on a redrive is refused", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		held := newPark()
		of := identityOf(t, "orders", 1, projection.Whole())
		stand.park = held

		spec := drainingSpec(stand, held, of, applies(stand.model), &sink{}, owning("orders", 1))
		spec.EffectsAfter = 9
		built, err := projection.NewRedrive(spec)
		if err == nil {
			t.Fatal("a redrive carrying a barrier was accepted, and what it then does is apply a letter and stage nothing — the one outcome [SPEC] §1.6 calls the effect lost for ever")
		}
		if built != nil {
			t.Fatal("a refused redrive answered a Redrive as well as an error")
		}
		if !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a barrier on a redrive was refused as %v, which is not the class a spec that cannot be assembled carries", err)
		}
		if !strings.Contains(err.Error(), "EffectsAfter") {
			t.Fatalf("the refusal reads %q and does not name the field it is about", err)
		}

		t.Run("the control: the same redrive with the field at zero", func(t *testing.T) {
			spec.EffectsAfter = 0
			if _, err := projection.NewRedrive(spec); err != nil {
				t.Fatalf("the same redrive with no barrier over it was refused: %v, so the refusal above is another field's", err)
			}
		})

		t.Run("the control: the loop accepts the barrier this door refuses", func(t *testing.T) {
			legal := stand.staging(stand.spec("orders", applies(stand.model)), &sink{})
			legal.Generation, legal.Generations, legal.EffectsAfter = 1, owning("orders", 1), 9
			if _, err := projection.New(legal); err != nil {
				t.Fatalf("the loop refused the barrier too: %v — the refusal above is the queue's and not the barrier's", err)
			}
		})
	})
}

// The redrive both halves of this section's queue cases are built on, with the
// fields the loop has wired the way the loop wires them.
func draining(t *testing.T, held *stand, queue *park, of projection.Identity, handler projection.Handler, effects projection.Effects, row projection.Generations) *projection.Redrive {
	t.Helper()
	held.park = queue
	built, err := projection.NewRedrive(drainingSpec(held, queue, of, handler, effects, row))
	if err != nil {
		t.Fatalf("a well-formed redrive was refused: %v", err)
	}
	return built
}

func drainingSpec(held *stand, queue *park, of projection.Identity, handler projection.Handler, effects projection.Effects, row projection.Generations) projection.RedriveSpec {
	return projection.RedriveSpec{
		Identity:    of,
		Handler:     handler,
		Park:        queue,
		Unit:        held.operating(&transaction{named: "one resource"}),
		Destination: readModel{pool: held},
		Effects:     effects,
		Generations: row,
	}
}

// The ordering is cheapest-first and that is what it buys: a rebuild pays a
// round trip per page for a capability it does not have if the row is read
// unconditionally.
func TestTheOwnershipRowIsNotReadWhenThereIsNothingToStage(t *testing.T) {
	t.Run("a spec with no sink", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1", "A2", "A3")

		row := owning("orders", projection.Ungenerated)
		spec := stand.staging(stand.spec("orders", applies(stand.model)), nil)
		spec.Generations = row
		running(t, newProjection(t, spec))
		stand.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})
		if reads := row.reads.Load(); reads != 0 {
			t.Fatalf("the ownership row was read %d times by a projection with no sink, and the first suppressor makes no call and no round trip", reads)
		}
	})

	t.Run("a page with nothing past the barrier", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1", "A2", "A3")

		row := owning("orders", projection.Ungenerated)
		spec := stand.staging(stand.spec("orders", applies(stand.model)), &sink{})
		spec.Generations, spec.EffectsAfter = row, 9
		running(t, newProjection(t, spec))
		stand.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})
		if reads := row.reads.Load(); reads != 0 {
			t.Fatalf("the ownership row was read %d times for a page with nothing past the barrier, and the second suppressor answers off the page already in hand", reads)
		}
	})

	t.Run("the control: one applied envelope past the barrier reads it exactly once", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1", "A2", "A3")

		row := owning("orders", projection.Ungenerated)
		live := &sink{}
		spec := stand.staging(stand.spec("orders", applies(stand.model)), live)
		spec.Generations = row
		running(t, newProjection(t, spec))
		stand.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})
		if reads := row.reads.Load(); reads != 1 {
			t.Fatalf("the ownership row was read %d times for one page that owes effects, where one delivery asks it once and the two arms above would be satisfied by a row nothing ever reads", reads)
		}
		if staged := live.payloads(); !same(staged, []string{"A1", "A2", "A3"}) {
			t.Fatalf("the sink took %v, so the read counted above bought nothing", staged)
		}
	})
}

// The half of the barrier that is NOT durable, measured rather than implied.
// The envelope's side of the comparison is a checkpoint column; the barrier's
// side is a constant the deployment holds, nothing records what a generation was
// warmed up under, and nothing refuses a restart that names a lower one. So the
// failure the study moves from a process restart to a configuration change is
// still a failure, and this is the concrete input: the same generation, the same
// rows, the field dropped by a release that rolled back.
//
// The control is the shipped property — with the field still set, the same
// restart resumes SUPPRESSED over what is left of the warm-up — so what the arm
// measures is attributable to the dropped constant and to nothing else.
func TestABarrierDroppedFromTheSpecRestagesTheWarmUpBelowIt(t *testing.T) {
	for _, restart := range []struct {
		what   string
		after  event.Position
		staged []string
	}{
		{"the barrier dropped by a release that rolled back", 0, []string{"A3", "A4", "A5"}},
		{"the control: the same restart with the barrier still named", 4, []string{"A5"}},
	} {
		t.Run(restart.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			stand.append(t, "a", "A1", "A2")

			row := owning("orders", 1)
			warm := &sink{}
			spec := stand.staging(stand.spec("orders", applies(stand.model)), warm)
			spec.Generation, spec.Generations, spec.EffectsAfter = 1, row, 4
			cancel, returned := running(t, newProjection(t, spec))
			stand.observer.await(t, "the warm-up applied everything the log held", func(state projection.State) bool {
				return state.Progress.Applied == 2
			})
			if calls := warm.calls.Load(); calls != 0 {
				t.Fatalf("the warm-up staged %d times below its own barrier, so this arm starts from a generation that was never warming up", calls)
			}
			cancel()
			returns(t, returned)

			stand.append(t, "a", "A3", "A4", "A5")
			again := &sink{}
			resumed := stand.staging(stand.spec("orders", applies(stand.model)), again)
			resumed.Generation, resumed.Generations, resumed.EffectsAfter = 1, row, restart.after
			resumed.Ticks = newTicks().Ticks
			running(t, newProjection(t, resumed))
			stand.observer.await(t, "the restart applied the rest of the log", func(state projection.State) bool {
				return state.Progress.Applied == 5
			})

			if staged := again.payloads(); !same(staged, restart.staged) {
				t.Fatalf("the restarted generation staged %v where %v was owed: the barrier is a constant this spec carries and not a position anything recorded, so dropping it stages A3 and A4 — the part of the warm-up this generation had not reached yet", staged, restart.staged)
			}
		})
	}
}

// A warm-up that parks an event passes its own barrier over a read model that
// never received it, and the rows the generation then holds are not comparable
// to the live one's — which is the only evidence a cutover has.
func TestABarrierBesideAParkIsRefused(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})

	refused := stand.parking(stand.spec("orders", applies(stand.model)), newPark())
	refused.Effects, refused.EffectsAfter = &sink{}, 3
	built, err := projection.New(refused)
	if err == nil {
		t.Fatal("a warm-up under a queue was accepted, and what it leaves is a generation whose rows a cutover cannot compare")
	}
	if built != nil {
		t.Fatal("a refused spec answered a projection as well as an error")
	}
	if !errors.Is(err, projection.ErrSpec) {
		t.Fatalf("a barrier beside a park was refused as %v, which is not the class a spec that cannot be assembled carries", err)
	}
	for _, names := range []string{"EffectsAfter", "ParkSequence"} {
		if !strings.Contains(err.Error(), names) {
			t.Fatalf("the refusal reads %q and does not name %s, so it is the combination that is refused rather than either field", err, names)
		}
	}

	t.Run("the control: a barrier beside Halt", func(t *testing.T) {
		accepted := stand.parking(stand.spec("orders", applies(stand.model)), newPark())
		accepted.OnPermanentFailure = projection.Halt
		accepted.Effects, accepted.EffectsAfter = &sink{}, 3
		if _, err := projection.New(accepted); err != nil {
			t.Fatalf("a warm-up under Halt was refused: %v", err)
		}
	})

	t.Run("the control: a park with no barrier over it", func(t *testing.T) {
		accepted := stand.parking(stand.spec("orders", applies(stand.model)), newPark())
		accepted.Effects = &sink{}
		if _, err := projection.New(accepted); err != nil {
			t.Fatalf("a queue beside a sink with no barrier was refused: %v", err)
		}
	})
}

// A field that is accepted and ignored is worse than an absent one: a default
// assigned to it makes it look live to the next reader.
func TestABarrierWithNothingToGateIsRefused(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})

	refused := stand.staging(stand.spec("orders", applies(stand.model)), nil)
	refused.EffectsAfter = 3
	built, err := projection.New(refused)
	if err == nil {
		t.Fatal("a barrier with nothing to gate was accepted, and a caller who set it got a suppression of nothing")
	}
	if built != nil {
		t.Fatal("a refused spec answered a projection as well as an error")
	}
	if !errors.Is(err, projection.ErrSpec) {
		t.Fatalf("a barrier with no sink was refused as %v, which is not the class a spec that cannot be assembled carries", err)
	}
	if !strings.Contains(err.Error(), "EffectsAfter") {
		t.Fatalf("the refusal reads %q and does not name the field it is about", err)
	}

	t.Run("the control: a sink with no barrier stages from the first envelope", func(t *testing.T) {
		held := newStand(t, eventmemory.Spec{})
		held.append(t, "a", "A1", "A2")

		live := &sink{}
		running(t, newProjection(t, held.staging(held.spec("orders", applies(held.model)), live)))
		held.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 2
		})
		if staged := live.payloads(); !same(staged, []string{"A1", "A2"}) {
			t.Fatalf("a sink with no barrier over it took %v, where a zero EffectsAfter is no gate at all", staged)
		}
	})
}

// The measured edge of the guarantee, and it is measured rather than implied:
// the framework cannot tell a second projection name from a rebuild of the
// first, so the ownership gate does not reach that spelling. The control is the
// recipe the gate does cover.
func TestASecondProjectionNameWithEffectsStagesEveryHistoricalEvent(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "A1", "A2")
	stand.append(t, "b", "B1")

	live := &sink{}
	running(t, newProjection(t, stand.staging(stand.spec("orders-rebuild", applies(stand.model)), live)))
	stand.observer.await(t, "the rebuild read the whole log", func(state projection.State) bool {
		return state.Progress.Applied == 3
	})
	if staged := live.payloads(); !same(staged, []string{"A1", "A2", "B1"}) {
		t.Fatalf("a rebuild spelled as a second projection name staged %v, and this arm records what the gate does NOT reach — a name is not a generation, and requiring an ownership row beside every sink would not have covered it either", staged)
	}

	t.Run("the control: the same rebuild spelled as a generation", func(t *testing.T) {
		held := newStand(t, eventmemory.Spec{})
		held.append(t, "a", "A1", "A2")
		held.append(t, "b", "B1")

		quiet := &sink{}
		spec := held.staging(held.spec("orders", applies(held.model)), quiet)
		spec.Generation, spec.Generations = 2, owning("orders", 1)
		running(t, newProjection(t, spec))
		held.observer.await(t, "the rebuild read the whole log", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})
		if calls := quiet.calls.Load(); calls != 0 {
			t.Fatalf("the generation-spelled rebuild staged %d times, so the recipe the module page teaches is not the one the gate covers", calls)
		}
	})
}

// The migration every running deployment has, and the case the suppressor's
// gating is decided by. Gated on Spec.Generation it never runs at Ungenerated,
// so the first cutover of every live deployment leaves the retiring projection
// staging beside the arriving one for ever, with no error on any path.
func TestAnUngeneratedProjectionWithGenerationsStopsStagingAtTheCutover(t *testing.T) {
	ctx := context.Background()
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "A1", "A2")

	row := owning("orders", projection.Ungenerated)
	live := &sink{}
	spec := stand.staging(stand.spec("orders", applies(stand.model)), live)
	spec.Generations = row
	running(t, newProjection(t, spec))
	stand.observer.await(t, "the first page was applied", func(state projection.State) bool {
		return state.Progress.Applied == 2
	})
	if staged := live.payloads(); !same(staged, []string{"A1", "A2"}) {
		t.Fatalf("a projection at Ungenerated over a row that answers Ungenerated staged %v, where the row names its own generation and it stages exactly as it did before the field arrived", staged)
	}

	if err := row.Activate(ctx, "orders", projection.Ungenerated, 2); err != nil {
		t.Fatalf("moving the read target from Ungenerated to generation 2 answered %v", err)
	}
	stand.append(t, "a", "A3")
	stand.ticks.fire(t)
	stand.observer.await(t, "the page after the cutover was applied", func(state projection.State) bool {
		return state.Progress.Applied == 3
	})

	if staged := live.payloads(); !same(staged, []string{"A1", "A2"}) {
		t.Fatalf("the retiring projection staged %v after the row moved, and the read happens inside the transaction that commits its own advance", staged)
	}
	if rows := stand.model.rows(); !same(rows, []string{"A1", "A2", "A3"}) {
		t.Fatalf("the retiring projection applied %v, and a projection that stopped staging keeps applying and keeps advancing", rows)
	}
	if held := stand.row(t, "orders"); held.Advance != 2 {
		t.Fatalf("the checkpoint row stands at advance %d, where the pass that staged nothing still committed its own advance", held.Advance)
	}

	t.Run("the control: the same projection with no ownership row", func(t *testing.T) {
		held := newStand(t, eventmemory.Spec{})
		held.append(t, "a", "A1", "A2")

		row := owning("orders", projection.Ungenerated)
		live := &sink{}
		running(t, newProjection(t, held.staging(held.spec("orders", applies(held.model)), live)))
		held.observer.await(t, "the first page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 2
		})
		if err := row.Activate(ctx, "orders", projection.Ungenerated, 2); err != nil {
			t.Fatalf("moving the read target from Ungenerated to generation 2 answered %v", err)
		}
		held.append(t, "a", "A3")
		held.ticks.fire(t)
		held.observer.await(t, "the page after the cutover was applied", func(state projection.State) bool {
			return state.Progress.Applied == 3
		})
		if staged := live.payloads(); !same(staged, []string{"A1", "A2", "A3"}) {
			t.Fatalf("a projection holding no route to an ownership row staged %v after a cutover it cannot see, and that limit is measured here rather than written down — the remedy is the release that adds the row before anything else changes", staged)
		}
	})
}

// The ownership row under the two recipes an application can implement Active
// with, and one clause of SQL is the whole difference between them. A LOCKING
// read — SELECT active … FOR SHARE — is one a concurrent cutover's UPDATE waits
// behind, so the generation that staged is the generation that owned the row for
// the whole of its unit. A plain read is not: at READ COMMITTED a read and a
// concurrent update of one row do not conflict, both commit, and nothing rolls
// back.
//
// The share is held until the unit that took it ends, because that is what a row
// lock is — reading() is what wraps the stand's own unit to get it, and a
// Generations reached over a second pool is the recipe that has neither.
type ownedRow struct {
	mutex   sync.Mutex
	freed   *sync.Cond
	held    map[string]projection.Generation
	shares  int
	locking bool

	answered func(projection.Generation)
	issued   func()
}

func lockingRow(name string, generation projection.Generation) *ownedRow {
	return rowHolding(name, generation, true)
}

func plainRow(name string, generation projection.Generation) *ownedRow {
	return rowHolding(name, generation, false)
}

func rowHolding(name string, generation projection.Generation, locking bool) *ownedRow {
	held := &ownedRow{held: map[string]projection.Generation{name: generation}, locking: locking}
	held.freed = sync.NewCond(&held.mutex)
	return held
}

type share struct{ taken bool }

type shareKey struct{}

func (this *ownedRow) Active(ctx context.Context, name string) (projection.Generation, error) {
	this.mutex.Lock()
	answered := this.held[name]
	if taken, held := ctx.Value(shareKey{}).(*share); this.locking && held && !taken.taken {
		taken.taken = true
		this.shares++
	}
	this.mutex.Unlock()
	if this.answered != nil {
		this.answered(answered)
	}
	return answered, nil
}

func (this *ownedRow) Activate(_ context.Context, name string, from, to projection.Generation) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.issued != nil {
		this.issued()
	}
	for this.locking && this.shares > 0 {
		this.freed.Wait()
	}
	if this.held[name] != from {
		return fmt.Errorf("%w: %q holds generation %d and this call names %d", event.ErrConflict, name, this.held[name], from)
	}
	this.held[name] = to
	return nil
}

func (this *ownedRow) active(name string) projection.Generation {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.held[name]
}

// The stand's own unit with the row's share released when the transaction that
// took it ends — on the commit and on the rollback both, which is the half a
// share that lived for the length of one call could not have.
func (this *ownedRow) reading(open func(context.Context, func(context.Context) error) error) func(context.Context, func(context.Context) error) error {
	return func(ctx context.Context, work func(context.Context) error) error {
		taken := &share{}
		err := open(context.WithValue(ctx, shareKey{}, taken), work)
		this.mutex.Lock()
		if taken.taken {
			taken.taken = false
			this.shares--
			this.freed.Broadcast()
		}
		this.mutex.Unlock()
		return err
	}
}

// What one drive of the interleaving answers: what each generation staged, and
// what the retiring one had staged at the moment the cutover's write RETURNED —
// which is the whole question, because a cutover that returned while a unit that
// read the row was still open is a cutover that did not wait for it.
type senders struct {
	retiring []string
	arriving []string
	settled  []string
}

// The cutover committed while a retiring pass is open between its ownership read
// and its commit, with the arriving generation's own pass open across the same
// moment. Both passes are over the SAME envelope, which is the ordinary state of
// a cutover: the two generations are draining one log.
//
// Nothing here sleeps or hopes. The retiring pass is held inside Active, the
// cutover is issued from a goroutine of the test's, and the arriving pass is
// released afterwards — so which value each of the two reads is decided by the
// recipe and by nothing else.
func cuttingOverMidPass(t *testing.T, row *ownedRow) senders {
	t.Helper()
	stand := newStand(t, eventmemory.Spec{})
	stand.park = newPark()
	stand.append(t, "a", "A1", "A2")

	retiring, arriving := &sink{}, &sink{}
	live := stand.staging(stand.spec("orders", applies(stand.model)), retiring)
	live.Generation, live.Generations, live.Unit = 1, row, row.reading(live.Unit)
	running(t, newProjection(t, live))
	stand.observer.await(t, "generation 1 applied the whole log", func(state projection.State) bool {
		return state.Identity.Generation() == 1 && state.Progress.Applied == 2
	})

	beside, rebuilt := newTicks(), &model{}
	warm := stand.staging(stand.spec("orders", applies(rebuilt)), arriving)
	warm.Generation, warm.Generations, warm.Unit = 2, row, row.reading(warm.Unit)
	warm.Ticks = beside.Ticks
	running(t, newProjection(t, warm))
	stand.observer.await(t, "generation 2 applied the whole log", func(state projection.State) bool {
		return state.Identity.Generation() == 2 && state.Progress.Applied == 2
	})

	var armed atomic.Bool
	var reads atomic.Int64
	reached, release, issued := make(chan struct{}), make(chan struct{}), make(chan struct{})
	row.answered = func(projection.Generation) {
		if !armed.Load() || reads.Add(1) != 1 {
			return
		}
		close(reached)
		<-release
	}
	row.issued = func() { close(issued) }

	stand.append(t, "a", "A3")
	armed.Store(true)
	stand.ticks.fire(t)
	await(t, reached, "the retiring pass read the ownership row")

	cutover := make(chan senders, 1)
	go func() {
		err := row.Activate(context.Background(), "orders", 1, 2)
		if err != nil {
			t.Errorf("the cutover from generation 1 to 2 answered %v", err)
		}
		cutover <- senders{settled: retiring.payloads()}
	}()
	await(t, issued, "the cutover issued its write")
	if !row.locking {
		awaitRow(t, row, 2)
	}

	beside.fire(t)
	stand.observer.await(t, "generation 2 applied the event that arrived", func(state projection.State) bool {
		return state.Identity.Generation() == 2 && state.Progress.Applied == 3
	})
	close(release)
	stand.observer.await(t, "generation 1 applied the event that arrived", func(state projection.State) bool {
		return state.Identity.Generation() == 1 && state.Progress.Applied == 3
	})

	held := <-cutover
	held.retiring, held.arriving = retiring.payloads(), arriving.payloads()
	if row.active("orders") != 2 {
		t.Fatalf("the ownership row holds generation %d after a cutover that answered no error", row.active("orders"))
	}
	return held
}

func await(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(settle):
		t.Fatalf("%s never happened", what)
	}
}

func awaitRow(t *testing.T, row *ownedRow, generation projection.Generation) {
	t.Helper()
	deadline := time.After(settle)
	for row.active("orders") != generation {
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatalf("the ownership row never reached generation %d", generation)
		}
	}
}

// The boundary [SPEC] §1.6 claims, measured rather than asserted — and what it
// measures is that the boundary is the APPLICATION'S to draw. Both arms run the
// one interleaving that decides it: the cutover commits while a retiring pass
// sits between its ownership read and its commit.
//
// [[D-126]] forbids this framework choosing an isolation level, so what closes
// the window is the read Active makes. Under the documented locking read the two
// passes cannot disagree about who owns the effects and the envelope is staged
// once; under a plain read — READ COMMITTED's default, and REPEATABLE READ's
// too — they can, and it is staged twice.
func TestTheOwnershipReadIsABoundaryOnlyWhenACutoverWaitsForIt(t *testing.T) {
	t.Run("the documented locking read: the cutover waits for the unit that read the row", func(t *testing.T) {
		held := cuttingOverMidPass(t, lockingRow("orders", 1))

		if !same(held.retiring, []string{"A1", "A2", "A3"}) {
			t.Fatalf("the retiring generation staged %v, where a pass that owned the row when it read it and still owned it when it committed owes an effect for what it applied", held.retiring)
		}
		if len(held.arriving) != 0 {
			t.Fatalf("the arriving generation staged %v beside the retiring one, and no envelope is staged by both", held.arriving)
		}
		if !same(held.settled, []string{"A1", "A2", "A3"}) {
			t.Fatalf("the cutover's write returned with %v staged, where a locking read is one the UPDATE waits behind — so the unit that staged A3 had committed before the row moved", held.settled)
		}
	})

	t.Run("the control: a plain read leaves two senders for one envelope", func(t *testing.T) {
		held := cuttingOverMidPass(t, plainRow("orders", 1))

		if !same(held.retiring, []string{"A1", "A2", "A3"}) {
			t.Fatalf("the retiring generation staged %v, and this arm is about a stage that COMMITTED after the row moved rather than about one that never happened", held.retiring)
		}
		if !same(held.arriving, []string{"A3"}) {
			t.Fatalf("the arriving generation staged %v, where the row names it and it owns every envelope it applies", held.arriving)
		}
		if !same(held.settled, []string{"A1", "A2"}) {
			t.Fatalf("the cutover's write returned with %v staged, so it waited for the open unit after all and the two arms measure the same recipe", held.settled)
		}
		t.Logf("A3 was staged by generation 1 and by generation 2: this is the window a plain Active leaves, and what closes it is the application's read")
	})
}

// An ownership row that cannot be read, which is a read of the application's own
// table rather than of the log: a cutover row that blinked is not a reason to
// stop applying events.
type unreadable struct{ reads atomic.Int64 }

func (this *unreadable) Active(context.Context, string) (projection.Generation, error) {
	this.reads.Add(1)
	return projection.Ungenerated, errors.New("the ownership table is unreachable")
}

func (this *unreadable) Activate(context.Context, string, projection.Generation, projection.Generation) error {
	return errors.New("the ownership table is unreachable")
}

// What each of the two failures the gate has costs, and the third arm is the one
// that matters: a stage that failed permanently is NOT routed to the queue, because
// parking an envelope over a sink that refused would record a read-model hole
// that does not exist. Told apart by the attempt, which a redelivery consumes
// and a postpone does not.
func TestAStageThatFailsIsRedeliveredOrHaltsAndIsNeverParked(t *testing.T) {
	t.Run("a sink that refuses transiently is redelivered", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		live := &sink{}
		live.refusing(errors.New("the staging table is locked"))
		running(t, newProjection(t, stand.staging(stand.spec("orders", applies(stand.model)), live)))
		state := stand.observer.await(t, "the pass was retried over a sink that refused", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying
		})
		if state.Attempt != 2 {
			t.Fatalf("the projection retried at attempt %d, where a sink that refused costs the attempt a handler failure costs", state.Attempt)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the checkpoint row stands at advance %d over a page whose effect was never staged, so the advance did not roll back with it", row.Advance)
		}

		live.refusing(nil)
		stand.ticks.fire(t)
		stand.observer.await(t, "the page was applied once the sink took it", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})
		if staged := live.payloads(); !same(staged, []string{"A1"}) {
			t.Fatalf("the sink took %v after it stopped refusing, so the retry above was not a redelivery of the same page", staged)
		}
	})

	t.Run("a sink that refuses permanently halts and parks nothing", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		held := newPark()
		live := &sink{}
		live.refusing(fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload))
		var delivered atomic.Int64
		spec := stand.parking(stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			delivered.Add(1)
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		})), held)
		spec.Effects = live
		running(t, newProjection(t, spec))
		state := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})

		if !errors.Is(state.Err, projection.ErrHalted) {
			t.Fatalf("a sink that refused permanently was answered with %v, where the pass stops rather than skipping the page", state.Err)
		}
		if names := held.names(identityOf(t, "orders", projection.Ungenerated, projection.Whole())); len(names) != 0 {
			t.Fatalf("the queue holds %v after a stage that failed, and parking an envelope over a sink that refused records a read-model hole that does not exist", names)
		}
		if calls := delivered.Load(); calls != 1 {
			t.Fatalf("the page was delivered %d times, where a stage that failed is answered where it happened rather than sent back through the isolation pass one envelope at a time", calls)
		}
		if state.Progress.Quarantined != 0 {
			t.Fatalf("the projection reports %d quarantined envelopes over a page the handler applied", state.Progress.Quarantined)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the checkpoint row stands at advance %d after a halt, so the advance did not roll back with the stage", row.Advance)
		}
	})

	t.Run("an ownership row that cannot be read postpones and costs no attempt", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "A1")

		row := &unreadable{}
		live := &sink{}
		spec := stand.staging(stand.spec("orders", applies(stand.model)), live)
		spec.Generations = row
		running(t, newProjection(t, spec))
		state := stand.observer.await(t, "the pass was postponed over a row it could not read", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying
		})

		if state.Attempt != 1 {
			t.Fatalf("the projection retried at attempt %d, where a read of the application's own table costs no attempt — left consumed, a table that was away for Attempts passes would make the next handler failure permanent", state.Attempt)
		}
		if !strings.Contains(state.Err.Error(), "ownership row") {
			t.Fatalf("the postponed pass reads %q and does not name what could not be read", state.Err)
		}
		if calls := live.calls.Load(); calls != 0 {
			t.Fatalf("the sink was called %d times over a row nobody could read, and what the row decides is whether this delivery stages at all", calls)
		}
		if held := stand.row(t, "orders"); !held.Fresh() {
			t.Fatalf("the checkpoint row stands at advance %d over a pass that never staged, so the advance did not roll back with it", held.Advance)
		}
	})
}

// The capability is a value and a Handler holds no route to one: not a field, not
// a method, not a context key. The literal below is the compile-time half — it is
// unkeyed, so a fifth field or a reordering stops the package building — and the
// walks are what say the same thing about a route nobody declared.
func TestABatchCarriesNoRouteToAnEffect(t *testing.T) {
	_ = projection.Batch{"orders", projection.Identity{}, nil, 1}

	batch := reflect.TypeFor[projection.Batch]()
	if batch.NumField() != 4 {
		t.Fatalf("Batch carries %d fields, and the literal above pins four", batch.NumField())
	}
	if batch.NumMethod() != 0 {
		t.Fatalf("Batch carries %d methods, and a handler reaches an effect through one of them", batch.NumMethod())
	}
	effects := reflect.TypeFor[projection.Effects]()
	for index := range batch.NumField() {
		field := batch.Field(index)
		switch {
		case field.Type.Implements(effects), field.Type == effects:
			t.Fatalf("Batch.%s is an Effects, so the capability is handed to the projection handler", field.Name)
		case field.Type.Kind() == reflect.Func, field.Type.Kind() == reflect.Chan, field.Type.Kind() == reflect.Interface:
			t.Fatalf("Batch.%s is a %s, and a handler that can be handed one can be handed a dispatcher through it", field.Name, field.Type.Kind())
		}
	}

	for _, reached := range contextValues(t, parsedPackage(t, ".")) {
		t.Errorf("%s, and a capability smuggled through a context is one a projection handler has a route to", reached)
	}

	t.Run("the control: the same walk over a package that carries one", func(t *testing.T) {
		fixture, err := parser.ParseFile(token.NewFileSet(), "effects.go", `package forms

import "context"

type key struct{}

func with(ctx context.Context, effects any) context.Context {
	return context.WithValue(ctx, key{}, effects)
}
`, 0)
		if err != nil {
			t.Fatalf("the fixture would not parse: %v", err)
		}
		if reached := contextValues(t, []*ast.File{fixture}); len(reached) == 0 {
			t.Fatal("a package that puts a value in a context was walked and reported nothing, so the arm above is a walk that resolved nothing rather than a clean tree")
		}
	})
}

// Every non-test file of a package, parsed. A file added to the package is
// walked without anybody listing it, which is the property that matters here.
func parsedPackage(t *testing.T, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s answered %v", dir, err)
	}
	fset := token.NewFileSet()
	var held []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s answered %v", name, err)
		}
		held = append(held, parsed)
	}
	if len(held) < 16 {
		t.Fatalf("%d files were parsed, and the package holds more than that outside its tests — so this walked the wrong directory", len(held))
	}
	return held
}

func contextValues(t *testing.T, files []*ast.File) []string {
	t.Helper()
	var reached []string
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, is := node.(*ast.CallExpr)
			if !is {
				return true
			}
			selector, is := call.Fun.(*ast.SelectorExpr)
			if !is {
				return true
			}
			switch {
			case selector.Sel.Name == "WithValue":
				reached = append(reached, file.Name.Name+" puts a value in a context")
			case selector.Sel.Name == "Value" && len(call.Args) == 1:
				reached = append(reached, file.Name.Name+" reads a value out of a context")
			}
			return true
		})
	}
	return reached
}

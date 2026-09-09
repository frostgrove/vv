package projection_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
	"github.com/frostgrove/vv/runtime"
)

var errCommitLost = errors.New("the transaction was rolled back on its way to committing")

// A read model written through the unit rather than beside it: the handler
// stages its rows and the unit publishes them at the commit or drops them with
// everything else the unit staged. It is what makes "neither the write nor the
// advance" a measurement here rather than a sentence.
type staging struct {
	mutex  sync.Mutex
	rows   []string
	staged []string
}

func (this *staging) write(rows ...string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.staged = append(this.staged, rows...)
}

func (this *staging) commit() {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.rows, this.staged = append(this.rows, this.staged...), nil
}

func (this *staging) discard() {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.staged = nil
}

func (this *staging) committed() []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]string(nil), this.rows...)
}

// The advance and the handler's rows are one write or none, and the kill point
// is what says which: a unit that fails on its way to committing leaves neither,
// and the same unit allowed through leaves both.
func TestTheAdvanceAndTheHandlersRowsCommitTogetherInMemory(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "one")

	rows := &staging{}
	var broken atomic.Bool
	broken.Store(true)
	spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
		rows.write(payloadsOf(batch.Envelopes)...)
		return nil
	}))
	spec.Advance = projection.InUnit
	spec.Destination = projection.Unchecked
	spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
		tx, err := stand.checkpoints.Begin(ctx)
		if err != nil {
			return err
		}
		if err := work(eventmemory.WithTransaction(ctx, tx)); err != nil {
			_ = tx.Rollback(ctx)
			rows.discard()
			return err
		}
		if broken.Load() {
			_ = tx.Rollback(ctx)
			rows.discard()
			return errCommitLost
		}
		if err := tx.Commit(ctx); err != nil {
			rows.discard()
			return err
		}
		rows.commit()
		return nil
	}
	running(t, newProjection(t, spec))

	stand.ticks.expect(t, time.Second, "the poll the loop opens with")
	stand.ticks.expect(t, 250*time.Millisecond, "the backoff after the unit was lost")
	if held := rows.committed(); len(held) != 0 {
		t.Fatalf("the read model holds %v after a unit that did not commit", held)
	}
	if row := stand.row(t, "orders"); !row.Fresh() {
		t.Fatalf("the checkpoint is at advance %d after a unit that did not commit, so the advance was written outside it", row.Advance)
	}

	broken.Store(false)
	stand.ticks.fire(t)
	stand.observer.await(t, "the page applied", func(state projection.State) bool {
		return state.Progress.Applied == 1
	})
	if held := rows.committed(); !same(held, []string{"one"}) {
		t.Fatalf("the committed unit left %v in the read model", held)
	}
	if row := stand.row(t, "orders"); row.Advance != 1 {
		t.Fatalf("the committed unit left the checkpoint at advance %d", row.Advance)
	}
}

// Inside a unit the two writes commit together, so their order is the lock
// manager's business alone and the advance is claimed first — which is what
// keeps a second live instance from applying a page it is about to lose.
// Outside one the order IS the delivery guarantee, and it does not move.
func TestTheAdvanceIsClaimedBeforeTheHandlerInsideAUnitAndAfterItOutside(t *testing.T) {
	for _, mode := range []struct {
		what     string
		advance  projection.Advance
		expected []string
	}{
		{"outside a unit the handler goes first", projection.AfterApply, []string{"apply", "save"}},
		{"inside a unit the claim goes first", projection.InUnit, []string{"save", "apply"}},
	} {
		t.Run(mode.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			stand.append(t, "a", "one")

			var mutex sync.Mutex
			var order []string
			record := func(what string) {
				mutex.Lock()
				defer mutex.Unlock()
				order = append(order, what)
			}
			stand.points.onSave = func(event.Checkpoint, int64) (bool, error) {
				record("save")
				return true, nil
			}
			spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
				record("apply")
				stand.model.write(payloadsOf(batch.Envelopes)...)
				return nil
			}))
			spec.Advance = mode.advance
			if mode.advance == projection.InUnit {
				spec.Destination = projection.Unchecked
				spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
					tx, err := stand.checkpoints.Begin(ctx)
					if err != nil {
						return err
					}
					if err := work(eventmemory.WithTransaction(ctx, tx)); err != nil {
						_ = tx.Rollback(ctx)
						return err
					}
					return tx.Commit(ctx)
				}
			}
			running(t, newProjection(t, spec))
			stand.observer.await(t, "the page applied", func(state projection.State) bool {
				return state.Progress.Applied == 1
			})

			mutex.Lock()
			defer mutex.Unlock()
			if !same(order, mode.expected) {
				t.Fatalf("the pass wrote in the order %v where %s", order, mode.what)
			}
		})
	}
}

// The deployment declared this runner a singleton and is running two of it,
// which is what every rolling restart does for a few seconds on purpose. So a
// lost fence is not a halt: the loser takes the row the winner left and carries
// on, and what it costs is loud rather than silent — the streak a lost fence
// opens is cleared by a save that lands and by nothing else, so a projection
// that keeps losing reports through Ready instead of applying every page twice
// in the dark.
func TestTwoLiveInstancesOfOneNameTakeTurnsAndNeitherHalts(t *testing.T) {
	ctx := context.Background()

	t.Run("the loser takes the row it lost and keeps applying", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{MaxRead: 1})
		for _, payload := range []string{"one", "two", "three", "four", "five", "six"} {
			stand.append(t, "a", payload)
		}

		// The other instance, and it is the whole point of the case: it presents
		// the advance this one is about to present, from the same fence, and gets
		// there first every time.
		stand.points.onSave = func(checkpoint event.Checkpoint, _ int64) (bool, error) {
			winner := event.Checkpoint{Projection: checkpoint.Projection, Cursor: checkpoint.Cursor, Advance: checkpoint.Advance}
			if err := stand.checkpoints.Save(ctx, winner); err != nil {
				return false, err
			}
			return true, nil
		}
		spec := stand.spec("orders", applies(stand.model))
		spec.Tolerate = time.Second
		spec.Backoff = projection.Backoff{First: 250 * time.Millisecond, Max: time.Second}
		held := newProjection(t, spec)
		running(t, held)

		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		for _, delay := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second} {
			stand.ticks.expect(t, delay, "the backoff after the fence was lost")
			contended := stand.observer.await(t, "it is retrying over a second writer", func(state projection.State) bool {
				return state.Phase == projection.PhaseRetrying && errors.Is(state.Err, projection.ErrOvertaken)
			})
			if !errors.Is(contended.Err, projection.ErrOvertaken) {
				t.Fatalf("a lost fence was published as %v", contended.Err)
			}
			stand.ticks.fire(t)
		}

		// The fourth interval is asked for after the third was waited out, which is
		// what makes the backoff this projection has accumulated a number to read
		// rather than one to race.
		stand.ticks.expect(t, time.Second, "the backoff after the fence was lost")
		if err := held.Ready(ctx); !errors.Is(err, projection.ErrOvertaken) {
			t.Fatalf("a projection that has lost the fence three times over 1.75s of backoff answers %v, and a second live writer at one name is a deployment error nobody is told about any other way", err)
		}
		if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
			t.Fatal("a projection that lost the fence halted, and a rolling deploy runs two instances of a singleton on purpose")
		}

		for range 2 {
			stand.ticks.fire(t)
			stand.ticks.expect(t, time.Second, "the backoff after the fence was lost")
		}
		stand.ticks.fire(t)
		stand.observer.await(t, "the whole log was applied", func(projection.State) bool {
			return len(stand.model.rows()) == 6
		})
		if rows := stand.model.rows(); !same(rows, []string{"one", "two", "three", "four", "five", "six"}) {
			t.Fatalf("the losing instance applied %v, where taking the row it lost resumes at that row's own cursor", rows)
		}
		if row := stand.row(t, "orders"); row.Advance != 6 {
			t.Fatalf("the checkpoint is at advance %d after six pages one writer took every time", row.Advance)
		}
	})

	t.Run("the control: one instance alone loses nothing and stays healthy", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{MaxRead: 1})
		for _, payload := range []string{"one", "two", "three"} {
			stand.append(t, "a", payload)
		}
		held := newProjection(t, stand.spec("orders", applies(stand.model)))
		running(t, held)

		stand.observer.await(t, "it is following", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing
		})
		if rows := stand.model.rows(); !same(rows, []string{"one", "two", "three"}) {
			t.Fatalf("one instance alone applied %v", rows)
		}
		if err := held.Ready(ctx); err != nil {
			t.Fatalf("one instance alone answered %v, so the readiness above reports on everything", err)
		}
		if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
			t.Fatal("one instance alone halted")
		}
	})
}

// The advance a settling load finds is not proof of who wrote it. The fence
// admits one writer at each advance, and a second live instance of one name
// reaches the presented advance the moment this pass's unit rolls back and
// releases the row — so what tells this pass's own save from that instance's is
// the cursor the row carries, and resuming from this pass's own instead leaves
// every position between the two applied by nobody and behind the checkpoint
// for good.
func TestASettlementResumesFromTheRowsCursorAndNeverFromItsOwn(t *testing.T) {
	ctx := context.Background()
	payloads := []string{"one", "two", "three", "four"}

	for _, arm := range []struct {
		what    string
		short   bool
		applied []string
	}{
		{"the row carries another instance's cursor, so everything above it is delivered again", true, []string{"two", "three", "four"}},
		{"the row carries this pass's own cursor, so the page it accounts for is not delivered again", false, []string{"four"}},
	} {
		t.Run(arm.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{MaxRead: 3})
			for _, payload := range payloads {
				stand.append(t, "a", payload)
			}
			behind := cursorAfterOnePage(t, stand)

			// The other instance: it takes the advance this pass is presenting,
			// from the same fence, and its own cursor is a page behind this
			// pass's because it read a page at a time. What this pass's save
			// then gets is an answer that carries no outcome at all.
			stand.points.onSave = func(checkpoint event.Checkpoint, saves int64) (bool, error) {
				if saves != 1 {
					return true, nil
				}
				winner := event.Checkpoint{
					Projection: checkpoint.Projection,
					Cursor:     checkpoint.Cursor,
					Advance:    checkpoint.Advance,
					Progress:   event.Progress{Highest: 1, Applied: 1, At: time.Now()},
				}
				if arm.short {
					winner.Cursor = behind
				}
				if err := stand.checkpoints.Save(ctx, winner); err != nil {
					return false, err
				}
				return false, event.Failure(event.Unconfirmed, nil)
			}

			spec := stand.spec("orders", applies(stand.model))
			spec.Ticks = runtime.SystemTicks
			spec.Idle = 10 * time.Millisecond
			spec.Backoff = projection.Backoff{First: 5 * time.Millisecond, Max: 20 * time.Millisecond}
			spec.Advance = projection.InUnit
			spec.Destination = projection.Unchecked
			spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
				tx, err := stand.checkpoints.Begin(ctx)
				if err != nil {
					return err
				}
				if err := work(eventmemory.WithTransaction(ctx, tx)); err != nil {
					_ = tx.Rollback(ctx)
					return err
				}
				return tx.Commit(ctx)
			}
			held := newProjection(t, spec)
			running(t, held)

			deadline := time.After(settle)
			for {
				if row := stand.row(t, "orders"); row.Advance == 2 {
					break
				}
				select {
				case <-deadline:
					t.Fatalf("the settled projection never saved a second advance: the row is %+v and it applied %v", stand.row(t, "orders"), stand.model.rows())
				case <-time.After(time.Millisecond):
				}
			}

			if rows := stand.model.rows(); !same(rows, arm.applied) {
				t.Fatalf("the settled projection applied %v where the row it settled against carries the cursor after %v, and every position above that cursor is one no other instance can have applied",
					rows, payloads[:1])
			}
			if state := held.State(); state.Phase == projection.PhaseHalted {
				t.Fatalf("the settled projection halted with %v", state.Err)
			}
		})
	}
}

// The cursor a reader of one page at a time stands at after the first event,
// which is what a second instance reading a shorter page has recorded.
func cursorAfterOnePage(t *testing.T, stand *stand) event.Cursor {
	t.Helper()
	reader, err := event.Read(event.ReadOnly(newStore(t, stand.log, eventmemory.Spec{MaxRead: 1})), "")
	if err != nil {
		t.Fatalf("a reader over the same log was refused: %v", err)
	}
	if more, err := reader.Next(context.Background()); err != nil || !more {
		t.Fatalf("the first page of the log answered %v (more %v), so this case has no shorter cursor to hand the other instance", err, more)
	}
	return reader.Cursor()
}

// A unit of work runs the work it is given exactly once, and the shape that
// breaks that is the ordinary one: a caller's own retry around the transaction,
// which is what [[D-126]] and [[D-040]] tell a caller to own because the store
// may not. The second run must not present a second advance over a fence the
// first run already moved, so it is refused by name before it writes anything —
// and what the pass does then is what the row says, which is that the whole unit
// went back and the page is where the projection left it. So: the page is
// re-delivered, it is applied exactly once, and the projection does not halt.
func TestAUnitThatRunsTheWorkTwiceIsRefusedAndThePageIsRedelivered(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "one")

	rows := &staging{}
	var losing atomic.Bool
	losing.Store(true)
	spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
		rows.write(payloadsOf(batch.Envelopes)...)
		return nil
	}))
	spec.Advance = projection.InUnit
	spec.Destination = projection.Unchecked
	var ran atomic.Int64
	spec.Unit = func(ctx context.Context, work func(context.Context) error) error {
		var refused error
		for range 2 {
			ran.Add(1)
			tx, err := stand.checkpoints.Begin(ctx)
			if err != nil {
				return err
			}
			if err := work(eventmemory.WithTransaction(ctx, tx)); err != nil {
				_ = tx.Rollback(ctx)
				rows.discard()
				refused = err
				continue
			}
			if losing.CompareAndSwap(true, false) {
				_ = tx.Rollback(ctx)
				rows.discard()
				refused = errCommitLost
				continue
			}
			if err := tx.Commit(ctx); err != nil {
				rows.discard()
				return err
			}
			rows.commit()
			return nil
		}
		return refused
	}
	running(t, newProjection(t, spec))

	stand.ticks.expect(t, time.Second, "the poll the loop opens with")
	retrying := stand.observer.await(t, "the pass was returned to retrying", func(state projection.State) bool {
		return state.Phase == projection.PhaseRetrying
	})
	if reported := retrying.Err.Error(); !strings.Contains(reported, "ran the work it was given more than once") {
		t.Fatalf("a unit that ran the work twice was reported as %q, and a caller reading that learns nothing about the field that is wrong", reported)
	}
	stand.ticks.expect(t, 250*time.Millisecond, "the backoff after the unit ran the work twice")
	stand.ticks.fire(t)

	stand.observer.await(t, "the page applied", func(state projection.State) bool {
		return state.Progress.Applied == 1
	})
	if held := rows.committed(); !same(held, []string{"one"}) {
		t.Fatalf("the read model holds %v, where a unit that ran the work twice leaves the page applied once", held)
	}
	if row := stand.row(t, "orders"); row.Advance != 1 {
		t.Fatalf("the checkpoint is at advance %d for one applied page, where a pass presents one advance however many times its unit runs the work", row.Advance)
	}
	if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
		t.Fatalf("a unit that ran the work twice halted the projection, and the answer to a caller's own retry loop is to re-deliver the page: %v", stand.observer.await(t, "it halted", func(state projection.State) bool { return state.Phase == projection.PhaseHalted }).Err)
	}
	if runs := ran.Load(); runs != 3 {
		t.Fatalf("the unit was asked to run the work %d times over two passes, where the first pass runs it twice and the second once", runs)
	}
}

// The one door errFenceRefused is behind, and it is a store that broke its own
// contract rather than a second writer: it refused a save at the very advance
// its fence admits, and the row it did not move says so. The control is the same
// refusal from a store whose row DID move, which is contention and takes its
// turn — so a halt on every conflict fails the second half.
func TestACheckpointStoreThatRefusesASaveItsFenceAdmitsHalts(t *testing.T) {
	ctx := context.Background()

	t.Run("the row did not move, so the store refused what it admits", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")
		stand.points.onSave = func(event.Checkpoint, int64) (bool, error) {
			return false, event.Failure(event.Conflict, nil)
		}
		running(t, newProjection(t, stand.spec("orders", applies(stand.model))))

		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, projection.ErrHalted) {
			t.Fatalf("a store that refused a save its own fence admits halted with %v", halted.Err)
		}
		if reported := halted.Err.Error(); !strings.Contains(reported, "refused a save over the very row its own fence admits") {
			t.Fatalf("the halt reads %q, and an operator cannot tell a broken store from a second writer by that", reported)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the row is at advance %d, so this case proved something else", row.Advance)
		}
	})

	t.Run("the control: the same refusal over a row that moved is contention", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")
		stand.points.onSave = func(checkpoint event.Checkpoint, _ int64) (bool, error) {
			winner := event.Checkpoint{Projection: checkpoint.Projection, Cursor: checkpoint.Cursor, Advance: checkpoint.Advance}
			if err := stand.checkpoints.Save(ctx, winner); err != nil {
				return false, err
			}
			return false, event.Failure(event.Conflict, nil)
		}
		running(t, newProjection(t, stand.spec("orders", applies(stand.model))))

		contended := stand.observer.await(t, "it is retrying over a second writer", func(state projection.State) bool {
			return state.Phase == projection.PhaseRetrying && errors.Is(state.Err, projection.ErrOvertaken)
		})
		if !errors.Is(contended.Err, projection.ErrOvertaken) {
			t.Fatalf("a conflict over a row that moved was published as %v", contended.Err)
		}
		if count := stand.observer.counted(projection.PhaseHalted); count != 0 {
			t.Fatal("a conflict over a row that moved halted, and that is the rolling deploy this halt must not catch")
		}
	})
}

// Two values of one name, both running, over one log and one checkpoint row.
// Under InUnit against a checkpoint store whose fenced save is evaluated under
// the row's own lock, the loser is refused before its handler runs and no event
// is applied twice. Under AfterApply there is no lock to take across a handler
// call and both instances apply the overlapping page — which is what at least
// once means and what the module page has to say out loud.
func TestTwoLiveInstancesUnderInUnitApplyEachEventOnce(t *testing.T) {
	payloads := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}

	for _, mode := range []struct {
		what    string
		advance projection.Advance
		once    bool
	}{
		{"under InUnit the loser never applies", projection.InUnit, true},
		{"under AfterApply the loser has already applied", projection.AfterApply, false},
	} {
		t.Run(mode.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{MaxRead: 1})
			for _, payload := range payloads {
				stand.append(t, "a", payload)
			}

			var mutex sync.Mutex
			applied := map[string]int{}
			handler := projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
				mutex.Lock()
				defer mutex.Unlock()
				for _, payload := range payloadsOf(batch.Envelopes) {
					applied[payload]++
				}
				return nil
			})

			// The row lock a live checkpoint store takes at its fenced save and
			// holds until the unit ends: the second instance's claim waits here and
			// is refused against the row the winner committed.
			var lock sync.Mutex
			unit := func(ctx context.Context, work func(context.Context) error) error {
				lock.Lock()
				defer lock.Unlock()
				tx, err := stand.checkpoints.Begin(ctx)
				if err != nil {
					return err
				}
				if err := work(eventmemory.WithTransaction(ctx, tx)); err != nil {
					_ = tx.Rollback(ctx)
					return err
				}
				return tx.Commit(ctx)
			}

			both := make([]*projection.Projection, 0, 2)
			for range 2 {
				spec := stand.spec("orders", handler)
				spec.Ticks = runtime.SystemTicks
				spec.Idle = 10 * time.Millisecond
				spec.Backoff = projection.Backoff{First: 5 * time.Millisecond, Max: 20 * time.Millisecond}
				spec.Advance = mode.advance
				if mode.advance == projection.InUnit {
					spec.Destination = projection.Unchecked
					spec.Unit = unit
				}
				held := newProjection(t, spec)
				running(t, held)
				both = append(both, held)
			}

			deadline := time.After(settle)
			for {
				if row := stand.row(t, "orders"); row.Progress.Highest == event.Position(len(payloads)) {
					break
				}
				select {
				case <-deadline:
					t.Fatalf("two instances of one name never drained a log of %d events: the row is %+v", len(payloads), stand.row(t, "orders"))
				case <-time.After(time.Millisecond):
				}
			}

			mutex.Lock()
			defer mutex.Unlock()
			for _, payload := range payloads {
				if applied[payload] == 0 {
					t.Fatalf("%q was never applied by either instance, and a checkpoint that advanced past it is a lost event", payload)
				}
				if mode.once && applied[payload] != 1 {
					t.Fatalf("%q was applied %d times, where a claim taken before the handler is one the losing instance never gets past", payload, applied[payload])
				}
			}
			for _, held := range both {
				if state := held.State(); state.Phase == projection.PhaseHalted {
					t.Fatalf("an instance halted with %v, and a second live writer at one name is not a reason to stop applying", state.Err)
				}
			}
		})
	}
}

package projection_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

// The origin is where a projection with no row starts, and the control is the
// half that makes that mean something: a second value over the same stores
// resumes at the saved cursor and applies only what the log grew by.
func TestAFirstRunStartsAtTheOriginAndASecondResumes(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "first", "second")

	first := newProjection(t, stand.spec("orders", applies(stand.model)))
	stop, returned := running(t, first)
	stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
		return state.Progress.Applied == 2
	})
	stop()
	returns(t, returned)

	if rows := stand.model.rows(); !same(rows, []string{"first", "second"}) {
		t.Fatalf("a projection with no row applied %v, where a first run starts at the origin", rows)
	}
	if row := stand.row(t, "orders"); row.Advance != 1 || row.Progress.Applied != 2 {
		t.Fatalf("the first save left %+v, where a first save carries advance 1 and the envelopes it applied", row)
	}

	stand.append(t, "a", "third")
	second := newProjection(t, stand.spec("orders", applies(stand.model)))
	running(t, second)
	stand.observer.await(t, "the second run applied what the log grew by", func(state projection.State) bool {
		return state.Progress.Applied == 3
	})

	if rows := stand.model.rows(); !same(rows, []string{"first", "second", "third"}) {
		t.Fatalf("a second value over the same stores applied %v, where it resumes at the saved cursor rather than re-applying the log", rows)
	}
	if row := stand.row(t, "orders"); row.Advance != 2 {
		t.Fatalf("the second run's save is at advance %d, where the fence is the row's plus one", row.Advance)
	}
}

// A cursor this build cannot read is where the tempting repair is the wrong one:
// starting again at the origin re-applies the whole log against a live read
// model, with no error on any path. The control is absence, which IS the origin,
// so the refusal is discriminating rather than universal.
func TestAnUnreadableCursorHaltsAndNeverRestartsAtTheOrigin(t *testing.T) {
	elsewhere := newStand(t, eventmemory.Spec{})
	elsewhere.append(t, "a", "somewhere else")
	_, foreign, err := elsewhere.store.ReadAll(context.Background(), "")
	if err != nil {
		t.Fatalf("minting a cursor over another log answered %v", err)
	}

	for _, unreadable := range []struct {
		what   string
		cursor event.Cursor
	}{
		{"a cursor minted over another log", foreign},
		{"a cursor in a format this store does not mint", event.Cursor("vv-retired-format-1")},
	} {
		t.Run(unreadable.what, func(t *testing.T) {
			stand := newStand(t, eventmemory.Spec{})
			stand.append(t, "a", "first")
			written := event.Checkpoint{
				Projection: "orders", Cursor: unreadable.cursor, Advance: 1,
				Progress: event.Progress{Highest: 40, Applied: 40, At: time.Now()},
			}
			if err := stand.checkpoints.Save(context.Background(), written); err != nil {
				t.Fatalf("writing the row this case is about answered %v", err)
			}

			running(t, newProjection(t, stand.spec("orders", applies(stand.model))))
			halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
				return state.Phase == projection.PhaseHalted
			})
			if !errors.Is(halted.Err, event.ErrCursor) {
				t.Fatalf("%s halted with %v, where a cursor this build cannot read is ErrCursor", unreadable.what, halted.Err)
			}
			if rows := stand.model.rows(); len(rows) != 0 {
				t.Fatalf("%v was applied from a checkpoint that could not be read, so the walk restarted at the origin", rows)
			}
			if row := stand.row(t, "orders"); row != written {
				t.Fatalf("the row this projection refused reads back as %+v where it was written as %+v, and an operator's only repair is the row that is there", row, written)
			}
		})
	}

	t.Run("the control: no row at all starts at the origin", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "first")
		running(t, newProjection(t, stand.spec("orders", applies(stand.model))))
		stand.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})
		if rows := stand.model.rows(); !same(rows, []string{"first"}) {
			t.Fatalf("a projection with no row applied %v, so the two refusals above are a halt on everything", rows)
		}
	})
}

// Retiring the name of a running projection is visible and correct rather than
// silent: the next save finds no row at its advance, and creating a fresh one at
// advance 1 would leave the read model holding events no checkpoint accounts for.
func TestAForgottenCheckpointRefusesTheNextSaveAndHalts(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "first")

	running(t, newProjection(t, stand.spec("orders", applies(stand.model))))
	stand.observer.await(t, "the first page was applied", func(state projection.State) bool {
		return state.Progress.Applied == 1
	})
	stand.ticks.expect(t, time.Second, "the poll")
	stand.observer.await(t, "it is following", func(state projection.State) bool {
		return state.Phase == projection.PhaseFollowing
	})

	stand.append(t, "a", "second")
	if err := stand.checkpoints.Forget(context.Background(), "orders"); err != nil {
		t.Fatalf("forgetting the name of a running projection answered %v", err)
	}
	stand.ticks.fire(t)

	halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
		return state.Phase == projection.PhaseHalted
	})
	if !errors.Is(halted.Err, event.ErrConflict) {
		t.Fatalf("a save over a row that was retired underneath it answered %v, where the fence refuses it as a conflict", halted.Err)
	}
	if row := stand.row(t, "orders"); !row.Fresh() {
		t.Fatalf("a row at advance %d exists after the name was forgotten, so the projection re-seated its own fence and started a new history", row.Advance)
	}
}

// An idle projection costs the database one read a poll and nothing else: a save
// for a page it did not apply would be a write per poll for ever, and discarding
// the in-memory cursor would re-mint a settlement bound every pass.
func TestNIdlePollsIssueZeroSavesAndNRoundTrips(t *testing.T) {
	const polls = 4
	stand := newStand(t, eventmemory.Spec{})

	arrived := make(chan event.Cursor, polls+2)
	stand.read.answer = func(_ context.Context, after event.Cursor, reads int64) ([]event.Envelope, event.Cursor, error) {
		arrived <- after
		return nil, event.Cursor("settled-" + strconv.FormatInt(reads, 10)), nil
	}

	running(t, newProjection(t, stand.spec("orders", applies(stand.model))))
	stand.ticks.expect(t, time.Second, "the poll")
	for range polls - 1 {
		stand.ticks.fire(t)
	}

	seen := make([]event.Cursor, 0, polls)
	for range polls {
		select {
		case cursor := <-arrived:
			seen = append(seen, cursor)
		case <-time.After(settle):
			t.Fatalf("only %d of %d polls reached the log", len(seen), polls)
		}
	}
	if saves := stand.points.saves.Load(); saves != 0 {
		t.Fatalf("%d checkpoint saves were issued over %d idle polls, and an idle projection issues none", saves, polls)
	}
	if reads := stand.read.reads.Load(); reads != polls {
		t.Fatalf("%d round trips were made over %d idle polls", reads, polls)
	}
	for index, cursor := range seen {
		want := event.Cursor("")
		if index > 0 {
			want = event.Cursor("settled-" + strconv.Itoa(index))
		}
		if cursor != want {
			t.Fatalf("poll %d resumed from %q where the previous one answered %q, so the walk's own settling state is thrown away every pass", index+1, cursor, want)
		}
	}
	if rows := stand.model.rows(); len(rows) != 0 {
		t.Fatalf("an idle projection applied %v", rows)
	}
}

// A wake is a hint layered on a poll that always runs, and a closed channel is
// the one shape that would make it the delivery mechanism instead: it is
// permanently ready, so a loop that received from it without asking whether it
// was still open would read the log as fast as the store could answer, for ever.
// Closing a channel is how Go broadcasts to an unknown number of waiters, so a
// composition root that closes Wake at shutdown is the ordinary case rather than
// the exotic one. The control is the open channel, which must still release the
// poll — a loop that ignored Wake entirely would pass the first half alone.
func TestAClosedWakeStopsWakingAndAnOpenOneStillDoes(t *testing.T) {
	t.Run("a closed wake channel wakes nothing", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		wake := make(chan struct{})
		close(wake)
		spec := stand.spec("orders", applies(stand.model))
		spec.Wake = wake
		running(t, newProjection(t, spec))

		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		time.Sleep(200 * time.Millisecond)
		if reads := stand.read.reads.Load(); reads > 2 {
			t.Fatalf("a closed Wake read the log %d times in 200ms with no tick fired, and a wake that is not a hint is a busy loop against the store", reads)
		}
	})

	t.Run("an open wake channel still releases the poll", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		wake := make(chan struct{})
		spec := stand.spec("orders", applies(stand.model))
		spec.Wake = wake
		running(t, newProjection(t, spec))

		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		select {
		case wake <- struct{}{}:
		case <-time.After(settle):
			t.Fatal("the loop was never waiting on Wake, so a signal reaches nothing")
		}
		deadline := time.After(settle)
		for stand.read.reads.Load() < 2 {
			select {
			case <-deadline:
				t.Fatalf("a wake released nothing: the log was read %d times", stand.read.reads.Load())
			case <-time.After(time.Millisecond):
			}
		}
	})
}

// Draining and following are one loop with one branch: pages arrive back to back
// with no wait between them, the first empty one is what following means, and
// nothing is saved after it.
func TestADrainingProjectionReachesFollowingAndStaysThere(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{MaxRead: 2})
	stand.append(t, "a", "one", "two", "three", "four", "five")

	running(t, newProjection(t, stand.spec("orders", applies(stand.model))))
	stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
		return state.Progress.Applied == 5
	})
	stand.observer.await(t, "it is following", func(state projection.State) bool {
		return state.Phase == projection.PhaseFollowing
	})

	if stand.observer.counted(projection.PhaseDraining) == 0 {
		t.Fatal("a projection that applied five events over three pages never published that it was draining")
	}
	if saves := stand.points.saves.Load(); saves != 3 {
		t.Fatalf("%d saves were issued for three applied pages, and the checkpoint advances once per page a projection finished with", saves)
	}
	if row := stand.row(t, "orders"); row.Advance != 3 || row.Progress.Applied != 5 {
		t.Fatalf("three applied pages left %+v", row)
	}

	stand.ticks.expect(t, time.Second, "the poll")
	for range 3 {
		stand.ticks.fire(t)
	}
	stand.observer.await(t, "the polls happened", func(projection.State) bool { return true })
	if saves := stand.points.saves.Load(); saves != 3 {
		t.Fatalf("%d saves were issued once the log had been drained, and following issues none", saves)
	}
	if state := stand.observer.await(t, "it is still following", func(state projection.State) bool {
		return state.Phase == projection.PhaseFollowing
	}); state.Phase != projection.PhaseFollowing {
		t.Fatal("following is not terminal and this projection left it with nothing to apply")
	}

	stand.append(t, "a", "six")
	stand.ticks.fire(t)
	stand.observer.await(t, "the later append was applied", func(state projection.State) bool {
		return state.Progress.Applied == 6
	})
	if rows := stand.model.rows(); !same(rows, []string{"one", "two", "three", "four", "five", "six"}) {
		t.Fatalf("the log was applied as %v", rows)
	}
}

// Terminal and retryable are told apart by behaviour rather than by a table
// nobody runs: a closed store halts, and a backend that went away and came back
// is applied without a halt and without a duplicate save.
func TestAClosedStoreHaltsAndATransientBackendRecovers(t *testing.T) {
	t.Run("a closed store halts", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "first")
		stand.read.answer = func(context.Context, event.Cursor, int64) ([]event.Envelope, event.Cursor, error) {
			return nil, "", event.Failure(event.Closed, nil)
		}
		running(t, newProjection(t, stand.spec("orders", applies(stand.model))))

		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, event.ErrClosed) {
			t.Fatalf("a closed store halted with %v", halted.Err)
		}
		stand.ticks.expect(t, time.Second, "the poll the loop opens with")
		stand.ticks.quiet(t, 100*time.Millisecond)
	})

	t.Run("a transient backend recovers", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "first")
		stand.read.answer = func(ctx context.Context, after event.Cursor, reads int64) ([]event.Envelope, event.Cursor, error) {
			if reads <= 3 {
				return nil, "", event.Failure(event.NotWritten, nil)
			}
			return stand.read.Log.ReadAll(ctx, after)
		}
		running(t, newProjection(t, stand.spec("orders", applies(stand.model))))

		stand.ticks.expect(t, time.Second, "the poll")
		for _, delay := range []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second} {
			stand.ticks.expect(t, delay, "the backoff after a backend failure")
			stand.ticks.fire(t)
		}
		stand.observer.await(t, "the page was applied", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})
		if stand.observer.counted(projection.PhaseHalted) != 0 {
			t.Fatal("a backend that came back halted the projection, and a store failure retries without limit")
		}
		if saves := stand.points.saves.Load(); saves != 1 {
			t.Fatalf("%d saves were issued for one page that was applied once", saves)
		}
		if rows := stand.model.rows(); !same(rows, []string{"first"}) {
			t.Fatalf("the page recovered as %v", rows)
		}
	})
}

// A payload this build cannot read does not become readable by trying again, and
// the refusal that says so carries the type and the revision and neither the key
// nor the payload.
func TestAHistoryClassFailureHaltsAndNamesNoData(t *testing.T) {
	const key = "secret-key-9f7"
	const item = "secret-payload-b31"

	stand := newStand(t, eventmemory.Spec{})
	current := declareOrders(t)
	wrote(t, writes(t, stand.store, current.aggregate), key, current.placed.New(key, placed{Item: item, Quantity: 2}))

	// The reading declaration retains one revision where the writing one retained
	// two, which is the deploy that has not caught up yet.
	behind := declareOrdersV1(t)
	router := projection.NewRouter(projection.SkipForeign)
	projection.On(router, behind.placed, func(_ context.Context, carried placedV1, _ event.Envelope) error {
		stand.model.write(carried.Item)
		return nil
	})

	running(t, newProjection(t, stand.spec("orders", router)))
	halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
		return state.Phase == projection.PhaseHalted
	})
	if !errors.Is(halted.Err, event.ErrRevision) {
		t.Fatalf("a revision this build does not retain halted with %v", halted.Err)
	}
	if projection.Classify(halted.Err) != projection.Permanent {
		t.Fatal("the default classifier calls a history-class failure retryable, so a build that cannot read a payload retries it for ever")
	}
	for _, secret := range []string{key, item} {
		if strings.Contains(halted.Err.Error(), secret) {
			t.Fatalf("the refusal is %q and renders data a log line must never carry", halted.Err)
		}
	}
	if row := stand.row(t, "orders"); !row.Fresh() {
		t.Fatalf("the checkpoint advanced to %d over an event nothing applied", row.Advance)
	}
	if rows := stand.model.rows(); len(rows) != 0 {
		t.Fatalf("%v was applied through a fact that refused the payload", rows)
	}
}

// The read is outside every unit of work, and it is not a style preference: a
// store's walk mints no settlement bound while a transaction of its backing is
// bound, so a walk inside the projection's own write transaction stops at the
// first burnt gap and stays there for the life of the transaction. Every test
// over a gapless log would be green.
func TestEveryReadArrivesOutsideEveryUnit(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{MaxRead: 1})
	stand.append(t, "a", "one", "two", "three")

	inside := 0
	stand.read.arrived = func(ctx context.Context) {
		authority, err := stand.checkpoints.Transaction(ctx)
		if err != nil || authority.Valid() {
			inside++
			t.Errorf("a read arrived on a context carrying a transaction of the log's backing (%v), and a walk inside the write cannot pass a burnt gap", err)
		}
	}

	spec := stand.spec("orders", applies(stand.model))
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
	running(t, newProjection(t, spec))

	stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
		return state.Progress.Applied == 3
	})
	if reads := stand.read.reads.Load(); reads < 4 {
		t.Fatalf("only %d reads were made, so this proves almost nothing about where they arrived", reads)
	}
	if inside != 0 {
		t.Fatalf("%d reads arrived inside a unit of work", inside)
	}
}

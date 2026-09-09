package eventmemory_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/eventtest"
)

func newCheckpoints(t testing.TB, spec eventmemory.CheckpointSpec) *eventmemory.Checkpoints {
	t.Helper()
	held, err := eventmemory.NewCheckpoints(spec)
	if err != nil {
		t.Fatalf("a checkpoint store over %+v was refused: %v", spec, err)
	}
	t.Cleanup(func() { _ = held.Close() })
	return held
}

func TestTheCheckpointStoreSatisfiesTheContract(t *testing.T) {
	eventtest.RunCheckpoints(t, checkpointConformance(t))
}

// One log for the whole run, because the cursors this factory answers are that
// log's own and a second log's would be foreign to every row already written.
// Every value New builds is a second value over it, which is what a restart is.
func checkpointConformance(t *testing.T) eventtest.CheckpointFactory {
	log := newLog(t, eventmemory.LogSpec{})
	minting := &minter{store: newStore(t, eventmemory.Spec{Log: log})}
	return eventtest.CheckpointFactory{
		New: func(t *testing.T) event.Checkpoints {
			return newCheckpoints(t, eventmemory.CheckpointSpec{Log: log})
		},
		Begin: func(t *testing.T, ctx context.Context, c event.Checkpoints) (context.Context, eventtest.Tx) {
			held, is := c.(*eventmemory.Checkpoints)
			if !is {
				t.Fatalf("the suite asked a checkpoint store this factory did not build to begin a transaction: %T", c)
			}
			tx, err := held.Begin(ctx)
			if err != nil {
				t.Fatalf("beginning a transaction of the log answered %v", err)
			}
			return eventmemory.WithTransaction(ctx, tx), tx
		},
		Sibling: func(t *testing.T, _ event.Checkpoints) event.Checkpoints {
			return newCheckpoints(t, eventmemory.CheckpointSpec{Log: log})
		},
		Cursor: minting.next,
	}
}

// A cursor of this log, minted by writing to it and reading the page back, so
// what the sections save is what a walk of this store would have persisted
// rather than a literal invented here.
type minter struct {
	store *eventmemory.Store
	at    atomic.Uint64
	last  atomic.Pointer[event.Cursor]
}

func (this *minter) next(t *testing.T) event.Cursor {
	t.Helper()
	ctx := context.Background()
	stream := streamOf("eventmemory.checkpoints", "cursor/"+strconv.FormatUint(this.at.Add(1), 10))
	if err := this.store.Append(ctx, event.AppendRequest{Stream: stream, Records: records("minted")}); err != nil {
		t.Fatalf("the cursor this suite saves could not be minted: %v", err)
	}
	var from event.Cursor
	if held := this.last.Load(); held != nil {
		from = *held
	}
	_, minted, err := this.store.ReadAll(ctx, from)
	if err != nil {
		t.Fatalf("reading the log for a cursor to save answered %v", err)
	}
	this.last.Store(&minted)
	return minted
}

func TestForgetTouchesOneNameAndAnAbsentOneIsNotARefusal(t *testing.T) {
	ctx := context.Background()
	log, store := openStore(t)
	held := newCheckpoints(t, eventmemory.CheckpointSpec{Log: log})
	minting := &minter{store: store}

	mine := event.Checkpoint{Projection: "orders.v1", Cursor: minting.next(t), Advance: 1,
		Progress: event.Progress{Highest: 3, Applied: 3}}
	beside := event.Checkpoint{Projection: "orders.v2", Cursor: minting.next(t), Advance: 1,
		Progress: event.Progress{Highest: 5, Applied: 5, Quarantined: 1}}
	saveCheckpoint(t, ctx, held, mine)
	saveCheckpoint(t, ctx, held, beside)

	if err := held.Forget(ctx, mine.Projection); err != nil {
		t.Fatalf("forgetting a name that has a row answered %v", err)
	}
	if after := loadCheckpoint(t, ctx, held, mine.Projection); !after.Fresh() {
		t.Fatalf("a name that was forgotten answers advance %d", after.Advance)
	}
	if after := loadCheckpoint(t, ctx, held, beside.Projection); after != beside {
		t.Fatalf("forgetting %q left %q at %+v where it held %+v, so a retirement reached a projection nobody retired",
			mine.Projection, beside.Projection, after, beside)
	}
	if err := held.Forget(ctx, mine.Projection); err != nil {
		t.Fatalf("forgetting a name that has no row answered %v, and a name nobody ever saved is already forgotten", err)
	}
	if err := held.Forget(ctx, "orders.never-run"); err != nil {
		t.Fatalf("forgetting a name nothing ever saved answered %v", err)
	}
}

// Two values over one log are one checkpoint store, which is what a restart
// resumes through: the value that wrote the row does not exist any more. The
// control is the second half — a value over another log holds none of it, so the
// rows are the log's and not this process's.
func TestTwoCheckpointValuesOverOneLogAreOneStore(t *testing.T) {
	ctx := context.Background()
	log, store := openStore(t)
	first := newCheckpoints(t, eventmemory.CheckpointSpec{Log: log})
	second := newCheckpoints(t, eventmemory.CheckpointSpec{Log: log})
	elsewhere := newCheckpoints(t, eventmemory.CheckpointSpec{Log: newLog(t, eventmemory.LogSpec{})})
	minting := &minter{store: store}

	written := event.Checkpoint{Projection: "orders.v1", Cursor: minting.next(t), Advance: 1,
		Progress: event.Progress{Highest: 9, Applied: 9}}
	saveCheckpoint(t, ctx, first, written)

	if !second.Backing().Equal(first.Backing()) {
		t.Fatal("two checkpoint values over one log name two backings, so a consumer cannot tell a restart from a second deployment")
	}
	if through := loadCheckpoint(t, ctx, second, written.Projection); through != written {
		t.Fatalf("a value built beside the one that wrote answers %+v where the row is %+v", through, written)
	}
	if err := second.Save(ctx, event.Checkpoint{Projection: written.Projection, Cursor: minting.next(t), Advance: 1}); err == nil {
		t.Fatal("a second value over one log admitted a save at the advance the first one's row already holds, so the two are two stores and each is fencing against its own")
	}

	if elsewhere.Backing().Equal(first.Backing()) {
		t.Fatal("a checkpoint store over another log names the same backing as this one")
	}
	if through := loadCheckpoint(t, ctx, elsewhere, written.Projection); !through.Fresh() {
		t.Fatalf("a checkpoint store over another log answers %+v for a row this one wrote, so the rows are the process's rather than the log's", through)
	}
}

// Absence is total, and a unit of work that staged a removal is where it is
// easiest to answer half of it: the removal travels as an advance-zero entry, so
// a store that hands that entry back names the projection beside no advance and
// the kernel's own door refuses it as a store that did not answer the question.
// The control is the same load with nothing staged, which answers the row.
func TestALoadInsideAUnitThatStagedAForgetAnswersTheZeroCheckpoint(t *testing.T) {
	ctx := context.Background()
	log, store := openStore(t)
	held := newCheckpoints(t, eventmemory.CheckpointSpec{Log: log})
	minting := &minter{store: store}

	written := event.Checkpoint{Projection: "orders.v1", Cursor: minting.next(t), Advance: 1,
		Progress: event.Progress{Highest: 7, Applied: 7}}
	saveCheckpoint(t, ctx, held, written)

	tx, err := held.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a unit of work through the checkpoint store answered %v", err)
	}
	inside := eventmemory.WithTransaction(ctx, tx)
	if err := held.Forget(inside, written.Projection); err != nil {
		t.Fatalf("forgetting a name inside a unit of work answered %v", err)
	}

	after := loadCheckpoint(t, inside, held, written.Projection)
	if after != (event.Checkpoint{}) {
		t.Fatalf("a load inside a unit that staged a removal answered %+v, where absence is total and a half-absent row is a store that did not answer the question", after)
	}
	tracker, err := event.Track(held, written.Projection)
	if err != nil {
		t.Fatalf("tracking %q answered %v", written.Projection, err)
	}
	if _, err := tracker.Load(inside); err != nil {
		t.Fatalf("the kernel's door refused what this store answered inside a unit that staged a removal: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rolling back the unit answered %v", err)
	}

	control, err := held.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a second unit of work answered %v", err)
	}
	defer func() { _ = control.Rollback(ctx) }()
	if row := loadCheckpoint(t, eventmemory.WithTransaction(ctx, control), held, written.Projection); row != written {
		t.Fatalf("a load inside a unit that staged nothing answers %+v where the row is %+v, so the answer above is not the removal's", row, written)
	}
}

func saveCheckpoint(t *testing.T, ctx context.Context, held *eventmemory.Checkpoints, checkpoint event.Checkpoint) {
	t.Helper()
	if err := held.Save(ctx, checkpoint); err != nil {
		t.Fatalf("saving %q at advance %d answered %v", checkpoint.Projection, checkpoint.Advance, err)
	}
}

func loadCheckpoint(t *testing.T, ctx context.Context, held *eventmemory.Checkpoints, projection string) event.Checkpoint {
	t.Helper()
	found, err := held.Load(ctx, projection)
	if err != nil {
		t.Fatalf("loading the checkpoint of %q answered %v", projection, err)
	}
	return found
}

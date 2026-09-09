package eventtest

import (
	"context"
	"sync"

	"github.com/frostgrove/vv/event"
)

// One inventory of the defects the checkpoint suite is built to detect, in code,
// because a count kept in prose is what drifts: a test runs the section each row
// names against the store that row describes and fails when the section passes.
//
// All five are decorators over a checkpoint store that is otherwise correct, and
// each is a shape a real implementation reaches by writing one statement wrong —
// a read-then-write instead of a conditional update, a Load whose parameter is
// the wrong one, a save issued on a pool while a caller holds a transaction.
type checkpointDefect struct {
	name    string
	section string
	over    func(event.Checkpoints) event.Checkpoints
}

func checkpointDefects() []checkpointDefect {
	return []checkpointDefect{
		{"reads the row and then writes it, so every save lands", "fence",
			func(held event.Checkpoints) event.Checkpoints { return unfenced{held} }},
		{"answers the cursor it held before the last save", "round trip",
			func(held event.Checkpoints) event.Checkpoints {
				return &stale{Checkpoints: held, seen: map[string][]event.Cursor{}}
			}},
		{"reports absence for a row that exists", "absence",
			func(held event.Checkpoints) event.Checkpoints { return absent{held} }},
		{"ignores the projection argument of Load", "names",
			func(held event.Checkpoints) event.Checkpoints { return &oneName{Checkpoints: held} }},
		{"saves outside the caller's transaction", "transactions",
			func(held event.Checkpoints) event.Checkpoints { return detaching{held} }},
	}
}

// The fence read into the process and applied there, which is correct only
// against a row nobody else can move.
type unfenced struct{ event.Checkpoints }

func (this unfenced) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	held, err := this.Checkpoints.Load(ctx, checkpoint.Projection)
	if err != nil {
		return err
	}
	checkpoint.Advance = held.Advance + 1
	return this.Checkpoints.Save(ctx, checkpoint)
}

// A checkpoint one save behind: the row is this store's own and the cursor is
// the one before it, which is what a statement that reads a column of a
// yesterday's copy answers.
type stale struct {
	event.Checkpoints

	mutex sync.Mutex
	seen  map[string][]event.Cursor
}

func (this *stale) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if err := this.Checkpoints.Save(ctx, checkpoint); err != nil {
		return err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.seen[checkpoint.Projection] = append(this.seen[checkpoint.Projection], checkpoint.Cursor)
	return nil
}

func (this *stale) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	held, err := this.Checkpoints.Load(ctx, projection)
	if err != nil || held.Fresh() {
		return held, err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if saved := this.seen[projection]; len(saved) > 1 {
		held.Cursor = saved[len(saved)-2]
	}
	return held, nil
}

type absent struct{ event.Checkpoints }

func (this absent) Load(context.Context, string) (event.Checkpoint, error) {
	return event.Checkpoint{}, nil
}

// The Load whose statement is mis-parameterised: it answers the first row this
// value ever wrote, whoever asks.
type oneName struct {
	event.Checkpoints

	mutex sync.Mutex
	first string
}

func (this *oneName) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if err := this.Checkpoints.Save(ctx, checkpoint); err != nil {
		return err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.first == "" {
		this.first = checkpoint.Projection
	}
	return nil
}

func (this *oneName) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	this.mutex.Lock()
	named := this.first
	this.mutex.Unlock()
	if named != "" {
		projection = named
	}
	return this.Checkpoints.Load(ctx, projection)
}

// The store that never looks for the caller's unit of work: the deadline travels
// and the values do not, which is exactly what a save issued on the pool does.
type detaching struct{ event.Checkpoints }

func (this detaching) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	beside, stop := beside(ctx)
	defer stop()
	return this.Checkpoints.Save(beside, checkpoint)
}

func beside(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, held := ctx.Deadline(); held {
		return context.WithDeadline(context.Background(), deadline)
	}
	return context.WithCancel(context.Background())
}

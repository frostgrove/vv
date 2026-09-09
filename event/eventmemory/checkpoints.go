package eventmemory

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/frostgrove/vv/event"
)

var (
	errCheckpointMoved   = errors.New("eventmemory: the checkpoint row is not at the advance this save follows")
	errCursorEmpty       = errors.New("eventmemory: the empty cursor is the origin of the log rather than a point to resume past")
	errCursorTooLong     = errors.New("eventmemory: this cursor is longer than the ceiling the kernel publishes")
	errProjectionUnnamed = errors.New("eventmemory: a checkpoint is a row of one projection's and this one names none")
	errProjectionTooLong = errors.New("eventmemory: this projection name is longer than the kernel's identifier bound")
)

type CheckpointSpec struct{ Log *Log }

// The rows live on the log rather than here, so two values over one log are one
// checkpoint store and a restart resumes through a value that did not exist when
// the row was written. It joins the log's own ambient transaction, which is what
// lets a consumer prove the one-unit path with no database at all.
type Checkpoints struct {
	log    *Log
	closed atomic.Bool
}

var _ event.Checkpoints = (*Checkpoints)(nil)

// Performs no I/O and starts nothing.
func NewCheckpoints(spec CheckpointSpec) (*Checkpoints, error) {
	if spec.Log == nil {
		return nil, fmt.Errorf("%w: a checkpoint store records against a log and this spec names none", event.ErrWrongStore)
	}
	return &Checkpoints{log: spec.Log}, nil
}

func (this *Checkpoints) Capabilities() event.CheckpointCapabilities {
	return event.CheckpointCapabilities{Transactions: event.Supported, Persistence: event.Unsupported}
}

func (this *Checkpoints) Backing() event.Backing { return this.log.backing }

func (this *Checkpoints) Transaction(ctx context.Context) (event.Authority, error) {
	return this.log.transaction(ctx)
}

// A transaction of the log, opened through the checkpoint store rather than
// through a *Store, so a consumer that records progress and writes nothing to
// the log needs no store value to open a unit of work. WithTransaction binds it
// under the log's own key, so a unit begun through either peer is found by both.
func (this *Checkpoints) Begin(ctx context.Context) (*Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if this.closed.Load() {
		return nil, event.ErrClosed
	}
	return this.log.begin(), nil
}

func (this *Checkpoints) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return event.Checkpoint{}, err
	}
	if this.closed.Load() {
		return event.Checkpoint{}, event.Failure(event.Closed, nil)
	}
	tx, err := this.log.ambient(ctx)
	if err != nil {
		return event.Checkpoint{}, event.Failure(event.Refused, err)
	}

	this.log.mutex.Lock()
	defer this.log.mutex.Unlock()

	if err := tx.live(); err != nil {
		return event.Checkpoint{}, event.Failure(event.Refused, err)
	}
	return this.held(projection, tx), nil
}

// The fence is evaluated here, under the log's mutex, against the rows as this
// transaction's own staged saves amend them, and Commit evaluates it again — so
// a row that moved between the stage and the commit is an error rather than a
// save that overwrote a winner.
func (this *Checkpoints) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.Failure(event.Closed, nil)
	}
	if err := refusable(checkpoint.Projection); err != nil {
		return err
	}
	switch {
	case checkpoint.Cursor == "":
		return event.Failure(event.Refused, errCursorEmpty)
	case len(checkpoint.Cursor) > event.MaxCursorBytes:
		return event.Failure(event.Refused, errCursorTooLong)
	}
	tx, err := this.log.ambient(ctx)
	if err != nil {
		return event.Failure(event.Refused, err)
	}

	this.log.mutex.Lock()
	defer this.log.mutex.Unlock()

	if err := tx.live(); err != nil {
		return event.Failure(event.Refused, err)
	}
	if checkpoint.Advance != this.held(checkpoint.Projection, tx).Advance+1 {
		return event.Failure(event.Conflict, errCheckpointMoved)
	}
	if tx == nil {
		this.log.recordCheckpoint(checkpoint)
		return nil
	}
	tx.stageSave(checkpoint)
	return nil
}

// Not fenced, and an absent name is not a refusal. Inside a unit of work it is
// staged as a save at advance zero, which is how the removal rolls back with
// everything else the unit staged.
func (this *Checkpoints) Forget(ctx context.Context, projection string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.Failure(event.Closed, nil)
	}
	if err := refusable(projection); err != nil {
		return err
	}
	tx, err := this.log.ambient(ctx)
	if err != nil {
		return event.Failure(event.Refused, err)
	}

	this.log.mutex.Lock()
	defer this.log.mutex.Unlock()

	if err := tx.live(); err != nil {
		return event.Failure(event.Refused, err)
	}
	if tx == nil {
		delete(this.log.checkpoints, projection)
		return nil
	}
	tx.stageSave(event.Checkpoint{Projection: projection})
	return nil
}

// It closes nothing: the log was constructed by the composition root and is
// shared with every other value over it, and staged work belongs to the
// transaction that staged it.
func (this *Checkpoints) Close() error {
	this.closed.Store(true)
	return nil
}

func (this *Checkpoints) held(projection string, tx *Tx) event.Checkpoint {
	if tx != nil {
		return tx.checkpointHeld(projection)
	}
	return this.log.checkpointHeld(projection)
}

// The bounds the row's own key has, refused before anything is staged rather
// than at the moment of writing: a name a store cannot key a row by is a caller's
// mistake and not a backend that failed.
func refusable(projection string) error {
	switch {
	case projection == "":
		return event.Failure(event.Refused, errProjectionUnnamed)
	case len(projection) > event.MaxNameBytes:
		return event.Failure(event.Refused, errProjectionTooLong)
	}
	return nil
}

package eventmemory

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/frostgrove/vv/event"
)

var (
	errNotTransaction  = errors.New("eventmemory: this context carries no transaction of this store's for this store")
	errFinished        = errors.New("eventmemory: this transaction has already been committed or rolled back")
	errStaleClaim      = errors.New("eventmemory: a claimed stream moved while it was claimed")
	errStaleCheckpoint = errors.New("eventmemory: a checkpoint row moved between the save this transaction staged and its commit")
)

// Two staging areas rather than one, and the reason is the arithmetic below:
// Rollback returns the positions this transaction's envelopes would have taken,
// and it counts staged. A checkpoint save riding in that slice would burn a
// position on rollback, so the next append would land one above where it
// belongs — an event the log will never hold and a gap no reader can explain.
type Tx struct {
	log      *Log
	identity txIdentity
	finished atomic.Bool

	staged []event.Envelope
	counts map[event.Stream]int
	saves  []event.Checkpoint
}

// What the authority names, and deliberately not the *Tx: a claim is released
// when nothing can reach the transaction any more, and the authority travels in
// every commit receipt, which is a value a caller is meant to keep — an audit
// buffer, an outbox row, two subsystems comparing receipts later. A store whose
// transaction is a database handle names the handle, because there the locks go
// at the commit and not at a collection. Monotone per log, so no two
// transactions of one log are ever named alike and a name is never reused.
type txIdentity struct {
	log *Log
	nth uint64
}

// The binding is keyed by the log it was begun on, so an operation that writes
// to two backings binds a transaction for each and neither shadows the other.
// A nil transaction names no log: it could have been meant for any store, so
// every store that has no binding of its own finds it and refuses.
type transactionKey struct{ log *Log }

func WithTransaction(ctx context.Context, tx *Tx) context.Context {
	if tx == nil {
		return context.WithValue(ctx, transactionKey{}, (*Tx)(nil))
	}
	return context.WithValue(ctx, transactionKey{log: tx.log}, tx)
}

func (this *Log) begin() *Tx {
	return &Tx{log: this, identity: this.nameTransaction(), counts: map[event.Stream]int{}}
}

func (this *Store) Begin(ctx context.Context) (*Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if this.closed.Load() {
		return nil, event.ErrClosed
	}
	return this.log.begin(), nil
}

func (this *Log) transaction(ctx context.Context) (event.Authority, error) {
	tx, err := this.ambient(ctx)
	if err == nil {
		err = tx.live()
	}
	if err != nil || tx == nil {
		return event.Authority{}, err
	}
	return event.NewAuthority(this.backing, tx.identity)
}

func (this *Store) Transaction(ctx context.Context) (event.Authority, error) {
	return this.log.transaction(ctx)
}

// The two keys: this store's own transaction, and the one bound for no log at
// all, which could have been meant for any store and is refused by every store
// that finds no binding of its own. A transaction of another log is nothing of
// this store's, so its operations run on this store's own autocommit.
//
// ctx.Value is the caller's own code — a wrapper, a decorator, a chain of them
// — so the lookup runs under no lock of this store's. A caller whose Value
// takes a lock would otherwise hold the log's mutex while acquiring it, and any
// goroutine holding that lock across an append would hang every reader and
// writer of the log for good.
//
// The lookup is the log's rather than a store value's, so the log's other peer
// — the checkpoint store over it — finds the same unit of work through the same
// key, and a unit begun through either of them is found by both.
func (this *Log) ambient(ctx context.Context) (*Tx, error) {
	tx, carried := ctx.Value(transactionKey{log: this}).(*Tx)
	if !carried {
		if _, unattributable := ctx.Value(transactionKey{}).(*Tx); unattributable {
			return nil, errNotTransaction
		}
		return nil, nil
	}
	return tx, nil
}

func (this *Store) ambient(ctx context.Context) (*Tx, error) { return this.log.ambient(ctx) }

// A finished transaction is refused rather than run on this store's autocommit:
// the caller holds a context that reads like a transaction and is not one, and
// answering it with autocommit is the escape the transaction rule exists to
// close. The check is the half of the answer that is only worth having inside
// the section that acts on it — every door asks after it holds the log's mutex,
// so a transaction another goroutine finishes in between is refused rather than
// staged into after its records were released.
func (this *Tx) live() error {
	if this != nil && this.finished.Load() {
		return errFinished
	}
	return nil
}

// Neither this nor Rollback reads the context's deadline. A transaction that
// refused to finish because the request was cancelled would hold its claims for
// the life of the process and starve every other writer of those streams, and
// there is no window here for a cancellation to be uncertain about.
func (this *Tx) Commit(context.Context) error {
	this.log.mutex.Lock()
	defer this.log.mutex.Unlock()
	if this.finished.Load() {
		return errFinished
	}
	if err := this.revalidate(); err != nil {
		return err
	}
	if err := this.revalidateSaves(); err != nil {
		return err
	}
	this.log.publish(this.staged)
	for _, held := range this.saves {
		this.log.recordCheckpoint(held)
	}
	this.release()
	return nil
}

func (this *Tx) Rollback(context.Context) error {
	this.log.mutex.Lock()
	defer this.log.mutex.Unlock()
	if this.finished.Load() {
		return errFinished
	}
	this.log.position += event.Position(len(this.staged))
	this.release()
	return nil
}

// What the claim taken at Append promises, checked where the records are
// published rather than assumed: every staged record still lands at the version
// it was admitted at, densely, on a stream nobody else advanced.
func (this *Tx) revalidate() error {
	staged := map[event.Stream]event.Version{}
	for _, envelope := range this.staged {
		staged[envelope.Stream]++
		if envelope.Version != this.log.version(envelope.Stream)+staged[envelope.Stream] {
			return errStaleClaim
		}
	}
	return nil
}

// What the fence taken at Save promises, checked where the rows are published
// rather than assumed: every staged save still follows the row it was admitted
// over, so a fence broken between the stage and the commit is an error from
// Commit rather than a row that overwrote a winner. A staged removal is the
// advance-zero entry and is not fenced, exactly as Forget is not.
func (this *Tx) revalidateSaves() error {
	staged := map[string]event.Checkpoint{}
	for _, held := range this.saves {
		over, amended := staged[held.Projection]
		if !amended {
			over = this.log.checkpointHeld(held.Projection)
		}
		if held.Advance != 0 && held.Advance != over.Advance+1 {
			return errStaleCheckpoint
		}
		staged[held.Projection] = held
	}
	return nil
}

// What this transaction would leave the row at: the last save it staged for the
// name, or the committed row where it has staged none. A staged removal is the
// advance-zero entry, and what it would leave behind is no row — handing the
// entry itself back would name a projection beside no advance, and absence is
// total.
func (this *Tx) checkpointHeld(projection string) event.Checkpoint {
	for index := len(this.saves) - 1; index >= 0; index-- {
		if this.saves[index].Projection != projection {
			continue
		}
		if this.saves[index].Advance == 0 {
			return event.Checkpoint{}
		}
		return this.saves[index]
	}
	return this.log.checkpointHeld(projection)
}

func (this *Tx) stageSave(held event.Checkpoint) { this.saves = append(this.saves, held) }

func (this *Tx) release() {
	for stream := range this.counts {
		delete(this.log.claims, stream)
	}
	this.staged, this.counts, this.saves = nil, nil, nil
	this.finished.Store(true)
}

func (this *Tx) stage(stream event.Stream, envelopes []event.Envelope) {
	this.log.claim(stream, this)
	this.staged = append(this.staged, envelopes...)
	this.counts[stream] += len(envelopes)
}

func (this *Tx) stagedCount(stream event.Stream) event.Version {
	if this == nil {
		return 0
	}
	return event.Version(this.counts[stream])
}

func (this *Tx) stagedFor(stream event.Stream) []event.Envelope {
	if this == nil || this.counts[stream] == 0 {
		return nil
	}
	held := make([]event.Envelope, 0, this.counts[stream])
	for _, envelope := range this.staged {
		if envelope.Stream == stream {
			held = append(held, envelope)
		}
	}
	return held
}

package eventmemory

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/frostgrove/vv/event"
)

var (
	errNotTransaction = errors.New("eventmemory: this context carries no transaction of this store's for this store")
	errFinished       = errors.New("eventmemory: this transaction has already been committed or rolled back")
	errStaleClaim     = errors.New("eventmemory: a claimed stream moved while it was claimed")
)

type Tx struct {
	log      *Log
	finished atomic.Bool

	staged []event.Envelope
	counts map[event.Stream]int
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

func (this *Store) Begin(ctx context.Context) (*Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if this.closed.Load() {
		return nil, event.ErrClosed
	}
	return &Tx{log: this.log, counts: map[event.Stream]int{}}, nil
}

func (this *Store) Transaction(ctx context.Context) (event.Authority, error) {
	tx, err := this.ambient(ctx)
	if err != nil || tx == nil {
		return event.Authority{}, err
	}
	return event.NewAuthority(this.log.backing, tx)
}

// A transaction of another log is nothing of this store's, so its operations
// run on this store's own autocommit. A finished one is not: the caller holds a
// context that reads like a transaction and is not one, and answering it with
// autocommit is the escape the transaction rule exists to close. The two doors
// that act on the answer resolve it inside the section that acts, so a
// transaction another goroutine finishes in between is refused rather than
// staged into after its records were released.
func (this *Store) ambient(ctx context.Context) (*Tx, error) {
	tx, carried := ctx.Value(transactionKey{log: this.log}).(*Tx)
	if !carried {
		if _, unattributable := ctx.Value(transactionKey{}).(*Tx); unattributable {
			return nil, errNotTransaction
		}
		return nil, nil
	}
	if tx.finished.Load() {
		return nil, errFinished
	}
	return tx, nil
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
	this.log.publish(this.staged)
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

func (this *Tx) release() {
	for stream := range this.counts {
		delete(this.log.claims, stream)
	}
	this.staged, this.counts = nil, nil
	this.finished.Store(true)
}

func (this *Tx) stage(stream event.Stream, envelopes []event.Envelope) {
	this.log.claims[stream] = this
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

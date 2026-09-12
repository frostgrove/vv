package receipt

import (
	"context"
	"fmt"
	"time"

	"github.com/frostgrove/vv/event"
)

// What a resolve found, and it is a different vocabulary from a claim's because
// the two doors can answer different questions: a claim holds the index and never
// sees Unresolved, and a resolve writes nothing and never sees Recorded. One
// enum with seven members would let each door return the other's.
type Standing uint8

const (
	Found      Standing = iota + 1 // present and complete: here is the range
	Incomplete                     // present with no range — a claim that committed without its append
	Unresolved                     // absent, and nothing may be concluded
	Expired                        // absent, and older than this ledger answers for
)

func (this Standing) Valid() bool {
	return this == Found || this == Incomplete || this == Unresolved || this == Expired
}

func (this Standing) String() string {
	switch this {
	case Found:
		return "[standing found]"
	case Incomplete:
		return "[standing incomplete]"
	case Unresolved:
		return "[standing unresolved]"
	case Expired:
		return "[standing expired]"
	default:
		return "[standing unstated]"
	}
}

type Resolution struct {
	Standing Standing
	Receipt  Receipt
	Horizon  time.Time
}

// Store is what this spec asks the placement question of, and that is the whole
// of its purpose: a Resolve issued on the claiming transaction's own context
// reads its own uncommitted row and answers Found for an operation that can still
// roll back. A transaction of the store's or of the ledger's bound to ctx is
// ErrSpec.
//
// Issued is the instant the caller minted the key, in the caller's own clock, and
// it is OPTIONAL. An HTTP handler holding a retried idempotency key has the key
// and not the instant, and requiring one sends it to time.Now(), which is after
// every horizon and silently turns Expired off for ever. Absent, an absent row is
// Unresolved and Resolution.Horizon carries the ledger's instant so the caller can
// make the comparison with a date it does have. Present, the comparison runs and
// its error term is the skew between this host and the database — which decides
// between two non-conclusions and can produce neither a false Found nor a false
// "it did not happen".
type ResolveSpec struct {
	Ledger Ledger
	Store  event.Store
	Key    Key
	Issued time.Time
}

// Asks what happened to an operation, on a connection that is not the one that
// issued it. It writes nothing and reads no event.
//
// AN ABSENT ROW IS NOT A ROLLBACK. A resolver on a second connection sees no row
// for either of two reasons — the writing transaction rolled back, or it is still
// open — and PostgreSQL gives it nothing to tell them apart: there is no row to
// lock, so a share lock blocks on nothing. Unresolved is that state named rather
// than guessed at, and what bounds it is idle_in_transaction_session_timeout on
// the application role, which is the deployment's lever and not this package's.
//
// What a caller does with Unresolved: it does not re-issue the command, because a
// fresh load and a fresh decision at the new version is a different operation to
// this key and nothing would catch the second write. It reports the operation as
// in flight, and it resolves again once that timeout has elapsed.
func Resolve(ctx context.Context, spec ResolveSpec) (Resolution, error) {
	if err := resolvable(ctx, spec); err != nil {
		return Resolution{}, err
	}
	held, found, err := spec.Ledger.Find(ctx, spec.Key)
	if err != nil {
		return Resolution{}, err
	}
	horizon, err := spec.Ledger.Horizon(ctx)
	if err != nil {
		return Resolution{}, err
	}
	if err := findAnswered(spec, held, found, horizon); err != nil {
		return Resolution{}, err
	}
	if !found {
		return Resolution{Standing: standingOfAnAbsentRow(spec.Issued, horizon), Horizon: horizon}, nil
	}
	if !held.Complete {
		return Resolution{Standing: Incomplete, Receipt: held, Horizon: horizon}, nil
	}
	return Resolution{Standing: Found, Receipt: held, Horizon: horizon}, nil
}

// The comparison is the one cross-clock question this package asks, and it
// decides between two non-conclusions: a caller clock behind the database's
// answers Expired for an operation that is in flight, and one ahead answers
// Unresolved for a row that was swept. Neither is a false Found and neither is a
// false "it did not happen", which is why the error term is tolerable and is
// written down rather than hidden.
//
// The zero instant is a caller that cannot date its own key, which is the
// canonical one, and it gets no conclusion rather than a refusal: a refusal here
// sends that caller to time.Now(), which is after every horizon and disables
// Expired for ever with no error anywhere.
func standingOfAnAbsentRow(issued, horizon time.Time) Standing {
	if issued.IsZero() || !issued.Before(horizon) {
		return Unresolved
	}
	return Expired
}

func resolvable(ctx context.Context, spec ResolveSpec) error {
	if absent(spec.Ledger) {
		return fmt.Errorf("%w: Ledger names no ledger, and the row a resolve reads is in the application's own table", ErrSpec)
	}
	if absent(spec.Store) {
		return fmt.Errorf("%w: Store names no store, and the one question this resolve asks of one is whether a transaction of its is bound to this context", ErrSpec)
	}
	if spec.Key.Zero() {
		return fmt.Errorf("%w: Key is the zero Key, which NewKey never answers — a resolve is a lookup and there is nothing to look up under", ErrSpec)
	}
	return outsideEveryTransaction(ctx, spec)
}

// The mirror of the claim's placement rule: a claim and a completion refuse
// outside one transaction, and a resolve refuses inside any. A resolve issued on
// the claiming transaction's own context reads that transaction's uncommitted row
// and answers Found for an operation that can still roll back, and "resolve
// first, then decide, in one unit" is an ordinary retry shape rather than an
// exotic one.
//
// A question that cannot be answered is refused rather than read as a no: a store
// or a ledger that reports something bound and not a transaction has told this
// call the one thing it cannot proceed past.
func outsideEveryTransaction(ctx context.Context, spec ResolveSpec) error {
	authority, err := spec.Store.Transaction(ctx)
	if err != nil {
		return fmt.Errorf("%w: what this context carries for the event store is not a transaction, and a resolve that cannot tell whether it is inside the writing unit cannot say its own answer is about a committed row: %w", ErrSpec, err)
	}
	if authority.Valid() {
		return fmt.Errorf("%w: a transaction of the event store's is bound to this context, and a resolve issued inside the unit that took the claim reads its own uncommitted row and answers Found for an operation that can still roll back", ErrSpec)
	}
	held, err := spec.Ledger.Transaction(ctx)
	if err != nil {
		return fmt.Errorf("%w: what this context carries for the ledger is not a transaction, and a resolve that cannot tell whether it is inside the writing unit cannot say its own answer is about a committed row: %w", ErrSpec, err)
	}
	if held.Valid() {
		return fmt.Errorf("%w: a transaction of the ledger's is bound to this context, and a resolve issued inside the unit that took the claim reads its own uncommitted row whether or not the event store shares that unit", ErrSpec)
	}
	return nil
}

// No process clock is read here and the horizon is not compared against one: a
// database two minutes ahead of this host would otherwise refuse every resolve it
// answered. What CAN be checked is inside the ledger's own clock — a horizon at
// or before the oldest row the ledger still answers for is the contract, and a
// row it answered for that is older than the horizon it published falsifies it.
// That is the MAX(recorded_at)-for-MIN(recorded_at) defect, which reads a swept
// row as one that never existed.
func findAnswered(spec ResolveSpec, held Receipt, found bool, horizon time.Time) error {
	if !found {
		return nil
	}
	if held.Key.Zero() || held.Key != spec.Key {
		return fmt.Errorf("%w: it reported a row for this key and answered a receipt for another key or for none at all", ErrLedger)
	}
	if !held.RecordedAt.IsZero() && horizon.After(held.RecordedAt) {
		return fmt.Errorf("%w: it published a horizon later than a row it still answers for, and a horizon is an instant at or before the oldest row a ledger holds — one after it reports a swept row as one that never existed", ErrLedger)
	}
	return nil
}

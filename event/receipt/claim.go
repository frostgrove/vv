package receipt

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"

	"github.com/frostgrove/vv/event"
)

// A claim's three answers, and there is no fourth: the index makes a claim wait
// rather than guess, so a claim never has to answer the Unresolved a Resolve does.
// The two states a caller must not proceed from carry no verdict of their own —
// Collided is returned beside ErrCollision, and a row whose claim nobody resolved
// is ErrIncomplete with no verdict at all, because a state nothing may be
// concluded from is a refusal and not an answer.
type Verdict uint8

const (
	Recorded Verdict = iota + 1 // no row existed and this claim took the key
	Repeated                    // a complete row exists and its fingerprint compares equal
	Collided                    // a row exists and its fingerprint differs
)

func (this Verdict) Valid() bool {
	return this == Recorded || this == Repeated || this == Collided
}

func (this Verdict) String() string {
	switch this {
	case Recorded:
		return "[verdict recorded]"
	case Repeated:
		return "[verdict repeated]"
	case Collided:
		return "[verdict collided]"
	default:
		return "[verdict unstated]"
	}
}

type ClaimSpec struct {
	Ledger      Ledger
	Store       event.Store
	Key         Key
	Fingerprint Fingerprint
	Stream      event.Stream
}

// What a claim took, and the door to completing it. It carries the claim's own
// transaction and whether it has been completed, so Complete can refuse the calls
// that would undo what Claim proved — which is why it holds an unexported pointer
// and a copy of one is the same claim rather than a second.
type Held struct{ taken *claimed }

type claimed struct {
	spec      ClaimSpec
	authority event.Authority
	verdict   Verdict
	receipt   Receipt
	completed atomic.Bool
}

func (this Held) Verdict() Verdict {
	if this.taken == nil {
		return 0
	}
	return this.taken.verdict
}

func (this Held) Receipt() Receipt {
	if this.taken == nil {
		return Receipt{}
	}
	return this.taken.receipt
}

// Claims the key inside the caller's transaction, before the decision is
// appended. Refused unless the ledger's transaction and the store's compare
// Same, unless the store claims transactions, and unless one is bound: a receipt
// that is not atomic with its append is a row that outlives a rollback and
// reports an operation that never happened.
//
// It answers a non-nil error for the two states a caller must not proceed from: a
// row whose fingerprint differs is ErrCollision, and a row that exists and is not
// complete is ErrIncomplete, because whether that operation's events reached the
// log is not a question its row answers. A verdict is a value it is legal to
// discard and a refusal is not, and the worst code that compiles must refuse
// rather than spend somebody else's key.
//
// The order is claim, decide, append, complete, and the claim is first on
// purpose. A receipt written only after the append is safe against two writers at
// one expected version, because exactly one of those is admitted — and not
// against two retries at different expected versions, which is what a caller does
// when it does not know the outcome. Both append, and the collision is found with
// both sets of events already in the log. Nothing in this package can see an
// append that already happened on this context, so Once is the spelling that
// makes the order unwritable and this one carries the obligation.
func Claim(ctx context.Context, spec ClaimSpec) (Held, error) {
	authority, err := claimable(ctx, spec)
	if err != nil {
		return Held{}, err
	}
	held, won, err := spec.Ledger.Claim(ctx, Receipt{Key: spec.Key, Fingerprint: spec.Fingerprint, Stream: spec.Stream})
	if err != nil {
		return Held{}, err
	}
	if err := claimAnswered(spec, held, won); err != nil {
		return Held{}, err
	}
	if !won && !held.Complete {
		return Held{}, fmt.Errorf("%w: this key holds a row whose claim committed without its completion, so nothing may be concluded from it — the events of that operation may be in the log or may not, and the row reads the same either way. Resolve names the same state and it is a defect report rather than a state to retry through", ErrIncomplete)
	}
	taken := &claimed{spec: spec, authority: authority, verdict: verdictOf(spec, held, won), receipt: held}
	if taken.verdict == Collided {
		return Held{taken: taken}, fmt.Errorf("%w: this key holds a complete row whose fingerprint is not this operation's, so the two are different operations under one identity", ErrCollision)
	}
	return Held{taken: taken}, nil
}

// Claim, append, complete — with the order owned by the call rather than by the
// caller. The work runs only on Recorded, runs once, and runs inside the caller's
// transaction through the context it is given; its commit is completed in that
// same transaction. A Repeated key never reaches it, so a retry costs one
// statement and no append. Opens nothing, starts nothing, and returns the Held it
// claimed, already completed.
func Once(ctx context.Context, spec ClaimSpec, work func(context.Context) (event.Commit, error)) (Held, error) {
	if work == nil {
		return Held{}, fmt.Errorf("%w: work names no decision to run under this key, and a claim with nothing to complete it is the row Resolve reports as a defect", ErrSpec)
	}
	held, err := Claim(ctx, spec)
	if err != nil || held.Verdict() != Recorded {
		return held, err
	}
	commit, err := work(ctx)
	if err != nil {
		return held, err
	}
	return held, held.Complete(ctx, commit)
}

// Writes the range onto the row this claim took, in the same transaction, and it
// is not optional on any path: a claim that reaches its transaction's commit
// without one is the Incomplete row Resolve reports as a defect.
//
// Five refusals, and each of them is a way to undo what Claim proved. A verdict
// that is not Recorded — completing a repeat would overwrite the range the first
// attempt wrote with this one's. A second call on one Held — an operation that
// appended twice would otherwise record one range. A context carrying no
// transaction of the store's, or one that is not the claim's: a completion that
// lands outside the claim's transaction writes a range that survives the rollback
// of the events it names. And a commit whose Stream() is not the claim's, which is
// one claim completed with another aggregate's append.
//
// The commit's own Authority is compared with the claim's too, except on an empty
// commit: an append that wrote nothing was written through nobody's transaction
// and carries the invalid authority by design. An empty commit is how a decision
// that yielded no changes is recorded, and it completes the row with a zero range
// — Commit.Last on one is the version the token went in at rather than a version
// anything was written at, so neither end of the range is taken from it.
func (this Held) Complete(ctx context.Context, commit event.Commit) error {
	if this.taken == nil {
		return fmt.Errorf("%w: this Held took no claim, so there is no row for a completion to be written onto — the zero value is not a claim Claim answered", ErrSpec)
	}
	taken := this.taken
	if taken.verdict != Recorded {
		return fmt.Errorf("%w: this claim took no key and found a row some earlier attempt wrote, so completing it would overwrite that attempt's range with this one's", ErrSpec)
	}
	if commit.Stream() != taken.spec.Stream {
		return fmt.Errorf("%w: this commit is another aggregate's, and one operation key covers one append to the stream the claim names", ErrSpec)
	}
	authority, err := sameUnit(ctx, taken.spec.Ledger, taken.spec.Store, "a completion")
	if err != nil {
		return err
	}
	if !authority.Same(taken.authority) {
		return fmt.Errorf("%w: this completion is issued on a transaction that is not the one the claim was taken in, so the range it writes would survive the rollback of the events it names", ErrSpec)
	}
	if !commit.Empty() && !commit.Authority().Same(taken.authority) {
		return fmt.Errorf("%w: this commit was written through a transaction that is not the one the claim was taken in, so the row and the events it names are two commits", ErrSpec)
	}
	if !taken.completed.CompareAndSwap(false, true) {
		return fmt.Errorf("%w: this claim has already been completed, and an operation that appended twice under one key would otherwise record one range", ErrSpec)
	}
	filled := taken.receipt
	filled.Complete = true
	if !commit.Empty() {
		filled.First, filled.Last = commit.First(), commit.Last()
	}
	if err := taken.spec.Ledger.Complete(ctx, filled); err != nil {
		return err
	}
	taken.receipt = filled
	return nil
}

func claimable(ctx context.Context, spec ClaimSpec) (event.Authority, error) {
	if absent(spec.Ledger) {
		return event.Authority{}, fmt.Errorf("%w: Ledger names no ledger, and the row a claim takes is written to the application's own table", ErrSpec)
	}
	if absent(spec.Store) {
		return event.Authority{}, fmt.Errorf("%w: Store names no store, and a claim is atomic with an append through the transaction the store resolves", ErrSpec)
	}
	if spec.Key.Zero() {
		return event.Authority{}, fmt.Errorf("%w: Key is the zero Key, which NewKey never answers — an operation key is the caller's own request identity and this framework derives none", ErrSpec)
	}
	if spec.Fingerprint.Zero() {
		return event.Authority{}, fmt.Errorf("%w: Fingerprint is the zero Fingerprint, which NewFingerprint never answers — Repo.Digest is where the bytes this append would write are digested", ErrSpec)
	}
	if spec.Stream.Family == "" || spec.Stream.Key == "" {
		return event.Authority{}, fmt.Errorf("%w: Stream is the zero Stream, and a key covers one append to the stream the claim names — At.Stream is the token's own", ErrSpec)
	}
	return sameUnit(ctx, spec.Ledger, spec.Store, "a claim")
}

// The three the atomicity rests on, asked in one place because a claim and its
// completion owe the same answer: the store states transactions at all, one of
// its own is bound to this context, and the ledger resolves this context to that
// same transaction. It is projection's own checkUnit put to a second purpose, and
// what it closes is [[D-118]]'s sentence — "atomic" across two handles is a
// sentence with no meaning.
func sameUnit(ctx context.Context, ledger Ledger, store event.Store, what string) (event.Authority, error) {
	if store.Capabilities().Transactions != event.Supported {
		return event.Authority{}, fmt.Errorf("%w: this store states no transactions, and %s written on a store's own autocommit commits whether or not the events beside it do", ErrSpec, what)
	}
	authority, err := store.Transaction(ctx)
	if err != nil {
		return event.Authority{}, fmt.Errorf("%w: what this context carries for the event store is not a transaction, so %s would be written beside an append nothing holds it together with: %w", ErrSpec, what, err)
	}
	if !authority.Valid() {
		return event.Authority{}, fmt.Errorf("%w: this context carries no transaction of the event store's, and %s outside one is a row that survives the rollback of the events it names", ErrSpec, what)
	}
	held, err := ledger.Transaction(ctx)
	if err != nil {
		return event.Authority{}, fmt.Errorf("%w: what this context carries for the ledger is not a transaction, so %s cannot be shown to be one commit with its append: %w", ErrSpec, what, err)
	}
	if !held.Valid() {
		return event.Authority{}, fmt.Errorf("%w: this context carries no transaction of the ledger's, so %s would be written on the ledger's own autocommit and outlive the rollback of the events it names", ErrSpec, what)
	}
	if !held.Same(authority) {
		return event.Authority{}, fmt.Errorf("%w: the ledger and the event store resolve this context to two different transactions, so %s and its append are two commits and a crash between them leaves one of the two", ErrSpec, what)
	}
	return authority, nil
}

// A ledger's answer is checked before it is compared or rendered, which is the
// rule the kernel already applies to a store's strings and numbers. What is
// asked is whether the row is about the key that was claimed, and — on a win —
// whether it is the row this claim has just written: a winner reads back its own
// insert, so a fingerprint, a stream, a range or a completion that is not the one
// it handed in is an answer about some other operation.
func claimAnswered(spec ClaimSpec, held Receipt, won bool) error {
	if held.Key.Zero() {
		return fmt.Errorf("%w: it answered a claim with no row at all, and the statement that reads the key back runs after the insert precisely so that a loser reads the row the winner committed", ErrLedger)
	}
	if held.Key != spec.Key {
		return fmt.Errorf("%w: it answered a claim with a row for another key than the one it was asked to claim", ErrLedger)
	}
	if !won {
		return nil
	}
	if !held.Fingerprint.Equal(spec.Fingerprint) || held.Stream != spec.Stream {
		return fmt.Errorf("%w: it reported that this claim took the key and answered a row whose fingerprint or stream is not the one it was handed", ErrLedger)
	}
	if held.Complete || held.First != 0 || held.Last != 0 {
		return fmt.Errorf("%w: it reported that this claim took the key and answered a row that already carries a range, which is a row some other operation wrote", ErrLedger)
	}
	return nil
}

func verdictOf(spec ClaimSpec, held Receipt, won bool) Verdict {
	switch {
	case won:
		return Recorded
	case held.Fingerprint.Equal(spec.Fingerprint):
		return Repeated
	default:
		return Collided
	}
}

func absent(value any) bool {
	if value == nil {
		return true
	}
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

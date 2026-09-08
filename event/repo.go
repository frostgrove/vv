package event

import (
	"context"
	"fmt"
)

// One store and one declaration, held together. It is a handle: copy the
// pointer, share it between every request goroutine, and it keeps nothing
// between two calls — no state, no version, no memo of a fold.
type Repo[S any, ID any] struct {
	store        Store
	aggregate    *Aggregate[S, ID]
	capabilities Capabilities
	limits       Limits
}

// Three steps, and the order is fixed: the identity through the declared mapper
// and the key it renders against the kernel's text rules and the store's MaxKey;
// then the transaction question; then the paged read. Reading the retained
// limits issues nothing, so a load with a zero identity against a closed store
// is ErrKey and not ErrClosed.
//
// Any non-nil error returns the zero state and the zero token — never the
// accumulator. Fold answers the opposite way because it was handed the caller's
// own value and has already advanced it; this builds a value the caller has
// never seen and can therefore keep to itself.
func (this *Repo[S, ID]) Load(ctx context.Context, id ID) (S, At[S], error) {
	var none S
	stream, err := this.streamOf(id)
	if err != nil {
		return none, At[S]{}, err
	}
	backing := this.store.Backing()
	if _, err := this.transaction(ctx, backing); err != nil {
		return none, At[S]{}, err
	}
	state, version, err := this.replay(ctx, stream)
	if err != nil {
		return none, At[S]{}, err
	}
	return state, At[S]{stream: stream, version: version, backing: backing}, nil
}

// Six steps, and the order is fixed because two of these refusals are in two
// classes and one of them is what refuses a token no Load minted:
//
//  1. the token's own key, against the kernel's text rules and the MaxKey
//     retained at Bind — neither of which touches the store
//  2. every change's stream against the token's, and the aggregate it was
//     decided on against this repository's
//  3. each change's own carried refusal
//  4. the store's bounds: the payload per record, the batch count, and the
//     bytes this append would hold at once
//  5. the backing, read from the store now and compared with Equal
//  6. the transaction question
//
// Nothing reaches the store until all six pass. The empty-append short circuit
// sits between the first and the second, so a forged token is refused even when
// nothing would be written and an empty append still costs no store call at all.
//
// Every refusal returns the token that went in, and only for these six is the
// reason that nothing reached the backing. Past them the value is unchanged and
// what it means is the outcome's: a conflict says the stream moved, an
// uncertainty says the append was issued and nobody confirmed it. Reading
// err != nil as "nothing was written" is the inference ErrUncertain exists to
// refuse.
func (this *Repo[S, ID]) Append(ctx context.Context, at At[S], changes ...Change[S]) (At[S], Commit, error) {
	if err := this.checkKey(at.stream.Key); err != nil {
		return at, Commit{}, err
	}
	if len(changes) == 0 {
		return at, Commit{stream: at.stream, last: at.version}, nil
	}
	for _, change := range changes {
		if err := change.decidedFor(at.stream, this.aggregate); err != nil {
			return at, Commit{}, err
		}
	}
	for _, change := range changes {
		if change.err != nil {
			return at, Commit{}, change.err
		}
	}
	records, err := this.records(changes)
	if err != nil {
		return at, Commit{}, err
	}
	backing := this.store.Backing()
	if !backing.Equal(at.backing) {
		return at, Commit{}, fmt.Errorf("%w: this token was minted over another backing", ErrWrongStore)
	}
	authority, err := this.transaction(ctx, backing)
	if err != nil {
		return at, Commit{}, err
	}
	if err := this.store.Append(ctx, AppendRequest{Stream: at.stream, Expected: at.version, Records: records}); err != nil {
		return at, Commit{}, refuseAppend(err)
	}
	last := at.version + Version(len(changes))
	return At[S]{stream: at.stream, version: last, backing: backing},
		Commit{stream: at.stream, first: at.version + 1, last: last, count: len(changes), authority: authority}, nil
}

// A context, never a bound repository and never an inner store: a policing
// decorator stays in the path for the whole transaction, and an append through
// another transaction's context is not expressible because there is no bound
// value to call it on. It opens nothing — the transaction is the caller's
// throughout — and on a closed store it answers this question and no other, so
// the refusal for the closure comes from the load or the append that follows.
func (this *Repo[S, ID]) Within(ctx context.Context) (context.Context, error) {
	if this.capabilities.Transactions != Supported {
		return ctx, ErrNoTransactionBinding
	}
	authority, err := this.store.Transaction(ctx)
	if err != nil {
		return ctx, refuseTransaction(err)
	}
	if !authority.Valid() {
		return ctx, ErrNoTransaction
	}
	return withMarker(ctx, authority), nil
}

// The store's answer for this context and nothing else, so an application that
// must prove two subsystems wrote in one transaction has a value to compare
// before, between or after operations that decided nothing. It reports what is
// bound and never whether an append may be written through it: on a context
// Within marked for one transaction and which has since acquired another, this
// answers the one the store finds and the Load or Append that follows refuses.
func (this *Repo[S, ID]) Authority(ctx context.Context) (Authority, error) {
	authority, err := this.store.Transaction(ctx)
	if err != nil {
		return Authority{}, refuseTransaction(err)
	}
	return authority, nil
}

// The three answers a store gives about what the context carries for its own
// backing, and the fourth refusal that exists only because Within was called.
// The store is the only party that knows what its own executors are, so the
// kernel asks and never guesses: nothing bound runs on the store's own
// autocommit, a transaction is written inside whether or not Within was called,
// and an executor that is not a transaction refuses before any statement rather
// than falling back to autocommit.
func (this *Repo[S, ID]) transaction(ctx context.Context, backing Backing) (Authority, error) {
	authority, err := this.store.Transaction(ctx)
	if err != nil {
		return Authority{}, refuseTransaction(err)
	}
	if marked, found := markerFor(ctx, backing); found && !marked.Same(authority) {
		return Authority{}, ErrTransactionMismatch
	}
	return authority, nil
}

func (this *Repo[S, ID]) streamOf(id ID) (Stream, error) {
	stream, err := this.aggregate.locate(id)
	if err != nil {
		return Stream{}, err
	}
	if err := this.checkKey(stream.Key); err != nil {
		return Stream{}, err
	}
	return stream, nil
}

func (this *Repo[S, ID]) checkKey(key Key) error {
	if broken := checkText(string(key), this.limits.MaxKey); broken != "" {
		return fmt.Errorf("%w: the stream key %s", ErrKey, broken)
	}
	return nil
}

func (this *Repo[S, ID]) records(changes []Change[S]) ([]Record, error) {
	if len(changes) > this.limits.MaxBatch {
		return nil, tooMany("the batch", len(changes), this.limits.MaxBatch)
	}
	resident := 0
	records := make([]Record, 0, len(changes))
	for _, change := range changes {
		if len(change.payload) > this.limits.MaxPayload {
			return nil, tooLarge("the encoded payload", len(change.payload), this.limits.MaxPayload)
		}
		resident += len(change.payload)
		records = append(records, Record{Type: change.name, Revision: change.revision, Payload: change.payload})
	}
	if resident > MaxResidentBytes {
		return nil, tooLarge("this append", resident, MaxResidentBytes)
	}
	return records, nil
}

// The loop stops on a short page and issues no confirming read: the alternative
// costs an extra round trip on every load, and a store that returns a short page
// that is not the end of the stream truncates a history silently, which is what
// the contract makes its own clause and the suite its own defect.
func (this *Repo[S, ID]) replay(ctx context.Context, stream Stream) (S, Version, error) {
	var state S
	var at Version
	for {
		page, err := this.store.ReadStream(ctx, stream, at)
		if err != nil {
			return this.nothing(refuseRead(err))
		}
		if err := this.checkPage(page, stream, at); err != nil {
			return this.nothing(err)
		}
		for _, envelope := range page {
			if state, err = this.apply(state, envelope); err != nil {
				return this.nothing(err)
			}
		}
		at += Version(len(page))
		if len(page) < this.limits.StreamPage {
			return state, at, nil
		}
	}
}

func (this *Repo[S, ID]) nothing(err error) (S, Version, error) {
	var none S
	return none, 0, err
}

// The kernel applies the four bounds a store does not, and verifies the two it
// does: only the store issues the read, so only the store can cap a page, and a
// page longer than the number the store itself published is the same answer as a
// zero at a door. The shape is verified for the same money and for a worse
// failure: the fold is order-dependent by construction, so a page returned as
// [v3, v1, v2] folds to a state that is wrong at the right version, with the
// right count and no refusal anywhere. Dense from 1 is a property of the
// concatenation this loop builds and never of a page — page two of a long stream
// begins where page one ended.
func (this *Repo[S, ID]) checkPage(page []Envelope, stream Stream, after Version) error {
	if len(page) > this.limits.StreamPage {
		return fmt.Errorf("%w: %s answered with %d envelopes where it publishes a stream page of %d",
			ErrBackend, stream, len(page), this.limits.StreamPage)
	}
	for offset, envelope := range page {
		if envelope.Stream != stream {
			return fmt.Errorf("%w: %s was read and %s answered", ErrBackend, stream, envelope.Stream)
		}
		if envelope.Version != after+Version(offset)+1 {
			return fmt.Errorf("%w: %s answered a page that does not begin at the version it was read from and rise by one, so folding it would produce a state that is wrong at the right version",
				ErrBackend, stream)
		}
	}
	return nil
}

// A store's strings and numbers are checked before the kernel uses them for
// anything, a rendering included, because a store is a shared database, a
// restored dump or another service's writer. A type name that breaks the
// declared-identifier rule names no declared fact and can name none — no
// declaration could have produced it — so it is the same refusal a
// legal-but-unknown name gets, and it does not travel, which is what keeps a
// megabyte of it, a newline in it, or a bracket that would forge a second field
// out of a log line.
func (this *Repo[S, ID]) apply(state S, envelope Envelope) (S, error) {
	if broken := checkName(envelope.Type); broken != "" {
		return state, fmt.Errorf("%w: %s recorded a type name of %d bytes that %s", ErrUnknownType, envelope.Stream, len(envelope.Type), broken)
	}
	fold, declared := this.aggregate.facts[envelope.Type]
	if !declared {
		return state, fmt.Errorf("%w: %q on %s", ErrUnknownType, envelope.Type, envelope.Stream)
	}
	if len(envelope.Payload) > this.limits.MaxPayload {
		return state, fmt.Errorf("%w: the recorded payload is %d bytes against a bound of %d", ErrPayload, len(envelope.Payload), this.limits.MaxPayload)
	}
	return fold(state, envelope.Revision, envelope.Payload)
}

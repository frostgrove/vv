package event

import (
	"bytes"
	"fmt"
	"sync"
)

type Aggregate[S any, ID any] struct {
	family string
	key    func(ID) Key

	mutex  sync.Mutex
	sealed bool
	facts  map[string]func(S, int, []byte) (S, error)
}

// The identity mapper is retained on the declaration and called from every
// request goroutine concurrently, so it must be safe for that; its panic is not
// recovered, because it has no error channel for the panic to be a second
// spelling of. It must also be injective over the aggregate's identity domain:
// two identities rendering one key are two aggregates over one history, folding
// each other's facts, with no error at any point. The framework cannot check
// that, so it makes the correct rendering the shortest one to write — Compose —
// and eventtest.Keys is the runnable proxy.
func Define[S any, ID any](family string, key func(ID) Key) *Aggregate[S, ID] {
	aggregate, err := TryDefine[S, ID](family, key)
	if err != nil {
		panic(err)
	}
	return aggregate
}

func TryDefine[S any, ID any](family string, key func(ID) Key) (*Aggregate[S, ID], error) {
	if broken := checkText(family, MaxNameBytes); broken != "" {
		return nil, fmt.Errorf("%w: the stream family %s", ErrDeclaration, broken)
	}
	if key == nil {
		return nil, fmt.Errorf("%w: the aggregate %q declares no identity mapper", ErrDeclaration, family)
	}
	return &Aggregate[S, ID]{family: family, key: key, facts: map[string]func(S, int, []byte) (S, error){}}, nil
}

func (this *Aggregate[S, ID]) Family() string {
	this.seal()
	return this.family
}

func (this *Aggregate[S, ID]) Key(id ID) (Key, error) {
	this.seal()
	stream, err := this.locate(id)
	return stream.Key, err
}

// Runs the caller's own folds over the caller's own value, in the loop the
// caller would otherwise have written: state is CONSUMED, written through for
// every reference kind it reaches, and a state type that is one has no other
// way to work. Take the result and do not use the argument again.
//
// The four causes run in Append's own order, and the first three are checks
// over the whole list before any fold runs, so they return the state untouched:
// the identity renders an illegal key, a change was decided for another stream,
// a change carries its own refusal from Fact.New. The fourth is per change and
// can only fire after earlier folds have already advanced the value, so it
// returns the state as of the last change applied — which for a reference kind
// is the argument. On any error the returned state is not usable and a reload
// is the authority.
func (this *Aggregate[S, ID]) Fold(id ID, state S, changes ...Change[S]) (S, error) {
	this.seal()
	stream, err := this.locate(id)
	if err != nil {
		return state, err
	}
	for _, change := range changes {
		if err := change.decidedFor(stream); err != nil {
			return state, err
		}
	}
	for _, change := range changes {
		if change.err != nil {
			return state, change.err
		}
	}
	for _, change := range changes {
		state, err = change.apply(state, change.revision, bytes.Clone(change.payload))
		if err != nil {
			return state, err
		}
	}
	return state, nil
}

func (this *Aggregate[S, ID]) locate(id ID) (Stream, error) {
	key := this.key(id)
	if broken := checkText(string(key), MaxKeyBytes); broken != "" {
		return Stream{}, fmt.Errorf("%w: the rendered key %s", ErrKey, broken)
	}
	return Stream{Family: this.family, Key: key}, nil
}

// The one non-generic view of a declaration. Its unexported method is what
// keeps the set to *Aggregate, and it has exactly two readers in phase 1 — the
// Binding's bound-family set and eventtest.Families.
type Declaration interface {
	Family() string
	declaration()
}

func (*Aggregate[S, ID]) declaration() {}

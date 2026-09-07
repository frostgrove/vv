package event

import (
	"bytes"
	"fmt"
)

type Fact[S any, ID any, E any] struct {
	aggregate *Aggregate[S, ID]
	name      string
	chain     Chain[E]
	apply     func(S, int, []byte) (S, error)
}

func Declare[S, ID, E any](a *Aggregate[S, ID], name string, chain Chain[E], fold func(S, E) S) *Fact[S, ID, E] {
	declared, err := TryDeclare(a, name, chain, fold)
	if err != nil {
		panic(err)
	}
	return declared
}

func TryDeclare[S, ID, E any](a *Aggregate[S, ID], name string, chain Chain[E], fold func(S, E) S) (*Fact[S, ID, E], error) {
	if a == nil {
		return nil, fmt.Errorf("%w: a fact is declared on an aggregate and %q names none", ErrDeclaration, name)
	}
	if broken := checkText(name, MaxNameBytes); broken != "" {
		return nil, fmt.Errorf("%w: a wire type name on the aggregate %q %s", ErrDeclaration, a.family, broken)
	}
	if fold == nil {
		return nil, fmt.Errorf("%w: %q declares no fold", ErrDeclaration, name)
	}
	if chain.err != nil {
		return nil, chain.err
	}
	if len(chain.links) == 0 {
		return nil, fmt.Errorf("%w: %q declares no reader chain", ErrDeclaration, name)
	}
	apply := applierOf(chain, fold)
	if err := a.declare(name, apply); err != nil {
		return nil, err
	}
	return &Fact[S, ID, E]{aggregate: a, name: name, chain: chain, apply: apply}, nil
}

func (this *Fact[S, ID, E]) Name() string { return this.name }

func (this *Fact[S, ID, E]) Revisions() int { return len(this.chain.links) }

// One value, so a slice literal of changes stays writable. The payload is
// encoded here, at the moment of decision, with the current revision's codec,
// and the identity is rendered here through the aggregate's declared mapper —
// which is what lets the change name its own stream. A failure is carried on
// the change rather than returned: Err exposes it, Append surfaces it before
// the store is reached, and Fold surfaces it rather than folding nothing.
func (this *Fact[S, ID, E]) New(id ID, payload E) Change[S] {
	this.aggregate.seal()
	change := Change[S]{name: this.name, revision: len(this.chain.links), apply: this.apply}
	stream, err := this.aggregate.locate(id)
	if err != nil {
		change.err = err
		return change
	}
	change.stream = stream
	encoded, err := encodeWith(this.chain.codec, payload)
	if err != nil {
		change.err = err
		return change
	}
	if len(encoded) > MaxPayloadBytes {
		change.err = tooLarge("the encoded payload", len(encoded), MaxPayloadBytes)
		return change
	}
	frozen := bytes.Clone(encoded)
	change.payload = frozen[:len(frozen):len(frozen)]
	return change
}

// One sample per retained revision, of that revision's own reader type, carried
// to the current type exactly as a load would carry it. It proves two things
// and the second is why it is here rather than in a test helper.
//
// Fidelity: each sample is encoded by its own revision's codec, read back by
// that revision's own reader and returned, so a codec that cannot decode its
// own output and an upcaster that refuses a legal historical value are both
// found before a stream contains one.
//
// Non-aliasing: between reading the sample and re-encoding what it produced,
// that revision's own codec is made to decode a second, different payload — the
// encoding of its reader type's zero value. A codec that decoded into a buffer
// it reuses has its first answer rewritten underneath it, and the two encodings
// of one value differ. Both encodings are the revision's own, over the value its
// own codec returned, because every declared upcaster between that value and the
// current type converts and therefore copies: compared after one, an aliasing
// revision-1 codec behind a copying upcaster passes, and only the last revision
// of a chain is ever under test. A sample that encodes exactly as that zero value
// cannot disturb anything and is refused as a sample rather than reported as a
// pass, because it proves nothing about fidelity either.
func (this *Fact[S, ID, E]) RoundTrip(byRevision ...any) ([]E, error) {
	this.aggregate.seal()
	if len(byRevision) != len(this.chain.links) {
		return nil, fmt.Errorf("%w: %q retains %d revisions and %d samples were given", ErrSample, this.name, len(this.chain.links), len(byRevision))
	}
	carried := make([]E, 0, len(byRevision))
	for index, sample := range byRevision {
		value, err := this.roundTrip(index, sample)
		if err != nil {
			return nil, err
		}
		carried = append(carried, value)
	}
	return carried, nil
}

func (this *Fact[S, ID, E]) roundTrip(index int, sample any) (E, error) {
	var none E
	read := this.chain.links[index]
	revision := index + 1
	if !read.accepts(sample) {
		return none, fmt.Errorf("%w: revision %d of %q reads %s and the sample is a %T", ErrSample, revision, this.name, read.typeName, sample)
	}
	written, err := read.selfEncode(sample)
	if err != nil {
		return none, err
	}
	written = bytes.Clone(written)
	zero, err := read.selfZero()
	if err != nil {
		return none, err
	}
	zero = bytes.Clone(zero)
	if bytes.Equal(written, zero) {
		return none, fmt.Errorf("%w: revision %d of %q encodes its sample exactly as its own zero value, so nothing a second read could disturb is under test", ErrSample, revision, this.name)
	}
	reused, err := reusesItsBuffer(read, written, zero)
	if err != nil {
		return none, err
	}
	if reused {
		return none, fmt.Errorf("%w: revision %d of %q decoded into memory its codec reuses, so reading the next payload rewrote the value it had already returned", ErrPayload, revision, this.name)
	}
	return read.decode(written)
}

func reusesItsBuffer[V any](read link[V], written, zero []byte) (bool, error) {
	own, err := read.selfDecode(written)
	if err != nil {
		return false, err
	}
	before, err := read.selfEncode(own)
	if err != nil {
		return false, err
	}
	before = bytes.Clone(before)
	if _, err := read.selfDecode(zero); err != nil {
		return false, err
	}
	after, err := read.selfEncode(own)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(before, after), nil
}

func applierOf[S, E any](chain Chain[E], fold func(S, E) S) func(S, int, []byte) (S, error) {
	return func(state S, revision int, payload []byte) (S, error) {
		if revision < 1 || revision > len(chain.links) {
			return state, fmt.Errorf("%w: revision %d against the %d this fact retains", ErrRevision, revision, len(chain.links))
		}
		value, err := chain.links[revision-1].decode(payload)
		if err != nil {
			return state, err
		}
		return fold(state, value), nil
	}
}

package event

import (
	"bytes"
	"fmt"
	"reflect"
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
	if broken := checkName(name); broken != "" {
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
	change := Change[S]{origin: this.aggregate, name: this.name, revision: len(this.chain.links), apply: this.apply}
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
// that revision's own reader and compared with the sample in the revision's own
// type, so a codec that drops part of what it was given, one that cannot decode
// its own output, and an upcaster that refuses a legal historical value are all
// found before a stream contains one. What the value comparison cannot answer it
// does not pass over: a struct with no exported field, no Equal of its own and
// no == is settled at the wire instead, by encoding what came back and comparing
// it with the bytes the sample encoded to — a question that needs no method and
// so can be asked of a big.Int, which no repair the caller could make would
// answer. Only a leaf no wire format records at all, a func, is refused
// (ErrSample), because there both answers are silent. A value the codec
// substituted for another — reachable only behind an interface, where the two
// have different types — is a difference, except between two numbers holding one
// value, which is what any wire format with a single number type produces.
//
// Non-aliasing: the same bytes are decoded twice, from two separate input
// buffers, and the two answers are searched for a slice or a map they share. A
// value that aliases the payload it was handed aliases its own copy of it and
// is permitted; one that aliases memory the codec keeps is the same address in
// both answers, and the next Decode rewrites what the application already
// holds. That comparison is exact. Behind it, the revision's codec is also made
// to decode a second, different payload — its reader type's zero value — and
// the first answer re-encoded either side of it, which reaches a reused buffer
// the value walk cannot see through. Both encodings are the revision's own,
// because every declared upcaster between that value and the current type
// converts and therefore copies: compared after one, an aliasing revision-1
// codec behind a copying upcaster passes, and only the last revision of a chain
// is ever under test. A sample that encodes exactly as that zero value cannot
// disturb anything and is refused as a sample rather than reported as a pass —
// unless the reader type holds exactly one value, which is what a marker fact
// carries and what leaves the caller no other sample to give. Then fidelity is
// proved and the non-aliasing half is not run, because there is no second
// payload for the codec to reuse memory between.
//
// Both walks are bounded and neither can answer for a value it did not reach,
// so a sample larger than the bound is refused rather than half-compared.
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

type roundTripping[V any] struct {
	read     link[V]
	written  []byte
	zero     []byte
	revision int
}

func (this *Fact[S, ID, E]) roundTrip(index int, sample any) (E, error) {
	var none E
	carried := roundTripping[E]{read: this.chain.links[index], revision: index + 1}
	if !carried.read.accepts(sample) {
		return none, fmt.Errorf("%w: revision %d of %q reads %s and the sample is a %T", ErrSample, carried.revision, this.name, carried.read.typeName, sample)
	}
	written, err := carried.read.selfEncode(sample)
	if err != nil {
		return none, err
	}
	carried.written = bytes.Clone(written)
	zero, err := carried.read.selfZero()
	if err != nil {
		return none, err
	}
	carried.zero = bytes.Clone(zero)
	disturbs := !bytes.Equal(carried.written, carried.zero)
	if !disturbs && !singleValued(reflect.TypeOf(sample)) {
		return none, fmt.Errorf("%w: revision %d of %q encodes its sample exactly as its own zero value, so nothing a second read could disturb is under test", ErrSample, carried.revision, this.name)
	}
	own, err := carried.read.selfDecode(bytes.Clone(carried.written))
	if err != nil {
		return none, err
	}
	if disturbs {
		if err := this.notAliased(carried, own); err != nil {
			return none, err
		}
	}
	if err := this.readBack(carried, own, sample); err != nil {
		return none, err
	}
	return carried.read.decode(carried.written)
}

func (this *Fact[S, ID, E]) notAliased(carried roundTripping[E], own any) error {
	walk := valueWalk{budget: valueWalkNodes}
	reused, err := reusesItsBuffer(carried, own, &walk)
	if err != nil {
		return err
	}
	if reused {
		return fmt.Errorf("%w: revision %d of %q decoded into memory its codec reuses, so reading the next payload rewrote the value it had already returned", ErrPayload, carried.revision, this.name)
	}
	return walk.unanswered(carried.revision, this.name)
}

func (this *Fact[S, ID, E]) readBack(carried roundTripping[E], own, sample any) error {
	walk := valueWalk{budget: valueWalkNodes}
	if !walk.same(reflect.ValueOf(own), reflect.ValueOf(sample)) {
		return fmt.Errorf("%w: revision %d of %q does not read back the %s it was given: what the sample encoded to decodes to a different value, so a fact recorded through it loses what no load can recover", ErrPayload, carried.revision, this.name, carried.read.typeName)
	}
	if err := walk.unanswered(carried.revision, this.name); err != nil {
		return err
	}
	if walk.opaque == "" {
		return nil
	}
	same, err := readsBackOnTheWire(carried, own)
	if err != nil {
		return err
	}
	if same {
		return nil
	}
	return fmt.Errorf("%w: revision %d of %q does not read back the %s it was given: nothing can compare the %s it carries, so what came back was encoded again and the bytes are not the ones the sample encoded to, and a fact recorded through it loses what no load can recover", ErrPayload, carried.revision, this.name, carried.read.typeName, walk.opaque)
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

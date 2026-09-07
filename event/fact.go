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
// that revision's own reader and compared with the sample in the revision's own
// type, so a codec that drops part of what it was given, one that cannot decode
// its own output, and an upcaster that refuses a legal historical value are all
// found before a stream contains one.
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
// disturb anything and is refused as a sample rather than reported as a pass,
// because it proves nothing about fidelity either.
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
	own, err := read.selfDecode(bytes.Clone(written))
	if err != nil {
		return none, err
	}
	reused, err := reusesItsBuffer(read, own, written, zero)
	if err != nil {
		return none, err
	}
	if reused {
		return none, fmt.Errorf("%w: revision %d of %q decoded into memory its codec reuses, so reading the next payload rewrote the value it had already returned", ErrPayload, revision, this.name)
	}
	budget := codecGraphNodes
	if !sameValue(reflect.ValueOf(own), reflect.ValueOf(sample), &budget) {
		return none, fmt.Errorf("%w: revision %d of %q does not read back the %s it was given: what the sample encoded to decodes to a different value, so a fact recorded through it loses what no load can recover", ErrPayload, revision, this.name, read.typeName)
	}
	return read.decode(written)
}

func reusesItsBuffer[V any](read link[V], own any, written, zero []byte) (bool, error) {
	second, err := read.selfDecode(bytes.Clone(written))
	if err != nil {
		return false, err
	}
	budget := codecGraphNodes
	if sharesMemory(reflect.ValueOf(own), reflect.ValueOf(second), &budget) {
		return true, nil
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

// Two values a codec returned for one payload, walked in step for a slice or a
// map they hold in common. Only the exported half of a struct is walked: an
// unexported field is the application's own business, and a decoded value
// carrying a pointer to something a package holds forever — a time zone, an
// interned constant — is ordinary and is not the reuse this asks about. An
// empty slice has no address of its own to compare.
func sharesMemory(first, second reflect.Value, budget *int) bool {
	if *budget <= 0 || !first.IsValid() || !second.IsValid() || first.Kind() != second.Kind() {
		return false
	}
	*budget--
	switch first.Kind() {
	case reflect.Slice, reflect.Map:
		if first.Len() > 0 && second.Len() > 0 && first.UnsafePointer() == second.UnsafePointer() {
			return true
		}
	case reflect.Pointer, reflect.Interface:
		return !first.IsNil() && !second.IsNil() && sharesMemory(first.Elem(), second.Elem(), budget)
	}
	switch first.Kind() {
	case reflect.Slice, reflect.Array:
		for index := range min(first.Len(), second.Len()) {
			if sharesMemory(first.Index(index), second.Index(index), budget) {
				return true
			}
		}
	case reflect.Map:
		for _, key := range first.MapKeys() {
			if sharesMemory(first.MapIndex(key), second.MapIndex(key), budget) {
				return true
			}
		}
	case reflect.Struct:
		for index := range first.NumField() {
			if first.Type().Field(index).IsExported() && sharesMemory(first.Field(index), second.Field(index), budget) {
				return true
			}
		}
	}
	return false
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

// The sample and what the codec read back, compared the way a recorded fact
// makes it matter. reflect.DeepEqual is the wrong comparison here and answers
// false for two payloads nothing is wrong with: a time.Time carries a monotonic
// reading and a location that no encoding preserves and that its own Equal says
// are not the difference, and an unexported field is not what was recorded. So
// the walk asks the type's own Equal method wherever it declares one, compares
// the exported half of a struct, treats two NaNs as one value because a codec
// that carries them is not the failure this looks for, and runs out of budget in
// the caller's favour — this refuses a declaration, so what it cannot answer it
// does not accuse.
func sameValue(first, second reflect.Value, budget *int) bool {
	if *budget <= 0 {
		return true
	}
	*budget--
	if !first.IsValid() || !second.IsValid() || first.Kind() != second.Kind() {
		return first.IsValid() == second.IsValid()
	}
	if answered, asked := equalByMethod(first, second); asked {
		return answered
	}
	switch first.Kind() {
	case reflect.Struct:
		for index := range first.NumField() {
			if first.Type().Field(index).IsExported() && !sameValue(first.Field(index), second.Field(index), budget) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		return first.Len() == second.Len() && sameElements(first, second, budget)
	case reflect.Map:
		return first.Len() == second.Len() && sameEntries(first, second, budget)
	case reflect.Pointer, reflect.Interface:
		if first.IsNil() || second.IsNil() {
			return first.IsNil() == second.IsNil()
		}
		return sameValue(first.Elem(), second.Elem(), budget)
	case reflect.Float32, reflect.Float64:
		held, read := first.Float(), second.Float()
		return held == read || (held != held && read != read)
	default:
		return !first.Comparable() || first.Equal(second)
	}
	return true
}

func sameElements(first, second reflect.Value, budget *int) bool {
	for index := range first.Len() {
		if !sameValue(first.Index(index), second.Index(index), budget) {
			return false
		}
	}
	return true
}

func sameEntries(first, second reflect.Value, budget *int) bool {
	for _, key := range first.MapKeys() {
		if !sameValue(first.MapIndex(key), second.MapIndex(key), budget) {
			return false
		}
	}
	return true
}

func equalByMethod(first, second reflect.Value) (bool, bool) {
	equal := first.MethodByName("Equal")
	if !equal.IsValid() {
		return false, false
	}
	asked := equal.Type()
	if asked.NumIn() != 1 || asked.In(0) != first.Type() || asked.NumOut() != 1 || asked.Out(0).Kind() != reflect.Bool {
		return false, false
	}
	return equal.Call([]reflect.Value{second})[0].Bool(), true
}

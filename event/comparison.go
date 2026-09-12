package event

import (
	"bytes"
	"fmt"
	"reflect"
)

// A type with exactly one value, which is every zero-sized type and so every
// marker fact: struct{} carries that a thing happened and nothing else, so its
// only sample encodes as its own zero value, there is no second payload to prove
// anything about reuse with, and nothing it holds can come back different.
func singleValued(held reflect.Type) bool {
	return held != nil && held.Size() == 0
}

func reusesItsBuffer[V any](carried roundTripping[V], own any, walk *valueWalk) (bool, error) {
	second, err := carried.read.selfDecode(bytes.Clone(carried.written))
	if err != nil {
		return false, err
	}
	if walk.shares(reflect.ValueOf(own), reflect.ValueOf(second)) {
		return true, nil
	}
	before, err := carried.read.selfEncode(own)
	if err != nil {
		return false, err
	}
	before = bytes.Clone(before)
	if _, err := carried.read.selfDecode(carried.zero); err != nil {
		return false, err
	}
	after, err := carried.read.selfEncode(own)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(before, after) {
		return true, nil
	}
	return disturbedAtItsOwnWidth(carried, own, before), nil
}

// The second disturbance, and the wider one. The zero value's encoding is
// narrower than the sample's wherever a payload carries anything, so a decode of
// it reaches the front of a reused buffer and never the bytes an answer of the
// sample's own width points at — which leaves a codec that fills its buffer to
// the payload's width and reaches it through a field no walk over exported
// fields compares seen by neither half. This one is the sample's own payload
// with one byte changed, which such a codec writes over the whole of what it
// already answered.
//
// Nothing is asked of the codec for it: a payload it refuses, or panics on, is
// one this probe learned nothing from, and the answer is then the narrower
// probe's. That is why a refusal here is not the caller's error and why the
// panic is swallowed rather than carried out — this is a question the round trip
// asks of its own accord, about a payload no store will ever hold.
func disturbedAtItsOwnWidth[V any](carried roundTripping[V], own any, before []byte) (moved bool) {
	if len(carried.written) == 0 {
		return false
	}
	defer func() {
		if recover() != nil {
			moved = false
		}
	}()
	disturbing := bytes.Clone(carried.written)
	disturbing[len(disturbing)-1]++
	if _, err := carried.read.selfDecode(disturbing); err != nil {
		return false
	}
	after, err := carried.read.selfEncode(own)
	if err != nil {
		return false
	}
	return !bytes.Equal(before, after)
}

// What the codec read back, put back on the wire and compared with the bytes the
// sample encoded to. It needs no method and no name and therefore answers for
// every type, which is what the value walk cannot do: a struct that keeps its
// state privately, declares no Equal and forbids == is every math/big value, and
// there the walk has nothing while the wire has the codec's own answer. It is
// the weaker of the two — a codec that drops the same thing on both sides
// re-encodes to the bytes it was given — so it sits behind the walk rather than
// beside it, and only where the walk had nothing to say.
func readsBackOnTheWire[V any](carried roundTripping[V], own any) (bool, error) {
	again, err := carried.read.selfEncode(own)
	if err != nil {
		return false, err
	}
	return bytes.Equal(again, carried.written), nil
}

// A sample is a specimen a caller wrote to prove a codec, not a production
// payload, and sixty-five thousand values is far past anything written by hand.
// codecGraphNodes is the wrong number here and was the one used: it bounds a
// type graph, and one type has any number of values.
const valueWalkNodes = 1 << 16

// The two values a round trip produced, walked in step under one budget. The
// type walk beside this one refuses what it cannot reach and so does this: a
// walk that stopped early has compared nothing past where it stopped, and
// answering the caller's way there is a check that cannot fail. What went
// unanswered is carried out rather than resolved — as a refusal where nothing
// else can answer, and as opaque where the wire still can.
type valueWalk struct {
	budget       int
	beyond       bool
	panicked     string
	unrecordable string
	opaque       string
}

func (this *valueWalk) spend() bool {
	if this.beyond || this.panicked != "" || this.unrecordable != "" || this.opaque != "" {
		return false
	}
	if this.budget <= 0 {
		this.beyond = true
		return false
	}
	this.budget--
	return true
}

func (this *valueWalk) unanswered(revision int, name string) error {
	switch {
	case this.panicked != "":
		return fmt.Errorf("%w: revision %d of %q carries a %s whose own Equal panicked when it was asked whether the sample came back, so what a load recovers is established by nothing here; compare that type in a test of your own", ErrSample, revision, name, this.panicked)
	case this.unrecordable != "":
		return fmt.Errorf("%w: revision %d of %q carries a %s: no wire format records a function and Go compares no two, so neither what came back nor what it encodes to says anything about it; record what identifies the behaviour — a name, a code — and choose the function from that when you fold", ErrSample, revision, name, this.unrecordable)
	case this.beyond:
		return fmt.Errorf("%w: revision %d of %q was given a sample holding more than the %d values a round trip walks, so what is past that is compared by nothing and a pass would claim more than it proved; round trip a smaller sample of the same type", ErrSample, revision, name, valueWalkNodes)
	}
	return nil
}

// Two values a codec returned for one payload, walked in step for a slice or a
// map they hold in common. Only the exported half of a struct is walked: an
// unexported field is the application's own business, and a decoded value
// carrying a pointer to something a package holds forever — a time zone, an
// interned constant — is ordinary and is not the reuse this asks about. An
// empty slice has no address of its own to compare. Two values of different
// types are answered rather than walked: one cannot be indexed or keyed by the
// other's shape, and memory neither reaches the same way is not memory they
// share.
func (this *valueWalk) shares(first, second reflect.Value) bool {
	if !this.spend() || !first.IsValid() || !second.IsValid() || first.Type() != second.Type() {
		return false
	}
	switch first.Kind() {
	case reflect.Slice, reflect.Map:
		if first.Len() > 0 && second.Len() > 0 && first.UnsafePointer() == second.UnsafePointer() {
			return true
		}
	case reflect.Pointer, reflect.Interface:
		return !first.IsNil() && !second.IsNil() && this.shares(first.Elem(), second.Elem())
	}
	switch first.Kind() {
	case reflect.Slice, reflect.Array:
		for index := range min(first.Len(), second.Len()) {
			if this.shares(first.Index(index), second.Index(index)) {
				return true
			}
		}
	case reflect.Map:
		for _, key := range first.MapKeys() {
			if this.shares(first.MapIndex(key), second.MapIndex(key)) {
				return true
			}
		}
	case reflect.Struct:
		for index := range first.NumField() {
			if first.Type().Field(index).IsExported() && this.shares(first.Field(index), second.Field(index)) {
				return true
			}
		}
	}
	return false
}

// The sample and what the codec read back, compared the way a recorded fact
// makes it matter. reflect.DeepEqual is the wrong comparison here and answers
// false for two payloads nothing is wrong with: a time.Time carries a monotonic
// reading and a location that no encoding preserves and that its own Equal says
// are not the difference, and an unexported field is not what was recorded. So
// the walk compares the exported half of a struct, treats two NaNs as one value
// because a codec that carries them is not the failure this looks for, and asks
// a type's own Equal only where that leaves it nothing to compare.
//
// Two values of different types are reached through an interface and are
// answered here rather than walked: walking would index one by the other's shape
// and panic, and a value substituted for another is a difference like any other.
// A number is the one exception, because no wire format this admits has an
// integer distinct from a float.
//
// Every kind the last arm reaches is comparable but one. A func is not — Go
// compares no two of them and no wire format records one — so it is carried out
// as ErrSample naming the repair rather than waved through as agreement, which
// is what asking whether the value was comparable at all used to do.
func (this *valueWalk) same(first, second reflect.Value) bool {
	if !this.spend() {
		return true
	}
	if !first.IsValid() || !second.IsValid() {
		return first.IsValid() == second.IsValid()
	}
	if first.Type() != second.Type() {
		return sameNumber(first, second)
	}
	switch first.Kind() {
	case reflect.Struct:
		return this.sameFields(first, second)
	case reflect.Slice, reflect.Array:
		return first.Len() == second.Len() && this.sameElements(first, second)
	case reflect.Map:
		return first.Len() == second.Len() && this.sameEntries(first, second)
	case reflect.Pointer, reflect.Interface:
		if first.IsNil() || second.IsNil() {
			return first.IsNil() == second.IsNil()
		}
		return this.same(first.Elem(), second.Elem())
	case reflect.Float32, reflect.Float64:
		held, read := first.Float(), second.Float()
		return held == read || (held != held && read != read)
	default:
		if first.Comparable() {
			return first.Equal(second)
		}
		this.unrecordable = first.Type().String()
		return true
	}
}

// The widening a codec produces for a number behind an interface, which is the
// one type difference that is not a substitution: JSON has one number, so an
// int64 an application recorded reads back as a float64 and a codec of its own
// may narrow just as freely. Compared as the wire carried it, and a number too
// large for that to hold is a difference like any other.
func sameNumber(first, second reflect.Value) bool {
	held, is := asFloat(first)
	read, was := asFloat(second)
	return is && was && held == read
}

func asFloat(value reflect.Value) (float64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		held := value.Int()
		return float64(held), int64(float64(held)) == held
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		held := value.Uint()
		return float64(held), uint64(float64(held)) == held
	case reflect.Float32, reflect.Float64:
		return value.Float(), true
	}
	return 0, false
}

func (this *valueWalk) sameFields(first, second reflect.Value) bool {
	compared := false
	for index := range first.NumField() {
		if !first.Type().Field(index).IsExported() {
			continue
		}
		compared = true
		if !this.same(first.Field(index), second.Field(index)) {
			return false
		}
	}
	if compared {
		return true
	}
	return this.sameOpaque(first, second)
}

func (this *valueWalk) sameElements(first, second reflect.Value) bool {
	for index := range first.Len() {
		if !this.same(first.Index(index), second.Index(index)) {
			return false
		}
	}
	return true
}

func (this *valueWalk) sameEntries(first, second reflect.Value) bool {
	for _, key := range first.MapKeys() {
		if !this.same(first.MapIndex(key), second.MapIndex(key)) {
			return false
		}
	}
	return true
}

// A struct whose every field is unexported is the one shape the field walk has
// nothing to compare, and it is the shape a value type that keeps its state
// privately has — time.Time, which every second payload carries, is one. Three
// answers in order, and the order is the point. Its own Equal first, because a
// type that declares one says there what its equality is: time.Time's says a
// monotonic reading and a location are not the difference, and == on it says
// they are. Then ==, which is what netip.Addr, netip.Prefix and every other
// comparable value object has instead of an Equal. Where neither exists the walk
// has no answer, and it says so rather than resolving it as agreement: a struct
// nothing could compare is exactly where a codec that drops what it holds would
// go unseen. What settles it there is the wire, one level up — big.Int, big.Rat
// and big.Float have neither an Equal nor a ==, and neither repair a fixed
// method name could ask for exists for a type the consumer does not own. Ahead
// of all three, a type of size zero holds one value and nothing it carries can
// come back different, which is what lets a marker fact round trip even where it
// forbids ==.
//
// Equal is asked here and nowhere else. Asked wherever it is declared it would
// answer for the whole struct, and an application writes Equal for the
// application's question: one that compares a title and not a body vetoes the
// only check that says the codec recorded what it was handed, and this is what
// that check is. It is not asked a second question either, because a type that
// declares Equal has said there what its equality is: a codec that normalises a
// time.Time to UTC is faithful by that answer and writes different bytes, and
// asking the wire beside the method would call it a difference. The call is into
// code the framework was promised nothing about, so a panic out of it is carried
// out as ErrSample rather than unwound.
func (this *valueWalk) sameOpaque(first, second reflect.Value) bool {
	if singleValued(first.Type()) {
		return true
	}
	if answered, same := this.equalByMethod(first, second); answered {
		return same
	}
	if first.Comparable() {
		return first.Equal(second)
	}
	this.opaque = first.Type().String()
	return true
}

func (this *valueWalk) equalByMethod(first, second reflect.Value) (answered, same bool) {
	equal := first.MethodByName("Equal")
	if !equal.IsValid() {
		return false, false
	}
	asked := equal.Type()
	if asked.NumIn() != 1 || asked.In(0) != first.Type() || asked.NumOut() != 1 || asked.Out(0).Kind() != reflect.Bool {
		return false, false
	}
	defer func() {
		if recover() != nil {
			this.panicked, answered, same = first.Type().String(), true, true
		}
	}()
	return true, equal.Call([]reflect.Value{second})[0].Bool()
}

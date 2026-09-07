package event

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// The second extension point, and the one that decides what a payload may be:
// whether a type can be encoded is answered here and never by a rule the kernel
// holds. Three obligations an implementation cannot be checked against and is
// therefore told:
//
//	The framework calls all three methods from every request goroutine
//	concurrently, and each must be safe for that.
//
//	Decode may return a value that aliases the payload it was given and may
//	return freshly allocated memory. It must not return one that aliases memory
//	the codec itself will write or reuse — Fact.RoundTrip is the runnable proxy
//	and refuses a codec that does.
//
//	CanEncode answers for the reader type alone and is asked at declaration, so
//	it must be constant and cheap; the shipped codec charges its answer when the
//	codec value is built.
//
// A panic out of any of the three is recovered into the refusal that method's
// own error produces — ErrEncode, ErrPayload and ErrCodecType. That is the whole
// rule for every extension point: the kernel recovers a panic from an extension
// that has a stated refusal channel for the same failure and maps it to the
// sentinel that channel already produces, and it recovers nothing else. So an
// upcaster's panic is recovered into ErrUpcast, and a fold's, an identity
// mapper's and a store's are not — the first two have no error channel to be a
// second spelling of, and a store has both an error channel and an Outcome
// vocabulary, so recovering it would be the kernel classifying a failure the
// store did not classify.
type Codec[V any] interface {
	Encode(V) ([]byte, error)
	Decode([]byte) (V, error)
	CanEncode() error
}

func JSON[V any]() Codec[V] {
	return jsonCodec[V]{charged: chargeJSON(reflect.TypeFor[V]())}
}

type jsonCodec[V any] struct{ charged error }

func (this jsonCodec[V]) CanEncode() error { return this.charged }

// The pointer is not incidental. It makes the value addressable, which is what
// lets encoding/json reach a marshaller declared on a pointer receiver — the
// ordinary Go idiom. Without it such a type is written field by field, as {} for
// most of them, and Decode, which has always passed a pointer, reads a zero
// value back with no error at any door.
func (jsonCodec[V]) Encode(value V) ([]byte, error) { return json.Marshal(&value) }

func (jsonCodec[V]) Decode(payload []byte) (V, error) {
	var value V
	err := json.Unmarshal(payload, &value)
	return value, err
}

const (
	codecGraphDepth = 1024
	codecGraphNodes = 1024
	codecGraphEdges = 4096
)

var (
	jsonMarshaler   = reflect.TypeFor[json.Marshaler]()
	jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
	textMarshaler   = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// A type and whether encoding/json can take its address where it stands, which
// decides which of the type's two method sets encoding/json reaches. Encode
// marshals a pointer, so the reader type is addressable, and so is everything
// behind a pointer, a slice element and an element of an addressable array; a
// map's keys and values are not, and never become so.
type position struct {
	value       reflect.Type
	addressable bool
}

type jsonWalk struct {
	seen  map[position]bool
	edges int
}

type route uint8

const (
	byFields route = iota
	byJSONMethods
	byTextMethods
)

// Whether a value of this type survives Encode and then Decode through this
// codec, charged once when the codec value is built so a declaration asks it
// once per retained revision. "Can encoding/json encode it" is the wrong
// question and was the one asked first: encoding/json answers nil for a type it
// writes as an empty object, and that fact is then recorded, stored and
// replayed as a zero value with no refusal at any door. So the walk refuses the
// five shapes whose bytes no declaration reads back — a type that writes itself
// and declares no matching unmarshaller, one whose marshaller sits on a pointer
// receiver where encoding/json cannot address it, a map key that writes itself
// and does not read itself, a struct with fields and no field encoding/json
// writes, and two fields of one struct rendering one JSON name — and stops at a
// type that both writes and reads itself, because its fields are then not what
// is written.
//
// The graph is an application's, may be recursive and is not the kernel's to
// trust, so the walk is bounded on three axes and keeps a visited set keyed by
// position rather than by type: one type is legal at the reader type and
// illegal as a map value, and a set keyed by type alone would answer for the
// first position it was reached through. Fields promoted from two different
// embedded structs are encoding/json's own depth rule and are not checked here.
func chargeJSON(value reflect.Type) error {
	walk := &jsonWalk{seen: map[position]bool{}}
	return walk.visit(position{value: value, addressable: true}, value.String(), 0)
}

func (this *jsonWalk) visit(at position, where string, depth int) error {
	if this.seen[at] {
		return nil
	}
	if depth > codecGraphDepth || len(this.seen) >= codecGraphNodes {
		return fmt.Errorf("%w: the type graph reached from %s is larger than the codec walks", ErrCodecType, where)
	}
	this.seen[at] = true
	if at.value.Kind() == reflect.Interface {
		return fmt.Errorf("%w: %s is an interface, so what it holds is not known at declaration and does not read back; declare the concrete type or a codec of your own", ErrCodecType, where)
	}
	if own, err := ownMethods(at, where); own || err != nil {
		return err
	}
	switch at.value.Kind() {
	case reflect.Chan, reflect.Func, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
		return fmt.Errorf("%w: %s is a %s and JSON encodes none", ErrCodecType, where, at.value.Kind())
	case reflect.Pointer:
		return this.descend(position{at.value.Elem(), true}, where, depth)
	case reflect.Slice:
		return this.descend(position{at.value.Elem(), true}, where+"[]", depth)
	case reflect.Array:
		return this.descend(position{at.value.Elem(), at.addressable}, where+"[]", depth)
	case reflect.Map:
		if err := objectKey(at.value.Key(), where); err != nil {
			return err
		}
		return this.descend(position{at.value.Elem(), false}, where+"[]", depth)
	case reflect.Struct:
		return this.fields(at, where, depth)
	}
	return nil
}

func (this *jsonWalk) fields(at position, where string, depth int) error {
	rendered := map[string]bool{}
	written := 0
	for index := range at.value.NumField() {
		field := at.value.Field(index)
		if field.Tag.Get("json") == "-" || !readAsJSON(field) {
			continue
		}
		written++
		if name := renderedName(field); name != "" {
			if rendered[name] {
				return fmt.Errorf("%w: two fields of %s render the JSON name %q, and encoding/json writes neither", ErrCodecType, where, name)
			}
			rendered[name] = true
		}
		if err := this.descend(position{field.Type, at.addressable}, where+"."+field.Name, depth); err != nil {
			return err
		}
	}
	if written == 0 && at.value.NumField() > 0 {
		return fmt.Errorf("%w: %s has %d fields and encoding/json writes none of them, so every value of it encodes as an empty object and reads back as its zero value", ErrCodecType, where, at.value.NumField())
	}
	return nil
}

func (this *jsonWalk) descend(child position, where string, depth int) error {
	if this.edges >= codecGraphEdges {
		return fmt.Errorf("%w: the type graph reached from %s is larger than the codec walks", ErrCodecType, where)
	}
	this.edges++
	return this.visit(child, where, depth+1)
}

func ownMethods(at position, where string) (bool, error) {
	writes, reads := writeRoute(at), readRoute(at)
	switch {
	case writes != byFields && writes == reads:
		return true, nil
	case writes != byFields:
		return true, fmt.Errorf("%w: %s writes itself through %s and declares no %s, so a fact recorded with it is read back by no declaration", ErrCodecType, where, writes.writer(), writes.reader())
	case writeRoute(position{value: at.value, addressable: true}) != byFields:
		return true, fmt.Errorf("%w: %s stands where encoding/json cannot take its address, so the marshaller on its pointer receiver is never reached and it is written field by field; hold it behind a pointer, or declare the marshaller on the value receiver", ErrCodecType, where)
	}
	return false, nil
}

func writeRoute(at position) route {
	switch {
	case writesAs(at, jsonMarshaler):
		return byJSONMethods
	case writesAs(at, textMarshaler):
		return byTextMethods
	}
	return byFields
}

func readRoute(at position) route {
	pointer := reflect.PointerTo(at.value)
	switch {
	case pointer.Implements(jsonUnmarshaler):
		return byJSONMethods
	case pointer.Implements(textUnmarshaler):
		return byTextMethods
	}
	return byFields
}

func writesAs(at position, marshaler reflect.Type) bool {
	if at.value.Implements(marshaler) {
		return true
	}
	return at.addressable && at.value.Kind() != reflect.Pointer &&
		reflect.PointerTo(at.value).Implements(marshaler)
}

func (this route) writer() string {
	if this == byTextMethods {
		return "MarshalText"
	}
	return "MarshalJSON"
}

func (this route) reader() string {
	if this == byTextMethods {
		return "UnmarshalText"
	}
	return "UnmarshalJSON"
}

func objectKey(key reflect.Type, where string) error {
	switch key.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return nil
	}
	switch {
	case !key.Implements(textMarshaler):
		return fmt.Errorf("%w: %s is keyed by a %s and JSON renders no object key from one", ErrCodecType, where, key.Kind())
	case !reflect.PointerTo(key).Implements(textUnmarshaler):
		return fmt.Errorf("%w: %s is keyed by a type that writes itself through MarshalText and declares no UnmarshalText, so the names it writes are read back by no declaration", ErrCodecType, where)
	}
	return nil
}

func renderedName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	switch {
	case name != "":
		return name
	case field.Anonymous && structBehind(field.Type):
		return ""
	}
	return field.Name
}

func readAsJSON(field reflect.StructField) bool {
	return field.IsExported() || (field.Anonymous && structBehind(field.Type))
}

func structBehind(value reflect.Type) bool {
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value.Kind() == reflect.Struct
}

func typeNameOf[V any]() string { return reflect.TypeFor[V]().String() }

func encodeWith[V any](codec Codec[V], value V) (encoded []byte, err error) {
	defer func() {
		if recover() != nil {
			encoded, err = nil, newRefusal(ErrEncode, nil, nil)
		}
	}()
	encoded, err = codec.Encode(value)
	if err != nil {
		return nil, newRefusal(ErrEncode, nil, err)
	}
	return encoded, nil
}

func decodeWith[V any](codec Codec[V], payload []byte) (value V, err error) {
	defer func() {
		if recover() != nil {
			var none V
			value, err = none, newRefusal(ErrPayload, nil, nil)
		}
	}()
	value, err = codec.Decode(payload)
	if err != nil {
		var none V
		return none, newRefusal(ErrPayload, nil, err)
	}
	return value, nil
}

func canEncodeWith[V any](codec Codec[V], revision int) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("%w: revision %d's codec panicked when it was asked", ErrCodecType, revision)
		}
	}()
	if answer := codec.CanEncode(); answer != nil {
		return fmt.Errorf("%w: revision %d: %s", ErrCodecType, revision, answer)
	}
	return nil
}

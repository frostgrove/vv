package event

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

var (
	jsonMarshaler   = reflect.TypeFor[json.Marshaler]()
	jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
	textMarshaler   = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

type route uint8

const (
	byFields route = iota
	byJSONMethods
	byTextMethods
)

// The promotion question is asked before the receiver question, and of every
// struct that writes itself through methods rather than only where the two
// routes agree. An embedded pointer promotes both of the embedded type's method
// sets onto the outer value, so the outer implements the unmarshaller on its own
// value: the receiver arm answers first and tells a developer who declared the
// reader on the pointer receiver, correctly, to declare it on the pointer
// receiver — for a shape that discards no write but dereferences nil.
func ownMethods(at position, where string) (bool, error) {
	writes, reads := writeRoute(at), readRoute(at)
	if writes == byFields {
		if writeRoute(position{value: at.value, addressable: true}) != byFields {
			return true, fmt.Errorf("%w: %s stands where encoding/json cannot take its address, so the marshaller on its pointer receiver is never reached and it is written field by field; hold it behind a pointer, or declare the marshaller on the value receiver", ErrCodecType, where)
		}
		return false, nil
	}
	if behind, promotes := structBehind(at.value); promotes {
		if err := promotedMarshaller(behind, writes, where, ""); err != nil {
			return true, err
		}
	}
	switch {
	case writes == reads:
		return true, nil
	case onTheValueReceiver(at.value, writes.unmarshaler()):
		return true, fmt.Errorf("%w: %s declares %s on the value receiver, so encoding/json calls it on a copy and discards everything it writes; every fact recorded with it reads back as the zero value with no error at any door — declare it on the pointer receiver", ErrCodecType, where, writes.reader())
	}
	return true, fmt.Errorf("%w: %s writes itself through %s and declares no %s, so a fact recorded with it is read back by no declaration", ErrCodecType, where, writes.writer(), writes.reader())
}

// Go promotes an embedded type's methods onto the struct that embeds it, so a
// struct embedding a time.Time or any other shared value type with a
// marshalling pair writes itself as that value and nothing else: every field
// declared beside the embedded one is written by nobody and read back by
// nobody, with nil at every door. Promotion is a language rule and no struct tag
// undoes it, so the tag on an embedded field is not read here: json:"-" stops
// encoding/json writing that field as a member and leaves the promoted
// marshaller exactly where it was — and it is the spelling a developer reaches
// for after reading this refusal, which is why the refusal says so. reflect will
// not say whether a method is declared on the type or promoted into it, so the
// question is asked of the embedded fields instead. A struct that declares its
// own pair and embeds a marshalling type is refused too — it fails closed at
// declaration with one remedy, which is the trade the walk already makes
// elsewhere.
//
// Promotion carries through as many embeddings as it takes, so the question is
// asked of the whole chain and not of the outermost struct alone: struct{ B }
// where B is struct{ money; Extra string } writes itself as the money and loses
// Extra exactly as B does, and a walk that stops at the first hop accepts the
// wrapper while refusing what it wraps. The recursion enters an embedded struct
// held by value and nothing else — an embedded pointer is refused where it is
// found and a non-struct promotes no fields — and a struct cannot contain
// itself by value, so the chain is finite without a visited set.
//
// An embedded pointer promotes the same pair and is worse than a loss: nothing
// allocates it before encoding/json calls through it, so the read dereferences
// nil and panics, and so does the write of a value whose pointer was never set.
// That one is refused with nothing beside it too, because the panic is the whole
// failure and no field has to be hidden for it.
func promotedMarshaller(value reflect.Type, writes route, where, through string) error {
	for index := range value.NumField() {
		field := value.Field(index)
		if !field.Anonymous {
			continue
		}
		if writeRoute(position{value: field.Type, addressable: true}) != writes {
			continue
		}
		hidden := besideIt(value, index)
		if hidden != "" {
			hidden = through + hidden
		}
		if field.Type.Kind() == reflect.Pointer {
			return fmt.Errorf("%w: %s writes itself through the %s promoted from %s, which nothing allocates before encoding/json calls the %s promoted with it, so every load of a fact recorded with it panics on a nil pointer%s; embed the type by value, or name the field", ErrCodecType, where, writes.writer(), embeddedAs(field.Type, through), writes.reader(), alsoHidden(hidden))
		}
		if hidden != "" {
			return fmt.Errorf("%w: %s writes itself through the %s of %s, so %s is written by nobody and read back by nobody%s; %s", ErrCodecType, where, writes.writer(), embeddedAs(field.Type, through), hidden, noTagUndoesIt(field), nameItInstead(field.Type, writes))
		}
		if field.Type.Kind() != reflect.Struct {
			continue
		}
		if err := promotedMarshaller(field.Type, writes, where, through+field.Name+"."); err != nil {
			return err
		}
	}
	return nil
}

func embeddedAs(embedded reflect.Type, through string) string {
	named := embedded.String()
	if embedded.Kind() == reflect.Pointer {
		named = "pointer " + named
	}
	if through == "" {
		return "the embedded " + named
	}
	return fmt.Sprintf("the %s embedded in %s", named, strings.TrimSuffix(through, "."))
}

func alsoHidden(hidden string) string {
	if hidden == "" {
		return ""
	}
	return fmt.Sprintf(", and %s is written by nobody either", hidden)
}

func noTagUndoesIt(field reflect.StructField) string {
	tag, tagged := field.Tag.Lookup("json")
	if !tagged {
		return ""
	}
	return fmt.Sprintf(" — the json:%q you put on it does not undo that, because a tag speaks to encoding/json's field walk and method promotion is a language rule", tag)
}

func nameItInstead(embedded reflect.Type, writes route) string {
	switch {
	case readRoute(position{value: embedded, addressable: true}) == writes:
		return "name the embedded field instead of promoting it, or declare a codec of your own"
	case onTheValueReceiver(embedded, writes.unmarshaler()):
		return fmt.Sprintf("%s declares %s on the value receiver, where encoding/json calls it on a copy, so naming the embedded field moves the refusal one hop in rather than closing it — declare that reader on the pointer receiver", embedded, writes.reader())
	}
	return fmt.Sprintf("%s declares no %s, so naming the embedded field moves the refusal one hop in rather than closing it — declare that reader, or declare a codec of your own", embedded, writes.reader())
}

func besideIt(value reflect.Type, embedded int) string {
	for index := range value.NumField() {
		field := value.Field(index)
		if index == embedded || field.Tag.Get("json") == "-" || !readAsJSON(field) {
			continue
		}
		return field.Name
	}
	return ""
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
	switch {
	case readsAs(at.value, jsonUnmarshaler):
		return byJSONMethods
	case readsAs(at.value, textUnmarshaler):
		return byTextMethods
	}
	return byFields
}

// Decode always passes a pointer and encoding/json walks down one, so the read
// side asks nothing about the position: every value it fills is addressable.
// What it does ask is the receiver. encoding/json calls the unmarshaller through
// that pointer, so a method declared on the value receiver runs against a copy
// and every field it sets is discarded — json.Unmarshal returns nil and the
// value is its zero. Such a method is present, is inert, and is therefore not a
// read route.
func readsAs(value, unmarshaler reflect.Type) bool {
	if value.Kind() == reflect.Pointer {
		return readsAs(value.Elem(), unmarshaler)
	}
	return reflect.PointerTo(value).Implements(unmarshaler) && !value.Implements(unmarshaler)
}

func onTheValueReceiver(value, unmarshaler reflect.Type) bool {
	if value.Kind() == reflect.Pointer {
		return onTheValueReceiver(value.Elem(), unmarshaler)
	}
	return value.Implements(unmarshaler)
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

func (this route) unmarshaler() reflect.Type {
	if this == byTextMethods {
		return textUnmarshaler
	}
	return jsonUnmarshaler
}

// A map key is routed through MarshalText whatever its kind and read back
// through UnmarshalText whatever its kind, so the question is the one
// ownMethods asks of a value — do the two routes agree — and it is asked before
// the kinds JSON renders on their own. Asking the kinds first is what accepted
// `type Currency string` with a MarshalText written for display and no reader:
// every key it wrote was a rendered name that read back as itself.
func objectKey(key reflect.Type, where string) error {
	writes := key.Implements(textMarshaler)
	reads := readsAs(key, textUnmarshaler)
	switch {
	case writes && reads:
		return nil
	case writes && onTheValueReceiver(key, textUnmarshaler):
		return fmt.Errorf("%w: %s is keyed by a type declaring UnmarshalText on the value receiver, so encoding/json calls it on a copy and discards it; every key it wrote reads back as the zero key, and a map of them collapses to one entry with no error at any door — declare it on the pointer receiver", ErrCodecType, where)
	case writes:
		return fmt.Errorf("%w: %s is keyed by a type that writes itself through MarshalText and declares no UnmarshalText, so the names it writes are read back by no declaration", ErrCodecType, where)
	case reads:
		return fmt.Errorf("%w: %s is keyed by a type that reads itself through UnmarshalText and declares no MarshalText a map key can reach, so the names it writes are not the names it reads back; a key is never addressable, and a MarshalText on the pointer receiver is not one", ErrCodecType, where)
	}
	switch key.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return nil
	}
	return fmt.Errorf("%w: %s is keyed by a %s and JSON renders no object key from one", ErrCodecType, where, key.Kind())
}

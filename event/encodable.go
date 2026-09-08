package event

import (
	"fmt"
	"reflect"
	"strings"
)

const (
	codecGraphDepth = 1024
	codecGraphNodes = 1024
	codecGraphEdges = 4096
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

// Whether a value of this type survives Encode and then Decode through this
// codec, charged once when the codec value is built so a declaration asks it
// once per retained revision. "Can encoding/json encode it" is the wrong
// question and was the one asked first: encoding/json answers nil for a type it
// writes as an empty object, and that fact is then recorded, stored and
// replayed as a zero value with no refusal at any door. So the walk refuses the
// nine shapes whose bytes no declaration reads back — a type that writes
// itself and declares no matching unmarshaller, one whose unmarshaller sits on
// the value receiver and is therefore called on a copy, one whose marshaller
// sits on a pointer receiver where encoding/json cannot address it, a map key
// whose text methods do not come as a pair or come with the reader on the value
// receiver, a struct with fields and no field encoding/json writes, two fields
// rendering one JSON name, a struct written by a marshaller promoted from an
// embedded field with a field of its own beside it, at any depth of the
// promotion chain and however that field is tagged, since no tag undoes
// promotion, a struct whose marshalling pair is promoted from an embedded
// pointer, which encoding/json never allocates before calling through it, and a
// field reached through an embedded pointer to an unexported struct type, which
// encoding/json writes and can never allocate to read back.
//
// It stops at a type that both writes and reads itself, because its fields are
// then not what is written — except at a struct, where the pair may have been
// promoted from an embedded field rather than declared, and reflect will not say
// which.
//
// The graph is an application's, may be recursive and is not the kernel's to
// trust, so the walk is bounded on three axes and keeps a visited set keyed by
// position rather than by type: one type is legal at the reader type and
// illegal as a map value, and a set keyed by type alone would answer for the
// first position it was reached through.
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
	if settled, err := ownMethods(at, where); settled {
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
	claimed := members{where: where, held: map[string]string{}, budget: codecGraphNodes}
	if err := claimed.collect(at.value, "", "", map[reflect.Type]bool{}); err != nil {
		return err
	}
	written := 0
	for index := range at.value.NumField() {
		field := at.value.Field(index)
		if field.Tag.Get("json") == "-" || !readAsJSON(field) {
			continue
		}
		written++
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

// The JSON names one struct renders, promoted fields included. encoding/json
// writes the fields of an embedded struct as if they were declared here, and a
// name two of them claim is written once at best: the shallower field wins and
// the deeper is dropped, or two at one depth drop each other. Both lose a field
// with no error at any door, and neither is visible in the type's own field
// list — which is where this walk looked first, so a struct embedding two
// structs that each carry an ID encoded as `{}` and was accepted. A name
// claimed twice is therefore refused however it was reached, and the remedy is
// the same for every route: tag one of them, or spell it json:"-".
//
// The recursion follows embedded fields only, and a type already on the path is
// not entered again — an embedded pointer to the type being walked is legal Go
// and would otherwise not terminate.
//
// blocked carries the other half of encoding/json's promotion rule, which is
// asymmetric: it writes the fields promoted through an embedded pointer to an
// unexported struct type and cannot read one of them back, because reflect
// cannot allocate a pointer it may not set. The refusal is therefore raised
// where a name is recorded rather than where the pointer is seen — a promoted
// name that is never rendered, which is what a type embedding a pointer to
// itself has, costs nothing and stays legal.
type members struct {
	where  string
	held   map[string]string
	budget int
}

func (this *members) collect(owner reflect.Type, prefix, blocked string, chain map[reflect.Type]bool) error {
	if chain[owner] {
		return nil
	}
	chain[owner] = true
	defer delete(chain, owner)
	for index := range owner.NumField() {
		field := owner.Field(index)
		if field.Tag.Get("json") == "-" || !readAsJSON(field) {
			continue
		}
		rendered := taggedName(field)
		unwritable := blocked
		if unwritable == "" && field.Anonymous && !field.IsExported() && field.Type.Kind() == reflect.Pointer {
			unwritable = prefix + field.Name
		}
		if embedded, promotes := structBehind(field.Type); promotes && field.Anonymous && rendered == "" {
			if err := this.collect(embedded, prefix+field.Name+".", unwritable, chain); err != nil {
				return err
			}
			continue
		}
		this.budget--
		if this.budget < 0 {
			return fmt.Errorf("%w: the type graph reached from %s is larger than the codec walks", ErrCodecType, this.where)
		}
		if rendered == "" {
			rendered = field.Name
		}
		path := prefix + field.Name
		if unwritable != "" {
			return fmt.Errorf("%w: %s renders %s through the embedded pointer %s, whose type is unexported — encoding/json writes that name and can never read it back, because it cannot allocate a pointer it may not set, so the fact is recorded and every load of it fails forever; embed the type by value, or export it", ErrCodecType, this.where, path, unwritable)
		}
		if first, taken := this.held[rendered]; taken {
			return fmt.Errorf("%w: two fields of %s render the JSON name %q — %s and %s — so at most one of them is ever written and the payload does not read back as it was decided; tag one of them or spell it json:\"-\"", ErrCodecType, this.where, rendered, first, path)
		}
		this.held[rendered] = path
	}
	return nil
}

func taggedName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	return name
}

func readAsJSON(field reflect.StructField) bool {
	_, promotes := structBehind(field.Type)
	return field.IsExported() || (field.Anonymous && promotes)
}

func structBehind(value reflect.Type) (reflect.Type, bool) {
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value, value.Kind() == reflect.Struct
}

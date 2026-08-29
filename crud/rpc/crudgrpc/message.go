package crudgrpc

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

const maxExactQueryInteger = float64((1 << 53) - 1)
const maxExactResponseInteger int64 = (1 << 53) - 1

// The wire shape of every method is google.protobuf.Struct, in and out, and the
// document inside it is the one the HTTP bindings speak.
//
// It goes through encoding/json rather than through structpb's own map
// conversion, and that is the load-bearing part. The model's `json` tags decide
// the document, a generated <Model>Input keeps meaning, and crud.Opt keeps its
// three states: a key that is absent is absent, a key holding NullValue is an
// explicit null, and structpb tells the two apart because one is a map entry
// and the other is not ([[UC-003]]). A map[string]any built by hand would
// collapse them the moment it went through a Go nil.

// toStruct turns a value the service answered into the response document.
func toStruct(v any) (*structpb.Struct, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	st := &structpb.Struct{}
	if err := protojson.Unmarshal(raw, st); err != nil {
		return nil, err
	}
	return st, nil
}

// fromStruct decodes a request document into a Go value. A nil or empty Struct
// leaves the value alone: an empty request means "no narrowing", as an empty
// body does on POST /query and POST /count.
func fromStruct(st *structpb.Struct, v any) error {
	if st == nil || len(st.GetFields()) == 0 {
		return nil
	}
	raw, err := protojson.Marshal(st)
	if err != nil {
		return port.BadRequestAs(errs.CodeMalformedBody, nil, "%s", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return port.BadRequestAs(errs.CodeMalformedBody, nil, "%s", err)
	}
	return nil
}

// sub reads one field of a request as a nested document. A field that is
// present and is not an object is a client mistake rather than an empty one:
// silently reading `{"patch": 3}` as "no patch" is how a request that meant
// something becomes a no-op.
func sub(st *structpb.Struct, name string) (*structpb.Struct, error) {
	v, ok := st.GetFields()[name]
	if !ok || v == nil {
		return nil, nil
	}
	if _, isNull := v.GetKind().(*structpb.Value_NullValue); isNull {
		return nil, nil
	}
	nested, ok := v.GetKind().(*structpb.Value_StructValue)
	if !ok {
		return nil, port.BadRequestAs(errs.CodeMalformedBody, errs.Path{errs.Named(name)},
			"%s must be an object", name)
	}
	return nested.StructValue, nil
}

// requiredSub is the write-body variant of sub. A query can reasonably be
// absent (or explicitly null) because it is optional narrowing; a present
// mutation body cannot. Treating null as a zero DTO turns a malformed request
// into a write with a meaning the client did not send.
func requiredSub(st *structpb.Struct, name string) (*structpb.Struct, error) {
	v, ok := st.GetFields()[name]
	if !ok || v == nil {
		return nil, port.BadRequestAs(errs.CodeMalformedBody, errs.Path{errs.Named(name)},
			"%s must be an object", name)
	}
	if _, isNull := v.GetKind().(*structpb.Value_NullValue); isNull {
		return nil, port.BadRequestAs(errs.CodeMalformedBody, errs.Path{errs.Named(name)},
			"%s must be an object, not null", name)
	}
	nested, ok := v.GetKind().(*structpb.Value_StructValue)
	if !ok {
		return nil, port.BadRequestAs(errs.CodeMalformedBody, errs.Path{errs.Named(name)},
			"%s must be an object", name)
	}
	return nested.StructValue, nil
}

// queryOf reads a whole request document as the query DSL.
func queryOf(st *structpb.Struct, meta *crud.Meta) (*query.Request, error) {
	request := &query.Request{}
	switch lossyQueryNumber(st, meta) {
	case lossyFilterInteger:
		return nil, port.BadRequestAs(errs.CodeBadQuery, nil,
			"integral filter operands outside the exact range must be sent as decimal strings")
	case lossyQueryControl:
		return nil, port.BadRequestAs(errs.CodeBadQuery, nil,
			"page, limit, offset and preload.maxRows must be exact JSON integers")
	}
	if err := fromStruct(st, request); err != nil {
		return nil, err
	}
	return request, nil
}

// structpb stores every JSON number as float64. The loss matters for an
// integral model column, not for a float column merely because its magnitude
// is large. Walk the query's structured filter with its model metadata and
// reject only unsafe integral operands; exact decimal strings then reach the
// normal query coercer unchanged.
type lossyQueryNumberKind uint8

const (
	noLossyQueryNumber lossyQueryNumberKind = iota
	lossyFilterInteger
	lossyQueryControl
)

func lossyQueryNumber(st *structpb.Struct, meta *crud.Meta) lossyQueryNumberKind {
	if st == nil {
		return noLossyQueryNumber
	}
	for name, value := range st.GetFields() {
		switch name {
		case "filter":
			if lossyFilter(value, meta) {
				return lossyFilterInteger
			}
		case "preload":
			if kind := lossyPreloads(value, meta); kind != noLossyQueryNumber {
				return kind
			}
		case "page", "limit", "offset":
			if lossyIntegralValue(value) {
				return lossyQueryControl
			}
		}
	}
	return noLossyQueryNumber
}

// lossyPreloads follows each declared relation before inspecting its filter.
// Preload filters are compiled against the relation target, not the root model,
// so checking them with root metadata either misses the target's integral
// columns or mistakes an unrelated root field for one.
func lossyPreloads(value *structpb.Value, root *crud.Meta) lossyQueryNumberKind {
	if value == nil || root == nil {
		return noLossyQueryNumber
	}
	if one := value.GetStructValue(); one != nil {
		return lossyPreload(one, root)
	}
	if list := value.GetListValue(); list != nil {
		for _, item := range list.Values {
			if obj := item.GetStructValue(); obj != nil {
				if kind := lossyPreload(obj, root); kind != noLossyQueryNumber {
					return kind
				}
			}
		}
	}
	return noLossyQueryNumber
}

func lossyPreload(item *structpb.Struct, root *crud.Meta) lossyQueryNumberKind {
	// Struct stores every JSON number as float64. A row cap is an integer too,
	// so it needs the same exactness gate as paging before Request unmarshalling
	// turns the already-rounded value back into an int.
	if lossyIntegralValue(item.GetFields()["maxRows"]) {
		return lossyQueryControl
	}
	pathValue, ok := item.GetFields()["path"]
	if !ok || pathValue.GetStringValue() == "" {
		return noLossyQueryNumber // Query's normal validation gives malformed preloads their error.
	}
	rel, _, err := root.RelationAt(pathValue.GetStringValue())
	if err != nil {
		return noLossyQueryNumber
	}
	target, _, _, err := rel.Resolve()
	if err != nil {
		return noLossyQueryNumber
	}
	if lossyFilter(item.GetFields()["filter"], target) {
		return lossyFilterInteger
	}
	return noLossyQueryNumber
}

func lossyFilter(value *structpb.Value, meta *crud.Meta) bool {
	obj := value.GetStructValue()
	if obj == nil || meta == nil {
		return false
	}
	for name, operand := range obj.GetFields() {
		if isLogicalFilterKey(meta, name) {
			if nested := operand.GetStructValue(); nested != nil {
				if lossyFilter(operand, meta) {
					return true
				}
			}
			if list := operand.GetListValue(); list != nil {
				for _, item := range list.Values {
					if lossyFilter(item, meta) {
						return true
					}
				}
			}
			continue
		}
		field, _, err := meta.FieldAt(name)
		if err != nil {
			continue // Compile will name the unknown path as the client error.
		}
		if lossyFieldOperand(operand, crud.ElemType(field.Type).Kind()) {
			return true
		}
	}
	return false
}

func isLogicalFilterKey(meta *crud.Meta, name string) bool {
	if strings.HasPrefix(name, "$") {
		switch strings.ToLower(name) {
		case "$and", "$or", "$not":
			return true
		}
	}
	switch strings.ToLower(name) {
	case "and", "or", "not":
		_, _, err := meta.FieldAt(name)
		return err != nil
	default:
		return false
	}
}

func lossyFieldOperand(value *structpb.Value, kind reflect.Kind) bool {
	if value == nil {
		return false
	}
	if number, ok := value.Kind.(*structpb.Value_NumberValue); ok {
		return integralKind(kind) && lossyNumber(number.NumberValue)
	}
	if list := value.GetListValue(); list != nil {
		for _, item := range list.Values {
			if lossyFieldOperand(item, kind) {
				return true
			}
		}
	}
	if obj := value.GetStructValue(); obj != nil {
		for _, item := range obj.GetFields() {
			if lossyFieldOperand(item, kind) {
				return true
			}
		}
	}
	return false
}

func integralKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

func lossyIntegralValue(value *structpb.Value) bool {
	number, ok := value.GetKind().(*structpb.Value_NumberValue)
	return ok && lossyNumber(number.NumberValue)
}

func lossyNumber(n float64) bool {
	return math.Trunc(n) == n && math.Abs(n) > maxExactQueryInteger
}

// queryIn reads the nested `query` field of a request as the query DSL.
func queryIn(st *structpb.Struct, meta *crud.Meta) (*query.Request, error) {
	nested, err := sub(st, "query")
	if err != nil {
		return nil, err
	}
	return queryOf(nested, meta)
}

// idOf reads the key out of a request.
//
// A string, because google.protobuf.Value has no integer. The API treats an
// integral number at magnitude 2^53 and beyond as outside its safe range. A number is accepted only
// inside that exact range, because a caller typing {"id": 42} into grpcurl
// means it; outside it the caller must use the string spelling.
func idOf[ID comparable](st *structpb.Struct, name string) (ID, error) {
	var zero ID
	v, ok := st.GetFields()[name]
	if !ok {
		return zero, port.BadRequestAs(errs.CodeInvalidID, nil, "missing %s", name)
	}
	raw, err := scalar(v, name)
	if err != nil {
		return zero, err
	}
	return port.CoerceID[ID](raw)
}

// idsOf reads a set of keys, in the order the client sent them.
func idsOf[ID comparable](st *structpb.Struct, name string) ([]ID, error) {
	v, ok := st.GetFields()[name]
	if !ok {
		return nil, nil
	}
	if _, isNull := v.GetKind().(*structpb.Value_NullValue); isNull {
		return nil, nil
	}
	list, ok := v.GetKind().(*structpb.Value_ListValue)
	if !ok {
		return nil, port.BadRequestAs(errs.CodeBadQuery, errs.Path{errs.Named(name)},
			"%s must be a list", name)
	}
	values := list.ListValue.GetValues()
	out := make([]ID, 0, len(values))
	for i, item := range values {
		raw, err := scalar(item, name)
		if err != nil {
			return nil, err
		}
		id, err := port.CoerceID[ID](raw)
		if err != nil {
			return nil, port.BadRequestAs(errs.CodeInvalidID,
				errs.Path{errs.Named(name), errs.Indexed(i)}, "%q is not a valid id", raw)
		}
		out = append(out, id)
	}
	return out, nil
}

// scalar reads a key as the text port.CoerceID converts.
func scalar(v *structpb.Value, name string) (string, error) {
	switch k := v.GetKind().(type) {
	case *structpb.Value_StringValue:
		return k.StringValue, nil
	case *structpb.Value_NumberValue:
		if lossyNumber(k.NumberValue) {
			return "", port.BadRequestAs(errs.CodeInvalidID, errs.Path{errs.Named(name)},
				"%s outside the exact integer range must be sent as a string", name)
		}
		// 'f' with -1 precision, so 42 is "42" and not "42.000000". A caller
		// that needs a key this cannot spell exactly sends it as a string.
		return strconv.FormatFloat(k.NumberValue, 'f', -1, 64), nil
	default:
		return "", port.BadRequestAs(errs.CodeInvalidID, errs.Path{errs.Named(name)},
			"%s must be a string or a number", name)
	}
}

// countDoc and deletedDoc are the two answers that are not an entity. They are
// built as Structs directly rather than through a Go map, so the key is spelled
// once and matches the HTTP bindings' body.
func countDoc(n int64) *structpb.Struct {
	return &structpb.Struct{Fields: map[string]*structpb.Value{
		"count": exactIntValue(n),
	}}
}

func deletedDoc(n int64) *structpb.Struct {
	return &structpb.Struct{Fields: map[string]*structpb.Value{
		"deleted": exactIntValue(n),
	}}
}

// exactIntValue keeps response counts truthful in protobuf Struct, whose only
// numeric representation is float64. Small values remain JSON numbers for
// ergonomic clients; values outside vv's safe contiguous integer range
// (|n| < 2^53) travel as decimal strings rather than as nearby counts.
func exactIntValue(n int64) *structpb.Value {
	if n > maxExactResponseInteger || n < -maxExactResponseInteger {
		return structpb.NewStringValue(strconv.FormatInt(n, 10))
	}
	return structpb.NewNumberValue(float64(n))
}

package crudgrpc

import (
	"encoding/json"
	"strconv"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

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

// queryOf reads a whole request document as the query DSL.
func queryOf(st *structpb.Struct) (*query.Request, error) {
	req := &query.Request{}
	if err := fromStruct(st, req); err != nil {
		return nil, err
	}
	return req, nil
}

// queryIn reads the nested `query` field of a request as the query DSL.
func queryIn(st *structpb.Struct) (*query.Request, error) {
	nested, err := sub(st, "query")
	if err != nil {
		return nil, err
	}
	return queryOf(nested)
}

// idOf reads the key out of a request.
//
// A string, because google.protobuf.Value has no integer: an int64 above 2^53
// would not survive being a double. A number is accepted too, because a caller
// typing {"id": 42} into grpcurl means it — and past 2^53 that path is as lossy
// as any other number in a Struct, which is what the string spelling is for.
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
		"count": structpb.NewNumberValue(float64(n)),
	}}
}

func deletedDoc(n int64) *structpb.Struct {
	return &structpb.Struct{Fields: map[string]*structpb.Value{
		"deleted": structpb.NewNumberValue(float64(n)),
	}}
}

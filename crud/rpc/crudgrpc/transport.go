package crudgrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/remote"
)

// Transport calls a resource another service registered, over gRPC.
//
//	articles := remote.New[Article, int64, ArticleInput](
//	    crudgrpc.Transport(conn, "Article"))
//
// name is what the far side passed to [HandlerFor.Register], and [ServiceName]
// turns it into the same full service name on both ends, from the same
// function.
//
// There is no generated stub and none is needed. Every method of this binding
// is one google.protobuf.Struct in and one out, so a call is grpc.Invoke with
// the document in it — which is the property [[D-052]] chose the Struct shape
// for, read from the other side.
func Transport(conn grpc.ClientConnInterface, name string, opts ...TransportOption) remote.Transport {
	t := &transport{conn: conn, service: ServiceName(name)}
	for _, o := range opts {
		if o != nil {
			o(t)
		}
	}
	return t
}

// A TransportOption wires one part of a [Transport].
type TransportOption func(*transport)

// WithVocabulary replaces the codes the kind is sharpened through — the same
// value the far side's renderer was given. Without it a service's own code is
// carried through unchanged and only the standard ones refine a status.
func WithVocabulary(c *errs.Codes) TransportOption {
	return func(t *transport) { t.codes = c }
}

// WithCallOptions adds gRPC call options to every call — a per-call credential,
// a compressor, a size limit.
func WithCallOptions(opts ...grpc.CallOption) TransportOption {
	return func(t *transport) { t.call = append(t.call, opts...) }
}

type transport struct {
	conn    grpc.ClientConnInterface
	service string
	codes   *errs.Codes
	call    []grpc.CallOption
}

var standardCodes = sync.OnceValue(errs.StandardCodes)

func (t *transport) vocabulary() *errs.Codes {
	if t.codes != nil {
		return t.codes
	}
	return standardCodes()
}

// Do implements remote.Transport.
func (t *transport) Do(ctx context.Context, call remote.Call) (json.RawMessage, error) {
	in, err := requestFor(call)
	if err != nil {
		return nil, err
	}
	method := "/" + t.service + "/" + string(call.Method)

	out := &structpb.Struct{}
	if err := t.conn.Invoke(ctx, method, in, out, t.call...); err != nil {
		return nil, t.fault(call.Method, method, err)
	}
	raw, err := protojson.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("crudgrpc: reading the answer from %s: %w", method, err)
	}
	return raw, nil
}

// requestFor builds the document each method reads, field for field with the
// handler that reads it. The names are read in one place on each side and
// nowhere else.
func requestFor(call remote.Call) (*structpb.Struct, error) {
	switch call.Method {
	case remote.MethodGet, remote.MethodUpdate, remote.MethodReplace, remote.MethodDelete:
		if call.ID == "" {
			return nil, fmt.Errorf("crudgrpc: %s requires a non-empty id", call.Method)
		}
	}
	switch call.Method {
	case remote.MethodList, remote.MethodCount:
		return docOf(call.Query, "the query document")

	case remote.MethodGet:
		return document(func(f map[string]*structpb.Value) error {
			f["id"] = structpb.NewStringValue(call.ID)
			return nest(f, "query", call.Query)
		})

	case remote.MethodCreate:
		return rawDoc(call.Body, "the entity")

	case remote.MethodUpdate:
		if err := requireMutationBody(call.Method, call.Body); err != nil {
			return nil, err
		}
		return document(func(f map[string]*structpb.Value) error {
			f["id"] = structpb.NewStringValue(call.ID)
			return nestRaw(f, "patch", call.Body)
		})

	case remote.MethodReplace:
		if err := requireMutationBody(call.Method, call.Body); err != nil {
			return nil, err
		}
		return document(func(f map[string]*structpb.Value) error {
			f["id"] = structpb.NewStringValue(call.ID)
			return nestRaw(f, "entity", call.Body)
		})

	case remote.MethodDelete:
		return document(func(f map[string]*structpb.Value) error {
			f["id"] = structpb.NewStringValue(call.ID)
			return nil
		})

	case remote.MethodBulkDelete:
		ids, err := exactBulkIDs(call.IDs)
		if err != nil {
			return nil, err
		}
		return rawDoc(json.RawMessage(`{"ids":`+string(ids)+`}`), "the keys")
	}
	return nil, fmt.Errorf("crudgrpc: no method for %s", call.Method)
}

func requireMutationBody(method remote.Method, body json.RawMessage) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("crudgrpc: %s requires a non-null body", method)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return fmt.Errorf("crudgrpc: %s body must be a JSON object: %w", method, err)
	}
	return nil
}

// The key goes out as a string on every route that names one. google.protobuf
// .Value has no integer, so the API does not send an integral key at magnitude
// 2^53 and beyond as a number. idOf and idsOf on the far side read either spelling and the string
// one is the half that is always exact.

// exactBulkIDs turns numeric array members into decimal strings before the
// Struct encoder sees them. Call.IDs stays JSON so HTTP keeps its ordinary
// array shape, while gRPC gets the same exact key spelling as its entity routes.
func exactBulkIDs(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return json.RawMessage("null"), nil
	}
	var ids []json.RawMessage
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("crudgrpc: encoding the keys: %w", err)
	}
	for i, rawID := range ids {
		trimmed := bytes.TrimSpace(rawID)
		if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
			continue
		}
		var number json.Number
		if err := json.Unmarshal(trimmed, &number); err != nil {
			return nil, fmt.Errorf("crudgrpc: encoding key %d: %w", i, err)
		}
		encoded, err := json.Marshal(number.String())
		if err != nil {
			return nil, fmt.Errorf("crudgrpc: encoding key %d: %w", i, err)
		}
		ids[i] = encoded
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("crudgrpc: encoding the keys: %w", err)
	}
	return encoded, nil
}

func document(fill func(map[string]*structpb.Value) error) (*structpb.Struct, error) {
	fields := map[string]*structpb.Value{}
	if err := fill(fields); err != nil {
		return nil, err
	}
	return &structpb.Struct{Fields: fields}, nil
}

// docOf encodes a value as a document, through encoding/json rather than
// structpb's own map conversion — the same route toStruct takes, and for the
// same reason: the json tags decide the shape and crud.Opt keeps its three
// states.
func docOf(v any, what string) (*structpb.Struct, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("crudgrpc: encoding %s: %w", what, err)
	}
	return rawDoc(raw, what)
}

func rawDoc(raw json.RawMessage, what string) (*structpb.Struct, error) {
	st := &structpb.Struct{}
	if len(raw) == 0 || string(raw) == "null" {
		return st, nil
	}
	if err := protojson.Unmarshal(raw, st); err != nil {
		return nil, fmt.Errorf("crudgrpc: encoding %s: %w", what, err)
	}
	return st, nil
}

func nest(f map[string]*structpb.Value, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("crudgrpc: encoding %s: %w", key, err)
	}
	return nestRaw(f, key, raw)
}

func nestRaw(f map[string]*structpb.Value, key string, raw json.RawMessage) error {
	st, err := rawDoc(raw, key)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		// Null is absence for the optional query document. Write routes send an
		// object (including `{}`) so their required body cannot be dropped.
		return nil
	}
	f[key] = structpb.NewStructValue(st)
	return nil
}

// fault turns a failed call into the error a caller branches on.
//
// A status this library did not write is a *remote.ProtocolError and never a
// classified failure: Unimplemented from a method a read-only service never
// registered, Unavailable from a connection that never opened, anything from an
// interceptor in between. What tells them apart is the ErrorInfo detail — the
// [ErrorDomain] is this library's name, and a status without it did not come
// from a renderer here.
func (t *transport) fault(m remote.Method, where string, err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("crudgrpc: calling %s: %w", where, err)
	}

	code := errs.Code("")
	partial := false
	var vs []errs.Violation
	mine := false

	for _, d := range st.Details() {
		switch detail := d.(type) {
		case *errdetails.ErrorInfo:
			if detail.GetDomain() != ErrorDomain {
				continue
			}
			mine = true
			code = errs.Code(detail.GetReason())
			partial = detail.GetMetadata()[PartialKey] == "true"
		case *errdetails.BadRequest:
			for _, fv := range detail.GetFieldViolations() {
				vs = append(vs, errs.Violation{
					// The dotted form, which is what Field was rendered from.
					Path:    errs.ParsePath(fv.GetField()),
					Code:    errs.Code(fv.GetReason()),
					Message: fv.GetDescription(),
				})
			}
		}
	}

	if !mine {
		// An Internal status is the one this library writes with no details at
		// all, so it is the one exception: the silence is the message.
		if st.Code() == codes.Internal {
			return port.FaultFrom(errs.KindInternal, errs.CodeInternal, nil, false)
		}
		return &remote.ProtocolError{
			Method: m,
			Where:  where,
			Status: st.Code().String(),
			Body:   remote.Truncate(st.Message(), 200),
		}
	}

	return port.FaultFrom(t.kindOf(st.Code(), code), code, vs, partial)
}

// kindOf answers the class, sharpened by the code where the status word cannot
// tell two apart.
//
// InvalidArgument is the only such word: [CodeFor] sends a validation failure
// and a malformed request to the same code, and HTTP tells them apart with 422
// and 400. The vocabulary resolves the one the sender meant. A code it does not
// declare contributes nothing, which is the same rule port.KindOfWith follows
// on the way out — a service that declared a code and forgot to wire it must
// not have its 422 turned into something else by the omission.
func (t *transport) kindOf(c codes.Code, code errs.Code) errs.Kind {
	kind := KindForCode(c)
	if c != codes.InvalidArgument {
		return kind
	}
	if refined, ok := t.vocabulary().KindOf(code); ok {
		return refined
	}
	return kind
}

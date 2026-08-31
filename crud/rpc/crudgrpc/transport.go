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

func Transport(conn grpc.ClientConnInterface, name string, options ...TransportOption) remote.Transport {
	t := &transport{conn: conn, service: ServiceName(name)}
	for _, o := range options {
		if o != nil {
			o(t)
		}
	}
	return t
}

type TransportOption func(*transport)

func WithVocabulary(c *errs.Codes) TransportOption {
	return func(t *transport) { t.codes = c }
}

func WithCallOptions(options ...grpc.CallOption) TransportOption {
	return func(t *transport) { t.call = append(t.call, options...) }
}

type transport struct {
	conn    grpc.ClientConnInterface
	service string
	codes   *errs.Codes
	call    []grpc.CallOption
}

var standardCodes = sync.OnceValue(errs.StandardCodes)

func (this *transport) vocabulary() *errs.Codes {
	if this.codes != nil {
		return this.codes
	}
	return standardCodes()
}

func (this *transport) Do(ctx context.Context, call *remote.Call) (json.RawMessage, error) {
	if call == nil {
		return nil, fmt.Errorf("crudgrpc: call is nil")
	}
	in, err := requestFor(call)
	if err != nil {
		return nil, err
	}
	method := "/" + this.service + "/" + string(call.Method)

	out := &structpb.Struct{}
	if err := this.conn.Invoke(ctx, method, in, out, this.call...); err != nil {
		return nil, this.fault(call.Method, method, err)
	}
	raw, err := protojson.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("crudgrpc: reading the answer from %s: %w", method, err)
	}
	return raw, nil
}

func requestFor(call *remote.Call) (*structpb.Struct, error) {
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
		return nil
	}
	f[key] = structpb.NewStructValue(st)
	return nil
}

func (this *transport) fault(m remote.Method, where string, err error) error {
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
					Path:    errs.ParsePath(fv.GetField()),
					Code:    errs.Code(fv.GetReason()),
					Message: fv.GetDescription(),
				})
			}
		}
	}

	if !mine {
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

	return port.FaultFrom(this.kindOf(st.Code(), code), code, vs, partial)
}

func (this *transport) kindOf(c codes.Code, code errs.Code) errs.Kind {
	kind := KindForCode(c)
	if c != codes.InvalidArgument {
		return kind
	}
	if refined, ok := this.vocabulary().KindOf(code); ok {
		return refined
	}
	return kind
}

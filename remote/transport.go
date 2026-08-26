package remote

import (
	"context"
	"encoding/json"

	"github.com/frostgrove/vv/crud/query"
)

// A Method is one call a resource answers.
//
// The spellings are crud/rpc/crudgrpc's registered method names, so that transport
// builds its full method out of one directly. An HTTP transport maps them to
// verbs and paths, which is the mapping that has to exist somewhere and exists
// once.
type Method string

const (
	MethodList       Method = "List"
	MethodCount      Method = "Count"
	MethodGet        Method = "Get"
	MethodCreate     Method = "Create"
	MethodUpdate     Method = "Update"
	MethodReplace    Method = "Replace"
	MethodDelete     Method = "Delete"
	MethodBulkDelete Method = "BulkDelete"
)

// A Call is one invocation, carrying what the caller meant and nothing about
// how it will travel: no URL, no header, no connection.
//
// It is the mirror of the commands in port. Those are what a transport hands a
// service on the way in; this is what a resource hands a transport on the way
// out, and the two are deliberately the same shape of thing.
type Call struct {
	Method Method
	// ID is the key, already text. See port.FormatID.
	ID string
	// IDs is the set a bulk delete names, in the order the caller gave them,
	// already encoded as a JSON array.
	//
	// JSON and not text, which the single ID beside it is. That one goes into a
	// URL path and a path is text; these go into a document the far side
	// decodes into its own key type, so an int64 key sent as "42" arrives as a
	// string where a number was expected and the whole request is refused.
	IDs json.RawMessage
	// Query is the narrowing, for the three reads that take one.
	Query *query.Request
	// Body is the entity or the patch, already JSON. It is raw rather than a
	// value because encoding it is the resource's job — that is where crud.Opt
	// keeps its three states — and re-encoding it in the transport would
	// collapse absent and null the first time it passed through a Go nil.
	Body json.RawMessage
}

// A Transport carries a [Call] to a service and brings back the document it
// answered.
//
// A failed call must come back as a *errs.Fault built with port.FaultFrom, so
// the caller's errors.Is branch works whichever transport is underneath. That
// is the one thing an implementation owes beyond moving bytes, and it is why
// each transport in this library sits beside the binding it calls: the table
// that turns a status or a code into a kind is the inverse of the one that
// produced it, and the two cannot drift while they share a file.
type Transport interface {
	Do(ctx context.Context, call Call) (json.RawMessage, error)
}

// A ProtocolError is an answer that did not come from this library.
//
// A wrong base URL, a proxy that refused the request, a gateway that answered
// its own page, a method a read-only service never registered. Every one of
// those arrives wearing a status that means something else — a router's 404 is
// the case that matters, because a client reading the status alone would report
// it as crud.ErrNotFound and turn a misconfiguration into "the row is not
// there", permanently and with nothing in the response to contradict it.
//
// It carries no fault, which is what a caller wants: port.KindOf reads an
// unrecognised error as internal, so a gateway re-rendering it answers 500 with
// a body that says nothing, and a caller's errors.Is branches all stay false.
type ProtocolError struct {
	Method Method
	// Where is the address the call went to — a URL, or a full gRPC method.
	Where string
	// Status is the transport's own word for what came back.
	Status string
	// Body is what arrived, truncated. It is here because a misconfigured base
	// URL is otherwise indistinguishable from an empty table, and it never
	// reaches a response: nothing renders a ProtocolError.
	Body string
}

func (e *ProtocolError) Error() string {
	s := "remote: " + string(e.Method) + " " + e.Where + " answered " + e.Status +
		", which is not this library speaking"
	if e.Body != "" {
		s += ": " + e.Body
	}
	return s
}

// Truncate cuts a body down to what is worth putting in an error message.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

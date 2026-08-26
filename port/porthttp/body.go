package porthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

// DecodeJSON reads a JSON body onto v, bounded by [MaxBody]. An empty body is
// not an error: POST /count and POST /query both mean "no narrowing" when sent
// with no body.
//
// It decodes with encoding/json rather than a framework's binder because the
// binders validate: a `binding:"required"` tag on the consumer's model would
// start changing what the routes accept, and only under one transport.
func DecodeJSON(r io.Reader, v any) error {
	if r == nil {
		return nil
	}
	_, err := DecodeJSONKeep(r, v)
	return err
}

// MaxBody is how many bytes of request body a transport reads before refusing
// with [TooLarge]. Past it nothing is parsed and nothing is allocated.
//
// It is a cap and not a suggestion, and it exists because the obvious version
// had no cap at all: io.ReadAll on a body nobody bounded is one request holding
// as much memory as a client cares to send, and neither net/http nor Gin brings
// a limit of its own. Fiber does — 4 MiB — so the number here is Fiber's, and
// the three bindings refuse the same request at the same size rather than two
// of them accepting what the third rejects ([[FL-013]]).
//
// A resource that genuinely takes more says so per handler with the binding's
// MaxBody option; there is no way to say "unbounded", because that is the state
// this constant exists to end.
const MaxBody = 4 << 20

// MaxKeptBody caps the copy DecodeJSONKeep retains. Past it nothing is kept:
// the raw-body fallback then declines and the path is marked approximate, which
// is the honest outcome. Holding a megabyte of request per in-flight write so a
// hypothetical error can name a field is the wrong trade.
const MaxKeptBody = 64 << 10

// DecodeJSONKeep decodes like [DecodeJSON] and hands back the bytes it decoded,
// for the raw-body path fallback ([[D-043]]).
//
// The bytes are the caller's to keep: io.ReadAll allocates a fresh slice, so
// nothing here aliases a framework buffer. A binding whose framework owns the
// buffer — Fiber, whose c.Body() is documented valid only within the handler —
// uses [KeepBody] instead.
func DecodeJSONKeep(r io.Reader, v any) ([]byte, error) {
	return DecodeJSONKeepLimit(r, v, MaxBody)
}

// DecodeJSONKeepLimit is [DecodeJSONKeep] with the cap named, for a handler
// whose resource genuinely takes more — or less — than [MaxBody]. A limit of
// zero or less means [MaxBody]; there is no spelling for "unbounded".
//
// It reads one byte past the limit rather than the limit itself. io.LimitReader
// signals a full buffer and an exhausted reader identically, so a body of
// exactly the cap and a body of twice it would otherwise be indistinguishable —
// and the honest answer to the first is that it fits.
func DecodeJSONKeepLimit(r io.Reader, v any, limit int) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = MaxBody
	}
	body, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, MalformedBody(err)
	}
	if len(body) > limit {
		return nil, TooLarge(limit)
	}
	if len(body) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return KeepBody(body), MalformedBody(err)
	}
	return KeepBody(body), nil
}

// KeepBody returns a copy of b that outlives the handler, or nil when b is over
// [MaxKeptBody].
//
// A copy and not a reference, and the roadmap has to say so because the obvious
// version is a use-after-free: Fiber documents c.Body() as valid only within
// the handler and crudfiber builds its app with a plain fiber.New(), so
// Immutable is off. A stored reference surfaces as a corrupted field path under
// load, which is the worst way for this to fail.
func KeepBody(b []byte) []byte {
	if len(b) == 0 || len(b) > MaxKeptBody {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// MalformedBody marks a body that would not decode. It is here rather than in
// each binding so every transport agrees on the code as well as on the status
// ([[D-045]], [[D-059]]).
//
// **The decoder's own words never reach the client.** encoding/json's
// UnmarshalTypeError reads "cannot unmarshal string into Go struct field
// WidgetUpdate.price of type crud.Opt[int64]" — the Go type of the consumer's
// DTO and the Go type of the field, package path included. That is precisely
// what [[D-044]] forbids a response body from carrying, and it was reaching
// clients on every transport because BadRequestAs renders the message it is
// given ([[D-039]]: no path and no classification comes from a library's message
// text either).
//
// What survives is what the client itself sent: the JSON key it got wrong, and a
// word for the kind of value the field wanted. A syntax error keeps its byte
// offset, which is a fact about the client's own bytes.
func MalformedBody(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		// e.Field is the JSON path the client sent — its own words. e.Struct and
		// e.Type are ours and stay here.
		path := errs.Path(nil)
		if typeErr.Field != "" {
			path = errs.ParsePath(typeErr.Field)
		}
		return port.BadRequestAs(errs.CodeMalformedBody, path,
			"expected %s", wanted(typeErr.Type))
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return port.BadRequestAs(errs.CodeMalformedBody, nil,
			"the body is not valid JSON, at byte %d", syntaxErr.Offset)
	}
	// Anything else passes through. The rule is narrow on purpose: only
	// encoding/json's *structural* errors name Go types, and everything else
	// that reaches here is text somebody audited. query.Request's own
	// UnmarshalJSON refuses an option the document does not define and names the
	// key the client sent — which [[D-013]] requires it to do — and a consumer's
	// own UnmarshalJSON is that consumer's words about their own document.
	// Swallowing those would trade one disclosure bug for a refusal that says
	// nothing.
	return port.BadRequestAs(errs.CodeMalformedBody, nil, "%s", err)
}

// wanted names the shape a field expected, in the vocabulary a client speaks:
// what JSON calls it, never what Go calls it.
func wanted(t reflect.Type) string {
	if t == nil {
		return "a different value"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.String:
		return "a string"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map, reflect.Struct:
		return "an object"
	default:
		return "a different value"
	}
}

// TooLarge marks a body past the cap. It names the limit and nothing else: the
// number is the client's to act on, and there is nothing else here that a
// client could not have worked out from the bytes it sent.
//
// The kind is [errs.KindTooLarge] rather than a bad-request code, so the status
// is 413 on HTTP and ResourceExhausted on gRPC — "send less", which is a
// different instruction from "send something else".
func TooLarge(limit int) error {
	return errs.TooLarge().
		Code(errs.CodeTooLarge).
		Message(fmt.Sprintf("the request body is larger than the %d bytes this endpoint reads", limit)).
		Wrapping(port.ErrBadRequest).Fault()
}

type ctxKey int

const bodyKey ctxKey = iota

// WithBody carries the retained request body to the renderer. One context key
// for every binding, so the fallback works the same whichever framework
// retained the bytes.
func WithBody(ctx context.Context, body []byte) context.Context {
	if len(body) == 0 {
		return ctx
	}
	return context.WithValue(ctx, bodyKey, body)
}

// BodyFrom answers the retained body, or nil.
func BodyFrom(ctx context.Context) []byte {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(bodyKey).([]byte)
	return b
}

// WithLocale carries the request's language to the renderer. The compatibility
// hop over port.WithLocale.
//
// A forwarder and not a second key. The key is port's because a gRPC renderer
// reads the same one, and two keys would each be invisible to the other: both
// packages' tests would pass and the catalogue would answer in the default
// locale on one protocol.
func WithLocale(ctx context.Context, locale string) context.Context {
	return port.WithLocale(ctx, locale)
}

// LocaleFrom answers the request's language, or "". The compatibility hop over
// port.LocaleFrom.
func LocaleFrom(ctx context.Context) string { return port.LocaleFrom(ctx) }

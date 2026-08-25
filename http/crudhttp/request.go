package crudhttp

import (
	"context"
	"encoding/json"
	"io"
	"reflect"

	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/query"
)

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] struct {
	IDs []ID `json:"ids"`
}

// CoerceID converts a path parameter to the repository's key type, which is why
// a uuid or a slug key works in a URL with no extra code.
func CoerceID[ID comparable](raw string) (ID, error) {
	var zero ID
	if raw == "" {
		return zero, BadRequestAs(errs.CodeInvalidID, nil, "missing id")
	}
	v, err := query.Coerce(raw, reflect.TypeOf(zero))
	if err != nil {
		return zero, BadRequestAs(errs.CodeInvalidID, nil, "%q is not a valid id", raw)
	}
	id, ok := v.(ID)
	if !ok {
		return zero, BadRequestAs(errs.CodeInvalidID, nil, "%q is not a valid id", raw)
	}
	return id, nil
}

// NarrowForCount drops everything that means nothing to a COUNT. Leaving paging
// in would make the answer the size of one page rather than of the result.
func NarrowForCount(req *query.Request) {
	req.Page, req.Limit, req.Offset = 0, 0, 0
	req.Sort, req.Preload, req.Select = nil, nil, nil
}

// NarrowForEntity keeps only the shaping options. A single entity is addressed
// by its key, so a filter or a sort on the way to it is meaningless, and paging
// it would be a way to ask for row two of one row.
func NarrowForEntity(req *query.Request) {
	req.Filter, req.Terms, req.Search, req.Sort = query.Filter{}, nil, "", nil
	req.Page, req.Limit, req.Offset = 0, 0, 0
}

// DecodeJSON reads a JSON body onto v. An empty body is not an error: POST
// /count and POST /query both mean "no narrowing" when sent with no body.
//
// It decodes with encoding/json rather than a framework's binder because the
// binders validate: a `binding:"required"` tag on the consumer's model would
// start changing what the CRUD routes accept, and only under one transport.
func DecodeJSON(r io.Reader, v any) error {
	if r == nil {
		return nil
	}
	_, err := DecodeJSONKeep(r, v)
	return err
}

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
	if r == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, MalformedBody(err)
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
// each binding so the three agree on the code as well as on the status
// ([[D-034]]).
func MalformedBody(err error) error {
	return BadRequestAs(errs.CodeMalformedBody, nil, "%s", err)
}

type ctxKey int

const (
	bodyKey ctxKey = iota
	localeKey
)

// WithBody carries the retained request body to the renderer. One context key
// for all three bindings, so the fallback works the same whichever framework
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

// WithLocale carries the request's language to the renderer. The locale is a
// rendering parameter and never a field on the fault: a fault crossing a queue
// must not carry the locale of the request that made it.
func WithLocale(ctx context.Context, locale string) context.Context {
	if locale == "" {
		return ctx
	}
	return context.WithValue(ctx, localeKey, locale)
}

// LocaleFrom answers the request's language, or "".
func LocaleFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(localeKey).(string)
	return s
}

package crudnet

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/http/crudhttp"
	"github.com/shardit-io/vv/query"
)

type options[M any, ID comparable, U any] struct {
	query         *query.Config
	renderer      crudhttp.Renderer
	errorHandler  func(http.ResponseWriter, *http.Request, error)
	transform     func(*http.Request, M) any
	scope         func(*http.Request) ([]crud.Option, error)
	beforeSave    func(*http.Request, *M) error
	beforeUpdate  func(*http.Request, ID, *U) error
	readOnly      bool
	allowClientID bool
	maxBulk       int
}

// Option configures a handler. Type parameters are inferred from New's
// repository argument, so options never need explicit generics at the call site
// when written inline.
type Option[M any, ID comparable, U any] func(*options[M, ID, U])

// WithQuery bounds what clients may filter, sort, select and preload.
func WithQuery[M any, ID comparable, U any](cfg *query.Config) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.query = cfg }
}

// WithErrorHandler replaces the error-to-response mapping. Reuse Status rather
// than reimplementing the table, or the two will drift.
func WithErrorHandler[M any, ID comparable, U any](fn func(http.ResponseWriter, *http.Request, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.errorHandler = fn }
}

// WithRenderer replaces the body every failed request answers with — the seam
// for RFC 9457, or for a shape a client already speaks. The status table stays
// shared either way ([[D-045]]).
func WithRenderer[M any, ID comparable, U any](r crudhttp.Renderer) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.renderer = r }
}

// WithTransform renders each entity through a presenter — the place to hide
// columns the API should not expose.
func WithTransform[M any, ID comparable, U any](fn func(*http.Request, M) any) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.transform = fn }
}

// WithScope adds repository options to every read, derived from the request.
// The security decorator is the better place for row-level rules; this is for
// things that are genuinely transport-shaped, like an ?includeArchived flag.
//
// Reads only, and that is not a gap waiting to be filled: Save and Delete take
// no options, so there is nowhere for a predicate to go. The asymmetry is worth
// saying out loud, because it looks like protection and is not — with a scope
// of TenantID = 7, GET /{id} on somebody else's row is 404 while DELETE /{id}
// on the same row answers 200. Row-level rules on writes belong in
// security.Gate, whose scope really does reach the DELETE and the UPDATE.
func WithScope[M any, ID comparable, U any](fn func(*http.Request) ([]crud.Option, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.scope = fn }
}

// BeforeSave runs on create and replace, after binding and sanitising.
func BeforeSave[M any, ID comparable, U any](fn func(*http.Request, *M) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeSave = fn }
}

// BeforeUpdate runs on PATCH, after binding the DTO.
func BeforeUpdate[M any, ID comparable, U any](fn func(*http.Request, ID, *U) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeUpdate = fn }
}

// ReadOnly mounts only the read routes.
func ReadOnly[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.readOnly = true }
}

// AllowClientID lets a create request carry its own primary key even when the
// database would generate one.
func AllowClientID[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.allowClientID = true }
}

// MaxBulk caps how many ids one bulk delete may carry.
func MaxBulk[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.maxBulk = n }
}

// ---------------------------------------------------------------------------
// errors

// Envelope is the JSON shape of a failed request.
type Envelope = crudhttp.Envelope

// Renderer is the seam WithRenderer replaces.
type Renderer = crudhttp.Renderer

// defaultRenderer is what a handler and the middleware fall back to. One value,
// built once: a Renderer holds a vocabulary and a catalogue and nothing
// per-request, so sharing it is what makes the zero-config case free.
var defaultRenderer = crudhttp.NewRenderer()

// Status maps a repository or query error to an HTTP status code. Everything it
// recognises is a client mistake or an access decision; anything else is 500.
//
// It is exported so an application writing its own error bodies gets the same
// statuses without reimplementing the table.
func Status(err error) int { return crudhttp.Status(err) }

// DefaultErrorHandler writes the mapped status and the error envelope. A 500
// deliberately says nothing: the underlying message could be a SQL error.
func DefaultErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	render(defaultRenderer, w, r, err)
}

// render is the one place a failure leaves this package, whichever renderer
// produced it.
func render(rd crudhttp.Renderer, w http.ResponseWriter, r *http.Request, err error) {
	ctx := context.Background()
	if r != nil {
		// The locale is a rendering parameter, read here rather than carried on
		// the fault: a fault crossing a queue must not carry the locale of the
		// request that made it. First tag only — q-values pick between
		// translations we do not have.
		ctx = crudhttp.WithLocale(r.Context(), firstTag(r.Header.Get("Accept-Language")))
	}
	status, header, body := rd.Render(ctx, err)
	for k, vs := range header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if body == nil {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, body)
}

// firstTag reads the first language tag out of an Accept-Language header.
func firstTag(h string) string {
	if i := strings.IndexAny(h, ",;"); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSpace(h)
}

// writeJSON is the one place a response leaves this package.
//
// It marshals before touching the header for two reasons. A value that cannot
// be encoded — a presenter returning a channel, a NaN — would otherwise leave a
// half-written 200 with no way back, because WriteHeader after a write is
// ignored; deciding here makes it a 500 that says nothing, like any other
// server fault. And json.Encoder appends a newline, which would make this
// binding's bytes differ from the other two for no reason a caller could want.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("crudnet: encoding the response: %v", err)
		status = http.StatusInternalServerError
		body, _ = json.Marshal(crudhttp.Internal())
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

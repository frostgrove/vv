package crudgrpc

import (
	"context"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/query"
	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/port"
)

type options[M any, ID comparable, U any] struct {
	query         *query.Config
	renderer      Renderer
	transform     func(context.Context, M) any
	scope         func(context.Context) ([]crud.Option, error)
	beforeSave    func(context.Context, *M) error
	beforeUpdate  func(context.Context, ID, *U) error
	readOnly      bool
	allowClientID bool
	maxBulk       int
}

// Option configures a handler. Type parameters are inferred from New's
// repository argument, so options never need explicit generics at the call site
// when written inline.
//
// Three parameters and not four. Nothing an option sets mentions the input
// type, so a handler with one of its own takes the same options as any other
// ([[D-045]]).
//
// Every transport-shaped signature takes a context.Context where the three HTTP
// bindings take a request. A gRPC call has no request object: what a hook or a
// scope reads — the peer, the metadata, whatever an earlier interceptor put
// there — all hangs off the context.
type Option[M any, ID comparable, U any] func(*options[M, ID, U])

// collect applies the options once, so the four constructors read the same
// configuration.
func collect[M any, ID comparable, U any](opts []Option[M, ID, U]) options[M, ID, U] {
	var o options[M, ID, U]
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// service translates the options that are about rules rather than about
// transport into the ones the default service takes.
func (o options[M, ID, U]) service() []port.ServiceOption {
	var out []port.ServiceOption
	if o.query != nil {
		out = append(out, port.WithQuery(o.query))
	}
	if o.allowClientID {
		out = append(out, port.AllowClientID())
	}
	return out
}

// refuseServiceOptions panics when a service-shaped option is handed to a
// constructor that was given a finished service.
//
// A panic and not a silent no-op, named after the option so the message is the
// fix. Serving means the rules are the service's; an ignored WithQuery would
// leave an API accepting everything while its author believed it was bounded,
// and that is exactly the failure [[D-021]] says must happen at start-up.
func (o options[M, ID, U]) refuseServiceOptions(who string) {
	switch {
	case o.query != nil:
		panic(who + ": WithQuery configures the service, which is already built — pass port.WithQuery to it instead")
	case o.allowClientID:
		panic(who + ": AllowClientID configures the service, which is already built — pass port.AllowClientID to it instead")
	}
}

// WithQuery bounds what clients may filter, sort, select and preload.
func WithQuery[M any, ID comparable, U any](cfg *query.Config) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.query = cfg }
}

// WithRenderer replaces the status every failed call answers with. The code
// table stays shared either way ([[D-045]]).
func WithRenderer[M any, ID comparable, U any](r Renderer) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.renderer = r }
}

// WithTransform renders each entity through a presenter — the place to hide
// columns the API should not expose.
func WithTransform[M any, ID comparable, U any](fn func(context.Context, M) any) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.transform = fn }
}

// WithScope adds repository options to every read, derived from the call. The
// security decorator is the better place for row-level rules; this is for
// things that are genuinely transport-shaped, like a metadata flag.
//
// Reads only, and that is not a gap waiting to be filled: Save and Delete take
// no options, so there is nowhere for a predicate to go. The asymmetry is worth
// saying out loud, because it looks like protection and is not — with a scope
// of TenantID = 7, Get on somebody else's row is NotFound while Delete on the
// same row succeeds. Row-level rules on writes belong in security.Gate, whose
// scope really does reach the DELETE and the UPDATE.
func WithScope[M any, ID comparable, U any](fn func(context.Context) ([]crud.Option, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.scope = fn }
}

// BeforeSave runs on create and replace, after decoding and sanitising.
func BeforeSave[M any, ID comparable, U any](fn func(context.Context, *M) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeSave = fn }
}

// BeforeUpdate runs on Update, after decoding the DTO.
func BeforeUpdate[M any, ID comparable, U any](fn func(context.Context, ID, *U) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeUpdate = fn }
}

// ReadOnly registers only the read methods. The five writes are then not
// registered at all, so a client calling one is answered Unimplemented by gRPC
// itself.
func ReadOnly[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.readOnly = true }
}

// AllowClientID lets a create request carry its own primary key even when the
// database would generate one.
func AllowClientID[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.allowClientID = true }
}

// MaxBulk caps how many ids one BulkDelete may carry.
func MaxBulk[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.maxBulk = n }
}

// ---------------------------------------------------------------------------
// errors

// defaultRenderer is what a handler and the interceptor fall back to. One
// value, built once: a Renderer holds a vocabulary and a catalogue and nothing
// per-call, so sharing it is what makes the zero-config case free.
var defaultRenderer = NewRenderer()

// rendererFor answers the renderer a handler with these path hops needs. No
// hops is the ordinary case and keeps the shared value; a service or a mapper
// that declares one gets a renderer of its own ([[D-043]]).
func rendererFor(hops []errs.Resolver) Renderer {
	if len(hops) == 0 {
		return defaultRenderer
	}
	return NewRenderer(WithResolvers(hops...))
}

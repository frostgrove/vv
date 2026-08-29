package crudgrpc

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type options[M any, ID comparable, U any] struct {
	port.Rules
	renderer     Renderer
	transform    func(context.Context, M) any
	scope        func(context.Context) ([]crud.Option, error)
	beforeSave   func(context.Context, *M) error
	beforeUpdate func(context.Context, ID, *U) error
}

// Option configures a handler.
//
// New infers all three type parameters from its repository argument, so the
// constructor is written without them. An option is not: Go infers a function's
// type arguments from its own arguments, and nothing in `WithQuery(cfg)`
// mentions M, ID or U. Every option spells all three.
//
//	crudgrpc.New(articles,
//	    crudgrpc.WithQuery[Article, int64, ArticleUpdate](cfg),
//	    crudgrpc.MaxBulk[Article, int64, ArticleUpdate](100),
//	)
//
// One local helper per resource is what makes that bearable:
//
//	type articleOpt = crudgrpc.Option[Article, int64, ArticleUpdate]
//
//	func articleQuery(cfg *query.Config) articleOpt {
//	    return crudgrpc.WithQuery[Article, int64, ArticleUpdate](cfg)
//	}
//
// The alias alone does not help — it names the result type, which is not where
// the inference is stuck — so the helper is a function, not a name.
//
// Three parameters and not four. Nothing an option sets mentions the input
// type, so a handler with one of its own takes the same options as any other,
// and no existing call site has to be touched ([[D-045]]).
type Option[M any, ID comparable, U any] func(*options[M, ID, U])

// collect applies the options once, so the four constructors read the same
// configuration.
func collect[M any, ID comparable, U any](optionList []Option[M, ID, U]) options[M, ID, U] {
	var o options[M, ID, U]
	for _, fn := range optionList {
		fn(&o)
	}
	return o
}

// WithQuery bounds what clients may filter, sort, select and preload.
func WithQuery[M any, ID comparable, U any](config *query.Config) Option[M, ID, U] {
	return func(o *options[M, ID, U]) {
		o.Query, o.QueryVariants, o.QuerySelector = config, nil, nil
	}
}

// WithQueryFor selects one declared query vocabulary per request. The selector
// normally reads a principal from context; an undeclared non-empty name is a
// query refusal rather than a permissive fallback.
func WithQueryFor[M any, ID comparable, U any](defaultConfig *query.Config, variants map[string]*query.Config, selectConfig port.QuerySelector) Option[M, ID, U] {
	return func(o *options[M, ID, U]) {
		o.Query, o.QueryVariants, o.QuerySelector = defaultConfig, variants, selectConfig
	}
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
	return func(o *options[M, ID, U]) { o.ReadOnly = true }
}

// AllowClientID lets a create request carry its own primary key even when the
// database would generate one.
func AllowClientID[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.AllowClientID = true }
}

// MaxBulk caps how many ids one BulkDelete may carry.
func MaxBulk[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.MaxBulk = n }
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

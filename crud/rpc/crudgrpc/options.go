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

type Option[M any, ID comparable, U any] func(*options[M, ID, U])

func collect[M any, ID comparable, U any](optionList []Option[M, ID, U]) options[M, ID, U] {
	var o options[M, ID, U]
	for _, fn := range optionList {
		fn(&o)
	}
	return o
}

func WithQuery[M any, ID comparable, U any](config *query.Config) Option[M, ID, U] {
	return func(o *options[M, ID, U]) {
		o.Query, o.QueryVariants, o.QuerySelector = config, nil, nil
	}
}

func WithQueryFor[M any, ID comparable, U any](defaultConfig *query.Config, variants map[string]*query.Config, selectConfig port.QuerySelector) Option[M, ID, U] {
	return func(o *options[M, ID, U]) {
		o.Query, o.QueryVariants, o.QuerySelector = defaultConfig, variants, selectConfig
	}
}

func WithRenderer[M any, ID comparable, U any](r Renderer) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.renderer = r }
}

func WithTransform[M any, ID comparable, U any](fn func(context.Context, M) any) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.transform = fn }
}

func WithScope[M any, ID comparable, U any](fn func(context.Context) ([]crud.Option, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.scope = fn }
}

func BeforeSave[M any, ID comparable, U any](fn func(context.Context, *M) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeSave = fn }
}

func BeforeUpdate[M any, ID comparable, U any](fn func(context.Context, ID, *U) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeUpdate = fn }
}

func ReadOnly[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.ReadOnly = true }
}

func Exposing[M any, ID comparable, U any](operations port.Operations) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.Expose = operations }
}

func AllowClientID[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.AllowClientID = true }
}

func MaxBulk[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.MaxBulk = n }
}

var defaultRenderer = NewRenderer()

func rendererFor(hops []errs.Resolver) Renderer {
	if len(hops) == 0 {
		return defaultRenderer
	}
	return NewRenderer(WithResolvers(hops...))
}

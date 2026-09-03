package crudnet

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type options[M any, ID comparable, U any] struct {
	crudhttp.Rules
	renderer     crudhttp.Renderer
	errorHandler func(http.ResponseWriter, *http.Request, error)
	transform    func(*http.Request, M) any
	scope        func(*http.Request) ([]crud.Option, error)
	beforeSave   func(*http.Request, *M) error
	beforeUpdate func(*http.Request, ID, *U) error
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

func WithErrorHandler[M any, ID comparable, U any](fn func(http.ResponseWriter, *http.Request, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.errorHandler = fn }
}

func WithRenderer[M any, ID comparable, U any](r crudhttp.Renderer) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.renderer = r }
}

func WithTransform[M any, ID comparable, U any](fn func(*http.Request, M) any) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.transform = fn }
}

func WithScope[M any, ID comparable, U any](fn func(*http.Request) ([]crud.Option, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.scope = fn }
}

func BeforeSave[M any, ID comparable, U any](fn func(*http.Request, *M) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeSave = fn }
}

func BeforeUpdate[M any, ID comparable, U any](fn func(*http.Request, ID, *U) error) Option[M, ID, U] {
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

func MaxBody[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.MaxBody = n }
}

func MaxBulk[M any, ID comparable, U any](n int) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.MaxBulk = n }
}

type Envelope = crudhttp.Envelope

type Renderer = crudhttp.Renderer

var defaultRenderer = crudhttp.NewRenderer()

func rendererFor(hops []errs.Resolver) crudhttp.Renderer {
	if len(hops) == 0 {
		return defaultRenderer
	}
	return crudhttp.NewRenderer(crudhttp.WithResolvers(hops...))
}

func Status(err error) int { return crudhttp.Status(err) }

func DefaultErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	render(defaultRenderer, w, r, err)
}

func render(rd crudhttp.Renderer, w http.ResponseWriter, r *http.Request, err error) {
	ctx := context.Background()
	if r != nil {
		ctx = crudhttp.WithLocale(r.Context(), crudhttp.AcceptLanguage(r.Header.Get("Accept-Language")))
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
	writeJSON(r.Context(), w, status, body)
}

func writeJSON(ctx context.Context, w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		port.Logger(ctx).Error("crudnet: encoding the response", "err", err)
		status = http.StatusInternalServerError
		body, _ = json.Marshal(crudhttp.Internal())
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

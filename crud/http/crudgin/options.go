package crudgin

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
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
	errorHandler func(*gin.Context, error)
	transform    func(*gin.Context, M) any
	scope        func(*gin.Context) ([]crud.Option, error)
	beforeSave   func(*gin.Context, *M) error
	beforeUpdate func(*gin.Context, ID, *U) error
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

func WithErrorHandler[M any, ID comparable, U any](fn func(*gin.Context, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.errorHandler = fn }
}

func WithRenderer[M any, ID comparable, U any](r crudhttp.Renderer) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.renderer = r }
}

func WithTransform[M any, ID comparable, U any](fn func(*gin.Context, M) any) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.transform = fn }
}

func WithScope[M any, ID comparable, U any](fn func(*gin.Context) ([]crud.Option, error)) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.scope = fn }
}

func BeforeSave[M any, ID comparable, U any](fn func(*gin.Context, *M) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeSave = fn }
}

func BeforeUpdate[M any, ID comparable, U any](fn func(*gin.Context, ID, *U) error) Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.beforeUpdate = fn }
}

func ReadOnly[M any, ID comparable, U any]() Option[M, ID, U] {
	return func(o *options[M, ID, U]) { o.ReadOnly = true }
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

func writeJSON(c *gin.Context, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		port.Logger(c.Request.Context()).Error("crudgin: encoding the response", "err", err)
		status = http.StatusInternalServerError
		body, _ = json.Marshal(crudhttp.Internal())
		c.Abort()
	}
	c.Data(status, "application/json; charset=utf-8", body)
}

func Status(err error) int { return crudhttp.Status(err) }

func DefaultErrorHandler(c *gin.Context, err error) {
	render(defaultRenderer, c, err)
}

func render(rd crudhttp.Renderer, c *gin.Context, err error) {
	if err != nil {
		_ = c.Error(err)
	}
	write(rd, c, err)
}

func write(rd crudhttp.Renderer, c *gin.Context, err error) {
	ctx := crudhttp.WithLocale(c.Request.Context(), crudhttp.AcceptLanguage(c.GetHeader("Accept-Language")))
	status, header, body := rd.Render(ctx, err)
	for k, vs := range header {
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	if body == nil {
		c.AbortWithStatus(status)
		return
	}
	c.AbortWithStatusJSON(status, body)
}

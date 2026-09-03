package crudgin

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/crud/wire"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type Repository[M any, ID comparable, U any] = crudhttp.Repository[M, ID, U]

type Service[M any, ID comparable, U any] = port.Service[M, ID, U]

type Mapper[In, M any] = port.Mapper[In, M]

type PatchMapper[P, U any] = wire.PatchMapper[P, U]

type Presenter[M, R any] = wire.Presenter[M, R]

type ResourceFor[M any, ID comparable, U any, In any, P any, R any] struct {
	service   Service[M, ID, U]
	mapper    Mapper[In, M]
	patcher   PatchMapper[P, U]
	presenter Presenter[M, R]
	opt       options[M, ID, U]
}

type HandlerFor[M any, ID comparable, U any, In any] = ResourceFor[M, ID, U, In, U, M]

type Handler[M any, ID comparable, U any] = ResourceFor[M, ID, U, M, U, M]

func New[M any, ID comparable, U any](repository Repository[M, ID, U], options ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), port.Identity[M](), wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func NewFor[In, M any, ID comparable, U any](repository Repository[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), mapper, wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func Serving[M any, ID comparable, U any](service Service[M, ID, U], options ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(options)
	o.RefuseServiceOptions("crudgin.Serving")
	return build(service, port.Identity[M](), wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudgin.ServingFor")
	return build(service, mapper, wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func NewWire[In, P, R, M any, ID comparable, U any](repository Repository[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], options ...Option[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), mapper, patcher, presenter, o)
}

func ServingWire[In, P, R, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], options ...Option[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o := collect(options)
	o.RefuseServiceOptions("crudgin.ServingWire")
	return build(service, mapper, patcher, presenter, o)
}

func build[M any, ID comparable, U any, In any, P any, R any](service Service[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], o options[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o.RefuseContradictions("crudgin")
	h := &ResourceFor[M, ID, U, In, P, R]{service: service, mapper: mapper, patcher: patcher, presenter: presenter, opt: o}
	if h.opt.errorHandler == nil {
		rd := h.opt.renderer
		if rd == nil {
			rd = rendererFor(port.Hops(service, mapper))
		}
		h.opt.errorHandler = func(c *gin.Context, err error) { render(rd, c, err) }
	}
	return h
}

func (this *ResourceFor[M, ID, U, In, P, R]) Mount(r gin.IRouter, prefix string) {
	this.Register(r.Group(prefix))
}

func (this *ResourceFor[M, ID, U, In, P, R]) Register(r gin.IRoutes) {
	mounted := this.opt.Mounted()
	if mounted.Has(port.OpCreate) {
		r.POST("", this.Create)
	}
	if mounted.Has(port.OpBulkDelete) {
		r.POST("/bulk-delete", this.BulkDelete)
	}
	if mounted.Has(port.OpQuery) {
		r.POST("/query", this.Query)
	}
	if mounted.Has(port.OpCount) {
		r.GET("/count", this.CountGet)
	}
	if mounted.Has(port.OpCountQuery) {
		r.POST("/count", this.CountPost)
	}
	if mounted.Has(port.OpList) {
		r.GET("", this.List)
	}
	if mounted.Has(port.OpGet) {
		r.GET("/:id", this.GetByID)
	}
	if mounted.Has(port.OpUpdate) {
		r.PATCH("/:id", this.Update)
	}
	if mounted.Has(port.OpReplace) {
		r.PUT("/:id", this.Replace)
	}
	if mounted.Has(port.OpDelete) {
		r.DELETE("/:id", this.Delete)
	}
}

func (this *ResourceFor[M, ID, U, In, P, R]) List(c *gin.Context) {
	request, err := this.parseQueryString(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.list(c, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Query(c *gin.Context) {
	request, err := this.parseBody(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.list(c, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) list(c *gin.Context, request *query.Request) {
	scope, err := this.scope(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	page, err := this.service.List(c.Request.Context(), port.ListCommand{Query: request, Options: scope})
	if err != nil {
		this.fail(c, err)
		return
	}
	if this.opt.transform == nil {
		writeJSON(c, http.StatusOK, crud.MapPage(page, this.presenter.Response))
		return
	}
	writeJSON(c, http.StatusOK, crud.MapPage(page, func(m M) any {
		return this.opt.transform(c, m)
	}))
}

func (this *ResourceFor[M, ID, U, In, P, R]) CountGet(c *gin.Context) {
	request, err := this.parseQueryString(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.count(c, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) CountPost(c *gin.Context) {
	request, err := this.parseBody(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.count(c, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) count(c *gin.Context, request *query.Request) {
	scope, err := this.scope(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	n, err := this.service.Count(c.Request.Context(), port.CountCommand{Query: request, Options: scope})
	if err != nil {
		this.fail(c, err)
		return
	}
	writeJSON(c, http.StatusOK, gin.H{"count": n})
}

func (this *ResourceFor[M, ID, U, In, P, R]) GetByID(c *gin.Context) {
	id, err := this.id(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	request, err := this.parseQueryString(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	scope, err := this.scope(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err := this.service.Get(c.Request.Context(), port.GetCommand[ID]{ID: id, Query: request, Options: scope})
	if err != nil {
		this.fail(c, err)
		return
	}
	this.entity(c, http.StatusOK, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Create(c *gin.Context) {
	var in In
	raw, err := this.decode(c.Request.Body, &in)
	keep(c, raw)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err := this.mapper.Model(c.Request.Context(), in)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err = this.service.Create(c.Request.Context(), port.CreateCommand[M]{Model: m, Before: this.beforeSave(c)})
	if err != nil {
		this.fail(c, err)
		return
	}
	this.entity(c, http.StatusCreated, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Update(c *gin.Context) {
	id, err := this.id(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	var patch P
	raw, err := this.decode(c.Request.Body, &patch)
	keep(c, raw)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err := this.service.Update(c.Request.Context(), port.UpdateCommand[ID, U]{ID: id, Patch: this.patcher.Update(patch), Before: this.beforeUpdate(c, id)})
	if err != nil {
		this.fail(c, err)
		return
	}
	this.entity(c, http.StatusOK, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Replace(c *gin.Context) {
	id, err := this.id(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	var in In
	raw, err := this.decode(c.Request.Body, &in)
	keep(c, raw)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err := this.mapper.Model(c.Request.Context(), in)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err = this.service.Replace(c.Request.Context(), port.ReplaceCommand[ID, M]{ID: id, Model: m, Before: this.beforeSave(c)})
	if err != nil {
		this.fail(c, err)
		return
	}
	this.entity(c, http.StatusOK, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Delete(c *gin.Context) {
	id, err := this.id(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	n, err := this.service.Delete(c.Request.Context(), port.DeleteCommand[ID]{ID: id})
	if err != nil {
		this.fail(c, err)
		return
	}
	writeJSON(c, http.StatusOK, gin.H{"deleted": n})
}

type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

func (this *ResourceFor[M, ID, U, In, P, R]) BulkDelete(c *gin.Context) {
	var request BulkDeleteRequest[ID]
	if err := this.decodeOnly(c.Request.Body, &request); err != nil {
		this.fail(c, err)
		return
	}
	if len(request.IDs) > this.opt.BulkCap() {
		this.fail(c, crudhttp.BadRequestAs(errs.CodeBadQuery, nil, "at most %d ids per request", this.opt.BulkCap()))
		return
	}
	n, err := this.service.DeleteMany(c.Request.Context(), port.BulkDeleteCommand[ID]{IDs: request.IDs})
	if err != nil {
		this.fail(c, err)
		return
	}
	writeJSON(c, http.StatusOK, gin.H{"deleted": n})
}

func (this *ResourceFor[M, ID, U, In, P, R]) scope(c *gin.Context) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(c)
}

func (this *ResourceFor[M, ID, U, In, P, R]) beforeSave(c *gin.Context) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(c, m) }
}

func (this *ResourceFor[M, ID, U, In, P, R]) beforeUpdate(c *gin.Context, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(c, id, dataTransferObject) }
}

func (this *ResourceFor[M, ID, U, In, P, R]) parseQueryString(c *gin.Context) (*query.Request, error) {
	return query.ParseQuery(c.Request.URL.Query())
}

func (this *ResourceFor[M, ID, U, In, P, R]) parseBody(c *gin.Context) (*query.Request, error) {
	request := &query.Request{}
	if err := this.decodeOnly(c.Request.Body, request); err != nil {
		return nil, err
	}
	return request, nil
}

func (this *ResourceFor[M, ID, U, In, P, R]) decode(r io.Reader, v any) ([]byte, error) {
	return crudhttp.DecodeJSONKeepLimit(r, v, this.opt.MaxBody)
}

func (this *ResourceFor[M, ID, U, In, P, R]) decodeOnly(r io.Reader, v any) error {
	_, err := this.decode(r, v)
	return err
}

func (this *ResourceFor[M, ID, U, In, P, R]) id(c *gin.Context) (ID, error) {
	return port.CoerceID[ID](c.Param("id"))
}

func (this *ResourceFor[M, ID, U, In, P, R]) entity(c *gin.Context, status int, m M) {
	if this.opt.transform != nil {
		writeJSON(c, status, this.opt.transform(c, m))
		return
	}
	writeJSON(c, status, this.presenter.Response(m))
}

func (this *ResourceFor[M, ID, U, In, P, R]) fail(c *gin.Context, err error) {
	this.opt.errorHandler(c, err)
}

func keep(c *gin.Context, raw []byte) {
	if len(raw) == 0 {
		return
	}
	c.Request = c.Request.WithContext(crudhttp.WithBody(c.Request.Context(), raw))
}

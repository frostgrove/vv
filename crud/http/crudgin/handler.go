package crudgin

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type Repository[M any, ID comparable, U any] = crudhttp.Repository[M, ID, U]

type Service[M any, ID comparable, U any] = port.Service[M, ID, U]

type Mapper[In, M any] = port.Mapper[In, M]

type HandlerFor[M any, ID comparable, U any, In any] struct {
	service Service[M, ID, U]
	mapper  Mapper[In, M]
	opt     options[M, ID, U]
}

type Handler[M any, ID comparable, U any] = HandlerFor[M, ID, U, M]

func New[M any, ID comparable, U any](repository Repository[M, ID, U], options ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), port.Identity[M](), o)
}

func NewFor[In, M any, ID comparable, U any](repository Repository[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), mapper, o)
}

func Serving[M any, ID comparable, U any](service Service[M, ID, U], options ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(options)
	o.RefuseServiceOptions("crudgin.Serving")
	return build(service, port.Identity[M](), o)
}

func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudgin.ServingFor")
	return build(service, mapper, o)
}

func build[M any, ID comparable, U any, In any](service Service[M, ID, U], mapper Mapper[In, M], o options[M, ID, U]) *HandlerFor[M, ID, U, In] {
	h := &HandlerFor[M, ID, U, In]{service: service, mapper: mapper, opt: o}
	if h.opt.errorHandler == nil {
		rd := h.opt.renderer
		if rd == nil {
			rd = rendererFor(port.Hops(service, mapper))
		}
		h.opt.errorHandler = func(c *gin.Context, err error) { render(rd, c, err) }
	}
	return h
}

func (this *HandlerFor[M, ID, U, In]) Mount(r gin.IRouter, prefix string) {
	this.Register(r.Group(prefix))
}

func (this *HandlerFor[M, ID, U, In]) Register(r gin.IRoutes) {
	if !this.opt.ReadOnly {
		r.POST("", this.Create)
		r.POST("/bulk-delete", this.BulkDelete)
	}
	r.POST("/query", this.Query)
	r.GET("/count", this.CountGet)
	r.POST("/count", this.CountPost)
	r.GET("", this.List)
	r.GET("/:id", this.GetByID)
	if !this.opt.ReadOnly {
		r.PATCH("/:id", this.Update)
		r.PUT("/:id", this.Replace)
		r.DELETE("/:id", this.Delete)
	}
}

func (this *HandlerFor[M, ID, U, In]) List(c *gin.Context) {
	request, err := this.parseQueryString(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.list(c, request)
}

func (this *HandlerFor[M, ID, U, In]) Query(c *gin.Context) {
	request, err := this.parseBody(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.list(c, request)
}

func (this *HandlerFor[M, ID, U, In]) list(c *gin.Context, request *query.Request) {
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
		writeJSON(c, http.StatusOK, page)
		return
	}
	writeJSON(c, http.StatusOK, crud.MapPage(page, func(m M) any {
		return this.opt.transform(c, m)
	}))
}

func (this *HandlerFor[M, ID, U, In]) CountGet(c *gin.Context) {
	request, err := this.parseQueryString(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.count(c, request)
}

func (this *HandlerFor[M, ID, U, In]) CountPost(c *gin.Context) {
	request, err := this.parseBody(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	this.count(c, request)
}

func (this *HandlerFor[M, ID, U, In]) count(c *gin.Context, request *query.Request) {
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

func (this *HandlerFor[M, ID, U, In]) GetByID(c *gin.Context) {
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

func (this *HandlerFor[M, ID, U, In]) Create(c *gin.Context) {
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

func (this *HandlerFor[M, ID, U, In]) Update(c *gin.Context) {
	id, err := this.id(c)
	if err != nil {
		this.fail(c, err)
		return
	}
	var dataTransferObject U
	raw, err := this.decode(c.Request.Body, &dataTransferObject)
	keep(c, raw)
	if err != nil {
		this.fail(c, err)
		return
	}
	m, err := this.service.Update(c.Request.Context(), port.UpdateCommand[ID, U]{ID: id, Patch: dataTransferObject, Before: this.beforeUpdate(c, id)})
	if err != nil {
		this.fail(c, err)
		return
	}
	this.entity(c, http.StatusOK, m)
}

func (this *HandlerFor[M, ID, U, In]) Replace(c *gin.Context) {
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

func (this *HandlerFor[M, ID, U, In]) Delete(c *gin.Context) {
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

func (this *HandlerFor[M, ID, U, In]) BulkDelete(c *gin.Context) {
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

func (this *HandlerFor[M, ID, U, In]) scope(c *gin.Context) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(c)
}

func (this *HandlerFor[M, ID, U, In]) beforeSave(c *gin.Context) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(c, m) }
}

func (this *HandlerFor[M, ID, U, In]) beforeUpdate(c *gin.Context, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(c, id, dataTransferObject) }
}

func (this *HandlerFor[M, ID, U, In]) parseQueryString(c *gin.Context) (*query.Request, error) {
	return query.ParseQuery(c.Request.URL.Query())
}

func (this *HandlerFor[M, ID, U, In]) parseBody(c *gin.Context) (*query.Request, error) {
	request := &query.Request{}
	if err := this.decodeOnly(c.Request.Body, request); err != nil {
		return nil, err
	}
	return request, nil
}

func (this *HandlerFor[M, ID, U, In]) decode(r io.Reader, v any) ([]byte, error) {
	return crudhttp.DecodeJSONKeepLimit(r, v, this.opt.MaxBody)
}

func (this *HandlerFor[M, ID, U, In]) decodeOnly(r io.Reader, v any) error {
	_, err := this.decode(r, v)
	return err
}

func (this *HandlerFor[M, ID, U, In]) id(c *gin.Context) (ID, error) {
	return port.CoerceID[ID](c.Param("id"))
}

func (this *HandlerFor[M, ID, U, In]) entity(c *gin.Context, status int, m M) {
	if this.opt.transform != nil {
		writeJSON(c, status, this.opt.transform(c, m))
		return
	}
	writeJSON(c, status, m)
}

func (this *HandlerFor[M, ID, U, In]) fail(c *gin.Context, err error) {
	this.opt.errorHandler(c, err)
}

func keep(c *gin.Context, raw []byte) {
	if len(raw) == 0 {
		return
	}
	c.Request = c.Request.WithContext(crudhttp.WithBody(c.Request.Context(), raw))
}

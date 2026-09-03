package crudnet

import (
	"io"
	"net/http"
	"strings"

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
	o.RefuseServiceOptions("crudnet.Serving")
	return build(service, port.Identity[M](), wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudnet.ServingFor")
	return build(service, mapper, wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func NewWire[In, P, R, M any, ID comparable, U any](repository Repository[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], options ...Option[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), mapper, patcher, presenter, o)
}

func ServingWire[In, P, R, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], options ...Option[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o := collect(options)
	o.RefuseServiceOptions("crudnet.ServingWire")
	return build(service, mapper, patcher, presenter, o)
}

func build[M any, ID comparable, U any, In any, P any, R any](service Service[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], o options[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o.RefuseContradictions("crudnet")
	h := &ResourceFor[M, ID, U, In, P, R]{service: service, mapper: mapper, patcher: patcher, presenter: presenter, opt: o}
	if h.opt.errorHandler == nil {
		rd := h.opt.renderer
		if rd == nil {
			rd = rendererFor(port.Hops(service, mapper))
		}
		h.opt.errorHandler = func(w http.ResponseWriter, r *http.Request, err error) { render(rd, w, r, err) }
	}
	return h
}

func (this *ResourceFor[M, ID, U, In, P, R]) Mount(mux *http.ServeMux, prefix string) {
	p := strings.TrimSuffix(prefix, "/")
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	collection := []string{p + "/{$}"}
	if p != "" {
		collection = append(collection, p)
	}

	mounted := this.opt.Mounted()
	if mounted.Has(port.OpCreate) {
		for _, c := range collection {
			mux.HandleFunc("POST "+c, this.Create)
		}
	}
	if mounted.Has(port.OpBulkDelete) {
		mux.HandleFunc("POST "+p+"/bulk-delete", this.BulkDelete)
	}
	if mounted.Has(port.OpQuery) {
		mux.HandleFunc("POST "+p+"/query", this.Query)
	}
	if mounted.Has(port.OpCount) {
		mux.HandleFunc("GET "+p+"/count", this.CountGet)
	}
	if mounted.Has(port.OpCountQuery) {
		mux.HandleFunc("POST "+p+"/count", this.CountPost)
	}
	if mounted.Has(port.OpList) {
		for _, c := range collection {
			mux.HandleFunc("GET "+c, this.List)
		}
	}
	if mounted.Has(port.OpGet) {
		mux.HandleFunc("GET "+p+"/{id}", this.GetByID)
	}
	if mounted.Has(port.OpUpdate) {
		mux.HandleFunc("PATCH "+p+"/{id}", this.Update)
	}
	if mounted.Has(port.OpReplace) {
		mux.HandleFunc("PUT "+p+"/{id}", this.Replace)
	}
	if mounted.Has(port.OpDelete) {
		mux.HandleFunc("DELETE "+p+"/{id}", this.Delete)
	}
}

func (this *ResourceFor[M, ID, U, In, P, R]) List(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseQueryString(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.list(w, r, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Query(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseBody(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.list(w, r, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) list(w http.ResponseWriter, r *http.Request, request *query.Request) {
	scope, err := this.scope(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	page, err := this.service.List(r.Context(), port.ListCommand{Query: request, Options: scope})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	if this.opt.transform == nil {
		writeJSON(r.Context(), w, http.StatusOK, crud.MapPage(page, this.presenter.Response))
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, crud.MapPage(page, func(m M) any {
		return this.opt.transform(r, m)
	}))
}

func (this *ResourceFor[M, ID, U, In, P, R]) CountGet(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseQueryString(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.count(w, r, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) CountPost(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseBody(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.count(w, r, request)
}

func (this *ResourceFor[M, ID, U, In, P, R]) count(w http.ResponseWriter, r *http.Request, request *query.Request) {
	scope, err := this.scope(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	n, err := this.service.Count(r.Context(), port.CountCommand{Query: request, Options: scope})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"count": n})
}

func (this *ResourceFor[M, ID, U, In, P, R]) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := this.id(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	request, err := this.parseQueryString(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	scope, err := this.scope(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err := this.service.Get(r.Context(), port.GetCommand[ID]{ID: id, Query: request, Options: scope})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.entity(w, r, http.StatusOK, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Create(w http.ResponseWriter, r *http.Request) {
	var in In
	raw, err := this.decode(r.Body, &in)
	r = keep(r, raw)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err := this.mapper.Model(r.Context(), in)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err = this.service.Create(r.Context(), port.CreateCommand[M]{Model: m, Before: this.beforeSave(r)})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.entity(w, r, http.StatusCreated, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Update(w http.ResponseWriter, r *http.Request) {
	id, err := this.id(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	var patch P
	raw, err := this.decode(r.Body, &patch)
	r = keep(r, raw)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err := this.service.Update(r.Context(), port.UpdateCommand[ID, U]{ID: id, Patch: this.patcher.Update(patch), Before: this.beforeUpdate(r, id)})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.entity(w, r, http.StatusOK, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Replace(w http.ResponseWriter, r *http.Request) {
	id, err := this.id(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	var in In
	raw, err := this.decode(r.Body, &in)
	r = keep(r, raw)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err := this.mapper.Model(r.Context(), in)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err = this.service.Replace(r.Context(), port.ReplaceCommand[ID, M]{ID: id, Model: m, Before: this.beforeSave(r)})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.entity(w, r, http.StatusOK, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := this.id(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	n, err := this.service.Delete(r.Context(), port.DeleteCommand[ID]{ID: id})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"deleted": n})
}

type BulkDeleteRequest[ID comparable] = crudhttp.BulkDeleteRequest[ID]

func (this *ResourceFor[M, ID, U, In, P, R]) BulkDelete(w http.ResponseWriter, r *http.Request) {
	var request BulkDeleteRequest[ID]
	if err := this.decodeOnly(r.Body, &request); err != nil {
		this.fail(w, r, err)
		return
	}
	if len(request.IDs) > this.opt.BulkCap() {
		this.fail(w, r, crudhttp.BadRequestAs(errs.CodeBadQuery, nil, "at most %d ids per request", this.opt.BulkCap()))
		return
	}
	n, err := this.service.DeleteMany(r.Context(), port.BulkDeleteCommand[ID]{IDs: request.IDs})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, map[string]any{"deleted": n})
}

func (this *ResourceFor[M, ID, U, In, P, R]) scope(r *http.Request) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(r)
}

func (this *ResourceFor[M, ID, U, In, P, R]) beforeSave(r *http.Request) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(r, m) }
}

func (this *ResourceFor[M, ID, U, In, P, R]) beforeUpdate(r *http.Request, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(r, id, dataTransferObject) }
}

func (this *ResourceFor[M, ID, U, In, P, R]) parseQueryString(r *http.Request) (*query.Request, error) {
	return query.ParseQuery(r.URL.Query())
}

func (this *ResourceFor[M, ID, U, In, P, R]) parseBody(r *http.Request) (*query.Request, error) {
	request := &query.Request{}
	if err := this.decodeOnly(r.Body, request); err != nil {
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

func (this *ResourceFor[M, ID, U, In, P, R]) id(r *http.Request) (ID, error) {
	return port.CoerceID[ID](r.PathValue("id"))
}

func (this *ResourceFor[M, ID, U, In, P, R]) entity(w http.ResponseWriter, r *http.Request, status int, m M) {
	if this.opt.transform != nil {
		writeJSON(r.Context(), w, status, this.opt.transform(r, m))
		return
	}
	writeJSON(r.Context(), w, status, this.presenter.Response(m))
}

func (this *ResourceFor[M, ID, U, In, P, R]) fail(w http.ResponseWriter, r *http.Request, err error) {
	this.opt.errorHandler(w, r, err)
}

func keep(r *http.Request, raw []byte) *http.Request {
	if len(raw) == 0 {
		return r
	}
	return r.WithContext(crudhttp.WithBody(r.Context(), raw))
}

package crudnet

import (
	"io"
	"net/http"
	"strings"

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
	o.RefuseServiceOptions("crudnet.Serving")
	return build(service, port.Identity[M](), o)
}

func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudnet.ServingFor")
	return build(service, mapper, o)
}

func build[M any, ID comparable, U any, In any](service Service[M, ID, U], mapper Mapper[In, M], o options[M, ID, U]) *HandlerFor[M, ID, U, In] {
	h := &HandlerFor[M, ID, U, In]{service: service, mapper: mapper, opt: o}
	if h.opt.errorHandler == nil {
		rd := h.opt.renderer
		if rd == nil {
			rd = rendererFor(port.Hops(service, mapper))
		}
		h.opt.errorHandler = func(w http.ResponseWriter, r *http.Request, err error) { render(rd, w, r, err) }
	}
	return h
}

func (this *HandlerFor[M, ID, U, In]) Mount(mux *http.ServeMux, prefix string) {
	p := strings.TrimSuffix(prefix, "/")
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	collection := []string{p + "/{$}"}
	if p != "" {
		collection = append(collection, p)
	}

	if !this.opt.ReadOnly {
		for _, c := range collection {
			mux.HandleFunc("POST "+c, this.Create)
		}
		mux.HandleFunc("POST "+p+"/bulk-delete", this.BulkDelete)
	}
	mux.HandleFunc("POST "+p+"/query", this.Query)
	mux.HandleFunc("GET "+p+"/count", this.CountGet)
	mux.HandleFunc("POST "+p+"/count", this.CountPost)
	for _, c := range collection {
		mux.HandleFunc("GET "+c, this.List)
	}
	mux.HandleFunc("GET "+p+"/{id}", this.GetByID)
	if !this.opt.ReadOnly {
		mux.HandleFunc("PATCH "+p+"/{id}", this.Update)
		mux.HandleFunc("PUT "+p+"/{id}", this.Replace)
		mux.HandleFunc("DELETE "+p+"/{id}", this.Delete)
	}
}

func (this *HandlerFor[M, ID, U, In]) List(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseQueryString(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.list(w, r, request)
}

func (this *HandlerFor[M, ID, U, In]) Query(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseBody(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.list(w, r, request)
}

func (this *HandlerFor[M, ID, U, In]) list(w http.ResponseWriter, r *http.Request, request *query.Request) {
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
		writeJSON(r.Context(), w, http.StatusOK, page)
		return
	}
	writeJSON(r.Context(), w, http.StatusOK, crud.MapPage(page, func(m M) any {
		return this.opt.transform(r, m)
	}))
}

func (this *HandlerFor[M, ID, U, In]) CountGet(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseQueryString(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.count(w, r, request)
}

func (this *HandlerFor[M, ID, U, In]) CountPost(w http.ResponseWriter, r *http.Request) {
	request, err := this.parseBody(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.count(w, r, request)
}

func (this *HandlerFor[M, ID, U, In]) count(w http.ResponseWriter, r *http.Request, request *query.Request) {
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

func (this *HandlerFor[M, ID, U, In]) GetByID(w http.ResponseWriter, r *http.Request) {
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

func (this *HandlerFor[M, ID, U, In]) Create(w http.ResponseWriter, r *http.Request) {
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

func (this *HandlerFor[M, ID, U, In]) Update(w http.ResponseWriter, r *http.Request) {
	id, err := this.id(r)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	var dataTransferObject U
	raw, err := this.decode(r.Body, &dataTransferObject)
	r = keep(r, raw)
	if err != nil {
		this.fail(w, r, err)
		return
	}
	m, err := this.service.Update(r.Context(), port.UpdateCommand[ID, U]{ID: id, Patch: dataTransferObject, Before: this.beforeUpdate(r, id)})
	if err != nil {
		this.fail(w, r, err)
		return
	}
	this.entity(w, r, http.StatusOK, m)
}

func (this *HandlerFor[M, ID, U, In]) Replace(w http.ResponseWriter, r *http.Request) {
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

func (this *HandlerFor[M, ID, U, In]) Delete(w http.ResponseWriter, r *http.Request) {
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

func (this *HandlerFor[M, ID, U, In]) BulkDelete(w http.ResponseWriter, r *http.Request) {
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

func (this *HandlerFor[M, ID, U, In]) scope(r *http.Request) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(r)
}

func (this *HandlerFor[M, ID, U, In]) beforeSave(r *http.Request) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(r, m) }
}

func (this *HandlerFor[M, ID, U, In]) beforeUpdate(r *http.Request, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(r, id, dataTransferObject) }
}

func (this *HandlerFor[M, ID, U, In]) parseQueryString(r *http.Request) (*query.Request, error) {
	return query.ParseQuery(r.URL.Query())
}

func (this *HandlerFor[M, ID, U, In]) parseBody(r *http.Request) (*query.Request, error) {
	request := &query.Request{}
	if err := this.decodeOnly(r.Body, request); err != nil {
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

func (this *HandlerFor[M, ID, U, In]) id(r *http.Request) (ID, error) {
	return port.CoerceID[ID](r.PathValue("id"))
}

func (this *HandlerFor[M, ID, U, In]) entity(w http.ResponseWriter, r *http.Request, status int, m M) {
	if this.opt.transform != nil {
		writeJSON(r.Context(), w, status, this.opt.transform(r, m))
		return
	}
	writeJSON(r.Context(), w, status, m)
}

func (this *HandlerFor[M, ID, U, In]) fail(w http.ResponseWriter, r *http.Request, err error) {
	this.opt.errorHandler(w, r, err)
}

func keep(r *http.Request, raw []byte) *http.Request {
	if len(raw) == 0 {
		return r
	}
	return r.WithContext(crudhttp.WithBody(r.Context(), raw))
}

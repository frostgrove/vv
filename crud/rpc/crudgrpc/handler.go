package crudgrpc

import (
	"context"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

// Repository is everything the default service needs. crud.Repo[M, ID, U]
// satisfies it, and so does specs.Repo and any struct that embeds either —
// which is how a service layer with extra checks takes the repository's place.
type Repository[M any, ID comparable, U any] = port.Repository[M, ID, U]

// Service is the transport-neutral seam every method talks to. One value of it
// mounts on this binding, on Fiber, on Gin and on net/http, because a generic
// alias is the same type ([[D-045]]).
type Service[M any, ID comparable, U any] = port.Service[M, ID, U]

// Mapper turns this transport's input type into the model, for a resource whose
// request document is not the model's own JSON shape.
type Mapper[In, M any] = port.Mapper[In, M]

// HandlerFor is the mounted API for a resource whose input type is In.
//
// The fourth parameter is what lets New keep inferring three: [Handler] is an
// alias that fills In in with the model, so every constructor the three HTTP
// bindings have keeps the same shape here ([[D-022]]).
type HandlerFor[M any, ID comparable, U any, In any] struct {
	svc    Service[M, ID, U]
	mapper Mapper[In, M]
	opt    options[M, ID, U]
	render Renderer
}

// Handler is the mounted API — a HandlerFor whose input type is the model,
// which is what New means.
type Handler[M any, ID comparable, U any] = HandlerFor[M, ID, U, M]

// New builds a handler over a repository. All three type parameters are
// inferred from it, so the call site carries no generics.
func New[M any, ID comparable, U any](repo Repository[M, ID, U], opts ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(opts)
	return build(port.NewService(repo, o.Service()...), port.Identity[M](), o)
}

// NewFor builds a handler whose request document is a type of its own, mapped
// onto the model before the service sees it.
func NewFor[In, M any, ID comparable, U any](repo Repository[M, ID, U], mapper Mapper[In, M], opts ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(opts)
	return build(port.NewService(repo, o.Service()...), mapper, o)
}

// Serving mounts a service that is already built — the one a generator wrote,
// or one an application assembled itself.
//
// An option that configures the service is refused here rather than ignored:
// the service is already made, and a silent no-op is the wrong answer to
// "bound what clients may ask for" ([[D-021]]).
func Serving[M any, ID comparable, U any](svc Service[M, ID, U], opts ...Option[M, ID, U]) *Handler[M, ID, U] {
	o := collect(opts)
	o.RefuseServiceOptions("crudgrpc.Serving")
	return build(svc, port.Identity[M](), o)
}

// ServingFor mounts an already-built service behind an input type of its own.
func ServingFor[In, M any, ID comparable, U any](svc Service[M, ID, U], mapper Mapper[In, M], opts ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(opts)
	o.RefuseServiceOptions("crudgrpc.ServingFor")
	return build(svc, mapper, o)
}

// build is the one place a handler is assembled, so the four constructors
// cannot drift in what they wire.
func build[M any, ID comparable, U any, In any](svc Service[M, ID, U], mapper Mapper[In, M], o options[M, ID, U]) *HandlerFor[M, ID, U, In] {
	h := &HandlerFor[M, ID, U, In]{svc: svc, mapper: mapper, opt: o, render: o.renderer}
	if h.render == nil {
		h.render = rendererFor(port.Hops(svc, mapper))
	}
	return h
}

// ---------------------------------------------------------------------------
// reads

// List answers the query document with a page.
func (h *HandlerFor[M, ID, U, In]) List(ctx context.Context, req *structpb.Struct) (*structpb.Struct, error) {
	q, err := queryOf(req, h.svc.Meta())
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	scope, err := h.scope(ctx)
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	page, err := h.svc.List(ctx, port.ListCommand{Query: q, Options: scope})
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	if h.opt.transform == nil {
		return h.page(ctx, page.Total, page)
	}
	return h.page(ctx, page.Total, crud.MapPage(page, func(m M) any { return h.opt.transform(ctx, m) }))
}

// Count answers the query document with the size of the result. The service
// narrows the document first: paging left in would make the answer the size of
// one page.
func (h *HandlerFor[M, ID, U, In]) Count(ctx context.Context, req *structpb.Struct) (*structpb.Struct, error) {
	q, err := queryOf(req, h.svc.Meta())
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	scope, err := h.scope(ctx)
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	n, err := h.svc.Count(ctx, port.CountCommand{Query: q, Options: scope})
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	return countDoc(n), nil
}

// Get answers one entity by key, honouring the nested query document's preload
// and select.
func (h *HandlerFor[M, ID, U, In]) Get(ctx context.Context, req *structpb.Struct) (*structpb.Struct, error) {
	id, err := idOf[ID](req, "id")
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	q, err := queryIn(req, h.svc.Meta())
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	scope, err := h.scope(ctx)
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	m, err := h.svc.Get(ctx, port.GetCommand[ID]{ID: id, Query: q, Options: scope})
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	return h.entity(ctx, m)
}

// ---------------------------------------------------------------------------
// writes

// Create answers one new row. The document is decoded into this handler's input
// type and mapped onto the model; the service then clears a database-generated
// key and every `generated` column, so a client cannot pick its own id or forge
// a server-side timestamp.
func (h *HandlerFor[M, ID, U, In]) Create(ctx context.Context, req *structpb.Struct) (*structpb.Struct, error) {
	var in In
	if err := fromStruct(req, &in); err != nil {
		return nil, h.fail(ctx, err)
	}
	m, err := h.mapper.Model(ctx, in)
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	m, err = h.svc.Create(ctx, port.CreateCommand[M]{Model: m, Before: h.beforeSave(ctx)})
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	return h.entity(ctx, m)
}

// Update answers a partial update. The patch decodes straight into U, which is
// the generated DTO and therefore already the wire shape ([[D-018]]).
func (h *HandlerFor[M, ID, U, In]) Update(ctx context.Context, req *structpb.Struct) (*structpb.Struct, error) {
	id, err := idOf[ID](req, "id")
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	patch, err := requiredSub(req, "patch")
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	var dto U
	if err := fromStruct(patch, &dto); err != nil {
		return nil, h.fail(ctx, err)
	}
	m, err := h.svc.Update(ctx, port.UpdateCommand[ID, U]{ID: id, Patch: dto, Before: h.beforeUpdate(ctx, id)})
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	return h.entity(ctx, m)
}

// Replace answers a whole row at a key the caller chose, with the key taken
// from the request rather than the entity.
//
// When the database generates the key, a replace never creates: the key has to
// name a row that already exists. Otherwise it is the way around AllowClientID
// — a client cannot pick its key on Create but could name one here — and on
// PostgreSQL an explicit insert into a serial column does not advance the
// sequence, so the next create collides on the primary key and keeps colliding
// until somebody repairs the sequence by hand ([[D-012]]).
func (h *HandlerFor[M, ID, U, In]) Replace(ctx context.Context, req *structpb.Struct) (*structpb.Struct, error) {
	id, err := idOf[ID](req, "id")
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	body, err := requiredSub(req, "entity")
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	var in In
	if err := fromStruct(body, &in); err != nil {
		return nil, h.fail(ctx, err)
	}
	m, err := h.mapper.Model(ctx, in)
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	m, err = h.svc.Replace(ctx, port.ReplaceCommand[ID, M]{ID: id, Model: m, Before: h.beforeSave(ctx)})
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	return h.entity(ctx, m)
}

// Delete removes one row. Removing nothing is not found: the caller named a
// row, and it was not there.
func (h *HandlerFor[M, ID, U, In]) Delete(ctx context.Context, req *structpb.Struct) (*structpb.Struct, error) {
	id, err := idOf[ID](req, "id")
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	n, err := h.svc.Delete(ctx, port.DeleteCommand[ID]{ID: id})
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	return deletedDoc(n), nil
}

// BulkDelete removes a set. Removing nothing is a count of zero and not an
// error: the caller named a set, and a set may be empty.
func (h *HandlerFor[M, ID, U, In]) BulkDelete(ctx context.Context, req *structpb.Struct) (*structpb.Struct, error) {
	ids, err := idsOf[ID](req, "ids")
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	if len(ids) > h.opt.BulkCap() {
		return nil, h.fail(ctx, port.BadRequestAs(errs.CodeBadQuery, nil, "at most %d ids per request", h.opt.BulkCap()))
	}
	n, err := h.svc.DeleteMany(ctx, port.BulkDeleteCommand[ID]{IDs: ids})
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	return deletedDoc(n), nil
}

// ---------------------------------------------------------------------------
// plumbing

// scope is the transport's own narrowing, handed to the service as options it
// appends after the query document compiles. Appended and not merged, because
// crud.Where ANDs ([[D-004]]).
func (h *HandlerFor[M, ID, U, In]) scope(ctx context.Context) ([]crud.Option, error) {
	if h.opt.scope == nil {
		return nil, nil
	}
	return h.opt.scope(ctx)
}

// beforeSave binds the create-and-replace hook to this call, so the service can
// run it in the one place the order is documented: after the server-owned
// fields are cleared ([[UC-013]] guarantee 7).
func (h *HandlerFor[M, ID, U, In]) beforeSave(ctx context.Context) func(*M) error {
	if h.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return h.opt.beforeSave(ctx, m) }
}

// beforeUpdate binds the patch hook to this call and its key.
func (h *HandlerFor[M, ID, U, In]) beforeUpdate(ctx context.Context, id ID) func(*U) error {
	if h.opt.beforeUpdate == nil {
		return nil
	}
	return func(dto *U) error { return h.opt.beforeUpdate(ctx, id, dto) }
}

// entity renders one model, through the presenter when there is one.
func (h *HandlerFor[M, ID, U, In]) entity(ctx context.Context, m M) (*structpb.Struct, error) {
	if h.opt.transform != nil {
		return h.answer(ctx, h.opt.transform(ctx, m))
	}
	return h.answer(ctx, m)
}

// page mirrors answer but restores Total from its original int64 after Struct
// has encoded the rest of the page. Struct has only a double number kind, so a
// count at magnitude 2^53 and beyond uses the same exact decimal-string convention as Count
// and Delete rather than silently becoming its neighbouring value.
func (h *HandlerFor[M, ID, U, In]) page(ctx context.Context, total int64, v any) (*structpb.Struct, error) {
	st, err := h.answer(ctx, v)
	if err != nil {
		return nil, err
	}
	st.Fields["total"] = exactIntValue(total)
	// TotalPages is another derived count. It is normally far smaller than
	// Total, but a page size of one makes that assumption false.
	if page, ok := v.(crud.PaginatedResponse[M]); ok {
		st.Fields["totalPages"] = exactIntValue(int64(page.TotalPages))
	} else if page, ok := v.(crud.PaginatedResponse[any]); ok {
		st.Fields["totalPages"] = exactIntValue(int64(page.TotalPages))
	}
	return st, nil
}

// answer is the one place a successful response leaves this package. A value
// that will not encode — a presenter returning a channel, a NaN — is a server
// fault and answers like one, rather than a half-built document.
func (h *HandlerFor[M, ID, U, In]) answer(ctx context.Context, v any) (*structpb.Struct, error) {
	st, err := toStruct(v)
	if err != nil {
		return nil, h.fail(ctx, err)
	}
	return st, nil
}

// fail is the one place a failure leaves this package. The locale is installed
// here rather than at the top of every method because rendering is the only
// thing that reads it.
func (h *HandlerFor[M, ID, U, In]) fail(ctx context.Context, err error) error {
	return h.render.Render(withRequestLocale(ctx), err).Err()
}

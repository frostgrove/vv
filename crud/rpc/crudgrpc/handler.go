package crudgrpc

import (
	"context"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type Repository[M any, ID comparable, U any] = port.Repository[M, ID, U]

type Service[M any, ID comparable, U any] = port.Service[M, ID, U]

type Mapper[In, M any] = port.Mapper[In, M]

type HandlerFor[M any, ID comparable, U any, In any] struct {
	service Service[M, ID, U]
	mapper  Mapper[In, M]
	opt     options[M, ID, U]
	render  Renderer
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
	o.RefuseServiceOptions("crudgrpc.Serving")
	return build(service, port.Identity[M](), o)
}

func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudgrpc.ServingFor")
	return build(service, mapper, o)
}

func build[M any, ID comparable, U any, In any](service Service[M, ID, U], mapper Mapper[In, M], o options[M, ID, U]) *HandlerFor[M, ID, U, In] {
	h := &HandlerFor[M, ID, U, In]{service: service, mapper: mapper, opt: o, render: o.renderer}
	if h.render == nil {
		h.render = rendererFor(port.Hops(service, mapper))
	}
	return h
}

func (this *HandlerFor[M, ID, U, In]) List(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	q, err := queryOf(request, this.service.Meta())
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	scope, err := this.scope(ctx)
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	page, err := this.service.List(ctx, port.ListCommand{Query: q, Options: scope})
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	if this.opt.transform == nil {
		return this.page(ctx, page.Total, page)
	}
	return this.page(ctx, page.Total, crud.MapPage(page, func(m M) any { return this.opt.transform(ctx, m) }))
}

func (this *HandlerFor[M, ID, U, In]) Count(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	q, err := queryOf(request, this.service.Meta())
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	scope, err := this.scope(ctx)
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	n, err := this.service.Count(ctx, port.CountCommand{Query: q, Options: scope})
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return countDoc(n), nil
}

func (this *HandlerFor[M, ID, U, In]) Get(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	id, err := idOf[ID](request, "id")
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	q, err := queryIn(request, this.service.Meta())
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	scope, err := this.scope(ctx)
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	m, err := this.service.Get(ctx, port.GetCommand[ID]{ID: id, Query: q, Options: scope})
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return this.entity(ctx, m)
}

func (this *HandlerFor[M, ID, U, In]) Create(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	var in In
	if err := fromStruct(request, &in); err != nil {
		return nil, this.fail(ctx, err)
	}
	m, err := this.mapper.Model(ctx, in)
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	m, err = this.service.Create(ctx, port.CreateCommand[M]{Model: m, Before: this.beforeSave(ctx)})
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return this.entity(ctx, m)
}

func (this *HandlerFor[M, ID, U, In]) Update(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	id, err := idOf[ID](request, "id")
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	patch, err := requiredSub(request, "patch")
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	var dataTransferObject U
	if err := fromStruct(patch, &dataTransferObject); err != nil {
		return nil, this.fail(ctx, err)
	}
	m, err := this.service.Update(ctx, port.UpdateCommand[ID, U]{ID: id, Patch: dataTransferObject, Before: this.beforeUpdate(ctx, id)})
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return this.entity(ctx, m)
}

func (this *HandlerFor[M, ID, U, In]) Replace(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	id, err := idOf[ID](request, "id")
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	body, err := requiredSub(request, "entity")
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	var in In
	if err := fromStruct(body, &in); err != nil {
		return nil, this.fail(ctx, err)
	}
	m, err := this.mapper.Model(ctx, in)
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	m, err = this.service.Replace(ctx, port.ReplaceCommand[ID, M]{ID: id, Model: m, Before: this.beforeSave(ctx)})
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return this.entity(ctx, m)
}

func (this *HandlerFor[M, ID, U, In]) Delete(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	id, err := idOf[ID](request, "id")
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	n, err := this.service.Delete(ctx, port.DeleteCommand[ID]{ID: id})
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return deletedDoc(n), nil
}

func (this *HandlerFor[M, ID, U, In]) BulkDelete(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	ids, err := idsOf[ID](request, "ids")
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	if len(ids) > this.opt.BulkCap() {
		return nil, this.fail(ctx, port.BadRequestAs(errs.CodeBadQuery, nil, "at most %d ids per request", this.opt.BulkCap()))
	}
	n, err := this.service.DeleteMany(ctx, port.BulkDeleteCommand[ID]{IDs: ids})
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return deletedDoc(n), nil
}

func (this *HandlerFor[M, ID, U, In]) scope(ctx context.Context) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(ctx)
}

func (this *HandlerFor[M, ID, U, In]) beforeSave(ctx context.Context) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(ctx, m) }
}

func (this *HandlerFor[M, ID, U, In]) beforeUpdate(ctx context.Context, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(ctx, id, dataTransferObject) }
}

func (this *HandlerFor[M, ID, U, In]) entity(ctx context.Context, m M) (*structpb.Struct, error) {
	if this.opt.transform != nil {
		return this.answer(ctx, this.opt.transform(ctx, m))
	}
	return this.answer(ctx, m)
}

func (this *HandlerFor[M, ID, U, In]) page(ctx context.Context, total int64, v any) (*structpb.Struct, error) {
	st, err := this.answer(ctx, v)
	if err != nil {
		return nil, err
	}
	st.Fields["total"] = exactIntValue(total)

	if page, ok := v.(crud.PaginatedResponse[M]); ok {
		st.Fields["totalPages"] = exactIntValue(int64(page.TotalPages))
	} else if page, ok := v.(crud.PaginatedResponse[any]); ok {
		st.Fields["totalPages"] = exactIntValue(int64(page.TotalPages))
	}
	return st, nil
}

func (this *HandlerFor[M, ID, U, In]) answer(ctx context.Context, v any) (*structpb.Struct, error) {
	st, err := toStruct(v)
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return st, nil
}

func (this *HandlerFor[M, ID, U, In]) fail(ctx context.Context, err error) error {
	return this.render.Render(withRequestLocale(ctx), err).Err()
}

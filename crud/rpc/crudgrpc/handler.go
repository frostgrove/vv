package crudgrpc

import (
	"context"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/wire"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type Repository[M any, ID comparable, U any] = port.Repository[M, ID, U]

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
	render    Renderer
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
	o.RefuseServiceOptions("crudgrpc.Serving")
	return build(service, port.Identity[M](), wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func ServingFor[In, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], options ...Option[M, ID, U]) *HandlerFor[M, ID, U, In] {
	o := collect(options)
	o.RefuseServiceOptions("crudgrpc.ServingFor")
	return build(service, mapper, wire.IdentityPatch[U](), wire.IdentityPresenter[M](), o)
}

func NewWire[In, P, R, M any, ID comparable, U any](repository Repository[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], options ...Option[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o := collect(options)
	return build(port.NewService(repository, o.Service()...), mapper, patcher, presenter, o)
}

func ServingWire[In, P, R, M any, ID comparable, U any](service Service[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], options ...Option[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o := collect(options)
	o.RefuseServiceOptions("crudgrpc.ServingWire")
	return build(service, mapper, patcher, presenter, o)
}

func build[M any, ID comparable, U any, In any, P any, R any](service Service[M, ID, U], mapper Mapper[In, M], patcher PatchMapper[P, U], presenter Presenter[M, R], o options[M, ID, U]) *ResourceFor[M, ID, U, In, P, R] {
	o.RefuseContradictions("crudgrpc")
	h := &ResourceFor[M, ID, U, In, P, R]{service: service, mapper: mapper, patcher: patcher, presenter: presenter, opt: o, render: o.renderer}
	if h.render == nil {
		h.render = rendererFor(port.Hops(service, mapper))
	}
	return h
}

func (this *ResourceFor[M, ID, U, In, P, R]) List(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
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
		return this.page(ctx, page.Total, crud.MapPage(page, this.presenter.Response))
	}
	return this.page(ctx, page.Total, crud.MapPage(page, func(m M) any { return this.opt.transform(ctx, m) }))
}

func (this *ResourceFor[M, ID, U, In, P, R]) Count(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
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

func (this *ResourceFor[M, ID, U, In, P, R]) Get(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
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

func (this *ResourceFor[M, ID, U, In, P, R]) Create(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
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

func (this *ResourceFor[M, ID, U, In, P, R]) Update(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
	id, err := idOf[ID](request, "id")
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	patch, err := requiredSub(request, "patch")
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	var body P
	if err := fromStruct(patch, &body); err != nil {
		return nil, this.fail(ctx, err)
	}
	m, err := this.service.Update(ctx, port.UpdateCommand[ID, U]{ID: id, Patch: this.patcher.Update(body), Before: this.beforeUpdate(ctx, id)})
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return this.entity(ctx, m)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Replace(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
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

func (this *ResourceFor[M, ID, U, In, P, R]) Delete(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
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

func (this *ResourceFor[M, ID, U, In, P, R]) BulkDelete(ctx context.Context, request *structpb.Struct) (*structpb.Struct, error) {
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

func (this *ResourceFor[M, ID, U, In, P, R]) scope(ctx context.Context) ([]crud.Option, error) {
	if this.opt.scope == nil {
		return nil, nil
	}
	return this.opt.scope(ctx)
}

func (this *ResourceFor[M, ID, U, In, P, R]) beforeSave(ctx context.Context) func(*M) error {
	if this.opt.beforeSave == nil {
		return nil
	}
	return func(m *M) error { return this.opt.beforeSave(ctx, m) }
}

func (this *ResourceFor[M, ID, U, In, P, R]) beforeUpdate(ctx context.Context, id ID) func(*U) error {
	if this.opt.beforeUpdate == nil {
		return nil
	}
	return func(dataTransferObject *U) error { return this.opt.beforeUpdate(ctx, id, dataTransferObject) }
}

func (this *ResourceFor[M, ID, U, In, P, R]) entity(ctx context.Context, m M) (*structpb.Struct, error) {
	if this.opt.transform != nil {
		return this.answer(ctx, this.opt.transform(ctx, m))
	}
	return this.answer(ctx, this.presenter.Response(m))
}

func (this *ResourceFor[M, ID, U, In, P, R]) page(ctx context.Context, total int64, v any) (*structpb.Struct, error) {
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

func (this *ResourceFor[M, ID, U, In, P, R]) answer(ctx context.Context, v any) (*structpb.Struct, error) {
	st, err := toStruct(v)
	if err != nil {
		return nil, this.fail(ctx, err)
	}
	return st, nil
}

func (this *ResourceFor[M, ID, U, In, P, R]) fail(ctx context.Context, err error) error {
	return this.render.Render(withRequestLocale(ctx), err).Err()
}

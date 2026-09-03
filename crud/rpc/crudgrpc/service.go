package crudgrpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/port"
)

const ServicePrefix = "vv.crud.v1."

func ServiceName(name string) string {
	if strings.Contains(name, ".") {
		return name
	}
	return ServicePrefix + name
}

func (this *ResourceFor[M, ID, U, In, P, R]) Register(s grpc.ServiceRegistrar, name string) {
	s.RegisterService(this.Desc(name), nil)
}

func (this *ResourceFor[M, ID, U, In, P, R]) Desc(name string) *grpc.ServiceDesc {
	full := ServiceName(name)
	desc := &grpc.ServiceDesc{
		ServiceName: full,
		HandlerType: (*any)(nil),
		Metadata:    full,
	}
	add := func(method string, fn unaryFunc) {
		desc.Methods = append(desc.Methods, grpc.MethodDesc{
			MethodName: method,
			Handler:    unary(full, method, fn),
		})
	}

	mounted := this.opt.Mounted()
	if mounted.Has(port.OpList) {
		add("List", this.List)
	}
	if mounted.Has(port.OpCount) {
		add("Count", this.Count)
	}
	if mounted.Has(port.OpGet) {
		add("Get", this.Get)
	}
	if mounted.Has(port.OpCreate) {
		add("Create", this.Create)
	}
	if mounted.Has(port.OpUpdate) {
		add("Update", this.Update)
	}
	if mounted.Has(port.OpReplace) {
		add("Replace", this.Replace)
	}
	if mounted.Has(port.OpDelete) {
		add("Delete", this.Delete)
	}
	if mounted.Has(port.OpBulkDelete) {
		add("BulkDelete", this.BulkDelete)
	}
	return desc
}

type unaryFunc func(context.Context, *structpb.Struct) (*structpb.Struct, error)

func unary(service, method string, fn unaryFunc) grpc.MethodHandler {
	full := "/" + service + "/" + method
	return func(_ any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := new(structpb.Struct)
		if err := dec(in); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return fn(ctx, in)
		}
		info := &grpc.UnaryServerInfo{FullMethod: full}
		return interceptor(ctx, in, info, func(ctx context.Context, request any) (any, error) {
			return fn(ctx, request.(*structpb.Struct))
		})
	}
}

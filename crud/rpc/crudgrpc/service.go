package crudgrpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
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

	add("List", this.List)
	add("Count", this.Count)
	add("Get", this.Get)
	if !this.opt.ReadOnly {
		add("Create", this.Create)
		add("Update", this.Update)
		add("Replace", this.Replace)
		add("Delete", this.Delete)
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

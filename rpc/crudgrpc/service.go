package crudgrpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

// ServicePrefix is the proto package a resource is registered under when the
// name it was given is a bare one.
//
// Per-resource service names, rather than one fixed service with a `resource`
// field in every request. Reflection is unavailable either way — a generic
// resource has no compiled file descriptor to serve — so a shared name would
// cost the one thing it could have bought and take away two: a per-method
// interceptor and an authorization rule both key on the full method name, and
// under a shared service every resource's Create is the same method.
const ServicePrefix = "vv.crud.v1."

// ServiceName is the full proto service name a resource is registered under.
// A name that already carries a package is used verbatim, so an application can
// put its resources in a package of its own.
func ServiceName(name string) string {
	if strings.Contains(name, ".") {
		return name
	}
	return ServicePrefix + name
}

// Register mounts the methods on a gRPC server under the given resource name.
//
//	crudgrpc.New(articles).Register(server, "Article")
//
// registers vv.crud.v1.Article with the eight methods, or the three read ones
// under [ReadOnly] — a method that is not registered answers Unimplemented,
// which is gRPC's own answer and needs no arm here.
func (h *HandlerFor[M, ID, U, In]) Register(s grpc.ServiceRegistrar, name string) {
	// The implementation is nil, and that is what makes a generic resource
	// registrable at all: RegisterService checks the handler type only when it
	// is given one, so the methods can be closures over this handler and there
	// is no generated interface to satisfy.
	s.RegisterService(h.Desc(name), nil)
}

// Desc is the service descriptor Register installs, for a registrar that is not
// a *grpc.Server — a mock, a multiplexer, or a server built by something else.
//
// HandlerType is *any rather than a generated interface, so a registrar that
// does check it against a non-nil implementation still passes: every type
// implements the empty interface.
func (h *HandlerFor[M, ID, U, In]) Desc(name string) *grpc.ServiceDesc {
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

	add("List", h.List)
	add("Count", h.Count)
	add("Get", h.Get)
	if !h.opt.readOnly {
		add("Create", h.Create)
		add("Update", h.Update)
		add("Replace", h.Replace)
		add("Delete", h.Delete)
		add("BulkDelete", h.BulkDelete)
	}
	return desc
}

// A unaryFunc is what every method of this binding is: a document in, a
// document out.
type unaryFunc func(context.Context, *structpb.Struct) (*structpb.Struct, error)

// unary wraps one method as gRPC's own handler shape, including the
// interceptor hop generated code carries.
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
		return interceptor(ctx, in, info, func(ctx context.Context, req any) (any, error) {
			return fn(ctx, req.(*structpb.Struct))
		})
	}
}

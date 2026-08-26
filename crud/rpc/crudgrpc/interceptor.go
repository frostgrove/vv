package crudgrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// Errors is the interceptor. It renders an error a method returned through the
// same code table and the same violation pipeline this binding's own methods
// use, so a mux carrying both CRUD methods and hand-written ones answers one
// way.
//
// Installing it twice renders once, and the marker is the error itself rather
// than a wrapper. An HTTP binding cannot do that — a response is a stream a
// handler may already have half-written, so the marker there is the
// response-writer wrapper — but a gRPC response is a return value, and an error
// that already carries a status has already been rendered by something. That
// something may be the inner copy of this interceptor or the application's own
// status; either way, overwriting it would be the interceptor deciding it knows
// better than the method it wrapped.
func Errors(opts ...RenderOption) grpc.UnaryServerInterceptor {
	rd := Renderer(defaultRenderer)
	if len(opts) > 0 {
		rd = NewRenderer(opts...)
	}
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		if _, already := status.FromError(err); already {
			return resp, err
		}
		return resp, rd.Render(withRequestLocale(ctx), err).Err()
	}
}

// StreamErrors is [Errors] for a streaming method.
//
// It exists because the unary one is not enough and the gap was invisible: the
// auth interceptor (`authgrpc.Stream`) returns an unrendered `errs.Fault` and
// relies on something downstream to turn it into a status, exactly as its unary
// twin does — and there was no downstream for a stream. grpc-go then wrapped the
// bare error as codes.Unknown, so a refused stream answered Unknown where a
// refused unary call answered Unauthenticated, and a client branching on the code
// could not tell a rejected credential from a server bug.
//
// The same renderer and the same table as [Errors], for the reason [[D-045]]
// gives: one classification, spelled once per protocol, never once per entry
// point.
func StreamErrors(opts ...RenderOption) grpc.StreamServerInterceptor {
	rd := Renderer(defaultRenderer)
	if len(opts) > 0 {
		rd = NewRenderer(opts...)
	}
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, ss)
		if err == nil {
			return nil
		}
		if _, already := status.FromError(err); already {
			return err
		}
		return rd.Render(withRequestLocale(ss.Context()), err).Err()
	}
}

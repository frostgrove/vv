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

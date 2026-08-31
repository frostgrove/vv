package crudgrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

func Errors(options ...RenderOption) grpc.UnaryServerInterceptor {
	rd := Renderer(defaultRenderer)
	if len(options) > 0 {
		rd = NewRenderer(options...)
	}
	return func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		response, err := handler(ctx, request)
		if err == nil {
			return response, nil
		}
		if _, already := status.FromError(err); already {
			return response, err
		}
		return response, rd.Render(withRequestLocale(ctx), err).Err()
	}
}

func StreamErrors(options ...RenderOption) grpc.StreamServerInterceptor {
	rd := Renderer(defaultRenderer)
	if len(options) > 0 {
		rd = NewRenderer(options...)
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

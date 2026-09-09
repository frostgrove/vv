package crudgrpc

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

func Errors(options ...RenderOption) grpc.UnaryServerInterceptor {
	baseOptions := append([]RenderOption(nil), options...)
	rd := Renderer(defaultRenderer)
	if len(baseOptions) > 0 {
		rd = NewRenderer(baseOptions...)
	}
	return func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		boundary := &errorBoundary{}
		installed := withErrors(ctx, boundary)
		response, err := handler(installed, request)
		if err == nil {
			return response, nil
		}
		if rendered, ok := renderDeferred(boundary, rd, baseOptions, err); ok {
			return response, rendered
		}
		if _, already := status.FromError(err); already {
			return response, err
		}
		return response, rd.Render(withRequestLocale(installed), err).Err()
	}
}

type errorsKey struct{}
type errorBoundary struct{ marker byte }

func withErrors(ctx context.Context, boundary *errorBoundary) context.Context {
	return context.WithValue(ctx, errorsKey{}, boundary)
}

func errorBoundaryFrom(ctx context.Context) *errorBoundary {
	if ctx == nil {
		return nil
	}
	boundary, _ := ctx.Value(errorsKey{}).(*errorBoundary)
	return boundary
}

func errorsInstalled(ctx context.Context) bool { return errorBoundaryFrom(ctx) != nil }

type renderingKey struct{}

type resourceRendering struct {
	renderer Renderer
	standard bool
	options  []RenderOption
}

func withResourceRendering(ctx context.Context, rendering resourceRendering) context.Context {
	rendering.options = append([]RenderOption(nil), rendering.options...)
	return context.WithValue(ctx, renderingKey{}, rendering)
}

func resourceRenderingFrom(ctx context.Context) resourceRendering {
	if ctx == nil {
		return resourceRendering{}
	}
	rendering, _ := ctx.Value(renderingKey{}).(resourceRendering)
	rendering.options = append([]RenderOption(nil), rendering.options...)
	return rendering
}

func installedRenderer(base Renderer, baseOptions []RenderOption, resource resourceRendering) Renderer {
	if resource.renderer != nil {
		return resource.renderer
	}
	if !resource.standard || len(resource.options) == 0 {
		return base
	}
	options := make([]RenderOption, 0, len(baseOptions)+len(resource.options))
	options = append(options, baseOptions...)
	options = append(options, resource.options...)
	return NewRenderer(options...)
}

type deferredError struct {
	boundary  *errorBoundary
	ctx       context.Context
	err       error
	rendering resourceRendering
}

func deferError(ctx context.Context, err error, rendering resourceRendering) error {
	return &deferredError{boundary: errorBoundaryFrom(ctx), ctx: ctx, err: err, rendering: rendering}
}

func ContextError(ctx context.Context, err error) error {
	boundary := errorBoundaryFrom(ctx)
	if err == nil || boundary == nil {
		return err
	}
	var deferred *deferredError
	if errors.As(err, &deferred) && deferred.boundary == boundary {
		return err
	}
	return deferError(ctx, err, resourceRenderingFrom(ctx))
}

func (this *deferredError) Error() string { return this.err.Error() }

func (this *deferredError) Unwrap() error { return this.err }

func renderDeferred(boundary *errorBoundary, base Renderer, baseOptions []RenderOption, err error) (error, bool) {
	var deferred *deferredError
	if !errors.As(err, &deferred) || deferred.boundary != boundary {
		return nil, false
	}
	err = deferred.err
	if _, already := status.FromError(err); already {
		return err, true
	}
	renderer := installedRenderer(base, baseOptions, deferred.rendering)
	return renderer.Render(withRequestLocale(deferred.ctx), err).Err(), true
}

func StreamErrors(options ...RenderOption) grpc.StreamServerInterceptor {
	baseOptions := append([]RenderOption(nil), options...)
	rd := Renderer(defaultRenderer)
	if len(baseOptions) > 0 {
		rd = NewRenderer(baseOptions...)
	}
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		boundary := &errorBoundary{}
		installed := withErrors(ss.Context(), boundary)
		err := handler(srv, &contextServerStream{ServerStream: ss, ctx: installed})
		if err == nil {
			return nil
		}
		if rendered, ok := renderDeferred(boundary, rd, baseOptions, err); ok {
			return rendered
		}
		if _, already := status.FromError(err); already {
			return err
		}
		return rd.Render(withRequestLocale(installed), err).Err()
	}
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (this *contextServerStream) Context() context.Context { return this.ctx }

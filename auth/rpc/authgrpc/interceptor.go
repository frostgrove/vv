package authgrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/frostgrove/vv/auth"
)

type Option func(*config)

type config struct {
	skip map[string]struct{}
}

func Skip(fullMethods ...string) Option {
	return func(c *config) {
		if c.skip == nil {
			c.skip = make(map[string]struct{}, len(fullMethods))
		}
		for _, m := range fullMethods {
			c.skip[m] = struct{}{}
		}
	}
}

func Unary(guard *auth.Guard, options ...Option) grpc.UnaryServerInterceptor {
	configuration := configure(guard, "Unary", options)
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if configuration.skips(methodOf(info)) {
			return handler(ctx, request)
		}
		ctx, err := guard.AuthenticateValues(ctx, valuesFor(ctx))
		if err != nil {
			return nil, err
		}
		return handler(ctx, request)
	}
}

func Stream(guard *auth.Guard, options ...Option) grpc.StreamServerInterceptor {
	configuration := configure(guard, "Stream", options)
	return func(server any, serverStream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info != nil && configuration.skips(info.FullMethod) {
			return handler(server, serverStream)
		}
		ctx, err := guard.AuthenticateValues(serverStream.Context(), valuesFor(serverStream.Context()))
		if err != nil {
			return err
		}
		return handler(server, &authenticated{ServerStream: serverStream, ctx: ctx})
	}
}

func configure(guard *auth.Guard, interceptorName string, options []Option) *config {
	if err := guard.Validate(); err != nil {
		panic("authgrpc: " + interceptorName + " needs a ready Guard: " + err.Error())
	}
	configuration := &config{}
	for _, option := range options {
		if option != nil {
			option(configuration)
		}
	}
	return configuration
}

func (this *config) skips(fullMethod string) bool {
	if this.skip == nil {
		return false
	}
	_, ok := this.skip[fullMethod]
	return ok
}

func methodOf(info *grpc.UnaryServerInfo) string {
	if info == nil {
		return ""
	}
	return info.FullMethod
}

func valuesFor(ctx context.Context) func(string) []string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return func(string) []string { return nil }
	}
	return func(name string) []string {
		return md.Get(name)
	}
}

type authenticated struct {
	grpc.ServerStream
	ctx context.Context
}

func (this *authenticated) Context() context.Context { return this.ctx }

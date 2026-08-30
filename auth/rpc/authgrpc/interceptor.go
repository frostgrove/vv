package authgrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/frostgrove/vv/auth"
)

// An Option configures the interceptors.
type Option func(*config)

type config struct {
	skip map[string]struct{}
}

// Skip leaves the named full method names unauthenticated — a health check, a
// reflection service, a login call.
//
//	authgrpc.Unary(guard, authgrpc.Skip("/grpc.health.v1.Health/Check"))
//
// The name is the full one, with the leading slash, as it appears in
// grpc.UnaryServerInfo.FullMethod. A prefix is not accepted on purpose: an
// exact list is auditable and a prefix quietly widens the day somebody adds a
// method under it.
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

// Unary authenticates every unary call.
//
// A refusal is returned rather than rendered: crudgrpc.Errors turns an
// errs.Fault into a google.rpc.Status, and errs.KindUnauthorized already maps to
// UNAUTHENTICATED, so chaining the two is the whole wiring. Returning also means
// the method never runs.
//
// Consecutive interceptors with the same [auth.Guard] authenticate once. A
// different guard performs its own check; A -> B -> A fails closed because no
// assurance order is inferred ([[D-076]]).
func Unary(guard *auth.Guard, options ...Option) grpc.UnaryServerInterceptor {
	configuration := configure(guard, "Unary", options)
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if configuration.skips(methodOf(info)) {
			return handler(ctx, request)
		}
		ctx, err := guard.Authenticate(ctx, getterFor(ctx))
		if err != nil {
			return nil, err
		}
		return handler(ctx, request)
	}
}

// Stream authenticates a stream when it opens.
//
// Once, and only then. A credential that expires while the stream is open is
// not noticed here — an interceptor runs before the first message and never
// again — so a long-lived stream that must re-check does it in its own loop.
// Guard composition has the same adjacent-idempotence and ambiguous-reentry
// rules as [Unary].
func Stream(guard *auth.Guard, options ...Option) grpc.StreamServerInterceptor {
	configuration := configure(guard, "Stream", options)
	return func(server any, serverStream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info != nil && configuration.skips(info.FullMethod) {
			return handler(server, serverStream)
		}
		ctx, err := guard.Authenticate(serverStream.Context(), getterFor(serverStream.Context()))
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

// getterFor adapts incoming metadata to the shape auth.Guard takes.
//
// metadata.MD.Get lowercases the key it is given and the transport lowercased
// the keys on the way in, so "Authorization" finds what a client sent as
// "authorization" — the same case-insensitivity an http.Header has, reached a
// different way.
func getterFor(ctx context.Context) func(string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return func(string) string { return "" }
	}
	return func(name string) string {
		if vs := md.Get(name); len(vs) > 0 {
			return vs[0]
		}
		return ""
	}
}

// authenticated is a stream carrying the context the guard produced. A
// grpc.ServerStream answers its context from a method, so replacing it is the
// only way a handler downstream sees the principal.
type authenticated struct {
	grpc.ServerStream
	ctx context.Context
}

func (this *authenticated) Context() context.Context { return this.ctx }

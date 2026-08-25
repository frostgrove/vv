package authgrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/shardit-io/vv/auth"
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
// Chaining it twice authenticates once: [auth.Guard] hands back a context that
// already carries a principal untouched.
func Unary(g *auth.Guard, opts ...Option) grpc.UnaryServerInterceptor {
	c := configure(g, "Unary", opts)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if c.skips(methodOf(info)) {
			return handler(ctx, req)
		}
		ctx, err := g.Authenticate(ctx, getterFor(ctx))
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// Stream authenticates a stream when it opens.
//
// Once, and only then. A credential that expires while the stream is open is
// not noticed here — an interceptor runs before the first message and never
// again — so a long-lived stream that must re-check does it in its own loop.
func Stream(g *auth.Guard, opts ...Option) grpc.StreamServerInterceptor {
	c := configure(g, "Stream", opts)
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info != nil && c.skips(info.FullMethod) {
			return handler(srv, ss)
		}
		ctx, err := g.Authenticate(ss.Context(), getterFor(ss.Context()))
		if err != nil {
			return err
		}
		return handler(srv, &authenticated{ServerStream: ss, ctx: ctx})
	}
}

func configure(g *auth.Guard, who string, opts []Option) *config {
	if g == nil {
		panic("authgrpc: " + who + " needs a Guard; without one nothing is authenticated")
	}
	c := &config{}
	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}
	return c
}

func (c *config) skips(fullMethod string) bool {
	if c.skip == nil {
		return false
	}
	_, ok := c.skip[fullMethod]
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

func (s *authenticated) Context() context.Context { return s.ctx }

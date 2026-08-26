package authgrpc_test

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/frostgrove/vv/auth"
)

// The same three fixtures the HTTP bindings carry, and for the same reason: a
// test name that appears in all four transports has to mean the same thing.

const badToken = "signature does not verify"

var editor = auth.Claims{
	Sub:         "u-1",
	Roles:       []auth.Role{"editor"},
	Permissions: []auth.Permission{"article:read"},
}

func accepts() auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return editor, nil
	})
}

func refuses() auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return nil, auth.Unauthenticated(badToken)
	})
}

func counting(n *int) auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		*n++
		return editor, nil
	})
}

// seen records the principal the handler behind the interceptor observed. A
// gRPC method has only the context, so there is nowhere else it could look.
type seen struct {
	ran       bool
	principal auth.Principal
	found     bool
}

func (s *seen) handle(ctx context.Context, _ any) (any, error) {
	s.ran = true
	s.principal, s.found = auth.PrincipalFrom(ctx)
	return "ok", nil
}

// incoming builds the context a server sees for a call carrying these metadata
// pairs.
func incoming(kv ...string) context.Context {
	if len(kv) == 0 {
		return context.Background()
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(kv...))
}

func info(method string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: method}
}

const articleCreate = "/vv.crud.v1.Article/Create"

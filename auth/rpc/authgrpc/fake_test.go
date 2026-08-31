package authgrpc_test

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/frostgrove/vv/auth"
)

const badToken = "signature does not verify"

var errKeyProviderUnavailable = errors.New("verification key source unavailable")

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

func unavailable() auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return nil, errKeyProviderUnavailable
	})
}

func counting(n *int) auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		*n++
		return editor, nil
	})
}

type seen struct {
	ran       bool
	principal auth.Principal
	found     bool
}

func (this *seen) handle(ctx context.Context, _ any) (any, error) {
	this.ran = true
	this.principal, this.found = auth.PrincipalFrom(ctx)
	return "ok", nil
}

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

package authjwt

import (
	"context"
	"strings"

	"github.com/frostgrove/vv/auth"
)

func Authenticator[C any](p *Parser[C], to func(ctx context.Context, c C) (auth.Principal, error)) auth.Authenticator {
	if p == nil {
		panic("authjwt: Authenticator needs a Parser")
	}
	if to == nil {
		panic("authjwt: Authenticator needs a function that turns claims into a principal")
	}
	return auth.AuthenticatorFunc(func(ctx context.Context, cred auth.Credential) (auth.Principal, error) {
		claims, err := p.Parse(ctx, cred.Token)
		if err != nil {
			return nil, err
		}
		return to(ctx, claims)
	})
}

func Standard(k KeySource, roles auth.RoleMap, options ...Option) auth.Authenticator {
	p := New[Claims](k, options...)
	return Authenticator(p, func(_ context.Context, c Claims) (auth.Principal, error) {
		if strings.TrimSpace(c.Sub) == "" {
			return nil, auth.Unauthenticated("token has no subject")
		}
		return c.Grant(roles), nil
	})
}

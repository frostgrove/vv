package authjwt

import (
	"context"
	"strings"

	"github.com/frostgrove/vv/auth"
)

// Authenticator bridges a parser to the contract: it verifies the token and
// hands the claims to a function that says who that is.
//
// The mapping is the caller's because only the caller knows what its issuer's
// claims mean — that "org" is the tenant, that "groups" are roles, that a
// subject needs a prefix to be unique across two issuers. A library that
// guessed would be wrong on the first deployment that spelled anything
// differently.
//
// The mapper may refuse. Returning an error rejects the token even though it
// verified, which is where "this token is valid but its tenant was deleted"
// belongs. Build that error with [auth.Unauthenticated] so its reason stays out
// of the response.
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

// Standard is the zero-configuration path: parse into [Claims] and use them as
// the principal, with a role map expanded into permissions.
//
//	authn := authjwt.Standard(
//	    authjwt.HMAC(secret),
//	    auth.RoleMap{"editor": {"article:read", "article:write"}},
//	    authjwt.Issuer("https://id.example.com"),
//	    authjwt.Audience("articles-api"),
//	)
//
// A nil role map is fine and means the token's permissions are taken as they
// come. Standard refuses an empty subject: the ready-made identity is used for
// ownership scopes and audit records, so permissions alone are not an identity.
// A deployment with another subject rule uses [Authenticator] and states that
// mapping explicitly.
func Standard(k KeySource, roles auth.RoleMap, options ...Option) auth.Authenticator {
	p := New[Claims](k, options...)
	return Authenticator(p, func(_ context.Context, c Claims) (auth.Principal, error) {
		if strings.TrimSpace(c.Sub) == "" {
			return nil, auth.Unauthenticated("token has no subject")
		}
		return c.Grant(roles), nil
	})
}

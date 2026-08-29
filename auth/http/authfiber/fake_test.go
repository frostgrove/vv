package authfiber_test

import (
	"context"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
)

// The three fixtures every binding's middleware test is written against. They
// are duplicated file-for-file across authnet, authgin and authfiber on
// purpose: the point of the triplet is that the same test name means the same
// thing on every transport ([[FL-013]]).

const badToken = "signature does not verify"

var editor = auth.Claims{
	Sub:         "u-1",
	Roles:       []auth.Role{"editor"},
	Permissions: []auth.Permission{"article:read"},
}

// accepts answers the same principal for any credential.
func accepts() auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return editor, nil
	})
}

// refuses answers a 401 whose reason must never reach the client.
func refuses() auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		return nil, auth.Unauthenticated(badToken)
	})
}

// counting reports how many times a credential was actually verified.
func counting(n *int) auth.Authenticator {
	return auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		*n++
		return editor, nil
	})
}

// seen records the principal the handler behind the middleware observed. It
// reads c.Context and not Locals, because Locals does not reach a repository.
type seen struct {
	ran       bool
	principal auth.Principal
	found     bool
}

func (this *seen) handle(c fiber.Ctx) error {
	this.ran = true
	this.principal, this.found = auth.PrincipalFrom(c.Context())
	return c.SendString("ok")
}

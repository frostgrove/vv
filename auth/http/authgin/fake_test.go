package authgin_test

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"

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

func (this *seen) handle(c *gin.Context) {
	this.ran = true
	this.principal, this.found = auth.PrincipalFrom(c.Request.Context())
}

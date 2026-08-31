package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/frostgrove/vv/internal/nilvalue"
)

type Credential struct {
	Scheme string

	Token string
}

func (this Credential) Is(scheme string) bool {
	return strings.EqualFold(this.Scheme, scheme)
}

const SchemeBearer = "Bearer"

type Authenticator interface {
	Authenticate(ctx context.Context, c Credential) (Principal, error)
}

type AuthenticatorFunc func(ctx context.Context, c Credential) (Principal, error)

func (this AuthenticatorFunc) Authenticate(ctx context.Context, c Credential) (Principal, error) {
	return this(ctx, c)
}

func ParseAuthorization(header string) (Credential, bool) {
	header = strings.TrimSpace(header)
	i := strings.IndexByte(header, ' ')
	if i <= 0 {
		return Credential{}, false
	}
	token := strings.TrimSpace(header[i+1:])
	if token == "" {
		return Credential{}, false
	}
	return Credential{Scheme: header[:i], Token: token}, true
}

func Bearer(header string) (Credential, bool) {
	c, ok := ParseAuthorization(header)
	if !ok || !c.Is(SchemeBearer) {
		return Credential{}, false
	}
	return c, true
}

func Chain(as ...Authenticator) Authenticator {
	authenticators := make([]Authenticator, 0, len(as))
	for _, a := range as {
		if !nilvalue.Is(a) {
			authenticators = append(authenticators, a)
		}
	}
	return AuthenticatorFunc(func(ctx context.Context, c Credential) (Principal, error) {
		var refusal, infra error
		for _, a := range authenticators {
			p, e := a.Authenticate(ctx, c)
			if e == nil {
				if !nilvalue.Is(p) {
					return p, nil
				}

				refusal = Unauthenticated("authenticator returned no principal")
				continue
			}
			if errors.Is(e, ErrUnauthenticated) {
				refusal = e
				continue
			}

			if infra == nil {
				infra = e
			}
		}
		switch {
		case infra != nil:
			return nil, infra
		case refusal != nil:
			return nil, refusal
		default:
			return nil, Unauthenticated("no authenticator was wired")
		}
	})
}

package auth

import (
	"context"
	"strings"
)

// A Credential is what the caller presented: an authentication scheme and the
// token under it. Two strings, because a type from net/http here would put the
// gRPC interceptor out of reach ([[D-045]]).
type Credential struct {
	// Scheme is the auth-scheme token — "Bearer", "ApiKey". It is compared
	// case-insensitively, because RFC 7235 says it is case-insensitive and
	// clients spell it every way.
	Scheme string
	// Token is everything after the scheme, whitespace trimmed.
	Token string
}

// Is reports whether the credential carries the named scheme.
func (c Credential) Is(scheme string) bool {
	return strings.EqualFold(c.Scheme, scheme)
}

// SchemeBearer is the scheme a JWT arrives under.
const SchemeBearer = "Bearer"

// An Authenticator turns a presented credential into a caller.
//
// It returns an error rather than a bool so the reason survives to a log
// without reaching a body: build the error with [Unauthenticated] and the
// reason travels in the wrapped error, where nothing renders it.
//
// Implementations must not report which half of a credential was wrong. An
// unknown key and a bad signature are one answer.
type Authenticator interface {
	Authenticate(ctx context.Context, c Credential) (Principal, error)
}

// AuthenticatorFunc adapts a function to [Authenticator].
type AuthenticatorFunc func(ctx context.Context, c Credential) (Principal, error)

// Authenticate implements [Authenticator].
func (f AuthenticatorFunc) Authenticate(ctx context.Context, c Credential) (Principal, error) {
	return f(ctx, c)
}

// ParseAuthorization splits an Authorization header into a credential.
//
// It parses and does not judge: deciding that "Basic" is not acceptable here is
// the [Authenticator]'s, which is the only party that knows what it can verify.
// A header with no space is not a credential — a bare token is a scheme with
// nothing under it, and treating it as a bearer token would accept a malformed
// header that a proxy may have truncated.
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

// Bearer is [ParseAuthorization] narrowed to the one scheme, for the common
// case where anything else is not worth authenticating against.
func Bearer(header string) (Credential, bool) {
	c, ok := ParseAuthorization(header)
	if !ok || !c.Is(SchemeBearer) {
		return Credential{}, false
	}
	return c, true
}

// Chain tries each authenticator in order and answers the first that succeeds.
// It is how one endpoint accepts a JWT from a browser and an API key from a
// batch job.
//
// The error it returns when none succeeds is the last one, and it says nothing
// about how many were tried. Reporting "3 of 3 authenticators refused" would
// tell a caller which schemes exist.
func Chain(as ...Authenticator) Authenticator {
	return AuthenticatorFunc(func(ctx context.Context, c Credential) (Principal, error) {
		var err error
		for _, a := range as {
			if a == nil {
				continue
			}
			p, e := a.Authenticate(ctx, c)
			if e == nil {
				return p, nil
			}
			err = e
		}
		if err == nil {
			err = Unauthenticated("no authenticator was wired")
		}
		return nil, err
	})
}

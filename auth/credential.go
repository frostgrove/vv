package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/frostgrove/vv/internal/nilvalue"
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
func (this Credential) Is(scheme string) bool {
	return strings.EqualFold(this.Scheme, scheme)
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
func (this AuthenticatorFunc) Authenticate(ctx context.Context, c Credential) (Principal, error) {
	return this(ctx, c)
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
// The error it returns when none succeeds says nothing about how many were tried
// or which one produced it. Reporting "3 of 3 authenticators refused" would tell
// a caller which schemes exist.
//
// **A failure that is not a refusal wins over one that is**, whatever order the
// authenticators are in. An authenticator distinguishes "this credential is
// wrong" from "I could not tell" on purpose — apikey.Store has three results for
// exactly that, so a store outage renders as a 500 rather than a 401 ([[D-056]],
// TestAStoreFailureIsNotARefusal). Returning the *last* error threw that away
// whenever a later authenticator refused: Chain(keys, jwt) turned a database
// outage into "your key is invalid", which is wrong for the client and invisible
// to whoever watches the 5xx rate. The wiring order is not something a consumer
// should have to get right to keep an outage visible.
func Chain(as ...Authenticator) Authenticator {
	// Snapshot and normalise before the chain is published. A caller may reuse
	// or mutate the variadic slice after this call; request authentication must
	// not observe that storage. Nil-like optional entries are all treated like a
	// literal nil rather than surviving until their method is invoked.
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
				// Success without an identity is a refusal by contract, but a
				// later alternative still gets its turn.
				refusal = Unauthenticated("authenticator returned no principal")
				continue
			}
			if errors.Is(e, ErrUnauthenticated) {
				refusal = e
				continue
			}
			// The first one, not the last: an outage that happened before another
			// authenticator also failed is still the thing that happened.
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

package auth

import "context"

// A Guard turns a request's headers into an authenticated context. It is the
// whole of what an authentication middleware does that is not framework-shaped,
// which is why it is here and not in a binding: four transports would otherwise
// each carry their own copy of the same five decisions, and the copies would
// disagree the first time one of them was fixed.
//
// A binding supplies the one thing it alone knows — how to read a header — and
// gets back the context the rest of the chain should see:
//
//	ctx, err := g.Authenticate(r.Context(), r.Header.Get)
type Guard struct {
	authn    Authenticator
	header   string
	optional bool
	lookup   func(get func(string) string) (Credential, bool)
}

// An Option configures a [Guard].
type Option func(*Guard)

// HeaderAuthorization is where a credential is read from unless [Header] or
// [Lookup] says otherwise.
const HeaderAuthorization = "Authorization"

// NewGuard builds the guard. It panics on a nil authenticator, because a guard
// with nothing to authenticate against refuses every request and that is a
// misconfiguration a process should not start with ([[D-021]]).
func NewGuard(a Authenticator, options ...Option) *Guard {
	if a == nil {
		panic("auth: NewGuard needs an Authenticator; without one every request is refused")
	}
	g := &Guard{authn: a, header: HeaderAuthorization}
	for _, o := range options {
		if o != nil {
			o(g)
		}
	}
	return g
}

// Header reads the credential from a different header — "X-Api-Key", say.
func Header(name string) Option {
	return func(g *Guard) { g.header = name }
}

// Lookup replaces how a credential is found. The function is handed a
// header-getter rather than a request, which is what lets one option serve
// net/http, Gin, Fiber and gRPC metadata alike ([[D-045]]).
//
//	auth.Lookup(func(get func(string) string) (auth.Credential, bool) {
//	    if k := get("X-Api-Key"); k != "" {
//	        return auth.Credential{Scheme: "ApiKey", Token: k}, true
//	    }
//	    return auth.Bearer(get("Authorization"))
//	})
func Lookup(fn func(get func(name string) string) (Credential, bool)) Option {
	return func(g *Guard) { g.lookup = fn }
}

// Optional lets a request with no credential through unauthenticated.
//
// It does not let a *bad* credential through. A presented token that does not
// verify is a 401 whether or not the endpoint is optional: treating it as
// anonymous would mean a forged or expired token silently downgrades to the
// public view instead of telling the client to re-authenticate, and a client
// with a stale session would then see an empty list rather than a prompt.
//
// What comes after it must still fail closed. Optional means the principal may
// be absent, and every policy in crud/decorators/security refuses an absent
// principal — so an optional guard in front of a gated repository is a 401 at
// the repository instead of at the door, not an open door.
func Optional() Option {
	return func(g *Guard) { g.optional = true }
}

// Authenticate answers the context the rest of the chain should see.
//
// get reads a request header by name and answers "" for one that is not there;
// http.Header.Get, gin.Context.GetHeader and fiber.Ctx.Get all have that shape
// already.
//
// Installing two guards renders one decision: a context that already carries a
// principal is handed back untouched rather than re-authenticated. Without
// that, a guard mounted globally and again on a route group would parse the
// token twice, and a JWKS-backed one would spend two lookups per request.
func (this *Guard) Authenticate(ctx context.Context, get func(name string) string) (context.Context, error) {
	if _, already := PrincipalFrom(ctx); already {
		return ctx, nil
	}

	cred, ok := this.credential(get)
	if !ok {
		if this.optional {
			return ctx, nil
		}
		return ctx, Unauthenticated("no credential presented")
	}

	p, err := this.authn.Authenticate(ctx, cred)
	if err != nil {
		return ctx, err
	}
	if p == nil {
		// An authenticator that answers (nil, nil) has not authenticated
		// anybody. Storing it would put a nil principal one type assertion away
		// from every policy.
		return ctx, Unauthenticated("authenticator returned no principal")
	}
	return WithPrincipal(ctx, p), nil
}

func (this *Guard) credential(get func(name string) string) (Credential, bool) {
	if get == nil {
		return Credential{}, false
	}
	if this.lookup != nil {
		return this.lookup(get)
	}
	return ParseAuthorization(get(this.header))
}

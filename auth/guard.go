package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/frostgrove/vv/internal/nilvalue"
)

// A Guard turns a request's headers into an authenticated context. It is the
// whole of what an authentication middleware does that is not framework-shaped,
// which is why it is here and not in a binding: four transports would otherwise
// each carry their own copy of the same five decisions, and the copies would
// disagree the first time one of them was fixed.
//
// A binding supplies the one thing it alone knows — how to read every raw
// value of a header — and gets back the context the rest of the chain should
// see:
//
//	ctx, err := g.AuthenticateValues(r.Context(), r.Header.Values)
type Guard struct {
	authn    Authenticator
	header   string
	optional bool
	lookup   func(get func(string) string) (Credential, bool)
	ready    bool
}

type guardConfig struct {
	header   string
	optional bool
	lookup   func(get func(string) string) (Credential, bool)
}

// An Option is an immutable declaration applied while [NewGuard] builds a
// guard. Its configuration target is deliberately private: retaining an option
// cannot retain or mutate the published guard, and an option cannot be applied
// to a guard after construction. [Lookup] is the low-level escape hatch for a
// credential source the ready-made declarations do not cover.
type Option interface {
	apply(*guardConfig)
}

type guardOption func(*guardConfig)

func (option guardOption) apply(cfg *guardConfig) { option(cfg) }

// HeaderAuthorization is where a credential is read from unless [Header] or
// [Lookup] says otherwise.
const HeaderAuthorization = "Authorization"

// NewGuard builds the guard. It panics on a nil authenticator, because a guard
// with nothing to authenticate against refuses every request and that is a
// misconfiguration a process should not start with ([[D-021]]).
func NewGuard(a Authenticator, options ...Option) *Guard {
	if nilvalue.Is(a) {
		panic("auth: NewGuard needs an Authenticator; without one every request is refused")
	}
	cfg := guardConfig{header: HeaderAuthorization}
	for _, o := range options {
		if !nilvalue.Is(o) {
			o.apply(&cfg)
		}
	}
	// Build on a private value and copy only the finished state into the public
	// guard. No option ever receives the value middleware will publish.
	return &Guard{
		authn:    a,
		header:   cfg.header,
		optional: cfg.optional,
		lookup:   cfg.lookup,
		ready:    true,
	}
}

// Validate reports whether the guard is ready to be published by a transport.
// NewGuard always returns a valid value. The method exists because *Guard is a
// concrete integration type and new(auth.Guard) otherwise compiles, only to
// panic on the first request when it calls a nil authenticator.
func (this *Guard) Validate() error {
	if this == nil {
		return fmt.Errorf("%w: nil Guard", ErrGuardNotReady)
	}
	if !this.ready {
		return fmt.Errorf("%w: use NewGuard to build it", ErrGuardNotReady)
	}
	return nil
}

// Header reads a scheme-shaped credential from a different header. It changes
// the source, not the syntax: "X-Auth: Bearer token" is valid, while a bare API
// key belongs to apikey.Header.
func Header(name string) Option {
	return guardOption(func(cfg *guardConfig) {
		if strings.TrimSpace(name) == "" {
			panic("auth: Header needs a non-empty header name")
		}
		cfg.header = name
	})
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
	return guardOption(func(cfg *guardConfig) {
		if fn == nil {
			panic("auth: Lookup needs a function")
		}
		cfg.lookup = fn
	})
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
	return guardOption(func(cfg *guardConfig) { cfg.optional = true })
}

// Authenticate answers the context the rest of the chain should see for a
// legacy or deliberately single-value header getter.
//
// get reads a request header by name and answers "" for one that is not there;
// http.Header.Get, gin.Context.GetHeader and fiber.Ctx.Get all have that shape
// already. Official transport bindings use [Guard.AuthenticateValues] instead,
// because a single-value getter cannot distinguish one credential from two
// identical credentials.
//
// Installing the same guard consecutively renders one decision: its latest
// context marker makes the second installation a no-op. A different guard
// always authenticates again. Re-entering the first guard after the second
// fails closed; without an assurance order it is impossible to know whether
// retaining the second principal is a step-up or a downgrade ([[D-076]]).
func (this *Guard) Authenticate(ctx context.Context, get func(name string) string) (context.Context, error) {
	return this.authenticate(ctx, func(name string) []string {
		if get == nil {
			return nil
		}
		value := get(name)
		if value == "" {
			return nil
		}
		return []string{value}
	})
}

// AuthenticateValues authenticates from every raw value attached to a header
// or metadata key. A credential field is singular. More than one value is
// refused before an authenticator runs, including two byte-for-byte identical
// values: equality does not make it safe to guess which occurrence a proxy
// used. No values still means anonymous when [Optional] was declared.
//
// A custom [Lookup] remains list-safe. Its familiar single-value getter is
// backed by this method and records an ambiguity for every header the lookup
// actually reads.
func (this *Guard) AuthenticateValues(
	ctx context.Context,
	values func(name string) []string,
) (context.Context, error) {
	return this.authenticate(ctx, values)
}

func (this *Guard) authenticate(
	ctx context.Context,
	values func(name string) []string,
) (context.Context, error) {
	if err := this.Validate(); err != nil {
		return ctx, internal(err)
	}
	if mark := authenticationMark(ctx, this); mark != nil {
		if mark == latestAuthenticationMark(ctx) && mark.principal == principalStateFrom(ctx) {
			return ctx, nil
		}
		return ctx, internal(fmt.Errorf(
			"%w: the same Guard appears on both sides of another successful identity boundary",
			ErrAmbiguousGuardOrder,
		))
	}

	cred, ok, err := this.credential(values)
	if err != nil {
		return ctx, err
	}
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
	if nilvalue.Is(p) {
		// An authenticator that answers (nil, nil) has not authenticated
		// anybody. Storing it would put a nil principal one type assertion away
		// from every policy.
		return ctx, Unauthenticated("authenticator returned no principal")
	}
	return markAuthenticated(WithPrincipal(ctx, p), this), nil
}

func (this *Guard) credential(values func(name string) []string) (Credential, bool, error) {
	if values == nil {
		return Credential{}, false, nil
	}
	if this.lookup == nil {
		raw := values(this.header)
		if len(raw) > 1 {
			return Credential{}, false, invalidCredentialCardinality(len(raw))
		}
		if len(raw) == 0 {
			return Credential{}, false, nil
		}
		credential, ok := ParseAuthorization(raw[0])
		return credential, ok, nil
	}

	// Lookup predates list-aware transport bindings and deliberately exposes a
	// single-value getter. Preserve that small API while remembering whether it
	// tried to read an ambiguous field. Returning "" prevents a lookup from
	// authenticating whichever duplicate happened to be first.
	cardinality := 0
	get := func(name string) string {
		raw := values(name)
		if len(raw) > 1 {
			if len(raw) > cardinality {
				cardinality = len(raw)
			}
			return ""
		}
		if len(raw) == 1 {
			return raw[0]
		}
		return ""
	}
	credential, ok := this.lookup(get)
	if cardinality > 1 {
		return Credential{}, false, invalidCredentialCardinality(cardinality)
	}
	return credential, ok, nil
}

type guardMark struct {
	guard     *Guard
	principal *principalState
	previous  *guardMark
}

type guardMarkKey struct{}

func authenticationMark(ctx context.Context, guard *Guard) *guardMark {
	if ctx == nil {
		return nil
	}
	mark, _ := ctx.Value(guardMarkKey{}).(*guardMark)
	for ; mark != nil; mark = mark.previous {
		if mark.guard == guard {
			return mark
		}
	}
	return nil
}

func latestAuthenticationMark(ctx context.Context) *guardMark {
	if ctx == nil {
		return nil
	}
	mark, _ := ctx.Value(guardMarkKey{}).(*guardMark)
	return mark
}

func markAuthenticated(ctx context.Context, guard *Guard) context.Context {
	previous, _ := ctx.Value(guardMarkKey{}).(*guardMark)
	// The chain is immutable. A request context may be read by child goroutines,
	// so a mutable set here would turn middleware idempotence into a data race.
	return context.WithValue(ctx, guardMarkKey{}, &guardMark{
		guard: guard, principal: principalStateFrom(ctx), previous: previous,
	})
}

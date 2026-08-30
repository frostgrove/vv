package auth

import (
	"context"

	"github.com/frostgrove/vv/internal/nilvalue"
)

type ctxKey int

const principalKey ctxKey = iota

type principalState struct {
	principal Principal
}

// WithPrincipal carries the authenticated caller down the chain. The context is
// the only channel that reaches the security gate: a transport hook can reject
// a request but cannot rewrite the context the repository sees, so a principal
// left anywhere else — a framework's own per-request store, a handler field —
// is invisible to every policy.
//
// One key for every transport, for port.WithLocale's reason. A second key in an
// HTTP package would be invisible to the gRPC interceptor, both packages' tests
// would pass, and a policy would silently fail closed on one protocol only.
//
// A nil principal is dropped rather than stored. Storing it would make
// [PrincipalFrom] answer (nil, true), and every caller that branched on the
// second result would then dereference nothing.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	if nilvalue.Is(p) {
		return ctx
	}
	return context.WithValue(ctx, principalKey, &principalState{principal: p})
}

// PrincipalFrom answers the authenticated caller, if there is one.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return nil, false
	}
	state, ok := ctx.Value(principalKey).(*principalState)
	if !ok || state == nil || nilvalue.Is(state.principal) {
		return nil, false
	}
	return state.principal, true
}

func principalStateFrom(ctx context.Context) *principalState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(principalKey).(*principalState)
	return state
}

// Require is [PrincipalFrom] for the callers that have no answer without one.
// It is what a policy calls, and the error it returns is a 401 by the time it
// reaches a transport.
//
// Failing closed here is the whole point. A policy that treated an absent
// principal as "no narrowing" would widen every query on the one request where
// authentication was skipped — UC-004's guarantee 16.
func Require(ctx context.Context) (Principal, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, Unauthenticated("no principal in context")
	}
	return p, nil
}

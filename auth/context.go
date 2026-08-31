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

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	if nilvalue.Is(p) {
		return ctx
	}
	return context.WithValue(ctx, principalKey, &principalState{principal: p})
}

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

func Require(ctx context.Context) (Principal, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return nil, Unauthenticated("no principal in context")
	}
	return p, nil
}

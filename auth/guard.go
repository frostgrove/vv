package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/frostgrove/vv/internal/nilvalue"
)

type Guard struct {
	authn     Authenticator
	header    string
	optional  bool
	lookup    Lookout
	observers []Observer
	ready     bool
}

type Lookout func(get func(name string) string) (Credential, bool, error)

type guardConfig struct {
	header    string
	optional  bool
	lookup    Lookout
	observers []Observer
}

type Option interface {
	apply(*guardConfig)
}

type guardOption func(*guardConfig)

func (option guardOption) apply(cfg *guardConfig) { option(cfg) }

const HeaderAuthorization = "Authorization"

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

	return &Guard{
		authn:     a,
		header:    cfg.header,
		optional:  cfg.optional,
		lookup:    cfg.lookup,
		observers: cfg.observers,
		ready:     true,
	}
}

func (this *Guard) Validate() error {
	if this == nil {
		return fmt.Errorf("%w: nil Guard", ErrGuardNotReady)
	}
	if !this.ready {
		return fmt.Errorf("%w: use NewGuard to build it", ErrGuardNotReady)
	}
	return nil
}

func Header(name string) Option {
	return guardOption(func(cfg *guardConfig) {
		if strings.TrimSpace(name) == "" {
			panic("auth: Header needs a non-empty header name")
		}
		cfg.header = name
	})
}

func Lookup(fn func(get func(name string) string) (Credential, bool)) Option {
	return guardOption(func(cfg *guardConfig) {
		if fn == nil {
			panic("auth: Lookup needs a function")
		}
		cfg.lookup = func(get func(name string) string) (Credential, bool, error) {
			credential, found := fn(get)
			return credential, found, nil
		}
	})
}

func LookupOrRefuse(fn Lookout) Option {
	return guardOption(func(cfg *guardConfig) {
		if fn == nil {
			panic("auth: LookupOrRefuse needs a function")
		}
		cfg.lookup = fn
	})
}

func Optional() Option {
	return guardOption(func(cfg *guardConfig) { cfg.optional = true })
}

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
		return ctx, this.refuse(ctx, ReasonGuardUnusable, "the guard was never built", internal(err))
	}
	if mark := authenticationMark(ctx, this); mark != nil {
		if mark == latestAuthenticationMark(ctx) && mark.principal == principalStateFrom(ctx) {
			return ctx, nil
		}
		return ctx, this.refuse(ctx, ReasonGuardUnusable,
			"the same guard authenticates on both sides of another identity boundary",
			internal(fmt.Errorf(
				"%w: the same Guard appears on both sides of another successful identity boundary",
				ErrAmbiguousGuardOrder,
			)))
	}

	credential, found, err := this.credential(values)
	if err != nil {
		return ctx, this.refuse(ctx, ReasonAmbiguousCredential,
			"the credential source carried more than one value", err)
	}
	if !found {
		if this.optional {
			return ctx, nil
		}
		return ctx, this.refuse(ctx, ReasonNoCredential, "no credential presented",
			Unauthenticated("no credential presented"))
	}

	principal, err := this.authn.Authenticate(ctx, credential)
	if err != nil {
		return ctx, this.refuse(ctx, ReasonRejected, "the authenticator refused the credential", err)
	}
	if nilvalue.Is(principal) {
		return ctx, this.refuse(ctx, ReasonNoPrincipal, "the authenticator returned no principal",
			Unauthenticated("authenticator returned no principal"))
	}
	return markAuthenticated(WithPrincipal(ctx, principal), this), nil
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
	credential, ok, err := this.lookup(get)
	if cardinality > 1 {
		return Credential{}, false, invalidCredentialCardinality(cardinality)
	}
	if err != nil {
		return Credential{}, false, err
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

	return context.WithValue(ctx, guardMarkKey{}, &guardMark{
		guard: guard, principal: principalStateFrom(ctx), previous: previous,
	})
}

package auth

import (
	"context"
	"sync/atomic"

	"github.com/frostgrove/vv/internal/nilvalue"
)

type ReasonKind string

const (
	ReasonNoCredential ReasonKind = "no_credential"

	ReasonAmbiguousCredential ReasonKind = "ambiguous_credential"

	ReasonRejected ReasonKind = "rejected"

	ReasonNoPrincipal ReasonKind = "no_principal"

	ReasonGuardUnusable ReasonKind = "guard_unusable"
)

type Reason struct {
	Kind ReasonKind

	Detail string

	Err error
}

type Observer interface {
	Refused(ctx context.Context, reason Reason)
}

type ObserverFunc func(ctx context.Context, reason Reason)

func (this ObserverFunc) Refused(ctx context.Context, reason Reason) { this(ctx, reason) }

func Observe(observer Observer) Option {
	return guardOption(func(cfg *guardConfig) {
		if nilvalue.Is(observer) {
			panic("auth: Observe needs an Observer; without one nothing hears why a request was refused")
		}
		cfg.observers = append(cfg.observers, observer)
	})
}

func Sampled(oneIn int, observer Observer) Observer {
	if nilvalue.Is(observer) {
		panic("auth: Sampled needs an Observer to sample")
	}
	if oneIn <= 1 {
		return observer
	}
	return &sampling{oneIn: uint64(oneIn), observer: observer}
}

type sampling struct {
	oneIn    uint64
	seen     atomic.Uint64
	observer Observer
}

func (this *sampling) Refused(ctx context.Context, reason Reason) {
	if this.seen.Add(1)%this.oneIn != 1 {
		return
	}
	this.observer.Refused(ctx, reason)
}

// Each observer is contained. They are consumer code on the refusal path, and a
// panic in one used to take the request with it — and take every observer after
// it, so a metrics counter could silence an audit log. Every other fan-out in
// this repository isolates its children the same way; this one did not.
func (this *Guard) refuse(ctx context.Context, kind ReasonKind, detail string, err error) error {
	if this == nil {
		return err
	}
	reason := Reason{Kind: kind, Detail: detail, Err: err}
	for _, observer := range this.observers {
		observeRefusalContained(observer, ctx, reason)
	}
	return err
}

func observeRefusalContained(observer Observer, ctx context.Context, reason Reason) {
	defer func() { _ = recover() }()
	observer.Refused(ctx, reason)
}

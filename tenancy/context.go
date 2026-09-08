package tenancy

import "context"

type scopeKey struct{}

// The pin is separate from the scope because it has to outlive it. Unbound
// removes the scope, and if that also removed the refusal then bind-A,
// unbind, bind-B would be a supported way to re-target a unit of work that has
// already chosen its narrowing or its datasource — the exact move With exists to
// refuse. What a context was first bound to is therefore recorded once and never
// cleared.
type pinKey struct{}

type pin struct {
	reference Reference
	epoch     Epoch
}

func (this *Authority) Bind(ctx context.Context, class Class) (context.Context, error) {
	scope, err := this.Verify(ctx, class)
	if err != nil {
		return nil, err
	}
	return this.With(ctx, scope)
}

// Carrying a scope checks only that this authority minted it. Which classes of
// work its lifecycle admits is asked at the point of use, because the answer
// differs per class and a scope carried for a read must not have to be re-minted
// to be refused a write. A second bind for a different tenant inside work that is
// already bound is a refusal rather than a switch: the unit of work below it has
// already chosen its narrowing or its datasource, and letting the tenant move
// underneath it is the one shape that makes a transaction's atomicity
// meaningless.
func (this *Authority) With(ctx context.Context, scope Scope) (context.Context, error) {
	accepted, err := this.minted(scope)
	if err != nil {
		return nil, err
	}
	if current, ok := pinnedTo(ctx); ok && (current.reference != accepted.reference || current.epoch != accepted.epoch) {
		return nil, ErrPinned
	}
	bound := context.WithValue(ctx, scopeKey{}, accepted)
	return context.WithValue(bound, pinKey{}, pin{reference: accepted.reference, epoch: accepted.epoch}), nil
}

func pinnedTo(ctx context.Context) (pin, bool) {
	held, ok := ctx.Value(pinKey{}).(pin)
	return held, ok && !held.reference.IsZero()
}

// Carried work is pinned by default: the lifecycle and generation the scope was
// minted with hold until the next explicit boundary, because asking the control
// plane once per statement makes it a hot dependency of every request. A
// deployment that would rather pay that sets Spec.Revalidate, and then a tenant
// deleted or restored mid-request stops the very next verb.
func (this *Authority) Scope(ctx context.Context, class Class) (Scope, error) {
	if this == nil {
		return Scope{}, ErrNoScope
	}
	scope, ok := From(ctx)
	if !ok {
		return Scope{}, ErrNoScope
	}
	accepted, err := this.accept(scope, class)
	if err != nil {
		return Scope{}, err
	}
	if err := this.permittedByGrant(ctx, class); err != nil {
		return Scope{}, err
	}
	if !this.revalidate {
		return accepted, nil
	}
	return this.current(ctx, accepted, class)
}

func (this *Authority) current(ctx context.Context, scope Scope, class Class) (Scope, error) {
	resolution, err := this.resolver.Lookup(ctx, scope.reference)
	if err != nil {
		return Scope{}, Classify(err)
	}
	if resolution.Reference != scope.reference || !resolution.valid() {
		return Scope{}, ErrUntrusted
	}
	if resolution.Epoch != scope.epoch {
		return Scope{}, ErrStale
	}
	if !this.admission.Admits(class, resolution.Lifecycle) {
		return Scope{}, ErrInactive
	}
	return scope, nil
}

// Work that names no tenant must not inherit one. A worker's base context can
// already carry a scope — an in-process worker started under a bound request, a
// harness that bound once at start-up — and a durable record that asks for no
// tenant would then run as whatever that context happened to hold, with a full
// write scope, while every tenant-scoped path around it fails closed. A context
// value cannot be removed, so the key is set to a scope no authority minted and
// From reports it absent.
//
// What it does not remove is the pin. Clearing both would make it a public,
// capability-free way to re-target bound work: bind to one tenant, unbind, bind
// to another. The context comes back unbound, and still refuses any tenant but
// the one it was bound to.
func Unbound(ctx context.Context) context.Context {
	if _, bound := From(ctx); !bound {
		return ctx
	}
	return context.WithValue(ctx, scopeKey{}, Scope{})
}

func From(ctx context.Context) (Scope, bool) {
	scope, ok := ctx.Value(scopeKey{}).(Scope)
	if !ok || scope.IsZero() {
		return Scope{}, false
	}
	return scope, true
}

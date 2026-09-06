package tenancy

import "context"

type scopeKey struct{}

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
	if current, ok := From(ctx); ok && (current.reference != accepted.reference || current.epoch != accepted.epoch) {
		return nil, ErrPinned
	}
	return context.WithValue(ctx, scopeKey{}, accepted), nil
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

func From(ctx context.Context) (Scope, bool) {
	scope, ok := ctx.Value(scopeKey{}).(Scope)
	if !ok || scope.IsZero() {
		return Scope{}, false
	}
	return scope, true
}

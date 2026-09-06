package tenancy

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"
)

const MinDurableKeyBytes = 32

type Resolver interface {
	Resolve(ctx context.Context) (Resolution, error)
	Lookup(ctx context.Context, reference Reference) (Resolution, error)
}

type Spec struct {
	Resolver   Resolver
	Admission  Admission
	Origin     string
	DurableKey []byte
	Revalidate bool
	Now        func() time.Time
}

type Authority struct {
	resolver   Resolver
	admission  Admission
	origin     string
	salt       []byte
	durableKey []byte
	revalidate bool
	now        func() time.Time
}

func New(spec Spec) (*Authority, error) {
	if spec.Resolver == nil {
		return nil, errors.New("tenancy: an authority needs a resolver; there is no default tenant to fall back to")
	}
	admission := spec.Admission
	if admission.IsZero() {
		admission = AdmitAll(Active)
	}
	if state := admission.inconsistent(); state != LifecycleUnknown {
		return nil, fmt.Errorf("tenancy: %s admits writes but not reads, and every write resolves a read scope first: admit it for reads too, or for neither", state)
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("tenancy: cannot seed the scope binding: %w", err)
	}
	now := spec.Now
	if now == nil {
		now = time.Now
	}
	if len(spec.DurableKey) != 0 && len(spec.DurableKey) < MinDurableKeyBytes {
		return nil, fmt.Errorf("tenancy: a durable key is at least %d bytes", MinDurableKeyBytes)
	}
	return &Authority{
		resolver:   spec.Resolver,
		admission:  admission,
		origin:     spec.Origin,
		salt:       salt,
		durableKey: append([]byte(nil), spec.DurableKey...),
		revalidate: spec.Revalidate,
		now:        now,
	}, nil
}

func Must(spec Spec) *Authority {
	authority, err := New(spec)
	if err != nil {
		panic(err)
	}
	return authority
}

func (this *Authority) Admits(class Class, state Lifecycle) bool {
	return this.admission.Admits(class, state)
}

func (this *Authority) Verify(ctx context.Context, class Class) (Scope, error) {
	resolution, err := this.resolver.Resolve(ctx)
	if err != nil {
		return Scope{}, Classify(err)
	}
	return this.mint(resolution, class)
}

func (this *Authority) Lookup(ctx context.Context, reference Reference, class Class) (Scope, error) {
	if !reference.valid() {
		return Scope{}, ErrUntrusted
	}
	resolution, err := this.resolver.Lookup(ctx, reference)
	if err != nil {
		return Scope{}, Classify(err)
	}
	if resolution.Reference != reference {
		return Scope{}, ErrUntrusted
	}
	return this.mint(resolution, class)
}

func (this *Authority) mint(resolution Resolution, class Class) (Scope, error) {
	if !class.Valid() {
		return Scope{}, ErrIncompatible
	}
	if !resolution.valid() {
		return Scope{}, ErrUntrusted
	}
	if !this.admission.Admits(class, resolution.Lifecycle) {
		return Scope{}, ErrInactive
	}
	return Scope{
		reference: resolution.Reference,
		lifecycle: resolution.Lifecycle,
		epoch:     resolution.Epoch,
		binding:   bind(this.salt, this.origin, resolution),
	}, nil
}

func (this *Authority) minted(scope Scope) (Scope, error) {
	if scope.IsZero() {
		return Scope{}, ErrNoScope
	}
	if !scope.boundTo(this.salt, this.origin) {
		return Scope{}, ErrUntrusted
	}
	return scope, nil
}

func (this *Authority) accept(scope Scope, class Class) (Scope, error) {
	accepted, err := this.minted(scope)
	if err != nil {
		return Scope{}, err
	}
	if !this.admission.Admits(class, accepted.lifecycle) {
		return Scope{}, ErrInactive
	}
	return accepted, nil
}

type Fixed Resolution

func (this Fixed) Resolve(context.Context) (Resolution, error) { return Resolution(this), nil }

func (this Fixed) Lookup(_ context.Context, reference Reference) (Resolution, error) {
	if reference != this.Reference {
		return Resolution{}, ErrUnmapped
	}
	return Resolution(this), nil
}

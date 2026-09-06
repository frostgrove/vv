package tenancy

import (
	"errors"
	"fmt"

	"github.com/frostgrove/vv/crud"
)

var (
	ErrMalformed = fmt.Errorf("tenancy: value is not a well-formed tenant reference: %w", crud.ErrBadRequest)

	ErrNoScope = fmt.Errorf("tenancy: no verified tenant scope: %w", crud.ErrForbidden)

	ErrUntrusted = fmt.Errorf("tenancy: scope was not produced by this authority: %w", crud.ErrForbidden)

	ErrInactive = fmt.Errorf("tenancy: tenant lifecycle does not admit this work: %w", crud.ErrForbidden)

	ErrStale = fmt.Errorf("tenancy: tenant binding epoch has moved: %w", crud.ErrForbidden)

	ErrIncompatible = fmt.Errorf("tenancy: tenant binding is not compatible with this capability: %w", crud.ErrForbidden)

	ErrUnmapped = fmt.Errorf("tenancy: the tenant has no mapping for this capability: %w", crud.ErrForbidden)

	ErrGrantRequired = fmt.Errorf("tenancy: work across tenants needs an explicit grant: %w", crud.ErrForbidden)

	ErrPinned = fmt.Errorf("tenancy: a unit of work is already bound to another tenant: %w", crud.ErrConflict)

	ErrCapacity = errors.New("tenancy: the tenant capability budget is exhausted")

	ErrUnavailable = errors.New("tenancy: the tenant capability is unavailable")
)

var refusals = [...]error{
	ErrNoScope, ErrUntrusted, ErrInactive, ErrStale, ErrIncompatible,
	ErrUnmapped, ErrGrantRequired, ErrPinned, ErrCapacity, ErrUnavailable,
	ErrMalformed,
}

// A resolver reaches a control plane, and what it says about the failure names
// the tenant, the database or the credential that failed. None of that may
// travel back through a refusal, so a sentinel the resolver chose deliberately
// is kept and everything else collapses to one unavailable answer. The resolver
// is application code and owns the channel where the detail belongs. Every seam
// that calls application-supplied code — a resolver, a source factory, a fence —
// answers through this, which is why it is exported for the seams that live
// beside this package rather than inside it.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	for _, refusal := range refusals {
		if errors.Is(err, refusal) {
			return refusal
		}
	}
	return ErrUnavailable
}

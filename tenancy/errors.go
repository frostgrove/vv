package tenancy

import (
	"context"
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

	// Operational rather than a refusal of the caller: the tenant is entitled to
	// this, and the answer is "not now". Both wrap the retryable sentinel so they
	// render as 503 with a Retry-After rather than as the 500 that tells an
	// operator hunting for a bug.
	ErrCapacity = fmt.Errorf("tenancy: the tenant capability budget is exhausted: %w", crud.ErrUnavailable)

	ErrUnavailable = fmt.Errorf("tenancy: the tenant capability is unavailable: %w", crud.ErrUnavailable)

	// A run-level answer rather than a member's, so it is not in the outcome
	// vocabulary: every member is already recorded in the slice returned beside it.
	ErrCohortFailed = errors.New("tenancy: no member of the cohort completed")
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
	// A caller that went away names nothing about the tenant, the database or the
	// credential, so there is nothing to redact and it travels intact. Folding it
	// into ErrUnavailable would put every client disconnect on the graph that says
	// the control plane is down, and would take the deadline with it.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	for _, refusal := range refusals {
		if errors.Is(err, refusal) {
			return refusal
		}
	}
	return ErrUnavailable
}

package audit_test

import (
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

func completeCapabilities() audit.CapabilitySpec {
	return audit.CapabilitySpec{
		Transactions:      audit.SupportSupported,
		CrossSystemAtomic: audit.SupportUnsupported,
		Persistence:       audit.SupportUnsupported,
		Idempotency:       audit.SupportSupported,
		Reconciliation:    audit.SupportSupported,
		StableSearch:      audit.SupportSupported,
		ExactInspection:   audit.SupportSupported,
		AttemptLifecycle:  audit.SupportUnsupported,
		Holds:             audit.SupportUnsupported,
		PurgePlanning:     audit.SupportUnsupported,
	}
}

func completeLimits() audit.LimitSpec {
	return audit.LimitSpec{
		RevisionBytes:         1 << 20,
		AppendRequestBytes:    2 << 20,
		PageRevisions:         100,
		PageBytes:             2 << 20,
		ExactTargets:          100,
		ExactBytes:            2 << 20,
		PositionBytes:         256,
		InventoryCandidates:   100,
		InventoryCohorts:      100,
		InventoryBytes:        2 << 20,
		SnapshotBytes:         1 << 20,
		SearchCohortRevisions: 1000,
		SearchCohortBytes:     2 << 20,
		AttemptTransitions:    64,
		AttemptStateBytes:     1 << 20,
		AttemptOpenLifetime:   24 * time.Hour,
	}
}

func TestCapabilitiesRequireEveryStoreClaimToBeStated(t *testing.T) {
	spec := completeCapabilities()
	capabilities, err := audit.NewCapabilities(spec)
	if err != nil || capabilities.View() != spec {
		t.Fatalf("capabilities = (%+v, %v)", capabilities.View(), err)
	}
	spec.Persistence = audit.SupportUnstated
	if _, err := audit.NewCapabilities(spec); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("unstated persistence = %v", err)
	}
}

func TestLimitsRequireAnInternallyUsableBoundedProfile(t *testing.T) {
	spec := completeLimits()
	limits, err := audit.NewLimits(spec)
	if err != nil || limits.View() != spec {
		t.Fatalf("limits = (%+v, %v)", limits.View(), err)
	}
	spec.PageBytes = spec.RevisionBytes - 1
	if _, err := audit.NewLimits(spec); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("short page bytes = %v", err)
	}
	spec = completeLimits()
	spec.PageRevisions = audit.MaxPageRevisions + 1
	if _, err := audit.NewLimits(spec); !errors.Is(err, audit.ErrTooLarge) {
		t.Fatalf("oversized page = %v", err)
	}
}

func TestBackingAndAuthorityAreOpaqueExactComparableIdentities(t *testing.T) {
	type source struct{ name string }
	type transaction struct{ id int }
	left, err := audit.BackingFor(source{name: "db"})
	if err != nil {
		t.Fatal(err)
	}
	right, _ := audit.BackingFor(source{name: "db"})
	other, _ := audit.BackingFor(source{name: "other"})
	if !audit.SameBacking(left, right) || audit.SameBacking(left, other) || left.String() != "[audit backing]" {
		t.Fatal("backing equality or rendering is not exact")
	}
	if _, err := audit.BackingFor([]byte("not comparable")); !errors.Is(err, audit.ErrWrongStore) {
		t.Fatalf("uncomparable backing = %v", err)
	}

	authority, err := audit.AuthorityFor(source{name: "db"}, transaction{id: 7})
	if err != nil || !authority.Valid() {
		t.Fatalf("authority = (%v, %v)", authority, err)
	}
	same, _ := audit.AuthorityFor(source{name: "db"}, transaction{id: 7})
	different, _ := audit.AuthorityFor(source{name: "db"}, transaction{id: 8})
	if !audit.SameAuthority(authority, same) || audit.SameAuthority(authority, different) || authority.String() != "[audit authority]" {
		t.Fatal("authority equality or rendering is not exact")
	}
}

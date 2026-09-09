package audit

import (
	"fmt"
	"reflect"
	"time"
)

type CapabilitySpec struct {
	Transactions      Support
	CrossSystemAtomic Support
	Persistence       Support
	Idempotency       Support
	Reconciliation    Support
	StableSearch      Support
	ExactInspection   Support
	AttemptLifecycle  Support
	Holds             Support
	PurgePlanning     Support
}

type capabilities struct {
	view CapabilitySpec
}

type Capabilities struct {
	value capabilities
}

func NewCapabilities(spec CapabilitySpec) (Capabilities, error) {
	for _, capability := range []struct {
		name  string
		value Support
	}{
		{name: "transactions", value: spec.Transactions},
		{name: "cross_system_atomic", value: spec.CrossSystemAtomic},
		{name: "persistence", value: spec.Persistence},
		{name: "idempotency", value: spec.Idempotency},
		{name: "reconciliation", value: spec.Reconciliation},
		{name: "stable_search", value: spec.StableSearch},
		{name: "exact_inspection", value: spec.ExactInspection},
		{name: "attempt_lifecycle", value: spec.AttemptLifecycle},
		{name: "holds", value: spec.Holds},
		{name: "purge_planning", value: spec.PurgePlanning},
	} {
		if capability.value != SupportUnsupported && capability.value != SupportSupported {
			return Capabilities{}, auditErrorAt(ErrInvalid, capability.name)
		}
	}
	return Capabilities{value: capabilities{view: spec}}, nil
}

func (c Capabilities) View() CapabilitySpec {
	return c.value.view
}

type LimitSpec struct {
	RevisionBytes         uint64
	AppendRequestBytes    uint64
	PageRevisions         uint32
	PageBytes             uint64
	ExactTargets          uint32
	ExactBytes            uint64
	PositionBytes         uint32
	InventoryCandidates   uint32
	InventoryCohorts      uint32
	InventoryBytes        uint64
	SnapshotBytes         uint32
	SearchCohortRevisions uint32
	SearchCohortBytes     uint64
	AttemptTransitions    uint16
	AttemptStateBytes     uint64
	AttemptOpenLifetime   time.Duration
}

type limits struct {
	view LimitSpec
}

type Limits struct {
	value limits
}

func NewLimits(spec LimitSpec) (Limits, error) {
	bounds := []struct {
		name  string
		value uint64
		limit uint64
	}{
		{name: "revision_bytes", value: spec.RevisionBytes, limit: MaxRevisionBytes},
		{name: "append_request_bytes", value: spec.AppendRequestBytes, limit: MaxAppendRequestBytes},
		{name: "page_revisions", value: uint64(spec.PageRevisions), limit: MaxPageRevisions},
		{name: "page_bytes", value: spec.PageBytes, limit: MaxPageBytes},
		{name: "exact_targets", value: uint64(spec.ExactTargets), limit: MaxExactTargets},
		{name: "exact_bytes", value: spec.ExactBytes, limit: MaxPageBytes},
		{name: "position_bytes", value: uint64(spec.PositionBytes), limit: MaxStorePosition},
		{name: "inventory_candidates", value: uint64(spec.InventoryCandidates), limit: MaxInventoryCandidates},
		{name: "inventory_cohorts", value: uint64(spec.InventoryCohorts), limit: MaxInventoryCohorts},
		{name: "inventory_bytes", value: spec.InventoryBytes, limit: MaxInventoryResultBytes},
		{name: "snapshot_bytes", value: uint64(spec.SnapshotBytes), limit: MaxSnapshotBytes},
		{name: "search_cohort_revisions", value: uint64(spec.SearchCohortRevisions), limit: MaxSearchCohortRevisions},
		{name: "search_cohort_bytes", value: spec.SearchCohortBytes, limit: MaxSearchCohortBytes},
		{name: "attempt_transitions", value: uint64(spec.AttemptTransitions), limit: MaxAttemptTransitions},
		{name: "attempt_state_bytes", value: spec.AttemptStateBytes, limit: MaxAttemptStateBytes},
	}
	for _, bound := range bounds {
		if bound.value == 0 {
			return Limits{}, auditErrorAt(ErrInvalid, bound.name)
		}
		if bound.value > bound.limit {
			return Limits{}, auditTooLarge(bound.name, int(bound.limit))
		}
	}
	if spec.AppendRequestBytes < spec.RevisionBytes || spec.PageBytes < spec.RevisionBytes || spec.ExactBytes < spec.RevisionBytes {
		return Limits{}, fmt.Errorf("%w: aggregate byte limits cannot hold one revision", ErrInvalid)
	}
	if spec.AttemptOpenLifetime <= 0 || spec.AttemptOpenLifetime > MaxAttemptOpenLifetime {
		return Limits{}, auditErrorAt(ErrInvalid, "attempt_open_lifetime")
	}
	return Limits{value: limits{view: spec}}, nil
}

func (l Limits) View() LimitSpec {
	return l.value.view
}

type backing struct {
	identity any
}

type Backing struct {
	value backing
}

func BackingFor(identity any) (Backing, error) {
	if !comparableAuditIdentity(identity) {
		return Backing{}, auditErrorAt(ErrWrongStore, "backing")
	}
	return Backing{value: backing{identity: identity}}, nil
}

func (Backing) String() string {
	return "[audit backing]"
}

type authority struct {
	source      any
	transaction any
}

type Authority struct {
	value authority
}

func AuthorityFor(source, transaction any) (Authority, error) {
	if !comparableAuditIdentity(source) || !comparableAuditIdentity(transaction) {
		return Authority{}, auditErrorAt(ErrWrongAuthority, "authority")
	}
	return Authority{value: authority{source: source, transaction: transaction}}, nil
}

func (a Authority) Valid() bool {
	return comparableAuditIdentity(a.value.source) && comparableAuditIdentity(a.value.transaction)
}

func (Authority) String() string {
	return "[audit authority]"
}

func SameAuthority(left, right Authority) bool {
	return left.Valid() && right.Valid() && sameAuditIdentity(left.value.source, right.value.source) && sameAuditIdentity(left.value.transaction, right.value.transaction)
}

func SameBacking(left, right Backing) bool {
	return comparableAuditIdentity(left.value.identity) && comparableAuditIdentity(right.value.identity) && sameAuditIdentity(left.value.identity, right.value.identity)
}

func comparableAuditIdentity(value any) bool {
	if nilByReflection(value) {
		return false
	}
	return reflect.TypeOf(value).Comparable()
}

func sameAuditIdentity(left, right any) bool {
	if !comparableAuditIdentity(left) || !comparableAuditIdentity(right) || reflect.TypeOf(left) != reflect.TypeOf(right) {
		return false
	}
	return left == right
}

package jobs

import "fmt"

type AckMode uint8

const (
	AckBeforePersistence AckMode = iota + 1
	AckLocalPersistence
	AckRemoteWrite
	AckRemotePersistence
	AckRemoteApply
)

func (m AckMode) Valid() bool { return m >= AckBeforePersistence && m <= AckRemoteApply }
func (m AckMode) String() string {
	switch m {
	case AckBeforePersistence:
		return "before_persistence"
	case AckLocalPersistence:
		return "local_persistence"
	case AckRemoteWrite:
		return "remote_write"
	case AckRemotePersistence:
		return "remote_persistence"
	case AckRemoteApply:
		return "remote_apply"
	default:
		return "unknown"
	}
}

type AckModeSet struct{ bits uint16 }

func AckModes(values ...AckMode) (AckModeSet, error) {
	var result AckModeSet
	for _, value := range values {
		if !value.Valid() || result.Contains(value) {
			return AckModeSet{}, invalid("ack mode set")
		}
		result.bits |= 1 << value
	}
	return result, nil
}

func (s AckModeSet) Contains(value AckMode) bool {
	return value.Valid() && s.bits&(1<<value) != 0
}
func (s AckModeSet) Values() []AckMode {
	result := make([]AckMode, 0, AckRemoteApply)
	for value := AckBeforePersistence; value <= AckRemoteApply; value++ {
		if s.Contains(value) {
			result = append(result, value)
		}
	}
	return result
}
func (s AckModeSet) IsZero() bool { return s.bits == 0 }
func (s AckModeSet) valid() bool  { return s.bits&^uint16((1<<(AckRemoteApply+1))-2) == 0 }

type AcknowledgedLoss uint8

const (
	AcknowledgedLossPossible AcknowledgedLoss = iota + 1
	AcknowledgedLossExcludedForDeclaredFailures
)

func (l AcknowledgedLoss) Valid() bool {
	return l >= AcknowledgedLossPossible && l <= AcknowledgedLossExcludedForDeclaredFailures
}
func (l AcknowledgedLoss) String() string {
	if l == AcknowledgedLossPossible {
		return "possible"
	}
	if l == AcknowledgedLossExcludedForDeclaredFailures {
		return "excluded_for_declared_failures"
	}
	return "unknown"
}

type Failure uint8

const (
	FailureProcessCrash Failure = iota + 1
	FailureHostLoss
	FailureStorageLoss
	FailureSiteLoss
	FailureNetworkPartition
)

func (f Failure) Valid() bool { return f >= FailureProcessCrash && f <= FailureNetworkPartition }
func (f Failure) String() string {
	switch f {
	case FailureProcessCrash:
		return "process_crash"
	case FailureHostLoss:
		return "host_loss"
	case FailureStorageLoss:
		return "storage_loss"
	case FailureSiteLoss:
		return "site_loss"
	case FailureNetworkPartition:
		return "network_partition"
	default:
		return "unknown"
	}
}

type FailureSet struct{ bits uint16 }

func Failures(values ...Failure) (FailureSet, error) {
	var result FailureSet
	for _, value := range values {
		if !value.Valid() || result.Contains(value) {
			return FailureSet{}, invalid("durability failure set")
		}
		result.bits |= 1 << value
	}
	return result, nil
}

func (s FailureSet) Contains(value Failure) bool {
	return value.Valid() && s.bits&(1<<value) != 0
}
func (s FailureSet) Values() []Failure {
	result := make([]Failure, 0, FailureNetworkPartition)
	for value := FailureProcessCrash; value <= FailureNetworkPartition; value++ {
		if s.Contains(value) {
			result = append(result, value)
		}
	}
	return result
}
func (s FailureSet) IsZero() bool { return s.bits == 0 }
func (s FailureSet) valid() bool  { return s.bits&^uint16((1<<(FailureNetworkPartition+1))-2) == 0 }
func (s FailureSet) ContainsAll(required FailureSet) bool {
	return s.valid() && required.valid() && s.bits&required.bits == required.bits
}

type DurabilityRequirement struct {
	acceptedAckModes  AckModeSet
	protectedFailures FailureSet
}

func NewDurabilityRequirement(acceptedAckModes AckModeSet, protectedFailures FailureSet) (DurabilityRequirement, error) {
	requirement := DurabilityRequirement{acceptedAckModes: acceptedAckModes, protectedFailures: protectedFailures}
	if !requirement.valid() {
		return DurabilityRequirement{}, invalid("durability requirement")
	}
	return requirement, nil
}

func (r DurabilityRequirement) AcceptedAckModes() AckModeSet  { return r.acceptedAckModes }
func (r DurabilityRequirement) ProtectedFailures() FailureSet { return r.protectedFailures }
func (r DurabilityRequirement) IsZero() bool {
	return r.acceptedAckModes.IsZero() && r.protectedFailures.IsZero()
}
func (r DurabilityRequirement) String() string                 { return "[job durability requirement]" }
func (r DurabilityRequirement) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, r.String()) }
func (r DurabilityRequirement) valid() bool {
	if !r.acceptedAckModes.valid() || !r.protectedFailures.valid() {
		return false
	}
	return r.protectedFailures.IsZero() || r.acceptedAckModes.IsZero() || r.acceptedAckModes.bits&^(1<<AckBeforePersistence) != 0
}
func (r DurabilityRequirement) accepts(profile DurabilityProfile) bool {
	if !r.valid() || !profile.valid() || !r.acceptedAckModes.IsZero() && !r.acceptedAckModes.Contains(profile.AckMode()) {
		return false
	}
	return r.protectedFailures.IsZero() || profile.AcknowledgedLoss() == AcknowledgedLossExcludedForDeclaredFailures && profile.FailureModel().ContainsAll(r.protectedFailures)
}

func combineDurabilityRequirements(left, right DurabilityRequirement) (DurabilityRequirement, error) {
	if !left.valid() || !right.valid() {
		return DurabilityRequirement{}, invalid("durability requirement")
	}
	accepted := left.acceptedAckModes
	if accepted.IsZero() {
		accepted = right.acceptedAckModes
	} else if !right.acceptedAckModes.IsZero() {
		accepted.bits &= right.acceptedAckModes.bits
		if accepted.IsZero() {
			return DurabilityRequirement{}, fmt.Errorf("%w: durability ack modes do not overlap", ErrConflict)
		}
	}
	protected := FailureSet{bits: left.protectedFailures.bits | right.protectedFailures.bits}
	combined, err := NewDurabilityRequirement(accepted, protected)
	if err != nil {
		return DurabilityRequirement{}, fmt.Errorf("%w: durability requirements cannot be jointly satisfied", ErrConflict)
	}
	return combined, nil
}

type DurabilityProfile struct {
	ack      AckMode
	loss     AcknowledgedLoss
	failures FailureSet
}

func NewDurabilityProfile(ack AckMode, loss AcknowledgedLoss, failures FailureSet) (DurabilityProfile, error) {
	if !ack.Valid() || !loss.Valid() || !failures.valid() || loss == AcknowledgedLossPossible && !failures.IsZero() || loss == AcknowledgedLossExcludedForDeclaredFailures && failures.IsZero() || ack == AckBeforePersistence && loss != AcknowledgedLossPossible {
		return DurabilityProfile{}, invalid("durability profile")
	}
	return DurabilityProfile{ack: ack, loss: loss, failures: failures}, nil
}

func (p DurabilityProfile) AckMode() AckMode                   { return p.ack }
func (p DurabilityProfile) AcknowledgedLoss() AcknowledgedLoss { return p.loss }
func (p DurabilityProfile) FailureModel() FailureSet           { return p.failures }
func (p DurabilityProfile) IsZero() bool                       { return p.ack == 0 }
func (p DurabilityProfile) String() string                     { return "[job durability profile]" }
func (p DurabilityProfile) Format(state fmt.State, _ rune)     { _, _ = fmt.Fprint(state, p.String()) }
func (p DurabilityProfile) valid() bool {
	return p.ack.Valid() && p.loss.Valid() && p.failures.valid() && (p.loss == AcknowledgedLossPossible && p.failures.IsZero() || p.loss == AcknowledgedLossExcludedForDeclaredFailures && !p.failures.IsZero()) && (p.ack != AckBeforePersistence || p.loss == AcknowledgedLossPossible)
}

type Capabilities struct {
	Priority         bool
	Debounce         bool
	Scheduled        bool
	AttemptTrace     bool
	OrderedPartition bool
	ServerSideWakeup bool
}

func (c Capabilities) String() string                 { return "[job backend capabilities]" }
func (c Capabilities) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, c.String()) }
func (c Capabilities) satisfies(required Capabilities) bool {
	return (!required.Priority || c.Priority) &&
		(!required.Debounce || c.Debounce) &&
		(!required.Scheduled || c.Scheduled) &&
		(!required.AttemptTrace || c.AttemptTrace) &&
		(!required.OrderedPartition || c.OrderedPartition) &&
		(!required.ServerSideWakeup || c.ServerSideWakeup)
}

type ProducerRequirements struct {
	capabilities Capabilities
	explicit     bool
}

func StandardProducerRequirements() ProducerRequirements {
	return ProducerRequirements{capabilities: Capabilities{Priority: true, Debounce: true, Scheduled: true}, explicit: true}
}

func RequireProducerCapabilities(capabilities Capabilities) ProducerRequirements {
	return ProducerRequirements{capabilities: capabilities, explicit: true}
}

func ProducerCoreOnly() ProducerRequirements {
	return RequireProducerCapabilities(Capabilities{})
}

func (r ProducerRequirements) Capabilities() Capabilities { return r.capabilities }
func (r ProducerRequirements) IsZero() bool               { return !r.explicit }
func (r ProducerRequirements) String() string             { return "[job producer requirements]" }
func (r ProducerRequirements) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r ProducerRequirements) valid() bool { return r.explicit }

type BackendDescription struct {
	id           BackendID
	durability   DurabilityProfile
	capabilities Capabilities
}

func NewBackendDescription(id BackendID, durability DurabilityProfile, capabilities Capabilities) (BackendDescription, error) {
	if !id.valid() || !durability.valid() {
		return BackendDescription{}, invalid("backend description")
	}
	return BackendDescription{id: id, durability: durability, capabilities: capabilities}, nil
}

func (d BackendDescription) ID() BackendID                 { return d.id }
func (d BackendDescription) Durability() DurabilityProfile { return d.durability }
func (d BackendDescription) Capabilities() Capabilities    { return d.capabilities }
func (d BackendDescription) IsZero() bool                  { return d.id.IsZero() }
func (d BackendDescription) String() string                { return "[job backend description]" }
func (d BackendDescription) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d BackendDescription) valid() bool { return d.id.valid() && d.durability.valid() }

type TransactionBinding struct{ value [32]byte }

func TransactionBindingFromBytes(value [32]byte) (TransactionBinding, error) {
	if value == [32]byte{} {
		return TransactionBinding{}, invalid("transaction binding is zero")
	}
	return TransactionBinding{value: value}, nil
}

func (b TransactionBinding) Bytes() [32]byte { return b.value }
func (b TransactionBinding) IsZero() bool    { return b.value == [32]byte{} }
func (b TransactionBinding) String() string  { return "[job transaction binding]" }
func (b TransactionBinding) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, b.String())
}
func (TransactionBinding) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: transaction binding cannot be serialized", ErrUnsupported)
}
func (b TransactionBinding) valid() bool { return !b.IsZero() }

type TransactionContext struct {
	backend    BackendID
	binding    TransactionBinding
	durability DurabilityProfile
}

func NewTransactionContext(backend BackendID, binding TransactionBinding, durability DurabilityProfile) (TransactionContext, error) {
	if !backend.valid() || !binding.valid() || !durability.valid() {
		return TransactionContext{}, invalid("transaction context")
	}
	return TransactionContext{backend: backend, binding: binding, durability: durability}, nil
}

func (c TransactionContext) Backend() BackendID             { return c.backend }
func (c TransactionContext) Binding() TransactionBinding    { return c.binding }
func (c TransactionContext) Durability() DurabilityProfile  { return c.durability }
func (c TransactionContext) IsZero() bool                   { return c.backend.IsZero() }
func (c TransactionContext) String() string                 { return "[job transaction context]" }
func (c TransactionContext) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, c.String()) }
func (TransactionContext) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: transaction context cannot be serialized", ErrUnsupported)
}
func (c TransactionContext) valid() bool {
	return c.backend.valid() && c.binding.valid() && c.durability.valid()
}

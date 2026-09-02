package cache

import (
	"context"
	"fmt"
	"sort"
)

const (
	MaxCapabilityBytes      = 64
	MaxDeclaredCapabilities = 32
	MaxTagBytes             = 128
)

type Capability string

const (
	BatchReadCapability       Capability = "batch_read"
	CompareAndSwapCapability  Capability = "compare_and_swap"
	MaintenanceCapability     Capability = "maintenance"
	HealthCapability          Capability = "health"
	TagInvalidationCapability Capability = "tag_invalidation"
	TransactionCapability     Capability = "transaction"
)

func (this Capability) valid() bool {
	if this == "" || len(this) > MaxCapabilityBytes || this[0] == '_' || this[len(this)-1] == '_' {
		return false
	}
	for _, character := range string(this) {
		if character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

type BatchReader interface {
	GetMany(context.Context, []Address, BatchReadLimit) (map[Address][]byte, error)
}

type CompareAndSwapper interface {
	CompareAndSwap(ctx context.Context, address Address, expected, next []byte, expiry Expiry) (bool, error)
}

type MaintenanceLimit struct {
	MaxItems int
	MaxBytes int64
}

type MaintenanceReport struct {
	Removed int
	More    bool
}

type Maintainer interface {
	DeleteExpired(context.Context, MaintenanceLimit) (MaintenanceReport, error)
}

type HealthChecker interface {
	CheckBackend(context.Context) error
}

type Tag struct {
	value string
}

func NewTag(value string) (Tag, error) {
	if value == "" || len(value) > MaxTagBytes {
		return Tag{}, failure("build tag", fmt.Errorf("%w: tag length is invalid", ErrInvalid))
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return Tag{}, failure("build tag", fmt.Errorf("%w: tag contains a control or non-ASCII character", ErrInvalid))
		}
	}
	return Tag{value: value}, nil
}

func (this Tag) Value() string  { return this.value }
func (this Tag) IsZero() bool   { return this.value == "" }
func (this Tag) String() string { return "[cache tag]" }

type TagInvalidator interface {
	InvalidateTag(context.Context, Namespace, Tag) error
}

type Transactional interface {
	InTransaction(context.Context, func(context.Context, Backend) error) error
}

type CapabilityDeclarer interface {
	DeclaredCapabilities() []Capability
}

func BatchReaderOf(backend Backend) (BatchReader, bool) {
	return CapabilityOf[BatchReader](backend)
}

func CompareAndSwapperOf(backend Backend) (CompareAndSwapper, bool) {
	return CapabilityOf[CompareAndSwapper](backend)
}

func MaintainerOf(backend Backend) (Maintainer, bool) {
	return CapabilityOf[Maintainer](backend)
}

func HealthCheckerOf(backend Backend) (HealthChecker, bool) {
	return CapabilityOf[HealthChecker](backend)
}

func TagInvalidatorOf(backend Backend) (TagInvalidator, bool) {
	return CapabilityOf[TagInvalidator](backend)
}

func TransactionalOf(backend Backend) (Transactional, bool) {
	return CapabilityOf[Transactional](backend)
}

func Supports(backend Backend, capability Capability) bool {
	if !capability.valid() {
		return false
	}
	if probe, builtin := builtinCapabilityProbe(capability); builtin {
		return probe(backend)
	}
	for _, declared := range DeclaredCapabilitiesOf(backend) {
		if declared == capability {
			return true
		}
	}
	return false
}

func builtinCapabilityProbe(capability Capability) (func(Backend) bool, bool) {
	switch capability {
	case BatchReadCapability:
		return func(backend Backend) bool { _, ok := BatchReaderOf(backend); return ok }, true
	case CompareAndSwapCapability:
		return func(backend Backend) bool { _, ok := CompareAndSwapperOf(backend); return ok }, true
	case MaintenanceCapability:
		return func(backend Backend) bool { _, ok := MaintainerOf(backend); return ok }, true
	case HealthCapability:
		return func(backend Backend) bool { _, ok := HealthCheckerOf(backend); return ok }, true
	case TagInvalidationCapability:
		return func(backend Backend) bool { _, ok := TagInvalidatorOf(backend); return ok }, true
	case TransactionCapability:
		return func(backend Backend) bool { _, ok := TransactionalOf(backend); return ok }, true
	default:
		return nil, false
	}
}

func DeclaredCapabilitiesOf(backend Backend) []Capability {
	collected := make([]Capability, 0, MaxDeclaredCapabilities)
	seen := make(map[Capability]struct{}, MaxDeclaredCapabilities)
	current := backend
	visited := make(map[backendIdentity]struct{})
	for depth := 0; depth < 64 && !nilInterface(current) && len(collected) < MaxDeclaredCapabilities; depth++ {
		if repeatedBackend(current, visited) {
			break
		}
		if declarer, ok := current.(CapabilityDeclarer); ok && !nilInterface(declarer) {
			for _, capability := range invokeDeclaredCapabilities(declarer) {
				if _, builtin := builtinCapabilityProbe(capability); builtin || !capability.valid() {
					continue
				}
				if _, exists := seen[capability]; exists {
					continue
				}
				if len(collected) == MaxDeclaredCapabilities {
					break
				}
				seen[capability] = struct{}{}
				collected = append(collected, capability)
			}
		}
		next, found := nextBackend(current)
		if !found {
			break
		}
		current = next
	}
	sort.Slice(collected, func(left, right int) bool { return collected[left] < collected[right] })
	return collected
}

func invokeDeclaredCapabilities(declarer CapabilityDeclarer) (declared []Capability) {
	defer func() {
		if recover() != nil {
			declared = nil
		}
	}()
	declared = declarer.DeclaredCapabilities()
	if len(declared) > MaxDeclaredCapabilities {
		return nil
	}
	return declared
}

func CapabilityOf[T any](backend Backend) (T, bool) {
	var zero T
	current := backend
	seen := make(map[backendIdentity]struct{})
	for depth := 0; depth < 64 && !nilInterface(current); depth++ {
		if repeatedBackend(current, seen) {
			return zero, false
		}
		capability, ok := any(current).(T)
		if ok && !nilInterface(capability) {
			return capability, true
		}
		next, found := nextBackend(current)
		if !found {
			return zero, false
		}
		current = next
	}
	return zero, false
}

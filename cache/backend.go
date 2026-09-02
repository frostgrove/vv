package cache

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"
)

type ReadLimit struct {
	MaxBytes int
}

type BatchReadLimit struct {
	MaxItems      int
	MaxItemBytes  int
	MaxTotalBytes int64
}

type ExpiryMode uint8

const (
	RelativeExpiry ExpiryMode = iota + 1
	CapacityOnlyExpiry
)

type Expiry struct {
	Mode      ExpiryMode
	RetainFor time.Duration
	deadline  time.Time
}

type Backend interface {
	Get(context.Context, Address, ReadLimit) ([]byte, bool, error)
	Put(context.Context, Address, []byte, Expiry) error
	Delete(context.Context, Address) error
}

type BackendWrapper interface {
	Backend
	Next() Backend
}

type BackendTopology uint8

const (
	ProcessBackend BackendTopology = iota + 1
	SharedBackend
)

type ExpiryClock uint8

const (
	ProcessExpiryClock ExpiryClock = iota + 1
	ServerExpiryClock
)

type BackendDescription struct {
	Name              string
	Topology          BackendTopology
	ExpiryClock       ExpiryClock
	MaxItemBytes      int
	RelativeExpiry    bool
	MaxRelativeExpiry time.Duration
	CapacityBounded   bool
}

type BackendDescriber interface {
	DescribeBackend() BackendDescription
}

func BackendDescriptionOf(backend Backend) (description BackendDescription, ok bool) {
	current := backend
	seen := make(map[backendIdentity]struct{})
	for depth := 0; depth < 64 && !nilInterface(current); depth++ {
		if repeatedBackend(current, seen) {
			return BackendDescription{}, false
		}
		if describer, found := current.(BackendDescriber); found && !nilInterface(describer) {
			return invokeBackendDescription(describer)
		}
		next, found := nextBackend(current)
		if !found {
			return BackendDescription{}, false
		}
		current = next
	}
	return BackendDescription{}, false
}

type backendIdentity struct {
	typeName string
	pointer  uintptr
}

func repeatedBackend(backend Backend, seen map[backendIdentity]struct{}) bool {
	value := reflect.ValueOf(backend)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		identity := backendIdentity{typeName: value.Type().String(), pointer: value.Pointer()}
		if _, exists := seen[identity]; exists {
			return true
		}
		seen[identity] = struct{}{}
	}
	return false
}

func nextBackend(backend Backend) (next Backend, ok bool) {
	defer func() {
		if recover() != nil {
			next = nil
			ok = false
		}
	}()
	wrapper, ok := backend.(BackendWrapper)
	if !ok || nilInterface(wrapper) {
		return nil, false
	}
	next = wrapper.Next()
	return next, !nilInterface(next)
}

func invokeBackendDescription(describer BackendDescriber) (description BackendDescription, ok bool) {
	defer func() {
		if recover() != nil {
			description = BackendDescription{}
			ok = false
		}
	}()
	description = describer.DescribeBackend()
	return description, validBackendDescription(description) == nil
}

func validReadLimit(limit ReadLimit) error {
	if limit.MaxBytes <= 0 {
		return fmt.Errorf("%w: read limit must be positive", ErrInvalid)
	}
	return nil
}

func validBatchReadLimit(limit BatchReadLimit) error {
	if limit.MaxItems <= 0 || limit.MaxItemBytes <= 0 || limit.MaxTotalBytes < int64(limit.MaxItemBytes) {
		return fmt.Errorf("%w: batch read limits are invalid", ErrInvalid)
	}
	return nil
}

func validBackendDescription(description BackendDescription) error {
	if description.Name == "" || len(description.Name) > MaxNamespacePartBytes || strings.TrimSpace(description.Name) != description.Name || description.MaxItemBytes <= 0 ||
		description.Topology < ProcessBackend || description.Topology > SharedBackend ||
		description.ExpiryClock < ProcessExpiryClock || description.ExpiryClock > ServerExpiryClock {
		return fmt.Errorf("%w: backend description is invalid", ErrInvalid)
	}
	for _, character := range description.Name {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("%w: backend name is invalid", ErrInvalid)
		}
	}
	if (description.Topology == ProcessBackend) != (description.ExpiryClock == ProcessExpiryClock) {
		return fmt.Errorf("%w: backend topology and expiry clock disagree", ErrInvalid)
	}
	if description.RelativeExpiry {
		if description.MaxRelativeExpiry <= 0 {
			return fmt.Errorf("%w: backend relative expiry range is invalid", ErrInvalid)
		}
	} else if description.MaxRelativeExpiry != 0 {
		return fmt.Errorf("%w: backend relative expiry range is invalid", ErrInvalid)
	}
	if !description.RelativeExpiry && !description.CapacityBounded {
		return fmt.Errorf("%w: backend supports no expiry mode", ErrInvalid)
	}
	return nil
}

func validExpiry(expiry Expiry) error {
	switch expiry.Mode {
	case RelativeExpiry:
		if expiry.RetainFor <= 0 {
			return fmt.Errorf("%w: relative expiry must be positive", ErrInvalid)
		}
	case CapacityOnlyExpiry:
		if expiry.RetainFor != 0 {
			return fmt.Errorf("%w: capacity-only expiry has a duration", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: expiry mode is invalid", ErrInvalid)
	}
	return nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

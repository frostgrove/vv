package nilvalue_test

import (
	"testing"
	"unsafe"

	"github.com/frostgrove/vv/internal/nilvalue"
)

func TestIsRecognisesEveryNilableDynamicKind(t *testing.T) {
	var (
		pointer  *int
		function func()
		mapping  map[string]int
		slice    []int
		channel  chan int
		pointerU unsafe.Pointer
	)
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"an untyped nil", nil},
		{"a typed-nil pointer", pointer},
		{"a typed-nil function", function},
		{"a typed-nil map", mapping},
		{"a typed-nil slice", slice},
		{"a typed-nil channel", channel},
		{"a typed-nil unsafe pointer", pointerU},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !nilvalue.Is(tc.value) {
				t.Fatalf("Is(%T) = false for a nil-like value", tc.value)
			}
		})
	}
}

func TestIsDoesNotCallNonNilOrNonNilableValuesNil(t *testing.T) {
	number := 1
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"a pointer", new(int)},
		{"a function", func() {}},
		{"a map", map[string]int{}},
		{"a slice", []int{}},
		{"a channel", make(chan int)},
		{"an unsafe pointer", unsafe.Pointer(&number)},
		{"an integer", 0},
		{"a string", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if nilvalue.Is(tc.value) {
				t.Fatalf("Is(%T) = true for a present value", tc.value)
			}
		})
	}
}

type typedNilContract interface {
	isTypedNilContract()
}

type typedNilImplementation struct{}

func (*typedNilImplementation) isTypedNilContract() {}

func TestIsRecognisesATypedNilImplementationHeldByANonEmptyInterface(t *testing.T) {
	var implementation *typedNilImplementation
	var contract typedNilContract = implementation
	if !nilvalue.Is(contract) {
		t.Fatal("a typed-nil implementation held by a non-empty interface was not nil-like")
	}
}

func TestIsDoesNotFollowPointerToInterfaceCycles(t *testing.T) {
	var cycle any
	cycle = &cycle
	if nilvalue.Is(cycle) {
		t.Fatal("a non-nil pointer-to-interface cycle was mistaken for nil")
	}
}

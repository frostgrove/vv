package apikey_test

import (
	"reflect"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/apikey"
)

func TestTryStaticCopiesSupportedCyclesForEveryLookup(t *testing.T) {
	t.Run("pointer -> struct -> pointer", func(t *testing.T) {
		declared := &cyclicAttributeNode{Value: "declared"}
		declared.Next = declared
		firstClaims, secondClaims := staticCycleLookups(t, declared)
		first, firstOK := firstClaims.Attrs["cycle"].(*cyclicAttributeNode)
		second, secondOK := secondClaims.Attrs["cycle"].(*cyclicAttributeNode)
		if !firstOK || !secondOK {
			t.Fatalf("TryStatic changed pointer cycle types to %T and %T", firstClaims.Attrs["cycle"], secondClaims.Attrs["cycle"])
		}
		if first == declared || second == declared || first == second {
			t.Fatal("a pointer cycle shares identity with its declaration or another lookup")
		}
		if first.Next != first || second.Next != second {
			t.Fatal("a cloned pointer cycle no longer points to itself")
		}

		first.Value = "request-one"
		if declared.Value != "declared" || second.Value != "declared" {
			t.Fatal("mutating one pointer cycle changed its declaration or another lookup")
		}
	})

	t.Run("slice -> interface -> slice", func(t *testing.T) {
		declared := make([]any, 1)
		declared[0] = declared
		firstClaims, secondClaims := staticCycleLookups(t, declared)
		first, firstOK := firstClaims.Attrs["cycle"].([]any)
		second, secondOK := secondClaims.Attrs["cycle"].([]any)
		if !firstOK || !secondOK {
			t.Fatalf("TryStatic changed slice cycle types to %T and %T", firstClaims.Attrs["cycle"], secondClaims.Attrs["cycle"])
		}
		declaredPointer := reflect.ValueOf(declared).Pointer()
		firstPointer := reflect.ValueOf(first).Pointer()
		secondPointer := reflect.ValueOf(second).Pointer()
		if firstPointer == declaredPointer || secondPointer == declaredPointer || firstPointer == secondPointer {
			t.Fatal("a slice cycle shares backing storage with its declaration or another lookup")
		}
		firstLoop, firstOK := first[0].([]any)
		secondLoop, secondOK := second[0].([]any)
		if !firstOK || !secondOK || reflect.ValueOf(firstLoop).Pointer() != firstPointer || reflect.ValueOf(secondLoop).Pointer() != secondPointer {
			t.Fatal("a cloned slice cycle no longer points through its interface to itself")
		}

		firstLoop[0] = "request-one"
		declaredLoop, declaredOK := declared[0].([]any)
		secondLoop, secondOK = second[0].([]any)
		if !declaredOK || !secondOK || reflect.ValueOf(declaredLoop).Pointer() != declaredPointer || reflect.ValueOf(secondLoop).Pointer() != secondPointer {
			t.Fatal("mutating one slice cycle changed its declaration or another lookup")
		}
	})

	t.Run("pointer -> interface -> pointer", func(t *testing.T) {
		var declaredValue any
		declaredValue = &declaredValue
		declared := declaredValue.(*any)
		firstClaims, secondClaims := staticCycleLookups(t, declaredValue)
		first, firstOK := firstClaims.Attrs["cycle"].(*any)
		second, secondOK := secondClaims.Attrs["cycle"].(*any)
		if !firstOK || !secondOK {
			t.Fatalf("TryStatic changed interface cycle types to %T and %T", firstClaims.Attrs["cycle"], secondClaims.Attrs["cycle"])
		}
		if first == declared || second == declared || first == second {
			t.Fatal("an interface cycle shares identity with its declaration or another lookup")
		}
		firstLoop, firstOK := (*first).(*any)
		secondLoop, secondOK := (*second).(*any)
		if !firstOK || !secondOK || firstLoop != first || secondLoop != second {
			t.Fatal("a cloned interface cycle no longer points to itself")
		}

		*first = "request-one"
		declaredLoop, declaredOK := (*declared).(*any)
		secondLoop, secondOK = (*second).(*any)
		if !declaredOK || !secondOK || declaredLoop != declared || secondLoop != second {
			t.Fatal("mutating one interface cycle changed its declaration or another lookup")
		}
	})
}

func staticCycleLookups(t *testing.T, cycle any) (auth.Claims, auth.Claims) {
	t.Helper()
	store, err := apikey.TryStatic(map[string]auth.Principal{
		"k-1": auth.Claims{Sub: "batch", Attrs: map[string]any{"cycle": cycle}},
	})
	if err != nil {
		t.Fatalf("TryStatic refused a supported cycle: %v", err)
	}
	lookup := func() auth.Claims {
		principal, ok, err := store.Lookup(t.Context(), "k-1")
		if err != nil || !ok {
			t.Fatalf("looking up a cyclic principal: %v %v", ok, err)
		}
		claims, ok := principal.(auth.Claims)
		if !ok {
			t.Fatalf("TryStatic changed Claims into %T", principal)
		}
		return claims
	}
	return lookup(), lookup()
}

type cyclicAttributeNode struct {
	Value string
	Next  *cyclicAttributeNode
}

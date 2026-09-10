package vvotel_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frostgrove/vv/otel"
)

func TestApprovedNameAcceptsOnlyTheGeneratedDeclaration(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "ascii", value: "orders.primary-1", valid: true},
		{name: "unicode", value: "склад_2", valid: true},
		{name: "exact_byte_limit", value: strings.Repeat("a", vvotel.MaxResourceNameBytes), valid: true},
		{name: "empty", value: ""},
		{name: "too_long", value: strings.Repeat("a", vvotel.MaxResourceNameBytes+1)},
		{name: "space", value: "orders primary"},
		{name: "slash", value: "orders/primary"},
		{name: "invalid_utf8", value: string([]byte{0xff})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, err := vvotel.ApproveName(test.value)
			if test.valid {
				if err != nil || !name.Valid() || name.Value() != test.value {
					t.Fatalf("ApproveName(%q) = %q, %v", test.value, name.Value(), err)
				}
				if !vvotel.ValidResourceName(name.Value()) {
					t.Fatal("approved name disagrees with generated admission")
				}
				return
			}
			if !errors.Is(err, vvotel.ErrInvalidApprovedName) || name != "" {
				t.Fatalf("ApproveName(%q) = %q, %v", test.value, name.Value(), err)
			}
			if strings.Contains(fmt.Sprint(err), test.value) && test.value != "" {
				t.Fatal("approval error contains the rejected value")
			}
		})
	}
}

func TestApprovedNameDoesNotNormalizeOrTrustACast(t *testing.T) {
	for _, value := range []string{" orders ", "orders/primary", strings.Repeat("a", vvotel.MaxResourceNameBytes+1)} {
		if name, err := vvotel.ApproveName(value); err == nil || name != "" {
			t.Fatalf("invalid value %q was normalized or admitted", value)
		}
		forged := vvotel.ApprovedName(value)
		if forged.Valid() {
			t.Fatalf("forged value %q became valid", value)
		}
	}
	if vvotel.ApprovedName("").Valid() {
		t.Fatal("the unset zero value is not an approved declaration")
	}
}

func TestMustApproveNamePanicsWithoutDisclosingTheValue(t *testing.T) {
	secret := "orders/secret/customer-123"
	defer func() {
		value := recover()
		err, ok := value.(error)
		if !ok || !errors.Is(err, vvotel.ErrInvalidApprovedName) {
			t.Fatalf("unexpected panic: %v", value)
		}
		if strings.Contains(fmt.Sprint(value), secret) {
			t.Fatal("approval panic contains the rejected value")
		}
	}()
	vvotel.MustApproveName(secret)
}

func FuzzApproveNameMatchesGeneratedAdmission(f *testing.F) {
	for _, seed := range []string{"orders", "склад_2", "", "tenant/secret", strings.Repeat("a", vvotel.MaxResourceNameBytes)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		name, err := vvotel.ApproveName(value)
		valid := vvotel.ValidResourceName(value)
		if valid {
			if err != nil || name.Value() != value || !name.Valid() {
				t.Fatalf("valid generated name was rejected: name=%q err=%v", name.Value(), err)
			}
			return
		}
		if !errors.Is(err, vvotel.ErrInvalidApprovedName) || name != "" {
			t.Fatalf("invalid generated name was admitted: name=%q err=%v", name.Value(), err)
		}
	})
}

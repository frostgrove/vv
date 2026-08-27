package utils_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/utils"
)

func TestOptionalStatesAndSharedHelpers(t *testing.T) {
	var undefined utils.Opt[int]
	if undefined.IsDefined() || undefined.IsNull() || undefined.IsSet() {
		t.Fatalf("zero Opt has the wrong state: %v", undefined)
	}
	if value, defined, null, ok := utils.Inspect(utils.Set(7)); !ok || !defined || null || value != 7 {
		t.Fatalf("Inspect(Set(7)) = (%v, %v, %v, %v)", value, defined, null, ok)
	}
	if _, defined, null, ok := utils.Inspect(utils.Null[int]()); !ok || !defined || !null {
		t.Fatalf("Inspect(Null) = (defined=%v, null=%v, ok=%v)", defined, null, ok)
	}
	if value, defined, null, ok := utils.Inspect(7); ok || defined || null || value != nil {
		t.Fatalf("Inspect(non-Opt) = (%v, %v, %v, %v)", value, defined, null, ok)
	}

	p := utils.Ptr("x")
	if p == nil || *p != "x" {
		t.Fatalf("Ptr = %v", p)
	}
	if got := utils.Must(7, nil); got != 7 {
		t.Fatalf("Must = %d", got)
	}
	want := errors.New("boom")
	defer func() {
		if got := recover(); !errors.Is(asError(got), want) {
			t.Fatalf("Must panic = %v, want %v", got, want)
		}
	}()
	_ = utils.Must(0, want)
}

func asError(v any) error {
	err, _ := v.(error)
	return err
}

func TestOptTypeIntrospection(t *testing.T) {
	optType := reflect.TypeFor[utils.Opt[string]]()
	if !utils.IsOptType(optType) || utils.OptElem(optType) != reflect.TypeFor[string]() {
		t.Fatalf("Opt type introspection failed for %v", optType)
	}
	if utils.IsOptType(reflect.TypeFor[string]()) || utils.OptElem(reflect.TypeFor[string]()) != nil {
		t.Fatal("string became an Opt")
	}
}

package security

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
)

type pointerOnlyValue struct {
	text  string
	valid bool
}

func (this *pointerOnlyValue) Value() (driver.Value, error) {
	if !this.valid {
		return nil, nil
	}
	return this.text, nil
}

type nilBytesValue struct{}

func (nilBytesValue) Value() (driver.Value, error) { return []byte(nil), nil }

type failedValue struct{ err error }

func (this failedValue) Value() (driver.Value, error) { return nil, this.err }

func TestSnapshotValueCanonicalisesEverySupportedValuerNullShape(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		null  bool
		want  any
	}{
		{"pointer-only null", pointerOnlyValue{}, true, nil},
		{"pointer-only value", pointerOnlyValue{text: "kept", valid: true}, false, "kept"},
		{"typed nil pointer", (*pointerOnlyValue)(nil), true, nil},
		{"typed nil bytes", nilBytesValue{}, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, null, err := snapshotValue(tc.value)
			if err != nil || null != tc.null || got != tc.want {
				t.Fatalf("snapshotValue(%T) = %#v, null=%v, err=%v; want %#v, null=%v", tc.value, got, null, err, tc.want, tc.null)
			}
		})
	}

	boom := errors.New("value failed")
	if _, _, err := snapshotValue(failedValue{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("Valuer error = %v, want %v", err, boom)
	}
}

func TestInspectedIDRejectsNestedNonComparableDynamicValues(t *testing.T) {
	type nestedID struct{ Part any }
	type model struct {
		ID any `db:"id,pk,noauto"`
	}
	meta, err := crud.NewMeta[model]("nested_dynamic_ids")
	if err != nil {
		t.Fatal(err)
	}
	row := model{ID: nestedID{Part: []byte("not-a-map-key")}}
	if _, err := inspectedID[model, any](meta, &row); err == nil || !strings.Contains(err.Error(), "cannot key an atomic snapshot") {
		t.Fatalf("inspectedID(nested interface) = %v, want a fail-closed SchemaError", err)
	}
}

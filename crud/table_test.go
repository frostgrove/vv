package crud_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
)

type core028Model struct {
	ID int64 `db:"id,pk,auto"`
}

type core028Registered struct {
	ID int64 `db:"id,pk,auto"`
}

type core028DottedTabler struct {
	ID int64 `db:"id,pk,auto"`
}

func (core028DottedTabler) TableName() string { return "analytics.events" }

func TestTableRefQuotesEveryExactComponent(t *testing.T) {
	ref, err := crud.NewTableRefInSchema(`ana.ly"tics`, "event.s")
	if err != nil {
		t.Fatal(err)
	}
	if got := ref.String(); got != `ana.ly"tics.event.s` {
		t.Fatalf("diagnostic spelling = %q", got)
	}

	tests := []struct {
		name string
		d    crud.Dialect
		want string
	}{
		{"postgres", crud.Postgres{}, `"ana.ly""tics"."event.s"`},
		{"mysql", crud.MySQL{}, "`ana.ly\"tics`.`event.s`"},
		{"sqlite", crud.SQLite{}, `"ana.ly""tics"."event.s"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, err := crud.NewMetaRef[core028Model](ref)
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := crud.NewSQL(test.d, m).TableRef(ref).Done()
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("quoted table = %s, want %s", got, test.want)
			}
		})
	}

	parts := ref.Components()
	parts[0] = "retargeted"
	if ref.Schema != `ana.ly"tics` {
		t.Fatal("Components exposed mutable TableRef storage")
	}
}

func TestOneStringTableDeclarationsRefuseDotsAndInvalidComponents(t *testing.T) {
	if _, err := crud.NewTableRef("analytics.events"); err == nil || !strings.Contains(err.Error(), "separate components") {
		t.Fatalf("dotted NewTableRef error = %v", err)
	}
	if _, err := crud.NewMeta[core028Model]("analytics.events"); err == nil ||
		!strings.Contains(err.Error(), "DefineInSchema") {
		t.Fatalf("dotted NewMeta error = %v", err)
	}
	if _, err := crud.NewMeta[core028DottedTabler](""); err == nil ||
		!strings.Contains(err.Error(), "dotted string") {
		t.Fatalf("dotted TableName error = %v", err)
	}

	for _, ref := range []crud.TableRef{
		{},
		{Name: "events\x00archive"},
		{Schema: "analytics\x00archive", Name: "events"},
	} {
		if err := ref.Validate(); err == nil {
			t.Fatalf("TableRef %#v was accepted", ref)
		} else {
			var target *crud.TableRefError
			if !errors.As(err, &target) {
				t.Fatalf("validation error = %T, want *TableRefError", err)
			}
		}
	}
}

func TestMetaTableReferenceIsImmutableAfterValidation(t *testing.T) {
	m, err := crud.NewMetaInSchema[core028Model]("analytics", "events")
	if err != nil {
		t.Fatal(err)
	}
	copy := m.TableReference()
	copy.Schema = "retargeted"
	copy.Name = "other"
	m.Table = "also_retargeted"

	q, _, err := crud.NewSQL(crud.Postgres{}, m).Raw("SELECT 1 FROM ").Table().Done()
	if err != nil {
		t.Fatal(err)
	}
	if q != `SELECT 1 FROM "analytics"."events"` {
		t.Fatalf("validated Meta was retargeted: %s", q)
	}
}

func TestStructuredTableRegistrationRetainsTheWholePhysicalIdentity(t *testing.T) {
	ref, err := crud.NewTableRefInSchema("analytics", "events")
	if err != nil {
		t.Fatal(err)
	}
	if err := crud.TryRegisterTableRef[core028Registered](ref); err != nil {
		t.Fatal(err)
	}
	got, err := crud.TableRefOf(reflect.TypeFor[core028Registered]())
	if err != nil {
		t.Fatal(err)
	}
	if got != ref || crud.TableNameOf(reflect.TypeFor[core028Registered]()) != "analytics.events" {
		t.Fatalf("registered ref = %#v / %q", got, crud.TableNameOf(reflect.TypeFor[core028Registered]()))
	}
	if err := crud.TryRegisterTable[core028Registered]("analytics.events"); err == nil ||
		!strings.Contains(err.Error(), "separate components") {
		t.Fatalf("dotted string registration = %v", err)
	}
}

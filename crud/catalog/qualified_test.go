package catalog

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/crud"
)

func TestQualifiedLookupsKeepSameNamedPostgresTablesAndForeignKeysSeparate(t *testing.T) {
	fk := func(schema, table, name, col, refSchema, refTable, refCol string) []any {
		return []any{schema, table, name, "f", false, 1, col, refCol,
			refSchema, refTable, "NO ACTION", "NO ACTION", "foreign key"}
	}
	con := func(schema, table, name string) []any {
		row := pgConstraintRow(table, name, "p", 1, "id")
		row[0] = schema
		return row
	}
	s := pgSchema{
		columns: [][]any{
			pgColumnRowInSchema("public", "events", "id", 1, true),
			pgColumnRowInSchema("public", "event_notes", "id", 1, true),
			pgColumnRowInSchema("public", "event_notes", "event_id", 2, true),
			pgColumnRowInSchema("analytics", "events", "id", 1, false),
			pgColumnRowInSchema("analytics", "event_notes", "id", 1, false),
			pgColumnRowInSchema("analytics", "event_notes", "event_id", 2, false),
		},
		constraints: [][]any{
			con("public", "events", "events_pkey"),
			con("public", "event_notes", "event_notes_pkey"),
			fk("public", "event_notes", "event_notes_event_fk", "event_id", "public", "events", "id"),
			con("analytics", "events", "events_pkey"),
			con("analytics", "event_notes", "event_notes_pkey"),
			fk("analytics", "event_notes", "event_notes_event_fk", "event_id", "analytics", "events", "id"),
		},
	}

	cat, err := Load(context.Background(), recorder(s, 1))
	if err != nil {
		t.Fatal(err)
	}
	bare, ok := cat.Table("events")
	if !ok || bare.Schema != "public" {
		t.Fatalf("bare events resolved to %+v, want the pg_table_is_visible public table", bare)
	}
	bareConstraint, ok := cat.Constraint("events", "events_pkey")
	if !ok || bareConstraint.Schema != "public" {
		t.Fatalf("bare constraint resolved to %+v, want public", bareConstraint)
	}

	qualified, ok := cat.(QualifiedCatalog)
	if !ok {
		t.Fatal("Load did not expose qualified lookup")
	}
	analytics := crud.TableRef{Schema: "analytics", Name: "events"}
	table, ok := qualified.TableByRef(analytics)
	if !ok || table.Schema != "analytics" {
		t.Fatalf("analytics.events resolved to %+v", table)
	}
	constraint, ok := qualified.ConstraintByRef(analytics, "events_pkey")
	if !ok || constraint.Schema != "analytics" {
		t.Fatalf("analytics.events constraint resolved to %+v", constraint)
	}
	if _, ok := qualified.TableByRef(crud.TableRef{Schema: "missing", Name: "events"}); ok {
		t.Fatal("a missing qualifier fell back to bare events")
	}

	refs, ok := cat.(QualifiedReferrers)
	if !ok {
		t.Fatal("Load did not expose qualified inbound foreign-key lookup")
	}
	analyticsRefs := refs.ReferencedByRef(analytics)
	if len(analyticsRefs) != 1 || analyticsRefs[0].Schema != "analytics" || analyticsRefs[0].RefSchema != "analytics" {
		t.Fatalf("analytics inbound refs = %+v", analyticsRefs)
	}
	publicRefs := cat.(Referrers).ReferencedBy("events")
	if len(publicRefs) != 1 || publicRefs[0].Schema != "public" || publicRefs[0].RefSchema != "public" {
		t.Fatalf("bare inbound refs = %+v, want only public", publicRefs)
	}
}

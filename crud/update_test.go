package crud_test

import (
	"testing"

	"github.com/frostgrove/vv/crud"
)

type Item struct {
	ID    int64            `db:"id,pk,auto"`
	Name  string           `db:"name"`
	Qty   int              `db:"qty"`
	Note  crud.Opt[string] `db:"note"`
	Ratio *float64         `db:"ratio"`
	Tags  []byte           `db:"tags"`
	Owner crud.Opt[int64]  `db:"owner_id"`
}

// A DTO may mix all three intent styles and rename or drop fields.
type ItemPatch struct {
	Name     *string
	Qty      int              // plain: always applied
	Note     crud.Opt[string] // three-state
	Ratio    *float64
	Tags     []byte
	Internal string `db:"-"` // not a column at all
	Holder   *int64 `db:"Owner"`
}

func plan(t *testing.T) *crud.UpdatePlan {
	t.Helper()
	s, err := crud.SchemaOf[Item]()
	if err != nil {
		t.Fatal(err)
	}
	p, err := crud.PlanFor[ItemPatch](s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func changeMap(t *testing.T, p *crud.UpdatePlan, dto any, cur *Item) map[string]any {
	t.Helper()
	changes, err := p.Changes(dto, cur)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, c := range changes {
		out[c.Field.Name] = c.Value
	}
	return out
}

func TestPlanDiffsOnlyRealChanges(t *testing.T) {
	p := plan(t)
	ratio := 1.5
	cur := Item{ID: 1, Name: "a", Qty: 2, Note: crud.Set("n"), Ratio: &ratio, Tags: []byte("x")}

	// Everything matches: nothing to write. Note that the plain Qty field is
	// always "provided" but still diffed away.
	got := changeMap(t, p, ItemPatch{Name: ptr("a"), Qty: 2, Note: crud.Set("n"), Ratio: &ratio, Tags: []byte("x")}, &cur)
	if len(got) != 0 {
		t.Fatalf("changes = %v, want none", got)
	}

	// A plain field left at its zero value is still applied, because a plain
	// field carries no "absent" state — that goes for slices too, which is why
	// an optional column wants *T or Opt[T].
	got = changeMap(t, p, ItemPatch{}, &cur)
	tags, hasTags := got["Tags"].([]byte)
	if len(got) != 2 || got["Qty"] != 0 || !hasTags || tags != nil {
		t.Fatalf("changes = %#v, want Qty -> 0 and Tags -> nil", got)
	}
}

func TestPlanNullSemantics(t *testing.T) {
	p := plan(t)
	cur := Item{ID: 1, Note: crud.Set("n"), Owner: crud.Set(int64(4))}

	if got := changeMap(t, p, ItemPatch{Note: crud.Null[string]()}, &cur); got["Note"] != nil {
		t.Fatalf("changes = %#v, want Note -> NULL", got)
	}
	if _, ok := changeMap(t, p, ItemPatch{}, &cur)["Note"]; ok {
		t.Fatal("an undefined Opt must not be written")
	}
	// A nil pointer against a set column clears it; against an already-null
	// column it is simply absent.
	if _, ok := changeMap(t, p, ItemPatch{Holder: nil}, &cur)["Owner"]; ok {
		t.Fatal("a nil pointer means absent, not NULL")
	}
	if got := changeMap(t, p, ItemPatch{Holder: ptr(int64(9))}, &cur); got["Owner"] != int64(9) {
		t.Fatalf("changes = %#v", got)
	}
}

// Slices are compared by content, not by identity, so re-sending the same bytes
// is not a write.
func TestPlanComparesSlicesByValue(t *testing.T) {
	p := plan(t)
	cur := Item{Tags: []byte("abc")}
	if got := changeMap(t, p, ItemPatch{Tags: []byte("abc")}, &cur); len(got) != 0 {
		t.Fatalf("equal bytes reported as a change: %#v", got)
	}
	got := changeMap(t, p, ItemPatch{Tags: []byte("abd")}, &cur)
	if b, ok := got["Tags"].([]byte); !ok || string(b) != "abd" {
		t.Fatalf("changes = %#v", got)
	}
}

func TestDefinedFields(t *testing.T) {
	s, err := crud.SchemaOf[Item]()
	if err != nil {
		t.Fatal(err)
	}
	got, err := crud.DefinedFields(s, ItemPatch{Name: ptr("x"), Note: crud.Null[string]()})
	if err != nil {
		t.Fatal(err)
	}
	// Qty and Tags are plain, so they always count as provided.
	want := map[string]bool{"Name": true, "Qty": true, "Note": true, "Tags": true}
	if len(got) != len(want) {
		t.Fatalf("defined = %v, want %v", got, want)
	}
	for _, n := range got {
		if !want[n] {
			t.Fatalf("defined = %v, unexpected %s", got, n)
		}
	}
}

func TestPlanRejectsAForeignDTO(t *testing.T) {
	p := plan(t)
	var cur Item
	if _, err := p.Changes(struct{ Name *string }{}, &cur); err == nil {
		t.Fatal("passing the wrong DTO type must be an error")
	}
}

func ptr[T any](v T) *T { return &v }

// The probe binds values, and a name on its own binds nothing.
func TestDefinedChangesCarriesTheValuesDefinedFieldsOnlyNames(t *testing.T) {
	s, err := crud.SchemaOf[Item]()
	if err != nil {
		t.Fatal(err)
	}
	dto := ItemPatch{Name: ptr("x"), Note: crud.Null[string]()}

	names, err := crud.DefinedFields(s, dto)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := crud.DefinedChanges(s, dto)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != len(names) {
		t.Fatalf("DefinedChanges listed %d columns and DefinedFields %d: they describe the same set",
			len(changes), len(names))
	}
	got := map[string]any{}
	for _, c := range changes {
		got[c.Field.Name] = c.Value
	}
	if got["Name"] != "x" {
		t.Fatalf("Name came back as %#v, and a probe cannot bind a field name", got["Name"])
	}
	v, ok := got["Note"]
	if !ok || v != nil {
		t.Fatalf("an explicit null came back as %#v, want an entry holding nil", v)
	}
	// The control: a field the DTO left out has no value here either, so the
	// probe never binds one the statement did not write.
	if _, ok := got["Ratio"]; ok {
		t.Fatal("a field the DTO never defined turned up with a value")
	}
}

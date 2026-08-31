package probe

import (
	"strconv"
	"testing"

	"github.com/frostgrove/vv/crud/crudtest"
)

func flatAnswer(cands, rows int, hits ...[2]int) crudtest.Result {
	cells := make([]any, cands*rows)
	for i := range cells {
		cells[i] = false
	}
	for _, h := range hits {
		cells[h[0]*rows+h[1]] = true
	}
	return crudtest.Rows(cells)
}

const (
	cEmail = iota
	cTenantSlug
	cCode
	cOrgFK
	cRegionFK
)

func distinct(i int) map[string]any {
	m := insert()
	n := strconv.Itoa(i)
	m["email"], m["slug"], m["code"] = "e"+n+"@x.io", "s"+n, "C"+n
	return m
}

func TestABulkWriteAttributesEachViolationToItsRow(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 3, [2]int{cEmail, 1}, [2]int{cOrgFK, 2}))
	f := declared(t, fixture())
	request := request(conflict("", ""), rec, docMeta(t), row(distinct(0)), row(distinct(1)), row(distinct(2)))
	request.Batch = true

	got, err := f.Enrich(ctx, request)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}

	only(t, got, "unique@[1].Email", "foreign_key@[2].OrgID")
}

func TestOneBadRowInABatchAttributesToThatRowOnly(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 3, [2]int{cEmail, 2}))
	f := declared(t, fixture())
	request := request(conflict("", ""), rec, docMeta(t), row(distinct(0)), row(distinct(1)), row(distinct(2)))
	request.Batch = true

	got, err := f.Enrich(ctx, request)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@[2].Email")
}

func TestIntraPayloadDuplicatesAreFoundWithoutAStatement(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 2))
	f := declared(t, fixture())
	a, b := distinct(0), distinct(1)
	b["email"] = a["email"]
	request := request(conflict("", ""), rec, docMeta(t), row(a), row(b))
	request.Batch = true

	got, err := f.Enrich(ctx, request)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@", "unique@[0].Email", "unique@[1].Email")
	if n := len(rec.Statements()); n != 1 {
		t.Fatalf("finding duplicates inside the payload took %d statements; it takes a map", n)
	}
}

func TestABatchWithNoDuplicateProducesNone(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 2))
	f := declared(t, fixture())
	request := request(conflict("", ""), rec, docMeta(t), row(distinct(0)), row(distinct(1)))
	request.Batch = true

	got, err := f.Enrich(ctx, request)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@")
}

func TestANullKeyPartMakesARowUnkeyable(t *testing.T) {
	rec := crudtest.Postgres()

	rec.Push(flatAnswer(4, 2))
	f := declared(t, fixture())
	a, b := nulled(distinct(0), "slug"), nulled(distinct(1), "slug")
	b["email"] = a["email"]
	b["tenant_id"] = a["tenant_id"]
	request := request(conflict("", ""), rec, docMeta(t), row(a), row(b))
	request.Batch = true

	got, err := f.Enrich(ctx, request)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@", "unique@[0].Email", "unique@[1].Email")
}

func TestACompositeKeyWithNoNullPartIsKeyed(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 2))
	f := declared(t, fixture())
	a, b := distinct(0), distinct(1)
	b["slug"], b["tenant_id"] = a["slug"], a["tenant_id"]
	request := request(conflict("", ""), rec, docMeta(t), row(a), row(b))
	request.Batch = true

	got, err := f.Enrich(ctx, request)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@", "unique@[0]", "unique@[1]")
}

func TestANonComparableKeyPartMakesARowUnkeyable(t *testing.T) {
	rows := []Row{
		{Values: map[string]any{"email": []string{"a"}}},
		{Values: map[string]any{"email": []string{"b"}}},
	}
	for i, r := range rows {
		if _, ok := keyOf(r, []string{"email"}); ok {
			t.Fatalf("row %d was keyed on a slice, and two different slices key the same", i)
		}
	}

	ka, oka := keyOf(Row{Values: map[string]any{"email": "a"}}, []string{"email"})
	kb, okb := keyOf(Row{Values: map[string]any{"email": "b"}}, []string{"email"})
	if !oka || !okb {
		t.Fatal("a plain string key was refused, so the refusal above says nothing")
	}
	if ka == kb {
		t.Fatalf("two different values keyed the same: %q", ka)
	}
}

func TestTwoDifferentTuplesNeverKeyTheSame(t *testing.T) {
	a, _ := keyOf(Row{Values: map[string]any{"x": "a:b", "y": "c"}}, []string{"x", "y"})
	b, _ := keyOf(Row{Values: map[string]any{"x": "a", "y": "b:c"}}, []string{"x", "y"})
	if a == b {
		t.Fatalf("two tuples whose parts concatenate the same keyed the same: %q", a)
	}
}

func TestASingleRowWriteHasNoIntraPayloadDuplicates(t *testing.T) {
	f := declared(t, fixture())
	p := f.planFor(request(conflict("", ""), crudtest.Postgres(), docMeta(t), row(insert())))
	if n := len(f.duplicates(p)); n != 0 {
		t.Fatalf("one row was reported as duplicating something %d times", n)
	}
}

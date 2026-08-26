package probe

import (
	"strconv"
	"testing"

	"github.com/frostgrove/vv/crud/crudtest"
)

// flatAnswer builds the one result row a batch probe reads: one cell per
// constraint per row, in constraint order and then row order. hits names the
// (constraint, row) pairs that came back true.
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

// The constraints a full keyless insert plans, in catalog order.
const (
	cEmail = iota
	cTenantSlug
	cCode
	cOrgFK
	cRegionFK
)

// distinct is one row of a batch that collides with no other row of it, so a
// test that wants exactly one duplicate gets exactly one.
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
	req := request(conflict("", ""), rec, docMeta(t), row(distinct(0)), row(distinct(1)), row(distinct(2)))
	req.Batch = true

	got, err := f.Enrich(ctx, req)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	// Two violations, not three: the driver's own unnamed one is the unique
	// violation the probe named, and it was folded into it rather than listed
	// twice.
	only(t, got, "unique@[1].Email", "foreign_key@[2].OrgID")
}

// The control: one bad row attributes to that row and to no other. Without it a
// probe that stamped every violation onto row 0 passes the test above half the
// time.
func TestOneBadRowInABatchAttributesToThatRowOnly(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 3, [2]int{cEmail, 2}))
	f := declared(t, fixture())
	req := request(conflict("", ""), rec, docMeta(t), row(distinct(0)), row(distinct(1)), row(distinct(2)))
	req.Batch = true

	got, err := f.Enrich(ctx, req)
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
	b["email"] = a["email"] // the same address twice in one insert
	req := request(conflict("", ""), rec, docMeta(t), row(a), row(b))
	req.Batch = true

	got, err := f.Enrich(ctx, req)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@", "unique@[0].Email", "unique@[1].Email")
	if n := len(rec.Statements()); n != 1 {
		t.Fatalf("finding duplicates inside the payload took %d statements; it takes a map", n)
	}
}

// The control: a batch with no duplicate produces none, so the test above is
// about the duplicate and not about the probe reporting every row it sees.
func TestABatchWithNoDuplicateProducesNone(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 2))
	f := declared(t, fixture())
	req := request(conflict("", ""), rec, docMeta(t), row(distinct(0)), row(distinct(1)))
	req.Batch = true

	got, err := f.Enrich(ctx, req)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@")
}

// A NULL key part means the two rows do not collide under the default NULLS
// DISTINCT, so they are not duplicates of each other. The email beside it is the
// control: the same two rows do collide there, and that one is reported.
func TestANullKeyPartMakesARowUnkeyable(t *testing.T) {
	rec := crudtest.Postgres()
	// Only the email key, the code key and the two foreign keys survive a NULL
	// slug: the composite unique key over (tenant_id, slug) is skipped.
	rec.Push(flatAnswer(4, 2))
	f := declared(t, fixture())
	a, b := nulled(distinct(0), "slug"), nulled(distinct(1), "slug")
	b["email"] = a["email"]
	b["tenant_id"] = a["tenant_id"]
	req := request(conflict("", ""), rec, docMeta(t), row(a), row(b))
	req.Batch = true

	got, err := f.Enrich(ctx, req)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@", "unique@[0].Email", "unique@[1].Email")
}

// The control for the rule above: two rows agreeing on both halves of the
// composite key, neither NULL, are duplicates and are reported as such. The
// path is the row index alone, because a key spanning two fields has no single
// field to name.
func TestACompositeKeyWithNoNullPartIsKeyed(t *testing.T) {
	rec := crudtest.Postgres()
	rec.Push(flatAnswer(5, 2))
	f := declared(t, fixture())
	a, b := distinct(0), distinct(1)
	b["slug"], b["tenant_id"] = a["slug"], a["tenant_id"]
	req := request(conflict("", ""), rec, docMeta(t), row(a), row(b))
	req.Batch = true

	got, err := f.Enrich(ctx, req)
	if err != nil {
		t.Fatalf("the probe reported %v", err)
	}
	only(t, got, "unique@", "unique@[0]", "unique@[1]")
}

// A non-comparable key part collapses onto a per-type constant if nothing stops
// it, and then every row of the batch is a duplicate of every other —
// [[D-025]]'s open bug, one layer up.
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
	// The control: the comparable twin is keyed, and the two differ.
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

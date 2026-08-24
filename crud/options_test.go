package crud_test

import (
	"math"
	"testing"

	"github.com/shardit-io/vv/crud"
)

// Options are applied left to right, and each one only touches its own field.
func TestOptionsApplyInSequence(t *testing.T) {
	o := crud.Build(
		crud.Where(crud.Eq("Title", "Go")),
		crud.OrderBy(crud.Desc("Views")),
		crud.Select("Title", "Views"),
		crud.Page(3),
		crud.Limit(25),
		crud.Offset(7),
		crud.Distinct(),
		crud.ForUpdate(),
		crud.SkipTotal(),
		crud.Unsorted(),
		crud.Unpaged(),
	)

	if len(o.Filter) != 1 || len(o.Sort) != 1 {
		t.Fatalf("filter/sort = %d/%d, want 1/1", len(o.Filter), len(o.Sort))
	}
	if o.Sort[0].Field != "Views" || !o.Sort[0].Desc {
		t.Errorf("sort = %+v, want Views descending", o.Sort[0])
	}
	if len(o.Fields) != 2 || o.Fields[0] != "Title" || o.Fields[1] != "Views" {
		t.Errorf("fields = %v, want the projection in order", o.Fields)
	}
	if o.Page != 3 || o.Limit != 25 || o.Offset != 7 {
		t.Errorf("page/limit/offset = %d/%d/%d, want 3/25/7", o.Page, o.Limit, o.Offset)
	}
	if !o.Distinct || !o.ForUpdate || !o.NoTotal || !o.NoSort || !o.Unpaged {
		t.Errorf("flags = %+v, want all five set", o)
	}
}

func TestBuildOfNothingIsTheZeroShape(t *testing.T) {
	o := crud.Build()
	if o == nil {
		t.Fatal("Build returned nil")
	}
	if len(o.Filter) != 0 || len(o.Sort) != 0 || o.Limit != 0 || o.Unpaged {
		t.Fatalf("empty build = %+v, want the zero shape", o)
	}
	// A nil option in the list is skipped rather than dereferenced, because
	// callers assemble option slices conditionally.
	if got := crud.Build(nil, crud.Limit(5), nil).Limit; got != 5 {
		t.Fatalf("limit = %d, want the surviving option still applied around the nils", got)
	}
}

// Where ANDs; it never replaces. This is what lets a security decorator inject
// a scope that no caller option can peel off.
func TestWhereAccumulates(t *testing.T) {
	o := crud.Build(crud.Where(crud.Eq("Title", "Go")))
	o.Apply(crud.Where(crud.Gt("Views", 10)), crud.Where(nil))

	if len(o.Filter) != 2 {
		t.Fatalf("filter = %d predicates, want the second ANDed onto the first (nil ignored)", len(o.Filter))
	}
	checkRender(t, crud.Postgres{}, articleMeta(t), o.Predicate(),
		`("title" = $1 AND "views" > $2)`, []any{"Go", 10})
}

func TestPredicateFoldsTheFilter(t *testing.T) {
	if p := crud.Build().Predicate(); p != nil {
		t.Errorf("Predicate of no filters = %v, want nil so the statement gets no WHERE at all", p)
	}
	only := crud.Eq("Title", "Go")
	if got := crud.Build(crud.Where(only)).Predicate(); got != only {
		t.Error("a single filter should be handed back as it is, not wrapped in an AND node")
	}
}

func TestOrderByAppendsAndSortByReplaces(t *testing.T) {
	o := crud.Build(crud.OrderBy(crud.Desc("Views")), crud.OrderBy(crud.Asc("Title")))
	if len(o.Sort) != 2 || o.Sort[0].Field != "Views" || o.Sort[1].Field != "Title" {
		t.Fatalf("sort = %+v, want both terms in the order they were added", o.Sort)
	}

	o.Apply(crud.SortBy(crud.Asc("ID")))
	if len(o.Sort) != 1 || o.Sort[0].Field != "ID" {
		t.Fatalf("sort = %+v, want SortBy to have replaced everything", o.Sort)
	}
	o.Apply(crud.SortBy())
	if len(o.Sort) != 0 {
		t.Fatalf("sort = %+v, want an empty SortBy to clear the sort", o.Sort)
	}
}

// With replays a stored query shape into another one, so a caller can pass a
// prebuilt Options around.
func TestWithReplaysAStoredShape(t *testing.T) {
	stored := crud.Build(
		crud.Where(crud.Gt("Views", 10)),
		crud.OrderBy(crud.Asc("Title")),
		crud.Preload("Author"),
		crud.Select("Title"),
		crud.Page(2), crud.Limit(5), crud.Offset(9),
		crud.Distinct(), crud.ForUpdate(), crud.SkipTotal(), crud.Unsorted(), crud.Unpaged(),
	)

	o := crud.Build(crud.Where(crud.Eq("Title", "Go")), crud.With(stored))

	if len(o.Filter) != 2 {
		t.Fatalf("filter = %d predicates, want the caller's own plus the stored one", len(o.Filter))
	}
	checkRender(t, crud.Postgres{}, articleMeta(t), o.Predicate(),
		`("title" = $1 AND "views" > $2)`, []any{"Go", 10})
	if len(o.Sort) != 1 || len(o.Preloads) != 1 || len(o.Fields) != 1 {
		t.Errorf("sort/preloads/fields = %d/%d/%d, want 1/1/1", len(o.Sort), len(o.Preloads), len(o.Fields))
	}
	if o.Page != 2 || o.Limit != 5 || o.Offset != 9 {
		t.Errorf("page/limit/offset = %d/%d/%d, want the stored 2/5/9", o.Page, o.Limit, o.Offset)
	}
	if !o.Distinct || !o.ForUpdate || !o.NoTotal || !o.NoSort || !o.Unpaged {
		t.Errorf("flags = %+v, want the stored ones carried over", o)
	}
}

// A stored shape that says nothing about pagination must not reset it, and a
// flag already set cannot be turned back off by replaying a shape without it.
func TestWithLeavesUnsetFieldsAlone(t *testing.T) {
	o := crud.Build(crud.Page(4), crud.Limit(9), crud.Offset(3), crud.Distinct(),
		crud.With(crud.Build()), crud.With(nil))

	if o.Page != 4 || o.Limit != 9 || o.Offset != 3 || !o.Distinct {
		t.Fatalf("options = %+v, want page 4, limit 9, offset 3 and DISTINCT untouched", o)
	}
}

func TestResolved(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opts     []crud.Option
		defLimit int
		maxLimit int
		limit    int
		offset   int
		page     int
	}{
		{"no options take the repository's page size", nil, 20, 100, 20, 0, 1},
		{"an explicit limit wins", []crud.Option{crud.Limit(5)}, 20, 100, 5, 0, 1},
		{"a limit over the maximum is clamped", []crud.Option{crud.Limit(500)}, 20, 100, 100, 0, 1},
		{"no maximum means no clamp", []crud.Option{crud.Limit(500)}, 20, 0, 500, 0, 1},
		{"a zero limit falls back to the default", []crud.Option{crud.Limit(0)}, 20, 100, 20, 0, 1},
		{"a negative limit falls back too", []crud.Option{crud.Limit(-5)}, 20, 100, 20, 0, 1},
		{"page 3 skips two pages", []crud.Option{crud.Page(3), crud.Limit(10)}, 20, 100, 10, 20, 3},
		{"page 0 is the first page", []crud.Option{crud.Page(0)}, 20, 100, 20, 0, 1},
		{"a negative page is the first page", []crud.Option{crud.Page(-2)}, 20, 100, 20, 0, 1},
		{"an explicit offset overrides the page", []crud.Option{crud.Page(3), crud.Limit(10), crud.Offset(5)},
			20, 100, 10, 5, 3},
		{"unpaged drops the limit entirely where no maximum is declared",
			[]crud.Option{crud.Unpaged(), crud.Page(4), crud.Limit(10)}, 20, 0, 0, 0, 1},
		{"unpaged still honours an explicit offset", []crud.Option{crud.Unpaged(), crud.Offset(7)}, 20, 0, 0, 7, 1},
		// MaxLimit is the repository saying how much of the table one call may
		// return. Unpaged is a flag on the wire; it does not get to overrule it.
		{"a declared maximum survives unpaged",
			[]crud.Option{crud.Unpaged(), crud.Page(4), crud.Limit(10)}, 20, 100, 100, 0, 1},
		// The offset that a client's page number multiplies out to can wrap an
		// int. Saturating asks for a page past the end; wrapping used to hand
		// back page one wearing the page number that was asked for.
		{"a page number that would overflow the offset saturates",
			[]crud.Option{crud.Page(math.MaxInt), crud.Limit(20)}, 20, 100, 20, math.MaxInt, math.MaxInt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset, page := crud.Build(tc.opts...).Resolved(tc.defLimit, tc.maxLimit)
			if limit != tc.limit || offset != tc.offset || page != tc.page {
				t.Fatalf("Resolved(%d, %d) = (limit %d, offset %d, page %d), want (%d, %d, %d)",
					tc.defLimit, tc.maxLimit, limit, offset, page, tc.limit, tc.offset, tc.page)
			}
		})
	}
}

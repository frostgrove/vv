package crud_test

import (
	"encoding/json"
	"testing"

	"github.com/shardit-io/rx/crud"
)

func TestPagerArithmetic(t *testing.T) {
	// Five rows, two per page: pages 1 and 2 are full, page 3 holds the rest.
	for _, tc := range []struct {
		name       string
		items      []string
		page       int
		limit      int
		total      int64
		totalPages int
		hasPrev    bool
		hasNext    bool
	}{
		{"first page of three", []string{"a", "b"}, 1, 2, 5, 3, false, true},
		{"middle page has both neighbours", []string{"c", "d"}, 2, 2, 5, 3, true, true},
		{"last page is short and has no next", []string{"e"}, 3, 2, 5, 3, true, false},
		{"an exact multiple does not invent a trailing page", []string{"e", "f"}, 3, 2, 6, 3, true, false},
		{"a single page has no neighbours", []string{"a"}, 1, 10, 1, 1, false, false},
		{"no rows at all", nil, 1, 10, 0, 0, false, false},
		{"a filter that matched nothing on page 2", nil, 2, 10, 0, 0, true, false},
		{"an unpaged read reports one page", []string{"a", "b", "c"}, 1, 0, 3, 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := crud.NewPaginatedResponse(tc.items, tc.page, tc.limit, tc.total)
			if r.TotalPages != tc.totalPages {
				t.Errorf("totalPages = %d, want %d for %d rows at %d per page", r.TotalPages, tc.totalPages, tc.total, tc.limit)
			}
			if r.HasPrev != tc.hasPrev {
				t.Errorf("hasPrev = %v, want %v on page %d of %d", r.HasPrev, tc.hasPrev, tc.page, tc.totalPages)
			}
			if r.HasNext != tc.hasNext {
				t.Errorf("hasNext = %v, want %v on page %d of %d", r.HasNext, tc.hasNext, tc.page, tc.totalPages)
			}
			if r.Page != tc.page || r.Limit != tc.limit || r.Total != tc.total {
				t.Errorf("the pager rewrote what it was given: %+v", r)
			}
			if r.IsEmpty() != (len(tc.items) == 0) {
				t.Errorf("IsEmpty = %v with %d items", r.IsEmpty(), len(tc.items))
			}
		})
	}
}

// Asking for a page past the end is a client mistake, not a crash: the pager
// still describes the result set truthfully and offers the way back.
func TestPagerBeyondTheLastPage(t *testing.T) {
	r := crud.NewPaginatedResponse[string](nil, 9, 2, 5)
	if r.TotalPages != 3 || r.HasNext || !r.HasPrev {
		t.Fatalf("page 9 of 3 = %+v, want 3 pages, no next, a previous", r)
	}
}

// An empty page must serialise as [] rather than null, or every JavaScript
// client has to special-case it.
func TestEmptyPageMarshalsAsAnEmptyArray(t *testing.T) {
	out, err := json.Marshal(crud.NewPaginatedResponse[int](nil, 1, 10, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"items":[],"page":1,"limit":10,"total":0,"totalPages":0,"hasNext":false,"hasPrev":false}`
	if string(out) != want {
		t.Fatalf("json = %s\nwant  = %s", out, want)
	}
}

// MapPage exists so a transport can swap entities for DTOs without recomputing
// — or mistyping — the pager.
func TestMapPageKeepsThePagerAndChangesTheItems(t *testing.T) {
	type user struct{ Name string }
	src := crud.NewPaginatedResponse([]user{{"ann"}, {"bo"}}, 2, 2, 5)

	got := crud.MapPage(src, func(u user) string { return u.Name })

	if len(got.Items) != 2 || got.Items[0] != "ann" || got.Items[1] != "bo" {
		t.Fatalf("items = %v, want the mapped names in order", got.Items)
	}
	if got.Page != src.Page || got.Limit != src.Limit || got.Total != src.Total ||
		got.TotalPages != src.TotalPages || got.HasNext != src.HasNext || got.HasPrev != src.HasPrev {
		t.Fatalf("the pager changed under mapping:\ngot  %+v\nwant %+v", got, src)
	}
}

func TestMapPageOfAnEmptyPageStillMarshalsAsAnArray(t *testing.T) {
	got := crud.MapPage(crud.NewPaginatedResponse[int](nil, 1, 10, 0), func(i int) string { return "" })
	if got.Items == nil {
		t.Fatal("mapping an empty page produced a nil slice, which encodes as null")
	}
}

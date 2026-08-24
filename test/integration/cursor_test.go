//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/query"
)

// Offset paging answers "skip 10, take 10", and what those ten rows are depends
// on how many rows exist above them *at the moment the statement runs*. A cursor
// answers "the ten after this row", which nothing else writing to the table can
// change the meaning of.
//
// The tests below are about that difference, so they all interleave a write.

func cursorSeed(t *testing.T, tg egTarget, names ...string) {
	t.Helper()
	ctx := context.Background()
	rows := EgRows.Bind(tg.src)
	for i, n := range names {
		row := EgRow{ID: int64(i + 1), Name: n, Tenant: 1}
		if err := rows.Save(ctx, &row); err != nil {
			t.Fatalf("seeding %s: %v", n, err)
		}
	}
}

func namesOf(page crud.PaginatedResponse[EgRow]) []string {
	out := make([]string, 0, len(page.Items))
	for _, r := range page.Items {
		out = append(out, r.Name)
	}
	return out
}

// The headline: a row inserted above the reader shifts every offset by one, and
// the second page of an offset walk repeats a row the first page already showed.
// The same walk by cursor does not.
func TestACursorWalkIsNotDisturbedByAConcurrentInsert(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			cursorSeed(t, tg, "b", "c", "d", "e")
			rows := EgRows.Bind(tg.src)
			byName := crud.OrderBy(crud.Asc("Name"))

			// Page one, both ways.
			offsetPage, err := rows.Get(ctx, byName, crud.Limit(2))
			if err != nil {
				t.Fatal(err)
			}
			cursorPage, err := rows.Get(ctx, byName, crud.Limit(2))
			if err != nil {
				t.Fatal(err)
			}
			if got := namesOf(offsetPage); got[0] != "b" || got[1] != "c" {
				t.Fatalf("first page = %v", got)
			}
			if cursorPage.NextCursor == "" {
				t.Fatal("no cursor came back, so a client cannot switch to one")
			}

			// Somebody inserts a row that sorts above everything already read.
			if err := rows.Save(ctx, &EgRow{ID: 99, Name: "a", Tenant: 1}); err != nil {
				t.Fatal(err)
			}

			// Offset two now starts one row too early: "c" is served twice.
			second, err := rows.Get(ctx, byName, crud.Limit(2), crud.Page(2))
			if err != nil {
				t.Fatal(err)
			}
			if got := namesOf(second); got[0] != "c" {
				t.Fatalf("offset page 2 = %v: the premise of this test no longer holds", got)
			}

			// The cursor walk continues from where it actually stopped.
			next, err := rows.Get(ctx, byName, crud.Limit(2), crud.After(cursorPage.NextCursor))
			if err != nil {
				t.Fatal(err)
			}
			if got := namesOf(next); len(got) != 2 || got[0] != "d" || got[1] != "e" {
				t.Fatalf("cursor page 2 = %v, want [d e]: the insert disturbed the walk", got)
			}
		})
	}
}

// Walking the whole table by cursor visits every row exactly once.
func TestACursorWalkVisitsEveryRowOnce(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			cursorSeed(t, tg, "a", "b", "c", "d", "e", "f", "g")
			rows := EgRows.Bind(tg.src)

			var seen []string
			cursor := ""
			for range 10 { // bounded so a bug cannot spin forever
				opts := []crud.Option{crud.OrderBy(crud.Asc("Name")), crud.Limit(3)}
				if cursor != "" {
					opts = append(opts, crud.After(cursor))
				}
				page, err := rows.Get(ctx, opts...)
				if err != nil {
					t.Fatal(err)
				}
				seen = append(seen, namesOf(page)...)
				if !page.HasNext {
					break
				}
				cursor = page.NextCursor
			}
			want := []string{"a", "b", "c", "d", "e", "f", "g"}
			if len(seen) != len(want) {
				t.Fatalf("walked %v, want %v", seen, want)
			}
			for i := range want {
				if seen[i] != want[i] {
					t.Fatalf("walked %v, want %v", seen, want)
				}
			}
		})
	}
}

// Paging backwards returns the rows immediately before the cursor, in the sort's
// own order — not the first n of everything before it, which is the far end.
func TestPagingBackwardsReturnsTheRowsNearestTheCursor(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			cursorSeed(t, tg, "a", "b", "c", "d", "e", "f")
			rows := EgRows.Bind(tg.src)
			byName := crud.OrderBy(crud.Asc("Name"))

			// Walk to the last page, then step back.
			page, err := rows.Get(ctx, byName, crud.Limit(2), crud.After(mustCursorAt(t, rows, byName, 2)))
			if err != nil {
				t.Fatal(err)
			}
			if got := namesOf(page); got[0] != "c" || got[1] != "d" {
				t.Fatalf("forward page = %v, want [c d]", got)
			}

			back, err := rows.Get(ctx, byName, crud.Limit(2), crud.Before(page.PrevCursor))
			if err != nil {
				t.Fatal(err)
			}
			// The two rows immediately before "c" are "a" and "b", in that order.
			if got := namesOf(back); len(got) != 2 || got[0] != "a" || got[1] != "b" {
				t.Fatalf("backward page = %v, want [a b]", got)
			}
		})
	}
}

// mustCursorAt reads one page of n rows and returns its trailing cursor.
func mustCursorAt(t *testing.T, rows crud.Repo[EgRow, int64, EgRowUpdate], sort crud.Option, n int) string {
	t.Helper()
	page, err := rows.Get(context.Background(), sort, crud.Limit(n))
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == "" {
		t.Fatal("no cursor to continue from")
	}
	return page.NextCursor
}

// A descending sort reverses the comparison, and a mixed sort has to compare
// each column in its own direction — the case a naive row-value comparison gets
// wrong.
func TestACursorFollowsEachColumnsOwnDirection(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.src)
			// Two tenants so the first sort column ties and the second decides.
			rows := EgRows.Bind(tg.src)
			for i, r := range []EgRow{
				{ID: 1, Name: "x", Tenant: 1}, {ID: 2, Name: "x", Tenant: 2},
				{ID: 3, Name: "y", Tenant: 1}, {ID: 4, Name: "y", Tenant: 2},
			} {
				_ = i
				row := r
				if err := rows.Save(ctx, &row); err != nil {
					t.Fatal(err)
				}
			}
			sort := crud.OrderBy(crud.Asc("Name"), crud.Desc("Tenant"))

			first, err := rows.Get(ctx, sort, crud.Limit(2))
			if err != nil {
				t.Fatal(err)
			}
			if got := firstIDs(first); got[0] != 2 || got[1] != 1 {
				t.Fatalf("first page ids = %v, want [2 1] (name asc, tenant desc)", got)
			}
			next, err := rows.Get(ctx, sort, crud.Limit(2), crud.After(first.NextCursor))
			if err != nil {
				t.Fatal(err)
			}
			if got := firstIDs(next); len(got) != 2 || got[0] != 4 || got[1] != 3 {
				t.Fatalf("second page ids = %v, want [4 3]: a column's direction was ignored", got)
			}
		})
	}
}

func firstIDs(p crud.PaginatedResponse[EgRow]) []int64 {
	out := make([]int64, 0, len(p.Items))
	for _, r := range p.Items {
		out = append(out, r.ID)
	}
	return out
}

// A cursor is positional, so replaying one under a different sort would compare
// whichever columns happen to line up. It is refused instead.
func TestACursorIsRefusedUnderADifferentSort(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	tg := egEngines()[0]
	egWipe(t, tg.src)
	cursorSeed(t, tg, "a", "b", "c")
	rows := EgRows.Bind(tg.src)

	page, err := rows.Get(ctx, crud.OrderBy(crud.Asc("Name")), crud.Limit(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rows.Get(ctx, crud.OrderBy(crud.Desc("Tenant")), crud.Limit(1), crud.After(page.NextCursor)); err == nil {
		t.Fatal("a cursor made for one sort was accepted under another")
	}
	if _, err := rows.Get(ctx, crud.OrderBy(crud.Asc("Name")), crud.Limit(1), crud.After("not-a-cursor")); err == nil {
		t.Fatal("a malformed cursor was accepted")
	}
}

// The wire half: a client walks the list without ever sending a page number,
// and the cursor it gets back in the JSON is the one it sends next.
func TestACursorWalkOverTheWireDSL(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	tg := egEngines()[0]
	egWipe(t, tg.src)
	cursorSeed(t, tg, "a", "b", "c", "d")
	rows := EgRows.Bind(tg.src)

	compile := func(body string) []crud.Option {
		t.Helper()
		var req query.Request
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("body %s: %v", body, err)
		}
		opts, err := req.Compile(EgRows.Meta(), nil)
		if err != nil {
			t.Fatalf("compiling %s: %v", body, err)
		}
		return opts
	}

	first, err := rows.Get(ctx, compile(`{"sort":["name"],"limit":2}`)...)
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" {
		t.Fatal("the response carries no cursor for the client to continue with")
	}

	second, err := rows.Get(ctx, compile(`{"sort":["name"],"limit":2,"after":"`+first.NextCursor+`"}`)...)
	if err != nil {
		t.Fatal(err)
	}
	if got := namesOf(second); len(got) != 2 || got[0] != "c" || got[1] != "d" {
		t.Fatalf("second page = %v, want [c d]", got)
	}
	// A cursor walk has no total to divide into pages, and saying otherwise
	// would invite a client to render a pager it cannot drive.
	if second.TotalPages != 0 {
		t.Fatalf("totalPages = %d on a cursor walk", second.TotalPages)
	}

	// The query-string door says the same thing.
	v, _ := url.ParseQuery("sort=name&limit=2&after=" + url.QueryEscape(first.NextCursor))
	req, err := query.ParseQuery(v)
	if err != nil {
		t.Fatal(err)
	}
	opts, err := req.Compile(EgRows.Meta(), nil)
	if err != nil {
		t.Fatal(err)
	}
	viaQS, err := rows.Get(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if got := namesOf(viaQS); len(got) != 2 || got[0] != "c" {
		t.Fatalf("query-string walk = %v, want the same page as the document", got)
	}
}

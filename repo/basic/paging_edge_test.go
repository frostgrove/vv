package basic_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/repo/basic"
)

// A scope is what makes a row invisible, and Delete used to be the one statement
// with a WHERE clause that did not carry it. Through the HTTP handler that read
// as: GET /:id answers 404 while DELETE /:id on the same row answers 200.
func TestScopeReachesDeleteByID(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		call func(crud.Repo[User, int64, UserUpdate]) error
		want string
	}{
		{"one id",
			func(r crud.Repo[User, int64, UserUpdate]) error { _, err := r.Delete(ctx, 42); return err },
			`DELETE FROM "users" WHERE ("tenant_id" = $1 AND "id" = $2)`},
		{"several ids",
			func(r crud.Repo[User, int64, UserUpdate]) error { _, err := r.Delete(ctx, 42, 43); return err },
			`DELETE FROM "users" WHERE ("tenant_id" = $1 AND "id" IN ($2, $3))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
			if err := tc.call(scopedUsers.Bind(rec)); err != nil {
				t.Fatal(err)
			}
			wantSQL(t, rec.Last().SQL, tc.want)
			if got := rec.Last().Args[0]; got != int64(1) {
				t.Fatalf("the delete bound %#v first, want the scope's tenant", got)
			}
		})
	}
}

// An unscoped repository must not grow a WHERE clause it never declared.
func TestDeleteWithoutAScopeIsStillJustTheKey(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	if _, err := Users.Bind(rec).Delete(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `DELETE FROM "users" WHERE "id" = $1`)
}

// ---------------------------------------------------------------------------
// paging

var cappedUsers = basic.Define[User, int64, UserUpdate]("users",
	basic.DefaultLimit(20), basic.MaxLimit(50))

// MaxLimit is the repository declaring how much of the table one page may
// return, and `?unpaged=true` is one flag on the wire. The flag used to win, so
// the declared cap was skipped rather than applied and the whole table came back.
func TestMaxLimitSurvivesEveryWayAPageCanBeAskedFor(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		opts []crud.Option
	}{
		{"no options at all", nil},
		{"a limit over the cap", []crud.Option{crud.Limit(5000)}},
		{"unpaged", []crud.Option{crud.Unpaged()}},
		{"unpaged with a page number", []crud.Option{crud.Unpaged(), crud.Page(3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			if _, err := cappedUsers.Bind(rec).Get(ctx, tc.opts...); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rec.Last().SQL, " LIMIT ") {
				t.Fatalf("a repository with MaxLimit(50) emitted a statement with no LIMIT: %s", rec.Last().SQL)
			}
		})
	}
}

// GetAll's contract is every matching row, and MaxLimit caps a page. Truncating
// here would be worse than a slow query: the decorators that read a whole set in
// order to check it would check the first fifty and let the rest through.
func TestGetAllIsNotCappedByMaxLimit(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := cappedUsers.Bind(rec).GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Last().SQL, " LIMIT ") {
		t.Fatalf("GetAll was truncated to a page: %s", rec.Last().SQL)
	}
}

// SkipTotal means no COUNT ran, so the only number that is true is the size of
// what came back. Deriving it from the offset invented one the client had
// chosen: page 999 of an empty table reported 19960 results, over no rows.
func TestSkipTotalReportsWhatWasFetchedAndNotTheOffset(t *testing.T) {
	ctx := context.Background()

	t.Run("a page past the end of an empty table", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		page, err := Users.Bind(rec).Get(ctx, crud.SkipTotal(), crud.Page(999), crud.Limit(20))
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 0 {
			t.Fatalf("Total = %d over %d items; SkipTotal reports what was fetched, "+
				"and nothing was", page.Total, len(page.Items))
		}
		if page.HasNext {
			t.Fatalf("HasNext over an empty page")
		}
	})

	t.Run("one row deep into the table", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows(userRow(1, "a@b.c", "Ann", 30, 7)))
		page, err := Users.Bind(rec).Get(ctx, crud.SkipTotal(), crud.Offset(100), crud.Limit(20))
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 {
			t.Fatalf("Total = %d, want the 1 row that was fetched", page.Total)
		}
	})
}

// A page number is a client's number, multiplied by the page size, in an int.
// Wrapping made the offset non-positive, SQL dropped it, and the caller was
// handed page one wearing the page number they had asked for.
func TestAPageNumberThatWouldOverflowAsksForAPagePastTheEnd(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows([]any{int64(3)}))

	page, err := Users.Bind(rec).Get(context.Background(), crud.Page(math.MaxInt), crud.Limit(20))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustSQL(t, rec, 0).SQL; !strings.Contains(got, " OFFSET ") {
		t.Fatalf("no OFFSET at all, so the first page was returned as page %d: %s", page.Page, got)
	}
	if len(page.Items) != 0 {
		t.Fatalf("%d items came back from a page past the end", len(page.Items))
	}
}

// ---------------------------------------------------------------------------
// distinct

// distinct, select and sort all arrive from the wire and all three can arrive
// together. Both engines refuse a SELECT DISTINCT ordered by a column outside
// the select list, and the two ways out of that are not equal: quietly adding
// the column to the projection makes the statement run and the answer wrong —
// the column that decides the order is also the column that tells the duplicate
// rows apart, so nothing collapses any more — while refusing tells the caller
// that the two things they asked for cannot both be true.
func TestDistinctRefusesASortItCannotProject(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())

	_, err := Users.Bind(rec).GetAll(context.Background(),
		crud.Distinct(), crud.Select("Name"), crud.OrderBy(crud.Desc("Age")), crud.Limit(10))

	var se *crud.SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want a *crud.SchemaError naming the sort", err)
	}
	if !strings.Contains(se.Error(), "Age") {
		t.Fatalf("err = %v, want the refusal to name the column that cannot be sorted by", err)
	}
	if n := len(rec.Statements()); n != 0 {
		t.Fatalf("%d statements reached the database: %v", n, rec.SQL())
	}
}

// The repository's default sort is not something the caller asked for, so it
// must not be able to turn `?distinct=1&select=name` into an error nobody can
// avoid from the wire. It is dropped: a DISTINCT projection has no rows to put
// in an order anyway, only values.
func TestDistinctDropsADefaultSortItCannotProject(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	repo := basic.Define[User, int64, UserUpdate]("users",
		basic.DefaultSort(crud.Desc("CreatedAt"))).Bind(rec)

	if _, err := repo.GetAll(context.Background(), crud.Distinct(), crud.Select("Name")); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `SELECT DISTINCT "name" FROM "users"`)
}

// The primary-key tiebreaker makes pages a stable partition of rows. A DISTINCT
// projection has no rows to partition — and appending a unique column to the
// ORDER BY would put it in the select list, which is what made DISTINCT a no-op
// in the first place. So a paged DISTINCT goes without it.
func TestAPagedDistinctDoesNotAppendThePrimaryKey(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows([]any{int64(0)}))

	if _, err := Users.Bind(rec).Get(context.Background(),
		crud.Distinct(), crud.Select("Name"), crud.OrderBy(crud.Asc("Name")), crud.Limit(10)); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		`SELECT DISTINCT "name" FROM "users" ORDER BY "name" ASC LIMIT 10`)
}

// A sort through a relation renders as a scalar subquery, which can never
// appear in a select list. There is no statement to build, so say so instead of
// sending one that will be refused.
func TestDistinctRefusesASortThroughARelation(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())

	_, err := basic.Define[Book, int64, struct{}]("books").Bind(rec).GetAll(context.Background(),
		crud.Distinct(), crud.OrderBy(crud.Asc("Pages.Number")), crud.Limit(10))

	if err == nil {
		t.Fatal("a DISTINCT query sorted through a relation was accepted")
	}
	if !strings.Contains(err.Error(), "Pages.Number") {
		t.Fatalf("err = %v, want a refusal naming the sort that cannot be projected", err)
	}
	if n := len(rec.Statements()); n != 0 {
		t.Fatalf("%d statements reached the database: %v", n, rec.SQL())
	}
}

// ---------------------------------------------------------------------------
// the tiebreaker

// The primary-key tiebreaker is what makes paging over a non-unique sort stable.
// UnstablePagination is the documented way to turn it off, so it has to actually
// turn it off — and it must not touch the sort the caller asked for.
func TestUnstablePaginationDropsTheTiebreaker(t *testing.T) {
	ctx := context.Background()
	loose := basic.Define[User, int64, UserUpdate]("users", basic.UnstablePagination())

	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := loose.Bind(rec).Get(ctx, crud.OrderBy(crud.Asc("Name")), crud.Limit(10)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Last().SQL, `ORDER BY "name" ASC, "id" ASC`) {
		t.Fatalf("the tiebreaker is still there: %s", rec.Last().SQL)
	}
	if !strings.Contains(rec.Last().SQL, `ORDER BY "name" ASC`) {
		t.Fatalf("the caller's own sort went missing: %s", rec.Last().SQL)
	}

	// And the default, for contrast: without the setting the tiebreaker is on.
	rec = crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Users.Bind(rec).Get(ctx, crud.OrderBy(crud.Asc("Name")), crud.Limit(10)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Last().SQL, `ORDER BY "name" ASC, "id" ASC`) {
		t.Fatalf("the default lost its tiebreaker: %s", rec.Last().SQL)
	}
}

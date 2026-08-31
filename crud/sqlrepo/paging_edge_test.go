package sqlrepo_test

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

func TestScopeReachesDeleteByID(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		call func(*crud.Repo[User, int64, UserUpdate]) error
		want string
	}{
		{"one id",
			func(r *crud.Repo[User, int64, UserUpdate]) error { _, err := r.Delete(ctx, 42); return err },
			`DELETE FROM "users" WHERE ("tenant_id" = $1 AND "id" = $2)`},
		{"several ids",
			func(r *crud.Repo[User, int64, UserUpdate]) error { _, err := r.Delete(ctx, 42, 43); return err },
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

func TestDeleteWithoutAScopeIsStillJustTheKey(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})
	if _, err := Users.Bind(rec).Delete(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `DELETE FROM "users" WHERE "id" = $1`)
}

var cappedUsers = sqlrepo.Define[User, int64, UserUpdate]("users",
	sqlrepo.DefaultLimit(20), sqlrepo.MaxLimit(50))

func TestMaxLimitSurvivesEveryWayAPageCanBeAskedFor(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		options []crud.Option
	}{
		{"no options at all", nil},
		{"a limit over the cap", []crud.Option{crud.Limit(5000)}},
		{"unpaged", []crud.Option{crud.Unpaged()}},
		{"unpaged with a page number", []crud.Option{crud.Unpaged(), crud.Page(3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			if _, err := cappedUsers.Bind(rec).Get(ctx, tc.options...); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rec.Last().SQL, " LIMIT ") {
				t.Fatalf("a repository with MaxLimit(50) emitted a statement with no LIMIT: %s", rec.Last().SQL)
			}
		})
	}
}

func TestGetAllIsNotCappedByMaxLimit(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := cappedUsers.Bind(rec).GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Last().SQL, " LIMIT ") {
		t.Fatalf("GetAll was truncated to a page: %s", rec.Last().SQL)
	}
}

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

func TestSkipTotalDoesNotOverflowTheLargestLimit(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	page, err := Users.Bind(rec).Get(context.Background(), crud.SkipTotal(), crud.Limit(math.MaxInt))
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Last().SQL; !strings.Contains(got, "LIMIT "+strconv.Itoa(math.MaxInt)) {
		t.Fatalf("the limit+1 probe overflowed or disappeared: %s", got)
	}
	if page.HasNext {
		t.Fatal("an empty result reported another page")
	}
}

func TestCursorsOnlyAdvertiseExistingNeighbours(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name               string
		page               int
		rows               [][]any
		total              int64
		wantNext, wantPrev bool
	}{
		{
			name:     "first page only has a next cursor",
			page:     1,
			rows:     [][]any{userRow(1, "a@b.c", "Ann", 30, 7), userRow(2, "b@b.c", "Bea", 31, 7)},
			total:    3,
			wantNext: true,
		},
		{
			name:     "middle page has both cursors",
			page:     2,
			rows:     [][]any{userRow(3, "c@b.c", "Cam", 32, 7), userRow(4, "d@b.c", "Dee", 33, 7)},
			total:    5,
			wantNext: true,
			wantPrev: true,
		},
		{
			name:     "last page only has a previous cursor",
			page:     2,
			rows:     [][]any{userRow(3, "c@b.c", "Cam", 32, 7)},
			total:    3,
			wantPrev: true,
		},
		{
			name:  "one page has no cursors",
			page:  1,
			rows:  [][]any{userRow(1, "a@b.c", "Ann", 30, 7)},
			total: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows(tc.rows...), crudtest.Rows([]any{tc.total}))
			got, err := Users.Bind(rec).Get(ctx, crud.Page(tc.page), crud.Limit(2))
			if err != nil {
				t.Fatal(err)
			}
			if (got.NextCursor != "") != tc.wantNext {
				t.Fatalf("nextCursor = %q, want present: %v", got.NextCursor, tc.wantNext)
			}
			if (got.PrevCursor != "") != tc.wantPrev {
				t.Fatalf("prevCursor = %q, want present: %v", got.PrevCursor, tc.wantPrev)
			}
		})
	}
}

func TestANullableSortNeverAdvertisesACursorItsNextRequestWouldRefuse(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(
			userRow(1, "a@b.c", "Ann", 30, 7),
			userRow(2, "b@b.c", "Bea", 31, 7),
		),
		crudtest.Rows([]any{int64(3)}),
	)
	page, err := Users.Bind(rec).Get(context.Background(),
		crud.OrderBy(crud.Asc("Age")), crud.Limit(2))
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasNext {
		t.Fatal("control failed: the page needs a next neighbour")
	}
	if page.NextCursor != "" || page.PrevCursor != "" {
		t.Fatalf("nullable sort advertised cursors next=%q prev=%q", page.NextCursor, page.PrevCursor)
	}
}

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

func TestDistinctDropsADefaultSortItCannotProject(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	repository := sqlrepo.Define[User, int64, UserUpdate]("users",
		sqlrepo.DefaultSort(crud.Desc("CreatedAt"))).Bind(rec)

	if _, err := repository.GetAll(context.Background(), crud.Distinct(), crud.Select("Name")); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `SELECT DISTINCT "name" FROM "users"`)
}

func TestAPagedDistinctDoesNotAppendThePrimaryKey(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows([]any{int64(0)}))

	if _, err := Users.Bind(rec).Get(context.Background(),
		crud.Distinct(), crud.Select("Name"), crud.OrderBy(crud.Asc("Name")), crud.Limit(10)); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		`SELECT DISTINCT "name" FROM "users" ORDER BY "name" ASC LIMIT 10`)
}

func TestDistinctRefusesASortThroughARelation(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())

	_, err := sqlrepo.Define[Book, int64, struct{}]("books").Bind(rec).GetAll(context.Background(),
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

func TestUnstablePaginationDropsTheTiebreaker(t *testing.T) {
	ctx := context.Background()
	loose := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.UnstablePagination())

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

	rec = crudtest.Postgres().Push(crudtest.Rows())
	if _, err := Users.Bind(rec).Get(ctx, crud.OrderBy(crud.Asc("Name")), crud.Limit(10)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Last().SQL, `ORDER BY "name" ASC, "id" ASC`) {
		t.Fatalf("the default lost its tiebreaker: %s", rec.Last().SQL)
	}
}

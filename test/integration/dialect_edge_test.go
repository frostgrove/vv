//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/errs"
)

type EgOdd struct {
	ID       int64  `db:"id,pk,noauto"`
	Select   string `db:"select"`
	FullName string `db:"full name"`
	Flag     bool   `db:"flag"`
}

type EgOddUpdate struct {
	Select   *string
	FullName *string
	Flag     *bool
}

var EgOdds = sqlrepo.Define[EgOdd, int64, EgOddUpdate]("eg_odd")

func egRowText(r EgRow) string {
	return fmt.Sprintf("id=%d tenant=%d %q note=%s score=%s flag=%t",
		r.ID, r.Tenant, r.Name, r.Note, r.Score, r.Flag)
}

func egScores(rows []EgRow) string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Score.String()
	}
	return strings.Join(out, " ")
}

func TestQuotedIdentifiersSurviveEveryClause(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			odd := EgOdds.Bind(tg.source)

			for _, o := range []EgOdd{
				{ID: 1, Select: "from", FullName: "Ada Lovelace", Flag: true},
				{ID: 2, Select: "where", FullName: "Bob Barker", Flag: false},
			} {
				if _, err := odd.Save(ctx, &o); err != nil {
					t.Fatalf("INSERT into a table with awkward column names: %v", err)
				}
			}

			got, err := odd.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if got.Select != "from" || got.FullName != "Ada Lovelace" || !got.Flag {
				t.Fatalf("row = %+v", got)
			}
			n, err := odd.Count(ctx, crud.Where(crud.Eq("Select", "where")))
			if err != nil || n != 1 {
				t.Fatalf("filtering on a reserved-word column matched %d rows, err = %v", n, err)
			}

			page, err := odd.Get(ctx, crud.OrderBy(crud.Desc("FullName")), crud.Select("FullName"), crud.Limit(10))
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 2 || page.Items[0].FullName != "Bob Barker" {
				t.Fatalf("sorted by a spaced column = %+v", page.Items)
			}
			if page.Items[0].Select != "" {
				t.Fatalf("a projection that did not ask for it returned %q", page.Items[0].Select)
			}

			patched, err := odd.Update(ctx, 2, EgOddUpdate{Select: ptr("group"), Flag: ptr(true)})
			if err != nil {
				t.Fatal(err)
			}
			if patched.Select != "group" || !patched.Flag || patched.FullName != "Bob Barker" {
				t.Fatalf("patched = %+v", patched)
			}

			again := EgOdd{ID: 1, Select: "select", FullName: "Ada L.", Flag: false}
			if _, err := odd.Save(ctx, &again); err != nil {
				t.Fatalf("upsert over awkward column names: %v", err)
			}
			back, err := odd.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if back.Select != "select" || back.FullName != "Ada L." || back.Flag {
				t.Fatalf("after the upsert = %+v", back)
			}

			trues, err := odd.Count(ctx, crud.Where(crud.Eq("Flag", true)))
			if err != nil {
				t.Fatal(err)
			}
			falses, err := odd.Count(ctx, crud.Where(crud.Eq("Flag", false)))
			if err != nil {
				t.Fatal(err)
			}
			if trues != 1 || falses != 1 {
				t.Fatalf("flag = true matched %d rows and flag = false matched %d, want one each: "+
					"MySQL keeps a BOOLEAN in a tinyint and that must not show", trues, falses)
			}

			if n, err := odd.DeleteAll(ctx, crud.Where(crud.Eq("Select", "group"))); err != nil || n != 1 {
				t.Fatalf("DELETE by a reserved-word column removed %d rows, err = %v", n, err)
			}
		})
	}
}

func TestUpsertLeavesTheSameRowInEveryDialect(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	targets := []egTarget{
		{"postgres", "postgres", crudsql.Postgres(pgDB), true},
		{"mysql", "mysql", crudsql.MySQL(myDB), true},

		{"mysql(row alias)", "mysql", crudsql.Open(myDB, crud.MySQL{RowAlias: true}), false},
	}

	for _, tg := range targets {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)

			first := EgRow{ID: 1, Tenant: 7, Name: "before", Note: crud.Set("kept"), Score: crud.Set(1)}
			if _, err := rows.Save(ctx, &first); err != nil {
				t.Fatal(err)
			}

			second := EgRow{ID: 1, Tenant: 999, Name: "after", Note: crud.Null[string](), Score: crud.Set(2), Flag: true}
			if _, err := rows.Save(ctx, &second); err != nil {
				t.Fatal(err)
			}

			got, err := rows.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			want := `id=1 tenant=7 "after" note=<null> score=2 flag=true`
			if egRowText(got) != want {
				t.Fatalf("the upserted row is\n  %s\nwant\n  %s", egRowText(got), want)
			}
			if n, err := rows.Count(ctx); err != nil || n != 1 {
				t.Fatalf("count = %d, err = %v: the upsert inserted a second row", n, err)
			}
		})
	}
}

func TestSaveLeavesTheCallerHoldingTheStoredRowOnEveryEngine(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)

			row := EgRow{ID: 1, Tenant: 7, Name: "before"}
			if _, err := rows.Save(ctx, &row); err != nil {
				t.Fatal(err)
			}
			row.Tenant = 999
			row.Name = "after"
			answered, err := rows.Save(ctx, &row)
			if err != nil {
				t.Fatal(err)
			}

			stored, err := rows.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			want := `id=1 tenant=7 "after" note=<null> score=<null> flag=false`
			if egRowText(stored) != want {
				t.Fatalf("the stored row is\n  %s\nwant\n  %s", egRowText(stored), want)
			}

			if egRowText(answered) != want {
				t.Fatalf("Save answered\n  %s\nwhere the row is\n  %s",
					egRowText(answered), want)
			}

			if row.Tenant != 999 {
				t.Fatalf("Save edited the caller's model: tenant = %d, want the 999 it set", row.Tenant)
			}
		})
	}
}

func TestUpdateOfARowThatVanishesUnderneathIt(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			plain := EgRows.Bind(tg.source)
			egSeed(t, plain, EgRow{ID: 1, Tenant: 1, Name: "doomed"})

			s := egWatch(tg.source)
			s.beforeFirst("UPDATE ", func() {
				if _, err := plain.Delete(ctx, 1); err != nil {
					t.Errorf("deleting the row mid-update: %v", err)
				}
			})
			got, err := EgRows.Bind(s).Update(ctx, 1, EgRowUpdate{Name: ptr("renamed")})

			if n, cerr := plain.Count(ctx); cerr != nil || n != 0 {
				t.Fatalf("count = %d, err = %v: the row was supposed to be gone before the UPDATE", n, cerr)
			}
			if !errors.Is(err, crud.ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound; Update reported success for a row that "+
					"had already been deleted, handing back %+v", err, got)
			}
		})
	}
}

func TestAConcurrentWriteIsRefusedRatherThanLost(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egTargets() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			plain := EgVers.Bind(tg.source)
			row := EgVer{ID: 1, Name: "start"}
			if _, err := plain.Save(ctx, &row); err != nil {
				t.Fatal(err)
			}

			s := egWatch(tg.source)
			s.beforeFirst("UPDATE ", func() {
				if _, err := plain.Update(ctx, 1, EgVerUpdate{Name: ptr("theirs")}); err != nil {
					t.Errorf("the concurrent write failed: %v", err)
				}
			})

			_, err := EgVers.Bind(s).Update(ctx, 1, EgVerUpdate{Name: ptr("mine")})
			if !errors.Is(err, crud.ErrStaleVersion) {
				t.Fatalf("err = %v, want ErrStaleVersion", err)
			}
			if !errors.Is(err, crud.ErrConflict) {
				t.Fatal("a stale write has to read as a conflict, so a transport answers 409")
			}

			after, err := plain.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if after.Name != "theirs" {
				t.Fatalf("name = %q: the concurrent write was overwritten, which is the lost update the lock exists to prevent", after.Name)
			}
			if after.Version != 1 {
				t.Fatalf("version = %d, want 1: exactly one write should have landed", after.Version)
			}

			redone, err := plain.Update(ctx, 1, EgVerUpdate{Name: ptr("mine, retried")})
			if err != nil {
				t.Fatalf("the retry after a stale write failed: %v", err)
			}
			if redone.Name != "mine, retried" || redone.Version != 2 {
				t.Fatalf("after the retry the row is %+v, want name=mine, retried version=2", redone)
			}
		})
	}
}

func TestAFilteredUpdateIsAlsoNoticedByTheLock(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			plain := EgVers.Bind(tg.source)
			row := EgVer{ID: 1, Name: "start"}
			if _, err := plain.Save(ctx, &row); err != nil {
				t.Fatal(err)
			}

			s := egWatch(tg.source)
			s.beforeFirst("UPDATE ", func() {
				if _, err := plain.UpdateAll(ctx, EgVerUpdate{Name: ptr("bulk")},
					crud.Where(crud.Eq("ID", int64(1)))); err != nil {
					t.Errorf("the concurrent filtered update failed: %v", err)
				}
			})

			_, err := EgVers.Bind(s).Update(ctx, 1, EgVerUpdate{Name: ptr("mine")})
			if !errors.Is(err, crud.ErrStaleVersion) {
				t.Fatalf("err = %v: a filtered update slipped past the lock, and this write silently undid it", err)
			}
			after, err := plain.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if after.Name != "bulk" || after.Version != 1 {
				t.Fatalf("row = %+v, want the filtered update's value at version 1", after)
			}
		})
	}
}

func TestASaveCannotWindTheLockBack(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			vers := EgVers.Bind(tg.source)
			row := EgVer{ID: 1, Name: "start"}
			if _, err := vers.Save(ctx, &row); err != nil {
				t.Fatal(err)
			}
			if _, err := vers.Update(ctx, 1, EgVerUpdate{Name: ptr("second")}); err != nil {
				t.Fatal(err)
			}

			row.Name = "resaved"
			resaved, err := vers.Save(ctx, &row)
			if err != nil {
				t.Fatal(err)
			}
			if resaved.Version != 1 {
				t.Fatalf("Save answered version %d; it answers the row that is there, so a stale caller's copy is refreshed by using the return value", resaved.Version)
			}
			after, err := vers.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if after.Version != 1 {
				t.Fatalf("version = %d, want 1: a stale Save reset the lock", after.Version)
			}
			if after.Name != "resaved" {
				t.Fatalf("name = %q: the Save itself did not land", after.Name)
			}
		})
	}
}

func TestLikeFollowsTheCollationAndLikeIgnoreCaseOverridesIt(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)
			egSeed(t, rows,
				EgRow{ID: 1, Tenant: 1, Name: "Alpha"},
				EgRow{ID: 2, Tenant: 1, Name: "beta"},
			)

			for _, tc := range []struct {
				name string
				pred crud.Predicate
			}{
				{"upper pattern over a capitalised value", crud.LikeIgnoreCase("Name", "ALPHA")},
				{"upper pattern over a lower-case value", crud.LikeIgnoreCase("Name", "BETA")},
				{"lower pattern over a capitalised value", crud.LikeIgnoreCase("Name", "alpha")},
			} {
				n, err := rows.Count(ctx, crud.Where(tc.pred))
				if err != nil || n != 1 {
					t.Fatalf("LikeIgnoreCase, %s, matched %d rows, err = %v", tc.name, n, err)
				}
			}

			mismatched, err := rows.Count(ctx, crud.Where(crud.Contains("Name", "LPH")))
			if err != nil {
				t.Fatal(err)
			}
			switch tg.database {
			case "postgres":
				if mismatched != 0 {
					t.Fatalf(`Contains("LPH") matched %d rows, want none on a case-sensitive collation`, mismatched)
				}
			case "mysql":
				if mismatched != 1 {
					t.Fatalf(`Contains("LPH") matched %d rows, want Alpha: either the server's collation `+
						`stopped ignoring case or Like's contract moved`, mismatched)
				}
			}
		})
	}
}

func TestNullOrderingUsesEngineDefaultsUntilTheCallerChoosesAPortableHint(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)
			egSeed(t, rows,
				EgRow{ID: 1, Tenant: 1, Name: "a", Score: crud.Null[int]()},
				EgRow{ID: 2, Tenant: 1, Name: "b", Score: crud.Set(5)},
				EgRow{ID: 3, Tenant: 1, Name: "c", Score: crud.Null[int]()},
				EgRow{ID: 4, Tenant: 1, Name: "d", Score: crud.Set(1)},
			)

			for _, tc := range []struct {
				name            string
				order           crud.Order
				postgres, mysql string
			}{
				{"ascending", crud.Asc("Score"),
					"1 5 <null> <null>", "<null> <null> 1 5"},
				{"descending", crud.Desc("Score"),
					"<null> <null> 5 1", "5 1 <null> <null>"},
				{"ascending, NULLs last requested", crud.Asc("Score").WithNullsLast(),
					"1 5 <null> <null>", "1 5 <null> <null>"},
				{"descending, NULLs last requested", crud.Desc("Score").WithNullsLast(),
					"5 1 <null> <null>", "5 1 <null> <null>"},
				{"ascending, NULLs first requested", crud.Asc("Score").WithNullsFirst(),
					"<null> <null> 1 5", "<null> <null> 1 5"},
				{"descending, NULLs first requested", crud.Desc("Score").WithNullsFirst(),
					"<null> <null> 5 1", "<null> <null> 5 1"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, err := rows.GetAll(ctx, crud.OrderBy(tc.order), crud.Limit(10))
					if err != nil {
						t.Fatal(err)
					}
					want := tc.postgres
					if tg.database == "mysql" {
						want = tc.mysql
					}
					if egScores(got) != want {
						t.Fatalf("scores came back as [%s], want [%s]", egScores(got), want)
					}
				})
			}
		})
	}
}

func TestDistinctActuallyRemovesDuplicateRows(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			rows := EgRows.Bind(tg.source)
			egSeed(t, rows,
				EgRow{ID: 1, Tenant: 1, Name: "repeat", Score: crud.Set(3)},
				EgRow{ID: 2, Tenant: 1, Name: "repeat", Score: crud.Set(2)},
				EgRow{ID: 3, Tenant: 1, Name: "repeat", Score: crud.Set(1)},
				EgRow{ID: 4, Tenant: 1, Name: "only once", Score: crud.Set(9)},
			)

			names, err := rows.GetAll(ctx, crud.Distinct(), crud.Select("Name"), crud.OrderBy(crud.Asc("Name")))
			if err != nil {
				t.Fatal(err)
			}
			if got := egNames(names); !eq(got, []string{"only once", "repeat"}) {
				t.Fatalf("the distinct names are %v, want [only once repeat] — four rows, two names", got)
			}
			for _, r := range names {
				if r.ID != 0 {
					t.Fatalf("row %d came back with its primary key, which is what made DISTINCT unable to collapse anything", r.ID)
				}
			}

			page, err := rows.Get(ctx, crud.Distinct(), crud.Select("Name"), crud.Limit(1), crud.OrderBy(crud.Asc("Name")))
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 2 || page.TotalPages != 2 || len(page.Items) != 1 {
				t.Fatalf("page = %+v, want one of two distinct names over two pages", page)
			}

			whole, err := rows.GetAll(ctx, crud.Distinct(), crud.OrderBy(crud.Asc("ID")))
			if err != nil {
				t.Fatal(err)
			}
			if len(whole) != 4 {
				t.Fatalf("a bare DISTINCT returned %d of 4 rows", len(whole))
			}
		})
	}
}

func TestDistinctRefusesASortOutsideItsProjectionOnBothEngines(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			s := egWatch(tg.source)
			rows := EgRows.Bind(s)
			egSeed(t, rows, EgRow{ID: 1, Tenant: 1, Name: "a", Score: crud.Set(1)})
			s.forget()

			_, err := rows.GetAll(ctx, crud.Distinct(), crud.Select("Name"), crud.OrderBy(crud.Desc("Score")))
			var se *crud.SchemaError
			if !errors.As(err, &se) {
				t.Fatalf("err = %v, want a *crud.SchemaError naming Score", err)
			}
			if len(s.statements()) != 0 {
				t.Fatalf("the statement was sent anyway and the engine refused it: %v", s.statements())
			}

			ok, err := rows.GetAll(ctx, crud.Distinct(), crud.Select("Name", "Score"), crud.OrderBy(crud.Desc("Score")))
			if err != nil {
				t.Fatalf("selecting the sorted column too is the answer, and it failed: %v", err)
			}
			if len(ok) != 1 {
				t.Fatalf("rows = %d, want 1", len(ok))
			}
		})
	}
}

func TestPagingOverANullableColumnNeitherLosesNorRepeatsARow(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			s := egWatch(tg.source)
			rows := EgRows.Bind(s)

			const total, size = 12, 3
			for i := 1; i <= total; i++ {
				r := EgRow{ID: int64(i), Tenant: 1, Name: fmt.Sprintf("r%02d", i)}
				if i%2 == 0 {
					r.Score = crud.Set(7)
				}
				if stored, err := rows.Save(ctx, &r); err != nil {
					t.Fatal(err)
				} else {
					r = stored
				}
			}
			s.forget()

			seen := map[int64]int{}
			var order []int64
			for page := 1; page <= total/size; page++ {
				got, err := rows.Get(ctx, crud.OrderBy(crud.Asc("Score")), crud.Page(page), crud.Limit(size))
				if err != nil {
					t.Fatal(err)
				}
				if len(got.Items) != size {
					t.Fatalf("page %d holds %d rows, want %d", page, len(got.Items), size)
				}
				for _, r := range got.Items {
					seen[r.ID]++
					order = append(order, r.ID)
				}
			}
			for id := int64(1); id <= total; id++ {
				if seen[id] != 1 {
					t.Fatalf("row %d appeared %d times across the pages: %v", id, seen[id], order)
				}
			}

			q := tg.source.Dialect().Quote
			tail := fmt.Sprintf("ORDER BY %s ASC, %s ASC LIMIT %d OFFSET %d", q("score"), q("id"), size, size)
			if stmts := s.matching(tail); len(stmts) == 0 {
				t.Fatalf("no statement carried %q; the paged reads were %v", tail, s.matching("SELECT"))
			}
		})
	}
}

func TestRawEscapesAQuestionMarkForPostgresJSONBOperators(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	rows := EgRows.Bind(crudsql.Postgres(pgDB))
	egWipe(t, crudsql.Postgres(pgDB))
	egSeed(t, rows,
		EgRow{ID: 1, Tenant: 1, Name: "alpha"},
		EgRow{ID: 2, Tenant: 1, Name: "beta"},
	)

	n, err := rows.Count(ctx, crud.Where(crud.Raw(`('{"' || name || '": 1}')::jsonb ?? ?`, "beta")))
	if err != nil {
		t.Fatalf("the escaped operator did not reach PostgreSQL intact: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want the one row whose name is a key of its own document", n)
	}

	if _, err := rows.Count(ctx, crud.Where(crud.Raw(`('{"a": 1}')::jsonb ?? ?`, "a", "b"))); err == nil {
		t.Fatal("a fragment with more arguments than markers was accepted")
	}
}

func TestIntegrityViolationsAreClassifiedByEveryAdapter(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egTargets() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			cons := EgConses.Bind(tg.source)

			anchor := EgCons{Slug: "taken", Tag: crud.Set("t")}
			if stored, err := cons.Save(ctx, &anchor); err != nil {
				t.Fatal(err)
			} else {
				anchor = stored
			}

			for _, tc := range []struct {
				name string
				code errs.Code
				run  func() error
			}{
				{"a duplicate unique key", errs.CodeUnique, func() error {
					_, err := cons.Save(ctx, &EgCons{Slug: "taken", Tag: crud.Set("other")})

					return err
				}},
				{"a foreign key that points nowhere", errs.CodeForeignKey, func() error {
					_, err := cons.Save(ctx, &EgCons{Slug: "free", Tag: crud.Set("t"), Parent: crud.Set(int64(987654))})

					return err
				}},
				{"a NULL in a NOT NULL column", errs.CodeRequired, func() error {
					_, err := cons.Update(ctx, anchor.ID, EgConsUpdate{Tag: crud.Null[string]()})
					return err
				}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					err := tc.run()
					if err == nil {
						t.Fatal("the database accepted it")
					}
					t.Logf("the caller receives %T: %v", err, err)

					if !errors.Is(err, crud.ErrConflict) {
						t.Fatalf("err = %v, want it to wrap crud.ErrConflict", err)
					}

					for _, sentinel := range []error{crud.ErrNotFound, crud.ErrMissingID, crud.ErrReadOnly, crud.ErrForbidden} {
						if errors.Is(err, sentinel) {
							t.Fatalf("a constraint violation came back as %v", sentinel)
						}
					}

					f, has := errs.AsFault(err)
					if has != tg.classifies {
						t.Fatalf("a fault reached the caller = %v, want %v: %T: %v", has, tg.classifies, err, err)
					}
					if has && f.Code != tc.code {
						t.Fatalf("the fault says %q, want %q", f.Code, tc.code)
					}

					all, err := cons.GetAll(ctx, crud.OrderBy(crud.Asc("ID")))
					if err != nil {
						t.Fatalf("the repository stopped working after a rejected statement: %v", err)
					}
					if len(all) != 1 || all[0].Slug != "taken" || all[0].Tag.String() != "t" {
						t.Fatalf("the table holds %+v, want just the untouched anchor row", all)
					}
				})
			}
		})
	}
}

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

// The two engines vv supports disagree about more than syntax: MySQL has no
// RETURNING, its default collation ignores case, it orders NULLs the other way
// round and it spells identifiers with backticks. Every one of those is
// something a caller must not have to know about — and the ones that still leak
// through are worth a test that says so out loud, so that the next person to
// meet one finds it written down rather than in production.

// EgOdd's two awkward columns cannot be written unquoted in either dialect: one
// is a reserved word, the other has a space in it. Between them they walk the
// quoting through the SELECT list, the WHERE, the ORDER BY, the INSERT, the
// conflict clause and the UPDATE SET.
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

// egRowText renders every column of an EgRow, telling undefined from null, so
// that two engines' idea of the same row can be compared as one string.
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

// ---------------------------------------------------------------------------
// identifier quoting

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

			// SELECT list and WHERE.
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

			// ORDER BY, and a projection that names one of the two.
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

			// UPDATE SET.
			patched, err := odd.Update(ctx, 2, EgOddUpdate{Select: ptr("group"), Flag: ptr(true)})
			if err != nil {
				t.Fatal(err)
			}
			if patched.Select != "group" || !patched.Flag || patched.FullName != "Bob Barker" {
				t.Fatalf("patched = %+v", patched)
			}

			// The conflict clause, which quotes the same columns a third time.
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

			// A bool is a bool on both engines, whatever MySQL stores it in.
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

// ---------------------------------------------------------------------------
// the upsert

// ON CONFLICT DO UPDATE and ON DUPLICATE KEY UPDATE are different statements
// with the same job. What has to be identical is the row they leave behind — in
// all three spellings, including MySQL's `AS new` row alias.
func TestUpsertLeavesTheSameRowInEveryDialect(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	targets := []egTarget{
		{"postgres", "postgres", crudsql.Postgres(pgDB), true},
		{"mysql", "mysql", crudsql.MySQL(myDB), true},
		// Open takes a dialect and not an engine, so this one names none and
		// classifies nothing. Nothing in this test asks it to.
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

			// Everything changes at once: a plain column, a nullable one cleared
			// to NULL, a bool flipped — and the immutable column, which the
			// conflict clause must leave out however it is spelled.
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

// Save promises the model comes back describing the row. PostgreSQL keeps it
// with RETURNING in the same round trip; MySQL has none, so crud/sqlrepo reads the
// row back. The engines must be indistinguishable here, because the caller's
// model is what a handler serialises: an upsert's conflict clause leaves every
// immutable column out, and a model that kept the refused value would put it in
// the response body on one engine and not the other.
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
			row.Tenant = 999 // immutable: the conflict clause will not write it
			row.Name = "after"
			if _, err := rows.Save(ctx, &row); err != nil {
				t.Fatal(err)
			}

			// The table has the right row: tenant 7 survived, the name changed.
			stored, err := rows.GetByID(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			want := `id=1 tenant=7 "after" note=<null> score=<null> flag=false`
			if egRowText(stored) != want {
				t.Fatalf("the stored row is\n  %s\nwant\n  %s", egRowText(stored), want)
			}
			// And so does the caller's copy of it — including tenant 7, which
			// the caller had set to 999 and the conflict clause refused, and
			// note, which is an explicit null in the table rather than the
			// undefined Opt that was written.
			if egRowText(row) != want {
				t.Fatalf("Save left the caller with\n  %s\nwhere the row is\n  %s",
					egRowText(row), want)
			}
		})
	}
}

// Update loads the row, diffs the DTO against it, then writes — and the row can
// go away in between. Whatever the engine, the answer has to be ErrNotFound:
// a model describing a row that does not exist is worse than an error, because
// the caller has no way to tell it apart from a successful update.
func TestUpdateOfARowThatVanishesUnderneathIt(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			plain := EgRows.Bind(tg.source)
			egSeed(t, plain, EgRow{ID: 1, Tenant: 1, Name: "doomed"})

			// The gap between the read and the write is the only place this race
			// lives, so the delete is wedged into exactly that gap.
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

// The same race, on a model that declares a `version` column — and this time the
// answer is not "the last writer wins quietly". Update is load-then-write, and
// what the lock does is make the write refuse to land on a row that moved on in
// between. README used to hand this hole to the caller with "wrap it if that
// matters"; the wrapping is one struct tag now.
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

			// The other writer goes in the gap between this Update's read and its
			// write — the only place a lost update can happen.
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

			// And the other writer's row is intact: the refusal is what makes it so.
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

			// Reading again and reapplying is the caller's way through, and it
			// works because the version they now hold is the current one.
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

// A filtered update writes rows nobody named, and it is the write most likely to
// happen while somebody is in the middle of an Update. It advances every row it
// touches for exactly that reason: a lock that only one of the two write paths
// respects protects nothing.
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

// Save is a whole-row overwrite with no WHERE clause, so it cannot check the
// lock — MySQL's ON DUPLICATE KEY UPDATE has nowhere to put a condition. What it
// must not do is wind the counter back: a Save built from a model somebody has
// been holding would otherwise hand every other stale copy a fresh licence.
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

			// row is the copy from before the update: version 0, stale.
			row.Name = "resaved"
			if _, err := vers.Save(ctx, &row); err != nil {
				t.Fatal(err)
			}
			if row.Version != 1 {
				t.Fatalf("the model came back at version %d; Save refreshes it, so it has to describe the row that is there", row.Version)
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

// ---------------------------------------------------------------------------
// collation

// LIKE is the one predicate whose answer belongs to the column's collation
// rather than to vv: MySQL's default is case-insensitive and PostgreSQL's
// is not. LikeIgnoreCase exists precisely so a caller who means "ignore case"
// can say it and get it on both.
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

			// The portable half: asked to ignore case, both engines do, in both
			// directions.
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

			// The half that is the column's business. A caller who writes Like or
			// Contains gets the collation's answer, and the two engines' answers
			// are different — which is the whole reason LikeIgnoreCase exists.
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

// ---------------------------------------------------------------------------
// NULL ordering

// Where a NULL sorts is the engine's decision, not vv's: PostgreSQL puts
// NULLs last on ASC and first on DESC, MySQL does the opposite. crud.Order can
// say which it wants — and that request is rendered for PostgreSQL only, because
// MySQL has no NULLS LAST clause. So `Asc(f).WithNullsLast()` is honoured on one
// engine and silently dropped on the other.
//
// Emulating it on MySQL is one expression (`ISNULL(col)` as a leading sort key)
// and it belongs in Order.render in crud/predicate.go.
func TestWhereNULLsSortIsTheEnginesChoiceAndTheHintIsPostgresOnly(t *testing.T) {
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

			// Where the two columns differ, so do the engines; the third case is
			// the one that matters, because there the caller said what they
			// wanted and only one engine listened.
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
					"1 5 <null> <null>", "<null> <null> 1 5"},
				{"descending, NULLs last requested", crud.Desc("Score").WithNullsLast(),
					"5 1 <null> <null>", "5 1 <null> <null>"},
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

// ---------------------------------------------------------------------------
// distinct

// DISTINCT removes a row only when nothing unique is projected, and the primary
// key used to be forced into every projection — so `?distinct=1&select=name`
// asked three articles with two titles for their distinct titles and got three
// rows back. Nothing failed, because the two unit tests asserted the statement
// text and the statement was exactly `SELECT DISTINCT "id", "name"`. This
// counts rows instead, on both engines, which is the only assertion the feature
// cannot pass while being a no-op.
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

			// The pager has to agree with what it paged. count(*) over the table
			// would promise four.
			page, err := rows.Get(ctx, crud.Distinct(), crud.Select("Name"), crud.Limit(1), crud.OrderBy(crud.Asc("Name")))
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 2 || page.TotalPages != 2 || len(page.Items) != 1 {
				t.Fatalf("page = %+v, want one of two distinct names over two pages", page)
			}

			// DISTINCT on its own is every column, which for a table with a key
			// is every row: nothing collapses, and nothing may be dropped either.
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

// Both engines refuse a SELECT DISTINCT ordered by a column outside the select
// list — 42P10 on PostgreSQL, ER_FIELD_IN_ORDER_NOT_SELECT on MySQL — and all
// three of distinct, select and sort arrive from the wire together. vv
// answers before the database does, with a *crud.SchemaError that a transport
// turns into a 400 naming the column, rather than passing the combination on to
// be refused as a 500.
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

			// And the way to ask for it that both engines do accept.
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

// Whatever the engine does with NULLs, paging over a nullable column must be a
// partition of the table: the primary-key tiebreaker crud/sqlrepo appends is what
// stops two rows that tie from swapping places between one page and the next and
// taking a row with them.
func TestPagingOverANullableColumnNeitherLosesNorRepeatsARow(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	for _, tg := range egEngines() {
		t.Run(tg.name, func(t *testing.T) {
			egWipe(t, tg.source)
			s := egWatch(tg.source)
			rows := EgRows.Bind(s)

			// Twelve rows and two distinct sort values: every page is nothing but
			// ties, which is the only shape where the tiebreaker matters.
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

			// And the reason it holds, in the statement itself: the sort the
			// caller asked for, then the key.
			q := tg.source.Dialect().Quote
			tail := fmt.Sprintf("ORDER BY %s ASC, %s ASC LIMIT %d OFFSET %d", q("score"), q("id"), size, size)
			if stmts := s.matching(tail); len(stmts) == 0 {
				t.Fatalf("no statement carried %q; the paged reads were %v", tail, s.matching("SELECT"))
			}
		})
	}
}

// crud.Raw rewrites `?` into the dialect's bind marker, which leaves no way to
// write a question mark that means itself — and PostgreSQL's jsonb operators are
// spelled `?`, `?|` and `?&`. `??` is that way out, and it is the sort of thing
// that is only ever proved by sending it: the renderer's own idea of how many
// arguments a fragment wants has to match the server's.
func TestRawEscapesAQuestionMarkForPostgresJSONBOperators(t *testing.T) {
	ctx := context.Background()
	egSetup(t)

	rows := EgRows.Bind(crudsql.Postgres(pgDB))
	egWipe(t, crudsql.Postgres(pgDB))
	egSeed(t, rows,
		EgRow{ID: 1, Tenant: 1, Name: "alpha"},
		EgRow{ID: 2, Tenant: 1, Name: "beta"},
	)

	// Renders as: ('{"' || name || '": 1}')::jsonb ? $1 — one literal operator,
	// one bound argument.
	n, err := rows.Count(ctx, crud.Where(crud.Raw(`('{"' || name || '": 1}')::jsonb ?? ?`, "beta")))
	if err != nil {
		t.Fatalf("the escaped operator did not reach PostgreSQL intact: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want the one row whose name is a key of its own document", n)
	}

	// And the escape consumes no argument: one marker, one value.
	if _, err := rows.Count(ctx, crud.Where(crud.Raw(`('{"a": 1}')::jsonb ?? ?`, "a", "b"))); err == nil {
		t.Fatal("a fragment with more arguments than markers was accepted")
	}
}

// ---------------------------------------------------------------------------
// integrity errors

// A constraint violation is the one failure a CRUD caller is guaranteed to meet,
// and the first guarantee both engines owe is that they refuse it — MySQL
// outside strict mode would quietly substitute a value for the NULL instead.
//
// The second guarantee is the adapters': whatever the driver hands back becomes
// crud.ErrConflict, so a transport answers 409 with a message instead of a 500
// whose body deliberately says nothing. There are two independent classifiers —
// crudpgx reads *pgconn.PgError, crudsql reaches for a SQLSTATE by shape because
// the dependency-free module may not name a driver's error type — so this runs
// over every target rather than over the two engines. With only the engines in
// the list, the pgx half was never executed at all.
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

					// The adapter classifies it, so a transport can answer 409
					// with a message. Unclassified it arrived as a bare 500,
					// whose body deliberately says nothing at all.
					if !errors.Is(err, crud.ErrConflict) {
						t.Fatalf("err = %v, want it to wrap crud.ErrConflict", err)
					}

					// Mistaken for something else would be worse still. A
					// violation must never look like a missing row or a
					// malformed request, because a transport turns those into a
					// 404 and a 400. This loop is the control on the leg below:
					// it holds whatever the classifier learned or failed to.
					for _, sentinel := range []error{crud.ErrNotFound, crud.ErrMissingID, crud.ErrReadOnly, crud.ErrForbidden} {
						if errors.Is(err, sentinel) {
							t.Fatalf("a constraint violation came back as %v", sentinel)
						}
					}

					// And it arrives carrying which violation it was — except
					// through ent, which is reached with crudsql.From and
					// therefore names no engine. That is the degradation
					// [[D-046]]'s last forbid buys, here against a real ORM: the
					// status never moves, the code is absent rather than wrong.
					f, has := errs.AsFault(err)
					if has != tg.classifies {
						t.Fatalf("a fault reached the caller = %v, want %v: %T: %v", has, tg.classifies, err, err)
					}
					if has && f.Code != tc.code {
						t.Fatalf("the fault says %q, want %q", f.Code, tc.code)
					}

					// Nothing was written, and the repository still works: a
					// failed statement must not poison the connection it ran on.
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

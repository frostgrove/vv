//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shardit-io/vv/adapter/crudpgx"
	"github.com/shardit-io/vv/adapter/crudsql"
	"github.com/shardit-io/vv/catalog"
	"github.com/shardit-io/vv/crud"
)

// What a catalog claims about a schema can only be checked against a server, and
// the four engines disagree about most of it. These tests are the record of what
// each one actually answers — not what a reading of the standards would predict,
// which is how the engine matrix this repository started with came to be half
// wrong.
//
// Never t.Parallel() here: every test shares the same physical tables.

// catTarget is one database reached one way, plus what the catalog must call it.
type catTarget struct {
	name    string
	db      string // which catSchema built it
	dialect string // what catalog.Catalog.Dialect must answer
	src     crud.Source
}

var (
	catOnce sync.Once
	catErr  error
)

// catEngines is its own four-engine walker and not a widened egEngines.
//
// egEngines() returns three and says in its own comment why SQLite is not one of
// them; changing it would change what every test that walks it runs against.
// corpus.Engines is the only four-engine list in the tree and it hands back DSNs
// rather than crud.Sources.
//
// PostgreSQL appears twice, through database/sql and through pgx. The two
// drivers disagree about what a NULL and a smallint scan into, and every
// introspection statement here was written to be portable between them — which
// is a claim, until both run it.
func catEngines(t *testing.T) []catTarget {
	t.Helper()
	ctx := context.Background()

	// The three servers are built once per process. The failure is recorded
	// rather than reported from inside the Once: a t.Fatalf there exits through
	// runtime.Goexit, the Once still marks itself done, and every later test
	// reports a missing table instead of the DDL error that caused it.
	catOnce.Do(func() {
		for _, s := range []struct {
			db  string
			src crud.Source
		}{
			{"postgres", crudsql.Postgres(pgDB)},
			{"mysql", crudsql.MySQL(myDB)},
			{"mariadb", crudsql.MySQL(mariaDB)},
		} {
			for _, stmt := range catSchema[s.db] {
				if _, err := s.src.Exec(ctx, stmt); err != nil {
					catErr = fmt.Errorf("%s: %s: %w", s.db, catFirstLine(stmt), err)
					return
				}
			}
		}
	})
	if catErr != nil {
		t.Fatalf("the cat_ tables were never built: %v", catErr)
	}

	return []catTarget{
		{"postgres", "postgres", "postgres", crudsql.Postgres(pgDB)},
		{"pgx", "postgres", "postgres", crudpgx.Open(pgPool)},
		{"mysql", "mysql", "mysql", crudsql.MySQL(myDB)},
		{"mariadb", "mariadb", "mariadb", crudsql.MySQL(mariaDB)},
		{"sqlite", "sqlite", "sqlite", crudsql.SQLite(catOpenSQLite(t))},
	}
}

// catOpenSQLite builds a fresh file-backed database holding only this fixture.
func catOpenSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, stmt := range catSchema["sqlite"] {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("sqlite: %s: %v", catFirstLine(stmt), err)
		}
	}
	return db
}

func catFirstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// catLoad loads one target's catalog, or fails the test.
func catLoad(t *testing.T, tg catTarget) catalog.Catalog {
	t.Helper()
	cat, err := catalog.Load(context.Background(), tg.src)
	if err != nil {
		t.Fatalf("%s: loading the catalog: %v", tg.name, err)
	}
	return cat
}

func catConstraint(t *testing.T, cat catalog.Catalog, table, name string) *catalog.Constraint {
	t.Helper()
	c, ok := cat.Constraint(table, name)
	if !ok {
		t.Fatalf("the catalog has no constraint %s on %s", name, table)
	}
	return c
}

// catUnreproducible reports a unique key this catalog cannot replay from a value
// — a partial index, a prefix key, or a key part that is an expression rather
// than a column. It is one rule written once, because the invariant under test
// is the same on all four engines even though the kind of key that trips it is
// not.
func catUnreproducible(c *catalog.Constraint) bool {
	if c.Partial {
		return true
	}
	for _, n := range c.Prefixes {
		if n != 0 {
			return true
		}
	}
	for _, col := range c.Columns {
		if col == "" {
			return true
		}
	}
	return false
}

// The twin. A unique key the probe could not reproduce is recorded as such, and
// one of the same shape that it could is not — without the second half, a
// catalog that marked everything unreproducible passes.
//
// Which key is the unreproducible one differs per engine and the invariant does
// not: partial on PostgreSQL and SQLite, a prefix key on MySQL and MariaDB,
// because CREATE UNIQUE INDEX ... WHERE is error 1064 on both of those.
func TestAnUnreproducibleUniqueKeyIsRecordedAndItsPlainTwinIsNot(t *testing.T) {
	pairs := 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			hard := catConstraint(t, cat, "cat_rows", "cat_rows_ux_hard")
			easy := catConstraint(t, cat, "cat_rows", "cat_rows_ux_easy")
			pairs++

			if !catUnreproducible(hard) {
				t.Errorf("the unreproducible key came back with nothing marking it: %+v", hard)
			}
			if catUnreproducible(easy) {
				t.Errorf("its plain twin of the same shape came back marked unreproducible too, so the marking says nothing: %+v", easy)
			}

			// And the marker is the engine's own, not any marker at all.
			switch tg.db {
			case "postgres":
				if !hard.Partial {
					t.Error("the partial index is not marked partial")
				}
				if hard.Predicate == "" {
					t.Error("PostgreSQL hands back the predicate through pg_get_expr and the catalog dropped it")
				}
				if easy.Partial || easy.Predicate != "" {
					t.Errorf("the plain twin carries a predicate: %+v", easy)
				}
			case "sqlite":
				if !hard.Partial {
					t.Error("the partial index is not marked partial")
				}
				// Partial with no predicate is the fact, not a bug: SQLite
				// reports partial = 1 and keeps the WHERE clause only inside the
				// index's DDL, which nothing here parses.
				if hard.Predicate != "" {
					t.Errorf("SQLite grew a predicate reader: %q — if that is DDL parsing, D-041 forbids it", hard.Predicate)
				}
				if hard.Definition == "" {
					t.Error("the index DDL sqlite_master holds was not recorded, so nothing evidences the predicate at all")
				}
				if easy.Partial {
					t.Errorf("the plain twin is marked partial: %+v", easy)
				}
			case "mysql", "mariadb":
				if hard.Partial {
					t.Error("a partial index cannot exist here, so something invented one")
				}
				if !catAnyPrefix(hard.Prefixes) {
					t.Errorf("the prefix index recorded no prefix length: %+v", hard)
				}
				if catAnyPrefix(easy.Prefixes) {
					t.Errorf("the full-length twin recorded a prefix length: %+v", easy)
				}
			}
		})
	}
	// A fixture whose DDL silently did not take would leave every subtest above
	// asserting nothing and the loop green.
	if pairs == 0 {
		t.Error("no engine produced the twin, so the twin asserted nothing")
	}
}

// catAllNamed returns every constraint of a name on a table. Catalog.Constraint
// answers one, and one name can be two objects — a unique key and a foreign key
// on MySQL, a CHECK and a bare unique index on PostgreSQL — so the whole list is
// the only place the second one is visible.
func catAllNamed(t *testing.T, cat catalog.Catalog, table, name string) []*catalog.Constraint {
	t.Helper()
	tbl, ok := cat.Table(table)
	if !ok {
		t.Fatalf("the catalog has no table %s", table)
	}
	var out []*catalog.Constraint
	for i := range tbl.Constraints {
		if tbl.Constraints[i].Name == name {
			out = append(out, &tbl.Constraints[i])
		}
	}
	return out
}

func catAnyPrefix(prefixes []int) bool {
	for _, n := range prefixes {
		if n != 0 {
			return true
		}
	}
	return false
}

// The third kind of key the catalog cannot replay from a value, and the one the
// twin above cannot reach: a key part that is an expression rather than a
// column. Every engine that has one reports it by leaving the column name empty,
// so this is what makes catUnreproducible's Columns clause do anything at all —
// without it that clause can be deleted with the suite green.
//
// The twin is the same cat_rows_ux_easy: two plain columns, no empty part and no
// expression text. A loader that emptied every column name would otherwise pass.
//
// MariaDB builds no such index — ((lower(slug))) is error 1064 there, which is
// [[D-019]] difference 9 — so its half of the assertion is that the catalog
// invents nothing.
func TestAnExpressionUniqueKeyIsRecordedAsOneAndItsPlainTwinIsNot(t *testing.T) {
	checked, absent := 0, 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			easy := catConstraint(t, cat, "cat_rows", "cat_rows_ux_easy")

			expr, ok := cat.Constraint("cat_rows", "cat_rows_ux_expr")
			if tg.db == "mariadb" {
				absent++
				if ok {
					t.Error("MariaDB reported an expression index and the fixture builds none here — if the server grew them, D-019 difference 9 needs rewriting")
				}
				return
			}
			if !ok {
				t.Fatal("the catalog has no constraint cat_rows_ux_expr on cat_rows")
			}
			checked++

			if !catUnreproducible(expr) {
				t.Errorf("an expression key came back with nothing marking it: %+v", expr)
			}
			if catUnreproducible(easy) {
				t.Errorf("its plain twin of the same shape came back marked unreproducible too, so the marking says nothing: %+v", easy)
			}
			if len(expr.Columns) != 1 || expr.Columns[0] != "" {
				t.Errorf("the expression key part is not recorded as an empty column entry: %q", expr.Columns)
			}
			for i, col := range easy.Columns {
				if col == "" {
					t.Errorf("part %d of the plain twin came back as an expression part", i)
				}
			}

			switch tg.db {
			case "postgres", "mysql":
				// These two hand the text back — pg_get_indexdef with a column
				// number, information_schema.STATISTICS.EXPRESSION.
				if len(expr.Expressions) != len(expr.Columns) {
					t.Errorf("Expressions and Columns are not parallel by position: %q against %q", expr.Expressions, expr.Columns)
				}
				if len(expr.Expressions) == 0 || !strings.Contains(strings.ToLower(expr.Expressions[0]), "lower") {
					t.Errorf("the engine's own expression text was dropped: %q", expr.Expressions)
				}
				for i, e := range easy.Expressions {
					if e != "" {
						t.Errorf("part %d of the plain twin carries expression text %q", i, e)
					}
				}
			case "sqlite":
				// The sibling of partial-with-no-predicate: index_xinfo marks
				// the part with cid = -2 and gives no text, and the only other
				// source is the index's DDL, which [[D-041]] forbids parsing.
				if len(expr.Expressions) != 0 {
					t.Errorf("SQLite grew an expression reader: %q — if that is DDL parsing, D-041 forbids it", expr.Expressions)
				}
				if expr.Definition == "" {
					t.Error("the index DDL sqlite_master holds was not recorded, so nothing evidences the expression at all")
				}
			}
		})
	}
	// A fixture whose DDL silently did not take, or one that grew an expression
	// index on MariaDB, would leave one half of this asserting nothing.
	if checked == 0 || absent == 0 {
		t.Errorf("%d engines reported an expression key and %d were checked for having none; both halves are needed", checked, absent)
	}
}

// A constraint the server does not apply until COMMIT is one a pre-flight probe
// must not claim to have checked. PostgreSQL is the only engine with the notion
// — MySQL and MariaDB reject DEFERRABLE on a UNIQUE constraint and SQLite's
// pragmas expose no deferrability at all — so the twin is per engine: the pair
// on PostgreSQL, and everywhere else the claim that nothing was invented.
func TestADeferrableConstraintIsRecordedAndItsImmediateTwinIsNot(t *testing.T) {
	pairs, flat := 0, 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			if tg.db != "postgres" {
				flat++
				tbl, ok := cat.Table("cat_rows")
				if !ok {
					t.Fatal("the catalog has no cat_rows")
				}
				for i := range tbl.Constraints {
					if c := &tbl.Constraints[i]; c.Deferrable {
						t.Errorf("%s reported %s as deferrable and this engine has no deferred constraints, so something invented one", tg.db, c.Name)
					}
				}
				return
			}
			pairs++
			def := catConstraint(t, cat, "cat_rows", "cat_rows_uc_def")
			imm := catConstraint(t, cat, "cat_rows", "cat_rows_uc")
			if !def.Deferrable {
				t.Error("a DEFERRABLE INITIALLY DEFERRED unique key came back immediate — a probe reading that would claim to have checked a key the server does not apply until COMMIT")
			}
			if imm.Deferrable {
				t.Error("the immediate twin came back deferrable too, so the flag says nothing")
			}
		})
	}
	if pairs == 0 || flat == 0 {
		t.Errorf("%d engines carried the pair and %d were checked for having none; both halves are needed", pairs, flat)
	}
}

// A foreign key's conindid names the index it *references* on the parent table,
// so an anti-join that stops at conindid deletes the parent's bare unique index.
// It is silent — Load succeeds — and what is lost is the one class of key
// PostgreSQL enforces under a name no constraint catalog knows, so a live 23505
// under that name resolves to nothing for the life of the process.
//
// PostgreSQL only: it is the only engine with the anti-join. MySQL and MariaDB
// list every unique index in TABLE_CONSTRAINTS and SQLite lists every index in
// pragma_index_list, so neither has anything to qualify.
func TestABareUniqueIndexAForeignKeyPointsAtIsStillInTheCatalog(t *testing.T) {
	checked := 0
	for _, tg := range catEngines(t) {
		if tg.db != "postgres" {
			continue
		}
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			checked++

			referenced := catConstraint(t, cat, "cat_ref", "cat_ref_code_ux")
			if referenced.Kind != catalog.KindUniqueIndex || strings.Join(referenced.Columns, ",") != "code" {
				t.Errorf("the referenced bare unique index came back as %s over %v", referenced.Kind, referenced.Columns)
			}

			// The twin: the same index with nothing pointing at it. Without it a
			// loader that lost every bare unique index would pass the half above
			// by failing it for a different reason, and with it the pair says
			// the referencing is what made the difference.
			alone := catConstraint(t, cat, "cat_ref", "cat_ref_alt_ux")
			if alone.Kind != catalog.KindUniqueIndex || strings.Join(alone.Columns, ",") != "alt" {
				t.Errorf("the unreferenced twin came back as %s over %v", alone.Kind, alone.Columns)
			}

			// The control in the other direction: the anti-join still has to do
			// its original job. cat_ref_pkey is a constraint backed by its own
			// index, so the unique-index read must skip it — read twice, its one
			// key column is appended twice and Columns stops being the key.
			// Deleting the NOT EXISTS outright fails here.
			pk := catAllNamed(t, cat, "cat_ref", "cat_ref_pkey")
			if len(pk) != 1 || pk[0].Kind != catalog.KindPrimaryKey {
				t.Fatalf("cat_ref_pkey came back as %d constraints, want one primary key: %+v", len(pk), pk)
			}
			if strings.Join(pk[0].Columns, ",") != "id" {
				t.Errorf("the primary key's own index was read a second time as a bare one: cat_ref_pkey covers %q", pk[0].Columns)
			}
		})
	}
	if checked == 0 {
		t.Error("no PostgreSQL target was walked, so this asserted nothing")
	}
}

// REFERENCES cat_ref with no column list is a foreign key against the parent's
// primary key, and pragma_foreign_key_list answers NULL for the parent column
// rather than naming it. Scanned into a string that NULL was a start-up refusal
// on a schema SQLite creates and enforces without complaint, which is
// [[D-041]]'s "do not read an empty catalog as a blocked introspection, or the
// reverse" read backwards.
//
// The empty RefColumns entry is the whole of what is known, and it stays
// parallel to Columns by position. Filling it in from the parent's own primary
// key would be inventing, which catalog/sqlite.go's header forbids.
func TestAShorthandReferencesRecordsNoParentColumnAndItsExplicitTwinDoes(t *testing.T) {
	checked := 0
	for _, tg := range catEngines(t) {
		if tg.db != "sqlite" {
			continue
		}
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			checked++

			short := catFindFK(t, cat, "cat_rows", "short_id")
			if short.RefTable != "cat_ref" {
				t.Errorf("the shorthand foreign key references %q, want cat_ref", short.RefTable)
			}
			if len(short.RefColumns) != len(short.Columns) {
				t.Errorf("RefColumns %q is not parallel to Columns %q", short.RefColumns, short.Columns)
			}
			if len(short.RefColumns) != 1 || short.RefColumns[0] != "" {
				t.Errorf("the unnamed parent column came back as %q, want one empty entry", short.RefColumns)
			}

			// The twin, on the same table: the explicit form names its column.
			// Without it a loader that dropped RefColumns entirely — or one that
			// never ran — would pass the half above.
			explicit := catFindFK(t, cat, "cat_rows", "upd_id")
			if strings.Join(explicit.RefColumns, ",") != "id" {
				t.Errorf("the explicit-form twin references columns %q, want [id]", explicit.RefColumns)
			}
		})
	}
	if checked == 0 {
		t.Error("no SQLite target was walked, so this asserted nothing")
	}
}

// One name, two objects on one table. An index name and a foreign-key name live
// in different namespaces on MySQL and MariaDB, and a constraint name and an
// index name do on PostgreSQL, so a build keyed on the bare name folds the two
// into one: the key parts of both end up in one Columns, which stops being
// parallel to Expressions, Prefixes and RefColumns — the position a probe reads
// its results by ([[D-042]]) — and one of the two objects disappears.
func TestTwoObjectsSharingOneNameStayTwoConstraints(t *testing.T) {
	checked := 0
	// Catalog.Constraint answers one of the two, and identical DDL must not
	// describe itself two ways. The two engines agree only because both reads
	// order the rows that decide it; nothing in either server promises that on
	// its own ([[D-014]]).
	answered := map[string]catalog.Kind{}
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			tbl, ok := cat.Table("cat_rows")
			if !ok {
				t.Fatal("the catalog has no cat_rows")
			}
			dual := catAllNamed(t, cat, "cat_rows", "cat_dual")

			// The control, and it needs no branch: the split happens for the
			// colliding name and nowhere else. On MySQL and MariaDB every unique
			// key is announced twice — by TABLE_CONSTRAINTS, which names the
			// kind, and by STATISTICS, which names the key parts — so a build
			// that kept objects apart by exact kind rather than by family would
			// split all of them and the count below would stop meaning anything.
			seen := map[string]int{}
			for i := range tbl.Constraints {
				seen[tbl.Constraints[i].Name]++
			}
			for i := range tbl.Constraints {
				name := tbl.Constraints[i].Name
				if name == "cat_dual" || seen[name] <= 1 {
					continue
				}
				t.Errorf("%s appears %d times on cat_rows and names one object", name, seen[name])
				seen[name] = 1
			}

			if tg.db == "sqlite" {
				// Neither collision is expressible: no pragma names a CHECK and
				// none names a foreign key, so SQLite has nothing to collide.
				if len(dual) != 0 {
					t.Errorf("SQLite reported %d constraints named cat_dual and the fixture builds none — if a pragma names one now, D-019 difference 9 needs rewriting", len(dual))
				}
				return
			}
			checked++
			if len(dual) != 2 {
				t.Fatalf("one name over two objects came back as %d constraints: %+v", len(dual), dual)
			}

			byKind := map[catalog.Kind]*catalog.Constraint{}
			for _, c := range dual {
				byKind[c.Kind] = c
			}
			switch tg.db {
			case "postgres":
				check, ok := byKind[catalog.KindCheck]
				if !ok {
					t.Fatalf("neither cat_dual is the CHECK: %+v", dual)
				}
				if strings.Join(check.Columns, ",") != "qty" {
					t.Errorf("the CHECK named cat_dual covers %q, want the column conkey names", check.Columns)
				}
				index, ok := byKind[catalog.KindUniqueIndex]
				if !ok {
					t.Fatalf("neither cat_dual is the unique index: %+v", dual)
				}
				if strings.Join(index.Columns, ",") != "plain" {
					t.Errorf("the unique index named cat_dual covers %q, want [plain]", index.Columns)
				}
			case "mysql", "mariadb":
				fk, ok := byKind[catalog.KindForeignKey]
				if !ok {
					t.Fatalf("neither cat_dual is the foreign key: %+v", dual)
				}
				if strings.Join(fk.Columns, ",") != "shr_id" || strings.Join(fk.RefColumns, ",") != "id" {
					t.Errorf("the foreign key named cat_dual is %q -> %q, want [shr_id] -> [id]", fk.Columns, fk.RefColumns)
				}
				if len(fk.RefColumns) != len(fk.Columns) {
					t.Errorf("RefColumns %q is not parallel to Columns %q", fk.RefColumns, fk.Columns)
				}
				key, ok := byKind[catalog.KindUnique]
				if !ok {
					t.Fatalf("neither cat_dual is the unique key: %+v", dual)
				}
				answered[tg.db] = catConstraint(t, cat, "cat_rows", "cat_dual").Kind
				if strings.Join(key.Columns, ",") != "shr_id" {
					t.Errorf("the unique key named cat_dual covers %q, want [shr_id]", key.Columns)
				}
				if len(key.Prefixes) != len(key.Columns) {
					t.Errorf("Prefixes %v is not parallel to Columns %q", key.Prefixes, key.Columns)
				}
			}
		})
	}
	if checked == 0 {
		t.Error("no engine carried the collision, so this asserted nothing")
	}
	if answered["mysql"] == 0 || answered["mariadb"] == 0 {
		t.Error("one of the two MySQL-family engines never reached the lookup, so the comparison below asserts nothing")
	} else if answered["mysql"] != answered["mariadb"] {
		t.Errorf("MySQL answers %s for cat_dual and MariaDB %s, for identical DDL", answered["mysql"], answered["mariadb"])
	}
}

// [[D-019]] difference 9 in executable form. Two engines separate a unique
// constraint from a bare unique index and two do not, and both directions are
// asserted per engine — a loader that reported the split everywhere and one that
// reported it nowhere each fail.
func TestAUniqueIndexAndAUniqueConstraintAreToldApartWhereTheEngineTellsThemApart(t *testing.T) {
	checked := 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			index := catConstraint(t, cat, "cat_rows", "cat_rows_ux_easy")
			checked++

			switch tg.db {
			case "postgres":
				declared := catConstraint(t, cat, "cat_rows", "cat_rows_uc")
				if declared.Kind != catalog.KindUnique {
					t.Errorf("a declared UNIQUE constraint came back as %s", declared.Kind)
				}
				if index.Kind != catalog.KindUniqueIndex {
					t.Errorf("a bare CREATE UNIQUE INDEX came back as %s, so the two are not told apart", index.Kind)
				}
			case "sqlite":
				// SQLite reports the kind and loses the name: the constraint
				// declared as cat_rows_uc comes back under a generated
				// sqlite_autoindex_ name, so a phase-7 lookup by the author's
				// name misses here and nowhere else.
				if _, ok := cat.Constraint("cat_rows", "cat_rows_uc"); ok {
					t.Error("SQLite kept the declared constraint name — if that is true now, D-019 difference 9 needs rewriting")
				}
				declared := catFindKind(t, cat, "cat_rows", catalog.KindUnique)
				if !strings.HasPrefix(declared.Name, "sqlite_autoindex_") {
					t.Errorf("the declared unique constraint came back as %q, want a generated sqlite_autoindex_ name", declared.Name)
				}
				if strings.Join(declared.Columns, ",") != "req,qty" {
					t.Errorf("the generated name covers %v, want the columns cat_rows_uc was declared over", declared.Columns)
				}
				if index.Kind != catalog.KindUniqueIndex {
					t.Errorf("a bare CREATE UNIQUE INDEX came back as %s", index.Kind)
				}
			case "mysql", "mariadb":
				// The half that stops the split reading as universal. Measured
				// on 8.4 and 11.4: information_schema.TABLE_CONSTRAINTS lists
				// every unique index as UNIQUE, so there is nothing to tell
				// apart and the catalog must not pretend otherwise.
				declared := catConstraint(t, cat, "cat_rows", "cat_rows_uc")
				if declared.Kind != catalog.KindUnique {
					t.Errorf("a declared UNIQUE constraint came back as %s", declared.Kind)
				}
				if index.Kind != catalog.KindUnique {
					t.Errorf("a bare CREATE UNIQUE INDEX came back as %s — this engine calls it UNIQUE and a catalog that split them would be inventing the distinction", index.Kind)
				}
			}
		})
	}
	if checked == 0 {
		t.Error("no engine was checked, so this asserted nothing")
	}
}

// catFindKind returns the one constraint of a kind on a table, failing when
// there is not exactly one.
func catFindKind(t *testing.T, cat catalog.Catalog, table string, kind catalog.Kind) *catalog.Constraint {
	t.Helper()
	tbl, ok := cat.Table(table)
	if !ok {
		t.Fatalf("the catalog has no table %s", table)
	}
	var found []*catalog.Constraint
	for i := range tbl.Constraints {
		if tbl.Constraints[i].Kind == kind {
			found = append(found, &tbl.Constraints[i])
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s has %d constraints of kind %s, want exactly one", table, len(found), kind)
	}
	return found[0]
}

// The catalog produces the fourth name in the vocabulary errs.Detail.Dialect
// uses, which crud.Dialect.Name cannot: it answers "mysql" for MariaDB.
func TestTheCatalogNamesMariaDBRatherThanCallingItMySQL(t *testing.T) {
	seen := map[string]bool{}
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			if cat.Dialect() != tg.dialect {
				t.Errorf("the catalog calls this database %q, want %q", cat.Dialect(), tg.dialect)
			}
			seen[cat.Dialect()] = true

			// The half that shows the detection is worth its round trip: the
			// seam calls both servers "mysql", so the name above is information
			// the seam does not have.
			if tg.db == "mysql" || tg.db == "mariadb" {
				if got := tg.src.Dialect().Name(); got != "mysql" {
					t.Errorf("crud.Dialect.Name answers %q here — if it tells the two apart now, the version probe is dead weight", got)
				}
			}
		})
	}
	for _, want := range []string{"postgres", "mysql", "mariadb", "sqlite"} {
		if !seen[want] {
			t.Errorf("no engine was named %q, so a detector that always answered one thing would pass", want)
		}
	}
}

// A bare table name means whatever the loading connection resolved it to, and
// the catalog records where that was. Resolving it lazily per connection is what
// [[D-041]] forbids, and it is also what a DSN key would silently merge.
func TestABareTableNameResolvesOnceAndTheResolvedSchemaIsRecorded(t *testing.T) {
	ctx := context.Background()
	for _, stmt := range catSearchPathSchema {
		if _, err := pgDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", catFirstLine(stmt), err)
		}
	}

	one := catLoad(t, catTarget{name: "cat_s1", src: crudsql.Postgres(catSearchPath(t, "cat_s1"))})
	two := catLoad(t, catTarget{name: "cat_s2", src: crudsql.Postgres(catSearchPath(t, "cat_s2"))})

	a, ok := one.Table("cat_same")
	if !ok {
		t.Fatal("the first connection's catalog does not know cat_same")
	}
	b, ok := two.Table("cat_same")
	if !ok {
		t.Fatal("the second connection's catalog does not know cat_same")
	}
	if a.Schema == b.Schema {
		t.Fatalf("both connections resolved cat_same to %s, so the search_path was never read", a.Schema)
	}
	if a.Columns[0].Name == b.Columns[0].Name {
		t.Errorf("both catalogs describe the same table (%s), so the bare name was resolved once for everybody",
			a.Columns[0].Name)
	}

	// The control. Without it the difference above could be two unrelated
	// reads; with it, a loader that resolved the bare name lazily on whatever
	// connection asked would answer identically from both and fail here.
	alsoOne := catLoad(t, catTarget{name: "cat_s1 again", src: crudsql.Postgres(catSearchPath(t, "cat_s1"))})
	c, ok := alsoOne.Table("cat_same")
	if !ok {
		t.Fatal("a second connection on the same search_path does not know cat_same")
	}
	if c.Schema != a.Schema || c.Columns[0].Name != a.Columns[0].Name {
		t.Errorf("two connections with one search_path described cat_same two ways: %s.%s versus %s.%s",
			a.Schema, a.Columns[0].Name, c.Schema, c.Columns[0].Name)
	}
}

// catSearchPath opens a handle whose connections resolve bare names in schema.
func catSearchPath(t *testing.T, schema string) *sql.DB {
	t.Helper()
	u, err := url.Parse(pgDSN)
	if err != nil {
		t.Fatalf("the Postgres DSN is not a URL, cannot build a second one: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatalf("opening a handle on search_path=%s: %v", schema, err)
	}
	db.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// SQLite's pragma reports on_update before on_delete, which is the opposite of
// the order the DDL is written in. Two foreign keys with the actions the other
// way round is what makes a swap visible: with one action each way, both fail.
func TestForeignKeysCarryTheirActionsInTheOrderTheEngineReportsThem(t *testing.T) {
	checked := 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			// Looked up by column rather than by name: SQLite records no name
			// for a foreign key at all.
			del := catFindFK(t, cat, "cat_rows", "del_id")
			upd := catFindFK(t, cat, "cat_rows", "upd_id")
			checked++

			for _, tc := range []struct {
				what             string
				fk               *catalog.Constraint
				onDelete, onUpda string
			}{
				{"ON DELETE CASCADE ON UPDATE SET NULL", del, "CASCADE", "SET NULL"},
				{"ON DELETE SET NULL ON UPDATE CASCADE", upd, "SET NULL", "CASCADE"},
			} {
				if tc.fk.OnDelete != tc.onDelete || tc.fk.OnUpdate != tc.onUpda {
					t.Errorf("%s came back ON DELETE %s ON UPDATE %s — the two actions are swapped",
						tc.what, tc.fk.OnDelete, tc.fk.OnUpdate)
				}
				if tc.fk.RefTable != "cat_ref" {
					t.Errorf("%s references %q, want cat_ref", tc.what, tc.fk.RefTable)
				}
				if strings.Join(tc.fk.RefColumns, ",") != "id" {
					t.Errorf("%s references columns %v, want [id]", tc.what, tc.fk.RefColumns)
				}
			}

			if tg.db == "sqlite" {
				// A foreign key SQLite records no name for gets one from its
				// position, and the constraint map keys on the name: a synthetic
				// name equal to a real index name would answer about the wrong
				// object. The sqlite_ prefix is what makes that impossible, so
				// it is asserted rather than assumed.
				if !strings.HasPrefix(del.Name, "sqlite_") {
					t.Errorf("the synthetic foreign-key name is %q — without the sqlite_ prefix it can collide with a user index of the same name", del.Name)
				}
				// The control: the prefix protects nothing unless the engine
				// refuses it for user objects. If this CREATE ever succeeds, the
				// assertion above has stopped proving anything.
				if _, err := tg.src.Exec(context.Background(), "CREATE TABLE sqlite_cat_probe (id INTEGER)"); err == nil {
					_, _ = tg.src.Exec(context.Background(), "DROP TABLE sqlite_cat_probe")
					t.Error("this SQLite accepts a sqlite_-prefixed user table, so the prefix no longer keeps synthetic constraint names out of the user's namespace")
				}
			}
		})
	}
	if checked == 0 {
		t.Error("no engine was checked, so this asserted nothing")
	}
}

func catFindFK(t *testing.T, cat catalog.Catalog, table, column string) *catalog.Constraint {
	t.Helper()
	tbl, ok := cat.Table(table)
	if !ok {
		t.Fatalf("the catalog has no table %s", table)
	}
	for i := range tbl.Constraints {
		c := &tbl.Constraints[i]
		if c.Kind == catalog.KindForeignKey && len(c.Columns) == 1 && c.Columns[0] == column {
			return c
		}
	}
	t.Fatalf("%s has no foreign key on %s", table, column)
	return nil
}

// Every field the probe will read, asserted against its own opposite on the
// column next to it. That pairing is the control: a loader that hardcoded any of
// these answers fails the other half, and the fixture carries both shapes of
// every column so that it can.
func TestEachEngineReportsWhatTheProbeWillNeed(t *testing.T) {
	checked := 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			tbl, ok := cat.Table("cat_rows")
			if !ok {
				t.Fatal("the catalog has no cat_rows")
			}
			checked++

			req := catColumn(t, tbl, "req")
			opt := catColumn(t, tbl, "opt")
			gen := catColumn(t, tbl, "gen")
			plain := catColumn(t, tbl, "plain")
			qty := catColumn(t, tbl, "qty")
			note := catColumn(t, tbl, "note")

			// The declaration order is the same in every catSchema entry, so the
			// same ordinals here on all five targets are what says SQLite's cid
			// — the one 0-based ordinal of the four engines — is still shifted.
			// Absolutes and not a sorted order: a set shifted by one is still in
			// order, and a probe binding a violation by position would bind it
			// to the field next door.
			if id := catColumn(t, tbl, "id"); id.Position != 1 {
				t.Errorf("the first declared column reports position %d, want 1", id.Position)
			}
			for _, w := range []struct {
				col *catalog.Column
				pos int
			}{{req, 2}, {opt, 3}, {qty, 4}, {note, 5}, {gen, 6}, {plain, 7}} {
				if w.col.Position != w.pos {
					t.Errorf("%s reports position %d, want the engine's own 1-based ordinal %d", w.col.Name, w.col.Position, w.pos)
				}
			}

			if req.Nullable {
				t.Error("a NOT NULL column is reported nullable — a probe built on that bit invents violations on correct fields")
			}
			if !opt.Nullable {
				t.Error("a nullable column is reported NOT NULL")
			}

			// SQLite records VARCHAR(255) as type text and enforces no width,
			// so a catalog answering 255 there would claim an enforcement that
			// does not exist ([[D-019]] difference 6).
			wantMax := 255
			if tg.db == "sqlite" {
				wantMax = 0
				if !strings.Contains(strings.ToUpper(req.Type), "VARCHAR(255)") {
					t.Errorf("the declared width is not even carried as type text: %q", req.Type)
				}
			}
			if req.MaxLength != wantMax {
				t.Errorf("a VARCHAR(255) reports MaxLength %d, want %d", req.MaxLength, wantMax)
			}
			if opt.MaxLength == req.MaxLength && wantMax != 0 {
				t.Errorf("an unbounded column reports the same MaxLength as a bounded one (%d)", opt.MaxLength)
			}

			if !gen.Generated {
				t.Error("a generated column is not reported generated")
			}
			if plain.Generated {
				t.Error("a plain column is reported generated")
			}

			if qty.Default == nil {
				t.Error("a column with DEFAULT 7 reports no default — a NOT NULL column that has one cannot produce a not-null violation for an omitted field")
			} else if !strings.Contains(*qty.Default, "7") {
				t.Errorf("the default came back %q, want the engine's own rendering of 7", *qty.Default)
			}
			if note.Default != nil {
				t.Errorf("a column with no DEFAULT clause reports one: %q", *note.Default)
			}

			if len(tbl.PrimaryKey) != 1 || tbl.PrimaryKey[0] != "id" {
				t.Errorf("the primary key came back %v, want [id]", tbl.PrimaryKey)
			}

			if tg.db == "sqlite" {
				// No PRAGMA lists a CHECK, and the alternative is finding it in
				// the table's DDL, which is the DDL model D-041 forbids. The
				// text is carried verbatim and read by nobody.
				if _, ok := cat.Constraint("cat_rows", "cat_rows_qty_ck"); ok {
					t.Error("SQLite reported a CHECK constraint — if a pragma does that now, D-019 difference 9 needs rewriting")
				}
				if !strings.Contains(tbl.Definition, "cat_rows_qty_ck") {
					t.Error("the table DDL sqlite_master holds was not recorded, so the CHECK is evidenced nowhere at all")
				}
				return
			}
			check := catConstraint(t, cat, "cat_rows", "cat_rows_qty_ck")
			if check.Kind != catalog.KindCheck {
				t.Errorf("the CHECK came back as %s", check.Kind)
			}
			if check.Definition == "" {
				t.Error("the CHECK's own text was not recorded")
			}

			// PostgreSQL is the only engine that reports a CHECK's columns:
			// conkey names them, and a CHECK that names none has no key part at
			// all. The pair is the control — an empty name appended there is the
			// same shape an expression key part uses, so a probe reading Columns
			// by position would bind the violation to a field that is not there,
			// and a loader that dropped every CHECK column would pass the second
			// half alone.
			if tg.db == "postgres" {
				if strings.Join(check.Columns, ",") != "qty" {
					t.Errorf("the CHECK over qty covers %v, want [qty]", check.Columns)
				}
				none := catConstraint(t, cat, "cat_rows", "cat_rows_always_ck")
				if len(none.Columns) != 0 {
					t.Errorf("a CHECK that names no column covers %d key parts (%q), want none", len(none.Columns), none.Columns)
				}
			} else if check.Columns != nil {
				// Nil is not empty, and the engine split gives the twin for
				// free: PostgreSQL reads a CHECK's columns out of conkey, and
				// MySQL and MariaDB hand back the clause and no columns at all.
				// A probe that read "not known" as "no columns" would treat a
				// constraint it cannot reproduce as one with a trivially
				// reproducible key. Written as a nil test and never as
				// len() == 0: the len form passes under exactly the mutation
				// this exists to catch.
				t.Errorf("%s reported CHECK columns %#v — an empty non-nil slice here is the conflation catalog/doc.go forbids; if it grew a reader, that rule needs rewriting", tg.db, check.Columns)
			}

			// The control for the line above: a constraint whose columns every
			// engine does report must be non-nil everywhere, so a loader that
			// answered nil for every constraint could not pass the MySQL half by
			// accident.
			if uc := catConstraint(t, cat, "cat_rows", "cat_rows_uc"); uc.Columns == nil {
				t.Error("a UNIQUE constraint reports no columns at all, so the CHECK's nil above proves nothing")
			}

			// The constraint carries the schema its table resolved to, not "".
			if check.Schema != tbl.Schema || check.Schema == "" {
				t.Errorf("the CHECK says it lives in %q and its table in %q", check.Schema, tbl.Schema)
			}
		})
	}
	if checked == 0 {
		t.Error("no engine was checked, so this asserted nothing")
	}
}

func catColumn(t *testing.T, tbl *catalog.Table, name string) *catalog.Column {
	t.Helper()
	c, ok := tbl.Column(name)
	if !ok {
		t.Fatalf("%s has no column %s", tbl.Name, name)
	}
	return c
}

// One Set, four live databases: each one is read once and each one keeps its own
// schema. This is the identity rule against real handles rather than doubles.
func TestOneSetHoldsFourLiveDatabasesWithoutMergingThem(t *testing.T) {
	ctx := context.Background()
	var set catalog.Set

	seen := map[catalog.Catalog]string{}
	for _, tg := range catEngines(t) {
		cat, err := set.Load(ctx, tg.src)
		if err != nil {
			t.Fatalf("%s: %v", tg.name, err)
		}
		if other, ok := seen[cat]; ok && other != tg.name {
			t.Errorf("%s and %s were given one catalog", tg.name, other)
		}
		seen[cat] = tg.name

		again, err := set.Load(ctx, tg.src)
		if err != nil {
			t.Fatal(err)
		}
		if again != cat {
			t.Errorf("%s was introspected twice through one Set", tg.name)
		}
		if found, ok := set.For(tg.src); !ok || found != cat {
			t.Errorf("%s could not be found again in the Set that just loaded it", tg.name)
		}
		if cat.Dialect() != tg.dialect {
			t.Errorf("%s came back as %q", tg.name, cat.Dialect())
		}
	}

	// The two PostgreSQL targets are two handles over one server, so they are
	// two catalogs — the control that says this Set is keyed on the handle and
	// not on the server, which is the whole of the search_path argument.
	if len(seen) != 5 {
		t.Errorf("five sources produced %d catalogs, want five", len(seen))
	}
}

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

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/catalog"
)

type catTarget struct {
	name     string
	database string
	dialect  string
	source   crud.Source
}

var (
	catOnce sync.Once
	catErr  error
)

func catEngines(t *testing.T) []catTarget {
	t.Helper()
	ctx := context.Background()

	catOnce.Do(func() {
		for _, s := range []struct {
			database string
			source   crud.Source
		}{
			{"postgres", crudsql.Postgres(pgDB)},
			{"mysql", crudsql.MySQL(myDB)},
			{"mariadb", crudsql.MySQL(mariaDB)},
		} {
			for _, stmt := range catSchema[s.database] {
				if _, err := s.source.Exec(ctx, stmt); err != nil {
					catErr = fmt.Errorf("%s: %s: %w", s.database, catFirstLine(stmt), err)
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

func catOpenSQLite(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	for _, stmt := range catSchema["sqlite"] {
		if _, err := database.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("sqlite: %s: %v", catFirstLine(stmt), err)
		}
	}
	return database
}

func catFirstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func catLoad(t *testing.T, tg catTarget) catalog.Catalog {
	t.Helper()
	cat, err := catalog.Load(context.Background(), tg.source)
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

			switch tg.database {
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

	if pairs == 0 {
		t.Error("no engine produced the twin, so the twin asserted nothing")
	}
}

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

func TestAnExpressionUniqueKeyIsRecordedAsOneAndItsPlainTwinIsNot(t *testing.T) {
	checked, absent := 0, 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			easy := catConstraint(t, cat, "cat_rows", "cat_rows_ux_easy")

			expr, ok := cat.Constraint("cat_rows", "cat_rows_ux_expr")
			if tg.database == "mariadb" {
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

			switch tg.database {
			case "postgres", "mysql":
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
				if len(expr.Expressions) != 0 {
					t.Errorf("SQLite grew an expression reader: %q — if that is DDL parsing, D-041 forbids it", expr.Expressions)
				}
				if expr.Definition == "" {
					t.Error("the index DDL sqlite_master holds was not recorded, so nothing evidences the expression at all")
				}
			}
		})
	}

	if checked == 0 || absent == 0 {
		t.Errorf("%d engines reported an expression key and %d were checked for having none; both halves are needed", checked, absent)
	}
}

func TestADeferrableConstraintIsRecordedAndItsImmediateTwinIsNot(t *testing.T) {
	pairs, flat := 0, 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			if tg.database != "postgres" {
				flat++
				tbl, ok := cat.Table("cat_rows")
				if !ok {
					t.Fatal("the catalog has no cat_rows")
				}
				for i := range tbl.Constraints {
					if c := &tbl.Constraints[i]; c.Deferrable {
						t.Errorf("%s reported %s as deferrable and this engine has no deferred constraints, so something invented one", tg.database, c.Name)
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

func TestABareUniqueIndexAForeignKeyPointsAtIsStillInTheCatalog(t *testing.T) {
	checked := 0
	for _, tg := range catEngines(t) {
		if tg.database != "postgres" {
			continue
		}
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			checked++

			referenced := catConstraint(t, cat, "cat_ref", "cat_ref_code_ux")
			if referenced.Kind != catalog.KindUniqueIndex || strings.Join(referenced.Columns, ",") != "code" {
				t.Errorf("the referenced bare unique index came back as %s over %v", referenced.Kind, referenced.Columns)
			}

			alone := catConstraint(t, cat, "cat_ref", "cat_ref_alt_ux")
			if alone.Kind != catalog.KindUniqueIndex || strings.Join(alone.Columns, ",") != "alt" {
				t.Errorf("the unreferenced twin came back as %s over %v", alone.Kind, alone.Columns)
			}

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

func TestAShorthandReferencesRecordsNoParentColumnAndItsExplicitTwinDoes(t *testing.T) {
	checked := 0
	for _, tg := range catEngines(t) {
		if tg.database != "sqlite" {
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

func TestTwoObjectsSharingOneNameStayTwoConstraints(t *testing.T) {
	checked := 0

	answered := map[string]catalog.Kind{}
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			tbl, ok := cat.Table("cat_rows")
			if !ok {
				t.Fatal("the catalog has no cat_rows")
			}
			dual := catAllNamed(t, cat, "cat_rows", "cat_dual")

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

			if tg.database == "sqlite" {
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
			switch tg.database {
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
				answered[tg.database] = catConstraint(t, cat, "cat_rows", "cat_dual").Kind
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

func TestAUniqueIndexAndAUniqueConstraintAreToldApartWhereTheEngineTellsThemApart(t *testing.T) {
	checked := 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			index := catConstraint(t, cat, "cat_rows", "cat_rows_ux_easy")
			checked++

			switch tg.database {
			case "postgres":
				declared := catConstraint(t, cat, "cat_rows", "cat_rows_uc")
				if declared.Kind != catalog.KindUnique {
					t.Errorf("a declared UNIQUE constraint came back as %s", declared.Kind)
				}
				if index.Kind != catalog.KindUniqueIndex {
					t.Errorf("a bare CREATE UNIQUE INDEX came back as %s, so the two are not told apart", index.Kind)
				}
			case "sqlite":
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

func TestTheCatalogNamesMariaDBRatherThanCallingItMySQL(t *testing.T) {
	seen := map[string]bool{}
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)
			if cat.Dialect() != tg.dialect {
				t.Errorf("the catalog calls this database %q, want %q", cat.Dialect(), tg.dialect)
			}
			seen[cat.Dialect()] = true

			if tg.database == "mysql" || tg.database == "mariadb" {
				if got := tg.source.Dialect().Name(); got != "mysql" {
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

func TestABareTableNameResolvesOnceAndTheResolvedSchemaIsRecorded(t *testing.T) {
	ctx := context.Background()
	for _, stmt := range catSearchPathSchema {
		if _, err := pgDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", catFirstLine(stmt), err)
		}
	}

	one := catLoad(t, catTarget{name: "cat_s1", source: crudsql.Postgres(catSearchPath(t, "cat_s1"))})
	two := catLoad(t, catTarget{name: "cat_s2", source: crudsql.Postgres(catSearchPath(t, "cat_s2"))})

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

	alsoOne := catLoad(t, catTarget{name: "cat_s1 again", source: crudsql.Postgres(catSearchPath(t, "cat_s1"))})
	c, ok := alsoOne.Table("cat_same")
	if !ok {
		t.Fatal("a second connection on the same search_path does not know cat_same")
	}
	if c.Schema != a.Schema || c.Columns[0].Name != a.Columns[0].Name {
		t.Errorf("two connections with one search_path described cat_same two ways: %s.%s versus %s.%s",
			a.Schema, a.Columns[0].Name, c.Schema, c.Columns[0].Name)
	}
}

func catSearchPath(t *testing.T, schema string) *sql.DB {
	t.Helper()
	u, err := url.Parse(pgDSN)
	if err != nil {
		t.Fatalf("the Postgres DSN is not a URL, cannot build a second one: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	database, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatalf("opening a handle on search_path=%s: %v", schema, err)
	}
	database.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestForeignKeysCarryTheirActionsInTheOrderTheEngineReportsThem(t *testing.T) {
	checked := 0
	for _, tg := range catEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			cat := catLoad(t, tg)

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

			if tg.database == "sqlite" {
				if !strings.HasPrefix(del.Name, "sqlite_") {
					t.Errorf("the synthetic foreign-key name is %q — without the sqlite_ prefix it can collide with a user index of the same name", del.Name)
				}

				if _, err := tg.source.Exec(context.Background(), "CREATE TABLE sqlite_cat_probe (id INTEGER)"); err == nil {
					_, _ = tg.source.Exec(context.Background(), "DROP TABLE sqlite_cat_probe")
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

			request := catColumn(t, tbl, "req")
			opt := catColumn(t, tbl, "opt")
			gen := catColumn(t, tbl, "gen")
			plain := catColumn(t, tbl, "plain")
			qty := catColumn(t, tbl, "qty")
			note := catColumn(t, tbl, "note")

			if id := catColumn(t, tbl, "id"); id.Position != 1 {
				t.Errorf("the first declared column reports position %d, want 1", id.Position)
			}
			for _, w := range []struct {
				col *catalog.Column
				pos int
			}{{request, 2}, {opt, 3}, {qty, 4}, {note, 5}, {gen, 6}, {plain, 7}} {
				if w.col.Position != w.pos {
					t.Errorf("%s reports position %d, want the engine's own 1-based ordinal %d", w.col.Name, w.col.Position, w.pos)
				}
			}

			if request.Nullable {
				t.Error("a NOT NULL column is reported nullable — a probe built on that bit invents violations on correct fields")
			}
			if !opt.Nullable {
				t.Error("a nullable column is reported NOT NULL")
			}

			wantMax := 255
			if tg.database == "sqlite" {
				wantMax = 0
				if !strings.Contains(strings.ToUpper(request.Type), "VARCHAR(255)") {
					t.Errorf("the declared width is not even carried as type text: %q", request.Type)
				}
			}
			if request.MaxLength != wantMax {
				t.Errorf("a VARCHAR(255) reports MaxLength %d, want %d", request.MaxLength, wantMax)
			}
			if opt.MaxLength == request.MaxLength && wantMax != 0 {
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

			if tg.database == "sqlite" {
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

			if tg.database == "postgres" {
				if strings.Join(check.Columns, ",") != "qty" {
					t.Errorf("the CHECK over qty covers %v, want [qty]", check.Columns)
				}
				none := catConstraint(t, cat, "cat_rows", "cat_rows_always_ck")
				if len(none.Columns) != 0 {
					t.Errorf("a CHECK that names no column covers %d key parts (%q), want none", len(none.Columns), none.Columns)
				}
			} else if check.Columns != nil {
				t.Errorf("%s reported CHECK columns %#v — an empty non-nil slice here is the conflation crud/catalog/doc.go forbids; if it grew a reader, that rule needs rewriting", tg.database, check.Columns)
			}

			if uc := catConstraint(t, cat, "cat_rows", "cat_rows_uc"); uc.Columns == nil {
				t.Error("a UNIQUE constraint reports no columns at all, so the CHECK's nil above proves nothing")
			}

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

func TestOneSetHoldsFourLiveDatabasesWithoutMergingThem(t *testing.T) {
	ctx := context.Background()
	var set catalog.Set

	seen := map[catalog.Catalog]string{}
	for _, tg := range catEngines(t) {
		cat, err := set.Load(ctx, tg.source)
		if err != nil {
			t.Fatalf("%s: %v", tg.name, err)
		}
		if other, ok := seen[cat]; ok && other != tg.name {
			t.Errorf("%s and %s were given one catalog", tg.name, other)
		}
		seen[cat] = tg.name

		again, err := set.Load(ctx, tg.source)
		if err != nil {
			t.Fatal(err)
		}
		if again != cat {
			t.Errorf("%s was introspected twice through one Set", tg.name)
		}
		if found, ok := set.For(tg.source); !ok || found != cat {
			t.Errorf("%s could not be found again in the Set that just loaded it", tg.name)
		}
		if cat.Dialect() != tg.dialect {
			t.Errorf("%s came back as %q", tg.name, cat.Dialect())
		}
	}

	if len(seen) != 5 {
		t.Errorf("five sources produced %d catalogs, want five", len(seen))
	}
}

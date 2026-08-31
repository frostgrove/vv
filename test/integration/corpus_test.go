//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
	"github.com/frostgrove/vv/test/corpus"
)

func TestTheCorpusStillDescribesTheseServers(t *testing.T) {
	ctx := context.Background()
	dir, err := corpus.Dir()
	if err != nil {
		t.Fatal(err)
	}

	redacted := 0

	for _, e := range corpus.Engines(t.TempDir()) {
		t.Run(e.Name, func(t *testing.T) {
			want, err := sqlerr.Load(dir, e.Name)
			if err != nil {
				t.Fatalf("reading the checked-in corpus: %v", err)
			}
			got, err := corpus.Capture(ctx, e)
			if err != nil {
				t.Fatalf("recapturing: %v", err)
			}
			if len(got.Cases) != len(want.Cases) {
				t.Fatalf("captured %d cases, the corpus has %d — run make corpus",
					len(got.Cases), len(want.Cases))
			}
			for i, g := range got.Cases {
				w := want.Cases[i]
				if g.Name != w.Name {
					t.Fatalf("case %d is %q, the corpus has %q — run make corpus", i, g.Name, w.Name)
				}
				if (g.Unreachable == "") != (w.Unreachable == "") {
					t.Errorf("%s: the corpus says reachable=%v and this server says %v",
						g.Name, w.Unreachable == "", g.Unreachable == "")
					continue
				}
				if !g.Err.SameKey(w.Err) {
					t.Errorf("%s now reports %s\n            the corpus has %s\nrun make corpus, and read the diff before committing it",
						g.Name, g.Err.Key(), w.Err.Key())
				}
				if g.Err == nil || w.Err == nil {
					continue
				}

				for name, v := range w.Err.Fields {
					if v != corpus.Redacted {
						continue
					}
					redacted++
					if g.Err.Fields[name] != corpus.Redacted {
						t.Errorf("%s: the corpus records %s as varying between runs, and this capture recorded %q — the probe no longer names it Volatile",
							g.Name, name, g.Err.Fields[name])
					}
				}
			}
		})
	}

	if redacted == 0 {
		t.Error("no corpus entry records a redacted field, so the check that redaction survived asserted nothing")
	}
}

func TestEveryCorpusCaseClassifiesAsTheCorpusSays(t *testing.T) {
	ctx := context.Background()
	for _, e := range corpus.Engines(t.TempDir()) {
		t.Run(e.Name, func(t *testing.T) {
			database, err := e.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()

			for _, p := range e.Cases {
				t.Run(p.Name, func(t *testing.T) {
					err := replay(ctx, t, e, database, p)
					if p.Unreachable != "" {
						if err != nil {
							t.Fatalf("the corpus says this engine accepts it — %s — and it did not: %v",
								p.Unreachable, err)
						}
						return
					}
					if err == nil {
						t.Fatal("the database accepted it; the corpus says it refuses it")
					}
					conflict := errors.Is(err, crud.ErrConflict)
					want := p.Kind == sqlerr.KindIntegrity
					if conflict != want {
						t.Fatalf("errors.Is(err, crud.ErrConflict) = %v, want %v for a %q case\ngot %T: %v",
							conflict, want, p.Kind, err, err)
					}

					for _, s := range []error{crud.ErrNotFound, crud.ErrMissingID, crud.ErrReadOnly, crud.ErrForbidden} {
						if errors.Is(err, s) {
							t.Fatalf("came back as %v", s)
						}
					}
				})
			}
		})
	}
}

func replay(ctx context.Context, t *testing.T, e corpus.Engine, database *sql.DB, p corpus.Probe) error {
	t.Helper()

	faults := crudsql.WithFaults(sqlfault.New(e.Name))
	switch {
	case p.Contend:
		return e.Contend(ctx, database, func(wait *sql.Conn) error {
			_, err := crudsql.Source(wait, e.Dialect, faults).Exec(ctx, e.Lock)
			return err
		})
	case p.RaceA != nil:
		return e.Race(ctx, database, p, func(c *sql.Conn, stmt string) error {
			_, err := crudsql.Source(c, e.Dialect, faults).Exec(ctx, stmt)
			return err
		})
	case p.Tx != nil:
		tx, err := crudsql.Open(database, e.Dialect, faults).Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		return corpus.Script(p.Tx,
			func(stmt string) error { _, err := tx.Exec(ctx, stmt); return err },
			func() error { return tx.Commit(ctx) })
	case p.Session != nil:
		return e.Session(ctx, p, func(c *sql.Conn) error {
			_, err := crudsql.Source(c, e.Dialect, faults).Exec(ctx, p.Stmt)
			return err
		})
	case p.Connect != "":
		bad, err := sql.Open(e.Driver, p.Connect)
		if err != nil {
			return err
		}
		defer bad.Close()
		_, err = crudsql.Source(bad, e.Dialect, faults).Exec(ctx, "SELECT 1")
		return err
	case p.Stmt != "":
		_, err := crudsql.Source(database, e.Dialect, faults).Exec(ctx, p.Stmt)
		return err
	case p.Unreachable != "":
		return nil
	default:
		t.Fatalf("%s/%s has no arm here and no Unreachable — test/corpus grew a probe shape this switch does not know, and every case of that shape would read as \"the database accepted it\"",
			e.Name, p.Name)
		return nil
	}
}

func TestADeferredConstraintArrivesFromTheCommitAndNotTheStatement(t *testing.T) {
	ctx := context.Background()
	for _, e := range corpus.Engines(t.TempDir()) {
		p, ok := corpusProbe(e, "deferred_constraint")
		if !ok {
			t.Fatalf("%s has no deferred_constraint probe", e.Name)
		}
		if p.Unreachable != "" {
			continue
		}
		t.Run(e.Name, func(t *testing.T) {
			database, err := e.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()

			for _, b := range beginners(t, e, database) {
				t.Run(b.name, func(t *testing.T) {
					tx, err := b.begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(ctx)

					if _, err := tx.Exec(ctx, p.Tx[0]); err != nil {
						t.Fatalf("the insert was refused at the statement, so the constraint is not deferred: %v", err)
					}
					err = tx.Commit(ctx)
					if err == nil {
						t.Fatal("the commit succeeded; an orphan row is in the table")
					}
					if !errors.Is(err, crud.ErrConflict) {
						t.Fatalf("the commit's refusal is not a conflict, so it reaches a client as a 500: %T: %v", err, err)
					}

					immediate, _ := corpusProbe(e, "foreign_key")
					tx2, err := b.begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx2.Rollback(ctx)
					if _, err := tx2.Exec(ctx, immediate.Stmt); err == nil {
						t.Fatal("an immediate foreign key was accepted at the statement too, so nothing here distinguishes deferred from immediate")
					}
				})
			}
		})
	}
}

func corpusProbe(e corpus.Engine, name string) (corpus.Probe, bool) {
	for _, p := range e.Cases {
		if p.Name == name {
			return p, true
		}
	}
	return corpus.Probe{}, false
}

type beginner struct {
	name  string
	begin func(context.Context) (crud.Tx, error)
}

func beginners(t *testing.T, e corpus.Engine, database *sql.DB) []beginner {
	t.Helper()
	out := []beginner{{"crudsql", crudsql.Open(database, e.Dialect).Begin}}
	if e.Name != "postgres" {
		return out
	}
	pool, err := pgxpool.New(context.Background(), corpus.PostgresDSN())
	if err != nil {
		t.Fatalf("opening a pgx pool: %v", err)
	}
	t.Cleanup(pool.Close)
	out = append(out, beginner{"crudpgx", crudpgx.Open(pool).Begin})
	return out
}

func TestEveryCorpusCaseReachesTheCallerAsTheFaultTheCorpusNames(t *testing.T) {
	ctx := context.Background()
	dir, err := corpus.Dir()
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range corpus.Engines(t.TempDir()) {
		t.Run(e.Name, func(t *testing.T) {
			want, err := sqlerr.Load(dir, e.Name)
			if err != nil {
				t.Fatalf("reading the checked-in corpus: %v", err)
			}
			database, err := e.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()

			var faults, negatives int

			for _, p := range e.Cases {
				t.Run(p.Name, func(t *testing.T) {
					entry, ok := want.Case(p.Name)
					if !ok {
						t.Fatalf("the corpus has no entry for this case — run make corpus")
					}
					got := replay(ctx, t, e, database, p)
					if p.Unreachable != "" {
						return
					}
					if got == nil {
						t.Fatal("the database accepted it; the corpus says it refuses it")
					}

					code, _, classifiable := sqlerr.Classify(e.Name, entry.Err)
					f, has := errs.AsFault(got)
					if has != classifiable {
						t.Fatalf("a fault reached the caller = %v, and the corpus entry %s classifies = %v\ngot %T: %v",
							has, entry.Err.Key(), classifiable, got, got)
					}
					if !classifiable {
						negatives++
						return
					}
					faults++
					if f.Code != code {
						t.Fatalf("the caller got %q and the corpus entry classifies as %q", f.Code, code)
					}
					if f.Detail.SQLState != entry.Err.SQLState || f.Detail.Native != int(entry.Err.Native) {
						t.Fatalf("Detail carries %q/%d, the corpus entry is %q/%d — the live extraction and the capture disagree",
							f.Detail.SQLState, f.Detail.Native, entry.Err.SQLState, entry.Err.Native)
					}
					if f.Detail.Dialect != e.Name {
						t.Fatalf("Detail.Dialect = %q, want %q", f.Detail.Dialect, e.Name)
					}
				})
			}

			if faults == 0 {
				t.Error("no case produced a fault, so every assertion above was about an absence")
			}
			if negatives == 0 {
				t.Error("no case stayed unclassified, so nothing here says the classifier knows when to refuse")
			}
		})
	}
}

func TestAMariaDBCheckIsOnlyClassifiedWhenTheSourceSaysMariaDB(t *testing.T) {
	ctx := context.Background()
	var maria corpus.Engine
	for _, e := range corpus.Engines(t.TempDir()) {
		if e.Name == "mariadb" {
			maria = e
		}
	}
	if maria.Name == "" {
		t.Fatal("no mariadb engine in the corpus")
	}
	check, ok := corpusProbe(maria, "check")
	if !ok {
		t.Fatal("mariadb has no check probe")
	}

	database, err := maria.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	_, err = crudsql.MariaDB(database).Exec(ctx, check.Stmt)
	f, has := errs.AsFault(err)
	if !has || f.Code != errs.CodeCheck {
		t.Fatalf("a failed CHECK through crudsql.MariaDB came back as %T: %v", err, err)
	}
	if f.Detail.Native != 4025 {
		t.Fatalf("Detail.Native = %d, want MariaDB's 4025 — the number this constructor exists for", f.Detail.Native)
	}

	_, err = crudsql.MySQL(database).Exec(ctx, check.Stmt)
	if !errors.Is(err, crud.ErrConflict) {
		t.Fatalf("the status was lost as well as the code: %T: %v", err, err)
	}
	if f, has := errs.AsFault(err); has {
		t.Fatalf("MySQL's table answered %q for a number only MariaDB reports", f.Code)
	}
}

func TestAUniqueIndexOnAnExpressionFillsNoColumns(t *testing.T) {
	ctx := context.Background()
	var pg corpus.Engine
	for _, e := range corpus.Engines(t.TempDir()) {
		if e.Name == "postgres" {
			pg = e
		}
	}
	database, err := pg.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	for _, stmt := range []string{
		`DROP TABLE IF EXISTS cp_expr`,
		`CREATE TABLE cp_expr (
			id     int PRIMARY KEY,
			tenant int  NOT NULL,
			email  text NOT NULL,
			slug   text NOT NULL,
			CONSTRAINT cp_expr_slug UNIQUE (tenant, slug)
		)`,
		`CREATE UNIQUE INDEX cp_expr_email ON cp_expr (tenant, lower(email))`,
		`INSERT INTO cp_expr (id, tenant, email, slug) VALUES (1, 1, 'a@b.c', 's1')`,
	} {
		if _, err := database.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", firstLineOf(stmt), err)
		}
	}
	t.Cleanup(func() { database.ExecContext(context.Background(), `DROP TABLE IF EXISTS cp_expr`) })

	cat, err := catalog.Load(ctx, crudsql.Postgres(database))
	if err != nil {
		t.Fatalf("loading the catalog: %v", err)
	}
	source := crudsql.Postgres(database, crudsql.WithFaults(
		sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))))

	con, ok := cat.Constraint("cp_expr", "cp_expr_email")
	if !ok {
		t.Fatal("the catalog did not read the expression index at all")
	}
	if len(con.Columns) != 2 || con.Columns[0] != "tenant" || con.Columns[1] != "" {
		t.Fatalf("catalog Columns = %#v, want [tenant \"\"] — this server records an expression part some other way", con.Columns)
	}

	_, err = source.Exec(ctx, `INSERT INTO cp_expr (id, tenant, email, slug) VALUES (2, 1, 'A@B.C', 's2')`)
	f, has := errs.AsFault(err)
	if !has {
		t.Fatalf("the expression-key violation produced no fault: %T: %v", err, err)
	}
	if f.Violations[0].Source.Constraint != "cp_expr_email" {
		t.Fatalf("Source.Constraint = %q, want cp_expr_email — the wrong key was violated",
			f.Violations[0].Source.Constraint)
	}
	if got := f.Violations[0].Source.Columns; got != nil {
		t.Fatalf("Source.Columns = %#v, want nil: a key part no column can name was reported as one", got)
	}
	if f.Detail.Columns != nil {
		t.Fatalf("Detail.Columns = %#v, want nil", f.Detail.Columns)
	}

	_, err = source.Exec(ctx, `INSERT INTO cp_expr (id, tenant, email, slug) VALUES (3, 1, 'z@z.z', 's1')`)
	f, has = errs.AsFault(err)
	if !has {
		t.Fatalf("the plain-key violation produced no fault: %T: %v", err, err)
	}
	if f.Violations[0].Source.Constraint != "cp_expr_slug" {
		t.Fatalf("Source.Constraint = %q, want cp_expr_slug", f.Violations[0].Source.Constraint)
	}
	if got := f.Violations[0].Source.Columns; len(got) != 2 || got[0] != "tenant" || got[1] != "slug" {
		t.Fatalf("Source.Columns = %v, want [tenant slug] in key order", got)
	}
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func TestACatalogFillsTheColumnsAUniqueViolationDoesNotName(t *testing.T) {
	ctx := context.Background()
	var pg corpus.Engine
	for _, e := range corpus.Engines(t.TempDir()) {
		if e.Name == "postgres" {
			pg = e
		}
	}
	composite, ok := corpusProbe(pg, "unique_composite")
	if !ok {
		t.Fatal("postgres has no unique_composite probe")
	}
	notNull, ok := corpusProbe(pg, "not_null")
	if !ok {
		t.Fatal("postgres has no not_null probe")
	}

	database, err := pg.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cat, err := catalog.Load(ctx, crudsql.Postgres(database))
	if err != nil {
		t.Fatalf("loading the catalog: %v", err)
	}
	withCat := crudsql.Postgres(database, crudsql.WithFaults(
		sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))))

	_, err = withCat.Exec(ctx, composite.Stmt)
	f, has := errs.AsFault(err)
	if !has {
		t.Fatalf("the composite unique violation produced no fault: %T: %v", err, err)
	}
	if got := f.Violations[0].Source.Columns; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Source.Columns = %v, want [a b] in key order", got)
	}

	_, err = crudsql.Postgres(database).Exec(ctx, composite.Stmt)
	f, has = errs.AsFault(err)
	if !has {
		t.Fatalf("no fault: %v", err)
	}
	if f.Violations[0].Source.Columns != nil {
		t.Fatalf("Source.Columns = %#v with no catalog wired", f.Violations[0].Source.Columns)
	}
	if f.Violations[0].Source.Constraint != "cp_ab" {
		t.Fatalf("Source.Constraint = %q, want the real constraint name the lookup keys on",
			f.Violations[0].Source.Constraint)
	}

	for _, tc := range []struct {
		name   string
		source crud.Source
	}{
		{"with a catalog", withCat},
		{"without one", crudsql.Postgres(database)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.source.Exec(ctx, notNull.Stmt)
			f, has := errs.AsFault(err)
			if !has {
				t.Fatalf("no fault: %v", err)
			}
			if got := f.Violations[0].Source.Columns; len(got) != 1 || got[0] != "need" {
				t.Fatalf("Source.Columns = %v, want the driver's own [need]", got)
			}
		})
	}
}

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

// The corpus is the checked-in record of what each engine says when it refuses
// a statement, and the dialect parsers are written against it rather than
// against anybody's reading of the SQLSTATE specification. Two things have to
// stay true of it, and they are different things.
//
// One: it still describes these servers. Two: what it claims about a violation
// matches what the shipped adapter does with that violation. The first would
// pass on a corpus nothing reads; the second would pass on a corpus captured
// from a server nobody runs.

// The servers have not changed under the corpus.
//
// What is compared is the tuple a classifier dispatches on — driver type,
// SQLSTATE, native number, and which structured fields the driver filled in.
// The message is deliberately not compared. docker-compose.yml tracks floating
// tags, so a patch release that rewords one sentence would otherwise turn this
// red over a change no parser can see, and the fix would be to stop reading the
// failure. The text is still captured, and phase 2 is where a parser proves it
// never reads it.
func TestTheCorpusStillDescribesTheseServers(t *testing.T) {
	ctx := context.Background()
	dir, err := corpus.Dir()
	if err != nil {
		t.Fatal(err)
	}

	// How many redacted fields were checked, across every engine. Nothing here
	// runs in parallel, so a plain counter closed over by the subtests is safe.
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
				// A field the corpus records as redacted has to come back
				// redacted. SameKey compares which fields the driver filled in
				// and never their values, so a probe that lost its Volatile
				// list leaves every assertion above green while make corpus
				// rewrites postgres.json with two fresh backend pids every run
				// — and the diff, which is the only reader of these files,
				// becomes noise. [[D-040]]
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

	// The control. The redaction loop is empty for a corpus with nothing
	// redacted, and would then pass for a capture that redacts nothing at all.
	if redacted == 0 {
		t.Error("no corpus entry records a redacted field, so the check that redaction survived asserted nothing")
	}
}

// Every corpus case reaches a caller as what the corpus says it is.
//
// This is the half with teeth. The adapter answers exactly one question today —
// is this a conflict — and the corpus's Kind is the answer it must give:
// integrity yes, everything else no. Both directions matter and the second one
// more, because a classifier that widened until it caught everything would pass
// every positive here and turn a missing table into a 409.
//
// It also settles two things that were guesses. MySQL reports a failed CHECK as
// 3819 with SQLSTATE HY000 and a missing default as 1364, neither of them class
// 23, so both were unclassified 500s until the number list was added — this is
// what holds that fix down. MariaDB answers the same CHECK with 4025 and
// SQLSTATE 23000, so class 23 already covers it and no number is needed, which
// is why [[D-015]] was right to leave 4025 out and wrong about why.
func TestEveryCorpusCaseClassifiesAsTheCorpusSays(t *testing.T) {
	ctx := context.Background()
	for _, e := range corpus.Engines(t.TempDir()) {
		t.Run(e.Name, func(t *testing.T) {
			db, err := e.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			for _, p := range e.Cases {
				t.Run(p.Name, func(t *testing.T) {
					err := replay(ctx, t, e, db, p)
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
					// A violation must not be mistaken for something else
					// either: a transport turns these into 404 and 400.
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

// replay runs one corpus case through the adapter rather than the raw handle,
// so what comes back is what a repository would have handed its caller.
//
// It is a second copy of test/corpus's own switch, and that is deliberate — the
// point is that the same choreography goes through the adapter this time. What
// is not deliberate is the two drifting apart, which is why the default arm
// fails instead of quietly running nothing.
func replay(ctx context.Context, t *testing.T, e corpus.Engine, db *sql.DB, p corpus.Probe) error {
	t.Helper()
	// The engine string is the corpus's own name, which is where MariaDB and
	// MySQL part company: crud.Dialect is crud.MySQL for both ([[D-046]]).
	faults := crudsql.WithFaults(sqlfault.New(e.Name))
	switch {
	case p.Contend:
		// The waiting statement has to run on the connection whose patience was
		// cut, so the source is built over that connection rather than the pool.
		return e.Contend(ctx, db, func(wait *sql.Conn) error {
			_, err := crudsql.Source(wait, e.Dialect, faults).Exec(ctx, e.Lock)
			return err
		})
	case p.RaceA != nil:
		return e.Race(ctx, db, p, func(c *sql.Conn, stmt string) error {
			_, err := crudsql.Source(c, e.Dialect, faults).Exec(ctx, stmt)
			return err
		})
	case p.Tx != nil:
		// Through the source's own Begin, so the savepoint seam ([[FL-009]]) is
		// the one under test and Commit is the adapter's rather than the
		// driver's — which is where a deferred constraint arrives.
		tx, err := crudsql.Open(db, e.Dialect, faults).Begin(ctx)
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
		// A repository bound to a database it cannot reach. The refusal arrives
		// on the first statement and through the same classifier as any other.
		bad, err := sql.Open(e.Driver, p.Connect)
		if err != nil {
			return err
		}
		defer bad.Close()
		_, err = crudsql.Source(bad, e.Dialect, faults).Exec(ctx, "SELECT 1")
		return err
	case p.Stmt != "":
		_, err := crudsql.Source(db, e.Dialect, faults).Exec(ctx, p.Stmt)
		return err
	case p.Unreachable != "":
		// Nothing to run: a case this engine cannot reach at all, stated as an
		// absence rather than checked as an error.
		return nil
	default:
		t.Fatalf("%s/%s has no arm here and no Unreachable — test/corpus grew a probe shape this switch does not know, and every case of that shape would read as \"the database accepted it\"",
			e.Name, p.Name)
		return nil
	}
}

// A deferred constraint fires at COMMIT, and the adapter has to classify it
// there or the same violation is a 409 through one door and a 500 through the
// other.
//
// The immediate case in the same run is the control: it fails at Exec and never
// reaches Commit. Without that half this passes for a fixture whose constraint
// was never deferrable at all, and the pairing is also what makes the gap
// visible — untouched, crudsql.Tx.Commit and crudpgx.Tx.Commit hand the driver
// error straight back and this is the only thing in the tree that goes red.
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
			db, err := e.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			for _, b := range beginners(t, e, db) {
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

					// The control: the same violation raised immediately does
					// fail at the statement. Without it this test passes for a
					// table whose foreign key was never deferrable.
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

// beginners is every adapter that can start a transaction against this engine.
// Both PostgreSQL adapters are walked, because a deferred constraint that is a
// 409 through database/sql and a 500 through pgx is exactly the divergence
// TestIntegrityViolationsAreClassifiedByEveryAdapter exists to catch.
type beginner struct {
	name  string
	begin func(context.Context) (crud.Tx, error)
}

func beginners(t *testing.T, e corpus.Engine, db *sql.DB) []beginner {
	t.Helper()
	out := []beginner{{"crudsql", crudsql.Open(db, e.Dialect).Begin}}
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

// Every corpus case reaches the caller carrying the fault the corpus names.
//
// TestEveryCorpusCaseClassifiesAsTheCorpusSays above asks the one question the
// tree could answer before phase 3 — is this a conflict — and this asks the one
// phase 3 added: which violation was it. §15's named example is MySQL's CHECK,
// and this is what makes it non-vacuous: it was already a conflict, and now it
// arrives carrying errs.CodeCheck.
//
// The expectation comes from the checked-in corpus rather than from a list
// here, so what is under test is the shipped path — extraction in the adapter,
// the parser, the vocabulary, the builder — against evidence captured from the
// server.
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
			db, err := e.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			// Nothing here runs in parallel, so plain counters closed over by
			// the subtests are safe. Both are asserted below: without the first
			// a classifier that answers false for everything leaves the loop
			// green, and without the second a filter that swallowed the
			// negatives does.
			var faults, negatives int

			for _, p := range e.Cases {
				t.Run(p.Name, func(t *testing.T) {
					entry, ok := want.Case(p.Name)
					if !ok {
						t.Fatalf("the corpus has no entry for this case — run make corpus")
					}
					got := replay(ctx, t, e, db, p)
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

// The constructor is the declaration, and this is what that buys and what it
// costs ([[D-046]]).
//
// MariaDB and MySQL share a driver, a dialect and a wire protocol, and answer a
// failed CHECK with two different numbers. Nothing in the tree tells the two
// servers apart at run time, so the caller says which — and getting it wrong
// costs the code, never the status.
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

	db, err := maria.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = crudsql.MariaDB(db).Exec(ctx, check.Stmt)
	f, has := errs.AsFault(err)
	if !has || f.Code != errs.CodeCheck {
		t.Fatalf("a failed CHECK through crudsql.MariaDB came back as %T: %v", err, err)
	}
	if f.Detail.Native != 4025 {
		t.Fatalf("Detail.Native = %d, want MariaDB's 4025 — the number this constructor exists for", f.Detail.Native)
	}

	// The control, and without it this test says nothing about why the
	// constructor exists: through crudsql.MySQL the same violation on the same
	// server is still a 409 — class 23 covers it — and carries no code, because
	// 4025 is not in MySQL's table.
	_, err = crudsql.MySQL(db).Exec(ctx, check.Stmt)
	if !errors.Is(err, crud.ErrConflict) {
		t.Fatalf("the status was lost as well as the code: %T: %v", err, err)
	}
	if f, has := errs.AsFault(err); has {
		t.Fatalf("MySQL's table answered %q for a number only MariaDB reports", f.Code)
	}
}

// A PostgreSQL unique violation names the constraint and the table and no
// column. The columns a client would want come from the schema, and this is the
// first thing in the tree to read a loaded catalog for something other than the
// catalog's own tests.
// A key part that is an expression has no column name, and the catalog records
// it as "" with the engine's text in Expressions. "" is not a column: filled in,
// it names a column no schema has, and a mixed key with the empty part dropped
// would describe (tenant, lower(email)) as the key (tenant) — a key the engine
// does not enforce. Both would reach a resolver as a violation on a field
// nothing collided on, so the honest answer is the same one a miss gets.
func TestAUniqueIndexOnAnExpressionFillsNoColumns(t *testing.T) {
	ctx := context.Background()
	var pg corpus.Engine
	for _, e := range corpus.Engines(t.TempDir()) {
		if e.Name == "postgres" {
			pg = e
		}
	}
	db, err := pg.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// One table carrying both kinds of key, so the control below is the same
	// lookup on the same catalog and differs only in what the key is made of.
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
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", firstLineOf(stmt), err)
		}
	}
	t.Cleanup(func() { db.ExecContext(context.Background(), `DROP TABLE IF EXISTS cp_expr`) })

	cat, err := catalog.Load(ctx, crudsql.Postgres(db))
	if err != nil {
		t.Fatalf("loading the catalog: %v", err)
	}
	src := crudsql.Postgres(db, crudsql.WithFaults(
		sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))))

	// The control on the fixture: the catalog has to have recorded the expression
	// part as an empty name, or the assertion below is about a key that does not
	// exist.
	con, ok := cat.Constraint("cp_expr", "cp_expr_email")
	if !ok {
		t.Fatal("the catalog did not read the expression index at all")
	}
	if len(con.Columns) != 2 || con.Columns[0] != "tenant" || con.Columns[1] != "" {
		t.Fatalf("catalog Columns = %#v, want [tenant \"\"] — this server records an expression part some other way", con.Columns)
	}

	// The expression key. Same tenant, same lower(email), a different slug, so
	// only the expression index is violated.
	_, err = src.Exec(ctx, `INSERT INTO cp_expr (id, tenant, email, slug) VALUES (2, 1, 'A@B.C', 's2')`)
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

	// The control: the plain key on the same table, through the same catalog,
	// still fills. Without it a classifier that stopped filling anything at all
	// would pass everything above.
	_, err = src.Exec(ctx, `INSERT INTO cp_expr (id, tenant, email, slug) VALUES (3, 1, 'z@z.z', 's1')`)
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

	db, err := pg.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cat, err := catalog.Load(ctx, crudsql.Postgres(db))
	if err != nil {
		t.Fatalf("loading the catalog: %v", err)
	}
	withCat := crudsql.Postgres(db, crudsql.WithFaults(
		sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))))

	_, err = withCat.Exec(ctx, composite.Stmt)
	f, has := errs.AsFault(err)
	if !has {
		t.Fatalf("the composite unique violation produced no fault: %T: %v", err, err)
	}
	if got := f.Violations[0].Source.Columns; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Source.Columns = %v, want [a b] in key order", got)
	}

	// The first control: the identical live error with no catalog leaves the
	// list nil — nil, not empty — while the constraint name is the real one, so
	// the fill demonstrably came from the catalog and not from the driver.
	_, err = crudsql.Postgres(db).Exec(ctx, composite.Stmt)
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

	// The second control: a violation the driver *does* name a column for
	// answers the same either way. Without it the positive passes for a
	// classifier that consults the catalog on everything and overwrites what
	// the engine saw.
	for _, tc := range []struct {
		name string
		src  crud.Source
	}{
		{"with a catalog", withCat},
		{"without one", crudsql.Postgres(db)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.src.Exec(ctx, notNull.Stmt)
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

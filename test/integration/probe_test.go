//go:build integration

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/adapter/crudpgx"
	"github.com/shardit-io/vv/crud/adapter/crudsql"
	"github.com/shardit-io/vv/crud/catalog"
	"github.com/shardit-io/vv/crud/decorators/faults"
	"github.com/shardit-io/vv/crud/http/crudhttp"
	"github.com/shardit-io/vv/crud/probe"
	"github.com/shardit-io/vv/crud/sqlfault"
	"github.com/shardit-io/vv/crud/sqlrepo"
	"github.com/shardit-io/vv/errs"
)

// What the probe finds can only be checked against a server: the whole design
// turns on which violations an engine raises and which it swallows, and the four
// disagree about most of it.
//
// Never t.Parallel() here: every test shares the same physical tables.

type pbTarget struct {
	name    string
	db      string // which pbSchema built it
	dialect string
	src     crud.Source
	cat     catalog.Catalog
}

var (
	pbOnce sync.Once
	pbErr  error
)

// pbEngines builds the fixture once per process and hands back one target per
// engine, each carrying its own catalog and a classifier that reads it.
func pbEngines(t *testing.T) []pbTarget {
	t.Helper()
	ctx := context.Background()

	// The failure is recorded rather than reported from inside the Once: a
	// t.Fatalf there exits through runtime.Goexit, the Once still marks itself
	// done, and every later test reports a missing table instead of the DDL
	// error that caused it.
	pbOnce.Do(func() {
		for _, s := range []struct {
			db  string
			src crud.Source
		}{
			{"postgres", crudsql.Postgres(pgDB)},
			{"mysql", crudsql.MySQL(myDB)},
			{"mysql", crudsql.MySQL(mariaDB)},
		} {
			for _, stmt := range pbSchema[s.db] {
				if _, err := s.src.Exec(ctx, stmt); err != nil {
					pbErr = fmt.Errorf("%s: %s: %w", s.db, catFirstLine(stmt), err)
					return
				}
			}
		}
	})
	if pbErr != nil {
		t.Fatalf("the pb_ tables were never built: %v", pbErr)
	}

	out := []pbTarget{
		{name: "postgres", db: "postgres", dialect: "postgres"},
		{name: "pgx", db: "postgres", dialect: "postgres"},
		{name: "mysql", db: "mysql", dialect: "mysql"},
		{name: "mariadb", db: "mysql", dialect: "mariadb"},
		{name: "sqlite", db: "sqlite", dialect: "sqlite"},
	}
	sqliteDB := pbOpenSQLite(t)
	for i := range out {
		tg := &out[i]
		var plain crud.Source
		switch tg.name {
		case "postgres":
			plain = crudsql.Postgres(pgDB)
		case "pgx":
			plain = crudpgx.Open(pgPool)
		case "mysql":
			plain = crudsql.MySQL(myDB)
		case "mariadb":
			plain = crudsql.MariaDB(mariaDB)
		case "sqlite":
			plain = crudsql.SQLite(sqliteDB)
		}
		cat, err := catalog.Load(ctx, plain)
		if err != nil {
			t.Fatalf("%s: loading the catalog: %v", tg.name, err)
		}
		tg.cat = cat
		// The classifier reads the catalog, which is how a consumer wires it and
		// what gives a PostgreSQL composite-unique violation its columns.
		cls := sqlfault.New(tg.dialect, sqlfault.WithColumns(sqlfault.FromCatalog(cat)))
		switch tg.name {
		case "postgres":
			tg.src = crudsql.Postgres(pgDB, crudsql.WithFaults(cls))
		case "pgx":
			tg.src = crudpgx.Open(pgPool, crudpgx.WithFaults(cls))
		case "mysql":
			tg.src = crudsql.MySQL(myDB, crudsql.WithFaults(cls))
		case "mariadb":
			tg.src = crudsql.MariaDB(mariaDB, crudsql.WithFaults(cls))
		case "sqlite":
			tg.src = crudsql.SQLite(sqliteDB, crudsql.WithFaults(cls))
		}
	}
	return out
}

// pbOpenSQLite builds a fresh file-backed database holding only this fixture.
// Foreign keys are switched on in the DSN: SQLite has them off by default, and
// without that line the foreign-key half of this suite would insert cleanly and
// record that SQLite has no foreign keys.
func pbOpenSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "probe.db")+
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(200)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, stmt := range pbSchema["sqlite"] {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("sqlite: %s: %v", catFirstLine(stmt), err)
		}
	}
	return db
}

// pbSeed empties the fixture and refills it with the three rows every test
// starts from.
//
// Row 3 is the bait: it holds the label the negative twin will write and it is
// archived, so on the two engines whose hard key is a partial index it is not in
// that index at all. A probe that replayed the index as plain equality reports a
// violation the server would never have raised.
func pbSeed(t *testing.T, tg pbTarget) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DELETE FROM pb_note`,
		`DELETE FROM pb_doc`,
		`DELETE FROM pb_org`,
		`DELETE FROM pb_region`,
		`INSERT INTO pb_org (id) VALUES (1)`,
		`INSERT INTO pb_region (id, zone) VALUES (1, 'eu')`,
		`INSERT INTO pb_doc (tenant_id, email, code, slug, label, alt, archived)
			VALUES (1, 'one@x.io', 'CODE1', 'S1', NULL, 'ALT1', 0)`,
		`INSERT INTO pb_doc (tenant_id, email, code, slug, label, alt, archived)
			VALUES (1, 'two@x.io', 'CODE2', 'S2', NULL, 'ALT2', 0)`,
		`INSERT INTO pb_doc (tenant_id, email, code, slug, label, alt, archived)
			VALUES (1, 'bait@x.io', 'CODE3', NULL, 'BAIT-LABEL', 'ALT3', 1)`,
		`INSERT INTO pb_note (id, doc_code) VALUES (1, 'CODE2')`,
	} {
		if _, err := tg.src.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: seeding: %s: %v", tg.name, catFirstLine(stmt), err)
		}
	}
}

// pbIDOf reads back the generated key of one seeded row, because the engines
// disagree about what it will be.
func pbIDOf(t *testing.T, tg pbTarget, email string) int64 {
	t.Helper()
	rows, err := tg.src.Query(context.Background(),
		"SELECT id FROM pb_doc WHERE email = "+pbLiteral(tg, email))
	if err != nil {
		t.Fatalf("%s: reading back an id: %v", tg.name, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("%s: no row with email %s", tg.name, email)
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		t.Fatalf("%s: scanning an id: %v", tg.name, err)
	}
	return id
}

// pbLiteral quotes a string for the fixture's own statements. It is only ever
// handed values this file wrote.
func pbLiteral(_ pbTarget, s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// pbRepo binds the model with the probe wired the way a consumer would, and
// fails the test on a probe error.
//
// The error is advisory and never reaches a client, so without this a probe that
// silently refused every statement would leave most of this file green: the
// driver's own violation would still be there and the assertions on it would
// still hold.
func pbRepo(t *testing.T, tg pbTarget, opts ...probe.Option) crud.Repo[PbDoc, int64, PbDocUpdate] {
	t.Helper()
	return PbDocs.Bind(tg.src, faults.Enrich[PbDoc, int64](
		faults.WithProbe(probe.Full(tg.cat, opts...)),
		faults.WithProbeError(func(op string, err error) {
			t.Errorf("%s: the %s probe failed: %v", tg.name, op, err)
		})))
}

// pbRepoQuiet is pbRepo without the assertion that the probe never fails. Only
// the test that deliberately breaks the probe uses it.
func pbRepoQuiet(tg pbTarget, opts ...probe.Option) crud.Repo[PbDoc, int64, PbDocUpdate] {
	return PbDocs.Bind(tg.src, faults.Enrich[PbDoc, int64](
		faults.WithProbe(probe.Full(tg.cat, opts...))))
}

// pbPlain binds it with no probe at all — the "before" half of the positive
// control.
func pbPlain(tg pbTarget) crud.Repo[PbDoc, int64, PbDocUpdate] {
	return PbDocs.Bind(tg.src, faults.Enrich[PbDoc, int64]())
}

// pbPairs renders a fault as the set of (code, path) pairs a client sees.
func pbPairs(t *testing.T, err error) []string {
	t.Helper()
	f, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("the failure was not classified at all: %T: %v", err, err)
	}
	out := make([]string, 0, len(f.Violations))
	for _, v := range f.Violations {
		out = append(out, string(v.Code)+"@"+v.Path.String())
	}
	sort.Strings(out)
	return out
}

func pbFault(t *testing.T, err error) *errs.Fault {
	t.Helper()
	f, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("the failure was not classified at all: %T: %v", err, err)
	}
	return f
}

func pbSet(list []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range list {
		out[s] = true
	}
	return out
}

// ---------------------------------------------------------------------------
// the two named control cases

// One failed write, every violation it caused.
//
// Counting to three would pass for one violation repeated, so the assertion is
// on the set of (code, path) pairs and on its size.
func TestOneFailedWriteBecomesEveryViolationItCaused(t *testing.T) {
	want := []string{"foreign_key@OrgID", "restrict@Code", "unique@Email"}
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")
			patch := PbDocUpdate{
				Email: ptr("one@x.io"),          // doc 1 holds it
				Code:  ptr("CODE9"),             // a note still points at CODE2
				OrgID: crud.Set(int64(9999999)), // no such organisation
			}

			// Probe off: the database reports one violation per failed statement
			// and that is all anybody ever saw.
			_, err := pbPlain(tg).Update(context.Background(), id, patch)
			if got := pbPairs(t, err); len(got) != 1 {
				t.Fatalf("with the probe off the answer carried %d violations: %v", len(got), got)
			}

			// Probe on: three distinct codes at three distinct paths.
			_, err = pbRepo(t, tg).Update(context.Background(), id, patch)
			got := pbPairs(t, err)
			set := pbSet(got)
			for _, w := range want {
				if !set[w] {
					t.Errorf("the answer is missing %s: %v", w, got)
				}
			}
			if len(set) != 3 {
				t.Fatalf("the answer carries %d distinct (code, path) pairs, want 3: %v", len(set), got)
			}
			if len(got) != 3 {
				t.Fatalf("the answer carries %d violations for 3 distinct pairs, so one is listed twice: %v",
					len(got), got)
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

// The negative twin, and the one that catches real bugs.
//
// A payload whose only fault is the taken email, carrying beside it every shape
// a probe invents violations out of: a NULL nullable foreign key, a composite
// foreign key with one NULL column, a NULL half of a composite unique key, and
// the unreproducible unique key of the fixture twin.
func TestAPayloadWithOneRealViolationYieldsExactlyOne(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)

			doc := PbDoc{
				TenantID: 1,
				Email:    "one@x.io", // the one real violation
				Code:     "CODE-NEW",
				Alt:      crud.Set("ALT-NEW"),
				Archived: 1,
				// Left NULL: a nullable foreign key that is NULL satisfies its
				// constraint. A bare NOT EXISTS over NULL is true.
				OrgID: crud.Null[int64](),
				// One half of a composite foreign key pointing nowhere, the other
				// NULL. Any NULL column disables the whole constraint.
				RegionID: crud.Set(int64(4242)),
				Zone:     crud.Null[string](),
				// The NULL half of a composite unique key. Row 3 is NULL there
				// too, and under NULLS DISTINCT they do not collide.
				Slug: crud.Null[string](),
			}
			if bait, ok := pbBaitLabel[tg.dialect]; ok {
				// The archived row holds this label and is not in the partial
				// index, so nothing is violated — unless the index is replayed as
				// plain equality.
				doc.Label = crud.Set(bait)
			}

			err := pbRepo(t, tg).Save(context.Background(), &doc)
			got := pbPairs(t, err)
			if len(got) != 1 || got[0] != "unique@Email" {
				t.Fatalf("a payload with one real violation produced %v", got)
			}
			f := pbFault(t, err)
			for _, v := range f.Violations {
				if v.Source.Constraint == "pb_doc_ux_hard" {
					t.Fatalf("the unreproducible key was claimed: %+v", v)
				}
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

// The control for the twin above: the same payload with the foreign key filled
// in with a value that does not exist yields exactly two. Without it, a probe
// that closed the NULL hole by dropping foreign keys altogether passes.
func TestTheSamePayloadWithARealMissingParentYieldsTwo(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)

			doc := PbDoc{
				TenantID: 1,
				Email:    "one@x.io",
				Code:     "CODE-NEW",
				Alt:      crud.Set("ALT-NEW"),
				Archived: 1,
				OrgID:    crud.Set(int64(9999999)), // the only change from the twin
				RegionID: crud.Set(int64(4242)),
				Zone:     crud.Null[string](),
				Slug:     crud.Null[string](),
			}
			if bait, ok := pbBaitLabel[tg.dialect]; ok {
				doc.Label = crud.Set(bait)
			}

			err := pbRepo(t, tg).Save(context.Background(), &doc)
			got := pbPairs(t, err)
			set := pbSet(got)
			if !set["unique@Email"] || !set["foreign_key@OrgID"] {
				t.Fatalf("a real missing parent beside a taken email produced %v", got)
			}
			if len(got) != 2 {
				t.Fatalf("the answer carries %d violations, want exactly two: %v", len(got), got)
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

// ---------------------------------------------------------------------------

// A row does not collide with itself, and a column written with the value it
// already holds breaks nothing.
//
// The payload carries the taken email and the row's *own* code. The change set
// the probe reads is every column the DTO defined, not the diff ([[D-010]] drops
// the unchanged half of a composite key from the diff, so it cannot be what
// binds), so the code is there with the value the row already has. Two rules
// keep it from becoming a violation: a unique term excludes the row the write is
// aiming at, and a restrict term fires only when the value actually changes.
func TestAnUpdateDoesNotReportARowCollidingWithItself(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")

			_, err := pbRepo(t, tg).Update(context.Background(), id, PbDocUpdate{
				Email: ptr("one@x.io"), // the one real violation
				Code:  ptr("CODE2"),    // the value this row already holds
			})
			got := pbPairs(t, err)
			if len(got) != 1 || got[0] != "unique@Email" {
				t.Fatalf("an update that rewrote its own code produced %v", got)
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

// The unreproducible key is never claimed and its plain twin is. Without the
// second half, a probe that skipped every unique index would pass the first.
func TestTheUnreproducibleKeyIsNeverProbedAndItsPlainTwinIs(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)

			doc := PbDoc{
				TenantID: 1,
				Email:    "one@x.io",
				Code:     "CODE-NEW",
				Alt:      crud.Set("ALT1"), // row 1 holds it: the plain twin
				Archived: 1,
				Slug:     crud.Null[string](),
			}
			if bait, ok := pbBaitLabel[tg.dialect]; ok {
				doc.Label = crud.Set(bait)
			}

			err := pbRepo(t, tg).Save(context.Background(), &doc)
			named := map[string]bool{}
			for _, v := range pbFault(t, err).Violations {
				named[v.Source.Constraint] = true
			}
			if !named["pb_doc_ux_easy"] {
				t.Errorf("the reproducible twin over the same shape was not probed: %v", pbPairs(t, err))
			}
			if named["pb_doc_ux_hard"] {
				t.Errorf("the key this catalog cannot replay from a value was claimed anyway")
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

func TestPastTheCapTheAnswerSaysItIsIncomplete(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")
			patch := PbDocUpdate{
				Email: ptr("one@x.io"),
				Code:  ptr("CODE9"),
				OrgID: crud.Set(int64(9999999)),
			}

			_, err := pbRepo(t, tg, probe.WithMaxConstraints(1)).Update(context.Background(), id, patch)
			if !pbFault(t, err).Partial {
				t.Fatal("a capped probe presented an incomplete answer as complete")
			}
			// The control: the same write with the cap raised is not partial.
			_, err = pbRepo(t, tg, probe.WithMaxConstraints(probe.DefaultMaxConstraints)).
				Update(context.Background(), id, patch)
			if pbFault(t, err).Partial {
				t.Fatal("a probe that fitted inside its cap said it was incomplete")
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

// A probe that errors keeps the driver's violation. The catalog names a table
// the database no longer has, which is the shape a rolling migration produces.
func TestAProbeThatErrorsKeepsTheConflict(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")
			patch := PbDocUpdate{Email: ptr("one@x.io"), Code: ptr("CODE9")}

			// The control first, while the schema is whole: the same write
			// answers a conflict and is not partial.
			_, err := pbRepo(t, tg).Update(context.Background(), id, patch)
			good := pbFault(t, err)
			if crudhttp.Status(err) != 409 {
				t.Fatalf("a healthy probe answered %d", crudhttp.Status(err))
			}
			if good.Partial {
				t.Fatal("a healthy probe said its answer was incomplete")
			}

			ctx := context.Background()
			if _, err := tg.src.Exec(ctx, `DROP TABLE pb_note`); err != nil {
				t.Fatalf("%s: dropping the child table: %v", tg.name, err)
			}
			t.Cleanup(func() { pbRestoreNote(t, tg) })

			_, err = pbRepoQuiet(tg).Update(ctx, id, patch)
			if got := crudhttp.Status(err); got != 409 {
				t.Fatalf("a failed probe turned a %d into a %d", 409, got)
			}
			f := pbFault(t, err)
			if len(f.Violations) == 0 {
				t.Fatal("a failed probe lost the driver's own violation")
			}
			if !f.Partial {
				t.Fatal("a failed probe presented its answer as complete")
			}
			if !errors.Is(err, crud.ErrConflict) {
				t.Fatalf("a failed probe lost the classification: %v", err)
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

// pbRestoreNote puts the child table back after the probe-failure test dropped
// it, so the suite stays green run to run.
func pbRestoreNote(t *testing.T, tg pbTarget) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range pbSchema[tg.db] {
		if strings.Contains(stmt, "CREATE TABLE pb_note") {
			if _, err := tg.src.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: restoring pb_note: %v", tg.name, err)
			}
			return
		}
	}
	t.Fatalf("%s: the fixture has no statement creating pb_note", tg.name)
}

// The value never reaches the body by default, and does when the mode is on.
func TestTheOffendingValueReachesTheBodyOnlyWhenAsked(t *testing.T) {
	msgs := errs.NewMessages(errs.StandardCodes())
	if err := msgs.Add("", "unique", "the value {value} is already taken"); err != nil {
		t.Fatal(err)
	}
	render := crudhttp.NewRenderer(crudhttp.WithMessages(msgs))

	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)

			body := func(opts ...probe.Option) string {
				doc := PbDoc{TenantID: 1, Email: "one@x.io", Code: "CODE-NEW",
					Alt: crud.Set("ALT-NEW"), Slug: crud.Null[string]()}
				err := pbRepo(t, tg, opts...).Save(context.Background(), &doc)
				_, _, out := render.Render(context.Background(), err)
				b, jerr := json.Marshal(out)
				if jerr != nil {
					t.Fatal(jerr)
				}
				return string(b)
			}

			if got := body(); strings.Contains(got, "one@x.io") {
				t.Fatalf("the offending value reached the body by default: %s", got)
			}
			if got := body(probe.WithValues()); !strings.Contains(got, "one@x.io") {
				t.Fatalf("the value-echo mode was on and the value is not in the body: %s", got)
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

// The transaction matrix, live. Four arms with a counter per arm, so a case
// that stops running cannot leave the loop green.
func TestTheTransactionMatrix(t *testing.T) {
	patch := func() PbDocUpdate {
		return PbDocUpdate{
			Email: ptr("one@x.io"),
			Code:  ptr("CODE9"),
			OrgID: crud.Set(int64(9999999)),
		}
	}
	arms := 0
	for _, tg := range pbEngines(t) {
		// The engine that poisons its transaction is the one with a choice to
		// make; the other two are the control that the degrade is about the
		// engine and not about being in a transaction at all.
		_, statementScoped := tg.src.Dialect().(crud.StatementRollback)

		t.Run(tg.name+"/outside a transaction", func(t *testing.T) {
			arms++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")
			_, err := pbRepo(t, tg).Update(context.Background(), id, patch())
			if got := pbPairs(t, err); len(got) != 3 {
				t.Fatalf("outside a transaction the answer carried %v", got)
			}
		})

		t.Run(tg.name+"/inside our own transaction, no savepoints", func(t *testing.T) {
			arms++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")
			var got []string
			_ = crud.InTx(context.Background(), tg.src, func(ctx context.Context) error {
				_, err := pbRepo(t, tg).Update(ctx, id, patch())
				got = pbPairs(t, err)
				return err
			})
			want := 1
			if statementScoped {
				want = 3
			}
			if len(got) != want {
				t.Fatalf("inside a transaction the answer carried %d violations, want %d: %v",
					len(got), want, got)
			}
		})

		t.Run(tg.name+"/inside our own transaction, with savepoints", func(t *testing.T) {
			arms++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")
			var got []string
			_ = crud.InTx(context.Background(), tg.src, func(ctx context.Context) error {
				_, err := pbRepo(t, tg, probe.WithSavepoints()).Update(ctx, id, patch())
				got = pbPairs(t, err)
				return err
			})
			if len(got) != 3 {
				t.Fatalf("with savepoints the answer still degraded: %v", got)
			}
		})

		t.Run(tg.name+"/inside a foreign transaction", func(t *testing.T) {
			arms++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")
			ctx, done := pbForeignTx(t, tg)
			defer done()
			// WithSavepoints is on and must change nothing: vv does not own
			// this transaction and will not take savepoints inside it.
			_, err := pbRepo(t, tg, probe.WithSavepoints()).Update(ctx, id, patch())
			got := pbPairs(t, err)
			want := 1
			if statementScoped {
				want = 3
			}
			if len(got) != want {
				t.Fatalf("inside a foreign transaction the answer carried %d violations, want %d: %v",
					len(got), want, got)
			}
		})
	}
	if arms != 20 {
		t.Fatalf("walked %d arms of 20: a case was skipped rather than asserted", arms)
	}
}

// pbForeignTx opens a transaction the way an ent or gorm application would and
// pushes it in, so vv joins one it did not open.
func pbForeignTx(t *testing.T, tg pbTarget) (context.Context, func()) {
	t.Helper()
	ctx := context.Background()
	cls := sqlfault.New(tg.dialect, sqlfault.WithColumns(sqlfault.FromCatalog(tg.cat)))

	if tg.name == "pgx" {
		tx, err := pgPool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return crud.WithExecutor(ctx, crudpgx.From(tx, crudpgx.WithFaults(cls))),
			func() { _ = tx.Rollback(context.Background()) }
	}
	var db *sql.DB
	switch tg.name {
	case "postgres":
		db = pgDB
	case "mysql":
		db = myDB
	case "mariadb":
		db = mariaDB
	case "sqlite":
		db = pbSQLiteHandle(t, tg)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return crud.WithExecutor(ctx, crudsql.From(tx, crudsql.WithFaults(cls))),
		func() { _ = tx.Rollback() }
}

// pbSQLiteHandle digs the handle back out of the source, because the SQLite
// database is built per test and nothing else holds it.
func pbSQLiteHandle(t *testing.T, tg pbTarget) *sql.DB {
	t.Helper()
	id, ok := tg.src.(crud.Identified)
	if !ok {
		t.Fatal("the SQLite source cannot name its handle")
	}
	db, ok := id.DataSource().(*sql.DB)
	if !ok {
		t.Fatalf("the SQLite source named a %T", id.DataSource())
	}
	return db
}

// A bulk write attributes each violation to its row, and an intra-payload
// duplicate marks both rows.
func TestABulkWriteAttributesEachViolationToItsRow(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)
			repo := PbDocs.Bind(tg.src, faults.Enrich[PbDoc, int64](
				faults.WithProbeFor("SaveAll", probe.Full(tg.cat)),
				faults.WithProbeError(func(op string, err error) {
					t.Errorf("%s: the %s probe failed: %v", tg.name, op, err)
				})))

			rows := []*PbDoc{
				{TenantID: 1, Email: "n0@x.io", Code: "N0", Alt: crud.Set("A0"), Slug: crud.Null[string]()},
				{TenantID: 1, Email: "one@x.io", Code: "N1", Alt: crud.Set("A1"), Slug: crud.Null[string]()},
				{TenantID: 1, Email: "n2@x.io", Code: "N2", Alt: crud.Set("A2"), Slug: crud.Null[string]()},
			}
			err := repo.SaveAll(context.Background(), rows)
			got := pbSet(pbPairs(t, err))
			if !got["unique@[1].Email"] {
				t.Fatalf("the violation was not attributed to its row: %v", pbPairs(t, err))
			}
			for _, wrong := range []string{"unique@[0].Email", "unique@[2].Email"} {
				if got[wrong] {
					t.Fatalf("a row that was fine was blamed as well: %v", pbPairs(t, err))
				}
			}

			// The intra-payload duplicate: two rows of one insert with the same
			// address. The database reports one; both are wrong.
			pbSeed(t, tg)
			dup := []*PbDoc{
				{TenantID: 1, Email: "same@x.io", Code: "D0", Alt: crud.Set("B0"), Slug: crud.Null[string]()},
				{TenantID: 1, Email: "same@x.io", Code: "D1", Alt: crud.Set("B1"), Slug: crud.Null[string]()},
			}
			err = repo.SaveAll(context.Background(), dup)
			got = pbSet(pbPairs(t, err))
			if !got["unique@[0].Email"] || !got["unique@[1].Email"] {
				t.Fatalf("only one half of an intra-payload duplicate was reported: %v", pbPairs(t, err))
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

// A declaration against a catalog that does not know the table refuses to start.
//
// This is [[D-041]]'s owed half: a catalog that read zero rows and one that read
// every table but not this one are the same value, and the first declaration
// that names a table is what catches either.
func TestADeclarationAgainstACatalogWithoutTheTableRefusesToStart(t *testing.T) {
	Unknown := sqlrepo.Define[PbDoc, int64, PbDocUpdate]("pb_doc_that_is_not_there")
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			func() {
				defer func() {
					if r := recover(); r == nil {
						t.Fatal("a probe over a table the catalog does not know bound quietly")
					}
				}()
				Unknown.Bind(tg.src, faults.Enrich[PbDoc, int64](
					faults.WithProbe(probe.Full(tg.cat))))
			}()
			// The control: the table the catalog does know binds.
			PbDocs.Bind(tg.src, faults.Enrich[PbDoc, int64](
				faults.WithProbe(probe.Full(tg.cat))))
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

// The same failing request twice produces the same body, byte for byte.
func TestTheSameFailingRequestTwiceProducesTheSameBody(t *testing.T) {
	render := crudhttp.NewRenderer()
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")
			patch := PbDocUpdate{
				Email: ptr("one@x.io"),
				Code:  ptr("CODE9"),
				OrgID: crud.Set(int64(9999999)),
			}
			body := func() string {
				_, err := pbRepo(t, tg).Update(context.Background(), id, patch)
				_, _, out := render.Render(context.Background(), err)
				b, jerr := json.Marshal(out)
				if jerr != nil {
					t.Fatal(jerr)
				}
				return string(b)
			}
			first := body()
			for i := 0; i < 5; i++ {
				if again := body(); again != first {
					t.Fatalf("run %d differed:\n first %s\n then  %s", i, first, again)
				}
			}
			// The control on the comparison: three violations, so byte equality
			// is measuring an order rather than a single entry.
			if n := strings.Count(first, `"error_code"`); n != 3 {
				t.Fatalf("the body carries %d violations, so the comparison above measures nothing: %s", n, first)
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

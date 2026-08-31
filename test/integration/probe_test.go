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

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/probe"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/errs"
)

type pbTarget struct {
	name     string
	database string
	dialect  string
	source   crud.Source
	cat      catalog.Catalog
}

var (
	pbOnce sync.Once
	pbErr  error
)

func pbEngines(t *testing.T) []pbTarget {
	t.Helper()
	ctx := context.Background()

	pbOnce.Do(func() {
		for _, s := range []struct {
			database string
			source   crud.Source
		}{
			{"postgres", crudsql.Postgres(pgDB)},
			{"mysql", crudsql.MySQL(myDB)},
			{"mysql", crudsql.MySQL(mariaDB)},
		} {
			for _, stmt := range pbSchema[s.database] {
				if _, err := s.source.Exec(ctx, stmt); err != nil {
					pbErr = fmt.Errorf("%s: %s: %w", s.database, catFirstLine(stmt), err)
					return
				}
			}
		}
	})
	if pbErr != nil {
		t.Fatalf("the pb_ tables were never built: %v", pbErr)
	}

	out := []pbTarget{
		{name: "postgres", database: "postgres", dialect: "postgres"},
		{name: "pgx", database: "postgres", dialect: "postgres"},
		{name: "mysql", database: "mysql", dialect: "mysql"},
		{name: "mariadb", database: "mysql", dialect: "mariadb"},
		{name: "sqlite", database: "sqlite", dialect: "sqlite"},
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

		cls := sqlfault.New(tg.dialect, sqlfault.WithColumns(sqlfault.FromCatalog(cat)))
		switch tg.name {
		case "postgres":
			tg.source = crudsql.Postgres(pgDB, crudsql.WithFaults(cls))
		case "pgx":
			tg.source = crudpgx.Open(pgPool, crudpgx.WithFaults(cls))
		case "mysql":
			tg.source = crudsql.MySQL(myDB, crudsql.WithFaults(cls))
		case "mariadb":
			tg.source = crudsql.MariaDB(mariaDB, crudsql.WithFaults(cls))
		case "sqlite":
			tg.source = crudsql.SQLite(sqliteDB, crudsql.WithFaults(cls))
		}
	}
	return out
}

func pbOpenSQLite(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "probe.db")+
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(200)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	for _, stmt := range pbSchema["sqlite"] {
		if _, err := database.ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("sqlite: %s: %v", catFirstLine(stmt), err)
		}
	}
	return database
}

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
		if _, err := tg.source.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: seeding: %s: %v", tg.name, catFirstLine(stmt), err)
		}
	}
}

func pbIDOf(t *testing.T, tg pbTarget, email string) int64 {
	t.Helper()
	rows, err := tg.source.Query(context.Background(),
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

func pbLiteral(_ pbTarget, s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func pbRepo(t *testing.T, tg pbTarget, options ...probe.Option) *crud.Repo[PbDoc, int64, PbDocUpdate] {
	t.Helper()
	return PbDocs.Bind(tg.source, faults.Enrich[PbDoc, int64](
		faults.WithProbe(probe.Full(tg.cat, options...)),
		faults.WithProbeError(func(op string, err error) {
			t.Errorf("%s: the %s probe failed: %v", tg.name, op, err)
		})))
}

func pbRepoQuiet(tg pbTarget, options ...probe.Option) *crud.Repo[PbDoc, int64, PbDocUpdate] {
	return PbDocs.Bind(tg.source, faults.Enrich[PbDoc, int64](
		faults.WithProbe(probe.Full(tg.cat, options...))))
}

func pbPlain(tg pbTarget) *crud.Repo[PbDoc, int64, PbDocUpdate] {
	return PbDocs.Bind(tg.source, faults.Enrich[PbDoc, int64]())
}

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

func TestOneFailedWriteBecomesEveryViolationItCaused(t *testing.T) {
	want := []string{"foreign_key@OrgID", "restrict@Code", "unique@Email"}
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

			_, err := pbPlain(tg).Update(context.Background(), id, patch)
			if got := pbPairs(t, err); len(got) != 1 {
				t.Fatalf("with the probe off the answer carried %d violations: %v", len(got), got)
			}

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

func TestAPayloadWithOneRealViolationYieldsExactlyOne(t *testing.T) {
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

				OrgID: crud.Null[int64](),

				RegionID: crud.Set(int64(4242)),
				Zone:     crud.Null[string](),

				Slug: crud.Null[string](),
			}
			if bait, ok := pbBaitLabel[tg.dialect]; ok {
				doc.Label = crud.Set(bait)
			}

			_, err := pbRepo(t, tg).Save(context.Background(), &doc)
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
				OrgID:    crud.Set(int64(9999999)),
				RegionID: crud.Set(int64(4242)),
				Zone:     crud.Null[string](),
				Slug:     crud.Null[string](),
			}
			if bait, ok := pbBaitLabel[tg.dialect]; ok {
				doc.Label = crud.Set(bait)
			}

			_, err := pbRepo(t, tg).Save(context.Background(), &doc)
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

func TestAnUpdateDoesNotReportARowCollidingWithItself(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")

			_, err := pbRepo(t, tg).Update(context.Background(), id, PbDocUpdate{
				Email: ptr("one@x.io"),
				Code:  ptr("CODE2"),
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
				Alt:      crud.Set("ALT1"),
				Archived: 1,
				Slug:     crud.Null[string](),
			}
			if bait, ok := pbBaitLabel[tg.dialect]; ok {
				doc.Label = crud.Set(bait)
			}

			_, err := pbRepo(t, tg).Save(context.Background(), &doc)
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

func TestAProbeThatErrorsKeepsTheConflict(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)
			id := pbIDOf(t, tg, "two@x.io")
			patch := PbDocUpdate{Email: ptr("one@x.io"), Code: ptr("CODE9")}

			_, err := pbRepo(t, tg).Update(context.Background(), id, patch)
			good := pbFault(t, err)
			if crudhttp.Status(err) != 409 {
				t.Fatalf("a healthy probe answered %d", crudhttp.Status(err))
			}
			if good.Partial {
				t.Fatal("a healthy probe said its answer was incomplete")
			}

			ctx := context.Background()
			if _, err := tg.source.Exec(ctx, `DROP TABLE pb_note`); err != nil {
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

func pbRestoreNote(t *testing.T, tg pbTarget) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range pbSchema[tg.database] {
		if strings.Contains(stmt, "CREATE TABLE pb_note") {
			if _, err := tg.source.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: restoring pb_note: %v", tg.name, err)
			}
			return
		}
	}
	t.Fatalf("%s: the fixture has no statement creating pb_note", tg.name)
}

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

			body := func(options ...probe.Option) string {
				doc := PbDoc{TenantID: 1, Email: "one@x.io", Code: "CODE-NEW",
					Alt: crud.Set("ALT-NEW"), Slug: crud.Null[string]()}
				_, err := pbRepo(t, tg, options...).Save(context.Background(), &doc)
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
		_, statementScoped := tg.source.Dialect().(crud.StatementRollback)

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
			_ = crud.InTx(context.Background(), tg.source, func(ctx context.Context) error {
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
			_ = crud.InTx(context.Background(), tg.source, func(ctx context.Context) error {
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

func pbForeignTx(t *testing.T, tg pbTarget) (context.Context, func()) {
	t.Helper()
	ctx := context.Background()
	cls := sqlfault.New(tg.dialect, sqlfault.WithColumns(sqlfault.FromCatalog(tg.cat)))

	if tg.name == "pgx" {
		tx, err := pgPool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return crud.BindExecutor(ctx, tg.source, crudpgx.From(tx, crudpgx.WithFaults(cls))),
			func() { _ = tx.Rollback(context.Background()) }
	}
	var database *sql.DB
	switch tg.name {
	case "postgres":
		database = pgDB
	case "mysql":
		database = myDB
	case "mariadb":
		database = mariaDB
	case "sqlite":
		database = pbSQLiteHandle(t, tg)
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return crud.BindExecutor(ctx, tg.source, crudsql.From(tx, crudsql.WithFaults(cls))),
		func() { _ = tx.Rollback() }
}

func pbSQLiteHandle(t *testing.T, tg pbTarget) *sql.DB {
	t.Helper()
	id, ok := tg.source.(crud.Identified)
	if !ok {
		t.Fatal("the SQLite source cannot name its handle")
	}
	database, ok := id.DataSource().(*sql.DB)
	if !ok {
		t.Fatalf("the SQLite source named a %T", id.DataSource())
	}
	return database
}

func TestABulkWriteAttributesEachViolationToItsRow(t *testing.T) {
	engines := 0
	for _, tg := range pbEngines(t) {
		t.Run(tg.name, func(t *testing.T) {
			engines++
			pbSeed(t, tg)
			repository := PbDocs.Bind(tg.source, faults.Enrich[PbDoc, int64](
				faults.WithProbeFor("SaveAll", probe.Full(tg.cat)),
				faults.WithProbeError(func(op string, err error) {
					t.Errorf("%s: the %s probe failed: %v", tg.name, op, err)
				})))

			rows := []*PbDoc{
				{TenantID: 1, Email: "n0@x.io", Code: "N0", Alt: crud.Set("A0"), Slug: crud.Null[string]()},
				{TenantID: 1, Email: "one@x.io", Code: "N1", Alt: crud.Set("A1"), Slug: crud.Null[string]()},
				{TenantID: 1, Email: "n2@x.io", Code: "N2", Alt: crud.Set("A2"), Slug: crud.Null[string]()},
			}
			err := repository.SaveAll(context.Background(), rows)
			got := pbSet(pbPairs(t, err))
			if !got["unique@[1].Email"] {
				t.Fatalf("the violation was not attributed to its row: %v", pbPairs(t, err))
			}
			for _, wrong := range []string{"unique@[0].Email", "unique@[2].Email"} {
				if got[wrong] {
					t.Fatalf("a row that was fine was blamed as well: %v", pbPairs(t, err))
				}
			}

			pbSeed(t, tg)
			dup := []*PbDoc{
				{TenantID: 1, Email: "same@x.io", Code: "D0", Alt: crud.Set("B0"), Slug: crud.Null[string]()},
				{TenantID: 1, Email: "same@x.io", Code: "D1", Alt: crud.Set("B1"), Slug: crud.Null[string]()},
			}
			err = repository.SaveAll(context.Background(), dup)
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

func TestADeclarationAgainstACatalogWithoutTheTableRefusesToStart(t *testing.T) {
	Unknown := sqlrepo.Define[PbDoc, int64, PbDocUpdate]("pb_doc_that_is_not_there",
		sqlrepo.IndependentTable())
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
				Unknown.Bind(tg.source, faults.Enrich[PbDoc, int64](
					faults.WithProbe(probe.Full(tg.cat))))
			}()

			PbDocs.Bind(tg.source, faults.Enrich[PbDoc, int64](
				faults.WithProbe(probe.Full(tg.cat))))
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

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

			if n := strings.Count(first, `"error_code"`); n != 3 {
				t.Fatalf("the body carries %d violations, so the comparison above measures nothing: %s", n, first)
			}
		})
	}
	if engines != 5 {
		t.Fatalf("walked %d engines: a target was skipped rather than asserted", engines)
	}
}

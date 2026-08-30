//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudpgx"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/crud/probe"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/errs"
)

type core028Event struct {
	ID    int64  `db:"id,pk,auto"`
	Label string `db:"label"`
}

type core028EventUpdate struct {
	Label *string
}

func TestPgx(t *testing.T) {
	RunSuite(t, Target{Name: "pgx", DB: "postgres", Source: crudpgx.Open(pgPool)})
}

// pgx owns the transaction; vv joins it through the context. One physical
// transaction, two APIs.
func TestPgxSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	repository := Users.Bind(crudpgx.Open(pgPool))

	tx, err := pgPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txCtx := crud.WithExecutor(ctx, crudpgx.From(tx))

	u := User{TenantID: 1, Email: "pgx-tx@x.io", Name: "Joined"}
	if stored, err := repository.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	} else {
		u = stored
	}
	// A raw pgx statement in the same transaction sees the row vv wrote.
	var name string
	if err := tx.QueryRow(ctx, "SELECT name FROM users WHERE id = $1", u.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Joined" {
		t.Fatalf("pgx read back %q", name)
	}
	// And vv sees what pgx writes.
	if _, err := tx.Exec(ctx, "UPDATE users SET name = 'ByPgx' WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := repository.GetByID(txCtx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ByPgx" {
		t.Fatalf("github.com/frostgrove/vv read back %q", got.Name)
	}
	// Outside the transaction the row does not exist yet.
	if _, err := repository.GetByID(ctx, u.ID); !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v: the write leaked out of the transaction", err)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := repository.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count = %d err = %v: the rollback did not take", n, err)
	}
}

// pgx gives nested Begin savepoint semantics, so an inner failure can be undone
// without losing the outer transaction.
func TestPgxNestedSavepoint(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudpgx.Open(pgPool)
	repository := Users.Bind(source)

	err := crud.InTx(ctx, source, func(ctx context.Context) error {
		outer := User{TenantID: 1, Email: "outer@x.io", Name: "outer"}
		if _, err := repository.Save(ctx, &outer); err != nil {
			return err
		}
		inner, _ := crud.ExecutorFrom(ctx)
		sp, err := inner.(crud.Beginner).Begin(ctx)
		if err != nil {
			return err
		}
		spCtx := crud.WithExecutor(ctx, sp)
		doomed := User{TenantID: 1, Email: "doomed@x.io", Name: "doomed"}
		if _, err := repository.Save(spCtx, &doomed); err != nil {
			return err
		}
		return sp.Rollback(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}

	all, err := repository.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Email != "outer@x.io" {
		t.Fatalf("rows = %+v, want just the outer one", all)
	}
}

// The COPY fast path is picked up automatically when the executor offers it.
func TestPgxBulkCopy(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudpgx.Open(pgPool)

	bulk, ok := any(source).(crud.BulkInserter)
	if !ok {
		t.Fatal("the pgx adapter should implement crud.BulkInserter")
	}
	rows := [][]any{
		{int64(1), "copy-1@x.io", "c1", 20, true},
		{int64(1), "copy-2@x.io", "c2", 21, true},
	}
	n, err := bulk.CopyFrom(ctx, "users", []string{"tenant_id", "email", "name", "age", "active"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("copied %d rows", n)
	}
	if got, err := Users.Bind(source).Count(ctx); err != nil || got != 2 {
		t.Fatalf("count = %d err = %v", got, err)
	}
}

func TestQualifiedRepositoryAndPgxCopyUseTheSameStructuredTable(t *testing.T) {
	ctx := context.Background()
	if _, err := pgPool.Exec(ctx, `DROP SCHEMA IF EXISTS core028 CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := pgPool.Exec(ctx, `DROP SCHEMA IF EXISTS core028_shadow CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := pgPool.Exec(ctx, `CREATE SCHEMA core028`); err != nil {
		t.Fatal(err)
	}
	if _, err := pgPool.Exec(ctx, `CREATE SCHEMA core028_shadow`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pgPool.Exec(context.Background(), `DROP SCHEMA IF EXISTS core028 CASCADE`)
		_, _ = pgPool.Exec(context.Background(), `DROP SCHEMA IF EXISTS core028_shadow CASCADE`)
	})
	if _, err := pgPool.Exec(ctx, `CREATE TABLE core028.events (
		id BIGSERIAL PRIMARY KEY,
		label TEXT NOT NULL,
		CONSTRAINT events_label_key UNIQUE (label)
	)`); err != nil {
		t.Fatal(err)
	}
	// The same bare table and constraint names in another schema cover a
	// different column. A catalog keyed only on the two bare strings can make
	// the core028 fault confidently point at shadow_code.
	if _, err := pgPool.Exec(ctx, `CREATE TABLE core028_shadow.events (
		id BIGSERIAL PRIMARY KEY,
		label TEXT NOT NULL,
		shadow_code TEXT NOT NULL,
		CONSTRAINT events_label_key UNIQUE (shadow_code)
	)`); err != nil {
		t.Fatal(err)
	}

	bp, err := sqlrepo.TryDefineInSchema[core028Event, int64, core028EventUpdate]("core028", "events")
	if err != nil {
		t.Fatal(err)
	}
	plain := crudpgx.Open(pgPool)
	cat, err := catalog.Load(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	qualified, ok := cat.(catalog.QualifiedCatalog)
	if !ok {
		t.Fatal("loaded catalog has no qualified lookup")
	}
	for _, schema := range []string{"core028", "core028_shadow"} {
		table, ok := qualified.TableByRef(crud.TableRef{Schema: schema, Name: "events"})
		if !ok || table.Schema != schema {
			t.Fatalf("catalog lookup for %s.events = %+v", schema, table)
		}
	}
	classifier := sqlfault.New("postgres", sqlfault.WithColumns(sqlfault.FromCatalog(cat)))
	source := crudpgx.Open(pgPool, crudpgx.WithFaults(classifier))
	repository := bp.Bind(source, faults.Enrich[core028Event, int64](
		faults.WithProbe(probe.Full(cat)),
		faults.WithProbeError(func(op string, err error) {
			t.Errorf("qualified %s probe: %v", op, err)
		})))
	stored, err := repository.Save(ctx, &core028Event{Label: "repository"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repository.GetByID(ctx, stored.ID); err != nil || got.Label != "repository" {
		t.Fatalf("qualified read = %+v, %v", got, err)
	}
	if _, err := repository.Save(ctx, &core028Event{Label: "repository"}); err == nil {
		t.Fatal("qualified unique violation was accepted")
	} else if fault, ok := errs.AsFault(err); !ok {
		t.Fatalf("qualified unique violation was not classified: %v", err)
	} else {
		found := false
		for _, violation := range fault.Violations {
			if violation.Source.Schema == "core028" && violation.Source.Constraint == "events_label_key" {
				found = true
				if len(violation.Source.Columns) != 1 || violation.Source.Columns[0] != "label" || violation.Path.String() != "Label" {
					t.Fatalf("qualified fault used the shadow constraint: %+v", violation)
				}
			}
		}
		if !found {
			t.Fatalf("qualified fault has no core028.events_label_key violation: %+v", fault.Violations)
		}
	}

	n, err := source.CopyFromTable(ctx, crud.TableRef{Schema: "core028", Name: "events"},
		[]string{"label"}, [][]any{{"copy"}})
	if err != nil || n != 1 {
		t.Fatalf("qualified COPY = %d, %v", n, err)
	}
	if count, err := repository.Count(ctx); err != nil || count != 2 {
		t.Fatalf("count after both paths = %d, %v", count, err)
	}

	if _, err := source.CopyFrom(ctx, "core028.events", []string{"label"}, [][]any{{"wrong"}}); err == nil {
		t.Fatal("dotted string COPY was not refused")
	}
	if count, err := repository.Count(ctx); err != nil || count != 2 {
		t.Fatalf("refused COPY changed rows: %d, %v", count, err)
	}
}

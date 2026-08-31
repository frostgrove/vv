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

type core003RLSRow struct {
	ID    int64  `db:"id,pk,auto"`
	Label string `db:"label"`
}

var core003RLSRows = sqlrepo.DefineInSchema[core003RLSRow, int64, struct{}]("core003", "rls_rows")

func dropCore003RLSRole(ctx context.Context) error {
	var exists bool
	if err := pgDB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'core003_rls_writer')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := pgDB.ExecContext(ctx, `DROP OWNED BY core003_rls_writer`); err != nil {
		return err
	}
	_, err := pgDB.ExecContext(ctx, `DROP ROLE core003_rls_writer`)
	return err
}

type observedBulkSource struct {
	crud.Source
	bulk int
	exec int
}

func (this *observedBulkSource) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	this.exec++
	return this.Source.Exec(ctx, query, args...)
}

func (this *observedBulkSource) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return this.Source.Query(ctx, query, args...)
}

func (this *observedBulkSource) UnwrapSource() crud.Source { return this.Source }

func (this *observedBulkSource) UnsafeBulkInsert(ctx context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	bulk, ok := crud.UnsafeBulkInserterOf(this.Source)
	if !ok {
		return 0, crud.ErrNoBulkInsertSupport
	}
	this.bulk++
	return bulk.UnsafeBulkInsert(ctx, target, table, columns, rows)
}

func TestPgx(t *testing.T) {
	RunSuite(t, Target{Name: "pgx", DB: "postgres", Source: crudpgx.Open(pgPool)})
}

func TestPgxSharedTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudpgx.Open(pgPool)
	repository := Users.Bind(source)

	tx, err := pgPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txCtx := source.BindExecutor(ctx, tx)

	u := User{TenantID: 1, Email: "pgx-tx@x.io", Name: "Joined"}
	if stored, err := repository.Save(txCtx, &u); err != nil {
		t.Fatal(err)
	} else {
		u = stored
	}

	var name string
	if err := tx.QueryRow(ctx, "SELECT name FROM users WHERE id = $1", u.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Joined" {
		t.Fatalf("pgx read back %q", name)
	}

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

func TestPgxTransactionIdentityCannotEscapeRollback(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	repository := Users.Bind(crudpgx.Open(pgPool))
	tx, err := pgPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}

	txCtx := crud.WithExecutorFor(ctx, tx, crudpgx.From(tx))
	if _, err := repository.Save(txCtx, &User{TenantID: 1, Email: "scope-miss@x.io", Name: "must-not-escape"}); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("Save returned %v, want ErrExecutorScope", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := repository.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count = %d err = %v: the mismatched binding fell back to the pool", n, err)
	}

	tx, err = pgPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txSource := crudpgx.From(tx)
	txCtx = txSource.BindExecutor(ctx, tx)
	if _, err := repository.Save(txCtx, &User{TenantID: 1, Email: "session-scope-miss@x.io", Name: "must-not-escape"}); !errors.Is(err, crud.ErrExecutorScope) {
		t.Fatalf("BindExecutor with a transaction source returned %v, want ErrExecutorScope", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := repository.Count(ctx); err != nil || n != 0 {
		t.Fatalf("count = %d err = %v: BindExecutor accepted a transaction as canonical source", n, err)
	}
}

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
		spCtx := crud.BindExecutor(ctx, source, sp)
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

func TestPgxInsertBatchSelectsCopyFromTheRepository(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := &observedBulkSource{Source: crudpgx.Open(pgPool)}
	repository := Users.Bind(source)
	rows := []*User{
		{TenantID: 1, Email: "copy-1@x.io", Name: "c1", Age: crud.Set(20), Active: true},
		{TenantID: 1, Email: "copy-2@x.io", Name: "c2", Age: crud.Null[int](), Active: true},
	}
	if err := repository.InsertBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if source.bulk != 1 || source.exec != 0 {
		t.Fatalf("native/portable calls = %d/%d, want COPY selected once", source.bulk, source.exec)
	}
	if got, err := repository.Count(ctx); err != nil || got != 2 {
		t.Fatalf("count = %d err = %v", got, err)
	}
	stored, err := repository.GetAll(ctx, crud.OrderBy(crud.Asc("Email")))
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 || !stored[1].Age.IsNull() {
		t.Fatalf("stored COPY rows = %+v", stored)
	}
}

func TestPgxInsertBatchJoinsRepositoryTransaction(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudpgx.Open(pgPool)
	repository := Users.Bind(source)
	rollback := errors.New("rollback after repository batch")

	err := repository.Tx(ctx, func(txCtx context.Context) error {
		if err := repository.InsertBatch(txCtx, []*User{{
			TenantID: 1, Email: "transaction-copy@x.io", Name: "transaction", Age: crud.Set(20), Active: true,
		}}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("Tx returned %v, want rollback sentinel", err)
	}
	if got, err := repository.Count(ctx); err != nil || got != 0 {
		t.Fatalf("count after repository batch rollback = %d, %v", got, err)
	}
}

func TestPgxPortableBatchIsTheExplicitRLSPath(t *testing.T) {
	ctx := context.Background()
	if _, err := pgDB.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS core003`); err != nil {
		t.Fatal(err)
	}
	if _, err := pgDB.ExecContext(ctx, `DROP TABLE IF EXISTS core003.rls_rows`); err != nil {
		t.Fatal(err)
	}
	if err := dropCore003RLSRole(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := pgDB.ExecContext(cleanupCtx, `DROP TABLE IF EXISTS core003.rls_rows`); err != nil {
			t.Errorf("drop RLS table: %v", err)
		}
		if err := dropCore003RLSRole(cleanupCtx); err != nil {
			t.Errorf("drop RLS role: %v", err)
		}
	})
	for _, statement := range []string{
		`CREATE ROLE core003_rls_writer NOLOGIN`,
		`CREATE TABLE core003.rls_rows (id BIGSERIAL PRIMARY KEY, label TEXT NOT NULL)`,
		`ALTER TABLE core003.rls_rows ENABLE ROW LEVEL SECURITY`,
		`CREATE POLICY core003_allow_all ON core003.rls_rows USING (true) WITH CHECK (true)`,
		`GRANT USAGE ON SCHEMA core003 TO core003_rls_writer`,
		`GRANT SELECT, INSERT ON core003.rls_rows TO core003_rls_writer`,
		`GRANT USAGE, SELECT ON SEQUENCE core003.rls_rows_id_seq TO core003_rls_writer`,
	} {
		if _, err := pgDB.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	connection, err := pgPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SET ROLE core003_rls_writer`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = connection.Exec(context.Background(), `RESET ROLE`) }()
	repository := core003RLSRows.Bind(crudpgx.From(connection))
	if err := repository.InsertBatch(ctx, []*core003RLSRow{{Label: "native-copy-must-refuse"}}); err == nil {
		t.Fatal("COPY unexpectedly accepted an RLS-enabled table")
	}
	if count, err := repository.Count(ctx); err != nil || count != 0 {
		t.Fatalf("count after refused native batch = %d, %v", count, err)
	}
	if err := repository.InsertBatch(ctx, []*core003RLSRow{{Label: "portable-insert"}}, crud.PortableBatch()); err != nil {
		t.Fatal(err)
	}
	if count, err := repository.Count(ctx); err != nil || count != 1 {
		t.Fatalf("count after portable RLS batch = %d, %v", count, err)
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
	if err := repository.InsertBatch(ctx, []*core028Event{{Label: "repository"}}); err == nil {
		t.Fatal("qualified InsertBatch unique violation was accepted")
	} else if fault, ok := errs.AsFault(err); !ok {
		t.Fatalf("qualified InsertBatch violation was not classified: %v", err)
	} else if fault.Op != "InsertBatch" || len(fault.Violations) == 0 || fault.Violations[0].Path.String() != "Label" {
		t.Fatalf("qualified InsertBatch fault = %+v", fault)
	}

	if err := repository.InsertBatch(ctx, []*core028Event{{Label: "copy"}}); err != nil {
		t.Fatalf("qualified repository COPY = %v", err)
	}
	if count, err := repository.Count(ctx); err != nil || count != 2 {
		t.Fatalf("count after both paths = %d, %v", count, err)
	}

	if _, err := source.UnsafeCopyFrom(ctx, "core028.events", []string{"label"}, [][]any{{"wrong"}}); err == nil {
		t.Fatal("dotted string COPY was not refused")
	}
	if count, err := repository.Count(ctx); err != nil || count != 2 {
		t.Fatalf("refused COPY changed rows: %d, %v", count, err)
	}
}

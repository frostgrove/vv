//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/jmoiron/sqlx"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

var chunkedSeedIDs = []int64{101, 102, 103}

func seedChunkedRollbackRows(t *testing.T, source crud.Source) {
	t.Helper()
	rows := Users.Bind(source)
	models := make([]*User, len(chunkedSeedIDs))
	for i, id := range chunkedSeedIDs {
		models[i] = &User{
			ID: id, TenantID: 1, Email: fmt.Sprintf("seed-%d@x.io", id),
			Name: "seed", Active: true,
		}
	}
	if err := rows.SaveAll(context.Background(), models); err != nil {
		t.Fatal(err)
	}
}

func runChunkedForeignWrites(t *testing.T, ctx context.Context, source crud.Source) {
	t.Helper()
	// One bind leaves one id per DELETE statement. Six binds are exactly one
	// assigned-key User row, so SaveAll also has two statements. If InAtomic
	// mistakes the foreign transaction for a pool, both operations open and
	// commit their own transaction before the foreign owner rolls back.
	deleted, err := Users.Bind(withBindBudget(source, 1)).Delete(ctx, chunkedSeedIDs...)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != int64(len(chunkedSeedIDs)) {
		t.Fatalf("deleted = %d, want %d", deleted, len(chunkedSeedIDs))
	}
	if err := Users.Bind(withBindBudget(source, 6)).SaveAll(ctx, []*User{
		{ID: 201, TenantID: 1, Email: "new-201@x.io", Name: "new", Active: true},
		{ID: 202, TenantID: 1, Email: "new-202@x.io", Name: "new", Active: true},
	}); err != nil {
		t.Fatal(err)
	}
}

func assertChunkedForeignRollback(t *testing.T, source crud.Source) {
	t.Helper()
	got, err := Users.Bind(source).GetAll(context.Background(), crud.OrderBy(crud.Asc("ID")))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(chunkedSeedIDs) {
		t.Fatalf("rows after foreign rollback = %+v, want only the three seed rows", got)
	}
	for i, id := range chunkedSeedIDs {
		if got[i].ID != id || got[i].Name != "seed" {
			t.Fatalf("row %d after foreign rollback = %+v, want seed id %d", i, got[i], id)
		}
	}
}

func assertBoundTransaction(t *testing.T, ctx context.Context, source crud.Source) {
	t.Helper()
	executor, ok := crud.ExecutorFor(ctx, source)
	if !ok || !crud.IsTransaction(executor) {
		t.Fatalf("bound %T reports found=%v transaction=%v", executor, ok, ok && crud.IsTransaction(executor))
	}
}

func TestEntForeignTransactionRollsBackChunkedWrites(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudsql.Postgres(pgDB)
	seedChunkedRollbackRows(t, source)

	tx, err := entClient(pgDB, dialect.Postgres).Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	txCtx := source.BindExecutor(ctx, tx)
	assertBoundTransaction(t, txCtx, source)
	runChunkedForeignWrites(t, txCtx, source)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertChunkedForeignRollback(t, source)
}

func TestSQLXForeignTransactionRollsBackChunkedWrites(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudsql.Postgres(pgDB)
	seedChunkedRollbackRows(t, source)

	tx, err := sqlx.NewDb(pgDB, "pgx").BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	txCtx := source.BindExecutor(ctx, tx)
	assertBoundTransaction(t, txCtx, source)
	runChunkedForeignWrites(t, txCtx, source)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertChunkedForeignRollback(t, source)
}

func TestPreparedGormForeignTransactionRollsBackChunkedWrites(t *testing.T) {
	ctx := context.Background()
	truncate(t, pgDB)
	source := crudsql.Postgres(pgDB)
	seedChunkedRollbackRows(t, source)

	database, err := gorm.Open(gormpg.New(gormpg.Config{Conn: pgDB}), &gorm.Config{
		PrepareStmt: true,
		Logger:      logger.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback prepared gorm transaction")
	err = database.Transaction(func(tx *gorm.DB) error {
		if _, ok := tx.Statement.ConnPool.(*gorm.PreparedStmtTX); !ok {
			return fmt.Errorf("prepared gorm transaction uses %T, want *gorm.PreparedStmtTX", tx.Statement.ConnPool)
		}
		txCtx := source.BindExecutor(ctx, tx.Statement.ConnPool)
		assertBoundTransaction(t, txCtx, source)
		runChunkedForeignWrites(t, txCtx, source)
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("gorm transaction returned %v, want rollback sentinel", err)
	}
	assertChunkedForeignRollback(t, source)
}

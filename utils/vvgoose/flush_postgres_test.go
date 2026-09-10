package vvgoose

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/frostgrove/vv/vvdb"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// The scope decision is proved by resolveFlushTargets without a database; this
// is the other half — that the statement behind it reads real schemas, keeps
// PostgreSQL's own out, and that a narrowed flush drops the schema it names and
// only that one.
func TestFlushReadsRealSchemasAndDropsOnlyTheOnesNamed(t *testing.T) {
	dsn := os.Getenv("FROSTGROVE_VVGOOSE_TEST_DSN")
	if dsn == "" {
		t.Skip("FROSTGROVE_VVGOOSE_TEST_DSN is not set")
	}
	config := vvdb.Config{Engine: vvdb.Postgres, DSN: vvdb.Secret(dsn)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := vvdb.Open(&config)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	marker := fmt.Sprint(time.Now().UnixNano())
	named := "vvgoose_flush_named_" + marker
	spared := "vvgoose_flush_spared_" + marker
	for _, schema := range []string{named, spared} {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			if _, err := database.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
				t.Errorf("drop test schema %q: %v", schema, err)
			}
		})
		if _, err := database.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			t.Fatalf("create test schema %q: %v", schema, err)
		}
		if _, err := database.ExecContext(ctx, `CREATE TABLE "`+schema+`".kept (id integer PRIMARY KEY)`); err != nil {
			t.Fatalf("create table in test schema %q: %v", schema, err)
		}
	}

	targets, err := postgresFlushTargets(ctx, database, flushScope{}, "public")
	if err != nil {
		t.Fatalf("resolve flush targets: %v", err)
	}
	for _, schema := range []string{named, spared} {
		if !slices.Contains(targets, schema) {
			t.Fatalf("the default scope did not find schema %q, so it would leave behind a schema the connection owns: %v", schema, targets)
		}
	}
	for _, system := range []string{"pg_catalog", "pg_toast", "information_schema"} {
		if slices.Contains(targets, system) {
			t.Fatalf("the default scope offered to drop PostgreSQL's own schema %q", system)
		}
	}

	flushed, err := runFlush(ctx, config, flushScope{schemas: []string{named}}, alwaysConfirmFlush)
	if err != nil {
		t.Fatalf("flush schema %q: %v", named, err)
	}
	if !flushed {
		t.Fatal("flush reported that it dropped nothing")
	}
	if exists := postgresSchemaExists(t, ctx, database, named); exists {
		t.Fatalf("schema %q survived a flush that named it", named)
	}
	// Control: the flush is narrow. Without this the assertion above would also
	// pass for a flush that dropped every schema in the database.
	if exists := postgresSchemaExists(t, ctx, database, spared); !exists {
		t.Fatalf("schema %q was dropped although the flush named only %q", spared, named)
	}
}

func postgresSchemaExists(t *testing.T, ctx context.Context, database *sql.DB, schema string) bool {
	t.Helper()
	var exists bool
	if err := database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists); err != nil {
		t.Fatalf("look up schema %q: %v", schema, err)
	}
	return exists
}

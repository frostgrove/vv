//go:build integration

package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresVerifySchemaNeverMutatesMissingSchema(t *testing.T) {
	dsn := os.Getenv("FROSTGROVE_JOBSPG_TEST_DSN")
	if dsn == "" {
		t.Skip("FROSTGROVE_JOBSPG_TEST_DSN is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("jobspg_external_%d", time.Now().UnixNano())
	namespace, err := jobs.NamespaceOf("jobspg-schema-management", fmt.Sprint(time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	definition := postgresTestDefinition(t, "jobspg.schema-management")
	driver, err := New(Spec{DB: db, Namespace: namespace, Catalog: jobs.MustCatalog(definition), Schema: schema, SchemaManagement: VerifySchema})
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Prepare(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("verify missing schema = %v", err)
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists); err != nil || exists {
		t.Fatalf("schema exists after verify = (%v, %v)", exists, err)
	}
	if err := driver.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DROP SCHEMA `+quoteIdentifier(schema)+` CASCADE`)
	})
	if err := driver.BindCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if err := driver.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
}

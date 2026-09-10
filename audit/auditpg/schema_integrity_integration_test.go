//go:build integration

package auditpg

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

func TestPostgresReadinessRejectsPhysicalSchemaAndCatalogMutants(t *testing.T) {
	dsn := os.Getenv("FROSTGROVE_AUDITPG_TEST_DSN")
	if dsn == "" {
		t.Fatal("FROSTGROVE_AUDITPG_TEST_DSN is required for the live auditpg suite")
	}
	ctx := context.Background()
	t.Run("immutable trigger removed", func(t *testing.T) {
		db, schema, deployment := prepareIntegritySchema(t, dsn, "trigger")
		if _, err := db.ExecContext(ctx, `DROP TRIGGER revisions_immutable ON `+quoteIdentifier(schema.Name)+`.revisions`); err != nil {
			t.Fatal(err)
		}
		if err := deployment.Verify(ctx); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("verify after dropped trigger = %v", err)
		}
		if deployment.BackingID() != (audit.BackingID{}) {
			t.Fatal("failed verification retained stale readiness")
		}
	})
	t.Run("active catalog is orphaned", func(t *testing.T) {
		db, schema, _ := prepareIntegritySchema(t, dsn, "orphan")
		digest := make([]byte, 32)
		digest[0] = 1
		if _, err := db.ExecContext(ctx, `UPDATE `+quoteIdentifier(schema.Name)+`.settings
SET active_catalog_id='orphan.audit', active_generation=1, active_digest=$1, catalog_set_digest=$1
WHERE singleton`, digest); err != nil {
			t.Fatal(err)
		}
		store, err := New(Spec{DB: db, Source: crudsql.Postgres(db), Schema: schema})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Check(ctx); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("check with orphaned active catalog = %v", err)
		}
	})
	t.Run("essential column removed", func(t *testing.T) {
		db, schema, _ := prepareIntegritySchema(t, dsn, "column")
		if _, err := db.ExecContext(ctx, `ALTER TABLE `+quoteIdentifier(schema.Name)+`.revisions DROP COLUMN wire`); err != nil {
			t.Fatal(err)
		}
		store, err := New(Spec{DB: db, Source: crudsql.Postgres(db), Schema: schema})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Check(ctx); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("check after dropped column = %v", err)
		}
	})
	t.Run("standalone index added", func(t *testing.T) {
		db, schema, _ := prepareIntegritySchema(t, dsn, "index")
		store, err := New(Spec{DB: db, Source: crudsql.Postgres(db), Schema: schema})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Check(ctx); err != nil {
			t.Fatalf("legal schema check = %v", err)
		}
		if _, err := db.ExecContext(ctx, `CREATE UNIQUE INDEX revisions_operation_extra ON `+quoteIdentifier(schema.Name)+`.revisions (operation)`); err != nil {
			t.Fatal(err)
		}
		if err := store.Check(ctx); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("check after standalone index = %v", err)
		}
	})
	t.Run("persisted identity is zero", func(t *testing.T) {
		db, schema, _ := prepareIntegritySchema(t, dsn, "identity")
		if _, err := db.ExecContext(ctx, `UPDATE `+quoteIdentifier(schema.Name)+`.settings SET backing_id=$1 WHERE singleton`, make([]byte, 16)); err != nil {
			t.Fatal(err)
		}
		store, err := New(Spec{DB: db, Source: crudsql.Postgres(db), Schema: schema})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Check(ctx); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("check with zero persisted identity = %v", err)
		}
	})
}

func prepareIntegritySchema(t *testing.T, dsn, label string) (*sql.DB, Schema, *Deployment) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	schema := Schema{Name: "auditpg_guard_" + label + "_" + suffix}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quoteIdentifier(schema.Name)+` CASCADE`); err != nil {
			t.Errorf("drop scratch schema: %v", err)
		}
	})
	source := crudsql.Postgres(db)
	deployment, err := NewDeployment(DeploymentSpec{
		Runtime: Spec{DB: db, Source: source, Schema: schema}, SchemaManagement: ManageSchema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, schema, deployment
}

//go:build integration

package jobspg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresSchemaHardeningUpgradesV4AndRejectsDrift(t *testing.T) {
	db, ctx := openSchemaHardeningPostgres(t)
	marker := fmt.Sprint(time.Now().UnixNano())
	schema := "jobspg_hardening_" + marker
	repo := newRepository(schema)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+repo.schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	namespace, err := jobs.NamespaceOf("jobspg-hardening", marker)
	if err != nil {
		t.Fatal(err)
	}
	definition := postgresTestDefinition(t, "jobspg.hardening")
	catalog := jobs.MustCatalog(definition)
	driver, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	for _, spec := range repo.schemaConstraints() {
		if _, err := db.ExecContext(ctx, `ALTER TABLE `+repo.schemaConstraintTable(spec)+` DROP CONSTRAINT `+quoteIdentifier(spec.name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := driver.CheckSchema(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("CheckSchema accepted v4 without hardening constraints: %v", err)
	}
	if err := driver.Prepare(ctx); err != nil {
		t.Fatalf("Prepare did not upgrade existing v4 constraints: %v", err)
	}
	for _, spec := range repo.schemaConstraints() {
		var validated bool
		if err := db.QueryRowContext(ctx, `SELECT convalidated FROM pg_constraint WHERE conrelid = $1::regclass AND conname = $2`, repo.schemaConstraintTable(spec), spec.name).Scan(&validated); err != nil || !validated {
			t.Fatalf("constraint %q = (%v, %v)", spec.name, validated, err)
		}
	}
	driftedConstraint := repo.schemaConstraints()[2]
	if _, err := db.ExecContext(ctx, `ALTER TABLE `+repo.schemaConstraintTable(driftedConstraint)+` DROP CONSTRAINT `+quoteIdentifier(driftedConstraint.name)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE `+repo.schemaConstraintTable(driftedConstraint)+` ADD CONSTRAINT `+quoteIdentifier(driftedConstraint.name)+` CHECK (true)`); err != nil {
		t.Fatal(err)
	}
	if err := driver.CheckSchema(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("CheckSchema accepted same-name CHECK (true): %v", err)
	}
	if err := driver.Prepare(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("Prepare accepted same-name CHECK (true): %v", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE `+repo.schemaConstraintTable(driftedConstraint)+` DROP CONSTRAINT `+quoteIdentifier(driftedConstraint.name)); err != nil {
		t.Fatal(err)
	}
	for _, statement := range repo.schemaHardeningMigrationStatements() {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := driver.CheckSchema(ctx); err != nil {
		t.Fatalf("CheckSchema rejected restored constraint: %v", err)
	}
	for index, spec := range operationalIndexes {
		qualified := repo.schema + `.` + quoteIdentifier(spec.name)
		if _, err := db.ExecContext(ctx, `DROP INDEX `+qualified); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE INDEX `+quoteIdentifier(spec.name)+` ON `+repo.operationalIndexTable(spec)+` (`+spec.columns[0]+`)`); err != nil {
			t.Fatal(err)
		}
		if err := driver.CheckSchema(ctx); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("CheckSchema accepted wrong operational index %q: %v", spec.name, err)
		}
		if err := driver.Prepare(ctx); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("Prepare accepted wrong operational index %q: %v", spec.name, err)
		}
		if _, err := db.ExecContext(ctx, `DROP INDEX `+qualified); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, repo.operationalIndexStatements()[index]); err != nil {
			t.Fatal(err)
		}
		if err := driver.CheckSchema(ctx); err != nil {
			t.Fatalf("CheckSchema rejected restored operational index %q: %v", spec.name, err)
		}
	}
	assertPostgresSchemaBounds(t, ctx, db, repo, namespace, catalog.Fingerprint())
	assertPostgresMaximumPayloadRecord(t, ctx, db, schema)
}

func TestPostgresManualMigrationRejectsWrongConstraintBeforeVersionStamp(t *testing.T) {
	db, ctx := openSchemaHardeningPostgres(t)
	schema := "jobspg_constraint_manual_" + fmt.Sprint(time.Now().UnixNano())
	repo := newRepository(schema)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+repo.schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	statements, err := MigrationStatements(schema)
	if err != nil {
		t.Fatal(err)
	}
	target := repo.schemaConstraints()[0]
	injected := false
	rejected := false
	for _, statement := range statements {
		if strings.Contains(statement, `ADD CONSTRAINT `+quoteIdentifier(target.name)) {
			if _, err := db.ExecContext(ctx, `ALTER TABLE `+repo.schemaConstraintTable(target)+` ADD CONSTRAINT `+quoteIdentifier(target.name)+` CHECK (true)`); err != nil {
				t.Fatal(err)
			}
			injected = true
		}
		if _, err := db.ExecContext(ctx, statement); err != nil {
			if !strings.Contains(statement, "schema constraint "+target.name+" definition mismatch") {
				t.Fatalf("manual migration failed before exact constraint validation: %v", err)
			}
			rejected = true
			break
		}
	}
	if !injected || !rejected {
		t.Fatalf("manual constraint path = (injected %v, rejected %v)", injected, rejected)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM `+repo.meta+` WHERE singleton`).Scan(&version); err != nil || version != 2 {
		t.Fatalf("manual migration version after rejection = (%d, %v)", version, err)
	}
}

func TestPostgresManualMigrationRejectsWrongOperationalIndexBeforeVersionStamp(t *testing.T) {
	db, ctx := openSchemaHardeningPostgres(t)
	schema := "jobspg_index_manual_" + fmt.Sprint(time.Now().UnixNano())
	repo := newRepository(schema)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+repo.schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	statements, err := MigrationStatements(schema)
	if err != nil {
		t.Fatal(err)
	}
	injected := false
	rejected := false
	for _, statement := range statements {
		if strings.Contains(statement, `CREATE INDEX IF NOT EXISTS "deliveries_ready_idx"`) {
			if _, err := db.ExecContext(ctx, `CREATE INDEX deliveries_ready_idx ON `+repo.deliveries+` (namespace)`); err != nil {
				t.Fatal(err)
			}
			injected = true
		}
		if _, err := db.ExecContext(ctx, statement); err != nil {
			if !strings.Contains(statement, "operational index deliveries_ready_idx schema mismatch") {
				t.Fatalf("manual migration failed before operational validation: %v", err)
			}
			rejected = true
			break
		}
	}
	if !injected || !rejected {
		t.Fatalf("manual operational-index path = (injected %v, rejected %v)", injected, rejected)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM `+repo.meta+` WHERE singleton`).Scan(&version); err != nil || version != 2 {
		t.Fatalf("manual migration version after rejection = (%d, %v)", version, err)
	}
}

func assertPostgresSchemaBounds(t *testing.T, ctx context.Context, db *sql.DB, repo repository, namespace jobs.Namespace, fingerprint string) {
	t.Helper()
	invalidNamespace := bytes.Repeat([]byte{0x7f}, 32)
	if _, err := db.ExecContext(ctx, `INSERT INTO `+repo.catalogs+` (namespace, application, environment, fingerprint) VALUES ($1, $2, $3, $4)`, invalidNamespace, strings.Repeat("a", jobs.MaxNameBytes+1), "test", fingerprint); err == nil {
		t.Fatal("catalog application above the core bound was stored")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO `+repo.catalogs+` (namespace, application, environment, fingerprint) VALUES ($1, $2, $3, $4)`, invalidNamespace, "test", strings.Repeat("e", jobs.MaxNameBytes+1), fingerprint); err == nil {
		t.Fatal("catalog environment above the core bound was stored")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO `+repo.definitions+` (namespace, definition, fingerprint) VALUES ($1, $2, $3)`, namespaceArgument(namespace), strings.Repeat("d", jobs.MaxNameBytes+1), fingerprint); err == nil {
		t.Fatal("catalog definition above the core bound was stored")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO `+repo.definitions+` (namespace, definition, fingerprint, codec, codec_mode, codec_version, codec_revisions, partition_mode, payload_identity, payload_identity_version, payload_identity_automatic) VALUES ($1, 'jobspg.direct.codec', $2, $3, 'safe', 1, '1', 0, 'string', 1, true)`, namespaceArgument(namespace), fingerprint, strings.Repeat("c", jobs.MaxCodecIDBytes+1)); err == nil {
		t.Fatal("catalog codec above the core bound was stored")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO `+repo.definitions+` (namespace, definition, fingerprint, codec, codec_mode, codec_version, codec_revisions, partition_mode, payload_identity, payload_identity_version, payload_identity_automatic) VALUES ($1, 'jobspg.direct.identity', $2, 'string', 'safe', 1, '1', 0, $3, 1, true)`, namespaceArgument(namespace), fingerprint, strings.Repeat("i", jobs.MaxCodecIDBytes+1)); err == nil {
		t.Fatal("catalog payload identity above the core bound was stored")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO `+repo.definitions+` (namespace, definition, fingerprint, codec, codec_mode, codec_version, codec_revisions, partition_mode, payload_identity, payload_identity_version, payload_identity_automatic) VALUES ($1, 'jobspg.direct.revisions', $2, 'string', 'safe', 1, '1,01', 0, 'string', 1, true)`, namespaceArgument(namespace), fingerprint); err == nil {
		t.Fatal("noncanonical catalog revisions were stored")
	}
	type invalidDelivery struct {
		definition      string
		codec           string
		priority        int
		state           int
		recordSize      int
		record          []byte
		payloadIdentity any
		payloadVersion  any
		payloadDigest   any
		excludedBinding any
		excludedBuild   any
	}
	base := invalidDelivery{definition: "jobspg.direct", codec: "string", priority: 1, state: int(jobs.InvocationQueued), recordSize: 1, record: []byte{1}}
	tests := []struct {
		name   string
		change func(*invalidDelivery)
	}{
		{name: "definition", change: func(value *invalidDelivery) { value.definition = strings.Repeat("d", jobs.MaxNameBytes+1) }},
		{name: "codec", change: func(value *invalidDelivery) { value.codec = strings.Repeat("c", jobs.MaxCodecIDBytes+1) }},
		{name: "payload identity", change: func(value *invalidDelivery) {
			value.payloadIdentity = strings.Repeat("i", jobs.MaxCodecIDBytes+1)
			value.payloadVersion = 1
			value.payloadDigest = bytes.Repeat([]byte{1}, 32)
		}},
		{name: "binding", change: func(value *invalidDelivery) {
			value.excludedBinding = strings.Repeat("b", jobs.MaxBindingNameBytes+1)
			value.excludedBuild = "build"
		}},
		{name: "build", change: func(value *invalidDelivery) {
			value.excludedBinding = "binding"
			value.excludedBuild = strings.Repeat("b", jobs.MaxBuildIDBytes+1)
		}},
		{name: "priority", change: func(value *invalidDelivery) { value.priority = jobs.MaximumPriority + 1 }},
		{name: "state", change: func(value *invalidDelivery) { value.state = int(jobs.InvocationTerminated) + 1 }},
		{name: "logical record size", change: func(value *invalidDelivery) { value.recordSize = jobs.MaxDeliveryRecordBytes + 1 }},
	}
	for index, test := range tests {
		value := base
		test.change(&value)
		id := make([]byte, jobs.InvocationIDBytes)
		id[0] = byte(index + 1)
		_, err := db.ExecContext(ctx, `INSERT INTO `+repo.deliveries+` (
namespace, id, definition, codec, codec_version, priority, state, record_size, record,
payload_identity, payload_version, payload_digest, excluded_binding, excluded_build, created_at
) VALUES ($1, $2, $3, $4, 1, $5, $6, $7, $8, $9, $10, $11, $12, $13, clock_timestamp())`, namespaceArgument(namespace), id, value.definition, value.codec, value.priority, value.state, value.recordSize, value.record, value.payloadIdentity, value.payloadVersion, value.payloadDigest, value.excludedBinding, value.excludedBuild)
		if err == nil {
			t.Fatalf("direct invalid %s was stored", test.name)
		}
	}
	id := bytes.Repeat([]byte{0x7e}, jobs.InvocationIDBytes)
	if _, err := db.ExecContext(ctx, `INSERT INTO `+repo.deliveries+` (namespace, id, definition, codec, codec_version, priority, state, record_size, record, created_at) VALUES ($1, $2, 'jobspg.direct', 'string', 1, 1, $3, 1, repeat('x', $4)::bytea, clock_timestamp())`, namespaceArgument(namespace), id, int(jobs.InvocationQueued), maxEncodedDeliveryRecordBytes+1); err == nil {
		t.Fatal("encoded record above the PostgreSQL bound was stored")
	}
}

func assertPostgresMaximumPayloadRecord(t *testing.T, ctx context.Context, db *sql.DB, schema string) {
	t.Helper()
	namespace, catalog, placement := testMaximumPayloadPlacement(t)
	driver, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Place(ctx, placement); err != nil {
		t.Fatalf("store maximum-payload encoded record: %v", err)
	}
}

func openSchemaHardeningPostgres(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("FROSTGROVE_JOBSPG_TEST_DSN")
	if dsn == "" {
		t.Skip("FROSTGROVE_JOBSPG_TEST_DSN is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

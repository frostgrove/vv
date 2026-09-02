//go:build integration

package jobspg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
)

func TestPostgresIntentReservationsSurviveApplicationAndRollingRedrive(t *testing.T) {
	db, ctx := openSchemaHardeningPostgres(t)
	marker := fmt.Sprint(time.Now().UnixNano())
	schema := "jobspg_intent_keys_" + marker
	repo := newRepository(schema)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+repo.schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	namespace, err := jobs.NamespaceOf("jobspg-intent-keys", marker)
	if err != nil {
		t.Fatal(err)
	}
	unique := postgresTestDefinition(t, "jobspg.intent-keys.unique")
	collapse := postgresTestDefinition(t, "jobspg.intent-keys.collapse")
	legacyUnique := postgresTestDefinition(t, "jobspg.intent-keys.legacy-unique")
	catalog := jobs.MustCatalog(unique, collapse, legacyUnique)
	driver, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema, SchemaManagement: ManageSchema})
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	currentQueue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	rolling, err := jobs.NewIntentDigestPlan(jobs.DigestRevision2, jobs.DigestRevision1)
	if err != nil {
		t.Fatal(err)
	}
	rollingQueue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver, Digests: rolling})
	if err != nil {
		t.Fatal(err)
	}

	uniqueID, err := jobs.Enqueue(ctx, rollingQueue, unique, "first", jobs.Unique("rolling-unique"))
	if err != nil {
		t.Fatal(err)
	}
	uniqueBefore := readPostgresIntentMetadata(t, ctx, db, repo, namespace, uniqueID)
	if len(uniqueBefore) != jobs.MaxIntentDigestKeys || uniqueBefore[0].Revision() != jobs.DigestRevision2 || uniqueBefore[1].Revision() != jobs.DigestRevision1 {
		t.Fatalf("rolling unique metadata = %v", uniqueBefore)
	}
	view, err := driver.Get(ctx, uniqueID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := view.Invocation().LegacyIntent(); ok {
		t.Fatal("rolling unique stored a raw caller intent")
	}
	rawBefore := readPostgresIntentMetadataBytes(t, ctx, db, repo, namespace, uniqueID)
	if _, err := db.ExecContext(ctx, `UPDATE `+repo.deliveries+` SET intent_keys = ''::bytea WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(uniqueID)); err == nil {
		t.Fatal("schema accepted malformed intent metadata")
	}
	adminFinishInvocation(t, ctx, driver, namespace, unique, uniqueID, 21)
	rawAfter := readPostgresIntentMetadataBytes(t, ctx, db, repo, namespace, uniqueID)
	if !bytes.Equal(rawAfter, rawBefore) {
		t.Fatal("application rewrite changed durable intent metadata")
	}
	if count := countPostgresIntentRows(t, ctx, db, repo, namespace, uniqueID); count != 0 {
		t.Fatalf("terminal unique intent rows = %d", count)
	}
	if _, err := driver.Redrive(ctx, uniqueID); err != nil {
		t.Fatal(err)
	}
	if count := countPostgresIntentRows(t, ctx, db, repo, namespace, uniqueID); count != jobs.MaxIntentDigestKeys {
		t.Fatalf("redriven unique intent rows = %d", count)
	}
	if id, err := jobs.Enqueue(ctx, currentQueue, unique, "v1 duplicate", jobs.Unique("rolling-unique")); err != nil || id != uniqueID {
		t.Fatalf("v1 unique after rolling redrive = (%v, %v), want %v", id, err, uniqueID)
	}
	if id, err := jobs.Enqueue(ctx, rollingQueue, unique, "v2 duplicate", jobs.Unique("rolling-unique")); err != nil || id != uniqueID {
		t.Fatalf("v2 unique after rolling redrive = (%v, %v), want %v", id, err, uniqueID)
	}

	collapseID, err := jobs.Enqueue(ctx, currentQueue, collapse, "legacy", jobs.Collapse("rolling-collapse"))
	if err != nil {
		t.Fatal(err)
	}
	legacyView, err := driver.Get(ctx, collapseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE `+repo.deliveries+` SET intent_keys = NULL WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(collapseID)); err != nil {
		t.Fatal(err)
	}
	if id, err := jobs.Enqueue(ctx, rollingQueue, collapse, "upgraded", jobs.Collapse("rolling-collapse")); err != nil || id != collapseID {
		t.Fatalf("rolling collapse upgrade = (%v, %v), want %v", id, err, collapseID)
	}
	upgraded := readPostgresIntentMetadata(t, ctx, db, repo, namespace, collapseID)
	if len(upgraded) != jobs.MaxIntentDigestKeys || upgraded[0] != legacyView.Invocation().Intent() || upgraded[1].Revision() != jobs.DigestRevision2 {
		t.Fatalf("upgraded collapse metadata = %v", upgraded)
	}
	adminFinishInvocation(t, ctx, driver, namespace, collapse, collapseID, 22)
	if _, err := driver.Redrive(ctx, collapseID); err != nil {
		t.Fatal(err)
	}
	if count := countPostgresIntentRows(t, ctx, db, repo, namespace, collapseID); count != jobs.MaxIntentDigestKeys {
		t.Fatalf("redriven upgraded collapse intent rows = %d", count)
	}
	if id, err := jobs.Enqueue(ctx, currentQueue, collapse, "v1 duplicate", jobs.Collapse("rolling-collapse")); err != nil || id != collapseID {
		t.Fatalf("v1 collapse after rolling redrive = (%v, %v), want %v", id, err, collapseID)
	}
	if id, err := jobs.Enqueue(ctx, rollingQueue, collapse, "v2 duplicate", jobs.Collapse("rolling-collapse")); err != nil || id != collapseID {
		t.Fatalf("v2 collapse after rolling redrive = (%v, %v), want %v", id, err, collapseID)
	}

	legacyPlan, err := jobs.WithLegacyIntentCompatibility(jobs.CurrentIntentDigestPlan())
	if err != nil {
		t.Fatal(err)
	}
	legacyQueue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver, Digests: legacyPlan})
	if err != nil {
		t.Fatal(err)
	}
	legacyID, err := jobs.Enqueue(ctx, legacyQueue, legacyUnique, "legacy", jobs.Unique("legacy-fallback"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE `+repo.deliveries+` SET intent_keys = NULL WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(legacyID)); err != nil {
		t.Fatal(err)
	}
	if id, err := jobs.Enqueue(ctx, currentQueue, legacyUnique, "current-only", jobs.Unique("legacy-fallback")); err != nil || id != legacyID {
		t.Fatalf("current-only legacy placement = (%v, %v), want %v", id, err, legacyID)
	}
	if encoded := readPostgresIntentMetadataBytes(t, ctx, db, repo, namespace, legacyID); encoded != nil {
		t.Fatal("current-only placement disabled legacy intent regeneration")
	}
	adminFinishInvocation(t, ctx, driver, namespace, legacyUnique, legacyID, 23)
	if _, err := driver.Redrive(ctx, legacyID); err != nil {
		t.Fatal(err)
	}
	if count := countPostgresIntentRows(t, ctx, db, repo, namespace, legacyID); count != jobs.MaxIntentDigestKeys {
		t.Fatalf("legacy fallback intent rows = %d", count)
	}
}

func TestPostgresIntentKeysMigrationUpgradesV4(t *testing.T) {
	db, ctx := openSchemaHardeningPostgres(t)
	marker := fmt.Sprint(time.Now().UnixNano())
	schema := "jobspg_intent_keys_upgrade_" + marker
	repo := newRepository(schema)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+repo.schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	namespace, err := jobs.NamespaceOf("jobspg-intent-keys-upgrade", marker)
	if err != nil {
		t.Fatal(err)
	}
	definition := postgresTestDefinition(t, "jobspg.intent-keys.upgrade")
	catalog := jobs.MustCatalog(definition)
	driver, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema, SchemaManagement: ManageSchema})
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE ` + repo.deliveries + ` DROP CONSTRAINT deliveries_intent_keys_contract_check`,
		`ALTER TABLE ` + repo.deliveries + ` DROP COLUMN intent_keys`,
		`UPDATE ` + repo.meta + ` SET version = 4 WHERE singleton = true`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	upgraded, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema, SchemaManagement: ManageSchema})
	if err != nil {
		t.Fatal(err)
	}
	if err := upgraded.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	var nullable string
	var validated bool
	if err := db.QueryRowContext(ctx, `SELECT version FROM `+repo.meta+` WHERE singleton`).Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("upgraded schema version = (%d, %v)", version, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'deliveries' AND column_name = 'intent_keys'`, schema).Scan(&nullable); err != nil || nullable != "YES" {
		t.Fatalf("intent_keys nullable = (%q, %v)", nullable, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT convalidated FROM pg_constraint WHERE conrelid = $1::regclass AND conname = 'deliveries_intent_keys_contract_check'`, repo.deliveries).Scan(&validated); err != nil || !validated {
		t.Fatalf("intent key constraint = (%v, %v)", validated, err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE `+repo.deliveries+` ALTER COLUMN intent_keys SET NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.CheckSchema(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("CheckSchema accepted non-null intent_keys: %v", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE `+repo.deliveries+` ALTER COLUMN intent_keys DROP NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.CheckSchema(ctx); err != nil {
		t.Fatalf("CheckSchema rejected restored intent_keys: %v", err)
	}
}

func readPostgresIntentMetadata(t *testing.T, ctx context.Context, db *sql.DB, repo repository, namespace jobs.Namespace, id jobs.InvocationID) []jobs.IntentKey {
	t.Helper()
	encoded := readPostgresIntentMetadataBytes(t, ctx, db, repo, namespace, id)
	keys, err := decodeIntentKeys(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func readPostgresIntentMetadataBytes(t *testing.T, ctx context.Context, db *sql.DB, repo repository, namespace jobs.Namespace, id jobs.InvocationID) []byte {
	t.Helper()
	var encoded []byte
	if err := db.QueryRowContext(ctx, `SELECT intent_keys FROM `+repo.deliveries+` WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(id)).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	return encoded
}

func countPostgresIntentRows(t *testing.T, ctx context.Context, db *sql.DB, repo repository, namespace jobs.Namespace, id jobs.InvocationID) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+repo.intents+` WHERE namespace = $1 AND invocation_id = $2`, namespaceArgument(namespace), invocationArgument(id)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

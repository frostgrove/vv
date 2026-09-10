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

// The failure this whole version step exists for. A build appended
// deliveries_recent_idx to operationalIndexes without raising SchemaVersion, so
// every database that had already migrated took the currentVersion branch,
// which validates and never creates, and refused to start for good.
func TestPostgresVersionFiveStartUpCreatesAnIndexDeclaredAfterIt(t *testing.T) {
	db, ctx := openDeclaredIndexPostgres(t, 60*time.Second)
	schema, repo, namespace, catalog := stageDeclaredIndexSchema(t, db, ctx, "version_five")
	driver := openCatalogDriver(t, ctx, db, schema, namespace, catalog)
	target := operationalIndexes[len(operationalIndexes)-1]

	rewindDeclaredIndexSchema(t, ctx, db, repo, 5, target.name)
	assertOperationalIndexAbsent(t, ctx, db, repo, target, "on the rewound schema")
	if err := driver.CheckSchema(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("a schema missing operational index %q was accepted, so restoring it would prove nothing: %v", target.name, err)
	}

	if err := prepareCatalogDriver(ctx, db, schema, namespace, catalog); err != nil {
		t.Fatalf("start-up on a version 5 schema failed instead of migrating it to %d: %v", SchemaVersion, err)
	}

	assertOperationalIndexReady(t, ctx, db, repo, target, "after the version step")
	if version := declaredIndexSchemaVersion(t, ctx, db, repo); version != SchemaVersion {
		t.Fatalf("the schema did not reach the current version: stored %d, want %d", version, SchemaVersion)
	}
}

// Versions 3 and 4 were the worst case: too current for the branch that
// created operational indexes, not current enough for the branch that only
// validates, so neither index class was ever created there.
func TestPostgresIntermediateVersionStartUpCreatesBothIndexClasses(t *testing.T) {
	db, ctx := openDeclaredIndexPostgres(t, 120*time.Second)
	schema, repo, namespace, catalog := stageDeclaredIndexSchema(t, db, ctx, "intermediate")
	driver := openCatalogDriver(t, ctx, db, schema, namespace, catalog)
	operational := operationalIndexes[len(operationalIndexes)-1]
	retention := retentionIndexes[len(retentionIndexes)-1]

	for _, version := range []int{3, 4} {
		stage := fmt.Sprintf("version %d", version)
		rewindDeclaredIndexSchema(t, ctx, db, repo, version, operational.name, retention.name)
		assertOperationalIndexAbsent(t, ctx, db, repo, operational, stage)
		assertRetentionIndexAbsent(t, ctx, db, repo, retention, stage)
		if err := driver.CheckSchema(ctx); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("a %s schema missing both index classes was accepted, so restoring them would prove nothing: %v", stage, err)
		}

		if err := prepareCatalogDriver(ctx, db, schema, namespace, catalog); err != nil {
			t.Fatalf("start-up on a %s schema failed instead of creating both index classes: %v", stage, err)
		}

		assertOperationalIndexReady(t, ctx, db, repo, operational, stage)
		assertRetentionIndexReady(t, ctx, db, repo, retention, stage)
		if got := declaredIndexSchemaVersion(t, ctx, db, repo); got != SchemaVersion {
			t.Fatalf("a %s schema did not reach the current version: stored %d, want %d", stage, got, SchemaVersion)
		}
	}
}

// A start-up on a schema that is already current writes nothing. An index that
// is exactly right keeps its relation, so restarting a process neither rebuilds
// an index nor takes a lock on the delivery table to do it.
func TestPostgresCurrentSchemaRunsNoDDLOnRestart(t *testing.T) {
	db, ctx := openDeclaredIndexPostgres(t, 60*time.Second)
	schema, repo, namespace, catalog := stageDeclaredIndexSchema(t, db, ctx, "restart")
	driver := openCatalogDriver(t, ctx, db, schema, namespace, catalog)

	names := declaredIndexNames()
	before := make(map[string]uint32, len(names))
	for _, name := range names {
		relation := declaredIndexRelation(t, ctx, db, schema, name)
		if relation == 0 {
			t.Fatalf("declared index %q is missing after a successful start-up, so there is no relation to hold stable", name)
		}
		before[name] = relation
	}

	if err := driver.Prepare(ctx); err != nil {
		t.Fatalf("second start-up on an unchanged schema = %v", err)
	}

	for _, name := range names {
		if after := declaredIndexRelation(t, ctx, db, schema, name); after != before[name] {
			t.Fatalf("restarting rebuilt declared index %q although it was already exactly right: relation was %d, now %d", name, before[name], after)
		}
	}

	// Control: an unchanged relation only means something if a real rebuild
	// changes it. Rewind the version, drop them, and let the version step run.
	rewindDeclaredIndexSchema(t, ctx, db, repo, 5, names...)
	if err := driver.Prepare(ctx); err != nil {
		t.Fatalf("the version step did not recreate the dropped declared indexes: %v", err)
	}
	for _, name := range names {
		after := declaredIndexRelation(t, ctx, db, schema, name)
		if after == 0 || after == before[name] {
			t.Fatalf("declared index %q did not come back as a new relation after being dropped, so the assertions above cannot tell a rebuild from a no-op: relation was %d, now %d", name, before[name], after)
		}
	}
}

// Creating anything belongs to ManageSchema. A verify-only deployment reads the
// same broken schema, refuses it, and leaves it exactly as it found it — D-101
// survives this version step.
func TestPostgresVerifySchemaStartUpRefusesAMissingIndexAndCreatesNothing(t *testing.T) {
	db, ctx := openDeclaredIndexPostgres(t, 60*time.Second)
	schema, repo, namespace, catalog := stageDeclaredIndexSchema(t, db, ctx, "verify_only")
	openCatalogDriver(t, ctx, db, schema, namespace, catalog)
	target := operationalIndexes[len(operationalIndexes)-1]
	rewindDeclaredIndexSchema(t, ctx, db, repo, 5, target.name)

	verifier, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema, SchemaManagement: VerifySchema})
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.Prepare(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("a verify-only start-up accepted a schema missing operational index %q: %v", target.name, err)
	}
	assertOperationalIndexAbsent(t, ctx, db, repo, target, "after a verify-only start-up")
	if version := declaredIndexSchemaVersion(t, ctx, db, repo); version != 5 {
		t.Fatalf("a verify-only start-up moved the schema version to %d", version)
	}

	// Control: the same schema, the same missing index, under ManageSchema.
	// Without this, the refusal above could be any unrelated breakage.
	if err := prepareCatalogDriver(ctx, db, schema, namespace, catalog); err != nil {
		t.Fatalf("a managed start-up on the same schema failed, so the refusal above says nothing about the deployment profile: %v", err)
	}
	assertOperationalIndexReady(t, ctx, db, repo, target, "after a managed start-up")
}

func openDeclaredIndexPostgres(t *testing.T, budget time.Duration) (*sql.DB, context.Context) {
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
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func stageDeclaredIndexSchema(t *testing.T, db *sql.DB, ctx context.Context, name string) (string, repository, jobs.Namespace, jobs.Catalog) {
	t.Helper()
	marker := fmt.Sprint(time.Now().UnixNano())
	schema := "jobspg_declared_" + name + "_" + marker
	repo := newRepository(schema)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+repo.schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	namespace, err := jobs.NamespaceOf("jobspg-declared-"+name, marker)
	if err != nil {
		t.Fatal(err)
	}
	return schema, repo, namespace, jobs.MustCatalog(postgresTestDefinition(t, "jobspg.declared-"+name))
}

// rewindDeclaredIndexSchema stages the state a database is in when it last
// migrated under an older build: the named indexes never existed, and the
// stored version is the one that build stamped.
func rewindDeclaredIndexSchema(t *testing.T, ctx context.Context, db *sql.DB, repo repository, version int, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS `+repo.schema+`.`+quoteIdentifier(name)); err != nil {
			t.Fatalf("drop index %q to stage a schema that predates it: %v", name, err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE `+repo.meta+` SET version = $1 WHERE singleton = true`, version); err != nil {
		t.Fatalf("stage stored schema version %d: %v", version, err)
	}
	if got := declaredIndexSchemaVersion(t, ctx, db, repo); got != version {
		t.Fatalf("stored schema version is %d, so this run does not exercise the version %d path at all", got, version)
	}
}

func declaredIndexSchemaVersion(t *testing.T, ctx context.Context, db *sql.DB, repo repository) int {
	t.Helper()
	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM `+repo.meta+` WHERE singleton = true`).Scan(&version); err != nil {
		t.Fatalf("read the stored schema version: %v", err)
	}
	return version
}

// declaredIndexRelation answers the index relation's identity, or 0 when the
// index does not exist. PostgreSQL mints a fresh one for a rebuilt relation, so
// an unchanged answer across a restart is the statement that nothing was built.
func declaredIndexRelation(t *testing.T, ctx context.Context, db *sql.DB, schema, name string) uint32 {
	t.Helper()
	var relation uint32
	err := db.QueryRowContext(ctx, `SELECT relation.oid
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = $1 AND relation.relname = $2`, schema, name).Scan(&relation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("read the relation of index %q: %v", name, err)
	}
	return relation
}

func assertOperationalIndexAbsent(t *testing.T, ctx context.Context, db *sql.DB, repo repository, spec operationalIndex, stage string) {
	t.Helper()
	valid, ready, matching, exists, err := repo.operationalIndexState(ctx, db, spec)
	if err != nil || exists || valid || ready || matching {
		t.Fatalf("operational index %q is present %s when the test needs it gone: exists=%v valid=%v ready=%v matching=%v err=%v", spec.name, stage, exists, valid, ready, matching, err)
	}
}

func assertOperationalIndexReady(t *testing.T, ctx context.Context, db *sql.DB, repo repository, spec operationalIndex, stage string) {
	t.Helper()
	valid, ready, matching, exists, err := repo.operationalIndexState(ctx, db, spec)
	if err != nil || !exists || !valid || !ready || !matching {
		t.Fatalf("operational index %q was not created %s: exists=%v valid=%v ready=%v matching=%v err=%v", spec.name, stage, exists, valid, ready, matching, err)
	}
}

func assertRetentionIndexAbsent(t *testing.T, ctx context.Context, db *sql.DB, repo repository, spec retentionIndex, stage string) {
	t.Helper()
	valid, ready, matching, identified, exists, err := repo.retentionIndexState(ctx, db, spec)
	if err != nil || exists || valid || ready || matching || identified {
		t.Fatalf("retention index %q is present %s when the test needs it gone: exists=%v valid=%v ready=%v matching=%v identified=%v err=%v", spec.name, stage, exists, valid, ready, matching, identified, err)
	}
}

func assertRetentionIndexReady(t *testing.T, ctx context.Context, db *sql.DB, repo repository, spec retentionIndex, stage string) {
	t.Helper()
	valid, ready, matching, identified, exists, err := repo.retentionIndexState(ctx, db, spec)
	if err != nil || !exists || !valid || !ready || !matching || !identified {
		t.Fatalf("retention index %q was not created %s: exists=%v valid=%v ready=%v matching=%v identified=%v err=%v", spec.name, stage, exists, valid, ready, matching, identified, err)
	}
}

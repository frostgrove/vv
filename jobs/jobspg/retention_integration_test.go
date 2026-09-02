//go:build integration

package jobspg

import (
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

func TestPostgresTerminalRetentionIsPolicyAwareBoundedAndPreservesOnceIntents(t *testing.T) {
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
	namespace, err := jobs.NamespaceOf("jobspg-retention-integration", fmt.Sprint(time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	repo := newRepository(DefaultSchema)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := repo.deleteNamespace(cleanupCtx, db, namespace); err != nil {
			t.Errorf("delete test namespace: %v", err)
		}
	})
	shortRetention := 500 * time.Millisecond
	intentRetention := 1200 * time.Millisecond
	short := postgresTestDefinition(t, "jobspg.retention.short", jobs.RetainFor(shortRetention), jobs.RetainIntentsFor(intentRetention))
	once := postgresTestDefinition(t, "jobspg.retention.once", jobs.RetainFor(shortRetention), jobs.RetainIntentsFor(intentRetention))
	long := postgresTestDefinition(t, "jobspg.retention.long", jobs.RetainFor(time.Hour), jobs.RetainIntentsFor(2*time.Hour))
	catalog := jobs.MustCatalog(short, once, long)
	driver, err := Open(ctx, db, namespace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	shortIDs := make([]jobs.InvocationID, 3)
	for index := range shortIDs {
		shortIDs[index], err = jobs.Enqueue(ctx, queue, short, fmt.Sprintf("short-%d", index))
		if err != nil {
			t.Fatal(err)
		}
		adminFinishInvocation(t, ctx, driver, namespace, short, shortIDs[index], byte(index+1))
	}
	onceID, outcome, err := jobs.EnqueueOnce(ctx, queue, once, jobs.Intent("account:42"), "stable")
	if err != nil || outcome != jobs.EnqueueCreated {
		t.Fatalf("enqueue once = (%v, %v)", outcome, err)
	}
	adminFinishInvocation(t, ctx, driver, namespace, once, onceID, 4)
	longID, err := jobs.Enqueue(ctx, queue, long, "long")
	if err != nil {
		t.Fatal(err)
	}
	adminFinishInvocation(t, ctx, driver, namespace, long, longID, 5)
	recordExpiresAt, intentExpiresAt := retentionDeadlinesFor(t, ctx, db, repo, namespace, onceID)
	if delta := intentExpiresAt.Sub(recordExpiresAt); delta < intentRetention-shortRetention-time.Millisecond || delta > intentRetention-shortRetention+time.Millisecond {
		t.Fatalf("retention deadline delta = %v", delta)
	}
	if swept, err := driver.SweepTerminalRetention(ctx, 1); err != nil || swept != 0 {
		t.Fatalf("premature sweep = (%d, %v)", swept, err)
	}
	time.Sleep(time.Until(recordExpiresAt) + 20*time.Millisecond)
	total := 0
	for attempt := 0; attempt < 10; attempt++ {
		swept, err := driver.SweepTerminalRetention(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if swept > 1 {
			t.Fatalf("bounded sweep processed %d rows", swept)
		}
		total += swept
		if swept == 0 {
			break
		}
	}
	if total != 4 {
		t.Fatalf("expired record sweeps = %d, want 4", total)
	}
	for _, id := range shortIDs {
		if _, err := driver.Get(ctx, id); !errors.Is(err, jobs.ErrInvocationNotFound) {
			t.Fatalf("short invocation %v remains visible: %v", id, err)
		}
	}
	if _, err := driver.Get(ctx, onceID); !errors.Is(err, jobs.ErrInvocationNotFound) {
		t.Fatalf("once tombstone remains visible: %v", err)
	}
	if _, err := driver.Get(ctx, longID); err != nil {
		t.Fatalf("long retention was removed: %v", err)
	}
	existingID, outcome, err := jobs.EnqueueOnce(ctx, queue, once, jobs.Intent("account:42"), "stable")
	if err != nil || outcome != jobs.EnqueueExistingSamePayload || existingID != onceID {
		t.Fatalf("retained once intent = (%v, %v, %v), want (%v, %v)", existingID, outcome, err, onceID, jobs.EnqueueExistingSamePayload)
	}
	conflictID, outcome, err := jobs.EnqueueOnce(ctx, queue, once, jobs.Intent("account:42"), "changed")
	if err != nil || outcome != jobs.EnqueueConflict || conflictID != onceID {
		t.Fatalf("retained once conflict = (%v, %v, %v), want (%v, %v)", conflictID, outcome, err, onceID, jobs.EnqueueConflict)
	}
	time.Sleep(time.Until(intentExpiresAt) + 20*time.Millisecond)
	if swept, err := driver.SweepTerminalRetention(ctx, 1); err != nil || swept != 1 {
		t.Fatalf("intent expiry sweep = (%d, %v)", swept, err)
	}
	recreatedID, outcome, err := jobs.EnqueueOnce(ctx, queue, once, jobs.Intent("account:42"), "stable")
	if err != nil || outcome != jobs.EnqueueCreated || recreatedID == onceID {
		t.Fatalf("expired once intent = (%v, %v, %v), old %v", recreatedID, outcome, err, onceID)
	}
	if _, err := db.ExecContext(ctx, `UPDATE `+repo.deliveries+` SET record_expires_at = NULL, intent_expires_at = NULL WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(longID)); err != nil {
		t.Fatal(err)
	}
	if swept, err := driver.SweepTerminalRetention(ctx, 1); err != nil || swept != 1 {
		t.Fatalf("legacy deadline backfill = (%d, %v)", swept, err)
	}
	longRecordDeadline, longIntentDeadline := retentionDeadlinesFor(t, ctx, db, repo, namespace, longID)
	if delta := longIntentDeadline.Sub(longRecordDeadline); delta < time.Hour-time.Millisecond || delta > time.Hour+time.Millisecond {
		t.Fatalf("backfilled policy delta = %v", delta)
	}
	if _, err := driver.Get(ctx, longID); err != nil {
		t.Fatalf("backfill removed unexpired invocation: %v", err)
	}
}

func TestPostgresSchemaTwoUpgradeBackfillsTerminalRetention(t *testing.T) {
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
	marker := fmt.Sprint(time.Now().UnixNano())
	schema := "jobspg_retention_upgrade_" + marker
	namespace, err := jobs.NamespaceOf("jobspg-retention-upgrade", marker)
	if err != nil {
		t.Fatal(err)
	}
	repo := newRepository(schema)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+repo.schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	definition := postgresTestDefinition(t, "jobspg.retention.upgrade")
	catalog := jobs.MustCatalog(definition)
	driver, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema, SchemaManagement: ManageSchema})
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	id, err := jobs.Enqueue(ctx, queue, definition, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	adminFinishInvocation(t, ctx, driver, namespace, definition, id, 11)
	for _, statement := range []string{
		`DROP INDEX ` + repo.schema + `.deliveries_retention_idx`,
		`CREATE INDEX deliveries_retention_idx ON ` + repo.deliveries + ` (namespace, id)`,
		`ALTER TABLE ` + repo.deliveries + ` DROP COLUMN record_expires_at, DROP COLUMN intent_expires_at`,
		`ALTER TABLE ` + repo.deliveries + ` ALTER COLUMN record SET NOT NULL, ALTER COLUMN record_size SET NOT NULL`,
		`UPDATE ` + repo.meta + ` SET version = 2 WHERE singleton = true`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	upgraded, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema, SchemaManagement: ManageSchema})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog, Schema: schema, SchemaManagement: ManageSchema})
	if err != nil {
		t.Fatal(err)
	}
	prepared := make(chan error, 2)
	go func() { prepared <- upgraded.Prepare(ctx) }()
	go func() { prepared <- peer.Prepare(ctx) }()
	for range 2 {
		if err := <-prepared; err != nil {
			t.Fatal(err)
		}
	}
	var validatedConstraints int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE conname IN ('deliveries_record_pair_check', 'deliveries_retention_deadline_pair_check') AND conrelid = $1::regclass AND convalidated`, repo.deliveries).Scan(&validatedConstraints); err != nil || validatedConstraints != 2 {
		t.Fatalf("upgraded retention constraints = (%d, %v)", validatedConstraints, err)
	}
	var version int
	var indexValid, indexReady bool
	if err := db.QueryRowContext(ctx, `SELECT version FROM `+repo.meta+` WHERE singleton`).Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("upgraded schema version = (%d, %v)", version, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT index.indisvalid, index.indisready FROM pg_class AS relation JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace JOIN pg_index AS index ON index.indexrelid = relation.oid WHERE namespace.nspname = $1 AND relation.relname = 'deliveries_retention_idx'`, schema).Scan(&indexValid, &indexReady); err != nil || !indexValid || !indexReady {
		t.Fatalf("upgraded retention index = (%v, %v, %v)", indexValid, indexReady, err)
	}
	valid, ready, matching, identified, exists, err := repo.retentionIndexState(ctx, db, retentionIndexes[0])
	if err != nil || !exists || !valid || !ready || !matching || !identified {
		t.Fatalf("upgraded retention index definition = (%v, %v, %v, %v, %v, %v)", valid, ready, matching, identified, exists, err)
	}
	assertGenericRetentionPlanUsesIndex(t, ctx, db, repo, namespace)
	if _, err := upgraded.Get(ctx, id); err != nil {
		t.Fatalf("upgraded terminal invocation = %v", err)
	}
	if swept, err := upgraded.SweepTerminalRetention(ctx, 1); err != nil || swept != 1 {
		t.Fatalf("upgraded retention backfill = (%d, %v)", swept, err)
	}
	recordExpiresAt, intentExpiresAt := retentionDeadlinesFor(t, ctx, db, repo, namespace, id)
	if !recordExpiresAt.Before(intentExpiresAt) {
		t.Fatalf("upgraded deadlines = (%v, %v)", recordExpiresAt, intentExpiresAt)
	}
	if _, err := upgraded.Get(ctx, id); err != nil {
		t.Fatalf("backfill prematurely removed invocation: %v", err)
	}
	readLock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readLock.ExecContext(ctx, `SELECT 1 FROM `+repo.deliveries+` WHERE false`); err != nil {
		t.Fatal(err)
	}
	recheckCtx, recheckCancel := context.WithTimeout(ctx, time.Second)
	err = peer.Prepare(recheckCtx)
	recheckCancel()
	if err != nil {
		t.Fatalf("prepared v4 schema requested a delivery table write lock: %v", err)
	}
	if err := readLock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX `+repo.schema+`.deliveries_retention_idx`); err != nil {
		t.Fatal(err)
	}
	wrong := `CREATE INDEX deliveries_retention_idx ON ` + repo.deliveries + ` (namespace, (COALESCE(CASE WHEN record IS NULL THEN record_expires_at ELSE intent_expires_at END, '-infinity'::timestamptz)), updated_at DESC, id) WHERE state IN (3)`
	if _, err := db.ExecContext(ctx, wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `COMMENT ON INDEX `+repo.schema+`.deliveries_retention_idx IS '`+retentionIndexes[0].fingerprint()+`'`); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.CheckSchema(ctx); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("CheckSchema accepted wrong retention index: %v", err)
	}
}

func TestPostgresManualMigrationRejectsWrongRetentionIndexBeforeVersionStamp(t *testing.T) {
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
	schema := "jobspg_retention_manual_" + fmt.Sprint(time.Now().UnixNano())
	repo := newRepository(schema)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
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
		if strings.Contains(statement, `CREATE INDEX CONCURRENTLY IF NOT EXISTS "deliveries_retention_idx"`) {
			wrong := `CREATE INDEX deliveries_retention_idx ON ` + repo.deliveries + ` (namespace, (COALESCE(CASE WHEN record IS NULL THEN record_expires_at ELSE intent_expires_at END, '-infinity'::timestamptz)), updated_at DESC, id) WHERE state IN (3)`
			if _, err := db.ExecContext(ctx, wrong); err != nil {
				t.Fatal(err)
			}
			injected = true
		}
		if _, err := db.ExecContext(ctx, statement); err != nil {
			if !strings.Contains(statement, "retention index deliveries_retention_idx schema mismatch") {
				t.Fatalf("manual migration failed before exact validation: %v", err)
			}
			rejected = true
			break
		}
	}
	if !injected || !rejected {
		t.Fatalf("manual migration wrong-index path = (injected %v, rejected %v)", injected, rejected)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM `+repo.meta+` WHERE singleton`).Scan(&version); err != nil || version != 2 {
		t.Fatalf("manual migration version after rejection = (%d, %v)", version, err)
	}
	var fingerprint string
	err = db.QueryRowContext(ctx, `SELECT COALESCE(obj_description(relation.oid, 'pg_class'), '') FROM pg_class AS relation JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace WHERE namespace.nspname = $1 AND relation.relname = 'deliveries_retention_idx'`, schema).Scan(&fingerprint)
	if err != nil || fingerprint == retentionIndexes[0].fingerprint() {
		t.Fatalf("wrong retention index was stamped = (%q, %v)", fingerprint, err)
	}
	if err := repo.migrate(ctx, db); err != nil {
		t.Fatalf("automatic migration did not repair rejected manual index: %v", err)
	}
	valid, ready, matching, identified, exists, err := repo.retentionIndexState(ctx, db, retentionIndexes[0])
	if err != nil || !exists || !valid || !ready || !matching || !identified {
		t.Fatalf("repaired retention index = (%v, %v, %v, %v, %v, %v)", valid, ready, matching, identified, exists, err)
	}
	var beforeOID uint32
	if err := db.QueryRowContext(ctx, `SELECT relation.oid FROM pg_class AS relation JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace WHERE namespace.nspname = $1 AND relation.relname = 'deliveries_retention_idx'`, schema).Scan(&beforeOID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`COMMENT ON INDEX ` + repo.schema + `.deliveries_retention_idx IS NULL`,
		`UPDATE ` + repo.meta + ` SET version = 2 WHERE singleton`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.migrate(ctx, db); err != nil {
		t.Fatalf("automatic migration did not identify exact index: %v", err)
	}
	var afterOID uint32
	if err := db.QueryRowContext(ctx, `SELECT relation.oid FROM pg_class AS relation JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace WHERE namespace.nspname = $1 AND relation.relname = 'deliveries_retention_idx'`, schema).Scan(&afterOID); err != nil {
		t.Fatal(err)
	}
	if afterOID != beforeOID {
		t.Fatalf("exact retention index was rebuilt: before %d, after %d", beforeOID, afterOID)
	}
}

func TestPostgresRetentionMigrationLockCleanup(t *testing.T) {
	dsn := os.Getenv("FROSTGROVE_JOBSPG_TEST_DSN")
	if dsn == "" {
		t.Skip("FROSTGROVE_JOBSPG_TEST_DSN is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	repo := newRepository("jobspg_retention_lock_" + fmt.Sprint(time.Now().UnixNano()))
	marker := errors.New("migration panic")
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = repo.withMigrationLock(ctx, db, func(*sql.Conn) error { panic(marker) })
	}()
	if recovered != marker {
		t.Fatalf("migration panic = %v", recovered)
	}
	assertMigrationLockAvailable(t, ctx, db, repo)
	if err := repo.withMigrationLock(ctx, db, func(*sql.Conn) error { return context.Canceled }); !errors.Is(err, context.Canceled) {
		t.Fatalf("migration work error = %v", err)
	}
	assertMigrationLockAvailable(t, ctx, db, repo)
	holder, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var held bool
	if err := holder.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, retentionMigrationLock(repo.rawSchema)).Scan(&held); err != nil || !held {
		t.Fatalf("hold migration lock = (%v, %v)", held, err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 75*time.Millisecond)
	workCalled := false
	err = repo.withMigrationLock(waitCtx, db, func(*sql.Conn) error {
		workCalled = true
		return nil
	})
	waitCancel()
	if !errors.Is(err, context.DeadlineExceeded) || workCalled {
		t.Fatalf("contended migration lock = (%v, work %v)", err, workCalled)
	}
	var unlocked bool
	if err := holder.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, retentionMigrationLock(repo.rawSchema)).Scan(&unlocked); err != nil || !unlocked {
		t.Fatalf("release migration lock = (%v, %v)", unlocked, err)
	}
	if err := holder.Close(); err != nil {
		t.Fatal(err)
	}
	assertMigrationLockAvailable(t, ctx, db, repo)
}

func assertGenericRetentionPlanUsesIndex(t *testing.T, ctx context.Context, db *sql.DB, repo repository, namespace jobs.Namespace) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`SET LOCAL plan_cache_mode = force_generic_plan`,
		`SET LOCAL enable_seqscan = off`,
		`PREPARE frostgrove_retention_plan(bytea, timestamptz, integer) AS ` + repo.retentionCandidatesQuery(),
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	digest := namespace.Digest()
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`EXPLAIN (FORMAT TEXT) EXECUTE frostgrove_retention_plan(decode('%x', 'hex'), clock_timestamp(), 100)`, digest))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "deliveries_retention_idx") || strings.Contains(plan, "Seq Scan") {
		t.Fatalf("generic retention plan does not use retention index:\n%s", plan)
	}
}

func assertMigrationLockAvailable(t *testing.T, ctx context.Context, db *sql.DB, repo repository) {
	t.Helper()
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := db.Conn(probeCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var locked bool
	if err := conn.QueryRowContext(probeCtx, `SELECT pg_try_advisory_lock($1)`, retentionMigrationLock(repo.rawSchema)).Scan(&locked); err != nil || !locked {
		t.Fatalf("reacquire migration lock = (%v, %v)", locked, err)
	}
	var unlocked bool
	if err := conn.QueryRowContext(probeCtx, `SELECT pg_advisory_unlock($1)`, retentionMigrationLock(repo.rawSchema)).Scan(&unlocked); err != nil || !unlocked {
		t.Fatalf("release probe migration lock = (%v, %v)", unlocked, err)
	}
}

func retentionDeadlinesFor(t *testing.T, ctx context.Context, db *sql.DB, repo repository, namespace jobs.Namespace, id jobs.InvocationID) (time.Time, time.Time) {
	t.Helper()
	var recordExpiresAt, intentExpiresAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT record_expires_at, intent_expires_at FROM `+repo.deliveries+` WHERE namespace = $1 AND id = $2`, namespaceArgument(namespace), invocationArgument(id)).Scan(&recordExpiresAt, &intentExpiresAt); err != nil {
		t.Fatal(err)
	}
	return recordExpiresAt.Round(0).UTC(), intentExpiresAt.Round(0).UTC()
}

//go:build integration

package eventpg

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestAMigrationOverASchemaItDidNotBuildRefusesRatherThanRestampingIt(t *testing.T) {
	name := scratch(t, "eventpg_s1_drift")
	deployed := Schema{Name: name, MaxPayload: 1024}
	if err := migrate(t, deployed); err != nil {
		t.Fatalf("the narrow schema did not deploy, so nothing below was compared against anything: %v", err)
	}
	stamped, err := deployed.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, log := metaRow(t, name)
	if fingerprint != stamped {
		t.Fatalf("the schema was migrated by this build and schema_meta carries %s rather than %s", fingerprint, stamped)
	}

	t.Run("the same build re-runs its own list and changes nothing", func(t *testing.T) {
		if err := migrate(t, deployed); err != nil {
			t.Fatalf("a second run of the same list refused, and a migration step nobody can retry is worse than one that rewrites: %v", err)
		}
		again, sameLog := metaRow(t, name)
		if again != stamped {
			t.Errorf("a re-run of the same build moved the fingerprint from %s to %s", stamped, again)
		}
		if !bytes.Equal(sameLog, log) {
			t.Error("a re-run minted a second log, so every cursor persisted against the first one is now unreadable")
		}
	})

	t.Run("a build at another bound refuses and records nothing", func(t *testing.T) {
		err := migrate(t, Schema{Name: name})
		if err == nil {
			t.Fatal("a build whose expectation is not the deployed one ran its whole list without objecting, so schema_meta was made to agree with a schema this run did not build")
		}
		var refusal *pgconn.PgError
		if !errors.As(err, &refusal) || refusal.Code != schemaMismatchState {
			t.Errorf("the re-run failed with %v, and a refusal nobody can tell from a syntax error is one a caller would have to classify by message text", err)
		} else {
			wanted, err := (Schema{Name: name}).Fingerprint()
			if err != nil {
				t.Fatal(err)
			}
			for _, named := range []string{stamped, wanted} {
				if !strings.Contains(refusal.Message+refusal.Detail, named) {
					t.Errorf("the refusal is %q / %q and does not name %s, so an operator is told two schemas differ and not which two", refusal.Message, refusal.Detail, named)
				}
			}
		}
		after, _ := metaRow(t, name)
		if after != stamped {
			t.Errorf("schema_meta.fingerprint is %s and the deployed schema is still the one %s describes, so the recorded fact is false", after, stamped)
		}
		definition := constraintDefinition(t, name, "events", "events_payload_check")
		if !strings.Contains(definition, "1024") {
			t.Errorf("the deployed payload bound is %q, so this test compared the wrong schema", definition)
		}
	})
}

func TestARefusedMigrationLeavesTheTablesItCreatedRolledBack(t *testing.T) {
	name := scratch(t, "eventpg_s1_rollback")
	deployed := Schema{Name: name, MaxPayload: 1024}
	if err := migrate(t, deployed); err != nil {
		t.Fatalf("the narrow schema did not deploy: %v", err)
	}
	if _, err := liveDB(t).Exec("DROP TABLE " + quoteIdentifier(name) + ".events"); err != nil {
		t.Fatalf("the events table could not be dropped, so the half-built schema this test needs was never built: %v", err)
	}
	if err := migrate(t, Schema{Name: name}); err == nil {
		t.Fatal("a build at another bound rebuilt the missing table and stamped its own fingerprint over a schema whose remaining tables it did not build")
	}
	var exists bool
	row := liveDB(t).QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = 'events')`, name)
	if err := row.Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("the refused run left the events table it created behind, so the schema is now half this build's and half another's")
	}
}

func TestASchemaNamedForAReservedWordDeploys(t *testing.T) {
	for _, name := range []string{"user", "table", "select", "order", "group", "all", "default", "do"} {
		t.Run(name, func(t *testing.T) {
			scratch(t, name)
			if err := migrate(t, Schema{Name: name}); err != nil {
				t.Fatalf("a schema named %q resolved, fingerprinted and answered eleven statements, and PostgreSQL refused them: %v", name, err)
			}
			expected, err := (Schema{Name: name}).Fingerprint()
			if err != nil {
				t.Fatal(err)
			}
			if fingerprint, _ := metaRow(t, name); fingerprint != expected {
				t.Errorf("the schema deployed but records %s rather than %s", fingerprint, expected)
			}
		})
	}

	t.Run("a name the rule refuses is still refused", func(t *testing.T) {
		for _, refused := range []string{"User", "1events", "events; DROP TABLE x", `events"`} {
			if _, err := MigrationStatements(Schema{Name: refused}); err == nil {
				t.Errorf("%q answered a migration, so quoting the identifier was read as permission to take any name", refused)
			}
		}
	})
}

// The two halves of the advisory lock key as pg_locks holds them: classid is the
// high 32 bits and objid the low ones.
func lockHalves(schema string) (int64, int64) {
	key := migrationLock(schema)
	return int64(uint64(key) >> 32), int64(uint32(key))
}

func advisoryHolders(t *testing.T, schema string) []int64 {
	t.Helper()
	classid, objid := lockHalves(schema)
	rows, err := liveDB(t).Query(`SELECT pid FROM pg_locks
		WHERE locktype = 'advisory' AND granted AND classid::bigint = $1 AND objid::bigint = $2
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`, classid, objid)
	if err != nil {
		t.Fatalf("pg_locks could not be read, so nothing about the migration lock was observed: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var held []int64
	for rows.Next() {
		var pid int64
		if err := rows.Scan(&pid); err != nil {
			t.Fatal(err)
		}
		held = append(held, pid)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return held
}

func waitFor(t *testing.T, what string, until func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if until() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s did not happen inside twenty seconds, so what this test measures never occurred", what)
}

func TestTwoConcurrentPreparesTakeOneLockOnOneBackend(t *testing.T) {
	for pass := range 5 {
		schema := Schema{Name: scratch(t, "eventpg_s2_race_"+strconv.Itoa(pass))}
		first := openStore(t, schema, ManageSchema)
		second := openStore(t, schema, ManageSchema)

		start := make(chan struct{})
		answers := make(chan error, 2)
		for _, store := range []*Store{first, second} {
			go func() {
				<-start
				answers <- store.Prepare(context.Background())
			}()
		}
		close(start)
		for range 2 {
			if err := <-answers; err != nil {
				t.Fatalf("pass %d: two replicas migrating one schema at once left one of them with %v — a 23505 on pg_type_typname_nsp_index is what an unpinned lock produces, intermittently", pass, err)
			}
		}

		var rows int
		if err := liveDB(t).QueryRow(`SELECT count(*) FROM ` + quoteIdentifier(schema.Name) + `.schema_meta`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("pass %d: schema_meta holds %d rows, so both replicas minted a log and every cursor names one of two", pass, rows)
		}
		if held := advisoryHolders(t, schema.Name); len(held) != 0 {
			t.Fatalf("pass %d: the migration lock is still held by %v after both callers returned", pass, held)
		}
	}

	t.Run("the lock, the DDL and the unlock are one backend", func(t *testing.T) {
		schema := deployed(t, "eventpg_s2_pinned")
		ctx := context.Background()
		blocker, err := liveDB(t).Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Close() }()
		holding, err := blocker.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = holding.Rollback() }()
		if _, err := holding.ExecContext(ctx, "LOCK TABLE "+quoteIdentifier(schema.Name)+".events IN ACCESS SHARE MODE"); err != nil {
			t.Fatalf("the events table could not be locked, so the migration below would not have been held anywhere observable: %v", err)
		}

		store := openStore(t, schema, ManageSchema)
		migrating := make(chan error, 1)
		go func() { migrating <- store.Migrate(ctx) }()

		var holder []int64
		waitFor(t, "the migration taking its advisory lock", func() bool {
			holder = advisoryHolders(t, schema.Name)
			return len(holder) > 0
		})
		if len(holder) != 1 {
			t.Fatalf("%d backends hold the migration lock at once, and one of them is migrating while the others think they are", len(holder))
		}

		var waiting []int64
		waitFor(t, "the migration's DDL waiting for the table this test holds", func() bool {
			waiting = relationWaiters(t, schema)
			return len(waiting) > 0
		})
		if len(waiting) != 1 || waiting[0] != holder[0] {
			t.Fatalf("the advisory lock is held by %v and the DDL is issued by %v: a lock on one pooled connection and a CREATE TRIGGER on another serialise nothing", holder, waiting)
		}

		if err := holding.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := <-migrating; err != nil {
			t.Fatalf("the migration that waited for the table lock failed: %v", err)
		}
		if held := advisoryHolders(t, schema.Name); len(held) != 0 {
			t.Fatalf("the migration lock is still held by %v, so the unlock ran on a connection that did not own it", held)
		}
	})
}

func relationWaiters(t *testing.T, schema Schema) []int64 {
	t.Helper()
	rows, err := liveDB(t).Query(`SELECT pid FROM pg_locks WHERE locktype = 'relation' AND NOT granted
		AND relation = $1::regclass`, quoteIdentifier(schema.Name)+".events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var held []int64
	for rows.Next() {
		var pid int64
		if err := rows.Scan(&pid); err != nil {
			t.Fatal(err)
		}
		held = append(held, pid)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return held
}

func TestASecondMigrationBlocksAgainstAHeldLock(t *testing.T) {
	schema := Schema{Name: scratch(t, "eventpg_s2_held")}
	ctx := context.Background()
	owner, err := liveDB(t).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	var owned bool
	if err := owner.QueryRowContext(ctx, `SELECT pg_advisory_lock($1) IS NULL`, migrationLock(schema.Name)).Scan(&owned); err != nil {
		t.Fatalf("this test could not take the migration lock, so nothing below waited for anything: %v", err)
	}
	var pid int64
	if err := owner.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}

	store := openStore(t, schema, ManageSchema)
	migrating := make(chan error, 1)
	go func() { migrating <- store.Prepare(ctx) }()

	select {
	case err := <-migrating:
		t.Fatalf("a migration ran straight through a lock another session holds, and two replicas therefore race CREATE TABLE: %v", err)
	case <-time.After(1500 * time.Millisecond):
	}
	if schemaExists(t, schema.Name) {
		t.Error("the blocked migration created the schema anyway, so it was never waiting for the lock at all")
	}
	if held := advisoryHolders(t, schema.Name); len(held) != 1 || held[0] != pid {
		t.Errorf("the migration lock is held by %v and this test's connection is %d", held, pid)
	}

	var released bool
	if err := owner.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, migrationLock(schema.Name)).Scan(&released); err != nil || !released {
		t.Fatalf("this test could not release the lock it took: %v", err)
	}
	select {
	case err := <-migrating:
		if err != nil {
			t.Fatalf("the migration resumed and failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the migration never resumed after the lock was released, so a store that waits for it waits forever")
	}
	if !schemaExists(t, schema.Name) {
		t.Error("the migration reported success and the schema is not there")
	}

	t.Run("nobody waits when nobody holds it", func(t *testing.T) {
		free := Schema{Name: scratch(t, "eventpg_s2_free")}
		started := time.Now()
		if err := openStore(t, free, ManageSchema).Prepare(context.Background()); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
			t.Fatalf("an uncontended migration took %s, which is the window the case above reads as waiting", elapsed)
		}
	})
}

// The version-1 list, rendered from the model this build still carries so a case
// can deploy the schema an earlier build deployed. Version 1 stamps nothing:
// there is no version to migrate from.
func versionOneStatements(t *testing.T, schema Schema) []string {
	t.Helper()
	resolved, err := schema.Resolved()
	if err != nil {
		t.Fatalf("%+v does not resolve: %v", schema, err)
	}
	print, err := resolved.fingerprintAt(1)
	if err != nil {
		t.Fatalf("%+v has no version-1 fingerprint: %v", schema, err)
	}
	return migrationStatements(expected(resolved, 1), print, "")
}

func deployVersionOne(t *testing.T, schema Schema) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tx, err := liveDB(t).BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("the version-1 migration could not open a transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	for index, statement := range versionOneStatements(t, schema) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatalf("statement %d of the version-1 list: %v", index+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("the version-1 migration could not commit: %v", err)
	}
}

func metaVersion(t *testing.T, schema string) int {
	t.Helper()
	var version int
	row := liveDB(t).QueryRow("SELECT version FROM " + quoteIdentifier(schema) + ".schema_meta WHERE singleton")
	if err := row.Scan(&version); err != nil {
		t.Fatalf("%s.schema_meta holds no version this test could read: %v", schema, err)
	}
	return version
}

func tableExists(t *testing.T, schema, table string) bool {
	t.Helper()
	var exists bool
	row := liveDB(t).QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2)`, schema, table)
	if err := row.Scan(&exists); err != nil {
		t.Fatalf("whether %s.%s exists could not be read: %v", schema, table, err)
	}
	return exists
}

// The migration this schema version is for: a deployed version 1 with history in
// it, transformed by the one list, verified at all three levels afterwards, and
// every event read back byte for byte. The log is asserted unmoved too, because
// a migration that reissued it would orphan every cursor a projection persisted.
func TestAVersionOneSchemaWithRowsMigratesToVersionTwoAndReadsBackUnchanged(t *testing.T) {
	name := scratch(t, "eventpg_s2_v1_rows")
	schema := Schema{Name: name}
	deployVersionOne(t, schema)
	if metaVersion(t, name) != 1 {
		t.Fatalf("the schema this case deploys is at version %d and not 1, so nothing below is a migration", metaVersion(t, name))
	}
	if tableExists(t, name, checkpointsTable) {
		t.Fatal("the version-1 list built a checkpoints table, so the version this case migrates from is not version 1")
	}
	before, log := metaRow(t, name)

	stream := aStream("orders.order", "acme/A-17")
	mustExecute(t, "INSERT INTO "+quoteIdentifier(name)+".streams (family, key, version) VALUES ($1, $2, 3)",
		stream.Family, string(stream.Key))
	for version := 1; version <= 3; version++ {
		mustExecute(t, "INSERT INTO "+quoteIdentifier(name)+
			".events (family, key, version, type, revision, payload, recorded_at) VALUES ($1, $2, $3, $4, 1, $5, now())",
			stream.Family, string(stream.Key), version, "orders.order.held", []byte("payload "+strconv.Itoa(version)))
	}
	held := stored(t, schema, stream)
	if len(held) != 3 {
		t.Fatalf("the version-1 schema holds %d events where this case wrote three", len(held))
	}

	if err := migrate(t, schema); err != nil {
		t.Fatalf("a version-1 schema at this build's own bounds did not migrate to version 2: %v", err)
	}
	if version := metaVersion(t, name); version != SchemaVersion {
		t.Fatalf("the migrated schema is at version %d and this build is version %d", version, SchemaVersion)
	}
	after, sameLog := metaRow(t, name)
	described, err := schema.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if after != described {
		t.Errorf("the migrated schema records %s where this build describes %s", after, described)
	}
	if after == before {
		t.Error("the migration left the version-1 fingerprint in place, so a schema at version 2 records the description of another one")
	}
	if !bytes.Equal(sameLog, log) {
		t.Error("the migration minted a second log, so every cursor a projection persisted against the first is now foreign")
	}
	if !tableExists(t, name, checkpointsTable) {
		t.Fatal("the migration stamped version 2 and built no checkpoints table")
	}

	store := prepared(t, schema)
	migrated := storedOn(t, liveDB(t), schema, stream)
	if len(migrated) != len(held) {
		t.Fatalf("the migrated schema holds %d events where the version-1 one held %d", len(migrated), len(held))
	}
	for index := range held {
		before, after := held[index], migrated[index]
		if after.position != before.position || after.version != before.version || after.name != before.name ||
			after.revision != before.revision || !bytes.Equal(after.payload, before.payload) || !after.recordedAt.Equal(before.recordedAt) {
			t.Errorf("event %d reads back as %+v where version 1 held %+v", index+1, after, before)
		}
	}
	page, err := store.ReadStream(t.Context(), stream, 0)
	if err != nil {
		t.Fatalf("reading the migrated stream through a verified store answered %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("a verified store reads %d events off the migrated stream", len(page))
	}

	t.Run("a checkpoint store over the migrated schema records against it", func(t *testing.T) {
		pool := checkpointPool(t, 4)
		checkpoints := preparedCheckpoints(t, pool, schema, VerifySchema)
		saveOne(t, context.WithoutCancel(t.Context()), checkpoints,
			event.Checkpoint{Projection: "orders.v1", Cursor: "vve1-migrated", Advance: 1, Progress: event.Progress{At: instant()}})
		if row := loadOne(t, context.WithoutCancel(t.Context()), checkpoints, "orders.v1"); row.Advance != 1 {
			t.Fatalf("a save against the migrated schema left the row at advance %d", row.Advance)
		}
	})
}

// D1's whole point. Guarded on the version alone, this build's default-bounds
// list meeting a version-1 schema deployed at another bound would create the
// fourth table, stamp its own version and fingerprint, and then pass its own
// assertion — §UC-074's silent restamping, reached by a list that looks correct.
func TestAVersionOneSchemaAtOtherBoundsIsNotRestamped(t *testing.T) {
	name := scratch(t, "eventpg_s2_v1_other_bounds")
	deployed := Schema{Name: name, MaxPayload: 1024}
	deployVersionOne(t, deployed)
	stamped, log := metaRow(t, name)

	err := migrate(t, Schema{Name: name})
	if err == nil {
		t.Fatal("a build at another bound ran its whole list over a version-1 schema it did not build, so schema_meta was made to agree with a schema this run did not deploy")
	}
	var refusal *pgconn.PgError
	if !errors.As(err, &refusal) || refusal.Code != schemaMismatchState {
		t.Errorf("the refusal is %v, and one nobody can tell from a syntax error is one a caller would have to classify by message text", err)
	}
	if version := metaVersion(t, name); version != 1 {
		t.Errorf("the refused migration left the schema at version %d, so a version-1 schema at other bounds was restamped", version)
	}
	after, sameLog := metaRow(t, name)
	if after != stamped {
		t.Errorf("schema_meta.fingerprint is %s where the deployed schema is the one %s describes, so the recorded fact is false", after, stamped)
	}
	if !bytes.Equal(sameLog, log) {
		t.Error("the refused migration minted a second log")
	}
	if tableExists(t, name, checkpointsTable) {
		t.Error("the refused migration left the checkpoints table it created behind, so the schema is now half this build's and half another's")
	}

	t.Run("the same schema at its own bounds migrates", func(t *testing.T) {
		if err := migrate(t, deployed); err != nil {
			t.Fatalf("a version-1 schema at the bounds it was deployed at did not migrate: %v", err)
		}
		if version := metaVersion(t, name); version != SchemaVersion {
			t.Fatalf("the schema is at version %d after its own build migrated it", version)
		}
		if err := openStore(t, deployed, VerifySchema).Prepare(t.Context()); err != nil {
			t.Fatalf("the migrated schema does not verify at all three levels: %v", err)
		}
	})
}

// One list, two jobs: a fresh database takes exactly what a migrated version-1
// schema took, and the two are the same schema afterwards — which is what makes
// the version-1 path a migration rather than a second product.
func TestAFreshDatabaseTakesTheSameVersionTwoList(t *testing.T) {
	fresh := Schema{Name: scratch(t, "eventpg_s2_v2_fresh")}
	if err := migrate(t, fresh); err != nil {
		t.Fatalf("a fresh database did not take the version-2 list: %v", err)
	}
	if version := metaVersion(t, fresh.Name); version != SchemaVersion {
		t.Fatalf("a fresh database is at version %d after the version-2 list", version)
	}
	if err := openStore(t, fresh, VerifySchema).Prepare(t.Context()); err != nil {
		t.Fatalf("a freshly migrated schema does not verify at all three levels: %v", err)
	}

	migrated := Schema{Name: scratch(t, "eventpg_s2_v2_migrated")}
	deployVersionOne(t, migrated)
	if err := migrate(t, migrated); err != nil {
		t.Fatalf("a version-1 schema did not take the version-2 list: %v", err)
	}
	for _, held := range []Schema{fresh, migrated} {
		described, err := held.Fingerprint()
		if err != nil {
			t.Fatal(err)
		}
		if recorded, _ := metaRow(t, held.Name); recorded != described {
			t.Errorf("%q records %s where this build describes %s", held.Name, recorded, described)
		}
		if version := metaVersion(t, held.Name); version != SchemaVersion {
			t.Errorf("%q is at version %d", held.Name, version)
		}
	}
	if apart := listsApart(t, fresh, migrated); apart != "" {
		t.Fatalf("the list a fresh database took and the one a version-1 schema took differ beyond the schema name: %s", apart)
	}
	for _, table := range []string{metaTable, streamsTable, eventsTable, checkpointsTable} {
		if !tableExists(t, fresh.Name, table) || !tableExists(t, migrated.Name, table) {
			t.Errorf("%s is not in both schemas, so the two paths do not produce one schema", table)
		}
	}
	if err := openStore(t, migrated, VerifySchema).Prepare(t.Context()); err != nil {
		t.Errorf("the migrated schema does not verify at all three levels: %v", err)
	}
}

// The two lists with the schema name and the two fingerprints taken out, so what
// is compared is the list rather than the three literals every statement of it
// carries — a schema's name is a fingerprint input, so two names are two
// fingerprints and the lists could never be byte-identical.
func listsApart(t *testing.T, first, second Schema) string {
	t.Helper()
	rendered := make([]string, 2)
	for index, schema := range []Schema{first, second} {
		statements, err := MigrationStatements(schema)
		if err != nil {
			t.Fatal(err)
		}
		print2, err := schema.Fingerprint()
		if err != nil {
			t.Fatal(err)
		}
		print1, err := schema.fingerprintAt(1)
		if err != nil {
			t.Fatal(err)
		}
		held := strings.ReplaceAll(listing(statements), quoteIdentifier(schema.Name), quoteIdentifier("@"))
		held = strings.ReplaceAll(held, print2, "@v2")
		rendered[index] = strings.ReplaceAll(held, print1, "@v1")
	}
	if rendered[0] == rendered[1] {
		return ""
	}
	return "the two renderings are " + strconv.Itoa(len(rendered[0])) + " and " + strconv.Itoa(len(rendered[1])) + " bytes"
}

func TestTwoConcurrentPreparesTakeOneLockAtTheNewVersion(t *testing.T) {
	for pass := range 3 {
		schema := Schema{Name: scratch(t, "eventpg_s2_v2_race_"+strconv.Itoa(pass))}
		first := openStore(t, schema, ManageSchema)
		second := openStore(t, schema, ManageSchema)

		start := make(chan struct{})
		answers := make(chan error, 2)
		for _, store := range []*Store{first, second} {
			go func() {
				<-start
				answers <- store.Prepare(context.Background())
			}()
		}
		close(start)
		for range 2 {
			if err := <-answers; err != nil {
				t.Fatalf("pass %d: two replicas migrating one schema to version 2 at once left one of them with %v", pass, err)
			}
		}

		var rows int
		if err := liveDB(t).QueryRow(`SELECT count(*) FROM ` + quoteIdentifier(schema.Name) + `.schema_meta`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("pass %d: schema_meta holds %d rows, so both replicas minted a log and every cursor names one of two", pass, rows)
		}
		if version := metaVersion(t, schema.Name); version != SchemaVersion {
			t.Fatalf("pass %d: the raced schema is at version %d", pass, version)
		}
		if held := advisoryHolders(t, schema.Name); len(held) != 0 {
			t.Fatalf("pass %d: the migration lock is still held by %v after both callers returned", pass, held)
		}
	}
}

// The in-process pair the fourth table creates: a store and a checkpoint store
// are two resources over one schema, and both may be asked to manage it. The
// advisory lock is per schema, so they serialise on it and the second finds the
// list idempotent and the assertion satisfied.
func TestAStoreAndACheckpointsManagingOneSchemaInOneProcessSerialiseOnTheLock(t *testing.T) {
	for pass := range 3 {
		schema := Schema{Name: scratch(t, "eventpg_s2_pair_"+strconv.Itoa(pass))}
		pool := checkpointPool(t, 6)
		store, err := New(Spec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema, SchemaManagement: ManageSchema})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		checkpoints, err := NewCheckpoints(CheckpointSpec{DB: pool, Source: crudsql.Postgres(pool), Schema: schema, SchemaManagement: ManageSchema})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = checkpoints.Close() })

		start := make(chan struct{})
		answers := make(chan error, 2)
		go func() { <-start; answers <- store.Prepare(context.Background()) }()
		go func() { <-start; answers <- checkpoints.Prepare(context.Background()) }()
		close(start)
		for range 2 {
			if err := <-answers; err != nil {
				t.Fatalf("pass %d: a store and a checkpoint store managing one schema at once left one of them with %v", pass, err)
			}
		}

		var rows int
		if err := liveDB(t).QueryRow(`SELECT count(*) FROM ` + quoteIdentifier(schema.Name) + `.schema_meta`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("pass %d: schema_meta holds %d rows after the pair migrated one schema", pass, rows)
		}
		if held := advisoryHolders(t, schema.Name); len(held) != 0 {
			t.Fatalf("pass %d: the migration lock is still held by %v after both callers returned", pass, held)
		}
		if err := store.Check(context.Background()); err != nil {
			t.Errorf("pass %d: the store is not ready after the pair prepared: %v", pass, err)
		}
		if err := checkpoints.Check(context.Background()); err != nil {
			t.Errorf("pass %d: the checkpoint store is not ready after the pair prepared: %v", pass, err)
		}
	}
}

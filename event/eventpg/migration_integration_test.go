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

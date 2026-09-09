package eventpg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/event"
)

// The SQLSTATE the assertion raises. PostgreSQL defines no code for "this
// schema was built by another expectation", and P0001 is every plpgsql RAISE, so
// a caller that has to tell this refusal from any other would be left comparing
// message text.
const schemaMismatchState = "EVPG1"

// The thirteen statements, rendered from the same expectation the fingerprint
// digests. The one that mints the log takes no argument from this process, so a
// re-run of an already-migrated schema issues no second log and every persisted
// cursor stays readable.
//
// The stamping UPDATE is guarded on the deployed fingerprint and not on the
// version alone, and that is the whole of what makes one list safe for both
// jobs. Version 2 adds a table and alters none, so the CREATE … IF NOT EXISTS
// list already transforms a deployed version 1 at THESE bounds into it, and the
// UPDATE records that. Guarded on the version alone, the same list meeting a
// version-1 schema deployed at other bounds would create the fourth table, stamp
// its own fingerprint over a schema it did not build, and then pass its own
// assertion. Guarded on the fingerprint the UPDATE matches nothing, the
// assertion raises, and the whole transaction rolls back.
//
// The assertion is last and records nothing: a list may record a description
// only of a schema it can produce.
func migrationStatements(model expectation, fingerprint, previous string) []string {
	schema := quoteIdentifier(model.schema.Name)
	statements := []string{"CREATE SCHEMA IF NOT EXISTS " + schema}
	for _, table := range model.tables {
		statements = append(statements, createTable(schema, table))
	}
	for _, function := range model.functions {
		statements = append(statements, createFunction(schema, function))
	}
	for _, table := range model.tables {
		for _, trigger := range table.triggers {
			statements = append(statements, createTrigger(schema, table.name, trigger))
		}
	}
	quoted := quoteLiteral(fingerprint)
	version := strconv.Itoa(model.version)
	statements = append(statements,
		"INSERT INTO "+schema+"."+metaTable+" (singleton, version, log, fingerprint)\n"+
			"VALUES (true, "+version+", decode(replace(gen_random_uuid()::text, '-', ''), 'hex'), "+quoted+")\n"+
			"ON CONFLICT (singleton) DO NOTHING")
	if model.version > 1 {
		statements = append(statements, stampMeta(schema, model.version, fingerprint, previous))
	}
	return append(statements, assertMeta(schema, version, fingerprint))
}

func stampMeta(schema string, version int, fingerprint, previous string) string {
	return "UPDATE " + schema + "." + metaTable + "\n" +
		"   SET version = " + strconv.Itoa(version) + ", fingerprint = " + quoteLiteral(fingerprint) + "\n" +
		" WHERE singleton AND version = " + strconv.Itoa(version-1) + " AND fingerprint = " + quoteLiteral(previous)
}

func assertMeta(schema, version, fingerprint string) string {
	return "DO $eventpg$\n\tDECLARE\n\t\tdeployed record;\n\tBEGIN\n" +
		"\t\tSELECT version, fingerprint INTO deployed FROM " + schema + "." + metaTable + " WHERE singleton;\n" +
		"\t\tIF deployed.version IS DISTINCT FROM " + version +
		" OR deployed.fingerprint IS DISTINCT FROM " + quoteLiteral(fingerprint) + " THEN\n" +
		"\t\t\tRAISE EXCEPTION USING\n" +
		"\t\t\t\tERRCODE = " + quoteLiteral(schemaMismatchState) + ",\n" +
		"\t\t\t\tMESSAGE = format('eventpg: this schema is version %s at %s', deployed.version, deployed.fingerprint),\n" +
		"\t\t\t\tDETAIL = " + quoteLiteral("eventpg: this migration builds version "+version+" at "+fingerprint) + ",\n" +
		"\t\t\t\tHINT = " + quoteLiteral("eventpg: this list creates what is absent and stamps only the version and bounds it can transform, so it cannot make this schema the one it describes") + ";\n" +
		"\t\tEND IF;\n" +
		"\tEND\n$eventpg$"
}

func createTable(schema string, table expectedTable) string {
	var parts []string
	for _, column := range table.columns {
		parts = append(parts, column.definition())
	}
	parts = append(parts, "CONSTRAINT "+table.primaryKeyName()+" PRIMARY KEY ("+strings.Join(table.pk, ", ")+")")
	for _, unique := range table.uniques {
		parts = append(parts, "CONSTRAINT "+unique.name+" UNIQUE ("+strings.Join(unique.columns, ", ")+")")
	}
	for _, foreign := range table.foreign {
		parts = append(parts, "CONSTRAINT "+foreign.name+" FOREIGN KEY ("+strings.Join(foreign.columns, ", ")+
			") REFERENCES "+schema+"."+foreign.table+" ("+strings.Join(foreign.references, ", ")+")")
	}
	for _, check := range table.checks {
		parts = append(parts, "CONSTRAINT "+check.name+" "+check.definition())
	}
	return "CREATE TABLE IF NOT EXISTS " + schema + "." + table.name + " (\n\t" +
		strings.Join(parts, ",\n\t") + "\n)"
}

func (this expectedColumn) definition() string {
	written := this.name + " " + this.dataType
	if this.identity.present() {
		written += " " + this.identity.clause()
	}
	if this.notNull {
		written += " NOT NULL"
	}
	if this.byDefault != "" {
		written += " DEFAULT " + this.byDefault
	}
	return written
}

func (this expectedIdentity) clause() string {
	cycle := "NO CYCLE"
	if this.cycle {
		cycle = "CYCLE"
	}
	return "GENERATED " + strings.ToUpper(this.generation) + " AS IDENTITY (INCREMENT " +
		strconv.Itoa(this.increment) + " CACHE " + strconv.Itoa(this.cache) + " " + cycle + ")"
}

func createFunction(schema string, function expectedFunction) string {
	return "CREATE OR REPLACE FUNCTION " + schema + "." + function.name + "() RETURNS trigger\n" +
		"LANGUAGE plpgsql AS $eventpg$\n\tBEGIN\n\t\t" +
		strings.Join(function.body, "\n\t\t") + "\n\tEND\n$eventpg$"
}

func createTrigger(schema, table string, trigger expectedTrigger) string {
	return "DO $eventpg$\n\tBEGIN\n" +
		"\t\tDROP TRIGGER IF EXISTS " + trigger.name + " ON " + schema + "." + table + ";\n" +
		"\t\tCREATE TRIGGER " + trigger.name + " " + trigger.timing + " " + trigger.events +
		" ON " + schema + "." + table + "\n" +
		"\t\t\tFOR EACH " + trigger.level + " EXECUTE FUNCTION " + schema + "." + trigger.function + "();\n" +
		"\tEND\n$eventpg$"
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// One lock per deployed schema, so two schemas in one database migrate at once
// and two replicas of one schema do not.
func migrationLock(schema string) int64 {
	digest := sha256.Sum256([]byte("frostgrove.event.postgres.migration.v1\x00" + schema))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

// The one door in this package that opens a transaction, and the only file that
// names BeginTx, Commit or Rollback. It is outside the eight store methods, it
// runs before the store serves anything, and the transaction is its own and
// never a caller's.
func (this *Store) Migrate(ctx context.Context) error {
	if this.management != ManageSchema {
		return fmt.Errorf("%w: this store is %s, and a schema is created only where a deployment asked for it with %s",
			ErrSpec, this.management, ManageSchema)
	}
	if this.closed.Load() {
		return event.ErrClosed
	}
	return migrateSchema(ctx, this.db, this.schema)
}

// The store and the checkpoint store over one schema are two resources at one
// schema version, so the deployment work is a function of the schema rather than
// a method of either. The advisory lock is per schema, so two of them managing
// one schema in one process serialise on it and the second finds the list
// idempotent and the assertion satisfied.
func migrateSchema(ctx context.Context, db *sql.DB, schema Schema) error {
	statements, err := MigrationStatements(schema)
	if err != nil {
		return err
	}
	return withMigrationLock(ctx, db, schema, func(conn *sql.Conn) error {
		return migrateOn(ctx, conn, schema, statements)
	})
}

// Everything on one pinned connection, because a session-level advisory lock
// taken over a *sql.DB serialises nothing: the pool hands the lock, the DDL and
// the unlock to three different connections, the unlock answers false, and two
// replicas race CREATE TABLE into a 23505 on pg_type_typname_nsp_index. An
// unlock that answers false is therefore an error and the connection is
// discarded rather than returned to the pool holding a lock nobody will release.
func withMigrationLock(ctx context.Context, db *sql.DB, schema Schema, work func(*sql.Conn) error) (resultErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("eventpg: migrating %q could not check a connection out of the pool: %w", schema.Name, err)
	}
	locked := false
	discard := false
	defer func() {
		if locked {
			unlocking, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			var unlocked bool
			err := conn.QueryRowContext(unlocking, `SELECT pg_advisory_unlock($1)`, migrationLock(schema.Name)).Scan(&unlocked)
			cancel()
			if err == nil && !unlocked {
				err = errors.New("this session did not hold it, so the lock never serialised anything")
			}
			if err != nil {
				discard = true
				resultErr = errors.Join(resultErr, fmt.Errorf("eventpg: releasing the migration lock on %q: %w", schema.Name, err))
			}
		}
		if discard {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		resultErr = errors.Join(resultErr, conn.Close())
	}()
	for !locked {
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, migrationLock(schema.Name)).Scan(&locked); err != nil {
			discard = true
			return fmt.Errorf("eventpg: taking the migration lock on %q: %w", schema.Name, err)
		}
		if locked {
			break
		}
		waiting := time.NewTimer(250*time.Millisecond + time.Duration(rand.Int64N(int64(250*time.Millisecond))))
		select {
		case <-ctx.Done():
			waiting.Stop()
			return ctx.Err()
		case <-waiting.C:
		}
	}
	return work(conn)
}

func migrateOn(ctx context.Context, conn *sql.Conn, schema Schema, statements []string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("eventpg: migrating %q could not open its transaction: %w", schema.Name, err)
	}
	defer func() { _ = tx.Rollback() }()
	for index, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return migrationFailure(schema, index, len(statements), err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("eventpg: migrating %q could not commit: %w", schema.Name, err)
	}
	return nil
}

func migrationFailure(schema Schema, index, count int, err error) error {
	if fault := sqlfault.Extract(err); fault != nil && fault.SQLState == schemaMismatchState {
		fingerprint, failed := schema.Fingerprint()
		if failed != nil {
			return failed
		}
		return fmt.Errorf("%w: %q is not the schema this build describes at %s, and version %d creates what is absent and stamps only the version and bounds it can transform, so this list cannot make it one: %w",
			ErrSchemaMismatch, schema.Name, fingerprint, SchemaVersion, err)
	}
	return fmt.Errorf("eventpg: statement %d of %d migrating %q: %w", index+1, count, schema.Name, err)
}

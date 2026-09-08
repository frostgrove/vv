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

// The SQLSTATE statement eleven raises. PostgreSQL defines no code for "this
// schema was built by another expectation", and P0001 is every plpgsql RAISE, so
// a caller that has to tell this refusal from any other would be left comparing
// message text.
const schemaMismatchState = "EVPG1"

// The eleven statements, rendered from the same expectation the fingerprint
// digests. Statement ten mints the log in the database and takes no argument
// from this process, so a re-run of an already-migrated schema issues no second
// log and every persisted cursor stays readable.
//
// Statement eleven records nothing, and the rule is general: a list may record a
// description only of a schema it can produce. Version 1 creates what is absent
// and replaces the two functions and the three triggers; it alters no table, so
// against a schema another expectation built it changes nothing and writing this
// build's fingerprint would record a fact that is false. It asserts instead, and
// the raise rolls the whole transaction back. A version N carrying the ALTERs
// that transform N-1 into N does produce the new description; its last statement
// is an UPDATE guarded on the version it migrates from, then this assertion.
func migrationStatements(model expectation, fingerprint string) []string {
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
	version := strconv.Itoa(SchemaVersion)
	return append(statements,
		"INSERT INTO "+schema+"."+metaTable+" (singleton, version, log, fingerprint)\n"+
			"VALUES (true, "+version+", decode(replace(gen_random_uuid()::text, '-', ''), 'hex'), "+quoted+")\n"+
			"ON CONFLICT (singleton) DO NOTHING",
		assertMeta(schema, version, fingerprint),
	)
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
		"\t\t\t\tHINT = " + quoteLiteral("eventpg: version "+version+" creates and never alters, so it cannot make this schema the one it describes") + ";\n" +
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
	statements, err := MigrationStatements(this.schema)
	if err != nil {
		return err
	}
	return this.withMigrationLock(ctx, func(conn *sql.Conn) error {
		return this.migrateOn(ctx, conn, statements)
	})
}

// Everything on one pinned connection, because a session-level advisory lock
// taken over a *sql.DB serialises nothing: the pool hands the lock, the DDL and
// the unlock to three different connections, the unlock answers false, and two
// replicas race CREATE TABLE into a 23505 on pg_type_typname_nsp_index. An
// unlock that answers false is therefore an error and the connection is
// discarded rather than returned to the pool holding a lock nobody will release.
func (this *Store) withMigrationLock(ctx context.Context, work func(*sql.Conn) error) (resultErr error) {
	conn, err := this.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("eventpg: migrating %q could not check a connection out of the pool: %w", this.schema.Name, err)
	}
	locked := false
	discard := false
	defer func() {
		if locked {
			unlocking, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			var unlocked bool
			err := conn.QueryRowContext(unlocking, `SELECT pg_advisory_unlock($1)`, migrationLock(this.schema.Name)).Scan(&unlocked)
			cancel()
			if err == nil && !unlocked {
				err = errors.New("this session did not hold it, so the lock never serialised anything")
			}
			if err != nil {
				discard = true
				resultErr = errors.Join(resultErr, fmt.Errorf("eventpg: releasing the migration lock on %q: %w", this.schema.Name, err))
			}
		}
		if discard {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		resultErr = errors.Join(resultErr, conn.Close())
	}()
	for !locked {
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, migrationLock(this.schema.Name)).Scan(&locked); err != nil {
			discard = true
			return fmt.Errorf("eventpg: taking the migration lock on %q: %w", this.schema.Name, err)
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

func (this *Store) migrateOn(ctx context.Context, conn *sql.Conn, statements []string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("eventpg: migrating %q could not open its transaction: %w", this.schema.Name, err)
	}
	defer func() { _ = tx.Rollback() }()
	for index, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return this.migrationFailure(index, len(statements), err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("eventpg: migrating %q could not commit: %w", this.schema.Name, err)
	}
	return nil
}

func (this *Store) migrationFailure(index, count int, err error) error {
	if fault := sqlfault.Extract(err); fault != nil && fault.SQLState == schemaMismatchState {
		fingerprint, failed := this.schema.Fingerprint()
		if failed != nil {
			return failed
		}
		return fmt.Errorf("%w: %q is not the schema this build describes at %s, and version %d creates and never alters, so this list cannot make it one: %w",
			ErrSchemaMismatch, this.schema.Name, fingerprint, SchemaVersion, err)
	}
	return fmt.Errorf("eventpg: statement %d of %d migrating %q: %w", index+1, count, this.schema.Name, err)
}

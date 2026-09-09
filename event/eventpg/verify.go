package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/event"
)

const (
	logBytes = 16

	undefinedTableState = "42P01"
	invalidSchemaState  = "3F000"
)

// What a verified store carries: the log the deployed schema minted, which is
// what a cursor is bound to. A nil pointer is a store that has not verified, so
// readiness and the log are one fact and cannot be read half-set.
type readiness struct {
	log [logBytes]byte
}

func (this *Store) Prepare(ctx context.Context) error {
	this.state.Store(nil)
	if this.management == ManageSchema {
		if err := this.Migrate(ctx); err != nil {
			return err
		}
	}
	return this.Verify(ctx)
}

// Three levels, in order, failing closed at the first: the version integer, the
// fingerprint, then the catalog itself. Nothing here writes, under either
// schema management, and the store is not ready until all three passed.
func (this *Store) Verify(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.ErrClosed
	}
	this.state.Store(nil)
	held, err := verifySchema(ctx, this.db, this.schema)
	if err != nil {
		return err
	}
	this.state.Store(&held)
	return nil
}

// The three levels a resource over this schema passes before it serves
// anything, and they are a function of the schema rather than a method of the
// store: a Store and a Checkpoints over one schema are two resources at one
// schema version, and each verifies at its own Prepare.
func verifySchema(ctx context.Context, db *sql.DB, schema Schema) (readiness, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return readiness{}, fmt.Errorf("eventpg: verifying %q could not check a connection out of the pool: %w", schema.Name, err)
	}
	defer func() { _ = conn.Close() }()
	log, err := readMeta(ctx, conn, schema)
	if err != nil {
		return readiness{}, err
	}
	if err := inspect(ctx, conn, schema); err != nil {
		return readiness{}, err
	}
	return readiness{log: log}, nil
}

// Levels 1 and 2 and no more: level 3 is eight statements over pg_catalog and a
// readiness probe runs on the deployment's own schedule. The log is compared
// too, because a schema dropped and migrated again under a running store
// fingerprints identically and every cursor this store handed out is stale.
func (this *Store) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.ErrClosed
	}
	held := this.state.Load()
	if held == nil {
		return ErrNotReady
	}
	return checkSchema(ctx, this.db, this.schema, *held)
}

func checkSchema(ctx context.Context, db *sql.DB, schema Schema, held readiness) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("eventpg: checking %q could not check a connection out of the pool: %w", schema.Name, err)
	}
	defer func() { _ = conn.Close() }()
	log, err := readMeta(ctx, conn, schema)
	if err != nil {
		return err
	}
	if log != held.log {
		return fmt.Errorf("%w: %q names a log this resource did not verify, so the schema was migrated again under it and every cursor it minted is foreign", ErrSchemaMismatch, schema.Name)
	}
	return nil
}

func readMeta(ctx context.Context, on *sql.Conn, schema Schema) ([logBytes]byte, error) {
	var held [logBytes]byte
	var version int32
	var log []byte
	var fingerprint string
	row := on.QueryRowContext(ctx, `SELECT version, log, fingerprint FROM `+
		quoteIdentifier(schema.Name)+`.`+metaTable+` WHERE singleton`)
	switch err := row.Scan(&version, &log, &fingerprint); {
	case errors.Is(err, sql.ErrNoRows):
		return held, fmt.Errorf("%w: %s.%s holds no row, so this schema was never migrated to its end",
			ErrSchemaMismatch, schema.Name, metaTable)
	case err != nil:
		return held, metaFailure(schema, err)
	}
	if version != SchemaVersion {
		return held, fmt.Errorf("%w: %q is schema version %d and this build is version %d",
			ErrSchemaMismatch, schema.Name, version, SchemaVersion)
	}
	described, err := schema.Fingerprint()
	if err != nil {
		return held, err
	}
	if fingerprint != described {
		return held, fmt.Errorf("%w: %q was migrated at %s and this build describes %s",
			ErrSchemaMismatch, schema.Name, fingerprint, described)
	}
	if len(log) != logBytes {
		return held, fmt.Errorf("%w: %q names a log of %d bytes and a log is %d",
			ErrSchemaMismatch, schema.Name, len(log), logBytes)
	}
	copy(held[:], log)
	return held, nil
}

func metaFailure(schema Schema, err error) error {
	if fault := sqlfault.Extract(err); fault != nil {
		switch fault.SQLState {
		case undefinedTableState, invalidSchemaState:
			return fmt.Errorf("%w: %s.%s is not there, so nothing this store expects was ever deployed here: %w",
				ErrSchemaMismatch, schema.Name, metaTable, err)
		}
	}
	return fmt.Errorf("eventpg: reading %s.%s: %w", schema.Name, metaTable, err)
}

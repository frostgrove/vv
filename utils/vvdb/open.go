package vvdb

import (
	"database/sql"
	"fmt"
)

// Open builds the DSN and opens a *sql.DB with the pool already sized.
//
// The driver is not imported here — it is the consumer's blank import, and
// that is what keeps this package free of dependencies:
//
//	import _ "github.com/jackc/pgx/v5/stdlib"   // or go-sql-driver/mysql
//
//	sqlDB, err := vvdb.Open(&cfg.DB)
//
// sql.Open does not connect, so a wrong driver name fails here and a wrong
// password fails at the first query. Call PingContext if start-up should be
// the place that finds out.
func Open(c *Config) (*sql.DB, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: config", ErrMissing)
	}
	if c.Replica != nil {
		return nil, fmt.Errorf("%w: replica is declared; use OpenReadWrite so it is not silently ignored", ErrConflict)
	}
	return open(c)
}

// open opens exactly one already-selected database configuration. Keeping it
// private makes Open's single-handle contract explicit while OpenReadWrite can
// still open the validated primary and replica independently.
func open(c *Config) (*sql.DB, error) {
	dsn, err := DSN(c)
	if err != nil {
		return nil, err
	}
	name := DriverName(c)
	if name == "" {
		return nil, fmt.Errorf("%w: %q has no default driver name — set driver", ErrEngine, c.Engine)
	}
	database, err := sql.Open(name, dsn)
	if err != nil {
		// The DSN is deliberately not in the message: it carries the password.
		return nil, fmt.Errorf("vvdb: opening %s with driver %q: %w", c.Engine, name, err)
	}
	c.Pool.apply(database)
	return database, nil
}

// MustOpen is Open for a main function, and panics rather than returning.
//
// A configuration that is wrong should stop the process at start-up rather than
// surface once traffic arrives ([[D-021]]). Nothing else in this package
// panics.
func MustOpen(c *Config) *sql.DB {
	database, err := Open(c)
	if err != nil {
		panic(err)
	}
	return database
}

// OpenReadWrite opens the primary and, when the config declares one, the
// replica. The second result is nil when it does not.
//
// The pair is handed to crud.ReadWrite, which is what decides that a read goes
// to the replica and a write, a locked read and the load half of an update do
// not:
//
//	primary, replica, err := vvdb.OpenReadWrite(&cfg.DB)
//	src := crudsql.Postgres(primary)
//	if replica != nil {
//	    src = crud.ReadWrite(src, crudsql.Postgres(replica))
//	}
func OpenReadWrite(c *Config) (primary, replica *sql.DB, err error) {
	if c == nil {
		return nil, nil, fmt.Errorf("%w: config", ErrMissing)
	}
	if err := c.Validate(); err != nil {
		return nil, nil, err
	}
	primaryCfg := *c
	primaryCfg.Replica = nil
	primary, err = open(&primaryCfg)
	if err != nil {
		return nil, nil, err
	}
	r, ok := c.ReadReplica()
	if !ok {
		return primary, nil, nil
	}
	replica, err = open(r)
	if err != nil {
		_ = primary.Close()
		return nil, nil, fmt.Errorf("replica: %w", err)
	}
	return primary, replica, nil
}

// MustOpenReadWrite is OpenReadWrite for a main function. It returns the
// primary and optional replica together so a declarative replica cannot be
// accidentally ignored by a single-handle convenience call.
func MustOpenReadWrite(c *Config) (primary, replica *sql.DB) {
	primary, replica, err := OpenReadWrite(c)
	if err != nil {
		panic(err)
	}
	return primary, replica
}

// Apply sizes a database/sql handle the application opened itself. It is the
// companion to Open for gorm, an instrumented connector or an IAM driver: the
// configuration remains the single declaration even when vvdb does not create
// the handle.
func (this *Pool) Apply(database *sql.DB) error {
	if this == nil {
		return fmt.Errorf("%w: pool", ErrMissing)
	}
	if database == nil {
		return fmt.Errorf("%w: database handle", ErrMissing)
	}
	if err := this.Validate(); err != nil {
		return err
	}
	this.apply(database)
	return nil
}

// apply sizes the pool after its configuration has been validated. A zero is
// "leave database/sql's own default alone" rather than "no connections", which
// is what setting it would mean.
func (this *Pool) apply(database *sql.DB) {
	if this.MaxOpen > 0 {
		database.SetMaxOpenConns(this.MaxOpen)
	}
	if this.MaxIdle != 0 {
		database.SetMaxIdleConns(this.MaxIdle)
	}
	if this.MaxLifetime > 0 {
		database.SetConnMaxLifetime(this.MaxLifetime)
	}
	if this.MaxIdleTime > 0 {
		database.SetConnMaxIdleTime(this.MaxIdleTime)
	}
}

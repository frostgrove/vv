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
//	import _ "github.com/jackc/pgx/v5/stdlib"   // or lib/pq, or go-sql-driver/mysql
//
//	sqlDB, err := vvdb.Open(cfg.DB)
//
// sql.Open does not connect, so a wrong driver name fails here and a wrong
// password fails at the first query. Call PingContext if start-up should be
// the place that finds out.
func Open(c Config) (*sql.DB, error) {
	dsn, err := DSN(c)
	if err != nil {
		return nil, err
	}
	name := DriverName(c)
	if name == "" {
		return nil, fmt.Errorf("%w: %q has no default driver name — set driver", ErrEngine, c.Engine)
	}
	db, err := sql.Open(name, dsn)
	if err != nil {
		// The DSN is deliberately not in the message: it carries the password.
		return nil, fmt.Errorf("vvdb: opening %s with driver %q: %w", c.Engine, name, err)
	}
	c.Pool.apply(db)
	return db, nil
}

// MustOpen is Open for a main function, and panics rather than returning.
//
// A configuration that is wrong should stop the process at start-up rather than
// surface once traffic arrives ([[D-021]]). Nothing else in this package
// panics.
func MustOpen(c Config) *sql.DB {
	db, err := Open(c)
	if err != nil {
		panic(err)
	}
	return db
}

// OpenReadWrite opens the primary and, when the config declares one, the
// replica. The second result is nil when it does not.
//
// The pair is handed to crud.ReadWrite, which is what decides that a read goes
// to the replica and a write, a locked read and the load half of an update do
// not:
//
//	primary, replica, err := vvdb.OpenReadWrite(cfg.DB)
//	src := crudsql.Postgres(primary)
//	if replica != nil {
//	    src = crud.ReadWrite(src, crudsql.Postgres(replica))
//	}
func OpenReadWrite(c Config) (primary, replica *sql.DB, err error) {
	primary, err = Open(c)
	if err != nil {
		return nil, nil, err
	}
	r, ok := c.ReadReplica()
	if !ok {
		return primary, nil, nil
	}
	replica, err = Open(r)
	if err != nil {
		_ = primary.Close()
		return nil, nil, fmt.Errorf("replica: %w", err)
	}
	return primary, replica, nil
}

// apply sizes the pool. A zero is "leave database/sql's own default alone"
// rather than "no connections", which is what setting it would mean.
func (p Pool) apply(db *sql.DB) {
	if p.MaxOpen > 0 {
		db.SetMaxOpenConns(p.MaxOpen)
	}
	if p.MaxIdle > 0 {
		db.SetMaxIdleConns(p.MaxIdle)
	}
	if p.MaxLifetime > 0 {
		db.SetConnMaxLifetime(p.MaxLifetime)
	}
	if p.MaxIdleTime > 0 {
		db.SetConnMaxIdleTime(p.MaxIdleTime)
	}
}

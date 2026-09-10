package vvdb

import (
	"database/sql"
	"fmt"
)

func Open(c *Config) (*sql.DB, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: config", ErrMissing)
	}
	if c.Replica != nil {
		return nil, fmt.Errorf("%w: replica is declared; use OpenReadWrite so it is not silently ignored", ErrConflict)
	}
	return open(c)
}

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
		return nil, fmt.Errorf("vvdb: opening %s with driver %q: %w", c.Engine, name,
			RedactError("driver rejected the connection configuration", err))
	}
	c.Pool.apply(database)
	return database, nil
}

func MustOpen(c *Config) *sql.DB {
	database, err := Open(c)
	if err != nil {
		panic(err)
	}
	return database
}

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

func MustOpenReadWrite(c *Config) (primary, replica *sql.DB) {
	primary, replica, err := OpenReadWrite(c)
	if err != nil {
		panic(err)
	}
	return primary, replica
}

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

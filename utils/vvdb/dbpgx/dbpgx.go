// Package dbpgx opens a pgx pool from a vvdb.Config.
//
// It is a module of its own because pgxpool is not database/sql: everything
// vvdb can do with the standard library it does there, and a consumer on
// database/sql, ent or gorm never takes pgx for it ([[D-033]], [[D-051]]).
//
//	pool := dbpgx.MustConnect(ctx, cfg.DB)
//	defer pool.Close()
//	repo := Products.Bind(crudpgx.Open(pool))
//
// The second line is the whole relationship between this package and vv: the
// application opened the pool and handed it over. Nothing here imports crud,
// and the pool's lifetime stays the caller's ([[D-057]]).
package dbpgx

import (
	"context"
	"fmt"
	"math"

	"github.com/frostgrove/vv/utils/vvdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// An Option reaches the pgx configuration after vvdb's fields have been
// applied. It is the escape hatch for what one config cannot describe for four
// engines — a tracer, an AfterConnect hook, a custom type map.
type Option func(*pgxpool.Config)

// Connect builds the connection string, applies the pool settings and verifies
// that the pool can reach the server. pgxpool.NewWithConfig is lazy, so the
// explicit Ping is what makes Connect live up to its name instead of returning
// a handle that fails on its first real query.
func Connect(ctx context.Context, c vvdb.Config, opts ...Option) (*pgxpool.Pool, error) {
	if c.Replica != nil {
		return nil, fmt.Errorf("%w: replica is declared; use ConnectReadWrite so it is not silently ignored", vvdb.ErrConflict)
	}
	return connect(ctx, c, opts...)
}

func connect(ctx context.Context, c vvdb.Config, opts ...Option) (*pgxpool.Pool, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.Pool.MaxOpen > math.MaxInt32 {
		return nil, fmt.Errorf("%w: pool.max_open %d exceeds pgx's int32 limit", vvdb.ErrUnsupported, c.Pool.MaxOpen)
	}
	if c.Pool.MaxIdle > math.MaxInt32 {
		return nil, fmt.Errorf("%w: pool.max_idle %d exceeds pgx's int32 limit", vvdb.ErrUnsupported, c.Pool.MaxIdle)
	}
	if c.Engine != vvdb.Postgres {
		return nil, fmt.Errorf("%w: dbpgx speaks to postgres and the config says %q", vvdb.ErrEngine, c.Engine)
	}
	dsn, err := vvdb.PostgresDSN(c)
	if err != nil {
		return nil, err
	}
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// The string is not in the message: it carries the password.
		return nil, fmt.Errorf("dbpgx: the connection string vvdb built was refused by pgx: %w", err)
	}
	if err := Apply(pc, c.Pool); err != nil {
		return nil, err
	}
	for _, o := range opts {
		if o != nil {
			o(pc)
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("dbpgx: connecting to %s: %w", c.Name, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("dbpgx: connecting to %s: %w", c.Name, err)
	}
	return pool, nil
}

// MustConnect is Connect for a main function, and panics rather than
// returning. A configuration that is wrong should stop the process at start-up
// ([[D-021]]).
func MustConnect(ctx context.Context, c vvdb.Config, opts ...Option) *pgxpool.Pool {
	pool, err := Connect(ctx, c, opts...)
	if err != nil {
		panic(err)
	}
	return pool
}

// ConnectReadWrite opens the primary and, when the config declares one, the
// replica. The second result is nil when it does not; the pair is what
// crud.ReadWrite takes.
func ConnectReadWrite(ctx context.Context, c vvdb.Config, opts ...Option) (primary, replica *pgxpool.Pool, err error) {
	if err := c.Validate(); err != nil {
		return nil, nil, err
	}
	primaryCfg := c
	primaryCfg.Replica = nil
	primary, err = connect(ctx, primaryCfg, opts...)
	if err != nil {
		return nil, nil, err
	}
	r, ok := c.ReadReplica()
	if !ok {
		return primary, nil, nil
	}
	replica, err = connect(ctx, r, opts...)
	if err != nil {
		primary.Close()
		return nil, nil, fmt.Errorf("replica: %w", err)
	}
	return primary, replica, nil
}

// MustConnectReadWrite is ConnectReadWrite for a main function.
func MustConnectReadWrite(ctx context.Context, c vvdb.Config, opts ...Option) (primary, replica *pgxpool.Pool) {
	primary, replica, err := ConnectReadWrite(ctx, c, opts...)
	if err != nil {
		panic(err)
	}
	return primary, replica
}

// Apply maps the portable pool settings onto a parsed pgx configuration. It is
// for applications that own their own pgx construction but still want one
// vvdb.Config to size every handle.
func Apply(pc *pgxpool.Config, p vvdb.Pool) error {
	if pc == nil {
		return fmt.Errorf("%w: pgx pool configuration", vvdb.ErrMissing)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if p.MaxIdle < 0 {
		return fmt.Errorf("%w: pool.max_idle cannot be negative for pgx; it has no max-idle equivalent", vvdb.ErrUnsupported)
	}
	if p.MaxOpen > math.MaxInt32 || p.MaxIdle > math.MaxInt32 {
		return fmt.Errorf("%w: pool limit exceeds pgx's int32 limit", vvdb.ErrUnsupported)
	}
	// Apply may be used with a parsed pgxpool.Config whose MaxConns came from
	// pgx rather than p.MaxOpen. Validate against that effective ceiling before
	// writing MinConns: pgx calls it a minimum, but it still cannot exceed the
	// total number of connections it may open.
	effectiveMax := pc.MaxConns
	if p.MaxOpen > 0 {
		effectiveMax = int32(p.MaxOpen)
	}
	if p.MaxIdle > 0 && effectiveMax > 0 && int64(p.MaxIdle) > int64(effectiveMax) {
		return fmt.Errorf("%w: pool.max_idle %d exceeds effective pgx max connections %d", vvdb.ErrConflict, p.MaxIdle, effectiveMax)
	}
	apply(pc, p)
	return nil
}

// apply maps the four portable limits onto pgx's names. A zero is left alone:
// pgx's own default for MaxConns is four connections or the number of CPUs,
// and writing a 0 over it would be a pool that cannot open anything.
func apply(pc *pgxpool.Config, p vvdb.Pool) {
	if p.MaxOpen > 0 {
		pc.MaxConns = int32(p.MaxOpen)
	}
	if p.MaxIdle > 0 {
		// pgx keeps a floor rather than a ceiling on idle connections, which is
		// the closest thing it has to database/sql's MaxIdleConns. It is not
		// the same promise, and saying so here is cheaper than a reader
		// discovering it from a graph.
		pc.MinConns = int32(p.MaxIdle)
	}
	if p.MaxLifetime > 0 {
		pc.MaxConnLifetime = p.MaxLifetime
	}
	if p.MaxIdleTime > 0 {
		pc.MaxConnIdleTime = p.MaxIdleTime
	}
	if p.ConnectTimeout > 0 {
		pc.ConnConfig.ConnectTimeout = p.ConnectTimeout
	}
}

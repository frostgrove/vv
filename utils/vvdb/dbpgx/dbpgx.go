package dbpgx

import (
	"context"
	"fmt"
	"math"

	"github.com/frostgrove/vv/utils/vvdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Option func(*pgxpool.Config)

type ReadWriteOption func(*readWriteOptions)

type readWriteOptions struct {
	common  []Option
	primary []Option
	replica []Option
}

func Common(options ...Option) ReadWriteOption {
	copyOf := append([]Option(nil), options...)
	return func(o *readWriteOptions) { o.common = append(o.common, copyOf...) }
}

func Primary(options ...Option) ReadWriteOption {
	copyOf := append([]Option(nil), options...)
	return func(o *readWriteOptions) { o.primary = append(o.primary, copyOf...) }
}

func Replica(options ...Option) ReadWriteOption {
	copyOf := append([]Option(nil), options...)
	return func(o *readWriteOptions) { o.replica = append(o.replica, copyOf...) }
}

func splitReadWriteOptions(options ...ReadWriteOption) (primary, replica []Option) {
	var o readWriteOptions
	for _, option := range options {
		if option != nil {
			option(&o)
		}
	}
	primary = append(append([]Option(nil), o.common...), o.primary...)
	replica = append(append([]Option(nil), o.common...), o.replica...)
	return primary, replica
}

func Connect(ctx context.Context, c *vvdb.Config, options ...Option) (*pgxpool.Pool, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: config", vvdb.ErrMissing)
	}
	if c.Replica != nil {
		return nil, fmt.Errorf("%w: replica is declared; use ConnectReadWrite so it is not silently ignored", vvdb.ErrConflict)
	}
	return connect(ctx, c, options...)
}

func connect(ctx context.Context, c *vvdb.Config, options ...Option) (*pgxpool.Pool, error) {
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
		return nil, vvdb.RedactError("dbpgx: pgx rejected the connection configuration", err)
	}
	if err := Apply(pc, &c.Pool); err != nil {
		return nil, err
	}
	for _, o := range options {
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

func MustConnect(ctx context.Context, c *vvdb.Config, options ...Option) *pgxpool.Pool {
	pool, err := Connect(ctx, c, options...)
	if err != nil {
		panic(err)
	}
	return pool
}

func ConnectReadWrite(ctx context.Context, c *vvdb.Config, options ...ReadWriteOption) (primary, replica *pgxpool.Pool, err error) {
	if c == nil {
		return nil, nil, fmt.Errorf("%w: config", vvdb.ErrMissing)
	}
	if err := c.Validate(); err != nil {
		return nil, nil, err
	}
	primaryCfg := *c
	primaryCfg.Replica = nil
	primaryOptions, replicaOptions := splitReadWriteOptions(options...)
	primary, err = connect(ctx, &primaryCfg, primaryOptions...)
	if err != nil {
		return nil, nil, err
	}
	r, ok := c.ReadReplica()
	if !ok {
		return primary, nil, nil
	}
	replica, err = connect(ctx, r, replicaOptions...)
	if err != nil {
		primary.Close()
		return nil, nil, fmt.Errorf("replica: %w", err)
	}
	return primary, replica, nil
}

func MustConnectReadWrite(ctx context.Context, c *vvdb.Config, options ...ReadWriteOption) (primary, replica *pgxpool.Pool) {
	primary, replica, err := ConnectReadWrite(ctx, c, options...)
	if err != nil {
		panic(err)
	}
	return primary, replica
}

func Apply(pc *pgxpool.Config, p *vvdb.Pool) error {
	if pc == nil {
		return fmt.Errorf("%w: pgx pool configuration", vvdb.ErrMissing)
	}
	if p == nil {
		return fmt.Errorf("%w: pool", vvdb.ErrMissing)
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

func apply(pc *pgxpool.Config, p *vvdb.Pool) {
	if p.MaxOpen > 0 {
		pc.MaxConns = int32(p.MaxOpen)
	}
	if p.MaxIdle > 0 {
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

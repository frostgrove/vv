package locksql

import (
	"context"
	"database/sql"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/vvdb/lock"
)

// In wraps a database/sql transaction as the source a lock.Locks needs, for a driver that holds a
// *sql.Tx rather than a crud.Source. The engine is PostgreSQL because that is the only engine
// lock.For serves; a driver on another one is refused there rather than here.
func In(tx *sql.Tx, policy lock.Policy) (*lock.Locks, crud.Executor, error) {
	executor := crudsql.From(tx, crudsql.WithTransaction())
	locks, err := lock.For(crudsql.Source(tx, crud.Postgres{}, crudsql.WithTransaction()), policy)
	if err != nil {
		return nil, nil, err
	}
	return locks, executor, nil
}

// Take holds every guard for the rest of tx, in lock.Locks' one deterministic order.
func Take(ctx context.Context, tx *sql.Tx, policy lock.Policy, guards ...lock.Guard) error {
	locks, executor, err := In(tx, policy)
	if err != nil {
		return err
	}
	return locks.Take(ctx, executor, guards...)
}

// TryTake answers whether the guard was free, without waiting for it.
func TryTake(ctx context.Context, tx *sql.Tx, guard lock.Guard) (bool, error) {
	locks, executor, err := In(tx, lock.Policy{})
	if err != nil {
		return false, err
	}
	return locks.TryTake(ctx, executor, guard)
}

package eventpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event"
)

var errAmbientNotTransaction = errors.New("eventpg: this context carries an executor of this store's data source that is not a transaction, and an operation through it would write beside the caller's own work rather than inside it")

// A closed store answers exactly what an open one would and reports closure
// nowhere here: this issues nothing and mints nothing, and what the context
// carries did not change when the store was closed.
func (this *Store) Transaction(ctx context.Context) (event.Authority, error) {
	tx, err := this.bound(ctx)
	if err != nil || tx == nil {
		return event.Authority{}, err
	}
	return event.NewAuthority(this.backing, tx)
}

// The order every operating door opens in, and it is one order rather than
// three: a cancellation travels as itself, a closed store says so, a store that
// has not verified its schema refuses as policy, and an ambient executor that is
// not a transaction refuses before a statement is built. Only then is anything
// issued. The readiness it answers is the log every cursor of this store is
// bound to.
func (this *Store) opened(ctx context.Context) (*readiness, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if this.closed.Load() {
		return nil, event.Failure(event.Closed, nil)
	}
	held := this.state.Load()
	if held == nil {
		return nil, event.Failure(event.Refused, ErrNotReady)
	}
	if _, err := this.Transaction(ctx); err != nil {
		return nil, event.Failure(event.Refused, err)
	}
	return held, nil
}

func (this *Store) bound(ctx context.Context) (*sql.Tx, error) { return boundTx(ctx, this.source) }

func boundTx(ctx context.Context, source crud.Source) (*sql.Tx, error) {
	held, found := crud.ExecutorFor(ctx, source)
	if !found {
		return nil, nil
	}
	tx, taken := crudsql.Transaction(held)
	if !taken {
		return nil, errAmbientNotTransaction
	}
	return tx, nil
}

type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type run struct {
	on     executor
	joined bool
}

// *sql.Tx and *sql.Conn are the only two database/sql values that execute a
// statement exactly once. (*sql.DB).ExecContext retries a driver.ErrBadConn on
// two cached connections and then once more on a fresh one — three executions of
// one append, measured, on exactly the path where the outcome is uncertain — so
// nothing here ever calls a statement method on the pool. A bound transaction is
// used as it stands and closed by nobody; everything else runs on a connection
// this call checked out and returns. The callback is what keeps that connection
// alive for the length of a *sql.Rows scan and makes a read of several
// statements one checkout rather than several.
//
// A checkout failure is not uncertainty: use was never called, so nothing
// reached a server, and that is proof the write did not land.
func (this *Store) onExecutor(ctx context.Context, use func(run) error) error {
	return onExecutor(ctx, this.db, this.source, this.schema.Name, use)
}

func onExecutor(ctx context.Context, db *sql.DB, source crud.Source, name string, use func(run) error) error {
	tx, err := boundTx(ctx, source)
	if err != nil {
		return err
	}
	if tx != nil {
		return use(run{on: tx, joined: true})
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("eventpg: no connection to %q was checked out of the pool, so nothing was issued: %w", name, err)
	}
	defer func() { _ = conn.Close() }()
	return use(run{on: conn})
}

package crudsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/frostgrove/vv/crud"
)

type topLevelDriver struct{}

func (topLevelDriver) Open(string) (driver.Conn, error) { return topLevelConn{}, nil }

type topLevelConnector struct{}

func (topLevelConnector) Connect(context.Context) (driver.Conn, error) { return topLevelConn{}, nil }
func (topLevelConnector) Driver() driver.Driver                        { return topLevelDriver{} }

type topLevelConn struct{}

func (topLevelConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (topLevelConn) Close() error                        { return nil }
func (topLevelConn) Begin() (driver.Tx, error)           { return topLevelDriverTx{}, nil }
func (topLevelConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return topLevelDriverTx{}, nil
}
func (topLevelConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

type topLevelDriverTx struct{}

func (topLevelDriverTx) Commit() error   { return nil }
func (topLevelDriverTx) Rollback() error { return nil }

type topLevelExecutorWrapper struct{ crud.Executor }

func (this topLevelExecutorWrapper) UnwrapExecutor() crud.Executor { return this.Executor }

func TestTopLevelTransactionAcceptsOnlyTheDirectFrameworkRoot(t *testing.T) {
	database := sql.OpenDB(topLevelConnector{})
	defer database.Close()
	source := Open(database, crud.Postgres{})

	executor, err := source.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := executor.(*Tx)
	defer root.Rollback(context.Background())

	transaction, ok := TopLevelTransaction(root)
	if !ok || transaction != root.tx {
		t.Fatalf("TopLevelTransaction = (%p, %v), want the direct root %p", transaction, ok, root.tx)
	}
	if transaction, ok := Transaction(root); !ok || transaction != root.tx {
		t.Fatalf("legacy Transaction stopped recognizing the direct root: (%p, %v)", transaction, ok)
	}
}

func TestTopLevelTransactionRejectsLaunderedAndNestedHandles(t *testing.T) {
	database := sql.OpenDB(topLevelConnector{})
	defer database.Close()
	source := Open(database, crud.Postgres{})
	executor, err := source.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := executor.(*Tx)
	defer root.Rollback(context.Background())

	nested, err := root.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tampered := &Tx{Executor: From(new(sql.Tx), WithTransaction()), tx: root.tx}
	shallowCopy := &Tx{Executor: root.Executor, tx: root.tx, provenance: root.provenance}
	sameRawReplacement := &Tx{Executor: root.Executor, tx: root.tx}

	cases := map[string]crud.Executor{
		"typed nil root":       (*Tx)(nil),
		"framework savepoint":  nested,
		"raw From":             From(root.tx),
		"raw explicit From":    From(root.tx, WithTransaction()),
		"declared wrapper":     topLevelExecutorWrapper{Executor: root},
		"tampered executor":    tampered,
		"shallow root copy":    shallowCopy,
		"same raw replacement": sameRawReplacement,
		"database pool":        source,
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			transaction, ok := TopLevelTransaction(candidate)
			if ok || transaction != nil {
				t.Fatalf("TopLevelTransaction = (%p, %v), want refusal", transaction, ok)
			}
		})
	}

	if transaction, ok := Transaction(nested); !ok || transaction != root.tx {
		t.Fatalf("legacy Transaction stopped normalizing savepoints: (%p, %v)", transaction, ok)
	}
	if transaction, ok := Transaction(From(root.tx)); !ok || transaction != root.tx {
		t.Fatalf("legacy Transaction stopped normalizing From(raw): (%p, %v)", transaction, ok)
	}
	if transaction, ok := Transaction(topLevelExecutorWrapper{Executor: root}); !ok || transaction != root.tx {
		t.Fatalf("legacy Transaction stopped walking declared wrappers: (%p, %v)", transaction, ok)
	}
}

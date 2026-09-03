package crudsqlfx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/utils/vvdb"
)

// pinger is a database/sql connector that answers a ping with the context it was
// handed and counts how many arrived. It exists because the property under test
// is WHEN the pool is contacted, which no real engine makes observable.
type pinger struct{ pings atomic.Int64 }

func (this *pinger) Connect(context.Context) (driver.Conn, error) {
	return &pingerConn{owner: this}, nil
}
func (this *pinger) Driver() driver.Driver { return nil }

type pingerConn struct{ owner *pinger }

func (pingerConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not a real connection")
}
func (pingerConn) Close() error              { return nil }
func (pingerConn) Begin() (driver.Tx, error) { return nil, errors.New("not a real connection") }

func (this pingerConn) Ping(ctx context.Context) error {
	if this.owner != nil {
		this.owner.pings.Add(1)
	}
	return ctx.Err()
}

// openAnything is the registered driver behind the pool the first test opens: it
// hands back a connection without touching a file, so what that test observes is
// the hooks Open registered and nothing an engine did.
type openAnything struct{}

func (openAnything) Open(string) (driver.Conn, error) { return pingerConn{}, nil }

func init() { sql.Register("crudsqlfx-nothing", openAnything{}) }

type recordedHooks struct{ hooks []fx.Hook }

func (this *recordedHooks) Append(hook fx.Hook) { this.hooks = append(this.hooks, hook) }

func TestOpeningThePoolContactsNothing(t *testing.T) {
	lifecycle := &recordedHooks{}

	database, err := Open(lifecycle, &vvdb.Config{
		Engine: vvdb.SQLite, Driver: "crudsqlfx-nothing", Path: ":memory:",
	})
	if err != nil {
		t.Fatalf("opening the pool: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	for _, hook := range lifecycle.hooks {
		if hook.OnStart != nil {
			t.Fatal("opening the pool registered a start hook of its own; the connection check belongs to the module, which is the only place that knows the source was asked for")
		}
	}
	if len(lifecycle.hooks) != 1 || lifecycle.hooks[0].OnStop == nil {
		t.Fatalf("the pool did not register exactly one stop hook, so it is either never closed or closed twice: %d hooks", len(lifecycle.hooks))
	}
}

func TestTheConnectionCheckRunsOnTheContextOfTheStart(t *testing.T) {
	connector := &pinger{}
	database := sql.OpenDB(connector)
	t.Cleanup(func() { _ = database.Close() })

	lifecycle := &recordedHooks{}
	verify(lifecycle, database, nil)

	if connector.pings.Load() != 0 {
		t.Fatal("the database was contacted while the graph was being built, where no deadline of Start reaches it")
	}
	var start func(context.Context) error
	for _, hook := range lifecycle.hooks {
		if hook.OnStart != nil {
			start = hook.OnStart
		}
	}
	if start == nil {
		t.Fatal("nothing checks the connection at start, so an unreachable database is discovered by the first request instead")
	}

	if err := start(context.Background()); err != nil {
		t.Fatalf("the check refused a reachable pool: %v", err)
	}
	if connector.pings.Load() == 0 {
		t.Fatal("the check never reached the pool at all, so it proves nothing about the database")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := start(ctx)
	if err == nil {
		t.Fatal("a start whose context was already over still reported the database reachable")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the check did not run on the context of the start: %v", err)
	}
}

// sleeper answers every query slowly enough that a deadline is the only thing
// that can end the wait, which is the shape of a database that is reachable in
// the sense that TCP connects and not in any sense that matters.
type sleeper struct{ nap time.Duration }

func (this sleeper) Connect(context.Context) (driver.Conn, error) { return sleeperConn(this), nil }
func (this sleeper) Driver() driver.Driver                        { return openAnything{} }

type sleeperConn struct{ nap time.Duration }

func (sleeperConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not a real connection")
}
func (sleeperConn) Close() error                    { return nil }
func (sleeperConn) Begin() (driver.Tx, error)       { return nil, errors.New("not a real connection") }
func (this sleeperConn) Ping(context.Context) error { return nil }

func (this sleeperConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	select {
	case <-time.After(this.nap):
		return nil, errors.New("not a real connection")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// observed records what the schema read was given. The driver is reached by name
// through the configuration, so a package-level variable is the only channel
// between the test and it; nothing else in this package uses it.
var observed struct {
	sync.Mutex
	hadDeadline bool
	asked       bool
}

func init() { sql.Register("crudsqlfx-observing", observingDriver{}) }

type observingDriver struct{}

func (observingDriver) Open(string) (driver.Conn, error) { return observingConn{}, nil }

type observingConn struct{}

func (observingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("ask with a context")
}
func (observingConn) Close() error               { return nil }
func (observingConn) Begin() (driver.Tx, error)  { return nil, errors.New("ask with a context") }
func (observingConn) Ping(context.Context) error { return nil }

func (observingConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	_, hasDeadline := ctx.Deadline()
	observed.Lock()
	observed.asked = true
	observed.hadDeadline = hasDeadline
	observed.Unlock()
	return nil, errors.New("the schema is not what this test is about")
}

func TestTheSchemaTheGraphReadsIsAskedForUnderADeadline(t *testing.T) {
	observed.Lock()
	observed.asked, observed.hadDeadline = false, false
	observed.Unlock()

	// The error is expected: this driver answers no schema. What is under test is
	// the context that reached it, because reading the schema is the one thing
	// this module cannot move out of a constructor — and fx.StartTimeout does not
	// reach a constructor, so an unbounded read hangs the process at start.
	_ = fx.New(Module(&vvdb.Config{
		Engine: vvdb.SQLite, Driver: "crudsqlfx-observing", Path: ":memory:",
	}), fx.NopLogger).Err()

	observed.Lock()
	defer observed.Unlock()
	if !observed.asked {
		t.Fatal("the schema was never read, so this proves nothing about how it is read")
	}
	if !observed.hadDeadline {
		t.Fatal("the schema was read on a context with no deadline: a server that accepts the connection and never answers hangs the building of the graph, and the process neither starts nor exits")
	}
}

func TestTheDeadlineOfTheSchemaReadIsTheOneTheConfigurationStates(t *testing.T) {
	stated := &vvdb.Config{Engine: vvdb.SQLite, Path: ":memory:"}
	stated.Pool.ConnectTimeout = 3 * time.Second
	if got := schemaDeadline(stated); got != 3*time.Second {
		t.Fatalf("a deployment stated how long it waits for a connection and the schema read ignores it: %s", got)
	}
	if got := schemaDeadline(&vvdb.Config{Engine: vvdb.SQLite}); got != DefaultSchemaTimeout {
		t.Fatalf("a configuration that states nothing did not fall back to the default: %s", got)
	}
}

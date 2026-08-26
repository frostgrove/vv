package vvdb_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/utils/vvdb"
)

// recorder is a driver that connects to nothing and remembers the string it was
// asked to open. It stands in for pgx and go-sql-driver so this package can
// test what it builds without taking either as a dependency.
type recorder struct{ dsn string }

func (r *recorder) Open(name string) (driver.Conn, error) {
	r.dsn = name
	return nil, io.EOF // nothing here ever runs a statement
}

func register(t *testing.T, name string) *recorder {
	t.Helper()
	r := &recorder{}
	sql.Register(name, r)
	return r
}

func TestOpenHandsTheDriverTheStringItBuilt(t *testing.T) {
	r := register(t, "vvdbtest-postgres")
	db, err := vvdb.Open(vvdb.Config{
		Engine: vvdb.Postgres, Driver: "vvdbtest-postgres",
		Host: "db.internal", User: "vv", Password: "s3cret", Name: "app",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// sql.Open is lazy, so the driver is only asked once a connection is
	// wanted. The error is the recorder's and is not the point.
	_ = db.Ping()
	if r.dsn != "postgres://vv:s3cret@db.internal:5432/app" {
		t.Errorf("the driver was handed %q", r.dsn)
	}
}

func TestOpenSizesThePool(t *testing.T) {
	register(t, "vvdbtest-pool")
	db, err := vvdb.Open(vvdb.Config{
		Engine: vvdb.Postgres, Driver: "vvdbtest-pool", Host: "h", Name: "app",
		Pool: vvdb.Pool{MaxOpen: 7, MaxIdle: 3, MaxLifetime: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if got := db.Stats().MaxOpenConnections; got != 7 {
		t.Errorf("the pool limit in the config never reached the handle: MaxOpenConnections is %d", got)
	}
}

// The control case for the one above: an unset limit must stay database/sql's
// own default and not become a limit of zero, which would be a pool that can
// open nothing.
func TestAnUnsetPoolLimitIsLeftAlone(t *testing.T) {
	register(t, "vvdbtest-nopool")
	db, err := vvdb.Open(vvdb.Config{Engine: vvdb.Postgres, Driver: "vvdbtest-nopool", Host: "h", Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if got := db.Stats().MaxOpenConnections; got != 0 {
		t.Errorf("database/sql spells \"unlimited\" 0, and nothing should have written a limit: got %d", got)
	}
}

func TestOpenRefusesBeforeItReachesTheDriver(t *testing.T) {
	_, err := vvdb.Open(vvdb.Config{Engine: "cockroach", Name: "app"})
	if !errors.Is(err, vvdb.ErrEngine) {
		t.Fatalf("a configuration that cannot be built must not reach sql.Open; got %v", err)
	}
}

func TestAFailureToOpenDoesNotPrintThePassword(t *testing.T) {
	_, err := vvdb.Open(vvdb.Config{
		Engine: vvdb.Postgres, Driver: "vvdbtest-not-registered",
		Host: "h", User: "vv", Password: "s3cret", Name: "app",
	})
	if err == nil {
		t.Fatal("an unregistered driver should fail at once")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("the DSN carries the password and must not reach a log through an error: %v", err)
	}
}

func TestOpenReadWriteOpensBothOrNeither(t *testing.T) {
	r := register(t, "vvdbtest-rw")
	cfg := vvdb.Config{
		Engine: vvdb.Postgres, Driver: "vvdbtest-rw",
		Host: "primary.internal", User: "vv", Password: "s3cret", Name: "app",
		Replica: &vvdb.Config{Host: "replica.internal"},
	}
	p, rep, err := vvdb.OpenReadWrite(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if rep == nil {
		t.Fatal("a declared replica should come back opened")
	}
	defer rep.Close()
	_ = rep.Ping()
	if !strings.Contains(r.dsn, "replica.internal") {
		t.Errorf("the replica should have been opened against its own host, got %q", r.dsn)
	}

	cfg.Replica = nil
	p2, rep2, err := vvdb.OpenReadWrite(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	if rep2 != nil {
		t.Error("no replica declared means no second handle, not a second handle on the primary")
	}
}

func TestMustOpenPanicsSoStartUpStops(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a configuration that cannot be opened must stop the process, not return a handle that fails later")
		}
	}()
	vvdb.MustOpen(vvdb.Config{Engine: vvdb.Postgres})
}

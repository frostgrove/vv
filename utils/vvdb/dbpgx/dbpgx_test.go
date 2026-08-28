package dbpgx_test

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/frostgrove/vv/utils/vvdb"
	"github.com/frostgrove/vv/utils/vvdb/dbpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// unreachable is a config that parses and cannot connect: the assertions here
// are about what reaches pgx, and an option runs before the first dial.
func unreachable() *vvdb.Config {
	return &vvdb.Config{
		Engine: vvdb.Postgres, Host: "127.0.0.1", Port: 1,
		User: "vv", Password: "s3cret", Name: "app", SSLMode: "disable",
		Pool: vvdb.Pool{MaxOpen: 7, MaxIdle: 2, MaxLifetime: time.Hour, ConnectTimeout: 200 * time.Millisecond},
	}
}

func TestTheConfigReachesPgx(t *testing.T) {
	var got *pgxpool.Config
	pool, _ := dbpgx.Connect(context.Background(), unreachable(), func(pc *pgxpool.Config) { got = pc })
	if pool != nil {
		pool.Close()
	}
	if got == nil {
		t.Fatal("the option never ran, so nothing here was configured")
	}
	if got.MaxConns != 7 {
		t.Errorf("the pool limit in the config never reached pgx: MaxConns is %d", got.MaxConns)
	}
	if got.MinConns != 2 {
		t.Errorf("MaxIdle should land on MinConns, got %d", got.MinConns)
	}
	if got.MaxConnLifetime != time.Hour {
		t.Errorf("MaxLifetime should land on MaxConnLifetime, got %s", got.MaxConnLifetime)
	}
	if got.ConnConfig.ConnectTimeout != 200*time.Millisecond {
		t.Errorf("ConnectTimeout should reach the connection, got %s", got.ConnConfig.ConnectTimeout)
	}
	if got.ConnConfig.Database != "app" || got.ConnConfig.User != "vv" || got.ConnConfig.Password != "s3cret" {
		t.Errorf("pgx parsed back something other than what the config said: %s@%s",
			got.ConnConfig.User, got.ConnConfig.Database)
	}
}

// The control case: without a pool section, pgx keeps its own defaults rather
// than being told zero.
func TestAnUnsetPoolLeavesPgxsDefaults(t *testing.T) {
	c := unreachable()
	c.Pool = vvdb.Pool{}
	var got *pgxpool.Config
	pool, _ := dbpgx.Connect(context.Background(), c, func(pc *pgxpool.Config) { got = pc })
	if pool != nil {
		pool.Close()
	}
	if got == nil {
		t.Fatal("the option never ran")
	}
	if got.MaxConns < 1 {
		t.Errorf("a pool that can open no connections is not a default: MaxConns is %d", got.MaxConns)
	}
}

func TestAnotherEnginesConfigIsRefused(t *testing.T) {
	c := unreachable()
	c.Engine = vvdb.MySQL
	c.SSLMode = ""
	if _, err := dbpgx.Connect(context.Background(), c); !errors.Is(err, vvdb.ErrEngine) {
		t.Fatalf("dbpgx speaks postgres, and a mysql config here is a mistake worth naming; got %v", err)
	}
}

func TestConnectRefusesBeforeItDials(t *testing.T) {
	c := unreachable()
	c.Name = ""
	if _, err := dbpgx.Connect(context.Background(), c); !errors.Is(err, vvdb.ErrMissing) {
		t.Fatalf("a configuration that cannot be built must not reach the network; got %v", err)
	}
}

func TestConnectPingsRatherThanReturningALazyPool(t *testing.T) {
	boom := errors.New("stop before dialing")
	called := false
	_, err := dbpgx.Connect(context.Background(), unreachable(), func(pc *pgxpool.Config) {
		pc.BeforeConnect = func(context.Context, *pgx.ConnConfig) error {
			called = true
			return boom
		}
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Connect() = %v, want the connection attempt error", err)
	}
	if !called {
		t.Fatal("Connect returned a lazy pool without attempting a connection")
	}
}

func TestConnectRefusesAReplicaAndAnUnrepresentablePoolBeforeDialing(t *testing.T) {
	c := unreachable()
	c.Replica = &vvdb.Config{Host: "replica"}
	if _, err := dbpgx.Connect(context.Background(), c); !errors.Is(err, vvdb.ErrConflict) {
		t.Fatalf("Connect() = %v, want a replica refused by the single-handle API", err)
	}

	if strconv.IntSize < 64 {
		t.Skip("an int cannot represent a value above pgx's int32 limit here")
	}
	c = unreachable()
	c.Pool.MaxOpen = math.MaxInt32 + 1
	if _, err := dbpgx.Connect(context.Background(), c); !errors.Is(err, vvdb.ErrUnsupported) {
		t.Fatalf("Connect() = %v, want the pgx int32 bound refused before dialing", err)
	}
}

func TestApplySizesAnApplicationOwnedPgxConfig(t *testing.T) {
	pc, err := pgxpool.ParseConfig("postgres://vv:secret@db.internal:5432/app?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool := vvdb.Pool{MaxOpen: 9, MaxIdle: 3}
	if err := dbpgx.Apply(pc, &pool); err != nil {
		t.Fatal(err)
	}
	if pc.MaxConns != 9 || pc.MinConns != 3 {
		t.Fatalf("Apply did not size the application-owned config: MaxConns=%d MinConns=%d", pc.MaxConns, pc.MinConns)
	}
	unsupported := vvdb.Pool{MaxIdle: -1}
	if err := dbpgx.Apply(pc, &unsupported); !errors.Is(err, vvdb.ErrUnsupported) {
		t.Fatalf("Apply() = %v, want pgx's unsupported max-idle spelling named", err)
	}
	for _, p := range []vvdb.Pool{{MaxOpen: -1}, {MaxOpen: 1, MaxIdle: 2}} {
		if err := dbpgx.Apply(pc, &p); err == nil {
			t.Fatalf("Apply(%+v) accepted a pool Config.Validate refuses", p)
		}
	}
}

func TestApplyRefusesMaxIdleAboveTheParsedEffectiveMaximumWithoutMutation(t *testing.T) {
	pc, err := pgxpool.ParseConfig("postgres://vv:secret@db.internal:5432/app?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pc.MaxConns = 4
	pc.MinConns = 1
	pool := vvdb.Pool{MaxIdle: 5}
	if err := dbpgx.Apply(pc, &pool); !errors.Is(err, vvdb.ErrConflict) {
		t.Fatalf("Apply() = %v, want the effective pgx maximum conflict", err)
	}
	if pc.MaxConns != 4 || pc.MinConns != 1 {
		t.Fatalf("Apply mutated a config it refused: MaxConns=%d MinConns=%d", pc.MaxConns, pc.MinConns)
	}
}

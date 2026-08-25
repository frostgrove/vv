package dbpgx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shardit-io/vv/vvdb"
	"github.com/shardit-io/vv/vvdb/dbpgx"
)

// unreachable is a config that parses and cannot connect: the assertions here
// are about what reaches pgx, and an option runs before the first dial.
func unreachable() vvdb.Config {
	return vvdb.Config{
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

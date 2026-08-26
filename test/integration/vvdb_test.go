//go:build integration

package integration

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/utils/vvdb"
	"github.com/frostgrove/vv/utils/vvdb/dbpgx"
)

// The compose file is the source of these, and the environment overrides them
// the same way the rest of the suite is overridden. When VV_*_DSN is set the
// builder is bypassed on purpose: the question this file asks then is only
// whether vvdb can open what it is pointed at.
func vvdbConfig(t *testing.T, engine vvdb.Engine, envVar string, port int) vvdb.Config {
	t.Helper()
	if dsn := os.Getenv(envVar); dsn != "" {
		return vvdb.Config{Engine: engine, DSN: dsn}
	}
	c := vvdb.Config{
		Engine: engine, Host: "127.0.0.1", Port: port,
		User: "vv", Password: "vv", Name: "vv",
		Pool: vvdb.Pool{MaxOpen: 4, ConnectTimeout: 5 * time.Second},
	}
	if engine == vvdb.Postgres {
		c.SSLMode = "disable"
	}
	return c
}

// The whole promise of the package in one test: the same three fields, spelled
// once, reach three different servers through two different string syntaxes.
func TestOneConfigShapeOpensEveryEngine(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		cfg    vvdb.Config
		source func(*sql.DB) crud.Source
	}{
		{"postgres", vvdbConfig(t, vvdb.Postgres, "VV_PG_DSN", 55432), func(db *sql.DB) crud.Source { return crudsql.Postgres(db) }},
		{"mysql", vvdbConfig(t, vvdb.MySQL, "VV_MYSQL_DSN", 53306), func(db *sql.DB) crud.Source { return crudsql.MySQL(db) }},
		{"mariadb", vvdbConfig(t, vvdb.MariaDB, "VV_MARIADB_DSN", 53307), func(db *sql.DB) crud.Source { return crudsql.MariaDB(db) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := vvdb.Open(tc.cfg)
			if err != nil {
				t.Fatalf("opening %s from a config failed: %v", tc.name, err)
			}
			defer db.Close()
			if err := db.PingContext(ctx); err != nil {
				t.Fatalf("the %s server refused the string vvdb built: %v", tc.name, err)
			}
			// Through the adapter, because a handle that pings and cannot run a
			// statement would still pass the line above.
			var one int
			rows, err := tc.source(db).Query(ctx, "SELECT 1")
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatalf("%s returned no row for SELECT 1", tc.name)
			}
			if err := rows.Scan(&one); err != nil || one != 1 {
				t.Fatalf("%s answered %d, %v", tc.name, one, err)
			}
			if got := db.Stats().MaxOpenConnections; got != 4 {
				t.Errorf("the pool section did not reach the handle: MaxOpenConnections is %d", got)
			}
		})
	}
}

// The control case. Without it the test above passes on any string that
// happens to connect, including one built from the wrong fields.
func TestAWrongPasswordIsRefusedByTheServer(t *testing.T) {
	if os.Getenv("VV_PG_DSN") != "" {
		t.Skip("the DSN is supplied whole, so there are no fields to get wrong")
	}
	c := vvdbConfig(t, vvdb.Postgres, "", 55432)
	c.Password = "not-the-password"
	db, err := vvdb.Open(c)
	if err != nil {
		t.Fatalf("the string should build; it is the server that must refuse it: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err == nil {
		t.Fatal("the server accepted a wrong password, so this suite is not proving the password travels at all")
	}
}

func TestDbpgxOpensAPoolFromTheSameConfig(t *testing.T) {
	ctx := context.Background()
	pool, err := dbpgx.Connect(ctx, vvdbConfig(t, vvdb.Postgres, "VV_PG_DSN", 55432))
	if err != nil {
		t.Fatalf("opening a pgx pool from a config failed: %v", err)
	}
	defer pool.Close()
	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("the pool answered %d, %v", one, err)
	}
	if got := pool.Config().MaxConns; got != 4 {
		t.Errorf("the pool section did not reach pgx: MaxConns is %d", got)
	}
}

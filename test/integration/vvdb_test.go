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

func vvdbConfig(t *testing.T, engine vvdb.Engine, envVar string, port int) vvdb.Config {
	t.Helper()
	if dsn := os.Getenv(envVar); dsn != "" {
		return vvdb.Config{Engine: engine, DSN: vvdb.Secret(dsn)}
	}
	c := vvdb.Config{
		Engine: engine, Host: "127.0.0.1", Port: port,
		User: "vv", Password: "vv", Name: "vv",
		Pool: vvdb.Pool{MaxOpen: 4, ConnectTimeout: 5 * time.Second},
	}

	c.SSLMode = "disable"
	return c
}

func TestOneConfigShapeOpensEveryEngine(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		config vvdb.Config
		source func(*sql.DB) crud.Source
	}{
		{"postgres", vvdbConfig(t, vvdb.Postgres, "VV_PG_DSN", 55432), func(database *sql.DB) crud.Source { return crudsql.Postgres(database) }},
		{"mysql", vvdbConfig(t, vvdb.MySQL, "VV_MYSQL_DSN", 53306), func(database *sql.DB) crud.Source { return crudsql.MySQL(database) }},
		{"mariadb", vvdbConfig(t, vvdb.MariaDB, "VV_MARIADB_DSN", 53307), func(database *sql.DB) crud.Source { return crudsql.MariaDB(database) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := vvdb.Open(&tc.config)
			if err != nil {
				t.Fatalf("opening %s from a config failed: %v", tc.name, err)
			}
			defer database.Close()
			if err := database.PingContext(ctx); err != nil {
				t.Fatalf("the %s server refused the string vvdb built: %v", tc.name, err)
			}

			var one int
			rows, err := tc.source(database).Query(ctx, "SELECT 1")
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
			if got := database.Stats().MaxOpenConnections; got != 4 {
				t.Errorf("the pool section did not reach the handle: MaxOpenConnections is %d", got)
			}
		})
	}
}

func TestAWrongPasswordIsRefusedByTheServer(t *testing.T) {
	if os.Getenv("VV_PG_DSN") != "" {
		t.Skip("the DSN is supplied whole, so there are no fields to get wrong")
	}
	c := vvdbConfig(t, vvdb.Postgres, "", 55432)
	c.Password = "not-the-password"
	database, err := vvdb.Open(&c)
	if err != nil {
		t.Fatalf("the string should build; it is the server that must refuse it: %v", err)
	}
	defer database.Close()
	if err := database.PingContext(context.Background()); err == nil {
		t.Fatal("the server accepted a wrong password, so this suite is not proving the password travels at all")
	}
}

func TestDbpgxOpensAPoolFromTheSameConfig(t *testing.T) {
	ctx := context.Background()
	database := vvdbConfig(t, vvdb.Postgres, "VV_PG_DSN", 55432)
	pool, err := dbpgx.Connect(ctx, &database)
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

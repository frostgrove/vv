//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/frostgrove/vv/crud/query"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/frostgrove/vv/test/corpus"
)

var (
	pgDSN    = corpus.PostgresDSN()
	mysqlDSN = corpus.MySQLDSN()
	mariaDSN = corpus.MariaDBDSN()
)

var (
	pgDB    *sql.DB
	myDB    *sql.DB
	mariaDB *sql.DB
	pgPool  *pgxpool.Pool
	skipMsg string
)

func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		fmt.Fprintf(os.Stderr, "github.com/frostgrove/vv integration setup failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "start the databases with: docker compose up -d --wait")
		os.Exit(1)
	}
	code := m.Run()
	teardown()
	os.Exit(code)
}

func setup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var err error
	if pgDB, err = openAndWait(ctx, "pgx", pgDSN); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if myDB, err = openAndWait(ctx, "mysql", mysqlDSN); err != nil {
		return fmt.Errorf("mysql: %w", err)
	}
	if mariaDB, err = openAndWait(ctx, "mysql", mariaDSN); err != nil {
		return fmt.Errorf("mariadb: %w", err)
	}
	if pgPool, err = pgxpool.New(ctx, pgDSN); err != nil {
		return fmt.Errorf("pgx pool: %w", err)
	}
	if err := pgPool.Ping(ctx); err != nil {
		return fmt.Errorf("pgx pool ping: %w", err)
	}

	if _, err := pgDB.ExecContext(ctx, schemaPostgres); err != nil {
		return fmt.Errorf("postgres schema: %w", err)
	}
	for _, database := range []struct {
		name     string
		database *sql.DB
	}{{"mysql", myDB}, {"mariadb", mariaDB}} {
		if _, err := database.database.ExecContext(ctx, "DROP TABLE IF EXISTS users"); err != nil {
			return fmt.Errorf("%s drop: %w", database.name, err)
		}
		if _, err := database.database.ExecContext(ctx, schemaMySQL); err != nil {
			return fmt.Errorf("%s schema: %w", database.name, err)
		}
	}

	if _, err := pgDB.ExecContext(ctx, schemaBlogPostgres); err != nil {
		return fmt.Errorf("postgres blog schema: %w", err)
	}
	for _, stmt := range schemaBlogMySQL {
		if _, err := myDB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("mysql blog schema: %w", err)
		}
	}
	return nil
}

func openAndWait(ctx context.Context, driver, dsn string) (*sql.DB, error) {
	database, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(8)
	deadline := time.Now().Add(45 * time.Second)
	for {
		if err = database.PingContext(ctx); err == nil {
			return database, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func teardown() {
	if pgPool != nil {
		pgPool.Close()
	}
	for _, database := range []*sql.DB{pgDB, myDB, mariaDB} {
		if database != nil {
			_ = database.Close()
		}
	}
}

func truncate(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec("DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
}

var unpagedOK = &query.Config{AllowUnpaged: true}

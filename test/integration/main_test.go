//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/shardit-io/vv/test/corpus"
)

// Where the databases are. The corpus generator reads the same variables and
// the same defaults, from the one place that declares them: two copies would
// mean pointing the suite at another server silently left the corpus behind.
var (
	pgDSN    = corpus.PostgresDSN()
	mysqlDSN = corpus.MySQLDSN()
	mariaDSN = corpus.MariaDBDSN()
)

// Package-level handles, opened once for the whole run.
//
// MariaDB is a fourth engine and not a second MySQL. It answers a failed CHECK
// with 4025 and SQLSTATE 23000 where MySQL answers 3819 and HY000, and it names
// a duplicated index without its table where MySQL prefixes one. crud.MySQL
// claims to target both and, until this handle existed, had never been run
// against it.
var (
	pgDB    *sql.DB
	myDB    *sql.DB
	mariaDB *sql.DB
	pgPool  *pgxpool.Pool
	skipMsg string
)

func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		fmt.Fprintf(os.Stderr, "github.com/shardit-io/vv integration setup failed: %v\n", err)
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
	for _, db := range []struct {
		name string
		db   *sql.DB
	}{{"mysql", myDB}, {"mariadb", mariaDB}} {
		if _, err := db.db.ExecContext(ctx, "DROP TABLE IF EXISTS users"); err != nil {
			return fmt.Errorf("%s drop: %w", db.name, err)
		}
		if _, err := db.db.ExecContext(ctx, schemaMySQL); err != nil {
			return fmt.Errorf("%s schema: %w", db.name, err)
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
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	deadline := time.Now().Add(45 * time.Second)
	for {
		if err = db.PingContext(ctx); err == nil {
			return db, nil
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
	for _, db := range []*sql.DB{pgDB, myDB, mariaDB} {
		if db != nil {
			_ = db.Close()
		}
	}
}

// truncate wipes the shared table; the suite calls it between subtests, but
// interop tests that build their own repositories use it directly.
func truncate(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
}

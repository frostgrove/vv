// Package corpus captures what each database actually says when it refuses a
// statement.
//
// It exists because the engine matrix it replaced was written from memory and
// half of it was wrong. MySQL answers a failed CHECK with 3819 and SQLSTATE
// HY000 — not class 23, which is where every reading of the specification puts
// it — so the shipped classifier returned 500 where the documentation promised
// 409, and no test noticed. A table nobody provoked is a guess with a citation.
//
// The output is checked in under errs/sqlerr/testdata/corpus and is what the
// dialect parsers are written against. It lives in the test module because it
// needs the drivers; the types it writes live in the root module, because the
// parsers are unit-tested and a unit test there cannot import this.
package corpus

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/shardit-io/vv/errs/sqlerr"
)

// The defaults match docker-compose.yml, and the variables match the ones the
// integration suite reads, so pointing one at another server points both.
func PostgresDSN() string {
	return env("VV_PG_DSN", "postgres://vv:vv@127.0.0.1:55432/vv?sslmode=disable")
}
func MySQLDSN() string {
	return env("VV_MYSQL_DSN", "vv:vv@tcp(127.0.0.1:53306)/vv?parseTime=true")
}
func MariaDBDSN() string {
	return env("VV_MARIADB_DSN", "vv:vv@tcp(127.0.0.1:53307)/vv?parseTime=true")
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Engines is every database the corpus covers. tmp is a writable directory for
// SQLite, which needs a file rather than a server.
//
// Foreign keys are switched on in the DSN. SQLite has them off by default, so
// without that line the two foreign-key cases would insert cleanly and the
// corpus would record that SQLite has no foreign keys — which is a statement
// about the connection, not the engine.
func Engines(tmp string) []Engine {
	return []Engine{
		postgres(PostgresDSN()),
		mysqlish("mysql", MySQLDSN()),
		mysqlish("mariadb", MariaDBDSN()),
		sqlite("file:" + filepath.Join(tmp, "corpus.db") +
			"?_pragma=foreign_keys(1)&_pragma=busy_timeout(200)"),
	}
}

// Dir locates the checked-in corpus by walking up to the repository root.
//
// The generator and the integration suite sit at different depths, and a
// relative literal in each is how one of them goes stale without anybody
// noticing which.
func Dir() (string, error) {
	d, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(d, "errs", "sqlerr", "testdata", "corpus")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no errs/sqlerr/testdata/corpus above the working directory")
		}
		d = parent
	}
}

// Capture builds one engine's tables, provokes every case and records what came
// back.
//
// A case that produces no error at all aborts the run, naming itself. Without
// that an engine which quietly accepted everything would write a clean-looking
// file full of nulls, and the corpus would be at its least trustworthy exactly
// when it mattered most. The inverse holds for a case declared Unreachable: it
// is expected to succeed, and an error there is equally a finding.
func Capture(ctx context.Context, e Engine) (*sqlerr.Corpus, error) {
	db, err := e.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	out := &sqlerr.Corpus{Engine: e.Name, Driver: e.Pkg}
	if err := db.QueryRowContext(ctx, e.version).Scan(&out.Server); err != nil {
		return nil, fmt.Errorf("%s: reading the server version: %w", e.Name, err)
	}

	for _, p := range e.Cases {
		err := e.provoke(ctx, db, p)
		switch {
		case p.Unreachable != "" && err != nil:
			return nil, fmt.Errorf("%s: %s is marked unreachable and the server refused it anyway: %w",
				e.Name, p.Name, err)
		case p.Unreachable == "" && err == nil:
			return nil, fmt.Errorf("%s: %s produced no error; the statement no longer violates anything",
				e.Name, p.Name)
		}
		out.Cases = append(out.Cases, sqlerr.Case{
			Name:        p.Name,
			Kind:        p.Kind,
			Want:        p.Want,
			Stmt:        p.Stmt,
			Unreachable: p.Unreachable,
			Err:         capture(err),
		})
	}
	return out, nil
}

// Open connects, builds the fixture tables and seeds the anchor row.
//
// The schema is rebuilt on every run rather than created once. These tables are
// the corpus's own, nothing else reads them, and a case that leaves a row behind
// — SQLite accepts three the others refuse — would otherwise change what the
// next run's duplicate-key case collides with.
func (e Engine) Open(ctx context.Context) (*sql.DB, error) {
	db, err := sql.Open(e.Driver, e.DSN)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.Name, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", e.Name, err)
	}
	for _, stmt := range append(append([]string{}, e.schema...), e.seed...) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %s: %w", e.Name, firstLine(stmt), err)
		}
	}
	return db, nil
}

// provoke runs one case against a raw handle. A case with nothing to run at all
// is one this engine cannot reach, and says so in Unreachable.
func (e Engine) provoke(ctx context.Context, db *sql.DB, p Probe) error {
	switch {
	case p.Contend:
		return e.Contend(ctx, db, func(wait *sql.Conn) error {
			_, err := wait.ExecContext(ctx, e.Lock)
			return err
		})
	case p.Connect != "":
		return e.Reach(ctx, p.Connect)
	case p.Stmt != "":
		_, err := db.ExecContext(ctx, p.Stmt)
		return err
	default:
		return nil
	}
}

// Contend holds the anchor row on one connection and hands wait a second one
// whose patience has been cut to a fraction of a second. What wait returns is
// the contention error.
//
// The caller supplies the waiting statement so the same contention can be run
// through a repository rather than a raw handle, which is what lets the
// integration suite check the adapter's verdict on the same error the corpus
// recorded.
//
// A deadlock would be the other half of the retryable class and is not here: it
// needs two goroutines racing through a barrier, and a corpus entry that depends
// on scheduling is one that regenerates differently every time.
func (e Engine) Contend(ctx context.Context, db *sql.DB, wait func(*sql.Conn) error) error {
	holder, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("holding connection: %w", err)
	}
	defer holder.Close()
	held, err := holder.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("holding transaction: %w", err)
	}
	defer held.Rollback()
	if _, err := held.ExecContext(ctx, e.Lock); err != nil {
		return fmt.Errorf("taking the lock: %w", err)
	}

	waiter, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("waiting connection: %w", err)
	}
	defer waiter.Close()
	for _, stmt := range e.Waiter {
		if _, err := waiter.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return wait(waiter)
}

// Reach opens a second handle at dsn and pings it. Both connection-time
// negatives are refusals no statement on the main handle can produce, and a
// repository bound to such a DSN meets them on its first call.
func (e Engine) Reach(ctx context.Context, dsn string) error {
	db, err := sql.Open(e.Driver, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

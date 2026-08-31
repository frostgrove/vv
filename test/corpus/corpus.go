package corpus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/frostgrove/vv/errs/sqlerr"
)

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

func Engines(tmp string) []Engine {
	return []Engine{
		postgres(PostgresDSN()),
		mysqlish("mysql", MySQLDSN()),
		mysqlish("mariadb", MariaDBDSN()),
		sqlite("file:" + filepath.Join(tmp, "corpus.db") +
			"?_pragma=foreign_keys(1)&_pragma=busy_timeout(200)"),
	}
}

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

func Capture(ctx context.Context, e Engine) (*sqlerr.Corpus, error) {
	database, err := e.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	out := &sqlerr.Corpus{Engine: e.Name, Driver: e.Pkg}
	if err := database.QueryRowContext(ctx, e.version).Scan(&out.Server); err != nil {
		return nil, fmt.Errorf("%s: reading the server version: %w", e.Name, err)
	}

	for _, p := range e.Cases {
		err := e.provoke(ctx, database, p)
		switch {
		case errors.Is(err, errHarness):
			return nil, fmt.Errorf("%s: %s: %w", e.Name, p.Name, err)
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
			Err:         capture(err, p.Volatile),
		})
	}
	return out, nil
}

func (this Engine) Open(ctx context.Context) (*sql.DB, error) {
	database, err := sql.Open(this.Driver, this.DSN)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", this.Name, err)
	}
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("%s: %w", this.Name, err)
	}
	for _, stmt := range append(append([]string{}, this.schema...), this.seed...) {
		if _, err := database.ExecContext(ctx, stmt); err != nil {
			database.Close()
			return nil, fmt.Errorf("%s: %s: %w", this.Name, firstLine(stmt), err)
		}
	}
	return database, nil
}

func (this Engine) provoke(ctx context.Context, database *sql.DB, p Probe) error {
	switch {
	case p.Contend:
		return this.Contend(ctx, database, func(wait *sql.Conn) error {
			_, err := wait.ExecContext(ctx, this.Lock)
			return err
		})
	case p.RaceA != nil:
		return this.Race(ctx, database, p, func(c *sql.Conn, stmt string) error {
			_, err := c.ExecContext(ctx, stmt)
			return err
		})
	case p.Tx != nil:
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("%w: opening the transaction: %v", errHarness, err)
		}
		defer tx.Rollback()
		return Script(p.Tx,
			func(stmt string) error { _, err := tx.ExecContext(ctx, stmt); return err },
			tx.Commit)
	case p.Session != nil:
		return this.Session(ctx, p, func(c *sql.Conn) error {
			_, err := c.ExecContext(ctx, p.Stmt)
			return err
		})
	case p.Connect != "":
		return this.Reach(ctx, p.Connect)
	case p.Stmt != "":
		_, err := database.ExecContext(ctx, p.Stmt)
		return err
	default:
		return nil
	}
}

var errHarness = errors.New("the probe could not be run")

func Script(stmts []string, exec func(string) error, commit func() error) error {
	var last error
	for _, stmt := range stmts {
		if stmt == commitStep {
			last = commit()
			continue
		}
		last = exec(stmt)
	}
	return last
}

func (this Engine) Race(ctx context.Context, database *sql.DB, p Probe, exec func(*sql.Conn, string) error) error {
	if len(p.RaceA) != len(p.RaceB) {
		return fmt.Errorf("%w: %s: the two sides have %d and %d statements and would wait for each other forever",
			errHarness, p.Name, len(p.RaceA), len(p.RaceB))
	}
	a, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: first connection: %v", errHarness, err)
	}
	defer a.Close()
	b, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: second connection: %v", errHarness, err)
	}
	defer b.Close()

	toA, toB := make(chan struct{}, 1), make(chan struct{}, 1)
	side := func(c *sql.Conn, stmts []string, mine chan<- struct{}, theirs <-chan struct{}) error {
		var first error
		for _, stmt := range stmts {
			select {
			case mine <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case <-theirs:
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := exec(c, stmt); err != nil && first == nil {
				first = err
			}
		}
		return first
	}

	done := make(chan error, 1)
	go func() { done <- side(b, p.RaceB, toB, toA) }()
	errA := side(a, p.RaceA, toA, toB)
	errB := <-done

	switch {
	case errA != nil && errB != nil:
		return fmt.Errorf("%w: %s: both sides lost — A: %v; B: %v", errHarness, p.Name, errA, errB)
	case errA != nil:
		return errA
	default:
		return errB
	}
}

func (this Engine) Contend(ctx context.Context, database *sql.DB, wait func(*sql.Conn) error) error {
	holder, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: holding connection: %v", errHarness, err)
	}
	defer holder.Close()
	held, err := holder.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: holding transaction: %v", errHarness, err)
	}
	defer held.Rollback()
	if _, err := held.ExecContext(ctx, this.Lock); err != nil {
		return fmt.Errorf("%w: taking the lock: %v", errHarness, err)
	}

	waiter, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: waiting connection: %v", errHarness, err)
	}
	defer waiter.Close()
	for _, stmt := range this.Waiter {
		if _, err := waiter.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w: %s: %v", errHarness, stmt, err)
		}
	}

	defer func() {
		for _, stmt := range this.Restore {
			waiter.ExecContext(ctx, stmt)
		}
	}()
	return wait(waiter)
}

func (this Engine) Session(ctx context.Context, p Probe, run func(*sql.Conn) error) error {
	database, err := sql.Open(this.Driver, this.DSN)
	if err != nil {
		return fmt.Errorf("%w: %v", errHarness, err)
	}
	defer database.Close()
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: taking a connection: %v", errHarness, err)
	}
	defer conn.Close()
	for _, stmt := range p.Session {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w: %s: %v", errHarness, stmt, err)
		}
	}
	return run(conn)
}

func (this Engine) Reach(ctx context.Context, dsn string) error {
	database, err := sql.Open(this.Driver, dsn)
	if err != nil {
		return err
	}
	defer database.Close()
	return database.PingContext(ctx)
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

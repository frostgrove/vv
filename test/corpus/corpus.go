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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/frostgrove/vv/errs/sqlerr"
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
//
// A failure of the probe itself — a session statement the server refused, a race
// both sides lost — aborts too, and does not become an entry. An engine's answer
// and a broken fixture look the same in a JSON file, and only one of them is
// evidence.
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
	case p.RaceA != nil:
		return e.Race(ctx, db, p, func(c *sql.Conn, stmt string) error {
			_, err := c.ExecContext(ctx, stmt)
			return err
		})
	case p.Tx != nil:
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("%w: opening the transaction: %v", errHarness, err)
		}
		defer tx.Rollback()
		return Script(p.Tx,
			func(stmt string) error { _, err := tx.ExecContext(ctx, stmt); return err },
			tx.Commit)
	case p.Session != nil:
		return e.Session(ctx, p, func(c *sql.Conn) error {
			_, err := c.ExecContext(ctx, p.Stmt)
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

// errHarness marks a failure of the capture rather than of the statement. A
// probe that could not even be set up must abort the run: recorded as the case's
// error it would look like a server answer, and the one file whose whole value
// is that every entry is real would be carrying a fabrication.
var errHarness = errors.New("the probe could not be run")

// Script runs a transaction probe. The error it returns is the *last* step's,
// not the first one that failed: both cases that need this are about what
// happens after something already failed, and "first non-nil" would record
// MySQL's duplicate-key error under the name transaction_aborted and call an
// unreachable case reachable.
//
// A step spelled COMMIT commits. A deferred constraint fires there and nowhere
// else, so a script that could not name the commit could not reach it.
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

// Race runs two scripts on two connections in lock step: both sides finish
// statement N before either starts N+1.
//
// Exactly one side is expected to lose. Which one is the engine's own choice —
// PostgreSQL picks a deadlock victim by cost, InnoDB by the smaller
// transaction — so the schedule is not ours to predict; what is stable is the
// key, which is why the entry regenerates identically even though the run does
// not. Both sides failing, or neither, is a finding rather than a capture: the
// corpus records one error and picking between two would be a coin toss.
//
// The rendezvous is between every pair of statements and not only the first,
// because the statement that opens the transaction takes no lock. Letting the
// two sides race from there is what made a deadlock look like something that
// needed a barrier and a retry ([[D-040]]); with the rendezvous it fires every
// run.
func (e Engine) Race(ctx context.Context, db *sql.DB, p Probe, exec func(*sql.Conn, string) error) error {
	if len(p.RaceA) != len(p.RaceB) {
		return fmt.Errorf("%w: %s: the two sides have %d and %d statements and would wait for each other forever",
			errHarness, p.Name, len(p.RaceA), len(p.RaceB))
	}
	a, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: first connection: %v", errHarness, err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: second connection: %v", errHarness, err)
	}
	defer b.Close()

	// Capacity one, one token per round: a side reaching the next round before
	// the other has taken its token blocks on the send, which is the rendezvous
	// doing its job rather than a lost wakeup.
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

// Contend holds the anchor row on one connection and hands wait a second one
// whose patience has been cut to a fraction of a second. What wait returns is
// the contention error.
//
// The caller supplies the waiting statement so the same contention can be run
// through a repository rather than a raw handle, which is what lets the
// integration suite check the adapter's verdict on the same error the corpus
// recorded.
//
// A deadlock is the other half of the retryable class and needs the other shape:
// two connections that both hold something before either reaches for what the
// other holds. That is Engine.Race, and a rendezvous between every pair of
// statements is what made it deterministic — the entry regenerates identically
// because the key does, even though which side loses is the engine's choice.
func (e Engine) Contend(ctx context.Context, db *sql.DB, wait func(*sql.Conn) error) error {
	holder, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: holding connection: %v", errHarness, err)
	}
	defer holder.Close()
	held, err := holder.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: holding transaction: %v", errHarness, err)
	}
	defer held.Rollback()
	if _, err := held.ExecContext(ctx, e.Lock); err != nil {
		return fmt.Errorf("%w: taking the lock: %v", errHarness, err)
	}

	waiter, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: waiting connection: %v", errHarness, err)
	}
	defer waiter.Close()
	for _, stmt := range e.Waiter {
		if _, err := waiter.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w: %s: %v", errHarness, stmt, err)
		}
	}
	// The connection goes back to the pool when this returns, and a cut-down
	// patience left on it is measured by whatever probe draws it next.
	defer func() {
		for _, stmt := range e.Restore {
			waiter.ExecContext(ctx, stmt)
		}
	}()
	return wait(waiter)
}

// Session runs the probe's session statements on a connection of its own and
// then hands that same connection to run.
//
// The handle is its own, not one drawn from the shared pool, and that is the
// whole point. A session variable set on a pooled connection goes back to the
// pool with it: the first capture recorded the undefined-table message in
// Russian, two cases after the locale probe, because the locale outlived the
// probe that set it. Closing this handle closes the connection for real.
func (e Engine) Session(ctx context.Context, p Probe, run func(*sql.Conn) error) error {
	db, err := sql.Open(e.Driver, e.DSN)
	if err != nil {
		return fmt.Errorf("%w: %v", errHarness, err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
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

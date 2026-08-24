package corpus

import (
	"net/url"
	"strings"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs/sqlerr"
)

// A probe is one violation, deliberately provoked.
//
// Exactly one of Stmt and run is set, except for a case an engine cannot reach
// at all, which may set neither and explain itself instead.
type Probe struct {
	Name, Kind, Want string
	Stmt             string
	// Connect is the DSN for a case that fails before any statement runs. A
	// repository bound to it fails on its first call, so these reach the
	// classifier the same way the statement cases do and belong in the same
	// table.
	Connect string
	// Contend marks the case that needs two connections: one holds a row and
	// the other waits for it past its patience. What they run is Engine.Lock.
	Contend bool
	// Unreachable inverts the expectation: this engine accepts the statement,
	// and the capture fails if it turns out to refuse it. An absence stated and
	// checked is worth more than a row quietly left out.
	Unreachable string
}

// An Engine is one database, the fixture schema it is given, and everything
// asked of it.
type Engine struct {
	Name        string
	Driver, Pkg string
	DSN         string
	// Dialect is what a repository bound to this engine would use, so a caller
	// replaying a case through the adapter does not have to re-derive it.
	// MariaDB shares crud.MySQL, RowAlias off — it has no `AS new`.
	Dialect      crud.Dialect
	version      string // the query that names the server
	schema, seed []string
	Cases        []Probe
	// Waiter is what the second connection runs before waiting, to cut the
	// engine's default patience down from seconds. Lock is what both sides run.
	Waiter []string
	Lock   string
}

// The four engines, written out rather than generated from a shared spine.
// Where two of them agree the repetition costs a few lines; where they disagree
// — and MySQL and MariaDB disagree three times in this table — the difference is
// visible on the page instead of hidden in a conditional.

func postgres(dsn string) Engine {
	return Engine{
		Name:    "postgres",
		Dialect: crud.Postgres{},
		Driver:  "pgx",
		Pkg:     "github.com/jackc/pgx/v5/stdlib",
		DSN:     dsn,
		version: "SELECT version()",
		schema: []string{
			`DROP TABLE IF EXISTS cp_child`,
			`DROP TABLE IF EXISTS cp_parent`,
			`CREATE TABLE cp_parent (
				id    BIGINT     PRIMARY KEY,
				slug  VARCHAR(8) NOT NULL UNIQUE,
				need  VARCHAR(8) NOT NULL,
				num   INTEGER        NULL,
				small SMALLINT       NULL,
				a     VARCHAR(8)     NULL,
				b     VARCHAR(8)     NULL,
				CONSTRAINT cp_ab  UNIQUE (a, b),
				CONSTRAINT cp_pos CHECK (num IS NULL OR num >= 0)
			)`,
			`CREATE TABLE cp_child (
				id     BIGINT PRIMARY KEY,
				parent BIGINT     NULL,
				CONSTRAINT cp_fk FOREIGN KEY (parent) REFERENCES cp_parent (id)
			)`,
		},
		seed:   seed,
		Waiter: []string{`SET lock_timeout = '200ms'`},
		Lock:   lockRow,
		Cases: []Probe{
			{Name: "unique", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupSlug},
			{Name: "unique_composite", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupPair},
			{Name: "primary_key", Kind: sqlerr.KindIntegrity, Want: "primary_key", Stmt: dupID},
			{Name: "foreign_key", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Stmt: orphanChild},
			{Name: "restrict", Kind: sqlerr.KindIntegrity, Want: "restrict", Stmt: deleteReferenced},
			{Name: "not_null", Kind: sqlerr.KindIntegrity, Want: "not_null", Stmt: explicitNull},
			{Name: "check", Kind: sqlerr.KindIntegrity, Want: "check", Stmt: negativeNum},
			// PostgreSQL has no separate answer for an omitted column: leaving
			// out a NOT NULL column with no default is the same 23502 as sending
			// NULL. MySQL and MariaDB raise 1364 instead, so the same payload
			// arrives as two different violations depending on the engine.
			{Name: "missing_default", Kind: sqlerr.KindIntegrity, Want: "not_null", Stmt: omitNeed},
			{Name: "too_long", Kind: sqlerr.KindData, Want: "too_long", Stmt: overlongSlug},
			{Name: "out_of_range", Kind: sqlerr.KindData, Want: "out_of_range", Stmt: hugeSmall},
			{Name: "bad_type", Kind: sqlerr.KindData, Want: "bad_type", Stmt: lettersInNum},
			{Name: "lock_timeout", Kind: sqlerr.KindRetryable, Want: "lock_timeout", Contend: true},
			{Name: "undefined_table", Kind: sqlerr.KindNone, Stmt: missingTable},
			{Name: "access_denied", Kind: sqlerr.KindNone,
				Connect: pgWith(dsn, func(u *url.URL) {
					u.User = url.UserPassword(u.User.Username(), "not-the-password")
				})},
			{Name: "connect_failure", Kind: sqlerr.KindNone,
				Connect: pgWith(dsn, func(u *url.URL) { u.Host = closedPort })},
		},
	}
}

// mysqlish covers MySQL and MariaDB, which take identical DDL and identical
// statements and then answer three of them differently. That is the reason they
// are two entries in this file and two parsers later.
func mysqlish(name, dsn string) Engine {
	return Engine{
		Name:    name,
		Dialect: crud.MySQL{},
		Driver:  "mysql",
		Pkg:     "github.com/go-sql-driver/mysql",
		DSN:     dsn,
		version: "SELECT VERSION()",
		schema: []string{
			`DROP TABLE IF EXISTS cp_child`,
			`DROP TABLE IF EXISTS cp_parent`,
			`CREATE TABLE cp_parent (
				id    BIGINT     NOT NULL PRIMARY KEY,
				slug  VARCHAR(8) NOT NULL UNIQUE,
				need  VARCHAR(8) NOT NULL,
				num   INT            NULL,
				small SMALLINT       NULL,
				a     VARCHAR(8)     NULL,
				b     VARCHAR(8)     NULL,
				CONSTRAINT cp_ab  UNIQUE (a, b),
				CONSTRAINT cp_pos CHECK (num IS NULL OR num >= 0)
			) ENGINE=InnoDB`,
			`CREATE TABLE cp_child (
				id     BIGINT NOT NULL PRIMARY KEY,
				parent BIGINT     NULL,
				CONSTRAINT cp_fk FOREIGN KEY (parent) REFERENCES cp_parent (id)
			) ENGINE=InnoDB`,
		},
		seed:   seed,
		Waiter: []string{`SET SESSION innodb_lock_wait_timeout = 1`},
		Lock:   lockRow,
		Cases: []Probe{
			{Name: "unique", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupSlug},
			{Name: "unique_composite", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupPair},
			{Name: "primary_key", Kind: sqlerr.KindIntegrity, Want: "primary_key", Stmt: dupID},
			{Name: "foreign_key", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Stmt: orphanChild},
			{Name: "restrict", Kind: sqlerr.KindIntegrity, Want: "restrict", Stmt: deleteReferenced},
			{Name: "not_null", Kind: sqlerr.KindIntegrity, Want: "not_null", Stmt: explicitNull},
			{Name: "check", Kind: sqlerr.KindIntegrity, Want: "check", Stmt: negativeNum},
			{Name: "missing_default", Kind: sqlerr.KindIntegrity, Want: "missing_default", Stmt: omitNeed},
			{Name: "too_long", Kind: sqlerr.KindData, Want: "too_long", Stmt: overlongSlug},
			{Name: "out_of_range", Kind: sqlerr.KindData, Want: "out_of_range", Stmt: hugeSmall},
			{Name: "bad_type", Kind: sqlerr.KindData, Want: "bad_type", Stmt: lettersInNum},
			{Name: "lock_timeout", Kind: sqlerr.KindRetryable, Want: "lock_timeout", Contend: true},
			{Name: "undefined_table", Kind: sqlerr.KindNone, Stmt: missingTable},
			// The server's own catalogue, which this user has no rights on. It
			// needs no privileged setup, which a GRANT would.
			{Name: "access_denied", Kind: sqlerr.KindNone,
				Connect: mysqlDatabase(dsn, "mysql")},
			{Name: "connect_failure", Kind: sqlerr.KindNone,
				Connect: mysqlAddr(dsn, closedPort)},
		},
	}
}

func sqlite(dsn string) Engine {
	return Engine{
		Name:    "sqlite",
		Dialect: crud.SQLite{},
		Driver:  "sqlite",
		Pkg:     "modernc.org/sqlite",
		DSN:     dsn,
		version: "SELECT sqlite_version()",
		schema: []string{
			`CREATE TABLE cp_parent (
				id    INTEGER    PRIMARY KEY,
				slug  VARCHAR(8) NOT NULL UNIQUE,
				need  VARCHAR(8) NOT NULL,
				num   INTEGER        NULL,
				small INTEGER        NULL,
				a     VARCHAR(8)     NULL,
				b     VARCHAR(8)     NULL,
				CONSTRAINT cp_ab  UNIQUE (a, b),
				CONSTRAINT cp_pos CHECK (num IS NULL OR num >= 0)
			)`,
			`CREATE TABLE cp_child (
				id     INTEGER PRIMARY KEY,
				parent INTEGER     NULL,
				CONSTRAINT cp_fk FOREIGN KEY (parent) REFERENCES cp_parent (id)
			)`,
		},
		seed: seed,
		// SQLite has no row locks: a writer locks the database, so contention
		// is two writers rather than two readers of one row.
		Lock: `UPDATE cp_parent SET need = 'z' WHERE id = 1`,
		Cases: []Probe{
			{Name: "unique", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupSlug},
			{Name: "unique_composite", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupPair},
			{Name: "primary_key", Kind: sqlerr.KindIntegrity, Want: "primary_key", Stmt: dupID},
			{Name: "foreign_key", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Stmt: orphanChild},
			{Name: "restrict", Kind: sqlerr.KindIntegrity, Want: "restrict", Stmt: deleteReferenced},
			{Name: "not_null", Kind: sqlerr.KindIntegrity, Want: "not_null", Stmt: explicitNull},
			{Name: "check", Kind: sqlerr.KindIntegrity, Want: "check", Stmt: negativeNum},
			{Name: "missing_default", Kind: sqlerr.KindIntegrity, Want: "not_null", Stmt: omitNeed},
			// SQLite does not enforce a declared width, a declared range or a
			// declared type. The same three payloads that are 422 on the other
			// engines are 200 here, and the row is stored as sent. That is an
			// observable dialect difference and it belongs in the file.
			{Name: "too_long", Kind: sqlerr.KindNone, Stmt: overlongSlug,
				Unreachable: "SQLite ignores a declared VARCHAR width and stores the value whole"},
			{Name: "out_of_range", Kind: sqlerr.KindNone, Stmt: hugeSmall,
				Unreachable: "SQLite has no fixed-width integer columns, so nothing is out of range"},
			{Name: "bad_type", Kind: sqlerr.KindNone, Stmt: lettersInNum,
				Unreachable: "SQLite type affinity keeps a string that will not convert, rather than refusing it"},
			{Name: "lock_timeout", Kind: sqlerr.KindRetryable, Want: "lock_timeout", Contend: true},
			{Name: "undefined_table", Kind: sqlerr.KindNone, Stmt: missingTable},
			{Name: "access_denied", Kind: sqlerr.KindNone,
				Unreachable: "SQLite has no users; the file's permissions are the whole gate"},
			{Name: "connect_failure", Kind: sqlerr.KindNone,
				Connect: "/nonexistent-directory/cp.db"},
		},
	}
}

// The statements, shared because they are the same in all four dialects. Every
// id is distinct so that a case an engine accepts leaves a row that cannot
// collide with a later one.
const (
	dupSlug          = `INSERT INTO cp_parent (id, slug, need) VALUES (10, 'anchor', 'y')`
	dupPair          = `INSERT INTO cp_parent (id, slug, need, a, b) VALUES (11, 's11', 'y', 'p', 'q')`
	dupID            = `INSERT INTO cp_parent (id, slug, need) VALUES (1, 's1', 'y')`
	orphanChild      = `INSERT INTO cp_child (id, parent) VALUES (12, 424242)`
	deleteReferenced = `DELETE FROM cp_parent WHERE id = 1`
	explicitNull     = `INSERT INTO cp_parent (id, slug, need) VALUES (13, 's13', NULL)`
	negativeNum      = `INSERT INTO cp_parent (id, slug, need, num) VALUES (14, 's14', 'x', -1)`
	omitNeed         = `INSERT INTO cp_parent (id, slug) VALUES (15, 's15')`
	overlongSlug     = `INSERT INTO cp_parent (id, slug, need) VALUES (16, 'far-too-long-for-the-column', 'x')`
	hugeSmall        = `INSERT INTO cp_parent (id, slug, need, small) VALUES (17, 's17', 'x', 99999)`
	lettersInNum     = `INSERT INTO cp_parent (id, slug, need, num) VALUES (18, 's18', 'x', 'abc')`
	missingTable     = `SELECT id FROM cp_nope`

	lockRow = `SELECT id FROM cp_parent WHERE id = 1 FOR UPDATE`
)

var seed = []string{
	`INSERT INTO cp_parent (id, slug, need, a, b) VALUES (1, 'anchor', 'x', 'p', 'q')`,
	`INSERT INTO cp_child (id, parent) VALUES (1, 1)`,
}

// closedPort is a port nothing listens on. Port 1 is reserved and unprivileged
// processes cannot bind it, so this cannot accidentally reach something real.
const closedPort = "127.0.0.1:1"

// pgWith rewrites part of a PostgreSQL URL.
func pgWith(dsn string, fn func(*url.URL)) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	fn(u)
	return u.String()
}

// mysqlDatabase rewrites the database in a go-sql-driver DSN, which is not a
// URL and cannot be parsed as one: the name sits between the last slash and the
// parameters.
func mysqlDatabase(dsn, name string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return dsn
	}
	rest := dsn[slash+1:]
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		return dsn[:slash+1] + name + rest[q:]
	}
	return dsn[:slash+1] + name
}

// mysqlAddr rewrites the address inside tcp(…).
func mysqlAddr(dsn, addr string) string {
	open := strings.Index(dsn, "tcp(")
	if open < 0 {
		return dsn
	}
	close := strings.IndexByte(dsn[open:], ')')
	if close < 0 {
		return dsn
	}
	return dsn[:open+4] + addr + dsn[open+close:]
}

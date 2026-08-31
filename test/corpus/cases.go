package corpus

import (
	"net/url"
	"strings"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs/sqlerr"
)

type Probe struct {
	Name, Kind, Want string
	Stmt             string

	Connect string

	Contend bool

	Session []string

	Tx []string

	RaceA, RaceB []string

	Volatile []string

	Unreachable string
}

type Engine struct {
	Name        string
	Driver, Pkg string
	DSN         string

	Dialect      crud.Dialect
	version      string
	schema, seed []string
	Cases        []Probe

	Waiter, Restore []string
	Lock            string
}

func postgres(dsn string) Engine {
	return Engine{
		Name:    "postgres",
		Dialect: crud.Postgres{},
		Driver:  "pgx",
		Pkg:     "github.com/jackc/pgx/v5/stdlib",
		DSN:     dsn,
		version: "SELECT version()",
		schema: []string{
			`DROP TABLE IF EXISTS cp_deferred`,
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

			`CREATE TABLE cp_deferred (
				id     BIGINT PRIMARY KEY,
				parent BIGINT     NULL,
				CONSTRAINT cp_dfk FOREIGN KEY (parent) REFERENCES cp_parent (id)
					DEFERRABLE INITIALLY DEFERRED
			)`,
		},
		seed:    seed,
		Waiter:  []string{`SET lock_timeout = '200ms'`},
		Restore: []string{`RESET lock_timeout`},
		Lock:    lockRow,
		Cases: []Probe{
			{Name: "unique", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupSlug},
			{Name: "unique_composite", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupPair},
			{Name: "primary_key", Kind: sqlerr.KindIntegrity, Want: "primary_key", Stmt: dupID},
			{Name: "foreign_key", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Stmt: orphanChild},

			{Name: "restrict", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Stmt: deleteReferenced},
			{Name: "not_null", Kind: sqlerr.KindIntegrity, Want: "not_null", Stmt: explicitNull},
			{Name: "check", Kind: sqlerr.KindIntegrity, Want: "check", Stmt: negativeNum},

			{Name: "missing_default", Kind: sqlerr.KindIntegrity, Want: "not_null", Stmt: omitNeed},
			{Name: "too_long", Kind: sqlerr.KindData, Want: "too_long", Stmt: overlongSlug},
			{Name: "out_of_range", Kind: sqlerr.KindData, Want: "out_of_range", Stmt: hugeSmall},
			{Name: "bad_type", Kind: sqlerr.KindData, Want: "bad_type", Stmt: lettersInNum},
			{Name: "lock_timeout", Kind: sqlerr.KindRetryable, Want: "lock_timeout", Contend: true},

			{Name: "deadlock", Kind: sqlerr.KindRetryable, Want: "deadlock",
				RaceA: deadlockA, RaceB: deadlockB, Volatile: []string{"Detail"}},
			{Name: "serialization_failure", Kind: sqlerr.KindRetryable, Want: "serialization_failure",
				RaceA: []string{`BEGIN ISOLATION LEVEL SERIALIZABLE`, readTwo, writeOne, commitStep},
				RaceB: []string{`BEGIN ISOLATION LEVEL SERIALIZABLE`, readOne, writeTwo, commitStep}},
			{Name: "transaction_aborted", Kind: sqlerr.KindRetryable, Want: "transaction_aborted", Tx: poisoned},
			{Name: "deferred_constraint", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Tx: deferred},
			{Name: "unique_in_another_locale", Kind: sqlerr.KindNone,
				Unreachable: "postgres:17-alpine is built without NLS, so it accepts lc_messages and answers in English anyway"},
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
		seed:    seed,
		Waiter:  []string{`SET SESSION innodb_lock_wait_timeout = 1`},
		Restore: []string{`SET SESSION innodb_lock_wait_timeout = DEFAULT`},
		Lock:    lockRow,
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
			{Name: "deadlock", Kind: sqlerr.KindRetryable, Want: "deadlock",
				RaceA: deadlockA, RaceB: deadlockB},

			{Name: "serialization_failure", Kind: sqlerr.KindRetryable, Want: "deadlock",
				RaceA: []string{`SET TRANSACTION ISOLATION LEVEL SERIALIZABLE`, `BEGIN`, readTwo, writeOne, commitStep},
				RaceB: []string{`SET TRANSACTION ISOLATION LEVEL SERIALIZABLE`, `BEGIN`, readOne, writeTwo, commitStep}},
			{Name: "transaction_aborted", Kind: sqlerr.KindNone, Tx: poisoned,
				Unreachable: "the statement rolls back and the transaction stays usable; only PostgreSQL poisons the whole of it"},
			{Name: "deferred_constraint", Kind: sqlerr.KindNone,
				Unreachable: "no deferrable constraints: DEFERRABLE is parsed and ignored, so a foreign key is always checked per statement"},
			{Name: "unique_in_another_locale", Kind: sqlerr.KindIntegrity, Want: "unique",
				Session: []string{russian}, Stmt: dupSlug},
			{Name: "undefined_table", Kind: sqlerr.KindNone, Stmt: missingTable},

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
			`CREATE TABLE cp_deferred (
				id     INTEGER PRIMARY KEY,
				parent INTEGER     NULL,
				CONSTRAINT cp_dfk FOREIGN KEY (parent) REFERENCES cp_parent (id)
					DEFERRABLE INITIALLY DEFERRED
			)`,
		},
		seed: seed,

		Lock: `UPDATE cp_parent SET need = 'z' WHERE id = 1`,
		Cases: []Probe{
			{Name: "unique", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupSlug},
			{Name: "unique_composite", Kind: sqlerr.KindIntegrity, Want: "unique", Stmt: dupPair},
			{Name: "primary_key", Kind: sqlerr.KindIntegrity, Want: "primary_key", Stmt: dupID},
			{Name: "foreign_key", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Stmt: orphanChild},

			{Name: "restrict", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Stmt: deleteReferenced},
			{Name: "not_null", Kind: sqlerr.KindIntegrity, Want: "not_null", Stmt: explicitNull},
			{Name: "check", Kind: sqlerr.KindIntegrity, Want: "check", Stmt: negativeNum},
			{Name: "missing_default", Kind: sqlerr.KindIntegrity, Want: "not_null", Stmt: omitNeed},

			{Name: "too_long", Kind: sqlerr.KindNone, Stmt: overlongSlug,
				Unreachable: "SQLite ignores a declared VARCHAR width and stores the value whole"},
			{Name: "out_of_range", Kind: sqlerr.KindNone, Stmt: hugeSmall,
				Unreachable: "SQLite has no fixed-width integer columns, so nothing is out of range"},
			{Name: "bad_type", Kind: sqlerr.KindNone, Stmt: lettersInNum,
				Unreachable: "SQLite type affinity keeps a string that will not convert, rather than refusing it"},
			{Name: "lock_timeout", Kind: sqlerr.KindRetryable, Want: "lock_timeout", Contend: true},

			{Name: "deadlock", Kind: sqlerr.KindRetryable, Want: "lock_timeout",
				RaceA: deadlockA, RaceB: deadlockB},
			{Name: "serialization_failure", Kind: sqlerr.KindRetryable, Want: "lock_timeout",
				RaceA: []string{`BEGIN`, readTwo, writeOne, commitStep},
				RaceB: []string{`BEGIN`, readOne, writeTwo, commitStep}},
			{Name: "transaction_aborted", Kind: sqlerr.KindNone, Tx: poisoned,
				Unreachable: "the statement rolls back and the transaction stays usable; only PostgreSQL poisons the whole of it"},
			{Name: "deferred_constraint", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Tx: deferred},
			{Name: "unique_in_another_locale", Kind: sqlerr.KindNone,
				Unreachable: "SQLite ships one set of English strings and has no locale setting to change them"},
			{Name: "undefined_table", Kind: sqlerr.KindNone, Stmt: missingTable},
			{Name: "access_denied", Kind: sqlerr.KindNone,
				Unreachable: "SQLite has no users; the file's permissions are the whole gate"},
			{Name: "connect_failure", Kind: sqlerr.KindNone,
				Connect: "/nonexistent-directory/cp.db"},
		},
	}
}

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
	orphanDeferred   = `INSERT INTO cp_deferred (id, parent) VALUES (20, 424242)`

	lockRow = `SELECT id FROM cp_parent WHERE id = 1 FOR UPDATE`

	commitStep = `COMMIT`

	russian = `SET lc_messages = 'ru_RU'`
)

var seed = []string{
	`INSERT INTO cp_parent (id, slug, need, a, b) VALUES (1, 'anchor', 'x', 'p', 'q')`,

	`INSERT INTO cp_parent (id, slug, need) VALUES (2, 's2', 'x')`,
	`INSERT INTO cp_child (id, parent) VALUES (1, 1)`,
}

var (
	deadlockA = []string{`BEGIN`, `UPDATE cp_parent SET need = 'a' WHERE id = 1`,
		`UPDATE cp_parent SET need = 'a' WHERE id = 2`, `COMMIT`}
	deadlockB = []string{`BEGIN`, `UPDATE cp_parent SET need = 'b' WHERE id = 2`,
		`UPDATE cp_parent SET need = 'b' WHERE id = 1`, `COMMIT`}

	readOne  = `SELECT need FROM cp_parent WHERE id = 1`
	readTwo  = `SELECT need FROM cp_parent WHERE id = 2`
	writeOne = `UPDATE cp_parent SET need = 'a' WHERE id = 1`
	writeTwo = `UPDATE cp_parent SET need = 'b' WHERE id = 2`

	poisoned = []string{dupSlug, `SELECT 1`}

	deferred = []string{orphanDeferred, commitStep}
)

const closedPort = "127.0.0.1:1"

func pgWith(dsn string, fn func(*url.URL)) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	fn(u)
	return u.String()
}

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

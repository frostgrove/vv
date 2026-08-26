package corpus

import (
	"net/url"
	"strings"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs/sqlerr"
)

// A probe is one violation, deliberately provoked.
//
// One shape is set — Stmt, Connect, Contend, Tx or RaceA/RaceB — except for a
// case an engine cannot reach at all, which may set none and explain itself
// instead.
type Probe struct {
	Name, Kind, Want string
	Stmt             string
	// Connect is a DSN this case needs a handle of its own for, because what it
	// provokes happens before any statement runs. A repository bound to it fails
	// on its first call, so these reach the classifier the same way the
	// statement cases do and belong in the same table.
	Connect string
	// Contend marks the case that needs two connections: one holds a row and
	// the other waits for it past its patience. What they run is Engine.Lock.
	Contend bool
	// Session runs on a connection of the probe's own before Stmt, and Stmt then
	// runs on that same connection. A locale set on a pooled connection is not
	// the one the statement gets, and worse, it outlives the probe: the first
	// capture recorded the undefined-table message in Russian two cases later.
	// The handle is opened and closed for this probe alone.
	Session []string
	// Tx runs every statement inside one transaction, and the error recorded is
	// the *last* step's, not the first non-nil one. Both cases that need this
	// are interesting for what happens after something already failed: 25P02 is
	// the second statement's refusal, and a deferred constraint is the commit's.
	// A step spelled COMMIT is the commit rather than a statement.
	Tx []string
	// RaceA and RaceB are two scripts run on two connections, in lock step: both
	// sides finish statement N before either starts N+1. Contention that depends
	// on scheduling is what [[D-040]] said kept a deadlock out of the corpus; a
	// rendezvous between every pair of statements is what makes it fire every
	// run instead of one run in ten.
	RaceA, RaceB []string
	// Volatile names structured fields whose value changes every run —
	// PostgreSQL's deadlock DETAIL carries the backend pids. The field keeps its
	// name and loses its value, because the name is what SameKey compares and
	// the value is what would break Save's byte-identical promise.
	Volatile []string
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
	// engine's default patience down from seconds. Restore puts it back, and is
	// not optional: the connection goes back to the pool with the setting still
	// on it, and the deadlock race two cases later then loses to a 200ms lock
	// timeout instead of forming a cycle. That was measured, not foreseen — the
	// first capture recorded 55P03 under the name deadlock.
	// Lock is what both sides run.
	Waiter, Restore []string
	Lock            string
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
			// A second child table rather than a deferrable cp_fk. Making the
			// existing key deferrable would move the foreign_key and restrict
			// captures to commit time, and two entries would change meaning
			// without their names or their statements saying so.
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
			// Both foreign-key directions are 23503 with the same constraint,
			// the same fields and the same key. Only the localised Detail
			// separates them, and reading it is what [[D-039]] forbids — so the
			// class a parser must produce is foreign_key, and the direction is
			// owed to whichever phase sets Fault.Op.
			{Name: "restrict", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Stmt: deleteReferenced},
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
			// The DETAIL names the two backend pids and the two transaction
			// ids, so it is different every run and the field is redacted.
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
			// InnoDB's SERIALIZABLE turns a plain SELECT into a shared lock, so
			// a write skew reaches the engine as a lock cycle and comes back as
			// 1213. The case keeps its name — it is the same question — and its
			// Want records the answer this engine gives rather than the one the
			// name suggests.
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
			`CREATE TABLE cp_deferred (
				id     INTEGER PRIMARY KEY,
				parent INTEGER     NULL,
				CONSTRAINT cp_dfk FOREIGN KEY (parent) REFERENCES cp_parent (id)
					DEFERRABLE INITIALLY DEFERRED
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
			// Like PostgreSQL, and for the same reason: both directions are ext
			// 787 and the message says only "FOREIGN KEY constraint failed".
			{Name: "restrict", Kind: sqlerr.KindIntegrity, Want: "foreign_key", Stmt: deleteReferenced},
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
			// SQLite has one writer, so a lock cycle cannot form: the second
			// writer waits out its busy timeout and gets SQLITE_BUSY. The two
			// cases below run the same choreography as the servers and record
			// the answer this engine gives, which is the same 5 lock_timeout
			// already carries.
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
	orphanDeferred   = `INSERT INTO cp_deferred (id, parent) VALUES (20, 424242)`

	lockRow = `SELECT id FROM cp_parent WHERE id = 1 FOR UPDATE`

	// commitStep is a script step that commits rather than executes. A deferred
	// constraint fires here and nowhere else, so a transaction script that could
	// not name the commit could not reach the violation at all.
	commitStep = `COMMIT`

	// The Russian locale, on the two engines that were measured to honour it.
	// The value is the one MySQL and MariaDB ship a message catalogue for; a
	// locale they do not know is refused rather than ignored, which is why the
	// capture treats a failed session statement as a finding.
	russian = `SET lc_messages = 'ru_RU'`
)

var seed = []string{
	`INSERT INTO cp_parent (id, slug, need, a, b) VALUES (1, 'anchor', 'x', 'p', 'q')`,
	// A deadlock needs two rows each side can take before either reaches for the
	// other's. Ids 10 to 18 are spoken for by the statements above, and a case
	// this row let through would collide with one of them.
	`INSERT INTO cp_parent (id, slug, need) VALUES (2, 's2', 'x')`,
	`INSERT INTO cp_child (id, parent) VALUES (1, 1)`,
}

// The two race scripts, in lock step: both sides finish statement N before
// either starts N+1. Each side takes one row, and only then reaches for the row
// the other is holding — which is what makes the cycle certain rather than
// likely.
var (
	deadlockA = []string{`BEGIN`, `UPDATE cp_parent SET need = 'a' WHERE id = 1`,
		`UPDATE cp_parent SET need = 'a' WHERE id = 2`, `COMMIT`}
	deadlockB = []string{`BEGIN`, `UPDATE cp_parent SET need = 'b' WHERE id = 2`,
		`UPDATE cp_parent SET need = 'b' WHERE id = 1`, `COMMIT`}

	// A write skew: each side reads the row the other is about to write. Under
	// anything weaker than SERIALIZABLE both commit and the pair ends in a state
	// neither transaction would have allowed, so the isolation level is the
	// probe rather than a detail of it. The statements are shared; how each
	// engine is told to be serialisable is not, and sits in its own table.
	readOne  = `SELECT need FROM cp_parent WHERE id = 1`
	readTwo  = `SELECT need FROM cp_parent WHERE id = 2`
	writeOne = `UPDATE cp_parent SET need = 'a' WHERE id = 1`
	writeTwo = `UPDATE cp_parent SET need = 'b' WHERE id = 2`

	// The second statement is the one that matters: on PostgreSQL the first
	// failure poisons the whole transaction and everything after it is 25P02.
	poisoned = []string{dupSlug, `SELECT 1`}

	// The insert is accepted and the commit is refused. Nothing about the
	// statement says which, which is the whole reason the entry exists.
	deferred = []string{orphanDeferred, commitStep}
)

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

package sqlfault

import (
	"strings"

	"github.com/shardit-io/vv/errs/sqlerr"
)

// mysqlIntegrityNumbers are integrity violations MySQL reports as HY000 — its
// "no more specific state" code — rather than as class 23. Measured on MySQL
// 8.4.11; both are silent 500s without this, because the state classifies
// neither, while PostgreSQL reports the same two conditions as 23514 and 23502.
//
// MariaDB needs no entry. It was measured too: a failed CHECK there is 4025 with
// SQLSTATE 23000, which the class arm already covers.
var mysqlIntegrityNumbers = map[uint64]bool{
	3819: true, // ER_CHECK_CONSTRAINT_VIOLATED
	1364: true, // ER_NO_DEFAULT_FOR_FIELD
}

// sqliteConstraint is SQLITE_CONSTRAINT. Every constraint violation carries it
// in the low byte of an extended result code — 2067 for unique, 787 for a
// foreign key, 1299 for NOT NULL, 275 for CHECK — so the low byte is the test
// and the subcodes need no list. An extended code is not interchangeable with a
// primary one: SQLITE_BUSY is 5, busy-snapshot is 517.
const sqliteConstraint = 19

// Integrity answers whether the driver is describing a constraint the database
// refused to break. It is the gate for crud.ErrConflict and nothing else decides
// that sentinel.
//
// Three arms, because the four engines answer in three different ways, and the
// state is what selects between them rather than what decides:
//
//   - Class 23 is the portable half, and PostgreSQL's whole answer.
//   - HY000 is MySQL saying it has nothing more specific. Its CHECK and
//     missing-default errors land there, so the number is the only thing
//     separating them from an ordinary server error.
//   - No state at all is SQLite, which has no SQLSTATE and never will. Every one
//     of its constraint violations was a bare 500 until this arm existed — seven
//     classes on a shipped dialect, because the one test that would have caught
//     it runs over a target list SQLite is not on.
//
// It stays wider than sqlerr.Classify on purpose. A class-23 number nobody
// provoked — 1216 and 1217 are two — and a low-byte-19 code whose high byte no
// probe produced are conflicts here and unclassified there, so such a violation
// reaches a caller as a 409 with no code rather than as a 500 ([[D-046]]).
//
// The no-state arm reads a number of its own — [sqliteNative] — and not the
// merged sqlerr.Err.Native. It is the one arm with no engine behind it, and
// "MySQL always carries a state" is not true of the driver: the SQLSTATE is
// optional in MySQL's ERR packet and go-sql-driver/mysql leaves the [5]byte
// unset when the '#' marker is absent, so 1043 — ER_HANDSHAKE_ERROR, 19 in its
// low byte — would answer a refused handshake with 409 and the driver's sentence
// in the body. A number is read only once something has said whose it is
// ([[D-046]]).
func Integrity(err error) bool { return integrity(Extract(err), sqliteNative(err)) }

// integrity takes the SQLite arm's number separately because a *sqlerr.Err has
// nowhere to keep the provenance: one Native field holds whichever spelling was
// found, and the corpus is written against that.
func integrity(e *sqlerr.Err, sqlite uint64) bool {
	if e == nil {
		return false
	}
	switch {
	case strings.HasPrefix(e.SQLState, "23"):
		return true
	case e.SQLState == "HY000":
		return mysqlIntegrityNumbers[e.Native]
	case e.SQLState == "":
		return sqlite&0xff == sqliteConstraint
	}
	return false
}

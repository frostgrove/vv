package sqlerr

import "github.com/shardit-io/vv/errs"

// The two primary result codes this table reads, in the low byte of whatever
// the driver reported.
//
// An extended result code is never compared whole. Every SQLITE_CONSTRAINT_* is
// 19 | (n<<8), so the low byte says what kind of failure it is and the high byte
// says which one — and comparing the whole number is how busy-snapshot (517)
// becomes a constraint violation the day the codes shift ([[D-046]]). 517 needs
// no row here: its low byte is 5 and the rule covers it.
const (
	sqliteConstraint = 19 // SQLITE_CONSTRAINT
	sqliteBusy       = 5  // SQLITE_BUSY, and 517 SQLITE_BUSY_SNAPSHOT above it
)

// sqliteConstraints selects on the high byte, once the low byte has said this
// is a constraint at all. Each row names the extended code the corpus captured.
var sqliteConstraints = map[uint64]errs.Code{
	8: errs.CodeUnique,     // 2067 SQLITE_CONSTRAINT_UNIQUE — unique, unique_composite
	6: errs.CodeUnique,     // 1555 SQLITE_CONSTRAINT_PRIMARYKEY — primary_key
	3: errs.CodeForeignKey, // 787  SQLITE_CONSTRAINT_FOREIGNKEY — foreign_key, restrict, deferred_constraint
	5: errs.CodeRequired,   // 1299 SQLITE_CONSTRAINT_NOTNULL — not_null, missing_default
	1: errs.CodeCheck,      // 275  SQLITE_CONSTRAINT_CHECK — check
}

// sqlite reads the number and only the number, and refuses anything carrying a
// SQLSTATE at all.
//
// The guard is not defensive tidiness. pgconn spells the SQLSTATE in a field
// named Code, so an extractor asking by shape can hand a PostgreSQL error here
// with a number that means nothing; requiring the absence of a state before any
// number is read is what [[D-046]] means by "a number is only trusted once the
// state has said which engine is speaking", and it is the same guard
// adapter/crudsql keeps.
//
// A constraint subcode the corpus never produced stays unclassified rather than
// being guessed, and that has a cost worth naming: sqlfault.Integrity calls
// anything with 19 in the low byte a conflict, so such a code reaches a caller
// as crud.ErrConflict with no code at all. That is what phase 3 decided — the
// sentinel gate stays the wider of the two, because narrowing it onto this table
// would turn a duplicate key into a 500 on a subcode nobody has produced
// ([[D-046]]). TestTheTwoGatesAnswerDifferentQuestions in sqlfault/gate_test.go
// is where the divergence is written down, one case per cell of the 2x2.
func sqlite(e *Err) (errs.Code, errs.Source, bool) {
	if e.SQLState != "" {
		return "", errs.Source{}, false
	}
	switch e.Native & 0xff {
	case sqliteConstraint:
		code, ok := sqliteConstraints[e.Native>>8]
		if !ok {
			return "", errs.Source{}, false
		}
		return code, errs.Source{}, true
	case sqliteBusy:
		return errs.CodeLockTimeout, errs.Source{}, true
	}
	return "", errs.Source{}, false
}

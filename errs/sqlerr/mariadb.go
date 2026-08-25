package sqlerr

import "github.com/shardit-io/vv/errs"

// mariadbKeys is MariaDB 11.4.12, every row provoked and captured.
//
// It agrees with mysqlKeys on nine rows and disagrees on two, and the two are
// the whole reason this file exists:
//
//   - a failed CHECK is 4025 under 23000 here and 3819 under HY000 there;
//   - a bad value for a column is 1366 under 22007 here and 1366 under HY000
//     there — the same number, a different class.
//
// The nine it agrees on are written out rather than shared. Where two engines
// agree the repetition costs a few lines; where they disagree the difference is
// visible on the page instead of hidden in a conditional — the argument
// test/corpus/cases.go already made for the same pair. Merging the tables is
// green on every test in this package but one, and that one exists for this.
var mariadbKeys = map[key]errs.Code{
	{"23000", 1062}: errs.CodeUnique,        // unique, unique_composite, primary_key
	{"23000", 1452}: errs.CodeForeignKey,    // foreign_key
	{"23000", 1451}: errs.CodeRestrict,      // restrict
	{"23000", 1048}: errs.CodeRequired,      // not_null
	{"23000", 4025}: errs.CodeCheck,         // check — MySQL says HY000/3819
	{"HY000", 1364}: errs.CodeRequired,      // missing_default
	{"22007", 1366}: errs.CodeInvalidFormat, // bad_type — MySQL says HY000/1366
	{"HY000", 1205}: errs.CodeLockTimeout,   // lock_timeout
	{"22001", 1406}: errs.CodeTooLong,       // too_long
	{"22003", 1264}: errs.CodeOutOfRange,    // out_of_range
	{"40001", 1213}: errs.CodeDeadlock,      // deadlock, serialization_failure
}

// mariadb classifies on the pair, and answers nothing for a state and number it
// was not provoked with. The Source is zero for the same reason MySQL's is: the
// driver is the same one and it carries no structured fields.
func mariadb(e *Err) (errs.Code, errs.Source, bool) {
	code, ok := mariadbKeys[key{e.SQLState, e.Native}]
	if !ok {
		return "", errs.Source{}, false
	}
	return code, errs.Source{}, true
}

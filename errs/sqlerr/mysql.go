package sqlerr

import "github.com/frostgrove/vv/errs"

// key is what MySQL and MariaDB are classified on: the SQLSTATE and the
// engine's own number together, never either alone.
//
// The pair is the key because HY000 is MySQL saying it has nothing more
// specific, and 1205 and 3819 both live there and mean opposite things — a
// lock timeout and a CHECK violation. A parser reading the number first answers
// check for a lock timeout, which is [[D-040]]'s fourth forbid. And the same
// number carries different classes between the two engines: 1366 is HY000 here
// and 22007 next door.
type key struct {
	state  string
	native uint64
}

// mysqlKeys is MySQL 8.4.11, every row provoked and captured. Its twin in
// mariadb.go differs at exactly two rows; see the header there for why the two
// are not one table.
var mysqlKeys = map[key]errs.Code{
	{"23000", 1062}: errs.CodeUnique,        // unique, unique_composite, primary_key
	{"23000", 1452}: errs.CodeForeignKey,    // foreign_key
	{"23000", 1451}: errs.CodeRestrict,      // restrict
	{"23000", 1048}: errs.CodeRequired,      // not_null
	{"HY000", 3819}: errs.CodeCheck,         // check
	{"HY000", 1364}: errs.CodeRequired,      // missing_default
	{"HY000", 1366}: errs.CodeInvalidFormat, // bad_type
	{"HY000", 1205}: errs.CodeLockTimeout,   // lock_timeout
	{"22001", 1406}: errs.CodeTooLong,       // too_long
	{"22003", 1264}: errs.CodeOutOfRange,    // out_of_range
	{"40001", 1213}: errs.CodeDeadlock,      // deadlock, serialization_failure
}

// mysql classifies on the pair and refuses everything else, including a class-23
// state whose number is not listed. 1216 and 1217 are two such, and they are
// left out on purpose: [[D-046]] forbids adding a number from documentation, so
// they wait for a probe that provokes them.
//
// The Source is always zero. mysql.MySQLError carries Number, SQLState and
// Message and nothing else, and every MySQL entry in the corpus records no
// fields at all. The table name is in the message, and taking it from there is
// the one thing [[D-039]] forbids outright.
func mysql(e *Err) (errs.Code, errs.Source, bool) {
	code, ok := mysqlKeys[key{e.SQLState, e.Native}]
	if !ok {
		return "", errs.Source{}, false
	}
	return code, errs.Source{}, true
}

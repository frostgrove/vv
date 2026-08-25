package sqlerr

import "github.com/shardit-io/vv/errs"

// postgres classifies on the SQLSTATE alone, and on the whole five characters
// rather than the class. That is the sentence [[D-046]] supersedes: 23503 and
// 23514 share a class and are different violations, and 25P02 shares a class
// with nothing else this table knows.
//
// The native number is never read. Every PostgreSQL entry in the corpus records
// zero for it — pgconn spells the SQLSTATE in a field called Code and has no
// number — so an arm keyed on one would be written from documentation, which is
// the thing the corpus exists to stop. It would also fire on a MySQL error
// routed here by a mis-wired dialect.
//
// Each row cites the corpus case that produced it.
var postgresStates = map[string]errs.Code{
	"23505": errs.CodeUnique,               // unique, unique_composite, primary_key
	"23503": errs.CodeForeignKey,           // foreign_key, restrict, deferred_constraint
	"23502": errs.CodeRequired,             // not_null, missing_default
	"23514": errs.CodeCheck,                // check
	"22001": errs.CodeTooLong,              // too_long
	"22003": errs.CodeOutOfRange,           // out_of_range
	"22P02": errs.CodeInvalidFormat,        // bad_type
	"55P03": errs.CodeLockTimeout,          // lock_timeout
	"40P01": errs.CodeDeadlock,             // deadlock
	"40001": errs.CodeSerializationFailure, // serialization_failure
	"25P02": errs.CodeTransactionAborted,   // transaction_aborted
}

// 23503 answers foreign_key for both directions and not restrict for one of
// them. A missing parent and a child still referring to the row are the same
// state with the same constraint, the same table and the same fields; the only
// thing that separates them is the localised Detail, and reading it is what
// [[D-039]] forbids. The direction has to come from the verb — see [[D-046]].
func postgres(e *Err) (errs.Code, errs.Source, bool) {
	code, ok := postgresStates[e.SQLState]
	if !ok {
		return "", errs.Source{}, false
	}
	return code, postgresSource(e.Fields), true
}

// postgresSource carries across the structural fields pgconn populated, and
// only those. Detail is not read even though it holds the offending value: the
// field it would fill is errs.Detail.Value — errs.Source has no Value — and
// filling it is best-effort enrichment. Phase 3 owns that enrichment and
// deliberately did not do it: Detail is the one field on this error the server
// localises ([[D-039]]), and echoing a value is off by deployment default
// anyway ([[D-044]]).
//
// Columns stays nil when the driver named no column. An empty slice would read
// as "no columns" where the truth is "not known" — the rule errs/build.go's
// cloneStrings already holds for the builder.
func postgresSource(f map[string]string) errs.Source {
	s := errs.Source{
		Constraint: f["ConstraintName"],
		Table:      f["TableName"],
		Schema:     f["SchemaName"],
	}
	if c := f["ColumnName"]; c != "" {
		s.Columns = []string{c}
	}
	return s
}

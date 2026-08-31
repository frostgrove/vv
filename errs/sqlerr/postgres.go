package sqlerr

import "github.com/frostgrove/vv/errs"

var postgresStates = map[string]errs.Code{
	"23505": errs.CodeUnique,
	"23503": errs.CodeForeignKey,
	"23502": errs.CodeRequired,
	"23514": errs.CodeCheck,
	"22001": errs.CodeTooLong,
	"22003": errs.CodeOutOfRange,
	"22P02": errs.CodeInvalidFormat,
	"55P03": errs.CodeLockTimeout,
	"40P01": errs.CodeDeadlock,
	"40001": errs.CodeSerializationFailure,
	"25P02": errs.CodeTransactionAborted,
	"42P01": errs.CodeSchemaNotReady,
}

func postgres(e *Err) (errs.Code, errs.Source, bool) {
	code, ok := postgresStates[e.SQLState]
	if !ok {
		return "", errs.Source{}, false
	}
	return code, postgresSource(e.Fields), true
}

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

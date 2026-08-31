package sqlerr

import "github.com/frostgrove/vv/errs"

type key struct {
	state  string
	native uint64
}

var mysqlKeys = map[key]errs.Code{
	{"23000", 1062}: errs.CodeUnique,
	{"23000", 1452}: errs.CodeForeignKey,
	{"23000", 1451}: errs.CodeRestrict,
	{"23000", 1048}: errs.CodeRequired,
	{"HY000", 3819}: errs.CodeCheck,
	{"HY000", 1364}: errs.CodeRequired,
	{"HY000", 1366}: errs.CodeInvalidFormat,
	{"HY000", 1205}: errs.CodeLockTimeout,
	{"22001", 1406}: errs.CodeTooLong,
	{"22003", 1264}: errs.CodeOutOfRange,
	{"40001", 1213}: errs.CodeDeadlock,
	{"42S02", 1146}: errs.CodeSchemaNotReady,
}

func mysql(e *Err) (errs.Code, errs.Source, bool) {
	code, ok := mysqlKeys[key{e.SQLState, e.Native}]
	if !ok {
		return "", errs.Source{}, false
	}
	return code, errs.Source{}, true
}

package sqlerr

import "github.com/frostgrove/vv/errs"

const (
	sqliteConstraint = 19
	sqliteBusy       = 5
)

var sqliteConstraints = map[uint64]errs.Code{
	8: errs.CodeUnique,
	6: errs.CodeUnique,
	3: errs.CodeForeignKey,
	5: errs.CodeRequired,
	1: errs.CodeCheck,
}

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

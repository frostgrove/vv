package sqlerr

import "github.com/frostgrove/vv/errs"

func Classify(dialect string, e *Err) (errs.Code, errs.Source, bool) {
	if e == nil {
		return "", errs.Source{}, false
	}
	switch dialect {
	case "postgres":
		return postgres(e)
	case "mysql":
		return mysql(e)
	case "mariadb":
		return mariadb(e)
	case "sqlite":
		return sqlite(e)
	}
	return "", errs.Source{}, false
}

package sqlfault

import (
	"strings"

	"github.com/frostgrove/vv/errs/sqlerr"
)

var mysqlIntegrityNumbers = map[uint64]bool{
	3819: true,
	1364: true,
}

const sqliteConstraint = 19

func Integrity(err error) bool { return integrity(Extract(err), sqliteNative(err)) }

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

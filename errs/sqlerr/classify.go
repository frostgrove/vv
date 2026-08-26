package sqlerr

import "github.com/frostgrove/vv/errs"

// Classify answers what one driver error is, from its key alone.
//
// dialect is one of "postgres", "mysql", "mariadb" and "sqlite" — the vocabulary
// errs.Detail.Dialect documents and [Corpus.Engine] carries. It is a plain
// string on purpose. A named type here would be a fifth collision with
// crud.Dialect and would buy nothing at the one seam where it matters, because
// crud.Dialect.Name() answers "mysql" for MariaDB and so cannot be the source of
// this argument ([[D-046]]).
//
// The third result is false when nothing in that dialect's table matches. A
// caller must not turn that into a code of its own: an unclassified error is a
// 500, and a guess is worse than silence.
//
// What no arm may read: [Err.Message], Fields["Detail"], Fields["Hint"] and
// [Err.Type]. Three of the four engines localise the first three through a
// session setting, so a parser that reads any of them classifies differently
// depending on where the server was deployed ([[D-039]]). The type is the
// capture's own evidence that an entry is real, not a key.
//
// It is total. An unknown dialect and a nil error both answer false rather than
// panicking, for the same reason a nil *errs.Codes reads as empty: a component
// whose wiring is wrong must degrade, not take the process down at the first
// error.
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

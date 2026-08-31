package corpus

import (
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs/sqlerr"
)

func capture(err error, volatile []string) *sqlerr.Err {
	e := sqlfault.Extract(err)
	if e == nil {
		return nil
	}
	for _, name := range volatile {
		if _, ok := e.Fields[name]; ok {
			e.Fields[name] = Redacted
		}
	}
	if len(e.Fields) == 0 {
		e.Fields = nil
	}
	return e
}

const Redacted = "(varies between runs)"

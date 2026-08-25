package corpus

import (
	"github.com/shardit-io/vv/crud/sqlfault"
	"github.com/shardit-io/vv/errs/sqlerr"
)

// capture flattens whatever a driver returned into a corpus entry.
//
// The flattening is sqlfault.Extract's and not this file's, and that is the
// point: the corpus supplies the expectations both adapters are tested against,
// so a second implementation of the same rule here could stay green while the
// shipped one was broken. It is the argument phase 6 made for crud.KeyOf, on the
// same kind of rule.
//
// What is left here is the two things only a capture needs: the volatile
// substitution, and re-normalising an emptied map back to nil.
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

// Redacted stands in for a value that is different every run. It is a fixed
// string rather than a dropped field, because the field *name* is what SameKey
// compares: dropping it would quietly narrow the key.
//
// A field named in a probe's Volatile list keeps its name and loses its value:
// PostgreSQL's deadlock DETAIL names the two backend pids, so recapturing would
// rewrite the file every time and a diff nobody reads is a guard that has
// stopped guarding. It costs one thing, and it is not free: a server that
// stopped emitting that field on that one row would no longer be a finding,
// because only the name is compared and the name is what is kept.
const Redacted = "(varies between runs)"

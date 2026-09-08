package eventpg

import (
	"database/sql/driver"
	"errors"
	"strings"

	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/errs/sqlerr"
	"github.com/frostgrove/vv/event"
)

const postgresDialect = "postgres"

// NotWritten is the strongest claim this store makes, so it is selected only on
// proof, and there are four of them: the joined path, where a statement that
// failed leaves a transaction PostgreSQL will now refuse to commit; nothing
// issued at all; a driver that answered driver.ErrBadConn, which its contract
// permits only when it is certain the server never saw the query; and a SQLSTATE
// the backend sent while staying alive to send it.
//
// Everything else is Unconfirmed, and the last line is deliberately not a
// default arm of the switch: a default answering NotWritten declares "certainly
// did not land" about every failure nobody thought of, which is the one lie a
// caller cannot recover from.
func outcomeOf(failure error, issued, joined bool) event.Outcome {
	switch {
	case joined:
		return event.NotWritten
	case !issued:
		return event.NotWritten
	case errors.Is(failure, driver.ErrBadConn):
		return event.NotWritten
	case backendSurvived(sqlfault.Extract(failure)):
		return event.NotWritten
	}
	return event.Unconfirmed
}

// Class 08 is the connection exception class, and 57P01, 57P02 and 57P03 arrive
// with the session ending. A code the backend sent while staying alive is proof
// it processed the statement, aborted it and rolled it back; a code that
// accompanies the connection dying is not, because the statement may already
// have committed when the FATAL was raised. Selection is the SQLSTATE's own,
// never whether sqlerr recognises it: 57014 is not in that table and is still a
// server's answer.
func backendSurvived(fault *sqlerr.Err) bool {
	if fault == nil || fault.SQLState == "" {
		return false
	}
	switch {
	case strings.HasPrefix(fault.SQLState, "08"):
		return false
	case fault.SQLState == "57P01", fault.SQLState == "57P02", fault.SQLState == "57P03":
		return false
	}
	return true
}

// The second spelling of retryability the kernel reads, built for the codes that
// carry it and for no others: contention the server resolved by refusing this
// statement says the caller's transaction is dead and must be retried whole,
// which is a different obligation from the one ErrConflict names. Three codes
// and not four — errs.CodeUnavailable carries the same kind and no PostgreSQL
// SQLSTATE maps to it, so a fourth arm would be one no case can walk. A switch
// and not errs.StandardCodes(), because a package-level table of codes is state
// this store does not need and only retryability is load-bearing at this seam.
func causeOf(failure error) error {
	code, _, recognised := sqlerr.Classify(postgresDialect, sqlfault.Extract(failure))
	if !recognised {
		return failure
	}
	switch code {
	case errs.CodeSerializationFailure, errs.CodeDeadlock, errs.CodeLockTimeout:
		return errs.Retryable().Code(code).Wrapping(failure).Fault()
	}
	return errs.Internal().Code(code).Wrapping(failure).Fault()
}

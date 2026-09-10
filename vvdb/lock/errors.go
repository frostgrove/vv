package lock

import "errors"

var (
	ErrNoTransaction = errors.New("lock: an advisory lock outside a transaction is released before the caller can use it")

	// The engine cannot be asked for a lock with these semantics, so it is refused rather than
	// answered with something weaker. crud.SQLite.LockClause returns "" for the row-lock case and
	// the statement is simply built without it; an advisory lock has no such reading — a caller
	// that got no lock and no error would run its critical section unprotected.
	ErrDialectUnsupported = errors.New("lock: the engine offers no advisory lock with these semantics")

	errNoRow = errors.New("lock: the engine answered no row")
)

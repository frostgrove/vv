package catalog

import "errors"

// The three ways loading a catalog refuses. They are values rather than text
// because a caller branches on them and comparing an error by string is how a
// branch stops working when a message is reworded.
//
// They live here and not with the crud sentinels because those are the failures
// a transport maps to a status ([[D-015]]); none of them is a status a caller
// branches on. Two happen at declaration, before any request exists. The third
// can also arrive from Reload, which runs at request time — no transport
// recognises it either, so it renders as a 500 with a silent body.
// errs.ErrCodeRedeclared is the standing precedent for a declaration-time
// sentinel in its own package.
//
// There is deliberately no "unidentified source" sentinel. crud.KeyOf takes a
// source that cannot name its database at face value and returns the source
// itself, so the condition never arises — a sentinel for it would be dead
// surface pointing the next reader at a branch that does not exist.
var (
	// ErrUncomparableHandle is a datasource identity that can be stored and
	// never found again. Refused when the catalog is declared, because the
	// alternative is a catalog that re-introspects on every lookup and looks
	// like it is working.
	ErrUncomparableHandle = errors.New("catalog: the datasource handle cannot be compared, so a catalog keyed on it could never be found again")

	// ErrUnknownDialect is a dialect no back-end serves. It fails at start-up
	// rather than degrading to an empty catalog, because an empty catalog reads
	// as "this database has no constraint problems".
	ErrUnknownDialect = errors.New("catalog: no introspection for this dialect")

	// ErrIntrospection is a statement the server refused — insufficient
	// privileges, a proxy blocking information_schema. It wraps the driver's
	// error and names nothing itself ([[D-044]]).
	ErrIntrospection = errors.New("catalog: the schema could not be read")
)

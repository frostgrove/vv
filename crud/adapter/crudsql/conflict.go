package crudsql

import "github.com/shardit-io/vv/crud/sqlfault"

// conflict classifies whatever the driver returned: crud.ErrConflict for a
// constraint the database refused to break, and an errs.Fault carrying the code
// and the constraint where this executor was given a classifier. The driver
// error is kept underneath either way, so whoever needs the SQLSTATE or the
// constraint name can still errors.As their way to it.
//
// The work is sqlfault's, and both adapters call the same function so one
// violation cannot be a 409 through database/sql and a 500 through pgx. This
// package may not name a driver's error type — the module has no dependencies,
// drivers included — so the shapes are reached by name and kind in
// sqlfault.Extract, which is where a new driver's spelling is added.
func (e Executor) conflict(err error) error { return sqlfault.Wrap(e.faults, err) }

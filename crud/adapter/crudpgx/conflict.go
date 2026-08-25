package crudpgx

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/shardit-io/vv/crud/sqlfault"
	"github.com/shardit-io/vv/errs/sqlerr"
)

// conflict classifies whatever pgx returned: crud.ErrConflict for a constraint
// PostgreSQL refused to break, and an errs.Fault carrying the code, the
// constraint and the table. The pgconn error is kept underneath, so a caller who
// wants the constraint name can still errors.As their way to it.
//
// The decision is sqlfault's and not this file's. crud/adapter/crudsql answers the
// same question over the same shapes, and the last time one rule lived in both
// adapters they diverged: a deferred constraint was a 409 through one and a 500
// through the other, with both test suites green.
func (e Executor) conflict(err error) error { return sqlfault.Wrap(e.faults, err) }

// extract reads *pgconn.PgError by name. This module may name it, so it does —
// a field pgx renames breaks the build here, where the by-shape reader in
// sqlfault goes quietly blank.
//
// The spellings are pgconn's own and they are not the obvious ones: there is no
// Column and no Table. Code holds the SQLSTATE as a string, and Native stays
// zero — pgconn has no number, every PostgreSQL entry in the corpus records
// zero, and errs/sqlerr/postgres.go is written against that.
//
// File, Line and Routine are excluded for the reason test/corpus excludes them:
// they name PostgreSQL's own C source and change when the server is rebuilt.
// Detail and Hint are carried and never read ([[D-039]]).
func extract(err error) *sqlerr.Err {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		// Not everything that reaches here is a PgError anywhere in its chain: a
		// dead connection is a *pgconn.ConnectError, which extracts to nothing and
		// classifies to nothing, and a second driver behind the same repository
		// spells its SQLSTATE somewhere only the by-shape reader looks. Without
		// this arm a state that is a 409 through database/sql is a 500 through
		// pgx. errors.As already unwraps, so a wrapping is not what this covers.
		return sqlfault.Extract(err)
	}
	e := &sqlerr.Err{
		Type:     "*pgconn.PgError",
		SQLState: pg.Code,
		Message:  pg.Message,
	}
	fields := map[string]string{
		"ConstraintName": pg.ConstraintName,
		"TableName":      pg.TableName,
		"SchemaName":     pg.SchemaName,
		"ColumnName":     pg.ColumnName,
		"DataTypeName":   pg.DataTypeName,
		"Detail":         pg.Detail,
		"Hint":           pg.Hint,
	}
	for name, v := range fields {
		if v == "" {
			delete(fields, name)
		}
	}
	if len(fields) > 0 {
		e.Fields = fields
	}
	return e
}

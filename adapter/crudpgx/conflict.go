package crudpgx

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"rx-crud/crud"
)

// conflict tags PostgreSQL's integrity errors as crud.ErrConflict, so a
// duplicate key reaches an HTTP client as 409 with a message rather than as a
// bare 500 that deliberately says nothing. The pgconn error is kept underneath,
// so a caller who wants the constraint name can still errors.As their way to it.
//
// SQLSTATE class 23 is integrity constraint violation: unique key, foreign key,
// NOT NULL and CHECK all live there, and nothing else does.
func conflict(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || !strings.HasPrefix(pg.SQLState(), "23") {
		return err
	}
	return fmt.Errorf("%w: %w", crud.ErrConflict, err)
}

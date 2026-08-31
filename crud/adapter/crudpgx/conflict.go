package crudpgx

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs/sqlerr"
)

func (this Executor) conflict(err error) error { return sqlfault.Wrap(this.faults, err) }

func extract(err error) *sqlerr.Err {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
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

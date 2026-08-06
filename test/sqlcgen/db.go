// Package sqlcgen mirrors what sqlc emits for PostgreSQL with
// sql_package: "database/sql". The sources it was written from are in
// test/sqlc/ — regenerate with `sqlc generate` if you change them.
//
// It exists so the integration suite can prove the thing that matters: sqlc's
// Queries and an rx-crud repository can be driven by the same *sql.Tx.
package sqlcgen

import (
	"context"
	"database/sql"
	"time"
)

type DBTX interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func New(db DBTX) *Queries {
	return &Queries{db: db}
}

type Queries struct {
	db DBTX
}

func (q *Queries) WithTx(tx *sql.Tx) *Queries {
	return &Queries{db: tx}
}

type User struct {
	ID        int64
	TenantID  int64
	Email     string
	Name      string
	Age       sql.NullInt32
	Active    bool
	CreatedAt time.Time
}

const createUser = `-- name: CreateUser :one
INSERT INTO users (tenant_id, email, name, age, active)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, tenant_id, email, name, age, active, created_at
`

type CreateUserParams struct {
	TenantID int64
	Email    string
	Name     string
	Age      sql.NullInt32
	Active   bool
}

func (q *Queries) CreateUser(ctx context.Context, arg CreateUserParams) (User, error) {
	row := q.db.QueryRowContext(ctx, createUser,
		arg.TenantID, arg.Email, arg.Name, arg.Age, arg.Active)
	var i User
	err := row.Scan(&i.ID, &i.TenantID, &i.Email, &i.Name, &i.Age, &i.Active, &i.CreatedAt)
	return i, err
}

const getUser = `-- name: GetUser :one
SELECT id, tenant_id, email, name, age, active, created_at FROM users WHERE id = $1
`

func (q *Queries) GetUser(ctx context.Context, id int64) (User, error) {
	row := q.db.QueryRowContext(ctx, getUser, id)
	var i User
	err := row.Scan(&i.ID, &i.TenantID, &i.Email, &i.Name, &i.Age, &i.Active, &i.CreatedAt)
	return i, err
}

const countUsersByTenant = `-- name: CountUsersByTenant :one
SELECT count(*) FROM users WHERE tenant_id = $1
`

func (q *Queries) CountUsersByTenant(ctx context.Context, tenantID int64) (int64, error) {
	row := q.db.QueryRowContext(ctx, countUsersByTenant, tenantID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

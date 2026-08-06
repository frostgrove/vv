// Package sqlcmysql mirrors what sqlc emits for MySQL with
// sql_package: "database/sql": ? placeholders and :execresult for inserts.
package sqlcmysql

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

func New(db DBTX) *Queries { return &Queries{db: db} }

type Queries struct {
	db DBTX
}

func (q *Queries) WithTx(tx *sql.Tx) *Queries { return &Queries{db: tx} }

type User struct {
	ID        int64
	TenantID  int64
	Email     string
	Name      string
	Age       sql.NullInt32
	Active    bool
	CreatedAt time.Time
}

const createUser = `-- name: CreateUser :execresult
INSERT INTO users (tenant_id, email, name, age, active) VALUES (?, ?, ?, ?, ?)
`

type CreateUserParams struct {
	TenantID int64
	Email    string
	Name     string
	Age      sql.NullInt32
	Active   bool
}

func (q *Queries) CreateUser(ctx context.Context, arg CreateUserParams) (sql.Result, error) {
	return q.db.ExecContext(ctx, createUser,
		arg.TenantID, arg.Email, arg.Name, arg.Age, arg.Active)
}

const getUser = `-- name: GetUser :one
SELECT id, tenant_id, email, name, age, active, created_at FROM users WHERE id = ?
`

func (q *Queries) GetUser(ctx context.Context, id int64) (User, error) {
	row := q.db.QueryRowContext(ctx, getUser, id)
	var i User
	err := row.Scan(&i.ID, &i.TenantID, &i.Email, &i.Name, &i.Age, &i.Active, &i.CreatedAt)
	return i, err
}

const countUsersByTenant = `-- name: CountUsersByTenant :one
SELECT count(*) FROM users WHERE tenant_id = ?
`

func (q *Queries) CountUsersByTenant(ctx context.Context, tenantID int64) (int64, error) {
	row := q.db.QueryRowContext(ctx, countUsersByTenant, tenantID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

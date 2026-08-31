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

func New(database DBTX) *Queries { return &Queries{database: database} }

type Queries struct {
	database DBTX
}

func (this *Queries) WithTx(tx *sql.Tx) *Queries { return &Queries{database: tx} }

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

func (this *Queries) CreateUser(ctx context.Context, arg CreateUserParams) (sql.Result, error) {
	return this.database.ExecContext(ctx, createUser,
		arg.TenantID, arg.Email, arg.Name, arg.Age, arg.Active)
}

const getUser = `-- name: GetUser :one
SELECT id, tenant_id, email, name, age, active, created_at FROM users WHERE id = ?
`

func (this *Queries) GetUser(ctx context.Context, id int64) (User, error) {
	row := this.database.QueryRowContext(ctx, getUser, id)
	var i User
	err := row.Scan(&i.ID, &i.TenantID, &i.Email, &i.Name, &i.Age, &i.Active, &i.CreatedAt)
	return i, err
}

const countUsersByTenant = `-- name: CountUsersByTenant :one
SELECT count(*) FROM users WHERE tenant_id = ?
`

func (this *Queries) CountUsersByTenant(ctx context.Context, tenantID int64) (int64, error) {
	row := this.database.QueryRowContext(ctx, countUsersByTenant, tenantID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

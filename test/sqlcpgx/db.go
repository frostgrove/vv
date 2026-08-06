// Package sqlcpgx mirrors what sqlc emits for PostgreSQL with
// sql_package: "pgx/v5". Its DBTX is satisfied by *pgxpool.Pool, *pgx.Conn and
// pgx.Tx — the very same handles rx-crud's pgx adapter takes, so one pgx.Tx can
// drive both.
package sqlcpgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type DBTX interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
}

func New(db DBTX) *Queries { return &Queries{db: db} }

type Queries struct {
	db DBTX
}

func (q *Queries) WithTx(tx pgx.Tx) *Queries { return &Queries{db: tx} }

type User struct {
	ID        int64
	TenantID  int64
	Email     string
	Name      string
	Age       pgtype.Int4
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
	Age      pgtype.Int4
	Active   bool
}

func (q *Queries) CreateUser(ctx context.Context, arg CreateUserParams) (User, error) {
	row := q.db.QueryRow(ctx, createUser, arg.TenantID, arg.Email, arg.Name, arg.Age, arg.Active)
	var i User
	err := row.Scan(&i.ID, &i.TenantID, &i.Email, &i.Name, &i.Age, &i.Active, &i.CreatedAt)
	return i, err
}

const getUser = `-- name: GetUser :one
SELECT id, tenant_id, email, name, age, active, created_at FROM users WHERE id = $1
`

func (q *Queries) GetUser(ctx context.Context, id int64) (User, error) {
	row := q.db.QueryRow(ctx, getUser, id)
	var i User
	err := row.Scan(&i.ID, &i.TenantID, &i.Email, &i.Name, &i.Age, &i.Active, &i.CreatedAt)
	return i, err
}

const countUsersByTenant = `-- name: CountUsersByTenant :one
SELECT count(*) FROM users WHERE tenant_id = $1
`

func (q *Queries) CountUsersByTenant(ctx context.Context, tenantID int64) (int64, error) {
	row := q.db.QueryRow(ctx, countUsersByTenant, tenantID)
	var count int64
	err := row.Scan(&count)
	return count, err
}

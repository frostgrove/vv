-- name: CreateUser :one
INSERT INTO users (tenant_id, email, name, age, active)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: CountUsersByTenant :one
SELECT count(*) FROM users WHERE tenant_id = $1;

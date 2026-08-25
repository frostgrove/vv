// The pgx adapter is its own module so a consumer on database/sql, ent or gorm
// never takes pgx as a dependency. See D-033.
module github.com/shardit-io/vv/crud/adapter/crudpgx

go 1.26

require (
	github.com/jackc/pgx/v5 v5.7.6
	github.com/shardit-io/vv v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

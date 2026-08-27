// The pgx adapter is its own module so a consumer on database/sql, ent or gorm
// never takes pgx as a dependency. See D-033.
module github.com/frostgrove/vv/crud/adapter/crudpgx

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-20260827071144-9d6c18705a6c
	github.com/jackc/pgx/v5 v5.7.6
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

// The pgx pool opener is its own module so a consumer on database/sql, ent or
// gorm never takes pgx as a dependency. See D-033 and D-051.
module github.com/frostgrove/vv/utils/vvdb/dbpgx

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-20260828080731-73ebf0e2ce96
	github.com/jackc/pgx/v5 v5.7.6
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

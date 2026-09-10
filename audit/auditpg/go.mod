module github.com/frostgrove/vv/audit/auditpg

go 1.26.6

replace github.com/frostgrove/vv => ../..

require (
	github.com/frostgrove/vv v0.0.0-00010101000000-000000000000
	github.com/jackc/pgx/v5 v5.10.0
)

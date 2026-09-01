module github.com/frostgrove/vv/jobs/jobspg/jobspgfx

go 1.26.6

require (
	github.com/frostgrove/vv v0.0.0-00010101000000-000000000000
	github.com/frostgrove/vv/jobs/jobsfx v0.0.0-00010101000000-000000000000
	go.uber.org/fx v1.24.0
)

require (
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.26.0 // indirect
	golang.org/x/sys v0.0.0-20220412211240-33da011f77ad // indirect
)

replace github.com/frostgrove/vv => ../../..

replace github.com/frostgrove/vv/jobs/jobsfx => ../../jobsfx

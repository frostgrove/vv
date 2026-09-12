module github.com/frostgrove/vv/jobs/jobspg/jobspgfx

go 1.26.6

require (
	github.com/frostgrove/vv v0.0.0-00010101000000-000000000000
	github.com/frostgrove/vv/jobs/jobsfx v0.0.0-00010101000000-000000000000
	github.com/frostgrove/vv/jobs/jobspg v0.0.0-00010101000000-000000000000
	github.com/frostgrove/vv/runtime/runtimefx v0.0.0-00010101000000-000000000000
	go.uber.org/fx v1.24.0
)

require (
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/frostgrove/vv/jobs/jobsfx => ../../jobsfx

replace github.com/frostgrove/vv/jobs/jobspg => ..

replace github.com/frostgrove/vv/runtime/runtimefx => ../../../runtime/runtimefx

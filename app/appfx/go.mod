// The fx wiring for the composition root is its own module so that a consumer
// who assembles their program by hand — or with a different container — never
// takes uber/fx as a dependency. See [[D-033]] and [[D-074]].
//
// It requires fx and the root module, and nothing else. What it holds is the
// value groups and the constructors; every decision about what goes in them is
// still made in a call site the consumer wrote ([[D-037]]).
module github.com/frostgrove/vv/app/appfx

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-00010101000000-000000000000
	go.uber.org/fx v1.24.0
)

require (
	github.com/stretchr/testify v1.11.1 // indirect
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// The fx wiring for a database/sql source is its own module so that a consumer
// who opens their own pool — or uses a different container — never takes
// uber/fx as a dependency. See [[D-033]] and [[D-074]].
//
// It requires fx and the root module, and no driver. Which driver is registered
// is the application's decision and always was ([[D-057]]).
module github.com/frostgrove/vv/crud/adapter/crudsql/crudsqlfx

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-20260829132449-bc1e4c0b1038
	go.uber.org/fx v1.24.0
)

require (
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.26.0 // indirect
	golang.org/x/sys v0.0.0-20220412211240-33da011f77ad // indirect
)

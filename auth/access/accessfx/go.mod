// The fx wiring for the access context is its own module so that a consumer who
// assembles the context by hand — or with a different container — never takes
// uber/fx as a dependency. See [[D-033]] and [[D-074]].
//
// It requires the access module and fx, and nothing else. It names no transport:
// which routes this deployment exposes, and on which binding, is the
// application's ([[D-066]]).
module github.com/frostgrove/vv/auth/access/accessfx

go 1.26.5

require (
	github.com/frostgrove/vv v0.0.0-20260829170205-3b943f2e18f1
	github.com/frostgrove/vv/auth/access v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	go.uber.org/fx v1.24.0
)

require (
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.26.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// access has no tag yet; the workspace and this replace are how a satellite
// resolves it until the first release.
replace github.com/frostgrove/vv/auth/access => ..

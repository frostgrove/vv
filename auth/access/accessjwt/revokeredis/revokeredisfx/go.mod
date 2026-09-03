// The fx wiring for the Redis revocation list is its own module so a consumer
// who builds the list by hand — or uses a different container — never takes
// uber/fx as a dependency. See D-033 and D-074.
module github.com/frostgrove/vv/auth/access/accessjwt/revokeredis/revokeredisfx

go 1.26.5

// Nothing under access carries a tag yet; the workspace and these replaces are
// how a satellite resolves its siblings until the first release.
replace github.com/frostgrove/vv/auth/access/accessjwt/revokeredis => ..

replace github.com/frostgrove/vv/auth/access/accessjwt => ../..

replace github.com/frostgrove/vv/auth/access => ../../..

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/frostgrove/vv/auth/access/accessjwt v0.0.0-00010101000000-000000000000
	github.com/frostgrove/vv/auth/access/accessjwt/revokeredis v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.7.3
	go.uber.org/fx v1.24.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/frostgrove/vv v0.0.0-20260829170205-3b943f2e18f1 // indirect
	github.com/frostgrove/vv/auth/access v0.0.0-00010101000000-000000000000 // indirect
	github.com/frostgrove/vv/auth/authjwt v0.0.0-20260828080822-3c0a8bebc6f6 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.26.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

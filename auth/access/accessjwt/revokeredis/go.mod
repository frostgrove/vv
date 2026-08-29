// The Redis revocation list is its own module so a deployment that accepts the
// access token's lifetime as its revocation window never takes a Redis client
// as a dependency. See D-033.
module github.com/frostgrove/vv/auth/access/accessjwt/revokeredis

go 1.26.5

require (
	github.com/frostgrove/vv/auth/access/accessjwt v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.7.3
)

require (
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/frostgrove/vv v0.0.0-20260828080822-3c0a8bebc6f6 // indirect
	github.com/frostgrove/vv/auth/access v0.0.0-00010101000000-000000000000 // indirect
	github.com/frostgrove/vv/auth/authjwt v0.0.0-20260828080822-3c0a8bebc6f6 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// accessjwt has no tag yet; the workspace and this replace are how a satellite
// resolves it until the first release.
replace github.com/frostgrove/vv/auth/access/accessjwt => ..

replace github.com/frostgrove/vv/auth/access => ../..

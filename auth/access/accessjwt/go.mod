// The JWT strategy is its own module so a consumer holding opaque sessions
// never takes a JWT library as a dependency. See D-033.
module github.com/frostgrove/vv/auth/access/accessjwt

go 1.26.5

require (
	github.com/frostgrove/vv v0.0.0-20260828080822-3c0a8bebc6f6
	github.com/frostgrove/vv/auth/access v0.0.0-00010101000000-000000000000
	github.com/frostgrove/vv/auth/authjwt v0.0.0-20260828080822-3c0a8bebc6f6
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
)

require (
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// Neither access nor this module has a tag yet; the workspace and these
// replaces are how they resolve until the first release.
replace github.com/frostgrove/vv/auth/access => ..

// The JWT provider is its own module so a consumer authenticating with API
// keys, sessions or a gateway header never takes a JWT library as a
// dependency. See D-033.
//
// JWT verification is delegated to golang-jwt. Public Ed25519 trust material is
// decoded with filippo.io/edwards25519 because crypto/ed25519 intentionally
// exposes verification, not strict point/subgroup validation. The JWKS source
// underneath still uses net/http and encoding/json rather than another client
// or key-set stack a consumer did not choose. See D-051 and D-078.
module github.com/frostgrove/vv/auth/authjwt

go 1.26

require (
	filippo.io/edwards25519 v1.2.0
	github.com/frostgrove/vv v0.0.0-20260829132449-bc1e4c0b1038
	github.com/golang-jwt/jwt/v5 v5.3.1
)

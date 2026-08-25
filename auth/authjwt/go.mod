// The JWT provider is its own module so a consumer authenticating with API
// keys, sessions or a gateway header never takes a JWT library as a
// dependency. See D-033.
//
// One require and one dependency decision: golang-jwt is the whole of it, and
// the JWKS key source underneath uses net/http and encoding/json rather than a
// second choice a consumer did not make. See D-051.
module github.com/shardit-io/vv/auth/authjwt

go 1.26

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/shardit-io/vv v0.0.0
)

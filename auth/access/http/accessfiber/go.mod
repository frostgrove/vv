// The Fiber sign-in surface is its own module so a consumer on Gin or net/http
// never takes Fiber as a dependency. See D-033.
//
// It requires the access module and Fiber, and nothing else. Serving these
// routes and serving a CRUD resource are two things a consumer chooses
// separately, so this does not require crudfiber — see D-051.
module github.com/frostgrove/vv/auth/access/http/accessfiber

go 1.26.5

require (
	github.com/frostgrove/vv/auth/access v0.0.0-00010101000000-000000000000
	github.com/gofiber/fiber/v3 v3.4.0
)

require (
	github.com/andybalholm/brotli v1.2.2 // indirect
	github.com/frostgrove/vv v0.0.0-20260828080822-3c0a8bebc6f6 // indirect
	github.com/gofiber/schema v1.8.0 // indirect
	github.com/gofiber/utils/v2 v2.1.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.19.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.72.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

// access has no tag yet; the workspace and this replace are how a satellite
// resolves it until the first release.
replace github.com/frostgrove/vv/auth/access => ../..

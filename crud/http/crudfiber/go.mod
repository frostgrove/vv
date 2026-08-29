// The Fiber binding is its own module so a consumer on Gin, Echo or net/http
// never takes Fiber as a dependency. See D-033.
module github.com/frostgrove/vv/crud/http/crudfiber

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-20260829132449-bc1e4c0b1038
	github.com/gofiber/fiber/v3 v3.4.0
)

require (
	github.com/andybalholm/brotli v1.2.2 // indirect
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
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

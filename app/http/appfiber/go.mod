// The Fiber composition root is its own module for two dependency decisions
// that are one decision here: fx and Fiber. A consumer of this package is
// assembling Fiber routes out of an fx graph, and cannot want one half without
// the other ([[D-051]]).
//
// A consumer on Gin, net/http or gRPC never takes either ([[D-033]]).
//
// It requires authfiber because the boot access gate is the point of the
// package, and the gate reads Fiber's routing table through that binding.
module github.com/frostgrove/vv/app/http/appfiber

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-20260829132449-bc1e4c0b1038
	github.com/frostgrove/vv/auth/http/authfiber v0.0.0-20260828080822-3c0a8bebc6f6
	github.com/gofiber/fiber/v3 v3.4.0
	go.uber.org/fx v1.24.0
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
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.26.0 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

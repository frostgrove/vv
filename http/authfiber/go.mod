// The Fiber authentication middleware is its own module so a consumer on Gin,
// net/http or gRPC never takes Fiber as a dependency. See D-033.
//
// It does not require crudfiber. Authenticating a request and serving a CRUD
// resource are two things a consumer chooses separately, and a middleware that
// dragged the routes in would make one of them unavailable without the other —
// which is the shape D-051 calls a second decision.
module github.com/shardit-io/vv/http/authfiber

go 1.26

require (
	github.com/gofiber/fiber/v3 v3.4.0
	github.com/shardit-io/vv v0.0.0
)

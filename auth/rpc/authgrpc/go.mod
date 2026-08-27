// The gRPC authentication interceptor is its own module so a consumer on HTTP
// never takes grpc as a dependency. See D-033.
//
// One require and one dependency decision. It does not require crudgrpc:
// authenticating a call and serving a CRUD resource are two things a consumer
// chooses separately, and an interceptor that dragged the resource in would
// make one unavailable without the other. See D-051.
module github.com/frostgrove/vv/auth/rpc/authgrpc

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-20260827054915-979f9cb9cfb6
	google.golang.org/grpc v1.83.1
)

require (
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// The gRPC authentication interceptor is its own module so a consumer on HTTP
// never takes grpc as a dependency. See D-033.
//
// One require and one dependency decision. It does not require crudgrpc:
// authenticating a call and serving a CRUD resource are two things a consumer
// chooses separately, and an interceptor that dragged the resource in would
// make one unavailable without the other. See D-051.
module github.com/shardit-io/vv/rpc/authgrpc

go 1.26

require (
	github.com/shardit-io/vv v0.0.0
	google.golang.org/grpc v1.83.1
)

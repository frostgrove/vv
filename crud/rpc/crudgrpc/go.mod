// The gRPC binding is its own module so a consumer on HTTP never takes gRPC,
// protobuf and genproto as dependencies. See D-033 and D-051.
//
// Three requires and one dependency decision: grpc pulls its own wire types
// from protobuf and its error details from genproto, and no consumer can take
// one without the other two.
module github.com/frostgrove/vv/crud/rpc/crudgrpc

go 1.26

require (
	github.com/frostgrove/vv v0.0.0-00010101000000-000000000000
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688
	google.golang.org/grpc v1.83.1
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/go-logr/logr v1.4.4 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

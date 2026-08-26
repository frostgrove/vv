package crudgrpc_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/shardit-io/vv/auth"
	"github.com/shardit-io/vv/crud/rpc/crudgrpc"
)

// A refusal on a streaming method is classified, the way it is on a unary one.
//
// The auth interceptor returns an unrendered errs.Fault and relies on something
// downstream to turn it into a status — exactly as its unary twin does. There
// was no downstream for a stream: `Errors` is a UnaryServerInterceptor and had no
// counterpart, so grpc-go wrapped the bare error as codes.Unknown. A refused
// stream answered Unknown where a refused unary call answered Unauthenticated,
// and a client branching on the code could not tell a rejected credential from a
// server bug.
func TestARefusedStreamIsClassifiedLikeARefusedCall(t *testing.T) {
	refuse := func(any, grpc.ServerStream) error {
		return auth.Unauthenticated("forged token")
	}

	t.Run("without the interceptor the refusal is Unknown", func(t *testing.T) {
		// The control, and it is the finding: this is what shipped.
		code := codeOfStream(t, refuse)
		if code != codes.Unknown {
			t.Fatalf("an unrendered fault answered %s — if this is no longer Unknown, the test below proves nothing", code)
		}
	})

	t.Run("with it the refusal is Unauthenticated", func(t *testing.T) {
		code := codeOfStream(t, refuse, grpc.StreamInterceptor(crudgrpc.StreamErrors()))
		if code != codes.Unauthenticated {
			t.Fatalf("a refused stream answered %s, want Unauthenticated", code)
		}
	})

	t.Run("a status the method already set is left alone", func(t *testing.T) {
		// The same rule the unary interceptor keeps: an error that already
		// carries a status has been rendered by something, and overwriting it
		// would be this interceptor deciding it knows better.
		own := status.Error(codes.FailedPrecondition, "mine")
		code := codeOfStream(t, func(any, grpc.ServerStream) error { return own },
			grpc.StreamInterceptor(crudgrpc.StreamErrors()))
		if code != codes.FailedPrecondition {
			t.Fatalf("the method's own status was overwritten with %s", code)
		}
	})
}

// codeOfStream runs one streaming call against a server built with opts and
// answers the code the client saw.
func codeOfStream(t *testing.T, h grpc.StreamHandler, opts ...grpc.ServerOption) codes.Code {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(opts...)
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "vv.test.Streamer",
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName:    "Watch",
			Handler:       h,
			ServerStreams: true,
		}},
		Metadata: "vv.test",
	}, nil)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialling the in-process server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	st, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{StreamName: "Watch", ServerStreams: true}, "/vv.test.Streamer/Watch")
	if err != nil {
		return status.Code(err)
	}
	if err := st.SendMsg(&emptyMsg{}); err != nil && !errors.Is(err, io.EOF) {
		return status.Code(err)
	}
	return status.Code(st.RecvMsg(&emptyMsg{}))
}

// emptyMsg is a message the codec accepts and nobody reads.
type emptyMsg struct{}

func (*emptyMsg) Reset()         {}
func (*emptyMsg) String() string { return "" }
func (*emptyMsg) ProtoMessage()  {}

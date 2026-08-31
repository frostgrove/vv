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

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud/rpc/crudgrpc"
)

func TestARefusedStreamIsClassifiedLikeARefusedCall(t *testing.T) {
	refuse := func(any, grpc.ServerStream) error {
		return auth.Unauthenticated("forged token")
	}

	t.Run("without the interceptor the refusal is Unknown", func(t *testing.T) {
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
		own := status.Error(codes.FailedPrecondition, "mine")
		code := codeOfStream(t, func(any, grpc.ServerStream) error { return own },
			grpc.StreamInterceptor(crudgrpc.StreamErrors()))
		if code != codes.FailedPrecondition {
			t.Fatalf("the method's own status was overwritten with %s", code)
		}
	})
}

func codeOfStream(t *testing.T, h grpc.StreamHandler, options ...grpc.ServerOption) codes.Code {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(options...)
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

type emptyMsg struct{}

func (*emptyMsg) Reset()         {}
func (*emptyMsg) String() string { return "" }
func (*emptyMsg) ProtoMessage()  {}

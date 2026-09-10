package otelnative

import (
	"context"
	"net"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	grpcCodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const grpcPingMethod = "/otelnative.Transport/Ping"
const grpcFailMethod = "/otelnative.Transport/Fail"

type grpcTransportService interface {
	Ping(context.Context, *emptypb.Empty) (*wrapperspb.StringValue, error)
	Fail(context.Context, *emptypb.Empty) (*emptypb.Empty, error)
}

type grpcTransportServer struct {
	handlerSpan   trace.SpanContext
	metadataExact bool
	privacyOK     bool
}

func (s *grpcTransportServer) Ping(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.StringValue, error) {
	s.handlerSpan = trace.SpanContextFromContext(ctx)
	s.privacyOK = s.handlerSpan.TraceState().Len() == 0
	incoming, ok := metadata.FromIncomingContext(ctx)
	authorization := incoming.Get("authorization")
	bag := incoming.Get("baggage")
	s.metadataExact = ok && len(authorization) == 1 && authorization[0] == "secret-grpc-metadata" && len(bag) == 1 && bag[0] == "account=secret-baggage"
	return wrapperspb.String("exact-grpc-result"), nil
}

func (*grpcTransportServer) Fail(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	return nil, status.Error(grpcCodes.Internal, "secret-grpc-error")
}

func TestGRPCPublicServerAndClientKeepOneBoundedBoundary(t *testing.T) {
	methods, err := NewRPCTable(grpcPingMethod, grpcFailMethod)
	if err != nil {
		t.Fatal(err)
	}
	policy := TraceProjectionPolicy{
		RPCMethods: methods,
		ResourceAttributes: []attribute.KeyValue{
			attribute.String("service.name", allowedServiceName),
		},
	}
	telemetry := newTelemetryFixture(t, policy, nil)
	serverStats, err := GRPCServerStats(telemetry.providers, methods, PublicIngress)
	if err != nil {
		t.Fatal(err)
	}
	clientStats, err := GRPCClientStats(telemetry.providers, methods)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(grpc.StatsHandler(serverStats))
	service := &grpcTransportServer{}
	server.RegisterService(&grpcTransportServiceDesc, service)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(clientStats),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	carrier := propagation.HeaderCarrier{}
	remoteHeaders(t, carrier)
	remoteCtx := propagation.TraceContext{}.Extract(context.Background(), carrier)
	rootCtx, root := telemetry.providers.Tracer.Tracer("fixture").Start(remoteCtx, "fixture root")
	callCtx := metadata.NewOutgoingContext(rootCtx, metadata.Pairs(
		"authorization", "secret-grpc-metadata",
		"baggage", "account=secret-baggage",
	))
	response := new(wrapperspb.StringValue)
	if err := connection.Invoke(callCtx, grpcPingMethod, &emptypb.Empty{}, response); err != nil {
		t.Fatal(err)
	}
	if response.Value != "exact-grpc-result" || !service.metadataExact || !service.privacyOK {
		t.Fatalf("response=%q metadata=%t privacy=%t", response.Value, service.metadataExact, service.privacyOK)
	}
	failureErr := connection.Invoke(callCtx, grpcFailMethod, &emptypb.Empty{}, new(emptypb.Empty))
	if status.Code(failureErr) != grpcCodes.Internal {
		t.Fatalf("failure error=%v", failureErr)
	}
	unknownMethod := "/secret.Transport/secret-method-4815"
	unknownErr := connection.Invoke(callCtx, unknownMethod, &emptypb.Empty{}, new(emptypb.Empty))
	if status.Code(unknownErr) != grpcCodes.Unimplemented {
		t.Fatalf("unknown error=%v", unknownErr)
	}
	root.End()
	spans := telemetry.nativeSpans()
	if len(spans) != 5 {
		t.Fatalf("native spans=%d spans=%v", len(spans), spanNamesAndKinds(spans))
	}
	clientSpan := findSpan(t, spans, "otelnative.Transport/Ping", trace.SpanKindClient)
	serverSpan := findSpan(t, spans, "otelnative.Transport/Ping", trace.SpanKindServer)
	assertPublicSpan(t, serverSpan, clientSpan.SpanContext)
	if service.handlerSpan.TraceID() != serverSpan.SpanContext.TraceID() || service.handlerSpan.SpanID() != serverSpan.SpanContext.SpanID() {
		t.Fatalf("handler span=%v exported=%v", service.handlerSpan, serverSpan.SpanContext)
	}
	failureClient := findSpan(t, spans, "otelnative.Transport/Fail", trace.SpanKindClient)
	failureServer := findSpan(t, spans, "otelnative.Transport/Fail", trace.SpanKindServer)
	if failureClient.Status.Code != codes.Error || failureClient.Status.Description != "" || failureServer.Status.Code != codes.Error || failureServer.Status.Description != "" {
		t.Fatalf("failure client=%#v server=%#v", failureClient.Status, failureServer.Status)
	}
	findSpan(t, spans, fallbackRPCName, trace.SpanKindClient)
	metrics := telemetry.flushMetrics(t)
	assertNativeMetricsPresent(t, metrics)
	assertNativeExemplarPresent(t, metrics)
	assertNativePrivacy(t, spans, metrics,
		secretResource,
		"secret-state",
		"secret-baggage",
		"secret-grpc-metadata",
		"secret-grpc-error",
		"secret.Transport",
		"secret-method-4815",
	)
	for _, part := range metricPrivacyParts(metrics) {
		if strings.Contains(part, unknownMethod) {
			t.Fatalf("unknown RPC escaped metric projection: %q", part)
		}
	}
}

var grpcTransportServiceDesc = grpc.ServiceDesc{
	ServiceName: "otelnative.Transport",
	HandlerType: (*grpcTransportService)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Ping",
			Handler: func(server any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				request := new(emptypb.Empty)
				if err := decode(request); err != nil {
					return nil, err
				}
				if interceptor == nil {
					return server.(grpcTransportService).Ping(ctx, request)
				}
				info := &grpc.UnaryServerInfo{Server: server, FullMethod: grpcPingMethod}
				handler := func(ctx context.Context, request any) (any, error) {
					return server.(grpcTransportService).Ping(ctx, request.(*emptypb.Empty))
				}
				return interceptor(ctx, request, info, handler)
			},
		},
		{
			MethodName: "Fail",
			Handler: func(server any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				request := new(emptypb.Empty)
				if err := decode(request); err != nil {
					return nil, err
				}
				if interceptor == nil {
					return server.(grpcTransportService).Fail(ctx, request)
				}
				info := &grpc.UnaryServerInfo{Server: server, FullMethod: grpcFailMethod}
				handler := func(ctx context.Context, request any) (any, error) {
					return server.(grpcTransportService).Fail(ctx, request.(*emptypb.Empty))
				}
				return interceptor(ctx, request, info, handler)
			},
		},
	},
}

package revokeredisfx_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"

	"github.com/frostgrove/vv/auth/access/accessjwt"
	"github.com/frostgrove/vv/auth/access/accessjwt/revokeredis"
	"github.com/frostgrove/vv/auth/access/accessjwt/revokeredis/revokeredisfx"
)

type fakeServer struct {
	*miniredis.Miniredis
	asked atomic.Int64
}

func serverAnswering(t *testing.T, policy string) *fakeServer {
	t.Helper()
	return serverReplying(t, func(peer *server.Peer) {
		peer.WriteMapLen(1)
		peer.WriteBulk(revokeredis.EvictionParameter)
		peer.WriteBulk(policy)
	})
}

func serverRefusingConfig(t *testing.T) *fakeServer {
	t.Helper()
	return serverReplying(t, func(peer *server.Peer) {
		peer.WriteError("NOPERM this user has no permissions to run the 'config' command")
	})
}

func serverReplying(t *testing.T, reply func(*server.Peer)) *fakeServer {
	t.Helper()
	fake := &fakeServer{Miniredis: miniredis.RunT(t)}
	handler := func(peer *server.Peer, _ string, _ []string) {
		fake.asked.Add(1)
		reply(peer)
	}
	if err := fake.Server().Register("CONFIG", handler); err != nil {
		t.Fatalf("cannot teach the fake server to answer CONFIG: %v", err)
	}
	return fake
}

func clientOf(t *testing.T, fake *fakeServer) func() redis.UniversalClient {
	t.Helper()
	return func() redis.UniversalClient {
		client := redis.NewClient(&redis.Options{Addr: fake.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		return client
	}
}

func recordingLogger() (*slog.Logger, *bytes.Buffer) {
	written := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(written, &slog.HandlerOptions{Level: slog.LevelWarn})), written
}

func TestAnEvictingServerFailsTheStartAndNotTheGraph(t *testing.T) {
	fake := serverAnswering(t, "allkeys-lru")
	var list accessjwt.RevocationList
	application := fx.New(
		fx.NopLogger,
		fx.Provide(clientOf(t, fake)),
		revokeredisfx.Auto(),
		fx.Populate(&list),
	)

	if err := application.Err(); err != nil {
		t.Fatalf("the graph would not build at all: %v", err)
	}
	if asked := fake.asked.Load(); asked != 0 {
		t.Fatalf("building the graph asked the server %d times, so the check runs inside fx.New where fx.StartTimeout does not reach it", asked)
	}

	err := application.Start(context.Background())

	if !errors.Is(err, revokeredis.ErrEvicting) {
		t.Fatalf("a deployment whose revocation list lives on an evicting server started: %v", err)
	}
	if !strings.Contains(err.Error(), "allkeys-lru") {
		t.Fatalf("the start failure does not name the policy an operator has to change: %v", err)
	}
	if asked := fake.asked.Load(); asked != 1 {
		t.Fatalf("the start asked the server %d times", asked)
	}
}

func TestARetainingServerStartsAndTheListItLeavesBehindWorks(t *testing.T) {
	fake := serverAnswering(t, revokeredis.RetainingPolicy)
	var list accessjwt.RevocationList
	application := fx.New(
		fx.NopLogger,
		fx.Provide(clientOf(t, fake)),
		revokeredisfx.Auto(),
		fx.Populate(&list),
	)
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("a server that keeps what it is given was refused: %v", err)
	}
	t.Cleanup(func() { _ = application.Stop(context.Background()) })

	session := uuid.New()
	if err := list.Revoke(context.Background(), session, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("the list the graph published cannot revoke: %v", err)
	}
	revoked, err := list.Revoked(context.Background(), session)
	if err != nil || !revoked {
		t.Fatalf("a session revoked through the published list reads as revoked=%v, err=%v", revoked, err)
	}
}

func TestAServerThatWillNotSayIsWarnedAboutThroughTheGraphsLogger(t *testing.T) {
	fake := serverRefusingConfig(t)
	logger, written := recordingLogger()
	application := fx.New(
		fx.NopLogger,
		fx.Provide(clientOf(t, fake), func() *slog.Logger { return logger }),
		revokeredisfx.Auto(),
		fx.Invoke(func(*revokeredis.List) {}),
	)

	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("a managed server that forbids CONFIG was refused rather than reported: %v", err)
	}
	t.Cleanup(func() { _ = application.Stop(context.Background()) })

	said := written.String()
	if !strings.Contains(said, "WARN") || !strings.Contains(said, revokeredis.EvictionParameter) {
		t.Fatalf("the deployment started knowing nothing about what its Redis evicts, and said so nowhere: %q", said)
	}
	if !strings.Contains(said, "NOPERM") {
		t.Fatalf("the warning drops what the server said, so nobody can tell an ACL from an outage: %q", said)
	}
}

func TestADeploymentCanTurnAnUnknownPolicyIntoARefusedStart(t *testing.T) {
	fake := serverRefusingConfig(t)
	logger, _ := recordingLogger()
	application := fx.New(
		fx.NopLogger,
		fx.Provide(clientOf(t, fake), func() *slog.Logger { return logger }),
		revokeredisfx.Revoking(revokeredis.OnUnknownPolicy(revokeredis.Refused)),
		fx.Invoke(func(*revokeredis.List) {}),
	)

	err := application.Start(context.Background())

	if !errors.Is(err, revokeredis.ErrUnknownPolicy) {
		t.Fatalf("a deployment that asked to be refused an unproved server started: %v", err)
	}
}

func TestTheOptionsAConsumerWritesOutrankTheOnesTheGraphSupplies(t *testing.T) {
	fake := serverRefusingConfig(t)
	chosen, said := recordingLogger()
	ambient, ambientSaid := recordingLogger()
	application := fx.New(
		fx.NopLogger,
		fx.Provide(clientOf(t, fake), func() *slog.Logger { return ambient }),
		revokeredisfx.Revoking(revokeredis.Logger(chosen)),
		fx.Invoke(func(*revokeredis.List) {}),
	)
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("the graph did not start: %v", err)
	}
	t.Cleanup(func() { _ = application.Stop(context.Background()) })

	if !strings.Contains(said.String(), revokeredis.EvictionParameter) {
		t.Fatalf("the logger the consumer named was not used: %q", said.String())
	}
	if ambientSaid.Len() != 0 {
		t.Fatalf("the graph's logger won over the one the consumer wrote by name: %q", ambientSaid.String())
	}
}

func TestTheLowLevelFormChecksAListTheRootBuiltItself(t *testing.T) {
	fake := serverAnswering(t, "volatile-ttl")
	built := func(client redis.UniversalClient) (*revokeredis.List, error) {
		return revokeredis.New(client, revokeredis.Prefix("tenant:revoked:"))
	}
	application := fx.New(
		fx.NopLogger,
		fx.Provide(clientOf(t, fake), built),
		revokeredisfx.Verifying(),
	)

	err := application.Start(context.Background())

	if !errors.Is(err, revokeredis.ErrEvicting) {
		t.Fatalf("a list the root built for itself was started unchecked: %v", err)
	}
}

package revokeredis

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
)

type answeringServer struct {
	*miniredis.Miniredis
	asked atomic.Int64
}

func serverAnswering(t *testing.T, policy string) *answeringServer {
	t.Helper()
	return serverReplying(t, func(peer *server.Peer) {
		peer.WriteMapLen(1)
		peer.WriteBulk(EvictionParameter)
		peer.WriteBulk(policy)
	})
}

func serverRefusingConfig(t *testing.T, refusal string) *answeringServer {
	t.Helper()
	return serverReplying(t, func(peer *server.Peer) { peer.WriteError(refusal) })
}

func serverReplying(t *testing.T, reply func(*server.Peer)) *answeringServer {
	t.Helper()
	answering := &answeringServer{Miniredis: miniredis.RunT(t)}
	handler := func(peer *server.Peer, _ string, _ []string) {
		answering.asked.Add(1)
		reply(peer)
	}
	if err := answering.Server().Register("CONFIG", handler); err != nil {
		t.Fatalf("cannot teach the fake server to answer CONFIG: %v", err)
	}
	return answering
}

func listOn(t *testing.T, address string, options ...Option) *List {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = client.Close() })
	list, err := New(client, options...)
	if err != nil {
		t.Fatalf("cannot build the revocation list: %v", err)
	}
	return list
}

func recordingLogger() (*slog.Logger, *bytes.Buffer) {
	written := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(written, &slog.HandlerOptions{Level: slog.LevelWarn})), written
}

// The defect the whole check exists for: eviction is indistinguishable from a
// session that was never revoked, so the list answers "not revoked" and the
// signed-out token works again.
func TestAnEvictedRevocationReadsAsASessionNobodyRevoked(t *testing.T) {
	fake := miniredis.RunT(t)
	list := listOn(t, fake.Addr())
	session := uuid.New()

	if err := list.Revoke(context.Background(), session, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("cannot revoke: %v", err)
	}
	revoked, err := list.Revoked(context.Background(), session)
	if err != nil || !revoked {
		t.Fatalf("a session that was just revoked reads as revoked=%v, err=%v", revoked, err)
	}

	fake.Del(list.key(session))

	revoked, err = list.Revoked(context.Background(), session)
	if err != nil {
		t.Fatalf("reading a list whose key is gone failed instead of answering: %v", err)
	}
	if revoked {
		t.Fatal("the list still knows the session was revoked, so eviction is harmless and this check has no reason to exist")
	}
}

func TestOnlyNoevictionIsAcceptedAndEveryEvictingPolicyIsRefused(t *testing.T) {
	for _, policy := range []string{
		"allkeys-lru", "allkeys-lfu", "allkeys-random",
		"volatile-lru", "volatile-lfu", "volatile-random", "volatile-ttl",
		"allkeys-something-redis-has-not-shipped-yet",
	} {
		t.Run(policy, func(t *testing.T) {
			list := listOn(t, serverAnswering(t, policy).Addr())

			verdict, err := list.VerifyEvictionPolicy(context.Background())
			if !errors.Is(err, ErrEvicting) {
				t.Fatalf("a server under %s was accepted: %v", policy, err)
			}
			if verdict.Verdict != Evicting || verdict.Name != policy {
				t.Fatalf("the refusal reports %v rather than the policy the server named", verdict)
			}
			if !strings.Contains(err.Error(), policy) {
				t.Fatalf("the refusal does not name the policy an operator has to change: %v", err)
			}
		})
	}

	t.Run(RetainingPolicy, func(t *testing.T) {
		list := listOn(t, serverAnswering(t, RetainingPolicy).Addr())

		verdict, err := list.VerifyEvictionPolicy(context.Background())
		if err != nil {
			t.Fatalf("the one policy that keeps a revocation was refused: %v", err)
		}
		if verdict.Verdict != Retaining {
			t.Fatalf("noeviction produced %v", verdict)
		}
	})
}

func TestAServerThatWillNotAnswerIsUnknownAndSaidOutLoud(t *testing.T) {
	cases := map[string]*answeringServer{
		"an ACL that forbids CONFIG": serverRefusingConfig(t, "NOPERM this user has no permissions to run the 'config' command"),
		"a CONFIG that is disabled":  serverRefusingConfig(t, "ERR unknown command 'CONFIG'"),
	}
	for name, fake := range cases {
		t.Run(name, func(t *testing.T) {
			logger, written := recordingLogger()
			list := listOn(t, fake.Addr(), Logger(logger))

			verdict, err := list.VerifyEvictionPolicy(context.Background())
			if err != nil {
				t.Fatalf("a server that cannot be asked was refused rather than reported: %v", err)
			}
			if verdict.Verdict != Unknown {
				t.Fatalf("a server that answered nothing produced %v, which reads as a proof it does not have", verdict)
			}
			if verdict.Reason == nil {
				t.Fatal("the unknown verdict carries no reason, so nothing can say why it is unknown")
			}
			said := written.String()
			if !strings.Contains(said, "WARN") || !strings.Contains(said, EvictionParameter) {
				t.Fatalf("an unknown eviction policy passed in silence: %q", said)
			}
		})
	}
}

func TestADeploymentCanTurnAnUnknownEvictionPolicyIntoARefusal(t *testing.T) {
	fake := serverRefusingConfig(t, "NOPERM this user has no permissions to run the 'config' command")
	logger, written := recordingLogger()
	list := listOn(t, fake.Addr(), Logger(logger), OnUnknownPolicy(Refused))

	verdict, err := list.VerifyEvictionPolicy(context.Background())
	if !errors.Is(err, ErrUnknownPolicy) {
		t.Fatalf("a deployment that asked for a refusal got %v", err)
	}
	if verdict.Verdict != Unknown {
		t.Fatalf("the refusal reports %v rather than what was actually learned", verdict)
	}
	if !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("the refusal drops what the server said, so nobody can tell an ACL from an outage: %v", err)
	}
	if written.Len() != 0 {
		t.Fatalf("the refusal was logged as well as returned, so the same event is reported twice: %q", written.String())
	}
}

func TestAServerThatAnswersAnEmptyPolicyIsUnknownRatherThanRetaining(t *testing.T) {
	fake := serverReplying(t, func(peer *server.Peer) { peer.WriteMapLen(0) })
	logger, _ := recordingLogger()
	list := listOn(t, fake.Addr(), Logger(logger))

	if verdict := list.EvictionPolicy(context.Background()); verdict.Verdict != Unknown {
		t.Fatalf("a server that named no policy produced %v", verdict)
	}
}

func TestNothingIsAskedOfTheServerUntilTheCheckIsRun(t *testing.T) {
	fake := serverAnswering(t, "allkeys-lru")
	list := listOn(t, fake.Addr())

	if asked := fake.asked.Load(); asked != 0 {
		t.Fatalf("building the list already asked the server %d times, so the check runs where no start timeout reaches it", asked)
	}
	if _, err := list.VerifyEvictionPolicy(context.Background()); err == nil {
		t.Fatal("the evicting server was accepted")
	}
	if asked := fake.asked.Load(); asked != 1 {
		t.Fatalf("the check asked the server %d times", asked)
	}
}

func TestAnUnknownPolicyWordIsRefusedWhereItIsWritten(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = client.Close() })

	if _, err := New(client, OnUnknownPolicy("maybe")); err == nil {
		t.Fatal("a word that is neither Reported nor Refused was accepted, so a typo picks a policy silently")
	}
	if _, err := New(client, OnUnknownPolicy(Refused)); err != nil {
		t.Fatalf("a word the package defines was refused, so the check refuses everything: %v", err)
	}
}

func TestAServerThatIsNotThereIsRefusedRatherThanCalledUnknown(t *testing.T) {
	fake := miniredis.RunT(t)
	address := fake.Addr()
	fake.Close()

	logger, written := recordingLogger()
	list := listOn(t, address, Logger(logger))

	verdict, err := list.VerifyEvictionPolicy(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("a list with no server behind it started with %v, and every request it will be asked is going to fail", err)
	}
	if verdict.Verdict != Unknown {
		t.Fatalf("a server that answered nothing produced %v", verdict)
	}
	if written.Len() != 0 {
		t.Fatalf("an absent server was reported as one that merely would not answer: %q", written.String())
	}
}

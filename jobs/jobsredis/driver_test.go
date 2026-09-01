package jobsredis

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/frostgrove/vv/jobs"
	"github.com/redis/go-redis/v9"
)

func TestRecoveryWindowIsBounded(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo := newRepository(client, "jobs:{test}")
	var raw [jobs.WorkerIncarnationBytes]byte
	raw[0] = 1
	incarnation, err := jobs.WorkerIncarnationFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Round(0).UTC()
	for index := range 20 {
		if err := client.ZAdd(context.Background(), repo.leasedKey(), redis.Z{Score: float64(now.Add(-time.Second).UnixMilli()), Member: fmt.Sprintf("expired-%d", index)}).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.ZAdd(context.Background(), repo.incarnationKey(raw), redis.Z{Score: float64(now.Add(time.Minute).UnixMilli()), Member: fmt.Sprintf("owned-%d", index)}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := repo.recoveryIDs(context.Background(), incarnation, now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) > 6 {
		t.Fatalf("recovery window = %d, want at most 6", len(ids))
	}
}

func TestQueueAndWorkersEndToEnd(t *testing.T) {
	server := miniredis.RunT(t)
	serverTime := time.Now().Round(0).UTC()
	server.SetTime(serverTime)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	definition, catalog, namespace := testDefinition(t)
	driver, err := Open(context.Background(), client, namespace)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Enqueue(context.Background(), queue, definition, "normal", jobs.AtPriority(50)); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Enqueue(context.Background(), queue, definition, "urgent", jobs.AtPriority(1)); err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Enqueue(context.Background(), queue, definition, "delayed", jobs.After(time.Second)); err != nil {
		t.Fatal(err)
	}

	handled := make(chan string, 3)
	consumer := jobs.On(definition, jobs.Handler[string](func(_ context.Context, payload string) error {
		handled <- payload
		return nil
	}), jobs.Concurrency(1))
	build, err := jobs.ParseBuildID("test:jobsredis")
	if err != nil {
		t.Fatal(err)
	}
	restorer := jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, _ jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
		return jobs.NewRestoredIdentity(ctx, jobs.ProducerPartition{}, jobs.ProducerActor{})
	})
	workers, err := jobs.NewWorkers(jobs.WorkersSpec{
		Namespace:    namespace,
		Catalog:      catalog,
		Driver:       driver,
		Build:        build,
		Identity:     restorer,
		PollInterval: jobs.MinimumPollInterval,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stopRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(runCtx) }()
	for _, expected := range []string{"urgent", "normal"} {
		select {
		case actual := <-handled:
			if actual != expected {
				t.Fatalf("handled %q, want %q", actual, expected)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %q", expected)
		}
	}
	select {
	case value := <-handled:
		t.Fatalf("handled delayed job early: %q", value)
	case <-time.After(30 * time.Millisecond):
	}
	server.SetTime(serverTime.Add(time.Second))
	select {
	case actual := <-handled:
		if actual != "delayed" {
			t.Fatalf("handled %q, want delayed", actual)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for delayed job")
	}
	drainCtx, stopDrain := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopDrain()
	if err := workers.Drain(drainCtx); err != nil {
		t.Fatal(err)
	}
	stopRun()
	select {
	case err := <-runResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not stop")
	}
}

func TestEnqueueOnceSurvivesDriverReopen(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	definition, catalog, namespace := testDefinition(t)
	first, err := Open(context.Background(), client, namespace)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: first})
	if err != nil {
		t.Fatal(err)
	}
	id, outcome, err := jobs.EnqueueOnce(context.Background(), queue, definition, jobs.Intent("invoice:42"), "payload")
	if err != nil || outcome != jobs.EnqueueCreated {
		t.Fatalf("first enqueue = %s, %v", outcome, err)
	}
	second, err := Open(context.Background(), client, namespace)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: second})
	if err != nil {
		t.Fatal(err)
	}
	existing, outcome, err := jobs.EnqueueOnce(context.Background(), reopened, definition, jobs.Intent("invoice:42"), "payload")
	if err != nil {
		t.Fatal(err)
	}
	if existing != id || outcome != jobs.EnqueueExistingSamePayload {
		t.Fatalf("reopened enqueue = %s %s, want %s %s", existing, outcome, id, jobs.EnqueueExistingSamePayload)
	}
}

func testDefinition(t *testing.T) (*jobs.Definition[string], jobs.Catalog, jobs.Namespace) {
	t.Helper()
	name, err := jobs.ParseName("example.deliver")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(1), Policy: policy})
	catalog := jobs.MustCatalog(definition)
	namespace, err := jobs.NamespaceOf("example", "test")
	if err != nil {
		t.Fatal(err)
	}
	return definition, catalog, namespace
}

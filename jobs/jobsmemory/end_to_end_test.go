package jobsmemory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
)

func TestQueueAndWorkersEndToEnd(t *testing.T) {
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
	backend, err := NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: backend})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = jobs.Enqueue(context.Background(), queue, definition, "payload"); err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("test:jobs")
	if err != nil {
		t.Fatal(err)
	}
	handled := make(chan string, 1)
	consumer := jobs.On(definition, jobs.Handler[string](func(_ context.Context, payload string) error {
		handled <- payload
		return nil
	}), jobs.Concurrency(1))
	restorer := jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, _ jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
		return jobs.NewRestoredIdentity(ctx, jobs.ProducerPartition{}, jobs.ProducerActor{})
	})
	workers, err := jobs.NewWorkers(jobs.WorkersSpec{
		Namespace:    namespace,
		Catalog:      catalog,
		Driver:       backend,
		Build:        build,
		Identity:     restorer,
		PollInterval: jobs.MinimumPollInterval,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	runContext, stopRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- workers.Run(runContext) }()
	select {
	case payload := <-handled:
		if payload != "payload" {
			t.Fatalf("payload = %q", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("job was not handled")
	}
	drainContext, stopDrain := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopDrain()
	if err = workers.Drain(drainContext); err != nil {
		t.Fatal(err)
	}
	stopRun()
	select {
	case err = <-runResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("run = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not stop")
	}
}

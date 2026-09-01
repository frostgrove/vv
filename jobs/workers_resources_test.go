package jobs

import (
	"context"
	"testing"
)

func TestWorkersDescriptionResolvesBackendResourcesForPlan(t *testing.T) {
	definition := testQueueDefinition(t, "workers.resources", String(1))
	profile, err := NewResourceProfile(ResourceProfileSpec{
		SteadyBase: ResourcesSpec{MaxConcurrentDBOps: 2},
		PerWorker:  ResourcesSpec{MaxConcurrentDBOps: 1},
		Lifecycle:  ResourcesSpec{MaxConcurrentDBOps: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	description, err := NewBackendDescriptionWithResources(queueTestBackendID(32), queueTestDurability(), Capabilities{Priority: true, Debounce: true, Unique: true, Scheduled: true}, profile)
	if err != nil {
		t.Fatal(err)
	}
	driver := &workersConfigDriver{description: description}
	build, err := ParseBuildID("deploy:resource-test")
	if err != nil {
		t.Fatal(err)
	}
	workers, err := NewWorkers(WorkersSpec{
		Namespace: queueTestNamespace(t, "workers-resources"),
		Catalog:   MustCatalog(definition),
		Driver:    driver,
		Build:     build,
		Identity:  &workersConfigIdentity{},
	}, On(definition, Handler[string](func(context.Context, string) error { return nil }), Concurrency(7)))
	if err != nil {
		t.Fatal(err)
	}
	resolved := workers.Describe()
	if resolved.Plan.TotalConcurrency != 7 || resolved.Resources.PinnedConnections() != 0 || resolved.Resources.MaxConcurrentDBOps() != 9 || resolved.Resources.MaxConcurrentRemoteOps() != 0 || !resolved.Resources.IsComplete() {
		t.Fatalf("runtime resources = %v", resolved.Resources)
	}
	if resolved.Lifecycle.MaxConcurrentDBOps() != 1 || !resolved.Lifecycle.IsComplete() {
		t.Fatalf("lifecycle resources = %v", resolved.Lifecycle)
	}
	resolved.Resources = Resources{}
	resolved.Lifecycle = Resources{}
	fresh := workers.Describe()
	if fresh.Resources.MaxConcurrentDBOps() != 9 || fresh.Lifecycle.MaxConcurrentDBOps() != 1 {
		t.Fatal("workers resource description was mutable")
	}
}

func TestWorkersDescriptionMarksLegacyResourceContractIncomplete(t *testing.T) {
	spec, consumer, _, _ := workersConfigFixture(t, "workers.legacy-resources")
	workers, err := NewWorkers(spec, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if workers.Describe().Resources.IsComplete() || workers.Describe().Lifecycle.IsComplete() {
		t.Fatal("legacy backend resources were reported as exact zero")
	}
}

package jobsredis

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisDescriptionPublishesRemoteNotDatabaseResources(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	_, _, namespace := testDefinition(t)
	driver, err := New(Spec{Client: client, Namespace: namespace})
	if err != nil {
		t.Fatal(err)
	}
	profile := driver.Description().ResourceProfile()
	resources, err := profile.Resolve(7)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.IsDeclared() || resources.PinnedConnections() != 0 || resources.MaxConcurrentDBOps() != 0 || resources.MaxConcurrentRemoteOps() != 9 || !resources.IsComplete() {
		t.Fatalf("Redis resources = %v", resources)
	}
	lifecycle := profile.Lifecycle()
	if lifecycle.PinnedConnections() != 0 || lifecycle.MaxConcurrentDBOps() != 0 || lifecycle.MaxConcurrentRemoteOps() != 1 || !lifecycle.IsComplete() {
		t.Fatalf("Redis lifecycle resources = %v", lifecycle)
	}
	next, err := profile.Resolve(8)
	if err != nil {
		t.Fatal(err)
	}
	if next.MaxConcurrentRemoteOps() != 10 {
		t.Fatal("Redis resources were not resolved from the actual worker concurrency")
	}
}

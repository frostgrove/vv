package jobspg

import (
	"database/sql"
	"testing"
)

func TestPostgresDescriptionPublishesAffineWorkerResources(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	profile := driver.Description().ResourceProfile()
	resources, err := profile.Resolve(7)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.IsDeclared() || resources.PinnedConnections() != 0 || resources.MaxConcurrentDBOps() != 9 || resources.MaxConcurrentRemoteOps() != 0 || !resources.IsComplete() {
		t.Fatalf("PostgreSQL resources = %v", resources)
	}
	lifecycle := profile.Lifecycle()
	if lifecycle.PinnedConnections() != 1 || lifecycle.MaxConcurrentDBOps() != 1 || lifecycle.MaxConcurrentRemoteOps() != 0 || !lifecycle.IsComplete() {
		t.Fatalf("PostgreSQL lifecycle resources = %v", lifecycle)
	}
	next, err := profile.Resolve(8)
	if err != nil {
		t.Fatal(err)
	}
	if profile.PerWorker().MaxConcurrentDBOps() != 1 || profile.SteadyBase().MaxConcurrentDBOps() != 2 || next.MaxConcurrentDBOps() != 10 {
		t.Fatal("PostgreSQL resources were not resolved from the actual worker concurrency")
	}
}

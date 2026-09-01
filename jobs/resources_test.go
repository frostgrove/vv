package jobs

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
)

func TestResourceProfileResolvesActualWorkerConcurrency(t *testing.T) {
	spec := ResourceProfileSpec{
		SteadyBase: ResourcesSpec{PinnedConnections: 1, MaxConcurrentDBOps: 2, MaxConcurrentRemoteOps: 3},
		PerWorker:  ResourcesSpec{PinnedConnections: 2, MaxConcurrentDBOps: 3, MaxConcurrentRemoteOps: 4},
		Lifecycle:  ResourcesSpec{PinnedConnections: 5, MaxConcurrentDBOps: 6, MaxConcurrentRemoteOps: 7},
	}
	profile, err := NewResourceProfile(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.SteadyBase.MaxConcurrentDBOps = MaxResourceUnits
	resolved, err := profile.Resolve(5)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.IsDeclared() || resolved.PinnedConnections() != 11 || resolved.MaxConcurrentDBOps() != 17 || resolved.MaxConcurrentRemoteOps() != 23 || !resolved.IsComplete() || resolved.IsEmpty() {
		t.Fatalf("resolved resources = %v", resolved)
	}
	lifecycle := profile.Lifecycle()
	if lifecycle.PinnedConnections() != 5 || lifecycle.MaxConcurrentDBOps() != 6 || lifecycle.MaxConcurrentRemoteOps() != 7 || !lifecycle.IsComplete() {
		t.Fatalf("lifecycle resources = %v", lifecycle)
	}
	if profile.SteadyBase().MaxConcurrentDBOps() != 2 || profile.PerWorker().MaxConcurrentDBOps() != 3 {
		t.Fatal("resource profile retained mutable input")
	}
	if fmt.Sprint(profile) != "[job resource profile declared=true]" || slog.AnyValue(resolved).Resolve().String() != resolved.String() {
		t.Fatal("resource descriptions are not stable")
	}
}

func TestResourcesDistinguishExactZeroFromUnknown(t *testing.T) {
	exact, err := NewResources(ResourcesSpec{})
	if err != nil {
		t.Fatal(err)
	}
	unknown := Resources{}
	if !exact.IsComplete() || !exact.IsEmpty() || unknown.IsComplete() || unknown.IsEmpty() {
		t.Fatalf("exact=%v unknown=%v", exact, unknown)
	}
	known, err := NewResources(ResourcesSpec{PinnedConnections: 1, MaxConcurrentDBOps: 2, MaxConcurrentRemoteOps: 3})
	if err != nil {
		t.Fatal(err)
	}
	total, err := SumResources(exact, known)
	if err != nil {
		t.Fatal(err)
	}
	if !total.IsComplete() || total.PinnedConnections() != 1 || total.MaxConcurrentDBOps() != 2 || total.MaxConcurrentRemoteOps() != 3 {
		t.Fatalf("complete total = %v", total)
	}
	partial, err := SumResources(known, unknown)
	if err != nil {
		t.Fatal(err)
	}
	if partial.IsComplete() || partial.PinnedConnections() != 1 || partial.MaxConcurrentDBOps() != 2 || partial.MaxConcurrentRemoteOps() != 3 {
		t.Fatalf("partial total = %v", partial)
	}
	empty, err := SumResources()
	if err != nil || !empty.IsComplete() || !empty.IsEmpty() {
		t.Fatalf("empty total = (%v, %v)", empty, err)
	}
}

func TestResourceBoundsRejectInvalidAndOverflowingContracts(t *testing.T) {
	for _, spec := range []ResourcesSpec{
		{PinnedConnections: -1},
		{MaxConcurrentDBOps: -1},
		{MaxConcurrentRemoteOps: -1},
	} {
		if _, err := NewResources(spec); !errors.Is(err, ErrInvalid) {
			t.Fatalf("negative resources %#v = %v", spec, err)
		}
	}
	for _, spec := range []ResourcesSpec{
		{PinnedConnections: MaxResourceUnits + 1},
		{MaxConcurrentDBOps: MaxResourceUnits + 1},
		{MaxConcurrentRemoteOps: MaxResourceUnits + 1},
	} {
		if _, err := NewResources(spec); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("oversized resources %#v = %v", spec, err)
		}
	}
	profile, err := NewResourceProfile(ResourceProfileSpec{PerWorker: ResourcesSpec{MaxConcurrentDBOps: MaxResourceUnits}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Resolve(0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero concurrency = %v", err)
	}
	if _, err := profile.Resolve(MaxWorkerConcurrency + 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized concurrency = %v", err)
	}
	if _, err := profile.Resolve(2); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("resolved overflow = %v", err)
	}
	maximum, err := NewResources(ResourcesSpec{MaxConcurrentDBOps: MaxResourceUnits})
	if err != nil {
		t.Fatal(err)
	}
	one, err := NewResources(ResourcesSpec{MaxConcurrentDBOps: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SumResources(maximum, one); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("sum overflow = %v", err)
	}
}

func TestBackendDescriptionSupportsDeclaredAndLegacyResourceContracts(t *testing.T) {
	id := queueTestBackendID(31)
	durability := queueTestDurability()
	capabilities := Capabilities{Priority: true}
	legacy, err := NewBackendDescription(id, durability, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ResourceProfile().IsDeclared() || !legacy.valid() {
		t.Fatalf("legacy description = %v", legacy.ResourceProfile())
	}
	profile, err := NewResourceProfile(ResourceProfileSpec{SteadyBase: ResourcesSpec{MaxConcurrentDBOps: 2}})
	if err != nil {
		t.Fatal(err)
	}
	description, err := NewBackendDescriptionWithResources(id, durability, capabilities, profile)
	if err != nil {
		t.Fatal(err)
	}
	if description.ResourceProfile() != profile || !description.ResourceProfile().IsDeclared() || !description.valid() {
		t.Fatalf("declared description = %v", description.ResourceProfile())
	}
	if _, err := NewBackendDescriptionWithResources(id, durability, capabilities, ResourceProfile{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing resource profile = %v", err)
	}
}

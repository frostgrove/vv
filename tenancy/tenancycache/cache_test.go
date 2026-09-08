package tenancycache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/tenancy"
)

func fixedAuthority(t *testing.T, raw string, state tenancy.Lifecycle, generation uint64) *tenancy.Authority {
	t.Helper()
	reference, err := tenancy.ParseReference(raw)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := tenancy.NewEpoch(generation)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver: tenancy.Fixed{Reference: reference, Lifecycle: state, Epoch: epoch},
	})
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func keyed(t *testing.T, authority *tenancy.Authority, key string) Key[string] {
	t.Helper()
	ctx, err := authority.Bind(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	value, err := Keyed(ctx, authority, tenancy.ClassRead, key)
	if err != nil {
		t.Fatalf("cannot build the cache key: %v", err)
	}
	return value
}

func partitionOf(t *testing.T, key Key[string], limit cache.KeyLimit) []byte {
	t.Helper()
	partition, err := Partition[string]()(key, limit)
	if err != nil {
		t.Fatal(err)
	}
	return partition
}

func TestTwoTenantsWithEqualCacheKeysStayApart(t *testing.T) {
	limit := cache.KeyLimit{MaxBytes: 64}
	ours := partitionOf(t, keyed(t, fixedAuthority(t, "acme", tenancy.Active, 1), "invoice:1"), limit)
	theirs := partitionOf(t, keyed(t, fixedAuthority(t, "globex", tenancy.Active, 1), "invoice:1"), limit)

	if string(ours) == string(theirs) {
		t.Fatal("one logical key resolved to one cache entry for two tenants")
	}
}

func TestARestoredGenerationReadsNoneOfThePreviousOnesValues(t *testing.T) {
	limit := cache.KeyLimit{MaxBytes: 64}
	before := partitionOf(t, keyed(t, fixedAuthority(t, "acme", tenancy.Active, 1), "invoice"), limit)
	after := partitionOf(t, keyed(t, fixedAuthority(t, "acme", tenancy.Active, 2), "invoice"), limit)

	if string(before) == string(after) {
		t.Fatal("the cache still serves the previous generation's values")
	}
}

func TestAPartitionWithNoScopeIsRefused(t *testing.T) {
	if _, err := Partition[string]()(Key[string]{key: "invoice"}, cache.KeyLimit{MaxBytes: 64}); !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("err = %v — an unverified request reached the cache", err)
	}
}

// A partition narrower than this is a tenant sharing an eviction domain with
// whoever else lands on its prefix, which is worse than refusing the operation.
func TestAPartitionTooNarrowToIdentifyATenantIsRefused(t *testing.T) {
	key := keyed(t, fixedAuthority(t, "acme", tenancy.Active, 1), "invoice")
	if _, err := Partition[string]()(key, cache.KeyLimit{MaxBytes: MinPartitionBytes - 1}); !errors.Is(err, tenancy.ErrIncompatible) {
		t.Fatalf("err = %v, want ErrIncompatible", err)
	}
	if got := len(partitionOf(t, key, cache.KeyLimit{MaxBytes: MinPartitionBytes})); got != MinPartitionBytes {
		t.Fatalf("the narrowest allowed budget produced %d bytes", got)
	}
	if got := len(partitionOf(t, key, cache.KeyLimit{MaxBytes: MaxPartitionBytes + 8})); got != MaxPartitionBytes {
		t.Fatalf("a budget wider than the digest produced %d bytes", got)
	}
}

func TestACacheKeyIsRefusedWhenTheClassIsNot(t *testing.T) {
	reference, err := tenancy.ParseReference("acme")
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver:  tenancy.Fixed{Reference: reference, Lifecycle: tenancy.Suspended, Epoch: epoch},
		Admission: tenancy.Admit(tenancy.ClassRead, tenancy.Active, tenancy.Suspended).Merge(tenancy.Admit(tenancy.ClassWrite, tenancy.Active)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := authority.Bind(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Keyed(ctx, authority, tenancy.ClassRead, "invoice"); err != nil {
		t.Fatalf("the read this deployment allows was refused: %v", err)
	}
	if _, err := Keyed(ctx, authority, tenancy.ClassWrite, "invoice"); !errors.Is(err, tenancy.ErrInactive) {
		t.Fatalf("err = %v, want ErrInactive — a suspended tenant addressed a cache entry to write", err)
	}
}

func TestTheCacheSeamRefusesAScopeAnotherAuthorityMinted(t *testing.T) {
	mine := fixedAuthority(t, "acme", tenancy.Active, 1)
	theirs := fixedAuthority(t, "acme", tenancy.Active, 1)

	carried, err := theirs.Bind(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Keyed(carried, mine, tenancy.ClassRead, "invoice"); !errors.Is(err, tenancy.ErrUntrusted) {
		t.Fatalf("err = %v, want ErrUntrusted — a scope this authority never minted addressed a cache entry", err)
	}

	own, err := mine.Bind(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Keyed(own, mine, tenancy.ClassRead, "invoice"); err != nil {
		t.Fatalf("the control refuses too, so the assertion above proves nothing: %v", err)
	}
}

// The request is bound and the wiring is not: whoever assembled this seam left
// the authority out, and what reaches it is a scope somebody else minted. There
// is nothing here that can check it, so the answer is a refusal rather than a
// panic in a handler that had a perfectly good tenant.
func TestASeamWiredWithNoAuthorityRefusesRatherThanPanics(t *testing.T) {
	ctx, err := fixedAuthority(t, "acme", tenancy.Active, 1).Bind(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Keyed(ctx, nil, tenancy.ClassRead, "invoice"); !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("err = %v, want ErrNoScope — a seam wired without an authority addressed a cache entry", err)
	}
}

// Partition is exercised above as a function; Partitioned is what a deployment
// actually wires, and until this test nothing walked it. Replacing its body with
// cache.Global left the whole suite green, which is the definition of a hole:
// every assertion above compares two partitions to each other and none of them
// asks whether the partition reaches the cache at all.
func partitionedCache(t *testing.T) *cache.Cache[Key[string], string] {
	t.Helper()
	backend, err := cachememory.New(cachememory.Limits{MaxEntries: 128, MaxBytes: 1 << 20, MaxItemBytes: 1 << 16})
	if err != nil {
		t.Fatal(err)
	}
	keys := cache.MustKeyFunc(1, func(key Key[string], limit cache.KeyLimit) ([]byte, error) {
		// Only the logical key. Everything separating the tenants has to come
		// from the scope, or this test proves the key codec rather than the seam.
		encoded := []byte(key.Unwrap())
		if len(encoded) > limit.MaxBytes {
			return nil, cache.ErrTooLarge
		}
		return encoded, nil
	})
	instance, err := cache.New(
		cache.Runtime{ClockSkew: cache.SingleProcessClock()},
		backend,
		Partitioned[string](cache.MustNamespace("billing", "test", "invoices", 1)),
		keys,
		cache.String(1),
		cache.Policy{
			Freshness:        cache.Expiring(time.Hour, time.Hour),
			Retention:        cache.ExpireAfter(3 * time.Hour),
			Negative:         cache.NoNegativeCaching(),
			Jitter:           cache.NoJitter(),
			MaxKeyBytes:      256,
			MaxValueBytes:    4 << 10,
			MaxValueDepth:    16,
			MaxFlights:       8,
			FlightSaturation: cache.WaitBounded(time.Hour),
			Stale:            cache.RefreshBlocking,
			LastWaiter:       cache.CancelLoader,
			Corruption:       cache.RefuseCorrupt,
		},
	)
	if err != nil {
		t.Fatalf("cannot build a cache over the tenant partition: %v", err)
	}
	return instance
}

func TestOneTenantsCachedValueIsNotReadByAnother(t *testing.T) {
	instance := partitionedCache(t)
	ours := keyed(t, fixedAuthority(t, "acme", tenancy.Active, 1), "invoice:1")
	theirs := keyed(t, fixedAuthority(t, "globex", tenancy.Active, 1), "invoice:1")

	if err := instance.Put(context.Background(), ours, "acme's invoice"); err != nil {
		t.Fatalf("put: %v", err)
	}

	// The control. Without it a Partitioned that refused every read would pass
	// the assertion below and prove nothing.
	mine, err := instance.Lookup(context.Background(), ours)
	if err != nil {
		t.Fatalf("the tenant that wrote the value cannot read it back: %v", err)
	}
	if mine.State != cache.Hit || mine.Value != "acme's invoice" {
		t.Fatalf("the writing tenant read state=%v value=%q", mine.State, mine.Value)
	}

	other, err := instance.Lookup(context.Background(), theirs)
	if err != nil {
		t.Fatalf("the second tenant's lookup failed for some other reason: %v", err)
	}
	if other.State != cache.Miss {
		t.Fatalf("one tenant read another's cached value: state=%v %q", other.State, other.Value)
	}
}

func TestARestoredGenerationDoesNotReadThePreviousOnesCachedValue(t *testing.T) {
	instance := partitionedCache(t)
	before := keyed(t, fixedAuthority(t, "acme", tenancy.Active, 1), "invoice:1")
	after := keyed(t, fixedAuthority(t, "acme", tenancy.Active, 2), "invoice:1")

	if err := instance.Put(context.Background(), before, "written before the restore"); err != nil {
		t.Fatalf("put: %v", err)
	}
	restored, err := instance.Lookup(context.Background(), after)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if restored.State != cache.Miss {
		t.Fatalf("a restored generation read the previous one's value: state=%v %q", restored.State, restored.Value)
	}
}

package tenancycache

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/cache"
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

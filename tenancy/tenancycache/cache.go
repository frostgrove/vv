package tenancycache

import (
	"context"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/tenancy"
)

const (
	MinPartitionBytes = 16
	MaxPartitionBytes = 32
)

// A cache partitions on the key it is handed, not on a context, so a tenant-owned
// key carries its own scope and cannot be built without one.
type Key[K any] struct {
	scope tenancy.Scope
	key   K
}

// The key is where a cache reads its tenant from, so building one is the moment
// the scope has to be checked: a bare Scope in hand says an authority minted it
// once, not that this authority admits this class of work now.
func Keyed[K any](ctx context.Context, authority *tenancy.Authority, class tenancy.Class, key K) (Key[K], error) {
	scope, err := authority.Scope(ctx, class)
	if err != nil {
		return Key[K]{}, err
	}
	return Key[K]{scope: scope, key: key}, nil
}

func (this Key[K]) Unwrap() K            { return this.key }
func (this Key[K]) Scope() tenancy.Scope { return this.scope }
func (this Key[K]) String() string       { return "[tenant cache key]" }

// The budget a cache hands a partitioner is what is left of the key allowance
// after the key itself, not the whole allowance — so demanding a full digest
// would refuse every operation on a deployment with a long key and a short
// MaxKeyBytes. The cache re-digests whatever comes back, so the partition only
// has to identify the tenant: it takes as much of the digest as the budget
// allows, and refuses below the width where a collision stops being unthinkable.
func Partition[K any]() cache.Partitioner[Key[K]] {
	return func(key Key[K], limit cache.KeyLimit) ([]byte, error) {
		if key.scope.IsZero() {
			return nil, tenancy.ErrNoScope
		}
		if limit.MaxBytes < MinPartitionBytes {
			return nil, tenancy.ErrIncompatible
		}
		digest := key.scope.Digest()
		return digest[:min(limit.MaxBytes, MaxPartitionBytes)], nil
	}
}

func Partitioned[K any](namespace cache.Namespace) cache.Scope[Key[K]] {
	return cache.Partitioned(namespace, Partition[K]())
}

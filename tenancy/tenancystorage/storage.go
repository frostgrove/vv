package tenancystorage

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/frostgrove/vv/storage"
	"github.com/frostgrove/vv/tenancy"
)

// The widest prefix the object store's own namespace rule leaves once this seam
// has spent its share of it on the digest. Pinned from both sides by
// TestAPrefixIsRefusedInThisPackagesVocabularyOrLeavesRoomForTheDigest, because
// the budget it divides is `storage`'s and unexported.
const (
	MaxPrefixBytes = 30
	digestBytes    = 16
)

// Resolving the scope for the class is the check, and it happens here rather than
// at the bind: holding a scope says an authority minted it once, not that this
// authority admits objects to be written under it now.
func Namespace(ctx context.Context, authority *tenancy.Authority, prefix string, class tenancy.Class) (storage.Namespace, error) {
	scope, err := authority.Scope(ctx, class)
	if err != nil {
		return storage.Namespace{}, err
	}
	return namespaceOf(prefix, scope)
}

func Store(ctx context.Context, authority *tenancy.Authority, prefix string, class tenancy.Class, backend storage.Backend) (storage.Store, error) {
	namespace, err := Namespace(ctx, authority, prefix, class)
	if err != nil {
		return nil, err
	}
	return storage.New(&storage.Config{Namespace: namespace.Value(), Backend: backend})
}

// The namespace is a digest and not the reference, for two reasons that pull the
// same way. A bucket path is quoted in provider errors, access logs and support
// tickets, and a customer's own name has no business being read there by whoever
// reads those; and a digest is fixed width, so no tenant's namespace can be a
// prefix of another's however the references were named. The generation is part
// of the digest, so a tenant restored to a new generation reads none of the
// previous one's objects.
//
// It is an address and not an anonymisation: it is stable, and it is derived from
// a reference an attacker can guess, so it must not travel into telemetry any
// more than the reference itself may.
//
// What a namespace may be is `storage`'s rule and is checked there, once:
// `MaxPrefixBytes` is what is left of that budget after this seam's digest, and
// a second copy of the bound here would be a second thing to keep in step.
// The refusal is answered in this package's vocabulary, because a caller that has
// to branch on two error taxonomies branches on neither.
func namespaceOf(prefix string, scope tenancy.Scope) (storage.Namespace, error) {
	if scope.IsZero() {
		return storage.Namespace{}, tenancy.ErrNoScope
	}
	digest := scope.Digest()
	namespace, err := storage.ParseNamespace(prefix + "-" + hex.EncodeToString(digest[:digestBytes]))
	if err != nil {
		return storage.Namespace{}, fmt.Errorf("tenancy: a prefix of 1..%d portable bytes addresses objects, and %q does not: %w",
			MaxPrefixBytes, prefix, tenancy.ErrMalformed)
	}
	return namespace, nil
}

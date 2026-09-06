package tenancystorage

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/tenancy"
)

func fixedAuthority(t *testing.T, raw string, state tenancy.Lifecycle, generation uint64) (*tenancy.Authority, tenancy.Scope) {
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
	scope, err := authority.Verify(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatalf("the authority refused a %s tenant: %v", state, err)
	}
	return authority, scope
}

func TestAnObjectNamespaceIsInjectiveAndNoTenantsIsAPrefixOfAnothers(t *testing.T) {
	const tenants = 500
	seen := make(map[string]string, tenants)

	for i := range tenants {
		raw := "tenant-" + strings.Repeat("x", i%7) + strconv.Itoa(i)
		_, scope := fixedAuthority(t, raw, tenancy.Active, 1)
		namespace, err := namespaceOf("objects", scope)
		if err != nil {
			t.Fatalf("%q has no namespace: %v", raw, err)
		}
		if previous, collided := seen[namespace.Value()]; collided {
			t.Fatalf("%q and %q share the namespace %q", previous, raw, namespace.Value())
		}
		for other := range seen {
			if strings.HasPrefix(other, namespace.Value()) || strings.HasPrefix(namespace.Value(), other) {
				t.Fatalf("%q contains %q, so a listing of one reaches the other", other, namespace.Value())
			}
		}
		seen[namespace.Value()] = raw
	}
	if len(seen) != tenants {
		t.Fatalf("%d tenants produced %d namespaces", tenants, len(seen))
	}
}

func TestARestoredGenerationReadsNoneOfThePreviousOnesObjects(t *testing.T) {
	_, first := fixedAuthority(t, "acme", tenancy.Active, 1)
	_, second := fixedAuthority(t, "acme", tenancy.Active, 2)

	before, err := namespaceOf("objects", first)
	if err != nil {
		t.Fatal(err)
	}
	after, err := namespaceOf("objects", second)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("a tenant restored to a new generation still reads the objects the previous one left")
	}
}

func TestANamespaceWithNoScopeIsRefused(t *testing.T) {
	if _, err := namespaceOf("objects", tenancy.Scope{}); !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("err = %v — an unverified request reached a bucket", err)
	}
}

// The prefix budget is this seam's and the rest of the namespace is `storage`'s,
// so the two have to agree: a prefix of exactly the advertised size must still
// leave room for the digest. Every refusal is answered in this package's
// vocabulary, because a caller that has to branch on two error taxonomies
// branches on neither.
func TestAPrefixIsRefusedInThisPackagesVocabularyOrLeavesRoomForTheDigest(t *testing.T) {
	_, scope := fixedAuthority(t, "acme", tenancy.Active, 1)

	widest := strings.Repeat("p", MaxPrefixBytes)
	namespace, err := namespaceOf(widest, scope)
	if err != nil {
		t.Fatalf("a prefix of the advertised %d bytes was refused, so the seam and the store disagree about the budget: %v", MaxPrefixBytes, err)
	}
	suffix := strings.TrimPrefix(namespace.Value(), widest+"-")
	if len(suffix) < 32 {
		t.Fatalf("the namespace carries %d hex characters of digest, which is fewer than the 128 bits two tenants must not collide in", len(suffix))
	}

	for name, prefix := range map[string]string{
		"empty":           "",
		"over the budget": strings.Repeat("p", MaxPrefixBytes+1),
		"not portable":    "Invoices",
		"reserved shape":  "-objects",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := namespaceOf(prefix, scope); !errors.Is(err, tenancy.ErrMalformed) {
				t.Fatalf("err = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestANamespaceIsRefusedWhenTheClassIsNot(t *testing.T) {
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
	if _, err := Namespace(ctx, authority, "objects", tenancy.ClassWrite); !errors.Is(err, tenancy.ErrInactive) {
		t.Fatalf("err = %v, want ErrInactive — a suspended tenant named its bucket to write into", err)
	}
	if _, err := Namespace(ctx, authority, "objects", tenancy.ClassRead); err != nil {
		t.Fatalf("the read this deployment allows was refused: %v", err)
	}
}

func TestTheObjectSeamRefusesAScopeAnotherAuthorityMinted(t *testing.T) {
	mine, _ := fixedAuthority(t, "acme", tenancy.Active, 1)
	theirs, foreign := fixedAuthority(t, "acme", tenancy.Active, 1)

	carried, err := theirs.With(context.Background(), foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Namespace(carried, mine, "objects", tenancy.ClassRead); !errors.Is(err, tenancy.ErrUntrusted) {
		t.Fatalf("err = %v, want ErrUntrusted — a scope this authority never minted named a bucket", err)
	}

	own, err := mine.Bind(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Namespace(own, mine, "objects", tenancy.ClassRead); err != nil {
		t.Fatalf("the control refuses too, so the assertions above prove nothing: %v", err)
	}
}

// The request is bound and the wiring is not: whoever assembled this seam left
// the authority out, and what reaches it is a scope somebody else minted. There
// is nothing here that can check it, so the answer is a refusal rather than a
// panic in a handler that had a perfectly good tenant.
func TestASeamWiredWithNoAuthorityRefusesRatherThanPanics(t *testing.T) {
	authority, _ := fixedAuthority(t, "acme", tenancy.Active, 1)
	ctx, err := authority.Bind(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Namespace(ctx, nil, "objects", tenancy.ClassRead); !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("err = %v, want ErrNoScope — a seam wired without an authority named a bucket", err)
	}
}

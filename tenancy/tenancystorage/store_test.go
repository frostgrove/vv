package tenancystorage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/frostgrove/vv/storage"
	"github.com/frostgrove/vv/storage/storagefs"
	"github.com/frostgrove/vv/tenancy"
)

func boundStore(t *testing.T, backend storage.Backend, raw string, generation uint64) storage.Store {
	t.Helper()
	authority, _ := fixedAuthority(t, raw, tenancy.Active, generation)
	ctx, err := authority.Bind(context.Background(), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Store(ctx, authority, "invoices", tenancy.ClassWrite, backend)
	if err != nil {
		t.Fatalf("cannot build the store: %v", err)
	}
	return store
}

func put(t *testing.T, store storage.Store, key, body string) {
	t.Helper()
	name, err := storage.ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), name, strings.NewReader(body), storage.PutOptions{}); err != nil {
		t.Fatalf("cannot write the object: %v", err)
	}
}

func read(t *testing.T, store storage.Store, key string) (string, error) {
	t.Helper()
	name, err := storage.ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := store.Open(context.Background(), name, storage.ReadOptions{})
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	read, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(read), nil
}

func backendIn(t *testing.T) storage.Backend {
	t.Helper()
	backend, err := storagefs.New(&storagefs.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("cannot build the object backend: %v", err)
	}
	return backend
}

// Store is the function a composition root actually calls, and until now nothing
// exercised it: every test reached namespaceOf or Namespace directly, so a Store
// that dropped the namespace and put every tenant in one place would have shipped
// green. Two tenants writing the same logical key is the whole question.
func TestTwoTenantsWritingOneKeyThroughStoreDoNotShareTheObject(t *testing.T) {
	backend := backendIn(t)
	acme := boundStore(t, backend, "acme", 1)
	globex := boundStore(t, backend, "globex", 1)

	put(t, acme, "invoice.txt", "acme-secret")
	put(t, globex, "invoice.txt", "globex-secret")

	mine, err := read(t, acme, "invoice.txt")
	if err != nil {
		t.Fatalf("a tenant could not read back its own object: %v", err)
	}
	if mine != "acme-secret" {
		t.Fatalf("one tenant read another's object through Store: %q", mine)
	}

	theirs, err := read(t, globex, "invoice.txt")
	if err != nil {
		t.Fatalf("the second tenant could not read back its own object: %v", err)
	}
	if theirs != "globex-secret" {
		t.Fatalf("the second tenant read the first one's object through Store: %q", theirs)
	}
}

// The generation is inside the namespace digest, so a tenant restored to a new
// one addresses none of the objects the previous generation left. Store is where
// that has to survive, because it is the object handle business code holds.
func TestAStoreForARestoredGenerationReadsNoneOfThePreviousOnes(t *testing.T) {
	backend := backendIn(t)

	put(t, boundStore(t, backend, "acme", 1), "invoice.txt", "first-generation")

	restored := boundStore(t, backend, "acme", 2)
	if body, err := read(t, restored, "invoice.txt"); err == nil {
		t.Fatalf("a restored generation read the previous one's object: %q", body)
	} else if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("the read failed for the wrong reason: %v", err)
	}

	// The control: the generation that wrote it still reads it, so the test above
	// is not passing because nothing was ever written.
	if body, err := read(t, boundStore(t, backend, "acme", 1), "invoice.txt"); err != nil || body != "first-generation" {
		t.Fatalf("the generation that wrote the object cannot read it: %q %v", body, err)
	}
}

// A store is built from a scope the authority admits for the class, so a tenant
// whose lifecycle no longer admits writes does not get to name its bucket.
func TestAStoreIsRefusedWhenTheLifecycleDoesNotAdmitTheClass(t *testing.T) {
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
		t.Fatalf("a suspended tenant this deployment still admits for reads could not bind: %v", err)
	}
	if _, err := Store(ctx, authority, "invoices", tenancy.ClassWrite, backendIn(t)); err == nil {
		t.Fatal("a suspended tenant was handed a store to write objects into")
	}
	if _, err := Store(ctx, authority, "invoices", tenancy.ClassRead, backendIn(t)); err != nil {
		t.Fatalf("the same tenant was refused the reads this deployment does admit: %v", err)
	}
}

package audit_test

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/audit"
)

func testCatalogRef() audit.CatalogRef {
	var digest audit.CatalogDigest
	digest[0] = 1
	return audit.CatalogRef{ID: "application", Generation: 1, Digest: digest}
}

func TestStoreCatalogStatesKeepEmptyInactiveAndActiveDistinct(t *testing.T) {
	empty := audit.NewEmptyStoreCatalogState()
	if empty.HasActive() || empty.Active() != (audit.CatalogRef{}) || empty.SetDigest() != (audit.CatalogSetDigest{}) {
		t.Fatalf("empty state = %+v", empty)
	}
	var set audit.CatalogSetDigest
	set[0] = 1
	inactive, err := audit.NewInactiveStoreCatalogState(set)
	if err != nil || inactive.HasActive() || inactive.SetDigest() != set {
		t.Fatalf("inactive state = (%+v, %v)", inactive, err)
	}
	active, err := audit.NewStoreCatalogState(testCatalogRef(), set)
	if err != nil || !active.HasActive() || active.Active() != testCatalogRef() || active.SetDigest() != set {
		t.Fatalf("active state = (%+v, %v)", active, err)
	}
	if _, err := audit.NewInactiveStoreCatalogState(audit.CatalogSetDigest{}); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("zero inactive digest = %v", err)
	}
	if _, err := audit.NewStoreCatalogState(audit.CatalogRef{}, set); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("zero active ref = %v", err)
	}
}

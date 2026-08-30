package catalog

import (
	"context"
	"fmt"
	"sync"

	"github.com/frostgrove/vv/crud"
)

// Set is a caller's catalogs, one per physical database handle. The zero value
// is ready.
//
// It is a value the application holds, never a package-level variable. A global
// is the same bug [[D-032]] and [[D-027]] already exist for wearing different
// clothes: right in every single-database test, wrong in the deployment that
// matters, and silent either way.
//
// Entries live in a slice compared with crud.SameDataSource rather than in a
// map[any], and that is not a style choice. A datasource handle is a pointer in
// practice but nothing in the contract says it must be, and an uncomparable map
// key panics at run time ([[D-041]]).
type Set struct {
	mu      sync.Mutex
	entries []entry
}

type entry struct {
	key any
	cat Catalog
}

// Load answers the catalog for src, reading the schema the first time and
// returning the same catalog every time after.
//
// The key is crud.KeyOf, so a raw handle, a source over it and a crud.ReadWrite
// pair over that source all land on one catalog — the same rule
// crud.ExecutorFor decides "is there a transaction for MY database" with. It is
// reused rather than restated: two implementations of one identity rule drift,
// and the drift here is a repository running a statement on one connection while
// consulting a schema read from another.
//
// A handle whose identity cannot be compared is refused before any statement
// runs. Stored, it could never be found again, so every Load would re-introspect
// and every For would miss — a catalog that looks like it is working.
func (this *Set) Load(ctx context.Context, source crud.Source) (Catalog, error) {
	k := crud.KeyOf(source)
	if !findable(k) {
		return nil, fmt.Errorf("%w: %T", ErrUncomparableHandle, k)
	}

	this.mu.Lock()
	for _, e := range this.entries {
		if crud.SameDataSource(e.key, k) {
			this.mu.Unlock()
			return e.cat, nil
		}
	}
	this.mu.Unlock()

	// Loaded outside the lock: introspection is several round trips, and holding
	// the lock across them would serialise the start-up of every other database
	// in the process behind the slowest one.
	cat, err := Load(ctx, source)
	if err != nil {
		return nil, err
	}

	this.mu.Lock()
	defer this.mu.Unlock()
	// Two goroutines declaring over one handle at once both read; the first one
	// back wins, so everybody afterwards sees one catalog rather than two.
	for _, e := range this.entries {
		if crud.SameDataSource(e.key, k) {
			return e.cat, nil
		}
	}
	this.entries = append(this.entries, entry{key: k, cat: cat})
	return cat, nil
}

// findable reports whether a key can be looked up again.
//
// The test is the lookup itself rather than a reflect call of our own, because a
// second spelling of the rule can disagree with the one that does the finding.
// SameDataSource uses reflect.Value.Comparable, so it checks dynamic values held
// in interface fields as well as the outer static type and returns false instead
// of panicking when either value cannot be compared.
func findable(k any) bool {
	return crud.SameDataSource(k, k)
}

// For answers the catalog already loaded for src. It does no I/O and takes no
// context — a lookup that could load is a lazy loader, and a lazy loader cannot
// fail at start-up.
func (this *Set) For(source crud.Source) (Catalog, bool) {
	k := crud.KeyOf(source)
	this.mu.Lock()
	defer this.mu.Unlock()
	for _, e := range this.entries {
		if crud.SameDataSource(e.key, k) {
			return e.cat, true
		}
	}
	return nil, false
}

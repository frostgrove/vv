package catalog

import (
	"context"
	"fmt"
	"sync"

	"github.com/frostgrove/vv/crud"
)

type Set struct {
	mu      sync.Mutex
	entries []entry
}

type entry struct {
	key any
	cat Catalog
}

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

	cat, err := Load(ctx, source)
	if err != nil {
		return nil, err
	}

	this.mu.Lock()
	defer this.mu.Unlock()

	for _, e := range this.entries {
		if crud.SameDataSource(e.key, k) {
			return e.cat, nil
		}
	}
	this.entries = append(this.entries, entry{key: k, cat: cat})
	return cat, nil
}

func findable(k any) bool {
	return crud.SameDataSource(k, k)
}

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

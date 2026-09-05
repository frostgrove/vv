package storage_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/frostgrove/vv/storage"
)

type fakeStore struct {
	log  *[]string
	caps storage.Capabilities
}

func (f *fakeStore) Put(ctx context.Context, key storage.Key, r io.Reader, opts storage.PutOptions) (storage.Info, error) {
	*f.log = append(*f.log, "base.Put")
	return storage.Info{}, nil
}
func (f *fakeStore) Open(ctx context.Context, key storage.Key) (io.ReadCloser, storage.Info, error) {
	*f.log = append(*f.log, "base.Open")
	return io.NopCloser(strings.NewReader("")), storage.Info{}, nil
}
func (f *fakeStore) Head(ctx context.Context, key storage.Key) (storage.Info, error) {
	return storage.Info{}, nil
}
func (f *fakeStore) Delete(ctx context.Context, key storage.Key) error { return nil }
func (f *fakeStore) Stage(ctx context.Context, r io.Reader, opts storage.StageOptions) (storage.Staged, error) {
	return storage.Staged{}, nil
}
func (f *fakeStore) Promote(ctx context.Context, stageID storage.StageID, key storage.Key, opts storage.PromoteOptions) (storage.Info, error) {
	return storage.Info{}, nil
}
func (f *fakeStore) Abort(ctx context.Context, stageID storage.StageID) error { return nil }
func (f *fakeStore) CleanupExpired(ctx context.Context, opts storage.CleanupOptions) (storage.CleanupResult, error) {
	return storage.CleanupResult{}, nil
}
func (f *fakeStore) TemporaryURL(ctx context.Context, key storage.Key, opts storage.TemporaryURLOptions) (storage.Link, error) {
	return storage.Link{}, nil
}
func (f *fakeStore) Capabilities() storage.Capabilities {
	return f.caps
}

type wrappingStore struct {
	storage.Store
	name string
	log  *[]string
}

func (w *wrappingStore) Put(ctx context.Context, key storage.Key, r io.Reader, opts storage.PutOptions) (storage.Info, error) {
	*w.log = append(*w.log, w.name+".enter")
	info, err := w.Store.Put(ctx, key, r, opts)
	*w.log = append(*w.log, w.name+".exit")
	return info, err
}

func TestChain_ExecutionOrderAndCapabilities(t *testing.T) {
	var log []string
	expectedCaps := storage.Capabilities{
		TemporaryURL: true,
		Staging:      true,
	}
	base := &fakeStore{log: &log, caps: expectedCaps}

	mw1 := func(next storage.Store) storage.Store {
		return &wrappingStore{Store: next, name: "mw1", log: &log}
	}
	mw2 := func(next storage.Store) storage.Store {
		return &wrappingStore{Store: next, name: "mw2", log: &log}
	}

	chained := storage.Chain(base, mw1, nil, mw2)
	if chained.Capabilities() != expectedCaps {
		t.Fatalf("capabilities not preserved: got %+v, want %+v", chained.Capabilities(), expectedCaps)
	}

	k, err := storage.ParseKey("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = chained.Put(context.Background(), k, strings.NewReader("hi"), storage.PutOptions{})

	expected := []string{"mw1.enter", "mw2.enter", "base.Put", "mw2.exit", "mw1.exit"}
	if len(log) != len(expected) {
		t.Fatalf("unexpected call count: got %v, want %v", log, expected)
	}
	for i, v := range expected {
		if log[i] != v {
			t.Errorf("at step %d: got %s, want %s", i, log[i], v)
		}
	}
}

func TestChain_NilBase(t *testing.T) {
	if storage.Chain(nil) != nil {
		t.Fatal("expected nil for nil base")
	}
	var typedNil *fakeStore
	if storage.Chain(typedNil) != nil {
		t.Fatal("expected nil for typed-nil base")
	}
	var nilMiddleware storage.Middleware
	if storage.Chain(&fakeStore{}, nilMiddleware) == nil {
		t.Fatal("nil middleware function should be skipped")
	}
}

func TestChain_NilMiddlewareResultDoesNotFailOpen(t *testing.T) {
	base := &fakeStore{}
	nilResult := func(storage.Store) storage.Store { return nil }
	if chained := storage.Chain(base, nilResult); chained != nil {
		t.Fatal("expected nil when middleware returns nil")
	}

	var typedNil *wrappingStore
	typedNilResult := func(storage.Store) storage.Store { return typedNil }
	if chained := storage.Chain(base, typedNilResult); chained != nil {
		t.Fatal("expected nil when middleware returns typed nil")
	}
}

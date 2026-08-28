package storage_test

import (
	"context"
	"io"

	"github.com/frostgrove/vv/storage"
)

// fakeBackend records the adapter boundary. The core tests use it to prove
// validation and normalization happen before an implementation sees a call.
type fakeBackend struct {
	calls int

	put       func(context.Context, storage.Namespace, storage.Key, io.Reader, storage.PutOptions) (storage.Info, error)
	open      func(context.Context, storage.Namespace, storage.Key) (io.ReadCloser, storage.Info, error)
	head      func(context.Context, storage.Namespace, storage.Key) (storage.Info, error)
	delete    func(context.Context, storage.Namespace, storage.Key) error
	stage     func(context.Context, storage.Namespace, io.Reader, storage.StageOptions) (storage.Staged, error)
	promote   func(context.Context, storage.Namespace, storage.StageID, storage.Key, storage.PromoteOptions) (storage.Info, error)
	abort     func(context.Context, storage.Namespace, storage.StageID) error
	cleanup   func(context.Context, storage.Namespace, storage.CleanupOptions) (storage.CleanupResult, error)
	temporary func(context.Context, storage.Namespace, storage.Key, storage.TemporaryURLOptions) (storage.Link, error)
	caps      storage.Capabilities
}

func (b *fakeBackend) Put(ctx context.Context, namespace storage.Namespace, key storage.Key, source io.Reader, opts storage.PutOptions) (storage.Info, error) {
	b.calls++
	if b.put == nil {
		panic("unexpected Backend.Put")
	}
	return b.put(ctx, namespace, key, source, opts)
}

func (b *fakeBackend) Open(ctx context.Context, namespace storage.Namespace, key storage.Key) (io.ReadCloser, storage.Info, error) {
	b.calls++
	if b.open == nil {
		panic("unexpected Backend.Open")
	}
	return b.open(ctx, namespace, key)
}

func (b *fakeBackend) Head(ctx context.Context, namespace storage.Namespace, key storage.Key) (storage.Info, error) {
	b.calls++
	if b.head == nil {
		panic("unexpected Backend.Head")
	}
	return b.head(ctx, namespace, key)
}

func (b *fakeBackend) Delete(ctx context.Context, namespace storage.Namespace, key storage.Key) error {
	b.calls++
	if b.delete == nil {
		panic("unexpected Backend.Delete")
	}
	return b.delete(ctx, namespace, key)
}

func (b *fakeBackend) Stage(ctx context.Context, namespace storage.Namespace, source io.Reader, opts storage.StageOptions) (storage.Staged, error) {
	b.calls++
	if b.stage == nil {
		panic("unexpected Backend.Stage")
	}
	return b.stage(ctx, namespace, source, opts)
}

func (b *fakeBackend) Promote(ctx context.Context, namespace storage.Namespace, id storage.StageID, key storage.Key, opts storage.PromoteOptions) (storage.Info, error) {
	b.calls++
	if b.promote == nil {
		panic("unexpected Backend.Promote")
	}
	return b.promote(ctx, namespace, id, key, opts)
}

func (b *fakeBackend) Abort(ctx context.Context, namespace storage.Namespace, id storage.StageID) error {
	b.calls++
	if b.abort == nil {
		panic("unexpected Backend.Abort")
	}
	return b.abort(ctx, namespace, id)
}

func (b *fakeBackend) CleanupExpired(ctx context.Context, namespace storage.Namespace, opts storage.CleanupOptions) (storage.CleanupResult, error) {
	b.calls++
	if b.cleanup == nil {
		panic("unexpected Backend.CleanupExpired")
	}
	return b.cleanup(ctx, namespace, opts)
}

func (b *fakeBackend) TemporaryURL(ctx context.Context, namespace storage.Namespace, key storage.Key, opts storage.TemporaryURLOptions) (storage.Link, error) {
	b.calls++
	if b.temporary == nil {
		panic("unexpected Backend.TemporaryURL")
	}
	return b.temporary(ctx, namespace, key, opts)
}

func (b *fakeBackend) Capabilities() storage.Capabilities { return b.caps }

func newStore(backend storage.Backend) storage.Store {
	store, err := storage.New(storage.Config{Namespace: "documents", Backend: backend})
	if err != nil {
		panic(err)
	}
	return store
}

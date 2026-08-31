package storage_test

import (
	"context"
	"io"

	"github.com/frostgrove/vv/storage"
)

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

func (this *fakeBackend) Put(ctx context.Context, namespace storage.Namespace, key storage.Key, source io.Reader, options storage.PutOptions) (storage.Info, error) {
	this.calls++
	if this.put == nil {
		panic("unexpected Backend.Put")
	}
	return this.put(ctx, namespace, key, source, options)
}

func (this *fakeBackend) Open(ctx context.Context, namespace storage.Namespace, key storage.Key) (io.ReadCloser, storage.Info, error) {
	this.calls++
	if this.open == nil {
		panic("unexpected Backend.Open")
	}
	return this.open(ctx, namespace, key)
}

func (this *fakeBackend) Head(ctx context.Context, namespace storage.Namespace, key storage.Key) (storage.Info, error) {
	this.calls++
	if this.head == nil {
		panic("unexpected Backend.Head")
	}
	return this.head(ctx, namespace, key)
}

func (this *fakeBackend) Delete(ctx context.Context, namespace storage.Namespace, key storage.Key) error {
	this.calls++
	if this.delete == nil {
		panic("unexpected Backend.Delete")
	}
	return this.delete(ctx, namespace, key)
}

func (this *fakeBackend) Stage(ctx context.Context, namespace storage.Namespace, source io.Reader, options storage.StageOptions) (storage.Staged, error) {
	this.calls++
	if this.stage == nil {
		panic("unexpected Backend.Stage")
	}
	return this.stage(ctx, namespace, source, options)
}

func (this *fakeBackend) Promote(ctx context.Context, namespace storage.Namespace, id storage.StageID, key storage.Key, options storage.PromoteOptions) (storage.Info, error) {
	this.calls++
	if this.promote == nil {
		panic("unexpected Backend.Promote")
	}
	return this.promote(ctx, namespace, id, key, options)
}

func (this *fakeBackend) Abort(ctx context.Context, namespace storage.Namespace, id storage.StageID) error {
	this.calls++
	if this.abort == nil {
		panic("unexpected Backend.Abort")
	}
	return this.abort(ctx, namespace, id)
}

func (this *fakeBackend) CleanupExpired(ctx context.Context, namespace storage.Namespace, options storage.CleanupOptions) (storage.CleanupResult, error) {
	this.calls++
	if this.cleanup == nil {
		panic("unexpected Backend.CleanupExpired")
	}
	return this.cleanup(ctx, namespace, options)
}

func (this *fakeBackend) TemporaryURL(ctx context.Context, namespace storage.Namespace, key storage.Key, options storage.TemporaryURLOptions) (storage.Link, error) {
	this.calls++
	if this.temporary == nil {
		panic("unexpected Backend.TemporaryURL")
	}
	return this.temporary(ctx, namespace, key, options)
}

func (this *fakeBackend) Capabilities() storage.Capabilities { return this.caps }

func newStore(backend storage.Backend) storage.Store {
	store, err := storage.New(&storage.Config{Namespace: "documents", Backend: backend})
	if err != nil {
		panic(err)
	}
	return store
}

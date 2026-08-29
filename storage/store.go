package storage

import (
	"context"
	"fmt"
	"io"
	"reflect"
)

// Store is the application-facing contract shared by filesystem and MinIO
// backends. Callers own every source reader and every returned read body.
type Store interface {
	Put(context.Context, Key, io.Reader, PutOptions) (Info, error)
	Open(context.Context, Key) (io.ReadCloser, Info, error)
	Head(context.Context, Key) (Info, error)
	Delete(context.Context, Key) error

	Stage(context.Context, io.Reader, StageOptions) (Staged, error)
	Promote(context.Context, StageID, Key, PromoteOptions) (Info, error)
	Abort(context.Context, StageID) error
	CleanupExpired(context.Context, CleanupOptions) (CleanupResult, error)

	TemporaryURL(context.Context, Key, TemporaryURLOptions) (Link, error)
	Capabilities() Capabilities
}

// Backend is the adapter seam. Applications normally use it only as the value
// of Config.Backend and call the scoped Store returned by New.
type Backend interface {
	Put(context.Context, Namespace, Key, io.Reader, PutOptions) (Info, error)
	Open(context.Context, Namespace, Key) (io.ReadCloser, Info, error)
	Head(context.Context, Namespace, Key) (Info, error)
	Delete(context.Context, Namespace, Key) error

	Stage(context.Context, Namespace, io.Reader, StageOptions) (Staged, error)
	Promote(context.Context, Namespace, StageID, Key, PromoteOptions) (Info, error)
	Abort(context.Context, Namespace, StageID) error
	CleanupExpired(context.Context, Namespace, CleanupOptions) (CleanupResult, error)

	TemporaryURL(context.Context, Namespace, Key, TemporaryURLOptions) (Link, error)
	Capabilities() Capabilities
}

type Config struct {
	Namespace string
	Backend   Backend
}

type store struct {
	namespace Namespace
	backend   Backend
}

func New(config *Config) (Store, error) {
	if config == nil {
		return nil, NewError("construct", KindInvalid, fmt.Errorf("config is nil"))
	}
	namespace, err := ParseNamespace(config.Namespace)
	if err != nil {
		return nil, err
	}
	if nilInterface(config.Backend) {
		return nil, NewError("construct", KindInvalid, fmt.Errorf("backend is nil"))
	}
	return &store{namespace: namespace, backend: config.Backend}, nil
}

func (this *store) Put(ctx context.Context, key Key, source io.Reader, options PutOptions) (Info, error) {
	if err := validateCall(ctx, key, source); err != nil {
		return Info{}, NewError("put", KindInvalid, err)
	}
	normalized, err := normalizePutOptions(options)
	if err != nil {
		return Info{}, NewError("put", KindInvalid, err)
	}
	info, err := this.backend.Put(ctx, this.namespace, key, source, normalized)
	if err != nil {
		return Info{}, projectError("put", err)
	}
	return cloneInfo(info), nil
}

func (this *store) Open(ctx context.Context, key Key) (io.ReadCloser, Info, error) {
	if err := validateReadCall(ctx, key); err != nil {
		return nil, Info{}, NewError("open", KindInvalid, err)
	}
	body, info, err := this.backend.Open(ctx, this.namespace, key)
	if err != nil {
		if !nilInterface(body) {
			_ = body.Close()
		}
		return nil, Info{}, projectError("open", err)
	}
	if nilInterface(body) {
		return nil, Info{}, NewError("open", KindInternal, fmt.Errorf("backend returned a nil body"))
	}
	return body, cloneInfo(info), nil
}

func (this *store) Head(ctx context.Context, key Key) (Info, error) {
	if err := validateReadCall(ctx, key); err != nil {
		return Info{}, NewError("head", KindInvalid, err)
	}
	info, err := this.backend.Head(ctx, this.namespace, key)
	if err != nil {
		return Info{}, projectError("head", err)
	}
	return cloneInfo(info), nil
}

func (this *store) Delete(ctx context.Context, key Key) error {
	if err := validateReadCall(ctx, key); err != nil {
		return NewError("delete", KindInvalid, err)
	}
	return projectError("delete", this.backend.Delete(ctx, this.namespace, key))
}

func (this *store) Stage(ctx context.Context, source io.Reader, options StageOptions) (Staged, error) {
	if nilInterface(ctx) || nilInterface(source) {
		return Staged{}, NewError("stage", KindInvalid, fmt.Errorf("context or source is nil"))
	}
	normalized, err := normalizeStageOptions(options)
	if err != nil {
		return Staged{}, NewError("stage", KindInvalid, err)
	}
	staged, err := this.backend.Stage(ctx, this.namespace, source, normalized)
	if err != nil {
		return Staged{}, projectError("stage", err)
	}
	if !staged.ID.valid() || staged.ExpiresAt.IsZero() {
		return Staged{}, NewError("stage", KindInternal, fmt.Errorf("backend returned an invalid stage"))
	}
	staged.Info = cloneInfo(staged.Info)
	return staged, nil
}

func (this *store) Promote(ctx context.Context, id StageID, key Key, options PromoteOptions) (Info, error) {
	if nilInterface(ctx) || !id.valid() || !key.valid() {
		return Info{}, NewError("promote", KindInvalid, fmt.Errorf("context, stage id or key is invalid"))
	}
	mode, err := normalizeMode(options.Mode)
	if err != nil {
		return Info{}, NewError("promote", KindInvalid, err)
	}
	info, err := this.backend.Promote(ctx, this.namespace, id, key, PromoteOptions{Mode: mode})
	if err != nil {
		return Info{}, projectError("promote", err)
	}
	return cloneInfo(info), nil
}

func (this *store) Abort(ctx context.Context, id StageID) error {
	if nilInterface(ctx) || !id.valid() {
		return NewError("abort", KindInvalid, fmt.Errorf("context or stage id is invalid"))
	}
	return projectError("abort", this.backend.Abort(ctx, this.namespace, id))
}

func (this *store) CleanupExpired(ctx context.Context, options CleanupOptions) (CleanupResult, error) {
	if nilInterface(ctx) {
		return CleanupResult{}, NewError("cleanup", KindInvalid, fmt.Errorf("context is nil"))
	}
	normalized, err := normalizeCleanupOptions(options)
	if err != nil {
		return CleanupResult{}, NewError("cleanup", KindInvalid, err)
	}
	result, err := this.backend.CleanupExpired(ctx, this.namespace, normalized)
	if err != nil {
		return CleanupResult{}, projectError("cleanup", err)
	}
	return result, nil
}

func (this *store) TemporaryURL(ctx context.Context, key Key, options TemporaryURLOptions) (Link, error) {
	if err := validateReadCall(ctx, key); err != nil {
		return Link{}, NewError("temporary URL", KindInvalid, err)
	}
	normalized, err := normalizeTemporaryURLOptions(options)
	if err != nil {
		return Link{}, NewError("temporary URL", KindInvalid, err)
	}
	link, err := this.backend.TemporaryURL(ctx, this.namespace, key, normalized)
	if err != nil {
		return Link{}, projectError("temporary URL", err)
	}
	if link.rawURL == "" || link.expiresAt.IsZero() {
		return Link{}, NewError("temporary URL", KindInternal, fmt.Errorf("backend returned an invalid URL"))
	}
	return link, nil
}

func (this *store) Capabilities() Capabilities { return this.backend.Capabilities() }

func validateCall(ctx context.Context, key Key, source io.Reader) error {
	if nilInterface(source) {
		return fmt.Errorf("source is nil")
	}
	return validateReadCall(ctx, key)
}

func validateReadCall(ctx context.Context, key Key) error {
	if nilInterface(ctx) {
		return fmt.Errorf("context is nil")
	}
	if !key.valid() {
		return fmt.Errorf("key is invalid")
	}
	return nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

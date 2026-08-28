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

func (s *store) Put(ctx context.Context, key Key, source io.Reader, opts PutOptions) (Info, error) {
	if err := validateCall(ctx, key, source); err != nil {
		return Info{}, NewError("put", KindInvalid, err)
	}
	normalized, err := normalizePutOptions(opts)
	if err != nil {
		return Info{}, NewError("put", KindInvalid, err)
	}
	info, err := s.backend.Put(ctx, s.namespace, key, source, normalized)
	if err != nil {
		return Info{}, projectError("put", err)
	}
	return cloneInfo(info), nil
}

func (s *store) Open(ctx context.Context, key Key) (io.ReadCloser, Info, error) {
	if err := validateReadCall(ctx, key); err != nil {
		return nil, Info{}, NewError("open", KindInvalid, err)
	}
	body, info, err := s.backend.Open(ctx, s.namespace, key)
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

func (s *store) Head(ctx context.Context, key Key) (Info, error) {
	if err := validateReadCall(ctx, key); err != nil {
		return Info{}, NewError("head", KindInvalid, err)
	}
	info, err := s.backend.Head(ctx, s.namespace, key)
	if err != nil {
		return Info{}, projectError("head", err)
	}
	return cloneInfo(info), nil
}

func (s *store) Delete(ctx context.Context, key Key) error {
	if err := validateReadCall(ctx, key); err != nil {
		return NewError("delete", KindInvalid, err)
	}
	return projectError("delete", s.backend.Delete(ctx, s.namespace, key))
}

func (s *store) Stage(ctx context.Context, source io.Reader, opts StageOptions) (Staged, error) {
	if nilInterface(ctx) || nilInterface(source) {
		return Staged{}, NewError("stage", KindInvalid, fmt.Errorf("context or source is nil"))
	}
	normalized, err := normalizeStageOptions(opts)
	if err != nil {
		return Staged{}, NewError("stage", KindInvalid, err)
	}
	staged, err := s.backend.Stage(ctx, s.namespace, source, normalized)
	if err != nil {
		return Staged{}, projectError("stage", err)
	}
	if !staged.ID.valid() || staged.ExpiresAt.IsZero() {
		return Staged{}, NewError("stage", KindInternal, fmt.Errorf("backend returned an invalid stage"))
	}
	staged.Info = cloneInfo(staged.Info)
	return staged, nil
}

func (s *store) Promote(ctx context.Context, id StageID, key Key, opts PromoteOptions) (Info, error) {
	if nilInterface(ctx) || !id.valid() || !key.valid() {
		return Info{}, NewError("promote", KindInvalid, fmt.Errorf("context, stage id or key is invalid"))
	}
	mode, err := normalizeMode(opts.Mode)
	if err != nil {
		return Info{}, NewError("promote", KindInvalid, err)
	}
	info, err := s.backend.Promote(ctx, s.namespace, id, key, PromoteOptions{Mode: mode})
	if err != nil {
		return Info{}, projectError("promote", err)
	}
	return cloneInfo(info), nil
}

func (s *store) Abort(ctx context.Context, id StageID) error {
	if nilInterface(ctx) || !id.valid() {
		return NewError("abort", KindInvalid, fmt.Errorf("context or stage id is invalid"))
	}
	return projectError("abort", s.backend.Abort(ctx, s.namespace, id))
}

func (s *store) CleanupExpired(ctx context.Context, opts CleanupOptions) (CleanupResult, error) {
	if nilInterface(ctx) {
		return CleanupResult{}, NewError("cleanup", KindInvalid, fmt.Errorf("context is nil"))
	}
	normalized, err := normalizeCleanupOptions(opts)
	if err != nil {
		return CleanupResult{}, NewError("cleanup", KindInvalid, err)
	}
	result, err := s.backend.CleanupExpired(ctx, s.namespace, normalized)
	if err != nil {
		return CleanupResult{}, projectError("cleanup", err)
	}
	return result, nil
}

func (s *store) TemporaryURL(ctx context.Context, key Key, opts TemporaryURLOptions) (Link, error) {
	if err := validateReadCall(ctx, key); err != nil {
		return Link{}, NewError("temporary URL", KindInvalid, err)
	}
	normalized, err := normalizeTemporaryURLOptions(opts)
	if err != nil {
		return Link{}, NewError("temporary URL", KindInvalid, err)
	}
	link, err := s.backend.TemporaryURL(ctx, s.namespace, key, normalized)
	if err != nil {
		return Link{}, projectError("temporary URL", err)
	}
	if link.rawURL == "" || link.expiresAt.IsZero() {
		return Link{}, NewError("temporary URL", KindInternal, fmt.Errorf("backend returned an invalid URL"))
	}
	return link, nil
}

func (s *store) Capabilities() Capabilities { return s.backend.Capabilities() }

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

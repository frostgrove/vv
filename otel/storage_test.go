package vvotel_test

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/storage"
	"go.opentelemetry.io/otel/codes"
)

type fakeStorageStore struct {
	lastCtx  context.Context
	panic    bool
	goexit   bool
	panicNil bool
	err      error
}

func (f *fakeStorageStore) Put(ctx context.Context, key storage.Key, source io.Reader, options storage.PutOptions) (storage.Info, error) {
	f.lastCtx = ctx
	if f.panic {
		panic("disk unmounted")
	}
	if f.err != nil {
		return storage.Info{}, f.err
	}
	return storage.Info{Size: 10}, nil
}
func (f *fakeStorageStore) Open(ctx context.Context, key storage.Key) (io.ReadCloser, storage.Info, error) {
	f.lastCtx = ctx
	if f.panic {
		panic("disk unmounted")
	}
	if f.err != nil {
		return nil, storage.Info{}, f.err
	}
	return io.NopCloser(strings.NewReader("hello")), storage.Info{Size: 5}, nil
}
func (f *fakeStorageStore) Head(ctx context.Context, key storage.Key) (storage.Info, error) {
	f.lastCtx = ctx
	if f.panic {
		panic("disk unmounted")
	}
	if f.err != nil {
		return storage.Info{}, f.err
	}
	return storage.Info{Size: 10}, nil
}
func (f *fakeStorageStore) Delete(ctx context.Context, key storage.Key) error {
	f.lastCtx = ctx
	if f.panic {
		panic("disk unmounted")
	}
	if f.goexit {
		runtime.Goexit()
	}
	if f.panicNil {
		panic(nil)
	}
	return f.err
}
func (f *fakeStorageStore) Stage(ctx context.Context, source io.Reader, options storage.StageOptions) (storage.Staged, error) {
	f.lastCtx = ctx
	if f.panic {
		panic("disk unmounted")
	}
	if f.err != nil {
		return storage.Staged{}, f.err
	}
	return storage.Staged{Info: storage.Info{Size: 10}}, nil
}
func (f *fakeStorageStore) Promote(ctx context.Context, stageID storage.StageID, key storage.Key, options storage.PromoteOptions) (storage.Info, error) {
	f.lastCtx = ctx
	if f.panic {
		panic("disk unmounted")
	}
	if f.err != nil {
		return storage.Info{}, f.err
	}
	return storage.Info{Size: 10}, nil
}
func (f *fakeStorageStore) Abort(ctx context.Context, stageID storage.StageID) error {
	f.lastCtx = ctx
	if f.panic {
		panic("disk unmounted")
	}
	return f.err
}
func (f *fakeStorageStore) CleanupExpired(ctx context.Context, options storage.CleanupOptions) (storage.CleanupResult, error) {
	f.lastCtx = ctx
	if f.panic {
		panic("disk unmounted")
	}
	if f.err != nil {
		return storage.CleanupResult{}, f.err
	}
	return storage.CleanupResult{Removed: 1}, nil
}
func (f *fakeStorageStore) TemporaryURL(ctx context.Context, key storage.Key, options storage.TemporaryURLOptions) (storage.Link, error) {
	f.lastCtx = ctx
	if f.panic {
		panic("disk unmounted")
	}
	if f.err != nil {
		return storage.Link{}, f.err
	}
	link, _ := storage.NewLink("https://example.com/file", time.Now().Add(time.Hour))
	return link, nil
}
func (f *fakeStorageStore) Capabilities() storage.Capabilities {
	return storage.Capabilities{Staging: true}
}

func TestStorage_AllNineOperationsTotality(t *testing.T) {
	key, _ := storage.ParseKey("images/pic.png")
	stageID, _ := storage.ParseStageID("11111111-1111-4111-8111-111111111111")

	ops := []struct {
		name     string
		execute  func(ctx context.Context, s storage.Store) error
		wantSpan string
	}{
		{
			name: "Put",
			execute: func(ctx context.Context, s storage.Store) error {
				_, err := s.Put(ctx, key, strings.NewReader("data"), storage.PutOptions{})
				return err
			},
			wantSpan: "vv.storage put",
		},
		{
			name: "Open",
			execute: func(ctx context.Context, s storage.Store) error {
				rc, _, err := s.Open(ctx, key)
				if err == nil {
					_ = rc.Close()
				}
				return err
			},
			wantSpan: "vv.storage open",
		},
		{
			name: "Head",
			execute: func(ctx context.Context, s storage.Store) error {
				_, err := s.Head(ctx, key)
				return err
			},
			wantSpan: "vv.storage head",
		},
		{
			name: "Delete",
			execute: func(ctx context.Context, s storage.Store) error {
				return s.Delete(ctx, key)
			},
			wantSpan: "vv.storage delete",
		},
		{
			name: "Stage",
			execute: func(ctx context.Context, s storage.Store) error {
				_, err := s.Stage(ctx, strings.NewReader("staged"), storage.StageOptions{})
				return err
			},
			wantSpan: "vv.storage stage",
		},
		{
			name: "Promote",
			execute: func(ctx context.Context, s storage.Store) error {
				_, err := s.Promote(ctx, stageID, key, storage.PromoteOptions{})
				return err
			},
			wantSpan: "vv.storage promote",
		},
		{
			name: "Abort",
			execute: func(ctx context.Context, s storage.Store) error {
				return s.Abort(ctx, stageID)
			},
			wantSpan: "vv.storage abort",
		},
		{
			name: "CleanupExpired",
			execute: func(ctx context.Context, s storage.Store) error {
				_, err := s.CleanupExpired(ctx, storage.CleanupOptions{})
				return err
			},
			wantSpan: "vv.storage cleanup_expired",
		},
		{
			name: "TemporaryURL",
			execute: func(ctx context.Context, s storage.Store) error {
				_, err := s.TemporaryURL(ctx, key, storage.TemporaryURLOptions{})
				return err
			},
			wantSpan: "vv.storage temporary_url",
		},
	}

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			tel, err := vvotel.New(vvotel.Config{
				TracerProvider: tp,
				ResourceName:   "assets",
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			raw := &fakeStorageStore{}
			s := vvotel.Store(tel)(raw)

			if err := op.execute(context.Background(), s); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if raw.lastCtx.Value(spanKey{}) == nil {
				t.Fatal("derived context not passed down")
			}

			if len(tp.spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(tp.spans))
			}
			span := tp.spans[0]
			if span.name != op.wantSpan {
				t.Errorf("got span %q, want %q", span.name, op.wantSpan)
			}
			if !span.ended {
				t.Error("span must be ended")
			}
			if span.status != codes.Unset {
				t.Errorf("expected status Unset, got %v", span.status)
			}
			if outcome := span.attributes[vvotel.AttrOperationOutcome].AsString(); outcome != vvotel.OutcomeOk {
				t.Errorf("got outcome %q, want %q", outcome, vvotel.OutcomeOk)
			}
			if res := span.attributes[vvotel.AttrResourceName].AsString(); res != "assets" {
				t.Errorf("got resource %q, want assets", res)
			}
		})
	}
}

func TestStorage_CapabilitiesDoNotEmitSpan(t *testing.T) {
	tp := newTestTracerProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp})

	raw := &fakeStorageStore{}
	s := vvotel.Store(tel)(raw)

	caps := s.Capabilities()
	if !caps.Staging {
		t.Fatal("expected Capabilities.Staging to be true")
	}
	if len(tp.spans) != 0 {
		t.Fatalf("Capabilities must not emit spans, got %d", len(tp.spans))
	}
}

func TestStorage_WithStorageResourceOption(t *testing.T) {
	tp := newTestTracerProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp})

	raw := &fakeStorageStore{}
	mw := vvotel.Store(tel, vvotel.WithStorageResource("custom_bucket"))
	s := mw(raw)

	k, _ := storage.ParseKey("test.txt")
	_, _ = s.Head(context.Background(), k)

	if len(tp.spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tp.spans))
	}
	if res := tp.spans[0].attributes[vvotel.AttrResourceName].AsString(); res != "custom_bucket" {
		t.Errorf("got resource %q, want custom_bucket", res)
	}
}

func TestStorage_NilAndDisabledPreservesBehavior(t *testing.T) {
	raw := &fakeStorageStore{}
	mw := vvotel.Store(nil)
	if mw(nil) != nil {
		t.Fatal("expected nil when inner is nil")
	}

	sNilTel := mw(raw)
	k, _ := storage.ParseKey("test.txt")
	_, err := sNilTel.Head(context.Background(), k)
	if err != nil {
		t.Fatalf("unexpected error with nil telemetry: %v", err)
	}

	tp := newTestTracerProvider()
	telDisabled, _ := vvotel.New(vvotel.Config{
		TracerProvider:        tp,
		StorageTracesDisabled: true,
	})
	sDisabled := vvotel.Store(telDisabled)(raw)
	_, err = sDisabled.Head(context.Background(), k)
	if err != nil {
		t.Fatalf("unexpected error with disabled storage traces: %v", err)
	}
	if len(tp.spans) != 0 {
		t.Fatal("no spans should be recorded when storage traces disabled")
	}
}

func TestStorage_PanicsEndedSafelyAndRepanicked(t *testing.T) {
	tp := newTestTracerProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp})

	raw := &fakeStorageStore{panic: true}
	s := vvotel.Store(tel)(raw)

	defer func() {
		r := recover()
		if r == nil || r != "disk unmounted" {
			t.Fatalf("expected panic 'disk unmounted', got %v", r)
		}

		if len(tp.spans) != 1 {
			t.Fatalf("expected 1 span, got %d", len(tp.spans))
		}
		span := tp.spans[0]
		if !span.ended {
			t.Error("span must be ended on panic")
		}
		if span.status != codes.Error {
			t.Errorf("got status %v, want Error", span.status)
		}
		if errType := span.attributes[vvotel.AttrErrorType].AsString(); errType != vvotel.ErrorTypePanic {
			t.Errorf("got error.type %q, want panic", errType)
		}
	}()

	k, _ := storage.ParseKey("test.txt")
	_ = s.Delete(context.Background(), k)
}

func TestStorage_ErrorClassifications(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantOutcome string
		wantType    string
	}{
		{
			name:        "canceled",
			err:         context.Canceled,
			wantOutcome: vvotel.OutcomeCanceled,
			wantType:    vvotel.ErrorTypeCanceled,
		},
		{
			name:        "timeout",
			err:         context.DeadlineExceeded,
			wantOutcome: vvotel.OutcomeTimeout,
			wantType:    vvotel.ErrorTypeTimeout,
		},
		{
			name:        "not_found",
			err:         storage.ErrNotFound,
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeNotFound,
		},
		{
			name:        "forbidden",
			err:         storage.ErrForbidden,
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeForbidden,
		},
		{
			name:        "conflict",
			err:         storage.ErrConflict,
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeConflict,
		},
		{
			name:        "already_exists",
			err:         storage.ErrAlreadyExists,
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeConflict,
		},
		{
			name:        "invalid",
			err:         storage.ErrInvalid,
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeInvalid,
		},
		{
			name:        "precondition_failed",
			err:         storage.ErrPreconditionFailed,
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeInvalid,
		},
		{
			name:        "unsupported",
			err:         storage.ErrUnsupported,
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeInvalid,
		},
		{
			name:        "cancelled_kind",
			err:         storage.ErrCancelled,
			wantOutcome: vvotel.OutcomeCanceled,
			wantType:    vvotel.ErrorTypeCanceled,
		},
		{
			name:        "expired",
			err:         storage.ErrExpired,
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeStaleVersion,
		},
		{
			name:        "internal",
			err:         errors.New("raw disk I/O error"),
			wantOutcome: vvotel.OutcomeError,
			wantType:    vvotel.ErrorTypeInternal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp})

			raw := &fakeStorageStore{err: tc.err}
			s := vvotel.Store(tel)(raw)

			k, _ := storage.ParseKey("test.txt")
			_, err := s.Head(context.Background(), k)
			if !errors.Is(err, tc.err) {
				t.Fatalf("expected error identity preserved, got %v", err)
			}

			if len(tp.spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(tp.spans))
			}
			span := tp.spans[0]
			if outcome := span.attributes[vvotel.AttrOperationOutcome].AsString(); outcome != tc.wantOutcome {
				t.Errorf("got outcome %q, want %q", outcome, tc.wantOutcome)
			}
			if errType := span.attributes[vvotel.AttrErrorType].AsString(); errType != tc.wantType {
				t.Errorf("got error.type %q, want %q", errType, tc.wantType)
			}
		})
	}
}

func TestStorage_OpenErrorAndPanic(t *testing.T) {
	tp := newTestTracerProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp})

	rawErr := &fakeStorageStore{err: storage.ErrNotFound}
	sErr := vvotel.Store(tel)(rawErr)

	k, _ := storage.ParseKey("test.txt")
	_, _, err := sErr.Open(context.Background(), k)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if len(tp.spans) != 1 || tp.spans[0].name != "vv.storage open" {
		t.Fatalf("expected 1 span for Open error")
	}
	if tp.spans[0].status != codes.Error {
		t.Errorf("expected Error status, got %v", tp.spans[0].status)
	}

	tp2 := newTestTracerProvider()
	tel2, _ := vvotel.New(vvotel.Config{TracerProvider: tp2})
	rawPanic := &fakeStorageStore{panic: true}
	sPanic := vvotel.Store(tel2)(rawPanic)

	defer func() {
		r := recover()
		if r == nil || r != "disk unmounted" {
			t.Fatalf("expected panic 'disk unmounted', got %v", r)
		}
		if len(tp2.spans) != 1 {
			t.Fatalf("expected 1 span for Open panic")
		}
		if tp2.spans[0].status != codes.Error {
			t.Errorf("expected Error status, got %v", tp2.spans[0].status)
		}
	}()
	_, _, _ = sPanic.Open(context.Background(), k)
}

func TestStorage_GoexitPreservesGoroutineTermination(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	s := vvotel.Store(tel)(&fakeStorageStore{goexit: true})
	k, _ := storage.ParseKey("test.txt")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Delete(context.Background(), k)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime.Goexit did not terminate the goroutine")
	}
}

func TestStorage_PanicNilIsNotSuppressed(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	s := vvotel.Store(tel)(&fakeStorageStore{panicNil: true})
	k, _ := storage.ParseKey("test.txt")
	defer func() {
		if recover() == nil {
			t.Fatal("panic(nil) was suppressed")
		}
	}()
	_ = s.Delete(context.Background(), k)
}

func TestStorage_OpenDisabled(t *testing.T) {
	tp := newTestTracerProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp, StorageTracesDisabled: true})

	raw := &fakeStorageStore{}
	s := vvotel.Store(tel)(raw)

	k, _ := storage.ParseKey("test.txt")
	rc, _, err := s.Open(context.Background(), k)
	if err != nil {
		t.Fatalf("unexpected Open error: %v", err)
	}
	_ = rc.Close()
	if len(tp.spans) != 0 {
		t.Fatal("Open must not emit span when storage traces disabled")
	}
}

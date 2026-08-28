package storage_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/storage"
)

func TestEveryPortableErrorKindIsReachableWithErrorsIs(t *testing.T) {
	cases := []struct {
		kind     storage.Kind
		sentinel error
	}{
		{storage.KindInvalid, storage.ErrInvalid},
		{storage.KindNotFound, storage.ErrNotFound},
		{storage.KindAlreadyExists, storage.ErrAlreadyExists},
		{storage.KindPreconditionFailed, storage.ErrPreconditionFailed},
		{storage.KindExpired, storage.ErrExpired},
		{storage.KindUnsupported, storage.ErrUnsupported},
		{storage.KindForbidden, storage.ErrForbidden},
		{storage.KindConflict, storage.ErrConflict},
		{storage.KindCancelled, storage.ErrCancelled},
		{storage.KindSource, storage.ErrSource},
		{storage.KindTemporary, storage.ErrTemporary},
		{storage.KindUnavailable, storage.ErrUnavailable},
		{storage.KindInternal, storage.ErrInternal},
	}

	for _, tc := range cases {
		cause := errors.New("provider detail")
		err := storage.NewError("put", tc.kind, cause)
		if !errors.Is(err, tc.sentinel) {
			t.Errorf("NewError(%q) is not its sentinel", tc.kind)
		}
		if !errors.Is(err, cause) {
			t.Errorf("NewError(%q) lost its cause", tc.kind)
		}
		if got := storage.KindOf(fmt.Errorf("outer: %w", err)); got != tc.kind {
			t.Errorf("KindOf(wrapped %q) = %q", tc.kind, got)
		}
	}

	if got := storage.KindOf(errors.New("ordinary")); got != "" {
		t.Fatalf("KindOf(ordinary error) = %q, want empty", got)
	}
	if got := storage.KindOf(storage.ErrNotFound); got != storage.KindNotFound {
		t.Fatalf("KindOf(ErrNotFound) = %q", got)
	}
	if err := storage.NewError("put", storage.Kind("private-key"), nil); !errors.Is(err, storage.ErrInternal) || strings.Contains(err.Error(), "private-key") {
		t.Fatalf("unknown Kind was not bounded: %v", err)
	}
}

func TestAStorageErrorRetainsDiagnosticsWithoutFormattingThem(t *testing.T) {
	secret := "https://access:secret@minio.internal/private/key?signature=sentinel"
	cause := errors.New(secret)
	err := storage.NewError("open", storage.KindUnavailable, cause)

	if strings.Contains(fmt.Sprint(err), secret) || strings.Contains(fmt.Sprint(err), "signature=sentinel") {
		t.Fatalf("public error text disclosed its cause: %q", err)
	}
	assertFormattingRedacted(t, err, secret, "signature=sentinel")
	if got := fmt.Sprint(err); got != "storage open: unavailable" {
		t.Fatalf("Error() = %q", got)
	}
	if !errors.Is(err, cause) {
		t.Fatal("controlled errors.Is traversal cannot reach the retained cause")
	}
	var storageError *storage.Error
	if !errors.As(err, &storageError) {
		t.Fatal("errors.As could not recover *storage.Error")
	}
	if storageError.Operation != "open" || storageError.Kind != storage.KindUnavailable {
		t.Fatalf("storage error = %#v", storageError)
	}
}

func TestTheCoreProjectsOnlyBoundedBackendFailures(t *testing.T) {
	key, err := storage.ParseKey("documents/report.pdf")
	if err != nil {
		t.Fatal(err)
	}
	secret := "filesystem /srv/private/documents/report.pdf failed"

	t.Run("unknown backend error", func(t *testing.T) {
		backend := &fakeBackend{head: func(context.Context, storage.Namespace, storage.Key) (storage.Info, error) {
			return storage.Info{Size: 99}, errors.New(secret)
		}}
		info, err := newStore(backend).Head(context.Background(), key)
		if !errors.Is(err, storage.ErrInternal) || storage.KindOf(err) != storage.KindInternal {
			t.Fatalf("Head error = %v, kind %q", err, storage.KindOf(err))
		}
		if strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("projected error disclosed backend text: %q", err)
		}
		if !reflect.DeepEqual(info, storage.Info{}) {
			t.Fatalf("Head returned Info with an error: %#v", info)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		backend := &fakeBackend{delete: func(context.Context, storage.Namespace, storage.Key) error {
			return context.Canceled
		}}
		err := newStore(backend).Delete(context.Background(), key)
		if !errors.Is(err, storage.ErrCancelled) || !errors.Is(err, context.Canceled) {
			t.Fatalf("Delete cancellation = %v", err)
		}
	})

	t.Run("classified backend error", func(t *testing.T) {
		backendOperation := "minio get " + secret
		backend := &fakeBackend{open: func(context.Context, storage.Namespace, storage.Key) (io.ReadCloser, storage.Info, error) {
			return nil, storage.Info{}, storage.NewError(backendOperation, storage.KindNotFound, errors.New(secret))
		}}
		_, _, err := newStore(backend).Open(context.Background(), key)
		if !errors.Is(err, storage.ErrNotFound) || storage.KindOf(err) != storage.KindNotFound {
			t.Fatalf("Open classified error = %v", err)
		}
		if strings.Contains(fmt.Sprint(err), secret) {
			t.Fatalf("classified error disclosed backend text: %q", err)
		}
		var projected *storage.Error
		if !errors.As(err, &projected) || projected.Operation != "open" {
			t.Fatalf("classified error operation = %#v, want bounded core operation", projected)
		}
	})
}

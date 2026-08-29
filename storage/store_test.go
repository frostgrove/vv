package storage_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/storage"
)

func TestNewRejectsAnInvalidNamespaceAndEveryNilBackendShape(t *testing.T) {
	backend := &fakeBackend{}
	if _, err := storage.New(&storage.Config{Namespace: "Documents", Backend: backend}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("New with invalid namespace error = %v, want ErrInvalid", err)
	}
	if backend.calls != 0 {
		t.Fatalf("invalid construction made %d backend calls", backend.calls)
	}

	if _, err := storage.New(&storage.Config{Namespace: "documents"}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("New with nil backend error = %v, want ErrInvalid", err)
	}
	var typedNil *fakeBackend
	if _, err := storage.New(&storage.Config{Namespace: "documents", Backend: typedNil}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("New with typed nil backend error = %v, want ErrInvalid", err)
	}
}

func TestPutNormalizesAndCopiesEverythingBeforeTheBackend(t *testing.T) {
	key := mustKey(t, "reports/annual.pdf")
	source := &trackedSource{Reader: bytes.NewReader([]byte("report bytes"))}
	size := int64(len("report bytes"))
	metadata := storage.Metadata{"classification": "public"}
	backendInfoMetadata := storage.Metadata{"answer": "from backend"}

	var gotNamespace storage.Namespace
	var gotKey storage.Key
	var gotSource io.Reader
	var gotOptions storage.PutOptions
	backend := &fakeBackend{put: func(_ context.Context, namespace storage.Namespace, key storage.Key, source io.Reader, options storage.PutOptions) (storage.Info, error) {
		gotNamespace, gotKey, gotSource, gotOptions = namespace, key, source, options
		body, err := io.ReadAll(source)
		if err != nil {
			return storage.Info{}, err
		}
		if string(body) != "report bytes" {
			t.Fatalf("backend read %q", body)
		}
		return storage.Info{Size: int64(len(body)), ContentType: options.ContentType, Metadata: backendInfoMetadata}, nil
	}}

	info, err := newStore(backend).Put(context.Background(), key, source, storage.PutOptions{
		Size:     &size,
		Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if gotNamespace.Value() != "documents" || gotKey != key || gotSource != source {
		t.Fatalf("backend boundary got namespace=%q key=%q source identity=%t", gotNamespace.Value(), gotKey.Value(), gotSource == source)
	}
	if gotOptions.Mode != storage.CreateOnly {
		t.Fatalf("zero write mode normalized to %v, want CreateOnly", gotOptions.Mode)
	}
	if gotOptions.ContentType != "application/octet-stream" {
		t.Fatalf("empty content type normalized to %q", gotOptions.ContentType)
	}
	if gotOptions.Size == nil || *gotOptions.Size != int64(len("report bytes")) || gotOptions.Size == &size {
		t.Fatal("known size was not copied before reaching the backend")
	}
	if gotOptions.Metadata["classification"] != "public" {
		t.Fatalf("metadata at backend = %#v", gotOptions.Metadata)
	}
	if source.closes != 0 {
		t.Fatalf("Put closed its caller-owned source %d times", source.closes)
	}

	// Mutating either caller-owned input or the returned Info must not mutate
	// what lives on the other side of the storage boundary.
	size = 1
	metadata["classification"] = "changed"
	info.Metadata["answer"] = "changed"
	if *gotOptions.Size != int64(len("report bytes")) || gotOptions.Metadata["classification"] != "public" {
		t.Fatalf("caller mutation reached normalized backend options: %#v", gotOptions)
	}
	if backendInfoMetadata["answer"] != "from backend" {
		t.Fatalf("returned Info metadata aliases backend state: %#v", backendInfoMetadata)
	}
}

func TestInvalidPutInputIsRejectedBeforeTheSourceOrBackend(t *testing.T) {
	tooManyMetadata := make(storage.Metadata, storage.MaxMetadataEntries+1)
	for i := range storage.MaxMetadataEntries + 1 {
		tooManyMetadata[fmt.Sprintf("key%d", i)] = "value"
	}
	overBudgetMetadata := make(storage.Metadata, 9)
	for i := range 9 {
		overBudgetMetadata[fmt.Sprintf("key%d", i)] = strings.Repeat("x", 500)
	}
	invalidUTF8 := string([]byte{0xff})
	negative := int64(-1)

	cases := []struct {
		name    string
		options storage.PutOptions
	}{
		{"unknown mode", storage.PutOptions{Mode: storage.WriteMode(99)}},
		{"negative size", storage.PutOptions{Size: &negative}},
		{"malformed content type", storage.PutOptions{ContentType: "text/plain; charset"}},
		{"too many metadata entries", storage.PutOptions{Metadata: tooManyMetadata}},
		{"metadata total budget", storage.PutOptions{Metadata: overBudgetMetadata}},
		{"empty metadata key", storage.PutOptions{Metadata: storage.Metadata{"": "value"}}},
		{"upper case metadata key", storage.PutOptions{Metadata: storage.Metadata{"Secret": "value"}}},
		{"reserved metadata prefix", storage.PutOptions{Metadata: storage.Metadata{"x-amz-acl": "public"}}},
		{"oversized metadata value", storage.PutOptions{Metadata: storage.Metadata{"note": strings.Repeat("x", storage.MaxMetadataValueBytes+1)}}},
		{"control in metadata value", storage.PutOptions{Metadata: storage.Metadata{"note": "line\nbreak"}}},
		{"invalid utf8 metadata value", storage.PutOptions{Metadata: storage.Metadata{"note": invalidUTF8}}},
	}

	for _, tc := range cases {
		backend := &fakeBackend{}
		source := &trackedSource{Reader: bytes.NewReader([]byte("must stay unread"))}
		_, err := newStore(backend).Put(context.Background(), mustKey(t, "valid/key"), source, tc.options)
		if !errors.Is(err, storage.ErrInvalid) {
			t.Errorf("%s: Put error = %v, want ErrInvalid", tc.name, err)
		}
		if backend.calls != 0 || source.reads != 0 || source.closes != 0 {
			t.Errorf("%s: backend calls=%d source reads=%d closes=%d", tc.name, backend.calls, source.reads, source.closes)
		}
	}

	backend := &fakeBackend{}
	source := &trackedSource{Reader: bytes.NewReader([]byte("must stay unread"))}
	if _, err := newStore(backend).Put(context.Background(), storage.Key{}, source, storage.PutOptions{}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("Put with zero Key error = %v, want ErrInvalid", err)
	}
	if backend.calls != 0 || source.reads != 0 {
		t.Fatal("a zero Key reached the source or backend")
	}

	var typedNilSource *trackedSource
	if _, err := newStore(backend).Put(context.Background(), mustKey(t, "valid/key"), typedNilSource, storage.PutOptions{}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("Put with typed nil source error = %v, want ErrInvalid", err)
	}
	if backend.calls != 0 {
		t.Fatalf("a typed nil source made %d backend calls", backend.calls)
	}
}

func TestPutReturnsNoInfoWhenTheBackendFails(t *testing.T) {
	source := &trackedSource{Reader: bytes.NewReader([]byte("body"))}
	backend := &fakeBackend{put: func(context.Context, storage.Namespace, storage.Key, io.Reader, storage.PutOptions) (storage.Info, error) {
		return storage.Info{Size: 42, Metadata: storage.Metadata{"should": "disappear"}}, storage.NewError("fs put", storage.KindUnavailable, errors.New("disk full"))
	}}
	info, err := newStore(backend).Put(context.Background(), mustKey(t, "valid/key"), source, storage.PutOptions{})
	if !errors.Is(err, storage.ErrUnavailable) {
		t.Fatalf("Put error = %v, want ErrUnavailable", err)
	}
	if !reflect.DeepEqual(info, storage.Info{}) {
		t.Fatalf("Put returned Info alongside error: %#v", info)
	}
	if source.closes != 0 {
		t.Fatalf("failed Put closed its caller-owned source %d times", source.closes)
	}
}

func TestExactSizeReturnsAnIndependentOptionalValue(t *testing.T) {
	first, second := storage.ExactSize(12), storage.ExactSize(12)
	if first == nil || second == nil || *first != 12 || *second != 12 {
		t.Fatalf("ExactSize values = %v and %v", first, second)
	}
	if first == second {
		t.Fatal("two ExactSize calls returned the same pointer")
	}
	*first = 1
	if *second != 12 {
		t.Fatal("mutating one ExactSize result changed another")
	}
}

func TestPutCanonicalizesEveryEmptyMetadataMapToNil(t *testing.T) {
	backend := &fakeBackend{put: func(_ context.Context, _ storage.Namespace, _ storage.Key, _ io.Reader, options storage.PutOptions) (storage.Info, error) {
		if options.Metadata != nil {
			t.Fatalf("backend metadata = %#v, want canonical nil", options.Metadata)
		}
		return storage.Info{}, nil
	}}
	info, err := newStore(backend).Put(context.Background(), mustKey(t, "metadata/empty"), strings.NewReader("body"), storage.PutOptions{Metadata: storage.Metadata{}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Metadata != nil {
		t.Fatalf("returned metadata = %#v, want nil", info.Metadata)
	}
}

func TestOpenLeavesASuccessfulBodyForTheCallerAndClosesAnErroredOne(t *testing.T) {
	key := mustKey(t, "valid/key")

	t.Run("success", func(t *testing.T) {
		body := &trackedBody{Reader: strings.NewReader("contents")}
		backendMetadata := storage.Metadata{"classification": "public"}
		backend := &fakeBackend{open: func(context.Context, storage.Namespace, storage.Key) (io.ReadCloser, storage.Info, error) {
			return body, storage.Info{Size: 8, Metadata: backendMetadata}, nil
		}}
		opened, info, err := newStore(backend).Open(context.Background(), key)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if opened != body || body.closes != 0 {
			t.Fatalf("Open body identity=%t closes=%d", opened == body, body.closes)
		}
		prefix := make([]byte, 3)
		if _, err := io.ReadFull(opened, prefix); err != nil || string(prefix) != "con" {
			t.Fatalf("read prefix = %q, %v", prefix, err)
		}
		if err := opened.Close(); err != nil || body.closes != 1 {
			t.Fatalf("caller Close error=%v count=%d", err, body.closes)
		}
		info.Metadata["classification"] = "changed"
		if backendMetadata["classification"] != "public" {
			t.Fatal("Open Info metadata aliases backend state")
		}
	})

	t.Run("backend error", func(t *testing.T) {
		body := &trackedBody{Reader: strings.NewReader("partial")}
		backend := &fakeBackend{open: func(context.Context, storage.Namespace, storage.Key) (io.ReadCloser, storage.Info, error) {
			return body, storage.Info{Size: 7}, storage.NewError("minio open", storage.KindTemporary, errors.New("lost response"))
		}}
		opened, info, err := newStore(backend).Open(context.Background(), key)
		if !errors.Is(err, storage.ErrTemporary) {
			t.Fatalf("Open error = %v", err)
		}
		if opened != nil || !reflect.DeepEqual(info, storage.Info{}) || body.closes != 1 {
			t.Fatalf("errored Open returned body=%v info=%#v and closed %d times", opened, info, body.closes)
		}
	})

	t.Run("backend error with typed nil body", func(t *testing.T) {
		var body *trackedBody
		backend := &fakeBackend{open: func(context.Context, storage.Namespace, storage.Key) (io.ReadCloser, storage.Info, error) {
			return body, storage.Info{Size: 7}, storage.NewError("minio open", storage.KindTemporary, errors.New("lost response"))
		}}
		opened, info, err := newStore(backend).Open(context.Background(), key)
		if !errors.Is(err, storage.ErrTemporary) || opened != nil || !reflect.DeepEqual(info, storage.Info{}) {
			t.Fatalf("errored typed-nil Open = body %v info %#v error %v", opened, info, err)
		}
	})

	t.Run("nil successful body", func(t *testing.T) {
		backend := &fakeBackend{open: func(context.Context, storage.Namespace, storage.Key) (io.ReadCloser, storage.Info, error) {
			return nil, storage.Info{Size: 1}, nil
		}}
		opened, info, err := newStore(backend).Open(context.Background(), key)
		if !errors.Is(err, storage.ErrInternal) || opened != nil || !reflect.DeepEqual(info, storage.Info{}) {
			t.Fatalf("nil-body Open = body %v info %#v error %v", opened, info, err)
		}
	})

	t.Run("typed nil successful body", func(t *testing.T) {
		var body *trackedBody
		backend := &fakeBackend{open: func(context.Context, storage.Namespace, storage.Key) (io.ReadCloser, storage.Info, error) {
			return body, storage.Info{Size: 1}, nil
		}}
		opened, info, err := newStore(backend).Open(context.Background(), key)
		if !errors.Is(err, storage.ErrInternal) || opened != nil || !reflect.DeepEqual(info, storage.Info{}) {
			t.Fatalf("typed-nil-body Open = body %v info %#v error %v", opened, info, err)
		}
	})
}

func TestCapabilitiesAreTheBackendsImmutableDeclaration(t *testing.T) {
	want := storage.Capabilities{CreateOnly: true, Replace: true, Staging: true, TemporaryURL: true}
	backend := &fakeBackend{caps: want}
	if got := newStore(backend).Capabilities(); got != want {
		t.Fatalf("Capabilities() = %#v, want %#v", got, want)
	}
	if backend.calls != 0 {
		t.Fatalf("Capabilities unexpectedly counted as an object operation: %d", backend.calls)
	}
}

type trackedSource struct {
	*bytes.Reader
	reads  int
	closes int
}

func (this *trackedSource) Read(p []byte) (int, error) {
	this.reads++
	return this.Reader.Read(p)
}

func (this *trackedSource) Close() error {
	this.closes++
	return nil
}

type trackedBody struct {
	io.Reader
	closes int
}

func (this *trackedBody) Close() error {
	this.closes++
	return nil
}

func mustKey(t *testing.T, raw string) storage.Key {
	t.Helper()
	key, err := storage.ParseKey(raw)
	if err != nil {
		t.Fatalf("ParseKey(%q): %v", raw, err)
	}
	return key
}

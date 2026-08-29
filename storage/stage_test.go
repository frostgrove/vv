package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/storage"
)

func TestAStagedUploadRoundTripsThroughAFormBeforePromotion(t *testing.T) {
	id, err := storage.NewStageID()
	if err != nil {
		t.Fatalf("NewStageID: %v", err)
	}
	key := mustKey(t, "images/final.png")
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	backendStageMetadata := storage.Metadata{"classification": "preview"}
	backendPromoteMetadata := storage.Metadata{"classification": "final"}
	source := &trackedSource{Reader: bytes.NewReader([]byte("png bytes"))}
	size := int64(len("png bytes"))
	inputMetadata := storage.Metadata{"classification": "preview"}

	var stagedOptions storage.StageOptions
	var promotedID storage.StageID
	var promotedKey storage.Key
	var promotedOptions storage.PromoteOptions
	backend := &fakeBackend{
		stage: func(_ context.Context, namespace storage.Namespace, source io.Reader, options storage.StageOptions) (storage.Staged, error) {
			if namespace.Value() != "documents" || source != source {
				t.Fatalf("Stage boundary namespace=%q source identity=%t", namespace.Value(), source == source)
			}
			stagedOptions = options
			body, err := io.ReadAll(source)
			if err != nil {
				return storage.Staged{}, err
			}
			return storage.Staged{
				ID:        id,
				Info:      storage.Info{Size: int64(len(body)), ContentType: options.ContentType, Metadata: backendStageMetadata},
				ExpiresAt: now.Add(options.ExpiresIn),
			}, nil
		},
		promote: func(_ context.Context, namespace storage.Namespace, gotID storage.StageID, gotKey storage.Key, options storage.PromoteOptions) (storage.Info, error) {
			if namespace.Value() != "documents" {
				t.Fatalf("Promote namespace = %q", namespace.Value())
			}
			promotedID, promotedKey, promotedOptions = gotID, gotKey, options
			return storage.Info{Size: size, ContentType: "image/png", Metadata: backendPromoteMetadata}, nil
		},
	}

	firstStore := newStore(backend)
	staged, err := firstStore.Stage(context.Background(), source, storage.StageOptions{
		Size:        &size,
		ContentType: "image/png",
		Metadata:    inputMetadata,
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if staged.ID != id || !staged.ExpiresAt.Equal(now.Add(storage.DefaultStageTTL)) {
		t.Fatalf("Staged = %#v", staged)
	}
	if stagedOptions.ExpiresIn != storage.DefaultStageTTL || stagedOptions.ContentType != "image/png" {
		t.Fatalf("normalized StageOptions = %#v", stagedOptions)
	}
	if stagedOptions.Size == nil || *stagedOptions.Size != size || stagedOptions.Size == &size {
		t.Fatal("Stage size was not copied")
	}
	if source.closes != 0 {
		t.Fatalf("Stage closed its caller-owned source %d times", source.closes)
	}

	inputMetadata["classification"] = "changed by caller"
	staged.Info.Metadata["classification"] = "changed result"
	if stagedOptions.Metadata["classification"] != "preview" || backendStageMetadata["classification"] != "preview" {
		t.Fatal("Stage metadata crossed a caller/backend ownership boundary")
	}

	// This is the actual two-request UI seam: only the opaque text survives,
	// and a newly constructed Store can promote the parsed value.
	serialized := staged.ID.Value()
	parsed, err := storage.ParseStageID(serialized)
	if err != nil {
		t.Fatalf("ParseStageID(form value): %v", err)
	}
	secondStore := newStore(backend)
	info, err := secondStore.Promote(context.Background(), parsed, key, storage.PromoteOptions{})
	if err != nil {
		t.Fatalf("Promote after round trip: %v", err)
	}
	if promotedID != id || promotedKey != key || promotedOptions.Mode != storage.CreateOnly {
		t.Fatalf("Promote boundary id=%q key=%q mode=%v", promotedID.Value(), promotedKey.Value(), promotedOptions.Mode)
	}
	info.Metadata["classification"] = "changed result"
	if backendPromoteMetadata["classification"] != "final" {
		t.Fatal("Promote Info metadata aliases backend state")
	}
}

func TestInvalidStageInputNeverStartsAnUpload(t *testing.T) {
	negative := int64(-1)
	cases := []struct {
		name    string
		options storage.StageOptions
	}{
		{"negative size", storage.StageOptions{Size: &negative}},
		{"expiry below minimum", storage.StageOptions{ExpiresIn: time.Millisecond}},
		{"expiry above maximum", storage.StageOptions{ExpiresIn: storage.MaxStageTTL + time.Second}},
		{"malformed content type", storage.StageOptions{ContentType: "image/png; broken"}},
		{"invalid metadata", storage.StageOptions{Metadata: storage.Metadata{"Upper": "value"}}},
	}
	for _, tc := range cases {
		backend := &fakeBackend{}
		source := &trackedSource{Reader: bytes.NewReader([]byte("must stay unread"))}
		staged, err := newStore(backend).Stage(context.Background(), source, tc.options)
		if !errors.Is(err, storage.ErrInvalid) {
			t.Errorf("%s: Stage error = %v, want ErrInvalid", tc.name, err)
		}
		if !reflect.DeepEqual(staged, storage.Staged{}) {
			t.Errorf("%s: Stage returned %#v with an error", tc.name, staged)
		}
		if backend.calls != 0 || source.reads != 0 || source.closes != 0 {
			t.Errorf("%s: calls=%d reads=%d closes=%d", tc.name, backend.calls, source.reads, source.closes)
		}
	}

	backend := &fakeBackend{}
	var nilSource *trackedSource
	if _, err := newStore(backend).Stage(context.Background(), nilSource, storage.StageOptions{}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("typed nil source error = %v, want ErrInvalid", err)
	}
	var nilContext context.Context
	if _, err := newStore(backend).Stage(nilContext, bytes.NewReader(nil), storage.StageOptions{}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("nil context error = %v, want ErrInvalid", err)
	}
	if backend.calls != 0 {
		t.Fatalf("nil Stage input made %d backend calls", backend.calls)
	}
}

func TestStageCanonicalizesEveryEmptyMetadataMapToNil(t *testing.T) {
	id, err := storage.NewStageID()
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{stage: func(_ context.Context, _ storage.Namespace, _ io.Reader, options storage.StageOptions) (storage.Staged, error) {
		if options.Metadata != nil {
			t.Fatalf("backend metadata = %#v, want canonical nil", options.Metadata)
		}
		return storage.Staged{ID: id, ExpiresAt: time.Now().Add(options.ExpiresIn)}, nil
	}}
	staged, err := newStore(backend).Stage(context.Background(), strings.NewReader("body"), storage.StageOptions{Metadata: storage.Metadata{}})
	if err != nil {
		t.Fatal(err)
	}
	if staged.Info.Metadata != nil {
		t.Fatalf("returned metadata = %#v, want nil", staged.Info.Metadata)
	}
}

func TestStageRefusesAnInvalidBackendResultAndErasesAnErroredOne(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		result storage.Staged
		err    error
		kind   storage.Kind
	}{
		{"zero stage id", storage.Staged{ExpiresAt: now}, nil, storage.KindInternal},
		{"zero expiry", storage.Staged{ID: mustStageID(t)}, nil, storage.KindInternal},
		{"backend error", storage.Staged{ID: mustStageID(t), Info: storage.Info{Size: 12}, ExpiresAt: now}, storage.NewError("stage write", storage.KindSource, errors.New("reader failed")), storage.KindSource},
	}
	for _, tc := range cases {
		backend := &fakeBackend{stage: func(context.Context, storage.Namespace, io.Reader, storage.StageOptions) (storage.Staged, error) {
			return tc.result, tc.err
		}}
		got, err := newStore(backend).Stage(context.Background(), bytes.NewReader(nil), storage.StageOptions{})
		if storage.KindOf(err) != tc.kind {
			t.Errorf("%s: Stage error=%v kind=%q, want %q", tc.name, err, storage.KindOf(err), tc.kind)
		}
		if !reflect.DeepEqual(got, storage.Staged{}) {
			t.Errorf("%s: Stage returned result with error: %#v", tc.name, got)
		}
	}
}

func TestPromoteValidatesItsSerializableInputsAndReturnsNoInfoOnFailure(t *testing.T) {
	id := mustStageID(t)
	key := mustKey(t, "images/final.png")

	backend := &fakeBackend{promote: func(context.Context, storage.Namespace, storage.StageID, storage.Key, storage.PromoteOptions) (storage.Info, error) {
		return storage.Info{Size: 99, Metadata: storage.Metadata{"should": "disappear"}}, storage.NewError("copy", storage.KindAlreadyExists, errors.New("destination exists"))
	}}
	info, err := newStore(backend).Promote(context.Background(), id, key, storage.PromoteOptions{Mode: storage.CreateOnly})
	if !errors.Is(err, storage.ErrAlreadyExists) || !reflect.DeepEqual(info, storage.Info{}) {
		t.Fatalf("failed Promote = info %#v error %v", info, err)
	}

	invalidCases := []struct {
		name    string
		id      storage.StageID
		key     storage.Key
		options storage.PromoteOptions
	}{
		{"zero stage id", storage.StageID{}, key, storage.PromoteOptions{}},
		{"zero key", id, storage.Key{}, storage.PromoteOptions{}},
		{"unknown mode", id, key, storage.PromoteOptions{Mode: storage.WriteMode(99)}},
	}
	for _, tc := range invalidCases {
		fresh := &fakeBackend{}
		_, err := newStore(fresh).Promote(context.Background(), tc.id, tc.key, tc.options)
		if !errors.Is(err, storage.ErrInvalid) {
			t.Errorf("%s: Promote error = %v, want ErrInvalid", tc.name, err)
		}
		if fresh.calls != 0 {
			t.Errorf("%s: made %d backend calls", tc.name, fresh.calls)
		}
	}
}

func TestAbortAcceptsAParsedStageIDAndRejectsTheZeroValueLocally(t *testing.T) {
	id := mustStageID(t)
	parsed, err := storage.ParseStageID(id.Value())
	if err != nil {
		t.Fatal(err)
	}
	var got storage.StageID
	backend := &fakeBackend{abort: func(_ context.Context, namespace storage.Namespace, id storage.StageID) error {
		if namespace.Value() != "documents" {
			t.Fatalf("Abort namespace = %q", namespace.Value())
		}
		got = id
		return nil
	}}
	if err := newStore(backend).Abort(context.Background(), parsed); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if got != id {
		t.Fatalf("Abort ID = %q, want %q", got.Value(), id.Value())
	}

	fresh := &fakeBackend{}
	if err := newStore(fresh).Abort(context.Background(), storage.StageID{}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("Abort(zero) error = %v, want ErrInvalid", err)
	}
	if fresh.calls != 0 {
		t.Fatalf("Abort(zero) made %d backend calls", fresh.calls)
	}
}

func mustStageID(t *testing.T) storage.StageID {
	t.Helper()
	id, err := storage.NewStageID()
	if err != nil {
		t.Fatalf("NewStageID: %v", err)
	}
	return id
}

package storageminio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/storage"
	"github.com/minio/minio-go/v7"
)

var testNow = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

type fakeClient struct {
	put       func(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error)
	stat      func(context.Context, string, string, minio.StatObjectOptions) (minio.ObjectInfo, error)
	remove    func(context.Context, string, string, minio.RemoveObjectOptions) error
	list      func(context.Context, string, minio.ListObjectsOptions) <-chan minio.ObjectInfo
	presigned func(context.Context, string, string, time.Duration, url.Values) (*url.URL, error)
}

func (f *fakeClient) PutObject(ctx context.Context, bucket, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	if f.put == nil {
		panic("unexpected PutObject")
	}
	return f.put(ctx, bucket, object, source, size, opts)
}

func (f *fakeClient) StatObject(ctx context.Context, bucket, object string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	if f.stat == nil {
		panic("unexpected StatObject")
	}
	return f.stat(ctx, bucket, object, opts)
}

func (f *fakeClient) RemoveObject(ctx context.Context, bucket, object string, opts minio.RemoveObjectOptions) error {
	if f.remove == nil {
		panic("unexpected RemoveObject")
	}
	return f.remove(ctx, bucket, object, opts)
}

func (f *fakeClient) ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
	if f.list == nil {
		panic("unexpected ListObjects")
	}
	return f.list(ctx, bucket, opts)
}

func (f *fakeClient) PresignedGetObject(ctx context.Context, bucket, object string, ttl time.Duration, values url.Values) (*url.URL, error) {
	if f.presigned == nil {
		panic("unexpected PresignedGetObject")
	}
	return f.presigned(ctx, bucket, object, ttl, values)
}

type fakeCore struct {
	get func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error)
}

func (f *fakeCore) GetObject(ctx context.Context, bucket, object string, opts minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
	if f.get == nil {
		panic("unexpected GetObject")
	}
	return f.get(ctx, bucket, object, opts)
}

func newTestBackend(t *testing.T, client *fakeClient, core *fakeCore, mutate ...func(*Config)) *Backend {
	t.Helper()
	config := Config{
		Bucket: "test-bucket",
		Prefix: "root",
		Clock:  func() time.Time { return testNow },
	}
	for _, fn := range mutate {
		fn(&config)
	}
	backend, err := newBackend(&config, client, core)
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func newTestStore(t *testing.T, backend *Backend) storage.Store {
	t.Helper()
	store, err := storage.New(&storage.Config{Namespace: "tenant", Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testKey(t *testing.T, value string) storage.Key {
	t.Helper()
	key, err := storage.ParseKey(value)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type closeSpy struct {
	*bytes.Reader
	closed int
}

func (r *closeSpy) Close() error {
	r.closed++
	return nil
}

type readSpy struct{ reads int }

func (r *readSpy) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

type hostileBody struct {
	read   func([]byte) (int, error)
	close  func() error
	closed int
}

func (b *hostileBody) Read(p []byte) (int, error) {
	return b.read(p)
}

func (b *hostileBody) Close() error {
	b.closed++
	if b.close == nil {
		return nil
	}
	return b.close()
}

type noProgressReader struct{ reads int }

func (r *noProgressReader) Read([]byte) (int, error) {
	r.reads++
	return 0, nil
}

type blockedFailureReader struct {
	started chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
}

func newBlockedFailureReader(err error) *blockedFailureReader {
	return &blockedFailureReader{started: make(chan struct{}), release: make(chan struct{}), err: err}
}

func (r *blockedFailureReader) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, r.err
}

func TestNewValidatesConfiguration(t *testing.T) {
	if _, err := New(&Config{Bucket: "test-bucket"}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("nil client error = %v", err)
	}

	client := &fakeClient{}
	core := &fakeCore{}
	tests := []Config{
		{Bucket: "UPPER", Prefix: "ok"},
		{Bucket: "test-bucket", Prefix: "/absolute"},
		{Bucket: "test-bucket", Prefix: "trailing/"},
		{Bucket: "test-bucket", Prefix: strings.Repeat("a", maxPrefixBytes+1)},
		{Bucket: "test-bucket", MaxLinkTTL: 1500 * time.Millisecond},
		{Bucket: "test-bucket", MaxLinkTTL: storage.MaxTemporaryURLTTL + time.Second},
	}
	for _, config := range tests {
		if _, err := newBackend(&config, client, core); !errors.Is(err, storage.ErrInvalid) {
			t.Fatalf("config %#v error = %v, want invalid", config, err)
		}
	}
}

func TestPutModesMetadataAndSourceOwnership(t *testing.T) {
	tests := []struct {
		name          string
		mode          storage.WriteMode
		wantCondition string
	}{
		{name: "create only", mode: storage.CreateOnly, wantCondition: "*"},
		{name: "replace", mode: storage.Replace},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotObject string
			var gotOptions minio.PutObjectOptions
			var gotSize int64
			client := &fakeClient{put: func(_ context.Context, bucket, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
				if bucket != "test-bucket" {
					t.Fatalf("bucket = %q", bucket)
				}
				gotObject, gotOptions, gotSize = object, opts, size
				body, err := io.ReadAll(source)
				if err != nil {
					return minio.UploadInfo{}, err
				}
				return minio.UploadInfo{Size: int64(len(body)), ETag: "etag", VersionID: "version"}, nil
			}}
			backend := newTestBackend(t, client, &fakeCore{})
			store := newTestStore(t, backend)
			source := &closeSpy{Reader: bytes.NewReader([]byte("abc"))}
			metadata := storage.Metadata{"owner": "avatar"}
			info, err := store.Put(context.Background(), testKey(t, "images/a.png"), source, storage.PutOptions{
				Mode:        tt.mode,
				Size:        storage.ExactSize(3),
				ContentType: "image/png",
				Metadata:    metadata,
			})
			if err != nil {
				t.Fatal(err)
			}
			if source.closed != 0 {
				t.Fatalf("source closed %d times", source.closed)
			}
			if gotObject != "root/tenant/images/a.png" {
				t.Fatalf("object = %q", gotObject)
			}
			if gotSize != 3 {
				t.Fatalf("SDK size = %d, want declared size", gotSize)
			}
			if got := gotOptions.Header().Get("If-None-Match"); got != tt.wantCondition {
				t.Fatalf("If-None-Match = %q", got)
			}
			if gotOptions.DisableMultipart != (tt.mode == storage.CreateOnly) {
				t.Fatalf("DisableMultipart = %v", gotOptions.DisableMultipart)
			}
			if gotOptions.ContentType != "image/png" || gotOptions.UserMetadata["owner"] != "avatar" {
				t.Fatalf("options = %#v", gotOptions)
			}
			if info.Size != 3 || info.ETag != "etag" || info.Version != "version" || info.Metadata["owner"] != "avatar" {
				t.Fatalf("info = %#v", info)
			}
			metadata["owner"] = "changed"
			if info.Metadata["owner"] != "avatar" {
				t.Fatal("returned metadata aliases caller metadata")
			}
		})
	}
}

func TestPutDeclaredZeroSizeIsProbedBeforeSDKCall(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantError error
		wantCalls int
	}{
		{name: "empty", wantCalls: 1},
		{name: "non-empty", body: "x", wantError: storage.ErrSource},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := &fakeClient{put: func(_ context.Context, _, _ string, _ io.Reader, size int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
				calls++
				if size != 0 {
					t.Fatalf("SDK size = %d", size)
				}
				// A real HTTP client need not call Read for Content-Length: 0.
				return minio.UploadInfo{}, nil
			}}
			store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
			source := &closeSpy{Reader: bytes.NewReader([]byte(tt.body))}
			_, err := store.Put(context.Background(), testKey(t, "zero"), source, storage.PutOptions{Size: storage.ExactSize(0)})
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("error = %v, want %v", err, tt.wantError)
			}
			if calls != tt.wantCalls || source.closed != 0 {
				t.Fatalf("calls/closed = %d/%d", calls, source.closed)
			}
		})
	}
}

func TestPutPreCancelledContextDoesNotReadOrCallSDK(t *testing.T) {
	client := &fakeClient{put: func(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error) {
		t.Fatal("pre-cancelled put reached SDK")
		return minio.UploadInfo{}, nil
	}}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	source := &readSpy{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.Put(ctx, testKey(t, "cancelled"), source, storage.PutOptions{Size: storage.ExactSize(0)})
	if !errors.Is(err, storage.ErrCancelled) || source.reads != 0 {
		t.Fatalf("error/reads = %v/%d", err, source.reads)
	}
}

func TestCreateOnlyRejectsPayloadAboveSinglePutLimitWithoutReading(t *testing.T) {
	client := &fakeClient{put: func(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error) {
		t.Fatal("oversized CreateOnly reached SDK")
		return minio.UploadInfo{}, nil
	}}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	source := &readSpy{}
	_, err := store.Put(context.Background(), testKey(t, "too-large"), source, storage.PutOptions{
		Mode: storage.CreateOnly,
		Size: storage.ExactSize(MaxCreateOnlySize + 1),
	})
	if !errors.Is(err, storage.ErrUnsupported) || source.reads != 0 {
		t.Fatalf("error/reads = %v/%d", err, source.reads)
	}
}

func TestPutDeclaredSizeMismatchAbortsBeforeSuccess(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "fewer", body: "ab", wantErr: true},
		{name: "exact", body: "abc"},
		{name: "more", body: "abcd", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeClient{put: func(_ context.Context, _, _ string, source io.Reader, size int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
				if size != 3 {
					t.Fatalf("SDK size = %d", size)
				}
				body, err := io.ReadAll(source)
				if err != nil {
					return minio.UploadInfo{}, err
				}
				return minio.UploadInfo{Size: int64(len(body))}, nil
			}}
			backend := newTestBackend(t, client, &fakeCore{})
			store := newTestStore(t, backend)
			source := &closeSpy{Reader: bytes.NewReader([]byte(tt.body))}
			_, err := store.Put(context.Background(), testKey(t, "sized"), source, storage.PutOptions{Size: storage.ExactSize(3)})
			if tt.wantErr && !errors.Is(err, storage.ErrSource) {
				t.Fatalf("error = %v, want source", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatal(err)
			}
			if source.closed != 0 {
				t.Fatalf("source closed %d times", source.closed)
			}
		})
	}
}

func TestNoProgressSourceIsBoundedForPutAndStage(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(storage.Store, io.Reader) error
	}{
		{
			name: "put known size",
			invoke: func(store storage.Store, source io.Reader) error {
				_, err := store.Put(context.Background(), testKey(t, "known"), source, storage.PutOptions{Mode: storage.Replace, Size: storage.ExactSize(1)})
				return err
			},
		},
		{
			name: "put unknown size",
			invoke: func(store storage.Store, source io.Reader) error {
				_, err := store.Put(context.Background(), testKey(t, "unknown"), source, storage.PutOptions{Mode: storage.Replace})
				return err
			},
		},
		{
			name: "stage known size",
			invoke: func(store storage.Store, source io.Reader) error {
				_, err := store.Stage(context.Background(), source, storage.StageOptions{Size: storage.ExactSize(1), ExpiresIn: time.Hour})
				return err
			},
		},
		{
			name: "stage unknown size",
			invoke: func(store storage.Store, source io.Reader) error {
				_, err := store.Stage(context.Background(), source, storage.StageOptions{ExpiresIn: time.Hour})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdkCalls := 0
			visible := false
			client := &fakeClient{put: func(_ context.Context, _, _ string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
				sdkCalls++
				written, err := io.Copy(io.Discard, source)
				if err != nil {
					return minio.UploadInfo{}, err
				}
				visible = true
				return minio.UploadInfo{Size: written}, nil
			}}
			store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
			source := &noProgressReader{}
			err := tt.invoke(store, source)
			if !errors.Is(err, storage.ErrSource) || !errors.Is(err, io.ErrNoProgress) {
				t.Fatalf("error = %v", err)
			}
			if source.reads != maxConsecutiveNoProgressReads || sdkCalls != 1 || visible {
				t.Fatalf("reads/sdk calls/visible = %d/%d/%v", source.reads, sdkCalls, visible)
			}
		})
	}
}

func TestSourceReaderResetsNoProgressCounterAfterData(t *testing.T) {
	reads := 0
	source := &sourceReader{reader: readerFunc(func(p []byte) (int, error) {
		reads++
		switch {
		case reads == maxConsecutiveNoProgressReads:
			p[0] = 'x'
			return 1, nil
		case reads == 2*maxConsecutiveNoProgressReads:
			return 0, io.EOF
		default:
			return 0, nil
		}
	})}
	buffer := make([]byte, 1)
	for {
		_, err := source.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read %d: %v", reads, err)
		}
	}
	if reads != 2*maxConsecutiveNoProgressReads || source.err != nil {
		t.Fatalf("reads/error = %d/%v", reads, source.err)
	}
}

func TestUnknownSizeCreateOnlyStagesThenConditionallyWritesFinal(t *testing.T) {
	callerSource := &closeSpy{Reader: bytes.NewReader([]byte("abc"))}
	stageBody := &closeSpy{Reader: bytes.NewReader([]byte("abc"))}
	var stageObject string
	putCalls := 0
	var removed []string
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			putCalls++
			body, err := io.ReadAll(source)
			if err != nil {
				return minio.UploadInfo{}, err
			}
			switch putCalls {
			case 1:
				stageObject = object
				if !strings.HasPrefix(object, "root/.vv-stage/tenant/") || size != -1 || opts.Header().Get("If-None-Match") != "" {
					t.Fatalf("stage target/size/condition = %q/%d/%q", object, size, opts.Header().Get("If-None-Match"))
				}
				if opts.UserMetadata[stageMarkerKey] != stageMarkerValue || string(body) != "abc" {
					t.Fatalf("stage metadata/body = %#v/%q", opts.UserMetadata, body)
				}
				return minio.UploadInfo{Size: 3}, nil
			case 2:
				if object != "root/tenant/final" || size != 3 || opts.Header().Get("If-None-Match") != "*" || !opts.DisableMultipart || string(body) != "abc" {
					t.Fatalf("final target/size/condition/single/body = %q/%d/%q/%v/%q", object, size, opts.Header().Get("If-None-Match"), opts.DisableMultipart, body)
				}
				return minio.UploadInfo{Size: 3, ETag: "final-etag"}, nil
			default:
				t.Fatal("unexpected extra put")
				return minio.UploadInfo{}, nil
			}
		},
		remove: func(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
			removed = append(removed, object)
			return nil
		},
	}
	core := &fakeCore{get: func(_ context.Context, _, object string, _ minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		if object != stageObject {
			t.Fatalf("opened stage = %q, want %q", object, stageObject)
		}
		return stageBody, stageObjectInfo(testNow.Add(storage.DefaultStageTTL)), nil, nil
	}}
	store := newTestStore(t, newTestBackend(t, client, core))
	info, err := store.Put(context.Background(), testKey(t, "final"), callerSource, storage.PutOptions{ContentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if info.ETag != "final-etag" || putCalls != 2 || !slices.Equal(removed, []string{stageObject}) {
		t.Fatalf("info/calls/removed = %#v/%d/%v", info, putCalls, removed)
	}
	if callerSource.closed != 0 || stageBody.closed != 1 {
		t.Fatalf("caller/stage close counts = %d/%d", callerSource.closed, stageBody.closed)
	}
}

func TestUnknownSizeCreateOnlyCleansPrivateStageAfterGETFailure(t *testing.T) {
	var stageObject string
	removed := ""
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
			stageObject = object
			_, err := io.ReadAll(source)
			return minio.UploadInfo{Size: 3}, err
		},
		remove: func(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
			removed = object
			return nil
		},
	}
	core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		return nil, minio.ObjectInfo{}, nil, minio.ErrorResponse{Code: minio.InternalError, StatusCode: http.StatusInternalServerError}
	}}
	store := newTestStore(t, newTestBackend(t, client, core))
	_, err := store.Put(context.Background(), testKey(t, "final"), strings.NewReader("abc"), storage.PutOptions{})
	if !errors.Is(err, storage.ErrTemporary) || removed != stageObject {
		t.Fatalf("error/stage/removed = %v/%q/%q", err, stageObject, removed)
	}
}

func TestUnknownSizeCreateOnlyRejectsOversizedStageBeforeFinalPUT(t *testing.T) {
	putCalls := 0
	removed := ""
	var stageObject string
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
			putCalls++
			stageObject = object
			_, err := io.ReadAll(source)
			return minio.UploadInfo{Size: 1}, err
		},
		remove: func(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
			removed = object
			return nil
		},
	}
	body := &closeSpy{Reader: bytes.NewReader(nil)}
	core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		info := stageObjectInfo(testNow.Add(time.Hour))
		info.Size = MaxCreateOnlySize + 1
		return body, info, nil, nil
	}}
	store := newTestStore(t, newTestBackend(t, client, core))
	_, err := store.Put(context.Background(), testKey(t, "final"), strings.NewReader("x"), storage.PutOptions{})
	if !errors.Is(err, storage.ErrUnsupported) || putCalls != 1 || removed != stageObject || body.closed != 1 {
		t.Fatalf("error/calls/stage/removed/closed = %v/%d/%q/%q/%d", err, putCalls, stageObject, removed, body.closed)
	}
}

func TestOpenUsesImmediateCoreGetAndFiltersInternalMetadata(t *testing.T) {
	body := &closeSpy{Reader: bytes.NewReader([]byte("abc"))}
	core := &fakeCore{get: func(_ context.Context, bucket, object string, _ minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		if bucket != "test-bucket" || object != "root/tenant/image" {
			t.Fatalf("target = %s/%s", bucket, object)
		}
		return body, minio.ObjectInfo{
			Size:         3,
			ContentType:  "image/png",
			LastModified: testNow,
			ETag:         "etag",
			VersionID:    "version",
			UserMetadata: minio.StringMap{"Owner": "avatar", "Vv-Stage": "1", "Vv-Stage-Expires": testNow.Add(time.Hour).Format(time.RFC3339Nano)},
		}, nil, nil
	}}
	backend := newTestBackend(t, &fakeClient{}, core)
	store := newTestStore(t, backend)
	gotBody, info, err := store.Open(context.Background(), testKey(t, "image"))
	if err != nil {
		t.Fatal(err)
	}
	if gotBody == body || body.closed != 0 {
		t.Fatal("backend did not return a caller-owned wrapper around the immediate GET body")
	}
	if info.Metadata["owner"] != "avatar" || len(info.Metadata) != 1 {
		t.Fatalf("metadata = %#v", info.Metadata)
	}
	payload, err := io.ReadAll(gotBody)
	if err != nil || string(payload) != "abc" || body.closed != 0 {
		t.Fatalf("read/body/closed = %q/%v/%d", payload, err, body.closed)
	}
	if err := gotBody.Close(); err != nil || body.closed != 1 {
		t.Fatalf("close err/count = %v/%d", err, body.closed)
	}
}

func TestOpenBodyMapsAndRedactsDeferredReadError(t *testing.T) {
	providerErr := minio.ErrorResponse{
		Code:       "SlowDown",
		StatusCode: http.StatusServiceUnavailable,
		BucketName: "secret-bucket",
		Key:        "secret-key",
		Message:    "secret read failure",
	}
	body := &hostileBody{read: func(p []byte) (int, error) {
		p[0] = 'x'
		return 1, providerErr
	}}
	core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		return body, minio.ObjectInfo{Size: 1, ContentType: "application/octet-stream", LastModified: testNow}, nil, nil
	}}
	store := newTestStore(t, newTestBackend(t, &fakeClient{}, core))
	gotBody, _, err := store.Open(context.Background(), testKey(t, "image"))
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	n, err := gotBody.Read(buffer)
	if n != 1 || buffer[0] != 'x' || !errors.Is(err, storage.ErrTemporary) {
		t.Fatalf("read = %d/%q/%v", n, buffer, err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("unredacted deferred read error = %q", err)
	}
	var portable *storage.Error
	if !errors.As(err, &portable) || portable.Operation != "open" {
		t.Fatalf("error = %#v", err)
	}
	if err := gotBody.Close(); err != nil || body.closed != 1 {
		t.Fatalf("close err/count = %v/%d", err, body.closed)
	}
}

func TestOpenBodyMapsAndRedactsDeferredCloseError(t *testing.T) {
	providerErr := storage.NewError("secret-close-operation", storage.KindForbidden, errors.New("secret close detail"))
	body := &hostileBody{
		read:  func([]byte) (int, error) { return 0, io.EOF },
		close: func() error { return providerErr },
	}
	core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		return body, minio.ObjectInfo{Size: 0, ContentType: "application/octet-stream", LastModified: testNow}, nil, nil
	}}
	store := newTestStore(t, newTestBackend(t, &fakeClient{}, core))
	gotBody, _, err := store.Open(context.Background(), testKey(t, "image"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gotBody.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("EOF = %v", err)
	}
	err = gotBody.Close()
	if !errors.Is(err, storage.ErrForbidden) || !errors.Is(err, providerErr) || body.closed != 1 {
		t.Fatalf("close error/count = %v/%d", err, body.closed)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("unredacted deferred close error = %q", err)
	}
	var portable *storage.Error
	if !errors.As(err, &portable) || portable.Operation != "open" {
		t.Fatalf("error = %#v", err)
	}
}

func TestOpenRejectsCaseAmbiguousMetadata(t *testing.T) {
	body := &closeSpy{Reader: bytes.NewReader(nil)}
	core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		return body, minio.ObjectInfo{
			Size:         0,
			ContentType:  "application/octet-stream",
			LastModified: testNow,
			UserMetadata: minio.StringMap{"Owner": "one", "owner": "two"},
		}, nil, nil
	}}
	store := newTestStore(t, newTestBackend(t, &fakeClient{}, core))
	_, _, err := store.Open(context.Background(), testKey(t, "image"))
	if !errors.Is(err, storage.ErrInternal) || body.closed != 1 {
		t.Fatalf("error/closed = %v/%d", err, body.closed)
	}
}

func TestCreateOnlyErrorIsTypedAndRedacted(t *testing.T) {
	client := &fakeClient{put: func(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error) {
		return minio.UploadInfo{}, minio.ErrorResponse{
			Code:       minio.PreconditionFailed,
			StatusCode: http.StatusPreconditionFailed,
			BucketName: "secret-bucket",
			Key:        "secret-key",
			Message:    "secret-key already exists",
		}
	}}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	_, err := store.Put(context.Background(), testKey(t, "visible-key"), strings.NewReader("x"), storage.PutOptions{Size: storage.ExactSize(1)})
	if !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "visible-key") {
		t.Fatalf("unredacted error = %q", err)
	}
}

func TestReaderStorageErrorIsReclassifiedAsBoundedSourceFailure(t *testing.T) {
	client := &fakeClient{put: func(_ context.Context, _, _ string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
		_, err := io.ReadAll(source)
		return minio.UploadInfo{}, err
	}}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	hostile := storage.NewError("secret-key-operation", storage.KindNotFound, errors.New("secret-provider-detail"))
	_, err := store.Put(context.Background(), testKey(t, "visible"), errorReader{err: hostile}, storage.PutOptions{Size: storage.ExactSize(1)})
	if !errors.Is(err, storage.ErrSource) || errors.Is(err, storage.ErrNotFound) || !errors.Is(err, hostile) {
		t.Fatalf("error kind = %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("unbounded source error = %q", err)
	}
}

func TestReaderCancellationIsASourceFailureWhileTheCallIsLive(t *testing.T) {
	client := &fakeClient{put: func(_ context.Context, _, _ string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
		_, err := io.ReadAll(source)
		return minio.UploadInfo{}, err
	}}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	_, err := store.Put(context.Background(), testKey(t, "visible"), errorReader{err: context.Canceled}, storage.PutOptions{Size: storage.ExactSize(1)})
	if !errors.Is(err, storage.ErrSource) || !errors.Is(err, context.Canceled) || errors.Is(err, storage.ErrCancelled) {
		t.Fatalf("error = %v, want source provenance retaining context cancellation", err)
	}
}

func TestOperationCancellationDuringBlockedCallerReadWinsOverSourceFailure(t *testing.T) {
	for _, stage := range []bool{false, true} {
		name := "put"
		if stage {
			name = "stage"
		}
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{put: func(_ context.Context, _, _ string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
				_, err := io.Copy(io.Discard, source)
				return minio.UploadInfo{}, err
			}}
			store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
			ctx, cancel := context.WithCancel(context.Background())
			sourceFailure := errors.New("source unblocked with failure")
			source := newBlockedFailureReader(sourceFailure)
			key := testKey(t, "cancel/blocked")
			result := make(chan error, 1)
			go func() {
				if stage {
					_, err := store.Stage(ctx, source, storage.StageOptions{ExpiresIn: time.Hour})
					result <- err
					return
				}
				_, err := store.Put(ctx, key, source, storage.PutOptions{Mode: storage.Replace})
				result <- err
			}()
			<-source.started
			cancel()
			close(source.release)
			err := <-result
			if !errors.Is(err, storage.ErrCancelled) || !errors.Is(err, context.Canceled) || errors.Is(err, storage.ErrSource) || errors.Is(err, sourceFailure) {
				t.Fatalf("cancelled blocked reader error = %v, want only ErrCancelled", err)
			}
		})
	}
}

func TestBackendOwnedStageReadFailuresAreNotCallerSourceFailures(t *testing.T) {
	t.Run("unknown-size create-only provider read", func(t *testing.T) {
		providerErr := minio.ErrorResponse{Code: "SlowDown", StatusCode: http.StatusServiceUnavailable}
		body := &hostileBody{read: func([]byte) (int, error) { return 0, providerErr }}
		putCalls := 0
		client := &fakeClient{
			put: func(_ context.Context, _, _ string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
				putCalls++
				written, err := io.Copy(io.Discard, source)
				return minio.UploadInfo{Size: written}, err
			},
			remove: func(context.Context, string, string, minio.RemoveObjectOptions) error { return nil },
		}
		core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
			return body, minio.ObjectInfo{Size: 3, ContentType: "application/octet-stream", LastModified: testNow}, nil, nil
		}}
		store := newTestStore(t, newTestBackend(t, client, core))
		_, err := store.Put(context.Background(), testKey(t, "final"), strings.NewReader("abc"), storage.PutOptions{})
		if !errors.Is(err, storage.ErrTemporary) || errors.Is(err, storage.ErrSource) || putCalls != 2 || body.closed != 1 {
			t.Fatalf("error/calls/closed = %v/%d/%d", err, putCalls, body.closed)
		}
	})

	t.Run("promote truncated backend body", func(t *testing.T) {
		id, _ := storage.NewStageID()
		putCalls := 0
		client := &fakeClient{put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			putCalls++
			if strings.Contains(object, "/"+claimDirectory+"/") {
				return successfulClaimPut(t, source, size, opts)
			}
			written, err := io.Copy(io.Discard, source)
			return minio.UploadInfo{Size: written}, err
		}, stat: stageAndClaimStat(t, nil)}
		core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
			info := stageObjectInfo(testNow.Add(time.Hour))
			info.Size = 1
			return io.NopCloser(strings.NewReader("")), info, nil, nil
		}}
		store := newTestStore(t, newTestBackend(t, client, core))
		_, err := store.Promote(context.Background(), id, testKey(t, "final"), storage.PromoteOptions{})
		if !errors.Is(err, storage.ErrTemporary) || errors.Is(err, storage.ErrSource) || putCalls != 2 {
			t.Fatalf("error/calls = %v/%d", err, putCalls)
		}
	})
}

func TestBareProvider404IsUnavailableNotLogicalNotFound(t *testing.T) {
	client := &fakeClient{stat: func(context.Context, string, string, minio.StatObjectOptions) (minio.ObjectInfo, error) {
		return minio.ObjectInfo{}, minio.ErrorResponse{StatusCode: http.StatusNotFound}
	}}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	_, err := store.Head(context.Background(), testKey(t, "visible"))
	if !errors.Is(err, storage.ErrUnavailable) || errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestStageAddsReservedMetadataWithoutExposingIt(t *testing.T) {
	var gotObject string
	var gotOptions minio.PutObjectOptions
	client := &fakeClient{put: func(_ context.Context, _, object string, source io.Reader, _ int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
		gotObject, gotOptions = object, opts
		body, err := io.ReadAll(source)
		return minio.UploadInfo{Size: int64(len(body)), ETag: "stage-etag"}, err
	}}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	staged, err := store.Stage(context.Background(), strings.NewReader("abc"), storage.StageOptions{
		ContentType: "image/png",
		Metadata:    storage.Metadata{"owner": "avatar"},
		ExpiresIn:   time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotObject, "root/.vv-stage/tenant/") || !strings.HasSuffix(gotObject, staged.ID.Value()) {
		t.Fatalf("stage object = %q", gotObject)
	}
	if gotOptions.Header().Get("If-None-Match") != "" {
		t.Fatal("random private stage unexpectedly advertises a lost multipart condition")
	}
	if gotOptions.UserMetadata[stageMarkerKey] != stageMarkerValue || gotOptions.UserMetadata[stageExpiryKey] != testNow.Add(time.Hour).Format(time.RFC3339Nano) {
		t.Fatalf("internal metadata = %#v", gotOptions.UserMetadata)
	}
	if staged.ExpiresAt != testNow.Add(time.Hour) || len(staged.Info.Metadata) != 1 || staged.Info.Metadata["owner"] != "avatar" {
		t.Fatalf("staged = %#v", staged)
	}
}

func TestStageMetadataAtPortableBoundaryFitsMinIOWireBudget(t *testing.T) {
	metadata := metadataAtPortableLimit(t)
	clock := time.Date(2026, time.August, 27, 12, 0, 0, 123456789, time.UTC)
	client := &fakeClient{put: func(_ context.Context, _, _ string, source io.Reader, _ int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
		headerBytes := 0
		headerEntries := 0
		for key, values := range opts.Header() {
			if !strings.HasPrefix(strings.ToLower(key), minioMetadataPrefix) {
				continue
			}
			for _, value := range values {
				headerBytes += len(key) + len(value)
				headerEntries++
			}
		}
		if headerBytes != minioUserMetadataSize(opts.UserMetadata) {
			t.Fatalf("wire/adapter metadata bytes = %d/%d", headerBytes, minioUserMetadataSize(opts.UserMetadata))
		}
		if headerBytes > minioUserMetadataLimit || headerEntries != storage.MaxMetadataEntries+2 {
			t.Fatalf("metadata bytes/entries = %d/%d", headerBytes, headerEntries)
		}
		_, err := io.ReadAll(source)
		return minio.UploadInfo{}, err
	}}
	backend := newTestBackend(t, client, &fakeCore{}, func(config *Config) {
		config.Clock = func() time.Time { return clock }
	})
	store := newTestStore(t, backend)
	staged, err := store.Stage(context.Background(), bytes.NewReader(nil), storage.StageOptions{
		Size:      storage.ExactSize(0),
		Metadata:  metadata,
		ExpiresIn: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(staged.Info.Metadata) != storage.MaxMetadataEntries {
		t.Fatalf("returned metadata entries = %d", len(staged.Info.Metadata))
	}
}

func TestAbsentOrEmptyMetadataIsCanonicalNilAcrossResults(t *testing.T) {
	t.Run("put", func(t *testing.T) {
		client := &fakeClient{put: func(_ context.Context, _, _ string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
			_, err := io.ReadAll(source)
			return minio.UploadInfo{}, err
		}}
		store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
		info, err := store.Put(context.Background(), testKey(t, "empty"), bytes.NewReader(nil), storage.PutOptions{
			Size:     storage.ExactSize(0),
			Metadata: storage.Metadata{},
		})
		assertNilMetadata(t, info, err)
	})

	t.Run("stage", func(t *testing.T) {
		client := &fakeClient{put: func(_ context.Context, _, _ string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
			_, err := io.ReadAll(source)
			return minio.UploadInfo{}, err
		}}
		store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
		staged, err := store.Stage(context.Background(), bytes.NewReader(nil), storage.StageOptions{
			Size:      storage.ExactSize(0),
			Metadata:  storage.Metadata{},
			ExpiresIn: time.Hour,
		})
		assertNilMetadata(t, staged.Info, err)
	})

	t.Run("promote", func(t *testing.T) {
		id, _ := storage.NewStageID()
		putCalls := 0
		client := &fakeClient{
			put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
				putCalls++
				if strings.Contains(object, "/"+claimDirectory+"/") {
					return successfulClaimPut(t, source, size, opts)
				}
				_, err := io.ReadAll(source)
				return minio.UploadInfo{}, err
			},
			remove: func(context.Context, string, string, minio.RemoveObjectOptions) error { return nil },
			stat:   stageAndClaimStat(t, nil),
		}
		core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
			return io.NopCloser(bytes.NewReader(nil)), minio.ObjectInfo{
				Size:         0,
				ContentType:  "application/octet-stream",
				LastModified: testNow,
				UserMetadata: minio.StringMap{
					stageMarkerKey: stageMarkerValue,
					stageExpiryKey: testNow.Add(time.Hour).Format(time.RFC3339Nano),
				},
			}, nil, nil
		}}
		store := newTestStore(t, newTestBackend(t, client, core))
		info, err := store.Promote(context.Background(), id, testKey(t, "empty"), storage.PromoteOptions{})
		assertNilMetadata(t, info, err)
		if putCalls != 3 {
			t.Fatalf("put calls = %d", putCalls)
		}
	})

	t.Run("head", func(t *testing.T) {
		client := &fakeClient{stat: func(context.Context, string, string, minio.StatObjectOptions) (minio.ObjectInfo, error) {
			return minio.ObjectInfo{
				Size:         0,
				ContentType:  "application/octet-stream",
				LastModified: testNow,
				UserMetadata: minio.StringMap{},
			}, nil
		}}
		store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
		info, err := store.Head(context.Background(), testKey(t, "empty"))
		assertNilMetadata(t, info, err)
	})

	t.Run("open", func(t *testing.T) {
		core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
			return io.NopCloser(bytes.NewReader(nil)), minio.ObjectInfo{
				Size:         0,
				ContentType:  "application/octet-stream",
				LastModified: testNow,
			}, nil, nil
		}}
		store := newTestStore(t, newTestBackend(t, &fakeClient{}, core))
		body, info, err := store.Open(context.Background(), testKey(t, "empty"))
		assertNilMetadata(t, info, err)
		if err := body.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func assertNilMetadata(t *testing.T, info storage.Info, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if info.Metadata != nil {
		t.Fatalf("metadata = %#v, want nil", info.Metadata)
	}
}

func metadataAtPortableLimit(t *testing.T) storage.Metadata {
	t.Helper()
	metadata := make(storage.Metadata, storage.MaxMetadataEntries)
	remaining := storage.MaxMetadataTotalBytes
	for i := range storage.MaxMetadataEntries {
		key := fmt.Sprintf("k%02d", i)
		entriesLeft := storage.MaxMetadataEntries - i
		entryBytes := (remaining + entriesLeft - 1) / entriesLeft
		valueBytes := entryBytes - len(key)
		if valueBytes < 0 || valueBytes > storage.MaxMetadataValueBytes {
			t.Fatalf("cannot construct boundary metadata: key/value bytes = %d/%d", len(key), valueBytes)
		}
		metadata[key] = strings.Repeat("v", valueBytes)
		remaining -= len(key) + valueBytes
	}
	if remaining != 0 {
		t.Fatalf("unallocated metadata bytes = %d", remaining)
	}
	return metadata
}

func TestPromoteStreamsConditionallyThenRemovesStage(t *testing.T) {
	id, err := storage.NewStageID()
	if err != nil {
		t.Fatal(err)
	}
	stageBody := &closeSpy{Reader: bytes.NewReader([]byte("abc"))}
	core := &fakeCore{get: func(_ context.Context, _, object string, _ minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		if !strings.HasSuffix(object, id.Value()) {
			t.Fatalf("stage object = %q", object)
		}
		return stageBody, stageObjectInfo(testNow.Add(time.Hour)), nil, nil
	}}
	var removed []string
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			if strings.Contains(object, "/"+claimDirectory+"/") {
				return successfulClaimPut(t, source, size, opts)
			}
			if object != "root/tenant/final.png" || size != 3 || opts.Header().Get("If-None-Match") != "*" || !opts.DisableMultipart {
				t.Fatalf("put target/size/condition/single = %q/%d/%q/%v", object, size, opts.Header().Get("If-None-Match"), opts.DisableMultipart)
			}
			if _, ok := opts.UserMetadata[stageMarkerKey]; ok || opts.UserMetadata["owner"] != "avatar" {
				t.Fatalf("promoted metadata = %#v", opts.UserMetadata)
			}
			body, err := io.ReadAll(source)
			if string(body) != "abc" || err != nil {
				t.Fatalf("promoted body/error = %q/%v", body, err)
			}
			return minio.UploadInfo{Size: 3, ETag: "final-etag"}, nil
		},
		remove: func(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
			removed = append(removed, object)
			return errors.New("best-effort cleanup failed")
		},
		stat: stageAndClaimStat(t, nil),
	}
	backend := newTestBackend(t, client, core)
	namespace, _ := storage.ParseNamespace("tenant")
	info, err := backend.Promote(context.Background(), namespace, id, testKey(t, "final.png"), storage.PromoteOptions{Mode: storage.CreateOnly})
	if err != nil {
		t.Fatal(err)
	}
	if info.ETag != "final-etag" || info.Metadata["owner"] != "avatar" || len(removed) != 1 || stageBody.closed != 1 {
		t.Fatalf("info/removed/closed = %#v/%v/%d", info, removed, stageBody.closed)
	}
}

func TestPromoteCollisionAndExpiryKeepStage(t *testing.T) {
	id, err := storage.NewStageID()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		expiresAt  time.Time
		putError   error
		want       error
		wantPut    bool
		wantClosed int
	}{
		{
			name:      "collision",
			expiresAt: testNow.Add(time.Hour),
			putError:  minio.ErrorResponse{Code: minio.PreconditionFailed, StatusCode: http.StatusPreconditionFailed},
			want:      storage.ErrAlreadyExists,
			wantPut:   true,
		},
		{name: "expired", expiresAt: testNow, want: storage.ErrExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &closeSpy{Reader: bytes.NewReader([]byte("abc"))}
			core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
				return body, stageObjectInfo(tt.expiresAt), nil, nil
			}}
			putCalled := false
			client := &fakeClient{
				put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
					if strings.Contains(object, "/"+claimDirectory+"/") {
						return successfulClaimPut(t, source, size, opts)
					}
					putCalled = true
					_, _ = io.ReadAll(source)
					return minio.UploadInfo{}, tt.putError
				},
				remove: func(_ context.Context, _ string, _ string, _ minio.RemoveObjectOptions) error {
					t.Fatal("collision or expiry removed stage")
					return nil
				},
				stat: stageAndClaimStat(t, nil),
			}
			backend := newTestBackend(t, client, core)
			namespace, _ := storage.ParseNamespace("tenant")
			_, err := backend.Promote(context.Background(), namespace, id, testKey(t, "final"), storage.PromoteOptions{Mode: storage.CreateOnly})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v", err)
			}
			if putCalled != tt.wantPut || body.closed != 1 {
				t.Fatalf("put/closed = %v/%d", putCalled, body.closed)
			}
		})
	}
}

func TestPromoteSurfacesClaimReleaseFailureAfterFinalCollision(t *testing.T) {
	id, _ := storage.NewStageID()
	namespace, _ := storage.ParseNamespace("tenant")
	core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		return io.NopCloser(strings.NewReader("abc")), stageObjectInfo(testNow.Add(time.Hour)), nil, nil
	}}
	var activeClaim minio.ObjectInfo
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			_, _ = io.ReadAll(source)
			if strings.Contains(object, "/"+claimDirectory+"/") {
				if opts.Header().Get("If-Match") != "" {
					return minio.UploadInfo{}, minio.ErrorResponse{Code: "SlowDown", StatusCode: http.StatusServiceUnavailable}
				}
				upload, err := successfulClaimPut(t, strings.NewReader(opts.UserMetadata[claimTokenKey]), size, opts)
				activeClaim = claimInfoFromPut(upload, opts)
				return upload, err
			}
			return minio.UploadInfo{}, minio.ErrorResponse{Code: minio.PreconditionFailed, StatusCode: http.StatusPreconditionFailed}
		},
		stat: func(_ context.Context, _, object string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
			if strings.Contains(object, "/"+stageDirectory+"/") {
				return stageObjectInfo(testNow.Add(time.Hour)), nil
			}
			return activeClaim, nil
		},
		remove: func(context.Context, string, string, minio.RemoveObjectOptions) error {
			t.Fatal("final collision removed stage")
			return nil
		},
	}
	backend := newTestBackend(t, client, core)
	_, err := backend.Promote(context.Background(), namespace, id, testKey(t, "final"), storage.PromoteOptions{Mode: storage.CreateOnly})
	if !errors.Is(err, storage.ErrTemporary) || errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("error = %v, want claim cleanup failure", err)
	}
}

func TestAbortSurfacesClaimReleaseFailure(t *testing.T) {
	id, _ := storage.NewStageID()
	stageObject := "root/.vv-stage/tenant/" + id.Value()
	claimObject := "root/.vv-stage-claim/tenant/" + id.Value()
	var removed []string
	var activeClaim minio.ObjectInfo
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			if object != claimObject {
				t.Fatalf("claim = %q", object)
			}
			if opts.Header().Get("If-Match") != "" {
				_, _ = io.ReadAll(source)
				return minio.UploadInfo{}, minio.ErrorResponse{Code: "SlowDown", StatusCode: http.StatusServiceUnavailable}
			}
			upload, err := successfulClaimPut(t, source, size, opts)
			activeClaim = claimInfoFromPut(upload, opts)
			return upload, err
		},
		stat: func(context.Context, string, string, minio.StatObjectOptions) (minio.ObjectInfo, error) {
			return activeClaim, nil
		},
		remove: func(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
			removed = append(removed, object)
			if object != stageObject {
				t.Fatalf("removed unexpected object %q", object)
			}
			return nil
		},
	}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	err := store.Abort(context.Background(), id)
	if !errors.Is(err, storage.ErrTemporary) || !slices.Equal(removed, []string{stageObject}) {
		t.Fatalf("error/removed = %v/%v", err, removed)
	}
}

func TestAbortDeleteNotFoundIsIdempotentAndCleansTerminalClaim(t *testing.T) {
	id, _ := storage.NewStageID()
	stageObject := "root/.vv-stage/tenant/" + id.Value()
	claimObject := "root/.vv-stage-claim/tenant/" + id.Value()
	claims := newFakeClaimState(t)
	stageDeletes := 0
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			return claims.put(object, source, size, opts)
		},
		stat: stageAndClaimStat(t, claims),
		remove: func(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
			if object == stageObject {
				stageDeletes++
				return minio.ErrorResponse{Code: minio.NoSuchKey, StatusCode: http.StatusNotFound}
			}
			if object != claimObject {
				t.Fatalf("removed unexpected object %q", object)
			}
			claims.remove(object)
			return nil
		},
	}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	if err := store.Abort(context.Background(), id); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if stageDeletes != 1 {
		t.Fatalf("stage deletes = %d", stageDeletes)
	}
	if _, ok := claims.lookup(claimObject); ok {
		t.Fatal("terminal claim remains after idempotent abort")
	}
}

func TestCleanupExpiredScansPastLiveStages(t *testing.T) {
	liveID, _ := storage.NewStageID()
	expiredID, _ := storage.NewStageID()
	invalidID, _ := storage.NewStageID()
	prefix := "root/.vv-stage/tenant/"
	objects := []minio.ObjectInfo{
		{Key: prefix + liveID.Value()},
		{Key: prefix + expiredID.Value()},
		{Key: prefix + invalidID.Value()},
	}
	var removed []string
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			if !strings.Contains(object, "/"+claimDirectory+"/") {
				t.Fatalf("claim put = %q", object)
			}
			return successfulClaimPut(t, source, size, opts)
		},
		list: func(_ context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
			if bucket != "test-bucket" || opts.Prefix != prefix || !opts.Recursive || opts.MaxKeys != 0 {
				t.Fatalf("list = %q/%#v", bucket, opts)
			}
			result := make(chan minio.ObjectInfo, len(objects))
			for _, object := range objects {
				result <- object
			}
			close(result)
			return result
		},
		stat: func(_ context.Context, _, object string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
			switch object {
			case prefix + liveID.Value():
				return stageObjectInfo(testNow.Add(time.Hour)), nil
			case prefix + expiredID.Value():
				return stageObjectInfo(testNow.Add(-time.Hour)), nil
			default:
				return minio.ObjectInfo{UserMetadata: minio.StringMap{stageExpiryKey: testNow.Add(-time.Hour).Format(time.RFC3339Nano)}}, nil
			}
		},
		remove: func(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
			removed = append(removed, object)
			return nil
		},
	}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	result, err := store.CleanupExpired(context.Background(), storage.CleanupOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 || !result.More || len(removed) != 2 || removed[0] != prefix+expiredID.Value() || !strings.Contains(removed[1], "/"+claimDirectory+"/") {
		t.Fatalf("result/removed = %#v/%v", result, removed)
	}
}

func TestConcurrentPromoteElectsExactlyOneClaim(t *testing.T) {
	id, _ := storage.NewStageID()
	namespace, _ := storage.ParseNamespace("tenant")
	claimAcquired := make(chan struct{})
	allowWinner := make(chan struct{})
	claims := newFakeClaimState(t)
	var acquiredOnce sync.Once
	var mu sync.Mutex
	finalObjects := make([]string, 0, 1)
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			if strings.Contains(object, "/"+claimDirectory+"/") {
				upload, err := claims.put(object, source, size, opts)
				if err == nil && opts.Header().Get("If-None-Match") == "*" {
					acquiredOnce.Do(func() { close(claimAcquired) })
				}
				return upload, err
			}
			if opts.Header().Get("If-None-Match") != "*" {
				t.Fatalf("winner final write is not conditional")
			}
			_, err := io.ReadAll(source)
			if err != nil {
				return minio.UploadInfo{}, err
			}
			mu.Lock()
			finalObjects = append(finalObjects, object)
			mu.Unlock()
			return minio.UploadInfo{Size: 3}, nil
		},
		stat:   stageAndClaimStat(t, claims),
		remove: func(context.Context, string, string, minio.RemoveObjectOptions) error { return nil },
	}
	core := &fakeCore{get: func(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error) {
		<-allowWinner
		return io.NopCloser(strings.NewReader("abc")), stageObjectInfo(testNow.Add(time.Hour)), nil, nil
	}}
	backend := newTestBackend(t, client, core)
	winnerResult := make(chan error, 1)
	go func() {
		_, err := backend.Promote(context.Background(), namespace, id, testKey(t, "first"), storage.PromoteOptions{Mode: storage.CreateOnly})
		winnerResult <- err
	}()
	<-claimAcquired
	_, loserErr := backend.Promote(context.Background(), namespace, id, testKey(t, "second"), storage.PromoteOptions{Mode: storage.CreateOnly})
	if !errors.Is(loserErr, storage.ErrConflict) {
		t.Fatalf("loser error = %v", loserErr)
	}
	close(allowWinner)
	if err := <-winnerResult; err != nil {
		t.Fatalf("winner error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(finalObjects, []string{"root/tenant/first"}) {
		t.Fatalf("final objects = %v", finalObjects)
	}
}

func TestClaimReleaseLostResponseRetryCannotOverwriteSuccessor(t *testing.T) {
	id, _ := storage.NewStageID()
	namespace, _ := storage.ParseNamespace("tenant")
	claimObject := "root/.vv-stage-claim/tenant/" + id.Value()
	claims := newFakeClaimState(t)
	var backend *Backend
	var successorToken string
	injectedABA := false

	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			if object != claimObject {
				t.Fatalf("claim object = %q", object)
			}
			if opts.UserMetadata[claimStateKey] != claimStateRetired || injectedABA {
				return claims.put(object, source, size, opts)
			}

			// A's active -> retired CAS commits, but its response is lost.
			if _, err := claims.put(object, source, size, opts); err != nil {
				return minio.UploadInfo{}, err
			}
			// Before A's SDK retry is observed by the adapter, B acquires the
			// retired claim with a fresh generation and ETag.
			successorToken = claims.acquireSuccessor(object)
			injectedABA = true

			// This is the deterministic outcome of A's transparent stale retry:
			// its old If-Match no longer matches B, so B remains untouched.
			return minio.UploadInfo{}, minio.ErrorResponse{Code: minio.PreconditionFailed, StatusCode: http.StatusPreconditionFailed}
		},
		stat: stageAndClaimStat(t, claims),
		remove: func(context.Context, string, string, minio.RemoveObjectOptions) error {
			t.Fatal("claim release used an unsafe DELETE")
			return nil
		},
	}
	backend = newTestBackend(t, client, &fakeCore{})
	lease, err := backend.acquireClaim(context.Background(), "test claim", namespace, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.releaseClaim(context.Background(), "test claim", lease, claimStateRetired); err != nil {
		t.Fatalf("reconciled release error = %v", err)
	}

	current := claims.object(claimObject)
	if !injectedABA || current.UserMetadata[claimStateKey] != claimStateActive || current.UserMetadata[claimTokenKey] != successorToken || current.ETag == lease.etag {
		t.Fatalf("successor claim = %#v", current)
	}
	_, err = backend.acquireClaim(context.Background(), "test claim", namespace, id)
	if !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("third claimant error = %v", err)
	}
}

func TestClaimAcquireReconcilesCommittedConditionalWrite(t *testing.T) {
	tests := []struct {
		name        string
		seedRetired bool
	}{
		{name: "create"},
		{name: "retired cas", seedRetired: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, _ := storage.NewStageID()
			namespace, _ := storage.ParseNamespace("tenant")
			claimObject := "root/.vv-stage-claim/tenant/" + id.Value()
			claims := newFakeClaimState(t)
			oldETag := ""
			if tt.seedRetired {
				claims.seed(claimObject, "retired-generation", claimStateRetired, testNow)
				oldETag = claims.object(claimObject).ETag
			}
			lostResponse := false
			var committedCondition string
			client := &fakeClient{
				put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
					upload, err := claims.put(object, source, size, opts)
					if err == nil && opts.UserMetadata[claimStateKey] == claimStateActive && !lostResponse {
						lostResponse = true
						committedCondition = opts.Header().Get("If-Match")
						if committedCondition == "" {
							committedCondition = opts.Header().Get("If-None-Match")
						}
						return minio.UploadInfo{}, minio.ErrorResponse{Code: minio.PreconditionFailed, StatusCode: http.StatusPreconditionFailed}
					}
					return upload, err
				},
				stat: stageAndClaimStat(t, claims),
			}
			backend := newTestBackend(t, client, &fakeCore{})
			lease, err := backend.acquireClaim(context.Background(), "test claim", namespace, id)
			if err != nil {
				t.Fatal(err)
			}
			current := claims.object(claimObject)
			if !lostResponse || lease.token == "" || current.UserMetadata[claimTokenKey] != lease.token || current.UserMetadata[claimStateKey] != claimStateActive {
				t.Fatalf("lease/current = %#v/%#v", lease, current)
			}
			if tt.seedRetired {
				if committedCondition != `"`+oldETag+`"` {
					t.Fatalf("CAS condition = %q, want exact old ETag", committedCondition)
				}
			} else if committedCondition != "*" {
				t.Fatalf("create condition = %q", committedCondition)
			}
		})
	}
}

func TestClaimAcquireFailsClosedWithoutETag(t *testing.T) {
	id, _ := storage.NewStageID()
	namespace, _ := storage.ParseNamespace("tenant")
	claimObject := "root/.vv-stage-claim/tenant/" + id.Value()
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, _ int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
			if object != claimObject {
				t.Fatalf("claim object = %q", object)
			}
			_, _ = io.ReadAll(source)
			return minio.UploadInfo{}, minio.ErrorResponse{Code: minio.PreconditionFailed, StatusCode: http.StatusPreconditionFailed}
		},
		stat: func(_ context.Context, _, object string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
			if strings.Contains(object, "/"+stageDirectory+"/") {
				return stageObjectInfo(testNow.Add(time.Hour)), nil
			}
			return minio.ObjectInfo{UserMetadata: minio.StringMap{
				claimMarkerKey: claimMarkerValue,
				claimStateKey:  claimStateActive,
				claimTokenKey:  "active-generation",
				stageExpiryKey: testNow.Add(time.Hour).Format(time.RFC3339Nano),
			}}, nil
		},
	}
	backend := newTestBackend(t, client, &fakeCore{})
	_, err := backend.acquireClaim(context.Background(), "test claim", namespace, id)
	if !errors.Is(err, storage.ErrInternal) || errors.Is(err, storage.ErrConflict) {
		t.Fatalf("error = %v", err)
	}
}

func TestClaimAcquirePostcheckTerminalizesMissingStage(t *testing.T) {
	id, _ := storage.NewStageID()
	namespace, _ := storage.ParseNamespace("tenant")
	claimObject := "root/.vv-stage-claim/tenant/" + id.Value()
	claims := newFakeClaimState(t)
	stageStats := 0
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			return claims.put(object, source, size, opts)
		},
		stat: func(ctx context.Context, bucket, object string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
			if strings.Contains(object, "/"+stageDirectory+"/") {
				stageStats++
				if stageStats == 1 {
					return stageObjectInfo(testNow.Add(time.Hour)), nil
				}
				return minio.ObjectInfo{}, minio.ErrorResponse{Code: minio.NoSuchKey, StatusCode: http.StatusNotFound}
			}
			return claims.stat(ctx, bucket, object, opts)
		},
		remove: func(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
			claims.remove(object)
			return nil
		},
	}
	backend := newTestBackend(t, client, &fakeCore{})
	_, err := backend.acquireClaim(context.Background(), "test claim", namespace, id)
	if !errors.Is(err, storage.ErrNotFound) || stageStats != 2 {
		t.Fatalf("error/stage stats = %v/%d", err, stageStats)
	}
	if _, ok := claims.lookup(claimObject); ok {
		t.Fatal("terminalized claim remains after missing-stage postcheck")
	}
}

func TestCleanupExpiredDeletesTerminalizedOrphanClaim(t *testing.T) {
	id, _ := storage.NewStageID()
	claimPrefix := "root/.vv-stage-claim/tenant/"
	claimObject := claimPrefix + id.Value()
	claims := newFakeClaimState(t)
	claims.seed(claimObject, "orphan-generation", claimStateActive, testNow.Add(-time.Minute))
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			return claims.put(object, source, size, opts)
		},
		list: func(_ context.Context, _ string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
			result := make(chan minio.ObjectInfo, 1)
			if opts.Prefix == claimPrefix {
				result <- minio.ObjectInfo{Key: claimObject}
			}
			close(result)
			return result
		},
		stat: claims.stat,
		remove: func(_ context.Context, _, object string, _ minio.RemoveObjectOptions) error {
			claims.remove(object)
			return nil
		},
	}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	result, err := store.CleanupExpired(context.Background(), storage.CleanupOptions{Limit: 1})
	if err != nil || result.Removed != 1 || !result.More {
		t.Fatalf("result/error = %#v/%v", result, err)
	}
	if _, ok := claims.lookup(claimObject); ok {
		t.Fatal("terminal orphan claim remains visible")
	}
}

func TestCleanupExpiredPropagatesClaimReadFailuresAndSkipsMalformed(t *testing.T) {
	id, _ := storage.NewStageID()
	claimPrefix := "root/.vv-stage-claim/tenant/"
	claimObject := claimPrefix + id.Value()
	tests := []struct {
		name     string
		info     minio.ObjectInfo
		statErr  error
		want     error
		wantOkay bool
	}{
		{
			name:    "temporary",
			statErr: minio.ErrorResponse{Code: "SlowDown", StatusCode: http.StatusServiceUnavailable},
			want:    storage.ErrTemporary,
		},
		{
			name:    "forbidden",
			statErr: minio.ErrorResponse{Code: minio.AccessDenied, StatusCode: http.StatusForbidden},
			want:    storage.ErrForbidden,
		},
		{
			name:     "malformed private object",
			info:     minio.ObjectInfo{ETag: "foreign-etag", UserMetadata: minio.StringMap{"unowned": "1"}},
			wantOkay: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeClient{
				list: func(_ context.Context, _ string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
					objects := make(chan minio.ObjectInfo, 1)
					if opts.Prefix == claimPrefix {
						objects <- minio.ObjectInfo{Key: claimObject}
					}
					close(objects)
					return objects
				},
				stat: func(_ context.Context, _, object string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
					if object != claimObject {
						t.Fatalf("unexpected StatObject target %q", object)
					}
					return tt.info, tt.statErr
				},
				put: func(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error) {
					t.Fatal("claim read failure triggered a write")
					return minio.UploadInfo{}, nil
				},
				remove: func(context.Context, string, string, minio.RemoveObjectOptions) error {
					t.Fatal("claim read failure triggered a delete")
					return nil
				},
			}
			store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
			result, err := store.CleanupExpired(context.Background(), storage.CleanupOptions{Limit: 1})
			if tt.wantOkay {
				if err != nil || result != (storage.CleanupResult{}) {
					t.Fatalf("result/error = %#v/%v", result, err)
				}
				return
			}
			if !errors.Is(err, tt.want) || result != (storage.CleanupResult{}) {
				t.Fatalf("result/error = %#v/%v", result, err)
			}
		})
	}
}

func TestCleanupExpiredClaimLostResponseCannotDeleteSuccessor(t *testing.T) {
	id, _ := storage.NewStageID()
	claimPrefix := "root/.vv-stage-claim/tenant/"
	claimObject := claimPrefix + id.Value()
	claims := newFakeClaimState(t)
	claims.seed(claimObject, "expired-generation", claimStateActive, testNow.Add(-time.Minute))
	var successorToken string
	injectedABA := false
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			if opts.UserMetadata[claimStateKey] != claimStateTerminal || injectedABA {
				return claims.put(object, source, size, opts)
			}
			if _, err := claims.put(object, source, size, opts); err != nil {
				return minio.UploadInfo{}, err
			}
			successorToken = claims.acquireSuccessor(object)
			injectedABA = true
			return minio.UploadInfo{}, minio.ErrorResponse{Code: minio.PreconditionFailed, StatusCode: http.StatusPreconditionFailed}
		},
		list: func(_ context.Context, _ string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
			objects := make(chan minio.ObjectInfo, 1)
			if opts.Prefix == claimPrefix {
				objects <- minio.ObjectInfo{Key: claimObject}
			}
			close(objects)
			return objects
		},
		stat: claims.stat,
		remove: func(context.Context, string, string, minio.RemoveObjectOptions) error {
			t.Fatal("orphan claim cleanup used an unsafe DELETE")
			return nil
		},
	}
	backend := newTestBackend(t, client, &fakeCore{})
	store := newTestStore(t, backend)
	result, err := store.CleanupExpired(context.Background(), storage.CleanupOptions{Limit: 1})
	if err != nil || result.Removed != 0 || result.More {
		t.Fatalf("result/error = %#v/%v", result, err)
	}
	current := claims.object(claimObject)
	if !injectedABA || current.UserMetadata[claimStateKey] != claimStateActive || current.UserMetadata[claimTokenKey] != successorToken {
		t.Fatalf("successor claim = %#v", current)
	}
	namespace, _ := storage.ParseNamespace("tenant")
	_, err = backend.acquireClaim(context.Background(), "test claim", namespace, id)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("third claimant error = %v", err)
	}
}

func TestCleanupExpiredSharesOneRemovalBudgetAcrossStagesAndOrphanClaims(t *testing.T) {
	stageID, _ := storage.NewStageID()
	orphanID, _ := storage.NewStageID()
	stagePrefix := "root/.vv-stage/tenant/"
	claimPrefix := "root/.vv-stage-claim/tenant/"
	stageObject := stagePrefix + stageID.Value()
	orphanClaim := claimPrefix + orphanID.Value()
	claims := newFakeClaimState(t)
	claims.seed(orphanClaim, "orphan-generation", claimStateActive, testNow.Add(-time.Minute))
	client := &fakeClient{
		put: func(_ context.Context, _, object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
			return claims.put(object, source, size, opts)
		},
		list: func(_ context.Context, _ string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
			result := make(chan minio.ObjectInfo, 1)
			switch opts.Prefix {
			case stagePrefix:
				result <- minio.ObjectInfo{Key: stageObject}
			case claimPrefix:
				result <- minio.ObjectInfo{Key: orphanClaim}
			}
			close(result)
			return result
		},
		stat: func(_ context.Context, _, object string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
			if object == stageObject {
				return stageObjectInfo(testNow.Add(-time.Minute)), nil
			}
			if strings.HasPrefix(object, stagePrefix) {
				return minio.ObjectInfo{}, minio.ErrorResponse{Code: minio.NoSuchKey, StatusCode: http.StatusNotFound}
			}
			if strings.HasPrefix(object, claimPrefix) {
				return claims.stat(context.Background(), "", object, minio.StatObjectOptions{})
			}
			return minio.ObjectInfo{}, errors.New("unexpected stat target")
		},
		remove: func(context.Context, string, string, minio.RemoveObjectOptions) error { return nil },
	}
	store := newTestStore(t, newTestBackend(t, client, &fakeCore{}))
	result, err := store.CleanupExpired(context.Background(), storage.CleanupOptions{Limit: 2})
	if err != nil || result.Removed != 2 || !result.More {
		t.Fatalf("combined cleanup result/error = %#v/%v", result, err)
	}
}

func TestTemporaryURLPolicyAndResult(t *testing.T) {
	called := 0
	client := &fakeClient{presigned: func(_ context.Context, bucket, object string, ttl time.Duration, values url.Values) (*url.URL, error) {
		called++
		if bucket != "test-bucket" || object != "root/tenant/image" || ttl != 10*time.Minute || values != nil {
			t.Fatalf("presign = %q/%q/%s/%v", bucket, object, ttl, values)
		}
		return url.Parse("https://minio.example/test-bucket/object?signature=secret")
	}}
	backend := newTestBackend(t, client, &fakeCore{}, func(config *Config) { config.MaxLinkTTL = 10 * time.Minute })
	store := newTestStore(t, backend)
	link, err := store.TemporaryURL(context.Background(), testKey(t, "image"), storage.TemporaryURLOptions{ExpiresIn: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if link.URL() != "https://minio.example/test-bucket/object?signature=secret" || link.ExpiresAt() != testNow.Add(10*time.Minute) || link.String() != "[temporary storage URL]" {
		t.Fatalf("link = %#v", link)
	}
	_, err = store.TemporaryURL(context.Background(), testKey(t, "image"), storage.TemporaryURLOptions{ExpiresIn: 11 * time.Minute})
	if !errors.Is(err, storage.ErrInvalid) || called != 1 {
		t.Fatalf("over-limit error/calls = %v/%d", err, called)
	}
	namespace, _ := storage.ParseNamespace("tenant")
	_, err = backend.TemporaryURL(context.Background(), namespace, testKey(t, "image"), storage.TemporaryURLOptions{ExpiresIn: 1500 * time.Millisecond})
	if !errors.Is(err, storage.ErrInvalid) || called != 1 {
		t.Fatalf("fractional direct TTL error/calls = %v/%d", err, called)
	}
}

func TestCapabilities(t *testing.T) {
	capabilities := newTestBackend(t, &fakeClient{}, &fakeCore{}).Capabilities()
	if !capabilities.CreateOnly || !capabilities.Replace || !capabilities.Staging || !capabilities.TemporaryURL {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func stageObjectInfo(expiresAt time.Time) minio.ObjectInfo {
	return minio.ObjectInfo{
		Size:         3,
		ContentType:  "image/png",
		LastModified: testNow,
		ETag:         "stage-etag",
		UserMetadata: minio.StringMap{
			"Owner":        "avatar",
			stageMarkerKey: stageMarkerValue,
			stageExpiryKey: expiresAt.Format(time.RFC3339Nano),
		},
	}
}

func successfulClaimPut(t *testing.T, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	t.Helper()
	body, err := io.ReadAll(source)
	if err != nil {
		return minio.UploadInfo{}, err
	}
	token := opts.UserMetadata[claimTokenKey]
	if token == "" || string(body) != token || size != int64(len(token)) {
		t.Fatalf("claim token/body/size = %q/%q/%d", token, body, size)
	}
	if opts.UserMetadata[claimMarkerKey] != claimMarkerValue ||
		(opts.UserMetadata[claimStateKey] != claimStateActive && opts.UserMetadata[claimStateKey] != claimStateRetired && opts.UserMetadata[claimStateKey] != claimStateTerminal) ||
		!opts.DisableMultipart || !opts.SendContentMd5 {
		t.Fatalf("claim metadata/options = %#v/%#v", opts.UserMetadata, opts)
	}
	ifNone, ifMatch := opts.Header().Get("If-None-Match"), opts.Header().Get("If-Match")
	if (ifNone == "*") == (ifMatch != "") {
		t.Fatalf("claim conditions If-None-Match/If-Match = %q/%q", ifNone, ifMatch)
	}
	return minio.UploadInfo{Size: size, ETag: "etag-" + token}, nil
}

func claimInfoFromPut(upload minio.UploadInfo, opts minio.PutObjectOptions) minio.ObjectInfo {
	metadata := make(minio.StringMap, len(opts.UserMetadata))
	for key, value := range opts.UserMetadata {
		metadata[key] = value
	}
	return minio.ObjectInfo{
		Size:         upload.Size,
		ETag:         upload.ETag,
		ContentType:  opts.ContentType,
		LastModified: testNow,
		UserMetadata: metadata,
	}
}

type fakeClaimState struct {
	t        *testing.T
	mu       sync.Mutex
	nextETag int
	objects  map[string]minio.ObjectInfo
}

func newFakeClaimState(t *testing.T) *fakeClaimState {
	t.Helper()
	return &fakeClaimState{t: t, objects: make(map[string]minio.ObjectInfo)}
}

func (s *fakeClaimState) put(object string, source io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	s.t.Helper()
	base, err := successfulClaimPut(s.t, source, size, opts)
	if err != nil {
		return minio.UploadInfo{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.objects[object]
	if opts.Header().Get("If-None-Match") == "*" {
		if exists {
			return minio.UploadInfo{}, minio.ErrorResponse{Code: minio.PreconditionFailed, StatusCode: http.StatusPreconditionFailed}
		}
	} else {
		match := strings.Trim(opts.Header().Get("If-Match"), `"`)
		if !exists {
			return minio.UploadInfo{}, minio.ErrorResponse{Code: minio.NoSuchKey, StatusCode: http.StatusNotFound}
		}
		if match != current.ETag {
			return minio.UploadInfo{}, minio.ErrorResponse{Code: minio.PreconditionFailed, StatusCode: http.StatusPreconditionFailed}
		}
	}

	s.nextETag++
	base.ETag = fmt.Sprintf("claim-etag-%d", s.nextETag)
	s.objects[object] = claimInfoFromPut(base, opts)
	return base, nil
}

func (s *fakeClaimState) stat(_ context.Context, _, object string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.objects[object]
	if !ok {
		return minio.ObjectInfo{}, minio.ErrorResponse{Code: minio.NoSuchKey, StatusCode: http.StatusNotFound}
	}
	return cloneObjectInfo(info), nil
}

func (s *fakeClaimState) seed(object, token, state string, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextETag++
	s.objects[object] = minio.ObjectInfo{
		Size:         int64(len(token)),
		ETag:         fmt.Sprintf("claim-etag-%d", s.nextETag),
		ContentType:  "application/octet-stream",
		LastModified: testNow,
		UserMetadata: minio.StringMap{
			claimMarkerKey: claimMarkerValue,
			claimStateKey:  state,
			claimTokenKey:  token,
			stageExpiryKey: expiresAt.Format(time.RFC3339Nano),
		},
	}
}

func (s *fakeClaimState) acquireSuccessor(object string) string {
	s.t.Helper()
	retired := s.object(object)
	id, err := storage.NewStageID()
	if err != nil {
		s.t.Fatal(err)
	}
	token := id.Value()
	opts := minio.PutObjectOptions{
		ContentType:      "application/octet-stream",
		DisableMultipart: true,
		SendContentMd5:   true,
		UserMetadata: map[string]string{
			claimMarkerKey: claimMarkerValue,
			claimStateKey:  claimStateActive,
			claimTokenKey:  token,
			stageExpiryKey: testNow.Add(storage.MaxStageTTL).Format(time.RFC3339Nano),
		},
	}
	opts.SetMatchETag(retired.ETag)
	if _, err := s.put(object, strings.NewReader(token), int64(len(token)), opts); err != nil {
		s.t.Fatal(err)
	}
	return token
}

func (s *fakeClaimState) object(object string) minio.ObjectInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneObjectInfo(s.objects[object])
}

func (s *fakeClaimState) lookup(object string) (minio.ObjectInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.objects[object]
	return cloneObjectInfo(info), ok
}

func (s *fakeClaimState) remove(object string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, object)
}

func stageAndClaimStat(t *testing.T, claims *fakeClaimState) func(context.Context, string, string, minio.StatObjectOptions) (minio.ObjectInfo, error) {
	t.Helper()
	return func(ctx context.Context, bucket, object string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
		if strings.Contains(object, "/"+stageDirectory+"/") {
			return stageObjectInfo(testNow.Add(time.Hour)), nil
		}
		if claims != nil {
			return claims.stat(ctx, bucket, object, opts)
		}
		t.Fatalf("unexpected StatObject target %q", object)
		return minio.ObjectInfo{}, nil
	}
}

func cloneObjectInfo(info minio.ObjectInfo) minio.ObjectInfo {
	metadata := make(minio.StringMap, len(info.UserMetadata))
	for key, value := range info.UserMetadata {
		metadata[key] = value
	}
	info.UserMetadata = metadata
	return info
}

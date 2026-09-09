package vvotel_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/storage"
	"go.opentelemetry.io/otel/codes"
)

type fakeStorageStore struct {
	lastCtx             context.Context
	calls               int
	operationCalls      map[string]int
	panicValue          any
	goexit              bool
	panicNil            bool
	err                 error
	putKey              storage.Key
	putSource           io.Reader
	putOptions          storage.PutOptions
	openKey             storage.Key
	openOptions         storage.ReadOptions
	headKey             storage.Key
	deleteKey           storage.Key
	deleteOptions       storage.DeleteOptions
	stageSource         io.Reader
	stageOptions        storage.StageOptions
	promoteStageID      storage.StageID
	promoteKey          storage.Key
	promoteOptions      storage.PromoteOptions
	abortStageID        storage.StageID
	cleanupOptions      storage.CleanupOptions
	temporaryURLKey     storage.Key
	temporaryURLOptions storage.TemporaryURLOptions
	exactResults        bool
	putResult           storage.Info
	openReader          io.ReadCloser
	openInfo            storage.Info
	headResult          storage.Info
	stageResult         storage.Staged
	promoteResult       storage.Info
	cleanupResult       storage.CleanupResult
	temporaryURLResult  storage.Link
	capabilities        storage.Capabilities
	capabilitiesCalls   int
}

func (f *fakeStorageStore) call(ctx context.Context, operation string) {
	f.lastCtx = ctx
	f.calls++
	if f.operationCalls == nil {
		f.operationCalls = make(map[string]int)
	}
	f.operationCalls[operation]++
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	if f.goexit {
		runtime.Goexit()
	}
	if f.panicNil {
		panic(nil)
	}
}

func (f *fakeStorageStore) Put(ctx context.Context, key storage.Key, source io.Reader, options storage.PutOptions) (storage.Info, error) {
	f.putKey, f.putSource, f.putOptions = key, source, options
	f.call(ctx, vvotel.OpStoragePut)
	if f.exactResults {
		return f.putResult, f.err
	}
	if f.err != nil {
		return storage.Info{}, f.err
	}
	return storage.Info{Size: 10}, nil
}
func (f *fakeStorageStore) Open(ctx context.Context, key storage.Key, options storage.ReadOptions) (io.ReadCloser, storage.Info, error) {
	f.openKey, f.openOptions = key, options
	f.call(ctx, vvotel.OpStorageOpen)
	if f.exactResults {
		return f.openReader, f.openInfo, f.err
	}
	if f.err != nil {
		return nil, storage.Info{}, f.err
	}
	return io.NopCloser(strings.NewReader("hello")), storage.Info{Size: 5}, nil
}
func (f *fakeStorageStore) Head(ctx context.Context, key storage.Key) (storage.Info, error) {
	f.headKey = key
	f.call(ctx, vvotel.OpStorageHead)
	if f.exactResults {
		return f.headResult, f.err
	}
	if f.err != nil {
		return storage.Info{}, f.err
	}
	return storage.Info{Size: 10}, nil
}
func (f *fakeStorageStore) Delete(ctx context.Context, key storage.Key, options storage.DeleteOptions) error {
	f.deleteKey, f.deleteOptions = key, options
	f.call(ctx, vvotel.OpStorageDelete)
	return f.err
}
func (f *fakeStorageStore) Stage(ctx context.Context, source io.Reader, options storage.StageOptions) (storage.Staged, error) {
	f.stageSource, f.stageOptions = source, options
	f.call(ctx, vvotel.OpStorageStage)
	if f.exactResults {
		return f.stageResult, f.err
	}
	if f.err != nil {
		return storage.Staged{}, f.err
	}
	return storage.Staged{Info: storage.Info{Size: 10}}, nil
}
func (f *fakeStorageStore) Promote(ctx context.Context, stageID storage.StageID, key storage.Key, options storage.PromoteOptions) (storage.Info, error) {
	f.promoteStageID, f.promoteKey, f.promoteOptions = stageID, key, options
	f.call(ctx, vvotel.OpStoragePromote)
	if f.exactResults {
		return f.promoteResult, f.err
	}
	if f.err != nil {
		return storage.Info{}, f.err
	}
	return storage.Info{Size: 10}, nil
}
func (f *fakeStorageStore) Abort(ctx context.Context, stageID storage.StageID) error {
	f.abortStageID = stageID
	f.call(ctx, vvotel.OpStorageAbort)
	return f.err
}
func (f *fakeStorageStore) CleanupExpired(ctx context.Context, options storage.CleanupOptions) (storage.CleanupResult, error) {
	f.cleanupOptions = options
	f.call(ctx, vvotel.OpStorageCleanupExpired)
	if f.exactResults {
		return f.cleanupResult, f.err
	}
	if f.err != nil {
		return storage.CleanupResult{}, f.err
	}
	return storage.CleanupResult{Removed: 1}, nil
}
func (f *fakeStorageStore) TemporaryURL(ctx context.Context, key storage.Key, options storage.TemporaryURLOptions) (storage.Link, error) {
	f.temporaryURLKey, f.temporaryURLOptions = key, options
	f.call(ctx, vvotel.OpStorageTemporaryUrl)
	if f.exactResults {
		return f.temporaryURLResult, f.err
	}
	if f.err != nil {
		return storage.Link{}, f.err
	}
	link, _ := storage.NewLink("https://example.com/file", time.Now().Add(time.Hour))
	return link, nil
}
func (f *fakeStorageStore) Capabilities() storage.Capabilities {
	f.capabilitiesCalls++
	if f.exactResults {
		return f.capabilities
	}
	return storage.Capabilities{Staging: true}
}

type fakeStorageReadCloser struct {
	reader     *strings.Reader
	closeError error
	closeCalls int
}

func (r *fakeStorageReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *fakeStorageReadCloser) Close() error {
	r.closeCalls++
	return r.closeError
}

type storageOpenResult struct {
	reader io.ReadCloser
	info   storage.Info
}

type storageEffectFixture struct {
	putKey              storage.Key
	putSource           *strings.Reader
	putPayload          string
	putOptions          storage.PutOptions
	openKey             storage.Key
	openOptions         storage.ReadOptions
	headKey             storage.Key
	deleteKey           storage.Key
	deleteOptions       storage.DeleteOptions
	stageSource         *strings.Reader
	stagePayload        string
	stageOptions        storage.StageOptions
	promoteStageID      storage.StageID
	promoteKey          storage.Key
	promoteOptions      storage.PromoteOptions
	abortStageID        storage.StageID
	cleanupOptions      storage.CleanupOptions
	temporaryURLKey     storage.Key
	temporaryURLOptions storage.TemporaryURLOptions
}

func mustStorageKey(t *testing.T, raw string) storage.Key {
	t.Helper()
	key, err := storage.ParseKey(raw)
	if err != nil {
		t.Fatalf("ParseKey(%q): %v", raw, err)
	}
	return key
}

func mustStorageStageID(t *testing.T, raw string) storage.StageID {
	t.Helper()
	stageID, err := storage.ParseStageID(raw)
	if err != nil {
		t.Fatalf("ParseStageID(%q): %v", raw, err)
	}
	return stageID
}

func newStorageEffectFixture(t *testing.T) *storageEffectFixture {
	t.Helper()
	putPayload := "put-payload-exact"
	stagePayload := "stage-payload-exact"
	return &storageEffectFixture{
		putKey:     mustStorageKey(t, "put/exact-object.bin"),
		putSource:  strings.NewReader(putPayload),
		putPayload: putPayload,
		putOptions: storage.PutOptions{
			Mode:        storage.Replace,
			Size:        storage.ExactSize(931),
			ContentType: "application/x-put-exact",
			Metadata:    storage.Metadata{"put-key": "put-value", "put-second": "put-second-value"},
			IfMatch:     storage.IfMatch("put-etag-input"),
		},
		openKey:     mustStorageKey(t, "open/exact-object.bin"),
		openOptions: storage.ReadOptions{Offset: 37, Length: storage.ExactSize(43)},
		headKey:     mustStorageKey(t, "head/exact-object.bin"),
		deleteKey:   mustStorageKey(t, "delete/exact-object.bin"),
		deleteOptions: storage.DeleteOptions{
			IfMatch: storage.IfMatch("delete-etag-input"),
		},
		stageSource:  strings.NewReader(stagePayload),
		stagePayload: stagePayload,
		stageOptions: storage.StageOptions{
			Size:        storage.ExactSize(947),
			ContentType: "application/x-stage-exact",
			Metadata:    storage.Metadata{"stage-key": "stage-value", "stage-second": "stage-second-value"},
			ExpiresIn:   37 * time.Minute,
		},
		promoteStageID: mustStorageStageID(t, strings.Repeat("A", 32)),
		promoteKey:     mustStorageKey(t, "promote/exact-object.bin"),
		promoteOptions: storage.PromoteOptions{
			Mode:    storage.CreateOnly,
			IfMatch: storage.IfMatch("promote-etag-input"),
		},
		abortStageID:        mustStorageStageID(t, strings.Repeat("C", 32)),
		cleanupOptions:      storage.CleanupOptions{Limit: 613},
		temporaryURLKey:     mustStorageKey(t, "temporary/exact-object.bin"),
		temporaryURLOptions: storage.TemporaryURLOptions{ExpiresIn: 53 * time.Minute},
	}
}

func exactStorageInfo(size int64, marker string, modifiedAt time.Time) storage.Info {
	return storage.Info{
		Size:              size,
		ContentType:       "application/x-" + marker,
		Metadata:          storage.Metadata{"result": marker, "marker": marker + "-metadata"},
		ModifiedAt:        modifiedAt,
		ETag:              marker + "-etag",
		Version:           marker + "-version",
		MetadataTruncated: true,
	}
}

func newExactFakeStorageStore(t *testing.T) *fakeStorageStore {
	t.Helper()
	baseTime := time.Date(2037, time.June, 17, 11, 23, 41, 73000000, time.UTC)
	stageID := mustStorageStageID(t, strings.Repeat("B", 32))
	link, err := storage.NewLink("https://storage.example.test/exact/object?token=opaque", baseTime.Add(79*time.Minute))
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	return &fakeStorageStore{
		exactResults: true,
		putResult:    exactStorageInfo(1009, "put-result", baseTime.Add(time.Second)),
		openReader: &fakeStorageReadCloser{
			reader:     strings.NewReader("open-result-payload"),
			closeError: &fakeEffectError{operation: "open reader close"},
		},
		openInfo:           exactStorageInfo(1013, "open-result", baseTime.Add(2*time.Second)),
		headResult:         exactStorageInfo(1019, "head-result", baseTime.Add(3*time.Second)),
		stageResult:        storage.Staged{ID: stageID, Info: exactStorageInfo(1021, "stage-result", baseTime.Add(4*time.Second)), ExpiresAt: baseTime.Add(83 * time.Minute)},
		promoteResult:      exactStorageInfo(1031, "promote-result", baseTime.Add(5*time.Second)),
		cleanupResult:      storage.CleanupResult{Removed: 257, More: true},
		temporaryURLResult: link,
	}
}

func (f *storageEffectFixture) operations() []string {
	return []string{
		vvotel.OpStoragePut,
		vvotel.OpStorageOpen,
		vvotel.OpStorageHead,
		vvotel.OpStorageDelete,
		vvotel.OpStorageStage,
		vvotel.OpStoragePromote,
		vvotel.OpStorageAbort,
		vvotel.OpStorageCleanupExpired,
		vvotel.OpStorageTemporaryUrl,
	}
}

func (f *storageEffectFixture) execute(ctx context.Context, operation string, store storage.Store) (any, error) {
	switch operation {
	case vvotel.OpStoragePut:
		result, err := store.Put(ctx, f.putKey, f.putSource, f.putOptions)
		return result, err
	case vvotel.OpStorageOpen:
		reader, info, err := store.Open(ctx, f.openKey, f.openOptions)
		return storageOpenResult{reader: reader, info: info}, err
	case vvotel.OpStorageHead:
		result, err := store.Head(ctx, f.headKey)
		return result, err
	case vvotel.OpStorageDelete:
		return struct{}{}, store.Delete(ctx, f.deleteKey, f.deleteOptions)
	case vvotel.OpStorageStage:
		result, err := store.Stage(ctx, f.stageSource, f.stageOptions)
		return result, err
	case vvotel.OpStoragePromote:
		result, err := store.Promote(ctx, f.promoteStageID, f.promoteKey, f.promoteOptions)
		return result, err
	case vvotel.OpStorageAbort:
		return struct{}{}, store.Abort(ctx, f.abortStageID)
	case vvotel.OpStorageCleanupExpired:
		result, err := store.CleanupExpired(ctx, f.cleanupOptions)
		return result, err
	case vvotel.OpStorageTemporaryUrl:
		result, err := store.TemporaryURL(ctx, f.temporaryURLKey, f.temporaryURLOptions)
		return result, err
	default:
		panic("unknown storage operation: " + operation)
	}
}

func storageExpectedResult(raw *fakeStorageStore, operation string) any {
	switch operation {
	case vvotel.OpStoragePut:
		return raw.putResult
	case vvotel.OpStorageOpen:
		return storageOpenResult{reader: raw.openReader, info: raw.openInfo}
	case vvotel.OpStorageHead:
		return raw.headResult
	case vvotel.OpStorageDelete, vvotel.OpStorageAbort:
		return struct{}{}
	case vvotel.OpStorageStage:
		return raw.stageResult
	case vvotel.OpStoragePromote:
		return raw.promoteResult
	case vvotel.OpStorageCleanupExpired:
		return raw.cleanupResult
	case vvotel.OpStorageTemporaryUrl:
		return raw.temporaryURLResult
	default:
		panic("unknown storage operation: " + operation)
	}
}

func assertStorageMetadataIdentity(t *testing.T, got, want storage.Metadata) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata changed: got=%v want=%v", got, want)
	}
	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(want).Pointer() {
		t.Fatal("metadata map identity changed")
	}
}

func (f *storageEffectFixture) assertCaptured(t *testing.T, raw *fakeStorageStore, operation string) {
	t.Helper()
	switch operation {
	case vvotel.OpStoragePut:
		if raw.putKey != f.putKey || raw.putSource != f.putSource || !reflect.DeepEqual(raw.putOptions, f.putOptions) {
			t.Fatalf("Put inputs changed: key=%v source=%T options=%+v", raw.putKey, raw.putSource, raw.putOptions)
		}
		if raw.putOptions.Size != f.putOptions.Size || raw.putOptions.IfMatch != f.putOptions.IfMatch {
			t.Fatal("Put optional pointer identity changed")
		}
		assertStorageMetadataIdentity(t, raw.putOptions.Metadata, f.putOptions.Metadata)
		if f.putSource.Len() != len(f.putPayload) {
			t.Fatalf("Put source was consumed: remaining=%d want=%d", f.putSource.Len(), len(f.putPayload))
		}
	case vvotel.OpStorageOpen:
		if raw.openKey != f.openKey || !reflect.DeepEqual(raw.openOptions, f.openOptions) {
			t.Fatalf("Open inputs changed: key=%v options=%+v", raw.openKey, raw.openOptions)
		}
		if raw.openOptions.Length != f.openOptions.Length {
			t.Fatal("Open length pointer identity changed")
		}
	case vvotel.OpStorageHead:
		if raw.headKey != f.headKey {
			t.Fatal("Head key changed")
		}
	case vvotel.OpStorageDelete:
		if raw.deleteKey != f.deleteKey || !reflect.DeepEqual(raw.deleteOptions, f.deleteOptions) {
			t.Fatalf("Delete inputs changed: key=%v options=%+v", raw.deleteKey, raw.deleteOptions)
		}
		if raw.deleteOptions.IfMatch != f.deleteOptions.IfMatch {
			t.Fatal("Delete IfMatch pointer identity changed")
		}
	case vvotel.OpStorageStage:
		if raw.stageSource != f.stageSource || !reflect.DeepEqual(raw.stageOptions, f.stageOptions) {
			t.Fatalf("Stage inputs changed: source=%T options=%+v", raw.stageSource, raw.stageOptions)
		}
		if raw.stageOptions.Size != f.stageOptions.Size {
			t.Fatal("Stage size pointer identity changed")
		}
		assertStorageMetadataIdentity(t, raw.stageOptions.Metadata, f.stageOptions.Metadata)
		if f.stageSource.Len() != len(f.stagePayload) {
			t.Fatalf("Stage source was consumed: remaining=%d want=%d", f.stageSource.Len(), len(f.stagePayload))
		}
	case vvotel.OpStoragePromote:
		if raw.promoteStageID != f.promoteStageID || raw.promoteKey != f.promoteKey || !reflect.DeepEqual(raw.promoteOptions, f.promoteOptions) {
			t.Fatalf("Promote inputs changed: stage=%v key=%v options=%+v", raw.promoteStageID, raw.promoteKey, raw.promoteOptions)
		}
		if raw.promoteOptions.IfMatch != f.promoteOptions.IfMatch {
			t.Fatal("Promote IfMatch pointer identity changed")
		}
	case vvotel.OpStorageAbort:
		if raw.abortStageID != f.abortStageID {
			t.Fatal("Abort stage ID changed")
		}
	case vvotel.OpStorageCleanupExpired:
		if raw.cleanupOptions != f.cleanupOptions {
			t.Fatalf("CleanupExpired options changed: got=%+v want=%+v", raw.cleanupOptions, f.cleanupOptions)
		}
	case vvotel.OpStorageTemporaryUrl:
		if raw.temporaryURLKey != f.temporaryURLKey || raw.temporaryURLOptions != f.temporaryURLOptions {
			t.Fatalf("TemporaryURL inputs changed: key=%v options=%+v", raw.temporaryURLKey, raw.temporaryURLOptions)
		}
	default:
		t.Fatalf("unknown storage operation %q", operation)
	}
}

func assertStorageResultPreserved(t *testing.T, got, want any, operation string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s result changed: got=%#v want=%#v", operation, got, want)
	}
	switch operation {
	case vvotel.OpStoragePut, vvotel.OpStorageHead, vvotel.OpStoragePromote:
		assertStorageMetadataIdentity(t, got.(storage.Info).Metadata, want.(storage.Info).Metadata)
	case vvotel.OpStorageOpen:
		gotResult := got.(storageOpenResult)
		wantResult := want.(storageOpenResult)
		if gotResult.reader != wantResult.reader {
			t.Fatal("Open reader identity changed")
		}
		assertStorageMetadataIdentity(t, gotResult.info.Metadata, wantResult.info.Metadata)
	case vvotel.OpStorageStage:
		assertStorageMetadataIdentity(t, got.(storage.Staged).Info.Metadata, want.(storage.Staged).Info.Metadata)
	}
}

func TestStorage_AllNineOperationsTotality(t *testing.T) {
	terminals := []struct {
		name    string
		withErr bool
	}{
		{name: "success"},
		{name: "error", withErr: true},
	}

	for _, operation := range newStorageEffectFixture(t).operations() {
		for _, terminal := range terminals {
			t.Run(operation+"/"+terminal.name, func(t *testing.T) {
				fixture := newStorageEffectFixture(t)
				tp := newTestTracerProvider()
				tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, ResourceName: "assets"})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				raw := newExactFakeStorageStore(t)
				var wantErr error
				if terminal.withErr {
					wantErr = &fakeEffectError{operation: operation}
					raw.err = wantErr
				}

				result, callErr := fixture.execute(context.Background(), operation, vvotel.Store(tel)(raw))
				if callErr != wantErr {
					t.Fatalf("business error identity changed: got=%v want=%v", callErr, wantErr)
				}
				assertStorageResultPreserved(t, result, storageExpectedResult(raw, operation), operation)
				fixture.assertCaptured(t, raw, operation)
				if raw.calls != 1 || raw.operationCalls[operation] != 1 || len(raw.operationCalls) != 1 {
					t.Fatalf("business calls=%d by_operation=%v, want only %s once", raw.calls, raw.operationCalls, operation)
				}
				if raw.lastCtx.Value(spanKey{}) == nil {
					t.Fatal("derived context not passed to the business operation")
				}
				if len(tp.spans) != 1 || tp.endCalls != 1 {
					t.Fatalf("spans=%d end_calls=%d, want one each", len(tp.spans), tp.endCalls)
				}
				span := tp.spans[0]
				if !span.ended || span.name != vvotel.StorageSpanName(operation) {
					t.Fatalf("storage span differs: name=%q ended=%t", span.name, span.ended)
				}
				wantStatus := codes.Unset
				wantOutcome := vvotel.OutcomeOk
				wantErrorType := ""
				if terminal.withErr {
					wantStatus = codes.Error
					wantOutcome = vvotel.OutcomeError
					wantErrorType = vvotel.ErrorTypeInternal
				}
				if span.status != wantStatus || span.attributes[vvotel.AttrOperationOutcome].AsString() != wantOutcome {
					t.Fatalf("span terminal differs: status=%v attributes=%v", span.status, span.attributes)
				}
				if got := span.attributes[vvotel.AttrErrorType].AsString(); got != wantErrorType {
					t.Fatalf("span error.type=%q, want %q", got, wantErrorType)
				}
				if span.attributes[vvotel.AttrResourceName].AsString() != "assets" {
					t.Fatal("approved resource name was not preserved on the span")
				}
				assertCurrentSpanConforms(t, "storage_span", span)

				if operation == vvotel.OpStorageOpen && !terminal.withErr {
					reader := result.(storageOpenResult).reader
					payload, readErr := io.ReadAll(reader)
					if readErr != nil || string(payload) != "open-result-payload" {
						t.Fatalf("Open reader changed: payload=%q err=%v", payload, readErr)
					}
					wantCloseErr := raw.openReader.(*fakeStorageReadCloser).closeError
					if closeErr := reader.Close(); closeErr != wantCloseErr {
						t.Fatalf("Open close error identity changed: got=%v want=%v", closeErr, wantCloseErr)
					}
					if raw.openReader.(*fakeStorageReadCloser).closeCalls != 1 {
						t.Fatal("Open reader Close was not forwarded exactly once")
					}
				}
			})
		}
	}
}

func TestStorage_CapabilitiesDoNotEmitSpan(t *testing.T) {
	profiles := []struct {
		name string
		want storage.Capabilities
	}{
		{
			name: "alternating",
			want: storage.Capabilities{
				CreateOnly:       true,
				Replace:          false,
				Staging:          true,
				TemporaryURL:     false,
				ConditionalWrite: true,
				RangeRead:        false,
			},
		},
		{
			name: "complement",
			want: storage.Capabilities{
				CreateOnly:       false,
				Replace:          true,
				Staging:          false,
				TemporaryURL:     true,
				ConditionalWrite: false,
				RangeRead:        true,
			},
		},
	}

	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
			if err != nil {
				t.Fatal(err)
			}
			raw := &fakeStorageStore{exactResults: true, capabilities: profile.want}
			got := vvotel.Store(tel)(raw).Capabilities()
			if got != profile.want {
				t.Fatalf("Capabilities changed: got=%+v want=%+v", got, profile.want)
			}
			if raw.capabilitiesCalls != 1 {
				t.Fatalf("Capabilities business calls=%d, want 1", raw.capabilitiesCalls)
			}
			if len(tp.spans) != 0 || tp.tracerCalls != 1 || tp.startCalls != 0 {
				t.Fatalf("Capabilities emitted telemetry: spans=%d tracer_calls=%d start_calls=%d", len(tp.spans), tp.tracerCalls, tp.startCalls)
			}
		})
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

	want := &fakeBusinessPanic{operation: "storage delete"}
	raw := &fakeStorageStore{panicValue: want}
	s := vvotel.Store(tel)(raw)

	defer func() {
		r := recover()
		if r != want {
			t.Fatalf("business panic identity changed: got=%v want=%p", r, want)
		}
		if raw.calls != 1 || raw.operationCalls[vvotel.OpStorageDelete] != 1 || len(raw.operationCalls) != 1 {
			t.Fatalf("business calls=%d by_operation=%v, want Delete once", raw.calls, raw.operationCalls)
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
		assertCurrentSpanConforms(t, "storage_span", span)
	}()

	k, _ := storage.ParseKey("test.txt")
	_ = s.Delete(context.Background(), k, storage.DeleteOptions{})
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
			assertCurrentSpanConforms(t, "storage_span", span)
		})
	}
}

func TestStorage_OpenErrorAndPanic(t *testing.T) {
	tp := newTestTracerProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp})

	rawErr := &fakeStorageStore{err: storage.ErrNotFound}
	sErr := vvotel.Store(tel)(rawErr)

	k, _ := storage.ParseKey("test.txt")
	_, _, err := sErr.Open(context.Background(), k, storage.ReadOptions{})
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
	wantPanic := &fakeBusinessPanic{operation: "storage open"}
	rawPanic := &fakeStorageStore{panicValue: wantPanic}
	sPanic := vvotel.Store(tel2)(rawPanic)

	defer func() {
		r := recover()
		if r != wantPanic {
			t.Fatalf("business panic identity changed: got=%v want=%p", r, wantPanic)
		}
		if rawPanic.calls != 1 || rawPanic.operationCalls[vvotel.OpStorageOpen] != 1 || len(rawPanic.operationCalls) != 1 {
			t.Fatalf("business calls=%d by_operation=%v, want Open once", rawPanic.calls, rawPanic.operationCalls)
		}
		if len(tp2.spans) != 1 {
			t.Fatalf("expected 1 span for Open panic")
		}
		if tp2.spans[0].status != codes.Error {
			t.Errorf("expected Error status, got %v", tp2.spans[0].status)
		}
	}()
	_, _, _ = sPanic.Open(context.Background(), k, storage.ReadOptions{})
}

func TestStorage_GoexitPreservesGoroutineTermination(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	raw := &fakeStorageStore{goexit: true}
	s := vvotel.Store(tel)(raw)
	k, _ := storage.ParseKey("test.txt")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Delete(context.Background(), k, storage.DeleteOptions{})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime.Goexit did not terminate the goroutine")
	}
	if raw.calls != 1 || raw.operationCalls[vvotel.OpStorageDelete] != 1 || len(raw.operationCalls) != 1 {
		t.Fatalf("business calls=%d by_operation=%v, want Delete once", raw.calls, raw.operationCalls)
	}
	if len(tp.spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(tp.spans))
	}
	span := tp.spans[0]
	if !span.ended {
		t.Fatal("Goexit span was not ended")
	}
	if span.status != codes.Error {
		t.Fatalf("got status %v, want Error", span.status)
	}
	if got := span.attributes[vvotel.AttrOperationOutcome].AsString(); got != vvotel.OutcomeGoroutineExit {
		t.Fatalf("got outcome %q, want %q", got, vvotel.OutcomeGoroutineExit)
	}
	if _, ok := span.attributes[vvotel.AttrErrorType]; ok {
		t.Fatal("Goexit span must not contain error.type")
	}
	if tp.endCalls != 1 {
		t.Fatalf("End called %d times, want 1", tp.endCalls)
	}
}

func TestStorage_PanicNilIsNotSuppressed(t *testing.T) {
	tp := newTestTracerProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
	if err != nil {
		t.Fatal(err)
	}
	raw := &fakeStorageStore{panicNil: true}
	s := vvotel.Store(tel)(raw)
	k, _ := storage.ParseKey("test.txt")
	returned := false
	func() {
		defer func() { _ = recover() }()
		_ = s.Delete(context.Background(), k, storage.DeleteOptions{})
		returned = true
	}()
	if returned {
		t.Fatal("panic(nil) was suppressed")
	}
	if raw.calls != 1 || raw.operationCalls[vvotel.OpStorageDelete] != 1 || len(raw.operationCalls) != 1 {
		t.Fatalf("business calls=%d by_operation=%v, want Delete once", raw.calls, raw.operationCalls)
	}
	if len(tp.spans) != 1 || tp.endCalls != 1 || !tp.spans[0].ended {
		t.Fatal("panic(nil) span was not ended exactly once")
	}
	if tp.spans[0].status != codes.Error {
		t.Fatalf("panic(nil) span status=%v, want Error", tp.spans[0].status)
	}
	if got := tp.spans[0].attributes[vvotel.AttrErrorType].AsString(); got != vvotel.ErrorTypePanic {
		t.Fatalf("panic(nil) error.type=%q, want %q", got, vvotel.ErrorTypePanic)
	}
}

func TestStorage_OpenDisabled(t *testing.T) {
	tp := newTestTracerProvider()
	tel, _ := vvotel.New(vvotel.Config{TracerProvider: tp, StorageTracesDisabled: true})

	raw := &fakeStorageStore{}
	s := vvotel.Store(tel)(raw)

	k, _ := storage.ParseKey("test.txt")
	rc, _, err := s.Open(context.Background(), k, storage.ReadOptions{})
	if err != nil {
		t.Fatalf("unexpected Open error: %v", err)
	}
	_ = rc.Close()
	if len(tp.spans) != 0 {
		t.Fatal("Open must not emit span when storage traces disabled")
	}
}

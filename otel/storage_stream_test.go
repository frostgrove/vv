package vvotel_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/storage"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type storageStreamReadResult struct {
	n          int
	err        error
	panicValue any
	panicNil   bool
	goexit     bool
}

type scriptedStorageReadCloser struct {
	mu sync.Mutex

	reads          []storageStreamReadResult
	readCalls      int
	readBuffers    [][]byte
	closeCalls     int
	closeErr       error
	closePanic     any
	closePanicNil  bool
	closeGoexit    bool
	readEntered    chan struct{}
	readRelease    chan struct{}
	blockReadIndex int
}

func (r *scriptedStorageReadCloser) Read(p []byte) (int, error) {
	r.mu.Lock()
	index := r.readCalls
	r.readCalls++
	r.readBuffers = append(r.readBuffers, p)
	var result storageStreamReadResult
	if index < len(r.reads) {
		result = r.reads[index]
	}
	entered := r.readEntered
	release := r.readRelease
	blocked := index == r.blockReadIndex && release != nil
	r.mu.Unlock()
	if blocked {
		if entered != nil {
			entered <- struct{}{}
		}
		<-release
	}
	if result.goexit {
		runtime.Goexit()
	}
	if result.panicNil {
		panic(nil)
	}
	if result.panicValue != nil {
		panic(result.panicValue)
	}
	return result.n, result.err
}

func (r *scriptedStorageReadCloser) Close() error {
	r.mu.Lock()
	r.closeCalls++
	panicValue := r.closePanic
	panicNil := r.closePanicNil
	goexit := r.closeGoexit
	err := r.closeErr
	r.mu.Unlock()
	if goexit {
		runtime.Goexit()
	}
	if panicNil {
		panic(nil)
	}
	if panicValue != nil {
		panic(panicValue)
	}
	return err
}

func (r *scriptedStorageReadCloser) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readCalls, r.closeCalls
}

func (r *scriptedStorageReadCloser) buffer(index int) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readBuffers[index]
}

type typedNilStorageReadCloser struct{}

func (*typedNilStorageReadCloser) Read([]byte) (int, error) { return 0, io.EOF }
func (*typedNilStorageReadCloser) Close() error             { return nil }

type storageStreamContextKey struct{}

func openStorageStream(t *testing.T, tp *testTracerProvider, mp *testMeterProvider, reader io.ReadCloser, config func(*vvotel.Config)) io.ReadCloser {
	t.Helper()
	cfg := vvotel.Config{TracerProvider: tp, MeterProvider: mp}
	if config != nil {
		config(&cfg)
	}
	tel, err := vvotel.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw := &fakeStorageStore{
		exactResults: true,
		openReader:   reader,
		openInfo:     exactStorageInfo(41, "stream", time.Unix(123, 0)),
	}
	store := vvotel.Store(tel, vvotel.WithStorageStreams())(raw)
	key := mustStorageKey(t, "stream/object")
	result, info, err := store.Open(context.WithValue(context.Background(), storageStreamContextKey{}, "request-secret"), key, storage.ReadOptions{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !reflect.DeepEqual(info, raw.openInfo) || raw.operationCalls[vvotel.OpStorageOpen] != 1 {
		t.Fatalf("Open effect changed: info=%+v calls=%v", info, raw.operationCalls)
	}
	return result
}

func recordedStorageStreamSpans(tp *testTracerProvider) []*recordedSpan {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	var result []*recordedSpan
	for _, span := range tp.spans {
		if span.name == vvotel.SpanStorageStream {
			result = append(result, span)
		}
	}
	return result
}

func recordedStorageStreamMetrics(mp *testMeterProvider, name string) []*recordedMetric {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	var result []*recordedMetric
	for _, measurement := range mp.metrics {
		if measurement.name == name {
			result = append(result, measurement)
		}
	}
	return result
}

func assertStorageStreamTerminal(t *testing.T, tp *testTracerProvider, mp *testMeterProvider, outcome string, errorType string, bytes *int64) {
	t.Helper()
	spans := recordedStorageStreamSpans(tp)
	if len(spans) != 1 {
		t.Fatalf("stream spans=%d, want 1", len(spans))
	}
	span := spans[0]
	if !span.ended || span.kind != trace.SpanKindInternal || span.attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentStorageStream || span.attributes[vvotel.AttrOperationName].AsString() != vvotel.OpStorageStreamConsume || span.attributes[vvotel.AttrOperationOutcome].AsString() != outcome {
		t.Fatalf("stream span mismatch: %+v", span)
	}
	wantStatus := codes.Unset
	if errorType != "" || outcome == vvotel.OutcomeGoroutineExit {
		wantStatus = codes.Error
	}
	if span.status != wantStatus {
		t.Fatalf("stream status=%v, want %v", span.status, wantStatus)
	}
	if got := span.attributes[vvotel.AttrErrorType].AsString(); got != errorType {
		t.Fatalf("stream error.type=%q, want %q", got, errorType)
	}
	durations := recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamDuration)
	if outcome == vvotel.OutcomeGoroutineExit {
		if len(durations) != 0 || len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamBytes)) != 0 {
			t.Fatal("Goexit emitted stream metrics")
		}
		return
	}
	if len(durations) != 1 {
		t.Fatalf("stream durations=%d, want 1", len(durations))
	}
	assertStorageStreamMetricAttributes(t, durations[0], outcome, errorType)
	if durations[0].context.Value(storageStreamContextKey{}) != nil {
		t.Fatal("stream retained the request context")
	}
	byteMetrics := recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamBytes)
	if bytes == nil {
		if len(byteMetrics) != 0 {
			t.Fatalf("stream bytes=%d, want none", len(byteMetrics))
		}
		return
	}
	if len(byteMetrics) != 1 || byteMetrics[0].value != *bytes {
		t.Fatalf("stream byte metrics=%+v, want %d", byteMetrics, *bytes)
	}
	assertStorageStreamMetricAttributes(t, byteMetrics[0], outcome, errorType)
}

func assertStorageStreamMetricAttributes(t *testing.T, measurement *recordedMetric, outcome string, errorType string) {
	t.Helper()
	if measurement.attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentStorageStream || measurement.attributes[vvotel.AttrOperationName].AsString() != vvotel.OpStorageStreamConsume || measurement.attributes[vvotel.AttrOperationOutcome].AsString() != outcome || measurement.attributes[vvotel.AttrErrorType].AsString() != errorType {
		t.Fatalf("stream metric attributes=%v", measurement.attributes)
	}
}

func TestStorageStream_OpenResultMatrixPreservesIdentity(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	wantInfo := exactStorageInfo(71, "open-matrix", time.Unix(456, 0))
	wantErr := errors.New("open-secret")
	var typedNil *typedNilStorageReadCloser
	readers := []struct {
		name    string
		reader  io.ReadCloser
		err     error
		wrapped bool
	}{
		{name: "success", reader: &scriptedStorageReadCloser{}, wrapped: true},
		{name: "error_with_reader", reader: &scriptedStorageReadCloser{}, err: wantErr},
		{name: "success_nil"},
		{name: "success_typed_nil", reader: typedNil},
		{name: "error_typed_nil", reader: typedNil, err: wantErr},
	}
	for _, tc := range readers {
		t.Run(tc.name, func(t *testing.T) {
			raw := &fakeStorageStore{exactResults: true, openReader: tc.reader, openInfo: wantInfo, err: tc.err}
			got, info, gotErr := vvotel.Store(tel, vvotel.WithStorageStreams())(raw).Open(context.Background(), mustStorageKey(t, "matrix/object"), storage.ReadOptions{Offset: 3})
			if gotErr != tc.err || !reflect.DeepEqual(info, wantInfo) || raw.operationCalls[vvotel.OpStorageOpen] != 1 {
				t.Fatalf("Open changed result: reader=%T info=%+v err=%v calls=%v", got, info, gotErr, raw.operationCalls)
			}
			if tc.wrapped {
				stream, ok := got.(vvotel.StorageStream)
				if !ok || stream.Unwrap() != tc.reader {
					t.Fatalf("successful reader was not an exact unwrap-capable stream: %T", got)
				}
			} else if got != tc.reader {
				t.Fatalf("reader identity changed: got=%#v want=%#v", got, tc.reader)
			}
		})
	}
	if len(recordedStorageStreamSpans(tp)) != 0 || len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamDuration)) != 0 || len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamBytes)) != 0 {
		t.Fatal("Open result matrix emitted stream telemetry before use")
	}
}

func TestStorageStream_AllSignalsDisabledAndOptionOmittedPreserveReader(t *testing.T) {
	for _, tc := range []struct {
		name    string
		option  bool
		disable vvotel.Signals
	}{
		{name: "option_omitted"},
		{name: "all_disabled", option: true, disable: vvotel.Signals{vvotel.SignalStorageStreamSpan, vvotel.SignalStorageStreamDuration, vvotel.SignalStorageStreamBytes}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp, Disable: tc.disable})
			if err != nil {
				t.Fatal(err)
			}
			rawReader := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{n: 0, err: io.EOF}}}
			raw := &fakeStorageStore{exactResults: true, openReader: rawReader}
			var options []vvotel.StorageOption
			if tc.option {
				options = append(options, vvotel.WithStorageStreams())
			}
			got, _, err := vvotel.Store(tel, options...)(raw).Open(context.Background(), mustStorageKey(t, "disabled/object"), storage.ReadOptions{})
			if err != nil || got != rawReader {
				t.Fatalf("reader identity changed: got=%T err=%v", got, err)
			}
			if _, err := got.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("raw Read error=%v", err)
			}
			if len(recordedStorageStreamSpans(tp)) != 0 || len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamDuration)) != 0 || len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamBytes)) != 0 {
				t.Fatal("disabled stream emitted telemetry")
			}
		})
	}
}

func TestStorageStream_SignalsCanBeDisabledIndependently(t *testing.T) {
	for _, tc := range []struct {
		name         string
		disable      vvotel.Signal
		wantSpans    int
		wantDuration int
		wantBytes    int
	}{
		{name: "span", disable: vvotel.SignalStorageStreamSpan, wantDuration: 1, wantBytes: 1},
		{name: "duration", disable: vvotel.SignalStorageStreamDuration, wantSpans: 1, wantBytes: 1},
		{name: "bytes", disable: vvotel.SignalStorageStreamBytes, wantSpans: 1, wantDuration: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			rawReader := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{n: 2, err: io.EOF}}}
			reader := openStorageStream(t, tp, mp, rawReader, func(config *vvotel.Config) {
				config.Disable = vvotel.Signals{tc.disable}
			})
			if n, err := reader.Read(make([]byte, 2)); n != 2 || err != io.EOF {
				t.Fatalf("Read=(%d,%v)", n, err)
			}
			if got := len(recordedStorageStreamSpans(tp)); got != tc.wantSpans {
				t.Fatalf("stream spans=%d, want %d", got, tc.wantSpans)
			}
			if got := len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamDuration)); got != tc.wantDuration {
				t.Fatalf("stream durations=%d, want %d", got, tc.wantDuration)
			}
			if got := len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamBytes)); got != tc.wantBytes {
				t.Fatalf("stream bytes=%d, want %d", got, tc.wantBytes)
			}
		})
	}
}

func TestStorageStream_TerminalMatrix(t *testing.T) {
	internalErr := errors.New("read-secret")
	wrappedEOF := fmt.Errorf("wrapped: %w", io.EOF)
	tests := []struct {
		name      string
		reads     []storageStreamReadResult
		closeErr  error
		action    string
		wantErr   error
		outcome   string
		errorType string
		bytes     int64
	}{
		{name: "eof", reads: []storageStreamReadResult{{err: io.EOF}}, action: "read", wantErr: io.EOF, outcome: "eof"},
		{name: "partial_eof", reads: []storageStreamReadResult{{n: 3, err: io.EOF}}, action: "read", wantErr: io.EOF, outcome: "eof", bytes: 3},
		{name: "wrapped_eof", reads: []storageStreamReadResult{{n: 2, err: wrappedEOF}}, action: "read", wantErr: wrappedEOF, outcome: vvotel.OutcomeError, errorType: vvotel.ErrorTypeInternal, bytes: 2},
		{name: "read_error", reads: []storageStreamReadResult{{n: 4, err: internalErr}}, action: "read", wantErr: internalErr, outcome: vvotel.OutcomeError, errorType: vvotel.ErrorTypeInternal, bytes: 4},
		{name: "read_canceled", reads: []storageStreamReadResult{{n: 1, err: context.Canceled}}, action: "read", wantErr: context.Canceled, outcome: vvotel.OutcomeError, errorType: vvotel.ErrorTypeCanceled, bytes: 1},
		{name: "closed", action: "close", outcome: "closed"},
		{name: "close_timeout", action: "close", closeErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded, outcome: vvotel.OutcomeError, errorType: vvotel.ErrorTypeTimeout},
		{name: "unwrapped", reads: []storageStreamReadResult{{n: 5}}, action: "unwrap", outcome: "unwrapped", bytes: 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			raw := &scriptedStorageReadCloser{reads: tc.reads, closeErr: tc.closeErr}
			stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
			var gotErr error
			switch tc.action {
			case "read":
				buffer := make([]byte, 8)
				n, err := stream.Read(buffer)
				gotErr = err
				if n != tc.reads[0].n {
					t.Fatalf("Read n=%d, want %d", n, tc.reads[0].n)
				}
			case "close":
				gotErr = stream.Close()
			case "unwrap":
				n, err := stream.Read(make([]byte, 8))
				if n != tc.reads[0].n || err != tc.reads[0].err || stream.Unwrap() != raw {
					t.Fatalf("unwrap setup changed effect: n=%d err=%v", n, err)
				}
			}
			if gotErr != tc.wantErr {
				t.Fatalf("error identity changed: got=%v want=%v", gotErr, tc.wantErr)
			}
			assertStorageStreamTerminal(t, tp, mp, tc.outcome, tc.errorType, &tc.bytes)
		})
	}
}

func TestStorageStream_UnwrapBeforeStartDisablesObservation(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	wantErr := errors.New("after-unwrap")
	raw := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{n: 7, err: wantErr}}}
	stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
	if stream.Unwrap() != raw || stream.Unwrap() != raw {
		t.Fatal("Unwrap did not return the raw reader")
	}
	n, err := stream.Read(make([]byte, 8))
	if n != 7 || err != wantErr {
		t.Fatalf("post-Unwrap Read changed: n=%d err=%v", n, err)
	}
	if len(recordedStorageStreamSpans(tp)) != 0 || len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamDuration)) != 0 || len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamBytes)) != 0 {
		t.Fatal("pre-start Unwrap emitted telemetry")
	}
}

func TestStorageStream_PostTerminationCallsStillDelegate(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	lateErr := errors.New("late-secret")
	raw := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{err: io.EOF}, {n: 7, err: lateErr}}}
	stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
	if n, err := stream.Read(make([]byte, 8)); n != 0 || err != io.EOF {
		t.Fatalf("first Read=(%d,%v)", n, err)
	}
	if n, err := stream.Read(make([]byte, 8)); n != 7 || err != lateErr {
		t.Fatalf("late Read=(%d,%v)", n, err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	reads, closes := raw.counts()
	if reads != 2 || closes != 1 {
		t.Fatalf("underlying calls read=%d close=%d", reads, closes)
	}
	wantBytes := int64(0)
	assertStorageStreamTerminal(t, tp, mp, "eof", "", &wantBytes)
}

func TestStorageStream_ReadForwardsTheExactBuffer(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	raw := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{n: 1}}}
	stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
	buffer := []byte{3, 5, 7, 11}
	n, err := stream.Read(buffer)
	if n != 1 || err != nil {
		t.Fatalf("Read=(%d,%v)", n, err)
	}
	forwarded := raw.buffer(0)
	if len(forwarded) != len(buffer) || &forwarded[0] != &buffer[0] {
		t.Fatal("Read buffer identity changed")
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	wantBytes := int64(1)
	assertStorageStreamTerminal(t, tp, mp, "closed", "", &wantBytes)
}

func TestStorageStream_InvalidReadCountSuppressesOnlyBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		cap  int
	}{
		{name: "negative", n: -1, cap: 1},
		{name: "larger_than_buffer", n: 2, cap: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			raw := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{n: tc.n}, {err: io.EOF}}}
			stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
			if n, err := stream.Read(make([]byte, tc.cap)); n != tc.n || err != nil {
				t.Fatalf("invalid Read changed: n=%d err=%v", n, err)
			}
			if _, err := stream.Read(make([]byte, tc.cap)); err != io.EOF {
				t.Fatalf("terminal Read error=%v", err)
			}
			assertStorageStreamTerminal(t, tp, mp, "eof", "", nil)
		})
	}
}

func TestStorageStream_PanicAndGoexitTerminals(t *testing.T) {
	for _, action := range []string{"read", "close"} {
		t.Run("panic_"+action, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			want := &fakeBusinessPanic{operation: "stream " + action}
			raw := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{panicValue: want}}, closePanic: want}
			stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
			var got any
			func() {
				defer func() { got = recover() }()
				if action == "read" {
					_, _ = stream.Read(make([]byte, 1))
				} else {
					_ = stream.Close()
				}
			}()
			if got != want {
				t.Fatalf("panic identity changed: got=%v want=%v", got, want)
			}
			wantBytes := int64(0)
			assertStorageStreamTerminal(t, tp, mp, vvotel.OutcomeError, vvotel.ErrorTypePanic, &wantBytes)
		})
		t.Run("goexit_"+action, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			raw := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{goexit: true}}, closeGoexit: true}
			stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
			done := make(chan struct{})
			go func() {
				defer close(done)
				if action == "read" {
					_, _ = stream.Read(make([]byte, 1))
				} else {
					_ = stream.Close()
				}
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("Goexit did not terminate the goroutine")
			}
			assertStorageStreamTerminal(t, tp, mp, vvotel.OutcomeGoroutineExit, "", nil)
		})
	}
}

func TestStorageStream_PanicNilIsNotSuppressed(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	raw := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{panicNil: true}}}
	stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
	returned := false
	func() {
		defer func() { _ = recover() }()
		_, _ = stream.Read(make([]byte, 1))
		returned = true
	}()
	if returned {
		t.Fatal("panic(nil) was suppressed")
	}
	wantBytes := int64(0)
	assertStorageStreamTerminal(t, tp, mp, vvotel.OutcomeError, vvotel.ErrorTypePanic, &wantBytes)
}

func TestStorageStream_DoesNotSerializeUnderlyingIO(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	entered := make(chan struct{})
	release := make(chan struct{})
	raw := &scriptedStorageReadCloser{
		reads:          []storageStreamReadResult{{n: 1}},
		readEntered:    entered,
		readRelease:    release,
		blockReadIndex: 0,
	}
	stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _ = stream.Read(make([]byte, 1))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("underlying Read was not entered")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- stream.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close was serialized behind Read")
	}
	close(release)
	<-readDone
	wantBytes := int64(0)
	assertStorageStreamTerminal(t, tp, mp, "closed", "", &wantBytes)
}

func TestStorageStream_UnwrapDoesNotWaitForInflightRead(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	entered := make(chan struct{})
	release := make(chan struct{})
	raw := &scriptedStorageReadCloser{
		reads:          []storageStreamReadResult{{n: 1}},
		readEntered:    entered,
		readRelease:    release,
		blockReadIndex: 0,
	}
	stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _ = stream.Read(make([]byte, 1))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("underlying Read was not entered")
	}
	unwrapped := make(chan io.ReadCloser, 1)
	go func() { unwrapped <- stream.Unwrap() }()
	select {
	case got := <-unwrapped:
		if got != raw {
			t.Fatal("Unwrap identity changed")
		}
	case <-time.After(time.Second):
		t.Fatal("Unwrap waited for an in-flight Read")
	}
	close(release)
	<-readDone
	wantBytes := int64(0)
	assertStorageStreamTerminal(t, tp, mp, "unwrapped", "", &wantBytes)
}

type storageStreamRaceReader struct {
	mu       sync.Mutex
	calls    int
	entered  [2]chan struct{}
	release  [2]chan struct{}
	results  [2]storageStreamReadResult
	warmDone bool
}

func (r *storageStreamRaceReader) Read([]byte) (int, error) {
	r.mu.Lock()
	if !r.warmDone {
		r.warmDone = true
		r.calls++
		r.mu.Unlock()
		return 2, nil
	}
	index := r.calls - 1
	r.calls++
	entered := r.entered[index]
	release := r.release[index]
	result := r.results[index]
	r.mu.Unlock()
	close(entered)
	<-release
	return result.n, result.err
}

func (r *storageStreamRaceReader) Close() error { return nil }

func TestStorageStream_FirstCompletedTerminalWinsAndExcludesLateBytes(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	winnerErr := errors.New("winner-secret")
	raw := &storageStreamRaceReader{
		entered: [2]chan struct{}{make(chan struct{}), make(chan struct{})},
		release: [2]chan struct{}{make(chan struct{}), make(chan struct{})},
		results: [2]storageStreamReadResult{{n: 3, err: io.EOF}, {n: 4, err: winnerErr}},
	}
	stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
	if n, err := stream.Read(make([]byte, 8)); n != 2 || err != nil {
		t.Fatalf("warm Read=(%d,%v)", n, err)
	}
	type result struct {
		n   int
		err error
	}
	first := make(chan result, 1)
	second := make(chan result, 1)
	go func() {
		n, err := stream.Read(make([]byte, 8))
		first <- result{n: n, err: err}
	}()
	<-raw.entered[0]
	go func() {
		n, err := stream.Read(make([]byte, 8))
		second <- result{n: n, err: err}
	}()
	<-raw.entered[1]
	close(raw.release[1])
	gotSecond := <-second
	if gotSecond.n != 4 || gotSecond.err != winnerErr {
		t.Fatalf("winner result=%+v", gotSecond)
	}
	close(raw.release[0])
	gotFirst := <-first
	if gotFirst.n != 3 || gotFirst.err != io.EOF {
		t.Fatalf("late result=%+v", gotFirst)
	}
	wantBytes := int64(6)
	assertStorageStreamTerminal(t, tp, mp, vvotel.OutcomeError, vvotel.ErrorTypeInternal, &wantBytes)
}

func TestStorageStream_PendingTerminalDuringStartUsesCompletionTimestamp(t *testing.T) {
	for _, action := range []string{"read", "close", "unwrap"} {
		t.Run(action, func(t *testing.T) {
			tp := newTestTracerProvider()
			tp.blockSpanName = vvotel.SpanStorageStream
			tp.startEntered = make(chan struct{})
			tp.startRelease = make(chan struct{})
			mp := newTestMeterProvider()
			reads := []storageStreamReadResult{{err: io.EOF}, {n: 1}}
			raw := &scriptedStorageReadCloser{reads: reads}
			stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
			startDone := make(chan struct{})
			go func() {
				defer close(startDone)
				_, _ = stream.Read(make([]byte, 1))
			}()
			select {
			case <-tp.startEntered:
			case <-time.After(time.Second):
				t.Fatal("stream Start was not entered")
			}
			outcome := ""
			switch action {
			case "read":
				if _, err := stream.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("racing Read error=%v", err)
				}
				outcome = "eof"
			case "close":
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				outcome = "closed"
			case "unwrap":
				if stream.Unwrap() != raw {
					t.Fatal("Unwrap identity changed")
				}
				outcome = "unwrapped"
			}
			releasedAt := time.Now()
			close(tp.startRelease)
			select {
			case <-startDone:
			case <-time.After(time.Second):
				t.Fatal("start winner did not return")
			}
			spans := recordedStorageStreamSpans(tp)
			if len(spans) != 1 || !spans[0].endTime.Before(releasedAt) || spans[0].endTime.Before(spans[0].startTime) {
				t.Fatalf("explicit timestamps start=%v end=%v release=%v", spans[0].startTime, spans[0].endTime, releasedAt)
			}
			durations := recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamDuration)
			if len(durations) != 1 || durations[0].value != spans[0].endTime.Sub(spans[0].startTime).Seconds() {
				t.Fatalf("duration=%v span=%v", durations, spans[0].endTime.Sub(spans[0].startTime))
			}
			wantBytes := int64(0)
			if action != "read" {
				wantBytes = 0
			}
			assertStorageStreamTerminal(t, tp, mp, outcome, "", &wantBytes)
		})
	}
}

func TestStorageStream_ProviderFaultsDoNotChangeEffects(t *testing.T) {
	for _, tc := range []struct {
		name             string
		set              func(*testTracerProvider, *testMeterProvider)
		durationSurvives bool
		bytesSurvive     bool
	}{
		{name: "start_panic", set: func(tp *testTracerProvider, _ *testMeterProvider) { tp.panicStart = true }, durationSurvives: true, bytesSurvive: true},
		{name: "typed_nil_span", set: func(tp *testTracerProvider, _ *testMeterProvider) { tp.typedNilSpan = true }, durationSurvives: true, bytesSurvive: true},
		{name: "typed_nil_context", set: func(tp *testTracerProvider, _ *testMeterProvider) { tp.typedNilContext = true }, durationSurvives: true, bytesSurvive: true},
		{name: "span_attributes_panic", set: func(tp *testTracerProvider, _ *testMeterProvider) { tp.panicAttributes = true }, durationSurvives: true, bytesSurvive: true},
		{name: "span_status_panic", set: func(tp *testTracerProvider, _ *testMeterProvider) { tp.panicStatus = true }, durationSurvives: true, bytesSurvive: true},
		{name: "span_end_panic", set: func(tp *testTracerProvider, _ *testMeterProvider) { tp.panicEnd = true }, durationSurvives: true, bytesSurvive: true},
		{name: "metric_record_panic", set: func(_ *testTracerProvider, mp *testMeterProvider) { mp.panicHistogramRecord = true }},
		{name: "duration_record_panic", set: func(_ *testTracerProvider, mp *testMeterProvider) {
			mp.panicFloatHistogramNames = map[string]bool{vvotel.MetricStorageStreamDuration: true}
		}, bytesSurvive: true},
		{name: "bytes_record_panic", set: func(_ *testTracerProvider, mp *testMeterProvider) {
			mp.panicInt64HistogramNames = map[string]bool{vvotel.MetricStorageStreamBytes: true}
		}, durationSurvives: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			tc.set(tp, mp)
			wantErr := errors.New("exact-reader-error")
			raw := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{n: 3, err: wantErr}}}
			stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
			n, err := stream.Read(make([]byte, 4))
			if n != 3 || err != wantErr {
				t.Fatalf("effect changed: n=%d err=%v", n, err)
			}
			reads, _ := raw.counts()
			if reads != 1 {
				t.Fatalf("underlying reads=%d", reads)
			}
			if tc.durationSurvives && len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamDuration)) != 1 {
				t.Fatal("a trace/neighboring metric fault suppressed stream duration")
			}
			if tc.bytesSurvive && len(recordedStorageStreamMetrics(mp, vvotel.MetricStorageStreamBytes)) != 1 {
				t.Fatal("a trace/neighboring metric fault suppressed stream bytes")
			}
		})
	}
}

func TestStorageStream_TraceStartFailureUsesIncomingSpanContextForMetrics(t *testing.T) {
	tp := newTestTracerProvider()
	tp.panicStart = true
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{17, 18, 19, 20, 21, 22, 23, 24},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.WithValue(context.Background(), storageStreamContextKey{}, "request-secret"), parent)
	rawReader := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{n: 3, err: io.EOF}}}
	raw := &fakeStorageStore{exactResults: true, openReader: rawReader}
	reader, _, err := vvotel.Store(tel, vvotel.WithStorageStreams())(raw).Open(ctx, mustStorageKey(t, "trace-failure/object"), storage.ReadOptions{})
	if err != nil || raw.lastCtx != ctx {
		t.Fatalf("Open changed context/error: same=%t err=%v", raw.lastCtx == ctx, err)
	}
	n, readErr := reader.Read(make([]byte, 4))
	if n != 3 || readErr != io.EOF {
		t.Fatalf("Read=(%d,%v)", n, readErr)
	}
	for _, name := range []string{vvotel.MetricStorageStreamDuration, vvotel.MetricStorageStreamBytes} {
		measurements := recordedStorageStreamMetrics(mp, name)
		if len(measurements) != 1 {
			t.Fatalf("%s measurements=%d", name, len(measurements))
		}
		metricContext := measurements[0].context
		if !trace.SpanContextFromContext(metricContext).Equal(parent) || metricContext.Value(storageStreamContextKey{}) != nil {
			t.Fatalf("%s fallback context=%v value=%v", name, trace.SpanContextFromContext(metricContext), metricContext.Value(storageStreamContextKey{}))
		}
	}
}

func TestStorageStream_OpenStartFailureParentsStreamToIncomingSpanContext(t *testing.T) {
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17},
		SpanID:     trace.SpanID{18, 19, 20, 21, 22, 23, 24, 25},
		TraceFlags: trace.FlagsSampled,
	})
	child := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    parent.TraceID(),
		SpanID:     trace.SpanID{26, 27, 28, 29, 30, 31, 32, 33},
		TraceFlags: trace.FlagsSampled,
	})
	tp := newTestTracerProvider()
	tp.panicStartNames = map[string]bool{vvotel.StorageSpanName(vvotel.OpStorageOpen): true}
	tp.spanContexts = map[string]trace.SpanContext{vvotel.SpanStorageStream: child}
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(context.WithValue(context.Background(), storageStreamContextKey{}, "request-secret"), parent)
	rawReader := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{err: io.EOF}}}
	raw := &fakeStorageStore{exactResults: true, openReader: rawReader}
	reader, _, err := vvotel.Store(tel, vvotel.WithStorageStreams())(raw).Open(ctx, mustStorageKey(t, "open-failure/object"), storage.ReadOptions{})
	if err != nil || raw.lastCtx != ctx {
		t.Fatalf("Open changed context/error: same=%t err=%v", raw.lastCtx == ctx, err)
	}
	if _, err := reader.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("Read error=%v", err)
	}
	spans := recordedStorageStreamSpans(tp)
	if len(spans) != 1 || !spans[0].parent.Equal(parent) || !spans[0].spanContext.Equal(child) {
		t.Fatalf("stream parent/context=%v/%v, want %v/%v", spans[0].parent, spans[0].spanContext, parent, child)
	}
	for _, name := range []string{vvotel.MetricStorageStreamDuration, vvotel.MetricStorageStreamBytes} {
		measurements := recordedStorageStreamMetrics(mp, name)
		if len(measurements) != 1 || !trace.SpanContextFromContext(measurements[0].context).Equal(child) || measurements[0].context.Value(storageStreamContextKey{}) != nil {
			t.Fatalf("%s context was not the derived stream span: %+v", name, measurements)
		}
	}
}

func TestStorageStream_TracingDisabledUsesIncomingSpanContextForMetrics(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18},
		SpanID:     trace.SpanID{19, 20, 21, 22, 23, 24, 25, 26},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.WithValue(context.Background(), storageStreamContextKey{}, "request-secret"), parent)
	rawReader := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{n: 2, err: io.EOF}}}
	raw := &fakeStorageStore{exactResults: true, openReader: rawReader}
	reader, _, err := vvotel.Store(tel, vvotel.WithStorageStreams())(raw).Open(ctx, mustStorageKey(t, "trace-disabled/object"), storage.ReadOptions{})
	if err != nil || raw.lastCtx != ctx {
		t.Fatalf("Open changed context/error: same=%t err=%v", raw.lastCtx == ctx, err)
	}
	if n, err := reader.Read(make([]byte, 2)); n != 2 || err != io.EOF {
		t.Fatalf("Read=(%d,%v)", n, err)
	}
	for _, name := range []string{vvotel.MetricStorageStreamDuration, vvotel.MetricStorageStreamBytes} {
		measurements := recordedStorageStreamMetrics(mp, name)
		if len(measurements) != 1 || !trace.SpanContextFromContext(measurements[0].context).Equal(parent) || measurements[0].context.Value(storageStreamContextKey{}) != nil {
			t.Fatalf("%s context did not retain only the incoming SpanContext: %+v", name, measurements)
		}
	}
}

func TestStorageStream_NoSensitiveInputsBecomeAttributes(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	secret := "tenant-token-storage-key-sql-header"
	raw := &scriptedStorageReadCloser{reads: []storageStreamReadResult{{err: errors.New(secret)}}}
	stream := openStorageStream(t, tp, mp, raw, nil).(vvotel.StorageStream)
	_, _ = stream.Read([]byte(secret))
	for _, span := range recordedStorageStreamSpans(tp) {
		for key, value := range span.attributes {
			if key == attribute.Key(secret) || value.AsString() == secret {
				t.Fatalf("secret leaked into span attributes: %v", span.attributes)
			}
		}
	}
	for _, name := range []string{vvotel.MetricStorageStreamDuration, vvotel.MetricStorageStreamBytes} {
		for _, measurement := range recordedStorageStreamMetrics(mp, name) {
			for key, value := range measurement.attributes {
				if key == attribute.Key(secret) || value.AsString() == secret {
					t.Fatalf("secret leaked into metric attributes: %v", measurement.attributes)
				}
			}
		}
	}
}

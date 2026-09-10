package vvotel

import (
	"context"
	"io"
	"math"
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type StorageStream interface {
	Read([]byte) (int, error)
	Close() error
	Unwrap() io.ReadCloser
}

type storageStream struct {
	inner    io.ReadCloser
	recorder *storageStreamRecorder
}

type storageStreamPhase uint8

const (
	storageStreamIdle storageStreamPhase = iota
	storageStreamStarting
	storageStreamStarted
	storageStreamOutcomeEOF       = "eof"
	storageStreamOutcomeClosed    = "closed"
	storageStreamOutcomeUnwrapped = "unwrapped"
)

type storageStreamRecorder struct {
	mu sync.Mutex

	tracer    trace.Tracer
	duration  metric.Float64Histogram
	bytes     metric.Int64Histogram
	parent    trace.SpanContext
	phase     storageStreamPhase
	startedAt time.Time
	context   context.Context
	span      trace.Span

	terminated     bool
	terminalAt     time.Time
	outcome        string
	errorType      string
	suppressMetric bool
	published      bool
	totalBytes     int64
	bytesValid     bool
}

type storageStreamCompletion struct {
	span           trace.Span
	context        context.Context
	duration       metric.Float64Histogram
	bytes          metric.Int64Histogram
	startedAt      time.Time
	endedAt        time.Time
	outcome        string
	errorType      string
	suppressMetric bool
	totalBytes     int64
	bytesValid     bool
}

func storageStreamSignalsEnabled(t *Telemetry) bool {
	return t != nil && (t.signalEnabled(SignalStorageStreamSpan) || t.signalEnabled(SignalStorageStreamDuration) || t.signalEnabled(SignalStorageStreamBytes))
}

func newStorageStream(inner io.ReadCloser, t *Telemetry, parent trace.SpanContext) StorageStream {
	recorder := &storageStreamRecorder{parent: parent, bytesValid: true}
	if t.signalEnabled(SignalStorageStreamSpan) {
		recorder.tracer = t.tracer
	}
	if t.signalEnabled(SignalStorageStreamDuration) {
		recorder.duration = t.float64Histogram(SignalStorageStreamDuration)
	}
	if t.signalEnabled(SignalStorageStreamBytes) {
		recorder.bytes = t.int64Histogram(SignalStorageStreamBytes)
	}
	return &storageStream{inner: inner, recorder: recorder}
}

func (s *storageStream) Read(p []byte) (n int, err error) {
	returned := false
	defer func() {
		if !returned {
			s.recorder.finishGoexit()
		}
	}()

	s.recorder.start()
	n, err, panicked, panicValue := invokeStorageStreamRead(s.inner, p)
	returned = true
	if panicked {
		s.recorder.finish(OutcomeError, ErrorTypePanic, false)
		panic(panicValue)
	}
	s.recorder.readCompleted(n, len(p), err)
	return n, err
}

func (s *storageStream) Close() (err error) {
	returned := false
	defer func() {
		if !returned {
			s.recorder.finishGoexit()
		}
	}()

	s.recorder.start()
	err, panicked, panicValue := invokeStorageStreamClose(s.inner)
	returned = true
	if panicked {
		s.recorder.finish(OutcomeError, ErrorTypePanic, false)
		panic(panicValue)
	}
	if err == nil {
		s.recorder.finish(storageStreamOutcomeClosed, "", false)
		return nil
	}
	outcome, errorType := classifyStorageStreamError(err)
	s.recorder.finish(outcome, errorType, false)
	return err
}

func (s *storageStream) Unwrap() io.ReadCloser {
	completion := s.recorder.unwrap()
	publishStorageStream(completion)
	return s.inner
}

func (r *storageStreamRecorder) start() {
	r.mu.Lock()
	if r.phase != storageStreamIdle || r.terminated {
		r.mu.Unlock()
		return
	}
	r.phase = storageStreamStarting
	r.startedAt = time.Now()
	startedAt := r.startedAt
	parent := r.parent
	tracer := r.tracer
	r.mu.Unlock()

	spanContext := context.Background()
	if parent.IsValid() {
		spanContext = trace.ContextWithSpanContext(spanContext, parent)
	}
	operationContext := spanContext
	var span trace.Span
	if !nilInterface(tracer) {
		attributes, admitted := storageStreamSpanAttributes(storageStreamOutcomeEOF, "")
		if admitted {
			next, startedSpan, ok := safeStart(
				tracer,
				spanContext,
				SpanStorageStream,
				trace.WithSpanKind(trace.SpanKindInternal),
				trace.WithTimestamp(startedAt),
				trace.WithAttributes(spanStartAttributes(attributes)...),
			)
			if ok {
				operationContext = next
				span = startedSpan
			}
		}
	}

	r.mu.Lock()
	r.phase = storageStreamStarted
	r.context = operationContext
	r.span = span
	completion := r.completionLocked()
	r.mu.Unlock()
	publishStorageStream(completion)
}

func (r *storageStreamRecorder) readCompleted(n int, capacity int, err error) {
	var outcome string
	var errorType string
	if err == io.EOF {
		outcome = storageStreamOutcomeEOF
	} else if err != nil {
		outcome, errorType = classifyStorageStreamError(err)
	}

	r.mu.Lock()
	if r.terminated {
		r.mu.Unlock()
		return
	}
	if r.bytesValid {
		r.totalBytes, r.bytesValid = addStorageStreamBytes(r.totalBytes, n, capacity)
	}
	if outcome == "" {
		r.mu.Unlock()
		return
	}
	r.terminateLocked(outcome, errorType, false)
	completion := r.completionLocked()
	r.mu.Unlock()
	publishStorageStream(completion)
}

func (r *storageStreamRecorder) finish(outcome string, errorType string, suppressMetric bool) {
	r.mu.Lock()
	if r.terminated {
		r.mu.Unlock()
		return
	}
	r.terminateLocked(outcome, errorType, suppressMetric)
	completion := r.completionLocked()
	r.mu.Unlock()
	publishStorageStream(completion)
}

func (r *storageStreamRecorder) finishGoexit() {
	r.finish(OutcomeGoroutineExit, "", true)
}

func (r *storageStreamRecorder) unwrap() *storageStreamCompletion {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminated {
		return nil
	}
	if r.phase == storageStreamIdle {
		r.terminated = true
		return nil
	}
	r.terminateLocked(storageStreamOutcomeUnwrapped, "", false)
	return r.completionLocked()
}

func (r *storageStreamRecorder) terminateLocked(outcome string, errorType string, suppressMetric bool) {
	r.terminated = true
	r.terminalAt = time.Now()
	r.outcome = outcome
	r.errorType = errorType
	r.suppressMetric = suppressMetric
}

func (r *storageStreamRecorder) completionLocked() *storageStreamCompletion {
	if !r.terminated || r.phase != storageStreamStarted || r.published {
		return nil
	}
	r.published = true
	return &storageStreamCompletion{
		span:           r.span,
		context:        r.context,
		duration:       r.duration,
		bytes:          r.bytes,
		startedAt:      r.startedAt,
		endedAt:        r.terminalAt,
		outcome:        r.outcome,
		errorType:      r.errorType,
		suppressMetric: r.suppressMetric,
		totalBytes:     r.totalBytes,
		bytesValid:     r.bytesValid,
	}
}

func publishStorageStream(completion *storageStreamCompletion) {
	if completion == nil {
		return
	}
	if !nilInterface(completion.span) {
		attributes, admitted := storageStreamSpanAttributes(completion.outcome, completion.errorType)
		if admitted {
			if completion.errorType != "" || completion.outcome == OutcomeGoroutineExit {
				safeSetStatus(completion.span, codes.Error, "")
			}
			safeSetAttributes(completion.span, spanTerminalAttributes(attributes)...)
		}
		safeEnd(completion.span, trace.WithTimestamp(completion.endedAt))
	}
	if completion.suppressMetric {
		return
	}
	duration := completion.endedAt.Sub(completion.startedAt).Seconds()
	if !nilInterface(completion.duration) && duration >= 0 {
		attributes, admitted := storageStreamMetricAttributes(SignalStorageStreamDuration, completion.outcome, completion.errorType)
		if admitted {
			safeRecord(completion.duration, completion.context, duration, metric.WithAttributes(attributes...))
		}
	}
	if !nilInterface(completion.bytes) && completion.bytesValid {
		attributes, admitted := storageStreamMetricAttributes(SignalStorageStreamBytes, completion.outcome, completion.errorType)
		if admitted {
			safeRecordInt64(completion.bytes, completion.context, completion.totalBytes, metric.WithAttributes(attributes...))
		}
	}
}

func addStorageStreamBytes(total int64, n int, capacity int) (int64, bool) {
	if n < 0 || n > capacity || int64(n) > math.MaxInt64-total {
		return total, false
	}
	return total + int64(n), true
}

func classifyStorageStreamError(err error) (string, string) {
	_, errorType := safeClassifyError(classifyStorageError, err)
	return OutcomeError, errorType
}

func invokeStorageStreamRead(inner io.ReadCloser, p []byte) (n int, err error, panicked bool, panicValue any) {
	returned := false
	defer func() {
		if returned {
			return
		}
		panicValue = recover()
		panicked = true
	}()
	n, err = inner.Read(p)
	returned = true
	return n, err, false, nil
}

func invokeStorageStreamClose(inner io.ReadCloser) (err error, panicked bool, panicValue any) {
	returned := false
	defer func() {
		if returned {
			return
		}
		panicValue = recover()
		panicked = true
	}()
	err = inner.Close()
	returned = true
	return err, false, nil
}

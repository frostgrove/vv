package vvotel_test

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type recordedSpan struct {
	name       string
	kind       trace.SpanKind
	attributes map[attribute.Key]attribute.Value
	events     []string
	status     codes.Code
	ended      bool
	startTime  time.Time
	endTime    time.Time
}

type testTracerProvider struct {
	tracenoop.TracerProvider
	mu              sync.Mutex
	spans           []*recordedSpan
	panicTracer     bool
	panicStart      bool
	panicAttributes bool
	panicStatus     bool
	panicEnd        bool
	panicAddEvent   bool
	addEventCalls   int
	attributeCalls  int
	statusCalls     int
	endCalls        int
}

func newTestTracerProvider() *testTracerProvider {
	return &testTracerProvider{}
}

func (p *testTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	if p.panicTracer {
		panic("tracer creation failed")
	}
	return &testTracer{provider: p}
}

type testTracer struct {
	tracenoop.Tracer
	provider *testTracerProvider
}

type spanKey struct{}

func (t *testTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if t.provider.panicStart {
		panic("tracer start failed")
	}
	cfg := trace.NewSpanStartConfig(opts...)
	span := &testSpan{
		provider: t.provider,
		rec: &recordedSpan{
			name:       spanName,
			kind:       cfg.SpanKind(),
			attributes: make(map[attribute.Key]attribute.Value),
			startTime:  time.Now(),
		},
	}
	for _, kv := range cfg.Attributes() {
		span.rec.attributes[kv.Key] = kv.Value
	}

	t.provider.mu.Lock()
	t.provider.spans = append(t.provider.spans, span.rec)
	t.provider.mu.Unlock()

	ctx = trace.ContextWithSpan(ctx, span)
	return context.WithValue(ctx, spanKey{}, span), span
}

type testSpan struct {
	tracenoop.Span
	provider *testTracerProvider
	mu       sync.Mutex
	rec      *recordedSpan
}

func (s *testSpan) End(options ...trace.SpanEndOption) {
	s.provider.mu.Lock()
	s.provider.endCalls++
	s.provider.mu.Unlock()
	if s.provider.panicEnd {
		panic("span end failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec.ended = true
	s.rec.endTime = time.Now()
}

func (s *testSpan) AddEvent(name string, options ...trace.EventOption) {
	s.provider.mu.Lock()
	s.provider.addEventCalls++
	s.provider.mu.Unlock()
	if s.provider.panicAddEvent {
		panic("span event failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec.events = append(s.rec.events, name)
}

func (s *testSpan) IsRecording() bool {
	return true
}

func (s *testSpan) RecordError(err error, options ...trace.EventOption) {}

func (s *testSpan) SpanContext() trace.SpanContext {
	return trace.SpanContext{}
}

func (s *testSpan) SetStatus(code codes.Code, description string) {
	s.provider.mu.Lock()
	s.provider.statusCalls++
	s.provider.mu.Unlock()
	if s.provider.panicStatus {
		panic("span status failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec.status = code
}

func (s *testSpan) SetName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec.name = name
}

func (s *testSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.provider.mu.Lock()
	s.provider.attributeCalls++
	s.provider.mu.Unlock()
	if s.provider.panicAttributes {
		panic("span attributes failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, attr := range kv {
		s.rec.attributes[attr.Key] = attr.Value
	}
}

func (s *testSpan) TracerProvider() trace.TracerProvider {
	return nil
}

type recordedMetric struct {
	name       string
	value      any
	attributes map[attribute.Key]attribute.Value
}

type testMeterProvider struct {
	metricnoop.MeterProvider
	mu                   sync.Mutex
	metrics              []*recordedMetric
	meters               int
	histogramCreations   int
	counterCreations     int
	panicMeter           bool
	panicHistogramCreate bool
	panicCounterCreate   bool
	panicHistogramRecord bool
	panicCounterAdd      bool
	counterAddCalls      int
	histogramRecordCalls int
}

func newTestMeterProvider() *testMeterProvider {
	return &testMeterProvider{}
}

func (p *testMeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	if p.panicMeter {
		panic("meter creation failed")
	}
	p.mu.Lock()
	p.meters++
	p.mu.Unlock()
	return &testMeter{provider: p}
}

type testMeter struct {
	metricnoop.Meter
	provider *testMeterProvider
}

func (m *testMeter) Int64Counter(name string, options ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if m.provider.panicCounterCreate {
		panic("counter creation failed")
	}
	m.provider.mu.Lock()
	m.provider.counterCreations++
	m.provider.mu.Unlock()
	return &testCounter{name: name, provider: m.provider}, nil
}

func (m *testMeter) Float64Histogram(name string, options ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	if m.provider.panicHistogramCreate {
		panic("histogram creation failed")
	}
	m.provider.mu.Lock()
	m.provider.histogramCreations++
	m.provider.mu.Unlock()
	return &testHistogram{name: name, provider: m.provider}, nil
}

type testCounter struct {
	metricnoop.Int64Counter
	name     string
	provider *testMeterProvider
}

func (c *testCounter) Add(ctx context.Context, incr int64, options ...metric.AddOption) {
	c.provider.mu.Lock()
	c.provider.counterAddCalls++
	c.provider.mu.Unlock()
	if c.provider.panicCounterAdd {
		panic("counter add failed")
	}
	cfg := metric.NewAddConfig(options)
	attrs := make(map[attribute.Key]attribute.Value)
	set := cfg.Attributes()
	for _, kv := range set.ToSlice() {
		attrs[kv.Key] = kv.Value
	}
	c.provider.mu.Lock()
	c.provider.metrics = append(c.provider.metrics, &recordedMetric{
		name:       c.name,
		value:      incr,
		attributes: attrs,
	})
	c.provider.mu.Unlock()
}

func (p *testTracerProvider) eventCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addEventCalls
}

func (p *testMeterProvider) metricCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.metrics)
}

func (p *testMeterProvider) counterAdds() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counterAddCalls
}

func (p *testMeterProvider) hasMetricAttribute(key attribute.Key) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, metric := range p.metrics {
		if _, ok := metric.attributes[key]; ok {
			return true
		}
	}
	return false
}

type testHistogram struct {
	metricnoop.Float64Histogram
	name     string
	provider *testMeterProvider
}

func (h *testHistogram) Record(ctx context.Context, incr float64, options ...metric.RecordOption) {
	h.provider.mu.Lock()
	h.provider.histogramRecordCalls++
	h.provider.mu.Unlock()
	if h.provider.panicHistogramRecord {
		panic("histogram record failed")
	}
	cfg := metric.NewRecordConfig(options)
	attrs := make(map[attribute.Key]attribute.Value)
	set := cfg.Attributes()
	for _, kv := range set.ToSlice() {
		attrs[kv.Key] = kv.Value
	}
	h.provider.mu.Lock()
	h.provider.metrics = append(h.provider.metrics, &recordedMetric{
		name:       h.name,
		value:      incr,
		attributes: attrs,
	})
	h.provider.mu.Unlock()
}

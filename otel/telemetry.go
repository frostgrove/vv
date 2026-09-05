package vvotel

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"unicode"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	ErrNilConfig     = errors.New("vvotel: config is nil")
	ErrNilProvider   = errors.New("vvotel: tracer or meter provider is nil")
	ErrProviderPanic = errors.New("vvotel: provider panicked")
)

type Config struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider

	Disabled               bool
	CommandTracesDisabled  bool
	CommandMetricsDisabled bool
	StorageTracesDisabled  bool
	CacheMetricsDisabled   bool

	ResourceName string
}

const maxResourceNameBytes = 64

type Telemetry struct {
	config Config

	tracer trace.Tracer
	meter  metric.Meter

	commandDurationOnce sync.Once
	cacheOperationsOnce sync.Once
	resourceMu          sync.Mutex
	resourceNames       map[string]struct{}
	commandDuration     metric.Float64Histogram
	cacheOperations     metric.Int64Counter
}

func New(config Config) (*Telemetry, error) {
	if !config.Disabled {
		if nilInterface(config.TracerProvider) && nilInterface(config.MeterProvider) {
			return nil, fmt.Errorf("%w: at least one of TracerProvider or MeterProvider must be provided", ErrNilProvider)
		}
	}

	t := &Telemetry{
		config:        config,
		resourceNames: make(map[string]struct{}),
	}
	t.config.ResourceName = normalizeResourceName(config.ResourceName)
	if t.config.ResourceName != "" {
		t.resourceNames[t.config.ResourceName] = struct{}{}
	}

	if config.Disabled {
		return t, nil
	}

	if !nilInterface(config.TracerProvider) {
		tracer, panicked := providerTracer(config.TracerProvider,
			ScopeName,
			trace.WithInstrumentationVersion(ScopeVersion),
		)
		if panicked {
			return nil, ErrProviderPanic
		}
		t.tracer = tracer
	}
	if !nilInterface(config.MeterProvider) {
		meter, panicked := providerMeter(config.MeterProvider,
			ScopeName,
			metric.WithInstrumentationVersion(ScopeVersion),
		)
		if panicked {
			return nil, ErrProviderPanic
		}
		t.meter = meter
	}

	return t, nil
}

func (t *Telemetry) traceDisabled(isStorage bool) bool {
	if t.config.Disabled {
		return true
	}
	if isStorage {
		return t.config.StorageTracesDisabled
	}
	return t.config.CommandTracesDisabled
}

func (t *Telemetry) resourceName() string {
	return t.config.ResourceName
}

func (t *Telemetry) boundResourceName(name string) string {
	if t == nil {
		return name
	}
	name = normalizeResourceName(name)
	if name == "" {
		return ""
	}
	t.resourceMu.Lock()
	defer t.resourceMu.Unlock()
	if _, ok := t.resourceNames[name]; ok {
		return name
	}
	if len(t.resourceNames) >= MaxResourceNameValues {
		return ""
	}
	t.resourceNames[name] = struct{}{}
	return name
}

func normalizeResourceName(name string) string {
	if name == "" || len(name) > maxResourceNameBytes {
		return ""
	}
	for _, r := range name {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-') {
			return ""
		}
	}
	return name
}

func (t *Telemetry) commandDurationInstrument() metric.Float64Histogram {
	if t == nil || t.config.Disabled || t.config.CommandMetricsDisabled || nilInterface(t.meter) {
		return nil
	}
	t.commandDurationOnce.Do(func() {
		hist, err, panicked := createHistogram(t.meter)
		if err == nil && !panicked {
			t.commandDuration = hist
		}
	})
	return t.commandDuration
}

func (t *Telemetry) cacheOperationsInstrument() metric.Int64Counter {
	if t == nil || t.config.Disabled || t.config.CacheMetricsDisabled || nilInterface(t.meter) {
		return nil
	}
	t.cacheOperationsOnce.Do(func() {
		counter, err, panicked := createCounter(t.meter)
		if err == nil && !panicked {
			t.cacheOperations = counter
		}
	})
	return t.cacheOperations
}

func providerTracer(provider trace.TracerProvider, name string, options ...trace.TracerOption) (tracer trace.Tracer, panicked bool) {
	defer func() {
		if recover() != nil {
			tracer = nil
			panicked = true
		}
	}()
	tracer = provider.Tracer(name, options...)
	if nilInterface(tracer) {
		tracer = nil
	}
	return tracer, false
}

func providerMeter(provider metric.MeterProvider, name string, options ...metric.MeterOption) (meter metric.Meter, panicked bool) {
	defer func() {
		if recover() != nil {
			meter = nil
			panicked = true
		}
	}()
	meter = provider.Meter(name, options...)
	if nilInterface(meter) {
		meter = nil
	}
	return meter, false
}

func createHistogram(meter metric.Meter) (histogram metric.Float64Histogram, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			histogram = nil
			panicked = true
		}
	}()
	histogram, err = meter.Float64Histogram(
		MetricCommandDuration,
		metric.WithDescription(MetricCommandDurationDescription),
		metric.WithUnit(MetricCommandDurationUnit),
		metric.WithExplicitBucketBoundaries(defaultDurationBoundaries...),
	)
	if nilInterface(histogram) {
		histogram = nil
	}
	return histogram, err, false
}

func createCounter(meter metric.Meter) (counter metric.Int64Counter, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			counter = nil
			panicked = true
		}
	}()
	counter, err = meter.Int64Counter(
		MetricCacheOperations,
		metric.WithDescription(MetricCacheOperationsDescription),
		metric.WithUnit(MetricCacheOperationsUnit),
	)
	if nilInterface(counter) {
		counter = nil
	}
	return counter, err, false
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	valueOf := reflect.ValueOf(value)
	switch valueOf.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return valueOf.IsNil()
	default:
		return false
	}
}

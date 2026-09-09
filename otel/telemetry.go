package vvotel

import (
	"errors"
	"reflect"
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	ErrNilConfig       = errors.New("vvotel: config is nil")
	ErrNilProvider     = errors.New("vvotel: tracer or meter provider is nil")
	ErrProviderPanic   = errors.New("vvotel: provider panicked")
	ErrAssembly        = errors.New("vvotel: assembly failed")
	ErrInvalidSignal   = errors.New("vvotel: invalid signal")
	ErrDuplicateSignal = errors.New("vvotel: duplicate signal")
	ErrInstrument      = errors.New("vvotel: instrument construction failed")
	ErrNilInstrument   = errors.New("vvotel: instrument is nil")
)

type Config struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider

	Disabled bool

	CommandTracesDisabled  bool
	CommandMetricsDisabled bool
	StorageTracesDisabled  bool
	CacheMetricsDisabled   bool

	ResourceName ApprovedName
	Disable      Signals
}

type Telemetry struct {
	activeSignals map[Signal]struct{}

	tracer trace.Tracer
	meter  metric.Meter

	float64Histograms    map[Signal]metric.Float64Histogram
	int64Histograms      map[Signal]metric.Int64Histogram
	int64Counters        map[Signal]metric.Int64Counter
	int64ObservableGauge map[Signal]metric.Int64ObservableGauge

	configuredResourceName string
	resourceNames          *approvedNameBudget
}

type approvedNameBudget struct {
	mu    sync.Mutex
	names map[string]struct{}
}

func New(config Config) (*Telemetry, error) {
	descriptors := descriptorsBySignalID()
	disabled, err := validateDisabledSignals(config.Disable, descriptors)
	if err != nil {
		return nil, err
	}

	t := newTelemetry(config.ResourceName)
	if config.Disabled {
		return t, nil
	}
	if nilInterface(config.TracerProvider) && nilInterface(config.MeterProvider) {
		return nil, newProviderError(ErrNilProvider, "")
	}

	applyLegacyDisable(disabled, config)
	if err := t.selectActiveSignals(descriptors, disabled, config); err != nil {
		return nil, err
	}

	if t.needsProvider(descriptors, "tracer") {
		tracer, panicked := providerTracer(config.TracerProvider,
			ScopeName,
			trace.WithInstrumentationVersion(ScopeVersion),
		)
		if panicked {
			return nil, newProviderError(ErrProviderPanic, "tracer")
		}
		if nilInterface(tracer) {
			return nil, newProviderError(ErrNilProvider, "tracer")
		}
		t.tracer = tracer
	}

	if t.needsProvider(descriptors, "meter") {
		meter, panicked := providerMeter(config.MeterProvider,
			ScopeName,
			metric.WithInstrumentationVersion(ScopeVersion),
		)
		if panicked {
			return nil, newProviderError(ErrProviderPanic, "meter")
		}
		if nilInterface(meter) {
			return nil, newProviderError(ErrNilProvider, "meter")
		}
		t.meter = meter
		if err := t.constructMetrics(descriptors); err != nil {
			return nil, err
		}
	}

	return t, nil
}

func Must(config Config) *Telemetry {
	t, err := New(config)
	if err != nil {
		panic(err)
	}
	return t
}

func newTelemetry(resourceName ApprovedName) *Telemetry {
	name := normalizeResourceName(resourceName.Value())
	t := &Telemetry{
		activeSignals:          make(map[Signal]struct{}),
		float64Histograms:      make(map[Signal]metric.Float64Histogram),
		int64Histograms:        make(map[Signal]metric.Int64Histogram),
		int64Counters:          make(map[Signal]metric.Int64Counter),
		int64ObservableGauge:   make(map[Signal]metric.Int64ObservableGauge),
		configuredResourceName: name,
		resourceNames: &approvedNameBudget{
			names: make(map[string]struct{}),
		},
	}
	return t
}

func (t *Telemetry) signalEnabled(signal Signal) bool {
	if t == nil {
		return false
	}
	_, ok := t.activeSignals[signal]
	return ok
}

func (t *Telemetry) resourceName() ApprovedName {
	if t == nil {
		return ""
	}
	return ApprovedName(t.configuredResourceName)
}

func (t *Telemetry) boundResourceName(name ApprovedName) string {
	if t == nil {
		return normalizeResourceName(name.Value())
	}
	normalized := normalizeResourceName(name.Value())
	if normalized == "" || t.resourceNames == nil {
		return ""
	}
	t.resourceNames.mu.Lock()
	defer t.resourceNames.mu.Unlock()
	if _, ok := t.resourceNames.names[normalized]; ok {
		return normalized
	}
	if len(t.resourceNames.names) >= MaxResourceNameValues {
		return ""
	}
	t.resourceNames.names[normalized] = struct{}{}
	return normalized
}

func normalizeResourceName(name string) string {
	if !ValidResourceName(name) {
		return ""
	}
	return name
}

func (t *Telemetry) float64Histogram(signal Signal) metric.Float64Histogram {
	if t == nil {
		return nil
	}
	return t.float64Histograms[signal]
}

func (t *Telemetry) int64Histogram(signal Signal) metric.Int64Histogram {
	if t == nil {
		return nil
	}
	return t.int64Histograms[signal]
}

func (t *Telemetry) int64Counter(signal Signal) metric.Int64Counter {
	if t == nil {
		return nil
	}
	return t.int64Counters[signal]
}

func providerTracer(provider trace.TracerProvider, name string, options ...trace.TracerOption) (tracer trace.Tracer, panicked bool) {
	completed := false
	defer func() {
		if !completed {
			_ = recover()
			tracer = nil
			panicked = true
		}
	}()
	tracer = provider.Tracer(name, options...)
	completed = true
	return tracer, false
}

func providerMeter(provider metric.MeterProvider, name string, options ...metric.MeterOption) (meter metric.Meter, panicked bool) {
	completed := false
	defer func() {
		if !completed {
			_ = recover()
			meter = nil
			panicked = true
		}
	}()
	meter = provider.Meter(name, options...)
	completed = true
	return meter, false
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

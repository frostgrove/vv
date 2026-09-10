package vvotel_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/storage"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type assemblyOrder struct {
	mu    sync.Mutex
	items []string
}

func (o *assemblyOrder) add(item string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.items = append(o.items, item)
	o.mu.Unlock()
}

func (o *assemblyOrder) snapshot() []string {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.items...)
}

type assemblyTracerProvider struct {
	tracenoop.TracerProvider
	order       *assemblyOrder
	calls       int
	scope       string
	version     string
	mode        string
	panicObject any
}

func (p *assemblyTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	p.calls++
	p.scope = name
	config := trace.NewTracerConfig(options...)
	p.version = config.InstrumentationVersion()
	p.order.add("tracer")
	switch p.mode {
	case "nil":
		return nil
	case "typed_nil":
		var tracer *typedNilTracer
		return tracer
	case "panic":
		panic("tracer secret")
	case "panic_nil":
		panic(nil)
	case "hostile_panic":
		panic(p.panicObject)
	default:
		return p.TracerProvider.Tracer(name, options...)
	}
}

type typedNilTracer struct {
	trace.Tracer
}

type metricConstructorCall struct {
	signal      vvotel.Signal
	apiKind     string
	name        string
	description string
	unit        string
	boundaries  []float64
	callbacks   int
}

type assemblyMeterProvider struct {
	metricnoop.MeterProvider
	order       *assemblyOrder
	calls       int
	scope       string
	version     string
	mode        string
	panicObject any
	meter       *assemblyMeter
}

func newAssemblyMeterProvider() *assemblyMeterProvider {
	base := metricnoop.NewMeterProvider().Meter("assembly-test")
	return &assemblyMeterProvider{meter: &assemblyMeter{Meter: base}}
}

func (p *assemblyMeterProvider) Meter(name string, options ...metric.MeterOption) metric.Meter {
	p.calls++
	p.scope = name
	config := metric.NewMeterConfig(options...)
	p.version = config.InstrumentationVersion()
	p.order.add("meter")
	switch p.mode {
	case "nil":
		return nil
	case "typed_nil":
		var meter *typedNilMeter
		return meter
	case "panic":
		panic("meter secret")
	case "panic_nil":
		panic(nil)
	case "hostile_panic":
		panic(p.panicObject)
	default:
		return p.meter
	}
}

type typedNilMeter struct {
	metric.Meter
}

type assemblyMeter struct {
	metric.Meter
	mu                sync.Mutex
	calls             []metricConstructorCall
	faultSignal       vvotel.Signal
	faultMode         string
	faultCause        error
	panicObject       any
	registerCallbacks int
}

func (m *assemblyMeter) constructor(apiKind, name, description, unit string, boundaries []float64, callbacks int) (vvotel.Signal, string, error, any) {
	signal := metricSignalByName(name)
	m.mu.Lock()
	m.calls = append(m.calls, metricConstructorCall{
		signal:      signal,
		apiKind:     apiKind,
		name:        name,
		description: description,
		unit:        unit,
		boundaries:  append([]float64(nil), boundaries...),
		callbacks:   callbacks,
	})
	mode := ""
	var cause error
	var panicObject any
	if signal == m.faultSignal {
		mode = m.faultMode
		cause = m.faultCause
		panicObject = m.panicObject
	}
	m.mu.Unlock()
	return signal, mode, cause, panicObject
}

func (m *assemblyMeter) callsSnapshot() []metricConstructorCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]metricConstructorCall, len(m.calls))
	copy(result, m.calls)
	for i := range result {
		result[i].boundaries = append([]float64(nil), result[i].boundaries...)
	}
	return result
}

func (m *assemblyMeter) Float64Histogram(name string, options ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	config := metric.NewFloat64HistogramConfig(options...)
	_, mode, cause, panicObject := m.constructor("float64_histogram", name, config.Description(), config.Unit(), config.ExplicitBucketBoundaries(), 0)
	switch mode {
	case "error":
		return nil, cause
	case "nil":
		return nil, nil
	case "typed_nil":
		var instrument *typedNilFloat64Histogram
		return instrument, nil
	case "panic":
		panic("instrument secret")
	case "panic_nil":
		panic(nil)
	case "hostile_panic":
		panic(panicObject)
	default:
		return m.Meter.Float64Histogram(name, options...)
	}
}

func (m *assemblyMeter) Int64Histogram(name string, options ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	config := metric.NewInt64HistogramConfig(options...)
	_, mode, cause, panicObject := m.constructor("int64_histogram", name, config.Description(), config.Unit(), config.ExplicitBucketBoundaries(), 0)
	switch mode {
	case "error":
		return nil, cause
	case "nil":
		return nil, nil
	case "typed_nil":
		var instrument *typedNilInt64Histogram
		return instrument, nil
	case "panic":
		panic("instrument secret")
	case "panic_nil":
		panic(nil)
	case "hostile_panic":
		panic(panicObject)
	default:
		return m.Meter.Int64Histogram(name, options...)
	}
}

func (m *assemblyMeter) Int64Counter(name string, options ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	config := metric.NewInt64CounterConfig(options...)
	_, mode, cause, panicObject := m.constructor("int64_counter", name, config.Description(), config.Unit(), nil, 0)
	switch mode {
	case "error":
		return nil, cause
	case "nil":
		return nil, nil
	case "typed_nil":
		var instrument *typedNilInt64Counter
		return instrument, nil
	case "panic":
		panic("instrument secret")
	case "panic_nil":
		panic(nil)
	case "hostile_panic":
		panic(panicObject)
	default:
		return m.Meter.Int64Counter(name, options...)
	}
}

func (m *assemblyMeter) Int64ObservableGauge(name string, options ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	config := metric.NewInt64ObservableGaugeConfig(options...)
	_, mode, cause, panicObject := m.constructor("int64_observable_gauge", name, config.Description(), config.Unit(), nil, len(config.Callbacks()))
	switch mode {
	case "error":
		return nil, cause
	case "nil":
		return nil, nil
	case "typed_nil":
		var instrument *typedNilInt64ObservableGauge
		return instrument, nil
	case "panic":
		panic("instrument secret")
	case "panic_nil":
		panic(nil)
	case "hostile_panic":
		panic(panicObject)
	default:
		return m.Meter.Int64ObservableGauge(name, options...)
	}
}

func (m *assemblyMeter) RegisterCallback(callback metric.Callback, instruments ...metric.Observable) (metric.Registration, error) {
	m.mu.Lock()
	m.registerCallbacks++
	m.mu.Unlock()
	return m.Meter.RegisterCallback(callback, instruments...)
}

type typedNilFloat64Histogram struct {
	metric.Float64Histogram
}

type typedNilInt64Histogram struct {
	metric.Int64Histogram
}

type typedNilInt64Counter struct {
	metric.Int64Counter
}

type typedNilInt64ObservableGauge struct {
	metric.Int64ObservableGauge
}

type hostileValue struct {
	secret string
	used   *bool
}

func (v *hostileValue) mark() {
	*v.used = true
	panic("hostile formatter invoked")
}

func (v *hostileValue) Error() string {
	v.mark()
	return ""
}

func (v *hostileValue) String() string {
	v.mark()
	return ""
}

func (v *hostileValue) GoString() string {
	v.mark()
	return ""
}

func (v *hostileValue) Format(fmt.State, rune) {
	v.mark()
}

func metricDescriptors() []vvotel.SignalDescriptor {
	var descriptors []vvotel.SignalDescriptor
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.Provider == "meter" {
			descriptors = append(descriptors, descriptor)
		}
	}
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].SignalID < descriptors[j].SignalID
	})
	return descriptors
}

func providerSignals(provider string) vvotel.Signals {
	var result vvotel.Signals
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.Provider == provider {
			result = append(result, descriptor.SignalID)
		}
	}
	return result
}

func metricSignalByName(name string) vvotel.Signal {
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.Provider == "meter" && descriptor.Name == name {
			return descriptor.SignalID
		}
	}
	return 0
}

func TestAssembly_RegistryRosterAndEagerConstructorOptionsAreExact(t *testing.T) {
	descriptors := vvotel.SignalDescriptors()
	providers := map[string]int{}
	apiKinds := map[string]int{}
	for _, descriptor := range descriptors {
		providers[descriptor.Provider]++
		if descriptor.Provider == "meter" {
			apiKinds[descriptor.APIKind]++
		}
	}
	if len(descriptors) != 59 || providers["meter"] != 45 || providers["tracer"] != 11 || providers["context_only"] != 3 {
		t.Fatalf("registry roster changed: total=%d providers=%v", len(descriptors), providers)
	}
	wantKinds := map[string]int{
		"float64_histogram":      14,
		"int64_histogram":        13,
		"int64_counter":          12,
		"int64_observable_gauge": 6,
	}
	if !reflect.DeepEqual(apiKinds, wantKinds) {
		t.Fatalf("metric API roster=%v, want %v", apiKinds, wantKinds)
	}

	order := &assemblyOrder{}
	tp := &assemblyTracerProvider{order: order}
	mp := newAssemblyMeterProvider()
	mp.order = order
	if _, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp}); err != nil {
		t.Fatal(err)
	}
	if tp.calls != 1 || mp.calls != 1 {
		t.Fatalf("provider getters: tracer=%d meter=%d, want one each", tp.calls, mp.calls)
	}
	if got := order.snapshot(); !reflect.DeepEqual(got, []string{"tracer", "meter"}) {
		t.Fatalf("provider order=%v, want tracer then meter", got)
	}
	if tp.scope != vvotel.ScopeName || tp.version != vvotel.ScopeVersion || mp.scope != vvotel.ScopeName || mp.version != vvotel.ScopeVersion {
		t.Fatalf("scope/version mismatch: tracer=%q/%q meter=%q/%q", tp.scope, tp.version, mp.scope, mp.version)
	}
	calls := mp.meter.callsSnapshot()
	wantDescriptors := metricDescriptors()
	if len(calls) != 45 {
		t.Fatalf("constructors=%d, want 45", len(calls))
	}
	for i, call := range calls {
		want := wantDescriptors[i]
		if call.signal != want.SignalID || call.apiKind != want.APIKind || call.name != want.Name || call.description != want.Description || call.unit != want.Unit {
			t.Fatalf("constructor %d=%+v, want signal=%d api=%q name=%q description=%q unit=%q", i, call, want.SignalID, want.APIKind, want.Name, want.Description, want.Unit)
		}
		if !slices.Equal(call.boundaries, want.Boundaries) {
			t.Fatalf("constructor %d boundaries=%v, want %v", i, call.boundaries, want.Boundaries)
		}
		if call.callbacks != 0 {
			t.Fatalf("constructor %d registered %d callbacks", i, call.callbacks)
		}
		if i > 0 && calls[i-1].signal >= call.signal {
			t.Fatalf("constructor order is not increasing: %d then %d", calls[i-1].signal, call.signal)
		}
	}
	if mp.meter.registerCallbacks != 0 {
		t.Fatalf("assembly registered %d callbacks", mp.meter.registerCallbacks)
	}
}

func TestAssembly_ExplicitDisableValidationPrecedesDisabledAndProviders(t *testing.T) {
	tests := []struct {
		name       string
		disable    vvotel.Signals
		want       error
		wantSignal vvotel.Signal
	}{
		{name: "unknown_first", disable: vvotel.Signals{60000, vvotel.SignalCommandSpan, vvotel.SignalCommandSpan}, want: vvotel.ErrInvalidSignal, wantSignal: 60000},
		{name: "duplicate_before_unknown", disable: vvotel.Signals{vvotel.SignalCommandSpan, vvotel.SignalCommandSpan, 60000}, want: vvotel.ErrDuplicateSignal, wantSignal: vvotel.SignalCommandSpan},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			order := &assemblyOrder{}
			tp := &assemblyTracerProvider{order: order, mode: "panic"}
			mp := newAssemblyMeterProvider()
			mp.order = order
			mp.mode = "panic"
			_, err := vvotel.New(vvotel.Config{Disabled: true, Disable: tc.disable, TracerProvider: tp, MeterProvider: mp})
			if !errors.Is(err, vvotel.ErrAssembly) || !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want assembly and %v", err, tc.want)
			}
			var assemblyErr *vvotel.AssemblyError
			if !errors.As(err, &assemblyErr) || assemblyErr.Signal() != tc.wantSignal || assemblyErr.Provider() != "" {
				t.Fatalf("typed error=%#v, want signal=%d provider empty", assemblyErr, tc.wantSignal)
			}
			if len(order.snapshot()) != 0 {
				t.Fatalf("validation touched providers: %v", order.snapshot())
			}
		})
	}
}

func TestAssembly_DisabledAndProviderFamilyMatrix(t *testing.T) {
	t.Run("master_disabled", func(t *testing.T) {
		tp := &assemblyTracerProvider{mode: "panic"}
		mp := newAssemblyMeterProvider()
		mp.mode = "panic"
		tel, err := vvotel.New(vvotel.Config{Disabled: true, TracerProvider: tp, MeterProvider: mp})
		if err != nil || tel == nil || tp.calls != 0 || mp.calls != 0 {
			t.Fatalf("disabled assembly touched providers: tel=%v err=%v tracer=%d meter=%d", tel != nil, err, tp.calls, mp.calls)
		}
	})

	t.Run("missing_both", func(t *testing.T) {
		_, err := vvotel.New(vvotel.Config{Disable: vvotel.AllSignals()})
		if !errors.Is(err, vvotel.ErrAssembly) || !errors.Is(err, vvotel.ErrNilProvider) {
			t.Fatalf("error=%v, want ErrAssembly and ErrNilProvider", err)
		}
	})

	t.Run("trace_only", func(t *testing.T) {
		tp := &assemblyTracerProvider{}
		tel, err := vvotel.New(vvotel.Config{TracerProvider: tp})
		if err != nil || tel == nil || tp.calls != 1 {
			t.Fatalf("trace-only assembly: tel=%v err=%v calls=%d", tel != nil, err, tp.calls)
		}
	})

	t.Run("metric_only", func(t *testing.T) {
		mp := newAssemblyMeterProvider()
		tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
		if err != nil || tel == nil || mp.calls != 1 || len(mp.meter.callsSnapshot()) != 45 {
			t.Fatalf("metric-only assembly: tel=%v err=%v getter=%d constructors=%d", tel != nil, err, mp.calls, len(mp.meter.callsSnapshot()))
		}
	})

	t.Run("typed_nil_tracer_with_meter", func(t *testing.T) {
		var tp *assemblyTracerProvider
		mp := newAssemblyMeterProvider()
		tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
		if err != nil || tel == nil || mp.calls != 1 {
			t.Fatalf("typed-nil tracer blocked meter family: tel=%v err=%v meter=%d", tel != nil, err, mp.calls)
		}
	})

	t.Run("typed_nil_meter_with_tracer", func(t *testing.T) {
		tp := &assemblyTracerProvider{}
		var mp *assemblyMeterProvider
		tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
		if err != nil || tel == nil || tp.calls != 1 {
			t.Fatalf("typed-nil meter blocked tracer family: tel=%v err=%v tracer=%d", tel != nil, err, tp.calls)
		}
	})

	t.Run("no_selected_family_getter", func(t *testing.T) {
		tp := &assemblyTracerProvider{mode: "panic"}
		mp := newAssemblyMeterProvider()
		mp.mode = "panic"
		disable := append(providerSignals("tracer"), providerSignals("meter")...)
		tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp, Disable: disable})
		if err != nil || tel == nil || tp.calls != 0 || mp.calls != 0 {
			t.Fatalf("unselected families touched providers: tel=%v err=%v tracer=%d meter=%d", tel != nil, err, tp.calls, mp.calls)
		}
	})

	t.Run("only_tracer_family_unselected", func(t *testing.T) {
		tp := &assemblyTracerProvider{mode: "panic"}
		mp := newAssemblyMeterProvider()
		_, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp, Disable: providerSignals("tracer")})
		if err != nil || tp.calls != 0 || mp.calls != 1 {
			t.Fatalf("provider getter selection: err=%v tracer=%d meter=%d", err, tp.calls, mp.calls)
		}
	})

	t.Run("only_meter_family_unselected", func(t *testing.T) {
		tp := &assemblyTracerProvider{}
		mp := newAssemblyMeterProvider()
		mp.mode = "panic"
		_, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp, Disable: providerSignals("meter")})
		if err != nil || tp.calls != 1 || mp.calls != 0 {
			t.Fatalf("provider getter selection: err=%v tracer=%d meter=%d", err, tp.calls, mp.calls)
		}
	})
}

func TestAssembly_ProviderFailuresAreTypedRedactedAndOrdered(t *testing.T) {
	used := false
	hostile := &hostileValue{secret: "provider-secret", used: &used}
	tests := []struct {
		name     string
		provider string
		mode     string
	}{
		{name: "tracer_nil", provider: "tracer", mode: "nil"},
		{name: "tracer_typed_nil", provider: "tracer", mode: "typed_nil"},
		{name: "tracer_panic", provider: "tracer", mode: "panic"},
		{name: "tracer_panic_nil", provider: "tracer", mode: "panic_nil"},
		{name: "tracer_hostile_panic", provider: "tracer", mode: "hostile_panic"},
		{name: "meter_nil", provider: "meter", mode: "nil"},
		{name: "meter_typed_nil", provider: "meter", mode: "typed_nil"},
		{name: "meter_panic", provider: "meter", mode: "panic"},
		{name: "meter_panic_nil", provider: "meter", mode: "panic_nil"},
		{name: "meter_hostile_panic", provider: "meter", mode: "hostile_panic"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			used = false
			order := &assemblyOrder{}
			tp := &assemblyTracerProvider{order: order}
			mp := newAssemblyMeterProvider()
			mp.order = order
			if tc.provider == "tracer" {
				tp.mode = tc.mode
				tp.panicObject = hostile
			} else {
				mp.mode = tc.mode
				mp.panicObject = hostile
			}
			_, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
			wantKind := vvotel.ErrNilProvider
			if strings.Contains(tc.mode, "panic") {
				wantKind = vvotel.ErrProviderPanic
			}
			if !errors.Is(err, vvotel.ErrAssembly) || !errors.Is(err, wantKind) || errors.Is(err, vvotel.ErrInstrument) {
				t.Fatalf("error=%v, want assembly/provider classification", err)
			}
			var assemblyErr *vvotel.AssemblyError
			if !errors.As(err, &assemblyErr) || assemblyErr.Signal() != 0 || assemblyErr.Provider() != tc.provider || assemblyErr.SignalName() != "" || assemblyErr.Unwrap() != nil {
				t.Fatalf("typed error accessors mismatch: %#v", assemblyErr)
			}
			assertAssemblyErrorRedacted(t, assemblyErr, "provider-secret")
			if used {
				t.Fatal("hostile panic formatter was invoked")
			}
			if tc.provider == "tracer" && !reflect.DeepEqual(order.snapshot(), []string{"tracer"}) {
				t.Fatalf("meter getter ran after tracer failure: %v", order.snapshot())
			}
			if tc.provider == "meter" && !reflect.DeepEqual(order.snapshot(), []string{"tracer", "meter"}) {
				t.Fatalf("provider order=%v, want tracer then meter", order.snapshot())
			}
		})
	}
}

func TestAssembly_EveryMetricConstructorFailureStopsAtTheSignal(t *testing.T) {
	for descriptorIndex, descriptor := range metricDescriptors() {
		descriptorIndex := descriptorIndex
		descriptor := descriptor
		for _, mode := range []string{"error", "panic", "nil"} {
			mode := mode
			t.Run(fmt.Sprintf("%03d_%s", descriptor.SignalID, mode), func(t *testing.T) {
				cause := errors.New("constructor-secret")
				mp := newAssemblyMeterProvider()
				mp.meter.faultSignal = descriptor.SignalID
				mp.meter.faultMode = mode
				mp.meter.faultCause = cause
				_, err := vvotel.New(vvotel.Config{MeterProvider: mp})
				if !errors.Is(err, vvotel.ErrAssembly) || !errors.Is(err, vvotel.ErrInstrument) {
					t.Fatalf("error=%v, want assembly instrument failure", err)
				}
				if mode == "panic" && !errors.Is(err, vvotel.ErrProviderPanic) {
					t.Fatalf("panic error=%v, want ErrProviderPanic", err)
				}
				if mode == "nil" && !errors.Is(err, vvotel.ErrNilInstrument) {
					t.Fatalf("nil error=%v, want ErrNilInstrument", err)
				}
				var assemblyErr *vvotel.AssemblyError
				if !errors.As(err, &assemblyErr) || assemblyErr.Signal() != descriptor.SignalID || assemblyErr.Provider() != "meter" || assemblyErr.SignalName() != descriptor.Name {
					t.Fatalf("typed error accessors mismatch: %#v", assemblyErr)
				}
				if mode == "error" {
					if assemblyErr.Unwrap() != cause || !errors.Is(err, cause) {
						t.Fatal("constructor cause was not preserved exactly")
					}
				} else if assemblyErr.Unwrap() != nil || errors.Is(err, cause) {
					t.Fatal("non-error constructor failure retained a cause")
				}
				calls := mp.meter.callsSnapshot()
				if len(calls) != descriptorIndex+1 || calls[len(calls)-1].signal != descriptor.SignalID {
					t.Fatalf("construction did not stop at signal %d: %v", descriptor.SignalID, calls)
				}
				for _, call := range calls {
					if call.signal > descriptor.SignalID {
						t.Fatalf("later signal %d constructed after failure at %d", call.signal, descriptor.SignalID)
					}
				}
				assertAssemblyErrorRedacted(t, assemblyErr, "constructor-secret")
			})
		}
	}
}

func TestAssembly_ReturnedConstructorCauseIsNeverFormatted(t *testing.T) {
	used := false
	cause := &hostileValue{secret: "cause-secret", used: &used}
	mp := newAssemblyMeterProvider()
	first := metricDescriptors()[0]
	mp.meter.faultSignal = first.SignalID
	mp.meter.faultMode = "error"
	mp.meter.faultCause = cause
	_, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	var assemblyErr *vvotel.AssemblyError
	if !errors.As(err, &assemblyErr) || assemblyErr.Unwrap() != cause || !errors.Is(err, cause) {
		t.Fatalf("constructor cause was not retained exactly: %#v", err)
	}
	assertAssemblyErrorRedacted(t, assemblyErr, "cause-secret")
	if used {
		t.Fatal("constructor cause formatter was invoked")
	}
}

func TestAssembly_ConstructorTypedNilPanicNilAndHostilePanicAreContained(t *testing.T) {
	first := metricDescriptors()[0]
	used := false
	hostile := &hostileValue{secret: "panic-secret", used: &used}
	for _, mode := range []string{"typed_nil", "panic_nil", "hostile_panic"} {
		t.Run(mode, func(t *testing.T) {
			used = false
			mp := newAssemblyMeterProvider()
			mp.meter.faultSignal = first.SignalID
			mp.meter.faultMode = mode
			mp.meter.panicObject = hostile
			_, err := vvotel.New(vvotel.Config{MeterProvider: mp})
			want := vvotel.ErrProviderPanic
			if mode == "typed_nil" {
				want = vvotel.ErrNilInstrument
			}
			if !errors.Is(err, vvotel.ErrAssembly) || !errors.Is(err, vvotel.ErrInstrument) || !errors.Is(err, want) {
				t.Fatalf("error=%v, want assembly/instrument/%v", err, want)
			}
			var assemblyErr *vvotel.AssemblyError
			if !errors.As(err, &assemblyErr) || assemblyErr.Unwrap() != nil {
				t.Fatalf("unexpected error=%#v", err)
			}
			assertAssemblyErrorRedacted(t, assemblyErr, "panic-secret")
			if used {
				t.Fatal("hostile panic formatter was invoked")
			}
		})
	}
}

func assertAssemblyErrorRedacted(t *testing.T, err *vvotel.AssemblyError, forbidden string) {
	t.Helper()
	formats := []string{"%v", "%+v", "%#v", "%s", "%q"}
	for _, format := range formats {
		formatted := fmt.Sprintf(format, err)
		if strings.Contains(formatted, forbidden) {
			t.Fatalf("format %q exposed %q in %q", format, forbidden, formatted)
		}
	}
	if strings.Contains(err.GoString(), forbidden) || strings.Contains(err.Error(), forbidden) {
		t.Fatalf("error formatting exposed %q", forbidden)
	}
}

func TestAssembly_ExplicitDisableIsCopiedAndLegacyUnionIsIdempotent(t *testing.T) {
	disable := vvotel.Signals{vvotel.SignalCommandSpan}
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{
		TracerProvider:        tp,
		MeterProvider:         mp,
		Disable:               disable,
		CommandTracesDisabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	disable[0] = vvotel.SignalCommandDuration
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{})
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	if len(tp.spans) != 0 {
		t.Fatalf("copied disabled command span emitted %d spans", len(tp.spans))
	}
	if len(mp.metrics) != 1 || mp.metrics[0].name != vvotel.MetricCommandDuration {
		t.Fatalf("caller mutation disabled a metric after assembly: %+v", mp.metrics)
	}
}

func TestAssembly_LegacyAliasesMapToExactSignalSets(t *testing.T) {
	cacheLegacy := vvotel.Signals{
		vvotel.SignalCacheOperations,
		vvotel.SignalCacheEvents,
		vvotel.SignalCacheItems,
		vvotel.SignalCacheEncodedBytes,
		vvotel.SignalCachePayloadBytes,
		vvotel.SignalCacheValueBytes,
		vvotel.SignalCacheChargedBytes,
		vvotel.SignalCacheMemoryEntries,
		vvotel.SignalCacheMemoryBytes,
		vvotel.SignalCacheMemoryEntryLimit,
		vvotel.SignalCacheMemoryByteLimit,
		vvotel.SignalCacheMemoryActive,
		vvotel.SignalCacheMemoryClosed,
	}
	tests := []struct {
		name        string
		configure   func(*vvotel.Config)
		disabled    vvotel.Signals
		notDisabled vvotel.Signals
	}{
		{name: "command_traces", configure: func(c *vvotel.Config) { c.CommandTracesDisabled = true }, disabled: vvotel.Signals{vvotel.SignalCommandSpan}, notDisabled: vvotel.Signals{vvotel.SignalStorageSpan}},
		{name: "command_metrics", configure: func(c *vvotel.Config) { c.CommandMetricsDisabled = true }, disabled: vvotel.Signals{vvotel.SignalCommandDuration}, notDisabled: vvotel.Signals{vvotel.SignalCacheOperations}},
		{name: "storage_traces", configure: func(c *vvotel.Config) { c.StorageTracesDisabled = true }, disabled: vvotel.Signals{vvotel.SignalStorageSpan, vvotel.SignalStorageStreamSpan}, notDisabled: vvotel.Signals{vvotel.SignalCommandSpan}},
		{name: "cache_metrics", configure: func(c *vvotel.Config) { c.CacheMetricsDisabled = true }, disabled: cacheLegacy, notDisabled: vvotel.Signals{vvotel.SignalCacheEvent, vvotel.SignalCacheBackendEvent}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mp := newAssemblyMeterProvider()
			tp := newTestTracerProvider()
			config := vvotel.Config{TracerProvider: tp, MeterProvider: mp}
			tc.configure(&config)
			tel, err := vvotel.New(config)
			if err != nil {
				t.Fatal(err)
			}
			constructed := map[vvotel.Signal]bool{}
			for _, call := range mp.meter.callsSnapshot() {
				constructed[call.signal] = true
			}
			for _, signal := range tc.disabled {
				if constructed[signal] {
					t.Fatalf("legacy alias left metric signal %d constructed", signal)
				}
			}
			for _, signal := range tc.notDisabled {
				if isMetricSignal(signal) && !constructed[signal] {
					t.Fatalf("legacy alias disabled unrelated metric signal %d", signal)
				}
			}
			assertCurrentSignalsForLegacyCase(t, tel, tp, tc.disabled)
		})
	}
}

func isMetricSignal(signal vvotel.Signal) bool {
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.SignalID == signal {
			return descriptor.Provider == "meter"
		}
	}
	return false
}

func assertCurrentSignalsForLegacyCase(t *testing.T, tel *vvotel.Telemetry, tp *testTracerProvider, disabled vvotel.Signals) {
	t.Helper()
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{})
	_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	store := vvotel.Store(tel)(&fakeStorageStore{})
	key, _ := storage.ParseKey("a/b")
	_, _ = store.Head(context.Background(), key)
	ctx, parent := tp.Tracer("ambient").Start(context.Background(), "ambient")
	vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true)).Observe(ctx, cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome})
	vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true)).Observe(ctx, cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.HitOutcome})
	parent.End()
	spanNames := map[string]int{}
	events := map[string]int{}
	for _, span := range tp.spans {
		spanNames[span.name]++
		for _, event := range span.events {
			events[event]++
		}
	}
	assertPresentUnlessDisabled(t, disabledSignal(disabled, vvotel.SignalCommandSpan), vvotel.SignalCommandSpan, spanNames["vv.command get"])
	assertPresentUnlessDisabled(t, disabledSignal(disabled, vvotel.SignalStorageSpan), vvotel.SignalStorageSpan, spanNames["vv.storage head"])
	if events[vvotel.EventCache] != 1 || events[vvotel.EventCacheBackend] != 1 {
		t.Fatalf("legacy aliases changed context-only events: %v", events)
	}
}

func disabledSignal(disabled vvotel.Signals, signal vvotel.Signal) vvotel.Signal {
	if disabled.Has(signal) {
		return signal
	}
	return 0
}

func TestAssembly_ContextOnlyEventsRemainActiveWithoutTracerProvider(t *testing.T) {
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	tp := newTestTracerProvider()
	ctx, span := tp.Tracer("ambient").Start(context.Background(), "ambient")
	vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true)).Observe(ctx, cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome})
	vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true)).Observe(ctx, cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.HitOutcome})
	span.End()
	if !reflect.DeepEqual(tp.spans[0].events, []string{vvotel.EventCache, vvotel.EventCacheBackend}) {
		t.Fatalf("context-only events=%v", tp.spans[0].events)
	}
}

func TestAssembly_EmittedSignalsDisableIndependently(t *testing.T) {
	current := vvotel.Signals{
		vvotel.SignalCommandDuration,
		vvotel.SignalCommandSpan,
		vvotel.SignalStorageDuration,
		vvotel.SignalStorageSpan,
		vvotel.SignalCacheOperations,
		vvotel.SignalCacheEvent,
		vvotel.SignalCacheBackendEvent,
		vvotel.SignalCacheEvents,
		vvotel.SignalCacheItems,
		vvotel.SignalCacheEncodedBytes,
		vvotel.SignalCachePayloadBytes,
		vvotel.SignalCacheValueBytes,
		vvotel.SignalCacheChargedBytes,
	}
	for _, disabled := range current {
		t.Run(fmt.Sprintf("signal_%d", disabled), func(t *testing.T) {
			tp := newTestTracerProvider()
			mp := newTestMeterProvider()
			tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp, Disable: vvotel.Signals{disabled}})
			if err != nil {
				t.Fatal(err)
			}
			svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{})
			_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
			store := vvotel.Store(tel)(&fakeStorageStore{})
			key, _ := storage.ParseKey("a/b")
			_, _ = store.Head(context.Background(), key)
			ctx, parent := tp.Tracer("ambient").Start(context.Background(), "ambient")
			vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true)).Observe(ctx, cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome, Items: 1, EncodedBytes: 1, PayloadBytes: 1})
			vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true)).Observe(ctx, cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.HitOutcome, Items: 1, ValueBytes: 1, ChargedBytes: 1})
			parent.End()

			spanNames := map[string]int{}
			events := map[string]int{}
			for _, span := range tp.spans {
				spanNames[span.name]++
				for _, event := range span.events {
					events[event]++
				}
			}
			metricNames := map[string]int{}
			for _, measurement := range mp.metrics {
				metricNames[measurement.name]++
			}

			assertPresentUnlessDisabled(t, disabled, vvotel.SignalCommandDuration, metricNames[vvotel.MetricCommandDuration])
			assertPresentUnlessDisabled(t, disabled, vvotel.SignalCommandSpan, spanNames["vv.command get"])
			assertPresentUnlessDisabled(t, disabled, vvotel.SignalStorageDuration, metricNames[vvotel.MetricStorageDuration])
			assertPresentUnlessDisabled(t, disabled, vvotel.SignalStorageSpan, spanNames["vv.storage head"])
			assertCountUnlessDisabled(t, disabled, vvotel.SignalCacheOperations, metricNames[vvotel.MetricCacheOperations], 2)
			assertCountUnlessDisabled(t, disabled, vvotel.SignalCacheEvents, metricNames[vvotel.MetricCacheEvents], 2)
			assertCountUnlessDisabled(t, disabled, vvotel.SignalCacheItems, metricNames[vvotel.MetricCacheItems], 2)
			assertPresentUnlessDisabled(t, disabled, vvotel.SignalCacheEncodedBytes, metricNames[vvotel.MetricCacheEncodedBytes])
			assertPresentUnlessDisabled(t, disabled, vvotel.SignalCachePayloadBytes, metricNames[vvotel.MetricCachePayloadBytes])
			assertPresentUnlessDisabled(t, disabled, vvotel.SignalCacheValueBytes, metricNames[vvotel.MetricCacheValueBytes])
			assertPresentUnlessDisabled(t, disabled, vvotel.SignalCacheChargedBytes, metricNames[vvotel.MetricCacheChargedBytes])
			assertPresentUnlessDisabled(t, disabled, vvotel.SignalCacheEvent, events[vvotel.EventCache])
			assertPresentUnlessDisabled(t, disabled, vvotel.SignalCacheBackendEvent, events[vvotel.EventCacheBackend])
		})
	}
}

func assertPresentUnlessDisabled(t *testing.T, disabled, signal vvotel.Signal, count int) {
	assertCountUnlessDisabled(t, disabled, signal, count, 1)
}

func assertCountUnlessDisabled(t *testing.T, disabled, signal vvotel.Signal, count, enabledCount int) {
	t.Helper()
	want := enabledCount
	if disabled == signal {
		want = 0
	}
	if count != want {
		t.Fatalf("signal %d count=%d, want %d when signal %d disabled", signal, count, want, disabled)
	}
}

func TestAssembly_MustPanicsWithTheExactTypedAssemblyError(t *testing.T) {
	cause := errors.New("constructor failure")
	mp := newAssemblyMeterProvider()
	first := metricDescriptors()[0]
	mp.meter.faultSignal = first.SignalID
	mp.meter.faultMode = "error"
	mp.meter.faultCause = cause
	defer func() {
		got := recover()
		assemblyErr, ok := got.(*vvotel.AssemblyError)
		if !ok {
			t.Fatalf("Must panic=%T %v, want *AssemblyError", got, got)
		}
		if assemblyErr.Signal() != first.SignalID || assemblyErr.Unwrap() != cause || mp.calls != 1 {
			t.Fatalf("Must panic=%#v getter calls=%d", assemblyErr, mp.calls)
		}
	}()
	_ = vvotel.Must(vvotel.Config{MeterProvider: mp})
}

func TestAssembly_ConcurrentAdaptersReadImmutableAssembly(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{})
			_, _ = svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
			vvotel.Cache(tel).Observe(context.Background(), cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome})
		}()
	}
	wait.Wait()
}

var _ error = (*hostileValue)(nil)
var _ fmt.Stringer = (*hostileValue)(nil)
var _ fmt.GoStringer = (*hostileValue)(nil)
var _ fmt.Formatter = (*hostileValue)(nil)

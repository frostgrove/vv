package vvotel

import (
	"fmt"
	"io"
	"sort"
	"strconv"

	"go.opentelemetry.io/otel/metric"
)

type AssemblyError struct {
	kind       error
	signal     Signal
	provider   string
	signalName string
	cause      error
}

func (e *AssemblyError) Error() string {
	if e == nil {
		return ErrAssembly.Error()
	}
	switch e.kind {
	case ErrInvalidSignal:
		return "vvotel: assembly failed: invalid signal"
	case ErrDuplicateSignal:
		return "vvotel: assembly failed: duplicate signal " + e.signalName
	case ErrNilProvider:
		if e.provider == "" {
			return "vvotel: assembly failed: tracer and meter providers are nil"
		}
		return "vvotel: assembly failed: " + e.provider + " provider is nil"
	case ErrProviderPanic:
		if e.signal == 0 {
			return "vvotel: assembly failed: " + e.provider + " provider panicked"
		}
		return "vvotel: assembly failed: instrument " + e.signalName + " provider panicked"
	case ErrNilInstrument:
		return "vvotel: assembly failed: instrument " + e.signalName + " is nil"
	default:
		return "vvotel: assembly failed: instrument " + e.signalName + " could not be constructed"
	}
}

func (e *AssemblyError) Format(state fmt.State, verb rune) {
	message := e.Error()
	if verb == 'q' {
		_, _ = io.WriteString(state, strconv.Quote(message))
		return
	}
	_, _ = io.WriteString(state, message)
}

func (e *AssemblyError) GoString() string {
	return e.Error()
}

func (e *AssemblyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *AssemblyError) Is(target error) bool {
	if e == nil {
		return false
	}
	if target == ErrAssembly || target == e.kind {
		return true
	}
	return target == ErrInstrument && e.signal != 0 && (e.kind == ErrProviderPanic || e.kind == ErrNilInstrument)
}

func (e *AssemblyError) Signal() Signal {
	if e == nil {
		return 0
	}
	return e.signal
}

func (e *AssemblyError) Provider() string {
	if e == nil {
		return ""
	}
	return e.provider
}

func (e *AssemblyError) SignalName() string {
	if e == nil {
		return ""
	}
	return e.signalName
}

func validateDisabledSignals(signals Signals, descriptors []SignalDescriptor) (map[Signal]struct{}, error) {
	disabled := make(map[Signal]struct{}, len(signals))
	for _, signal := range append(Signals(nil), signals...) {
		if !signal.Valid() {
			return nil, &AssemblyError{kind: ErrInvalidSignal, signal: signal}
		}
		if _, exists := disabled[signal]; exists {
			return nil, &AssemblyError{kind: ErrDuplicateSignal, signal: signal, signalName: descriptorName(descriptors, signal)}
		}
		disabled[signal] = struct{}{}
	}
	return disabled, nil
}

func descriptorsBySignalID() []SignalDescriptor {
	descriptors := SignalDescriptors()
	sort.Slice(descriptors, func(i, j int) bool {
		return descriptors[i].SignalID < descriptors[j].SignalID
	})
	return descriptors
}

func descriptorName(descriptors []SignalDescriptor, signal Signal) string {
	for _, descriptor := range descriptors {
		if descriptor.SignalID == signal {
			return descriptor.Name
		}
	}
	return ""
}

func (t *Telemetry) selectActiveSignals(descriptors []SignalDescriptor, disabled map[Signal]struct{}, config Config) error {
	for _, descriptor := range descriptors {
		if _, excluded := disabled[descriptor.SignalID]; excluded {
			continue
		}
		switch descriptor.Provider {
		case "context_only":
			t.activeSignals[descriptor.SignalID] = struct{}{}
		case "tracer":
			if !nilInterface(config.TracerProvider) {
				t.activeSignals[descriptor.SignalID] = struct{}{}
			}
		case "meter":
			if !nilInterface(config.MeterProvider) {
				t.activeSignals[descriptor.SignalID] = struct{}{}
			}
		default:
			return &AssemblyError{kind: ErrInstrument, signal: descriptor.SignalID, signalName: descriptor.Name}
		}
	}
	return nil
}

func (t *Telemetry) needsProvider(descriptors []SignalDescriptor, provider string) bool {
	for _, descriptor := range descriptors {
		if descriptor.Provider == provider && t.signalEnabled(descriptor.SignalID) {
			return true
		}
	}
	return false
}

func (t *Telemetry) constructMetrics(descriptors []SignalDescriptor) error {
	for _, descriptor := range descriptors {
		if descriptor.Provider != "meter" || !t.signalEnabled(descriptor.SignalID) {
			continue
		}
		if !validMetricAPIKind(descriptor.APIKind) {
			return &AssemblyError{kind: ErrInstrument, signal: descriptor.SignalID, provider: "meter", signalName: descriptor.Name}
		}
		instrument, cause, panicked := constructMetric(t.meter, descriptor)
		if panicked {
			return &AssemblyError{kind: ErrProviderPanic, signal: descriptor.SignalID, provider: "meter", signalName: descriptor.Name}
		}
		if cause != nil {
			return &AssemblyError{kind: ErrInstrument, signal: descriptor.SignalID, provider: "meter", signalName: descriptor.Name, cause: cause}
		}
		if nilInterface(instrument) {
			return &AssemblyError{kind: ErrNilInstrument, signal: descriptor.SignalID, provider: "meter", signalName: descriptor.Name}
		}
		switch descriptor.APIKind {
		case "float64_histogram":
			t.float64Histograms[descriptor.SignalID] = instrument.(metric.Float64Histogram)
		case "int64_histogram":
			t.int64Histograms[descriptor.SignalID] = instrument.(metric.Int64Histogram)
		case "int64_counter":
			t.int64Counters[descriptor.SignalID] = instrument.(metric.Int64Counter)
		case "int64_observable_gauge":
			t.int64ObservableGauge[descriptor.SignalID] = instrument.(metric.Int64ObservableGauge)
		}
	}
	return nil
}

func constructMetric(meter metric.Meter, descriptor SignalDescriptor) (instrument any, cause error, panicked bool) {
	completed := false
	defer func() {
		if !completed {
			_ = recover()
			instrument = nil
			cause = nil
			panicked = true
		}
	}()

	switch descriptor.APIKind {
	case "float64_histogram":
		instrument, cause = meter.Float64Histogram(
			descriptor.Name,
			metric.WithDescription(descriptor.Description),
			metric.WithUnit(descriptor.Unit),
			metric.WithExplicitBucketBoundaries(descriptor.Boundaries...),
		)
	case "int64_histogram":
		instrument, cause = meter.Int64Histogram(
			descriptor.Name,
			metric.WithDescription(descriptor.Description),
			metric.WithUnit(descriptor.Unit),
			metric.WithExplicitBucketBoundaries(descriptor.Boundaries...),
		)
	case "int64_counter":
		instrument, cause = meter.Int64Counter(
			descriptor.Name,
			metric.WithDescription(descriptor.Description),
			metric.WithUnit(descriptor.Unit),
		)
	case "int64_observable_gauge":
		instrument, cause = meter.Int64ObservableGauge(
			descriptor.Name,
			metric.WithDescription(descriptor.Description),
			metric.WithUnit(descriptor.Unit),
		)
	}
	completed = true
	return instrument, cause, false
}

func validMetricAPIKind(kind string) bool {
	switch kind {
	case "float64_histogram", "int64_histogram", "int64_counter", "int64_observable_gauge":
		return true
	default:
		return false
	}
}

func newProviderError(kind error, provider string) *AssemblyError {
	return &AssemblyError{kind: kind, provider: provider}
}

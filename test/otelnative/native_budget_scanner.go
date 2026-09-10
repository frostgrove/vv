package otelnative

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

var ErrNativeBudgetViolation = errors.New("otelnative: native metric budget violation")

type NativeBudgetInstrumentRef struct {
	Resource string
	Scope    string
	Name     string
}

type NativeBudgetScanner struct {
	mu          sync.Mutex
	manifest    NativeBudgetManifest
	resources   map[string]string
	scopes      map[string]string
	instruments map[string]NativeBudgetInstrument
	series      map[string]map[string]struct{}
}

func NewNativeBudgetScanner(manifest NativeBudgetManifest) (*NativeBudgetScanner, error) {
	manifest = snapshotNativeBudgetManifest(manifest)
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	scanner := &NativeBudgetScanner{
		manifest:    manifest,
		resources:   make(map[string]string, len(manifest.Resources)),
		scopes:      make(map[string]string, len(manifest.Scopes)),
		instruments: make(map[string]NativeBudgetInstrument, len(manifest.Instruments)),
		series:      make(map[string]map[string]struct{}, len(manifest.Instruments)),
	}
	for _, item := range manifest.Resources {
		scanner.resources[nativeStringTuple(item.SchemaURL, item.Attributes)] = item.ID
	}
	for _, item := range manifest.Scopes {
		scanner.scopes[nativeStringTuple(nativeTuple(item.Name, item.Version, item.SchemaURL), item.Attributes)] = item.ID
	}
	for _, item := range manifest.Instruments {
		key := nativeInstrumentKey(item.Resource, item.Scope, item.Name)
		scanner.instruments[key] = item
		scanner.series[key] = make(map[string]struct{})
	}
	return scanner, nil
}

func (s *NativeBudgetScanner) Scan(metrics metricdata.ResourceMetrics) error {
	if s == nil {
		return nativeBudgetViolation("scanner is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scan(metrics, nil)
}

func (s *NativeBudgetScanner) ScanExact(metrics metricdata.ResourceMetrics, expected []NativeBudgetInstrumentRef) error {
	if s == nil {
		return nativeBudgetViolation("scanner is nil")
	}
	if len(expected) == 0 {
		return nativeBudgetViolation("exact roster is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scan(metrics, expected)
}

func (s *NativeBudgetScanner) scan(metrics metricdata.ResourceMetrics, expected []NativeBudgetInstrumentRef) error {
	resourceID, err := s.matchResource(metrics.Resource)
	if err != nil {
		return err
	}
	exactInstruments, exactScopes, err := s.exactRoster(resourceID, expected)
	if err != nil {
		return err
	}
	pending := make(map[string]map[string]struct{})
	batchScopes := make(map[string]struct{}, len(metrics.ScopeMetrics))
	for _, scopeMetrics := range metrics.ScopeMetrics {
		scopeID, err := s.matchScope(scopeMetrics.Scope)
		if err != nil {
			return err
		}
		scopeTuple := nativeTuple(resourceID, scopeID)
		if _, duplicate := batchScopes[scopeTuple]; duplicate {
			return nativeBudgetViolation("duplicate scope tuple %q/%q", resourceID, scopeID)
		}
		if exactScopes != nil {
			if _, wanted := exactScopes[scopeTuple]; !wanted {
				return nativeBudgetViolation("unexpected scope tuple %q/%q", resourceID, scopeID)
			}
		}
		batchScopes[scopeTuple] = struct{}{}
		batchInstruments := make(map[string]struct{}, len(scopeMetrics.Metrics))
		for _, measurement := range scopeMetrics.Metrics {
			key := nativeInstrumentKey(resourceID, scopeID, measurement.Name)
			instrument, known := s.instruments[key]
			if !known {
				return nativeBudgetViolation("unknown instrument tuple %q/%q/%q", resourceID, scopeID, measurement.Name)
			}
			if _, duplicate := batchInstruments[key]; duplicate {
				return nativeBudgetViolation("duplicate instrument tuple %q/%q/%q", resourceID, scopeID, measurement.Name)
			}
			if exactInstruments != nil {
				if _, wanted := exactInstruments[key]; !wanted {
					return nativeBudgetViolation("unexpected instrument tuple %q/%q/%q", resourceID, scopeID, measurement.Name)
				}
			}
			batchInstruments[key] = struct{}{}
			metricType, temporality, monotonic, points, err := nativeMetricShape(measurement.Data)
			if err != nil {
				return nativeBudgetViolation("instrument %q: %v", measurement.Name, err)
			}
			if metricType != instrument.Type || measurement.Unit != instrument.Unit || temporality != instrument.Temporality || !sameNativeMonotonicity(monotonic, instrument.Monotonic) {
				return nativeBudgetViolation("instrument %q shape is type=%q unit=%q temporality=%q monotonic=%v", measurement.Name, metricType, measurement.Unit, temporality, monotonic)
			}
			if len(points) == 0 {
				return nativeBudgetViolation("instrument %q has no datapoints", measurement.Name)
			}
			if pending[key] == nil {
				pending[key] = make(map[string]struct{}, len(points))
			}
			for _, point := range points {
				series, err := s.validateSeries(instrument, point)
				if err != nil {
					return nativeBudgetViolation("instrument %q: %v", measurement.Name, err)
				}
				if _, duplicate := pending[key][series]; duplicate {
					return nativeBudgetViolation("instrument %q duplicates series %q", measurement.Name, series)
				}
				pending[key][series] = struct{}{}
			}
		}
	}
	for scopeTuple := range exactScopes {
		if _, present := batchScopes[scopeTuple]; !present {
			return nativeBudgetViolation("missing scope tuple %q", scopeTuple)
		}
	}
	for key := range exactInstruments {
		if _, present := pending[key]; !present {
			return nativeBudgetViolation("missing instrument tuple %q", key)
		}
	}
	for key, additions := range pending {
		count := uint64(len(s.series[key]))
		for series := range additions {
			if _, exists := s.series[key][series]; !exists {
				count++
			}
		}
		if count > s.instruments[key].SeriesCeiling {
			return nativeBudgetViolation("instrument %q has %d series above ceiling %d", s.instruments[key].Name, count, s.instruments[key].SeriesCeiling)
		}
	}
	for key, additions := range pending {
		for series := range additions {
			s.series[key][series] = struct{}{}
		}
	}
	return nil
}

func (s *NativeBudgetScanner) exactRoster(resourceID string, expected []NativeBudgetInstrumentRef) (map[string]struct{}, map[string]struct{}, error) {
	if expected == nil {
		return nil, nil, nil
	}
	instruments := make(map[string]struct{}, len(expected))
	scopes := make(map[string]struct{}, len(expected))
	for _, item := range expected {
		if item.Resource != resourceID {
			return nil, nil, nativeBudgetViolation("expected resource %q does not match captured resource %q", item.Resource, resourceID)
		}
		key := nativeInstrumentKey(item.Resource, item.Scope, item.Name)
		if _, known := s.instruments[key]; !known {
			return nil, nil, nativeBudgetViolation("exact roster contains unknown instrument tuple %q/%q/%q", item.Resource, item.Scope, item.Name)
		}
		if _, duplicate := instruments[key]; duplicate {
			return nil, nil, nativeBudgetViolation("exact roster duplicates instrument tuple %q/%q/%q", item.Resource, item.Scope, item.Name)
		}
		instruments[key] = struct{}{}
		scopes[nativeTuple(item.Resource, item.Scope)] = struct{}{}
	}
	return instruments, scopes, nil
}

func (s *NativeBudgetScanner) SeriesCount(resourceID, scopeID, name string) uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return uint64(len(s.series[nativeInstrumentKey(resourceID, scopeID, name)]))
}

func (s *NativeBudgetScanner) matchResource(value *resource.Resource) (string, error) {
	if value == nil {
		return "", nativeBudgetViolation("resource is nil")
	}
	attributes, err := nativeStringAttributes(value.Attributes())
	if err != nil {
		return "", nativeBudgetViolation("resource: %v", err)
	}
	id, known := s.resources[nativeStringTuple(value.SchemaURL(), attributes)]
	if !known {
		return "", nativeBudgetViolation("unknown resource tuple")
	}
	return id, nil
}

func (s *NativeBudgetScanner) matchScope(value instrumentation.Scope) (string, error) {
	attributes, err := nativeStringAttributes(value.Attributes.ToSlice())
	if err != nil {
		return "", nativeBudgetViolation("scope %q: %v", value.Name, err)
	}
	tuple := nativeStringTuple(nativeTuple(value.Name, value.Version, value.SchemaURL), attributes)
	id, known := s.scopes[tuple]
	if !known {
		return "", nativeBudgetViolation("unknown scope tuple %q/%q/%q", value.Name, value.Version, value.SchemaURL)
	}
	return id, nil
}

func (s *NativeBudgetScanner) validateSeries(instrument NativeBudgetInstrument, set attribute.Set) (string, error) {
	actual := make(map[string]attribute.KeyValue, set.Len())
	for _, item := range set.ToSlice() {
		actual[string(item.Key)] = item
	}
	declared := make(map[string]NativeBudgetAttribute, len(instrument.Attributes))
	for _, item := range instrument.Attributes {
		declared[item.Key] = item
	}
	for key := range actual {
		if _, known := declared[key]; !known {
			return "", fmt.Errorf("unknown attribute %q", key)
		}
	}
	keys := make([]string, 0, len(declared))
	for key := range declared {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		definition := declared[key]
		value, present := actual[key]
		appendNativeTuplePart(&builder, key)
		if !present {
			if !definition.AllowAbsent {
				return "", fmt.Errorf("attribute %q is absent", key)
			}
			appendNativeTuplePart(&builder, "absent")
			continue
		}
		if err := validateNativeDomainValue(s.manifest.Domains[definition.Domain], value); err != nil {
			return "", fmt.Errorf("attribute %q: %w", key, err)
		}
		appendNativeTuplePart(&builder, "present")
		appendNativeTuplePart(&builder, value.Value.Type().String())
		appendNativeTuplePart(&builder, value.Value.Emit())
	}
	return builder.String(), nil
}

func snapshotNativeBudgetManifest(source NativeBudgetManifest) NativeBudgetManifest {
	clone := source
	clone.Domains = make(map[string]NativeBudgetDomain, len(source.Domains))
	for name, domain := range source.Domains {
		domain.StringValues = append([]string(nil), domain.StringValues...)
		if domain.Int64Range != nil {
			value := *domain.Int64Range
			domain.Int64Range = &value
		}
		clone.Domains[name] = domain
	}
	clone.Resources = append([]NativeBudgetResource(nil), source.Resources...)
	for index := range clone.Resources {
		clone.Resources[index].Attributes = cloneNativeStringMap(source.Resources[index].Attributes)
	}
	clone.Scopes = append([]NativeBudgetScope(nil), source.Scopes...)
	for index := range clone.Scopes {
		clone.Scopes[index].Attributes = cloneNativeStringMap(source.Scopes[index].Attributes)
	}
	clone.Instruments = append([]NativeBudgetInstrument(nil), source.Instruments...)
	for index := range clone.Instruments {
		clone.Instruments[index].Attributes = append([]NativeBudgetAttribute(nil), source.Instruments[index].Attributes...)
		if source.Instruments[index].Monotonic != nil {
			value := *source.Instruments[index].Monotonic
			clone.Instruments[index].Monotonic = &value
		}
	}
	return clone
}

func cloneNativeStringMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func nativeMetricShape(data metricdata.Aggregation) (string, string, *bool, []attribute.Set, error) {
	switch value := data.(type) {
	case metricdata.Gauge[int64]:
		return "gauge_int64", "", nil, nativeDataPointSets(value.DataPoints), nil
	case metricdata.Gauge[float64]:
		return "gauge_float64", "", nil, nativeDataPointSets(value.DataPoints), nil
	case metricdata.Sum[int64]:
		return "sum_int64", nativeTemporality(value.Temporality), nativeBool(value.IsMonotonic), nativeDataPointSets(value.DataPoints), nil
	case metricdata.Sum[float64]:
		return "sum_float64", nativeTemporality(value.Temporality), nativeBool(value.IsMonotonic), nativeDataPointSets(value.DataPoints), nil
	case metricdata.Histogram[int64]:
		return "histogram_int64", nativeTemporality(value.Temporality), nil, nativeHistogramPointSets(value.DataPoints), nil
	case metricdata.Histogram[float64]:
		return "histogram_float64", nativeTemporality(value.Temporality), nil, nativeHistogramPointSets(value.DataPoints), nil
	default:
		return "", "", nil, nil, fmt.Errorf("unsupported aggregation %T", data)
	}
}

func nativeDataPointSets[N int64 | float64](points []metricdata.DataPoint[N]) []attribute.Set {
	sets := make([]attribute.Set, len(points))
	for index, point := range points {
		sets[index] = point.Attributes
	}
	return sets
}

func nativeHistogramPointSets[N int64 | float64](points []metricdata.HistogramDataPoint[N]) []attribute.Set {
	sets := make([]attribute.Set, len(points))
	for index, point := range points {
		sets[index] = point.Attributes
	}
	return sets
}

func nativeTemporality(value metricdata.Temporality) string {
	switch value {
	case metricdata.CumulativeTemporality:
		return "cumulative"
	case metricdata.DeltaTemporality:
		return "delta"
	default:
		return "unknown"
	}
}

func nativeBool(value bool) *bool {
	return &value
}

func sameNativeMonotonicity(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validateNativeDomainValue(domain NativeBudgetDomain, item attribute.KeyValue) error {
	switch domain.Type {
	case "string":
		if item.Value.Type() != attribute.STRING {
			return fmt.Errorf("type is %s, expected string", item.Value.Type())
		}
		for _, allowed := range domain.StringValues {
			if item.Value.AsString() == allowed {
				return nil
			}
		}
		return fmt.Errorf("unknown value %q", item.Value.AsString())
	case "int64":
		if item.Value.Type() != attribute.INT64 {
			return fmt.Errorf("type is %s, expected int64", item.Value.Type())
		}
		value := item.Value.AsInt64()
		if domain.Int64Range != nil && value >= domain.Int64Range.Minimum && value <= domain.Int64Range.Maximum {
			return nil
		}
		return fmt.Errorf("value %d is outside the configured range", value)
	default:
		return fmt.Errorf("unsupported domain type %q", domain.Type)
	}
}

func nativeStringAttributes(items []attribute.KeyValue) (map[string]string, error) {
	values := make(map[string]string, len(items))
	for _, item := range items {
		if item.Value.Type() != attribute.STRING {
			return nil, fmt.Errorf("attribute %q is not a string", item.Key)
		}
		values[string(item.Key)] = item.Value.AsString()
	}
	return values, nil
}

func nativeBudgetViolation(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNativeBudgetViolation, fmt.Sprintf(format, args...))
}

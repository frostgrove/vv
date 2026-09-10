package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type wireManifest struct {
	ContractVersion string                `json:"contract_version"`
	Scope           wireScope             `json:"scope"`
	Signals         map[string]wireSignal `json:"signals"`
}

type wireScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type wireSignal struct {
	Kind             string        `json:"kind"`
	Availability     string        `json:"availability"`
	Names            []string      `json:"names"`
	SpanKind         string        `json:"span_kind"`
	APIKind          string        `json:"api_kind"`
	Instrument       string        `json:"instrument"`
	NumberType       string        `json:"number_type"`
	Unit             string        `json:"unit"`
	ResolvedVariants []wireVariant `json:"resolved_variants"`
}

type wireVariant struct {
	Attributes map[string]wireValue `json:"attributes"`
	Status     string               `json:"status"`
}

type wireValue struct {
	Type     string   `json:"type"`
	Values   []string `json:"values"`
	Optional bool     `json:"optional"`
	Declared bool     `json:"declared"`
	MaxBytes int      `json:"max_bytes"`
	Charset  string   `json:"charset"`
}

type otlpEnvelope struct {
	ResourceSpans   []otlpResourceSpans   `json:"resourceSpans"`
	ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
	ResourceLogs    []otlpResourceLogs    `json:"resourceLogs"`
}

type otlpResourceSpans struct {
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type otlpSpan struct {
	Name                   string          `json:"name"`
	Kind                   any             `json:"kind"`
	Attributes             []otlpAttribute `json:"attributes"`
	DroppedAttributesCount json.Number     `json:"droppedAttributesCount"`
	Status                 otlpStatus      `json:"status"`
	Events                 []otlpEvent     `json:"events"`
}

type otlpStatus struct {
	Code any `json:"code"`
}

type otlpEvent struct {
	Name                   string          `json:"name"`
	Attributes             []otlpAttribute `json:"attributes"`
	DroppedAttributesCount json.Number     `json:"droppedAttributesCount"`
}

type otlpResourceMetrics struct {
	ScopeMetrics []otlpScopeMetrics `json:"scopeMetrics"`
}

type otlpScopeMetrics struct {
	Scope   otlpScope    `json:"scope"`
	Metrics []otlpMetric `json:"metrics"`
}

type otlpResourceLogs struct {
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpScopeLogs struct {
	Scope      otlpScope         `json:"scope"`
	LogRecords []json.RawMessage `json:"logRecords"`
}

type otlpMetric struct {
	Name                 string          `json:"name"`
	Unit                 string          `json:"unit"`
	Gauge                *otlpMetricData `json:"gauge"`
	Sum                  *otlpMetricData `json:"sum"`
	Histogram            *otlpMetricData `json:"histogram"`
	ExponentialHistogram *otlpMetricData `json:"exponentialHistogram"`
	Summary              *otlpMetricData `json:"summary"`
}

type otlpMetricData struct {
	DataPoints  []otlpDataPoint `json:"dataPoints"`
	IsMonotonic *bool           `json:"isMonotonic"`
}

type otlpDataPoint struct {
	Attributes             []otlpAttribute `json:"attributes"`
	DroppedAttributesCount json.Number     `json:"droppedAttributesCount"`
	AsInt                  *string         `json:"asInt"`
	AsDouble               *float64        `json:"asDouble"`
	Exemplars              []otlpExemplar  `json:"exemplars"`
}

type otlpExemplar struct {
	AsInt    *string  `json:"asInt"`
	AsDouble *float64 `json:"asDouble"`
}

type otlpAttribute struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	String *string          `json:"stringValue"`
	Bool   *bool            `json:"boolValue"`
	Int    *string          `json:"intValue"`
	Double *float64         `json:"doubleValue"`
	Bytes  *string          `json:"bytesValue"`
	Array  *json.RawMessage `json:"arrayValue"`
	KVList *json.RawMessage `json:"kvlistValue"`
}

func (value *otlpAnyValue) UnmarshalJSON(content []byte) error {
	type rawAnyValue otlpAnyValue
	var decoded rawAnyValue
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode AnyValue: %w", err)
	}
	*value = otlpAnyValue(decoded)
	return nil
}

type observedValue struct {
	Kind  string
	Value string
}

type observation struct {
	Kind             string
	Name             string
	Status           string
	SpanKind         string
	MetricInstrument string
	NumberType       string
	Unit             string
	Attributes       map[string]observedValue
}

func main() {
	manifestPath := flag.String("manifest", "otel/wire_manifest.json", "vv-otel/v2 wire manifest")
	inputPath := flag.String("input", "", "OTLP protobuf-JSON export request")
	allowPartial := flag.Bool("allow-partial", false, "allow implemented signal identities to be absent")
	flag.Parse()
	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "-input is required")
		os.Exit(2)
	}
	manifest, err := loadManifest(*manifestPath)
	if err == nil {
		var input []byte
		input, err = os.ReadFile(*inputPath)
		if err == nil {
			err = validateOTLP(manifest, input, !*allowPartial)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loadManifest(path string) (wireManifest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return wireManifest{}, err
	}
	var manifest wireManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return wireManifest{}, err
	}
	if manifest.ContractVersion != "vv-otel/v2" {
		return wireManifest{}, fmt.Errorf("manifest contract_version %q, want vv-otel/v2", manifest.ContractVersion)
	}
	if manifest.Scope.Name == "" || manifest.Scope.Version == "" {
		return wireManifest{}, errors.New("manifest scope name and version are required")
	}
	return manifest, nil
}

func validateOTLP(manifest wireManifest, input []byte, requireComplete bool) error {
	var envelope otlpEnvelope
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("decode OTLP JSON: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("decode OTLP JSON: trailing JSON value")
		}
		return fmt.Errorf("decode OTLP JSON trailing content: %w", err)
	}
	contracts, expected, err := indexContracts(manifest)
	if err != nil {
		return err
	}
	observations, err := collectObservations(manifest.Scope, envelope)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	var violations []string
	for _, observed := range observations {
		key := observationKey(observed.Kind, observed.Name)
		contract, ok := contracts[key]
		if !ok {
			violations = append(violations, fmt.Sprintf("extra %s signal %q", observed.Kind, observed.Name))
			continue
		}
		seen[key] = true
		if err := accepts(contract, observed); err != nil {
			violations = append(violations, fmt.Sprintf("%s %q: %v", observed.Kind, observed.Name, err))
		}
	}
	if requireComplete {
		for key, identity := range expected {
			if !seen[key] {
				violations = append(violations, "missing "+identity)
			}
		}
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return errors.New(strings.Join(violations, "\n"))
}

func indexContracts(manifest wireManifest) (map[string]wireSignal, map[string]string, error) {
	contracts := make(map[string]wireSignal)
	expected := make(map[string]string)
	for _, signal := range manifest.Signals {
		if signal.Availability != "implemented" {
			continue
		}
		switch signal.Kind {
		case "span":
			if !contains([]string{"unspecified", "internal", "server", "client", "producer", "consumer"}, signal.SpanKind) {
				return nil, nil, fmt.Errorf("implemented span %q has unsupported span_kind %q", signal.Names, signal.SpanKind)
			}
		case "metric":
			if err := validateMetricContract(signal); err != nil {
				return nil, nil, err
			}
		case "span_event":
		default:
			return nil, nil, fmt.Errorf("implemented signal has unsupported kind %q", signal.Kind)
		}
		for _, name := range signal.Names {
			key := observationKey(signal.Kind, name)
			if _, exists := contracts[key]; exists {
				return nil, nil, fmt.Errorf("manifest repeats %s %q", signal.Kind, name)
			}
			contracts[key] = signal
			expected[key] = signal.Kind + " signal " + strconv.Quote(name)
		}
	}
	return contracts, expected, nil
}

func collectObservations(scope wireScope, envelope otlpEnvelope) ([]observation, error) {
	var observations []observation
	for _, resource := range envelope.ResourceSpans {
		for _, scoped := range resource.ScopeSpans {
			if scoped.Scope.Name != scope.Name {
				continue
			}
			if scoped.Scope.Version != scope.Version {
				return nil, fmt.Errorf("Frostgrove span scope version %q, want %q", scoped.Scope.Version, scope.Version)
			}
			for _, span := range scoped.Spans {
				status, err := statusName(span.Status.Code)
				if err != nil {
					return nil, fmt.Errorf("span %q: %w", span.Name, err)
				}
				spanKind, err := spanKindName(span.Kind)
				if err != nil {
					return nil, fmt.Errorf("span %q: %w", span.Name, err)
				}
				attributes, err := decodeAttributes(span.Attributes, span.DroppedAttributesCount)
				if err != nil {
					return nil, fmt.Errorf("span %q: %w", span.Name, err)
				}
				observations = append(observations, observation{Kind: "span", Name: span.Name, Status: status, SpanKind: spanKind, Attributes: attributes})
				for _, event := range span.Events {
					attributes, err = decodeAttributes(event.Attributes, event.DroppedAttributesCount)
					if err != nil {
						return nil, fmt.Errorf("span event %q: %w", event.Name, err)
					}
					observations = append(observations, observation{Kind: "span_event", Name: event.Name, Attributes: attributes})
				}
			}
		}
	}
	for _, resource := range envelope.ResourceMetrics {
		for _, scoped := range resource.ScopeMetrics {
			if scoped.Scope.Name != scope.Name {
				continue
			}
			if scoped.Scope.Version != scope.Version {
				return nil, fmt.Errorf("Frostgrove metric scope version %q, want %q", scoped.Scope.Version, scope.Version)
			}
			for _, metric := range scoped.Metrics {
				instrument, numberType, points, err := decodeMetricShape(metric)
				if err != nil {
					return nil, fmt.Errorf("metric %q: %w", metric.Name, err)
				}
				for _, point := range points {
					attributes, err := decodeAttributes(point.Attributes, point.DroppedAttributesCount)
					if err != nil {
						return nil, fmt.Errorf("metric %q: %w", metric.Name, err)
					}
					observations = append(observations, observation{Kind: "metric", Name: metric.Name, MetricInstrument: instrument, NumberType: numberType, Unit: metric.Unit, Attributes: attributes})
				}
			}
		}
	}
	for _, resource := range envelope.ResourceLogs {
		for _, scoped := range resource.ScopeLogs {
			if scoped.Scope.Name != scope.Name {
				continue
			}
			if scoped.Scope.Version != scope.Version {
				return nil, fmt.Errorf("Frostgrove log scope version %q, want %q", scoped.Scope.Version, scope.Version)
			}
			for range scoped.LogRecords {
				observations = append(observations, observation{Kind: "log", Name: "log_record"})
			}
		}
	}
	return observations, nil
}

func decodeAttributes(attributes []otlpAttribute, dropped json.Number) (map[string]observedValue, error) {
	if dropped != "" && dropped != "0" {
		return nil, fmt.Errorf("droppedAttributesCount=%s", dropped)
	}
	decoded := make(map[string]observedValue, len(attributes))
	for _, attribute := range attributes {
		if attribute.Key == "" {
			return nil, errors.New("empty attribute key")
		}
		if _, exists := decoded[attribute.Key]; exists {
			return nil, fmt.Errorf("duplicate attribute %q", attribute.Key)
		}
		value, err := decodeValue(attribute.Value)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", attribute.Key, err)
		}
		decoded[attribute.Key] = value
	}
	return decoded, nil
}

func decodeValue(value otlpAnyValue) (observedValue, error) {
	count := 0
	for _, populated := range []bool{value.String != nil, value.Bool != nil, value.Int != nil, value.Double != nil, value.Bytes != nil, value.Array != nil, value.KVList != nil} {
		if populated {
			count++
		}
	}
	if count != 1 {
		return observedValue{}, fmt.Errorf("AnyValue contains %d value kinds, want exactly one", count)
	}
	switch {
	case value.String != nil:
		return observedValue{Kind: "string", Value: *value.String}, nil
	case value.Bool != nil:
		return observedValue{Kind: "bool", Value: strconv.FormatBool(*value.Bool)}, nil
	case value.Int != nil:
		return observedValue{Kind: "int64", Value: *value.Int}, nil
	case value.Double != nil:
		return observedValue{Kind: "float64", Value: strconv.FormatFloat(*value.Double, 'g', -1, 64)}, nil
	case value.Bytes != nil, value.Array != nil, value.KVList != nil:
		return observedValue{}, errors.New("unsupported AnyValue kind")
	}
	return observedValue{}, errors.New("unsupported AnyValue")
}

func validateMetricContract(signal wireSignal) error {
	if signal.Instrument == "" || signal.NumberType == "" || signal.APIKind == "" {
		return fmt.Errorf("implemented metric %q has incomplete shape metadata", signal.Names)
	}
	wantAPIKind := signal.NumberType + "_" + signal.Instrument
	if signal.APIKind != wantAPIKind {
		return fmt.Errorf("implemented metric %q api_kind %q, want %q", signal.Names, signal.APIKind, wantAPIKind)
	}
	switch signal.Instrument {
	case "counter", "histogram", "observable_gauge":
	default:
		return fmt.Errorf("implemented metric %q has unsupported instrument %q", signal.Names, signal.Instrument)
	}
	if signal.NumberType != "int64" && signal.NumberType != "float64" {
		return fmt.Errorf("implemented metric %q has unsupported number_type %q", signal.Names, signal.NumberType)
	}
	return nil
}

func decodeMetricShape(metric otlpMetric) (string, string, []otlpDataPoint, error) {
	type metricCase struct {
		name string
		data *otlpMetricData
	}
	cases := []metricCase{
		{"observable_gauge", metric.Gauge},
		{"sum", metric.Sum},
		{"histogram", metric.Histogram},
		{"exponential_histogram", metric.ExponentialHistogram},
		{"summary", metric.Summary},
	}
	var selected metricCase
	count := 0
	for _, candidate := range cases {
		if candidate.data == nil {
			continue
		}
		selected = candidate
		count++
	}
	if count != 1 {
		return "", "", nil, fmt.Errorf("contains %d metric data kinds, want exactly one", count)
	}
	if len(selected.data.DataPoints) == 0 {
		return "", "", nil, errors.New("has no data points")
	}
	instrument := selected.name
	if instrument == "sum" {
		if selected.data.IsMonotonic == nil || !*selected.data.IsMonotonic {
			return "", "", nil, errors.New("sum is not a monotonic counter")
		}
		instrument = "counter"
	}
	var numberTypes []string
	if instrument == "histogram" || instrument == "exponential_histogram" {
		for _, point := range selected.data.DataPoints {
			for _, exemplar := range point.Exemplars {
				numberType, err := numberKind(exemplar.AsInt, exemplar.AsDouble)
				if err != nil {
					return "", "", nil, fmt.Errorf("exemplar: %w", err)
				}
				numberTypes = append(numberTypes, numberType)
			}
		}
		if len(numberTypes) == 0 {
			return "", "", nil, errors.New("histogram has no exemplar proving its API number type")
		}
	} else {
		for _, point := range selected.data.DataPoints {
			numberType, err := numberKind(point.AsInt, point.AsDouble)
			if err != nil {
				return "", "", nil, err
			}
			numberTypes = append(numberTypes, numberType)
		}
	}
	numberType := numberTypes[0]
	for _, current := range numberTypes[1:] {
		if current != numberType {
			return "", "", nil, fmt.Errorf("mixes %s and %s number data", numberType, current)
		}
	}
	return instrument, numberType, selected.data.DataPoints, nil
}

func numberKind(asInt *string, asDouble *float64) (string, error) {
	if (asInt == nil) == (asDouble == nil) {
		return "", errors.New("number must contain exactly one of asInt or asDouble")
	}
	if asInt != nil {
		if _, err := strconv.ParseInt(*asInt, 10, 64); err != nil {
			return "", fmt.Errorf("invalid asInt %q", *asInt)
		}
		return "int64", nil
	}
	return "float64", nil
}

func accepts(contract wireSignal, observed observation) error {
	if contract.Kind == "span" && observed.SpanKind != contract.SpanKind {
		return fmt.Errorf("span kind %q, want %q", observed.SpanKind, contract.SpanKind)
	}
	if contract.Kind == "metric" {
		if observed.MetricInstrument != contract.Instrument {
			return fmt.Errorf("metric instrument %q, want %q", observed.MetricInstrument, contract.Instrument)
		}
		if observed.NumberType != contract.NumberType {
			return fmt.Errorf("metric number type %q, want %q", observed.NumberType, contract.NumberType)
		}
		if observed.Unit != contract.Unit {
			return fmt.Errorf("metric unit %q, want %q", observed.Unit, contract.Unit)
		}
	}
	var reasons []string
	for _, variant := range contract.ResolvedVariants {
		if contract.Kind == "span" && variant.Status != observed.Status {
			continue
		}
		if err := acceptsVariant(variant, observed.Attributes); err == nil {
			return nil
		} else {
			reasons = append(reasons, err.Error())
		}
	}
	if len(reasons) == 0 {
		return fmt.Errorf("status %q has no declared variant", observed.Status)
	}
	return errors.New(strings.Join(reasons, "; "))
}

func acceptsVariant(variant wireVariant, attributes map[string]observedValue) error {
	for key := range attributes {
		if _, ok := variant.Attributes[key]; !ok {
			return fmt.Errorf("extra attribute %q", key)
		}
	}
	for key, spec := range variant.Attributes {
		value, present := attributes[key]
		if !present {
			if spec.Optional {
				continue
			}
			return fmt.Errorf("missing attribute %q", key)
		}
		if value.Kind != spec.Type {
			return fmt.Errorf("attribute %q type %s, want %s", key, value.Kind, spec.Type)
		}
		if spec.Declared {
			if !declaredValueAccepted(spec, value.Value) {
				return fmt.Errorf("attribute %q violates its declared bound", key)
			}
			continue
		}
		if !contains(spec.Values, value.Value) {
			return fmt.Errorf("attribute %q value %q is outside its closed domain", key, value.Value)
		}
	}
	return nil
}

func declaredValueAccepted(spec wireValue, value string) bool {
	if spec.Type != "string" || spec.MaxBytes <= 0 || len(value) == 0 || len(value) > spec.MaxBytes || !utf8.ValidString(value) || spec.Charset != "unicode_letter_digit_dot_underscore_hyphen" {
		return false
	}
	for _, current := range value {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) && current != '.' && current != '_' && current != '-' {
			return false
		}
	}
	return true
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func statusName(value any) (string, error) {
	switch current := value.(type) {
	case nil:
		return "unset", nil
	case string:
		switch current {
		case "STATUS_CODE_ERROR", "ERROR", "2":
			return "error", nil
		case "STATUS_CODE_OK", "OK", "1":
			return "ok", nil
		case "STATUS_CODE_UNSET", "UNSET", "0":
			return "unset", nil
		}
	case json.Number:
		switch current.String() {
		case "0":
			return "unset", nil
		case "1":
			return "ok", nil
		case "2":
			return "error", nil
		}
	case float64:
		switch current {
		case 0:
			return "unset", nil
		case 1:
			return "ok", nil
		case 2:
			return "error", nil
		}
	}
	return "", fmt.Errorf("invalid span status code %v", value)
}

func spanKindName(value any) (string, error) {
	if value == nil {
		return "unspecified", nil
	}
	if rendered, ok := value.(string); ok {
		switch rendered {
		case "SPAN_KIND_UNSPECIFIED", "UNSPECIFIED", "0":
			return "unspecified", nil
		case "SPAN_KIND_INTERNAL", "INTERNAL", "1":
			return "internal", nil
		case "SPAN_KIND_SERVER", "SERVER", "2":
			return "server", nil
		case "SPAN_KIND_CLIENT", "CLIENT", "3":
			return "client", nil
		case "SPAN_KIND_PRODUCER", "PRODUCER", "4":
			return "producer", nil
		case "SPAN_KIND_CONSUMER", "CONSUMER", "5":
			return "consumer", nil
		}
	}
	var number int64
	switch current := value.(type) {
	case json.Number:
		parsed, err := current.Int64()
		if err != nil {
			return "", fmt.Errorf("invalid span kind %v", value)
		}
		number = parsed
	case float64:
		if current != float64(int64(current)) {
			return "", fmt.Errorf("invalid span kind %v", value)
		}
		number = int64(current)
	default:
		return "", fmt.Errorf("invalid span kind %v", value)
	}
	switch number {
	case 0:
		return "unspecified", nil
	case 1:
		return "internal", nil
	case 2:
		return "server", nil
	case 3:
		return "client", nil
	case 4:
		return "producer", nil
	case 5:
		return "consumer", nil
	}
	return "", fmt.Errorf("invalid span kind %v", value)
}

func observationKey(kind, name string) string {
	return kind + "\x00" + name
}

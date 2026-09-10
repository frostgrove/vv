package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCheckedInV2ManifestAndOTLPCapture(t *testing.T) {
	manifest, err := loadManifest("testdata/manifest.v2.json")
	if err != nil {
		t.Fatal(err)
	}
	capture, err := os.ReadFile("testdata/capture.v2.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOTLP(manifest, capture, true); err != nil {
		t.Fatalf("checked-in v2 fixture: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*otlpEnvelope)
		want   string
	}{
		{"span kind", func(capture *otlpEnvelope) { capture.ResourceSpans[0].ScopeSpans[0].Spans[0].Kind = "SPAN_KIND_SERVER" }, `span kind "server", want "internal"`},
		{"unknown span kind", func(capture *otlpEnvelope) {
			capture.ResourceSpans[0].ScopeSpans[0].Spans[0].Kind = "SPAN_KIND_UNKNOWN"
		}, "invalid span kind"},
		{"unknown status", func(capture *otlpEnvelope) {
			capture.ResourceSpans[0].ScopeSpans[0].Spans[0].Status.Code = "STATUS_CODE_UNKNOWN"
		}, "invalid span status code"},
		{"multiple AnyValue fields", addSecondAnyValueField, "AnyValue contains 2 value kinds, want exactly one"},
		{"supported and unsupported AnyValue fields", addUnsupportedAnyValueField, "AnyValue contains 2 value kinds, want exactly one"},
		{"unsupported AnyValue field", replaceWithUnsupportedAnyValue, "unsupported AnyValue kind"},
		{"histogram to gauge", mutateHistogramToGauge, `metric instrument "observable_gauge", want "histogram"`},
		{"counter number type", mutateCounterToDouble, `metric number type "float64", want "int64"`},
		{"unit", func(capture *otlpEnvelope) { capture.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Unit = "ms" }, `metric unit "ms", want "s"`},
		{"missing event", func(capture *otlpEnvelope) { capture.ResourceSpans[0].ScopeSpans[0].Spans[0].Events = nil }, `missing span_event signal "cache.event"`},
		{"extra event", appendExtraEvent, `extra span_event signal "cache.extra"`},
		{"missing signal", func(capture *otlpEnvelope) {
			capture.ResourceMetrics[0].ScopeMetrics[0].Metrics = capture.ResourceMetrics[0].ScopeMetrics[0].Metrics[:1]
		}, `missing metric signal "vv.cache.operations"`},
		{"extra signal", appendExtraMetric, `extra metric signal "vv.extra"`},
		{"histogram evidence", removeHistogramExemplars, "histogram has no exemplar proving its API number type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := decodeCheckedInCapture(t, capture)
			test.mutate(&mutated)
			encoded, err := json.Marshal(mutated)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateOTLP(manifest, encoded, true); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("result = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDecodeValueCountsAllOTLPAnyValueArms(t *testing.T) {
	stringValue := "value"
	for _, test := range []struct {
		name    string
		payload string
	}{
		{"bytes", `{"bytesValue":"dmFsdWU="}`},
		{"array", `{"arrayValue":{"values":[]}}`},
		{"kvlist", `{"kvlistValue":{"values":[]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value otlpAnyValue
			if err := json.Unmarshal([]byte(test.payload), &value); err != nil {
				t.Fatal(err)
			}
			if _, err := decodeValue(value); err == nil || !strings.Contains(err.Error(), "unsupported AnyValue kind") {
				t.Fatalf("unsupported-only result = %v", err)
			}
			value.String = &stringValue
			if _, err := decodeValue(value); err == nil || !strings.Contains(err.Error(), "contains 2 value kinds") {
				t.Fatalf("supported plus unsupported result = %v", err)
			}
		})
	}
}

func TestValidateOTLPRejectsUnknownAnyValueField(t *testing.T) {
	manifest := fixtureManifest()
	valid := fixtureOTLP(`[{"key":"vv.component","value":{"stringValue":"command"}}]`, "vv.command get")
	unknown := strings.Replace(valid, `"stringValue":"command"`, `"stringValue":"command","futureValue":"secret"`, 1)
	if err := validateOTLP(manifest, []byte(unknown), true); err == nil || !strings.Contains(err.Error(), `unknown field "futureValue"`) {
		t.Fatalf("unknown AnyValue field result = %v", err)
	}
}

func TestValidateOTLPStatusCodesAreClosed(t *testing.T) {
	for _, test := range []struct {
		value any
		want  string
	}{
		{nil, "unset"},
		{json.Number("0"), "unset"},
		{"STATUS_CODE_UNSET", "unset"},
		{json.Number("1"), "ok"},
		{"STATUS_CODE_OK", "ok"},
		{json.Number("2"), "error"},
		{"STATUS_CODE_ERROR", "error"},
	} {
		got, err := statusName(test.value)
		if err != nil || got != test.want {
			t.Errorf("statusName(%v) = %q, %v; want %q", test.value, got, err, test.want)
		}
	}
	for _, value := range []any{json.Number("3"), json.Number("1.5"), "UNKNOWN", true} {
		if got, err := statusName(value); err == nil {
			t.Errorf("statusName(%v) = %q, want error", value, got)
		}
	}
}

func TestValidateOTLPRejectsFrostgroveLogsAndIgnoresNativeLogs(t *testing.T) {
	manifest := fixtureManifest()
	capture := decodeOTLP(t, []byte(fixtureOTLP(`[{"key":"vv.component","value":{"stringValue":"command"}}]`, "vv.command get")))
	capture.ResourceLogs = []otlpResourceLogs{{ScopeLogs: []otlpScopeLogs{
		{Scope: otlpScope{Name: "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", Version: "v0.63.0"}, LogRecords: []json.RawMessage{json.RawMessage(`{}`)}},
	}}}
	encoded, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOTLP(manifest, encoded, true); err != nil {
		t.Fatalf("native-scope log: %v", err)
	}

	capture.ResourceLogs[0].ScopeLogs = append(capture.ResourceLogs[0].ScopeLogs, otlpScopeLogs{
		Scope:      otlpScope{Name: manifest.Scope.Name, Version: manifest.Scope.Version},
		LogRecords: []json.RawMessage{json.RawMessage(`{}`)},
	})
	encoded, err = json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOTLP(manifest, encoded, true); err == nil || !strings.Contains(err.Error(), `extra log signal "log_record"`) {
		t.Fatalf("Frostgrove-scope log result = %v", err)
	}
}

func TestValidateOTLPRejectsTrailingJSONValue(t *testing.T) {
	manifest := fixtureManifest()
	input := fixtureOTLP(`[{"key":"vv.component","value":{"stringValue":"command"}}]`, "vv.command get") + "\n{}"
	if err := validateOTLP(manifest, []byte(input), true); err == nil || !strings.Contains(err.Error(), "trailing JSON value") {
		t.Fatalf("trailing JSON result = %v", err)
	}
}

func TestRepositoryV2ManifestCarriesStrictShapeContracts(t *testing.T) {
	manifest, err := loadManifest("../../otel/wire_manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	contracts, expected, err := indexContracts(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) == 0 || len(expected) != len(contracts) {
		t.Fatalf("indexed contracts=%d expected=%d", len(contracts), len(expected))
	}
	capture, err := os.ReadFile("testdata/capture.v2.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOTLP(manifest, capture, false); err != nil {
		t.Fatalf("checked-in capture against repository manifest: %v", err)
	}
}

func TestValidateOTLPRejectsMissingExtraAndInvalidAttributes(t *testing.T) {
	manifest := fixtureManifest()
	valid := fixtureOTLP(`[{"key":"vv.component","value":{"stringValue":"command"}}]`, "vv.command get")
	if err := validateOTLP(manifest, []byte(valid), true); err != nil {
		t.Fatalf("valid fixture: %v", err)
	}

	extra := fixtureOTLP(`[{"key":"vv.component","value":{"stringValue":"command"}},{"key":"secret.id","value":{"stringValue":"secret"}}]`, "vv.command get")
	if err := validateOTLP(manifest, []byte(extra), true); err == nil || !strings.Contains(err.Error(), `extra attribute "secret.id"`) {
		t.Fatalf("extra attribute result = %v", err)
	}

	invalid := fixtureOTLP(`[{"key":"vv.component","value":{"stringValue":"storage"}}]`, "vv.command get")
	if err := validateOTLP(manifest, []byte(invalid), true); err == nil || !strings.Contains(err.Error(), "outside its closed domain") {
		t.Fatalf("invalid value result = %v", err)
	}

	missing := `{"resourceSpans":[]}`
	if err := validateOTLP(manifest, []byte(missing), true); err == nil || !strings.Contains(err.Error(), `missing span signal "vv.command get"`) {
		t.Fatalf("missing signal result = %v", err)
	}
	if err := validateOTLP(manifest, []byte(missing), false); err != nil {
		t.Fatalf("partial fixture: %v", err)
	}
}

func TestValidateOTLPRejectsExtraSignalScopeDriftAndDroppedAttributes(t *testing.T) {
	manifest := fixtureManifest()
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{"extra signal", fixtureOTLP(`[{"key":"vv.component","value":{"stringValue":"command"}}]`, "vv.command unknown"), `extra span signal "vv.command unknown"`},
		{"scope drift", strings.Replace(fixtureOTLP(`[{"key":"vv.component","value":{"stringValue":"command"}}]`, "vv.command get"), `"v0.1.0"`, `"v0.2.0"`, 1), `scope version "v0.2.0"`},
		{"dropped", strings.Replace(fixtureOTLP(`[{"key":"vv.component","value":{"stringValue":"command"}}]`, "vv.command get"), `"attributes":`, `"droppedAttributesCount":"1","attributes":`, 1), `droppedAttributesCount=1`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateOTLP(manifest, []byte(test.input), false); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("result = %v, want %q", err, test.want)
			}
		})
	}
}

func fixtureManifest() wireManifest {
	return wireManifest{
		ContractVersion: "vv-otel/v2",
		Scope:           wireScope{Name: "github.com/frostgrove/vv/otel", Version: "v0.1.0"},
		Signals: map[string]wireSignal{
			"command": {
				Kind:         "span",
				Availability: "implemented",
				Names:        []string{"vv.command get"},
				SpanKind:     "internal",
				ResolvedVariants: []wireVariant{{
					Status: "unset",
					Attributes: map[string]wireValue{
						"vv.component": {Type: "string", Values: []string{"command"}},
					},
				}},
			},
		},
	}
}

func fixtureOTLP(attributes, name string) string {
	return `{"resourceSpans":[{"scopeSpans":[{"scope":{"name":"github.com/frostgrove/vv/otel","version":"v0.1.0"},"spans":[{"name":"` + name + `","kind":"SPAN_KIND_INTERNAL","attributes":` + attributes + `}]}]}]}`
}

func decodeCheckedInCapture(t *testing.T, content []byte) otlpEnvelope {
	t.Helper()
	return decodeOTLP(t, content)
}

func decodeOTLP(t *testing.T, content []byte) otlpEnvelope {
	t.Helper()
	var capture otlpEnvelope
	if err := json.Unmarshal(content, &capture); err != nil {
		t.Fatal(err)
	}
	return capture
}

func addSecondAnyValueField(capture *otlpEnvelope) {
	value := "1"
	capture.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes[0].Value.Int = &value
}

func addUnsupportedAnyValueField(capture *otlpEnvelope) {
	value := "Y29tbWFuZA=="
	capture.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes[0].Value.Bytes = &value
}

func replaceWithUnsupportedAnyValue(capture *otlpEnvelope) {
	addUnsupportedAnyValueField(capture)
	capture.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes[0].Value.String = nil
}

func mutateHistogramToGauge(capture *otlpEnvelope) {
	metric := &capture.ResourceMetrics[0].ScopeMetrics[0].Metrics[0]
	value := 0.012
	for index := range metric.Histogram.DataPoints {
		metric.Histogram.DataPoints[index].AsDouble = &value
	}
	metric.Gauge = metric.Histogram
	metric.Histogram = nil
}

func mutateCounterToDouble(capture *otlpEnvelope) {
	point := &capture.ResourceMetrics[0].ScopeMetrics[0].Metrics[1].Sum.DataPoints[0]
	value := float64(1)
	point.AsInt = nil
	point.AsDouble = &value
}

func appendExtraEvent(capture *otlpEnvelope) {
	span := &capture.ResourceSpans[0].ScopeSpans[0].Spans[0]
	extra := span.Events[0]
	extra.Name = "cache.extra"
	span.Events = append(span.Events, extra)
}

func appendExtraMetric(capture *otlpEnvelope) {
	metrics := capture.ResourceMetrics[0].ScopeMetrics[0].Metrics
	extra := metrics[1]
	extra.Name = "vv.extra"
	capture.ResourceMetrics[0].ScopeMetrics[0].Metrics = append(metrics, extra)
}

func removeHistogramExemplars(capture *otlpEnvelope) {
	for index := range capture.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Histogram.DataPoints {
		capture.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Histogram.DataPoints[index].Exemplars = nil
	}
}

package otelnative

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestNativeBudgetScannerCountsSeriesByIndependentIdentity(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	scanner, err := NewNativeBudgetScanner(manifest)
	if err != nil {
		t.Fatal(err)
	}
	metrics := nativeRuntimeMemoryFixture(t, manifest,
		attribute.String("go.memory.type", "other"),
		attribute.String("go.memory.type", "stack"),
	)
	if err = scanner.Scan(metrics); err != nil {
		t.Fatal(err)
	}
	if err = scanner.Scan(metrics); err != nil {
		t.Fatalf("repeated cumulative snapshot: %v", err)
	}
	if count := scanner.SeriesCount("runtime", "runtime", "go.memory.used"); count != 2 {
		t.Fatalf("series count=%d", count)
	}
	if err = scanner.Scan(nativeRuntimeMemoryFixture(t, manifest, attribute.KeyValue{})); err != nil {
		t.Fatal(err)
	}
	if count := scanner.SeriesCount("runtime", "runtime", "go.memory.used"); count != 3 {
		t.Fatalf("series count with absence=%d", count)
	}
}

func TestNativeBudgetScannerRejectsUnknownTuplesAttributesValuesAndDuplicates(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	tests := []struct {
		name   string
		mutate func(*metricdata.ResourceMetrics)
	}{
		{
			name: "resource tuple",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				metrics.Resource = resource.NewSchemaless(
					attribute.String("service.name", runtimeServiceName),
					attribute.String("deployment.environment", "secret"),
				)
			},
		},
		{
			name: "scope tuple",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				metrics.ScopeMetrics[0].Scope.SchemaURL = "https://unknown.example/schema"
			},
		},
		{
			name: "instrument tuple",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				metrics.ScopeMetrics[0].Metrics[0].Name = "go.memory.secret"
			},
		},
		{
			name: "attribute key",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				setNativeRuntimePoints(metrics, attribute.NewSet(attribute.String("secret.attribute", "secret")))
			},
		},
		{
			name: "attribute value",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				setNativeRuntimePoints(metrics, attribute.NewSet(attribute.String("go.memory.type", "secret")))
			},
		},
		{
			name: "attribute type",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				setNativeRuntimePoints(metrics, attribute.NewSet(attribute.Int64("go.memory.type", 1)))
			},
		},
		{
			name: "metric shape",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				metrics.ScopeMetrics[0].Metrics[0].Unit = "KiBy"
			},
		},
		{
			name: "duplicate scope",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				metrics.ScopeMetrics = append(metrics.ScopeMetrics, metrics.ScopeMetrics[0])
			},
		},
		{
			name: "duplicate instrument",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				metrics.ScopeMetrics[0].Metrics = append(metrics.ScopeMetrics[0].Metrics, metrics.ScopeMetrics[0].Metrics[0])
			},
		},
		{
			name: "duplicate series",
			mutate: func(metrics *metricdata.ResourceMetrics) {
				setNativeRuntimePoints(metrics,
					attribute.NewSet(attribute.String("go.memory.type", "other")),
					attribute.NewSet(attribute.String("go.memory.type", "other")),
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scanner, err := NewNativeBudgetScanner(manifest)
			if err != nil {
				t.Fatal(err)
			}
			metrics := nativeRuntimeMemoryFixture(t, manifest, attribute.String("go.memory.type", "other"))
			test.mutate(&metrics)
			if err = scanner.Scan(metrics); !errors.Is(err, ErrNativeBudgetViolation) {
				t.Fatalf("error=%v", err)
			}
			if count := scanner.SeriesCount("runtime", "runtime", "go.memory.used"); count != 0 {
				t.Fatalf("failed batch committed %d series", count)
			}
		})
	}
}

func TestNativeBudgetScannerRejectsOverBudgetAtomically(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	scanner, err := NewNativeBudgetScanner(manifest)
	if err != nil {
		t.Fatal(err)
	}
	key := nativeInstrumentKey("runtime", "runtime", "go.memory.used")
	instrument := scanner.instruments[key]
	instrument.SeriesCeiling = 1
	scanner.instruments[key] = instrument
	metrics := nativeRuntimeMemoryFixture(t, manifest,
		attribute.String("go.memory.type", "other"),
		attribute.String("go.memory.type", "stack"),
	)
	if err = scanner.Scan(metrics); !errors.Is(err, ErrNativeBudgetViolation) {
		t.Fatalf("error=%v", err)
	}
	if count := scanner.SeriesCount("runtime", "runtime", "go.memory.used"); count != 0 {
		t.Fatalf("failed batch committed %d series", count)
	}
}

func TestNativeBudgetScannerTreatsAbsenceAsAnExplicitDomainMember(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	for index := range manifest.Instruments {
		instrument := &manifest.Instruments[index]
		if instrument.Resource == "runtime" && instrument.Scope == "runtime" && instrument.Name == "go.memory.used" {
			instrument.Attributes[0].AllowAbsent = false
		}
	}
	scanner, err := NewNativeBudgetScanner(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = scanner.Scan(nativeRuntimeMemoryFixture(t, manifest, attribute.KeyValue{})); !errors.Is(err, ErrNativeBudgetViolation) {
		t.Fatalf("error=%v", err)
	}
}

func TestNativeBudgetScannerRejectsAValidInstrumentUnderAnotherResource(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	scanner, err := NewNativeBudgetScanner(manifest)
	if err != nil {
		t.Fatal(err)
	}
	metrics := nativeRuntimeMemoryFixture(t, manifest, attribute.String("go.memory.type", "other"))
	metrics.Resource = nativeBudgetResource(t, manifest, "database")
	if err = scanner.Scan(metrics); !errors.Is(err, ErrNativeBudgetViolation) {
		t.Fatalf("error=%v", err)
	}
}

func nativeRuntimeMemoryFixture(t *testing.T, manifest NativeBudgetManifest, values ...attribute.KeyValue) metricdata.ResourceMetrics {
	t.Helper()
	sets := make([]attribute.Set, len(values))
	for index, value := range values {
		if value == (attribute.KeyValue{}) {
			sets[index] = attribute.NewSet()
			continue
		}
		sets[index] = attribute.NewSet(value)
	}
	points := make([]metricdata.DataPoint[int64], len(sets))
	for index, set := range sets {
		points[index] = metricdata.DataPoint[int64]{Attributes: set, Value: int64(index + 1)}
	}
	return metricdata.ResourceMetrics{
		Resource: nativeBudgetResource(t, manifest, "runtime"),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope: nativeBudgetScope(t, manifest, "runtime"),
			Metrics: []metricdata.Metrics{{
				Name: "go.memory.used",
				Unit: "By",
				Data: metricdata.Sum[int64]{
					Temporality: metricdata.CumulativeTemporality,
					IsMonotonic: false,
					DataPoints:  points,
				},
			}},
		}},
	}
}

func setNativeRuntimePoints(metrics *metricdata.ResourceMetrics, sets ...attribute.Set) {
	data := metrics.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
	data.DataPoints = make([]metricdata.DataPoint[int64], len(sets))
	for index, set := range sets {
		data.DataPoints[index] = metricdata.DataPoint[int64]{Attributes: set, Value: int64(index + 1)}
	}
	metrics.ScopeMetrics[0].Metrics[0].Data = data
}

func nativeBudgetResource(t *testing.T, manifest NativeBudgetManifest, id string) *resource.Resource {
	t.Helper()
	for _, definition := range manifest.Resources {
		if definition.ID != id {
			continue
		}
		attributes := make([]attribute.KeyValue, 0, len(definition.Attributes))
		for key, value := range definition.Attributes {
			attributes = append(attributes, attribute.String(key, value))
		}
		return resource.NewWithAttributes(definition.SchemaURL, attributes...)
	}
	t.Fatalf("missing resource %q", id)
	return nil
}

func nativeBudgetScope(t *testing.T, manifest NativeBudgetManifest, id string) instrumentation.Scope {
	t.Helper()
	for _, definition := range manifest.Scopes {
		if definition.ID != id {
			continue
		}
		attributes := make([]attribute.KeyValue, 0, len(definition.Attributes))
		for key, value := range definition.Attributes {
			attributes = append(attributes, attribute.String(key, value))
		}
		return instrumentation.Scope{
			Name:       definition.Name,
			Version:    definition.Version,
			SchemaURL:  definition.SchemaURL,
			Attributes: attribute.NewSet(attributes...),
		}
	}
	t.Fatalf("missing scope %q", id)
	return instrumentation.Scope{}
}

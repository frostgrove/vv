package otelnative

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeBudgetManifestPinsDefaultModeAndIndependentCeilings(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	withUnsetEnvironment(t, manifest.SemconvMode.Environment)
	if err := manifest.ValidateEnvironment(); err != nil {
		t.Fatal(err)
	}
	expectedModules := map[string]string{
		"otelhttp":  "v0.69.0",
		"otelgin":   "v0.69.0",
		"fiberotel": "v1.2.0",
		"otelgrpc":  "v0.69.0",
		"otelpgx":   "v0.11.1",
		"otelsql":   "v0.43.0",
		"pgxpool":   "v1",
		"runtime":   "v0.69.0",
	}
	actualModules := make(map[string]string, len(manifest.Scopes))
	for _, scope := range manifest.Scopes {
		actualModules[scope.ID] = scope.ModuleVersion
	}
	if !reflect.DeepEqual(actualModules, expectedModules) {
		t.Fatalf("module pins=%v", actualModules)
	}
	for _, instrument := range manifest.Instruments {
		calculated, err := manifest.CalculateSeriesCeiling(instrument)
		if err != nil {
			t.Fatalf("%s/%s/%s: %v", instrument.Resource, instrument.Scope, instrument.Name, err)
		}
		if calculated > instrument.SeriesCeiling {
			t.Fatalf("%s/%s/%s calculated=%d ceiling=%d", instrument.Resource, instrument.Scope, instrument.Name, calculated, instrument.SeriesCeiling)
		}
	}
	if got := manifest.Domains["servemux_route_projections"].StringValues; !reflect.DeepEqual(got, []string{"/items/{id}", "/live"}) {
		t.Fatalf("ServeMux routes=%v", got)
	}
	if got := manifest.Domains["router_route_templates"].StringValues; !reflect.DeepEqual(got, []string{"/items/:id", "/fail/:id"}) {
		t.Fatalf("router routes=%v", got)
	}
	if got := manifest.Domains["rpc_methods"].StringValues; !reflect.DeepEqual(got, []string{"otelnative.Transport/Ping", "otelnative.Transport/Fail", "_OTHER/_OTHER"}) {
		t.Fatalf("RPC methods=%v", got)
	}
	if got := manifest.Domains["database_pools"].StringValues; !reflect.DeepEqual(got, []string{"primary"}) {
		t.Fatalf("database pools=%v", got)
	}
}

func TestNativeBudgetManifestMatchesCurrentIntegrationSpecs(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	for scopeID, specs := range map[string]map[string]nativeMetricSpec{
		"otelhttp":  transportMetricSpecs(manifestScopeName(t, manifest, "otelhttp"), compiledTracePolicy{}),
		"otelgin":   transportMetricSpecs(manifestScopeName(t, manifest, "otelgin"), compiledTracePolicy{}),
		"fiberotel": transportMetricSpecs(manifestScopeName(t, manifest, "fiberotel"), compiledTracePolicy{}),
		"otelgrpc":  transportMetricSpecs(manifestScopeName(t, manifest, "otelgrpc"), compiledTracePolicy{}),
	} {
		assertNativeBudgetSpecs(t, manifest, "transport", scopeID, specs)
	}
	databaseScopes := databaseMetricScopes(map[string]struct{}{"primary": {}})
	for scopeID, scopeName := range map[string]string{
		"otelpgx": manifestScopeName(t, manifest, "otelpgx"),
		"otelsql": manifestScopeName(t, manifest, "otelsql"),
		"pgxpool": manifestScopeName(t, manifest, "pgxpool"),
	} {
		assertNativeBudgetSpecs(t, manifest, "database", scopeID, databaseScopes[scopeName].metrics)
	}
	assertNativeBudgetSpecs(t, manifest, "runtime", "runtime", runtimeMetricSpecs())
}

func TestNativeBudgetManifestRejectsConfiguredSemconvEnvironment(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	t.Setenv(manifest.SemconvMode.Environment, "http")
	if err := manifest.ValidateEnvironment(); !errors.Is(err, ErrInvalidNativeBudget) {
		t.Fatalf("error=%v", err)
	}
}

func TestNativeBudgetManifestRejectsSchemaAndCardinalityMutations(t *testing.T) {
	base := loadNativeBudgetForTest(t)
	tests := []struct {
		name   string
		mutate func(*NativeBudgetManifest)
	}{
		{
			name: "semconv environment may not be set",
			mutate: func(manifest *NativeBudgetManifest) {
				manifest.SemconvMode.Unset = false
			},
		},
		{
			name: "duplicate resource identity",
			mutate: func(manifest *NativeBudgetManifest) {
				duplicate := manifest.Resources[0]
				duplicate.ID = "duplicate"
				manifest.Resources = append(manifest.Resources, duplicate)
			},
		},
		{
			name: "duplicate scope identity",
			mutate: func(manifest *NativeBudgetManifest) {
				duplicate := manifest.Scopes[0]
				duplicate.ID = "duplicate"
				manifest.Scopes = append(manifest.Scopes, duplicate)
			},
		},
		{
			name: "duplicate instrument identity",
			mutate: func(manifest *NativeBudgetManifest) {
				manifest.Instruments = append(manifest.Instruments, manifest.Instruments[0])
			},
		},
		{
			name: "unknown attribute domain",
			mutate: func(manifest *NativeBudgetManifest) {
				manifest.Instruments[0].Attributes[0].Domain = "unknown"
			},
		},
		{
			name: "domain widening exceeds independent ceiling",
			mutate: func(manifest *NativeBudgetManifest) {
				domain := manifest.Domains["http_methods"]
				domain.StringValues = append(domain.StringValues, "BREW")
				manifest.Domains["http_methods"] = domain
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneNativeBudgetManifest(t, base)
			test.mutate(&manifest)
			if err := manifest.Validate(); !errors.Is(err, ErrInvalidNativeBudget) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestNativeBudgetCalculatorIncludesAbsenceAndRejectsOverflow(t *testing.T) {
	manifest := loadNativeBudgetForTest(t)
	memoryUsed := findNativeBudgetInstrument(t, manifest, "runtime", "runtime", "go.memory.used")
	ceiling, err := manifest.CalculateSeriesCeiling(memoryUsed)
	if err != nil {
		t.Fatal(err)
	}
	if ceiling != 3 {
		t.Fatalf("memory-used ceiling=%d", ceiling)
	}
	overflow := NativeBudgetManifest{Domains: map[string]NativeBudgetDomain{
		"huge": {
			Type:       "int64",
			Int64Range: &NativeBudgetInt64Range{Minimum: 0, Maximum: math.MaxInt64},
		},
	}}
	_, err = overflow.CalculateSeriesCeiling(NativeBudgetInstrument{Attributes: []NativeBudgetAttribute{
		{Key: "first", Domain: "huge"},
		{Key: "second", Domain: "huge"},
	}})
	if !errors.Is(err, ErrNativeBudgetOverflow) {
		t.Fatalf("overflow error=%v", err)
	}
}

func TestNativeBudgetLoaderRejectsUnknownAndTrailingJSON(t *testing.T) {
	for name, content := range map[string]string{
		"unknown":  `{"version":1,"unknown":true}`,
		"trailing": `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "manifest.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadNativeBudgetManifest(path); !errors.Is(err, ErrInvalidNativeBudget) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func loadNativeBudgetForTest(t *testing.T) NativeBudgetManifest {
	t.Helper()
	manifest, err := LoadNativeBudgetManifest("native_budget_manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func cloneNativeBudgetManifest(t *testing.T, manifest NativeBudgetManifest) NativeBudgetManifest {
	t.Helper()
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var clone NativeBudgetManifest
	if err = json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func findNativeBudgetInstrument(t *testing.T, manifest NativeBudgetManifest, resourceID, scopeID, name string) NativeBudgetInstrument {
	t.Helper()
	for _, instrument := range manifest.Instruments {
		if instrument.Resource == resourceID && instrument.Scope == scopeID && instrument.Name == name {
			return instrument
		}
	}
	t.Fatalf("missing instrument %s/%s/%s", resourceID, scopeID, name)
	return NativeBudgetInstrument{}
}

func manifestScopeName(t *testing.T, manifest NativeBudgetManifest, id string) string {
	t.Helper()
	for _, scope := range manifest.Scopes {
		if scope.ID == id {
			return scope.Name
		}
	}
	t.Fatalf("missing scope %q", id)
	return ""
}

func assertNativeBudgetSpecs(t *testing.T, manifest NativeBudgetManifest, resourceID, scopeID string, specs map[string]nativeMetricSpec) {
	t.Helper()
	declared := make(map[string]NativeBudgetInstrument)
	for _, instrument := range manifest.Instruments {
		if instrument.Resource == resourceID && instrument.Scope == scopeID {
			declared[instrument.Name] = instrument
		}
	}
	if len(declared) != len(specs) {
		t.Fatalf("%s/%s declared=%d specs=%d", resourceID, scopeID, len(declared), len(specs))
	}
	for name, spec := range specs {
		instrument, exists := declared[name]
		if !exists {
			t.Fatalf("%s/%s missing %q", resourceID, scopeID, name)
		}
		expectedType := nativeBudgetMetricType(t, spec.shape)
		if instrument.Type != expectedType || instrument.Unit != spec.unit {
			t.Fatalf("%s/%s/%s type/unit=%q/%q, want %q/%q", resourceID, scopeID, name, instrument.Type, instrument.Unit, expectedType, spec.unit)
		}
		if spec.shape == metricSumInt64 || spec.shape == metricSumFloat64 {
			if instrument.Temporality != "cumulative" || instrument.Monotonic == nil || *instrument.Monotonic != spec.monotonic {
				t.Fatalf("%s/%s/%s sum metadata=%q/%v", resourceID, scopeID, name, instrument.Temporality, instrument.Monotonic)
			}
		}
	}
}

func nativeBudgetMetricType(t *testing.T, shape metricShape) string {
	t.Helper()
	switch shape {
	case metricGaugeInt64:
		return "gauge_int64"
	case metricSumInt64:
		return "sum_int64"
	case metricSumFloat64:
		return "sum_float64"
	case metricHistogramInt64:
		return "histogram_int64"
	case metricHistogramFloat64:
		return "histogram_float64"
	default:
		t.Fatalf("unsupported metric shape %d", shape)
		return ""
	}
}

func withUnsetEnvironment(t *testing.T, key string) {
	t.Helper()
	value, present := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(key, value)
			return
		}
		_ = os.Unsetenv(key)
	})
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func registryFixture(t *testing.T) Registry {
	t.Helper()
	r, err := readRegistry("../../internal/otelreg/registry.json")
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func validRegistryFixture(t *testing.T) Registry {
	t.Helper()
	r := registryFixture(t)
	if err := validate(r); err != nil {
		t.Fatalf("checked-in registry is invalid before mutation: %v", err)
	}
	return r
}
func modifySignal(r *Registry, key string, change func(*Signal)) {
	s := r.Signals[key]
	change(&s)
	r.Signals[key] = s
}
func modifyAttribute(r *Registry, key string, change func(*Attribute)) {
	a := r.Attributes[key]
	change(&a)
	r.Attributes[key] = a
}
func modifyVocabulary(r *Registry, change func(*Vocabulary)) {
	c := r.Components["remote"]
	change(&c.Operations)
	r.Components["remote"] = c
}
func numericBound(value string) NumericBound {
	return NumericBound(value)
}

func TestRegistryRejectsInvalidContracts(t *testing.T) {
	validRegistryFixture(t)
	tests := map[string]func(*Registry){
		"contract":  func(r *Registry) { r.ContractVersion = "vv-otel/v1" },
		"scope":     func(r *Registry) { r.Scope.Name = "" },
		"migration": func(r *Registry) { r.Migration.To = "vv-otel/v3" },
		"domain_duplicate": func(r *Registry) {
			d := r.Domains["boolean"]
			d.Values = append(d.Values, "true")
			r.Domains["boolean"] = d
		},
		"domain_type":             func(r *Registry) { r.Domains["bad"] = Domain{Type: "float128", Values: []string{"1"}} },
		"domain_bool":             func(r *Registry) { r.Domains["bad"] = Domain{Type: "bool", Values: []string{"yes"}} },
		"domain_integer":          func(r *Registry) { r.Domains["bad"] = Domain{Type: "int64", Values: []string{"01"}} },
		"attribute_name":          func(r *Registry) { modifyAttribute(r, "reason", func(a *Attribute) { a.Name = "vv.component" }) },
		"attribute_privacy":       func(r *Registry) { modifyAttribute(r, "reason", func(a *Attribute) { a.PrivacyClass = "anything" }) },
		"attribute_maturity":      func(r *Registry) { modifyAttribute(r, "reason", func(a *Attribute) { a.Maturity = "unknown" }) },
		"unsafe_metric_attribute": func(r *Registry) { modifyAttribute(r, "resource_name", func(a *Attribute) { a.MetricEligible = true }) },
		"unbounded_name":          func(r *Registry) { modifyAttribute(r, "resource_name", func(a *Attribute) { a.MaxValues = 0 }) },
		"charset":                 func(r *Registry) { modifyAttribute(r, "resource_name", func(a *Attribute) { a.Charset = "anything" }) },
		"component_duplicate":     func(r *Registry) { c := r.Components["storage"]; c.WireValue = "command"; r.Components["storage"] = c },
		"shadowed_operations": func(r *Registry) {
			c := r.Components["remote"]
			c.Vocabularies["operations"] = c.Operations
			r.Components["remote"] = c
		},
		"missing_source": func(r *Registry) { modifyVocabulary(r, func(v *Vocabulary) { v.Source.Symbol = "" }) },
		"duplicate_source": func(r *Registry) {
			modifyVocabulary(r, func(v *Vocabulary) { v.Source.Members = append(v.Source.Members, v.Source.Members[0]) })
		},
		"missing_mapping": func(r *Registry) { modifyVocabulary(r, func(v *Vocabulary) { v.Mapping = v.Mapping[1:] }) },
		"duplicate_mapping": func(r *Registry) {
			modifyVocabulary(r, func(v *Vocabulary) { v.Mapping = append(v.Mapping, v.Mapping[0]) })
		},
		"unknown_source_value": func(r *Registry) { modifyVocabulary(r, func(v *Vocabulary) { v.Mapping[0].From = "not_in_source" }) },
		"unknown_wire_value":   func(r *Registry) { modifyVocabulary(r, func(v *Vocabulary) { v.Mapping[0].To = "tenant_secret" }) },
		"unknown_policy":       func(r *Registry) { modifyVocabulary(r, func(v *Vocabulary) { v.Unknown = "" }) },
		"invalid_fallback": func(r *Registry) {
			modifyVocabulary(r, func(v *Vocabulary) { v.Unknown = "fallback"; v.Fallback = "tenant_secret" })
		},
		"undeclared_domain": func(r *Registry) { modifyVocabulary(r, func(v *Vocabulary) { v.Domain = "missing" }) },
		"unknown_component": func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.Component = "missing" }) },
		"duplicate_signal_name": func(r *Registry) {
			modifySignal(r, "storage_duration", func(s *Signal) { s.Name = "vv.command.duration" })
		},
		"invalid_signal_kind":     func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.Kind = "request" }) },
		"missing_signal_metadata": func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.Source = "" }) },
		"instrument":              func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.Instrument = "summary" }) },
		"number_type":             func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.NumberType = "uint64" }) },
		"unit":                    func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.Unit = "seconds" }) },
		"unit_number_pair":        func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.NumberType = "int64" }) },
		"api_kind": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.APIKind = "int64_counter" })
		},
		"provider": func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.Provider = "tracer" }) },
		"nonfinite_bound": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Boundaries[0] = math.Inf(1) })
		},
		"nan_bound": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Boundaries[0] = math.NaN() })
		},
		"unordered_bound": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Boundaries[1] = s.Boundaries[0] })
		},
		"below_minimum": func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.Boundaries[0] = -1 }) },
		"nonfinite_minimum": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Min = numericBound("NaN") })
		},
		"above_maximum": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Max = numericBound("9") })
		},
		"counter_bounds": func(r *Registry) {
			modifySignal(r, "cache_operations", func(s *Signal) { s.Boundaries = []float64{1} })
		},
		"missing_histogram_bounds": func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.Boundaries = nil }) },
		"span_metric_metadata":     func(r *Registry) { modifySignal(r, "command_span", func(s *Signal) { s.Unit = "s" }) },
		"span_kind":                func(r *Registry) { modifySignal(r, "command_span", func(s *Signal) { s.SpanKind = "client" }) },
		"span_name_domain":         func(r *Registry) { modifySignal(r, "command_span", func(s *Signal) { s.NameDomain = "absent" }) },
		"foreign_span_name_domain": func(r *Registry) {
			modifySignal(r, "command_span", func(s *Signal) { s.NameDomain = "storage.operations" })
		},
		"duplicate_span_name_domain": func(r *Registry) {
			component := r.Components["command"]
			component.SpanNameDomains = append(component.SpanNameDomains, component.SpanNameDomains[0])
			r.Components["command"] = component
		},
		"unused_span_name_domain": func(r *Registry) {
			component := r.Components["command"]
			component.SpanNameDomains = append(component.SpanNameDomains, "storage.operations")
			r.Components["command"] = component
		},
		"missing_owned_span_name_domain": func(r *Registry) {
			component := r.Components["command"]
			component.SpanNameDomains = nil
			r.Components["command"] = component
		},
		"missing_variants": func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.Variants = nil }) },
		"unknown_base": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Base = "absent" })
		},
		"base_override": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Attributes["component"] = Binding{Const: "command"} })
		},
		"missing_attribute": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Attributes["missing"] = Binding{Const: "x"} })
		},
		"forbidden_metric_dimension": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Attributes["error_code"] = Binding{Domain: "error_codes"} })
		},
		"declared_metric_dimension": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Attributes["resource_name"] = Binding{Declared: true} })
		},
		"unbounded_binding": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Attributes["reason"] = Binding{} })
		},
		"foreign_constant_domain": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Attributes["reason"] = Binding{Const: "x", Domain: "cache.reason"} })
		},
		"domain_type_mismatch": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Attributes["reason"] = Binding{Domain: "boolean"} })
		},
		"presence_and_absence": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Absent = []string{"operation_outcome"} })
		},
		"duplicate_omission": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Absent = []string{"error_type", "error_type"} })
		},
		"metric_status": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Variants[0].Status = "error" })
		},
		"cardinality_budget":         func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.SeriesBudget = 99 }) },
		"missing_cardinality_budget": func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.SeriesBudget = 0 }) },
		"missing_signal_id":          func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.SignalID = 0 }) },
		"out_of_range_signal_id": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.SignalID = 65536 })
			r.Migration.SignalIDs["command_duration"] = 65536
		},
		"duplicate_signal_id": func(r *Registry) {
			r.Migration.SignalIDs["storage_duration"] = r.Migration.SignalIDs["command_duration"]
		},
		"retired_id_reused":    func(r *Registry) { r.Migration.SignalIDs["retired_metric"] = r.Migration.SignalIDs["command_duration"] },
		"id_moved":             func(r *Registry) { modifySignal(r, "command_duration", func(s *Signal) { s.SignalID = 64 }) },
		"generated_identifier": func(r *Registry) { r.Exports.Constants["ScopeName"] = "collision" },
		"camel_identifier": func(r *Registry) {
			r.Attributes["a_b"] = Attribute{Name: "vv.a", Type: "string", Source: "test", PrivacyClass: "safe", Maturity: "development", Owner: "global", Domains: []string{"ordinary_errors"}}
			r.Attributes["a__b"] = Attribute{Name: "vv.b", Type: "string", Source: "test", PrivacyClass: "safe", Maturity: "development", Owner: "global", Domains: []string{"ordinary_errors"}}
		},
		"log_selection": func(r *Registry) { r.LogCorrelation.Selection = "config_disable" },
		"log_key_duplicate": func(r *Registry) {
			v := r.LogCorrelation.Keys["span_id"]
			v.Name = "trace_id"
			r.LogCorrelation.Keys["span_id"] = v
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r := registryFixture(t)
			mutate(&r)
			if err := validate(r); err == nil {
				t.Fatal("invalid registry was accepted")
			}
		})
	}
}
func TestRegistryDecodingIsStrict(t *testing.T) {
	for name, input := range map[string]string{
		"duplicate_top":          `{"scope":{},"scope":{}}`,
		"duplicate_nested":       `{"scope":{"name":"one","name":"two"}}`,
		"duplicate_array_object": `{"domains":{"x":{"type":"string","values":[{"x":1,"x":2}]}}}`,
		"unknown":                `{"unexpected":true}`,
		"trailing_document":      `{"contract_version":"x"} {}`,
		"trailing_garbage":       `{"contract_version":"x"} x`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registry.json")
			if err := os.WriteFile(path, []byte(input), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := readRegistry(path); err == nil {
				t.Fatal("ambiguous registry document was accepted")
			}
		})
	}
}
func TestGoAndManifestGenerationAreDeterministicAndCurrent(t *testing.T) {
	r := validRegistryFixture(t)
	first, err := render(r)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := renderManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		next, err := render(r)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := renderManifest(r)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, next) || !bytes.Equal(manifest, wire) {
			t.Fatal("generation depends on map iteration order")
		}
	}
	if err := checkOutputs("../../otel/schema_gen.go", first, "../../otel/wire_manifest.json", manifest); err != nil {
		t.Fatal(err)
	}
	var parsed WireManifest
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.ContractVersion != r.ContractVersion || parsed.Scope != r.Scope || len(parsed.Signals) != len(r.Signals) {
		t.Fatal("wire manifest lost registry identity or signals")
	}
	for key, s := range r.Signals {
		if parsed.Signals[key].SignalID != s.SignalID || len(parsed.Signals[key].ResolvedVariants) != len(s.Variants) {
			t.Fatalf("wire descriptor lost signal %s", key)
		}
	}
}

func TestRenderedDescriptorCarriesCompleteRegistryProjection(t *testing.T) {
	r := validRegistryFixture(t)
	code, err := render(r)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "schema_gen.go", code, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantFields := map[string]bool{
		"SpanKind": false, "Source": false, "PrivacyClass": false, "Maturity": false,
		"Semconv": false, "NameDomain": false, "Inputs": false, "ValueSource": false, "ComputedSource": false,
		"DeclaredSources": false,
		"Minimum":         false, "Maximum": false, "MinimumInt64": false, "MaximumInt64": false, "HasMinimum": false, "HasMaximum": false,
	}
	wantMethods := map[string]bool{"AcceptsValue": false, "AcceptsInt64": false, "AcceptsFloat64": false}
	for _, declaration := range file.Decls {
		if group, ok := declaration.(*ast.GenDecl); ok {
			for _, raw := range group.Specs {
				spec, ok := raw.(*ast.TypeSpec)
				if !ok || spec.Name.Name != "SignalDescriptor" {
					continue
				}
				structure := spec.Type.(*ast.StructType)
				for _, field := range structure.Fields.List {
					for _, name := range field.Names {
						if _, expected := wantFields[name.Name]; expected {
							wantFields[name.Name] = true
						}
					}
				}
			}
		}
		method, ok := declaration.(*ast.FuncDecl)
		if ok && method.Recv != nil {
			if _, expected := wantMethods[method.Name.Name]; expected {
				wantMethods[method.Name.Name] = true
			}
		}
	}
	for field, found := range wantFields {
		if !found {
			t.Fatalf("generated SignalDescriptor is missing %s", field)
		}
	}
	for method, found := range wantMethods {
		if !found {
			t.Fatalf("generated SignalDescriptor is missing %s", method)
		}
	}
	text := string(code)
	for key, signal := range r.Signals {
		for _, value := range []string{signal.Source, signal.PrivacyClass, signal.Maturity, signal.Semconv, signal.ValueSource, signal.ComputedSource} {
			if value != "" && !strings.Contains(text, strconv.Quote(value)) {
				t.Fatalf("generated descriptor %s lost %q", key, value)
			}
		}
		for _, input := range signal.Inputs {
			if !strings.Contains(text, strconv.Quote(input)) {
				t.Fatalf("generated descriptor %s lost input %s", key, input)
			}
		}
	}
}
func TestCheckDetectsEitherStaleOutputWithoutWriting(t *testing.T) {
	for _, stale := range []string{"go", "manifest"} {
		t.Run(stale, func(t *testing.T) {
			dir := t.TempDir()
			goPath := filepath.Join(dir, "schema.go")
			manifestPath := filepath.Join(dir, "wire.json")
			source, manifest := []byte("generated Go"), []byte("generated wire")
			currentSource, currentManifest := append([]byte(nil), source...), append([]byte(nil), manifest...)
			if stale == "go" {
				currentSource = []byte("stale")
			} else {
				currentManifest = []byte("stale")
			}
			if err := os.WriteFile(goPath, currentSource, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestPath, currentManifest, 0644); err != nil {
				t.Fatal(err)
			}
			if err := checkOutputs(goPath, source, manifestPath, manifest); err == nil {
				t.Fatal("one stale output passed the combined check")
			}
			for path, want := range map[string][]byte{goPath: currentSource, manifestPath: currentManifest} {
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatal("check mode wrote to an output")
				}
			}
		})
	}
}
func TestCardinalityIsAUnionWithExplicitAbsence(t *testing.T) {
	r := validRegistryFixture(t)
	for key, want := range map[string]int{"command_duration": 100, "cache_operations": 49} {
		s := r.Signals[key]
		s.SeriesBudget = 100000
		bound, err := metricCardinality(r, s)
		if err != nil {
			t.Fatal(err)
		}
		if bound != want {
			t.Fatalf("%s combination bound = %d, want %d", key, bound, want)
		}
		s.Variants = append(s.Variants, s.Variants...)
		duplicated, err := metricCardinality(r, s)
		if err != nil {
			t.Fatal(err)
		}
		if duplicated != bound {
			t.Fatal("overlapping variants were counted more than once")
		}
	}
	s := r.Signals["cache_operations"]
	s.Variants = []Variant{{Attributes: map[string]Binding{"memoized": {Domain: "cache.memoized", Optional: true}}}, {Attributes: map[string]Binding{}}}
	bound, err := metricCardinality(r, s)
	if err != nil {
		t.Fatal(err)
	}
	if bound != 3 {
		t.Fatalf("true, false and absent should form three tuples; got %d", bound)
	}
}
func TestSourceInventoriesMatchRealEnums(t *testing.T) {
	r := validRegistryFixture(t)
	if err := validateInventories(r, "../.."); err != nil {
		t.Fatal(err)
	}
	modifyVocabulary(&r, func(v *Vocabulary) { v.Source.Members = v.Source.Members[1:]; v.Mapping = v.Mapping[1:] })
	if err := validate(r); err != nil {
		t.Fatalf("control must remain internally total: %v", err)
	}
	if err := validateInventories(r, "../.."); err == nil {
		t.Fatal("registry and mapping jointly omitted a real source enum without failing")
	}
}

func TestSourceInventoryRejectsChangedSourceSpelling(t *testing.T) {
	r := validRegistryFixture(t)
	modifyVocabulary(&r, func(v *Vocabulary) {
		v.Source.Members[0].Value = "list"
		v.Mapping[0].From = "list"
	})
	if err := validate(r); err != nil {
		t.Fatalf("control mapping must remain internally valid: %v", err)
	}
	if err := validateInventories(r, "../.."); err == nil {
		t.Fatal("source inventory did not notice that remote methods use uppercase values")
	}
}

func TestCardinalityExpansionRefusesExcessiveProducts(t *testing.T) {
	r := validRegistryFixture(t)
	values := make([]string, 500)
	for index := range values {
		values[index] = fmt.Sprintf("v%d", index)
	}
	r.Domains["large"] = Domain{Type: "string", Values: values}
	for _, key := range []string{"reason", "failure"} {
		a := r.Attributes[key]
		a.Domains = append(a.Domains, "large")
		r.Attributes[key] = a
	}
	s := r.Signals["cache_operations"]
	s.Variants = []Variant{{Attributes: map[string]Binding{"reason": {Domain: "large"}, "failure": {Domain: "large"}}}}
	if _, err := metricCardinality(r, s); err == nil || !strings.Contains(err.Error(), "expansion exceeds") {
		t.Fatalf("unbounded generation work was not rejected by the expansion limit: %v", err)
	}
}
func TestNewComponentsNeedNoGeneratorBranch(t *testing.T) {
	for _, id := range []int{65, 130, 65535} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			r := validRegistryFixture(t)
			c := r.Components["cache_memory"]
			c.WireValue = "quasar"
			c.Source = "A future subsystem"
			r.Components["quasar"] = c
			s := r.Signals["cache_memory_active"]
			s.Component = "quasar"
			s.Name = "vv.quasar.active"
			s.SignalID = id
			s.Availability = "planned"
			s.Inputs = []string{"cache_memory_backend.StatsContext", "cache_memory_stats.Closed"}
			s.Variants = []Variant{{Attributes: map[string]Binding{"component": {Const: "quasar"}}}}
			r.Signals["quasar_active"] = s
			shape := r.SourceShapes["cache_memory_stats"]
			shape.Components = append(shape.Components, "quasar")
			member := shape.Members["Closed"]
			member.Signals = append(member.Signals, "quasar_active")
			shape.Members["Closed"] = member
			r.SourceShapes["cache_memory_stats"] = shape
			backend := r.SourceShapes["cache_memory_backend"]
			backend.Components = append(backend.Components, "quasar")
			stats := backend.Members["StatsContext"]
			stats.Signals = append(stats.Signals, "quasar_active")
			backend.Members["StatsContext"] = stats
			r.SourceShapes["cache_memory_backend"] = backend
			limits := r.SourceShapes["cache_memory_limits"]
			limits.Components = append(limits.Components, "quasar")
			r.SourceShapes["cache_memory_limits"] = limits
			domain := r.Domains["components"]
			domain.Values = append(domain.Values, "quasar")
			r.Domains["components"] = domain
			s.Variants[0].Attributes["component"] = Binding{Domain: "components", Const: "quasar"}
			r.Signals["quasar_active"] = s
			r.Migration.SignalIDs["quasar_active"] = id
			if err := validate(r); err == nil {
				t.Fatal("new signal passed before its independent history append")
			}
			history := signalHistoryWith(t, "quasar_active", id)
			availability := availabilityHistoryWith(t, "quasar_active", "planned")
			if err := validateWithHistories(r, history, availability); err != nil {
				t.Fatal(err)
			}
			code, err := render(r)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(code), "MetricQuasarActive") || !strings.Contains(string(code), "SignalQuasarActive") {
				t.Fatal("a new component did not generate its public descriptors")
			}
		})
	}
}
func TestSourceToWireAndFutureMappings(t *testing.T) {
	r := validRegistryFixture(t)
	cases := []struct{ component, dimension, from, want string }{
		{"remote", "operations", "BulkDelete", "delete_many"},
		{"runtime_lifecycle", "placement", "per-replica", "per_replica"},
		{"runtime_lifecycle", "durability", "non-durable", "non_durable"},
		{"jobs_worker", "outcomes", "cancelled", "canceled"},
		{"jobs_worker", "outcomes", "timed_out", "timeout"},
		{"jobs_worker", "disposition", "cancelled", "canceled"},
		{"jobs_propagation", "outcomes", "tracestate_dropped", "tracestate_dropped"},
		{"storage_stream", "outcomes", "unwrapped", "unwrapped"},
	}
	for _, test := range cases {
		v := vocabularies(r.Components[test.component])[test.dimension]
		found := false
		for _, e := range v.Mapping {
			if e.From == test.from {
				found = e.To == test.want
			}
		}
		if !found {
			t.Fatalf("%s.%s does not normalize %s to %s", test.component, test.dimension, test.from, test.want)
		}
	}
}
func TestHistogramBoundariesAreIndependent(t *testing.T) {
	r := validRegistryFixture(t)
	modifySignal(&r, "storage_duration", func(s *Signal) { s.Boundaries = []float64{.003, .07, 19} })
	code, err := render(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), "func MetricStorageDurationBoundaries() []float64") || !strings.Contains(string(code), "0.003, 0.07, 19") {
		t.Fatal("second histogram did not receive its own boundaries")
	}
	if !strings.Contains(string(code), "var defaultDurationBoundaries = MetricCommandDurationBoundaries()") {
		t.Fatal("legacy command histogram lost its independent boundary alias")
	}
}

func TestAuthoredBudgetDoesNotBecomeTheCalculatedBound(t *testing.T) {
	r := validRegistryFixture(t)
	s := r.Signals["cache_events"]
	bound, err := metricCardinality(r, s)
	if err != nil {
		t.Fatal(err)
	}
	if bound >= s.SeriesBudget {
		t.Fatal("control metric needs an authored budget above its calculated bound")
	}
	s.SeriesBudget = bound - 1
	r.Signals["cache_events"] = s
	if err := validate(r); err == nil {
		t.Fatal("an under-budget metric was accepted")
	}
	s.SeriesBudget = bound + 500
	r.Signals["cache_events"] = s
	manifest, err := buildManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Signals["cache_events"].CardinalityBound != bound {
		t.Fatal("authored budget was copied into the calculated bound")
	}
}

func TestGeneratedNamesAreDirectAndLegacySpellingsRemain(t *testing.T) {
	r := validRegistryFixture(t)
	code, err := render(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SpanAuthentication", "SpanCrudSource", "SpanRemote", "EventAuthRefusal", "EventCache", "OpStorageTemporaryUrl", "OutcomeOk", "CommandSpanName", "CacheBackendOperationName", "MaxResourceNameBytes", "ValidResourceName"} {
		if !strings.Contains(string(code), name) {
			t.Fatalf("generated public name %s is missing", name)
		}
	}
	for _, stutter := range []string{"SpanAuthenticationSpan", "SpanCrudSourceSpan", "SpanRemoteSpan", "EventAuthRefusalEvent", "EventCacheEvent"} {
		if strings.Contains(string(code), stutter) {
			t.Fatalf("generated name still stutters: %s", stutter)
		}
	}
}

func TestWireManifestCarriesExactDeclaredAttributeCharsets(t *testing.T) {
	r := validRegistryFixture(t)
	manifest, err := buildManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, signal := range manifest.Signals {
		for _, variant := range signal.ResolvedVariants {
			for name, field := range variant.Attributes {
				if field.Declared {
					if name != r.Attributes[r.Exports.ResourceAttribute].Name || field.Charset != r.Attributes[r.Exports.ResourceAttribute].Charset {
						t.Fatalf("declared wire charset %s=%q", name, field.Charset)
					}
					found++
				} else if field.Charset != "" {
					t.Fatalf("closed attribute %s acquired declaration charset %q", name, field.Charset)
				}
			}
		}
	}
	if found != 20 {
		t.Fatalf("declared resource variants=%d, want 20", found)
	}
	wire, err := renderManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"charset": "unicode_letter_digit_dot_underscore_hyphen"`) {
		t.Fatal("serialized wire manifest omitted the declared charset")
	}
}

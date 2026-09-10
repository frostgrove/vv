package main

import (
	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/storage"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestResolvedComponentsOwnEveryVariantDomain(t *testing.T) {
	for name, change := range map[string]func(*Registry){
		"foreign_outcome": func(r *Registry) {
			modifySignal(r, "authentication_duration", func(s *Signal) {
				s.Variants[0].Attributes["operation_outcome"] = Binding{Domain: "command.outcomes", Const: "ok"}
			})
		},
		"foreign_operation": func(r *Registry) {
			r.AttributeSets["storage.base"]["operation_name"] = Binding{Domain: "command.operations"}
		},
		"foreign_vocabulary": func(r *Registry) {
			modifySignal(r, "cache_events", func(s *Signal) { s.Variants[0].Attributes["memoized"] = Binding{Domain: "boolean", Const: "false"} })
		},
		"false_global": func(r *Registry) {
			r.GlobalDomains = append(r.GlobalDomains, "command.outcomes")
			modifySignal(r, "authentication_duration", func(s *Signal) {
				s.Variants[0].Attributes["operation_outcome"] = Binding{Domain: "command.outcomes", Const: "ok"}
			})
		},
		"subset_escape": func(r *Registry) {
			modifySignal(r, "storage_operation_bytes", func(s *Signal) {
				s.Variants[0].Attributes["operation_name"] = Binding{Domain: "storage.operations", Values: []string{"get"}}
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := validRegistryFixture(t)
			change(&r)
			if err := validate(r); err == nil {
				t.Fatal("foreign resolved domain accepted")
			}
		})
	}
	r := validRegistryFixture(t)
	for _, variant := range r.Signals["cache_events"].Variants {
		if err := validateVariantOwnership(r, variant); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEveryStructuredSourceSurfaceRejectsDrift(t *testing.T) {
	r := validRegistryFixture(t)
	for key, shape := range r.SourceShapes {
		t.Run(key, func(t *testing.T) {
			if shape.Availability == "planned" {
				if err := validatePlannedSourceShape(key, shape, r, "../.."); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err := validateSourceShape(shape, "../.."); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []string{"addition", "removal", "rename", "type"} {
				t.Run(mutation, func(t *testing.T) {
					changed := shape
					changed.Members = map[string]ShapeMember{}
					for name, member := range shape.Members {
						changed.Members[name] = member
					}
					name := sortedKeys(changed.Members)[0]
					member := changed.Members[name]
					switch mutation {
					case "addition":
						changed.Members["NewSensitiveField"] = ShapeMember{Type: "string", Excluded: "must be explicitly reviewed"}
					case "removal":
						delete(changed.Members, name)
					case "rename":
						delete(changed.Members, name)
						changed.Members["RenamedSensitiveField"] = member
					case "type":
						member.Type = "newSensitiveType"
						changed.Members[name] = member
					}
					if err := validateSourceShape(changed, "../.."); err == nil {
						t.Fatal("source drift accepted")
					}
				})
			}
		})
	}
}

func TestPlannedSourceEvolutionDetectsArrival(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "source")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("existing.go", "package source\ntype Existing struct { Base int }\n")
	current := SourceShape{Availability: "current", Components: []string{"command"}, Kind: "fields", File: "source/existing.go", Type: "Existing", Scope: "current", Members: map[string]ShapeMember{"Base": {Type: "int", Excluded: "not observed"}}}
	additive := SourceShape{Availability: "planned", Evolution: "additive", Components: []string{"command"}, Kind: "fields", File: "source/existing.go", Type: "Existing", Scope: "planned", Members: map[string]ShapeMember{"Added": {Type: "string", Excluded: "not observed"}}}
	reg := Registry{SourceShapes: map[string]SourceShape{"current": current, "additive": additive}}
	if err := validatePlannedSourceShape("additive", additive, reg, root); err != nil {
		t.Fatal(err)
	}
	write("existing.go", "package source\ntype Existing struct { Base int; Added string }\n")
	if err := validatePlannedSourceShape("additive", additive, reg, root); err == nil {
		t.Fatal("arrived additive member remained planned")
	}
	write("existing.go", "package source\ntype Existing struct { Base int }\n")
	withoutCompanion := reg
	withoutCompanion.SourceShapes = map[string]SourceShape{"additive": additive}
	if err := validatePlannedSourceShape("additive", additive, withoutCompanion, root); err == nil {
		t.Fatal("additive source without an exact current companion was accepted")
	}
	overlapping := additive
	overlapping.Members = map[string]ShapeMember{"Base": {Type: "int", Excluded: "not observed"}}
	if err := validatePlannedSourceShape("additive", overlapping, reg, root); err == nil {
		t.Fatal("additive source overlapping its current companion was accepted")
	}
	invalidMode := additive
	invalidMode.Evolution = ""
	if err := validatePlannedSourceShape("additive", invalidMode, reg, root); err == nil {
		t.Fatal("planned source without an evolution mode was accepted")
	}
	prospective := SourceShape{Availability: "planned", Evolution: "new", Components: []string{"command"}, Kind: "fields", File: "source/future.go", Type: "Future", Scope: "planned", Members: map[string]ShapeMember{"Value": {Type: "int", Excluded: "not observed"}}}
	reg.SourceShapes["prospective"] = prospective
	if err := validatePlannedSourceShape("prospective", prospective, reg, root); err != nil {
		t.Fatal(err)
	}
	write("future.go", "package source\ntype Future struct { Value int }\n")
	if err := validatePlannedSourceShape("prospective", prospective, reg, root); err == nil {
		t.Fatal("arrived new source remained planned")
	}
}

func TestStructuredSourceMappingsAreReciprocalAndPrivate(t *testing.T) {
	for name, change := range map[string]func(*Registry){
		"missing_reason": func(r *Registry) {
			s := r.SourceShapes["auth_credential"]
			m := s.Members["Token"]
			m.Excluded = " "
			s.Members["Token"] = m
			r.SourceShapes["auth_credential"] = s
		},
		"missing_input": func(r *Registry) { modifySignal(r, "cache_items", func(s *Signal) { s.Inputs = nil }) },
		"unreciprocated_input": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.Inputs = append(s.Inputs, "cache_event.Items") })
		},
		"foreign_reciprocal": func(r *Registry) {
			s := r.SourceShapes["cache_event"]
			m := s.Members["Items"]
			m.Signals = append(m.Signals, "command_duration")
			s.Members["Items"] = m
			r.SourceShapes["cache_event"] = s
			modifySignal(r, "command_duration", func(s *Signal) { s.Inputs = append(s.Inputs, "cache_event.Items") })
		},
		"unconsumed_attribute": func(r *Registry) {
			s := r.SourceShapes["cache_event"]
			m := s.Members["Items"]
			m.Attributes = []string{"error_type"}
			s.Members["Items"] = m
			r.SourceShapes["cache_event"] = s
		},
		"unknown_computation": func(r *Registry) {
			modifySignal(r, "command_duration", func(s *Signal) { s.ComputedSource = "handwritten secret projection" })
		},
		"no_value_source": func(r *Registry) { modifySignal(r, "storage_operation_bytes", func(s *Signal) { s.ValueSource = "" }) },
		"value_not_input": func(r *Registry) {
			modifySignal(r, "storage_operation_bytes", func(s *Signal) { s.ValueSource = "storage_info.ETag" })
		},
		"non_numeric_direct_value": func(r *Registry) {
			modifySignal(r, "cache_encoded_bytes", func(s *Signal) { s.ValueSource = "cache_event.Operation" })
		},
		"implemented_planned_source": func(r *Registry) {
			modifySignal(r, "storage_stream_duration", func(s *Signal) { s.Availability = "implemented" })
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := validRegistryFixture(t)
			change(&r)
			if err := validate(r); err == nil {
				t.Fatal("incomplete or foreign source mapping accepted")
			}
		})
	}
	r := validRegistryFixture(t)
	for _, field := range []string{"Active", "Limit", "Definition", "Binding", "AdmissionGroup"} {
		if strings.TrimSpace(r.SourceShapes["jobs_worker_event"].Members[field].Excluded) == "" {
			t.Fatalf("worker %s lacks semantic/privacy exclusion", field)
		}
	}
	for _, key := range []string{"remote_call", "auth_credential", "auth_principal", "auth_claims", "jobs_delivery_meta", "jobs_trace_carrier", "jobs_context_capture", "jobs_identity_restore_request", "jobs_restored_identity", "storage_link", "storage_store"} {
		if _, ok := r.SourceShapes[key]; !ok {
			t.Fatalf("missing privacy surface %s", key)
		}
	}
}

func TestEveryVaryingProjectionHasSourceProvenance(t *testing.T) {
	for name, change := range map[string]func(*Registry){
		"field_attribute": func(r *Registry) {
			shape := r.SourceShapes["cache_event"]
			member := shape.Members["Memoized"]
			member.Attributes = nil
			shape.Members["Memoized"] = member
			r.SourceShapes["cache_event"] = shape
		},
		"method_identity_coverage": func(r *Registry) {
			modifySignal(r, "command_duration", func(signal *Signal) {
				inputs := signal.Inputs[:0]
				for _, input := range signal.Inputs {
					if input != "command_service.DeleteMany" {
						inputs = append(inputs, input)
					}
				}
				signal.Inputs = inputs
			})
			shape := r.SourceShapes["command_service"]
			member := shape.Members["DeleteMany"]
			member.Signals = []string{"command_span"}
			shape.Members["DeleteMany"] = member
			r.SourceShapes["command_service"] = shape
		},
		"result_classification": func(r *Registry) {
			shape := r.SourceShapes["auth_authenticator"]
			member := shape.Members["Authenticate"]
			member.Type = "func(ctx context.Context, c Credential) Principal"
			shape.Members["Authenticate"] = member
			r.SourceShapes["auth_authenticator"] = shape
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := validRegistryFixture(t)
			change(&r)
			if err := validate(r); err == nil {
				t.Fatal("unproven varying projection was accepted")
			}
		})
	}
}

func TestNestedSignalInputsMustBeReachable(t *testing.T) {
	r := validRegistryFixture(t)
	for _, reference := range []string{"jobs_worker_event.Operation", "jobs_worker_event.Outcome", "jobs_worker_event.Results", "jobs_worker_observer.Observe"} {
		shapeKey, memberName, _ := strings.Cut(reference, ".")
		shape := r.SourceShapes[shapeKey]
		member := shape.Members[memberName]
		remaining := member.Signals[:0]
		for _, signal := range member.Signals {
			if signal != "jobs_worker_delivery_results" {
				remaining = append(remaining, signal)
			}
		}
		member.Signals = remaining
		shape.Members[memberName] = member
		r.SourceShapes[shapeKey] = shape
	}
	modifySignal(&r, "jobs_worker_delivery_results", func(signal *Signal) {
		signal.Inputs = []string{
			"jobs_worker_delivery_result_count.Control",
			"jobs_worker_delivery_result_count.Items",
			"jobs_worker_delivery_result_count.Mutation",
		}
	})
	if err := validate(r); err == nil {
		t.Fatal("orphaned nested result inputs were accepted")
	}
}

func TestSyntheticComponentUsesOnlyGenericProjectionRules(t *testing.T) {
	r := validRegistryFixture(t)
	components := r.Domains["components"]
	components.Values = append(components.Values, "quasar")
	r.Domains["components"] = components
	r.Domains["quasar.operations"] = Domain{Type: "string", Values: []string{"pulse", "scan"}}
	operationName := r.Attributes["operation_name"]
	operationName.Domains = append(operationName.Domains, "quasar.operations")
	r.Attributes["operation_name"] = operationName
	operations := Vocabulary{
		Source:  Inventory{Kind: "projection", Symbol: "quasar calls", Members: []SourceMember{{Symbol: "Pulse", Value: "Pulse"}, {Symbol: "Scan", Value: "Scan"}}},
		Domain:  "quasar.operations",
		Mapping: []MappingEntry{{From: "Pulse", To: "pulse"}, {From: "Scan", To: "scan"}},
		Unknown: "drop",
	}
	r.Components["quasar"] = Component{
		SourceCoverage:  "structured",
		SourceRationale: "synthetic generic projection fixture",
		WireValue:       "quasar",
		Source:          "synthetic quasar calls",
		Maturity:        "development",
		Operations:      operations,
		Outcomes:        Vocabulary{Source: Inventory{Kind: "registry", Symbol: "no outcomes"}, Unknown: "drop"},
		Vocabularies:    map[string]Vocabulary{},
	}
	r.SourceShapes["quasar_calls"] = SourceShape{
		Availability: "planned",
		Evolution:    "new",
		Components:   []string{"quasar"},
		Kind:         "functions",
		File:         "quasar/quasar.go",
		Type:         "Package",
		Scope:        "synthetic generic projection fixture",
		Members: map[string]ShapeMember{
			"Pulse": {Type: "func()", Signals: []string{"quasar_calls"}},
			"Scan":  {Type: "func()", Signals: []string{"quasar_calls"}},
		},
	}
	r.Signals["quasar_calls"] = Signal{
		Inputs:         []string{"quasar_calls.Pulse", "quasar_calls.Scan"},
		ComputedSource: "event_count",
		SignalID:       65,
		Provider:       "meter",
		APIKind:        "int64_counter",
		Kind:           "metric",
		Name:           "vv.quasar.calls",
		Component:      "quasar",
		Instrument:     "counter",
		NumberType:     "int64",
		Unit:           "{call}",
		Description:    "Synthetic quasar calls",
		Source:         "synthetic quasar calls",
		PrivacyClass:   "safe",
		Maturity:       "development",
		Semconv:        "none",
		Availability:   "planned",
		SeriesBudget:   2,
		RecordWhen:     "non_negative",
		Variants: []Variant{{Attributes: map[string]Binding{
			"component":      {Const: "quasar", Domain: "components"},
			"operation_name": {Domain: "quasar.operations"},
		}}},
	}
	r.Migration.SignalIDs["quasar_calls"] = 65
	history := signalHistoryWith(t, "quasar_calls", 65)
	availability := availabilityHistoryWith(t, "quasar_calls", "planned")
	if err := validateWithHistories(r, history, availability); err != nil {
		t.Fatal(err)
	}
	operations.Source.Members = append(operations.Source.Members, SourceMember{Symbol: "Eclipse", Value: "Eclipse"})
	operations.Mapping = append(operations.Mapping, MappingEntry{From: "Eclipse", To: "eclipse"})
	r.Components["quasar"] = func(component Component) Component {
		component.Operations = operations
		return component
	}(r.Components["quasar"])
	domain := r.Domains["quasar.operations"]
	domain.Values = append(domain.Values, "eclipse")
	r.Domains["quasar.operations"] = domain
	if err := validateWithHistories(r, history, availability); err == nil {
		t.Fatal("synthetic component operation without a source input was accepted")
	}
}

func TestAuthoredVariantsMustBePairwiseDisjoint(t *testing.T) {
	r := validRegistryFixture(t)
	modifySignal(&r, "cache_memory_active", func(signal *Signal) {
		signal.Variants = append(signal.Variants, signal.Variants[0])
	})
	if err := validate(r); err == nil {
		t.Fatal("two authored variants admitted the same source projection")
	}
}

func TestVariantOverlapAccountsForStatusOptionalityAndFacts(t *testing.T) {
	r := validRegistryFixture(t)
	attribute := func(values []string, optional bool) WireAttribute {
		return WireAttribute{Type: "string", Values: values, Optional: optional}
	}
	base := WireVariant{Status: "error", Attributes: map[string]WireAttribute{"vv.operation.name": attribute([]string{"one"}, false)}}
	if !wireVariantsOverlap(r, base, base) {
		t.Fatal("identical variants were considered disjoint")
	}
	differentStatus := base
	differentStatus.Status = "unset"
	if wireVariantsOverlap(r, base, differentStatus) {
		t.Fatal("different status projections overlapped")
	}
	differentValue := WireVariant{Status: "error", Attributes: map[string]WireAttribute{"vv.operation.name": attribute([]string{"two"}, false)}}
	if wireVariantsOverlap(r, base, differentValue) {
		t.Fatal("disjoint required attribute values overlapped")
	}
	optionalOne := WireVariant{Attributes: map[string]WireAttribute{"vv.operation.name": attribute([]string{"one"}, true)}}
	optionalTwo := WireVariant{Attributes: map[string]WireAttribute{"vv.operation.name": attribute([]string{"two"}, true)}}
	if !wireVariantsOverlap(r, optionalOne, optionalTwo) {
		t.Fatal("two optional attributes did not overlap through absence")
	}
	greater := WireVariant{When: []SourcePredicate{{Fact: "count", Operator: "gt", Value: 0}}}
	zero := WireVariant{When: []SourcePredicate{{Fact: "count", Operator: "eq", Value: 0}}}
	if wireVariantsOverlap(r, greater, zero) {
		t.Fatal("mutually exclusive source predicates overlapped")
	}
	more := WireVariant{When: []SourcePredicate{{Fact: "count", Operator: "gt", Value: 1}}}
	if !wireVariantsOverlap(r, greater, more) {
		t.Fatal("compatible source predicates were considered disjoint")
	}
}

func TestCallableProjectionUsesOnlyResultTypes(t *testing.T) {
	identifiers := memberOutputIdentifiers("func[P any](ctx context.Context, value P, failure error) (Staged, error)")
	if !identifiers["Staged"] || !identifiers["error"] || identifiers["Context"] || identifiers["P"] {
		t.Fatalf("generic callable result identifiers = %v", identifiers)
	}
	if memberResultClassifies("func(failure error) bool") {
		t.Fatal("an error parameter was mistaken for result classification")
	}
}

func TestRequiredProjectionInputsCannotBeJointlyErased(t *testing.T) {
	required := map[string][]string{
		"cache_value_bytes":            {"cache_backend_event.Operation", "cache_backend_event.Outcome"},
		"cache_charged_bytes":          {"cache_backend_event.Operation", "cache_backend_event.Outcome"},
		"cache_memory_entries":         {"cache_memory_stats.Closed"},
		"cache_memory_bytes":           {"cache_memory_stats.Closed"},
		"cache_memory_entry_limit":     {"cache_memory_stats.Closed"},
		"cache_memory_byte_limit":      {"cache_memory_stats.Closed"},
		"jobs_worker_duration":         {"jobs_worker_event.Operation", "jobs_worker_event.Outcome"},
		"jobs_worker_items":            {"jobs_worker_event.Operation", "jobs_worker_event.Outcome", "jobs_worker_event.Failure"},
		"jobs_worker_bytes":            {"jobs_worker_event.Operation", "jobs_worker_event.Outcome", "jobs_worker_event.Failure"},
		"jobs_worker_admission":        {"jobs_worker_event.Operation", "jobs_worker_event.Outcome"},
		"jobs_worker_delivery_results": {"jobs_worker_event.Operation"},
		"jobs_worker_released":         {"jobs_worker_event.Operation", "jobs_worker_event.Outcome"},
	}
	for signalKey, references := range required {
		for _, reference := range references {
			t.Run(signalKey+"/"+reference, func(t *testing.T) {
				r := validRegistryFixture(t)
				modifySignal(&r, signalKey, func(signal *Signal) {
					signal.Inputs = withoutString(signal.Inputs, reference)
				})
				shapeKey, memberName, _ := strings.Cut(reference, ".")
				shape := r.SourceShapes[shapeKey]
				member := shape.Members[memberName]
				member.Signals = withoutString(member.Signals, signalKey)
				member.Gates = withoutString(member.Gates, signalKey)
				shape.Members[memberName] = member
				r.SourceShapes[shapeKey] = shape
				if err := validate(r); err == nil {
					t.Fatal("joint source/input deletion erased required projection provenance")
				}
			})
		}
	}
}

func TestDirectMeasurementsRequireAnObservationBoundary(t *testing.T) {
	r := validRegistryFixture(t)
	modifySignal(&r, "cache_encoded_bytes", func(signal *Signal) {
		signal.Inputs = withoutString(signal.Inputs, "cache_observer.Observe")
	})
	shape := r.SourceShapes["cache_observer"]
	member := shape.Members["Observe"]
	member.Signals = withoutString(member.Signals, "cache_encoded_bytes")
	shape.Members["Observe"] = member
	r.SourceShapes["cache_observer"] = shape
	if err := validate(r); err == nil {
		t.Fatal("direct value survived coordinated removal of its callable observation boundary")
	}
}

func TestDeclaredAttributesRequireStructuredConstructionSources(t *testing.T) {
	for signalKey, references := range map[string][]string{
		"command_span": {"otel_config.ResourceName", "otel_service_functions.WithServiceResource"},
		"storage_span": {"otel_config.ResourceName", "otel_storage_functions.WithStorageResource"},
	} {
		r := validRegistryFixture(t)
		declared := r.Signals[signalKey].DeclaredSources["resource_name"]
		if len(declared) != len(references) {
			t.Fatalf("%s declared resource source count=%d, want %d", signalKey, len(declared), len(references))
		}
		for index, reference := range references {
			if declared[index] != reference {
				t.Fatalf("%s declared source %d=%s, want %s", signalKey, index, declared[index], reference)
			}
			t.Run(signalKey+"/"+reference, func(t *testing.T) {
				r := validRegistryFixture(t)
				modifySignal(&r, signalKey, func(signal *Signal) {
					signal.Inputs = withoutString(signal.Inputs, reference)
				})
				shapeKey, memberName, _ := strings.Cut(reference, ".")
				shape := r.SourceShapes[shapeKey]
				member := shape.Members[memberName]
				member.Signals = withoutString(member.Signals, signalKey)
				member.Attributes = withoutString(member.Attributes, "resource_name")
				if len(member.Signals) == 0 && len(member.Attributes) == 0 && member.Nested == "" {
					member.Excluded = "mutated construction source"
				}
				shape.Members[memberName] = member
				r.SourceShapes[shapeKey] = shape
				if err := validateSignalProjection(r, signalKey, r.Signals[signalKey]); err == nil {
					t.Fatal("one declared construction source was jointly erased from signal and shape")
				}
				if err := validate(r); err == nil {
					t.Fatal("full validation accepted a missing declared construction source")
				}
			})
		}
		t.Run(signalKey+"/declared_contract", func(t *testing.T) {
			for name, mutate := range map[string]func(*Signal){
				"missing": func(signal *Signal) {
					signal.DeclaredSources["resource_name"] = signal.DeclaredSources["resource_name"][:1]
				},
				"duplicate": func(signal *Signal) {
					signal.DeclaredSources["resource_name"] = append(signal.DeclaredSources["resource_name"], signal.DeclaredSources["resource_name"][0])
				},
				"foreign":      func(signal *Signal) { signal.DeclaredSources["resource_name"][0] = "cache_event.Operation" },
				"non_declared": func(signal *Signal) { signal.DeclaredSources["reason"] = []string{signal.Inputs[0]} },
			} {
				t.Run(name, func(t *testing.T) {
					r := validRegistryFixture(t)
					modifySignal(&r, signalKey, mutate)
					if err := validate(r); err == nil {
						t.Fatal("invalid declared construction-source contract was accepted")
					}
				})
			}
		})
	}
}

func withoutString(values []string, removed string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != removed {
			result = append(result, value)
		}
	}
	return result
}

func TestSourcePresencePredicatesAreClosedAndDeclared(t *testing.T) {
	for name, change := range map[string]func(*Registry){
		"unknown_fact": func(r *Registry) {
			modifySignal(r, "cache_payload_bytes", func(s *Signal) { s.Variants[0].When[0].Fact = "unregistered" })
		},
		"invalid_operator": func(r *Registry) {
			modifySignal(r, "cache_payload_bytes", func(s *Signal) { s.Variants[0].When[0].Operator = "eval" })
		},
		"negative_threshold": func(r *Registry) {
			modifySignal(r, "cache_payload_bytes", func(s *Signal) { s.Variants[0].When[0].Value = -1 })
		},
		"duplicate_predicate": func(r *Registry) {
			modifySignal(r, "cache_payload_bytes", func(s *Signal) { s.Variants[0].When = append(s.Variants[0].When, s.Variants[0].When[0]) })
		},
		"duplicate_id": func(r *Registry) { r.SourceFacts["duplicate"] = r.SourceFacts["cache_encoded_bytes"] },
		"out_of_range": func(r *Registry) {
			f := r.SourceFacts["cache_encoded_bytes"]
			f.ID = 65536
			r.SourceFacts["cache_encoded_bytes"] = f
		},
		"unbounded_member": func(r *Registry) {
			f := r.SourceFacts["cache_encoded_bytes"]
			f.Member = "Cache"
			r.SourceFacts["cache_encoded_bytes"] = f
		},
		"foreign_owner": func(r *Registry) {
			f := r.SourceFacts["cache_encoded_bytes"]
			f.Component = "command"
			r.SourceFacts["cache_encoded_bytes"] = f
		},
		"undeclared_fact_input": func(r *Registry) {
			s := r.SourceShapes["cache_event"]
			m := s.Members["EncodedBytes"]
			m.Signals = []string{"cache_encoded_bytes"}
			s.Members["EncodedBytes"] = m
			r.SourceShapes["cache_event"] = s
			modifySignal(r, "cache_payload_bytes", func(s *Signal) {
				inputs := []string{}
				for _, input := range s.Inputs {
					if input != "cache_event.EncodedBytes" {
						inputs = append(inputs, input)
					}
				}
				s.Inputs = inputs
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := validRegistryFixture(t)
			change(&r)
			if err := validate(r); err == nil {
				t.Fatal("invalid presence fact accepted")
			}
		})
	}
}

func TestBoundedCleanupAndRecoveryResults(t *testing.T) {
	r := validRegistryFixture(t)
	maximum := r.Signals["storage_cleanup_removed"].Max
	if maximum.String() != strconv.FormatInt(storage.MaxCleanupLimit, 10) {
		t.Fatal("cleanup maximum drifted from real source")
	}
	for _, value := range []string{"NaN", "Infinity", "-1"} {
		s := r.Signals["storage_cleanup_removed"]
		s.Max = numericBound(value)
		if err := validateSignalShape(s); err == nil {
			t.Fatal("invalid maximum accepted")
		}
	}
	for key, id := range map[string]int{"storage_cleanup_removed": 58, "jobs_worker_released": 59} {
		s := r.Signals[key]
		if s.SignalID != id || s.RecordWhen != "non_negative" || s.APIKind != "int64_histogram" || s.Unit != "{item}" {
			t.Fatalf("%s measurement contract drifted", key)
		}
		count, err := metricCardinality(r, s)
		if err != nil {
			t.Fatal(err)
		}
		want := 2
		if key == "jobs_worker_released" {
			want = 3
		}
		if count != want {
			t.Fatalf("%s bound=%d, want %d", key, count, want)
		}
	}
	for _, out := range r.Domains["jobs_worker.outcomes"].Values {
		for _, more := range []string{"true", "false"} {
			want := out == "complete" || out == "empty" && more == "false"
			attrs := tuple(r, map[string]string{"component": "jobs_worker", "operation_name": "recover", "operation_outcome": out, "more": more})
			if acceptsTuple(t, r, "jobs_worker_released", attrs) != want {
				t.Fatalf("recovery %s/%s matrix drifted", out, more)
			}
		}
	}
}

func TestPlannedSourcesRequireReciprocalInputsAndFailPrematurePromotion(t *testing.T) {
	for _, key := range []string{"storage_stream"} {
		t.Run(key, func(t *testing.T) {
			r := validRegistryFixture(t)
			shape := r.SourceShapes[key]
			if shape.Availability != "planned" {
				t.Fatal("prospective source is not explicitly planned")
			}
			if !oneOf(shape.Evolution, "new", "additive") {
				t.Fatal("prospective source has no evolution mode")
			}
			shape.Availability = "current"
			r.SourceShapes[key] = shape
			if err := validateInventories(r, "../.."); err == nil {
				t.Fatal("premature source promotion accepted")
			}
			for name, member := range shape.Members {
				if len(member.Signals) > 0 {
					r = validRegistryFixture(t)
					signal := member.Signals[0]
					s := r.Signals[signal]
					inputs := []string{}
					for _, input := range s.Inputs {
						if input != key+"."+name {
							inputs = append(inputs, input)
						}
					}
					s.Inputs = inputs
					r.Signals[signal] = s
					if err := validate(r); err == nil {
						t.Fatal("planned source lost its reciprocal input")
					}
					break
				}
			}
		})
	}
}

func TestWorkerElapsedRequiresItsPresencePredicate(t *testing.T) {
	r := validRegistryFixture(t)
	for _, variant := range r.Signals["jobs_worker_duration"].Variants {
		if len(variant.When) != 1 || variant.When[0] != (SourcePredicate{Fact: "jobs_worker_elapsed", Operator: "gt", Value: 0}) {
			t.Fatal("ambiguous clock-failure zero became a measured duration")
		}
	}
}

func TestEnqueueUsesPublicOnceOutcomeNotPlacementDetails(t *testing.T) {
	r := validRegistryFixture(t)
	v := r.Components["jobs_enqueue"].Vocabularies["once_outcome"]
	if err := validateInventory(v.Source, "../.."); err != nil {
		t.Fatal(err)
	}
	for _, outcome := range []jobs.EnqueueOnceOutcome{jobs.EnqueueCreated, jobs.EnqueueExistingSamePayload, jobs.EnqueueConflict} {
		matched := false
		for _, mapping := range v.Mapping {
			matched = matched || mapping.From == outcome.String() && mapping.To == outcome.String()
		}
		if !matched {
			t.Fatal("public EnqueueOnce outcome mapping drifted")
		}
	}
	for _, shape := range []string{"jobs_placement_result", "jobs_staged"} {
		member := r.SourceShapes[shape].Members["Outcome"]
		if len(member.Signals) > 0 || member.Excluded == "" {
			t.Fatal("transactional staging inspected internal placement outcome")
		}
	}
}

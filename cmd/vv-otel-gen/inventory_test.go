package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostgrove/vv/internal/otelreg"
)

func exactInventorySnapshotError(reg Registry) error {
	wantComponents := []string{
		"auth_refusal", "authentication", "cache", "cache_backend", "cache_memory", "command",
		"crud_source", "health", "jobs_enqueue", "jobs_handler", "jobs_propagation", "jobs_scheduler",
		"jobs_worker", "remote", "runtime_lifecycle", "runtime_periodic", "storage", "storage_stream",
	}
	components := sortedKeys(reg.Components)
	if strings.Join(components, "\x00") != strings.Join(wantComponents, "\x00") {
		return fmt.Errorf("component roster=%v, want %v", components, wantComponents)
	}
	signalKinds := map[string]int{}
	availability := map[string]int{}
	for _, signal := range reg.Signals {
		signalKinds[signal.Kind]++
		availability[signal.Availability]++
	}
	if len(reg.Signals) != 59 || signalKinds["metric"] != 45 || signalKinds["span"] != 11 || signalKinds["span_event"] != 3 || len(signalKinds) != 3 {
		return fmt.Errorf("signal inventory=%d kinds=%v", len(reg.Signals), signalKinds)
	}
	if availability["implemented"] != 59 || len(availability) != 1 {
		return fmt.Errorf("signal availability=%v", availability)
	}
	var history SignalAvailabilityHistory
	if err := json.Unmarshal(otelreg.AvailabilityHistory(), &history); err != nil {
		return fmt.Errorf("availability authority: %w", err)
	}
	signals, historicalSignals := sortedKeys(reg.Signals), sortedKeys(history.Signals)
	if strings.Join(signals, "\x00") != strings.Join(historicalSignals, "\x00") {
		return fmt.Errorf("signal roster=%v, want %v", signals, historicalSignals)
	}
	shapeAvailability := map[string]int{}
	for _, shape := range reg.SourceShapes {
		shapeAvailability[shape.Availability]++
	}
	if len(reg.SourceShapes) != 86 || shapeAvailability["current"] != 86 || len(shapeAvailability) != 1 {
		return fmt.Errorf("source-shape inventory=%d availability=%v", len(reg.SourceShapes), shapeAvailability)
	}
	wantShapes := strings.Fields(`
auth_authenticator auth_claims auth_credential auth_observer auth_principal auth_reason
cache_backend_event cache_event cache_memory_backend cache_memory_limits cache_memory_observer cache_memory_stats cache_observer
command_restorable_service command_service
crud_beginner crud_executor_unwrapper crud_identified crud_read_sourcer crud_result crud_rows
crud_source_interface crud_source_unwrapper crud_transaction crud_transactional crud_unsafe_bulk_inserter
health_contribution health_probe health_report
jobs_adapter_handler jobs_attempt_controller jobs_context_capture
jobs_context_capture_request jobs_context_capture_spec jobs_correlation_field jobs_delivery_meta jobs_durable_context
jobs_enqueue_functions jobs_enqueue_once_outcome jobs_handler_callback
jobs_identity_restore_request jobs_lease_fence jobs_placement_result jobs_restored_identity
jobs_schedule_event jobs_schedule_observer jobs_schedule_result jobs_staged jobs_system_context_builders
jobs_trace_carrier jobs_trace_carrier_spec jobs_trusted_context_provider jobs_trusted_identity_restorer
jobs_worker_delivery_result_count jobs_worker_event jobs_worker_observer
otel_config otel_health_functions otel_periodic_functions otel_service_functions otel_storage_functions
remote_call remote_protocol_error remote_transport
runtime_declaration runtime_lifecycle_event runtime_lifecycle_observer runtime_observer runtime_periodic_spec runtime_runner_state
storage_capabilities storage_cleanup_options storage_cleanup_result storage_delete_options storage_info storage_key storage_link
storage_promote_options storage_put_options storage_read_options storage_stage_id storage_stage_options storage_staged
storage_store storage_stream storage_temporary_url_options
`)
	shapes := sortedKeys(reg.SourceShapes)
	if strings.Join(shapes, "\x00") != strings.Join(wantShapes, "\x00") {
		return fmt.Errorf("source-shape roster=%v, want %v", shapes, wantShapes)
	}
	return exactObservationBoundaryRostersError(reg)
}

func exactObservationBoundaryRostersError(reg Registry) error {
	want := map[string][]string{
		"cache_observer.Observe": {
			"cache_operations", "cache_event", "cache_events", "cache_items", "cache_encoded_bytes", "cache_payload_bytes",
		},
		"cache_memory_observer.Observe": {
			"cache_operations", "cache_backend_event", "cache_events", "cache_items", "cache_value_bytes", "cache_charged_bytes",
		},
		"jobs_worker_observer.Observe": {
			"jobs_worker_operations", "jobs_worker_duration", "jobs_worker_items", "jobs_worker_bytes",
			"jobs_worker_admission", "jobs_worker_delivery_results", "jobs_worker_dispositions", "jobs_worker_released",
		},
		"jobs_adapter_handler.Invoke": {
			"jobs_handler_span", "jobs_handler_duration", "jobs_queue_delay", "jobs_handler_attempt",
		},
		"jobs_schedule_observer.Observe": {
			"jobs_scheduler_cycles", "jobs_scheduler_duration", "jobs_scheduler_results",
		},
	}
	for reference, expected := range want {
		member, ok := shapeMember(reg, reference)
		if !ok || strings.Join(member.Signals, "\x00") != strings.Join(expected, "\x00") {
			return fmt.Errorf("observation boundary %s roster=%v, want %v", reference, member.Signals, expected)
		}
	}
	return nil
}

func TestRegistryHasTheExactA02InventorySnapshot(t *testing.T) {
	if err := exactInventorySnapshotError(validRegistryFixture(t)); err != nil {
		t.Fatal(err)
	}
}

func TestObservationBoundarySignalRostersAreExact(t *testing.T) {
	if err := exactObservationBoundaryRostersError(validRegistryFixture(t)); err != nil {
		t.Fatal(err)
	}
}

func TestExactA02InventorySnapshotDetectsOtherwiseValidCoordinatedDrift(t *testing.T) {
	tests := map[string]func(*Registry){
		"unused_component": func(reg *Registry) {
			component := reg.Components["cache_memory"]
			component.WireValue = "quasar"
			component.Source = "synthetic independently installable component"
			reg.Components["quasar"] = component
			domain := reg.Domains["components"]
			domain.Values = append(domain.Values, "quasar")
			reg.Domains["components"] = domain
		},
		"excluded_planned_shape": func(reg *Registry) {
			reg.SourceShapes["quasar_future"] = SourceShape{
				Availability: "planned",
				Evolution:    "new",
				Components:   []string{"command"},
				Kind:         "fields",
				File:         "port/command.go",
				Type:         "QuasarFutureTelemetry",
				Scope:        "Synthetic excluded future source.",
				Members: map[string]ShapeMember{
					"Ignored": {Type: "string", Excluded: "Synthetic member is deliberately outside telemetry."},
				},
			}
		},
		"same_count_shape_replacement": func(reg *Registry) {
			shape := reg.SourceShapes["jobs_system_context_builders"]
			delete(reg.SourceShapes, "jobs_system_context_builders")
			reg.SourceShapes["quasar_system_context_builders"] = shape
		},
		"signal_kind_flip": func(reg *Registry) {
			signal := reg.Signals["storage_duration"]
			signal.Kind = "span"
			signal.Provider = "tracer"
			signal.APIKind = ""
			signal.Instrument = ""
			signal.NumberType = ""
			signal.Unit = ""
			signal.Boundaries = nil
			signal.Min = ""
			signal.Max = ""
			signal.SeriesBudget = 0
			signal.RecordWhen = ""
			signal.SpanKind = "internal"
			signal.NameDomain = "storage.operations"
			reg.Signals["storage_duration"] = signal
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			reg := validRegistryFixture(t)
			mutate(&reg)
			if err := validate(reg); err != nil {
				t.Fatalf("mutation is not an otherwise-valid control: %v", err)
			}
			if err := validateInventories(reg, "../.."); err != nil {
				t.Fatalf("mutation is not source-valid: %v", err)
			}
			if err := exactInventorySnapshotError(reg); err == nil {
				t.Fatal("coordinated inventory drift escaped the exact snapshot")
			}
		})
	}
}

func TestEveryMethodInventoryRejectsSourceDrift(t *testing.T) {
	r := validRegistryFixture(t)
	count := 0
	for component, c := range r.Components {
		for dimension, v := range vocabularies(c) {
			if v.Source.Kind != "methods" && v.Source.Kind != "functions" {
				continue
			}
			count++
			t.Run(component+"."+dimension, func(t *testing.T) {
				if err := validateInventory(v.Source, "../.."); err != nil {
					t.Fatal(err)
				}
				s := v.Source
				s.Members = append([]SourceMember(nil), s.Members[1:]...)
				if err := validateInventory(s, "../.."); err == nil {
					t.Fatal("complete source member was silently omitted")
				}
				s = v.Source
				s.Members = append([]SourceMember(nil), s.Members...)
				s.Members[0].Value = "invented_source_spelling"
				if err := validateInventory(s, "../.."); err == nil {
					t.Fatal("method source spelling was not checked")
				}
				root := t.TempDir()
				s = v.Source
				var source strings.Builder
				source.WriteString("package fixture\n")
				if s.Kind == "methods" {
					s.Interfaces = []InterfaceRef{{File: "methods.go", Type: "Surface"}}
					source.WriteString("type Embedded interface {\n")
					for _, member := range s.Members {
						source.WriteString(member.Symbol + "()\n")
					}
					source.WriteString("}\ntype Surface interface { Embedded }\n")
				} else {
					s.File = "methods.go"
					for _, member := range s.Members {
						source.WriteString("func " + member.Symbol + "() {}\n")
					}
				}
				path := filepath.Join(root, "methods.go")
				if err := os.WriteFile(path, []byte(source.String()), 0600); err != nil {
					t.Fatal(err)
				}
				if err := validateInventory(s, root); err != nil {
					t.Fatalf("fixture control: %v", err)
				}
				drift := source.String()
				if s.Kind == "methods" {
					drift = strings.Replace(drift, "interface {\n", "interface {\nFutureMethod()\n", 1)
				} else {
					drift += "\nfunc FutureMethod() {}\n"
				}
				if err := os.WriteFile(path, []byte(drift), 0600); err != nil {
					t.Fatal(err)
				}
				if err := validateInventory(s, root); err == nil {
					t.Fatal("new source method/function was not detected")
				}
			})
		}
	}
	if count != 7 {
		t.Fatalf("method/function inventory controls = %d, want 7", count)
	}
}

func TestMethodInventoryResolvesGenericAliasesAndImportedEmbeddings(t *testing.T) {
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module fixture.example\n\ngo 1.26\n")
	write("embedded/embedded.go", "package embedded\ntype Parent interface { Imported() }\n")
	write("methods.go", `package fixture
import (
 edge "fixture.example/embedded"
 "io"
)
type Local[T any] interface { Local(T) }
type Alias = Local[string]
type Surface interface { Alias; edge.Parent; io.Closer; Own() }
`)
	s := Inventory{Kind: "methods", Interfaces: []InterfaceRef{{File: "methods.go", Type: "Surface"}}, Members: []SourceMember{
		{Symbol: "Local", Value: "Local"}, {Symbol: "Imported", Value: "Imported"}, {Symbol: "Close", Value: "Close"}, {Symbol: "Own", Value: "Own"},
	}}
	if err := validateInventory(s, root); err != nil {
		t.Fatal(err)
	}
	write("embedded/embedded.go", "package embedded\ntype Parent interface { Imported(); Added() }\n")
	if err := validateInventory(s, root); err == nil {
		t.Fatal("imported embedding drift was ignored")
	}
	write("methods.go", "package fixture\ntype Surface interface { Surface }\n")
	if err := validateInventory(s, root); err == nil {
		t.Fatal("recursive interface was accepted")
	}
	write("methods.go", "package fixture\ntype Surface struct {}\n")
	if err := validateInventory(s, root); err == nil {
		t.Fatal("non-interface inventory was accepted")
	}
}

func TestEnumInventoryIncludesTypedAliasConstants(t *testing.T) {
	source := Inventory{
		Kind: "enum",
		File: "cmd/vv-otel-gen/testdata/enum_alias.go.txt",
		Type: "Outcome",
		Members: []SourceMember{
			{Symbol: "OutcomeReady", Value: "ready"},
			{Symbol: "OutcomeAlias", Value: "ready"},
			{Symbol: "OutcomeWrapped", Value: "wrapped"},
		},
	}
	if err := validateInventory(source, "../.."); err != nil {
		t.Fatal(err)
	}
	withoutAlias := source
	withoutAlias.Members = append([]SourceMember(nil), source.Members[1:]...)
	if err := validateInventory(withoutAlias, "../.."); err == nil {
		t.Fatal("typed alias constant was omitted from the enum inventory")
	}
	wrongValue := source
	wrongValue.Members = append([]SourceMember(nil), source.Members...)
	wrongValue.Members[1].Value = "alias"
	if err := validateInventory(wrongValue, "../.."); err == nil {
		t.Fatal("typed alias constant value was not resolved")
	}
}

func TestMethodInventoriesRequireExplicitExcludedMetadata(t *testing.T) {
	for _, key := range []string{"command", "storage", "crud_source"} {
		r := validRegistryFixture(t)
		c := r.Components[key]
		if len(c.Operations.Source.Excluded) == 0 {
			t.Fatalf("%s metadata methods are not explicitly excluded", key)
		}
		c.Operations.Source.Excluded[0].Reason = ""
		r.Components[key] = c
		if err := validate(r); err == nil {
			t.Fatal("exclusion without reason was accepted")
		}
	}
	for _, mutate := range []func(*Inventory){
		func(s *Inventory) { s.Interfaces = nil },
		func(s *Inventory) { s.Interfaces[0].File = "" },
		func(s *Inventory) { s.Interfaces[0].Type = "" },
		func(s *Inventory) { s.Interfaces = append(s.Interfaces, s.Interfaces[0]) },
	} {
		r := validRegistryFixture(t)
		c := r.Components["command"]
		mutate(&c.Operations.Source)
		r.Components["command"] = c
		if err := validate(r); err == nil {
			t.Fatal("incomplete method provenance was accepted")
		}
	}
}

func TestIndependentHistoryRejectsJointRegistryLedgerMutation(t *testing.T) {
	for name, mutate := range map[string]func(*Registry){
		"rename": func(r *Registry) {
			s := r.Signals["command_span"]
			delete(r.Signals, "command_span")
			delete(r.Migration.SignalIDs, "command_span")
			r.Signals["renamed_span"] = s
			r.Migration.SignalIDs["renamed_span"] = s.SignalID
		},
		"delete": func(r *Registry) { delete(r.Signals, "command_span"); delete(r.Migration.SignalIDs, "command_span") },
		"reuse": func(r *Registry) {
			id := r.Signals["command_span"].SignalID
			delete(r.Signals, "command_span")
			delete(r.Migration.SignalIDs, "command_span")
			s := r.Signals["storage_span"]
			s.SignalID = id
			r.Signals["storage_span"] = s
			r.Migration.SignalIDs["storage_span"] = id
		},
		"whole_component": func(r *Registry) {
			delete(r.Components, "command")
			for key, s := range r.Signals {
				if s.Component == "command" {
					delete(r.Signals, key)
					delete(r.Migration.SignalIDs, key)
				}
			}
		},
		"unacknowledged_retirement": func(r *Registry) { delete(r.Signals, "command_span") },
	} {
		t.Run(name, func(t *testing.T) {
			r := validRegistryFixture(t)
			mutate(&r)
			if err := validateSignalHistory(r, otelreg.SignalHistory()); err == nil {
				t.Fatal("independent history allowed joint registry/ledger mutation")
			}
		})
	}
	r := validRegistryFixture(t)
	delete(r.Signals, "command_span")
	r.RetiredSignals = []string{"command_span"}
	if err := validateSignalHistory(r, otelreg.SignalHistory()); err != nil {
		t.Fatalf("explicit retirement control: %v", err)
	}
	r.RetiredSignals = append(r.RetiredSignals, "command_span")
	if err := validateSignalHistory(r, otelreg.SignalHistory()); err == nil {
		t.Fatal("duplicate retirement allowed")
	}
}

func signalHistoryWith(t *testing.T, key string, id int) []byte {
	t.Helper()
	var history SignalHistory
	if err := json.Unmarshal(otelreg.SignalHistory(), &history); err != nil {
		t.Fatal(err)
	}
	history.Assigned[key] = id
	data, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func availabilityHistoryWith(t *testing.T, key, availability string) []byte {
	t.Helper()
	var history SignalAvailabilityHistory
	if err := json.Unmarshal(otelreg.AvailabilityHistory(), &history); err != nil {
		t.Fatal(err)
	}
	history.Signals[key] = []string{availability}
	data, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSignalHistoryIsStrictAndComplete(t *testing.T) {
	r := validRegistryFixture(t)
	var history SignalHistory
	if err := json.Unmarshal(otelreg.SignalHistory(), &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Assigned) != 59 {
		t.Fatalf("v2 history has %d signals, want 59", len(history.Assigned))
	}
	for key, s := range r.Signals {
		if history.Assigned[key] != s.SignalID {
			t.Fatalf("initial signal %s is not frozen", key)
		}
	}
	for _, raw := range []string{
		`{"format":"vv-otel-signal-history/v1","assigned":{"one":1,"one":2}}`,
		`{"format":"vv-otel-signal-history/v1","assigned":{"one":1,"two":1}}`,
		`{"format":"vv-otel-signal-history/v1","assigned":{"one":65536}}`,
		`{"format":"vv-otel-signal-history/v1","assigned":{"one":1},"unknown":true}`,
		string(otelreg.SignalHistory()) + "{}",
	} {
		if err := validateSignalHistory(r, []byte(raw)); err == nil {
			t.Fatal("invalid history was accepted")
		}
	}
	r.Migration.SignalIDs["quasar"] = 65
	if err := validateSignalHistory(r, otelreg.SignalHistory()); err == nil {
		t.Fatal("migration entry without an independent history append was accepted")
	}
	appended := signalHistoryWith(t, "quasar", 65)
	if err := validateSignalHistory(r, appended); err == nil {
		t.Fatal("historical signal outside the active and retired partition was accepted")
	}
	r.RetiredSignals = append(r.RetiredSignals, "quasar")
	if err := validateSignalHistory(r, appended); err != nil {
		t.Fatalf("explicitly retired historical append: %v", err)
	}
	r.RetiredSignals = r.RetiredSignals[:len(r.RetiredSignals)-1]
	delete(r.Migration.SignalIDs, "quasar")
	if err := validateSignalHistory(r, signalHistoryWith(t, "quasar", 65)); err == nil {
		t.Fatal("history append without a migration entry was accepted")
	}
}

func TestSignalAvailabilityHistoryFreezesTheExactRoster(t *testing.T) {
	r := validRegistryFixture(t)
	var history SignalAvailabilityHistory
	if err := json.Unmarshal(otelreg.AvailabilityHistory(), &history); err != nil {
		t.Fatal(err)
	}
	originallyImplemented := map[string]bool{
		"command_duration":    true,
		"command_span":        true,
		"storage_span":        true,
		"cache_operations":    true,
		"cache_event":         true,
		"cache_backend_event": true,
	}
	if len(history.Signals) != 59 || len(r.Signals) != 59 {
		t.Fatalf("availability roster size=%d/%d, want 59/59", len(history.Signals), len(r.Signals))
	}
	planned := 0
	for key, signal := range r.Signals {
		states := history.Signals[key]
		wantStates := []string{"planned"}
		if originallyImplemented[key] {
			wantStates = []string{"implemented"}
		} else if signal.Availability == "implemented" {
			wantStates = []string{"planned", "implemented"}
		}
		if strings.Join(states, "\x00") != strings.Join(wantStates, "\x00") {
			t.Fatalf("%s availability history=%v, registry=%s", key, states, signal.Availability)
		}
		if signal.Availability == "planned" {
			planned++
		}
	}
	if planned != 0 {
		t.Fatalf("planned roster=%d, want 0", planned)
	}
}

func TestSignalAvailabilityFlipsFailAgainstCurrentHistory(t *testing.T) {
	for key, signal := range validRegistryFixture(t).Signals {
		t.Run(key, func(t *testing.T) {
			r := validRegistryFixture(t)
			changed := r.Signals[key]
			if signal.Availability == "implemented" {
				changed.Availability = "planned"
			} else {
				changed.Availability = "implemented"
			}
			r.Signals[key] = changed
			if err := validateAvailabilityHistory(r, otelreg.AvailabilityHistory()); err == nil {
				t.Fatal("availability flip bypassed the current authority")
			}
		})
	}
}

func TestSignalAvailabilityHistoryAllowsOnlyForwardPromotion(t *testing.T) {
	r := validRegistryFixture(t)
	planned := r.Signals["storage_duration"]
	planned.Availability = "implemented"
	r.Signals["storage_duration"] = planned
	var history SignalAvailabilityHistory
	if err := json.Unmarshal(otelreg.AvailabilityHistory(), &history); err != nil {
		t.Fatal(err)
	}
	history.Signals["storage_duration"] = []string{"planned", "implemented"}
	data, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAvailabilityHistory(r, data); err != nil {
		t.Fatalf("append-only promotion rejected: %v", err)
	}
	implemented := r.Signals["command_span"]
	implemented.Availability = "planned"
	r.Signals["command_span"] = implemented
	history.Signals["command_span"] = []string{"implemented", "planned"}
	data, err = json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAvailabilityHistory(r, data); err == nil {
		t.Fatal("implemented signal regressed to planned through a history rewrite")
	}
}

func TestSignalAvailabilityHistoryIsStrictAndComplete(t *testing.T) {
	r := validRegistryFixture(t)
	for _, raw := range []string{
		`{"format":"vv-otel-availability-history/v1","signals":{"one":["planned"],"one":["implemented"]}}`,
		`{"format":"vv-otel-availability-history/v1","signals":{"one":[]}}`,
		`{"format":"vv-otel-availability-history/v1","signals":{"one":["implemented","planned"]}}`,
		`{"format":"vv-otel-availability-history/v1","signals":{"one":["planned","implemented","planned"]}}`,
		`{"format":"vv-otel-availability-history/v1","signals":{"one":["unknown"]}}`,
		`{"format":"vv-otel-availability-history/v1","signals":{"one":["planned"]},"unknown":true}`,
		string(otelreg.AvailabilityHistory()) + `{}`,
	} {
		if err := validateAvailabilityHistory(r, []byte(raw)); err == nil {
			t.Fatal("invalid availability history was accepted")
		}
	}
	var history SignalAvailabilityHistory
	if err := json.Unmarshal(otelreg.AvailabilityHistory(), &history); err != nil {
		t.Fatal(err)
	}
	delete(history.Signals, "command_span")
	data, err := json.Marshal(history)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAvailabilityHistory(r, data); err == nil {
		t.Fatal("incomplete availability history was accepted")
	}
}

func TestMigrationRecordIsStrictCompleteAndProjected(t *testing.T) {
	for name, mutate := range map[string]func(*MigrationMetadata){
		"missing_changes":  func(migration *MigrationMetadata) { migration.WireChanges = nil },
		"empty_change":     func(migration *MigrationMetadata) { migration.WireChanges[0] = "" },
		"padded_change":    func(migration *MigrationMetadata) { migration.WireChanges[0] = " changed " },
		"duplicate_change": func(migration *MigrationMetadata) { migration.WireChanges[1] = migration.WireChanges[0] },
		"absolute_note":    func(migration *MigrationMetadata) { migration.Note = "/tmp/note.md" },
		"non_markdown_note": func(migration *MigrationMetadata) {
			migration.Note = "docs/release-notes/note.txt"
		},
	} {
		t.Run(name, func(t *testing.T) {
			reg := validRegistryFixture(t)
			mutate(&reg.Migration)
			if err := validate(reg); err == nil {
				t.Fatal("incomplete migration record was accepted")
			}
		})
	}
	reg := validRegistryFixture(t)
	reg.Migration.Note = "docs/release-notes/missing-otel-migration.md"
	if err := validate(reg); err != nil {
		t.Fatalf("lexically valid note control: %v", err)
	}
	if err := validateInventories(reg, "../.."); err == nil {
		t.Fatal("missing migration note was accepted by the source gate")
	}
	reg = validRegistryFixture(t)
	manifest, err := buildManifest(reg)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Migration.From != reg.Migration.From || manifest.Migration.To != reg.Migration.To || manifest.Migration.Note != reg.Migration.Note || len(manifest.Migration.WireChanges) != len(reg.Migration.WireChanges) || len(manifest.Migration.SignalIDs) != len(reg.Migration.SignalIDs) {
		t.Fatal("wire manifest lost the migration record")
	}
	manifest.Migration.WireChanges[0] = "changed"
	manifest.Migration.SignalIDs["command_span"] = 0
	if reg.Migration.WireChanges[0] == "changed" || reg.Migration.SignalIDs["command_span"] == 0 {
		t.Fatal("wire migration projection aliases registry authority")
	}
	code, err := render(reg)
	if err != nil {
		t.Fatal(err)
	}
	generatedValues := []string{"MigrationFrom", "MigrationTo", "MigrationNote", "MigrationWireChanges", reg.Migration.Note}
	generatedValues = append(generatedValues, reg.Migration.WireChanges...)
	for _, value := range generatedValues {
		if !strings.Contains(string(code), value) {
			t.Fatalf("generated Go lost migration value %q", value)
		}
	}
	wire, err := renderManifest(reg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"migration": {`) || !strings.Contains(string(wire), `"wire_changes": [`) {
		t.Fatal("serialized wire manifest lost the migration record")
	}
}

func TestOwnedDomainsRejectForeignConstants(t *testing.T) {
	for name, change := range map[string]func(*Registry){
		"unknown_constant": func(r *Registry) {
			s := r.Signals["command_duration"]
			s.Variants[0].Attributes["operation_outcome"] = Binding{Const: "secret", Domain: "command.outcomes"}
			r.Signals["command_duration"] = s
		},
		"foreign_valid_constant": func(r *Registry) {
			s := r.Signals["command_duration"]
			s.Variants[0].Attributes["operation_outcome"] = Binding{Const: "hit", Domain: "command.outcomes"}
			r.Signals["command_duration"] = s
		},
		"foreign_valid_domain": func(r *Registry) {
			s := r.Signals["command_duration"]
			s.Variants[0].Attributes["operation_outcome"] = Binding{Const: "get", Domain: "command.operations"}
			r.Signals["command_duration"] = s
		},
		"constant_without_domain": func(r *Registry) {
			s := r.Signals["command_duration"]
			s.Variants[0].Attributes["operation_outcome"] = Binding{Const: "ok"}
			r.Signals["command_duration"] = s
		},
		"missing_attribute_domains": func(r *Registry) { a := r.Attributes["component"]; a.Domains = nil; r.Attributes["component"] = a },
	} {
		t.Run(name, func(t *testing.T) {
			r := validRegistryFixture(t)
			change(&r)
			if err := validate(r); err == nil {
				t.Fatal("constant bypassed its attribute-owned vocabulary")
			}
		})
	}
}

func TestLogCorrelationRequiresExactKeysFormatAndWidths(t *testing.T) {
	for _, name := range []string{"trace_id", "span_id", "trace_flags"} {
		for _, change := range []func(*LogKey){func(k *LogKey) { k.Name = "other" }, func(k *LogKey) { k.Format = "upper_hex" }, func(k *LogKey) { k.Length++ }} {
			r := validRegistryFixture(t)
			key := r.LogCorrelation.Keys[name]
			change(&key)
			r.LogCorrelation.Keys[name] = key
			if err := validate(r); err == nil {
				t.Fatal("changed correlation contract was accepted")
			}
		}
		r := validRegistryFixture(t)
		delete(r.LogCorrelation.Keys, name)
		if err := validate(r); err == nil {
			t.Fatal("missing correlation key was accepted")
		}
	}
	r := validRegistryFixture(t)
	r.Migration.From = "vv-otel/anything"
	if err := validate(r); err == nil {
		t.Fatal("migration was not pinned to v1 to v2")
	}
}

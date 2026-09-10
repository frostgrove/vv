package vvotel_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/otel"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/storage"
)

func TestSchema_AllSpanNameDomainsAreResolvedAndClosed(t *testing.T) {
	schema := loadWireSchema(t)
	single := map[string]string{
		"authentication_span":   vvotel.SpanAuthentication,
		"health_span":           vvotel.SpanHealth,
		"runtime_periodic_span": vvotel.SpanRuntimePeriodic,
		"storage_stream_span":   vvotel.SpanStorageStream,
		"jobs_handler_span":     vvotel.SpanJobsHandler,
	}
	multiple := map[string]func(string) (string, bool){
		"command_span":             vvotel.SpanCommandName,
		"storage_span":             vvotel.SpanStorageName,
		"crud_source_span":         vvotel.SpanCrudSourceName,
		"remote_span":              vvotel.SpanRemoteName,
		"jobs_enqueue_span":        vvotel.SpanJobsEnqueueName,
		"jobs_enqueue_staged_span": vvotel.SpanJobsEnqueueStagedName,
	}
	count := 0
	for key, wire := range schema.Signals {
		if wire.NameDomain == "" {
			continue
		}
		count++
		if len(wire.Names) == 1 {
			if single[key] != wire.Names[0] {
				t.Fatalf("%s constant=%q, manifest=%q", key, single[key], wire.Names[0])
			}
			continue
		}
		lookup := multiple[key]
		if lookup == nil {
			t.Fatalf("%s has no tested generated closed lookup", key)
		}
		for _, name := range wire.Names {
			operation := strings.TrimPrefix(name, wire.Name+" ")
			if got, ok := lookup(operation); !ok || got != name {
				t.Fatalf("%s(%s)=%s/%t, want %s", key, operation, got, ok, name)
			}
		}
		if got, ok := lookup("unregistered_sensitive_value"); ok || got != "" {
			t.Fatalf("%s lookup did not fail closed", key)
		}
	}
	if count != len(single)+len(multiple) {
		t.Fatal("span domain inventory drifted")
	}
}

func wireAttributeValue(kind, value string) attribute.Value {
	switch kind {
	case "bool":
		return attribute.BoolValue(value == "true")
	case "int64":
		n, _ := strconv.ParseInt(value, 10, 64)
		return attribute.Int64Value(n)
	default:
		return attribute.StringValue(value)
	}
}

func TestSchema_GeneratedMatchersFollowEveryMetricVariant(t *testing.T) {
	schema := loadWireSchema(t)
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.Kind != "metric" {
			continue
		}
		wire := schema.Signals[descriptor.Key]
		if len(descriptor.Variants) != len(wire.ResolvedVariants) {
			t.Fatalf("%s lost variant descriptors", descriptor.Key)
		}
		positive := 0
		for _, variant := range wire.ResolvedVariants {
			facts := []vvotel.SignalSourceValue{}
			for _, predicate := range variant.When {
				value := predicate.Value
				if predicate.Operator == "gt" {
					value++
				}
				facts = append(facts, vvotel.SignalSourceValue{Fact: vvotel.SignalSourceFact(schema.SourceFacts[predicate.Fact].ID), Value: value})
			}
			rows := []map[attribute.Key]attribute.Value{{}}
			for key, spec := range variant.Attributes {
				next := []map[attribute.Key]attribute.Value{}
				for _, row := range rows {
					if spec.Optional {
						next = append(next, row)
					}
					for _, value := range spec.Values {
						copied := map[attribute.Key]attribute.Value{}
						for k, v := range row {
							copied[k] = v
						}
						copied[attribute.Key(key)] = wireAttributeValue(spec.Type, value)
						next = append(next, copied)
					}
				}
				rows = next
			}
			for _, row := range rows {
				attributes := []attribute.KeyValue{}
				for key, value := range row {
					attributes = append(attributes, attribute.KeyValue{Key: key, Value: value})
				}
				if !descriptor.Accepts("", attributes, facts...) || !wireAccepts(wire, row) {
					t.Fatalf("%s rejected a resolved positive tuple", descriptor.Key)
				}
				positive++
				if descriptor.Accepts("", append(attributes, attribute.String("sensitive.unregistered", "secret")), facts...) {
					t.Fatalf("%s admitted an extra key", descriptor.Key)
				}
				if len(attributes) > 0 {
					duplicate := append(append([]attribute.KeyValue{}, attributes...), attributes[0])
					if descriptor.Accepts("", duplicate, facts...) {
						t.Fatalf("%s admitted duplicate keys", descriptor.Key)
					}
					for index, a := range attributes {
						for _, value := range []attribute.Value{attribute.StringValue("unregistered_value"), attribute.StringSliceValue([]string{"value"})} {
							mutated := append([]attribute.KeyValue{}, attributes...)
							mutated[index] = attribute.KeyValue{Key: a.Key, Value: value}
							if descriptor.Accepts("", mutated, facts...) {
								t.Fatalf("%s admitted foreign value/type for %s", descriptor.Key, a.Key)
							}
						}
					}
				}
			}
		}
		if positive == 0 {
			t.Fatalf("%s matcher test was vacuous", descriptor.Key)
		}
	}
}

func TestSchema_GeneratedDescriptorDataCannotMutateAuthority(t *testing.T) {
	changes := vvotel.MigrationWireChanges()
	originalChange := changes[0]
	changes[0] = "mutated"
	if vvotel.MigrationWireChanges()[0] != originalChange {
		t.Fatal("migration wire changes alias generated authority")
	}
	descriptors := vvotel.SignalDescriptors()
	first := descriptors[0]
	originalName := first.Names[0]
	originalInput := first.Inputs[0]
	originalValue := first.Variants[0].Attributes[0].Values[0]
	first.Names[0] = "mutated"
	first.Inputs[0] = "mutated.input"
	first.Variants[0].Attributes[0].Values[0] = "mutated"
	next := vvotel.SignalDescriptors()[0]
	if next.Names[0] != originalName || next.Inputs[0] != originalInput || next.Variants[0].Attributes[0].Values[0] != originalValue {
		t.Fatal("returned descriptors alias generated authority")
	}
	for index, descriptor := range descriptors {
		if len(descriptor.Boundaries) > 0 {
			original := descriptor.Boundaries[0]
			descriptor.Boundaries[0] = -99
			if vvotel.SignalDescriptors()[index].Boundaries[0] != original {
				t.Fatal("histogram boundaries alias generated authority")
			}
		}
		for variantIndex, variant := range descriptor.Variants {
			if len(variant.When) > 0 {
				original := variant.When[0].Value
				variant.When[0].Value = 9999
				if vvotel.SignalDescriptors()[index].Variants[variantIndex].When[0].Value != original {
					t.Fatal("predicate slice aliases generated authority")
				}
			}
		}
		for attribute, sources := range descriptor.DeclaredSources {
			if len(sources) == 0 {
				continue
			}
			original := sources[0]
			sources[0] = "mutated"
			if vvotel.SignalDescriptors()[index].DeclaredSources[attribute][0] != original {
				t.Fatal("declared source slices alias generated authority")
			}
		}
	}
}

func TestSchema_TypedMeasurementAdmissionPreservesInt64(t *testing.T) {
	integer := schemaDescriptor(t, "cache_encoded_bytes")
	if !integer.AcceptsInt64(math.MaxInt64) {
		t.Fatal("typed int64 admission rounded MaxInt64")
	}
	if integer.AcceptsValue(float64(math.MaxInt64)) || integer.AcceptsValue(0.5) || integer.AcceptsValue(math.Inf(1)) || integer.AcceptsFloat64(1) {
		t.Fatal("integer descriptor accepted a lossy or wrong numeric API value")
	}

	floating := schemaDescriptor(t, "command_duration")
	if !floating.AcceptsFloat64(0.5) || !floating.AcceptsValue(0.5) || floating.AcceptsFloat64(math.NaN()) || floating.AcceptsFloat64(math.Inf(1)) || floating.AcceptsInt64(1) {
		t.Fatal("floating descriptor accepted a non-finite or wrong numeric API value")
	}
}

func TestSchema_SignalSelectionIsAnIndependentClosedList(t *testing.T) {
	first, second := vvotel.AllSignals(), vvotel.AllSignals()
	if len(first) != 59 || !slices.Equal(first, second) {
		t.Fatal("signal roster drifted")
	}
	first[0] = 0
	if !second[0].Valid() || !vvotel.AllSignals()[0].Valid() {
		t.Fatal("AllSignals exposes mutable authority")
	}
	for _, id := range []vvotel.Signal{0, 60, 65, 65535} {
		if id.Valid() {
			t.Fatalf("unknown signal %d accepted", id)
		}
	}
	selected := vvotel.Signals{vvotel.SignalCommandSpan, vvotel.SignalJobsWorkerReleased}
	if !selected.Has(vvotel.SignalCommandSpan) || selected.Has(vvotel.SignalStorageSpan) {
		t.Fatal("closed list membership differs")
	}
}

func schemaDescriptor(t *testing.T, key string) vvotel.SignalDescriptor {
	t.Helper()
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.Key == key {
			return descriptor
		}
	}
	t.Fatalf("unknown descriptor %s", key)
	return vvotel.SignalDescriptor{}
}

type schemaCacheObserver struct{ events chan cache.Event }

func (o schemaCacheObserver) Observe(_ context.Context, event cache.Event) { o.events <- event }

type schemaBackendObserver struct{ events chan cachememory.Event }

func (o schemaBackendObserver) Observe(_ context.Context, event cachememory.Event) { o.events <- event }

func schemaCacheEvent(t *testing.T, events <-chan cache.Event, operation cache.Operation, outcome cache.Outcome) cache.Event {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Operation == operation && event.Outcome == outcome {
				return event
			}
		case <-timer.C:
			t.Fatal("cache source did not emit expected event")
			return cache.Event{}
		}
	}
}
func schemaFacadeAttributes(event cache.Event) []attribute.KeyValue {
	attrs := []attribute.KeyValue{vvotel.AttrComponent.String(vvotel.ComponentCache), vvotel.AttrCacheLayer.String(vvotel.CacheLayerFacade), vvotel.AttrOperationName.String(string(event.Operation)), vvotel.AttrOperationOutcome.String(string(event.Outcome)), vvotel.AttrMemoized.Bool(event.Memoized)}
	if event.Reason != "" {
		attrs = append(attrs, vvotel.AttrReason.String(string(event.Reason)))
	}
	return attrs
}

func checkCacheEmptyValue[V any](t *testing.T, codec cache.Codec[V], empty V) {
	t.Helper()
	ctx := context.Background()
	events := make(chan cache.Event, 32)
	backend, err := cachememory.New(cachememory.Limits{MaxEntries: 16, MaxBytes: 1 << 20, MaxItemBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	policy, err := cache.Hot.With(cache.MaxValueBytes(64)).Build()
	if err != nil {
		t.Fatal(err)
	}
	keys := cache.MustKeyFunc[string](1, func(key string, _ cache.KeyLimit) ([]byte, error) { return []byte(key), nil })
	value, err := cache.New(cache.Runtime{Observer: schemaCacheObserver{events}}, backend, cache.Global[string](cache.MustNamespace("schema", "test", "empty", 1)), keys, codec, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = value.Lookup(ctx, "absent"); err != nil {
		t.Fatal(err)
	}
	absent := schemaCacheEvent(t, events, cache.LookupOperation, cache.MissOutcome)
	payload := schemaDescriptor(t, "cache_payload_bytes")
	encoded := schemaDescriptor(t, "cache_encoded_bytes")
	fact := func(event cache.Event) vvotel.SignalSourceValue {
		return vvotel.SignalSourceValue{Fact: vvotel.SourceFactCacheEncodedBytes, Value: event.EncodedBytes}
	}
	if payload.Accepts("", schemaFacadeAttributes(absent), fact(absent)) || encoded.Accepts("", schemaFacadeAttributes(absent), fact(absent)) {
		t.Fatal("absent lookup sizes admitted")
	}
	loader := func(context.Context, string) (cache.LoadResult[V], error) { return cache.Present(empty), nil }
	if _, err = value.Resolve(ctx, "loaded", loader); err != nil {
		t.Fatal(err)
	}
	loaded := schemaCacheEvent(t, events, cache.LoadOperation, cache.LoadedOutcome)
	if loaded.EncodedBytes <= 0 || loaded.PayloadBytes != 0 {
		t.Fatalf("empty cached source shape=%+v", loaded)
	}
	if !payload.Accepts("", schemaFacadeAttributes(loaded), fact(loaded)) || !payload.AcceptsValue(float64(loaded.PayloadBytes)) {
		t.Fatal("present empty cached payload not admitted as zero")
	}
	if payload.Accepts("", schemaFacadeAttributes(loaded)) {
		t.Fatal("source presence fact was not required")
	}
	if payload.Accepts("", schemaFacadeAttributes(loaded), fact(loaded), fact(loaded)) {
		t.Fatal("duplicate source fact accepted")
	}
	if payload.Accepts("", schemaFacadeAttributes(loaded), vvotel.SignalSourceValue{Fact: 65535, Value: 1}) {
		t.Fatal("unknown source fact accepted")
	}
	if _, err = value.Lookup(ctx, "loaded"); err != nil {
		t.Fatal(err)
	}
	hit := schemaCacheEvent(t, events, cache.LookupOperation, cache.HitOutcome)
	if hit.PayloadBytes != 0 || !payload.Accepts("", schemaFacadeAttributes(hit), fact(hit)) || !payload.AcceptsValue(0) {
		t.Fatal("empty cached hit did not retain zero measurement")
	}
	disabledPolicy, err := cache.Disabled.Build()
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := cache.New(cache.Runtime{Observer: schemaCacheObserver{events}}, backend, cache.Global[string](cache.MustNamespace("schema", "test", "disabled", 1)), keys, codec, disabledPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = disabled.Resolve(ctx, "fast", loader); err != nil {
		t.Fatal(err)
	}
	fast := schemaCacheEvent(t, events, cache.LoadOperation, cache.LoadedOutcome)
	if fast.EncodedBytes != 0 || fast.PayloadBytes != 0 || payload.Accepts("", schemaFacadeAttributes(fast), fact(fast)) || encoded.Accepts("", schemaFacadeAttributes(fast), fact(fast)) {
		t.Fatal("uncached load fast path fabricated present sizes")
	}
}

func TestSchema_CacheSourcePresencePreservesEmptyValues(t *testing.T) {
	t.Run("string", func(t *testing.T) { checkCacheEmptyValue(t, cache.String(1), "") })
	t.Run("bytes", func(t *testing.T) { checkCacheEmptyValue(t, cache.Bytes(1), []byte{}) })
	t.Run("backend", func(t *testing.T) {
		events := make(chan cachememory.Event, 16)
		backend, err := cachememory.New(cachememory.Limits{MaxEntries: 16, MaxBytes: 1 << 20, MaxItemBytes: 4096}, cachememory.WithObserver(schemaBackendObserver{events}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = backend.Close() })
		address := cache.Address{NamespaceDigest: [32]byte{1}, PartitionDigest: [32]byte{2}, KeyDigest: [32]byte{3}}
		if err = backend.Put(context.Background(), address, []byte{}, cache.Expiry{Mode: cache.CapacityOnlyExpiry}); err != nil {
			t.Fatal(err)
		}
		event := <-events
		attrs := []attribute.KeyValue{vvotel.AttrComponent.String(vvotel.ComponentCacheBackend), vvotel.AttrCacheLayer.String(vvotel.CacheBackendLayerMemoryBackend), vvotel.AttrOperationName.String(string(event.Operation)), vvotel.AttrOperationOutcome.String(string(event.Outcome))}
		descriptor := schemaDescriptor(t, "cache_value_bytes")
		if event.Operation != cachememory.PutOperation || event.Outcome != cachememory.StoredOutcome || event.ValueBytes != 0 || !descriptor.Accepts("", attrs) || !descriptor.AcceptsValue(0) {
			t.Fatalf("empty backend value shape not admitted: %+v", event)
		}
		attrs[3] = vvotel.AttrOperationOutcome.String("miss")
		attrs[2] = vvotel.AttrOperationName.String("get")
		if descriptor.Accepts("", attrs) {
			t.Fatal("backend miss fabricated a value size")
		}
	})
}

func TestSchema_NumericAdmissionAndElapsedPresenceAreIndependent(t *testing.T) {
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.AcceptsValue(math.NaN()) || descriptor.AcceptsValue(math.Inf(1)) || descriptor.AcceptsValue(-1) {
			t.Fatalf("%s admitted invalid numeric value", descriptor.Key)
		}
		if descriptor.Kind != "metric" && descriptor.AcceptsValue(1) {
			t.Fatal("non-metric accepted measurement")
		}
	}
	cleanup := schemaDescriptor(t, "storage_cleanup_removed")
	if !cleanup.AcceptsValue(0) || !cleanup.AcceptsValue(storage.MaxCleanupLimit) || cleanup.AcceptsValue(storage.MaxCleanupLimit+1) || cleanup.AcceptsValue(0.5) {
		t.Fatal("cleanup result did not honor source numeric bounds")
	}
	worker := schemaDescriptor(t, "jobs_worker_duration")
	attrs := []attribute.KeyValue{vvotel.AttrComponent.String("jobs_worker"), vvotel.AttrOperationName.String("claim"), vvotel.AttrOperationOutcome.String("complete")}
	if worker.Accepts("", attrs) || worker.Accepts("", attrs, vvotel.SignalSourceValue{Fact: vvotel.SourceFactJobsWorkerElapsed, Value: 0}) || !worker.Accepts("", attrs, vvotel.SignalSourceValue{Fact: vvotel.SourceFactJobsWorkerElapsed, Value: 1}) {
		t.Fatal("worker clock-failure zero was not distinguished from present elapsed")
	}
}

func TestSchema_IntegerBoundsMatchAuthoritativeSourceLimits(t *testing.T) {
	tests := []struct {
		key     string
		minimum int64
		maximum int64
	}{
		{"jobs_handler_attempt", 1, jobs.MaxAttemptOrdinal},
		{"jobs_worker_items", 0, jobs.MaxReclaimBatch},
		{"jobs_worker_bytes", 0, jobs.MaxClaimBytes},
		{"jobs_worker_delivery_results", 1, jobs.MaxClaimItems},
		{"jobs_scheduler_results", 0, jobs.MaxDefinitions},
		{"storage_cleanup_removed", 0, storage.MaxCleanupLimit},
		{"jobs_worker_released", 0, jobs.MaxReclaimBatch},
	}
	for _, test := range tests {
		descriptor := schemaDescriptor(t, test.key)
		if descriptor.NumberType != "int64" || !descriptor.HasMinimum || descriptor.MinimumInt64 != test.minimum || !descriptor.HasMaximum || descriptor.MaximumInt64 != test.maximum {
			t.Fatalf("%s exact bounds differ from source: %+v", test.key, descriptor)
		}
		if descriptor.Minimum != float64(test.minimum) || descriptor.Maximum != float64(test.maximum) {
			t.Fatalf("%s compatibility bounds differ from exact bounds", test.key)
		}
		if !descriptor.AcceptsInt64(test.minimum) || descriptor.AcceptsInt64(test.minimum-1) || !descriptor.AcceptsValue(float64(test.minimum)) || descriptor.AcceptsValue(float64(test.minimum-1)) {
			t.Fatalf("%s admission differs at source minimum %d", test.key, test.minimum)
		}
		if !descriptor.AcceptsInt64(test.maximum) || descriptor.AcceptsInt64(test.maximum+1) || !descriptor.AcceptsValue(float64(test.maximum)) || descriptor.AcceptsValue(float64(test.maximum+1)) {
			t.Fatalf("%s admission differs at source maximum %d", test.key, test.maximum)
		}
	}
}

func TestSchema_GoroutineExitTerminalsHaveNoMetricShape(t *testing.T) {
	count := 0
	for _, descriptor := range vvotel.SignalDescriptors() {
		for _, variant := range descriptor.Variants {
			terminal := false
			attrs := []attribute.KeyValue{}
			for _, field := range variant.Attributes {
				if field.Key == vvotel.AttrOperationOutcome && slices.Contains(field.Values, vvotel.OutcomeGoroutineExit) {
					terminal = true
				}
				if !field.Optional && len(field.Values) > 0 {
					attrs = append(attrs, attribute.KeyValue{Key: field.Key, Value: wireAttributeValue(field.Type, field.Values[0])})
				}
			}
			if !terminal {
				continue
			}
			count++
			if descriptor.Kind != "span" || !descriptor.Accepts("error", attrs) || descriptor.Accepts("unset", attrs) || descriptor.Accepts("error", append(attrs, vvotel.AttrErrorType.String("panic"))) {
				t.Fatalf("%s has an invalid Goexit terminal", descriptor.Key)
			}
		}
	}
	if count < 11 {
		t.Fatal("not all span wrappers admit a terminal Goexit")
	}
}

func TestSchema_GeneratedMatcherAdmitsOnlyApprovedTraceNames(t *testing.T) {
	for _, descriptor := range vvotel.SignalDescriptors() {
		if descriptor.Key != "command_span" {
			continue
		}
		base := []attribute.KeyValue{vvotel.AttrComponent.String("command"), vvotel.AttrOperationName.String("get"), vvotel.AttrOperationOutcome.String("ok")}
		if !descriptor.Accepts("unset", base) {
			t.Fatal("success control rejected")
		}
		if descriptor.Accepts("error", base) || descriptor.Accepts("", base) {
			t.Fatal("success attributes matched an incorrect span status")
		}
		for _, name := range []string{"safe_resource", "Продукты.1", "a-b"} {
			if !descriptor.Accepts("unset", append(base, vvotel.AttrResourceName.String(name))) {
				t.Fatalf("approved name %q rejected", name)
			}
		}
		for _, name := range []string{"", "user secret", "a/b", strings.Repeat("a", 65), string([]byte{255})} {
			if descriptor.Accepts("unset", append(base, vvotel.AttrResourceName.String(name))) {
				t.Fatalf("invalid name %q accepted", name)
			}
		}
		if descriptor.Accepts("unset", append(base, vvotel.AttrErrorCode.String(vvotel.ErrorCodeConflict))) {
			t.Fatal("success admitted an error code")
		}
	}
}
func TestSchema_CacheMappingsAreTotal(t *testing.T) {
	cacheOperations := []string{
		"lookup", "lookup_many", "load", "load_many", "put", "forget",
	}
	for _, value := range cacheOperations {
		if mapped, ok := vvotel.CacheOperationName(value); !ok || mapped != value {
			t.Errorf("cache operation %q mapped to %q, %v", value, mapped, ok)
		}
	}
	cacheOutcomes := []string{
		"hit", "miss", "negative", "stale", "loaded", "stored", "deleted",
		"superseded", "complete", "error",
	}
	for _, value := range cacheOutcomes {
		if mapped, ok := vvotel.CacheOutcomeName(value); !ok || mapped != value {
			t.Errorf("cache outcome %q mapped to %q, %v", value, mapped, ok)
		}
	}

	backendOperations := []string{
		"get", "get_many", "put", "delete", "evict", "reset", "close",
	}
	for _, value := range backendOperations {
		if mapped, ok := vvotel.CacheBackendOperationName(value); !ok || mapped != value {
			t.Errorf("backend operation %q mapped to %q, %v", value, mapped, ok)
		}
	}
	backendOutcomes := []string{
		"hit", "miss", "stored", "replaced", "deleted", "evicted", "rejected", "complete",
	}
	for _, value := range backendOutcomes {
		if mapped, ok := vvotel.CacheBackendOutcomeName(value); !ok || mapped != value {
			t.Errorf("backend outcome %q mapped to %q, %v", value, mapped, ok)
		}
	}
}

type wireSchema struct {
	SourceFacts map[string]struct {
		ID int `json:"id"`
	} `json:"source_facts"`
	ContractVersion      string                  `json:"contract_version"`
	AdditionalAttributes bool                    `json:"additional_attributes"`
	Migration            wireMigration           `json:"migration"`
	Signals              map[string]wireContract `json:"signals"`
}
type wireMigration struct {
	From        string         `json:"from"`
	To          string         `json:"to"`
	Status      string         `json:"status"`
	Since       string         `json:"since"`
	Policy      string         `json:"policy"`
	Note        string         `json:"note"`
	WireChanges []string       `json:"wire_changes"`
	SignalIDs   map[string]int `json:"signal_ids"`
}
type wireContract struct {
	Min              *float64            `json:"min"`
	Max              *float64            `json:"max"`
	Description      string              `json:"description"`
	Boundaries       []float64           `json:"boundaries"`
	Name             string              `json:"name"`
	Names            []string            `json:"names"`
	NameDomain       string              `json:"name_domain"`
	Kind             string              `json:"kind"`
	SignalID         int                 `json:"signal_id"`
	Provider         string              `json:"provider"`
	APIKind          string              `json:"api_kind"`
	CardinalityBound int                 `json:"cardinality_bound"`
	SpanKind         string              `json:"span_kind"`
	Source           string              `json:"source"`
	PrivacyClass     string              `json:"privacy_class"`
	Maturity         string              `json:"maturity"`
	Semconv          string              `json:"semconv"`
	Availability     string              `json:"availability"`
	Component        string              `json:"component"`
	Instrument       string              `json:"instrument"`
	NumberType       string              `json:"number_type"`
	Unit             string              `json:"unit"`
	SeriesBudget     int                 `json:"series_budget"`
	RecordWhen       string              `json:"record_when"`
	Inputs           []string            `json:"inputs"`
	DeclaredSources  map[string][]string `json:"declared_sources"`
	ValueSource      string              `json:"value_source"`
	ComputedSource   string              `json:"computed_source"`
	ResolvedVariants []wireVariant       `json:"resolved_variants"`
}
type wireVariant struct {
	When []struct {
		Fact     string `json:"fact"`
		Operator string `json:"operator"`
		Value    int64  `json:"value"`
	} `json:"when"`
	Attributes map[string]wireValue `json:"attributes"`
	Absent     []string             `json:"absent"`
	Status     string               `json:"status"`
}
type wireValue struct {
	Type      string   `json:"type"`
	Values    []string `json:"values"`
	Optional  bool     `json:"optional"`
	Declared  bool     `json:"declared"`
	MaxValues int      `json:"max_values"`
	MaxBytes  int      `json:"max_bytes"`
	Charset   string   `json:"charset"`
}

func loadWireSchema(t *testing.T) wireSchema {
	t.Helper()
	data, err := os.ReadFile("wire_manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema wireSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}
func wireAccepts(contract wireContract, attrs map[attribute.Key]attribute.Value) bool {
	return wireAcceptsStatus(contract, "", attrs)
}

func wireAcceptsStatus(contract wireContract, status string, attrs map[attribute.Key]attribute.Value) bool {
	for _, variant := range contract.ResolvedVariants {
		if variant.Status != status {
			continue
		}
		accepted := true
		for key, spec := range variant.Attributes {
			value, present := attrs[attribute.Key(key)]
			if !present {
				if !spec.Optional {
					accepted = false
				}
				continue
			}
			if spec.Declared {
				if spec.Type != "string" || value.Type() != attribute.STRING || spec.MaxBytes <= 0 || len(value.AsString()) == 0 || len(value.AsString()) > spec.MaxBytes || !utf8.ValidString(value.AsString()) || spec.Charset != "unicode_letter_digit_dot_underscore_hyphen" {
					accepted = false
					continue
				}
				for _, r := range value.AsString() {
					if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-' {
						accepted = false
					}
				}
				continue
			}
			var rendered string
			switch spec.Type {
			case "string":
				if value.Type() != attribute.STRING {
					accepted = false
					continue
				}
				rendered = value.AsString()
			case "bool":
				if value.Type() != attribute.BOOL {
					accepted = false
					continue
				}
				rendered = strconv.FormatBool(value.AsBool())
			case "int64":
				if value.Type() != attribute.INT64 {
					accepted = false
					continue
				}
				rendered = strconv.FormatInt(value.AsInt64(), 10)
			default:
				accepted = false
			}
			found := false
			for _, allowed := range spec.Values {
				if allowed == rendered {
					found = true
				}
			}
			if !found {
				accepted = false
			}
		}
		for key := range attrs {
			if _, exists := variant.Attributes[string(key)]; !exists {
				accepted = false
			}
		}
		for _, key := range variant.Absent {
			if _, exists := attrs[attribute.Key(key)]; exists {
				accepted = false
			}
		}
		if accepted {
			return true
		}
	}
	return false
}

func wireBoundsMatchDescriptor(wire wireContract, descriptor vvotel.SignalDescriptor) bool {
	if (wire.Min != nil) != descriptor.HasMinimum || (wire.Max != nil) != descriptor.HasMaximum {
		return false
	}
	if wire.Min != nil {
		minimum := descriptor.Minimum
		if descriptor.NumberType == "int64" {
			minimum = float64(descriptor.MinimumInt64)
		}
		if *wire.Min != minimum {
			return false
		}
	}
	if wire.Max != nil {
		maximum := descriptor.Maximum
		if descriptor.NumberType == "int64" {
			maximum = float64(descriptor.MaximumInt64)
		}
		if *wire.Max != maximum {
			return false
		}
	}
	return true
}

func declaredSourcesMatch(wire map[string][]string, generated map[string][]string) bool {
	if len(wire) != len(generated) {
		return false
	}
	for attribute, sources := range wire {
		if !slices.Equal(sources, generated[attribute]) {
			return false
		}
	}
	return true
}

func wireMigrationMatchesGenerated(migration wireMigration, descriptors []vvotel.SignalDescriptor) bool {
	if migration.From != vvotel.MigrationFrom || migration.To != vvotel.MigrationTo || migration.Status != vvotel.MigrationStatus || migration.Since != vvotel.MigrationSince || migration.Policy != vvotel.MigrationPolicy || migration.Note != vvotel.MigrationNote || !slices.Equal(migration.WireChanges, vvotel.MigrationWireChanges()) || len(migration.SignalIDs) != len(descriptors) {
		return false
	}
	for _, descriptor := range descriptors {
		if migration.SignalIDs[descriptor.Key] != int(descriptor.SignalID) {
			return false
		}
	}
	return true
}

func wireVariantsMatch(schema wireSchema, wire []wireVariant, generated []vvotel.SignalVariantDescriptor) bool {
	if len(wire) != len(generated) {
		return false
	}
	for index, expected := range wire {
		actual := generated[index]
		if expected.Status != actual.Status || len(expected.Absent) != len(actual.Absent) || len(expected.When) != len(actual.When) || len(expected.Attributes) != len(actual.Attributes) {
			return false
		}
		for absentIndex, key := range expected.Absent {
			if actual.Absent[absentIndex] != attribute.Key(key) {
				return false
			}
		}
		for predicateIndex, predicate := range expected.When {
			fact, exists := schema.SourceFacts[predicate.Fact]
			if !exists || actual.When[predicateIndex].Fact != vvotel.SignalSourceFact(fact.ID) || actual.When[predicateIndex].Operator != predicate.Operator || actual.When[predicateIndex].Value != predicate.Value {
				return false
			}
		}
		seenAttributes := make(map[string]bool, len(actual.Attributes))
		for _, field := range actual.Attributes {
			if seenAttributes[string(field.Key)] {
				return false
			}
			seenAttributes[string(field.Key)] = true
			expectedField, exists := expected.Attributes[string(field.Key)]
			if !exists || expectedField.Type != field.Type || !slices.Equal(expectedField.Values, field.Values) || expectedField.Optional != field.Optional || expectedField.Declared != field.Declared || expectedField.MaxValues != field.MaxValues || expectedField.MaxBytes != field.MaxBytes || expectedField.Charset != field.Charset {
				return false
			}
		}
	}
	return true
}

func TestSchema_WireManifestMatchesGeneratedDescriptors(t *testing.T) {
	schema := loadWireSchema(t)
	if schema.ContractVersion != vvotel.ContractVersion || schema.AdditionalAttributes {
		t.Fatal("wire manifest identity or closed-attribute policy differs")
	}
	descriptors := vvotel.SignalDescriptors()
	if len(descriptors) != len(schema.Signals) || !wireMigrationMatchesGenerated(schema.Migration, descriptors) {
		t.Fatal("generated descriptor inventory differs from wire manifest")
	}
	var all vvotel.Signals
	for _, descriptor := range descriptors {
		wire, exists := schema.Signals[descriptor.Key]
		name := wire.Name
		if len(wire.Names) == 1 {
			name = wire.Names[0]
		}
		if !exists || name != descriptor.Name || !slices.Equal(wire.Names, descriptor.Names) || wire.Kind != descriptor.Kind || wire.CardinalityBound != descriptor.CardinalityBound || wire.SignalID != int(descriptor.SignalID) || wire.Provider != descriptor.Provider || wire.APIKind != descriptor.APIKind || wire.Description != descriptor.Description || !slices.Equal(wire.Boundaries, descriptor.Boundaries) || !wireBoundsMatchDescriptor(wire, descriptor) || wire.SpanKind != descriptor.SpanKind || wire.Source != descriptor.Source || wire.PrivacyClass != descriptor.PrivacyClass || wire.Maturity != descriptor.Maturity || wire.Semconv != descriptor.Semconv || wire.NameDomain != descriptor.NameDomain || wire.Availability != descriptor.Availability || wire.Component != descriptor.Component || wire.Instrument != descriptor.Instrument || wire.NumberType != descriptor.NumberType || wire.Unit != descriptor.Unit || wire.SeriesBudget != descriptor.SeriesBudget || wire.RecordWhen != descriptor.RecordWhen || !slices.Equal(wire.Inputs, descriptor.Inputs) || !declaredSourcesMatch(wire.DeclaredSources, descriptor.DeclaredSources) || wire.ValueSource != descriptor.ValueSource || wire.ComputedSource != descriptor.ComputedSource || !wireVariantsMatch(schema, wire.ResolvedVariants, descriptor.Variants) {
			t.Fatalf("signal %s differs between generated Go and wire manifest", descriptor.Key)
		}
		if !descriptor.SignalID.Valid() || all.Has(descriptor.SignalID) {
			t.Fatalf("signal %s reused its explicit ID", descriptor.Key)
		}
		all = append(all, descriptor.SignalID)
		if descriptor.Kind == "metric" {
			for _, variant := range wire.ResolvedVariants {
				for key, value := range variant.Attributes {
					if value.Declared || key == string(vvotel.AttrResourceName) || key == string(vvotel.AttrErrorCode) {
						t.Fatalf("metric %s admits a trace-only dimension", descriptor.Key)
					}
				}
			}
		}
	}
	if !slices.Equal(all, vvotel.AllSignals()) {
		t.Fatal("AllSignals differs from the explicit signal ID ledger")
	}
	descriptors[0].Name = "mutated"
	if vvotel.SignalDescriptors()[0].Name == "mutated" {
		t.Fatal("callers can mutate the generated descriptor authority")
	}
}

func schemaAttributeSlice(values map[attribute.Key]attribute.Value) []attribute.KeyValue {
	result := make([]attribute.KeyValue, 0, len(values))
	for key, value := range values {
		result = append(result, attribute.KeyValue{Key: key, Value: value})
	}
	return result
}

func assertCurrentSpanConforms(t *testing.T, key string, span *recordedSpan) {
	t.Helper()
	status := "unset"
	if span.status == codes.Error {
		status = "error"
	} else if span.status != codes.Unset {
		t.Fatalf("span %q has unsupported status %v", span.name, span.status)
	}
	descriptor := schemaDescriptor(t, key)
	if !slices.Contains(descriptor.Names, span.name) || !descriptor.Accepts(status, schemaAttributeSlice(span.attributes)) || !wireAcceptsStatus(loadWireSchema(t).Signals[key], status, span.attributes) {
		t.Fatalf("span %q does not conform to %s", span.name, key)
	}
}

func assertCurrentMetricConforms(t *testing.T, key string, point *recordedMetric) {
	t.Helper()
	descriptor := schemaDescriptor(t, key)
	if point.name != descriptor.Name || !descriptor.Accepts("", schemaAttributeSlice(point.attributes)) || !wireAccepts(loadWireSchema(t).Signals[key], point.attributes) {
		t.Fatalf("metric %q does not conform to %s", point.name, key)
	}
}

func TestSchema_AllCurrentOperationEmittersConformToWireManifest(t *testing.T) {
	schema := loadWireSchema(t)
	tracer := newTestTracerProvider()
	meter := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tracer, MeterProvider: meter, ResourceName: "contract"})
	if err != nil {
		t.Fatal(err)
	}
	rawService := &fakeRestorablePortService{}
	service := vvotel.Service[dummyModel, string, dummyModel](tel)(rawService)
	commandCalls := []func() error{
		func() error { _, callErr := service.List(context.Background(), port.ListCommand{}); return callErr },
		func() error { _, callErr := service.Count(context.Background(), port.CountCommand{}); return callErr },
		func() error {
			_, callErr := service.Get(context.Background(), port.GetCommand[string]{ID: "private"})
			return callErr
		},
		func() error {
			_, callErr := service.Create(context.Background(), port.CreateCommand[dummyModel]{Model: dummyModel{ID: "private"}})
			return callErr
		},
		func() error {
			_, callErr := service.Update(context.Background(), port.UpdateCommand[string, dummyModel]{ID: "private"})
			return callErr
		},
		func() error {
			_, callErr := service.Replace(context.Background(), port.ReplaceCommand[string, dummyModel]{ID: "private"})
			return callErr
		},
		func() error {
			_, callErr := service.Delete(context.Background(), port.DeleteCommand[string]{ID: "private"})
			return callErr
		},
		func() error {
			_, callErr := service.DeleteMany(context.Background(), port.BulkDeleteCommand[string]{IDs: []string{"private"}})
			return callErr
		},
	}
	for _, call := range commandCalls {
		if err = call(); err != nil {
			t.Fatal(err)
		}
	}
	restorable, ok := port.RestorableOf[string](service)
	if !ok {
		t.Fatal("instrumented restorable service lost its capability")
	}
	if _, err = restorable.Restore(context.Background(), port.RestoreCommand[string]{ID: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err = restorable.RestoreMany(context.Background(), port.BulkRestoreCommand[string]{IDs: []string{"private"}}); err != nil {
		t.Fatal(err)
	}

	key, err := storage.ParseKey("contract/private")
	if err != nil {
		t.Fatal(err)
	}
	stageID, err := storage.ParseStageID(strings.Repeat("A", 32))
	if err != nil {
		t.Fatal(err)
	}
	store := vvotel.Store(tel)(&fakeStorageStore{})
	storageCalls := []func() error{
		func() error {
			_, callErr := store.Put(context.Background(), key, strings.NewReader("private"), storage.PutOptions{})
			return callErr
		},
		func() error {
			reader, _, callErr := store.Open(context.Background(), key, storage.ReadOptions{})
			if callErr == nil {
				callErr = reader.Close()
			}
			return callErr
		},
		func() error { _, callErr := store.Head(context.Background(), key); return callErr },
		func() error { return store.Delete(context.Background(), key, storage.DeleteOptions{}) },
		func() error {
			_, callErr := store.Stage(context.Background(), strings.NewReader("private"), storage.StageOptions{})
			return callErr
		},
		func() error {
			_, callErr := store.Promote(context.Background(), stageID, key, storage.PromoteOptions{})
			return callErr
		},
		func() error { return store.Abort(context.Background(), stageID) },
		func() error {
			_, callErr := store.CleanupExpired(context.Background(), storage.CleanupOptions{})
			return callErr
		},
		func() error {
			_, callErr := store.TemporaryURL(context.Background(), key, storage.TemporaryURLOptions{})
			return callErr
		},
	}
	for _, call := range storageCalls {
		if err = call(); err != nil {
			t.Fatal(err)
		}
	}

	command := schemaDescriptor(t, "command_span")
	storageSpan := schemaDescriptor(t, "storage_span")
	expectedSpans := make(map[string]string, len(command.Names)+len(storageSpan.Names))
	for _, name := range command.Names {
		expectedSpans[name] = "command_span"
	}
	for _, name := range storageSpan.Names {
		expectedSpans[name] = "storage_span"
	}
	seenSpans := map[string]int{}
	for _, span := range tracer.spans {
		key, exists := expectedSpans[span.name]
		if !exists || span.status != codes.Unset || !schemaDescriptor(t, key).Accepts("unset", schemaAttributeSlice(span.attributes)) || !wireAcceptsStatus(schema.Signals[key], "unset", span.attributes) {
			t.Fatalf("current span does not conform to the generated and wire contracts: %+v", span)
		}
		seenSpans[span.name]++
	}
	if len(tracer.spans) != len(expectedSpans) {
		t.Fatalf("recorded %d operation spans, want %d", len(tracer.spans), len(expectedSpans))
	}
	for name := range expectedSpans {
		if seenSpans[name] != 1 {
			t.Fatalf("span %q recorded %d times", name, seenSpans[name])
		}
	}
	metricKeys := map[string]string{
		vvotel.MetricCommandDuration:       "command_duration",
		vvotel.MetricStorageDuration:       "storage_duration",
		vvotel.MetricStorageOperationBytes: "storage_operation_bytes",
		vvotel.MetricStorageCleanupRemoved: "storage_cleanup_removed",
	}
	seenMetrics := map[string]int{}
	for _, point := range meter.metrics {
		key, exists := metricKeys[point.name]
		if !exists || !schemaDescriptor(t, key).Accepts("", schemaAttributeSlice(point.attributes)) || !wireAccepts(schema.Signals[key], point.attributes) {
			t.Fatalf("current operation metric does not conform to the generated and wire contracts: %+v", point)
		}
		operation := point.attributes[vvotel.AttrOperationName].AsString()
		seenMetrics[key+"/"+operation]++
	}
	wantMetricCount := len(command.Names) + len(storageSpan.Names) + 3
	if len(meter.metrics) != wantMetricCount {
		t.Fatalf("recorded %d operation metrics, want %d", len(meter.metrics), wantMetricCount)
	}
	for _, name := range command.Names {
		operation := strings.TrimPrefix(name, "vv.command ")
		if seenMetrics["command_duration/"+operation] != 1 {
			t.Fatalf("command metric operation %q recorded %d times", operation, seenMetrics["command_duration/"+operation])
		}
	}
	for _, name := range storageSpan.Names {
		operation := strings.TrimPrefix(name, "vv.storage ")
		if seenMetrics["storage_duration/"+operation] != 1 {
			t.Fatalf("storage duration operation %q recorded %d times", operation, seenMetrics["storage_duration/"+operation])
		}
	}
	if seenMetrics["storage_operation_bytes/put"] != 1 || seenMetrics["storage_operation_bytes/stage"] != 1 ||
		seenMetrics["storage_cleanup_removed/cleanup_expired"] != 1 {
		t.Fatalf("storage result metrics=%v, want put/stage bytes and cleanup result once", seenMetrics)
	}
}

func TestSchema_CurrentEmittersConformToWireManifest(t *testing.T) {
	schema := loadWireSchema(t)
	tracer := newTestTracerProvider()
	meter := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tracer, MeterProvider: meter})
	if err != nil {
		t.Fatal(err)
	}
	service := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{})
	_, _ = service.Get(context.Background(), port.GetCommand[string]{ID: "secret-id"})
	key, err := storage.ParseKey("manifest/object")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = vvotel.Store(tel)(&fakeStorageStore{}).Head(context.Background(), key)
	ctx, span := tracer.Tracer("schema").Start(context.Background(), "cache-parent")
	vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true)).Observe(ctx, cache.Event{Operation: cache.LookupOperation, Outcome: cache.HitOutcome, Items: 1, EncodedBytes: 1, PayloadBytes: 1})
	vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true)).Observe(ctx, cachememory.Event{Operation: cachememory.PutOperation, Outcome: cachememory.StoredOutcome, Items: 1, ValueBytes: 1, ChargedBytes: 1})
	span.End()
	if len(meter.metrics) != 12 {
		t.Fatalf("control operations produced %d metrics, want 12", len(meter.metrics))
	}
	for _, point := range meter.metrics {
		var key string
		switch point.name {
		case vvotel.MetricCommandDuration:
			key = "command_duration"
		case vvotel.MetricCacheOperations:
			key = "cache_operations"
		case vvotel.MetricCacheEvents:
			key = "cache_events"
		case vvotel.MetricCacheItems:
			key = "cache_items"
		case vvotel.MetricCacheEncodedBytes:
			key = "cache_encoded_bytes"
		case vvotel.MetricCachePayloadBytes:
			key = "cache_payload_bytes"
		case vvotel.MetricCacheValueBytes:
			key = "cache_value_bytes"
		case vvotel.MetricCacheChargedBytes:
			key = "cache_charged_bytes"
		case vvotel.MetricStorageDuration:
			key = "storage_duration"
		default:
			t.Fatalf("unregistered metric %q was emitted", point.name)
		}
		if !wireAccepts(schema.Signals[key], point.attributes) {
			t.Fatalf("existing %s metric does not match its wire variant", point.name)
		}
		point.attributes[attribute.Key("forbidden.id")] = attribute.StringValue("secret")
		if wireAccepts(schema.Signals[key], point.attributes) {
			t.Fatal("wire validator accepted an extra attribute")
		}
		delete(point.attributes, attribute.Key("forbidden.id"))
		point.attributes[vvotel.AttrOperationName] = attribute.StringValue("unregistered")
		if wireAccepts(schema.Signals[key], point.attributes) {
			t.Fatal("wire validator accepted an unregistered operation")
		}
	}
	var eventSpan *recordedSpan
	for _, recorded := range tracer.spans {
		if len(recorded.events) == 0 {
			continue
		}
		if eventSpan != nil {
			t.Fatal("cache events were split across spans")
		}
		eventSpan = recorded
	}
	if eventSpan == nil || len(eventSpan.events) != 2 || len(eventSpan.eventAttributes) != 2 {
		t.Fatalf("cache event emission inventory differs: %+v", tracer.spans)
	}
	for index, name := range eventSpan.events {
		var key string
		switch name {
		case vvotel.EventCache:
			key = "cache_event"
		case vvotel.EventCacheBackend:
			key = "cache_backend_event"
		default:
			t.Fatalf("unregistered span event %q was emitted", name)
		}
		contract := schema.Signals[key]
		if !slices.Contains(contract.Names, name) || !wireAccepts(contract, eventSpan.eventAttributes[index]) {
			t.Fatalf("existing %s span event does not match its wire variant", name)
		}
		eventSpan.eventAttributes[index][attribute.Key("forbidden.id")] = attribute.StringValue("secret")
		if wireAccepts(contract, eventSpan.eventAttributes[index]) {
			t.Fatal("wire validator accepted an extra span-event attribute")
		}
	}
	matchedSpans := 0
	for _, recorded := range tracer.spans {
		if recorded == eventSpan {
			continue
		}
		var key string
		switch {
		case slices.Contains(schema.Signals["command_span"].Names, recorded.name):
			key = "command_span"
		case slices.Contains(schema.Signals["storage_span"].Names, recorded.name):
			key = "storage_span"
		default:
			t.Fatalf("unregistered framework span %q was emitted", recorded.name)
		}
		status := "unset"
		if recorded.status == codes.Error {
			status = "error"
		}
		contract := schema.Signals[key]
		if !wireAcceptsStatus(contract, status, recorded.attributes) {
			t.Fatalf("existing %s span does not match its wire variant", recorded.name)
		}
		recorded.attributes[attribute.Key("forbidden.id")] = attribute.StringValue("secret")
		if wireAcceptsStatus(contract, status, recorded.attributes) {
			t.Fatal("wire validator accepted an extra span attribute")
		}
		matchedSpans++
	}
	if matchedSpans != 2 {
		t.Fatalf("matched %d implemented framework spans, want 2", matchedSpans)
	}
}
func TestSchema_NormalizesSourceSpellingsAndOwnsLogKeys(t *testing.T) {
	for _, test := range []struct {
		mapValue     func(string) (string, bool)
		source, want string
	}{
		{vvotel.RemoteOperationName, "BulkDelete", "delete_many"},
		{vvotel.RuntimePlacementName, "per-replica", "per_replica"},
		{vvotel.RuntimeDurabilityName, "non-durable", "non_durable"},
		{vvotel.JobsWorkerOutcomeName, "timed_out", "timeout"},
		{vvotel.JobsWorkerOutcomeName, "cancelled", "canceled"},
		{vvotel.JobsPropagationOutcomeName, "tracestate_dropped", "tracestate_dropped"},
	} {
		got, ok := test.mapValue(test.source)
		if !ok || got != test.want {
			t.Fatalf("source %s was not normalized to %s", test.source, test.want)
		}
		if _, ok := test.mapValue("secret-unregistered"); ok {
			t.Fatal("unknown source value entered a closed mapping")
		}
	}
	if vvotel.LogTraceIDKey != "trace_id" || vvotel.LogSpanIDKey != "span_id" || vvotel.LogTraceFlagsKey != "trace_flags" {
		t.Fatal("log correlation key contract changed")
	}
}

func TestSchema_UnknownValuesAreDropped(t *testing.T) {
	checks := []func(string) (string, bool){
		vvotel.CacheOperationName,
		vvotel.CacheOutcomeName,
		vvotel.CacheBackendOperationName,
		vvotel.CacheBackendOutcomeName,
		vvotel.AllowedErrorCode,
	}
	for _, check := range checks {
		if mapped, ok := check("tenant-secret-or-id"); ok || mapped != "" {
			t.Fatalf("unknown value mapped to %q, %v", mapped, ok)
		}
	}
}

func TestSchema_StandardErrorCodesAreAllowed(t *testing.T) {
	for _, code := range []string{
		"unique", "not_unique", "foreign_key", "restrict", "required", "check",
		"exclusion", "too_long", "out_of_range", "invalid_format", "invalid_enum",
		"stale_version", "malformed_body", "invalid_id", "unknown_field", "bad_query",
		"too_large", "conflict", "not_found", "forbidden", "method_not_allowed",
		"unauthenticated", "deadlock", "serialization_failure", "lock_timeout",
		"transaction_aborted", "unavailable", "schema_not_ready", "internal",
	} {
		if mapped, ok := vvotel.AllowedErrorCode(code); !ok || mapped != code {
			t.Errorf("error code %q mapped to %q, %v", code, mapped, ok)
		}
		tracer := newTestTracerProvider()
		meter := newTestMeterProvider()
		tel, err := vvotel.New(vvotel.Config{TracerProvider: tracer, MeterProvider: meter})
		if err != nil {
			t.Fatal(err)
		}
		fault := &errs.Fault{Kind: errs.KindInternal, Code: errs.Code(code)}
		service := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{err: fault})
		if _, got := service.Get(context.Background(), port.GetCommand[string]{ID: "private"}); got != fault {
			t.Fatal("instrumentation changed the fault identity")
		}
		if len(tracer.spans) != 1 || len(meter.metrics) != 1 {
			t.Fatalf("error code %q was not emitted on exactly one command span", code)
		}
		codeValue, present := tracer.spans[0].attributes[vvotel.AttrErrorCode]
		if !present || codeValue.AsString() != code {
			t.Fatalf("error code %q was not emitted on the command span", code)
		}
		assertCurrentSpanConforms(t, "command_span", tracer.spans[0])
		assertCurrentMetricConforms(t, "command_duration", meter.metrics[0])
	}
}

func TestSchema_RuntimeMetadataIsGenerated(t *testing.T) {
	attributeMetadata, ok := vvotel.AttributeMetadataByKey["resource_name"]
	if !ok || attributeMetadata.MetricEligible {
		t.Fatalf("resource metadata missing or metric-eligible: %+v", attributeMetadata)
	}
	metricMetadata, ok := vvotel.MetricMetadataByKey["command_duration"]
	if !ok || metricMetadata.Unit != "s" || metricMetadata.Type != "histogram" {
		t.Fatalf("command duration metadata missing or incomplete: %+v", metricMetadata)
	}
	if vvotel.MigrationStatus == "" || vvotel.MigrationSince == "" || vvotel.MigrationPolicy == "" {
		t.Fatal("migration metadata was not generated")
	}
}

func TestSchema_UnknownCacheValuesAreDroppedEndToEnd(t *testing.T) {
	tp := newTestTracerProvider()
	parentCtx, span := tp.Tracer("test").Start(context.Background(), "parent")
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	vvotel.Cache(tel, vvotel.WithCacheSpanEvents(true)).Observe(parentCtx, cache.Event{Operation: cache.Operation("tenant-secret"), Outcome: cache.HitOutcome})
	vvotel.CacheMemory(tel, vvotel.WithCacheMemorySpanEvents(true)).Observe(parentCtx, cachememory.Event{Operation: cachememory.GetOperation, Outcome: cachememory.Outcome("tenant-secret")})
	if tp.eventCalls() != 0 || mp.counterAdds() != 0 {
		t.Fatalf("unknown cache value reached telemetry: events=%d adds=%d", tp.eventCalls(), mp.counterAdds())
	}
	span.End()
}

func TestSchema_UnknownErrorCodeIsDroppedEndToEnd(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel, err := vvotel.New(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	if err != nil {
		t.Fatal(err)
	}
	want := &errs.Fault{Kind: errs.KindInternal, Code: errs.Code("tenant-secret-or-id")}
	svc := vvotel.Service[dummyModel, string, dummyModel](tel)(&fakePortService{err: want})
	_, got := svc.Get(context.Background(), port.GetCommand[string]{ID: "id"})
	if got != want {
		t.Fatalf("business error identity changed: got %p, want %p", got, want)
	}
	if len(tp.spans) != 1 {
		t.Fatalf("expected one span, got %d", len(tp.spans))
	}
	if _, ok := tp.spans[0].attributes[vvotel.AttrErrorCode]; ok {
		t.Fatal("unknown error code was exported")
	}
	if mp.hasMetricAttribute(vvotel.AttrErrorCode) {
		t.Fatal("error code was exported as a metric attribute")
	}
}

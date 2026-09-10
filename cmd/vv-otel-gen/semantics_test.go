package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/jobs"
)

func TestGoroutineExitIsACompleteSpanOnlyTerminal(t *testing.T) {
	r := validRegistryFixture(t)
	spans := 0
	for key, signal := range r.Signals {
		terminalOps, ordinaryOps := map[string]bool{}, map[string]bool{}
		for _, variant := range signal.Variants {
			resolved, err := resolveVariant(r, variant, signal.Kind == "metric")
			if err != nil {
				t.Fatal(err)
			}
			terminal := contains(resolved.Attributes[r.Attributes["operation_outcome"].Name].Values, "goroutine_exit")
			if terminal {
				if signal.Kind != "span" || variant.Status != "error" {
					t.Fatalf("%s has a non-span/error Goexit terminal", key)
				}
				if _, ok := resolved.Attributes[r.Attributes["error_type"].Name]; ok {
					t.Fatal("Goexit fabricated an error type")
				}
				if _, ok := resolved.Attributes[r.Attributes["error_code"].Name]; ok {
					t.Fatal("Goexit fabricated an error code")
				}
				if !contains(variant.Absent, "error_type") {
					t.Fatal("Goexit omission is not explicit")
				}
			}
			for _, op := range resolved.Attributes[r.Attributes["operation_name"].Name].Values {
				if terminal {
					terminalOps[op] = true
				} else {
					ordinaryOps[op] = true
				}
			}
		}
		if signal.Kind == "span" {
			spans++
			if len(terminalOps) == 0 || !reflect.DeepEqual(terminalOps, ordinaryOps) {
				t.Fatalf("%s Goexit operation domain incomplete: %v vs %v", key, terminalOps, ordinaryOps)
			}
		} else if len(terminalOps) > 0 {
			t.Fatalf("%s fabricated a metric/event sample on Goexit", key)
		}
	}
	if spans != 11 {
		t.Fatalf("span wrapper inventory drifted: %d", spans)
	}
	for key, want := range map[string]int{"command_duration": 100, "cache_operations": 116} {
		got, err := metricCardinality(r, r.Signals[key])
		if err != nil || got != want {
			t.Fatalf("Goexit changed %s bound: %d/%v", key, got, err)
		}
	}
}

func acceptsTuple(t *testing.T, r Registry, key string, attrs map[string]string) bool {
	t.Helper()
	matches := 0
	for _, v := range r.Signals[key].Variants {
		w, err := resolveVariant(r, v, r.Signals[key].Kind == "metric")
		if err != nil {
			t.Fatal(err)
		}
		allowed := true
		for name, spec := range w.Attributes {
			value, found := attrs[name]
			if !found && !spec.Optional || found && !contains(spec.Values, value) {
				allowed = false
				break
			}
		}
		for name := range attrs {
			if _, found := w.Attributes[name]; !found {
				allowed = false
				break
			}
		}
		if allowed {
			matches++
		}
	}
	if matches > 1 {
		t.Fatalf("%s tuple %v matches %d variants", key, attrs, matches)
	}
	return matches == 1
}
func tuple(r Registry, attrs map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range attrs {
		result[r.Attributes[key].Name] = value
	}
	return result
}

func TestCacheFacadeMeasurementMatricesAreExact(t *testing.T) {
	r := validRegistryFixture(t)
	allowed := map[string]bool{}
	for _, line := range []string{
		"lookup hit - false", "lookup hit - true", "lookup miss - false", "lookup miss - true",
		"lookup negative - false", "lookup negative - true", "lookup stale - false", "lookup stale - true",
		"lookup error backend false", "lookup error corrupt false", "lookup error runtime false", "lookup error runtime true",
		"lookup_many complete - false", "lookup_many error backend false", "lookup_many error corrupt false", "lookup_many error limit false", "lookup_many error runtime false",
		"load loaded - false", "load negative - false", "load error - false", "load superseded - false",
		"load_many complete - false", "load_many stored - false", "load_many error - false", "load_many error limit false", "load_many error backend false",
		"put stored - false", "put superseded - false", "put error backend false",
		"forget deleted - false", "forget error backend false",
	} {
		allowed[line] = true
	}
	encoded := map[string]bool{}
	payload := map[string]bool{}
	for _, memo := range []string{"false", "true"} {
		for _, out := range []string{"hit", "miss", "negative", "stale"} {
			encoded["lookup "+out+" - "+memo] = true
		}
		for _, out := range []string{"hit", "stale"} {
			payload["lookup "+out+" - "+memo] = true
		}
	}
	for _, line := range []string{"lookup_many complete - false", "load loaded - false", "load negative - false", "load_many complete - false", "load_many stored - false", "put stored - false"} {
		encoded[line] = true
	}
	for _, line := range []string{"lookup_many complete - false", "load loaded - false"} {
		payload[line] = true
	}
	for key, expected := range map[string]map[string]bool{"cache_events": allowed, "cache_items": allowed, "cache_encoded_bytes": encoded, "cache_payload_bytes": payload} {
		positive, negative := 0, 0
		for _, op := range r.Domains["cache.operations"].Values {
			for _, out := range r.Domains["cache.outcomes"].Values {
				for _, reason := range append([]string{"-"}, r.Domains["cache.reason"].Values...) {
					for _, memo := range []string{"false", "true"} {
						row := strings.Join([]string{op, out, reason, memo}, " ")
						attrs := map[string]string{"component": "cache", "cache_layer": "facade", "operation_name": op, "operation_outcome": out, "memoized": memo}
						if reason != "-" {
							attrs["reason"] = reason
						}
						got := acceptsTuple(t, r, key, tuple(r, attrs))
						if got != expected[row] {
							t.Fatalf("%s admits %q = %t, want %t", key, row, got, expected[row])
						}
						if got {
							positive++
						} else {
							negative++
						}
					}
				}
			}
		}
		if positive != len(expected) || negative == 0 {
			t.Fatalf("%s matrix was not exhaustive: %d/%d", key, positive, negative)
		}
	}
	for key, want := range map[string]int{"cache_events": 49, "cache_items": 49, "cache_encoded_bytes": 14, "cache_payload_bytes": 6, "cache_value_bytes": 13, "cache_charged_bytes": 11, "cache_operations": 116} {
		got, err := metricCardinality(r, r.Signals[key])
		if err != nil || got != want {
			t.Fatalf("%s bound=%d err=%v, want %d", key, got, err, want)
		}
	}
	for _, key := range []string{"cache_encoded_bytes", "cache_payload_bytes", "cache_value_bytes", "cache_charged_bytes"} {
		if r.Signals[key].RecordWhen != "non_negative" {
			t.Fatalf("%s must admit present zero measurements", key)
		}
	}
}

func cacheBackendExpectedRows() map[string]map[string]bool {
	events := map[string]bool{}
	for _, row := range []string{
		"get hit -", "get miss -", "get miss expired", "get rejected read_limit",
		"get_many complete -", "get_many rejected batch_item_limit", "get_many rejected batch_total_limit",
		"put stored -", "put replaced -", "put rejected max_item_bytes", "put rejected max_bytes",
		"delete miss -", "delete deleted -",
		"evict evicted expired", "evict evicted max_entries", "evict evicted max_bytes",
		"reset complete reset", "close complete close",
	} {
		events[row] = true
	}
	valueBytes := map[string]bool{}
	for _, row := range []string{
		"get hit -", "get rejected read_limit",
		"get_many complete -", "get_many rejected batch_item_limit", "get_many rejected batch_total_limit",
		"put stored -", "put replaced -", "put rejected max_item_bytes", "put rejected max_bytes",
		"delete deleted -",
		"evict evicted expired", "evict evicted max_entries", "evict evicted max_bytes",
	} {
		valueBytes[row] = true
	}
	chargedBytes := map[string]bool{}
	for _, row := range []string{
		"get hit -", "get_many complete -",
		"put stored -", "put replaced -", "put rejected max_bytes",
		"delete deleted -",
		"evict evicted expired", "evict evicted max_entries", "evict evicted max_bytes",
		"reset complete reset", "close complete close",
	} {
		chargedBytes[row] = true
	}
	return map[string]map[string]bool{
		"cache_events":        events,
		"cache_items":         events,
		"cache_value_bytes":   valueBytes,
		"cache_charged_bytes": chargedBytes,
	}
}

func cacheBackendMatrixError(t *testing.T, r Registry, key string, expected map[string]bool) error {
	t.Helper()
	positive, negative := 0, 0
	for _, operation := range r.Domains["cache_backend.operations"].Values {
		for _, outcome := range r.Domains["cache_backend.outcomes"].Values {
			for _, reason := range append([]string{"-"}, r.Domains["cache_backend.reason"].Values...) {
				row := strings.Join([]string{operation, outcome, reason}, " ")
				attrs := map[string]string{
					"component":         "cache_backend",
					"cache_layer":       "memory_backend",
					"operation_name":    operation,
					"operation_outcome": outcome,
				}
				if reason != "-" {
					attrs["reason"] = reason
				}
				got := acceptsTuple(t, r, key, tuple(r, attrs))
				if got != expected[row] {
					return fmt.Errorf("%s admits %q = %t, want %t", key, row, got, expected[row])
				}
				if got {
					positive++
				} else {
					negative++
				}
			}
		}
	}
	if positive != len(expected) || negative == 0 {
		return fmt.Errorf("%s matrix is not exhaustive: %d/%d", key, positive, negative)
	}
	return nil
}

func TestCacheBackendMeasurementMatricesAreExact(t *testing.T) {
	r := validRegistryFixture(t)
	for key, expected := range cacheBackendExpectedRows() {
		if err := cacheBackendMatrixError(t, r, key, expected); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCacheBackendMeasurementMatrixMutationsAreDetected(t *testing.T) {
	for key, expected := range cacheBackendExpectedRows() {
		t.Run(key, func(t *testing.T) {
			r := validRegistryFixture(t)
			modifySignal(&r, key, func(signal *Signal) {
				signal.Variants = append([]Variant(nil), signal.Variants[:len(signal.Variants)-1]...)
			})
			if err := cacheBackendMatrixError(t, r, key, expected); err == nil {
				t.Fatal("backend tuple deletion escaped the exhaustive semantic matrix")
			}
		})
	}
}

func TestWorkerAdmissionAndResultMatricesAreExact(t *testing.T) {
	r := validRegistryFixture(t)
	admitted := 0
	for _, out := range r.Domains["jobs_worker.outcomes"].Values {
		for _, signal := range r.Domains["jobs_worker.admission_signal"].Values {
			want := (out == "ready" || out == "saturated") && (signal == "ready" || signal == "unrestricted") ||
				out == "held" && signal == "held" || out == "stale" && signal == "stale" ||
				out == "invalid" && (signal == "uninitialized" || signal == "invalid")
			attrs := tuple(r, map[string]string{"component": "jobs_worker", "operation_name": "admission", "operation_outcome": out, "admission_signal": signal})
			if got := acceptsTuple(t, r, "jobs_worker_admission", attrs); got != want {
				t.Fatalf("admission %s/%s = %t, want %t", out, signal, got, want)
			}
			if want {
				admitted++
			}
		}
	}
	if admitted != 8 {
		t.Fatalf("admission union=%d, want 8", admitted)
	}
	results := 0
	for _, op := range r.Domains["jobs_worker.operations"].Values {
		for _, mutation := range r.Domains["jobs_worker.mutation"].Values {
			for _, control := range r.Domains["jobs_worker.control"].Values {
				want := (op == "renew" || op == "apply") && (mutation != "ambiguous" || control == "none") && !(op == "renew" && mutation == "applied" && control == "terminated")
				attrs := tuple(r, map[string]string{"component": "jobs_worker", "operation_name": op, "mutation": mutation, "control": control})
				if got := acceptsTuple(t, r, "jobs_worker_delivery_results", attrs); got != want {
					t.Fatalf("result %s/%s/%s=%t, want %t", op, mutation, control, got, want)
				}
				if want {
					results++
				}
			}
		}
	}
	if results != 13 {
		t.Fatalf("delivery result union=%d, want 13", results)
	}
}

func jobsNumericSemanticsError(r Registry) error {
	tests := []struct {
		key        string
		minimum    int64
		maximum    int64
		recordWhen string
		source     string
	}{
		{"jobs_handler_attempt", 1, jobs.MaxAttemptOrdinal, "positive", "jobs.MaxAttemptOrdinal"},
		{"jobs_worker_delivery_results", 1, jobs.MaxClaimItems, "positive", "jobs.MaxClaimItems"},
		{"jobs_scheduler_results", 0, jobs.MaxDefinitions, "non_negative", "jobs.MaxDefinitions"},
	}
	for _, test := range tests {
		signal := r.Signals[test.key]
		bounds, err := parseMetricBounds(signal)
		if err != nil {
			return fmt.Errorf("%s bounds: %w", test.key, err)
		}
		if !bounds.hasMinimum || bounds.minimumInt64 != test.minimum || !bounds.hasMaximum || bounds.maximumInt64 != test.maximum || signal.RecordWhen != test.recordWhen || !strings.Contains(signal.Source, test.source) {
			return fmt.Errorf("%s numeric source contract drifted", test.key)
		}
	}
	return nil
}

func TestJobsNumericMeasurementsMatchSourceInvariants(t *testing.T) {
	if err := jobsNumericSemanticsError(validRegistryFixture(t)); err != nil {
		t.Fatal(err)
	}
}

func TestJobsNumericSourceMutationsAreDetected(t *testing.T) {
	for name, mutate := range map[string]func(*Registry){
		"handler_zero": func(r *Registry) {
			modifySignal(r, "jobs_handler_attempt", func(signal *Signal) { signal.Min = numericBound("0") })
		},
		"delivery_zero": func(r *Registry) {
			modifySignal(r, "jobs_worker_delivery_results", func(signal *Signal) { signal.Min = numericBound("0") })
		},
		"scheduler_unbounded": func(r *Registry) {
			modifySignal(r, "jobs_scheduler_results", func(signal *Signal) { signal.Max = "" })
		},
		"scheduler_wrong_source": func(r *Registry) {
			modifySignal(r, "jobs_scheduler_results", func(signal *Signal) { signal.Source = "jobs.Scheduler.RunDue terminal cycle" })
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := validRegistryFixture(t)
			mutate(&r)
			if err := jobsNumericSemanticsError(r); err == nil {
				t.Fatal("source numeric mutation escaped its semantic authority")
			}
		})
	}
}

func TestWorkerOperationFailureAndMeasurementMatricesAreExact(t *testing.T) {
	r := validRegistryFixture(t)
	outcomes := map[string][]string{
		"run": {"started", "complete", "canceled", "failed"}, "drain": {"started", "complete", "forced", "failed"},
		"claim": {"complete", "empty", "saturated", "timeout", "canceled", "failed"}, "recover": {"complete", "empty", "saturated", "timeout", "canceled", "failed"},
		"renew": {"complete", "timeout", "canceled", "failed"}, "apply": {"complete", "timeout", "canceled", "failed"},
		"admission": {"ready", "held", "stale", "invalid", "saturated"},
	}
	for _, key := range []string{"jobs_worker_operations", "jobs_worker_duration", "jobs_worker_items", "jobs_worker_bytes"} {
		positive, negative := 0, 0
		for _, op := range r.Domains["jobs_worker.operations"].Values {
			for _, out := range r.Domains["jobs_worker.outcomes"].Values {
				for _, failure := range []string{"", "none", "driver", "driver_contract", "driver_panic", "runtime"} {
					driver := contains([]string{"claim", "recover", "renew", "apply"}, op)
					want := contains(outcomes[op], out) && ((out == "failed" && failure != "" && failure != "none" && (driver || failure == "runtime")) || (out != "failed" && failure == ""))
					switch key {
					case "jobs_worker_duration":
						want = want && out != "started" && out != "saturated" && op != "admission"
					case "jobs_worker_items":
						want = want && driver
					case "jobs_worker_bytes":
						want = want && (op == "claim" || op == "recover")
					}
					attrs := map[string]string{"component": "jobs_worker", "operation_name": op, "operation_outcome": out}
					if failure != "" {
						attrs["failure"] = failure
					}
					if got := acceptsTuple(t, r, key, tuple(r, attrs)); got != want {
						t.Fatalf("%s %s/%s/%s=%t, want %t", key, op, out, failure, got, want)
					}
					if want {
						positive++
					} else {
						negative++
					}
				}
			}
		}
		if positive == 0 || negative == 0 {
			t.Fatal("worker matrix lacks both controls")
		}
	}
}

func TestDispositionUnionMatchesPublicSourceConstructors(t *testing.T) {
	r := validRegistryFixture(t)
	backend, err := jobs.BackendIDFromBytes([jobs.BackendIDBytes]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	id, err := jobs.InvocationIDFromBytes([16]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.NewLeaseRef(backend, id, []byte("lease"))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := jobs.ParseBindingName("binding")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("build")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	add := func(command jobs.DeliveryCommand, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		disposition := command.Disposition()
		kind, reason := "", command.Reason().String()
		if !disposition.IsZero() {
			kind = disposition.Kind().String()
			if kind == "cancelled" {
				kind = "canceled"
			}
			reason = disposition.Reason().String()
		}
		want[strings.Join([]string{command.Kind().String(), kind, reason}, "|")] = true
	}
	add(jobs.BeginAttemptCommand(lease, binding, build))
	add(jobs.ProgressCommand(lease))
	add(jobs.RejectCorruptCommand(lease))
	add(jobs.ArbitrateAttemptDeadlineCommand(lease, jobs.MinRetryDelay))
	add(jobs.ReleaseUnchangedCommand(lease, binding, build, jobs.MinRetryDelay))
	add(jobs.ReleaseForShutdownCommand(lease, jobs.MinRetryDelay))
	add(jobs.ReleaseForAdmissionCommand(lease, jobs.MinRetryDelay))
	for reason := jobs.ReasonNone; reason <= jobs.ReasonAttemptsExhausted; reason++ {
		if c, err := jobs.DeferDeliveryCommand(lease, reason, jobs.PublicFailure{}, jobs.MinRetryDelay); err == nil {
			add(c, nil)
		}
		if c, err := jobs.FinishDeliveryCommand(lease, jobs.InvocationDiscarded, reason, jobs.PublicFailure{}); err == nil {
			add(c, nil)
		}
		if c, err := jobs.RevokeAttemptCommand(lease, reason, jobs.MinRetryDelay); err == nil {
			add(c, nil)
		}
		for kind := jobs.DispositionSucceeded; kind <= jobs.DispositionTerminated; kind++ {
			for _, cost := range []jobs.RetryCost{jobs.RetryCostNone, jobs.RetryCostCharged} {
				for _, delay := range []bool{false, true} {
					spec := jobs.DispositionSpec{Kind: kind, Reason: reason, RetryCost: cost}
					if delay {
						spec.RetryAfter = jobs.MinRetryDelay
					}
					d, err := jobs.NewDisposition(spec)
					if err != nil {
						continue
					}
					commandDelay := spec.RetryAfter
					if kind == jobs.DispositionRetry {
						commandDelay = jobs.MinRetryDelay
					}
					if c, err := jobs.FinishAttemptCommand(lease, d, commandDelay, jobs.MinRetryDelay); err == nil {
						add(c, nil)
					}
				}
			}
		}
	}
	if len(want) != 42 {
		t.Fatalf("public command projection union=%d, want 42", len(want))
	}
	positive, negative := 0, 0
	for _, cmd := range r.Domains["jobs_worker.command_kind"].Values {
		for _, kind := range append([]string{""}, r.Domains["jobs_worker.disposition"].Values...) {
			for _, reason := range r.Domains["jobs_worker.reason"].Values {
				key := strings.Join([]string{cmd, kind, reason}, "|")
				attrs := map[string]string{"component": "jobs_worker", "operation_name": "apply", "command_kind": cmd, "reason": reason}
				if kind != "" {
					attrs["disposition"] = kind
				}
				if got := acceptsTuple(t, r, "jobs_worker_dispositions", tuple(r, attrs)); got != want[key] {
					t.Fatalf("disposition %s=%t, source constructor allows %t", key, got, want[key])
				}
				if want[key] {
					positive++
				} else {
					negative++
				}
			}
		}
	}
	if positive != len(want) || negative == 0 {
		t.Fatal("disposition matrix is not exhaustive")
	}
}

func TestSuccessOmissionAndPersistedStorageSizeContract(t *testing.T) {
	r := validRegistryFixture(t)
	attrs := tuple(r, map[string]string{"component": "command", "operation_name": "get", "operation_outcome": "ok"})
	if !acceptsTuple(t, r, "command_span", attrs) {
		t.Fatal("command success control rejected")
	}
	attrs[r.Attributes["error_code"].Name] = r.Domains["error_codes"].Values[0]
	if acceptsTuple(t, r, "command_span", attrs) {
		t.Fatal("command success admitted error_code")
	}
	s := r.Signals["storage_operation_bytes"]
	if s.SignalID != 5 || !strings.Contains(s.Source, "returned Info.Size") || !strings.Contains(s.Source, "returned Staged.Info.Size") {
		t.Fatal("persisted size provenance/ID changed")
	}
	for _, op := range r.Domains["storage.operations"].Values {
		for _, out := range r.Domains["storage.outcomes"].Values {
			attrs := tuple(r, map[string]string{"component": "storage", "operation_name": op, "operation_outcome": out})
			want := (op == "put" || op == "stage") && out == "ok"
			if got := acceptsTuple(t, r, "storage_operation_bytes", attrs); got != want {
				t.Fatal(fmt.Sprintf("persisted-size %s/%s=%t, want %t", op, out, got, want))
			}
		}
	}
	for key, want := range map[string]string{"authentication_duration": "vv.authentication.duration", "remote_duration": "vv.remote.duration"} {
		if r.Signals[key].Name != want {
			t.Fatalf("%s changed its roadmap wire name", key)
		}
	}
}

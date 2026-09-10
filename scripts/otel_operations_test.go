package scripts

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const collectorDigest = "sha256:799dc6cf12c96192af37b5bdba804da8c10b3bc563b43cb90c3f3c58d9572ad6"

func TestOTelOperationsCollectorConfigsAreBounded(t *testing.T) {
	configs := []string{
		"collector.dev.yaml",
		"collector.production.yaml",
		"collector.production-wal.yaml",
		"collector.tail.yaml",
		"collector.metrics-only-management-filter.yaml",
	}
	for _, name := range configs {
		content := readOTelOperationFile(t, "../_examples/otel-collector/"+name)
		for _, required := range []string{
			"memory_limiter:",
			"batch:",
			"processors: [memory_limiter,",
			"metrics:\n      level: detailed",
			"without_type_suffix: true",
			"without_units: true",
		} {
			if !strings.Contains(content, required) {
				t.Errorf("%s lacks %q", name, required)
			}
		}
	}

	for _, name := range []string{"collector.production.yaml", "collector.production-wal.yaml", "collector.tail.yaml"} {
		content := readOTelOperationFile(t, "../_examples/otel-collector/"+name)
		for _, field := range []string{"num_consumers", "queue_size"} {
			if positiveYAMLInteger(content, field) <= 0 {
				t.Errorf("%s has no positive bounded %s", name, field)
			}
		}
		if !strings.Contains(content, "max_elapsed_time: 5m") {
			t.Errorf("%s has no finite retry window", name)
		}
	}

	wal := readOTelOperationFile(t, "../_examples/otel-collector/collector.production-wal.yaml")
	if !strings.Contains(wal, "file_storage:") || !strings.Contains(wal, "storage: file_storage") || !strings.Contains(wal, "extensions: [health_check, file_storage]") {
		t.Fatal("WAL config does not activate and attach file_storage")
	}
	plain := readOTelOperationFile(t, "../_examples/otel-collector/collector.production.yaml")
	if strings.Contains(plain, "file_storage") {
		t.Fatal("in-memory production config silently enables persistent storage")
	}
}

func TestOTelOperationsCollectorImageAndTailBoundaryArePinned(t *testing.T) {
	compose := readOTelOperationFile(t, "../_examples/otel-collector/compose.yaml")
	if !strings.Contains(compose, collectorDigest) || strings.Contains(compose, ":latest") {
		t.Fatal("Collector compose image is not digest-pinned")
	}
	tail := readOTelOperationFile(t, "../_examples/otel-collector/collector.tail.yaml")
	for _, required := range []string{
		"decision_wait: 30s",
		"expected_new_traces_per_sec: 1000",
		"sampled_cache_size:",
		"non_sampled_cache_size:",
		"type: status_code",
		"type: latency",
		"type: rate_limiting",
		"processors: [memory_limiter, tail_sampling, batch]",
	} {
		if !strings.Contains(tail, required) {
			t.Errorf("tail config lacks %q", required)
		}
	}
	doc := readOTelOperationFile(t, "../docs/operations/otel/sampling.md")
	for _, required := range []string{"literal\n`AlwaysSample`", "ParentBased(AlwaysSample())", "unsampled remote parent", "cannot reconstruct", "late-span", "trace-ID routing", "separate trace", "not an\naudit"} {
		if !strings.Contains(doc, required) {
			t.Errorf("tail boundary documentation lacks %q", required)
		}
	}
}

func TestOTelOperationsPrometheusReferencesAreContracted(t *testing.T) {
	rules := readOTelOperationFile(t, "../docs/operations/otel/prometheus-rules.yaml")
	if strings.Contains(rules, "or vector(0)") || strings.Contains(rules, "clamp_min") {
		t.Fatal("rules turn missing or zero denominators into healthy zero")
	}
	if strings.Count(rules, `vv_http_management!="true"`) < 3 || strings.Contains(rules, `http_route!~"^/(live|ready)$"`) {
		t.Fatal("HTTP numerator, denominator, and latency query do not all use the bounded management marker")
	}
	if strings.Contains(rules, "max by (exporter) (otelcol_exporter_queue_") {
		t.Fatal("Collector queue ratio can combine size and capacity from different signal layers")
	}

	allowed := loadOTelPrometheusContract(t)
	identifier := regexp.MustCompile(`\b(?:vv|http|otelcol)_[a-z0-9_]+\b`)
	for _, name := range identifier.FindAllString(rules, -1) {
		if !allowed[name] {
			t.Errorf("Prometheus identifier %q is absent from Frostgrove, native, and Collector contracts", name)
		}
	}

	fixtures := readOTelOperationFile(t, "../docs/operations/otel/prometheus-tests.yaml")
	for _, required := range []string{"counter reset", "memory_backend", "empty input", "zero denominator", "management routes", "auth refusal shift", "missing baseline", "mixed capacity rollout"} {
		if !strings.Contains(fixtures, required) {
			t.Errorf("Prometheus fixtures lack %q case", required)
		}
	}
	if strings.Count(fixtures, `vv_http_management="true"`) < 2 || strings.Count(fixtures, `vv_http_management="false"`) < 3 {
		t.Fatal("management fixture does not model authoritative true and false markers")
	}
}

func TestOTelOperationsCatalogStatesDenominatorsAndUnknowns(t *testing.T) {
	catalog := readOTelOperationFile(t, "../docs/operations/otel/queries.md")
	for _, required := range []string{
		"Every ratio divides events from one instrument and one semantic layer",
		"produces no result",
		"zero denominator produces\nNaN or infinity",
		"from both numerator and denominator",
		"`vv.http.management=\"true\"`",
		"writes `false` on\nevery other server datapoint",
		"`otelhttp.Labeler` value",
		"never infer management traffic from `http.route`",
		"metrics-only exclusion",
		"both-signals choice",
		"`vv:auth_refusal:reason_share1h`",
		"`vv:auth_refusal:reason_share_shift5m`",
		"Missing short or long input leaves the\nshift unknown",
		"directly\nmatching `otelcol_exporter_queue_capacity` series",
		"different\ncapacities are never combined",
	} {
		if !strings.Contains(catalog, required) {
			t.Errorf("query catalog lacks %q", required)
		}
	}
}

func TestOTelOperationsMetricsOnlyManagementFilterDoesNotTouchTraces(t *testing.T) {
	config := readOTelOperationFile(t, "../_examples/otel-collector/collector.metrics-only-management-filter.yaml")
	for _, required := range []string{
		`attributes["vv.http.management"] == "true"`,
		"processors: [memory_limiter, filter/management_metrics, batch]",
		"traces:\n      receivers: [otlp]\n      processors: [memory_limiter, batch]",
	} {
		if !strings.Contains(config, required) {
			t.Errorf("metrics-only filter lacks %q", required)
		}
	}
	traceSection := strings.SplitN(config, "    metrics:\n      receivers: [otlp]", 2)[0]
	if strings.Contains(traceSection[strings.LastIndex(traceSection, "    traces:"):], "filter/management_metrics") {
		t.Fatal("metrics-only management filter also suppresses request spans")
	}
}

func TestOTelOperationsWireValidatorIsAnExplicitV2Gate(t *testing.T) {
	script := readOTelOperationFile(t, "otel-operations.sh")
	for _, required := range []string{"OTEL_WIRE_INPUT", "VV_OTEL_WIRE_ALLOW_PARTIAL", "otel/wire_manifest.json", "./scripts/otel-wire-validator", `GOWORK=off GOPROXY=off GOTOOLCHAIN=local "$GO" run`} {
		if !strings.Contains(script, required) {
			t.Errorf("wire gate lacks %q", required)
		}
	}
}

func TestOTelOperationsWeaverRunsPinnedNativeLiveCheck(t *testing.T) {
	script := readOTelOperationFile(t, "otel-operations.sh")
	for _, required := range []string{
		"otel/weaver@sha256:d1fb16d279f39810c340fbbf1cf9e5e995a3a9cefa531938e9012437e3bc00c1",
		"registry live-check --v2",
		"https://github.com/open-telemetry/semantic-conventions@v1.40.0[model]",
		"--input-source otlp",
		"--otlp-grpc-address 127.0.0.1",
		"--otlp-grpc-port 14317",
		"--admin-port 14320",
		"--inactivity-timeout 120",
		"http://127.0.0.1:14320/health",
		"./otelnative/cmd/weaver-native-http",
		"http://127.0.0.1:14320/stop",
		"total_entities",
		"total_entities_by_type.span",
		`seen_registry_metrics["http.server.request.duration"]`,
		"advice_level_counts.violation",
		`wait "$listener_pid"`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("Weaver live-check lacks %q", required)
		}
	}
	weaverStart := strings.Index(script, "weaver()")
	wireStart := strings.Index(script, "wire()")
	if weaverStart < 0 || wireStart <= weaverStart {
		t.Fatal("cannot isolate Weaver operation")
	}
	weaverGate := script[weaverStart:wireStart]
	if strings.Contains(weaverGate, "otel/wire_manifest.json") || strings.Contains(weaverGate, "otel-wire-validator") {
		t.Fatal("Weaver sends Frostgrove custom telemetry against the upstream registry")
	}
}

func TestOTelOfflineGatesCannotWarmDependenciesFromTheNetwork(t *testing.T) {
	checks := readOTelOperationFile(t, "checks.sh")
	start := strings.Index(checks, "check_otel_module()")
	if start < 0 {
		t.Fatal("cannot locate check_otel_module")
	}
	end := strings.Index(checks[start:], "check_otel_operations()")
	if end < 0 {
		t.Fatal("cannot locate the end of check_otel_module")
	}
	moduleGate := checks[start : start+end]
	if strings.Count(moduleGate, "GOPROXY=off GOTOOLCHAIN=local") < 2 {
		t.Fatal("local module and consumer checks do not both disable network and toolchain downloads")
	}
	operations := readOTelOperationFile(t, "otel-operations.sh")
	offlineStart := strings.Index(operations, "offline()")
	if offlineStart < 0 {
		t.Fatal("cannot locate offline operation gate")
	}
	offlineEnd := strings.Index(operations[offlineStart:], "collector()")
	if offlineEnd < 0 {
		t.Fatal("cannot locate the end of offline operation gate")
	}
	if strings.Count(operations[offlineStart:offlineStart+offlineEnd], "GOPROXY=off GOTOOLCHAIN=local") < 2 {
		t.Fatal("offline operation tests do not both disable network and toolchain downloads")
	}
}

func readOTelOperationFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func positiveYAMLInteger(content, field string) int {
	pattern := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(field) + `:\s+([0-9]+)\s*$`)
	match := pattern.FindStringSubmatch(content)
	if len(match) != 2 {
		return 0
	}
	value, _ := strconv.Atoi(match[1])
	return value
}

func loadOTelPrometheusContract(t *testing.T) map[string]bool {
	t.Helper()
	content, err := os.ReadFile("../otel/wire_manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ContractVersion string `json:"contract_version"`
		Attributes      map[string]struct {
			Name string `json:"name"`
		} `json:"attributes"`
		Signals map[string]struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"signals"`
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ContractVersion != "vv-otel/v2" {
		t.Fatalf("wire contract version = %q", manifest.ContractVersion)
	}
	allowed := make(map[string]bool)
	for _, attribute := range manifest.Attributes {
		allowed[prometheusIdentifier(attribute.Name)] = true
	}
	for _, signal := range manifest.Signals {
		if signal.Kind != "metric" {
			continue
		}
		name := prometheusIdentifier(signal.Name)
		allowed[name] = true
		allowed[name+"_bucket"] = true
		allowed[name+"_count"] = true
		allowed[name+"_sum"] = true
	}
	for _, name := range []string{
		"http_server_request_duration_bucket",
		"http_server_request_duration_count",
		"http_route",
		"vv_http_management",
		"http_response_status_code",
		"otelcol_exporter_enqueue_failed_spans",
		"otelcol_exporter_enqueue_failed_metric_points",
		"otelcol_exporter_enqueue_failed_log_records",
		"otelcol_exporter_send_failed_spans",
		"otelcol_exporter_send_failed_metric_points",
		"otelcol_exporter_send_failed_log_records",
		"otelcol_exporter_queue_size",
		"otelcol_exporter_queue_capacity",
		"otelcol_receiver_refused_spans",
		"otelcol_receiver_refused_metric_points",
		"otelcol_receiver_refused_log_records",
		"otelcol_processor_tail_sampling_sampling_trace_dropped_too_early",
	} {
		allowed[name] = true
	}
	return allowed
}

func prometheusIdentifier(name string) string {
	return strings.NewReplacer(".", "_", "-", "_").Replace(name)
}

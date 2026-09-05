package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Registry struct {
	ContractVersion string               `json:"contract_version"`
	Scope           ScopeConfig          `json:"scope"`
	Components      []string             `json:"components"`
	Operations      map[string][]string  `json:"operations"`
	Outcomes        []string             `json:"outcomes"`
	CacheOutcomes   map[string][]string  `json:"cache_outcomes"`
	ErrorTypes      []string             `json:"error_types"`
	ErrorCodes      []string             `json:"error_codes"`
	Spans           SpansConfig          `json:"spans"`
	Attributes      map[string]Attribute `json:"attributes"`
	Metrics         map[string]Metric    `json:"metrics"`
	Mappings        map[string]Mapping   `json:"mappings"`
	Migration       MigrationMetadata    `json:"migration"`
}

type ScopeConfig struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type SpansConfig struct {
	CommandPrefix string `json:"command_prefix"`
	StoragePrefix string `json:"storage_prefix"`
}

type Attribute struct {
	Name             string `json:"name"`
	Source           string `json:"source"`
	PrivacyClass     string `json:"privacy_class"`
	CardinalityBound int    `json:"cardinality_bound"`
	MetricEligible   bool   `json:"metric_eligible"`
}

type Metric struct {
	Name             string    `json:"name"`
	Type             string    `json:"type"`
	Unit             string    `json:"unit"`
	Description      string    `json:"description"`
	Source           string    `json:"source"`
	PrivacyClass     string    `json:"privacy_class"`
	CardinalityBound int       `json:"cardinality_bound"`
	Semconv          string    `json:"semconv"`
	Maturity         string    `json:"maturity"`
	Boundaries       []float64 `json:"boundaries,omitempty"`
}

type Mapping struct {
	Operations map[string]string `json:"operations"`
	Outcomes   map[string]string `json:"outcomes"`
}

type MigrationMetadata struct {
	Status string `json:"status"`
	Since  string `json:"since"`
	Policy string `json:"policy"`
}

func main() {
	registryPath := flag.String("registry", "internal/otelreg/registry.json", "path to registry.json")
	outputPath := flag.String("out", "otel/schema_gen.go", "path to output schema_gen.go")
	check := flag.Bool("check", false, "check generated output without writing")
	scopeVersion := flag.String("scope-version", "", "override instrumentation scope version")
	writeScopeVersion := flag.String("write-scope-version", "", "update registry scope version before generating")
	flag.Parse()

	reg, err := readRegistry(*registryPath)
	if err != nil {
		fail(err)
	}
	if *scopeVersion != "" {
		reg.Scope.Version = *scopeVersion
	}
	if *writeScopeVersion != "" {
		if *check {
			fail(errors.New("-write-scope-version cannot be combined with -check"))
		}
		reg.Scope.Version = *writeScopeVersion
		if err := writeRegistry(*registryPath, reg); err != nil {
			fail(err)
		}
	}
	if err := validate(reg); err != nil {
		fail(err)
	}
	output, err := render(reg)
	if err != nil {
		fail(err)
	}

	if *check {
		if err := checkGeneratedOutput(*outputPath, output); err != nil {
			fail(err)
		}
		return
	}

	if err := os.MkdirAll(filepath.Dir(*outputPath), 0755); err != nil {
		fail(fmt.Errorf("create output directory: %w", err))
	}
	if err := os.WriteFile(*outputPath, output, 0644); err != nil {
		fail(fmt.Errorf("write generated output: %w", err))
	}
}

func writeRegistry(path string, registry Registry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

func checkGeneratedOutput(path string, expected []byte) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated output: %w", err)
	}
	if !bytes.Equal(current, expected) {
		return fmt.Errorf("generated output is stale: %s", path)
	}
	return nil
}

func readRegistry(path string) (Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, fmt.Errorf("read registry: %w", err)
	}
	var reg Registry
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reg); err != nil {
		return Registry{}, fmt.Errorf("parse registry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Registry{}, errors.New("parse registry: multiple JSON documents")
		}
		return Registry{}, fmt.Errorf("parse registry trailer: %w", err)
	}
	return reg, nil
}

func validate(reg Registry) error {
	if reg.ContractVersion == "" || reg.Scope.Name == "" || reg.Scope.Version == "" {
		return errors.New("registry: contract version, scope name and scope version are required")
	}
	if reg.Migration.Status == "" || reg.Migration.Since == "" || reg.Migration.Policy == "" {
		return errors.New("registry: migration metadata is required")
	}
	if len(reg.Components) == 0 || len(reg.Operations) == 0 {
		return errors.New("registry: components and operations are required")
	}
	if err := validateList("components", reg.Components); err != nil {
		return err
	}
	if err := validateList("outcomes", reg.Outcomes); err != nil {
		return err
	}
	if err := validateList("error_types", reg.ErrorTypes); err != nil {
		return err
	}
	if err := validateList("error_codes", reg.ErrorCodes); err != nil {
		return err
	}
	componentSet := set(reg.Components)
	for component, operations := range reg.Operations {
		if !componentSet[component] {
			return fmt.Errorf("registry: operations has undeclared component %q", component)
		}
		if err := validateList("operations."+component, operations); err != nil {
			return err
		}
	}
	for _, component := range reg.Components {
		if len(reg.Operations[component]) == 0 {
			return fmt.Errorf("registry: operations for %q are required", component)
		}
	}
	for component, outcomes := range reg.CacheOutcomes {
		if component != "cache" && component != "cache_backend" {
			return fmt.Errorf("registry: cache_outcomes has invalid component %q", component)
		}
		if err := validateList("cache_outcomes."+component, outcomes); err != nil {
			return err
		}
	}
	for component, mapping := range reg.Mappings {
		if !componentSet[component] {
			return fmt.Errorf("registry: mappings has undeclared component %q", component)
		}
		if err := validateMapping(component, mapping.Operations, reg.Operations[component]); err != nil {
			return err
		}
		allowedOutcomes := reg.Outcomes
		if values, ok := reg.CacheOutcomes[component]; ok {
			allowedOutcomes = values
		}
		if err := validateMapping(component, mapping.Outcomes, allowedOutcomes); err != nil {
			return err
		}
	}
	for _, component := range []string{"cache", "cache_backend"} {
		mapping, ok := reg.Mappings[component]
		if !ok {
			return fmt.Errorf("registry: mapping for %q is required", component)
		}
		if err := validateMapping(component, mapping.Operations, reg.Operations[component]); err != nil {
			return err
		}
		if err := validateMapping(component, mapping.Outcomes, reg.CacheOutcomes[component]); err != nil {
			return err
		}
	}
	for key, attribute := range reg.Attributes {
		if key == "" || !validToken(key) || attribute.Name == "" || attribute.Source == "" || attribute.PrivacyClass == "" || attribute.CardinalityBound <= 0 {
			return fmt.Errorf("registry: attribute %q has incomplete metadata", key)
		}
	}
	resource, ok := reg.Attributes["resource_name"]
	if !ok || resource.CardinalityBound <= 0 {
		return errors.New("registry: resource_name attribute metadata is required")
	}
	for key, metric := range reg.Metrics {
		if key == "" || !validToken(key) || metric.Name == "" || metric.Type == "" || metric.Unit == "" || metric.Description == "" || metric.Source == "" || metric.PrivacyClass == "" || metric.CardinalityBound <= 0 || metric.Semconv == "" || metric.Maturity == "" {
			return fmt.Errorf("registry: metric %q has incomplete metadata", key)
		}
		for i, boundary := range metric.Boundaries {
			if boundary <= 0 || (i > 0 && metric.Boundaries[i-1] >= boundary) {
				return fmt.Errorf("registry: metric %q boundaries must be positive and strictly increasing", key)
			}
		}
	}
	if reg.Spans.CommandPrefix == "" || reg.Spans.StoragePrefix == "" {
		return errors.New("registry: span prefixes are required")
	}
	if err := validateGeneratedIdentifiers(reg); err != nil {
		return err
	}
	return nil
}

func validateList(name string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("registry: %s is empty", name)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || !validToken(value) {
			return fmt.Errorf("registry: %s contains an empty value", name)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("registry: %s contains duplicate %q", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateMapping(component string, mapping map[string]string, source []string) error {
	if len(mapping) != len(source) {
		return fmt.Errorf("registry: %s mapping is not total", component)
	}
	allowed := set(source)
	for key, value := range mapping {
		if !allowed[key] || value == "" || !set(source)[value] || !validToken(value) {
			return fmt.Errorf("registry: invalid %s mapping entry %q=%q", component, key, value)
		}
	}
	return nil
}

func validToken(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for i := 1; i < len(value); i++ {
		if (value[i] < 'a' || value[i] > 'z') && (value[i] < '0' || value[i] > '9') && value[i] != '_' {
			return false
		}
	}
	return true
}

func validateGeneratedIdentifiers(reg Registry) error {
	seen := map[string]string{}
	add := func(name, source string) error {
		if previous, ok := seen[name]; ok {
			return fmt.Errorf("registry: generated identifier %q conflicts between %s and %s", name, previous, source)
		}
		seen[name] = source
		return nil
	}
	for key := range reg.Attributes {
		if err := add(toCamelCase("Attr_"+key), "attribute "+key); err != nil {
			return err
		}
	}
	for _, component := range reg.Components {
		if err := add(toCamelCase("Component_"+component), "component "+component); err != nil {
			return err
		}
	}
	for component, operations := range reg.Operations {
		for _, operation := range operations {
			if err := add(toCamelCase(fmt.Sprintf("Op_%s_%s", component, operation)), "operation "+component+"/"+operation); err != nil {
				return err
			}
		}
	}
	for _, outcome := range reg.Outcomes {
		if err := add(toCamelCase("Outcome_"+outcome), "outcome "+outcome); err != nil {
			return err
		}
	}
	for _, errType := range reg.ErrorTypes {
		if err := add(toCamelCase("ErrorType_"+errType), "error type "+errType); err != nil {
			return err
		}
	}
	for _, code := range reg.ErrorCodes {
		if err := add(toCamelCase("ErrorCode_"+code), "error code "+code); err != nil {
			return err
		}
	}
	for key := range reg.Metrics {
		name := toCamelCase("Metric_" + key)
		if err := add(name, "metric "+key); err != nil {
			return err
		}
		if err := add(name+"Description", "metric description "+key); err != nil {
			return err
		}
		if err := add(name+"Unit", "metric unit "+key); err != nil {
			return err
		}
	}
	return nil
}

func render(reg Registry) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("// Code generated by vv-otel-gen. DO NOT EDIT.\n\n")
	buf.WriteString("package vvotel\n\n")
	buf.WriteString("import \"go.opentelemetry.io/otel/attribute\"\n\n")

	buf.WriteString("const (\n")
	fmt.Fprintf(&buf, "\tScopeName = %q\n", reg.Scope.Name)
	fmt.Fprintf(&buf, "\tScopeVersion = %q\n", reg.Scope.Version)
	fmt.Fprintf(&buf, "\tContractVersion = %q\n", reg.ContractVersion)
	buf.WriteString(")\n\n")
	fmt.Fprintf(&buf, "const (\n\tMigrationStatus = %q\n\tMigrationSince = %q\n\tMigrationPolicy = %q\n)\n\n", reg.Migration.Status, reg.Migration.Since, reg.Migration.Policy)

	buf.WriteString("type AttributeMetadata struct {\n\tName attribute.Key\n\tSource string\n\tPrivacyClass string\n\tCardinalityBound int\n\tMetricEligible bool\n}\n\n")
	buf.WriteString("var AttributeMetadataByKey = map[string]AttributeMetadata{\n")
	for _, key := range sortedKeys(reg.Attributes) {
		attribute := reg.Attributes[key]
		fmt.Fprintf(&buf, "\t%q: {Name: %s, Source: %q, PrivacyClass: %q, CardinalityBound: %d, MetricEligible: %t},\n", key, toCamelCase("Attr_"+key), attribute.Source, attribute.PrivacyClass, attribute.CardinalityBound, attribute.MetricEligible)
	}
	buf.WriteString("}\n\n")
	buf.WriteString("type MetricMetadata struct {\n\tName string\n\tType string\n\tUnit string\n\tDescription string\n\tSource string\n\tPrivacyClass string\n\tCardinalityBound int\n\tSemconv string\n\tMaturity string\n}\n\n")
	buf.WriteString("var MetricMetadataByKey = map[string]MetricMetadata{\n")
	for _, key := range sortedKeys(reg.Metrics) {
		metric := reg.Metrics[key]
		fmt.Fprintf(&buf, "\t%q: {Name: %q, Type: %q, Unit: %q, Description: %q, Source: %q, PrivacyClass: %q, CardinalityBound: %d, Semconv: %q, Maturity: %q},\n", key, metric.Name, metric.Type, metric.Unit, metric.Description, metric.Source, metric.PrivacyClass, metric.CardinalityBound, metric.Semconv, metric.Maturity)
	}
	buf.WriteString("}\n\n")

	keys := sortedKeys(reg.Attributes)
	buf.WriteString("const (\n")
	for _, key := range keys {
		fmt.Fprintf(&buf, "\t%s = attribute.Key(%q)\n", toCamelCase("Attr_"+key), reg.Attributes[key].Name)
	}
	buf.WriteString(")\n\n")
	if resource, ok := reg.Attributes["resource_name"]; ok {
		fmt.Fprintf(&buf, "const MaxResourceNameValues = %d\n\n", resource.CardinalityBound)
	}

	buf.WriteString("const (\n")
	for _, component := range reg.Components {
		fmt.Fprintf(&buf, "\t%s = %q\n", toCamelCase("Component_"+component), component)
	}
	buf.WriteString(")\n\n")

	buf.WriteString("const (\n")
	for _, component := range sortedKeys(reg.Operations) {
		for _, operation := range reg.Operations[component] {
			fmt.Fprintf(&buf, "\t%s = %q\n", toCamelCase(fmt.Sprintf("Op_%s_%s", component, operation)), operation)
		}
	}
	buf.WriteString(")\n\n")

	buf.WriteString("const (\n")
	for _, outcome := range reg.Outcomes {
		fmt.Fprintf(&buf, "\t%s = %q\n", toCamelCase("Outcome_"+outcome), outcome)
	}
	buf.WriteString(")\n\n")

	buf.WriteString("const (\n")
	for _, errType := range reg.ErrorTypes {
		fmt.Fprintf(&buf, "\t%s = %q\n", toCamelCase("ErrorType_"+errType), errType)
	}
	buf.WriteString(")\n\n")

	buf.WriteString("const (\n")
	for _, code := range reg.ErrorCodes {
		fmt.Fprintf(&buf, "\t%s = %q\n", toCamelCase("ErrorCode_"+code), code)
	}
	buf.WriteString(")\n\n")

	buf.WriteString("const (\n")
	for _, key := range sortedKeys(reg.Metrics) {
		metric := reg.Metrics[key]
		name := toCamelCase("Metric_" + key)
		fmt.Fprintf(&buf, "\t%s = %q\n", name, metric.Name)
		fmt.Fprintf(&buf, "\t%sDescription = %q\n", name, metric.Description)
		fmt.Fprintf(&buf, "\t%sUnit = %q\n", name, metric.Unit)
	}
	buf.WriteString(")\n\n")

	for _, key := range sortedKeys(reg.Metrics) {
		metric := reg.Metrics[key]
		if len(metric.Boundaries) == 0 {
			continue
		}
		buf.WriteString("var defaultDurationBoundaries = []float64{\n")
		for _, boundary := range metric.Boundaries {
			fmt.Fprintf(&buf, "\t%s,\n", strconv.FormatFloat(boundary, 'f', -1, 64))
		}
		buf.WriteString("}\n\n")
		break
	}

	for _, component := range []string{"cache", "cache_backend"} {
		mapping := reg.Mappings[component]
		functionPrefix := toCamelCase(component)
		fmt.Fprintf(&buf, "func %sOperationName(value string) (string, bool) {\n\t switch value {\n", functionPrefix)
		for _, key := range sortedKeys(mapping.Operations) {
			fmt.Fprintf(&buf, "\tcase %q:\n\t\treturn %q, true\n", key, mapping.Operations[key])
		}
		buf.WriteString("\tdefault:\n\t\treturn \"\", false\n\t}\n}\n\n")
		fmt.Fprintf(&buf, "func %sOutcomeName(value string) (string, bool) {\n\t switch value {\n", functionPrefix)
		for _, key := range sortedKeys(mapping.Outcomes) {
			fmt.Fprintf(&buf, "\tcase %q:\n\t\treturn %q, true\n", key, mapping.Outcomes[key])
		}
		buf.WriteString("\tdefault:\n\t\treturn \"\", false\n\t}\n}\n\n")
	}

	buf.WriteString("func AllowedErrorCode(value string) (string, bool) {\n\t switch value {\n")
	for _, code := range reg.ErrorCodes {
		fmt.Fprintf(&buf, "\tcase %q:\n\t\treturn %q, true\n", code, code)
	}
	buf.WriteString("\tdefault:\n\t\treturn \"\", false\n\t}\n}\n\n")

	fmt.Fprintf(&buf, "func CommandSpanName(op string) string {\n\treturn %q + \" \" + op\n}\n\n", reg.Spans.CommandPrefix)
	fmt.Fprintf(&buf, "func StorageSpanName(op string) string {\n\treturn %q + \" \" + op\n}\n", reg.Spans.StoragePrefix)

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated code: %w", err)
	}
	return formatted, nil
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func set(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func toCamelCase(value string) string {
	parts := strings.Split(value, "_")
	var result strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		result.WriteString(strings.ToUpper(part[:1]))
		result.WriteString(part[1:])
	}
	return result.String()
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "vv-otel-gen: %v\n", err)
	os.Exit(1)
}

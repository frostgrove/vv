package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/internal/otelreg"
)

const expansionLimit = 200000

type Registry struct {
	ContractVersion string                        `json:"contract_version"`
	Scope           ScopeConfig                   `json:"scope"`
	Domains         map[string]Domain             `json:"domains"`
	Attributes      map[string]Attribute          `json:"attributes"`
	Components      map[string]Component          `json:"components"`
	AttributeSets   map[string]map[string]Binding `json:"attribute_sets"`
	Signals         map[string]Signal             `json:"signals"`
	Migration       MigrationMetadata             `json:"migration"`
	Exports         Exports                       `json:"exports"`
	LogCorrelation  LogCorrelation                `json:"log_correlation"`
	RetiredSignals  []string                      `json:"retired_signals,omitempty"`
	GlobalDomains   []string                      `json:"global_domains"`
	SourceShapes    map[string]SourceShape        `json:"source_shapes"`
	SourceFacts     map[string]SourceFact         `json:"source_facts"`
}
type LogCorrelation struct {
	Provider  string            `json:"provider"`
	Selection string            `json:"selection"`
	Collision string            `json:"collision"`
	Keys      map[string]LogKey `json:"keys"`
}
type LogKey struct {
	Name   string `json:"name"`
	GoName string `json:"go_name"`
	Format string `json:"format"`
	Length int    `json:"length"`
}
type ScopeConfig struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type Domain struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
}
type Attribute struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Source         string   `json:"source"`
	PrivacyClass   string   `json:"privacy_class"`
	MetricEligible bool     `json:"metric_eligible"`
	Maturity       string   `json:"maturity"`
	MaxValues      int      `json:"max_values,omitempty"`
	MaxBytes       int      `json:"max_bytes,omitempty"`
	Charset        string   `json:"charset,omitempty"`
	Domains        []string `json:"domains,omitempty"`
	Owner          string   `json:"owner"`
	Vocabulary     string   `json:"vocabulary,omitempty"`
}
type Component struct {
	SourceCoverage  string                `json:"source_coverage"`
	SourceRationale string                `json:"source_rationale"`
	WireValue       string                `json:"wire_value"`
	Source          string                `json:"source"`
	Maturity        string                `json:"maturity"`
	SpanNameDomains []string              `json:"span_name_domains,omitempty"`
	Operations      Vocabulary            `json:"operations"`
	Outcomes        Vocabulary            `json:"outcomes"`
	Vocabularies    map[string]Vocabulary `json:"vocabularies"`
}
type Vocabulary struct {
	Source   Inventory      `json:"source"`
	Domain   string         `json:"domain"`
	Mapping  []MappingEntry `json:"mapping"`
	Unknown  string         `json:"unknown"`
	Fallback string         `json:"fallback,omitempty"`
	GoHelper string         `json:"go_helper,omitempty"`
	GoPrefix string         `json:"go_prefix,omitempty"`
}
type Inventory struct {
	Kind       string            `json:"kind"`
	File       string            `json:"file,omitempty"`
	Type       string            `json:"type,omitempty"`
	Symbol     string            `json:"symbol"`
	Members    []SourceMember    `json:"members"`
	Excluded   []SourceExclusion `json:"excluded,omitempty"`
	Interfaces []InterfaceRef    `json:"interfaces,omitempty"`
}
type SourceExclusion struct {
	Value  string `json:"value"`
	Reason string `json:"reason"`
}
type InterfaceRef struct {
	File string `json:"file"`
	Type string `json:"type"`
}
type SignalHistory struct {
	Format   string         `json:"format"`
	Assigned map[string]int `json:"assigned"`
}
type SignalAvailabilityHistory struct {
	Format  string              `json:"format"`
	Signals map[string][]string `json:"signals"`
}
type SourceMember struct {
	Symbol string `json:"symbol"`
	Value  string `json:"value"`
}
type MappingEntry struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type Binding struct {
	Const    string   `json:"const,omitempty"`
	Domain   string   `json:"domain,omitempty"`
	Values   []string `json:"values,omitempty"`
	Declared bool     `json:"declared,omitempty"`
	Optional bool     `json:"optional,omitempty"`
}
type Variant struct {
	Base       string             `json:"base,omitempty"`
	Attributes map[string]Binding `json:"attributes"`
	Absent     []string           `json:"absent,omitempty"`
	Status     string             `json:"status,omitempty"`
	When       []SourcePredicate  `json:"when,omitempty"`
}

type NumericBound string

func (b *NumericBound) UnmarshalJSON(data []byte) error {
	number, err := parseJSONNumber(data)
	if err != nil {
		return err
	}
	*b = NumericBound(number)
	return nil
}

func (b NumericBound) MarshalJSON() ([]byte, error) {
	number, err := parseJSONNumber([]byte(b))
	if err != nil {
		return nil, err
	}
	return []byte(number), nil
}

func (b NumericBound) String() string {
	return string(b)
}

func parseJSONNumber(data []byte) (json.Number, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	number, ok := value.(json.Number)
	if !ok {
		return "", errors.New("bound must be a JSON number")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", errors.New("bound has trailing JSON data")
	}
	return number, nil
}

type Signal struct {
	Inputs           []string            `json:"inputs"`
	DeclaredSources  map[string][]string `json:"declared_sources,omitempty"`
	ValueSource      string              `json:"value_source,omitempty"`
	ComputedSource   string              `json:"computed_source,omitempty"`
	SignalID         int                 `json:"signal_id"`
	Provider         string              `json:"provider"`
	APIKind          string              `json:"api_kind,omitempty"`
	Kind             string              `json:"kind"`
	Name             string              `json:"name"`
	Component        string              `json:"component"`
	Instrument       string              `json:"instrument,omitempty"`
	NumberType       string              `json:"number_type,omitempty"`
	Unit             string              `json:"unit,omitempty"`
	Description      string              `json:"description"`
	Source           string              `json:"source"`
	PrivacyClass     string              `json:"privacy_class"`
	Maturity         string              `json:"maturity"`
	Semconv          string              `json:"semconv"`
	Availability     string              `json:"availability"`
	SpanKind         string              `json:"span_kind,omitempty"`
	NameDomain       string              `json:"name_domain,omitempty"`
	Boundaries       []float64           `json:"boundaries,omitempty"`
	Min              NumericBound        `json:"min,omitempty"`
	Max              NumericBound        `json:"max,omitempty"`
	SeriesBudget     int                 `json:"series_budget,omitempty"`
	RecordWhen       string              `json:"record_when,omitempty"`
	Variants         []Variant           `json:"variants"`
	GoName           string              `json:"go_name,omitempty"`
	BoundariesGoName string              `json:"boundaries_go_name,omitempty"`
}
type MigrationMetadata struct {
	From        string         `json:"from"`
	To          string         `json:"to"`
	Status      string         `json:"status"`
	Since       string         `json:"since"`
	Policy      string         `json:"policy"`
	Note        string         `json:"note"`
	WireChanges []string       `json:"wire_changes"`
	SignalIDs   map[string]int `json:"signal_ids"`
}
type Exports struct {
	Constants         map[string]string     `json:"constants"`
	SpanHelpers       map[string]SpanHelper `json:"span_helpers"`
	ErrorCodeDomain   string                `json:"error_code_domain"`
	DefaultHistogram  string                `json:"default_histogram"`
	ResourceAttribute string                `json:"resource_attribute"`
}
type SpanHelper struct {
	Signal string `json:"signal"`
}
type WireManifest struct {
	SourceFacts          map[string]SourceFact  `json:"source_facts"`
	SourceShapes         map[string]SourceShape `json:"source_shapes"`
	LogCorrelation       LogCorrelation         `json:"log_correlation"`
	Migration            MigrationMetadata      `json:"migration"`
	ContractVersion      string                 `json:"contract_version"`
	Scope                ScopeConfig            `json:"scope"`
	AdditionalAttributes bool                   `json:"additional_attributes"`
	Attributes           map[string]Attribute   `json:"attributes"`
	Signals              map[string]WireSignal  `json:"signals"`
}
type WireSignal struct {
	Signal
	Names            []string      `json:"names"`
	CardinalityBound int           `json:"cardinality_bound"`
	ResolvedVariants []WireVariant `json:"resolved_variants"`
}
type WireVariant struct {
	Attributes map[string]WireAttribute `json:"attributes"`
	Absent     []string                 `json:"absent"`
	Status     string                   `json:"status,omitempty"`
	When       []SourcePredicate        `json:"when,omitempty"`
}
type WireAttribute struct {
	Type      string   `json:"type"`
	Values    []string `json:"values,omitempty"`
	Optional  bool     `json:"optional"`
	Declared  bool     `json:"declared"`
	MaxValues int      `json:"max_values,omitempty"`
	MaxBytes  int      `json:"max_bytes,omitempty"`
	Charset   string   `json:"charset,omitempty"`
}

func main() {
	input := flag.String("registry", "internal/otelreg/registry.json", "registry input")
	output := flag.String("out", "otel/schema_gen.go", "generated Go output")
	manifest := flag.String("manifest", "otel/wire_manifest.json", "generated wire manifest")
	check := flag.Bool("check", false, "check both outputs without writing")
	version := flag.String("scope-version", "", "override scope version")
	writeVersion := flag.String("write-scope-version", "", "persist scope version")
	flag.Parse()
	if *check && *writeVersion != "" {
		fail(errors.New("-write-scope-version cannot be combined with -check"))
	}
	reg, err := readRegistry(*input)
	if err != nil {
		fail(err)
	}
	if *version != "" {
		reg.Scope.Version = *version
	}
	if *writeVersion != "" {
		reg.Scope.Version = *writeVersion
	}
	if err := validate(reg); err != nil {
		fail(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(*input)))
	if err := validateInventories(reg, root); err != nil {
		fail(err)
	}
	code, err := render(reg)
	if err != nil {
		fail(err)
	}
	wire, err := renderManifest(reg)
	if err != nil {
		fail(err)
	}
	if *check {
		if err := checkOutputs(*output, code, *manifest, wire); err != nil {
			fail(err)
		}
		return
	}
	if *writeVersion != "" {
		if err := writeRegistry(*input, reg); err != nil {
			fail(err)
		}
	}
	for path, data := range map[string][]byte{*output: code, *manifest: wire} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			fail(err)
		}
	}
}
func writeRegistry(path string, registry Registry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
func checkOutputs(goPath string, code []byte, manifestPath string, manifest []byte) error {
	return errors.Join(checkGeneratedOutput(goPath, code), checkGeneratedOutput(manifestPath, manifest))
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
		return Registry{}, err
	}
	if err := rejectDuplicateKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return Registry{}, fmt.Errorf("parse registry: %w", err)
	}
	var reg Registry
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reg); err != nil {
		return Registry{}, fmt.Errorf("parse registry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Registry{}, errors.New("parse registry: trailing JSON data")
	}
	return reg, nil
}
func rejectDuplicateKeys(decoder *json.Decoder) error {
	tok, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := tok.(json.Delim)
	if !compound {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid JSON key %q", name)
			}
			seen[name] = true
		}
		if err := rejectDuplicateKeys(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
func validate(reg Registry) error {
	return validateWithHistories(reg, otelreg.SignalHistory(), otelreg.AvailabilityHistory())
}
func validateWithSignalHistory(reg Registry, history []byte) error {
	return validateWithHistories(reg, history, otelreg.AvailabilityHistory())
}
func validateWithHistories(reg Registry, signalHistory, availabilityHistory []byte) error {
	if reg.ContractVersion != "vv-otel/v2" || reg.Scope.Name == "" || reg.Scope.Version == "" {
		return errors.New("registry: v2 contract and scope identity are required")
	}
	if err := validateMigrationMetadata(reg.Migration); err != nil {
		return err
	}
	if len(reg.Domains) == 0 || len(reg.Attributes) == 0 || len(reg.Components) == 0 || len(reg.Signals) == 0 {
		return errors.New("registry: domains, attributes, components and signals are required")
	}
	for id, domain := range reg.Domains {
		if !validID(id) || !oneOf(domain.Type, "string", "bool", "int64") || len(domain.Values) == 0 {
			return fmt.Errorf("registry: invalid domain %q", id)
		}
		seen := map[string]bool{}
		for _, value := range domain.Values {
			if seen[value] || !validValue(domain.Type, value) {
				return fmt.Errorf("registry: duplicate or invalid domain value %s/%q", id, value)
			}
			seen[value] = true
		}
	}
	if err := validateAttributes(reg.Attributes); err != nil {
		return err
	}
	for key, a := range reg.Attributes {
		seen := map[string]bool{}
		for _, name := range a.Domains {
			d, ok := reg.Domains[name]
			if !ok || d.Type != a.Type || seen[name] {
				return fmt.Errorf("attribute %s has invalid domain ownership", key)
			}
			seen[name] = true
		}
		if a.MaxValues > 0 && len(a.Domains) != 0 || a.MaxValues == 0 && a.PrivacyClass != "forbidden" && len(a.Domains) == 0 {
			return fmt.Errorf("attribute %s needs finite domain ownership or a declaration", key)
		}
	}
	wires := map[string]bool{}
	for id, component := range reg.Components {
		if !oneOf(component.SourceCoverage, "structured", "opaque_forwarding", "future_seam") || strings.TrimSpace(component.SourceRationale) == "" {
			return fmt.Errorf("component %s requires explicit source coverage", id)
		}
		if !validToken(id) || !validToken(component.WireValue) || wires[component.WireValue] || component.Source == "" || !validMaturity(component.Maturity) {
			return fmt.Errorf("registry: invalid or duplicate component %q", id)
		}
		wires[component.WireValue] = true
		if _, ok := component.Vocabularies["operations"]; ok {
			return errors.New("operations cannot be shadowed by a vocabulary")
		}
		if _, ok := component.Vocabularies["outcomes"]; ok {
			return errors.New("outcomes cannot be shadowed by a vocabulary")
		}
		for dimension, vocabulary := range vocabularies(component) {
			if !validToken(dimension) {
				return fmt.Errorf("registry: invalid dimension %q", dimension)
			}
			if err := validateVocabulary(reg, vocabulary); err != nil {
				return fmt.Errorf("registry: %s.%s: %w", id, dimension, err)
			}
		}
		nameDomains := map[string]bool{}
		for _, name := range component.SpanNameDomains {
			domain, ok := reg.Domains[name]
			if !ok || domain.Type != "string" || nameDomains[name] {
				return fmt.Errorf("component %s has an invalid span-name domain %s", id, name)
			}
			nameDomains[name] = true
		}
	}
	for id, attrs := range reg.AttributeSets {
		if !validID(id) || len(attrs) == 0 {
			return fmt.Errorf("registry: invalid attribute set %q", id)
		}
		if _, err := resolveVariant(reg, Variant{Attributes: attrs}, false); err != nil {
			return err
		}
	}
	if err := validateSignals(reg); err != nil {
		return err
	}
	if err := validateSignalIDs(reg, signalHistory); err != nil {
		return err
	}
	if err := validateAvailabilityHistory(reg, availabilityHistory); err != nil {
		return err
	}
	if err := validateShapeMappings(reg); err != nil {
		return err
	}
	if err := validateSourceFacts(reg); err != nil {
		return err
	}
	global := map[string]bool{}
	for _, name := range reg.GlobalDomains {
		if _, exists := reg.Domains[name]; !exists || global[name] {
			return errors.New("invalid or duplicate global domain")
		}
		global[name] = true
	}
	return validateExports(reg)
}
func validateMigrationMetadata(migration MigrationMetadata) error {
	if migration.From != "vv-otel/v1" || migration.To != "vv-otel/v2" || !oneOf(migration.Status, "development", "stable") || strings.TrimSpace(migration.Since) == "" || strings.TrimSpace(migration.Policy) == "" {
		return errors.New("registry: complete migration metadata is required")
	}
	if err := repositoryFile(migration.Note); err != nil || migration.Note != strings.TrimSpace(migration.Note) || filepath.Ext(migration.Note) != ".md" {
		return errors.New("registry: migration note must be a repository-relative Markdown file")
	}
	if len(migration.WireChanges) == 0 {
		return errors.New("registry: migration wire changes are required")
	}
	seen := map[string]bool{}
	for _, change := range migration.WireChanges {
		canonical := strings.TrimSpace(change)
		if canonical == "" || canonical != change || seen[canonical] {
			return errors.New("registry: migration wire changes must be nonempty and unique")
		}
		seen[canonical] = true
	}
	return nil
}
func validateAvailabilityHistory(reg Registry, data []byte) error {
	if err := rejectDuplicateKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	var history SignalAvailabilityHistory
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&history); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("availability history has trailing data")
	}
	if history.Format != "vv-otel-availability-history/v1" || len(history.Signals) == 0 || len(history.Signals) != len(reg.Migration.SignalIDs) {
		return errors.New("invalid availability history")
	}
	for key, states := range history.Signals {
		if !validToken(key) || reg.Migration.SignalIDs[key] == 0 || len(states) == 0 || len(states) > 2 {
			return fmt.Errorf("invalid availability history for signal %s", key)
		}
		if states[0] != "planned" && states[0] != "implemented" || len(states) == 2 && (states[0] != "planned" || states[1] != "implemented") {
			return fmt.Errorf("invalid availability transition for signal %s", key)
		}
		if signal, active := reg.Signals[key]; active && signal.Availability != states[len(states)-1] {
			return fmt.Errorf("signal %s availability changed outside its history", key)
		}
	}
	for key := range reg.Migration.SignalIDs {
		if len(history.Signals[key]) == 0 {
			return fmt.Errorf("migration signal %s is absent from availability history", key)
		}
	}
	return nil
}
func validateSignalIDs(reg Registry, history []byte) error {
	if err := validateSignalHistory(reg, history); err != nil {
		return err
	}
	seen := map[int]string{}
	for key, id := range reg.Migration.SignalIDs {
		if id < 1 || id > math.MaxUint16 {
			return fmt.Errorf("registry: signal ID for %s is outside uint16", key)
		}
		if prior, ok := seen[id]; ok {
			return fmt.Errorf("registry: signal ID %d reused by %s and %s", id, prior, key)
		}
		seen[id] = key
	}
	for key, s := range reg.Signals {
		if s.SignalID < 1 || s.SignalID > math.MaxUint16 || reg.Migration.SignalIDs[key] != s.SignalID {
			return fmt.Errorf("registry: signal %s must keep its explicit migration ID", key)
		}
		want := "tracer"
		if s.Kind == "metric" {
			want = "meter"
		}
		if s.Kind == "span_event" {
			want = "context_only"
		}
		if s.Provider != want {
			return fmt.Errorf("registry: signal %s requires provider %s", key, want)
		}
		if s.Kind == "metric" && s.APIKind != s.NumberType+"_"+s.Instrument || s.Kind != "metric" && s.APIKind != "" {
			return fmt.Errorf("registry: signal %s has an incompatible metric API kind", key)
		}
	}
	return nil
}
func validateSignalHistory(reg Registry, data []byte) error {
	if err := rejectDuplicateKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	var history SignalHistory
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&history); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("signal history has trailing data")
	}
	if history.Format != "vv-otel-signal-history/v1" || len(history.Assigned) == 0 || len(history.Assigned) != len(reg.Migration.SignalIDs) {
		return errors.New("invalid signal history")
	}
	ids := map[int]string{}
	for key, id := range history.Assigned {
		if !validToken(key) || id < 1 || id > math.MaxUint16 || ids[id] != "" {
			return errors.New("invalid or reused historical signal ID")
		}
		ids[id] = key
		if reg.Migration.SignalIDs[key] != id {
			return fmt.Errorf("historical signal %s must retain ID %d", key, id)
		}
	}
	for key, id := range reg.Migration.SignalIDs {
		if history.Assigned[key] != id {
			return fmt.Errorf("migration signal %s with ID %d is absent from history", key, id)
		}
	}
	retired := map[string]bool{}
	for _, key := range reg.RetiredSignals {
		if !validToken(key) || retired[key] || history.Assigned[key] == 0 {
			return errors.New("invalid or duplicate retired signal")
		}
		if _, active := reg.Signals[key]; active {
			return errors.New("retired signal is still active")
		}
		retired[key] = true
	}
	if len(reg.Signals)+len(retired) != len(history.Assigned) {
		return errors.New("active and retired signals do not partition signal history")
	}
	for key := range history.Assigned {
		if _, active := reg.Signals[key]; !active && !retired[key] {
			return fmt.Errorf("signal %s was removed without explicit retirement", key)
		}
	}
	return nil
}
func validateLogCorrelation(group LogCorrelation) error {
	if group.Provider != "context_only" || group.Selection != "explicit_handler" || group.Collision != "preserve_record" || len(group.Keys) != 3 {
		return errors.New("log correlation is an explicit context-only handler group")
	}
	for name, length := range map[string]int{"trace_id": 32, "span_id": 16, "trace_flags": 2} {
		key := group.Keys[name]
		if key.Name != name || key.Format != "lower_hex" || key.Length != length {
			return errors.New("log correlation requires exact W3C trace/span/flags keys and lower-hex widths")
		}
	}
	return nil
}
func validateAttributes(attributes map[string]Attribute) error {
	names := map[string]bool{}
	for id, a := range attributes {
		if !oneOf(a.Owner, "identity", "global", "component", "declaration") || a.Owner == "component" && a.Vocabulary == "" || a.Owner != "component" && a.Vocabulary != "" {
			return fmt.Errorf("attribute %s requires explicit semantic ownership", id)
		}
		if !validToken(id) || !validWireName(a.Name) || names[a.Name] || a.Source == "" || !oneOf(a.Type, "string", "bool", "int64") || !oneOf(a.PrivacyClass, "safe", "bounded", "forbidden") || !validMaturity(a.Maturity) {
			return fmt.Errorf("registry: invalid or duplicate attribute %q", id)
		}
		if a.MaxValues < 0 || a.MaxBytes < 0 || (a.MaxValues > 0) != (a.MaxBytes > 0) || a.MaxValues > 0 && a.PrivacyClass != "bounded" {
			return fmt.Errorf("registry: invalid declared bound for %q", id)
		}
		if a.MaxValues > 0 && a.Charset != "unicode_letter_digit_dot_underscore_hyphen" || a.MaxValues == 0 && a.Charset != "" {
			return fmt.Errorf("registry: invalid declaration charset for %q", id)
		}
		if a.MetricEligible && (a.PrivacyClass != "safe" || a.MaxValues != 0) {
			return fmt.Errorf("registry: unsafe metric attribute %q", id)
		}
		names[a.Name] = true
	}
	return nil
}
func vocabularies(c Component) map[string]Vocabulary {
	result := map[string]Vocabulary{"operations": c.Operations, "outcomes": c.Outcomes}
	for k, v := range c.Vocabularies {
		result[k] = v
	}
	return result
}
func validateVocabulary(reg Registry, v Vocabulary) error {
	s := v.Source
	if !oneOf(s.Kind, "enum", "methods", "functions", "registry", "projection") || s.Symbol == "" || !oneOf(v.Unknown, "drop", "omit", "fallback") {
		return errors.New("source inventory and explicit unknown policy are required")
	}
	if s.Kind == "enum" && (s.File == "" || s.Type == "") {
		return errors.New("enum inventory requires its source file and type")
	}
	if s.Kind == "methods" && len(s.Interfaces) == 0 || s.Kind != "methods" && len(s.Interfaces) != 0 || s.Kind == "functions" && s.File == "" {
		return errors.New("method inventories require interfaces; function inventories require a source file")
	}
	refs := map[InterfaceRef]bool{}
	for _, ref := range s.Interfaces {
		if ref.File == "" || !token.IsIdentifier(ref.Type) || refs[ref] {
			return errors.New("invalid or duplicate source interface")
		}
		refs[ref] = true
	}
	if len(s.Members) == 0 {
		if v.Domain != "" || len(v.Mapping) != 0 || v.Fallback != "" || len(s.Excluded) != 0 {
			return errors.New("an absent vocabulary has no domain, mapping or fallback")
		}
		return nil
	}
	domain, ok := reg.Domains[v.Domain]
	if !ok {
		return fmt.Errorf("unknown domain %q", v.Domain)
	}
	if v.Unknown == "fallback" && !contains(domain.Values, v.Fallback) || v.Unknown != "fallback" && v.Fallback != "" {
		return errors.New("fallback must belong to its domain and require fallback policy")
	}
	values, symbols := map[string]bool{}, map[string]bool{}
	for _, m := range s.Members {
		if m.Symbol == "" || m.Value == "" || values[m.Value] || symbols[m.Symbol] {
			return errors.New("duplicate or incomplete source inventory member")
		}
		values[m.Value], symbols[m.Symbol] = true, true
	}
	excluded := map[string]bool{}
	for _, exclusion := range s.Excluded {
		value := exclusion.Value
		if !values[value] || excluded[value] || strings.TrimSpace(exclusion.Reason) == "" {
			return errors.New("duplicate or unknown inventory exclusion")
		}
		excluded[value] = true
	}
	mapped := map[string]bool{}
	for _, entry := range v.Mapping {
		if !values[entry.From] || excluded[entry.From] || mapped[entry.From] || !contains(domain.Values, entry.To) {
			return fmt.Errorf("invalid or duplicate mapping %q -> %q", entry.From, entry.To)
		}
		mapped[entry.From] = true
	}
	if len(mapped)+len(excluded) != len(values) {
		return errors.New("mapping is not total over its independent source inventory")
	}
	return nil
}
func validateSignals(reg Registry) error {
	names := map[string]string{}
	usedNameDomains := map[string]bool{}
	for id, s := range reg.Signals {
		if !validToken(id) || !oneOf(s.Kind, "span", "span_event", "metric") || s.Description == "" || s.Source == "" || s.PrivacyClass != "safe" || !validMaturity(s.Maturity) || s.Semconv == "" || !oneOf(s.Availability, "implemented", "planned") || len(s.Variants) == 0 {
			return fmt.Errorf("registry: incomplete signal %q", id)
		}
		component, ok := reg.Components[s.Component]
		if !ok {
			return fmt.Errorf("registry: signal %q names unknown component", id)
		}
		if s.Kind == "span" {
			if s.NameDomain == "" || !contains(component.SpanNameDomains, s.NameDomain) {
				return fmt.Errorf("registry: span %q uses a name domain not owned by component %s", id, s.Component)
			}
			usedNameDomains[s.Component+"\x00"+s.NameDomain] = true
		}
		if err := validateSignalShape(s); err != nil {
			return fmt.Errorf("registry: signal %s: %w", id, err)
		}
		expanded, err := signalNames(reg, s)
		if err != nil {
			return err
		}
		for _, name := range expanded {
			key := s.Kind + "/" + name
			if prev, ok := names[key]; ok {
				return fmt.Errorf("registry: signal wire name %q conflicts between %s and %s", name, prev, id)
			}
			names[key] = id
		}
		for _, variant := range s.Variants {
			if err := validateVariantOwnership(reg, variant); err != nil {
				return fmt.Errorf("signal %s: %w", id, err)
			}
			if variant.Status != "" && (s.Kind != "span" || !oneOf(variant.Status, "unset", "error")) {
				return fmt.Errorf("registry: invalid span status on %q", id)
			}
			if _, err := resolveVariant(reg, variant, s.Kind == "metric"); err != nil {
				return fmt.Errorf("registry: %s: %w", id, err)
			}
		}
		if s.Kind == "metric" {
			bound, err := metricCardinality(reg, s)
			if err != nil {
				return fmt.Errorf("registry: %s: %w", id, err)
			}
			if s.SeriesBudget <= 0 || bound > s.SeriesBudget {
				return fmt.Errorf("registry: %s exceeds its series budget: %d", id, bound)
			}
		}
	}
	for componentID, component := range reg.Components {
		for _, domain := range component.SpanNameDomains {
			if !usedNameDomains[componentID+"\x00"+domain] {
				return fmt.Errorf("registry: component %s has unused span-name domain %s", componentID, domain)
			}
		}
	}
	return nil
}
func validateSignalShape(s Signal) error {
	if !validWireName(s.Name) {
		return errors.New("invalid wire name")
	}
	if s.Kind != "metric" {
		if s.Instrument != "" || s.NumberType != "" || s.Unit != "" || len(s.Boundaries) != 0 || s.Min != "" || s.Max != "" || s.SeriesBudget != 0 || s.BoundariesGoName != "" || s.RecordWhen != "" {
			return errors.New("non-metric signal carries instrument metadata")
		}
		if s.Kind == "span" && !oneOf(s.SpanKind, "internal", "producer", "consumer") || s.Kind != "span" && s.SpanKind != "" {
			return errors.New("invalid span kind")
		}
		return nil
	}
	if !oneOf(s.RecordWhen, "non_negative", "positive") {
		return errors.New("metric measurement admission is required")
	}
	if s.SpanKind != "" || s.NameDomain != "" || !oneOf(s.Instrument, "counter", "histogram", "gauge", "up_down_counter", "observable_gauge", "observable_counter", "observable_up_down_counter") || !oneOf(s.NumberType, "int64", "float64") || !validUnit(s.Unit) {
		return errors.New("unsupported instrument, number type or unit")
	}
	if s.Unit == "s" && s.NumberType != "float64" || (s.Unit == "By" || strings.HasPrefix(s.Unit, "{")) && s.NumberType != "int64" {
		return errors.New("unit and number type are incompatible")
	}
	bounds, err := parseMetricBounds(s)
	if err != nil {
		return err
	}
	if s.Instrument != "histogram" && len(s.Boundaries) > 0 {
		return errors.New("only histograms accept boundaries")
	}
	if s.Instrument == "histogram" && len(s.Boundaries) == 0 {
		return errors.New("histogram boundaries are required")
	}
	for i, b := range s.Boundaries {
		if math.IsNaN(b) || math.IsInf(b, 0) || i > 0 && b <= s.Boundaries[i-1] || !bounds.acceptsBoundary(b) {
			return errors.New("histogram boundaries must be finite, increasing and within measurement bounds")
		}
	}
	return nil
}

type metricBounds struct {
	hasMinimum   bool
	hasMaximum   bool
	minimumInt64 int64
	maximumInt64 int64
	minimumFloat float64
	maximumFloat float64
	numberType   string
}

func parseMetricBounds(signal Signal) (metricBounds, error) {
	result := metricBounds{hasMinimum: signal.Min != "", hasMaximum: signal.Max != "", numberType: signal.NumberType}
	if signal.NumberType == "int64" {
		if signal.Min != "" {
			value, err := signal.Min.int64Value()
			if err != nil {
				return metricBounds{}, errors.New("int64 measurement minimum must be integral and in range")
			}
			result.minimumInt64 = value
			result.minimumFloat = float64(value)
		}
		if signal.Max != "" {
			value, err := signal.Max.int64Value()
			if err != nil {
				return metricBounds{}, errors.New("int64 measurement maximum must be integral and in range")
			}
			result.maximumInt64 = value
			result.maximumFloat = float64(value)
		}
		if result.hasMinimum && result.hasMaximum && result.maximumInt64 < result.minimumInt64 {
			return metricBounds{}, errors.New("measurement maximum must not be below minimum")
		}
		return result, nil
	}
	if signal.Min != "" {
		value, err := signal.Min.float64Value()
		if err != nil {
			return metricBounds{}, errors.New("float64 measurement minimum must be finite and in range")
		}
		result.minimumFloat = value
	}
	if signal.Max != "" {
		value, err := signal.Max.float64Value()
		if err != nil {
			return metricBounds{}, errors.New("float64 measurement maximum must be finite and in range")
		}
		result.maximumFloat = value
	}
	if result.hasMinimum && result.hasMaximum && result.maximumFloat < result.minimumFloat {
		return metricBounds{}, errors.New("measurement maximum must not be below minimum")
	}
	return result, nil
}

func (b NumericBound) int64Value() (int64, error) {
	number, err := parseJSONNumber([]byte(b))
	if err != nil {
		return 0, err
	}
	value, ok := new(big.Rat).SetString(number.String())
	if !ok || !value.IsInt() || !value.Num().IsInt64() {
		return 0, errors.New("not an int64")
	}
	return value.Num().Int64(), nil
}

func (b NumericBound) float64Value() (float64, error) {
	number, err := parseJSONNumber([]byte(b))
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("not a finite float64")
	}
	return value, nil
}

func (b metricBounds) acceptsBoundary(value float64) bool {
	if b.numberType == "int64" {
		boundary := new(big.Rat).SetFloat64(value)
		return (!b.hasMinimum || boundary.Cmp(new(big.Rat).SetInt64(b.minimumInt64)) >= 0) &&
			(!b.hasMaximum || boundary.Cmp(new(big.Rat).SetInt64(b.maximumInt64)) <= 0)
	}
	return (!b.hasMinimum || value >= b.minimumFloat) && (!b.hasMaximum || value <= b.maximumFloat)
}
func resolveVariant(reg Registry, v Variant, metric bool) (WireVariant, error) {
	bindings := map[string]Binding{}
	if v.Base != "" {
		base, ok := reg.AttributeSets[v.Base]
		if !ok {
			return WireVariant{}, fmt.Errorf("unknown attribute set %q", v.Base)
		}
		for k, b := range base {
			bindings[k] = b
		}
	}
	for k, b := range v.Attributes {
		if _, exists := bindings[k]; exists {
			return WireVariant{}, fmt.Errorf("attribute %q overrides its base", k)
		}
		bindings[k] = b
	}
	result := WireVariant{Attributes: map[string]WireAttribute{}, Absent: []string{}, Status: v.Status, When: v.When}
	for k, b := range bindings {
		a, ok := reg.Attributes[k]
		if !ok || a.PrivacyClass == "forbidden" || metric && (!a.MetricEligible || b.Declared) {
			return WireVariant{}, fmt.Errorf("unknown or forbidden attribute %q", k)
		}
		if b.Declared && (b.Const != "" || b.Domain != "" || len(b.Values) != 0) || !b.Declared && b.Domain == "" || b.Const != "" && len(b.Values) != 0 {
			return WireVariant{}, fmt.Errorf("attribute %q needs an owned domain or a declaration", k)
		}
		w := WireAttribute{Type: a.Type, Optional: b.Optional, Declared: b.Declared}
		switch {
		case b.Declared:
			if a.PrivacyClass != "bounded" || a.MaxValues <= 0 || a.MaxBytes <= 0 {
				return WireVariant{}, fmt.Errorf("unbounded declaration for %q", k)
			}
			w.MaxValues, w.MaxBytes, w.Charset = a.MaxValues, a.MaxBytes, a.Charset
		case b.Domain != "":
			domain, ok := reg.Domains[b.Domain]
			if !ok || domain.Type != a.Type || !contains(a.Domains, b.Domain) {
				return WireVariant{}, fmt.Errorf("attribute %q has unknown or incompatible domain", k)
			}
			w.Values = append([]string(nil), domain.Values...)
			if len(b.Values) > 0 {
				w.Values = nil
				for _, value := range b.Values {
					if !contains(domain.Values, value) || contains(w.Values, value) {
						return WireVariant{}, fmt.Errorf("invalid or repeated subset value for %s", k)
					}
					w.Values = append(w.Values, value)
				}
			}
			if b.Const != "" {
				if !contains(domain.Values, b.Const) {
					return WireVariant{}, fmt.Errorf("constant for %q is outside its owned domain", k)
				}
				w.Values = []string{b.Const}
			}
		}
		sort.Strings(w.Values)
		result.Attributes[a.Name] = w
	}
	for _, k := range v.Absent {
		a, ok := reg.Attributes[k]
		_, bound := bindings[k]
		if !ok || bound || contains(result.Absent, a.Name) {
			return WireVariant{}, fmt.Errorf("invalid or duplicate omission %q", k)
		}
		result.Absent = append(result.Absent, a.Name)
	}
	sort.Strings(result.Absent)
	return result, nil
}
func metricCardinality(reg Registry, s Signal) (int, error) {
	seen := map[string]bool{}
	for _, v := range s.Variants {
		w, err := resolveVariant(reg, v, true)
		if err != nil {
			return 0, err
		}
		rows := []string{""}
		for _, key := range sortedKeys(w.Attributes) {
			a := w.Attributes[key]
			width := len(a.Values)
			if a.Optional {
				width++
			}
			if width == 0 || len(rows) > expansionLimit/width {
				return 0, errors.New("metric cardinality expansion exceeds the generation limit")
			}
			next := make([]string, 0, len(rows)*width)
			for _, row := range rows {
				if a.Optional {
					next = append(next, row)
				}
				for _, value := range a.Values {
					next = append(next, row+strconv.Quote(key)+":"+a.Type+":"+strconv.Quote(value)+";")
				}
			}
			rows = next
		}
		for _, row := range rows {
			seen[row] = true
			if len(seen) > expansionLimit {
				return 0, errors.New("metric cardinality union exceeds the generation limit")
			}
		}
	}
	return len(seen), nil
}
func signalNames(reg Registry, s Signal) ([]string, error) {
	if s.NameDomain == "" {
		return []string{s.Name}, nil
	}
	d, ok := reg.Domains[s.NameDomain]
	if !ok || d.Type != "string" || s.Kind != "span" {
		return nil, errors.New("name domain must be a finite span string domain")
	}
	names := make([]string, 0, len(d.Values))
	for _, v := range d.Values {
		if !validToken(v) {
			return nil, errors.New("span name domain contains an invalid operation")
		}
		names = append(names, s.Name+" "+v)
	}
	sort.Strings(names)
	return names, nil
}
func validateInventories(reg Registry, root string) error {
	if err := validateMigrationNote(reg.Migration.Note, root); err != nil {
		return err
	}
	for c, component := range reg.Components {
		for dimension, v := range vocabularies(component) {
			if err := validateInventory(v.Source, root); err != nil {
				return fmt.Errorf("inventory %s.%s: %w", c, dimension, err)
			}
		}
	}
	return validateSourceShapes(reg, root)
}
func validateMigrationNote(note, root string) error {
	if err := repositoryFile(note); err != nil {
		return err
	}
	info, err := os.Stat(filepath.Join(root, note))
	if err != nil {
		return fmt.Errorf("migration note: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("migration note must be a regular file")
	}
	return nil
}
func validateInventory(s Inventory, root string) error {
	if s.Kind == "registry" || s.Kind == "projection" {
		return nil
	}
	actual, rendered := map[string]bool{}, map[string]string{}
	if s.Kind == "methods" {
		resolver := interfaceResolver{root: root}
		for _, ref := range s.Interfaces {
			if err := repositoryFile(ref.File); err != nil {
				return err
			}
			methods, err := resolver.methods(filepath.Join(root, ref.File), ref.Type, map[string]bool{})
			if err != nil {
				return err
			}
			for name := range methods {
				actual[name] = true
				rendered[name] = name
			}
		}
	} else {
		if err := repositoryFile(s.File); err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, s.File), nil, 0)
		if err != nil {
			return err
		}
		if s.Kind == "enum" {
			actual, rendered = enumSymbols(file, s.Type), enumRenderedValues(file, s.Type)
		} else if s.Kind == "functions" {
			for _, declaration := range file.Decls {
				fn, ok := declaration.(*ast.FuncDecl)
				if ok && fn.Recv == nil && ast.IsExported(fn.Name.Name) {
					actual[fn.Name.Name] = true
					rendered[fn.Name.Name] = fn.Name.Name
				}
			}
		} else {
			return errors.New("unknown inventory kind")
		}
	}
	if len(actual) != len(s.Members) {
		return fmt.Errorf("source has %d members, registry has %d", len(actual), len(s.Members))
	}
	for _, m := range s.Members {
		if !actual[m.Symbol] {
			return fmt.Errorf("unknown source member %q", m.Symbol)
		}
		if value, known := rendered[m.Symbol]; known && value != m.Value {
			return fmt.Errorf("source value of %s is %q, registry says %q", m.Symbol, value, m.Value)
		}
	}
	return nil
}
func repositoryFile(path string) error {
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
		return errors.New("inventory file must remain within the repository")
	}
	return nil
}
func enumRenderedValues(file *ast.File, wanted string) map[string]string {
	result := map[string]string{}
	constants := enumConstants(file)
	for name, constant := range constants {
		if constant.kind == wanted && constant.known {
			result[name] = constant.value
		}
	}
	for _, declaration := range file.Decls {
		method, ok := declaration.(*ast.FuncDecl)
		if !ok || method.Name.Name != "String" || method.Recv == nil || len(method.Recv.List) != 1 {
			continue
		}
		receiver := method.Recv.List[0].Type
		if pointer, ok := receiver.(*ast.StarExpr); ok {
			receiver = pointer.X
		}
		name, ok := receiver.(*ast.Ident)
		if !ok || name.Name != wanted {
			continue
		}
		ast.Inspect(method.Body, func(node ast.Node) bool {
			branch, ok := node.(*ast.CaseClause)
			if !ok || len(branch.Body) != 1 {
				return true
			}
			returned, ok := branch.Body[0].(*ast.ReturnStmt)
			if !ok || len(returned.Results) != 1 {
				return false
			}
			literal, ok := returned.Results[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return false
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return false
			}
			for _, expression := range branch.List {
				if symbol, ok := expression.(*ast.Ident); ok {
					result[symbol.Name] = value
				}
			}
			return false
		})
	}
	return result
}
func enumSymbols(file *ast.File, wanted string) map[string]bool {
	result := map[string]bool{}
	for name, constant := range enumConstants(file) {
		if constant.kind == wanted && ast.IsExported(name) {
			result[name] = true
		}
	}
	return result
}

type enumConstant struct {
	kind  string
	value string
	known bool
}

func enumConstants(file *ast.File) map[string]enumConstant {
	result := map[string]enumConstant{}
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		previousKinds := []string{}
		previousValues := []ast.Expr{}
		for _, raw := range group.Specs {
			spec := raw.(*ast.ValueSpec)
			values := spec.Values
			inherited := len(values) == 0
			if inherited {
				values = previousValues
			}
			kinds := make([]string, len(spec.Names))
			if spec.Type != nil {
				if name, ok := spec.Type.(*ast.Ident); ok {
					for i := range kinds {
						kinds[i] = name.Name
					}
				}
			} else if inherited {
				for i := range kinds {
					if len(previousKinds) == 1 {
						kinds[i] = previousKinds[0]
					} else if i < len(previousKinds) {
						kinds[i] = previousKinds[i]
					}
				}
			} else {
				for i := range kinds {
					expression := expressionAt(values, i)
					kinds[i] = enumExpressionKind(expression, result)
				}
			}
			for i, name := range spec.Names {
				value, known := enumExpressionValue(expressionAt(values, i), result)
				result[name.Name] = enumConstant{kind: kinds[i], value: value, known: known}
			}
			previousKinds = kinds
			previousValues = values
		}
	}
	return result
}
func expressionAt(expressions []ast.Expr, index int) ast.Expr {
	if index < len(expressions) {
		return expressions[index]
	}
	if len(expressions) == 1 {
		return expressions[0]
	}
	return nil
}
func enumExpressionKind(expression ast.Expr, constants map[string]enumConstant) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return constants[value.Name].kind
	case *ast.CallExpr:
		if name, ok := value.Fun.(*ast.Ident); ok {
			return name.Name
		}
	case *ast.ParenExpr:
		return enumExpressionKind(value.X, constants)
	case *ast.UnaryExpr:
		return enumExpressionKind(value.X, constants)
	case *ast.BinaryExpr:
		if kind := enumExpressionKind(value.X, constants); kind != "" {
			return kind
		}
		return enumExpressionKind(value.Y, constants)
	}
	return ""
}
func enumExpressionValue(expression ast.Expr, constants map[string]enumConstant) (string, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)
			return text, err == nil
		}
	case *ast.Ident:
		constant, ok := constants[value.Name]
		return constant.value, ok && constant.known
	case *ast.CallExpr:
		if len(value.Args) == 1 {
			return enumExpressionValue(value.Args[0], constants)
		}
	case *ast.ParenExpr:
		return enumExpressionValue(value.X, constants)
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			left, leftOK := enumExpressionValue(value.X, constants)
			right, rightOK := enumExpressionValue(value.Y, constants)
			return left + right, leftOK && rightOK
		}
	}
	return "", false
}
func validateExports(reg Registry) error {
	if err := validateLogCorrelation(reg.LogCorrelation); err != nil {
		return err
	}
	if d, ok := reg.Domains[reg.Exports.ErrorCodeDomain]; !ok || d.Type != "string" {
		return errors.New("error-code export needs a string domain")
	}
	if s, ok := reg.Signals[reg.Exports.DefaultHistogram]; !ok || s.Instrument != "histogram" {
		return errors.New("default histogram export needs a histogram")
	}
	if a, ok := reg.Attributes[reg.Exports.ResourceAttribute]; !ok || a.Type != "string" || a.MaxValues <= 0 || a.MaxBytes <= 0 || a.Charset != "unicode_letter_digit_dot_underscore_hyphen" {
		return errors.New("resource export needs a bounded declaration")
	}
	seen := map[string]bool{}
	add := func(name string) error {
		if !token.IsIdentifier(name) || !ast.IsExported(name) || seen[name] {
			return fmt.Errorf("invalid or duplicate generated identifier %q", name)
		}
		seen[name] = true
		return nil
	}
	for _, name := range []string{"ScopeName", "ScopeVersion", "ContractVersion", "MigrationFrom", "MigrationTo", "MigrationStatus", "MigrationSince", "MigrationPolicy", "MigrationNote", "MigrationWireChanges", "AttributeMetadata", "AttributeMetadataByKey", "MetricMetadata", "MetricMetadataByKey", "MaxResourceNameValues", "MaxResourceNameBytes", "ValidResourceName", "AllowedErrorCode", "SignalDescriptor", "SignalDescriptors", "SignalVariantDescriptor", "SignalAttributeDescriptor", "Signal", "Signals", "AllSignals", "SignalSourceFact", "SignalSourceValue", "SignalSourceFacts", "SignalSourcePredicate"} {
		if err := add(name); err != nil {
			return err
		}
	}
	for key := range reg.SourceFacts {
		if err := add(toCamelCase("SourceFact_" + key)); err != nil {
			return err
		}
	}
	for key := range reg.Attributes {
		if err := add(toCamelCase("Attr_" + key)); err != nil {
			return err
		}
	}
	for key, c := range reg.Components {
		if err := add(toCamelCase("Component_" + key)); err != nil {
			return err
		}
		for _, op := range reg.Domains[c.Operations.Domain].Values {
			if err := add(toCamelCase("Op_" + key + "_" + op)); err != nil {
				return err
			}
		}
		for _, v := range vocabularies(c) {
			if v.GoHelper != "" {
				if err := add(v.GoHelper); err != nil {
					return err
				}
			}
			if v.GoPrefix != "" {
				for _, value := range reg.Domains[v.Domain].Values {
					if err := add(toCamelCase(v.GoPrefix + "_" + value)); err != nil {
						return err
					}
				}
			}
		}
	}
	for name := range reg.Exports.Constants {
		if err := add(name); err != nil {
			return err
		}
	}
	for _, key := range reg.LogCorrelation.Keys {
		if err := add(key.GoName); err != nil {
			return err
		}
	}
	for name, h := range reg.Exports.SpanHelpers {
		if err := add(name); err != nil {
			return err
		}
		if s, ok := reg.Signals[h.Signal]; !ok || s.Kind != "span" || s.NameDomain == "" {
			return errors.New("span helper requires a named span domain")
		}
	}
	for key, s := range reg.Signals {
		if err := add(toCamelCase("Signal_" + key)); err != nil {
			return err
		}
		name := signalGoName(key, s)
		if err := add(name); err != nil {
			return err
		}
		if s.Kind == "metric" {
			for _, suffix := range []string{"Description", "Unit"} {
				if err := add(name + suffix); err != nil {
					return err
				}
			}
			if s.Instrument == "histogram" {
				if err := add(boundariesName(key, s)); err != nil {
					return err
				}
			}
		}
		if s.NameDomain != "" && len(reg.Domains[s.NameDomain].Values) > 1 {
			if err := add(name + "Name"); err != nil {
				return err
			}
		}
	}
	return nil
}
func buildManifest(reg Registry) (WireManifest, error) {
	result := WireManifest{ContractVersion: reg.ContractVersion, Scope: reg.Scope, Migration: cloneMigration(reg.Migration), Attributes: reg.Attributes, Signals: map[string]WireSignal{}, LogCorrelation: reg.LogCorrelation, SourceFacts: reg.SourceFacts, SourceShapes: reg.SourceShapes}
	for id, s := range reg.Signals {
		wire := WireSignal{Signal: s}
		var err error
		wire.Names, err = signalNames(reg, s)
		if err != nil {
			return WireManifest{}, err
		}
		if s.Kind == "metric" {
			wire.CardinalityBound, err = metricCardinality(reg, s)
			if err != nil {
				return WireManifest{}, err
			}
		}
		for _, v := range s.Variants {
			resolved, err := resolveVariant(reg, v, s.Kind == "metric")
			if err != nil {
				return WireManifest{}, err
			}
			wire.ResolvedVariants = append(wire.ResolvedVariants, resolved)
		}
		result.Signals[id] = wire
	}
	return result, nil
}
func cloneMigration(migration MigrationMetadata) MigrationMetadata {
	result := migration
	result.WireChanges = append([]string(nil), migration.WireChanges...)
	result.SignalIDs = make(map[string]int, len(migration.SignalIDs))
	for key, id := range migration.SignalIDs {
		result.SignalIDs[key] = id
	}
	return result
}
func renderManifest(reg Registry) ([]byte, error) {
	result, err := buildManifest(reg)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	return append(data, '\n'), err
}
func attributeBound(reg Registry, key string) int {
	a := reg.Attributes[key]
	if a.MaxValues > 0 {
		return a.MaxValues
	}
	values := map[string]bool{}
	for _, s := range reg.Signals {
		for _, v := range s.Variants {
			w, err := resolveVariant(reg, v, s.Kind == "metric")
			if err != nil {
				continue
			}
			for _, value := range w.Attributes[a.Name].Values {
				values[value] = true
			}
		}
	}
	return len(values)
}
func render(reg Registry) ([]byte, error) {
	manifest, err := buildManifest(reg)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString("// Code generated by vv-otel-gen. DO NOT EDIT.\n\npackage vvotel\n\nimport (\"go.opentelemetry.io/otel/attribute\"; \"unicode\"; \"unicode/utf8\"; \"math\")\n\n")
	out.WriteString("type Signal uint16\n\ntype Signals []Signal\n\nconst (\n")
	for _, key := range sortedKeys(reg.Signals) {
		fmt.Fprintf(&out, "%s Signal = %d\n", toCamelCase("Signal_"+key), reg.Signals[key].SignalID)
	}
	out.WriteString(")\n\nfunc AllSignals() Signals { return Signals{")
	for _, key := range sortedKeys(reg.Signals) {
		out.WriteString(toCamelCase("Signal_"+key) + ",")
	}
	out.WriteString("} }\n\nfunc (s Signals) Has(signal Signal) bool { for _, current := range s { if current==signal { return true } }; return false }\n\nfunc (s Signal) Valid() bool { switch s {\n")
	for _, key := range sortedKeys(reg.Signals) {
		fmt.Fprintf(&out, "case %s: return true\n", toCamelCase("Signal_"+key))
	}
	out.WriteString("default: return false } }\n")
	fmt.Fprintf(&out, "const (\nScopeName = %q\nScopeVersion = %q\nContractVersion = %q\nMigrationFrom = %q\nMigrationTo = %q\nMigrationStatus = %q\nMigrationSince = %q\nMigrationPolicy = %q\nMigrationNote = %q\n)\n\n", reg.Scope.Name, reg.Scope.Version, reg.ContractVersion, reg.Migration.From, reg.Migration.To, reg.Migration.Status, reg.Migration.Since, reg.Migration.Policy, reg.Migration.Note)
	out.WriteString("func MigrationWireChanges() []string { return []string{")
	for _, change := range reg.Migration.WireChanges {
		fmt.Fprintf(&out, "%q,", change)
	}
	out.WriteString("} }\n\n")
	out.WriteString("type AttributeMetadata struct { Name attribute.Key; Source string; PrivacyClass string; CardinalityBound int; MetricEligible bool }\n\nvar AttributeMetadataByKey = map[string]AttributeMetadata{\n")
	for _, key := range sortedKeys(reg.Attributes) {
		a := reg.Attributes[key]
		fmt.Fprintf(&out, "%q: {Name:%s, Source:%q, PrivacyClass:%q, CardinalityBound:%d, MetricEligible:%t},\n", key, toCamelCase("Attr_"+key), a.Source, a.PrivacyClass, attributeBound(reg, key), a.MetricEligible)
	}
	out.WriteString("}\n\ntype MetricMetadata struct { Name string; Type string; Unit string; Description string; Source string; PrivacyClass string; CardinalityBound int; Semconv string; Maturity string }\n\nvar MetricMetadataByKey = map[string]MetricMetadata{\n")
	for _, key := range sortedKeys(reg.Signals) {
		s := reg.Signals[key]
		if s.Kind != "metric" {
			continue
		}
		fmt.Fprintf(&out, "%q: {Name:%q, Type:%q, Unit:%q, Description:%q, Source:%q, PrivacyClass:%q, CardinalityBound:%d, Semconv:%q, Maturity:%q},\n", key, s.Name, s.Instrument, s.Unit, s.Description, s.Source, s.PrivacyClass, manifest.Signals[key].CardinalityBound, s.Semconv, s.Maturity)
	}
	out.WriteString("}\n\nconst (\n")
	for _, key := range sortedKeys(reg.Attributes) {
		fmt.Fprintf(&out, "%s = attribute.Key(%q)\n", toCamelCase("Attr_"+key), reg.Attributes[key].Name)
	}
	resourceAttribute := reg.Attributes[reg.Exports.ResourceAttribute]
	fmt.Fprintf(&out, "MaxResourceNameValues = %d\nMaxResourceNameBytes = %d\n", resourceAttribute.MaxValues, resourceAttribute.MaxBytes)
	for _, key := range sortedKeys(reg.Components) {
		c := reg.Components[key]
		fmt.Fprintf(&out, "%s = %q\n", toCamelCase("Component_"+key), c.WireValue)
		for _, op := range reg.Domains[c.Operations.Domain].Values {
			fmt.Fprintf(&out, "%s = %q\n", toCamelCase("Op_"+key+"_"+op), op)
		}
		for _, dimension := range sortedKeys(vocabularies(c)) {
			v := vocabularies(c)[dimension]
			if v.GoPrefix != "" {
				for _, value := range reg.Domains[v.Domain].Values {
					fmt.Fprintf(&out, "%s = %q\n", toCamelCase(v.GoPrefix+"_"+value), value)
				}
			}
		}
	}
	for _, name := range sortedKeys(reg.Exports.Constants) {
		fmt.Fprintf(&out, "%s = %q\n", name, reg.Exports.Constants[name])
	}
	for _, id := range sortedKeys(reg.LogCorrelation.Keys) {
		key := reg.LogCorrelation.Keys[id]
		fmt.Fprintf(&out, "%s = %q\n", key.GoName, key.Name)
	}
	for _, key := range sortedKeys(reg.Signals) {
		s := reg.Signals[key]
		name := signalGoName(key, s)
		value := s.Name
		if len(manifest.Signals[key].Names) == 1 {
			value = manifest.Signals[key].Names[0]
		}
		fmt.Fprintf(&out, "%s = %q\n", name, value)
		if s.Kind == "metric" {
			fmt.Fprintf(&out, "%sDescription = %q\n%sUnit = %q\n", name, s.Description, name, s.Unit)
		}
	}
	out.WriteString(")\n\nfunc ValidResourceName(value string) bool {\nif len(value)==0 || len(value)>MaxResourceNameBytes || !utf8.ValidString(value) { return false }\nfor _, r := range value { if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-' { return false } }\nreturn true\n}\n\n")
	for _, key := range sortedKeys(reg.Signals) {
		s := reg.Signals[key]
		if s.Instrument != "histogram" {
			continue
		}
		fmt.Fprintf(&out, "func %s() []float64 { return []float64{", boundariesName(key, s))
		for _, b := range s.Boundaries {
			fmt.Fprintf(&out, "%s,", strconv.FormatFloat(b, 'g', -1, 64))
		}
		out.WriteString("} }\n\n")
	}
	def := reg.Exports.DefaultHistogram
	fmt.Fprintf(&out, "var defaultDurationBoundaries = %s()\n\n", boundariesName(def, reg.Signals[def]))
	for _, key := range sortedKeys(reg.Components) {
		c := reg.Components[key]
		for _, dimension := range sortedKeys(vocabularies(c)) {
			v := vocabularies(c)[dimension]
			if v.GoHelper != "" {
				renderMapping(&out, v.GoHelper, v.Mapping, v.Fallback)
			}
		}
	}
	entries := []MappingEntry{}
	for _, value := range reg.Domains[reg.Exports.ErrorCodeDomain].Values {
		entries = append(entries, MappingEntry{From: value, To: value})
	}
	renderMapping(&out, "AllowedErrorCode", entries, "")
	for _, name := range sortedKeys(reg.Exports.SpanHelpers) {
		fmt.Fprintf(&out, "func %s(op string) string { return %q + \" \" + op }\n\n", name, reg.Signals[reg.Exports.SpanHelpers[name].Signal].Name)
	}
	for _, key := range sortedKeys(reg.Signals) {
		s := reg.Signals[key]
		if s.NameDomain == "" || len(manifest.Signals[key].Names) <= 1 {
			continue
		}
		entries := []MappingEntry{}
		for _, value := range reg.Domains[s.NameDomain].Values {
			entries = append(entries, MappingEntry{From: value, To: s.Name + " " + value})
		}
		renderMapping(&out, signalGoName(key, s)+"Name", entries, "")
	}
	renderVariantDescriptors(&out, reg, manifest)
	out.WriteString("type SignalDescriptor struct { Key string; Name string; Names []string; Description string; Boundaries []float64; Minimum float64; Maximum float64; MinimumInt64 int64; MaximumInt64 int64; HasMinimum bool; HasMaximum bool; Variants []SignalVariantDescriptor; Kind string; Instrument string; NumberType string; Unit string; Component string; Availability string; CardinalityBound int; SeriesBudget int; SignalID Signal; Provider string; APIKind string; RecordWhen string; SpanKind string; Source string; PrivacyClass string; Maturity string; Semconv string; NameDomain string; Inputs []string; DeclaredSources map[string][]string; ValueSource string; ComputedSource string }\n\nfunc SignalDescriptors() []SignalDescriptor { return []SignalDescriptor{\n")
	for _, key := range sortedKeys(manifest.Signals) {
		s := manifest.Signals[key]
		name := s.Name
		if len(s.Names) == 1 {
			name = s.Names[0]
		}
		fmt.Fprintf(&out, "{Key:%q, Name:%q, Names:[]string{", key, name)
		for _, name := range s.Names {
			fmt.Fprintf(&out, "%q,", name)
		}
		fmt.Fprintf(&out, "}, Description:%q, Boundaries:[]float64{", s.Description)
		for _, boundary := range s.Boundaries {
			fmt.Fprintf(&out, "%s,", strconv.FormatFloat(boundary, 'g', -1, 64))
		}
		bounds, err := parseMetricBounds(s.Signal)
		if err != nil {
			return nil, fmt.Errorf("render signal %s bounds: %w", key, err)
		}
		fmt.Fprintf(&out, "}, Minimum:%s, Maximum:%s, MinimumInt64:%d, MaximumInt64:%d, HasMinimum:%t, HasMaximum:%t,", strconv.FormatFloat(bounds.minimumFloat, 'g', -1, 64), strconv.FormatFloat(bounds.maximumFloat, 'g', -1, 64), bounds.minimumInt64, bounds.maximumInt64, bounds.hasMinimum, bounds.hasMaximum)
		fmt.Fprintf(&out, "Variants:signalVariants(%s), Kind:%q, Instrument:%q, NumberType:%q, Unit:%q, Component:%q, Availability:%q, CardinalityBound:%d, SeriesBudget:%d, SignalID:%s, Provider:%q, APIKind:%q, RecordWhen:%q, SpanKind:%q, Source:%q, PrivacyClass:%q, Maturity:%q, Semconv:%q, NameDomain:%q, Inputs:[]string{", toCamelCase("Signal_"+key), s.Kind, s.Instrument, s.NumberType, s.Unit, s.Component, s.Availability, s.CardinalityBound, s.SeriesBudget, toCamelCase("Signal_"+key), s.Provider, s.APIKind, s.RecordWhen, s.SpanKind, s.Source, s.PrivacyClass, s.Maturity, s.Semconv, s.NameDomain)
		for _, input := range s.Inputs {
			fmt.Fprintf(&out, "%q,", input)
		}
		out.WriteString("}, DeclaredSources:map[string][]string{")
		for _, attribute := range sortedKeys(s.DeclaredSources) {
			fmt.Fprintf(&out, "%q:[]string{", attribute)
			for _, input := range s.DeclaredSources[attribute] {
				fmt.Fprintf(&out, "%q,", input)
			}
			out.WriteString("},")
		}
		fmt.Fprintf(&out, "}, ValueSource:%q, ComputedSource:%q},\n", s.ValueSource, s.ComputedSource)
	}
	out.WriteString("} }\n")
	data, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w", err)
	}
	return data, nil
}
func renderMapping(out *bytes.Buffer, name string, entries []MappingEntry, fallback string) {
	fmt.Fprintf(out, "func %s(value string) (string, bool) { switch value {\n", name)
	entries = append([]MappingEntry(nil), entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].From < entries[j].From })
	for _, e := range entries {
		fmt.Fprintf(out, "case %q: return %q, true\n", e.From, e.To)
	}
	fmt.Fprintf(out, "default: return %q, %t\n} }\n\n", fallback, fallback != "")
}
func signalGoName(key string, s Signal) string {
	if s.GoName != "" {
		return s.GoName
	}
	prefix := "Span_"
	if s.Kind == "metric" {
		prefix = "Metric_"
	}
	if s.Kind == "span_event" {
		prefix = "Event_"
	}
	return toCamelCase(prefix + key)
}
func boundariesName(key string, s Signal) string {
	if s.BoundariesGoName != "" {
		return s.BoundariesGoName
	}
	return signalGoName(key, s) + "Boundaries"
}
func validValue(kind, value string) bool {
	switch kind {
	case "string":
		return value != "" && len(value) <= 128 && !strings.ContainsAny(value, "\x00\n\r\t")
	case "bool":
		return value == "true" || value == "false"
	case "int64":
		n, err := strconv.ParseInt(value, 10, 64)
		return err == nil && strconv.FormatInt(n, 10) == value
	default:
		return false
	}
}
func validUnit(value string) bool {
	if oneOf(value, "s", "By", "1") {
		return true
	}
	return len(value) > 2 && value[0] == '{' && value[len(value)-1] == '}' && validToken(value[1:len(value)-1])
}
func validMaturity(value string) bool { return oneOf(value, "development", "stable", "deprecated") }
func validWireName(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '.' && ch != '_' {
			return false
		}
	}
	return !strings.HasSuffix(value, ".") && !strings.Contains(value, "..")
}
func validID(value string) bool                 { return validWireName(value) }
func validToken(value string) bool              { return validWireName(value) && !strings.Contains(value, ".") }
func oneOf(value string, values ...string) bool { return contains(values, value) }
func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func toCamelCase(value string) string {
	var result strings.Builder
	for _, part := range strings.Split(value, "_") {
		if part != "" {
			result.WriteString(strings.ToUpper(part[:1]))
			result.WriteString(part[1:])
		}
	}
	return result.String()
}
func fail(err error) { fmt.Fprintf(os.Stderr, "vv-otel-gen: %v\n", err); os.Exit(1) }

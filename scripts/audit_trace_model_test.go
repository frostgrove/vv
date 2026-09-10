package scripts

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	auditTraceRegistrySchema  = "audit-trace/v1"
	auditTraceSemanticsSchema = "audit-trace-semantics/v1"
	auditTraceAnchorSchema    = "audit-trace-anchor/v1"
)

var (
	auditTraceNumericID  = regexp.MustCompile(`^(AU|AH|AE|AI|AT)-[0-9]{3}$`)
	auditTraceMutantID   = regexp.MustCompile(`^AM-[A-Z]+(?:-[A-Z]+)*-[0-9]{3}$`)
	auditTracePositiveID = regexp.MustCompile(
		`^PN-[A-Z]+(?:-[A-Z]+)*-[0-9]{3}$`,
	)
	auditTraceReservationID = regexp.MustCompile(
		`^RT-(?:C-AT[0-9]{3}-S[0-7]|[MP]-[A-Z]+(?:-[A-Z]+)*-[0-9]{3})$`,
	)
	auditTracePackageID      = regexp.MustCompile(`^PKG-[A-Z][A-Z0-9]*$`)
	auditTraceGoSymbol       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	auditTraceDigest         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	auditTraceATSymbol       = regexp.MustCompile(`^TestAT[0-9]{3}`)
	auditTraceMutantPackages = map[string]string{
		"S0": "PKG-SCRIPTS",
		"S1": "PKG-AUDIT",
		"S2": "PKG-AUDIT",
		"S3": "PKG-AUDIT",
		"S4": "PKG-AUDITCRUD",
		"S5": "PKG-AUDITPG",
		"S6": "PKG-SCRIPTS",
	}
	auditTraceMutantPackageExceptions = map[string]string{
		"AM-APPEND-001":  "PKG-AUDITMEMORY",
		"AM-BRIDGE-001":  "PKG-AUDITCRUDBRIDGE",
		"AM-CONFORM-001": "PKG-AUDITTEST",
		"AM-OBS-001":     "PKG-AUDITFLOW",
	}
)

type auditTraceEdge struct {
	from     string
	relation string
	to       string
}

type auditTraceTest struct {
	id         string
	symbol     string
	activation string
	packageID  string
	role       string
}

type auditTracePackage struct {
	id         string
	directory  string
	pattern    string
	importPath string
	profile    string
}

type auditTraceRegistry struct {
	raw             []byte
	facts           []string
	nodes           map[string]string
	sections        map[string]uint64
	edges           []auditTraceEdge
	semanticDigests map[string]auditTraceSemanticFact
	tests           map[string]auditTraceTest
	packages        map[string]auditTracePackage
}

type auditTraceSemanticFact struct {
	id     string
	kind   string
	digest string
}

type auditTraceSemanticRecord struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type auditTraceSemantics struct {
	Schema  string                     `json:"schema"`
	Records []auditTraceSemanticRecord `json:"records"`
}

type auditTraceInventory struct {
	Kind  string   `json:"kind"`
	Count uint64   `json:"count"`
	IDs   []string `json:"ids"`
}

type auditTraceSemanticDigest struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Digest string `json:"digest"`
}

type auditTraceAnchor struct {
	Schema             string                     `json:"schema"`
	RegistrySchema     string                     `json:"registry_schema"`
	SemanticsSchema    string                     `json:"semantics_schema"`
	Inventories        []auditTraceInventory      `json:"inventories"`
	GraphFactCount     uint64                     `json:"graph_fact_count"`
	GoalTraceEdgeCount uint64                     `json:"goal_trace_edge_count"`
	GraphDigest        string                     `json:"graph_digest"`
	NodeSetDigest      string                     `json:"node_set_digest"`
	SemanticSetDigest  string                     `json:"semantic_set_digest"`
	FactDigests        []string                   `json:"fact_digests"`
	SemanticDigests    []auditTraceSemanticDigest `json:"semantic_digests"`
}

type auditTraceBundle struct {
	registry  *auditTraceRegistry
	semantics auditTraceSemantics
	anchor    auditTraceAnchor
}

func loadAuditTrace(repoRoot string) (*auditTraceBundle, error) {
	registryPath := filepath.Join(repoRoot, "scripts", "testdata", "audit_trace.tsv")
	semanticsPath := filepath.Join(repoRoot, "scripts", "testdata", "audit_trace_semantics.json")
	anchorPath := filepath.Join(repoRoot, "scripts", "testdata", "audit_trace_anchor.json")

	registryBytes, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("read audit trace registry: %w", err)
	}
	semanticsBytes, err := os.ReadFile(semanticsPath)
	if err != nil {
		return nil, fmt.Errorf("read audit trace semantics: %w", err)
	}
	anchorBytes, err := os.ReadFile(anchorPath)
	if err != nil {
		return nil, fmt.Errorf("read audit trace anchor: %w", err)
	}
	return parseAuditTraceBundle(registryBytes, semanticsBytes, anchorBytes)
}

func parseAuditTraceBundle(registryBytes, semanticsBytes, anchorBytes []byte) (*auditTraceBundle, error) {
	registry, err := parseAuditTraceRegistry(registryBytes)
	if err != nil {
		return nil, err
	}
	semantics, err := parseAuditTraceSemantics(semanticsBytes)
	if err != nil {
		return nil, err
	}
	if err := validateAuditTraceGraph(registry, semantics); err != nil {
		return nil, err
	}
	anchor, err := parseAuditTraceAnchor(anchorBytes)
	if err != nil {
		return nil, err
	}
	wantAnchor, err := buildAuditTraceAnchor(registry, semantics)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(anchor, wantAnchor) {
		return nil, fmt.Errorf("audit trace anchor does not match the registry and semantics")
	}
	return &auditTraceBundle{registry: registry, semantics: semantics, anchor: anchor}, nil
}

func parseAuditTraceRegistry(content []byte) (*auditTraceRegistry, error) {
	if err := validateAuditTraceRegistryBytes(content); err != nil {
		return nil, err
	}
	lines := strings.Split(string(content[:len(content)-1]), "\n")
	if lines[0] != "schema\t"+auditTraceRegistrySchema {
		return nil, fmt.Errorf("audit trace registry has an invalid schema row")
	}
	for index := 2; index < len(lines); index++ {
		if lines[index-1] >= lines[index] {
			return nil, fmt.Errorf("audit trace registry facts are not strictly byte-sorted at line %d", index+1)
		}
	}

	registry := &auditTraceRegistry{
		raw:             append([]byte(nil), content...),
		facts:           append([]string(nil), lines[1:]...),
		nodes:           make(map[string]string),
		sections:        make(map[string]uint64),
		semanticDigests: make(map[string]auditTraceSemanticFact),
		tests:           make(map[string]auditTraceTest),
		packages:        make(map[string]auditTracePackage),
	}
	for number, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		lineNumber := number + 2
		switch fields[0] {
		case "node":
			if len(fields) != 3 {
				return nil, fmt.Errorf("line %d has an invalid node fact", lineNumber)
			}
			if _, exists := registry.nodes[fields[1]]; exists {
				return nil, fmt.Errorf("line %d duplicates node %s", lineNumber, fields[1])
			}
			registry.nodes[fields[1]] = fields[2]
		case "section":
			if len(fields) != 3 {
				return nil, fmt.Errorf("line %d has an invalid section fact", lineNumber)
			}
			rank, err := parseAuditTraceUint(fields[2])
			if err != nil {
				return nil, fmt.Errorf("line %d has an invalid delivery rank: %w", lineNumber, err)
			}
			if _, exists := registry.sections[fields[1]]; exists {
				return nil, fmt.Errorf("line %d duplicates section %s", lineNumber, fields[1])
			}
			registry.sections[fields[1]] = rank
		case "edge":
			if len(fields) != 4 {
				return nil, fmt.Errorf("line %d has an invalid edge fact", lineNumber)
			}
			registry.edges = append(registry.edges, auditTraceEdge{from: fields[1], relation: fields[2], to: fields[3]})
		case "semantic":
			if len(fields) != 4 || !auditTraceDigest.MatchString(fields[3]) {
				return nil, fmt.Errorf("line %d has an invalid semantic fact", lineNumber)
			}
			if _, exists := registry.semanticDigests[fields[1]]; exists {
				return nil, fmt.Errorf("line %d duplicates semantic fact %s", lineNumber, fields[1])
			}
			registry.semanticDigests[fields[1]] = auditTraceSemanticFact{id: fields[1], kind: fields[2], digest: fields[3]}
		case "test":
			if len(fields) != 6 {
				return nil, fmt.Errorf("line %d has an invalid test fact", lineNumber)
			}
			if _, exists := registry.tests[fields[1]]; exists {
				return nil, fmt.Errorf("line %d duplicates test reservation %s", lineNumber, fields[1])
			}
			registry.tests[fields[1]] = auditTraceTest{id: fields[1], symbol: fields[2], activation: fields[3], packageID: fields[4], role: fields[5]}
		case "package":
			if len(fields) != 6 {
				return nil, fmt.Errorf("line %d has an invalid package fact", lineNumber)
			}
			if _, exists := registry.packages[fields[1]]; exists {
				return nil, fmt.Errorf("line %d duplicates package %s", lineNumber, fields[1])
			}
			registry.packages[fields[1]] = auditTracePackage{id: fields[1], directory: fields[2], pattern: fields[3], importPath: fields[4], profile: fields[5]}
		default:
			return nil, fmt.Errorf("line %d has unknown fact kind %q", lineNumber, fields[0])
		}
	}
	return registry, nil
}

func validateAuditTraceRegistryBytes(content []byte) error {
	if len(content) == 0 {
		return errors.New("audit trace registry is empty")
	}
	if !utf8.Valid(content) {
		return errors.New("audit trace registry is not valid UTF-8")
	}
	if bytes.HasPrefix(content, []byte{0xef, 0xbb, 0xbf}) {
		return errors.New("audit trace registry has a BOM")
	}
	if bytes.ContainsAny(content, "\r\x00") {
		return errors.New("audit trace registry contains CR or NUL")
	}
	if !bytes.HasSuffix(content, []byte("\n")) || bytes.HasSuffix(content, []byte("\n\n")) {
		return errors.New("audit trace registry must have exactly one terminal LF")
	}
	for number, line := range bytes.Split(content[:len(content)-1], []byte("\n")) {
		if len(line) == 0 {
			return fmt.Errorf("audit trace registry has a blank line at %d", number+1)
		}
		if line[0] == ' ' || line[len(line)-1] == ' ' {
			return fmt.Errorf("audit trace registry line %d has edge space", number+1)
		}
		for _, value := range line {
			if value == '\t' {
				continue
			}
			if value < 0x20 || value > 0x7e {
				return fmt.Errorf("audit trace registry line %d is not ASCII", number+1)
			}
		}
	}
	return nil
}

func parseAuditTraceSemantics(content []byte) (auditTraceSemantics, error) {
	var manifest auditTraceSemantics
	if err := decodeAuditTraceJSON(content, &manifest); err != nil {
		return manifest, fmt.Errorf("decode audit trace semantics: %w", err)
	}
	if manifest.Schema != auditTraceSemanticsSchema {
		return manifest, fmt.Errorf("audit trace semantics has schema %q", manifest.Schema)
	}
	canonical, err := encodeAuditTraceJSON(manifest)
	if err != nil {
		return manifest, err
	}
	if !bytes.Equal(content, canonical) {
		return manifest, errors.New("audit trace semantics is not canonical JSON")
	}
	seen := make(map[string]struct{}, len(manifest.Records))
	for index, record := range manifest.Records {
		if _, exists := seen[record.ID]; exists {
			return manifest, fmt.Errorf("audit trace semantics duplicates %s", record.ID)
		}
		seen[record.ID] = struct{}{}
		if record.Text == "" {
			return manifest, fmt.Errorf("audit trace semantic %s has empty text", record.ID)
		}
		if index > 0 && compareAuditTraceSemantic(manifest.Records[index-1], record) >= 0 {
			return manifest, errors.New("audit trace semantic records are not strictly byte-sorted")
		}
	}
	return manifest, nil
}

func parseAuditTraceAnchor(content []byte) (auditTraceAnchor, error) {
	var anchor auditTraceAnchor
	if err := decodeAuditTraceJSON(content, &anchor); err != nil {
		return anchor, fmt.Errorf("decode audit trace anchor: %w", err)
	}
	canonical, err := encodeAuditTraceJSON(anchor)
	if err != nil {
		return anchor, err
	}
	if !bytes.Equal(content, canonical) {
		return anchor, errors.New("audit trace anchor is not canonical JSON")
	}
	if anchor.Schema != auditTraceAnchorSchema || anchor.RegistrySchema != auditTraceRegistrySchema || anchor.SemanticsSchema != auditTraceSemanticsSchema {
		return anchor, errors.New("audit trace anchor has an invalid schema tuple")
	}
	return anchor, nil
}

func decodeAuditTraceJSON(content []byte, target any) error {
	if len(content) == 0 || !utf8.Valid(content) {
		return errors.New("JSON is empty or invalid UTF-8")
	}
	if bytes.ContainsAny(content, "\r\x00") {
		return errors.New("JSON contains CR or NUL")
	}
	if err := rejectAuditTraceDuplicateMembers(content); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON has a trailing value")
		}
		return err
	}
	return nil
}

func rejectAuditTraceDuplicateMembers(content []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := consumeAuditTraceJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("JSON has trailing token %v", token)
		}
		return err
	}
	return nil
}

func consumeAuditTraceJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("JSON object duplicates member %q", key)
			}
			seen[key] = struct{}{}
			if err := consumeAuditTraceJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := consumeAuditTraceJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("JSON array is not closed")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func encodeAuditTraceJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func validateAuditTraceGraph(registry *auditTraceRegistry, semantics auditTraceSemantics) error {
	if err := validateAuditTraceNodes(registry); err != nil {
		return err
	}
	if err := validateAuditTraceSections(registry); err != nil {
		return err
	}
	counts, err := validateAuditTraceEdges(registry)
	if err != nil {
		return err
	}
	if err := validateAuditTraceSemantics(registry, semantics); err != nil {
		return err
	}
	if err := validateAuditTracePackages(registry); err != nil {
		return err
	}
	if err := validateAuditTraceReservations(registry, counts); err != nil {
		return err
	}
	return nil
}

func validateAuditTraceNodes(registry *auditTraceRegistry) error {
	for id, kind := range registry.nodes {
		want, ok := auditTraceKindForID(id)
		if !ok {
			return fmt.Errorf("audit trace node %s has an invalid identity", id)
		}
		if kind != want {
			return fmt.Errorf("audit trace node %s has kind %s, expected %s", id, kind, want)
		}
	}
	return nil
}

func auditTraceKindForID(id string) (string, bool) {
	if auditTraceNumericID.MatchString(id) {
		switch id[:2] {
		case "AU":
			return "actor-goal", true
		case "AH":
			return "happy-requirement", true
		case "AE":
			return "edge-requirement", true
		case "AI":
			return "invariant", true
		case "AT":
			return "test-obligation", true
		}
	}
	if auditTraceMutantID.MatchString(id) {
		return "defect-mutant", true
	}
	if auditTracePositiveID.MatchString(id) {
		return "positive-neighbor", true
	}
	if auditTraceReservationID.MatchString(id) {
		return "test-reservation", true
	}
	if auditTracePackageID.MatchString(id) {
		return "package", true
	}
	if len(id) == 2 && id[0] == 'S' && id[1] >= '0' && id[1] <= '7' {
		return "implementation-section", true
	}
	return "", false
}

func validateAuditTraceSections(registry *auditTraceRegistry) error {
	want := map[string]uint64{"S0": 0, "S1": 1, "S2": 2, "S4": 3, "S3": 4, "S5": 5, "S6": 6, "S7": 7}
	if len(registry.sections) != len(want) {
		return fmt.Errorf("audit trace has %d section rows, expected %d", len(registry.sections), len(want))
	}
	for section, rank := range want {
		actualRank, exists := registry.sections[section]
		if !exists || registry.nodes[section] != "implementation-section" || actualRank != rank {
			return fmt.Errorf("audit trace section %s does not have delivery rank %d", section, rank)
		}
	}
	for section := range registry.sections {
		if registry.nodes[section] != "implementation-section" {
			return fmt.Errorf("audit trace section fact %s has no section node", section)
		}
	}
	return nil
}

type auditTraceEdgeCounts struct {
	requires     map[string]int
	requiredBy   map[string]int
	provedBy     map[string][]string
	implemented  map[string]int
	guards       map[string]int
	activated    map[string][]string
	killedBy     map[string][]string
	ownedBy      map[string][]string
	mutantRT     map[string][]string
	contrastedBy map[string][]string
	positiveAT   map[string][]string
	positiveRT   map[string][]string
	coverageRT   map[string][]string
}

func validateAuditTraceEdges(registry *auditTraceRegistry) (auditTraceEdgeCounts, error) {
	counts := auditTraceEdgeCounts{
		requires: make(map[string]int), requiredBy: make(map[string]int), provedBy: make(map[string][]string),
		implemented: make(map[string]int), guards: make(map[string]int), activated: make(map[string][]string),
		killedBy: make(map[string][]string), ownedBy: make(map[string][]string), mutantRT: make(map[string][]string),
		contrastedBy: make(map[string][]string), positiveAT: make(map[string][]string), positiveRT: make(map[string][]string),
		coverageRT: make(map[string][]string),
	}
	for _, edge := range registry.edges {
		fromKind, fromExists := registry.nodes[edge.from]
		toKind, toExists := registry.nodes[edge.to]
		if !fromExists || !toExists {
			return counts, fmt.Errorf("audit trace edge %s %s %s has an undeclared endpoint", edge.from, edge.relation, edge.to)
		}
		requirement := fromKind == "happy-requirement" || fromKind == "edge-requirement" || fromKind == "invariant"
		switch edge.relation {
		case "requires":
			if fromKind != "actor-goal" || !(toKind == "happy-requirement" || toKind == "edge-requirement" || toKind == "invariant") {
				return counts, fmt.Errorf("audit trace requires edge has wrong endpoint kinds")
			}
			counts.requires[edge.from]++
			counts.requiredBy[edge.to]++
		case "proved-by":
			if requirement && toKind == "test-obligation" {
				counts.provedBy[edge.from] = append(counts.provedBy[edge.from], edge.to)
			} else if fromKind == "positive-neighbor" && toKind == "test-obligation" {
				counts.positiveAT[edge.from] = append(counts.positiveAT[edge.from], edge.to)
			} else {
				return counts, fmt.Errorf("audit trace proved-by edge has wrong endpoint kinds")
			}
		case "implemented-in":
			if !requirement || toKind != "implementation-section" {
				return counts, fmt.Errorf("audit trace implemented-in edge has wrong endpoint kinds")
			}
			if edge.to == "S7" {
				return counts, fmt.Errorf("audit trace terminal section S7 implements no requirement")
			}
			counts.implemented[edge.from]++
		case "activated-in":
			if fromKind != "test-obligation" || toKind != "implementation-section" {
				return counts, fmt.Errorf("audit trace activated-in edge has wrong endpoint kinds")
			}
			if edge.to == "S7" {
				return counts, fmt.Errorf("audit trace terminal section S7 activates no obligation")
			}
			counts.activated[edge.from] = append(counts.activated[edge.from], edge.to)
		case "guards":
			if !requirement || toKind != "defect-mutant" {
				return counts, fmt.Errorf("audit trace guards edge has wrong endpoint kinds")
			}
			counts.guards[edge.from]++
		case "killed-by":
			if fromKind != "defect-mutant" || toKind != "test-obligation" {
				return counts, fmt.Errorf("audit trace killed-by edge has wrong endpoint kinds")
			}
			counts.killedBy[edge.from] = append(counts.killedBy[edge.from], edge.to)
		case "owned-by":
			if fromKind != "defect-mutant" || toKind != "implementation-section" || edge.to == "S7" {
				return counts, fmt.Errorf("audit trace owned-by edge has wrong endpoint kinds")
			}
			counts.ownedBy[edge.from] = append(counts.ownedBy[edge.from], edge.to)
		case "reserved-by":
			if toKind != "test-reservation" {
				return counts, fmt.Errorf("audit trace reserved-by edge has wrong target kind")
			}
			switch fromKind {
			case "defect-mutant":
				counts.mutantRT[edge.from] = append(counts.mutantRT[edge.from], edge.to)
			case "positive-neighbor":
				counts.positiveRT[edge.from] = append(counts.positiveRT[edge.from], edge.to)
			case "test-obligation":
				counts.coverageRT[edge.from] = append(counts.coverageRT[edge.from], edge.to)
			default:
				return counts, fmt.Errorf("audit trace reserved-by edge has wrong source kind")
			}
		case "contrasted-by":
			if fromKind != "defect-mutant" || toKind != "positive-neighbor" {
				return counts, fmt.Errorf("audit trace contrasted-by edge has wrong endpoint kinds")
			}
			counts.contrastedBy[edge.from] = append(counts.contrastedBy[edge.from], edge.to)
		default:
			return counts, fmt.Errorf("audit trace has unknown edge relation %q", edge.relation)
		}
	}

	for id, kind := range registry.nodes {
		switch kind {
		case "actor-goal":
			if counts.requires[id] == 0 {
				return counts, fmt.Errorf("actor goal %s has no requirement", id)
			}
		case "happy-requirement", "edge-requirement", "invariant":
			if counts.requiredBy[id] == 0 || len(counts.provedBy[id]) == 0 || counts.implemented[id] == 0 || counts.guards[id] == 0 {
				return counts, fmt.Errorf("requirement %s is not completely connected", id)
			}
		case "test-obligation":
			if len(counts.activated[id]) != 1 || len(counts.coverageRT[id]) != 1 {
				return counts, fmt.Errorf("test obligation %s does not have one activation and coverage reservation", id)
			}
		case "defect-mutant":
			if len(counts.killedBy[id]) != 1 || len(counts.ownedBy[id]) != 1 || len(counts.mutantRT[id]) != 1 || len(counts.contrastedBy[id]) != 1 {
				return counts, fmt.Errorf("defect mutant %s is not completely connected", id)
			}
		case "positive-neighbor":
			if len(counts.positiveAT[id]) != 1 || len(counts.positiveRT[id]) != 1 {
				return counts, fmt.Errorf("positive neighbor %s is not completely connected", id)
			}
		}
	}
	return counts, nil
}

func validateAuditTraceSemantics(registry *auditTraceRegistry, semantics auditTraceSemantics) error {
	records := make(map[string]auditTraceSemanticRecord, len(semantics.Records))
	for _, record := range semantics.Records {
		nodeKind, exists := registry.nodes[record.ID]
		if !exists {
			return fmt.Errorf("semantic record %s has no node", record.ID)
		}
		wantKind, ok := auditTraceSemanticKind(nodeKind)
		if !ok || record.Kind != wantKind {
			return fmt.Errorf("semantic record %s has kind %s, expected %s", record.ID, record.Kind, wantKind)
		}
		fact, exists := registry.semanticDigests[record.ID]
		if !exists || fact.kind != record.Kind {
			return fmt.Errorf("semantic record %s has no matching registry fact", record.ID)
		}
		if fact.digest != auditTraceSemanticRecordDigest(record) {
			return fmt.Errorf("semantic record %s digest does not match", record.ID)
		}
		records[record.ID] = record
	}
	for id, nodeKind := range registry.nodes {
		_, semanticNode := auditTraceSemanticKind(nodeKind)
		_, hasRecord := records[id]
		_, hasFact := registry.semanticDigests[id]
		if semanticNode != hasRecord || semanticNode != hasFact {
			return fmt.Errorf("audit trace node %s has incomplete semantic authority", id)
		}
	}
	for id := range registry.semanticDigests {
		nodeKind, exists := registry.nodes[id]
		if _, semanticNode := auditTraceSemanticKind(nodeKind); !exists || !semanticNode {
			return fmt.Errorf("audit trace semantic fact %s has no semantic node", id)
		}
	}
	return nil
}

func auditTraceSemanticKind(nodeKind string) (string, bool) {
	switch nodeKind {
	case "actor-goal":
		return "actor-goal", true
	case "happy-requirement":
		return "happy", true
	case "edge-requirement":
		return "edge", true
	case "invariant":
		return "invariant", true
	case "test-obligation":
		return "obligation", true
	case "defect-mutant":
		return "defect", true
	case "positive-neighbor":
		return "positive", true
	default:
		return "", false
	}
}

func validateAuditTracePackages(registry *auditTraceRegistry) error {
	for id, nodeKind := range registry.nodes {
		_, hasPackage := registry.packages[id]
		if (nodeKind == "package") != hasPackage {
			return fmt.Errorf("audit trace package node %s has incomplete package authority", id)
		}
	}
	for _, item := range registry.packages {
		if registry.nodes[item.id] != "package" {
			return fmt.Errorf("audit trace package fact %s has no package node", item.id)
		}
		if item.profile != "unit" && item.profile != "integration" {
			return fmt.Errorf("audit trace package %s has invalid profile %q", item.id, item.profile)
		}
		if item.directory == "" || filepath.IsAbs(item.directory) || filepath.Clean(item.directory) != item.directory || item.directory == ".." || strings.HasPrefix(item.directory, ".."+string(filepath.Separator)) {
			return fmt.Errorf("audit trace package %s has unsafe working directory", item.id)
		}
		if !strings.HasPrefix(item.pattern, "./") || strings.ContainsAny(item.pattern, " \t\r\n") || strings.HasPrefix(item.pattern, "./-") {
			return fmt.Errorf("audit trace package %s has unsafe package pattern", item.id)
		}
		if item.importPath == "" || strings.ContainsAny(item.importPath, " \t\r\n") || !strings.HasPrefix(item.importPath, "github.com/frostgrove/vv") {
			return fmt.Errorf("audit trace package %s has invalid import path", item.id)
		}
	}
	return nil
}

func validateAuditTraceReservations(registry *auditTraceRegistry, counts auditTraceEdgeCounts) error {
	for id, kind := range registry.nodes {
		_, exists := registry.tests[id]
		if (kind == "test-reservation") != exists {
			return fmt.Errorf("audit trace reservation node %s has incomplete test authority", id)
		}
	}
	seenSymbols := make(map[string]string)
	for id, test := range registry.tests {
		if registry.nodes[id] != "test-reservation" {
			return fmt.Errorf("audit trace test fact %s has no reservation node", id)
		}
		if !auditTraceGoSymbol.MatchString(test.symbol) {
			return fmt.Errorf("audit trace reservation %s has invalid Go symbol %q", id, test.symbol)
		}
		if registry.nodes[test.activation] != "implementation-section" || registry.nodes[test.packageID] != "package" {
			return fmt.Errorf("audit trace reservation %s has invalid activation or package", id)
		}
		if test.role != "coverage" && test.role != "mutant" && test.role != "positive" {
			return fmt.Errorf("audit trace reservation %s has invalid role %q", id, test.role)
		}
		key := test.packageID + "\x00" + test.symbol
		if previous, exists := seenSymbols[key]; exists {
			return fmt.Errorf("audit trace reservations %s and %s collide on one package symbol", previous, id)
		}
		seenSymbols[key] = id
	}
	for at, reservations := range counts.coverageRT {
		reservation := reservations[0]
		activation := counts.activated[at][0]
		wantID := "RT-C-" + strings.ReplaceAll(at, "-", "") + "-" + activation
		test := registry.tests[reservation]
		wantSymbol := "Test" + strings.ReplaceAll(at, "-", "") + activation + "Contract"
		if reservation != wantID || test.id != reservation || test.activation != activation || test.role != "coverage" || test.symbol != wantSymbol {
			return fmt.Errorf("test obligation %s has an invalid coverage reservation", at)
		}
	}
	for mutant, reservations := range counts.mutantRT {
		suffix := strings.TrimPrefix(mutant, "AM-")
		mutantReservation := reservations[0]
		positive := counts.contrastedBy[mutant][0]
		positiveReservations := counts.positiveRT[positive]
		if positive != "PN-"+suffix || mutantReservation != "RT-M-"+suffix || len(positiveReservations) != 1 || positiveReservations[0] != "RT-P-"+suffix {
			return fmt.Errorf("defect mutant %s has an invalid neighbor or reservation identity", mutant)
		}
		mutantTest := registry.tests[mutantReservation]
		positiveTest := registry.tests[positiveReservations[0]]
		owner := counts.ownedBy[mutant][0]
		at := counts.killedBy[mutant][0]
		if len(counts.positiveAT[positive]) != 1 || counts.positiveAT[positive][0] != at {
			return fmt.Errorf("defect mutant %s and its neighbor use different obligations", mutant)
		}
		wantPositive := ""
		if mutant == "AM-TRACE-001" {
			wantPositive = "TestAuditTraceCheckpointPreservesCompleteGraph"
			if at != "AT-017" || mutantTest.symbol != "TestAuditTraceCheckpointRejectsMissingTest" {
				return fmt.Errorf("defect mutant %s has an invalid trace reservation", mutant)
			}
		} else {
			if strings.Count(mutantTest.symbol, "Kills") != 1 {
				return fmt.Errorf("defect mutant %s symbol does not contain exactly one Kills token", mutant)
			}
			wantPrefix := "Test" + strings.ReplaceAll(at, "-", "") + "Kills"
			if !strings.HasPrefix(mutantTest.symbol, wantPrefix) {
				return fmt.Errorf("defect mutant %s symbol does not encode its primary obligation %s", mutant, at)
			}
			wantPositive = strings.Replace(mutantTest.symbol, "Kills", "Preserves", 1)
		}
		wantPackage := auditTraceMutantPackages[owner]
		if exception := auditTraceMutantPackageExceptions[mutant]; exception != "" {
			wantPackage = exception
		}
		if mutantTest.role != "mutant" || positiveTest.role != "positive" || mutantTest.activation != owner || positiveTest.activation != owner || mutantTest.packageID != wantPackage || positiveTest.packageID != wantPackage || positiveTest.symbol != wantPositive {
			return fmt.Errorf("defect mutant %s has an invalid executable pair", mutant)
		}
	}
	return nil
}

func buildAuditTraceAnchor(registry *auditTraceRegistry, semantics auditTraceSemantics) (auditTraceAnchor, error) {
	byKind := make(map[string][]string)
	for id, kind := range registry.nodes {
		byKind[kind] = append(byKind[kind], id)
	}
	kinds := make([]string, 0, len(byKind))
	for kind := range byKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	inventories := make([]auditTraceInventory, 0, len(kinds))
	for _, kind := range kinds {
		ids := byKind[kind]
		sort.Strings(ids)
		inventories = append(inventories, auditTraceInventory{Kind: kind, Count: uint64(len(ids)), IDs: ids})
	}

	nodeLines := make([]string, 0, len(registry.nodes))
	factDigests := make([]string, 0, len(registry.facts))
	goalEdges := uint64(0)
	for _, line := range registry.facts {
		if strings.HasPrefix(line, "node\t") {
			nodeLines = append(nodeLines, line)
		}
		if strings.HasPrefix(line, "edge\tAU-") && strings.Contains(line, "\trequires\t") {
			goalEdges++
		}
		factDigests = append(factDigests, auditTraceFactDigest(line))
	}
	sort.Strings(nodeLines)
	sort.Strings(factDigests)
	for index := 1; index < len(factDigests); index++ {
		if factDigests[index-1] == factDigests[index] {
			return auditTraceAnchor{}, errors.New("audit trace has duplicate fact digests")
		}
	}

	semanticDigests := make([]auditTraceSemanticDigest, 0, len(semantics.Records))
	for _, record := range semantics.Records {
		semanticDigests = append(semanticDigests, auditTraceSemanticDigest{ID: record.ID, Kind: record.Kind, Digest: auditTraceSemanticRecordDigest(record)})
	}
	return auditTraceAnchor{
		Schema: auditTraceAnchorSchema, RegistrySchema: auditTraceRegistrySchema, SemanticsSchema: auditTraceSemanticsSchema,
		Inventories: inventories, GraphFactCount: uint64(len(registry.facts)), GoalTraceEdgeCount: goalEdges,
		GraphDigest: auditTraceGraphDigest(registry.raw), NodeSetDigest: auditTraceNodeSetDigest(nodeLines),
		SemanticSetDigest: auditTraceSemanticSetDigest(semantics.Records), FactDigests: factDigests,
		SemanticDigests: semanticDigests,
	}, nil
}

func auditTraceGraphDigest(registry []byte) string {
	hash := sha256.New()
	hash.Write([]byte("frostgrove.audit/trace-graph/v1\x00"))
	writeAuditTraceUint64(hash, uint64(len(registry)))
	hash.Write(registry)
	return hex.EncodeToString(hash.Sum(nil))
}

func auditTraceNodeSetDigest(lines []string) string {
	hash := sha256.New()
	hash.Write([]byte("frostgrove.audit/trace-nodes/v1\x00"))
	writeAuditTraceUint64(hash, uint64(len(lines)))
	for _, line := range lines {
		writeAuditTraceBytes(hash, []byte(line))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func auditTraceFactDigest(line string) string {
	hash := sha256.New()
	hash.Write([]byte("frostgrove.audit/trace-fact/v1\x00"))
	writeAuditTraceBytes(hash, []byte(line))
	return hex.EncodeToString(hash.Sum(nil))
}

func auditTraceSemanticRecordDigest(record auditTraceSemanticRecord) string {
	hash := sha256.New()
	hash.Write([]byte("frostgrove.audit/trace-semantic/v1\x00"))
	writeAuditTraceBytes(hash, []byte(record.Kind))
	writeAuditTraceBytes(hash, []byte(record.ID))
	writeAuditTraceBytes(hash, []byte(record.Text))
	return hex.EncodeToString(hash.Sum(nil))
}

func auditTraceSemanticSetDigest(records []auditTraceSemanticRecord) string {
	hash := sha256.New()
	hash.Write([]byte("frostgrove.audit/trace-semantics/v1\x00"))
	writeAuditTraceUint64(hash, uint64(len(records)))
	for _, record := range records {
		writeAuditTraceBytes(hash, []byte(record.Kind))
		writeAuditTraceBytes(hash, []byte(record.ID))
		writeAuditTraceBytes(hash, []byte(record.Text))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeAuditTraceBytes(writer io.Writer, value []byte) {
	writeAuditTraceUint64(writer, uint64(len(value)))
	_, _ = writer.Write(value)
}

func writeAuditTraceUint64(writer io.Writer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

func parseAuditTraceUint(value string) (uint64, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') || value[0] == '+' || value[0] == '-' {
		return 0, errors.New("integer is not canonical")
	}
	return strconv.ParseUint(value, 10, 64)
}

func compareAuditTraceSemantic(left, right auditTraceSemanticRecord) int {
	if order := strings.Compare(left.Kind, right.Kind); order != 0 {
		return order
	}
	return strings.Compare(left.ID, right.ID)
}

func auditTraceCanonicalRegistry(facts []string) []byte {
	sorted := append([]string(nil), facts...)
	sort.Strings(sorted)
	return []byte("schema\t" + auditTraceRegistrySchema + "\n" + strings.Join(sorted, "\n") + "\n")
}

func readAuditTraceJSONEvents(input string) ([]auditTraceGoEvent, error) {
	scanner := bufio.NewScanner(strings.NewReader(input))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var events []auditTraceGoEvent
	for scanner.Scan() {
		var event auditTraceGoEvent
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		if err := decoder.Decode(&event); err != nil {
			return nil, fmt.Errorf("decode go test JSON event: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err == nil {
				return nil, errors.New("go test JSON event has a trailing value")
			}
			return nil, fmt.Errorf("decode trailing go test JSON value: %w", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

type auditTraceGoEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

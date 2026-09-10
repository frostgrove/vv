//go:build audit_trace_import

package scripts

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var (
	auditTraceAUHeading       = regexp.MustCompile(`^### (AU-[0-9]{3}) — (.+)$`)
	auditTraceRequirementItem = regexp.MustCompile(
		`^- \*\*((A[HEI]-[0-9]{3})(?: ([^*]+))?):\*\*(?: (.*))?$`,
	)
	auditTraceATItem = regexp.MustCompile(`^\*\*(AT-[0-9]{3}) —\*\*(?: (.*))?$`)
)

type auditTraceDesign struct {
	registry  []byte
	semantics []byte
	anchor    []byte
}

type auditTraceRequirementRow struct {
	id       string
	ats      []string
	sections []string
	mutants  []string
}

type auditTraceMutantRow struct {
	id             string
	defect         string
	positive       string
	relatedATs     []string
	owner          string
	reservedSymbol string
}

type auditTraceActivationRow struct {
	section   string
	packageID string
}

func TestAuditTraceDesignImport(t *testing.T) {
	root := ".."
	design, err := buildAuditTraceDesign(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]byte{
		"audit_trace.tsv":            design.registry,
		"audit_trace_semantics.json": design.semantics,
		"audit_trace_anchor.json":    design.anchor,
	} {
		path := filepath.Join(root, "scripts", "testdata", name)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s differs from the exact design import at byte %d", path, firstAuditTraceDifference(got, want))
		}
	}
	if _, err := parseAuditTraceBundle(design.registry, design.semantics, design.anchor); err != nil {
		t.Fatalf("the imported audit trace is invalid: %v", err)
	}
	runAuditTraceImporterAdversarialControls(t, root, design)
}

func TestAuditTraceDesignImportAdversarial(t *testing.T) {
	root := ".."
	design, err := buildAuditTraceDesign(root)
	if err != nil {
		t.Fatal(err)
	}
	runAuditTraceImporterAdversarialControls(t, root, design)
}

func buildAuditTraceDesign(repoRoot string) (auditTraceDesign, error) {
	usecasePath := filepath.Join(repoRoot, ".agents", "artifacts", "usecases", "AUDIT_USECASES.md")
	planPath := filepath.Join(repoRoot, ".agents", "artifacts", "plans", "AUDIT_PLAN.md")
	usecaseBytes, err := os.ReadFile(usecasePath)
	if err != nil {
		return auditTraceDesign{}, fmt.Errorf("read %s: %w", usecasePath, err)
	}
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		return auditTraceDesign{}, fmt.Errorf("read %s: %w", planPath, err)
	}
	return buildAuditTraceDesignSources(string(usecaseBytes), string(planBytes))
}

func buildAuditTraceDesignSources(usecases, plan string) (auditTraceDesign, error) {
	semanticText, semanticKind, err := importAuditTraceSemantics(usecases, plan)
	if err != nil {
		return auditTraceDesign{}, err
	}
	goals, err := importAuditTraceGoalRows(plan)
	if err != nil {
		return auditTraceDesign{}, err
	}
	requirements, err := importAuditTraceRequirementRows(plan)
	if err != nil {
		return auditTraceDesign{}, err
	}
	mutants, err := importAuditTraceMutantRows(plan)
	if err != nil {
		return auditTraceDesign{}, err
	}
	sections, err := importAuditTraceSections(plan)
	if err != nil {
		return auditTraceDesign{}, err
	}
	packages, err := importAuditTracePackages(plan)
	if err != nil {
		return auditTraceDesign{}, err
	}
	activations, err := importAuditTraceActivations(plan)
	if err != nil {
		return auditTraceDesign{}, err
	}
	defaultPackages, packageExceptions, err := importAuditTraceMutantPackages(plan)
	if err != nil {
		return auditTraceDesign{}, err
	}
	if !equalAuditTraceStringMap(defaultPackages, auditTraceMutantPackages) || !equalAuditTraceStringMap(packageExceptions, auditTraceMutantPackageExceptions) {
		return auditTraceDesign{}, fmt.Errorf("mutant routing tables differ from their closed authority")
	}

	records := make([]auditTraceSemanticRecord, 0, len(semanticText))
	for id, text := range semanticText {
		records = append(records, auditTraceSemanticRecord{ID: id, Kind: semanticKind[id], Text: text})
	}
	sort.Slice(records, func(left, right int) bool {
		return compareAuditTraceSemantic(records[left], records[right]) < 0
	})
	semantics := auditTraceSemantics{Schema: auditTraceSemanticsSchema, Records: records}
	semanticsBytes, err := encodeAuditTraceJSON(semantics)
	if err != nil {
		return auditTraceDesign{}, err
	}

	var facts []string
	nodeKinds := make(map[string]string)
	addNode := func(id, kind string) error {
		if previous, exists := nodeKinds[id]; exists {
			return fmt.Errorf("design imports node %s twice as %s and %s", id, previous, kind)
		}
		nodeKinds[id] = kind
		facts = append(facts, strings.Join([]string{"node", id, kind}, "\t"))
		return nil
	}
	for _, record := range records {
		nodeKind, ok := auditTraceNodeKindForSemantic(record.Kind)
		if !ok {
			return auditTraceDesign{}, fmt.Errorf("semantic %s has unsupported kind %s", record.ID, record.Kind)
		}
		if err := addNode(record.ID, nodeKind); err != nil {
			return auditTraceDesign{}, err
		}
		facts = append(facts, strings.Join([]string{"semantic", record.ID, record.Kind, auditTraceSemanticRecordDigest(record)}, "\t"))
	}
	for section, rank := range sections {
		if err := addNode(section, "implementation-section"); err != nil {
			return auditTraceDesign{}, err
		}
		facts = append(facts, fmt.Sprintf("section\t%s\t%d", section, rank))
	}
	for id, item := range packages {
		if err := addNode(id, "package"); err != nil {
			return auditTraceDesign{}, err
		}
		facts = append(facts, strings.Join([]string{"package", id, item.directory, item.pattern, item.importPath, item.profile}, "\t"))
	}

	for goal, required := range goals {
		if semanticKind[goal] != "actor-goal" {
			return auditTraceDesign{}, fmt.Errorf("GoalTrace names unknown actor goal %s", goal)
		}
		for _, requirement := range required {
			if !auditTraceRequirementSemanticKind(semanticKind[requirement]) {
				return auditTraceDesign{}, fmt.Errorf("GoalTrace %s names unknown requirement %s", goal, requirement)
			}
			facts = append(facts, strings.Join([]string{"edge", goal, "requires", requirement}, "\t"))
		}
	}
	for id, kind := range semanticKind {
		if kind == "actor-goal" {
			if _, exists := goals[id]; !exists {
				return auditTraceDesign{}, fmt.Errorf("actor goal %s is absent from GoalTrace", id)
			}
		}
	}

	for requirement, row := range requirements {
		if !auditTraceRequirementSemanticKind(semanticKind[requirement]) {
			return auditTraceDesign{}, fmt.Errorf("RequirementTrace names unknown requirement %s", requirement)
		}
		for _, at := range row.ats {
			if semanticKind[at] != "obligation" {
				return auditTraceDesign{}, fmt.Errorf("RequirementTrace %s names unknown obligation %s", requirement, at)
			}
			facts = append(facts, strings.Join([]string{"edge", requirement, "proved-by", at}, "\t"))
		}
		for _, section := range row.sections {
			if _, exists := sections[section]; !exists {
				return auditTraceDesign{}, fmt.Errorf("RequirementTrace %s names unknown section %s", requirement, section)
			}
			facts = append(facts, strings.Join([]string{"edge", requirement, "implemented-in", section}, "\t"))
		}
		for _, mutant := range row.mutants {
			if _, exists := mutants[mutant]; !exists {
				return auditTraceDesign{}, fmt.Errorf("RequirementTrace %s names unknown mutant %s", requirement, mutant)
			}
			facts = append(facts, strings.Join([]string{"edge", requirement, "guards", mutant}, "\t"))
		}
	}
	for id, kind := range semanticKind {
		if auditTraceRequirementSemanticKind(kind) {
			if _, exists := requirements[id]; !exists {
				return auditTraceDesign{}, fmt.Errorf("requirement %s is absent from RequirementTrace", id)
			}
		}
	}

	for at, activation := range activations {
		if semanticKind[at] != "obligation" {
			return auditTraceDesign{}, fmt.Errorf("activation table names unknown obligation %s", at)
		}
		facts = append(facts, strings.Join([]string{"edge", at, "activated-in", activation.section}, "\t"))
		reservationID := "RT-C-" + strings.ReplaceAll(at, "-", "") + "-" + activation.section
		if err := addNode(reservationID, "test-reservation"); err != nil {
			return auditTraceDesign{}, err
		}
		facts = append(facts,
			strings.Join([]string{"edge", at, "reserved-by", reservationID}, "\t"),
			strings.Join([]string{"test", reservationID, "Test" + strings.ReplaceAll(at, "-", "") + activation.section + "Contract", activation.section, activation.packageID, "coverage"}, "\t"),
		)
	}
	for id, kind := range semanticKind {
		if kind == "obligation" {
			if _, exists := activations[id]; !exists {
				return auditTraceDesign{}, fmt.Errorf("obligation %s is absent from activation table", id)
			}
		}
	}

	for mutant, row := range mutants {
		primaryAT, err := auditTracePrimaryAT(row)
		if err != nil {
			return auditTraceDesign{}, err
		}
		if !containsAuditTraceID(row.relatedATs, primaryAT) {
			return auditTraceDesign{}, fmt.Errorf("mutant %s primary obligation %s is absent from Related ATs", mutant, primaryAT)
		}
		packageID := defaultPackages[row.owner]
		if exception := packageExceptions[mutant]; exception != "" {
			packageID = exception
		}
		if packageID == "" || packages[packageID].id == "" {
			return auditTraceDesign{}, fmt.Errorf("mutant %s has no resolved package", mutant)
		}
		suffix := strings.TrimPrefix(mutant, "AM-")
		positive := "PN-" + suffix
		mutantReservation := "RT-M-" + suffix
		positiveReservation := "RT-P-" + suffix
		if nodeKinds[positive] != "positive-neighbor" {
			return auditTraceDesign{}, fmt.Errorf("mutant %s has no positive semantic node", mutant)
		}
		for _, idKind := range [][2]string{{mutantReservation, "test-reservation"}, {positiveReservation, "test-reservation"}} {
			if err := addNode(idKind[0], idKind[1]); err != nil {
				return auditTraceDesign{}, err
			}
		}
		positiveSymbol := "TestAuditTraceCheckpointPreservesCompleteGraph"
		if mutant != "AM-TRACE-001" {
			if strings.Count(row.reservedSymbol, "Kills") != 1 {
				return auditTraceDesign{}, fmt.Errorf("mutant %s symbol must contain exactly one Kills token", mutant)
			}
			positiveSymbol = strings.Replace(row.reservedSymbol, "Kills", "Preserves", 1)
		}
		facts = append(facts,
			strings.Join([]string{"edge", mutant, "killed-by", primaryAT}, "\t"),
			strings.Join([]string{"edge", mutant, "owned-by", row.owner}, "\t"),
			strings.Join([]string{"edge", mutant, "reserved-by", mutantReservation}, "\t"),
			strings.Join([]string{"edge", mutant, "contrasted-by", positive}, "\t"),
			strings.Join([]string{"edge", positive, "proved-by", primaryAT}, "\t"),
			strings.Join([]string{"edge", positive, "reserved-by", positiveReservation}, "\t"),
			strings.Join([]string{"test", mutantReservation, row.reservedSymbol, row.owner, packageID, "mutant"}, "\t"),
			strings.Join([]string{"test", positiveReservation, positiveSymbol, row.owner, packageID, "positive"}, "\t"),
		)
	}

	registryBytes := auditTraceCanonicalRegistry(facts)
	registry, err := parseAuditTraceRegistry(registryBytes)
	if err != nil {
		return auditTraceDesign{}, fmt.Errorf("parse imported registry: %w", err)
	}
	if err := validateAuditTraceGraph(registry, semantics); err != nil {
		return auditTraceDesign{}, fmt.Errorf("validate imported registry: %w", err)
	}
	if err := validateAuditTraceFrozenCounts(plan, registry, semantics); err != nil {
		return auditTraceDesign{}, err
	}
	anchor, err := buildAuditTraceAnchor(registry, semantics)
	if err != nil {
		return auditTraceDesign{}, err
	}
	anchorBytes, err := encodeAuditTraceJSON(anchor)
	if err != nil {
		return auditTraceDesign{}, err
	}
	return auditTraceDesign{registry: registryBytes, semantics: semanticsBytes, anchor: anchorBytes}, nil
}

func runAuditTraceImporterAdversarialControls(t *testing.T, repoRoot string, baseline auditTraceDesign) {
	t.Helper()
	usecasePath := filepath.Join(repoRoot, ".agents", "artifacts", "usecases", "AUDIT_USECASES.md")
	planPath := filepath.Join(repoRoot, ".agents", "artifacts", "plans", "AUDIT_PLAN.md")
	usecaseBytes, err := os.ReadFile(usecasePath)
	if err != nil {
		t.Fatal(err)
	}
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	usecases, plan := string(usecaseBytes), string(planBytes)
	type control struct {
		name           string
		usecaseOld     string
		usecaseNew     string
		planOld        string
		planNew        string
		requireRefusal bool
	}
	controls := []control{
		{name: "section rank", planOld: "| S4 | 3 |", planNew: "| S4 | 4 |", requireRefusal: true},
		{name: "duplicate section", planOld: "| S4 | 3 |", planNew: "| S4 | 3 |\n| S4 | 3 |", requireRefusal: true},
		{name: "package directory", planOld: "| PKG-SCRIPTS | `.` | `./scripts` |", planNew: "| PKG-SCRIPTS | `audit` | `./scripts` |"},
		{name: "package pattern", planOld: "| PKG-SCRIPTS | `.` | `./scripts` |", planNew: "| PKG-SCRIPTS | `.` | `./wrong` |"},
		{name: "package import", planOld: "| PKG-SCRIPTS | `.` | `./scripts` | `github.com/frostgrove/vv/scripts` |", planNew: "| PKG-SCRIPTS | `.` | `./scripts` | `github.com/frostgrove/vv/wrong` |"},
		{name: "package profile", planOld: "| PKG-SCRIPTS | `.` | `./scripts` | `github.com/frostgrove/vv/scripts` | unit |", planNew: "| PKG-SCRIPTS | `.` | `./scripts` | `github.com/frostgrove/vv/scripts` | integration |"},
		{name: "duplicate package", planOld: "| PKG-SCRIPTS | `.` | `./scripts` | `github.com/frostgrove/vv/scripts` | unit |", planNew: "| PKG-SCRIPTS | `.` | `./scripts` | `github.com/frostgrove/vv/scripts` | unit |\n| PKG-SCRIPTS | `.` | `./scripts` | `github.com/frostgrove/vv/scripts` | unit |", requireRefusal: true},
		{name: "activation rank", planOld: "| AT-017 | S0 | PKG-SCRIPTS |", planNew: "| AT-017 | S1 | PKG-SCRIPTS |"},
		{name: "activation package", planOld: "| AT-017 | S0 | PKG-SCRIPTS |", planNew: "| AT-017 | S0 | PKG-AUDIT |"},
		{name: "duplicate activation", planOld: "| AT-017 | S0 | PKG-SCRIPTS |", planNew: "| AT-017 | S0 | PKG-SCRIPTS |\n| AT-017 | S0 | PKG-SCRIPTS |", requireRefusal: true},
		{name: "default route", planOld: "\n| S0 | PKG-SCRIPTS |\n", planNew: "\n| S0 | PKG-AUDIT |\n"},
		{name: "duplicate default route", planOld: "\n| S0 | PKG-SCRIPTS |\n", planNew: "\n| S0 | PKG-SCRIPTS |\n| S0 | PKG-SCRIPTS |\n", requireRefusal: true},
		{name: "exception route", planOld: "| AM-OBS-001 | PKG-AUDITFLOW |", planNew: "| AM-OBS-001 | PKG-SCRIPTS |"},
		{name: "duplicate exception route", planOld: "| AM-OBS-001 | PKG-AUDITFLOW |", planNew: "| AM-OBS-001 | PKG-AUDITFLOW |\n| AM-OBS-001 | PKG-AUDITFLOW |", requireRefusal: true},
		{name: "node count", planOld: "| actor-goal | 11 |", planNew: "| actor-goal | 12 |", requireRefusal: true},
		{name: "duplicate node count", planOld: "| actor-goal | 11 |", planNew: "| actor-goal | 11 |\n| actor-goal | 11 |", requireRefusal: true},
		{name: "fact count", planOld: "| node | 642 |", planNew: "| node | 643 |", requireRefusal: true},
		{name: "duplicate fact count", planOld: "| node | 642 |", planNew: "| node | 642 |\n| node | 642 |", requireRefusal: true},
		{name: "node total", planOld: "| total | 642 |", planNew: "| total | 641 |", requireRefusal: true},
		{name: "fact total", planOld: "| total non-schema facts | 4172 |", planNew: "| total non-schema facts | 4171 |", requireRefusal: true},
		{name: "duplicate goal", planOld: "| AU-011 | AH-082,", planNew: "| AU-011 | AH-082,", requireRefusal: false},
		{name: "duplicate requirement", planOld: "| AI-083 | AT-007, AT-010, AT-014, AT-018 |", planNew: "| AI-083 | AT-007, AT-010, AT-014, AT-018 |", requireRefusal: false},
		{name: "duplicate mutant", planOld: "| AM-TRACE-001 | Omit, duplicate, range-collapse", planNew: "| AM-TRACE-001 | Omit, duplicate, range-collapse", requireRefusal: false},
		{name: "semantic digest input", usecaseOld: "A valid resource declaration seals on first use", usecaseNew: "A changed resource declaration seals on first use"},
		{name: "obligation digest input", planOld: "A stdlib-only `scripts/audit-trace.sh S<N>` checkpoint parses", planNew: "A stdlib-only `scripts/audit-trace.sh S<N>` checkpoint strictly parses"},
	}
	for _, item := range controls {
		t.Run(item.name, func(t *testing.T) {
			mutatedUsecases, mutatedPlan := usecases, plan
			if item.usecaseOld != "" {
				mutatedUsecases = replaceAuditTraceDesignOnce(t, mutatedUsecases, item.usecaseOld, item.usecaseNew)
			}
			if item.planOld != "" {
				if item.planOld == item.planNew {
					lineStart := strings.Index(mutatedPlan, item.planOld)
					if lineStart < 0 {
						t.Fatalf("control marker %q is missing", item.planOld)
					}
					lineEnd := strings.IndexByte(mutatedPlan[lineStart:], '\n')
					if lineEnd < 0 {
						t.Fatal("goal row has no terminal LF")
					}
					row := mutatedPlan[lineStart : lineStart+lineEnd]
					mutatedPlan = mutatedPlan[:lineStart] + row + "\n" + mutatedPlan[lineStart:]
					item.requireRefusal = true
				} else {
					mutatedPlan = replaceAuditTraceDesignOnce(t, mutatedPlan, item.planOld, item.planNew)
				}
			}
			got, err := buildAuditTraceDesignSources(mutatedUsecases, mutatedPlan)
			if item.requireRefusal {
				if err == nil {
					t.Fatal("the importer accepted a duplicate or contradictory singleton authority")
				}
				return
			}
			if err != nil {
				return
			}
			if bytes.Equal(got.registry, baseline.registry) && bytes.Equal(got.semantics, baseline.semantics) && bytes.Equal(got.anchor, baseline.anchor) {
				t.Fatal("the importer produced unchanged authorities for a hostile source change")
			}
		})
	}
}

func replaceAuditTraceDesignOnce(t *testing.T, source, old, replacement string) string {
	t.Helper()
	if strings.Count(source, old) != 1 {
		t.Fatalf("control marker %q occurs %d times", old, strings.Count(source, old))
	}
	return strings.Replace(source, old, replacement, 1)
}

func importAuditTraceSemantics(usecases, plan string) (map[string]string, map[string]string, error) {
	text := make(map[string]string)
	kinds := make(map[string]string)
	add := func(id, kind, value string) error {
		if value == "" {
			return fmt.Errorf("semantic %s is empty", id)
		}
		if _, exists := text[id]; exists {
			return fmt.Errorf("semantic %s appears twice", id)
		}
		text[id], kinds[id] = value, kind
		return nil
	}

	aus, err := importAuditTraceAUs(usecases)
	if err != nil {
		return nil, nil, err
	}
	for id, value := range aus {
		if err := add(id, "actor-goal", value); err != nil {
			return nil, nil, err
		}
	}
	requirements, err := importAuditTraceRequirements(usecases)
	if err != nil {
		return nil, nil, err
	}
	for id, value := range requirements {
		kind := map[string]string{"AH": "happy", "AE": "edge", "AI": "invariant"}[id[:2]]
		if err := add(id, kind, value); err != nil {
			return nil, nil, err
		}
	}
	ats, err := importAuditTraceATs(plan)
	if err != nil {
		return nil, nil, err
	}
	for id, value := range ats {
		if err := add(id, "obligation", value); err != nil {
			return nil, nil, err
		}
	}
	mutants, err := importAuditTraceMutantRows(plan)
	if err != nil {
		return nil, nil, err
	}
	for id, row := range mutants {
		if err := add(id, "defect", row.defect); err != nil {
			return nil, nil, err
		}
		if err := add("PN-"+strings.TrimPrefix(id, "AM-"), "positive", row.positive); err != nil {
			return nil, nil, err
		}
	}
	return text, kinds, nil
}

func importAuditTraceAUs(source string) (map[string]string, error) {
	lines := strings.Split(source, "\n")
	result := make(map[string]string)
	var id string
	var fragments []string
	next := 1
	flush := func() error {
		if id == "" {
			return nil
		}
		value := normalizeAuditTraceFragments(fragments)
		if value == "" {
			return fmt.Errorf("actor goal %s is empty", id)
		}
		if _, exists := result[id]; exists {
			return fmt.Errorf("actor goal %s appears twice", id)
		}
		result[id] = value
		return nil
	}
	for _, line := range lines {
		if match := auditTraceAUHeading.FindStringSubmatch(line); match != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			want := fmt.Sprintf("AU-%03d", next)
			if match[1] != want {
				return nil, fmt.Errorf("actor goals expected %s, found %s", want, match[1])
			}
			next++
			id, fragments = match[1], []string{match[2]}
			continue
		}
		if id != "" && (strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ")) {
			if err := flush(); err != nil {
				return nil, err
			}
			id, fragments = "", nil
			continue
		}
		if id != "" {
			fragments = append(fragments, line)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := validateAuditTraceNumericSequence(result, "AU"); err != nil {
		return nil, err
	}
	return result, nil
}

func importAuditTraceRequirements(source string) (map[string]string, error) {
	lines := strings.Split(source, "\n")
	result := make(map[string]string)
	var id string
	var fragments []string
	next := map[string]int{"AH": 1, "AE": 1, "AI": 1}
	flush := func() error {
		if id == "" {
			return nil
		}
		value := normalizeAuditTraceFragments(fragments)
		if value == "" {
			return fmt.Errorf("requirement %s is empty", id)
		}
		if _, exists := result[id]; exists {
			return fmt.Errorf("requirement %s appears twice", id)
		}
		result[id] = value
		return nil
	}
	for _, line := range lines {
		if match := auditTraceRequirementItem.FindStringSubmatch(line); match != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			prefix := match[2][:2]
			want := fmt.Sprintf("%s-%03d", prefix, next[prefix])
			if match[2] != want {
				return nil, fmt.Errorf("requirements expected %s, found %s", want, match[2])
			}
			next[prefix]++
			id = match[2]
			first := match[4]
			if match[3] != "" {
				first = match[3] + ": " + first
			}
			fragments = []string{first}
			continue
		}
		if id != "" && (strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ")) {
			if err := flush(); err != nil {
				return nil, err
			}
			id, fragments = "", nil
			continue
		}
		if id != "" {
			fragments = append(fragments, line)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	for _, prefix := range []string{"AH", "AE", "AI"} {
		if err := validateAuditTraceNumericSequence(result, prefix); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func importAuditTraceATs(source string) (map[string]string, error) {
	lines := strings.Split(source, "\n")
	result := make(map[string]string)
	var id string
	var fragments []string
	next := 1
	flush := func() error {
		if id == "" {
			return nil
		}
		value := normalizeAuditTraceFragments(fragments)
		if value == "" {
			return fmt.Errorf("obligation %s is empty", id)
		}
		if _, exists := result[id]; exists {
			return fmt.Errorf("obligation %s appears twice", id)
		}
		result[id] = value
		return nil
	}
	for _, line := range lines {
		if match := auditTraceATItem.FindStringSubmatch(line); match != nil {
			if err := flush(); err != nil {
				return nil, err
			}
			want := fmt.Sprintf("AT-%03d", next)
			if match[1] != want {
				return nil, fmt.Errorf("obligations expected %s, found %s", want, match[1])
			}
			next++
			id, fragments = match[1], []string{match[2]}
			continue
		}
		if id != "" && strings.HasPrefix(line, "## ") {
			if err := flush(); err != nil {
				return nil, err
			}
			id, fragments = "", nil
			continue
		}
		if id != "" {
			fragments = append(fragments, line)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := validateAuditTraceNumericSequence(result, "AT"); err != nil {
		return nil, err
	}
	return result, nil
}

func normalizeAuditTraceFragments(fragments []string) string {
	result := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		fragment = strings.Trim(fragment, " ")
		if fragment != "" {
			result = append(result, fragment)
		}
	}
	return strings.Join(result, " ")
}

func validateAuditTraceNumericSequence(values map[string]string, prefix string) error {
	var ids []string
	for id := range values {
		if strings.HasPrefix(id, prefix+"-") {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return fmt.Errorf("design has no %s semantic markers", prefix)
	}
	for index, id := range ids {
		want := fmt.Sprintf("%s-%03d", prefix, index+1)
		if id != want {
			return fmt.Errorf("design semantic sequence expected %s, found %s", want, id)
		}
	}
	return nil
}

func importAuditTraceGoalRows(plan string) (map[string][]string, error) {
	section, err := auditTraceBetween(plan, "### GoalTrace", "### RequirementTrace")
	if err != nil {
		return nil, err
	}
	rows := make(map[string][]string)
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| AU-") {
			continue
		}
		cells, err := splitAuditTraceMarkdownRow(line, 2)
		if err != nil {
			return nil, err
		}
		if _, exists := rows[cells[0]]; exists {
			return nil, fmt.Errorf("GoalTrace duplicates %s", cells[0])
		}
		rows[cells[0]], err = splitAuditTraceIDs(cells[1], "A")
		if err != nil {
			return nil, fmt.Errorf("GoalTrace %s: %w", cells[0], err)
		}
	}
	return rows, validateAuditTraceTableOrder(rows, "AU")
}

func importAuditTraceRequirementRows(plan string) (map[string]auditTraceRequirementRow, error) {
	section, err := auditTraceBetween(plan, "### RequirementTrace", "### MutantTrace")
	if err != nil {
		return nil, err
	}
	rows := make(map[string]auditTraceRequirementRow)
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| AH-") && !strings.HasPrefix(line, "| AE-") && !strings.HasPrefix(line, "| AI-") {
			continue
		}
		cells, err := splitAuditTraceMarkdownRow(line, 4)
		if err != nil {
			return nil, err
		}
		if _, exists := rows[cells[0]]; exists {
			return nil, fmt.Errorf("RequirementTrace duplicates %s", cells[0])
		}
		ats, err := splitAuditTraceIDs(cells[1], "AT")
		if err != nil {
			return nil, err
		}
		sections, err := splitAuditTraceIDs(cells[2], "S")
		if err != nil {
			return nil, err
		}
		mutants, err := splitAuditTraceIDs(cells[3], "AM")
		if err != nil {
			return nil, err
		}
		rows[cells[0]] = auditTraceRequirementRow{id: cells[0], ats: ats, sections: sections, mutants: mutants}
	}
	for _, prefix := range []string{"AH", "AE", "AI"} {
		if err := validateAuditTraceTableOrder(rows, prefix); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func importAuditTraceMutantRows(plan string) (map[string]auditTraceMutantRow, error) {
	section, err := auditTraceBetween(plan, "### MutantTrace", "## 5. Stop conditions")
	if err != nil {
		return nil, err
	}
	rows := make(map[string]auditTraceMutantRow)
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| AM-") {
			continue
		}
		cells, err := splitAuditTraceMarkdownRow(line, 6)
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(cells[1], `\|`) || strings.ContainsAny(cells[2], `\|`) {
			return nil, fmt.Errorf("MutantTrace semantic cells for %s contain a pipe or backslash", cells[0])
		}
		if _, exists := rows[cells[0]]; exists {
			return nil, fmt.Errorf("MutantTrace duplicates %s", cells[0])
		}
		related, err := splitAuditTraceIDs(cells[3], "AT")
		if err != nil {
			return nil, err
		}
		symbol, err := unwrapAuditTraceCode(cells[5])
		if err != nil {
			return nil, err
		}
		rows[cells[0]] = auditTraceMutantRow{id: cells[0], defect: cells[1], positive: cells[2], relatedATs: related, owner: cells[4], reservedSymbol: symbol}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("MutantTrace has no rows")
	}
	return rows, nil
}

func importAuditTraceSections(plan string) (map[string]uint64, error) {
	section, err := auditTraceBetween(plan, "The section-row delivery authority is exact:", "The exact AT activation")
	if err != nil {
		return nil, err
	}
	rows := make(map[string]uint64)
	for _, line := range strings.Split(section, "\n") {
		if len(line) < 5 || !strings.HasPrefix(line, "| S") || line[3] < '0' || line[3] > '7' || line[4] != ' ' {
			continue
		}
		cells, err := splitAuditTraceMarkdownRow(line, 2)
		if err != nil {
			return nil, err
		}
		if _, exists := rows[cells[0]]; exists {
			return nil, fmt.Errorf("section delivery table duplicates %s", cells[0])
		}
		rank, err := parseAuditTraceUint(cells[1])
		if err != nil {
			return nil, err
		}
		rows[cells[0]] = rank
	}
	return rows, nil
}

func importAuditTracePackages(plan string) (map[string]auditTracePackage, error) {
	section, err := auditTraceBetween(plan, "The frozen package records and reservation routing are:", "Package profiles are a closed enum")
	if err != nil {
		return nil, err
	}
	rows := make(map[string]auditTracePackage)
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| PKG-") {
			continue
		}
		cells, err := splitAuditTraceMarkdownRow(line, 5)
		if err != nil {
			return nil, err
		}
		for index := 1; index < 4; index++ {
			cells[index], err = unwrapAuditTraceCode(cells[index])
			if err != nil {
				return nil, err
			}
		}
		if _, exists := rows[cells[0]]; exists {
			return nil, fmt.Errorf("package routing table duplicates %s", cells[0])
		}
		rows[cells[0]] = auditTracePackage{id: cells[0], directory: cells[1], pattern: cells[2], importPath: cells[3], profile: cells[4]}
	}
	return rows, nil
}

func importAuditTraceActivations(plan string) (map[string]auditTraceActivationRow, error) {
	section, err := auditTraceBetween(plan, "The exact AT activation and coverage routing authority", "Mutant and positive reservations use")
	if err != nil {
		return nil, err
	}
	rows := make(map[string]auditTraceActivationRow)
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| AT-") {
			continue
		}
		cells, err := splitAuditTraceMarkdownRow(line, 3)
		if err != nil {
			return nil, err
		}
		if _, exists := rows[cells[0]]; exists {
			return nil, fmt.Errorf("AT activation table duplicates %s", cells[0])
		}
		rows[cells[0]] = auditTraceActivationRow{section: cells[1], packageID: cells[2]}
	}
	return rows, validateAuditTraceTableOrder(rows, "AT")
}

func importAuditTraceMutantPackages(plan string) (map[string]string, map[string]string, error) {
	section, err := auditTraceBetween(plan, "Mutant and positive reservations use", "The registry stores each table row")
	if err != nil {
		return nil, nil, err
	}
	defaults := make(map[string]string)
	exceptions := make(map[string]string)
	for _, line := range strings.Split(section, "\n") {
		if len(line) >= 5 && strings.HasPrefix(line, "| S") && line[3] >= '0' && line[3] <= '7' && line[4] == ' ' {
			cells, err := splitAuditTraceMarkdownRow(line, 2)
			if err != nil {
				return nil, nil, err
			}
			if _, exists := defaults[cells[0]]; exists {
				return nil, nil, fmt.Errorf("mutant package defaults duplicate %s", cells[0])
			}
			defaults[cells[0]] = cells[1]
		}
		if strings.HasPrefix(line, "| AM-") {
			cells, err := splitAuditTraceMarkdownRow(line, 2)
			if err != nil {
				return nil, nil, err
			}
			if _, exists := exceptions[cells[0]]; exists {
				return nil, nil, fmt.Errorf("mutant package exceptions duplicate %s", cells[0])
			}
			exceptions[cells[0]] = cells[1]
		}
	}
	return defaults, exceptions, nil
}

func validateAuditTraceFrozenCounts(plan string, registry *auditTraceRegistry, semantics auditTraceSemantics) error {
	section, err := auditTraceBetween(plan, "The frozen pre-S0 completeness counts", "Thus the semantic manifest")
	if err != nil {
		return err
	}
	wantKinds := make(map[string]uint64)
	wantGraph := make(map[string]uint64)
	var wantNodes, wantFacts, wantSemantics uint64
	graphTable := false
	nodeTotalSet := false
	graphTotalSet := false
	if strings.Count(section, "| NodeKind | Count |") != 1 || strings.Count(section, "| GraphFactKind | Count |") != 1 {
		return fmt.Errorf("frozen count tables must each appear exactly once")
	}
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "| GraphFactKind") {
			graphTable = true
			continue
		}
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| NodeKind") || strings.HasPrefix(line, "|---") {
			continue
		}
		cells, err := splitAuditTraceMarkdownRow(line, 2)
		if err != nil {
			return err
		}
		count, err := parseAuditTraceUint(cells[1])
		if err != nil {
			return err
		}
		if _, known := map[string]bool{
			"actor-goal": true, "happy-requirement": true, "edge-requirement": true, "invariant": true,
			"test-obligation": true, "implementation-section": true, "defect-mutant": true,
			"positive-neighbor": true, "test-reservation": true, "package": true,
		}[cells[0]]; known && !graphTable {
			if _, exists := wantKinds[cells[0]]; exists {
				return fmt.Errorf("frozen node counts duplicate %s", cells[0])
			}
			wantKinds[cells[0]] = count
			continue
		}
		if graphTable && cells[0] != "total non-schema facts" {
			if _, exists := wantGraph[cells[0]]; exists {
				return fmt.Errorf("frozen graph counts duplicate %s", cells[0])
			}
			wantGraph[cells[0]] = count
		}
		switch cells[0] {
		case "total":
			if graphTable || nodeTotalSet {
				return fmt.Errorf("frozen node counts duplicate or misplace total")
			}
			nodeTotalSet = true
			wantNodes = count
		case "total non-schema facts":
			if !graphTable || graphTotalSet {
				return fmt.Errorf("frozen graph counts duplicate or misplace total")
			}
			graphTotalSet = true
			wantFacts = count
		case "semantic":
			wantSemantics = count
		default:
			if !graphTable {
				return fmt.Errorf("unknown frozen node count %s", cells[0])
			}
		}
	}
	if !nodeTotalSet || !graphTotalSet {
		return fmt.Errorf("frozen counts omit a total")
	}
	actualKinds := make(map[string]uint64)
	for _, kind := range registry.nodes {
		actualKinds[kind]++
	}
	actualGraph := make(map[string]uint64)
	for _, fact := range registry.facts {
		label, err := auditTraceGraphFactLabel(fact, registry)
		if err != nil {
			return err
		}
		actualGraph[label]++
	}
	if !equalAuditTraceCounts(wantKinds, actualKinds) || !equalAuditTraceCounts(wantGraph, actualGraph) || wantNodes != uint64(len(registry.nodes)) || wantFacts != uint64(len(registry.facts)) || wantSemantics != uint64(len(semantics.Records)) {
		return fmt.Errorf("recomputed audit trace counts disagree with the frozen plan counts: kinds want=%v got=%v graph want=%v got=%v nodes want=%d got=%d facts want=%d got=%d semantics want=%d got=%d", wantKinds, actualKinds, wantGraph, actualGraph, wantNodes, len(registry.nodes), wantFacts, len(registry.facts), wantSemantics, len(semantics.Records))
	}
	return nil
}

func auditTraceGraphFactLabel(fact string, registry *auditTraceRegistry) (string, error) {
	fields := strings.Split(fact, "\t")
	switch fields[0] {
	case "node", "section", "semantic", "test", "package":
		return fields[0], nil
	case "edge":
		fromKind := registry.nodes[fields[1]]
		switch fields[2] {
		case "requires":
			return "actor-goal requires requirement", nil
		case "implemented-in":
			return "requirement implemented-in section", nil
		case "activated-in":
			return "test-obligation activated-in section", nil
		case "guards":
			return "requirement guards defect-mutant", nil
		case "killed-by":
			return "defect-mutant killed-by test-obligation", nil
		case "owned-by":
			return "defect-mutant owned-by section", nil
		case "contrasted-by":
			return "defect-mutant contrasted-by positive-neighbor", nil
		case "proved-by":
			if fromKind == "positive-neighbor" {
				return "positive-neighbor proved-by test-obligation", nil
			}
			return "requirement proved-by test-obligation", nil
		case "reserved-by":
			switch fromKind {
			case "defect-mutant":
				return "defect-mutant reserved-by test-reservation", nil
			case "positive-neighbor":
				return "positive-neighbor reserved-by test-reservation", nil
			case "test-obligation":
				return "test-obligation reserved-by test-reservation", nil
			}
		}
	}
	return "", fmt.Errorf("cannot classify audit trace fact %q", fact)
}

func auditTraceBetween(source, start, end string) (string, error) {
	if strings.Count(source, start) != 1 {
		return "", fmt.Errorf("design marker %q must appear exactly once", start)
	}
	if strings.Count(source, end) != 1 {
		return "", fmt.Errorf("design marker %q must appear exactly once", end)
	}
	startIndex := strings.Index(source, start)
	if startIndex < 0 {
		return "", fmt.Errorf("missing design marker %q", start)
	}
	endIndex := strings.Index(source[startIndex+len(start):], end)
	if endIndex < 0 {
		return "", fmt.Errorf("missing design marker %q", end)
	}
	return source[startIndex : startIndex+len(start)+endIndex], nil
}

func splitAuditTraceMarkdownRow(line string, count int) ([]string, error) {
	if strings.Count(line, "|") != count+1 {
		return nil, fmt.Errorf("malformed trace table row %q", line)
	}
	parts := strings.Split(line, "|")
	if len(parts) != count+2 || parts[0] != "" || parts[len(parts)-1] != "" {
		return nil, fmt.Errorf("malformed trace table row %q", line)
	}
	cells := make([]string, count)
	for index := range cells {
		value := parts[index+1]
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		if strings.HasSuffix(value, " ") {
			value = value[:len(value)-1]
		}
		if value == "" || strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") {
			return nil, fmt.Errorf("trace table row has noncanonical cell %q", line)
		}
		cells[index] = value
	}
	return cells, nil
}

func splitAuditTraceIDs(value, prefix string) ([]string, error) {
	ids := strings.Split(value, ", ")
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		matchesPrefix := strings.HasPrefix(id, prefix+"-")
		if prefix == "A" {
			matchesPrefix = strings.HasPrefix(id, "AH-") || strings.HasPrefix(id, "AE-") || strings.HasPrefix(id, "AI-")
		} else if prefix == "S" {
			matchesPrefix = len(id) == 2 && id[0] == 'S' && id[1] >= '0' && id[1] <= '7'
		}
		if id == "" || !matchesPrefix {
			return nil, fmt.Errorf("invalid %s identity list %q", prefix, value)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("identity list %q duplicates %s", value, id)
		}
		seen[id] = struct{}{}
	}
	return ids, nil
}

func unwrapAuditTraceCode(value string) (string, error) {
	if len(value) < 3 || value[0] != '`' || value[len(value)-1] != '`' || strings.Contains(value[1:len(value)-1], "`") {
		return "", fmt.Errorf("trace table value %q is not one code span", value)
	}
	return value[1 : len(value)-1], nil
}

func validateAuditTraceTableOrder[T any](rows map[string]T, prefix string) error {
	ids := make([]string, 0, len(rows))
	for id := range rows {
		if strings.HasPrefix(id, prefix+"-") {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for index, id := range ids {
		want := fmt.Sprintf("%s-%03d", prefix, index+1)
		if id != want {
			return fmt.Errorf("trace table expected %s, found %s", want, id)
		}
	}
	return nil
}

func auditTracePrimaryAT(row auditTraceMutantRow) (string, error) {
	if row.id == "AM-TRACE-001" {
		return "AT-017", nil
	}
	if !strings.HasPrefix(row.reservedSymbol, "TestAT") || len(row.reservedSymbol) < len("TestAT000Kills") {
		return "", fmt.Errorf("mutant %s has invalid reserved symbol %s", row.id, row.reservedSymbol)
	}
	number := row.reservedSymbol[len("TestAT") : len("TestAT")+3]
	if _, err := strconv.ParseUint(number, 10, 16); err != nil || !strings.Contains(row.reservedSymbol, "Kills") {
		return "", fmt.Errorf("mutant %s has invalid reserved symbol %s", row.id, row.reservedSymbol)
	}
	return "AT-" + number, nil
}

func auditTraceNodeKindForSemantic(kind string) (string, bool) {
	switch kind {
	case "actor-goal":
		return "actor-goal", true
	case "happy":
		return "happy-requirement", true
	case "edge":
		return "edge-requirement", true
	case "invariant":
		return "invariant", true
	case "obligation":
		return "test-obligation", true
	case "defect":
		return "defect-mutant", true
	case "positive":
		return "positive-neighbor", true
	default:
		return "", false
	}
}

func auditTraceRequirementSemanticKind(kind string) bool {
	return kind == "happy" || kind == "edge" || kind == "invariant"
}

func containsAuditTraceID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func equalAuditTraceCounts(left, right map[string]uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func equalAuditTraceStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func firstAuditTraceDifference(left, right []byte) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			return index
		}
	}
	return limit
}

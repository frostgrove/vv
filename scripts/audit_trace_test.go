package scripts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type auditTraceGoListPackage struct {
	ImportPath   string
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
}

func TestAuditTraceRegistry(t *testing.T) {
	if _, err := loadAuditTrace(".."); err != nil {
		t.Fatal(err)
	}
}

func TestAT017S0Contract(t *testing.T) {
	bundle, err := loadAuditTrace("..")
	if err != nil {
		t.Fatal(err)
	}
	tests, err := activeAuditTraceTests(bundle.registry, "S0")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"PKG-SCRIPTS\x00TestAT017S0Contract\x00coverage",
		"PKG-SCRIPTS\x00TestAuditTraceCheckpointPreservesCompleteGraph\x00positive",
		"PKG-SCRIPTS\x00TestAuditTraceCheckpointRejectsMissingTest\x00mutant",
	}
	got := make([]string, 0, len(tests))
	for _, test := range tests {
		got = append(got, test.packageID+"\x00"+test.symbol+"\x00"+test.role)
	}
	sort.Strings(got)
	if !equalAuditTraceStrings(got, want) {
		t.Fatalf("S0 active reservations are %q, expected %q", got, want)
	}
	info, err := os.Stat("audit-trace.sh")
	if err != nil {
		t.Fatalf("the executable audit trace checkpoint is missing: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("scripts/audit-trace.sh is not executable")
	}
}

func TestAuditTraceCheckpointRejectsMissingTest(t *testing.T) {
	registry, semantics, anchor := readAuditTraceAuthorityBytes(t)
	line := []byte("test\tRT-M-TRACE-001\tTestAuditTraceCheckpointRejectsMissingTest\tS0\tPKG-SCRIPTS\tmutant\n")
	mutated := bytes.Replace(registry, line, nil, 1)
	if bytes.Equal(mutated, registry) {
		t.Fatal("the missing-test control did not alter the registry")
	}
	if _, err := parseAuditTraceBundle(mutated, semantics, anchor); err == nil {
		t.Fatal("the audit trace accepted a missing executable mutant reservation")
	}
	controls := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "invalid sections", run: TestTraceScriptRejectsInvalidSections},
		{name: "event proof", run: TestTraceEventProofRejectsMissingSkippedDuplicateAndWrongPackage},
		{name: "exact bootstrap", run: TestTraceScriptUsesTheExactBootstrap},
		{name: "closed package commands", run: TestTracePackageCommandsAreClosed},
		{name: "hostile bootstrap events", run: TestTraceRunnerRejectsAdversarialBootstrapEvents},
	}
	for _, control := range controls {
		t.Run(control.name, control.run)
	}
}

func TestAuditTraceCheckpointPreservesCompleteGraph(t *testing.T) {
	registry, semantics, anchor := readAuditTraceAuthorityBytes(t)
	if _, err := parseAuditTraceBundle(registry, semantics, anchor); err != nil {
		t.Fatalf("the complete audit trace was refused: %v", err)
	}
}

func TestTraceCheckpointRunner(t *testing.T) {
	section := os.Getenv("AUDIT_TRACE_SECTION")
	if section == "" {
		return
	}
	bootstrap := os.Getenv("AUDIT_TRACE_BOOTSTRAP_JSON")
	if err := validateAuditTraceTestEvents(bootstrap, "github.com/frostgrove/vv/scripts", []string{"TestAuditTraceRegistry"}); err != nil {
		t.Fatalf("audit trace bootstrap failed: %v", err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := loadAuditTrace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := runAuditTraceReservations(root, bundle.registry, section); err != nil {
		t.Fatal(err)
	}
}

func TestTraceCanonicalSemanticJSON(t *testing.T) {
	manifest := auditTraceSemantics{
		Schema:  auditTraceSemanticsSchema,
		Records: []auditTraceSemanticRecord{{ID: "AH-001", Kind: "happy", Text: "<>&\u2028\u2029\n\t\x01"}},
	}
	got, err := encodeAuditTraceJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n" +
		"  \"schema\": \"audit-trace-semantics/v1\",\n" +
		"  \"records\": [\n" +
		"    {\n" +
		"      \"id\": \"AH-001\",\n" +
		"      \"kind\": \"happy\",\n" +
		"      \"text\": \"<>&\\u2028\\u2029\\n\\t\\u0001\"\n" +
		"    }\n" +
		"  ]\n" +
		"}\n"
	if string(got) != want {
		t.Fatalf("canonical semantic JSON changed:\n%s", got)
	}
}

func TestTraceStrictJSONRefusesAmbiguity(t *testing.T) {
	tests := map[string]string{
		"duplicate": "{\"schema\":\"audit-trace-semantics/v1\",\"schema\":\"audit-trace-semantics/v1\",\"records\":[]}\n",
		"unknown":   "{\"schema\":\"audit-trace-semantics/v1\",\"records\":[],\"extra\":true}\n",
		"trailing":  "{\"schema\":\"audit-trace-semantics/v1\",\"records\":[]} {}\n",
		"cr":        "{\r\n\"schema\":\"audit-trace-semantics/v1\",\"records\":[]}\n",
		"nul":       "{\"schema\":\"audit-trace-semantics/v1\",\"records\":[]}\x00\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAuditTraceSemantics([]byte(input)); err == nil {
				t.Fatalf("strict semantic JSON accepted the %s control", name)
			}
		})
	}
}

func TestTraceRegistryRefusesNoncanonicalBytes(t *testing.T) {
	registry, _, _ := readAuditTraceAuthorityBytes(t)
	mutations := map[string][]byte{
		"missing terminal LF": bytes.TrimSuffix(registry, []byte("\n")),
		"second terminal LF":  append(append([]byte(nil), registry...), '\n'),
		"CR":                  bytes.Replace(registry, []byte("\n"), []byte("\r\n"), 1),
		"NUL":                 append(append([]byte(nil), registry...), 0),
		"unsorted":            swapAuditTraceFacts(registry, 1, 2),
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAuditTraceRegistry(mutation); err == nil {
				t.Fatalf("registry accepted %s", name)
			}
		})
	}
}

func TestTraceAnchorRejectsAuthorityMutation(t *testing.T) {
	registry, semantics, anchor := readAuditTraceAuthorityBytes(t)
	mutations := map[string][3][]byte{
		"semantic body":          {registry, bytes.Replace(semantics, []byte("A valid resource declaration"), []byte("A changed resource declaration"), 1), anchor},
		"semantic fact digest":   {mutateAuditTraceHexAfter(registry, []byte("semantic\tAH-001\thappy\t")), semantics, anchor},
		"graph edge":             {bytes.Replace(registry, []byte("edge\tAU-001\trequires\tAH-003"), []byte("edge\tAU-001\trequires\tAH-004"), 1), semantics, anchor},
		"anchor graph digest":    {registry, semantics, mutateAuditTraceHexAfter(anchor, []byte(`"graph_digest": "`))},
		"anchor fact digest":     {registry, semantics, mutateAuditTraceHexAfter(anchor, []byte("\"fact_digests\": [\n    \""))},
		"anchor semantic digest": {registry, semantics, mutateAuditTraceHexAfter(anchor, []byte("\"digest\": \""))},
		"highest node":           {removeAuditTraceLines(registry, "node\tAU-011\t", "semantic\tAU-011\t", "edge\tAU-011\t"), removeAuditTraceSemanticRecord(semantics, "AU-011"), anchor},
		"package directory":      {bytes.Replace(registry, []byte("package\tPKG-SCRIPTS\t.\t./scripts"), []byte("package\tPKG-SCRIPTS\taudit\t./scripts"), 1), semantics, anchor},
		"package pattern":        {bytes.Replace(registry, []byte("package\tPKG-SCRIPTS\t.\t./scripts"), []byte("package\tPKG-SCRIPTS\t.\t./wrong"), 1), semantics, anchor},
		"package import":         {bytes.Replace(registry, []byte("github.com/frostgrove/vv/scripts\tunit"), []byte("github.com/frostgrove/vv/wrong\tunit"), 1), semantics, anchor},
		"package profile":        {bytes.Replace(registry, []byte("github.com/frostgrove/vv/scripts\tunit"), []byte("github.com/frostgrove/vv/scripts\tintegration"), 1), semantics, anchor},
		"section rank":           {bytes.Replace(registry, []byte("section\tS4\t3"), []byte("section\tS4\t4"), 1), semantics, anchor},
		"role":                   {bytes.Replace(registry, []byte("PKG-SCRIPTS\tmutant\n"), []byte("PKG-SCRIPTS\tcoverage\n"), 1), semantics, anchor},
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(mutation[0], registry) && bytes.Equal(mutation[1], semantics) && bytes.Equal(mutation[2], anchor) {
				t.Fatalf("the %s control changed nothing", name)
			}
			if _, err := parseAuditTraceBundle(mutation[0], mutation[1], mutation[2]); err == nil {
				t.Fatalf("the trace accepted the %s control", name)
			}
		})
	}
}

func TestTraceModelRejectsFactsWithoutNodes(t *testing.T) {
	bundle, err := loadAuditTrace("..")
	if err != nil {
		t.Fatal(err)
	}
	semanticDigest := auditTraceSemanticRecordDigest(auditTraceSemanticRecord{ID: "AH-999", Kind: "happy", Text: "extra"})
	controls := map[string]string{
		"semantic": "semantic\tAH-999\thappy\t" + semanticDigest,
		"package":  "package\tPKG-EXTRA\t.\t./scripts\tgithub.com/frostgrove/vv/scripts\tunit",
		"test":     "test\tRT-M-EXTRA-001\tTestAT017KillsExtraMutant\tS0\tPKG-SCRIPTS\tmutant",
	}
	for name, fact := range controls {
		t.Run(name, func(t *testing.T) {
			content := auditTraceCanonicalRegistry(append(append([]string(nil), bundle.registry.facts...), fact))
			registry, err := parseAuditTraceRegistry(content)
			if err != nil {
				t.Fatalf("the %s control is not a syntactically valid registry: %v", name, err)
			}
			if err := validateAuditTraceGraph(registry, bundle.semantics); err == nil {
				t.Fatalf("the model accepted an extra %s fact without its node", name)
			}
		})
	}
	t.Run("missing S0 row", func(t *testing.T) {
		facts := make([]string, 0, len(bundle.registry.facts))
		for _, fact := range bundle.registry.facts {
			if fact == "section\tS0\t0" {
				facts = append(facts, "section\tSX\t0")
				continue
			}
			facts = append(facts, fact)
		}
		registry, err := parseAuditTraceRegistry(auditTraceCanonicalRegistry(facts))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateAuditTraceGraph(registry, bundle.semantics); err == nil {
			t.Fatal("the model accepted a missing S0 section row through a zero-value lookup")
		}
	})
}

func TestTraceScriptRejectsInvalidSections(t *testing.T) {
	for _, arguments := range [][]string{nil, {"S8"}, {"S0", "S1"}, {"0"}} {
		command := exec.Command("./audit-trace.sh", arguments...)
		output, err := command.CombinedOutput()
		if err == nil {
			t.Fatalf("audit-trace.sh accepted arguments %q:\n%s", arguments, output)
		}
	}
}

func TestTraceEventProofRejectsMissingSkippedDuplicateAndWrongPackage(t *testing.T) {
	const packagePath = "github.com/frostgrove/vv/scripts"
	pass := `{"Action":"pass","Package":"github.com/frostgrove/vv/scripts","Test":"TestAT017S0Contract"}` + "\n"
	if err := validateAuditTraceTestEvents(pass, packagePath, []string{"TestAT017S0Contract"}); err != nil {
		t.Fatalf("one exact pass event was refused: %v", err)
	}
	controls := map[string]string{
		"missing":       `{"Action":"pass","Package":"github.com/frostgrove/vv/scripts"}` + "\n",
		"skipped":       `{"Action":"skip","Package":"github.com/frostgrove/vv/scripts","Test":"TestAT017S0Contract"}` + "\n",
		"failed":        `{"Action":"fail","Package":"github.com/frostgrove/vv/scripts","Test":"TestAT017S0Contract"}` + "\n",
		"duplicate":     pass + pass,
		"wrong package": `{"Action":"pass","Package":"example.com/wrong","Test":"TestAT017S0Contract"}` + "\n",
		"malformed":     "not-json\n",
		"trailing":      pass[:len(pass)-1] + ` {}` + "\n",
		"skipped child": `{"Action":"skip","Package":"github.com/frostgrove/vv/scripts","Test":"TestAT017S0Contract/required"}` + "\n" + pass,
	}
	for name, output := range controls {
		t.Run(name, func(t *testing.T) {
			if err := validateAuditTraceTestEvents(output, packagePath, []string{"TestAT017S0Contract"}); err == nil {
				t.Fatalf("event proof accepted the %s control", name)
			}
		})
	}
}

func TestTraceScriptUsesTheExactBootstrap(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("audit-trace.sh")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "scripts", "audit-trace.sh")
	if err := os.WriteFile(path, script, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "calls")
	goStub := filepath.Join(root, "go")
	stub := "#!/usr/bin/env bash\n" +
		"{\n" +
		"  printf 'CALL\\n'\n" +
		"  for argument in \"$@\"; do printf 'ARG:%s\\n' \"$argument\"; done\n" +
		"  printf 'PWD:%s\\n' \"$PWD\"\n" +
		"  printf 'SECTION:%s\\n' \"${AUDIT_TRACE_SECTION-}\"\n" +
		"  if [[ -n ${AUDIT_TRACE_BOOTSTRAP_JSON-} ]]; then printf 'BOOTSTRAP:set\\n'; else printf 'BOOTSTRAP:unset\\n'; fi\n" +
		"} >> \"$AUDIT_TRACE_STUB_LOG\"\n" +
		"if [[ ${2-} == -json ]]; then printf '{\"Action\":\"pass\",\"Package\":\"github.com/frostgrove/vv/scripts\",\"Test\":\"TestAuditTraceRegistry\"}\\n'; fi\n"
	if err := os.WriteFile(goStub, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(scriptPath string) string {
		t.Helper()
		if err := os.WriteFile(log, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(scriptPath, "S0")
		environment := make([]string, 0, len(os.Environ())+2)
		for _, variable := range os.Environ() {
			if strings.HasPrefix(variable, "AUDIT_TRACE_SECTION=") || strings.HasPrefix(variable, "AUDIT_TRACE_BOOTSTRAP_JSON=") {
				continue
			}
			environment = append(environment, variable)
		}
		command.Env = append(environment, "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"), "AUDIT_TRACE_STUB_LOG="+log)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("fixture script failed: %v\n%s", err, output)
		}
		calls, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		return string(calls)
	}
	want := "CALL\n" +
		"ARG:test\nARG:-json\nARG:-count=1\nARG:-run\nARG:^TestAuditTraceRegistry$\nARG:./scripts\n" +
		"PWD:" + root + "\nSECTION:\nBOOTSTRAP:unset\n" +
		"CALL\nARG:test\nARG:-count=1\nARG:-run\nARG:^TestTraceCheckpointRunner$\nARG:./scripts\n" +
		"PWD:" + root + "\nSECTION:S0\nBOOTSTRAP:set\n"
	if got := run(path); got != want {
		t.Fatalf("audit-trace.sh invocation changed:\n%s", got)
	}
	altered := bytes.Replace(script, []byte("bootstrap=(go test -json -count=1"), []byte("bootstrap=(go test -count=1"), 1)
	if bytes.Equal(altered, script) {
		t.Fatal("the altered-script control changed nothing")
	}
	alteredPath := filepath.Join(root, "scripts", "altered-audit-trace.sh")
	if err := os.WriteFile(alteredPath, altered, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := run(alteredPath); got == want {
		t.Fatal("the exact-bootstrap control accepted an altered script")
	}
}

func TestTracePackageCommandsAreClosed(t *testing.T) {
	unit := auditTracePackage{id: "PKG-SCRIPTS", directory: ".", pattern: "./scripts", importPath: "github.com/frostgrove/vv/scripts", profile: "unit"}
	pattern := "^(?:TestAT017S0Contract)$"
	arguments, err := auditTraceTestArguments(unit, pattern)
	if err != nil {
		t.Fatal(err)
	}
	wantUnit := []string{"test", "-json", "-count=1", "-run", pattern, "./scripts"}
	if !equalAuditTraceStrings(arguments, wantUnit) {
		t.Fatalf("unit arguments are %q, expected %q", arguments, wantUnit)
	}
	integration := auditTracePackage{id: "PKG-AUDITPG", directory: "audit/auditpg", pattern: "./...", importPath: "github.com/frostgrove/vv/audit/auditpg", profile: "integration"}
	t.Setenv("FROSTGROVE_AUDITPG_TEST_DSN", "")
	if _, err := auditTraceTestArguments(integration, pattern); err == nil {
		t.Fatal("integration arguments were built without FROSTGROVE_AUDITPG_TEST_DSN")
	}
	t.Setenv("FROSTGROVE_AUDITPG_TEST_DSN", "postgres://byte-exact")
	arguments, err = auditTraceTestArguments(integration, pattern)
	if err != nil {
		t.Fatal(err)
	}
	wantIntegration := []string{"test", "-json", "-count=1", "-tags=integration", "-run", pattern, "./..."}
	if !equalAuditTraceStrings(arguments, wantIntegration) {
		t.Fatalf("integration arguments are %q, expected %q", arguments, wantIntegration)
	}
	if os.Getenv("FROSTGROVE_AUDITPG_TEST_DSN") != "postgres://byte-exact" {
		t.Fatal("building integration arguments changed the caller's DSN")
	}
}

func TestTraceImporterAdversarialControlsRunInASubprocess(t *testing.T) {
	command := exec.Command("go", "test", "-tags=audit_trace_import", "-run", "^TestAuditTraceDesignImportAdversarial$", "-count=1", "./scripts")
	command.Dir = ".."
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("tagged adversarial importer controls failed: %v\n%s", err, output)
	}
}

func TestTraceRunnerRejectsAdversarialBootstrapEvents(t *testing.T) {
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "skip", "wrong-package", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			stubPath := filepath.Join(directory, "go")
			stub := "#!/usr/bin/env bash\n" +
				"if [[ ${1-} == test && ${2-} == -json && ${4-} == -run && ${5-} == '^TestAuditTraceRegistry$' ]]; then\n" +
				"  case \"$AUDIT_TRACE_EVENT_MODE\" in\n" +
				"    missing) printf '{\"Action\":\"pass\",\"Package\":\"github.com/frostgrove/vv/scripts\"}\\n' ;;\n" +
				"    skip) printf '{\"Action\":\"skip\",\"Package\":\"github.com/frostgrove/vv/scripts\",\"Test\":\"TestAuditTraceRegistry\"}\\n' ;;\n" +
				"    wrong-package) printf '{\"Action\":\"pass\",\"Package\":\"example.com/wrong\",\"Test\":\"TestAuditTraceRegistry\"}\\n' ;;\n" +
				"    duplicate) printf '{\"Action\":\"pass\",\"Package\":\"github.com/frostgrove/vv/scripts\",\"Test\":\"TestAuditTraceRegistry\"}\\n{\"Action\":\"pass\",\"Package\":\"github.com/frostgrove/vv/scripts\",\"Test\":\"TestAuditTraceRegistry\"}\\n' ;;\n" +
				"  esac\n" +
				"  exit 0\n" +
				"fi\n" +
				"exec \"$AUDIT_TRACE_REAL_GO\" \"$@\"\n"
			if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("./audit-trace.sh", "S0")
			command.Env = append(os.Environ(),
				"PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"),
				"AUDIT_TRACE_REAL_GO="+realGo,
				"AUDIT_TRACE_EVENT_MODE="+mode,
			)
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("audit trace runner accepted %s bootstrap events:\n%s", mode, output)
			}
		})
	}
}

func activeAuditTraceTests(registry *auditTraceRegistry, section string) ([]auditTraceTest, error) {
	rank, exists := registry.sections[section]
	if !exists {
		return nil, fmt.Errorf("unknown audit trace checkpoint %q", section)
	}
	var tests []auditTraceTest
	for _, test := range registry.tests {
		activationRank, exists := registry.sections[test.activation]
		if !exists {
			return nil, fmt.Errorf("reservation %s has unknown activation %s", test.id, test.activation)
		}
		if activationRank <= rank {
			tests = append(tests, test)
		}
	}
	sort.Slice(tests, func(left, right int) bool {
		if tests[left].packageID != tests[right].packageID {
			return tests[left].packageID < tests[right].packageID
		}
		if tests[left].symbol != tests[right].symbol {
			return tests[left].symbol < tests[right].symbol
		}
		return tests[left].role < tests[right].role
	})
	return tests, nil
}

func runAuditTraceReservations(root string, registry *auditTraceRegistry, section string) error {
	tests, err := activeAuditTraceTests(registry, section)
	if err != nil {
		return err
	}
	grouped := make(map[string][]auditTraceTest)
	for _, test := range tests {
		grouped[test.packageID] = append(grouped[test.packageID], test)
	}
	packageIDs := make([]string, 0, len(grouped))
	for packageID := range grouped {
		packageIDs = append(packageIDs, packageID)
	}
	sort.Strings(packageIDs)
	for _, packageID := range packageIDs {
		item := registry.packages[packageID]
		if err := validateAuditTraceSourceInventory(root, registry, item); err != nil {
			return err
		}
		symbols := make([]string, 0, len(grouped[packageID]))
		for _, test := range grouped[packageID] {
			symbols = append(symbols, test.symbol)
		}
		sort.Strings(symbols)
		pattern := "^(?:" + strings.Join(quoteAuditTraceSymbols(symbols), "|") + ")$"
		arguments, err := auditTraceTestArguments(item, pattern)
		if err != nil {
			return err
		}
		command := exec.Command("go", arguments...)
		command.Dir = filepath.Join(root, item.directory)
		output, commandErr := command.CombinedOutput()
		if commandErr != nil {
			return fmt.Errorf("audit trace package %s command failed: %w\n%s", packageID, commandErr, output)
		}
		if err := validateAuditTraceTestEvents(string(output), item.importPath, symbols); err != nil {
			return fmt.Errorf("audit trace package %s: %w", packageID, err)
		}
	}
	return nil
}

func auditTraceTestArguments(item auditTracePackage, pattern string) ([]string, error) {
	switch item.profile {
	case "unit":
		return []string{"test", "-json", "-count=1", "-run", pattern, item.pattern}, nil
	case "integration":
		if os.Getenv("FROSTGROVE_AUDITPG_TEST_DSN") == "" {
			return nil, errors.New("FROSTGROVE_AUDITPG_TEST_DSN is required for an integration audit trace checkpoint")
		}
		return []string{"test", "-json", "-count=1", "-tags=integration", "-run", pattern, item.pattern}, nil
	default:
		return nil, fmt.Errorf("unsupported audit trace package profile %q", item.profile)
	}
}

func validateAuditTraceTestEvents(output, importPath string, symbols []string) error {
	events, err := readAuditTraceJSONEvents(output)
	if err != nil {
		return err
	}
	wanted := make(map[string]struct{}, len(symbols))
	passes := make(map[string]int, len(symbols))
	for _, symbol := range symbols {
		wanted[symbol] = struct{}{}
	}
	for _, event := range events {
		matched := ""
		for symbol := range wanted {
			if event.Test == symbol || strings.HasPrefix(event.Test, symbol+"/") {
				matched = symbol
				break
			}
		}
		if matched == "" {
			continue
		}
		if event.Package != importPath {
			return fmt.Errorf("test %s emitted from package %s, expected %s", event.Test, event.Package, importPath)
		}
		switch event.Action {
		case "pass":
			if event.Test == matched {
				passes[matched]++
			}
		case "fail", "skip":
			return fmt.Errorf("test %s emitted %s", event.Test, event.Action)
		}
	}
	for _, symbol := range symbols {
		if passes[symbol] != 1 {
			return fmt.Errorf("test %s emitted %d pass events, expected exactly one", symbol, passes[symbol])
		}
	}
	return nil
}

func validateAuditTraceSourceInventory(root string, registry *auditTraceRegistry, item auditTracePackage) error {
	command := exec.Command("go", "list", "-json", item.pattern)
	command.Dir = filepath.Join(root, item.directory)
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("list audit trace package %s: %w", item.id, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	registered := make(map[string]struct{})
	for _, test := range registry.tests {
		if test.packageID == item.id {
			registered[test.symbol] = struct{}{}
		}
	}
	if item.id == "PKG-SCRIPTS" {
		registered["TestAuditTraceRegistry"] = struct{}{}
	}
	for {
		var listed auditTraceGoListPackage
		if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("decode go list for %s: %w", item.id, err)
		}
		for _, name := range append(append([]string(nil), listed.TestGoFiles...), listed.XTestGoFiles...) {
			path := filepath.Join(listed.Dir, name)
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			for _, declaration := range parsed.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Recv != nil {
					continue
				}
				name := function.Name.Name
				if !auditTraceATSymbol.MatchString(name) && !strings.HasPrefix(name, "TestAuditTrace") {
					continue
				}
				if _, exists := registered[name]; !exists {
					return fmt.Errorf("unregistered audit trace test %s in %s", name, path)
				}
			}
		}
	}
	return nil
}

func quoteAuditTraceSymbols(symbols []string) []string {
	quoted := make([]string, len(symbols))
	for index, symbol := range symbols {
		quoted[index] = regexp.QuoteMeta(symbol)
	}
	return quoted
}

func readAuditTraceAuthorityBytes(t *testing.T) ([]byte, []byte, []byte) {
	t.Helper()
	read := func(name string) []byte {
		content, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return content
	}
	return read("audit_trace.tsv"), read("audit_trace_semantics.json"), read("audit_trace_anchor.json")
}

func swapAuditTraceFacts(registry []byte, left, right int) []byte {
	lines := bytes.Split(append([]byte(nil), registry...), []byte("\n"))
	lines[left], lines[right] = lines[right], lines[left]
	return bytes.Join(lines, []byte("\n"))
}

func removeAuditTraceLines(registry []byte, prefixes ...string) []byte {
	lines := strings.Split(string(registry), "\n")
	kept := lines[:0]
	for _, line := range lines {
		remove := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(line, prefix) {
				remove = true
				break
			}
		}
		if !remove {
			kept = append(kept, line)
		}
	}
	return []byte(strings.Join(kept, "\n"))
}

func removeAuditTraceSemanticRecord(content []byte, id string) []byte {
	manifest, err := parseAuditTraceSemantics(content)
	if err != nil {
		return content
	}
	records := manifest.Records[:0]
	for _, record := range manifest.Records {
		if record.ID != id {
			records = append(records, record)
		}
	}
	manifest.Records = records
	encoded, err := encodeAuditTraceJSON(manifest)
	if err != nil {
		return content
	}
	return encoded
}

func mutateAuditTraceHexAfter(content, marker []byte) []byte {
	mutated := append([]byte(nil), content...)
	index := bytes.Index(mutated, marker)
	if index < 0 || index+len(marker) >= len(mutated) {
		return mutated
	}
	index += len(marker)
	if mutated[index] == '0' {
		mutated[index] = '1'
	} else {
		mutated[index] = '0'
	}
	return mutated
}

func equalAuditTraceStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

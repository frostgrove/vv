package scripts

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	libraryGoMod  = "module github.com/frostgrove/vv\n\ngo 1.26\n"
	librarySource = `package thing

func Name() string { return "vv" }
`
)

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("cannot create %s: %v", filepath.Dir(name), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("cannot create the fixture scripts directory: %v", err)
	}
	for _, script := range []string{"common.sh", "checks.sh"} {
		content, err := os.ReadFile(script)
		if err != nil {
			t.Fatalf("cannot read %s: %v", script, err)
		}
		if err := os.WriteFile(filepath.Join(root, "scripts", script), content, 0o755); err != nil {
			t.Fatalf("cannot copy %s into the fixture: %v", script, err)
		}
	}
	return root
}

func runCheck(t *testing.T, root, check string) (string, int) {
	t.Helper()
	return runCheckWithEnv(t, root, check)
}

func runCheckWithEnv(t *testing.T, root, check string, environment ...string) (string, int) {
	t.Helper()
	command := exec.Command("bash", filepath.Join(root, "scripts", "checks.sh"), check)
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
	command.Env = append(command.Env, environment...)
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("cannot run check-%s: %v\n%s", check, err, output)
	}
	return string(output), exit.ExitCode()
}

func goModFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	found := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "go.mod" {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found[relative] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("cannot read the fixture's go.mod files: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("the fixture has no go.mod at all, so it proves nothing")
	}
	return found
}

func satelliteFixture(t *testing.T, satelliteGoMod string) string {
	t.Helper()
	return fixture(t, map[string]string{
		"go.mod":             libraryGoMod,
		"thing/thing.go":     librarySource,
		"test/go.mod":        "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"test/suite.go":      "package suite\n",
		"_examples/go.mod":   "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"_examples/basic.go": "package basic\n",
		"jobsfx/go.mod":      satelliteGoMod,
		"jobsfx/jobsfx.go": `package jobsfx

import "github.com/frostgrove/vv/thing"

func Name() string { return thing.Name() }
`,
	})
}

const satelliteThatReplacesTheLibrary = `module github.com/frostgrove/vv/jobsfx

go 1.26

require github.com/frostgrove/vv v0.0.0-00010101000000-000000000000

replace github.com/frostgrove/vv => ..
`

func TestCheckTidyGivesBackEveryGoModExactlyAsItFoundIt(t *testing.T) {
	root := satelliteFixture(t, satelliteThatReplacesTheLibrary)
	before := goModFiles(t, root)

	output, code := runCheck(t, root, "tidy")
	if code != 0 {
		t.Fatalf("the fixture is tidy but check-tidy refused it with %d:\n%s", code, output)
	}

	for name, content := range goModFiles(t, root) {
		if content != before[name] {
			t.Errorf("check-tidy rewrote %s:\nbefore:\n%s\nafter:\n%s", name, before[name], content)
		}
	}
}

func TestCheckTidyNamesTheUntidyModuleAndPrintsItsDiff(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":         libraryGoMod,
		"thing/thing.go": librarySource,
		"test/go.mod": `module github.com/frostgrove/vv/test

go 1.26

replace github.com/frostgrove/vv => ../
`,
		"test/use.go": `package test

import "github.com/frostgrove/vv/thing"

var Name = thing.Name()
`,
	})

	output, code := runCheck(t, root, "tidy")
	if code == 0 {
		t.Fatalf("./test imports a package it does not require and check-tidy passed:\n%s", output)
	}
	if !strings.Contains(output, "./test is not tidy") {
		t.Errorf("check-tidy failed without naming the module it failed on:\n%s", output)
	}
	if !strings.Contains(output, "github.com/frostgrove/vv") || !strings.Contains(output, "diff") {
		t.Errorf("check-tidy failed without printing what go mod tidy would change:\n%s", output)
	}
}

func TestCheckTidyDoesNotReadAWarningAsAnUntidyModule(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
	})

	output, code := runCheck(t, root, "tidy")
	if code != 0 {
		t.Fatalf("every module is tidy and check-tidy exited %d:\n%s", code, output)
	}
}

func TestCheckReplacesRefusesASatelliteThatReplacesTheLibrary(t *testing.T) {
	root := satelliteFixture(t, satelliteThatReplacesTheLibrary)

	output, code := runCheck(t, root, "replaces")
	if code != 1 {
		t.Fatalf("a satellite replaces the library and check-replaces exited %d:\n%s", code, output)
	}
	if !strings.Contains(output, "./jobsfx replaces the library it requires") {
		t.Errorf("check-replaces refused without saying which module or why:\n%s", output)
	}
}

func TestCheckReplacesKeepsTheReplaceOfAnUntaggedSibling(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"access/go.mod":    "module github.com/frostgrove/vv/access\n\ngo 1.26\n",
		"access/accessfx/go.mod": `module github.com/frostgrove/vv/access/accessfx

go 1.26

require github.com/frostgrove/vv/access v0.0.0-00010101000000-000000000000

replace github.com/frostgrove/vv/access => ..
`,
	})

	output, code := runCheck(t, root, "replaces")
	if code != 0 {
		t.Fatalf("a replace of an untagged sibling is allowed and check-replaces exited %d:\n%s", code, output)
	}
}

func TestCheckReplacesRefusesAReplaceThatNamesTheWrongDirectory(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"access/go.mod":    "module github.com/frostgrove/vv/access\n\ngo 1.26\n",
		"access/accessfx/go.mod": `module github.com/frostgrove/vv/access/accessfx

go 1.26

require github.com/frostgrove/vv/access v0.0.0-00010101000000-000000000000

replace github.com/frostgrove/vv/access => ../../moved
`,
	})

	output, code := runCheck(t, root, "replaces")
	if code != 1 {
		t.Fatalf("the replace points outside the repository and check-replaces exited %d:\n%s", code, output)
	}
	if !strings.Contains(output, "this repository does not carry") {
		t.Errorf("check-replaces refused without saying what it could not find:\n%s", output)
	}
}

func TestNoPublishedModuleInThisRepositoryReplacesTheLibrary(t *testing.T) {
	command := exec.Command("bash", "checks.sh", "replaces")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("check-replaces refuses this repository:\n%s", output)
	}
}

const libraryGoModOfferingALocalDriver = `module github.com/frostgrove/vv

go 1.26

require example.com/driver v0.0.0

replace example.com/driver => ./driver
`

func rootModuleFixture(t *testing.T, integrationTest string) string {
	t.Helper()
	return fixture(t, map[string]string{
		"go.mod":                          libraryGoModOfferingALocalDriver,
		"driver/go.mod":                   "module example.com/driver\n\ngo 1.26\n",
		"driver/driver.go":                "package driver\n\nfunc Open() string { return \"driver\" }\n",
		"thing/thing.go":                  librarySource,
		"thing/thing_integration_test.go": integrationTest,
	})
}

func TestCheckDepsRefusesAThirdPartyPackageOnlyAnIntegrationTestImports(t *testing.T) {
	root := rootModuleFixture(t, `//go:build integration

package thing

import (
	"testing"

	_ "example.com/driver"
)

func TestTheDriverIsRegistered(t *testing.T) {}
`)

	output, code := runCheck(t, root, "deps")
	if code == 0 {
		t.Fatalf("a test in the root module imports a third-party package and check-deps passed:\n%s", output)
	}
	if !strings.Contains(output, "example.com/driver") {
		t.Errorf("check-deps refused without naming the package the root module would have to require:\n%s", output)
	}
}

func TestCheckDepsPassesWhenATestOfTheRootModuleStaysInTheStandardLibrary(t *testing.T) {
	root := rootModuleFixture(t, `//go:build integration

package thing

import "testing"

func TestTheLibraryNamesItself(t *testing.T) {
	if Name() != "vv" {
		t.Fatal("the fixture library changed under the test")
	}
}
`)

	output, code := runCheck(t, root, "deps")
	if code != 0 {
		t.Fatalf("nothing in the root module leaves the standard library and check-deps exited %d:\n%s", code, output)
	}
}

func TestCheckDepsRefusesTransitiveOpenTelemetryOutsideTheOTelModule(t *testing.T) {
	dependencies := t.TempDir()
	trace := filepath.Join(dependencies, "trace")
	bridge := filepath.Join(dependencies, "bridge")
	for name, content := range map[string]string{
		filepath.Join(trace, "go.mod"):   "module go.opentelemetry.io/otel/trace\n\ngo 1.26\n",
		filepath.Join(trace, "trace.go"): "package trace\n\nconst Enabled = true\n",
		filepath.Join(bridge, "go.mod"): `module example.com/bridge

go 1.26

require go.opentelemetry.io/otel/trace v0.0.0
`,
		filepath.Join(bridge, "bridge.go"): "package bridge\n\nimport \"go.opentelemetry.io/otel/trace\"\n\nvar Enabled = trace.Enabled\n",
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require (
	example.com/bridge v0.0.0
	go.opentelemetry.io/otel/trace v0.0.0 // indirect
)

replace example.com/bridge => ` + bridge + `
replace go.opentelemetry.io/otel/trace => ` + trace + `
`,
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport \"example.com/bridge\"\n\nvar Enabled = bridge.Enabled\n",
	})

	output, code := runCheck(t, root, "deps")
	if code == 0 {
		t.Fatalf("a non-OTel satellite reaches OpenTelemetry through a bridge and check-deps passed:\n%s", output)
	}
	if !strings.Contains(output, "./jobsfx reaches OpenTelemetry") || !strings.Contains(output, "go.opentelemetry.io/otel/trace") {
		t.Fatalf("check-deps did not identify the transitive OpenTelemetry dependency:\n%s", output)
	}
}

func TestCheckDepsRefusesTransitiveVVOTelWithoutAnUpstreamOTelImport(t *testing.T) {
	dependencies := t.TempDir()
	bridge := filepath.Join(dependencies, "bridge")
	writeInto(t, bridge, "go.mod", `module example.com/bridge

go 1.26

require github.com/frostgrove/vv/otel v0.0.0
`)
	writeInto(t, bridge, "bridge.go", `package bridge

import "github.com/frostgrove/vv/otel/fake"

var Enabled = fake.Enabled
`)
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require (
	example.com/bridge v0.0.0
	github.com/frostgrove/vv/otel v0.0.0 // indirect
)

replace example.com/bridge => ` + bridge + `
replace github.com/frostgrove/vv/otel => ../otel
`,
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport \"example.com/bridge\"\n\nvar Enabled = bridge.Enabled\n",
	})
	installOTelDependencyGate(t, root)
	writeInto(t, root, "otel/fake/fake.go", "package fake\n\nconst Enabled = true\n")

	output, code := runCheck(t, root, "deps")
	if code == 0 {
		t.Fatalf("a bridge reaches the forbidden vv/otel module and check-deps passed:\n%s", output)
	}
	if !strings.Contains(output, "./jobsfx reaches forbidden OpenTelemetry production code") || !strings.Contains(output, "github.com/frostgrove/vv/otel/fake") {
		t.Fatalf("check-deps refused without identifying the transitive vv/otel edge:\n%s", output)
	}
}

func TestCheckDepsRefusesADirectVVOTelImport(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require github.com/frostgrove/vv/otel v0.0.0

replace github.com/frostgrove/vv/otel => ../otel
`,
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport _ \"github.com/frostgrove/vv/otel/fake\"\n",
	})
	installOTelDependencyGate(t, root)
	writeInto(t, root, "otel/fake/fake.go", "package fake\n")

	output, code := runCheck(t, root, "deps")
	if code == 0 || !strings.Contains(output, "jobsfx.go -> github.com/frostgrove/vv/otel/fake") {
		t.Fatalf("a direct vv/otel import bypassed the dependency gate with %d:\n%s", code, output)
	}
}

func TestCheckDepsRefusesVVOTelThroughADotlessModule(t *testing.T) {
	bridge := t.TempDir()
	writeInto(t, bridge, "go.mod", "module corp/bridge\n\ngo 1.26\n\nrequire github.com/frostgrove/vv/otel v0.0.0\n")
	writeInto(t, bridge, "bridge.go", "package bridge\n\nimport _ \"github.com/frostgrove/vv/otel/fake\"\n")
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require (
	corp/bridge v0.0.0
	github.com/frostgrove/vv/otel v0.0.0 // indirect
)

replace corp/bridge => ` + bridge + `
replace github.com/frostgrove/vv/otel => ../otel
`,
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport _ \"corp/bridge\"\n",
	})
	installOTelDependencyGate(t, root)
	writeInto(t, root, "otel/fake/fake.go", "package fake\n")

	output, code := runCheck(t, root, "deps")
	if code == 0 || !strings.Contains(output, "corp/bridge") || !strings.Contains(output, "github.com/frostgrove/vv/otel/fake") {
		t.Fatalf("a dotless transitive module bypassed the dependency gate with %d:\n%s", code, output)
	}
}

func TestCheckDepsDoesNotTreatLegacyBuildWithoutBlankAsAConstraint(t *testing.T) {
	bridge := t.TempDir()
	writeInto(t, bridge, "go.mod", "module corp/bridge\n\ngo 1.26\n\nrequire github.com/frostgrove/vv/otel v0.0.0\n")
	writeInto(t, bridge, "bridge.go", "// +build ignore\npackage bridge\n\nimport _ \"github.com/frostgrove/vv/otel/fake\"\n")
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require (
	corp/bridge v0.0.0
	github.com/frostgrove/vv/otel v0.0.0 // indirect
)

replace corp/bridge => ` + bridge + `
replace github.com/frostgrove/vv/otel => ../otel
`,
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport _ \"corp/bridge\"\n",
	})
	installOTelDependencyGate(t, root)
	writeInto(t, root, "otel/fake/fake.go", "package fake\n")

	output, code := runCheck(t, root, "deps")
	if code == 0 || !strings.Contains(output, "corp/bridge") || !strings.Contains(output, "github.com/frostgrove/vv/otel/fake") {
		t.Fatalf("a no-blank legacy comment hid transitive vv/otel with %d:\n%s", code, output)
	}
}

func TestOTelDependencyGateUsesTheToolchainStandardLibraryInventory(t *testing.T) {
	if standardLibraryImport("corp/bridge") {
		t.Fatal("a dotless third-party module was classified as standard library")
	}
	if !standardLibraryImport("encoding/json/jsontext") {
		t.Fatal("a current-toolchain standard-library package missing from the default build was not inventoried")
	}
}

func TestOTelDependencyGraphResolvesAWorkspaceOnlyRootImport(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":         libraryGoMod,
		"thing/thing.go": librarySource,
		"jobsfx/go.mod":  "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n",
		"jobsfx/jobsfx.go": `package jobsfx

import "github.com/frostgrove/vv/thing"

func Name() string { return thing.Name() }
`,
	})
	modules, err := productionModuleGraph(root, filepath.Join(root, "jobsfx"))
	if err != nil {
		t.Fatalf("workspace-only root import cannot be resolved: %v", err)
	}
	directory, ok := importDirectory(modules, "github.com/frostgrove/vv/thing")
	if !ok || directory != filepath.Join(root, "thing") {
		t.Fatalf("root package resolved to %q/%t, want %q/true", directory, ok, filepath.Join(root, "thing"))
	}
}

func TestOTelDependencyGraphDoesNotResolveAnUnusedInvalidRequirement(t *testing.T) {
	t.Setenv("GONOPROXY", "*")
	t.Setenv("GONOSUMDB", "*")
	t.Setenv("GOPRIVATE", "*")
	t.Setenv("GOPROXY", "https://proxy.invalid")
	t.Setenv("GOSUMDB", "sum.invalid")
	t.Setenv("GOVCS", "*:all")
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require example.com/unavailable v0.0.0
`,
		"jobsfx/jobsfx.go": "package jobsfx\n\nconst Enabled = true\n",
	})
	modules, err := productionModuleGraph(root, filepath.Join(root, "jobsfx"))
	if err != nil {
		t.Fatalf("cannot retain the invalid requirement in the module graph: %v", err)
	}
	var retained bool
	for _, module := range modules {
		if module.Path == "example.com/unavailable" && module.Dir == "" && module.Error != nil {
			retained = true
		}
	}
	if !retained {
		t.Fatal("the module graph did not retain the unavailable requirement and its error")
	}

	finding, err := transitiveProductionOTelImport(root, filepath.Join(root, "jobsfx"), make(map[string]packageImports))
	if err != nil {
		t.Fatalf("an unreachable invalid requirement made the offline dependency gate fail: %v", err)
	}
	if finding != "" {
		t.Fatalf("an unreachable invalid requirement produced a forbidden dependency: %s", finding)
	}
}

func TestOTelDependencyGraphPreservesAHigherRootRequirementAndExclusion(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":         libraryGoMod,
		"thing/thing.go": librarySource,
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require github.com/frostgrove/vv v0.9.0

exclude github.com/frostgrove/vv v0.0.0

replace github.com/frostgrove/vv v0.9.0 => ..
`,
		"jobsfx/jobsfx.go": `package jobsfx

import "github.com/frostgrove/vv/thing"

func Name() string { return thing.Name() }
`,
	})
	module := filepath.Join(root, "jobsfx")
	before, err := os.ReadFile(filepath.Join(module, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}

	finding, err := transitiveProductionOTelImport(root, module, make(map[string]packageImports))
	if err != nil {
		t.Fatalf("the repository mapping did not preserve the selected root requirement: %v", err)
	}
	if finding != "" {
		t.Fatalf("the root import produced a forbidden dependency: %s", finding)
	}
	after, err := os.ReadFile(filepath.Join(module, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the dependency gate rewrote the module:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	modules, err := productionModuleGraph(root, module)
	if err != nil {
		t.Fatalf("cannot inspect the selected graph: %v", err)
	}
	var selected bool
	for _, listed := range modules {
		if listed.Path == "github.com/frostgrove/vv" && listed.Version == "v0.9.0" {
			selected = true
		}
		if listed.Path == "github.com/frostgrove/vv" && listed.Version == "v0.0.0" {
			t.Fatalf("the synthetic repository mapping introduced an excluded version: %+v", listed)
		}
	}
	if !selected {
		t.Fatal("the selected module graph did not retain github.com/frostgrove/vv v0.9.0")
	}
}

func TestOTelDependencyGraphResolvesOnlyLocalSentinelRequirementsFromCheckout(t *testing.T) {
	t.Run("root sentinel", func(t *testing.T) {
		root := fixture(t, map[string]string{
			"go.mod":              libraryGoMod,
			"thing/thing.go":      librarySource,
			"jobsfx/go.mod":       "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n",
			"jobsfx/jobsfx.go":    "package jobsfx\n\nimport _ \"github.com/frostgrove/vv/thing\"\n",
			"test/go.mod":         "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
			"_examples/go.mod":    "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
			"testdata/bad/go.mod": "module github.com/frostgrove/vv/bad\n\ngo 1.26\n",
		})
		before := manifestFiles(t, root)

		finding, err := transitiveProductionOTelImport(root, filepath.Join(root, "jobsfx"), make(map[string]packageImports))
		if err != nil || finding != "" {
			t.Fatalf("root sentinel did not resolve clean checkout source: finding=%q err=%v", finding, err)
		}
		assertManifestFiles(t, root, before)
	})

	t.Run("recursive local closure", func(t *testing.T) {
		root := fixture(t, map[string]string{
			"go.mod":           libraryGoMod,
			"thing/thing.go":   librarySource,
			"leaf/go.mod":      "module github.com/frostgrove/vv/leaf\n\ngo 1.26\n",
			"leaf/leaf.go":     "package leaf\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
			"bridge/go.mod":    "module github.com/frostgrove/vv/bridge\n\ngo 1.26\n\nrequire github.com/frostgrove/vv/leaf v0.0.0\n",
			"bridge/bridge.go": "package bridge\n\nimport _ \"github.com/frostgrove/vv/leaf\"\n",
			"jobsfx/go.mod":    "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire github.com/frostgrove/vv/bridge v0.0.0-00010101000000-000000000000\n",
			"jobsfx/jobsfx.go": "package jobsfx\n\nimport _ \"github.com/frostgrove/vv/bridge\"\n",
			"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
			"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		})
		before := manifestFiles(t, root)

		finding, err := transitiveProductionOTelImport(root, filepath.Join(root, "jobsfx"), make(map[string]packageImports))
		if err != nil {
			t.Fatal(err)
		}
		for _, edge := range []string{"github.com/frostgrove/vv/bridge", "github.com/frostgrove/vv/leaf", "go.opentelemetry.io/otel/trace"} {
			if !strings.Contains(finding, edge) {
				t.Fatalf("local sentinel closure finding omits %s: %s", edge, finding)
			}
		}
		assertManifestFiles(t, root, before)
	})

	t.Run("explicit replacement wins", func(t *testing.T) {
		replacement := t.TempDir()
		writeInto(t, replacement, "go.mod", "module github.com/frostgrove/vv\n\ngo 1.26\n")
		writeInto(t, replacement, "thing/thing.go", "package thing\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
		root := fixture(t, map[string]string{
			"go.mod":           libraryGoMod,
			"thing/thing.go":   librarySource,
			"jobsfx/go.mod":    "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv v0.0.0 => " + replacement + "\n",
			"jobsfx/jobsfx.go": "package jobsfx\n\nimport _ \"github.com/frostgrove/vv/thing\"\n",
			"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
			"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		})

		finding, err := transitiveProductionOTelImport(root, filepath.Join(root, "jobsfx"), make(map[string]packageImports))
		if err != nil || !strings.Contains(finding, "go.opentelemetry.io/otel/trace") {
			t.Fatalf("checkout overrode an explicit sentinel replacement: finding=%q err=%v", finding, err)
		}
	})
}

func TestCheckDepsTraversesASelectedCachedRootModule(t *testing.T) {
	proxy := t.TempDir()
	version := "v0.9.0"
	modulePath := "github.com/frostgrove/vv"
	moduleRoot := modulePath + "@" + version
	goMod := "module " + modulePath + "\n\ngo 1.26\n"
	writeInto(t, proxy, modulePath+"/@v/"+version+".mod", goMod)
	writeInto(t, proxy, modulePath+"/@v/"+version+".info", `{"Version":"v0.9.0","Time":"2026-01-01T00:00:00Z"}`)
	zipPath := filepath.Join(proxy, filepath.FromSlash(modulePath), "@v", version+".zip")
	archive, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	for name, content := range map[string]string{
		"go.mod":          goMod,
		"thing/thing.go":  "package thing\n\nfunc Name() string { return \"cached\" }\n",
		"thing/hidden.go": "//go:build cached_hidden\n\npackage thing\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
	} {
		entry, err := writer.Create(moduleRoot + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	moduleCache := t.TempDir()
	download := exec.Command("go", "mod", "download", "-json", modulePath+"@"+version)
	download.Env = append(offlineGoEnvironment(), "GOPROXY=file://"+filepath.ToSlash(proxy), "GOMODCACHE="+moduleCache)
	downloadOutput, err := download.CombinedOutput()
	if err != nil {
		t.Fatalf("cannot seed the isolated module cache: %v\n%s", err, downloadOutput)
	}
	var downloaded struct {
		Sum      string
		GoModSum string
	}
	if err := json.Unmarshal(downloadOutput, &downloaded); err != nil {
		t.Fatalf("cannot read the seeded module sums: %v\n%s", err, downloadOutput)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(moduleCache, func(path string, entry os.DirEntry, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	t.Setenv("GOMODCACHE", moduleCache)
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod":    "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.9.0\n",
		"jobsfx/go.sum":    modulePath + " " + version + " " + downloaded.Sum + "\n" + modulePath + " " + version + "/go.mod " + downloaded.GoModSum + "\n",
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport _ \"github.com/frostgrove/vv/thing\"\n",
	})
	installOTelDependencyGate(t, root)

	output, code := runCheck(t, root, "deps")
	if code == 0 || !strings.Contains(output, "github.com/frostgrove/vv/thing") || !strings.Contains(output, "go.opentelemetry.io/otel/trace") {
		t.Fatalf("the synthetic repository root shadowed the selected cached v0.9.0 module with %d:\n%s", code, output)
	}
}

func TestOTelDependencyGraphDoesNotReplaceASelectedCachedReleaseWithCheckout(t *testing.T) {
	modulePath := "github.com/frostgrove/vv"
	version := "v0.9.0"
	cached := seedCachedModuleFixture(t, modulePath, version, map[string]string{
		"go.mod":         "module " + modulePath + "\n\ngo 1.26\n",
		"thing/thing.go": "package thing\n",
	})
	t.Setenv("GOMODCACHE", cached.cache)
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   "package thing\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod":    "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire " + modulePath + " " + version + "\n",
		"jobsfx/go.sum":    modulePath + " " + version + " " + cached.sum + "\n" + modulePath + " " + version + "/go.mod " + cached.goModSum + "\n",
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport _ \"github.com/frostgrove/vv/thing\"\n",
	})
	before := manifestFiles(t, root)

	finding, err := transitiveProductionOTelImport(root, filepath.Join(root, "jobsfx"), make(map[string]packageImports))
	if err != nil || finding != "" {
		t.Fatalf("checkout shadowed cached v0.9.0: finding=%q err=%v", finding, err)
	}
	assertManifestFiles(t, root, before)
}

type cachedModuleFixture struct {
	cache    string
	sum      string
	goModSum string
}

type manifestFileState struct {
	exists  bool
	content string
}

func manifestFiles(t *testing.T, root string) map[string]manifestFileState {
	t.Helper()
	candidates := map[string]bool{
		"go.work":     true,
		"go.work.sum": true,
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		switch entry.Name() {
		case "go.mod":
			candidates[relative] = true
			candidates[filepath.Join(filepath.Dir(relative), "go.sum")] = true
		case "go.sum", "go.work", "go.work.sum":
			candidates[relative] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cannot inventory fixture manifests: %v", err)
	}
	states := make(map[string]manifestFileState, len(candidates))
	for relative := range candidates {
		content, err := os.ReadFile(filepath.Join(root, relative))
		if os.IsNotExist(err) {
			states[relative] = manifestFileState{}
			continue
		}
		if err != nil {
			t.Fatalf("cannot read fixture manifest %s: %v", relative, err)
		}
		states[relative] = manifestFileState{exists: true, content: string(content)}
	}
	return states
}

func assertManifestFiles(t *testing.T, root string, before map[string]manifestFileState) {
	t.Helper()
	after := manifestFiles(t, root)
	if len(after) != len(before) {
		t.Fatalf("manifest inventory changed: before=%v after=%v", before, after)
	}
	for relative, wanted := range before {
		got, ok := after[relative]
		if !ok || got != wanted {
			t.Fatalf("check-deps changed %s: before=%+v after=%+v", relative, wanted, got)
		}
	}
}

func writeProxyModule(t *testing.T, proxy, modulePath, version, goMod string, files map[string]string) {
	t.Helper()
	writeInto(t, proxy, modulePath+"/@v/"+version+".mod", goMod)
	writeInto(t, proxy, modulePath+"/@v/"+version+".info", `{"Version":"`+version+`","Time":"2026-01-01T00:00:00Z"}`)
	archive, err := os.Create(filepath.Join(proxy, filepath.FromSlash(modulePath), "@v", version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	files["go.mod"] = goMod
	for name, content := range files {
		entry, err := writer.Create(modulePath + "@" + version + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDepsUsesEachSatellitesStandaloneMVS(t *testing.T) {
	dependencies := t.TempDir()
	sharedV1 := filepath.Join(dependencies, "shared-v1")
	sharedV2 := filepath.Join(dependencies, "shared-v2")
	trace := filepath.Join(dependencies, "trace")
	writeInto(t, sharedV1, "go.mod", "module example.com/shared\n\ngo 1.26\n")
	writeInto(t, sharedV1, "shared.go", "package shared\n\nconst Version = 1\n")
	writeInto(t, trace, "go.mod", "module go.opentelemetry.io/otel/trace\n\ngo 1.26\n")
	writeInto(t, trace, "trace.go", "package trace\n\nconst Enabled = true\n")
	writeInto(t, sharedV2, "go.mod", "module example.com/shared\n\ngo 1.26\n\nrequire go.opentelemetry.io/otel/trace v1.0.0\n")
	writeInto(t, sharedV2, "shared.go", "package shared\n\nimport \"go.opentelemetry.io/otel/trace\"\n\nvar Version = trace.Enabled\n")
	root := fixture(t, map[string]string{
		"go.mod":             libraryGoMod,
		"thing/thing.go":     librarySource,
		"test/go.mod":        "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod":   "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/jobsfx.go":   "package jobsfx\n\nimport \"example.com/shared\"\n\nvar Version = shared.Version\n",
		"otel/instrument.go": "package vvotel\n",
	})
	installOTelDependencyGate(t, root)
	relative := func(module, target string) string {
		path, err := filepath.Rel(filepath.Join(root, module), target)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(path)
	}
	jobsGoMod := func(version, target string) string {
		return `module github.com/frostgrove/vv/jobsfx

go 1.26

require example.com/shared ` + version + `

replace (
	example.com/shared ` + version + ` => ` + relative("jobsfx", target) + `
	go.opentelemetry.io/otel/trace => ` + relative("jobsfx", trace) + `
)
`
	}
	writeInto(t, root, "jobsfx/go.mod", jobsGoMod("v1.0.0", sharedV1))
	writeInto(t, root, "otel/go.mod", `module github.com/frostgrove/vv/otel

go 1.26

require example.com/shared v1.1.0

replace (
	example.com/shared v1.1.0 => `+relative("otel", sharedV2)+`
	go.opentelemetry.io/otel/trace => `+relative("otel", trace)+`
)
`)
	writeInto(t, root, "go.work", `go 1.26

use (
	.
	./jobsfx
	./otel
)

replace (
	example.com/shared v1.0.0 => `+filepath.ToSlash(sharedV1)+`
	example.com/shared v1.1.0 => `+filepath.ToSlash(sharedV2)+`
	go.opentelemetry.io/otel/trace => `+filepath.ToSlash(trace)+`
)
`)
	work := "GOWORK=" + filepath.Join(root, "go.work")
	output, code := runCheckWithEnv(t, root, "deps", work)
	if code != 0 {
		t.Fatalf("the clean satellite inherited the OTel module's v2 selection with %d:\n%s", code, output)
	}
	if strings.Contains(output, "./jobsfx reaches OpenTelemetry") || !strings.Contains(output, "./jobsfx: 1 external packages") {
		t.Fatalf("the standalone satellite listing is wrong:\n%s", output)
	}

	writeInto(t, root, "jobsfx/go.mod", jobsGoMod("v1.1.0", sharedV2))
	output, code = runCheckWithEnv(t, root, "deps", work)
	if code == 0 || !strings.Contains(output, "go.opentelemetry.io/otel/trace") {
		t.Fatalf("a satellite that really selects the OTel-bearing v2 passed with %d:\n%s", code, output)
	}
}

func TestCheckDepsIgnoresModulesInNonPublishedTrees(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":                         libraryGoMod,
		"thing/thing.go":                 librarySource,
		"test/go.mod":                    "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod":               "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod":                  "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n",
		"jobsfx/jobsfx.go":               "package jobsfx\n",
		"jobsfx/testdata/hidden/go.mod":  "module go.opentelemetry.io/otel/testdata-hidden\n\ngo 1.26\n",
		"jobsfx/testdata/hidden/main.go": "package hidden\n",
		"jobsfx/vendor/hidden/go.mod":    "module go.opentelemetry.io/otel/vendor-hidden\n\ngo 1.26\n",
		"jobsfx/vendor/hidden/main.go":   "package hidden\n",
		"jobsfx/.hidden/go.mod":          "module go.opentelemetry.io/otel/dot-hidden\n\ngo 1.26\n",
		"jobsfx/.hidden/main.go":         "package hidden\n",
	})

	output, code := runCheck(t, root, "deps")
	if code != 0 {
		t.Fatalf("non-published nested modules entered dependency checks with %d:\n%s", code, output)
	}
	for _, hidden := range []string{"testdata/hidden", "vendor/hidden", ".hidden"} {
		if strings.Contains(output, hidden) {
			t.Fatalf("check-deps listed non-published module %s:\n%s", hidden, output)
		}
	}
}

func TestCheckDepsUsesCheckoutSourceOnlyForLocalSentinelsAndTheirClosure(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"leaf/go.mod":      "module github.com/frostgrove/vv/leaf\n\ngo 1.26\n",
		"leaf/leaf.go":     "package leaf\n\nconst Ready = true\n",
		"bridge/go.mod": `module github.com/frostgrove/vv/bridge

go 1.26

require github.com/frostgrove/vv/leaf v0.0.0-00010101000000-000000000000
`,
		"bridge/bridge.go": "package bridge\n\nimport \"github.com/frostgrove/vv/leaf\"\n\nvar Ready = leaf.Ready\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require github.com/frostgrove/vv/bridge v0.0.0
`,
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport \"github.com/frostgrove/vv/bridge\"\n\nvar Ready = bridge.Ready\n",
	})

	output, code := runCheck(t, root, "deps")
	if code != 0 {
		t.Fatalf("local sentinel closure was not resolved from the checkout with %d:\n%s", code, output)
	}
	for _, module := range []string{"./bridge: 0 external packages", "./jobsfx: 0 external packages", "./leaf: 0 external packages"} {
		if !strings.Contains(output, module) {
			t.Fatalf("check-deps omitted local sentinel module result %q:\n%s", module, output)
		}
	}
}

func TestCheckDepsKeepsASelectedReleaseInsteadOfCheckoutSource(t *testing.T) {
	modulePath := "github.com/frostgrove/vv"
	version := "v0.9.0"
	cached := seedCachedModuleFixture(t, modulePath, version, map[string]string{
		"go.mod":                     "module " + modulePath + "\n\ngo 1.26\n",
		"releaseonly/releaseonly.go": "package releaseonly\n\nconst Source = \"release\"\n",
	})
	t.Setenv("GOMODCACHE", cached.cache)
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod":    "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire " + modulePath + " " + version + "\n",
		"jobsfx/go.sum":    modulePath + " " + version + " " + cached.sum + "\n" + modulePath + " " + version + "/go.mod " + cached.goModSum + "\n",
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport \"github.com/frostgrove/vv/releaseonly\"\n\nvar Source = releaseonly.Source\n",
	})

	output, code := runCheck(t, root, "deps")
	if code != 0 {
		t.Fatalf("checkout source shadowed the selected cached release with %d:\n%s", code, output)
	}
	if !strings.Contains(output, "./jobsfx: 0 external packages") {
		t.Fatalf("selected release listing was not completed:\n%s", output)
	}
}

func TestCheckDepsNeverChangesLiveManifestOrChecksumFiles(t *testing.T) {
	for _, test := range []struct {
		name    string
		failing bool
	}{
		{name: "passing"},
		{name: "failing", failing: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			proxy := t.TempDir()
			moduleCache := t.TempDir()
			temporaryState := t.TempDir()
			t.Cleanup(func() {
				_ = filepath.WalkDir(moduleCache, func(path string, entry os.DirEntry, err error) error {
					if err == nil {
						_ = os.Chmod(path, 0o755)
					}
					return nil
				})
			})
			leafPath := "example.com/leaf"
			leafVersion := "v1.0.0"
			leafGoMod := "module " + leafPath + "\n\ngo 1.26\n"
			leafSource := "package leaf\n\nconst Ready = true\n"
			if test.failing {
				leafGoMod += "\nrequire go.opentelemetry.io/otel/trace v1.0.0\n"
				leafSource = "package leaf\n\nimport \"go.opentelemetry.io/otel/trace\"\n\nvar Ready = trace.Enabled\n"
				writeProxyModule(t, proxy, "go.opentelemetry.io/otel/trace", "v1.0.0", "module go.opentelemetry.io/otel/trace\n\ngo 1.26\n", map[string]string{
					"trace.go": "package trace\n\nconst Enabled = true\n",
				})
			}
			writeProxyModule(t, proxy, leafPath, leafVersion, leafGoMod, map[string]string{"leaf.go": leafSource})
			root := fixture(t, map[string]string{
				"go.mod":             libraryGoMod,
				"thing/thing.go":     librarySource,
				"test/go.mod":        "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
				"_examples/go.mod":   "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
				"jobsfx/go.mod":      "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire " + leafPath + " " + leafVersion + "\n",
				"jobsfx/go.sum":      "",
				"jobsfx/jobsfx.go":   "package jobsfx\n\nimport \"example.com/leaf\"\n\nvar Ready = leaf.Ready\n",
				"otel/instrument.go": "package vvotel\n",
			})
			installOTelDependencyGate(t, root)
			writeInto(t, root, "go.work", "go 1.26\n\nuse (\n\t.\n\t./jobsfx\n\t./otel\n)\n")
			before := manifestFiles(t, root)
			if before["go.work.sum"].exists || before["go.sum"].exists || before["otel/go.sum"].exists {
				t.Fatal("fixture checksum files that exercise missing-file preservation already exist")
			}
			if state := before["jobsfx/go.sum"]; !state.exists || state.content != "" {
				t.Fatalf("fixture jobsfx/go.sum state = %+v, want an existing empty file", state)
			}
			output, code := runCheckWithEnv(t, root, "deps",
				"GOWORK="+filepath.Join(root, "go.work"),
				"GOPROXY=file://"+filepath.ToSlash(proxy),
				"GOSUMDB=off",
				"GOMODCACHE="+moduleCache,
				"TMPDIR="+temporaryState,
			)
			assertManifestFiles(t, root, before)
			entries, err := os.ReadDir(temporaryState)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("check-deps left temporary state behind: %v", entries)
			}
			if test.failing && code == 0 {
				t.Fatalf("the OTel-bearing proxy module passed:\n%s", output)
			}
			if !test.failing && code != 0 {
				t.Fatalf("the clean proxy module failed with %d:\n%s", code, output)
			}
		})
	}
}

func seedCachedModuleFixture(t *testing.T, modulePath, version string, files map[string]string) cachedModuleFixture {
	t.Helper()
	proxy := t.TempDir()
	goMod := files["go.mod"]
	writeInto(t, proxy, modulePath+"/@v/"+version+".mod", goMod)
	writeInto(t, proxy, modulePath+"/@v/"+version+".info", `{"Version":"`+version+`","Time":"2026-01-01T00:00:00Z"}`)
	zipPath := filepath.Join(proxy, filepath.FromSlash(modulePath), "@v", version+".zip")
	archive, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	for name, content := range files {
		entry, err := writer.Create(modulePath + "@" + version + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	moduleCache := t.TempDir()
	download := exec.Command("go", "mod", "download", "-json", modulePath+"@"+version)
	download.Env = append(offlineGoEnvironment(), "GOPROXY=file://"+filepath.ToSlash(proxy), "GOMODCACHE="+moduleCache)
	output, err := download.CombinedOutput()
	if err != nil {
		t.Fatalf("cannot seed the isolated module cache: %v\n%s", err, output)
	}
	var downloaded struct {
		Sum      string
		GoModSum string
	}
	if err := json.Unmarshal(output, &downloaded); err != nil {
		t.Fatalf("cannot read the seeded module sums: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(moduleCache, func(path string, entry os.DirEntry, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	return cachedModuleFixture{cache: moduleCache, sum: downloaded.Sum, goModSum: downloaded.GoModSum}
}

func TestCheckDepsKeepsRepositoryFallbackImportsOnTheRepositoryWalk(t *testing.T) {
	modulePath := "github.com/frostgrove/vv"
	version := "v0.9.0"
	cached := seedCachedModuleFixture(t, modulePath, version, map[string]string{
		"go.mod":     "module " + modulePath + "\n\ngo 1.26\n",
		"old/old.go": "package old\n\nconst Source = \"cached\"\n",
	})
	t.Setenv("GOMODCACHE", cached.cache)
	dependencies := t.TempDir()
	bridgeV1 := filepath.Join(dependencies, "bridge-v1")
	bridgeV2 := filepath.Join(dependencies, "bridge-v2")
	writeInto(t, bridgeV1, "go.mod", "module corp/bridge\n\ngo 1.26\n")
	writeInto(t, bridgeV1, "bridge.go", "package bridge\n\nconst Enabled = true\n")
	writeInto(t, bridgeV2, "go.mod", "module corp/bridge\n\ngo 1.26\n")
	writeInto(t, bridgeV2, "bridge.go", "package bridge\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
	root := fixture(t, map[string]string{
		"go.mod": libraryGoMod + "\nrequire corp/bridge v1.0.0\n\nreplace corp/bridge v1.0.0 => " + bridgeV1 + "\n",
		"new/new.go": `package new

import _ "github.com/frostgrove/vv/old"
`,
		"old/old.go": `package old

import _ "corp/bridge"
`,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require (
	github.com/frostgrove/vv v0.9.0
	corp/bridge v2.0.0+incompatible
)

replace corp/bridge v2.0.0+incompatible => ` + bridgeV2 + `
`,
		"jobsfx/go.sum": modulePath + " " + version + " " + cached.sum + "\n" + modulePath + " " + version + "/go.mod " + cached.goModSum + "\n",
		"jobsfx/jobsfx.go": `package jobsfx

import _ "github.com/frostgrove/vv/new"
`,
	})
	finding, err := inspectReachableFixture(root)
	if err != nil {
		t.Fatalf("cannot inspect the selected and repository source walk: %v", err)
	}
	if finding == "" {
		t.Fatal("the repository fallback walk switched back to the selected module and hid OpenTelemetry")
	}
	for _, expected := range []string{"github.com/frostgrove/vv/new", "github.com/frostgrove/vv/old", "corp/bridge", "go.opentelemetry.io/otel/trace"} {
		if !strings.Contains(finding, expected) {
			t.Fatalf("the fallback finding omits %s:\n%s", expected, finding)
		}
	}
}

func TestCheckDepsDoesNotFallbackForAMissingSelectedTransitiveSibling(t *testing.T) {
	modulePath := "github.com/frostgrove/vv"
	version := "v0.9.0"
	cached := seedCachedModuleFixture(t, modulePath, version, map[string]string{
		"go.mod": "module " + modulePath + "\n\ngo 1.26\n",
		"old/old.go": `package old

import _ "github.com/frostgrove/vv/missing"
`,
	})
	t.Setenv("GOMODCACHE", cached.cache)
	root := fixture(t, map[string]string{
		"go.mod":             libraryGoMod,
		"missing/missing.go": "package missing\n",
		"test/go.mod":        "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod":   "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod":      "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.9.0\n",
		"jobsfx/go.sum":      modulePath + " " + version + " " + cached.sum + "\n" + modulePath + " " + version + "/go.mod " + cached.goModSum + "\n",
		"jobsfx/jobsfx.go":   "package jobsfx\n\nimport _ \"github.com/frostgrove/vv/old\"\n",
	})
	_, err := inspectReachableFixture(root)
	if err == nil || !strings.Contains(err.Error(), "github.com/frostgrove/vv/missing") || !strings.Contains(err.Error(), "absent from selected module") {
		t.Fatalf("a selected walk fell back for a missing transitive sibling: %v", err)
	}
}

func TestOTelDependencyGraphFallsBackOnlyWhenSelectedRootPackageIsAbsent(t *testing.T) {
	selected := t.TempDir()
	writeInto(t, selected, "go.mod", "module github.com/frostgrove/vv\n\ngo 1.26\n")
	repository := t.TempDir()
	writeInto(t, repository, "go.mod", "module github.com/frostgrove/vv\n\ngo 1.26\n")
	writeInto(t, repository, "newpkg/newpkg.go", "package newpkg\n")
	writeInto(t, repository, "newpkg/hidden.go", "//go:build fallback_hidden\n\npackage newpkg\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
	modules := []listedModule{{
		Path:                    "github.com/frostgrove/vv",
		Dir:                     selected,
		GoMod:                   filepath.Join(selected, "go.mod"),
		RepositoryFallbackDir:   repository,
		RepositoryFallbackGoMod: filepath.Join(repository, "go.mod"),
	}}
	directory, err := reachableImportDirectory(modules, "github.com/frostgrove/vv/newpkg")
	if err != nil || directory != filepath.Join(repository, "newpkg") {
		t.Fatalf("an absent selected package did not use the repository fallback: directory=%q err=%v", directory, err)
	}
	imports := cachedPackageImports(make(map[string]packageImports), directory)
	if imports.err != nil {
		t.Fatal(imports.err)
	}
	var finding bool
	for _, imported := range imports.edges {
		finding = finding || openTelemetryImport(imported.path)
	}
	if !finding {
		t.Fatal("the repository fallback hid an OpenTelemetry import")
	}

	_, err = reachableImportDirectory(modules, "github.com/frostgrove/vv/missing")
	if err == nil || !strings.Contains(err.Error(), "absent from the selected module and repository fallback") {
		t.Fatalf("a package absent from both selected and fallback roots did not fail closed: %v", err)
	}
}

func TestBuildConstraintDetectionUsesGoSemantics(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		ignored bool
	}{
		{name: "correlated contradiction", header: "//go:build tag && !tag\n\n", ignored: true},
		{name: "correlated tautology", header: "//go:build tag || !tag\n\n"},
		{name: "dependency gate policy keeps ignore false", header: "//go:build ignore\n\n", ignored: true},
		{name: "custom tag can be true", header: "//go:build custom\n\n"},
		{name: "legacy ignore", header: "// +build ignore\n\n", ignored: true},
		{name: "legacy without blank separator is ordinary comment", header: "// +build ignore\n"},
		{name: "legacy lines are anded", header: "// +build ignore\n// +build custom\n\n", ignored: true},
		{name: "legacy terms on a line are ored", header: "// +build ignore custom\n\n"},
		{name: "go build takes precedence", header: "//go:build custom\n// +build ignore\n\n"},
		{name: "go build accepts tab whitespace", header: "//go:build\tcustom\n\n"},
		{name: "legacy build accepts tab whitespace", header: "// +build\tcustom\n\n"},
		{name: "automatic and explicit operating system tags overlap", header: "//go:build linux && windows\n\n"},
		{name: "automatic and explicit compiler tags overlap", header: "//go:build gc && gccgo\n\n"},
		{name: "gccgo source is admitted", header: "//go:build gccgo\n\n"},
		{name: "supported experiments are admitted", header: "//go:build goexperiment.arenas\n\n"},
		{name: "modern after block comment", header: "/* license */\n//go:build custom\n\n"},
		{name: "modern ignore after block license", header: "/*\nlicense\n*/\n//go:build ignore\n\n", ignored: true},
		{name: "future releases may be explicit tags", header: "//go:build go1.999\n\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ignored, err := requiresIgnoredBuildTag([]byte(test.header + "package fixture\n"))
			if err != nil {
				t.Fatalf("cannot parse build constraint: %v", err)
			}
			if ignored != test.ignored {
				t.Fatalf("requiresIgnoredBuildTag() = %t, want %t", ignored, test.ignored)
			}
		})
	}
}

func TestBuildConstraintRejectsMultipleGoBuildLines(t *testing.T) {
	_, err := parseBuildConstraint([]byte("//go:build linux\n//go:build windows\n\npackage fixture\n"))
	if err == nil || !strings.Contains(err.Error(), "multiple //go:build comments") {
		t.Fatalf("multiple //go:build lines were not rejected: %v", err)
	}
}

func TestBuildConstraintIncludesTheFilenamePlatformSuffix(t *testing.T) {
	expression, err := parseBuildConstraint([]byte("//go:build !windows\n\npackage fixture\n"))
	if err != nil {
		t.Fatal(err)
	}
	possible, err := productionSourcesSatisfiable(productionSource{file: "fixture_windows.go", constraint: expression})
	if err != nil {
		t.Fatal(err)
	}
	if possible {
		t.Fatal("a _windows.go filename and a !windows header were treated as simultaneously buildable")
	}
}

func TestBuildConstraintIncludesCumulativeArchitectureFeatures(t *testing.T) {
	expression, err := parseBuildConstraint([]byte("//go:build !amd64.v1\n\npackage fixture\n"))
	if err != nil {
		t.Fatal(err)
	}
	possible, err := productionSourcesSatisfiable(productionSource{file: "fixture_amd64.go", constraint: expression})
	if err != nil {
		t.Fatal(err)
	}
	if possible {
		t.Fatal("an _amd64.go filename and !amd64.v1 header were treated as simultaneously buildable")
	}
}

type localOTelGraph struct {
	trace  string
	bridge string
	relay  string
}

func newLocalOTelGraph(t *testing.T, hiddenName, buildLine string, twoHop bool) localOTelGraph {
	t.Helper()
	dependencies := t.TempDir()
	graph := localOTelGraph{
		trace:  filepath.Join(dependencies, "trace"),
		bridge: filepath.Join(dependencies, "bridge"),
	}
	writeInto(t, graph.trace, "go.mod", "module go.opentelemetry.io/otel/trace\n\ngo 1.26\n")
	writeInto(t, graph.trace, "trace.go", "package trace\n\nconst Enabled = true\n")
	writeInto(t, graph.bridge, "go.mod", `module example.com/bridge

go 1.26

require go.opentelemetry.io/otel/trace v0.0.0
`)
	writeInto(t, graph.bridge, "bridge.go", "package bridge\n\nconst Enabled = true\n")
	writeInto(t, graph.bridge, hiddenName, buildLine+`package bridge

import "go.opentelemetry.io/otel/trace"

var TraceEnabled = trace.Enabled
`)
	if twoHop {
		graph.relay = filepath.Join(dependencies, "relay")
		writeInto(t, graph.relay, "go.mod", `module example.com/relay

go 1.26

require example.com/bridge v0.0.0
`)
		writeInto(t, graph.relay, "relay.go", `package relay

import "example.com/bridge"

var Enabled = bridge.Enabled
`)
	}
	return graph
}

func (graph localOTelGraph) satelliteGoMod() string {
	requirements := "\texample.com/bridge v0.0.0\n"
	relayReplacement := ""
	if graph.relay != "" {
		requirements = "\texample.com/relay v0.0.0\n\texample.com/bridge v0.0.0 // indirect\n"
		relayReplacement = "replace example.com/relay => " + graph.relay + "\n"
	}
	return `module github.com/frostgrove/vv/jobsfx

go 1.26

require (
` + requirements + `	go.opentelemetry.io/otel/trace v0.0.0 // indirect
)

replace example.com/bridge => ` + graph.bridge + `
` + relayReplacement + `replace go.opentelemetry.io/otel/trace => ` + graph.trace + `
`
}

func installOTelDependencyGate(t *testing.T, root string) {
	t.Helper()
	content, err := os.ReadFile("otel_dependency_test.go")
	if err != nil {
		t.Fatalf("cannot read the OpenTelemetry dependency gate: %v", err)
	}
	writeInto(t, root, "scripts/otel_dependency_test.go", string(content))
	writeInto(t, root, "otel/go.mod", "module github.com/frostgrove/vv/otel\n\ngo 1.26\n")
	writeInto(t, root, "otel/doc.go", "package vvotel\n")
}

func taggedBridgeFixture(t *testing.T, hiddenName, buildLine string, twoHop bool) string {
	t.Helper()
	graph := newLocalOTelGraph(t, hiddenName, buildLine, twoHop)
	entry := "example.com/bridge"
	if twoHop {
		entry = "example.com/relay"
	}
	root := fixture(t, map[string]string{
		"go.mod":             libraryGoMod,
		"thing/thing.go":     librarySource,
		"test/go.mod":        "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"test/suite.go":      "package suite\n",
		"_examples/go.mod":   "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"_examples/basic.go": "package basic\n",
		"jobsfx/go.mod":      graph.satelliteGoMod(),
		"jobsfx/jobsfx.go":   "package jobsfx\n\nimport \"" + entry + "\"\n\nvar Enabled = " + filepath.Base(entry) + ".Enabled\n",
	})
	installOTelDependencyGate(t, root)
	return root
}

func TestCheckDepsRefusesTransitiveOpenTelemetryBehindEveryBuildConstraintClass(t *testing.T) {
	tests := []struct {
		name       string
		hiddenName string
		buildLine  string
		twoHop     bool
	}{
		{name: "platform", hiddenName: "bridge_windows_arm64.go", buildLine: "//go:build windows && arm64\n\n"},
		{name: "explicit_windows_tag", hiddenName: "bridge_linux.go", buildLine: "//go:build windows\n\n"},
		{name: "custom", hiddenName: "bridge_custom.go", buildLine: "//go:build vvotel_hidden\n\n"},
		{name: "gccgo", hiddenName: "bridge_gccgo.go", buildLine: "//go:build gccgo\n\n"},
		{name: "experiment", hiddenName: "bridge_arenas.go", buildLine: "//go:build goexperiment.arenas\n\n"},
		{name: "two_hop", hiddenName: "bridge_custom.go", buildLine: "//go:build vvotel_hidden\n\n", twoHop: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := taggedBridgeFixture(t, test.hiddenName, test.buildLine, test.twoHop)
			output, code := runCheck(t, root, "deps")
			if code == 0 {
				t.Fatalf("a reachable bridge hides OpenTelemetry behind %s and check-deps passed:\n%s", test.name, output)
			}
			if !strings.Contains(output, "./jobsfx reaches forbidden OpenTelemetry production code") || !strings.Contains(output, test.hiddenName) || !strings.Contains(output, "go.opentelemetry.io/otel/trace") {
				t.Fatalf("check-deps refused without the reachable package chain:\n%s", output)
			}
			if test.twoHop && !strings.Contains(output, "example.com/relay") {
				t.Fatalf("the two-hop diagnostic omits the relay package:\n%s", output)
			}
		})
	}
}

func TestCheckDepsRefusesATransitiveTaggedBridgeFromTheRootModule(t *testing.T) {
	graph := newLocalOTelGraph(t, "bridge_custom.go", "//go:build vvotel_hidden\n\n", false)
	root := fixture(t, map[string]string{
		"go.mod": strings.Replace(graph.satelliteGoMod(), "module github.com/frostgrove/vv/jobsfx", "module github.com/frostgrove/vv", 1),
	})
	writeInto(t, root, "thing/thing.go", librarySource)
	writeInto(t, root, "thing/bridge_custom.go", `//go:build vvotel_hidden

package thing

import "example.com/bridge"

var BridgeEnabled = bridge.Enabled
`)
	writeInto(t, root, "test/go.mod", "module github.com/frostgrove/vv/test\n\ngo 1.26\n")
	writeInto(t, root, "_examples/go.mod", "module github.com/frostgrove/vv/examples\n\ngo 1.26\n")
	installOTelDependencyGate(t, root)

	output, code := runCheck(t, root, "deps")
	if code == 0 {
		t.Fatalf("the root reaches OpenTelemetry through a tagged bridge and check-deps passed:\n%s", output)
	}
	if !strings.Contains(output, ". reaches forbidden OpenTelemetry production code") || !strings.Contains(output, "bridge_custom.go") {
		t.Fatalf("check-deps refused without identifying the root chain:\n%s", output)
	}
}

func TestCheckDepsDoesNotRejectUnusedModuleGraphOpenTelemetry(t *testing.T) {
	graph := newLocalOTelGraph(t, "bridge_custom.go", "//go:build vvotel_hidden\n\n", false)
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod":    graph.satelliteGoMod(),
		"jobsfx/jobsfx.go": "package jobsfx\n\nconst Enabled = true\n",
	})
	installOTelDependencyGate(t, root)

	output, code := runCheck(t, root, "deps")
	if code != 0 {
		t.Fatalf("an unused OpenTelemetry module-graph edge is not a production dependency and check-deps exited %d:\n%s", code, output)
	}
}

func TestCheckDepsIgnoresOpenTelemetryOutsideProductionSource(t *testing.T) {
	dependencies := t.TempDir()
	trace := filepath.Join(dependencies, "trace")
	writeInto(t, trace, "go.mod", "module go.opentelemetry.io/otel/trace\n\ngo 1.26\n")
	writeInto(t, trace, "trace.go", "package trace\n\nconst Enabled = true\n")
	goMod := `module github.com/frostgrove/vv/jobsfx

go 1.26

require go.opentelemetry.io/otel/trace v0.0.0

replace go.opentelemetry.io/otel/trace => ` + trace + `
`
	root := fixture(t, map[string]string{
		"go.mod":                           libraryGoMod,
		"thing/thing.go":                   librarySource,
		"test/go.mod":                      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod":                 "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod":                    goMod,
		"jobsfx/jobsfx.go":                 "package jobsfx\n",
		"jobsfx/jobsfx_test.go":            "package jobsfx\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
		"jobsfx/testdata/helper/helper.go": "package helper\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
		"jobsfx/generate.go":               "//go:build ignore\n\npackage main\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
	})
	installOTelDependencyGate(t, root)

	output, code := runCheck(t, root, "deps")
	if code != 0 {
		t.Fatalf("test, testdata and build-ignore imports are not production dependencies and check-deps exited %d:\n%s", code, output)
	}
}

func TestCheckDepsAllowsApplicationOwnedSDKInUnpublishedModules(t *testing.T) {
	dependencies := t.TempDir()
	sdk := filepath.Join(dependencies, "sdk")
	writeInto(t, sdk, "go.mod", "module go.opentelemetry.io/otel/sdk\n\ngo 1.26\n")
	writeInto(t, sdk, "sdk.go", "package sdk\n\nconst Enabled = true\n")
	unpublishedGoMod := `module example.com/application

go 1.26

require go.opentelemetry.io/otel/sdk v0.0.0

replace go.opentelemetry.io/otel/sdk => ` + sdk + `
`
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      unpublishedGoMod,
		"test/sdk.go":      "package test\n\nimport _ \"go.opentelemetry.io/otel/sdk\"\n",
		"_examples/go.mod": strings.Replace(unpublishedGoMod, "example.com/application", "example.com/examples", 1),
		"_examples/sdk.go": "package examples\n\nimport _ \"go.opentelemetry.io/otel/sdk\"\n",
	})
	installOTelDependencyGate(t, root)

	output, code := runCheck(t, root, "deps")
	if code != 0 {
		t.Fatalf("unpublished application modules own SDK tooling and check-deps exited %d:\n%s", code, output)
	}
}

func TestCheckDepsFailsClosedOnHiddenDependencySourceErrors(t *testing.T) {
	tests := []struct {
		name   string
		goMod  string
		source string
		want   string
	}{
		{
			name:   "unresolved",
			goMod:  "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire example.com/missing v0.0.0\n",
			source: "//go:build vvotel_hidden\n\npackage jobsfx\n\nimport _ \"example.com/missing\"\n",
			want:   "cannot resolve reachable package example.com/missing",
		},
		{
			name:   "missing local replacement",
			goMod:  "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire example.com/missing v0.0.0\n\nreplace example.com/missing => ./missing-local-replacement\n",
			source: "//go:build vvotel_hidden\n\npackage jobsfx\n\nimport _ \"example.com/missing/package\"\n",
			want:   "module example.com/missing has no usable go.mod",
		},
		{
			name:   "parse",
			goMod:  "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n",
			source: "//go:build vvotel_hidden\n\npackage jobsfx\n\nimport (\n",
			want:   "expected ')'",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := fixture(t, map[string]string{
				"go.mod":           libraryGoMod,
				"thing/thing.go":   librarySource,
				"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
				"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
				"jobsfx/go.mod":    test.goMod,
				"jobsfx/jobsfx.go": "package jobsfx\n",
				"jobsfx/hidden.go": test.source,
			})
			installOTelDependencyGate(t, root)

			output, code := runCheck(t, root, "deps")
			if code == 0 {
				t.Fatalf("a hidden source error made check-deps pass:\n%s", output)
			}
			if !strings.Contains(output, test.want) {
				t.Fatalf("check-deps failed without the hidden source error %q:\n%s", test.want, output)
			}
			if strings.Contains(output, root) {
				t.Fatalf("check-deps exposed the fixture's absolute path instead of a module-relative diagnostic:\n%s", output)
			}
		})
	}
}

func reachableDependencyFixture(t *testing.T, modulePath, dependency string) string {
	t.Helper()
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require ` + modulePath + ` v0.0.0

replace ` + modulePath + ` => ` + dependency + `
`,
		"jobsfx/jobsfx.go": "package jobsfx\n",
		"jobsfx/hidden.go": "//go:build dependency_gate_hidden\n\npackage jobsfx\n\nimport _ \"" + modulePath + "\"\n",
	})
	return root
}

func inspectReachableFixture(root string) (string, error) {
	return transitiveProductionOTelImport(root, filepath.Join(root, "jobsfx"), make(map[string]packageImports))
}

func TestOTelDependencyGraphRejectsASelectedReplacementWithoutAGoMod(t *testing.T) {
	dependency := t.TempDir()
	writeInto(t, dependency, "library.go", "package library\n")
	root := reachableDependencyFixture(t, "example.com/library", dependency)

	_, err := inspectReachableFixture(root)
	if err == nil || !strings.Contains(err.Error(), "module example.com/library has no usable go.mod") {
		t.Fatalf("a selected replacement without go.mod did not fail closed: %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), dependency) {
		t.Fatalf("the selected-module diagnostic exposed an absolute path: %v", err)
	}
}

func TestOTelDependencyGraphRejectsASelectedModuleErrorEvenWithADirectory(t *testing.T) {
	directory := t.TempDir()
	writeInto(t, directory, "go.mod", "module example.com/library\n\ngo 1.26\n")
	modules := []listedModule{{
		Path:  "example.com/library",
		Dir:   directory,
		GoMod: filepath.Join(directory, "go.mod"),
		Error: &struct{ Err string }{Err: directory + ": unavailable"},
	}}

	_, err := reachableImportDirectory(modules, "example.com/library/package")
	if err == nil || err.Error() != "module example.com/library is unavailable" {
		t.Fatalf("a selected module error with Dir was not rejected safely: %v", err)
	}
}

func TestOTelDependencyGraphRejectsAReachedPackageWithoutProductionFiles(t *testing.T) {
	tests := map[string]map[string]string{
		"test only": {
			"go.mod":          "module example.com/library\n\ngo 1.26\n",
			"library_test.go": "package library\n",
		},
		"impossible constraint": {
			"go.mod":     "module example.com/library\n\ngo 1.26\n",
			"library.go": "//go:build tag && !tag\n\npackage library\n",
		},
	}
	for name, files := range tests {
		t.Run(name, func(t *testing.T) {
			dependency := t.TempDir()
			for file, content := range files {
				writeInto(t, dependency, file, content)
			}
			root := reachableDependencyFixture(t, "example.com/library", dependency)
			_, err := inspectReachableFixture(root)
			if err == nil || !strings.Contains(err.Error(), "package has no satisfiable production Go files") {
				t.Fatalf("a reached package without production files did not fail closed: %v", err)
			}
		})
	}
}

func TestOTelDependencyGraphChecksOverlappingPackageDeclarations(t *testing.T) {
	t.Run("overlap fails", func(t *testing.T) {
		dependency := t.TempDir()
		writeInto(t, dependency, "go.mod", "module example.com/library\n\ngo 1.26\n")
		writeInto(t, dependency, "alpha.go", "//go:build custom\n\npackage alpha\n")
		writeInto(t, dependency, "beta.go", "//go:build custom\n\npackage beta\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
		root := reachableDependencyFixture(t, "example.com/library", dependency)
		_, err := inspectReachableFixture(root)
		if err == nil || !strings.Contains(err.Error(), "declare different packages alpha and beta") {
			t.Fatalf("overlapping package declarations did not fail closed: %v", err)
		}
	})

	t.Run("mutually exclusive platforms pass", func(t *testing.T) {
		dependency := t.TempDir()
		writeInto(t, dependency, "go.mod", "module example.com/library\n\ngo 1.26\n")
		writeInto(t, dependency, "library_linux.go", "package linuxlibrary\n")
		writeInto(t, dependency, "library_windows.go", "package windowslibrary\n")
		root := reachableDependencyFixture(t, "example.com/library", dependency)
		finding, err := inspectReachableFixture(root)
		if err != nil || finding != "" {
			t.Fatalf("mutually exclusive platform packages were rejected: finding=%q err=%v", finding, err)
		}
	})
}

func TestOTelDependencyGraphRejectsAnUnselectedNestedModule(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":                  libraryGoMod,
		"thing/thing.go":          librarySource,
		"nested/go.mod":           "module github.com/frostgrove/vv/nested\n\ngo 1.26\n",
		"nested/package/file.go":  "package nestedpackage\n",
		"jobsfx/go.mod":           "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n",
		"jobsfx/jobsfx.go":        "package jobsfx\n",
		"jobsfx/hidden_nested.go": "//go:build dependency_gate_hidden\n\npackage jobsfx\n\nimport _ \"github.com/frostgrove/vv/nested/package\"\n",
	})

	_, err := inspectReachableFixture(root)
	if err == nil || !strings.Contains(err.Error(), "crosses unselected nested module nested") {
		t.Fatalf("an unselected nested module was traversed as part of the root: %v", err)
	}
}

func TestOTelDependencyGraphRejectsMalformedImportPathsBeforeResolution(t *testing.T) {
	for _, importPath := range []string{".", "..", "corp//bridge", "corp/./bridge", "corp/../bridge", `corp\bridge`, "/corp/bridge", "corp/bridge/", "corp/@bridge", "corp/CON", "corp/name~1"} {
		if err := validateImportPath(importPath); err == nil {
			t.Errorf("malformed import path %q was accepted", importPath)
		}
	}
	dependency := t.TempDir()
	writeInto(t, dependency, "go.mod", "module corp/library\n\ngo 1.26\n")
	writeInto(t, dependency, "library.go", "package library\n")
	root := reachableDependencyFixture(t, "corp/library", dependency)
	writeInto(t, root, "jobsfx/hidden.go", "//go:build dependency_gate_hidden\n\npackage jobsfx\n\nimport _ \"corp/library/../escape\"\n")

	_, err := inspectReachableFixture(root)
	if err == nil || !strings.Contains(err.Error(), `invalid reachable import path "corp/library/../escape"`) {
		t.Fatalf("a relative import component reached filesystem resolution: %v", err)
	}
}

func TestOTelDependencyGraphRejectsASymlinkEscape(t *testing.T) {
	dependency := t.TempDir()
	outside := t.TempDir()
	writeInto(t, dependency, "go.mod", "module corp/library\n\ngo 1.26\n")
	writeInto(t, dependency, "library.go", "package library\n")
	writeInto(t, outside, "outside.go", "package outside\n")
	if err := os.Symlink(outside, filepath.Join(dependency, "escape")); err != nil {
		t.Fatalf("cannot create the symlink fixture: %v", err)
	}
	root := reachableDependencyFixture(t, "corp/library", dependency)
	writeInto(t, root, "jobsfx/hidden.go", "//go:build dependency_gate_hidden\n\npackage jobsfx\n\nimport _ \"corp/library/escape\"\n")

	_, err := inspectReachableFixture(root)
	if err == nil || !strings.Contains(err.Error(), "package escapes selected module corp/library") {
		t.Fatalf("a symlink escaped the selected module directory: %v", err)
	}
}

func TestOTelDependencyGraphAllowsAMismatchedReplacementModuleDeclaration(t *testing.T) {
	dependency := t.TempDir()
	writeInto(t, dependency, "go.mod", "module corp/different\n\ngo 1.26\n")
	writeInto(t, dependency, "library.go", "package library\n")
	root := reachableDependencyFixture(t, "corp/library", dependency)

	finding, err := inspectReachableFixture(root)
	if err != nil || finding != "" {
		t.Fatalf("a readable local replacement accepted by Go was rejected: finding=%q err=%v", finding, err)
	}
}

func TestOTelDependencyGraphRejectsAForbiddenMismatchedReplacementIdentity(t *testing.T) {
	dependency := t.TempDir()
	writeInto(t, dependency, "go.mod", "module go.opentelemetry.io/otel/codes\n\ngo 1.26\n")
	writeInto(t, dependency, "library.go", "package library\n\nimport _ \"fmt\"\n")
	root := reachableDependencyFixture(t, "corp/otel/codes", dependency)

	_, err := inspectReachableFixture(root)
	if err == nil || !strings.Contains(err.Error(), "corp/otel/codes declares forbidden OpenTelemetry module go.opentelemetry.io/otel/codes") {
		t.Fatalf("an aliased local replacement hid its forbidden module identity: %v", err)
	}
}

func TestOTelDependencyGraphRejectsAForbiddenVersionReplacementIdentity(t *testing.T) {
	modules := []listedModule{{
		Path:    "corp/otel/codes",
		Replace: &listedModule{Path: "github.com/frostgrove/vv/otel", Version: "v1.0.0"},
	}}

	_, err := reachableImportDirectory(modules, "corp/otel/codes")
	if err == nil || !strings.Contains(err.Error(), "resolves to forbidden OpenTelemetry module github.com/frostgrove/vv/otel") {
		t.Fatalf("version replacement metadata hid a forbidden module identity: %v", err)
	}
}

func TestCheckDepsUsesAnExplicitRootModuleReplacement(t *testing.T) {
	dependencies := t.TempDir()
	replacement := filepath.Join(dependencies, "vv")
	trace := filepath.Join(dependencies, "trace")
	writeInto(t, trace, "go.mod", "module go.opentelemetry.io/otel/trace\n\ngo 1.26\n")
	writeInto(t, trace, "trace.go", "package trace\n")
	writeInto(t, replacement, "go.mod", "module github.com/frostgrove/vv\n\ngo 1.26\n\nrequire go.opentelemetry.io/otel/trace v0.0.0\n")
	writeInto(t, replacement, "thing/thing.go", "package thing\n\nfunc Name() string { return \"replacement\" }\n")
	writeInto(t, replacement, "thing/hidden.go", "//go:build replacement_hidden\n\npackage thing\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod": `module github.com/frostgrove/vv/jobsfx

go 1.26

require (
	github.com/frostgrove/vv v0.9.0
	go.opentelemetry.io/otel/trace v0.0.0 // indirect
)

replace github.com/frostgrove/vv v0.9.0 => ` + replacement + `
replace go.opentelemetry.io/otel/trace => ` + trace + `
`,
		"jobsfx/jobsfx.go": "package jobsfx\n\nimport \"github.com/frostgrove/vv/thing\"\n\nvar Name = thing.Name\n",
	})
	installOTelDependencyGate(t, root)

	output, code := runCheck(t, root, "deps")
	if code == 0 || !strings.Contains(output, "github.com/frostgrove/vv/thing") || !strings.Contains(output, "go.opentelemetry.io/otel/trace") {
		t.Fatalf("the synthetic repository root shadowed an explicit selected replacement with %d:\n%s", code, output)
	}
}

func TestOTelDependencyGraphTreatsTaggedMainFilesAsStandaloneTools(t *testing.T) {
	t.Run("unconditional library plus non-importable tagged main", func(t *testing.T) {
		dependency := t.TempDir()
		writeInto(t, dependency, "go.mod", "module example.com/library\n\ngo 1.26\n")
		writeInto(t, dependency, "library.go", "package library\n")
		writeInto(t, dependency, "tool.go", "//go:build tool\n\npackage main\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
		root := reachableDependencyFixture(t, "example.com/library", dependency)
		finding, err := inspectReachableFixture(root)
		if err != nil || finding != "" {
			t.Fatalf("a standalone tool was treated as part of an imported library: finding=%q err=%v", finding, err)
		}
	})

	t.Run("only main cannot be imported", func(t *testing.T) {
		dependency := t.TempDir()
		writeInto(t, dependency, "go.mod", "module example.com/library\n\ngo 1.26\n")
		writeInto(t, dependency, "tool.go", "package main\n")
		root := reachableDependencyFixture(t, "example.com/library", dependency)
		_, err := inspectReachableFixture(root)
		if err == nil || !strings.Contains(err.Error(), "package declares main and cannot be imported") {
			t.Fatalf("a command-only directory was accepted as an imported package: %v", err)
		}
	})
}

func TestOTelDependencyGraphExcludesOwnTaggedMainTransitiveEdges(t *testing.T) {
	bridge := t.TempDir()
	writeInto(t, bridge, "go.mod", "module example.com/bridge\n\ngo 1.26\n")
	writeInto(t, bridge, "bridge.go", "package bridge\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
		"jobsfx/go.mod":    "module github.com/frostgrove/vv/jobsfx\n\ngo 1.26\n\nrequire example.com/bridge v0.0.0\n\nreplace example.com/bridge => " + bridge + "\n",
		"jobsfx/jobsfx.go": "package jobsfx\n",
		"jobsfx/tool.go":   "//go:build tool\n\npackage main\n\nimport _ \"example.com/bridge\"\n",
	})

	finding, err := inspectReachableFixture(root)
	if err != nil || finding != "" {
		t.Fatalf("a non-importable own command became a transitive runtime edge: finding=%q err=%v", finding, err)
	}
}

func TestOTelDependencyGraphRequiresANonMainConfiguration(t *testing.T) {
	dependency := t.TempDir()
	writeInto(t, dependency, "go.mod", "module example.com/library\n\ngo 1.26\n")
	writeInto(t, dependency, "library.go", "//go:build tool\n\npackage library\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
	writeInto(t, dependency, "tool.go", "//go:build tool\n\npackage main\n")
	root := reachableDependencyFixture(t, "example.com/library", dependency)

	finding, err := inspectReachableFixture(root)
	if err != nil || finding != "" {
		t.Fatalf("a non-main edge with no importable package configuration was traversed: finding=%q err=%v", finding, err)
	}
}

func TestOTelDependencyGraphHandlesUTF8BOMBuildConstraints(t *testing.T) {
	for _, test := range []struct {
		name        string
		constraint  string
		wantFinding bool
	}{
		{name: "ignore", constraint: "//go:build ignore\n\n"},
		{name: "custom tag", constraint: "//go:build bom_enabled\n\n", wantFinding: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dependency := t.TempDir()
			writeInto(t, dependency, "go.mod", "module example.com/library\n\ngo 1.26\n")
			writeInto(t, dependency, "library.go", "package library\n")
			writeInto(t, dependency, "hidden.go", "\xef\xbb\xbf"+test.constraint+"package library\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
			root := reachableDependencyFixture(t, "example.com/library", dependency)

			finding, err := inspectReachableFixture(root)
			if err != nil {
				t.Fatal(err)
			}
			if (finding != "") != test.wantFinding {
				t.Fatalf("BOM build constraint produced finding %q, want finding=%t", finding, test.wantFinding)
			}
		})
	}
}

func TestCheckDepsRefusesOTelInADirectTaggedMainTool(t *testing.T) {
	root := fixture(t, map[string]string{
		"go.mod":           libraryGoMod,
		"thing/thing.go":   librarySource,
		"tool.go":          "//go:build tool\n\npackage main\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
		"test/go.mod":      "module github.com/frostgrove/vv/test\n\ngo 1.26\n",
		"_examples/go.mod": "module github.com/frostgrove/vv/examples\n\ngo 1.26\n",
	})
	installOTelDependencyGate(t, root)

	output, code := runCheck(t, root, "deps")
	if code == 0 || !strings.Contains(output, "tool.go imports forbidden OpenTelemetry package") {
		t.Fatalf("a direct tagged main tool bypassed the dependency gate with %d:\n%s", code, output)
	}
}

func TestNoTestInTheRootModuleOfThisRepositoryImportsAThirdPartyPackage(t *testing.T) {
	command := exec.Command("bash", "checks.sh", "deps")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("check-deps refuses this repository:\n%s", output)
	}
}

const (
	kernelSource = "package event\n\nfunc Family() string { return \"event\" }\n"
	storeSource  = "package eventpg\n\nfunc Store() string { return \"eventpg\" }\n"
)

// A git repository of the fixture's own, because the arm under test compares a
// working tree against a recorded commit and the fixture the other cases use is
// a directory. Git being absent is a failure and never a skip: a self-test that
// skips is the shape the arm itself exists to refuse.
func eventKernelFixture(t *testing.T) (string, string) {
	t.Helper()
	root := fixture(t, map[string]string{
		"go.mod":                   libraryGoMod,
		"event/store.go":           kernelSource,
		"event/eventpg/eventpg.go": storeSource,
	})
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("check-event-kernel is a git-only arm and this environment has no git: %v", err)
	}
	git := func(args ...string) string {
		command := exec.Command("git", append([]string{"-c", "user.email=fixture@example.com", "-c", "user.name=fixture"}, args...)...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in the fixture answered %v:\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q", "-b", "main")
	git("add", ".")
	git("commit", "-q", "--no-gpg-sign", "-m", "the fixture kernel")
	return root, git("rev-parse", "HEAD")
}

func writeInto(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("cannot create %s: %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("cannot write %s: %v", name, err)
	}
}

// Four cases, and they do not all guard the same thing: the first is what a
// gutted arm fails, the second what an arm with no untracked half fails, the
// third what an over-broad one fails, and the fourth what a silently vacuous one
// fails. The third alone certifies nothing, which is why it is never run alone.
func TestCheckEventKernelReportsADifferenceAndOtherwiseOk(t *testing.T) {
	t.Run("a tracked file under event/ differs from the baseline", func(t *testing.T) {
		root, baseline := eventKernelFixture(t)
		writeInto(t, root, "event/store.go", kernelSource+"\nfunc Added() int { return 1 }\n")

		output, code := runCheckWithEnv(t, root, "event-kernel", "EVENT_KERNEL_BASELINE="+baseline)
		if code != 1 {
			t.Fatalf("the kernel moved and check-event-kernel exited %d:\n%s", code, output)
		}
		if !strings.Contains(output, "event/store.go") {
			t.Errorf("check-event-kernel refused without naming the file that moved:\n%s", output)
		}
	})

	t.Run("an untracked new file under event/", func(t *testing.T) {
		root, baseline := eventKernelFixture(t)
		writeInto(t, root, "event/second.go", kernelSource)

		output, code := runCheckWithEnv(t, root, "event-kernel", "EVENT_KERNEL_BASELINE="+baseline)
		if code != 1 {
			t.Fatalf("a new file under event/ that git diff cannot see was admitted with %d:\n%s", code, output)
		}
		if !strings.Contains(output, "event/second.go") {
			t.Errorf("check-event-kernel refused without naming the file nobody tracked:\n%s", output)
		}
	})

	t.Run("only event/eventpg differs", func(t *testing.T) {
		root, baseline := eventKernelFixture(t)
		writeInto(t, root, "event/eventpg/eventpg.go", storeSource+"\nfunc Added() int { return 1 }\n")
		writeInto(t, root, "event/eventpg/read.go", storeSource)

		output, code := runCheckWithEnv(t, root, "event-kernel", "EVENT_KERNEL_BASELINE="+baseline)
		if code != 0 {
			t.Fatalf("the store the exemption exists for moved and check-event-kernel exited %d:\n%s", code, output)
		}
	})

	t.Run("the baseline names a commit this repository does not carry", func(t *testing.T) {
		root, _ := eventKernelFixture(t)

		output, code := runCheckWithEnv(t, root, "event-kernel",
			"EVENT_KERNEL_BASELINE=0000000000000000000000000000000000000000")
		if code == 0 {
			t.Fatalf("the baseline resolves to nothing and check-event-kernel reported ok:\n%s", output)
		}
		if !strings.Contains(output, "EVENT_KERNEL_BASELINE") {
			t.Errorf("check-event-kernel refused without naming the constant a person has to set:\n%s", output)
		}
	})
}

func TestTheEventKernelOfThisRepositoryIsWhereThePhaseOneCommitLeftIt(t *testing.T) {
	command := exec.Command("bash", "checks.sh", "event-kernel")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("check-event-kernel refuses this repository:\n%s", output)
	}
}

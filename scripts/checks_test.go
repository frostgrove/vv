package scripts

import (
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
	command := exec.Command("bash", filepath.Join(root, "scripts", "checks.sh"), check)
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
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

func TestNoTestInTheRootModuleOfThisRepositoryImportsAThirdPartyPackage(t *testing.T) {
	command := exec.Command("bash", "checks.sh", "deps")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("check-deps refuses this repository:\n%s", output)
	}
}

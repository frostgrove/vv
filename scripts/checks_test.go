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
	return runCheckArguments(t, root, check)
}

func runCheckArguments(t *testing.T, root string, arguments ...string) (string, int) {
	t.Helper()
	command := exec.Command("bash", append([]string{filepath.Join(root, "scripts", "checks.sh")}, arguments...)...)
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local")
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("cannot run checks.sh %v: %v\n%s", arguments, err, output)
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

const (
	kernelSource = "package event\n\nfunc Family() string { return \"event\" }\n"
	storeSource  = "package eventpg\n\nfunc Store() string { return \"eventpg\" }\n"
)

const eventKernelManifest = "scripts/event_kernel.sha256"

// A directory with its own recorded manifest, and no git anywhere: the arm under
// test compares the tree against a file it wrote itself, which is what lets it
// run from a tarball or a vendor directory.
func eventKernelFixture(t *testing.T) string {
	t.Helper()
	root := fixture(t, map[string]string{
		"go.mod":                   libraryGoMod,
		"event/store.go":           kernelSource,
		"event/eventpg/eventpg.go": storeSource,
	})
	if output, code := runCheck(t, root, "event-kernel-baseline"); code != 0 {
		t.Fatalf("the fixture recorded no manifest of its own, so no case below compares anything:\n%s", output)
	}
	return root
}

// The manifest the section started from, kept beside the fixture rather than
// inside event/, so recording it does not move what it is a record of.
func keepAside(t *testing.T, root string) string {
	t.Helper()
	recorded, err := os.ReadFile(filepath.Join(root, eventKernelManifest))
	if err != nil {
		t.Fatalf("the fixture wrote no manifest to keep aside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "predecessor.sha256"), recorded, 0o644); err != nil {
		t.Fatalf("cannot keep the fixture's predecessor aside: %v", err)
	}
	return "predecessor.sha256"
}

func fencedFixture(t *testing.T) (string, string) {
	t.Helper()
	root := eventKernelFixture(t)
	return root, keepAside(t, root)
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

func removeFrom(t *testing.T, root, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatalf("cannot remove %s: %v", name, err)
	}
}

// Five cases, and they do not all guard the same thing: the first is what a
// gutted arm fails, the second what a whole-tree digest fails, the third what a
// walk that looked each recorded file up rather than diffing two listings fails,
// the fourth what an over-broad arm fails, and the fifth what a silently vacuous
// one fails. The fourth alone certifies nothing, which is why it is never run
// alone.
func TestCheckEventKernelReportsADifferenceAndOtherwiseOk(t *testing.T) {
	t.Run("a file under event/ differs from the manifest", func(t *testing.T) {
		root := eventKernelFixture(t)
		writeInto(t, root, "event/store.go", kernelSource+"\nfunc Added() int { return 1 }\n")

		output, code := runCheck(t, root, "event-kernel")
		if code != 1 {
			t.Fatalf("the kernel moved and check-event-kernel exited %d:\n%s", code, output)
		}
		if !strings.Contains(output, "event/store.go") {
			t.Errorf("check-event-kernel refused without naming the file that moved:\n%s", output)
		}
	})

	t.Run("a new file under event/ that the manifest does not record", func(t *testing.T) {
		root := eventKernelFixture(t)
		writeInto(t, root, "event/second.go", kernelSource)

		output, code := runCheck(t, root, "event-kernel")
		if code != 1 {
			t.Fatalf("a file the manifest never recorded was admitted with %d:\n%s", code, output)
		}
		if !strings.Contains(output, "event/second.go") {
			t.Errorf("check-event-kernel refused without naming the file that appeared:\n%s", output)
		}
	})

	t.Run("a file removed from event/", func(t *testing.T) {
		root := eventKernelFixture(t)
		writeInto(t, root, "event/second.go", kernelSource)
		if output, code := runCheck(t, root, "event-kernel-baseline"); code != 0 {
			t.Fatalf("the two-file fixture could not be recorded:\n%s", output)
		}
		removeFrom(t, root, "event/second.go")

		output, code := runCheck(t, root, "event-kernel")
		if code != 1 {
			t.Fatalf("a recorded file disappeared and check-event-kernel exited %d:\n%s", code, output)
		}
		if !strings.Contains(output, "event/second.go") {
			t.Errorf("check-event-kernel refused without naming the file that disappeared:\n%s", output)
		}
	})

	t.Run("only event/eventpg differs", func(t *testing.T) {
		root := eventKernelFixture(t)
		writeInto(t, root, "event/eventpg/eventpg.go", storeSource+"\nfunc Added() int { return 1 }\n")
		writeInto(t, root, "event/eventpg/read.go", storeSource)

		output, code := runCheck(t, root, "event-kernel")
		if code != 0 {
			t.Fatalf("the store the exemption exists for moved and check-event-kernel exited %d:\n%s", code, output)
		}
	})

	t.Run("the manifest is not there to compare against", func(t *testing.T) {
		root := eventKernelFixture(t)
		removeFrom(t, root, eventKernelManifest)

		output, code := runCheck(t, root, "event-kernel")
		if code == 0 {
			t.Fatalf("there was nothing to compare the kernel with and check-event-kernel reported ok:\n%s", output)
		}
		if !strings.Contains(output, eventKernelManifest) || !strings.Contains(output, "check-event-kernel-baseline") {
			t.Errorf("check-event-kernel refused without naming the file it could not read and the command that records one:\n%s", output)
		}
	})
}

// check-event-kernel is green by construction the instant the baseline is
// regenerated, so these five are what stands between a phase and an unrecorded
// kernel edit. The first is the one the committed-diff pipeline this replaces
// could not have: it passed on an unchanged manifest and called that success.
func TestTheKernelFenceRefusesAMoveItWasNotToldAbout(t *testing.T) {
	t.Run("the predecessor and the manifest record the same kernel", func(t *testing.T) {
		root, predecessor := fencedFixture(t)

		output, code := runCheckArguments(t, root, "event-kernel-moved", predecessor, `^event/store\.go$`)
		if code != 1 {
			t.Fatalf("nothing under event/ moved and the fence exited %d:\n%s", code, output)
		}
		if !strings.Contains(output, "did not deliver") {
			t.Errorf("the fence failed without saying that a section which moved no kernel file did not deliver:\n%s", output)
		}
	})

	t.Run("a path in the moved set outside the allowed set", func(t *testing.T) {
		root, predecessor := fencedFixture(t)
		writeInto(t, root, "event/store.go", kernelSource+"\nfunc Added() int { return 1 }\n")
		writeInto(t, root, "event/second.go", kernelSource)
		if output, code := runCheck(t, root, "event-kernel-baseline"); code != 0 {
			t.Fatalf("the moved fixture could not be recorded:\n%s", output)
		}

		output, code := runCheckArguments(t, root, "event-kernel-moved", predecessor, `^event/store\.go$`, "event/store.go")
		if code != 1 {
			t.Fatalf("a file nobody planned for moved and the fence exited %d:\n%s", code, output)
		}
		if !strings.Contains(output, "event/second.go") {
			t.Errorf("the fence failed without naming the unplanned kernel edit:\n%s", output)
		}
	})

	t.Run("a required path absent from the moved set", func(t *testing.T) {
		root, predecessor := fencedFixture(t)
		writeInto(t, root, "event/second.go", kernelSource)
		if output, code := runCheck(t, root, "event-kernel-baseline"); code != 0 {
			t.Fatalf("the moved fixture could not be recorded:\n%s", output)
		}

		output, code := runCheckArguments(t, root, "event-kernel-moved", predecessor,
			`^event/(store|second)\.go$`, "event/store.go")
		if code != 1 {
			t.Fatalf("a file the section promised did not move and the fence exited %d:\n%s", code, output)
		}
		if !strings.Contains(output, "event/store.go") {
			t.Errorf("the fence failed without naming the file the section did not deliver:\n%s", output)
		}
	})

	t.Run("the predecessor is not there to compare against", func(t *testing.T) {
		root := eventKernelFixture(t)

		output, code := runCheckArguments(t, root, "event-kernel-moved", "nothing.sha256", `^event/store\.go$`)
		if code == 0 {
			t.Fatalf("there was nothing to compare the move with and the fence reported ok:\n%s", output)
		}
		if !strings.Contains(output, "nothing.sha256") || !strings.Contains(output, "event-kernel-baseline") {
			t.Errorf("the fence refused without naming the file it could not read and the command that records one:\n%s", output)
		}
	})

	// The honest case, and it carries the removal half: a moved set computed in
	// one direction only holds the file that changed and not the one that
	// disappeared, so the promised path is missing and this exits 1.
	t.Run("every required path present, one because it changed and one because it went", func(t *testing.T) {
		root := eventKernelFixture(t)
		writeInto(t, root, "event/second.go", kernelSource)
		if output, code := runCheck(t, root, "event-kernel-baseline"); code != 0 {
			t.Fatalf("the two-file fixture could not be recorded:\n%s", output)
		}
		predecessor := keepAside(t, root)
		writeInto(t, root, "event/store.go", kernelSource+"\nfunc Added() int { return 1 }\n")
		removeFrom(t, root, "event/second.go")
		if output, code := runCheck(t, root, "event-kernel-baseline"); code != 0 {
			t.Fatalf("the moved fixture could not be recorded:\n%s", output)
		}

		output, code := runCheckArguments(t, root, "event-kernel-moved", predecessor,
			`^event/(store|second)\.go$`, "event/store.go", "event/second.go")
		if code != 0 {
			t.Fatalf("the section moved exactly what it promised and the fence exited %d:\n%s", code, output)
		}
		if !strings.Contains(output, "event/store.go") || !strings.Contains(output, "event/second.go") {
			t.Errorf("the fence passed without printing the moved set in both directions:\n%s", output)
		}
	})
}

func TestTheEventKernelOfThisRepositoryIsWhereThisPhaseLeftIt(t *testing.T) {
	command := exec.Command("bash", "checks.sh", "event-kernel")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("check-event-kernel refuses this repository:\n%s", output)
	}
}

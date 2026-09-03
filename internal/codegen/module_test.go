package codegen

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const moduleRootSource = `package workspace

import (
	"github.com/frostgrove/vv/health"
)

func converterCheck() health.Contribution { return health.Contribution{} }

func Names() []string { return nil }
`

const moduleContractSource = `package contract

import "github.com/frostgrove/vv/runtime"

type Repo struct{}

func NewRepo() *Repo { return &Repo{} }

func NewSweeper() runtime.Runner { return nil }

func Label() string { return "" }

func newHidden() *Repo { return &Repo{} }
`

func moduleTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, source := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func workspaceTree(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleTree(t, map[string]string{
		"workspace/workspace.go":         moduleRootSource,
		"workspace/contract/contract.go": moduleContractSource,
	}), "workspace")
}

func moduleOptions(dir string) *ModuleOptions {
	return &ModuleOptions{Dir: dir, Import: "example.test/workspace"}
}

func readModuleManifestAt(t *testing.T, dir string) *moduleManifestDocument {
	t.Helper()
	document, err := readModuleManifest(filepath.Join(dir, DefaultModuleFile), packageNameOf(dir))
	if err != nil {
		t.Fatal(err)
	}
	if document == nil {
		t.Fatal("no manifest was written")
	}
	return document
}

func writeModuleManifestAt(t *testing.T, dir string, document *moduleManifestDocument) {
	t.Helper()
	source, err := marshalModuleManifest(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultModuleFile), source, 0o644); err != nil {
		t.Fatal(err)
	}
}

func confirmEveryContribution(t *testing.T, dir string) {
	t.Helper()
	document := readModuleManifestAt(t, dir)
	for index := range document.Contributions {
		document.Contributions[index].Confirmed = !document.Contributions[index].Excluded
	}
	writeModuleManifestAt(t, dir, document)
}

func kindOf(t *testing.T, document *moduleManifestDocument, symbol string) moduleManifestContribution {
	t.Helper()
	for _, contribution := range document.Contributions {
		if contribution.Symbol == symbol {
			return contribution
		}
	}
	t.Fatalf("the manifest carries %v, and not %s", document.Contributions, symbol)
	return moduleManifestContribution{}
}

func TestTheKindOfAContributionIsInferredFromWhatItsConstructorReturns(t *testing.T) {
	dir := workspaceTree(t)

	err := RunModule(moduleOptions(dir))
	var confirmation *ModuleConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("a module nobody confirmed was generated: %v", err)
	}
	if len(confirmation.Contributions) != 3 {
		t.Fatalf("the refusal names %v, not every contribution the walk found", confirmation.Contributions)
	}

	document := readModuleManifestAt(t, dir)
	if kind := kindOf(t, document, "contract.NewRepo"); kind.Kind != kindProvide || kind.Source != sourceFromSignature {
		t.Fatalf("a constructor returning a struct became %s from %s", kind.Kind, kind.Source)
	}
	if kind := kindOf(t, document, "contract.NewSweeper"); kind.Kind != kindWorker {
		t.Fatalf("a constructor returning runtime.Runner became %s, so the profile would carry it everywhere", kind.Kind)
	}
	if kind := kindOf(t, document, "converterCheck"); kind.Kind != kindCheck {
		t.Fatalf("a constructor returning health.Contribution became %s", kind.Kind)
	}
	for _, absent := range []string{"Names", "contract.Label", "contract.newHidden"} {
		for _, contribution := range document.Contributions {
			if contribution.Symbol == absent {
				t.Fatalf("%s is not something a container builds, and the walk offered it anyway", absent)
			}
		}
	}
}

func TestAnUnconfirmedModuleLeavesAFileThatWillNotCompile(t *testing.T) {
	dir := workspaceTree(t)

	if err := RunModule(moduleOptions(dir)); err == nil {
		t.Fatal("the first run generated a definition nobody had confirmed")
	}

	generated := readFile(t, filepath.Join(dir, DefaultModuleOut))
	if !strings.Contains(generated, moduleHint) {
		t.Fatalf("the placeholder does not say what to do:\n%s", generated)
	}
	if strings.Contains(generated, "MustDefine") {
		t.Fatalf("the placeholder is a working definition:\n%s", generated)
	}
}

func TestAConfirmedModuleBecomesOneDefinitionSortedIntoTheKindItWasGiven(t *testing.T) {
	dir := workspaceTree(t)

	if err := RunModule(moduleOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryContribution(t, dir)
	if err := RunModule(moduleOptions(dir)); err != nil {
		t.Fatalf("generating the confirmed module: %v", err)
	}

	generated := readFile(t, filepath.Join(dir, DefaultModuleOut))
	for _, expected := range []string{
		`vvmodule "github.com/frostgrove/vv/app/module"`,
		`contract "example.test/workspace/contract"`,
		`Name:  "workspace"`,
		"Provide: []any{\n\t\tcontract.NewRepo,\n\t}",
		"Workers: []any{\n\t\tcontract.NewSweeper,\n\t}",
		"Checks: []any{\n\t\tconverterCheck,\n\t}",
	} {
		if !strings.Contains(generated, expected) {
			t.Fatalf("the generated definition is missing %q:\n%s", expected, generated)
		}
	}
}

func TestChangingWhatAConstructorReturnsWithdrawsOnlyItsOwnConfirmation(t *testing.T) {
	dir := workspaceTree(t)

	if err := RunModule(moduleOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryContribution(t, dir)
	if err := RunModule(moduleOptions(dir)); err != nil {
		t.Fatalf("generating the confirmed module: %v", err)
	}

	changed := strings.Replace(moduleContractSource, "func NewRepo() *Repo", "func NewRepo(name string) *Repo", 1)
	if changed == moduleContractSource {
		t.Fatal("the fixture did not change, so the withdrawal would be measured against an untouched signature")
	}
	if err := os.WriteFile(filepath.Join(dir, "contract", "contract.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RunModule(moduleOptions(dir))
	var confirmation *ModuleConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("a constructor whose signature changed kept its confirmation: %v", err)
	}
	if len(confirmation.Contributions) != 1 || confirmation.Contributions[0] != "contract.NewRepo" {
		t.Fatalf("the refusal names %v, not the one constructor that changed", confirmation.Contributions)
	}
}

func TestAKindWrittenIntoTheManifestOutranksTheOneInferredFromTheSignature(t *testing.T) {
	dir := workspaceTree(t)

	if err := RunModule(moduleOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	document := readModuleManifestAt(t, dir)
	for index := range document.Contributions {
		document.Contributions[index].Confirmed = true
		if document.Contributions[index].Symbol == "contract.NewRepo" {
			document.Contributions[index].Kind = kindRoute
		}
	}
	writeModuleManifestAt(t, dir, document)

	if err := RunModule(moduleOptions(dir)); err != nil {
		t.Fatalf("generating over a hand-written kind: %v", err)
	}
	if kind := kindOf(t, readModuleManifestAt(t, dir), "contract.NewRepo"); kind.Kind != kindRoute || kind.Source != sourceFromManifest {
		t.Fatalf("the hand-written kind became %s from %s", kind.Kind, kind.Source)
	}

	generated := readFile(t, filepath.Join(dir, DefaultModuleOut))
	if !strings.Contains(generated, "Routes: []any{\n\t\tcontract.NewRepo,\n\t}") {
		t.Fatalf("the definition did not follow the manifest:\n%s", generated)
	}
	if strings.Contains(generated, "Provide:") {
		t.Fatalf("the inferred kind survived beside the confirmed one:\n%s", generated)
	}
}

func TestAContributionExcludedByHandIsNeitherWaitedForNorGenerated(t *testing.T) {
	dir := workspaceTree(t)

	if err := RunModule(moduleOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	document := readModuleManifestAt(t, dir)
	for index := range document.Contributions {
		document.Contributions[index].Confirmed = document.Contributions[index].Symbol != "contract.NewSweeper"
		document.Contributions[index].Excluded = document.Contributions[index].Symbol == "contract.NewSweeper"
	}
	writeModuleManifestAt(t, dir, document)

	if err := RunModule(moduleOptions(dir)); err != nil {
		t.Fatalf("an excluded contribution was still waited for: %v", err)
	}
	generated := readFile(t, filepath.Join(dir, DefaultModuleOut))
	if strings.Contains(generated, "NewSweeper") {
		t.Fatalf("an excluded constructor reached the definition:\n%s", generated)
	}
}

func TestAModuleWhoseEveryContributionIsExcludedIsRefusedRatherThanWritten(t *testing.T) {
	dir := workspaceTree(t)

	if err := RunModule(moduleOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	document := readModuleManifestAt(t, dir)
	for index := range document.Contributions {
		document.Contributions[index].Excluded = true
	}
	writeModuleManifestAt(t, dir, document)

	err := RunModule(moduleOptions(dir))
	if err == nil || !strings.Contains(err.Error(), "contributes nothing") {
		t.Fatalf("a module offering nothing was accepted: %v", err)
	}
}

func TestTheModuleCheckReportsDriftWithoutWriting(t *testing.T) {
	dir := workspaceTree(t)

	if err := RunModule(moduleOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryContribution(t, dir)
	if err := RunModule(moduleOptions(dir)); err != nil {
		t.Fatalf("generating the confirmed module: %v", err)
	}

	checking := moduleOptions(dir)
	checking.Check = true
	if err := RunModule(checking); err != nil {
		t.Fatalf("the file it had just written was called stale: %v", err)
	}

	out := filepath.Join(dir, DefaultModuleOut)
	fallBehind(t, out)
	stale := readFile(t, out)

	err := RunModule(checking)
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("a definition behind its package passed the check: %v", err)
	}
	if len(drift.Paths) != 1 || drift.Paths[0] != out {
		t.Fatalf("the check names %v, not the file that is behind the package", drift.Paths)
	}
	if readFile(t, out) != stale {
		t.Fatal("the check rewrote the file it was asked only to read")
	}
}

func TestAPackageThatAlreadyDeclaresTheModuleVariableKeepsItsOwn(t *testing.T) {
	dir := workspaceTree(t)
	if err := os.WriteFile(filepath.Join(dir, "own.go"),
		[]byte("package workspace\n\nvar VVModule = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunModule(moduleOptions(dir)); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryContribution(t, dir)

	err := RunModule(moduleOptions(dir))
	if err == nil || !strings.Contains(err.Error(), "already declares VVModule") {
		t.Fatalf("the generator wrote over a name the package owns: %v", err)
	}
}

func TestTheGeneratedDefinitionCompilesAndActivatesByRole(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tree := moduleTree(t, map[string]string{
		"go.mod":                         "module workspacefixture\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => " + root + "\n",
		"workspace/workspace.go":         moduleRootSource,
		"workspace/contract/contract.go": moduleContractSource,
		"main.go": `package main

import (
	"fmt"

	"github.com/frostgrove/vv/app/module"

	"workspacefixture/workspace"
)

func main() {
	fmt.Println(workspace.VVModule.Name(),
		len(workspace.VVModule.Active(module.Complete)),
		len(workspace.VVModule.Active(module.Base)))
}
`,
	})
	dir := filepath.Join(tree, "workspace")

	if err := RunModule(&ModuleOptions{Dir: dir}); err == nil {
		t.Fatal("the first run generated without confirmation")
	}
	confirmEveryContribution(t, dir)
	if err := RunModule(&ModuleOptions{Dir: dir}); err != nil {
		t.Fatalf("generating the confirmed module: %v", err)
	}

	command := exec.Command("go", "run", ".")
	command.Dir = tree
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	response, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("the generated definition does not build: %v\n%s\n--- generated ---\n%s",
			err, response, readFile(t, filepath.Join(dir, DefaultModuleOut)))
	}
	if strings.TrimSpace(string(response)) != "workspace 3 2" {
		t.Fatalf("the definition answers %q: the worker is not held back from a profile that carries no worker role", response)
	}
}

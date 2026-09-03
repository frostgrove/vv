package codegen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const checkedModel = `package m

type Invoice struct {
	ID     int64  @db:"id,pk,auto"@
	Number string @db:"number"@
}
`

func checkedPackage(t *testing.T, dir, source string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(tags(source)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func grownBy(t *testing.T, source, column string) string {
	t.Helper()
	grown := strings.Replace(source, "\tNumber string @db:\"number\"@", "\tNumber string @db:\"number\"@\n\t"+column, 1)
	if grown == source {
		t.Fatal("the fixture did not gain a column, so the check would be measured against an unchanged model")
	}
	return tags(grown)
}

func TestAColumnTheGeneratedFileNeverSawIsNamedInsteadOfWrittenOver(t *testing.T) {
	dir := t.TempDir()
	checkedPackage(t, dir, checkedModel)
	out := filepath.Join(dir, "vv_gen.go")

	if err := Run(&Options{Dir: dir, WithDTO: true, WithMeta: true, Binding: "none"}); err != nil {
		t.Fatalf("generating: %v", err)
	}
	if err := Run(&Options{Dir: dir, WithDTO: true, WithMeta: true, Binding: "none", Check: true}); err != nil {
		t.Fatalf("the file it had just written was called stale: %v", err)
	}
	generated := readFile(t, out)

	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(grownBy(t, checkedModel, `Total  int64  @db:"total"@`)), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Run(&Options{Dir: dir, WithDTO: true, WithMeta: true, Binding: "none", Check: true})
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("a column the DTO and the metamodel never saw passed the check: %v", err)
	}
	if len(drift.Paths) != 1 || drift.Paths[0] != out {
		t.Fatalf("the check names %v, not the file that is behind the model", drift.Paths)
	}
	if readFile(t, out) != generated {
		t.Fatal("the check rewrote the file it was asked only to read")
	}
}

func TestAPackageWithNoGeneratedFileAtAllFailsTheCheckWithoutCreatingOne(t *testing.T) {
	dir := t.TempDir()
	checkedPackage(t, dir, checkedModel)

	if err := Run(&Options{Dir: dir, WithDTO: true, WithMeta: true, Binding: "none", Check: true}); err == nil {
		t.Fatal("a package that had never been generated passed the check")
	}
	if _, err := os.Stat(filepath.Join(dir, "vv_gen.go")); !os.IsNotExist(err) {
		t.Fatal("the check created the file it was asked only to read")
	}
}

func TestTheRecursiveCheckNamesEveryPackageBehindItsModelsAndNotOnlyTheFirst(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "billing"), filepath.Join(root, "shipping")
	checkedPackage(t, first, checkedModel)
	checkedPackage(t, second, strings.Replace(checkedModel, "package m", "package n", 1))

	if err := Run(&Options{Dir: root, WithDTO: true, WithMeta: true, Binding: "none", Recursive: true}); err != nil {
		t.Fatalf("generating: %v", err)
	}
	if err := Run(&Options{Dir: root, WithDTO: true, WithMeta: true, Binding: "none", Recursive: true, Check: true}); err != nil {
		t.Fatalf("the tree it had just generated was called stale: %v", err)
	}

	grown := grownBy(t, checkedModel, `Total  int64  @db:"total"@`)
	if err := os.WriteFile(filepath.Join(first, "model.go"), []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "model.go"), []byte(strings.Replace(grown, "package m", "package n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Run(&Options{Dir: root, WithDTO: true, WithMeta: true, Binding: "none", Recursive: true, Check: true})
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("two packages behind their models passed the check: %v", err)
	}
	if len(drift.Paths) != 2 {
		t.Fatalf("the check names %v, but both packages are behind their models", drift.Paths)
	}
}

func wirePackageAt(t *testing.T, dir, source string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(tags(source)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fallBehind(t *testing.T, path string) {
	t.Helper()
	current := readFile(t, path)
	if err := os.WriteFile(path, []byte(current+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTheRecursiveWireCheckNamesEveryStalePackageAndNotOnlyTheFirst(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "billing"), filepath.Join(root, "shipping")
	wirePackageAt(t, first, wireModel)
	wirePackageAt(t, second, strings.Replace(wireModel, "package m", "package n", 1))

	if err := RunResource(&ResourceOptions{Dir: root, Recursive: true}); err != nil {
		t.Fatalf("generating: %v", err)
	}
	if err := RunResource(&ResourceOptions{Dir: root, Recursive: true, Check: true}); err != nil {
		t.Fatalf("the tree it had just written was called stale: %v", err)
	}

	firstOut := filepath.Join(first, "vv_wire_gen.go")
	secondOut := filepath.Join(second, "vv_wire_gen.go")
	fallBehind(t, firstOut)
	fallBehind(t, secondOut)

	err := RunResource(&ResourceOptions{Dir: root, Recursive: true, Check: true})
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("two packages behind their models passed the check: %v", err)
	}
	if len(drift.Paths) != 2 || drift.Paths[0] != firstOut || drift.Paths[1] != secondOut {
		t.Fatalf("the check names %v, and a caller who fixes that has to run it again to learn the rest", drift.Paths)
	}
}

func TestTheRecursiveWireRunNamesEveryBodyWaitingForConfirmationAndSaysWhichPackageItIsIn(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "billing"), filepath.Join(root, "shipping")
	wirePackageAt(t, first, wireModel)
	wirePackageAt(t, second, strings.Replace(wireModel, "package m", "package n", 1))

	if err := RunResource(&ResourceOptions{Dir: root, Recursive: true}); err != nil {
		t.Fatalf("generating: %v", err)
	}
	publish(t, first, "Account", "create", []string{"Email", "Locked", "Password"}, false)
	publish(t, second, "Account", "create", []string{"Email", "Locked", "Password"}, false)

	err := RunResource(&ResourceOptions{Dir: root, Recursive: true})
	var confirmation *ConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("two widened bodies nobody confirmed were generated: %v", err)
	}
	if len(confirmation.Bodies) != 2 ||
		confirmation.Bodies[0] != "billing.Account create" ||
		confirmation.Bodies[1] != "shipping.Account create" {
		t.Fatalf("the refusal names %v, not every body waiting and the package it is in", confirmation.Bodies)
	}
}

func TestTheRecursiveRoutesCheckNamesEveryStalePackageAndNotOnlyTheFirst(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "billing"), filepath.Join(root, "ops")
	toBilling := func(source string) string {
		return strings.ReplaceAll(strings.Replace(source, "package ops", "package billing", 1),
			"DeadJobsUseCase", "InvoicesUseCase")
	}
	for dir, files := range map[string]map[string]string{
		first:  {"invoices.usecase.go": toBilling(guardedUseCase), "billing.http-handler.go": toBilling(declaringHandler)},
		second: {"dead-jobs.usecase.go": guardedUseCase, "ops.http-handler.go": declaringHandler},
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, source := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	walking := routeOptions(root)
	walking.Recursive = true
	if err := RunRoutes(walking); err == nil {
		t.Fatal("two inferred pairs were generated without anybody confirming them")
	}
	confirmEveryOperation(t, first)
	confirmEveryOperation(t, second)
	if err := RunRoutes(walking); err != nil {
		t.Fatalf("generating the confirmed routes: %v", err)
	}

	firstOut := filepath.Join(first, DefaultRoutesOut)
	secondOut := filepath.Join(second, DefaultRoutesOut)
	fallBehind(t, firstOut)
	fallBehind(t, secondOut)

	checking := routeOptions(root)
	checking.Recursive, checking.Check = true, true
	err := RunRoutes(checking)
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("two packages whose routes file is behind the guard passed the check: %v", err)
	}
	if len(drift.Paths) != 2 || drift.Paths[0] != firstOut || drift.Paths[1] != secondOut {
		t.Fatalf("the check names %v, and a caller who fixes that has to run it again to learn the rest", drift.Paths)
	}
}

func moduleFixture(t *testing.T, name string) map[string]string {
	t.Helper()
	return map[string]string{
		name + "/" + name + ".go":      strings.Replace(moduleRootSource, "package workspace", "package "+name, 1),
		name + "/contract/contract.go": moduleContractSource,
	}
}

func twoModules(t *testing.T) string {
	t.Helper()
	files := map[string]string{"go.mod": "module modulefixture\n\ngo 1.26\n"}
	for _, name := range []string{"billing", "shipping"} {
		for path, source := range moduleFixture(t, name) {
			files[path] = source
		}
	}
	return moduleTree(t, files)
}

func TestTheRecursiveModuleRunNamesEveryContributionWaitingAndSaysWhichModuleItIsIn(t *testing.T) {
	root := twoModules(t)

	err := RunModule(&ModuleOptions{Dir: root, Recursive: true})
	var confirmation *ModuleConfirmationError
	if !errors.As(err, &confirmation) {
		t.Fatalf("two unconfirmed modules were generated: %v", err)
	}
	if len(confirmation.Contributions) != 6 ||
		confirmation.Contributions[0] != "billing.contract.NewRepo" ||
		confirmation.Contributions[5] != "shipping.converterCheck" {
		t.Fatalf("the walk names %v, not every contribution waiting and the module it is in", confirmation.Contributions)
	}
}

func TestTheRecursiveModuleCheckNamesEveryStaleModuleAndNotOnlyTheFirst(t *testing.T) {
	root := twoModules(t)
	first, second := filepath.Join(root, "billing"), filepath.Join(root, "shipping")

	if err := RunModule(&ModuleOptions{Dir: root, Recursive: true}); err == nil {
		t.Fatal("two unconfirmed modules were generated")
	}
	confirmEveryContribution(t, first)
	confirmEveryContribution(t, second)
	if err := RunModule(&ModuleOptions{Dir: root, Recursive: true}); err != nil {
		t.Fatalf("generating the confirmed modules: %v", err)
	}
	if err := RunModule(&ModuleOptions{Dir: root, Recursive: true, Check: true}); err != nil {
		t.Fatalf("the tree it had just written was called stale: %v", err)
	}

	firstOut := filepath.Join(first, DefaultModuleOut)
	secondOut := filepath.Join(second, DefaultModuleOut)
	fallBehind(t, firstOut)
	fallBehind(t, secondOut)

	err := RunModule(&ModuleOptions{Dir: root, Recursive: true, Check: true})
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("two modules behind their packages passed the check: %v", err)
	}
	if len(drift.Paths) != 2 || drift.Paths[0] != firstOut || drift.Paths[1] != secondOut {
		t.Fatalf("the check names %v, and a caller who fixes that has to run it again to learn the rest", drift.Paths)
	}
}

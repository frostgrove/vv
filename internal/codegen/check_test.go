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

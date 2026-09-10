package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

const sampleSource = `package sample

const Public = "value"
const private = "ignored"
var Exported []string
var privateVar int

type Alias = Box[int]

type Box[T ~int] struct {
	Public T ` + "`json:\"public\"`" + `
	private int
}

func (Box[T]) Value(input T) T { return input }
func (*Box[T]) Pointer(input ...T) (result T, err error) { return }

type Embedded interface {
	Exported(input int) error
}

type Contract interface {
	Embedded
	Do(input string) (result int)
	private()
}

func Generic[T interface { ~int | ~string }](input T, rest ...string) (result T, err error) {
	return
}

func privateFunc() {}
`

func TestRenderPackageCapturesTheCompleteExportedContract(t *testing.T) {
	surface := renderSource(t, sampleSource)
	for _, fragment := range []string{
		`const Public untyped string = "value"`,
		`var Exported []string`,
		`type Alias = Box[int]`,
		`func (Alias) Value(int) int`,
		`func (*Alias) Pointer(...int) (int, error)`,
		`type Box[T ~int] struct {`,
		`Public T "json:\"public\""`,
		`<unexported fields>`,
		`func (Box[T]) Value(T) T`,
		`func (*Box[T]) Pointer(...T) (T, error)`,
		`type Contract interface {`,
		`Embedded`,
		`Do(string) int`,
		`<unexported methods>`,
		`func Generic[T interface{~int | ~string}](T, ...string) (T, error)`,
	} {
		if !strings.Contains(surface, fragment) {
			t.Fatalf("surface does not contain %q:\n%s", fragment, surface)
		}
	}
	for _, fragment := range []string{"privateVar", "privateFunc", "private int", "input", "result"} {
		if strings.Contains(surface, fragment) {
			t.Fatalf("surface contains implementation detail %q:\n%s", fragment, surface)
		}
	}
}

func TestRenderPackageIgnoresImplementationOnlyChanges(t *testing.T) {
	changed := strings.NewReplacer(
		`const private = "ignored"`, `const renamed = "different"`,
		`private int`, `hidden map[string]int`,
		`Value(input T) T { return input }`, `Value(value T) T { var ignored int; _ = ignored; return value }`,
		`Pointer(input ...T) (result T, err error)`, `Pointer(values ...T) (answer T, failure error)`,
		`Generic[T interface { ~int | ~string }](input T, rest ...string) (result T, err error)`, `Generic[T interface { ~int | ~string }](value T, tail ...string) (answer T, failure error)`,
	).Replace(sampleSource)
	if before, after := renderSource(t, sampleSource), renderSource(t, changed); before != after {
		t.Fatalf("implementation-only edits changed the surface:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

func TestRenderPackageDetectsContractChanges(t *testing.T) {
	mutations := []string{
		strings.Replace(sampleSource, `const Public = "value"`, `const Public = "changed"`, 1),
		strings.Replace(sampleSource, `Public T `+"`json:\"public\"`", `Public T `+"`json:\"value\"`", 1),
		strings.Replace(sampleSource, `Public T `+"`json:\"public\"`", `Public string `+"`json:\"public\"`", 1),
		strings.Replace(sampleSource, `Do(input string) (result int)`, `Do(input []byte) (result int)`, 1),
		strings.Replace(sampleSource, `Value(input T) T`, `Value(input T) any`, 1),
	}
	baseline := renderSource(t, sampleSource)
	for index, mutation := range mutations {
		if got := renderSource(t, mutation); got == baseline {
			t.Fatalf("mutation %d did not change the surface", index)
		}
	}
}

func renderSource(t *testing.T, source string) string {
	t.Helper()
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "sample.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	configuration := types.Config{GoVersion: "go1.26"}
	pkg, err := configuration.Check("example.com/sample", files, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return renderPackage(pkg)
}

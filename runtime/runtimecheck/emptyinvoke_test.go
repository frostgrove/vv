package runtimecheck_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frostgrove/vv/runtime/runtimecheck"
)

func TestAnInvokeThatNamesAnEmptyFunctionIsAnActivationEvenThoughItIsNotALiteral(t *testing.T) {
	root := treeOf(t, map[string]string{
		"core/redis/module.go": `package redis

import "go.uber.org/fx"

func Module() fx.Option { return fx.Invoke(reached) }
`,
		"core/redis/client.go": `package redis

type Client struct{}

func reached(Client) {}
`,
	})

	found := scan(t, root)
	if len(found) != 1 {
		t.Fatalf("an fx.Invoke of an empty named function read as ordinary wiring: %v", found)
	}
	if found[0].File != "core/redis/module.go" || found[0].Name != "reached" || found[0].Line != 5 {
		t.Fatalf("the activation was reported as %v", found[0])
	}
}

func TestAnEmptyFunctionLiteralPassedToInvokeIsAnActivation(t *testing.T) {
	root := treeOf(t, map[string]string{"crudsqlfx/crudsqlfx.go": `package crudsqlfx

import "go.uber.org/fx"

var option = fx.Invoke(func(Source) {})

type Source struct{}
`})

	found := scan(t, root)
	if len(found) != 1 || found[0].Name != "func literal" {
		t.Fatalf("an empty function literal read as ordinary wiring: %v", found)
	}
}

func TestAnInvokeWhoseFunctionHasABodyIsOrdinaryWiring(t *testing.T) {
	root := treeOf(t, map[string]string{"wiring/wiring.go": `package wiring

import "go.uber.org/fx"

var options = fx.Options(
	fx.Invoke(bind),
	fx.Invoke(func(thing *Thing) { thing.Start() }),
)

type Thing struct{}

func (this *Thing) Start() {}

func bind(thing *Thing) { thing.Start() }
`})

	if found := scan(t, root); len(found) != 0 {
		t.Fatalf("wiring that does something was reported as an activation by side effect: %v", found)
	}
}

func TestATestFileMayActivateWhateverItLikes(t *testing.T) {
	root := treeOf(t, map[string]string{"wiring/wiring_test.go": `package wiring

import "go.uber.org/fx"

var option = fx.Invoke(func(*Thing) {})

type Thing struct{}
`})

	if found := scan(t, root); len(found) != 0 {
		t.Fatalf("a test's own graph was judged as production wiring: %v", found)
	}
}

func TestTheDefaultScanLeavesTestdataAndHiddenDirectoriesAlone(t *testing.T) {
	source := `package golden

import "go.uber.org/fx"

var option = fx.Invoke(func(*Thing) {})

type Thing struct{}
`
	root := treeOf(t, map[string]string{
		"pkg/testdata/golden.go": source,
		"pkg/.cache/golden.go":   source,
		"pkg/_scratch/golden.go": source,
	})

	if found := scan(t, root); len(found) != 0 {
		t.Fatalf("the walk read files that are not part of the tree: %v", found)
	}
}

func TestAScannerMaySayWhichDirectoriesAreNotPartOfTheTree(t *testing.T) {
	source := `package generated

import "go.uber.org/fx"

var option = fx.Invoke(func(*Thing) {})

type Thing struct{}
`
	root := treeOf(t, map[string]string{
		"gen/generated.go":  source,
		"hand/generated.go": source,
	})

	scanner := runtimecheck.Scanner{SkipDirectory: func(name string) bool { return name == "gen" }}
	found, err := scanner.EmptyInvokeActivations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].File != "hand/generated.go" {
		t.Fatalf("the caller's own skip list was not the one used: %v", found)
	}
}

func TestSourceThatDoesNotParseIsReportedInsteadOfPassing(t *testing.T) {
	root := treeOf(t, map[string]string{"broken/broken.go": "package broken\n\nfunc ((\n"})

	if _, err := runtimecheck.EmptyInvokeActivations(root); err == nil {
		t.Fatal("a tree the guard could not read was reported as a clean tree")
	}
}

func scan(t *testing.T, root string) []runtimecheck.Activation {
	t.Helper()
	found, err := runtimecheck.EmptyInvokeActivations(root)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func treeOf(t *testing.T, files map[string]string) string {
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

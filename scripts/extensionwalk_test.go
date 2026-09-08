package scripts

import (
	"path/filepath"
	"strings"
	"testing"
)

// The four graph checks beside this file run against the repository, where a
// green answer is what is wanted and therefore proves nothing about the walk
// that produced it. Each of the four walks is asked here about a tree written
// to hold the thing it exists to find, and about the idiom it must let through.

const lifecycleFixture = `package extension

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"time"
)

type Store struct{ name string }

func (this *Store) init() {}

var (
	ErrRefused = errors.New("extension: a refusal")
	errWrapped = fmt.Errorf("extension: and another: %w", ErrRefused)
	typeToken  = reflect.TypeFor[error]()
	raw        = []byte("a conversion runs nothing")
	absent     = (*Store)(nil)
	home       = os.Getenv("HOME")
	started    = time.Now()
	identifier = regexp.MustCompile("^[a-z]+$")
)

func init() { _ = home }

func Watch() {
	go func() { _ = started }()
}
`

func TestALifecycleAPackageStartsForItselfIsReportedAndTheIdiomIsNot(t *testing.T) {
	source := filepath.Join(fixture(t, map[string]string{"extension/lifecycle.go": lifecycleFixture}), "extension", "lifecycle.go")

	before := strings.Join(startsBeforeMain(t, source), "\n")
	for _, started := range []string{"declares init", "os.Getenv", "time.Now", "regexp.MustCompile"} {
		if !strings.Contains(before, started) {
			t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", started, before)
		}
	}
	for _, permitted := range []string{"errors.New", "fmt.Errorf", "reflect.TypeFor", "byte", "Store"} {
		if strings.Contains(before, permitted) {
			t.Fatalf("the fixture's %s was reported, and a sentinel, a reflected type token and a conversion are what this repository declares at package level:\n%s", permitted, before)
		}
	}

	goroutines := startsAGoroutine(t, source)
	if len(goroutines) != 1 || !strings.Contains(goroutines[0], "starts a goroutine") {
		t.Fatalf("the fixture starts one goroutine and %v came back, so a package a program links could own a lifecycle nobody asked for", goroutines)
	}
}

func TestAnImportOfTheExtensionFromOutsideItIsReportedAndOneInsideItIsNot(t *testing.T) {
	reported := crossingsInto("github.com/x/event", []string{
		"github.com/x/crud github.com/x/errs github.com/x/event",
		"github.com/x/jobs/jobsfx github.com/x/event/eventtest",
		"github.com/x/event github.com/x/crud github.com/x/errs",
		"github.com/x/event/eventmemory github.com/x/event",
		"github.com/x/eventually github.com/x/eventual",
		"github.com/x/port",
		"",
	})

	if len(reported) != 2 {
		t.Fatalf("two of the seven edges leave a base package reaching the extension and %v came back", reported)
	}
	for _, crossing := range []string{"github.com/x/crud imports", "github.com/x/jobs/jobsfx imports"} {
		if !strings.Contains(strings.Join(reported, "\n"), crossing) {
			t.Fatalf("%s was not reported, so a satellite could make the extension compulsory and this would stay green: %v", crossing, reported)
		}
	}
}

// One tree, read against two tables: the first understates what the extension
// costs and every sentence of the walk has something to report, the second
// states it exactly and the walk says nothing. The second half is what a written
// table is bought with — an arm that reports a package costing precisely what
// its row allows is an arm that gets loosened on its first run.
func TestAPackageCostingMoreThanItsRowSaysIsReportedAndOneCostingExactlyItIsNot(t *testing.T) {
	tree := fixture(t, map[string]string{
		"go.mod":               "module github.com/x\n\ngo 1.26\n",
		"contract/contract.go": "package contract\n",
		"outside/outside.go":   "package outside\n",
		"seam/seam.go":         "package seam\n",
		"ext/ext.go": `package ext

import (
	_ "github.com/x/contract"
	_ "github.com/x/outside"
)
`,
		"ext/charged/charged.go": `package charged

import (
	_ "github.com/x/ext"
	_ "github.com/x/seam"
)
`,
		"ext/unnamed/unnamed.go": `package unnamed

import _ "github.com/x/ext"
`,
	})
	understated := extensionCost{
		prefix:    "github.com/x/ext",
		root:      "ext",
		contracts: []string{"./contract"},
		charged:   map[string]string{"github.com/x/ext/charged": ""},
		core:      func(reached string) string { return "the core reaches " + reached },
		uncharged: func(path string) string { return path + " says nothing about what it costs" },
		overreach: func(path, reached, allowance string) string {
			return path + " reaches " + reached + ", and its row names " + allowance
		},
	}

	overruns := costOverruns(t, tree, understated)
	reported := strings.Join(overruns, "\n")
	for _, expected := range []string{
		"the extension has 3 packages and 1 of them say what they cost",
		"the core reaches github.com/x/outside",
		"github.com/x/ext/unnamed says nothing about what it costs",
		"github.com/x/ext/charged reaches github.com/x/seam",
	} {
		if !strings.Contains(reported, expected) {
			t.Fatalf("the fixture's %q was not reported, so the arm that would have found it proves nothing:\n%s", expected, reported)
		}
	}
	if len(overruns) != 4 {
		t.Fatalf("the fixture breaks the table in four ways and %d complaints came back, so this walk reports something nobody wrote:\n%s", len(overruns), reported)
	}

	stated := understated
	stated.contracts = []string{"./contract", "./outside"}
	stated.charged = map[string]string{
		"github.com/x/ext/charged": "./seam",
		"github.com/x/ext/unnamed": "",
	}
	if overruns := costOverruns(t, tree, stated); len(overruns) != 0 {
		t.Fatalf("every package costs what its row allows and %v came back, so a correct table is reported and the check gets loosened", overruns)
	}
}

func TestADirectoryHoldingSourceThatNoPackageListedIsReported(t *testing.T) {
	root := fixture(t, map[string]string{
		"extension/core/store.go":       "package core\n",
		"extension/nested/go.mod":       "module github.com/x/nested\n\ngo 1.26\n",
		"extension/nested/store.go":     "package nested\n",
		"extension/testdata/broken.go":  "package broken\n",
		"extension/core/_scratch/x.go":  "package scratch\n",
		"extension/docs/how-to-read.md": "# not source\n",
	})
	tree := filepath.Join(root, "extension")

	listedBoth := []extensionPackage{
		{path: "github.com/x/extension/core", directory: filepath.Join(tree, "core")},
		{path: "github.com/x/nested", directory: filepath.Join(tree, "nested")},
	}
	if uncovered := uncoveredDirectories(t, tree, listedBoth); len(uncovered) != 0 {
		t.Fatalf("every directory holding source was listed and %v came back, so the floor reports a tree that is whole", uncovered)
	}

	uncovered := uncoveredDirectories(t, tree, listedBoth[:1])
	if len(uncovered) != 1 || !strings.Contains(uncovered[0], "nested") {
		t.Fatalf("the nested module was listed by nobody and %v came back, so a store that becomes a module would be walked by no check at all", uncovered)
	}
}

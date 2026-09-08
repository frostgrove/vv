package scripts

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The prohibitions every optional extension's graph test holds, over what
// `scripts/extensionlisting_test.go` asked the toolchain for. They live beside
// each other because `scripts` is one Go package and a second copy does not
// compile, and because two copies of one prohibition drift and then answer the
// same question two ways — which is what had already happened between the
// tenancy grep and the event walk.

// What an extension may cost is an argument and stays in the extension's own
// file — the table, and the three sentences that report a breach of it. How the
// tree is walked to check that table is shared, because the two copies had
// already diverged in exactly the thing the walk is for: one reached a nested
// module and the other stopped at its `go.mod`.
type extensionCost struct {
	prefix    string
	root      string
	contracts []string
	charged   map[string]string
	core      func(reached string) string
	uncharged func(path string) string
	overreach func(path, reached, allowance string) string
}

func costsNoMoreThanItNames(t *testing.T, cost extensionCost) {
	t.Helper()
	for _, complaint := range costOverruns(t, "..", cost) {
		t.Error(complaint)
	}
}

func costOverruns(t *testing.T, tree string, cost extensionCost) []string {
	t.Helper()
	packages := packagesUnder(t, tree, cost.prefix, cost.root)
	var complaints []string
	if len(packages) != len(cost.charged)+1 {
		complaints = append(complaints, fmt.Sprintf("the extension has %d packages and %d of them say what they cost", len(packages), len(cost.charged)))
	}

	for _, found := range packages {
		reaches := firstPartyDependenciesIn(t, tree, found.module, found.path)
		if found.path == cost.prefix {
			contracts := firstPartyDependenciesIn(t, tree, ".", cost.contracts...)
			for reached := range reaches {
				if contracts[reached] || reached == cost.prefix {
					continue
				}
				complaints = append(complaints, cost.core(reached))
			}
			continue
		}
		allowance, charged := cost.charged[found.path]
		if !charged {
			complaints = append(complaints, cost.uncharged(found.path))
			continue
		}
		named := []string{"./" + cost.root}
		if allowance != "" {
			named = append(named, allowance)
		}
		allowed := firstPartyDependenciesIn(t, tree, ".", named...)
		for reached := range reaches {
			if allowed[reached] || reached == found.path {
				continue
			}
			complaints = append(complaints, cost.overreach(found.path, reached, allowance))
		}
	}
	return complaints
}

// An extension is optional in the way that matters — by the import graph, not by
// a paragraph. A base package that grew an import of it would compile, pass
// every test in this repository, and make the root module's consumers carry an
// extension they never selected. `make check-deps` measures third-party weight
// and would not see it, because an extension is first-party.
//
// Every package of every published module is asked, rather than a list of
// subsystems somebody remembered to extend: a list is what a new top-level
// directory is added beside. Direct imports are enough because every package is
// enumerated — a transitive edge is a direct edge somewhere along it.
func noBaseSubsystemDependsOn(t *testing.T, prefix string) {
	t.Helper()
	modules := publishedModules(t)
	if len(modules) < 20 {
		t.Fatalf("only %d published modules were found, so a satellite that imported the extension would not be read", len(modules))
	}
	var edges []string
	for _, module := range modules {
		edges = append(edges, listedIn(t, filepath.Join("..", module), "{{.ImportPath}} {{join .Imports \" \"}}", "./...")...)
	}
	for _, complaint := range crossingsInto(prefix, edges) {
		t.Error(complaint)
	}
	if len(edges) < 80 {
		t.Fatalf("only %d packages were listed across %d modules, so this proves almost nothing", len(edges), len(modules))
	}
}

// One edge is a package and everything it imports, as `go list` renders it. A
// package under the prefix may reach the extension freely; anything else that
// does has made it compulsory.
func crossingsInto(prefix string, edges []string) []string {
	var complaints []string
	for _, edge := range edges {
		fields := strings.Fields(edge)
		if len(fields) == 0 || under(prefix, fields[0]) {
			continue
		}
		for _, imported := range fields[1:] {
			if under(prefix, imported) {
				complaints = append(complaints,
					fields[0]+" imports "+imported+" — the extension is no longer optional, and every consumer of the root now compiles it")
			}
		}
	}
	return complaints
}

// Importing a package must not do anything. An init function, a goroutine
// started where nobody asked and a package-level value produced by calling
// something are three lifecycles the composition root never chose, and all
// three are invisible until the process they are in misbehaves.
//
// The initialiser arm is fail-closed: a call at package level is reported
// unless it is one of the three this repository's idiom needs, so an
// environment read, a compiled pattern, a clock reading and a pool opened at
// import time are all refused without any of them being named. A deny list of
// the two spellings somebody thought of is what this replaced.
//
// The goroutine arm asks only the packages a program links, and that predicate
// is derived rather than a name: a package that imports `testing` cannot be in
// a production binary, and a conformance suite is one — its concurrency section
// runs concurrent writers because that is the property it certifies. A store
// package gets no such exemption, and neither does the next one.
func startsNothing(t *testing.T, prefix, root string) {
	t.Helper()
	packages := packagesUnder(t, "..", prefix, root)
	if len(packages) == 0 {
		t.Fatal("the extension has no packages, so this proves nothing")
	}

	linked, walked := 0, 0
	for _, found := range packages {
		underTest := slices.Contains(found.imports, "testing")
		if !underTest {
			linked++
		}
		for _, source := range found.files {
			walked++
			for _, complaint := range startsBeforeMain(t, source) {
				t.Error(complaint)
			}
			if underTest {
				continue
			}
			for _, complaint := range startsAGoroutine(t, source) {
				t.Error(complaint)
			}
		}
	}
	if linked == 0 {
		t.Fatal("every package of the extension imports testing, so the goroutine arm asked nothing")
	}
	if walked == 0 {
		t.Fatal("the extension has no source files, so this proves nothing")
	}
}

var initialisersThatRunNothing = map[string]bool{
	"errors.New":      true,
	"fmt.Errorf":      true,
	"reflect.TypeFor": true,
}

func parsedSource(t *testing.T, source string) (*token.FileSet, *ast.File) {
	t.Helper()
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, source, nil, 0)
	if err != nil {
		t.Fatalf("%s could not be parsed, so nothing about it was checked: %v", source, err)
	}
	return positions, parsed
}

func startsBeforeMain(t *testing.T, source string) []string {
	t.Helper()
	positions, parsed := parsedSource(t, source)
	var complaints []string
	for _, declaration := range parsed.Decls {
		switch found := declaration.(type) {
		case *ast.FuncDecl:
			if found.Recv == nil && found.Name.Name == "init" {
				complaints = append(complaints, positions.Position(found.Pos()).String()+
					" declares init, so importing the extension runs it before the composition root exists")
			}
		case *ast.GenDecl:
			if found.Tok != token.VAR {
				continue
			}
			for _, called := range initialiserCalls(found) {
				complaints = append(complaints, positions.Position(called.Pos()).String()+" calls "+calledSpelling(called)+
					" to build a package-level value, and what a package holds before main runs is a literal or nothing at all")
			}
		}
	}
	return complaints
}

func startsAGoroutine(t *testing.T, source string) []string {
	t.Helper()
	positions, parsed := parsedSource(t, source)
	var complaints []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		if found, isGo := node.(*ast.GoStmt); isGo {
			complaints = append(complaints, positions.Position(found.Pos()).String()+
				" starts a goroutine, and a package a program links owns no lifecycle of its own")
		}
		return true
	})
	return complaints
}

func initialiserCalls(declaration *ast.GenDecl) []*ast.CallExpr {
	var called []*ast.CallExpr
	for _, spec := range declaration.Specs {
		value, isValue := spec.(*ast.ValueSpec)
		if !isValue {
			continue
		}
		for _, initialiser := range value.Values {
			ast.Inspect(initialiser, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if spelling := calledSpelling(call); spelling != "" && !initialisersThatRunNothing[spelling] {
					called = append(called, call)
				}
				return true
			})
		}
	}
	return called
}

// A conversion is not a call: `(*Store)(nil)` and `[]byte("…")` produce a value
// without running anything, and every shape that can only be a type is read as
// one. What is left is an identifier or a qualified name, and that is reported
// unless the table above says it runs nothing.
func calledSpelling(call *ast.CallExpr) string {
	called := call.Fun
	for {
		switch held := called.(type) {
		case *ast.IndexExpr:
			called = held.X
		case *ast.IndexListExpr:
			called = held.X
		case *ast.Ident:
			return held.Name
		case *ast.SelectorExpr:
			if qualifier, isName := held.X.(*ast.Ident); isName {
				return qualifier.Name + "." + held.Sel.Name
			}
			return held.Sel.Name
		default:
			return ""
		}
	}
}

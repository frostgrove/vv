package event

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The three structural checks beside this file ask what a value *is*, not what
// it is called, and a name is all `go/ast` can answer. So the sources are
// type-checked with `go/types` and the standard library's source importer,
// which adds no dependency and resolves the two contract packages the kernel
// reaches. A spelling-matched prohibition passes every violation written with
// another spelling, which is what these three were before.
type typedPackage struct {
	directory string
	fileset   *token.FileSet
	files     []*ast.File
	info      *types.Info
	pkg       *types.Package
}

// One file set and one importer for every package read here: the source
// importer compiles what it resolves, and three importers resolve the two
// contract packages three times.
var (
	typedGuard     sync.Mutex
	typedPositions = token.NewFileSet()
	typedImporter  = importer.ForCompiler(typedPositions, "source", nil)
	typedCache     = map[string]typedPackage{}
)

func typedSources(t *testing.T, directory string) typedPackage {
	t.Helper()
	typedGuard.Lock()
	defer typedGuard.Unlock()
	if held, read := typedCache[directory]; read {
		return held
	}
	typed := typeCheck(t, directory)
	typedCache[directory] = typed
	return typed
}

func typeCheck(t *testing.T, directory string) typedPackage {
	t.Helper()
	fileset := typedPositions
	var files []*ast.File
	for _, source := range sourcesIn(t, directory) {
		file, err := parser.ParseFile(fileset, source, nil, 0)
		if err != nil {
			t.Fatalf("%s could not be parsed, so nothing about it was checked: %v", source, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatalf("%s holds no source outside its tests, so nothing about it was checked", directory)
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	config := types.Config{Importer: typedImporter}
	pkg, err := config.Check(directory, fileset, files, info)
	if err != nil {
		t.Fatalf("%s does not type-check, so nothing about it was checked: %v", directory, err)
	}
	return typedPackage{directory: directory, fileset: fileset, files: files, info: info, pkg: pkg}
}

func (this typedPackage) at(node ast.Node) string {
	return this.fileset.Position(node.Pos()).String()
}

// One package is type-checked once and the three checks walk the same maps, so
// an analyser that memoised anything into what it reads would be a race between
// two of them rather than a wrong answer. Under `-race` this says which.
func TestTheStructuralChecksReadOneTypedPackageFromManyGoroutines(t *testing.T) {
	typed := typedSources(t, ".")
	alone := readingsOf(typed)

	together := make(chan readings, 8)
	var running sync.WaitGroup
	for range cap(together) {
		running.Add(1)
		go func() {
			defer running.Done()
			together <- readingsOf(typed)
		}()
	}
	running.Wait()
	close(together)

	answered := 0
	for read := range together {
		answered++
		if read != alone {
			t.Fatal("the three checks answered differently about one package when they ran together, so at least one of them writes into what all of them read")
		}
	}
	if answered != cap(together) {
		t.Fatalf("%d of %d readers answered, and a reader that never returned proves nothing about the ones that did", answered, cap(together))
	}
	if alone.counted.total == 0 || alone.declared == 0 {
		t.Fatalf("the kernel read as %d messages and %d package-level names, and a comparison of two empty answers holds whatever the checks do",
			alone.counted.total, alone.declared)
	}
}

type readings struct {
	counted           messages
	rendered, control string
	mutable           string
	declared          int
}

func readingsOf(typed typedPackage) readings {
	counted, rendered := renderedValues(typed)
	names, mutable := packageLevelState(typed)
	return readings{
		counted:  counted,
		rendered: strings.Join(rendered, "\n"),
		control:  strings.Join(controlComplaints(typed), "\n"),
		mutable:  strings.Join(mutable, "\n"),
		declared: len(names),
	}
}

func sourcesIn(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("%s could not be listed: %v", directory, err)
	}
	var found []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		found = append(found, filepath.Join(directory, name))
	}
	return found
}

func extensionDirectories(t *testing.T) []string {
	t.Helper()
	found := []string{"."}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("event/ could not be listed: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata" {
			continue
		}
		found = append(found, entry.Name())
	}
	return found
}

// A package that imports `testing` outside its own tests cannot be in a
// production binary, so the refusal rule is asked of the packages a program
// links and derived rather than named. The predicate is the package's import
// list and not its bytes: a comment or a string literal carrying the quoted word
// would otherwise drop a store out of the walk, silently.
// `scripts/extensions_test.go` reads the same list for the goroutine arm.
func linkedDirectories(t *testing.T) []string {
	t.Helper()
	var linked []string
	for _, directory := range extensionDirectories(t) {
		if !underTest(typedSources(t, directory)) {
			linked = append(linked, directory)
		}
	}
	if len(linked) < 2 {
		t.Fatalf("%d packages of the extension are linked into a program, and the vocabulary and one store are the least this tree holds", len(linked))
	}
	return linked
}

func underTest(typed typedPackage) bool {
	for _, file := range typed.files {
		for _, imported := range file.Imports {
			if imported.Path.Value == `"testing"` {
				return true
			}
		}
	}
	return false
}

func directoriesHoldingSource(t *testing.T) []string {
	t.Helper()
	var found []string
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if name := entry.Name(); path != "." && (name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
			return filepath.SkipDir
		}
		if len(sourcesIn(t, path)) > 0 {
			found = append(found, path)
		}
		return nil
	}
	if err := filepath.WalkDir(".", walk); err != nil {
		t.Fatalf("event/ could not be walked: %v", err)
	}
	return found
}

// What the three checks read is decided by two functions above, and a green
// answer out of either of them is also what a narrowed one gives: a walk that
// stopped at the kernel would leave every assertion in this package passing
// while §INV-011 and §INV-013 stopped being proved for the store beside it. So
// the walked set is compared against the tree itself, read a second time and
// recursively, and the linked set against what it is defined to be — the walked
// set less the packages a program cannot link. Both grow on their own when
// phase 2 adds a fourth package, which a counting floor does not.
func TestTheChecksWalkEveryPackageTheExtensionHoldsAndLinkAllButTheSuite(t *testing.T) {
	walked := slices.Sorted(slices.Values(extensionDirectories(t)))
	holding := slices.Sorted(slices.Values(directoriesHoldingSource(t)))
	if !slices.Equal(walked, holding) {
		t.Fatalf("the checks walk %v and the extension holds source in %v, so a prohibition this package states is not held over every package under it", walked, holding)
	}

	linked := linkedDirectories(t)
	var dropped []string
	for _, directory := range walked {
		if !slices.Contains(linked, directory) {
			dropped = append(dropped, directory)
		}
	}
	if !slices.Equal(dropped, []string{"eventtest"}) {
		t.Fatalf("the refusal walk reads %v and leaves out %v, and the conformance suite is the one package here that a program does not link", linked, dropped)
	}

	if underTest(typedSources(t, writeFixture(t, quotedTestingFixture))) {
		t.Fatal("a package that writes the word testing in a comment and in a string was read as one a program does not link, so a store drops out of the refusal walk by writing prose")
	}
	if !underTest(typedSources(t, writeFixture(t, importedTestingFixture))) {
		t.Fatal("a package that imports testing was read as one a program links, so the suite would be judged by a rule written for production code")
	}
}

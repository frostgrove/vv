package scripts

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Four invariants of the checkpoint phase are about a shape no package of the
// extension can hold over itself: what the whole exported surface may not
// spell, what no source of it may compare, what its comments may not promise,
// and what none of it may declare. They are held here, over the packages
// `docs/api/surface.md` lists, because that file is what a release reads and a
// package missing from it is a package nothing in this file would have walked.
//
// The surface is the package list and `go/types` is the signature, which is the
// division that matters: `go doc -short` renders a constructor as `func New(spec
// Spec) (*Projection, error)` and renders a method as `{ ... }`, so a check
// written against the rendered text would read a third of the surface and pass
// the rest.

const surfaceBaseline = "../docs/api/surface.md"

var surfaceSection = regexp.MustCompile(`^## (github\.com/frostgrove/vv[^ ]*)$`)

// A cursor is opaque bytes a store minted, and a position is a number the log
// assigned. A function from the second to the first is the arithmetic this whole
// phase exists to refuse: it would let a consumer compute where to resume from,
// which is sound only against a store whose cursor is its position and silently
// wrong against every other one.
func TestNoExportedFunctionTakesAPositionAndAnswersACursor(t *testing.T) {
	walked := 0
	for _, checked := range checkedEventPackages(t) {
		walked += checked.signatures
		for _, complaint := range answersFrom(checked.pkg, eventTypeName("Position"), eventTypeName("Cursor")) {
			t.Error(complaint + ", and a cursor is a store's own bytes rather than a number a consumer can compute")
		}
	}
	if walked < 100 {
		t.Fatalf("%d exported signatures were read across the extension, so this walked the wrong packages", walked)
	}
	assertTheWalkFindsAKnownSignature(t)
}

// The same question one field out. Progress is an observation an operator
// reads — a watermark, two counts and an instant — and the moment a constructor
// turns one into a cursor it has become a resume point, which is the second
// authority the checkpoint contract has exactly one of.
func TestNoConstructorTakesAProgressAndAnswersACursor(t *testing.T) {
	walked := 0
	for _, checked := range checkedEventPackages(t) {
		walked += checked.signatures
		for _, complaint := range answersFrom(checked.pkg, eventTypeName("Progress"), eventTypeName("Cursor")) {
			t.Error(complaint + ", and Progress is an observation nobody resumes from")
		}
	}
	if walked < 100 {
		t.Fatalf("%d exported signatures were read across the extension, so this walked the wrong packages", walked)
	}
	assertTheWalkFindsAKnownSignature(t)
}

// The positive control, over the real tree rather than over a fixture: the walk
// must find event.Read, which takes a Cursor and answers a *Reader. A walk that
// resolved no type, listed no package or read no signature answers "nothing
// found" to every question above, and that is the same answer a clean tree
// gives.
func assertTheWalkFindsAKnownSignature(t *testing.T) {
	t.Helper()
	for _, checked := range checkedEventPackages(t) {
		if checked.path != eventExtension {
			continue
		}
		found := answersFrom(checked.pkg, eventTypeName("Cursor"), "*"+eventTypeName("Reader"))
		if len(found) == 0 {
			t.Fatal("the walk did not find event.Read, which takes a cursor and answers a reader, so it would report nothing whatever the surface held")
		}
		return
	}
	t.Fatal("the vocabulary itself was not among the packages walked, so nothing here was checked")
}

// A cursor is opaque and ordering two of them is the same arithmetic by another
// spelling: `Cursor` is a string, so `<` compiles and answers the byte order of
// two encodings rather than the order of two points in a log. The store that
// mints an encoding is the one party that may compare what it parsed out of one,
// and it does that on the numbers, not on the cursor.
func TestCursorIsNeverCompared(t *testing.T) {
	read := 0
	for _, checked := range checkedEventPackages(t) {
		read += len(checked.files)
		for _, complaint := range orderedCursors(checked) {
			t.Error(complaint)
		}
	}
	if read < 40 {
		t.Fatalf("%d source files were read across the extension, so this walked the wrong tree", read)
	}

	fixture := checkedFixture(t, "comparison", `package comparison

import "github.com/frostgrove/vv/event"

func Resumed(held, taken event.Cursor) event.Cursor {
	if held < taken {
		return taken
	}
	return held
}
`)
	reported := orderedCursors(fixture)
	if len(reported) != 1 {
		t.Fatalf("the fixture orders two cursors with < and %v came back, so the arm that would have found one in the tree proves nothing", reported)
	}
}

// The docs walk reads `docs/` and nothing else, so a package comment promising
// exactly-once delivery ships unread — and a package comment is what a consumer
// sees in `go doc`, which makes it the one place the promise would be believed.
//
// The window is narrower than the prose walk's by one clause, and deliberately:
// a Go comment packs several subjects into a paragraph, so what decides whether
// a phrase is about delivery is the clause the phrase sits in plus the sentence
// before it — never the rest of the sentence after it. Read the wider way, "a
// unit runs the work exactly once, so the page is re-delivered rather than saved
// over twice" is a delivery promise, which it is not.
func TestNoCommentInTheProjectionPackagePromisesExactlyOnce(t *testing.T) {
	claims, read, files := commentClaims(t, "../event/projection")

	if files < 9 {
		t.Fatalf("%d source files of event/projection were read, and the package has nine outside its tests", files)
	}
	if read.counted["English"] == 0 {
		t.Fatal("not one comment of event/projection used either wording for how often a thing is delivered, and the package doc states the weaker one — so this read the wrong files")
	}
	for _, claim := range claims {
		t.Errorf("%s promises exactly-once delivery, and delivery in this repository is at least once", claim)
	}

	fixture := t.TempDir()
	written := "package projection\n\n" +
		"// Each event reaches the handler exactly once, so a read model needs no\n" +
		"// idempotency of its own.\n" +
		"func Apply() {}\n"
	if err := os.WriteFile(filepath.Join(fixture, "doc.go"), []byte(written), 0o644); err != nil {
		t.Fatalf("cannot write the fixture: %v", err)
	}
	reported, _, _ := commentClaims(t, fixture)
	if len(reported) != 1 {
		t.Fatalf("the fixture's package comment promises exactly-once delivery and %v came back, so the arm that would have found one proves nothing", reported)
	}
}

// Full replay is the only authority: no snapshot, no memo and no cache of a
// folded state. The name is what this reads, over every identifier the extension
// defines and over the release baseline beside it, because a snapshot arrives as
// a type before it arrives as a behaviour and the surface is where a consumer
// would first be promised one.
func TestNoSnapshotAuthorityIsDeclaredOrPromised(t *testing.T) {
	defined := 0
	for _, checked := range checkedEventPackages(t) {
		defined += len(checked.info.Defs)
		for _, complaint := range declaresASnapshot(checked) {
			t.Error(complaint)
		}
	}
	if defined < 1000 {
		t.Fatalf("%d identifiers were read across the extension, so this walked the wrong packages", defined)
	}

	baseline, err := os.ReadFile(surfaceBaseline)
	if err != nil {
		t.Fatalf("cannot read %s, which is what a release reads: %v", surfaceBaseline, err)
	}
	for _, complaint := range promisesASnapshot(surfaceBaseline, string(baseline)) {
		t.Error(complaint)
	}

	fixture := checkedFixture(t, "authority", `package authority

type Snapshot struct{ Version uint64 }

func snapshotAt() Snapshot { return Snapshot{} }
`)
	if reported := declaresASnapshot(fixture); len(reported) != 2 {
		t.Fatalf("the fixture declares a snapshot type and a snapshot function and %v came back, so the arm that would have found one proves nothing", reported)
	}
	promised := promisesASnapshot("fixture.md", "## github.com/frostgrove/vv/event\n```go\ntype Snapshot struct{ ... }\n```\n")
	if len(promised) != 1 {
		t.Fatalf("the fixture baseline publishes a snapshot type and %v came back, so the baseline arm proves nothing", promised)
	}
}

func eventTypeName(name string) string { return eventExtension + "." + name }

// Every exported function, method and interface method the package publishes,
// asked of the signature rather than of the name. A method reached through an
// exported type is published as surely as a package-level function is, and the
// method set of the pointer is what carries both receiver forms.
func answersFrom(pkg *types.Package, takes, answers string) []string {
	var complaints []string
	for _, signature := range exportedSignatures(pkg) {
		if !mentions(signature.signature.Params(), takes) || !mentions(signature.signature.Results(), answers) {
			continue
		}
		complaints = append(complaints, fmt.Sprintf("%s.%s takes %s and answers %s",
			pkg.Path(), signature.name, shortly(takes), shortly(answers)))
	}
	return complaints
}

type publishedSignature struct {
	name      string
	signature *types.Signature
}

func exportedSignatures(pkg *types.Package) []publishedSignature {
	var found []publishedSignature
	for _, name := range pkg.Scope().Names() {
		object := pkg.Scope().Lookup(name)
		if !object.Exported() {
			continue
		}
		switch held := object.(type) {
		case *types.Func:
			if signature, is := held.Type().(*types.Signature); is {
				found = append(found, publishedSignature{name: name, signature: signature})
			}
		case *types.TypeName:
			found = append(found, methodsOf(name, held)...)
		}
	}
	return found
}

func methodsOf(name string, held *types.TypeName) []publishedSignature {
	declared := held.Type()
	asked := declared
	if !types.IsInterface(declared) {
		asked = types.NewPointer(declared)
	}
	var found []publishedSignature
	set := types.NewMethodSet(asked)
	for index := range set.Len() {
		method := set.At(index).Obj()
		if !method.Exported() {
			continue
		}
		if signature, is := method.Type().(*types.Signature); is {
			found = append(found, publishedSignature{name: name + "." + method.Name(), signature: signature})
		}
	}
	return found
}

func mentions(list *types.Tuple, named string) bool {
	for index := range list.Len() {
		if types.TypeString(list.At(index).Type(), nil) == named {
			return true
		}
	}
	return false
}

func shortly(named string) string {
	return strings.ReplaceAll(named, eventExtension+".", "event.")
}

// Only the four ordering operators: `==` and `!=` over two cursors are how a
// store tells the origin from a point and how a settlement tells its own cursor
// from another writer's, and neither reads an order into what a store minted.
func orderedCursors(checked checkedPackage) []string {
	cursor := eventTypeName("Cursor")
	var complaints []string
	for _, file := range checked.files {
		ast.Inspect(file, func(node ast.Node) bool {
			binary, is := node.(*ast.BinaryExpr)
			if !is {
				return true
			}
			switch binary.Op {
			case token.LSS, token.LEQ, token.GTR, token.GEQ:
			default:
				return true
			}
			for _, side := range []ast.Expr{binary.X, binary.Y} {
				if types.TypeString(checked.info.TypeOf(side), nil) != cursor {
					continue
				}
				complaints = append(complaints, checked.fset.Position(binary.Pos()).String()+
					" orders two cursors, and a cursor is opaque bytes a store minted rather than a point on a line")
				return false
			}
			return true
		})
	}
	return complaints
}

var snapshotName = regexp.MustCompile(`(?i)snapshot`)

func declaresASnapshot(checked checkedPackage) []string {
	var complaints []string
	for identifier, object := range checked.info.Defs {
		if object == nil || !snapshotName.MatchString(identifier.Name) {
			continue
		}
		complaints = append(complaints, checked.fset.Position(identifier.Pos()).String()+" declares "+identifier.Name+
			", and full replay is the only authority a folded state has here")
	}
	slices.Sort(complaints)
	return complaints
}

// The baseline is read section by section, because the whole file is every
// module's surface and a snapshot in `storage` is an ordinary word there.
func promisesASnapshot(path, baseline string) []string {
	var complaints []string
	section := ""
	for number, line := range strings.Split(baseline, "\n") {
		if found := surfaceSection.FindStringSubmatch(line); found != nil {
			section = found[1]
			continue
		}
		if !under(eventExtension, section) || !snapshotName.MatchString(line) {
			continue
		}
		complaints = append(complaints, fmt.Sprintf("%s:%d publishes %q under %s, and this extension exports no snapshot",
			path, number+1, strings.TrimSpace(line), section))
	}
	return complaints
}

// A comment read the way a paragraph of prose is read: the marker is stripped so
// that an empty comment line separates two paragraphs as a blank line does, and
// every other line of the file becomes an empty one so that a claim is reported
// at the line a person opens.
func commentClaims(t *testing.T, directory string) ([]string, reading, int) {
	t.Helper()
	var claims []string
	read := reading{written: map[string]bool{}, counted: map[string]int{}, checked: map[string]int{}}
	files := 0
	for _, source := range goSourcesIn(t, directory) {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, source, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s could not be parsed, so nothing about it was checked: %v", source, err)
		}
		files++
		claims = append(claims, commentClaimsIn(filepath.ToSlash(source), commentLinesOf(t, fset, source, parsed), read)...)
	}
	return claims, read, files
}

// `claimsIn`'s model with one narrowing, and the narrowing is the whole reason
// this is not a call to it: what decides whether a phrase is about delivery is
// the clause the phrase sits in plus the sentence before it, which is the same
// span the refusal is read out of. Prose gives a claim a sentence to itself and
// a Go comment does not.
func commentClaimsIn(path, content string, read reading) []string {
	var claims []string
	for _, spoken := range deliveryWordings {
		if !spoken.written.MatchString(content) {
			continue
		}
		read.written[spoken.language] = true
		for _, paragraph := range paragraphsOf(content) {
			read.counted[spoken.language] += len(spoken.weaker.FindAllStringIndex(paragraph.text, -1))
			for _, at := range spoken.promise.FindAllStringIndex(paragraph.text, -1) {
				read.counted[spoken.language]++
				if !spoken.about.MatchString(paragraph.attachedTo(at)) {
					continue
				}
				read.checked[spoken.language]++
				if spoken.refuses(paragraph, at) {
					continue
				}
				claims = append(claims, path+":"+strconv.Itoa(paragraph.lineAt(at[0])))
			}
		}
	}
	return claims
}

func commentLinesOf(t *testing.T, fset *token.FileSet, source string, parsed *ast.File) string {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("cannot read %s: %v", source, err)
	}
	lines := strings.Split(string(content), "\n")
	kept := make([]string, len(lines))
	for _, group := range parsed.Comments {
		from, to := fset.Position(group.Pos()).Line, fset.Position(group.End()).Line
		for number := from; number <= to && number <= len(lines); number++ {
			written := strings.TrimSpace(lines[number-1])
			written = strings.TrimPrefix(written, "//")
			written = strings.TrimPrefix(written, "/*")
			written = strings.TrimSuffix(written, "*/")
			kept[number-1] = strings.TrimSpace(written)
		}
	}
	return strings.Join(kept, "\n")
}

func goSourcesIn(t *testing.T, directory string) []string {
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

// One file set and one source importer for every package read here. The
// standard library's source importer adds no dependency and resolves the two
// contract packages the vocabulary reaches, and type-checking is what makes a
// prohibition about a type rather than about a spelling: `Cursor`, `event.Cursor`
// and a local alias of either are one type and three names.
type checkedPackage struct {
	path       string
	fset       *token.FileSet
	files      []*ast.File
	info       *types.Info
	pkg        *types.Package
	signatures int
}

var (
	checkedGuard     sync.Mutex
	checkedPositions = token.NewFileSet()
	checkedImporter  = importer.ForCompiler(checkedPositions, "source", nil)
	checkedCache     = map[string]checkedPackage{}
	checkedEvent     []checkedPackage
)

// The package list is the release baseline's own, so a package that never
// reached `make api` is not one this file quietly stops asking about.
func checkedEventPackages(t *testing.T) []checkedPackage {
	t.Helper()
	checkedGuard.Lock()
	defer checkedGuard.Unlock()
	if checkedEvent != nil {
		return checkedEvent
	}
	listed := surfacedPackages(t, eventExtension)
	if len(listed) < 5 || !slices.Contains(listed, eventExtension+"/projection") {
		t.Fatalf("%v is what %s lists for the extension, and the vocabulary, three stores and the projection package are the least it holds — regenerate it with make api", listed, surfaceBaseline)
	}
	for _, path := range listed {
		checkedEvent = append(checkedEvent, typeChecked(t, path))
	}
	return checkedEvent
}

func surfacedPackages(t *testing.T, prefix string) []string {
	t.Helper()
	baseline, err := os.ReadFile(surfaceBaseline)
	if err != nil {
		t.Fatalf("cannot read %s, which is the list of packages this file walks: %v", surfaceBaseline, err)
	}
	var found []string
	for line := range strings.SplitSeq(string(baseline), "\n") {
		if held := surfaceSection.FindStringSubmatch(line); held != nil && under(prefix, held[1]) {
			found = append(found, held[1])
		}
	}
	return found
}

func typeChecked(t *testing.T, path string) checkedPackage {
	t.Helper()
	if held, read := checkedCache[path]; read {
		return held
	}
	directory := filepath.Join("..", strings.TrimPrefix(path, "github.com/frostgrove/vv/"))
	var files []*ast.File
	for _, source := range goSourcesIn(t, directory) {
		file, err := parser.ParseFile(checkedPositions, source, nil, 0)
		if err != nil {
			t.Fatalf("%s could not be parsed, so nothing about it was checked: %v", source, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatalf("%s holds no source outside its tests, so nothing about it was checked", directory)
	}
	checked := check(t, path, checkedPositions, files)
	checkedCache[path] = checked
	return checked
}

func checkedFixture(t *testing.T, name, source string) checkedPackage {
	t.Helper()
	checkedGuard.Lock()
	defer checkedGuard.Unlock()
	file, err := parser.ParseFile(checkedPositions, name+".go", source, 0)
	if err != nil {
		t.Fatalf("the %s fixture could not be parsed: %v", name, err)
	}
	return check(t, name, checkedPositions, []*ast.File{file})
}

func check(t *testing.T, path string, fset *token.FileSet, files []*ast.File) checkedPackage {
	t.Helper()
	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	config := types.Config{Importer: checkedImporter}
	pkg, err := config.Check(path, fset, files, info)
	if err != nil {
		t.Fatalf("%s does not type-check, so nothing about it was checked: %v", path, err)
	}
	return checkedPackage{
		path:       path,
		fset:       fset,
		files:      files,
		info:       info,
		pkg:        pkg,
		signatures: len(exportedSignatures(pkg)),
	}
}

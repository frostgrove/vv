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
	if walked < 200 {
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
	if walked < 200 {
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
	if read < 80 {
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

	if files < 20 {
		t.Fatalf("%d source files of event/projection were read, and the package has twenty outside its tests", files)
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

// The framework opens no transaction: Spec.Unit is the application's, and a
// package that reached for one of its own would be opening a second one beside
// the caller's — the advance riding in a transaction the handler's writes are not
// in, which is the tier this phase refuses. It is read as a call rather than as a
// spelling, so the words in a comment are invisible to it and a rename is not.
func TestNothingInTheProjectionPackageOpensATransaction(t *testing.T) {
	walked := 0
	for _, source := range goSourcesIn(t, "../event/projection") {
		walked++
		for _, complaint := range opensATransaction(t, source) {
			t.Error(complaint)
		}
	}
	if walked < 20 {
		t.Fatalf("%d files of event/projection were read, and the package holds twenty outside its tests — so this walked the wrong directory", walked)
	}

	// `event/receipt` owes the same sentence for the same reason one door over: a
	// claim, a completion and a resolve all run inside the caller's own unit, and
	// a package that opened one of its own would write the row beside the append
	// rather than in it.
	received := 0
	for _, source := range goSourcesIn(t, "../event/receipt") {
		received++
		for _, complaint := range opensATransaction(t, source) {
			t.Error(complaint)
		}
	}
	if received < 7 {
		t.Fatalf("%d files of event/receipt were read, and the package holds seven outside its tests — so this walked the wrong directory", received)
	}

	t.Run("the control: every shape is reported when it is there", func(t *testing.T) {
		fixture := filepath.Join(t.TempDir(), "fixture.go")
		if err := os.WriteFile(fixture, []byte(transactionShapes), 0o644); err != nil {
			t.Fatalf("cannot write the fixture: %v", err)
		}
		reported := strings.Join(opensATransaction(t, fixture), "\n")
		for _, shape := range []string{"Begin", "Commit", "Rollback", "crud.InNewTx", "crud.InTx", "crud.InAtomic"} {
			if !strings.Contains(reported, shape) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found one in the tree proves nothing:\n%s", shape, reported)
			}
		}
	})
}

const transactionShapes = `package fixture

import "github.com/frostgrove/vv/crud"

type opener interface{ Begin() error }

func opens(held opener, source crud.Source) error {
	if err := held.Begin(); err != nil {
		return err
	}
	if err := crud.InNewTx(nil, source, nil); err != nil {
		return err
	}
	if err := crud.InTx(nil, source, nil); err != nil {
		return err
	}
	if err := crud.InAtomic(nil, source, nil); err != nil {
		return err
	}
	if err := held.Commit(); err != nil {
		return err
	}
	return held.Rollback()
}
`

var (
	transactionOpeners = map[string]bool{"Begin": true, "Commit": true, "Rollback": true}
	transactionUnits   = map[string]bool{"InNewTx": true, "InTx": true, "InAtomic": true}
)

func opensATransaction(t *testing.T, source string) []string {
	t.Helper()
	positions, parsed := parsedSource(t, source)
	var complaints []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, is := node.(*ast.CallExpr)
		if !is {
			return true
		}
		selector, named := call.Fun.(*ast.SelectorExpr)
		if !named {
			return true
		}
		at := positions.Position(selector.Pos()).String()
		switch {
		case transactionOpeners[selector.Sel.Name]:
			complaints = append(complaints, at+" calls "+selector.Sel.Name+
				", and this package opens, commits and rolls back nothing — the unit of work an advance rides in is the caller's")
		case transactionUnits[selector.Sel.Name]:
			if held, isName := selector.X.(*ast.Ident); isName && held.Name == "crud" {
				complaints = append(complaints, at+" calls crud."+selector.Sel.Name+
					", and a unit of work this package opened would be a second one beside the caller's")
			}
		}
		return true
	})
	return complaints
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
	if defined < 5000 {
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
	if len(listed) < 6 || !slices.Contains(listed, eventExtension+"/projection") || !slices.Contains(listed, eventExtension+"/receipt") {
		t.Fatalf("%v is what %s lists for the extension, and the vocabulary, three stores, the projection package and the receipt package are the least it holds — regenerate it with make api", listed, surfaceBaseline)
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

// A predicate the surface publishes and no line of the package calls is a promise
// the tree does not keep: the vocabulary is there, the doc comments describe what
// it does, `make api` records it, and every runner built from it reads the whole
// log. Partition.Matches is the one this rule was written for — it is what makes
// Spec.Partition a share of the log rather than a share of a row key — and the
// walk is over calls rather than over text, so a comment naming it is invisible.
func TestEveryPublishedTopologyPredicateHasACaller(t *testing.T) {
	for _, published := range []struct {
		named string
		what  string
	}{
		{"Matches", "Partition.Matches decides which envelopes of a page this runner's partition owns"},
		{"SequenceOf", "Sequencer.SequenceOf is the key Partition.Matches is asked about"},
	} {
		if calls := callsTo(t, "../event/projection", published.named); calls == 0 {
			t.Errorf("no non-test file of event/projection calls %s, and %s", published.named, published.what)
		}
	}

	t.Run("the control: the same walk over a package that calls neither", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "fixture.go"), []byte(uncalledPredicates), 0o644); err != nil {
			t.Fatalf("cannot write the fixture: %v", err)
		}
		for _, named := range []string{"Matches", "SequenceOf"} {
			if calls := callsTo(t, directory, named); calls != 0 {
				t.Fatalf("the fixture declares %s and calls nothing, and the walk counted %d calls, so the arms above would pass over a package that calls neither", named, calls)
			}
		}
	})
}

const uncalledPredicates = `package fixture

type Partition struct{ id, mask uint32 }

func (this Partition) Matches(sequence string) bool { return this.mask == 0 }

type Sequencer interface{ SequenceOf(envelope string) string }
`

func callsTo(t *testing.T, directory, named string) int {
	t.Helper()
	calls := 0
	for _, source := range goSourcesIn(t, directory) {
		_, parsed := parsedSource(t, source)
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, is := node.(*ast.CallExpr)
			if !is {
				return true
			}
			if selector, reaches := call.Fun.(*ast.SelectorExpr); reaches && selector.Sel.Name == named {
				calls++
			}
			return true
		})
	}
	return calls
}

// A checkpoint row is keyed by the name a projection records through, and that
// name is an Identity's rendering: `Spec.Name` renders the same string only at
// Ungenerated over the whole key space, so a tracker keyed by it addresses the
// right row for every projection that exists today and another topology's row for
// every partitioned or generational one. That is not a difference a test of the
// happy path can see — the wrong row is usually absent, and absence reads as a
// fresh start — so it is held here, over the call rather than over the spelling.
//
// A name computed from an Identity and then carried in a variable is reported
// too. It is a true positive at this door: the question is whether the row this
// call addresses was derived from the identity in view, and an expression that
// mentions no Identity does not answer it.
func TestEveryTrackerInTheProjectionPackageIsKeyedByAnIdentity(t *testing.T) {
	walked := 0
	for _, checked := range checkedEventPackages(t) {
		if checked.path != eventExtension+"/projection" {
			continue
		}
		tracked, complaints := trackersKeyedBy(checked, checked.path+".Identity")
		walked += tracked
		for _, complaint := range complaints {
			t.Error(complaint)
		}
	}
	if walked < 4 {
		t.Fatalf("%d calls to event.Track were found in event/projection, and the resume, the settlement, the coarser probe and the handoff are the least it makes — so this walked the wrong package", walked)
	}

	t.Run("the control: one call keyed by an identity and one keyed by a name", func(t *testing.T) {
		fixture := checkedFixture(t, "names", `package names

import "github.com/frostgrove/vv/event"

type Identity struct{ projection string }

func (this Identity) String() string { return this.projection }

func Keyed(store event.Checkpoints, identity Identity, name string) {
	_, _ = event.Track(store, identity.String())
	_, _ = event.Track(store, name)
}
`)
		tracked, reported := trackersKeyedBy(fixture, "names.Identity")
		if tracked != 2 || len(reported) != 1 {
			t.Fatalf("the fixture makes two calls, one keyed by an identity and one by a name, and %d calls and %v came back — so the arm above proves nothing", tracked, reported)
		}
	})
}

func trackersKeyedBy(checked checkedPackage, identity string) (int, []string) {
	tracked := 0
	var complaints []string
	for _, file := range checked.files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, is := node.(*ast.CallExpr)
			if !is || len(call.Args) != 2 || called(checked, call.Fun) != eventTypeName("Track") {
				return true
			}
			tracked++
			if mentionsAValueOf(checked, call.Args[1], identity) {
				return true
			}
			complaints = append(complaints, checked.fset.Position(call.Pos()).String()+
				" keys a tracker by a name no Identity was read for, and a checkpoint row is keyed by an Identity's rendering — Spec.Name renders the same string only at Ungenerated over the whole key space")
			return true
		})
	}
	return tracked, complaints
}

func called(checked checkedPackage, fun ast.Expr) string {
	selector, is := fun.(*ast.SelectorExpr)
	if !is {
		return ""
	}
	object, found := checked.info.Uses[selector.Sel]
	if !found || object.Pkg() == nil {
		return ""
	}
	return object.Pkg().Path() + "." + object.Name()
}

func mentionsAValueOf(checked checkedPackage, expr ast.Expr, named string) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		held, is := node.(ast.Expr)
		if !is {
			return true
		}
		if reached := checked.info.TypeOf(held); reached != nil && types.TypeString(reached, nil) == named {
			found = true
		}
		return !found
	})
	return found
}

// A Cover and an Identity each carry a proof — a set somebody checked, a name
// somebody built — and the zero composite literal is the one way to hold one
// without it. So a door that takes either has to ask, and this is what makes that
// a rule rather than a habit: a new exported function taking one of the two whose
// body never reads Count() or Projection() and never compares against the zero
// value is reported here, in the section that adds it.
//
// It reports nothing today, because no exported function of the package takes
// either as a parameter yet. The control is what says so honestly.
func TestEveryDoorTakingACoverOrAnIdentityRefusesItsZeroValue(t *testing.T) {
	for _, complaint := range doorsThatDoNotAsk(t, "../event/projection") {
		t.Error(complaint)
	}

	t.Run("the control: a door that asks and one that does not", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "fixture.go"), []byte(unaskedDoors), 0o644); err != nil {
			t.Fatalf("cannot write the fixture: %v", err)
		}
		reported := strings.Join(doorsThatDoNotAsk(t, directory), "\n")
		for _, named := range []string{"Observe", "Redrive"} {
			if !strings.Contains(reported, named) {
				t.Fatalf("the fixture's %s takes one of the two and asks nothing, and it was not reported, so the arm above proves nothing:\n%s", named, reported)
			}
		}
		for _, named := range []string{"Reached", "Park", "unexported"} {
			if strings.Contains(reported, named) {
				t.Fatalf("the fixture's %s was reported and it asks, so the walk refuses every door rather than the ones that do not ask:\n%s", named, reported)
			}
		}
	})
}

const unaskedDoors = `package fixture

type Cover struct{ partitions []int }

func (this Cover) Count() int { return len(this.partitions) }

type Identity struct{ projection string }

func (this Identity) Projection() string { return this.projection }

func Observe(cover Cover) int { return 1 }

func Redrive(of Identity) error { return nil }

func Reached(cover Cover) error {
	if cover.Count() == 0 {
		return nil
	}
	return nil
}

func Park(of Identity) error {
	if of == (Identity{}) {
		return nil
	}
	return nil
}

func unexported(cover Cover, of Identity) int { return cover.Count() }
`

// The two questions a door may ask, and either one is exact: Count() and
// Projection() answer zero and "" for the value nobody built, and a comparison
// against the zero composite literal is the same question spelled out.
var carriedProofs = map[string]string{"Cover": "Count", "Identity": "Projection"}

func doorsThatDoNotAsk(t *testing.T, directory string) []string {
	t.Helper()
	var complaints []string
	for _, source := range goSourcesIn(t, directory) {
		positions, parsed := parsedSource(t, source)
		for _, declared := range parsed.Decls {
			function, is := declared.(*ast.FuncDecl)
			if !is || !function.Name.IsExported() || function.Body == nil {
				continue
			}
			for _, field := range function.Type.Params.List {
				named, is := field.Type.(*ast.Ident)
				if !is || carriedProofs[named.Name] == "" {
					continue
				}
				for _, held := range field.Names {
					if asksAbout(function.Body, held.Name, named.Name) {
						continue
					}
					complaints = append(complaints, positions.Position(function.Pos()).String()+": "+function.Name.Name+
						" takes the "+named.Name+" "+held.Name+" and never asks whether it was built — a "+named.Name+
						"{} literal is legal in any package, and it is the one value the constructor never answers")
				}
			}
		}
	}
	return complaints
}

func asksAbout(body *ast.BlockStmt, held, named string) bool {
	asked := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch found := node.(type) {
		case *ast.SelectorExpr:
			if reached, is := found.X.(*ast.Ident); is && reached.Name == held && found.Sel.Name == carriedProofs[named] {
				asked = true
			}
		case *ast.CompositeLit:
			if reached, is := found.Type.(*ast.Ident); is && reached.Name == named {
				asked = true
			}
		}
		return !asked
	})
	return asked
}

// A published spec is a form the caller fills in, and a field on one that no
// line of the package reads is a promise the tree does not keep — worse than an
// absent field, because a default assigned to it makes it look live to the next
// reader. RedriveSpec carried a Classifier that way: published, defaulted to
// Classify, and read by nothing, so a caller who supplied one to make a redrive
// give up on a letter got the same forever-requeue as one who supplied none.
//
// The question is asked of the field object rather than of its name, so a field
// called Handler is not counted read because some other type's Handler is.
//
// `receipt.Receipt` is deliberately not asked, and the reason is on the type: it
// is a record the LEDGER fills and the framework reads back, and `RecordedAt` is
// written by `statement_timestamp()` and read by the application and by an
// operator rather than by that package. A walk that demanded a read would force a
// use that does not exist, which is a test shaping code rather than measuring it.
func TestEveryFieldOfAPublishedSpecIsRead(t *testing.T) {
	checked := checkedProjection(t)
	for _, named := range []string{"Spec", "RedriveSpec", "WaitSpec"} {
		for _, complaint := range fieldsNothingReads(t, checked, named) {
			t.Error(complaint)
		}
	}
	receipts := checkedEventPackage(t, eventExtension+"/receipt")
	for _, named := range []string{"ClaimSpec", "ResolveSpec"} {
		for _, complaint := range fieldsNothingReads(t, receipts, named) {
			t.Error(complaint)
		}
	}

	t.Run("the control: a spec with a field nothing reads", func(t *testing.T) {
		fixture := checkedFixture(t, "forms", `package forms

type Spec struct {
	Name       string
	Classifier func(error) int
}

func New(spec Spec) string {
	if spec.Name == "" {
		return "unnamed"
	}
	return spec.Name
}
`)
		reported := strings.Join(fieldsNothingReads(t, fixture, "Spec"), "\n")
		if !strings.Contains(reported, "Spec.Classifier") {
			t.Fatalf("the fixture publishes a field nothing reads and %q came back, so the arm above proves nothing", reported)
		}
		if strings.Contains(reported, "Spec.Name") {
			t.Fatalf("the fixture's read field was reported as unread, so the walk reports every field rather than the ones nothing reads: %q", reported)
		}
	})
}

func checkedProjection(t *testing.T) checkedPackage {
	t.Helper()
	return checkedEventPackage(t, eventExtension+"/projection")
}

func checkedEventPackage(t *testing.T, path string) checkedPackage {
	t.Helper()
	for _, checked := range checkedEventPackages(t) {
		if checked.path == path {
			return checked
		}
	}
	t.Fatalf("%s lists no %s, so nothing about its published forms was checked", surfaceBaseline, path)
	return checkedPackage{}
}

func fieldsNothingReads(t *testing.T, checked checkedPackage, named string) []string {
	t.Helper()
	declared := checked.pkg.Scope().Lookup(named)
	if declared == nil {
		t.Fatalf("%s declares no %s, and the walk asked about a form that is not there", checked.path, named)
	}
	structure, is := declared.Type().Underlying().(*types.Struct)
	if !is {
		t.Fatalf("%s.%s is not a struct, and the walk asked about a form that is not one", checked.path, named)
	}
	read := map[types.Object]bool{}
	for _, used := range checked.info.Uses {
		read[used] = true
	}
	var complaints []string
	for index := range structure.NumFields() {
		field := structure.Field(index)
		if !field.Exported() || read[field] {
			continue
		}
		complaints = append(complaints, fmt.Sprintf("%s.%s.%s is published and no line of the package reads it, so a caller who fills it in gets nothing", checked.path, named, field.Name()))
	}
	return complaints
}

// The four packages of the standard library through which a process reaches the
// outside world. `net` is the transport under `net/http` and every client built
// on it; `net/smtp` is the mail the effect capability exists to keep out of a
// unit of work; `os/exec` is the shell that reaches anything at all.
var dispatchingPackages = map[string]string{
	"net":      "a socket, and everything an HTTP client is built on",
	"net/http": "an HTTP call, which is the irreversible action a rollback cannot take back",
	"net/smtp": "a mail, which is the effect ES-06 exists to keep out of a unit of work",
	"os/exec":  "a process, which reaches anything at all",
}

// §UC-177, §INV-102. The framework CONTRACTS against dispatch, and this is the
// half of that contract a walk can hold: no package on the projection path — the
// package itself and everything it reaches, transitively — imports anything that
// can dial out. It is not a sandbox and the module page says so in the same
// paragraph: a handler may still dial out, and what the shape does is make the
// honest thing the easy thing.
func TestNoPackageOnTheProjectionPathCanDispatch(t *testing.T) {
	reached := reachableFrom(t, typeChecked(t, eventExtension+"/projection").pkg)
	if len(reached) < 10 {
		t.Fatalf("%d packages were reached from event/projection, and the vocabulary, the runtime and the standard library it rests on are more than that — so this walked nothing", len(reached))
	}
	for _, complaint := range dispatchers(reached) {
		t.Error(complaint)
	}

	t.Run("the control: a package that imports net/http is reported by the same walk", func(t *testing.T) {
		fixture := checkedFixture(t, "dialling", `package dialling

import "net/http"

func Dials() *http.Client { return http.DefaultClient }
`)
		reported := dispatchers(reachableFrom(t, fixture.pkg))
		if len(reported) == 0 {
			t.Fatal("the fixture imports net/http and nothing came back, so a walk that resolved no import would be read as a clean tree")
		}
	})
}

// Every package the given one reaches, including itself, by name. The transitive
// half is the whole point: a package that imports a package that imports net/http
// is one hop from a dial-out and nothing about the first package's own import
// list would say so.
func reachableFrom(t *testing.T, pkg *types.Package) map[string][]string {
	t.Helper()
	found := map[string][]string{pkg.Path(): nil}
	var walk func(held *types.Package, through []string)
	walk = func(held *types.Package, through []string) {
		for _, imported := range held.Imports() {
			path := imported.Path()
			if _, seen := found[path]; seen {
				continue
			}
			found[path] = append(append([]string{}, through...), held.Path())
			walk(imported, found[path])
		}
	}
	walk(pkg, nil)
	return found
}

func dispatchers(reached map[string][]string) []string {
	var complaints []string
	for path, through := range reached {
		what, dials := dispatchingPackages[path]
		if !dials {
			continue
		}
		complaints = append(complaints, fmt.Sprintf("%s is on the projection path, reached through %v, and it opens %s",
			path, through, what))
	}
	slices.Sort(complaints)
	return complaints
}

// §INV-086. A partition is a mask and a key never moves except by a split, so
// the one arithmetic that must not appear is a modulus over the hash of a
// sequence key: under `hash % N -> hash % (N+1)` roughly N/(N+1) of all keys
// change partition and each lands in one whose checkpoint is at an unrelated
// position. The walk is over the operator applied to a CALL of the package's own
// hash rather than over the word, so a rename is invisible to it and a comment
// naming it is too.
func TestNoModulusIsAppliedToASequenceHash(t *testing.T) {
	checked := typeChecked(t, eventExtension+"/projection")
	if len(checked.files) < 18 {
		t.Fatalf("%d files of event/projection were read, and the package holds eighteen outside its tests", len(checked.files))
	}
	hashes := hashingFunctions(checked)
	if len(hashes) == 0 {
		t.Fatal("event/projection declares no function whose name says it hashes a key, so this walk has nothing to watch and a modulus over one would be invisible to it")
	}
	for _, complaint := range modulusOverAHash(checked, hashes) {
		t.Error(complaint)
	}

	t.Run("the control: the same walk over a package that does apply one", func(t *testing.T) {
		fixture := checkedFixture(t, "modulus", `package modulus

import "hash/fnv"

func hashOf(key string) uint32 {
	held := fnv.New32a()
	_, _ = held.Write([]byte(key))
	return held.Sum32()
}

func PartitionOf(key string, count uint32) uint32 { return hashOf(key) % count }
`)
		reported := modulusOverAHash(fixture, hashingFunctions(fixture))
		if len(reported) != 1 {
			t.Fatalf("the fixture takes a modulus of a hash and %v came back, so the arm that would have found one in the tree proves nothing", reported)
		}
	})
}

var hashingName = regexp.MustCompile(`(?i)hash`)

func hashingFunctions(checked checkedPackage) map[types.Object]bool {
	found := map[types.Object]bool{}
	for identifier, object := range checked.info.Defs {
		if object == nil || !hashingName.MatchString(identifier.Name) {
			continue
		}
		if _, is := object.Type().(*types.Signature); is {
			found[object] = true
		}
	}
	return found
}

func modulusOverAHash(checked checkedPackage, hashes map[types.Object]bool) []string {
	var complaints []string
	for _, file := range checked.files {
		ast.Inspect(file, func(node ast.Node) bool {
			binary, is := node.(*ast.BinaryExpr)
			if !is || binary.Op != token.REM {
				return true
			}
			for _, side := range []ast.Expr{binary.X, binary.Y} {
				call, isCall := side.(*ast.CallExpr)
				if !isCall {
					continue
				}
				named, isName := call.Fun.(*ast.Ident)
				if !isName || !hashes[checked.info.Uses[named]] {
					continue
				}
				complaints = append(complaints, checked.fset.Position(binary.Pos()).String()+
					" takes a modulus of "+named.Name+
					", and a partition is a mask: under a modulus a key moves the moment the count changes, into a partition whose checkpoint is at an unrelated position")
			}
			return true
		})
	}
	slices.Sort(complaints)
	return complaints
}

// §UC-142, §INV-088. A merge is the one topology change [[D-128]] and [[D-129]]
// jointly forbid, and its SIGNATURE is what it cannot hide: two partitions
// become one, so the function that performs it takes both children's cursors and
// has to decide which the survivor resumes from. There is no such decision — a
// cursor is a store's own bytes and ordering two of them is arithmetic on an
// encoding — so no exported signature of this extension mentions event.Cursor
// twice in its parameters.
//
// TestNoExportedFunctionTakesAPositionAndAnswersACursor asks a different
// question and would not see one: a merge takes no position and answers a cursor
// it was handed. TestCursorIsNeverCompared holds the ordering half.
func TestNoExportedFunctionOrdersOrTakesTwoCursors(t *testing.T) {
	walked := 0
	for _, checked := range checkedEventPackages(t) {
		walked += checked.signatures
		for _, complaint := range takesTwoCursors(checked.pkg) {
			t.Error(complaint)
		}
	}
	if walked < 200 {
		t.Fatalf("%d exported signatures were read across the extension, so this walked the wrong packages", walked)
	}

	t.Run("the control: a package declaring a merge is reported by the same walk", func(t *testing.T) {
		fixture := checkedFixture(t, "merging", `package merging

import "github.com/frostgrove/vv/event"

func Merge(held, taken event.Cursor) event.Cursor { return held }
`)
		reported := takesTwoCursors(fixture.pkg)
		if len(reported) != 1 {
			t.Fatalf("the fixture takes two cursors and %v came back, so the arm that would have found one in the tree proves nothing", reported)
		}
	})
}

func takesTwoCursors(pkg *types.Package) []string {
	cursor := eventTypeName("Cursor")
	var complaints []string
	for _, signature := range exportedSignatures(pkg) {
		params := signature.signature.Params()
		held := 0
		for index := range params.Len() {
			if types.TypeString(params.At(index).Type(), nil) == cursor {
				held++
			}
		}
		if held < 2 {
			continue
		}
		complaints = append(complaints, fmt.Sprintf("%s.%s takes %d cursors, and a call that holds two has to decide which one the survivor resumes from — which is the ordering a cursor does not answer",
			pkg.Path(), signature.name, held))
	}
	slices.Sort(complaints)
	return complaints
}

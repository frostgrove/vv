package scripts

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// A doc that names a test is telling the reader where the proof is. When the
// test is renamed the citation becomes a dead end, and nothing notices: no
// compiler reads Markdown and go test has no opinion about it either. These
// spans look like a citation and are not one.
var spansThatLookLikeACitationAndAreNot = map[string]string{
	"TestCreateRejectsAnUnderageUser": "Errs.md — the test a consumer is about to write",
	"TestGetByID_2":                   "D-020 — the naming this repository refuses",
	"TestServiceRejectsEmptyName":     "Crudtest.md — an example nothing compiles, which is the sentence's point",
	"TestServiceRejectsBadEmail":      "Crudtest.md — the same sentence",
	"TestProfile":                     "D-101 — jobspgfx's deployment profile constant",
}

// A path with a directory in it usually names this repository, and these five do
// not: four are a consumer's own layout, and one is a file whose *absence* is the
// point of the sentence citing it.
var pathsThatNameAnotherTree = map[string]string{
	"cmd/api/main.go":                  "General.md — a consumer's own entry point",
	"cmd/migrate/main.go":              "Vvgoose.md — the command a consumer writes",
	"path/to/file.go":                  "D-020 — a placeholder in a sentence about test naming",
	"src/app/product/product.model.go": "model-generation.md — a consumer's own layout",
	"port/porthttp/decode_test.go":     "Port.md — a file whose absence is the finding",
}

var (
	fenceLine        = regexp.MustCompile("^[ \t]*(```|~~~)")
	inlineCode       = regexp.MustCompile("`([^`\n]+)`")
	citedTestName    = regexp.MustCompile(`^Test[A-Za-z0-9_]*\*?$`)
	declaredTestName = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	citedFunction    = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_]*$`)
	citedGoSymbol    = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_./-]*\.go)(:[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)?$`)
)

func TestEveryTestNameTheDocsCiteExists(t *testing.T) {
	declared := declaredTestNames(t, "..")
	cited := citedTestNames(t, filepath.Join("..", "docs"))

	if len(cited) == 0 {
		t.Fatal("not one doc cited a test name, so nothing here was checked")
	}
	if len(declared) == 0 {
		t.Fatal("not one test name was read out of the tree, so every citation would look stale")
	}

	for _, name := range sortedKeys(cited) {
		if reason, excused := spansThatLookLikeACitationAndAreNot[name]; excused {
			if declared[name] {
				t.Errorf("%s is excused as not being a citation and a test now carries that name: drop the entry (%s)", name, reason)
			}
			continue
		}
		if resolvesToATest(name, declared) {
			continue
		}
		t.Errorf("%s is cited by %s and no _test.go declares it", name, strings.Join(cited[name], ", "))
	}
}

func TestARenamedTestIsReportedAgainstTheDocThatStillCitesIt(t *testing.T) {
	root := docsFixture(t)

	cited := citedTestNames(t, filepath.Join(root, "docs"))
	declared := declaredTestNames(t, root)

	if places := cited["TestThatWasRenamed"]; len(places) != 1 {
		t.Fatalf("the renamed name was cited by one doc and came back as %v", places)
	}
	if resolvesToATest("TestThatWasRenamed", declared) {
		t.Fatal("a name no _test.go declares was accepted as a citation that resolves")
	}
	if !resolvesToATest("TestThatStillExists", declared) {
		t.Fatal("a name the fixture does declare was reported as missing, so the check refuses everything")
	}
}

func TestATestNameInsideAFencedExampleIsNotACitation(t *testing.T) {
	cited := citedTestNames(t, filepath.Join(docsFixture(t), "docs"))

	for _, name := range []string{"TestOnlyEverAnExample", "TestOnlyInATildeFence"} {
		if places, found := cited[name]; found {
			t.Fatalf("%s is written inside a fenced example and was read as a claim about this tree: %v", name, places)
		}
	}
	if _, found := cited["TestThatStillExists"]; !found {
		t.Fatal("a fence swallowed the prose after it, so nothing following a code block is checked either")
	}
}

func TestAWildcardCitationIsSatisfiedByThePrefixItNames(t *testing.T) {
	declared := map[string]bool{"TestGinHTTPGetsARow": true}

	if !resolvesToATest("TestGinHTTP*", declared) {
		t.Fatal("a doc naming a family of tests by prefix was reported as stale")
	}
	if resolvesToATest("TestGinRPC*", declared) {
		t.Fatal("a prefix no test starts with was accepted, so the wildcard matches anything")
	}
}

func docsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("cannot create %s: %v", filepath.Dir(name), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", name, err)
		}
	}
	write("docs/guide.md", "The rule is pinned by `TestThatWasRenamed`.\n\n"+
		"```md\n| `TestOnlyEverAnExample` | the row a template shows |\n```\n\n"+
		"~~~text\n`TestOnlyInATildeFence` is not a claim about this tree either\n~~~\n\n"+
		"And the other half is `TestThatStillExists`.\n")
	write("thing/thing_test.go", "package thing\n\nimport \"testing\"\n\nfunc TestThatStillExists(t *testing.T) {}\n")
	return root
}

func declaredTestNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	declared := map[string]bool{}
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skippedTree(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range declaredTestName.FindAllStringSubmatch(string(content), -1) {
			declared[match[1]] = true
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("cannot read the test names out of %s: %v", root, err)
	}
	return declared
}

func citedTestNames(t *testing.T, root string) map[string][]string {
	t.Helper()
	cited := map[string][]string{}
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skippedTree(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fenced := false
		for number, line := range strings.Split(string(content), "\n") {
			if fenceLine.MatchString(line) {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			for _, span := range inlineCode.FindAllStringSubmatch(line, -1) {
				name := strings.TrimSpace(span[1])
				if !citedTestName.MatchString(name) {
					continue
				}
				place := filepath.ToSlash(path) + ":" + strconv.Itoa(number+1)
				if !contains(cited[name], place) {
					cited[name] = append(cited[name], place)
				}
			}
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("cannot read the citations out of %s: %v", root, err)
	}
	return cited
}

func resolvesToATest(name string, declared map[string]bool) bool {
	if prefix, family := strings.CutSuffix(name, "*"); family {
		for candidate := range declared {
			if strings.HasPrefix(candidate, prefix) {
				return true
			}
		}
		return false
	}
	return declared[name]
}

func skippedTree(name string) bool {
	return name == "node_modules" || name == "vendor" || strings.HasPrefix(name, ".")
}

func contains(places []string, place string) bool {
	for _, known := range places {
		if known == place {
			return true
		}
	}
	return false
}

func sortedKeys(cited map[string][]string) []string {
	names := make([]string, 0, len(cited))
	for name := range cited {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// A doc that names a function and a status code in the same breath is making a
// claim about that function's refusal. `go vet` cannot read it, so the claim
// outlives the code: D-008 kept promising a 403 for a hidden row long after
// `gate.saveTarget` had started answering `crud.ErrNotFound`.
func TestTheStatusADocPromisesIsTheOneTheFunctionItNamesReturns(t *testing.T) {
	refusals := refusalsByFunction(t, "..")
	if len(refusals) == 0 {
		t.Fatal("not one function was classified, so every promise would look kept")
	}

	claims := statusClaims(t, filepath.Join("..", "docs"), refusals)
	if len(claims) == 0 {
		t.Fatal("not one doc named a function beside a status code, so nothing here was checked")
	}
	for _, claim := range claims {
		if !claim.kept {
			t.Errorf("%s promises %s for %s, which answers %s", claim.place, claim.promised, strings.Join(claim.functions, " / "), claim.answers)
		}
	}
}

func TestADocPromisingTheStatusTheFunctionRefusesIsReported(t *testing.T) {
	root := statusFixture(t)
	refusals := refusalsByFunction(t, root)

	claims := statusClaims(t, filepath.Join(root, "docs"), refusals)
	if len(claims) != 2 {
		t.Fatalf("the fixture makes two claims and %d were read", len(claims))
	}
	for _, claim := range claims {
		switch claim.promised {
		case "403":
			if claim.kept {
				t.Fatal("a doc promising 403 for a function that only ever answers not-found was accepted")
			}
		case "404":
			if !claim.kept {
				t.Fatal("a doc promising 404 for a function that answers not-found was reported, so the check refuses everything")
			}
		}
	}
}

type statusClaim struct {
	place     string
	promised  string
	answers   string
	functions []string
	kept      bool
}

func statusClaims(t *testing.T, root string, refusals map[string]string) []statusClaim {
	t.Helper()
	var claims []statusClaim
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skippedTree(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fenced := false
		for number, line := range strings.Split(string(content), "\n") {
			if fenceLine.MatchString(line) {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			promised, single := loneStatusCode(line)
			if !single {
				continue
			}
			named, answers := classifiedFunctions(line, refusals)
			if len(named) == 0 {
				continue
			}
			claims = append(claims, statusClaim{
				place:     filepath.ToSlash(path) + ":" + strconv.Itoa(number+1),
				promised:  promised,
				answers:   strings.Join(answers, " / "),
				functions: named,
				kept:      contains(answers, promised),
			})
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("cannot read the status claims out of %s: %v", root, err)
	}
	return claims
}

func classifiedFunctions(line string, refusals map[string]string) (named []string, answers []string) {
	for _, span := range inlineCode.FindAllStringSubmatch(line, -1) {
		name := citedFunctionName(span[1])
		answer, classified := refusals[name]
		if !classified || contains(named, name) {
			continue
		}
		named = append(named, name)
		if !contains(answers, answer) {
			answers = append(answers, answer)
		}
	}
	return named, answers
}

func citedFunctionName(span string) string {
	name := strings.TrimSpace(span)
	if open := strings.Index(name, "("); open >= 0 {
		name = strings.TrimSpace(name[:open])
	}
	if colon := strings.LastIndex(name, ":"); colon >= 0 {
		name = name[colon+1:]
	}
	if !citedFunction.MatchString(name) {
		return ""
	}
	return name
}

func loneStatusCode(line string) (string, bool) {
	found := ""
	for index := 0; index+3 <= len(line); index++ {
		code := line[index : index+3]
		if code != "403" && code != "404" {
			continue
		}
		if index > 0 && !boundaryBefore(line[index-1]) {
			continue
		}
		if !boundaryAfter(line[index+3:]) {
			continue
		}
		if found != "" && found != code {
			return "", false
		}
		found = code
	}
	return found, found != ""
}

func boundaryBefore(char byte) bool {
	return !isWordByte(char) && char != '.'
}

func boundaryAfter(rest string) bool {
	if rest == "" {
		return true
	}
	if isWordByte(rest[0]) {
		return false
	}
	return !(rest[0] == '.' && len(rest) > 1 && rest[1] >= '0' && rest[1] <= '9')
}

func isWordByte(char byte) bool {
	switch {
	case char >= '0' && char <= '9', char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char == '_':
		return true
	}
	return false
}

func refusalsByFunction(t *testing.T, root string) map[string]string {
	t.Helper()
	bodies := map[string][]string{}
	fileSet := token.NewFileSet()
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skippedTree(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(fileSet, path, source, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		for _, declaration := range parsed.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction {
				continue
			}
			body := string(source[fileSet.Position(function.Pos()).Offset:fileSet.Position(function.End()).Offset])
			bodies[function.Name.Name] = append(bodies[function.Name.Name], body)
			if receiver := receiverTypeName(function); receiver != "" {
				qualified := receiver + "." + function.Name.Name
				bodies[qualified] = append(bodies[qualified], body)
			}
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("cannot read the functions out of %s: %v", root, err)
	}

	refusals := map[string]string{}
	for name, found := range bodies {
		if len(found) != 1 {
			continue
		}
		forbidden := strings.Contains(found[0], "Denied(") || strings.Contains(found[0], "ErrForbidden")
		missing := strings.Contains(found[0], "ErrNotFound")
		switch {
		case forbidden && !missing:
			refusals[name] = "403"
		case missing && !forbidden:
			refusals[name] = "404"
		}
	}
	return refusals
}

func receiverTypeName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return ""
	}
	expression := function.Recv.List[0].Type
	for {
		switch typed := expression.(type) {
		case *ast.StarExpr:
			expression = typed.X
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.IndexListExpr:
			expression = typed.X
		case *ast.Ident:
			return typed.Name
		default:
			return ""
		}
	}
}

func statusFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("cannot create %s: %v", filepath.Dir(name), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", name, err)
		}
	}
	write("gate/gate.go", "package gate\n\nimport \"errors\"\n\nvar ErrNotFound = errors.New(\"not found\")\n\n"+
		"func hiddenTarget() error { return ErrNotFound }\n")
	write("docs/promise.md", "The refusal is `hiddenTarget` — a deliberate 403.\n\n"+
		"```md\n`hiddenTarget` inside a fence claims 403 and is an example\n```\n\n"+
		"And `hiddenTarget` answers 404 for a row that is not yours.\n")
	return root
}

func TestEverySymbolTheDocsCiteIsDeclaredWhereTheDocSaysItIs(t *testing.T) {
	stale, checked := staleSymbolCitations(t, "..")
	if checked < 1000 {
		t.Fatal("no citation named a file this tree holds, so nothing here was checked")
	}
	for _, report := range stale {
		t.Error(report)
	}
}

func TestASymbolThatMovedOutOfTheFileTheDocNamesIsReported(t *testing.T) {
	stale, checked := staleSymbolCitations(t, symbolFixture(t))

	if checked != 2 {
		t.Fatalf("the fixture holds two citations into this tree and %d were checked — the third names the reader's own file", checked)
	}
	if len(stale) != 1 {
		t.Fatalf("one of the two moved and the check reported %v", stale)
	}
	if !strings.Contains(stale[0], "Moved") {
		t.Fatalf("the report does not name the symbol it is about: %s", stale[0])
	}
}

func staleSymbolCitations(t *testing.T, root string) (stale []string, checked int) {
	t.Helper()
	declared := declaredNamesByFile(t, root)
	if len(declared) == 0 {
		t.Fatalf("not one Go file was read out of %s, so every citation would look live", root)
	}

	cited := citedSymbols(t, filepath.Join(root, "docs"))
	if len(cited) == 0 {
		t.Fatalf("not one doc under %s cited a symbol, so nothing here was checked", root)
	}
	for _, citation := range cited {
		files := filesNamed(citation.path, declared)
		if len(files) == 0 {
			// A bare file name may belong to the reader's own project. A path with
			// a directory in it is a claim about this tree, and a claim about a
			// file this tree does not have is the drift a restructure produces:
			// the symbol check below can only see a symbol that moved *within* a
			// file that still exists.
			if !strings.Contains(citation.path, "/") || pathsThatNameAnotherTree[citation.path] != "" {
				continue
			}
			checked++
			stale = append(stale, fmt.Sprintf("%s cites %s and this tree has no %s",
				citation.place, citation.text, citation.path))
			continue
		}
		checked++
		if citation.symbol == "" {
			continue
		}
		if !declaresSymbol(files, declared, citation.symbol) {
			stale = append(stale, fmt.Sprintf("%s cites %s and %s declares no %s",
				citation.place, citation.text, strings.Join(files, " / "), citation.symbol))
		}
	}
	return stale, checked
}

func symbolFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("cannot create %s: %v", filepath.Dir(name), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", name, err)
		}
	}
	write("pkg/thing.go", "package pkg\n\ntype Holder struct{ Name string }\n\nfunc (this *Holder) Kept() int { return 1 }\n")
	write("docs/map.md", "It lives in `pkg/thing.go:Holder.Kept`, not in `pkg/thing.go:Moved`.\n\n"+
		"Your own project's `vv_wire_gen.go:Anything` is not this tree's to hold.\n")
	return root
}

type symbolCitation struct {
	place  string
	text   string
	path   string
	symbol string
}

func citedSymbols(t *testing.T, root string) []symbolCitation {
	t.Helper()
	var cited []symbolCitation
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skippedTree(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fenced := false
		for number, line := range strings.Split(string(content), "\n") {
			if fenceLine.MatchString(line) {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			for _, span := range inlineCode.FindAllStringSubmatch(line, -1) {
				match := citedGoSymbol.FindStringSubmatch(strings.TrimSpace(span[1]))
				if match == nil {
					continue
				}
				cited = append(cited, symbolCitation{
					place:  filepath.ToSlash(path) + ":" + strconv.Itoa(number+1),
					text:   strings.TrimSpace(span[1]),
					path:   match[1],
					symbol: strings.TrimPrefix(match[2], ":"),
				})
			}
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("cannot read the citations out of %s: %v", root, err)
	}
	return cited
}

func filesNamed(path string, declared map[string]map[string]bool) []string {
	var found []string
	for candidate := range declared {
		if candidate == path || strings.HasSuffix(candidate, "/"+path) {
			found = append(found, candidate)
		}
	}
	sort.Strings(found)
	return found
}

func declaresSymbol(files []string, declared map[string]map[string]bool, symbol string) bool {
	last := symbol
	if dot := strings.LastIndex(symbol, "."); dot >= 0 {
		last = symbol[dot+1:]
	}
	for _, file := range files {
		if declared[file][symbol] || declared[file][last] {
			return true
		}
	}
	return false
}

func declaredNamesByFile(t *testing.T, root string) map[string]map[string]bool {
	t.Helper()
	declared := map[string]map[string]bool{}
	fileSet := token.NewFileSet()
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skippedTree(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		relative := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(path, root), "/"))
		names := map[string]bool{}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.FuncDecl:
				names[typed.Name.Name] = true
				if receiver := receiverTypeName(typed); receiver != "" {
					names[receiver+"."+typed.Name.Name] = true
				}
			case *ast.TypeSpec:
				names[typed.Name.Name] = true
			case *ast.ValueSpec:
				for _, name := range typed.Names {
					names[name.Name] = true
				}
			case *ast.Field:
				for _, name := range typed.Names {
					names[name.Name] = true
				}
			}
			return true
		})
		declared[relative] = names
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("cannot read the declarations out of %s: %v", root, err)
	}
	return declared
}

// The four names `event/projection` removed under a rename, each paired with
// what replaced it. A consumer meets every one of them as a compile error, and
// the only string they hold at that moment is the old one — so the page that is
// their reference has to carry it, beside the new name, on one line. A page
// documenting only the new names is a page the search that sent them there does
// not match.
var renamesTheProjectionPagesMustCarry = [][2]string{
	{"Quarantines", "Park"},
	{"Quarantined", "Letter"},
	{"Quarantine", "ParkSequence"},
	{"Spec.Quarantine", "Spec.Park"},
}

var projectionPages = []string{"../docs/modules/en/projection.md", "../docs/modules/ru/projection.md"}

// Both guides, because they are parallel by design, and both halves, because
// either alone is green on a lie: a page may name a rename the package never
// made, and a package may make one no page mentions. The old names must be gone
// from `event/projection`, the new ones must be there, and the one name that
// deliberately did NOT change — `Progress.Quarantined`, the kernel's field —
// must still be declared and must be said so on both pages. Without that last
// row a reader grepping the old vocabulary finds the only member of it that is
// still correct and concludes the rest are too.
func TestEveryNameTheProjectionPackageRenamedIsOnBothGuidesAsARename(t *testing.T) {
	held := packageDeclarations(t, "../event/projection")
	for _, gone := range []struct{ kind, name string }{
		{"a type", "Quarantines"},
		{"a type", "Quarantined"},
		{"a constant", "Quarantine"},
		{"a field of Spec", "Spec.Quarantine"},
	} {
		if held.holds(gone.kind, gone.name) {
			t.Errorf("event/projection still declares %s named %s, so the migration table on both projection pages documents a rename that did not happen", gone.kind, gone.name)
		}
	}
	for _, arrived := range []struct{ kind, name string }{
		{"a type", "Park"},
		{"a type", "Letter"},
		{"a constant", "ParkSequence"},
		{"a field of Spec", "Spec.Park"},
	} {
		if !held.holds(arrived.kind, arrived.name) {
			t.Errorf("event/projection declares no %s named %s, so this walk read the wrong package and the four rows below prove nothing", arrived.kind, arrived.name)
		}
	}
	if kernel := packageDeclarations(t, "../event"); !kernel.holds("a field of Progress", "Progress.Quarantined") {
		t.Error("event declares no Progress.Quarantined, and the sentence both projection pages carry — that it keeps its name and its meaning — has stopped being true")
	}
	for _, page := range projectionPages {
		for _, complaint := range renamesNotWrittenAsRenames(t, page, renamesTheProjectionPagesMustCarry) {
			t.Error(complaint)
		}
		if !namesBoth(t, page, "Progress.Quarantined", "Progress.Quarantined") {
			t.Errorf("%s never names Progress.Quarantined, which is the one name of this vocabulary that did not change", page)
		}
	}
}

func TestAPageThatDocumentsOnlyTheNewNamesIsReported(t *testing.T) {
	page := filepath.Join(t.TempDir(), "projection.md")
	written := "`Park` is the queue, `Letter` is one entry, `ParkSequence` is the verdict, `Spec.Park` is the field.\n"
	if err := os.WriteFile(page, []byte(written), 0o600); err != nil {
		t.Fatalf("the fixture page could not be written: %v", err)
	}
	reported := renamesNotWrittenAsRenames(t, page, renamesTheProjectionPagesMustCarry)
	if len(reported) != len(renamesTheProjectionPagesMustCarry) {
		t.Fatalf("a page carrying only the new names was reported for %d of the %d renames, so a page that documents none of them reads as documenting some",
			len(reported), len(renamesTheProjectionPagesMustCarry))
	}
}

func renamesNotWrittenAsRenames(t *testing.T, path string, pairs [][2]string) []string {
	t.Helper()
	var missing []string
	for _, pair := range pairs {
		if namesBoth(t, path, pair[0], pair[1]) {
			continue
		}
		missing = append(missing, fmt.Sprintf("%s carries no line naming `%s` beside `%s`, so a consumer holding the removed name finds nothing on the page they were sent to", path, pair[0], pair[1]))
	}
	return missing
}

// One line, and the names read out of its code spans rather than off the raw
// text: `Quarantine` is a substring of `Quarantines` and of `Quarantined`, so a
// plain search answers yes for a row that is about another symbol.
func namesBoth(t *testing.T, path, was, is string) bool {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		spans := map[string]bool{}
		for _, span := range inlineCode.FindAllStringSubmatch(line, -1) {
			spans[span[1]] = true
		}
		if spans[was] && spans[is] {
			return true
		}
	}
	return false
}

// Package-level declarations of one directory, by kind, because the kinds are
// what tells a rename from a name that merely still exists: `Readiness` carries
// a field `Quarantined` on purpose, and a walk that collected every identifier
// would read it as the removed struct.
type declarations struct {
	types  map[string]bool
	values map[string]bool
	fields map[string]bool
}

func (this declarations) holds(kind, name string) bool {
	switch {
	case strings.Contains(kind, "field"):
		return this.fields[name]
	case strings.Contains(kind, "constant"):
		return this.values[name]
	default:
		return this.types[name]
	}
}

func packageDeclarations(t *testing.T, dir string) declarations {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot read %s: %v", dir, err)
	}
	held := declarations{types: map[string]bool{}, values: map[string]bool{}, fields: map[string]bool{}}
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("cannot parse %s: %v", path, err)
		}
		for _, declared := range parsed.Decls {
			general, is := declared.(*ast.GenDecl)
			if !is {
				continue
			}
			for _, spec := range general.Specs {
				switch typed := spec.(type) {
				case *ast.TypeSpec:
					held.types[typed.Name.Name] = true
					structure, shaped := typed.Type.(*ast.StructType)
					if !shaped {
						continue
					}
					for _, field := range structure.Fields.List {
						for _, name := range field.Names {
							held.fields[typed.Name.Name+"."+name.Name] = true
						}
					}
				case *ast.ValueSpec:
					for _, name := range typed.Names {
						held.values[name.Name] = true
					}
				}
			}
		}
	}
	return held
}

// "Exactly once" is a claim no queue and no event log in this repository can
// make, and a reader who takes one stops writing the idempotency their consumer
// needs. The phrase itself is not the defect — a preload resolved exactly once
// and a span ended exactly once are ordinary sentences — so what is checked is
// the phrase used *about delivery*, and every such use must be a prohibition.
//
// `docs/` ships two languages by design, so how the claim is worded is a row of
// a table rather than a literal in the walker. A language written in this tree
// whose counting vocabulary was never read at all fails the check: a row that
// matches nothing is a row that permits everything, and blindness here should
// be loud rather than green.
//
// A refusal is a negation *governing the claim*, not a negation nearby, and the
// distance at which a negation stops governing is the whole of what this model
// is. A negation inside the clause the phrase sits in refuses it: "no wording
// here may call it exactly-once". Further away — an earlier clause, or the
// sentence before — a negation refuses the claim only when that span is *about*
// the claim, by naming a promise or by spelling the frequency itself: "We never
// promise it. The broker delivers each event exactly once." Anything looser
// exempts a promise for a negation that is merely next to it, and the house
// style here is negative-heavy prose — "The framework deduplicates nothing",
// "there is no head" — so a window of neighbouring sentences reads half the
// corpus as a refusal. A heading that itself refuses covers the list beneath it.
//
// Every wording carried here is a claim spelled *positively*, because that is
// what the negation model can classify. The same promise spelled as a denial —
// "no duplicates ever reach your handler" — is not read: its negation is the
// claim, so the model would have to be inverted for it, and "deduplicates on
// that key" is ordinary prose about a mechanism that appears eleven times in
// this tree. That residual is stated in `[[FL-036]]` rather than half-checked.
type wording struct {
	language string
	written  *regexp.Regexp
	promise  *regexp.Regexp
	weaker   *regexp.Regexp
	about    *regexp.Regexp
	negation *regexp.Regexp
	denying  *regexp.Regexp
	refusing *regexp.Regexp
}

var deliveryWordings = []wording{
	{
		language: "English",
		written:  regexp.MustCompile(`(?i)\bthe\b`),
		promise:  regexp.MustCompile(`(?i)exactly.?once|once and only once|one time and one time only`),
		weaker:   regexp.MustCompile(`(?i)at.least.once`),
		about:    regexp.MustCompile(`(?i)deliver|broker|subscrib|consumer|projection|projector|handler|dispatch|inbox|outbox|at.least.once`),
		negation: regexp.MustCompile(`(?i)\bno\b|\bnot\b|\bnever\b|\bnothing\b|\bnone\b|non-goal|\bcannot\b|\bwithout\b|n't\b`),
		denying:  regexp.MustCompile(`(?i)promis|guarantee|\bclaim|\bwording\b`),
		refusing: regexp.MustCompile(`(?i)non-goal|out of scope|not in scope`),
	},
	{
		language: "Russian",
		written:  regexp.MustCompile(`[А-Яа-яЁё]`),
		promise:  regexp.MustCompile(`(?i)ровно один раз|ровно однажды|строго один раз|точно один раз|один и только один раз`),
		weaker:   regexp.MustCompile(`(?i)не менее одного раза|хотя бы один раз|как минимум один раз`),
		about:    regexp.MustCompile(`(?i)доставк|доставл|брокер|подписчик|потребител|проекц|обработчик|не менее одного раза|хотя бы один раз`),
		negation: regexp.MustCompile(`(?i)(^|[^\p{L}])(не|ни|нет|никогда|ничего|без)([^\p{L}]|$)`),
		denying:  regexp.MustCompile(`(?i)обеща|гаранти|утвержд|формулиров`),
		refusing: regexp.MustCompile(`(?i)не цел|вне области|не входит`),
	},
}

var (
	markdownHeading = regexp.MustCompile(`^#{1,6} `)
	markdownList    = regexp.MustCompile(`^([-*+]|\d+\.)\s`)
)

func TestNoDocPromisesExactlyOnceDelivery(t *testing.T) {
	claims, read := deliveryClaims(t, filepath.Join("..", "docs"))

	checked := 0
	for _, spoken := range deliveryWordings {
		if !read.written[spoken.language] {
			continue
		}
		if read.counted[spoken.language] == 0 {
			t.Errorf("%s is written in this tree and neither of its two wordings for how often a thing is delivered was read once, so the %s row matches nothing and permits everything", spoken.language, spoken.language)
		}
		checked += read.checked[spoken.language]
	}
	if checked == 0 {
		t.Fatal("not one doc used the phrase about delivery, so nothing here was checked and a moved directory reads as a clean tree")
	}
	for _, claim := range claims {
		t.Errorf("%s promises exactly-once delivery, and delivery in this repository is at least once", claim)
	}
}

func TestAPromiseOfExactlyOnceDeliveryIsReportedAndARefusalOfOneIsNot(t *testing.T) {
	claims, read := deliveryClaims(t, deliveryFixture(t))

	if endsWithOneOf(claims, "window.md:5") {
		t.Fatalf("the sentence before window.md:5's promise denies the promise itself and %v came back, so a page that says it does not promise this is reported anyway", claims)
	}
	if !endsWithOneOf(claims, "window.md:13") {
		t.Fatalf("the sentence before window.md:13's promise negates deduplication and not the claim, and the promise went unreported, so any negation at all in the sentence before licenses an exactly-once promise: %v", claims)
	}
	if endsWithOneOf(claims, "обход.md:9") || !endsWithOneOf(claims, "обход.md:11") {
		t.Fatalf("the same pair written in Russian did not come back the same way, so the two rows of the table do not read a preceding sentence alike: %v", claims)
	}

	promised := []string{
		"walk.md:3", "walk.md:7", "walk.md:9", "walk.md:11", "walk.md:15",
		"window.md:3", "window.md:7", "window.md:10", "window.md:13",
		"обход.md:3", "обход.md:7", "обход.md:11",
	}
	if len(claims) != len(promised) {
		t.Fatalf("the fixture writes %d promises and %v came back", len(promised), claims)
	}
	for _, expected := range promised {
		if !endsWithOneOf(claims, expected) {
			t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing: %v", expected, claims)
		}
	}
	if read.checked["English"] != 13 || read.checked["Russian"] != 5 {
		t.Fatalf("the fixture writes thirteen English uses about delivery and five Russian ones, and %d and %d were read", read.checked["English"], read.checked["Russian"])
	}
}

func TestTheSentenceBeforeAClaimIsTheOneThePagePutsThere(t *testing.T) {
	for written, before := range map[string]string{
		"Delivery is never exactly-once here. The broker delivers each event exactly once.": "Delivery is never exactly-once here. ",
		"We do not promise it.  The broker delivers each event exactly once.":               "We do not promise it.  ",
		"No such guarantee! The broker delivers each event exactly once.":                   "No such guarantee! ",
		"The broker delivers each event exactly once.":                                      "",
	} {
		paragraph := paragraphsOf(written)[0]
		found := deliveryWordings[0].promise.FindAllStringIndex(paragraph.text, -1)
		read := paragraph.sentenceBefore(found[len(found)-1][0])
		if read != before {
			t.Fatalf("the sentence before the promise in %q read as %q, and the paragraph puts %q there — a refusal written there is not seen and delivery vocabulary written there is not read", written, read, before)
		}
	}
}

func endsWithOneOf(claims []string, expected string) bool {
	for _, claim := range claims {
		if strings.HasSuffix(claim, expected) {
			return true
		}
	}
	return false
}

type reading struct {
	written map[string]bool
	counted map[string]int
	checked map[string]int
}

func deliveryClaims(t *testing.T, root string) ([]string, reading) {
	t.Helper()
	var claims []string
	read := reading{written: map[string]bool{}, counted: map[string]int{}, checked: map[string]int{}}

	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skippedTree(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		claims = append(claims, claimsIn(filepath.ToSlash(path), string(content), read)...)
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("cannot read the delivery claims out of %s: %v", root, err)
	}
	return claims, read
}

func claimsIn(path, content string, read reading) []string {
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
				if !spoken.about.MatchString(paragraph.around(at[0])) {
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

// A document is read a paragraph at a time and a claim is reported at a line
// number, so every offset this walk takes is into text it rebuilt from the one
// it was given. Two things hold over any bytes at all: nothing panics, and a
// line a reader is sent to is a line the document has.
func FuzzADeliveryClaimIsReportedAtALineTheDocumentHas(f *testing.F) {
	for _, seed := range []string{
		"The broker delivers each event exactly once.",
		"The delivery is at least once, and never exactly-once.",
		"## Initial non-goals\n\n- The exactly-once broker delivery.\n",
		"| a table row | the broker delivers exactly once |\n",
		"Проектор доставляет каждое событие ровно один раз.",
		"доставка ровно один раз",
		"the broker delivers exactly\nonce to the handler",
		"the handler sees %s exactly once", "",
		"\n\n\n", "#", "# ", "- ", "|", ".", "...the exactly once...deliver",
		"á the broker delivers exactly once", "\xff\xfe the broker delivers exactly once",
		strings.Repeat("the broker delivers exactly once. ", 40),
		"the broker delivers exactly once, exactly once, exactly once",
		"# The heading\n\nthe broker delivers exactly once",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, content string) {
		read := reading{written: map[string]bool{}, counted: map[string]int{}, checked: map[string]int{}}
		lines := strings.Count(content, "\n") + 1
		for _, claim := range claimsIn("doc.md", content, read) {
			number, err := strconv.Atoi(claim[strings.LastIndexByte(claim, ':')+1:])
			if err != nil {
				t.Fatalf("%q was reported as %q, and a place a person opens is a file and a line: %v", content, claim, err)
			}
			if number < 1 || number > lines {
				t.Fatalf("%q has %d lines and a claim was reported at line %d, which nobody can open", content, lines, number)
			}
		}
	})
}

func (this wording) refuses(paragraph block, at []int) bool {
	if this.refusing.MatchString(paragraph.heading) {
		return true
	}
	if this.negation.MatchString(paragraph.clauseAt(at)) {
		return true
	}
	return this.denies(paragraph.leadingTo(at))
}

func (this wording) denies(written string) bool {
	if !this.negation.MatchString(written) {
		return false
	}
	return this.denying.MatchString(written) || this.promise.MatchString(written) || this.weaker.MatchString(written)
}

// A paragraph read as one text, because a wrapped sentence is one sentence and
// a phrase split across two lines is one phrase. A list item, a table row and a
// heading each begin a paragraph of their own: a bullet's neighbour is not its
// context, and a heading is asked separately.
type block struct {
	text    string
	starts  []int
	numbers []int
	heading string
}

func paragraphsOf(content string) []block {
	var blocks []block
	var current *block
	heading := ""
	flush := func() {
		if current != nil {
			blocks = append(blocks, *current)
			current = nil
		}
	}
	for number, line := range strings.Split(content, "\n") {
		written := strings.TrimSpace(line)
		if written == "" {
			flush()
			continue
		}
		if markdownHeading.MatchString(written) {
			flush()
			heading = written
			continue
		}
		if current == nil || markdownList.MatchString(written) || strings.HasPrefix(written, "|") {
			flush()
			current = &block{heading: heading}
		}
		if current.text != "" {
			current.text += " "
		}
		current.starts = append(current.starts, len(current.text))
		current.numbers = append(current.numbers, number+1)
		current.text += written
	}
	flush()
	return blocks
}

func (this block) lineAt(offset int) int {
	number := this.numbers[0]
	for index, start := range this.starts {
		if start > offset {
			break
		}
		number = this.numbers[index]
	}
	return number
}

func (this block) around(offset int) string {
	return this.sentenceBefore(offset) + " " + this.text[this.sentenceFrom(offset):this.sentenceTo(offset)]
}

func (this block) attachedTo(at []int) string {
	return this.sentenceBefore(at[0]) + " " + this.text[this.sentenceFrom(at[0]):this.clauseTo(at[1])]
}

func (this block) clauseAt(at []int) string {
	return this.text[this.clauseFrom(at[0]):this.clauseTo(at[1])]
}

func (this block) leadingTo(at []int) string {
	return this.sentenceBefore(at[0]) + " " + this.text[this.sentenceFrom(at[0]):this.clauseFrom(at[0])]
}

func (this block) sentenceFrom(offset int) int {
	for index := offset; index > 0; index-- {
		if endsASentence(this.text, index-1) {
			return skipSpace(this.text, index)
		}
	}
	return 0
}

func (this block) sentenceTo(offset int) int {
	for index := offset; index < len(this.text); index++ {
		if endsASentence(this.text, index) {
			return index
		}
	}
	return len(this.text)
}

func (this block) sentenceBefore(offset int) string {
	from := this.sentenceFrom(offset)
	if from == 0 {
		return ""
	}
	ended := from - 1
	for ended > 0 && this.text[ended] == ' ' {
		ended--
	}
	return this.text[this.sentenceFrom(ended):from]
}

func (this block) clauseFrom(offset int) int {
	sentence := this.sentenceFrom(offset)
	from := sentence
	for index, letter := range this.text[sentence:offset] {
		if separatesAClause(letter) {
			from = skipSpace(this.text, sentence+index+utf8.RuneLen(letter))
		}
	}
	return from
}

func (this block) clauseTo(offset int) int {
	for index, letter := range this.text[offset:] {
		if separatesAClause(letter) || endsASentence(this.text, offset+index) {
			return offset + index
		}
	}
	return len(this.text)
}

func separatesAClause(letter rune) bool {
	return strings.ContainsRune(",;:—", letter)
}

func endsASentence(text string, index int) bool {
	if strings.IndexByte(".!?", text[index]) < 0 {
		return false
	}
	return index+1 == len(text) || text[index+1] == ' '
}

func skipSpace(text string, index int) int {
	for index < len(text) && text[index] == ' ' {
		index++
	}
	return index
}

func deliveryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	written := map[string][]string{
		"walk.md": {
			"# A walk over the log",
			"",
			"A projector that reads to the end delivers every committed event exactly once, so you do not need an inbox table.",
			"",
			"# No configuration needed",
			"",
			"A broker delivers each event exactly once.",
			"",
			"Every subscriber sees each committed event exactly once.",
			"",
			"A projector delivers every committed event exactly once.",
			"",
			"Delivery is at least once, and no wording here may call it exactly-once.",
			"",
			"A broker delivers each event once and only once.",
			"",
			"Delivery is at least once, and no page here promises it once and only once.",
			"",
			"## Initial non-goals",
			"",
			"- Exactly-once broker delivery.",
			"",
			"A preload resolves its options exactly once.",
		},
		"window.md": {
			"# The delivery window",
			"",
			"The broker is what carries them. Each event arrives exactly once.",
			"",
			"We never promise it. The broker delivers each event exactly once.",
			"",
			"- The projector delivers each event exactly once",
			"- It does not order streams",
			"",
			"| the projector delivers each event exactly once |",
			"| it does not order streams |",
			"",
			"The framework deduplicates nothing. Every event reaches the handler exactly once, so a read model needs no idempotency of its own.",
		},
		"обход.md": {
			"# Обход журнала",
			"",
			"Проектор доставляет каждое событие ровно один раз.",
			"",
			"Доставка гарантируется хотя бы один раз, и ни один текст здесь не назовёт её «ровно один раз».",
			"",
			"Каждый подписчик получает событие один и только один раз.",
			"",
			"Мы никогда этого не обещаем. Брокер доставляет каждое событие ровно один раз.",
			"",
			"Фреймворк ничего не дедуплицирует. Каждое событие доходит до обработчика ровно один раз.",
		},
	}
	for name, lines := range written {
		if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
			t.Fatalf("cannot write the fixture: %v", err)
		}
	}
	return root
}

// The reverse index in docs/ai/flows/Index.md is what an agent reads before
// editing a file, and CLAUDE.md's sentence about it is exact: an index that does
// not list a file is worse than a missing file, because an agent trusts the index
// and stops looking. Nothing else in this repository can see a file that was
// added and not listed — the flow bodies name symbols and the surface baseline
// names packages, and neither notices a whole new file.
//
// TWO ARMS, BECAUSE THE FIRST IS VACUOUS ALONE. A row that points at a flow whose
// body never names the file is the same failure one hop further on: the agent
// follows the index, opens the flow, and finds nothing about the file it came to
// read. So every non-test file under event/ must have a row, AND every flow that
// row names must name the file in its own body.
func TestEveryProjectionSourceFileIsNamedByTheFlowReverseIndex(t *testing.T) {
	listed := reverseIndex(t)
	sources := eventSources(t)
	if len(sources) < 90 {
		t.Fatalf("%d non-test files were read under event/, and the extension holds far more — so this walked the wrong directory", len(sources))
	}
	for _, source := range sources {
		row, named := reverseIndexRow(listed, source)
		if !named {
			t.Errorf("%s is a file of the event extension and docs/ai/flows/Index.md's reverse index does not name it, so an agent reading the index before editing it finds a file outside every flow", source)
			continue
		}
		for _, flow := range flowNumber.FindAllString(row, -1) {
			body := flowBody(t, flow)
			if body == "" {
				t.Errorf("%s's row names %s and no such flow document exists", source, flow)
				continue
			}
			if !strings.Contains(body, source) {
				t.Errorf("%s's row sends a reader to %s and that flow never names the file, which is the index being trusted and then found empty", source, flow)
			}
		}
	}

	t.Run("the control: the same containment answers no for a file nobody wrote", func(t *testing.T) {
		if strings.Contains(listed, "`event/projection/no-such-file.go`") {
			t.Fatal("the index names a file that does not exist, so the containment above is satisfied by anything and the arm proves nothing")
		}
		if _, named := reverseIndexRow(listed, "event/projection/no-such-file.go"); named {
			t.Fatal("a row was found for a file nobody wrote, so the row lookup matches anything")
		}
	})
}

var flowNumber = regexp.MustCompile(`FL-[0-9]+`)

func reverseIndexRow(listed, source string) (string, bool) {
	for _, line := range strings.Split(listed, "\n") {
		if strings.HasPrefix(line, "| `"+source+"` |") {
			return line, true
		}
	}
	return "", false
}

func flowBody(t *testing.T, flow string) string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join("..", "docs", "ai", "flows", flow+"-*.md"))
	if err != nil || len(found) != 1 {
		return ""
	}
	held, err := os.ReadFile(found[0])
	if err != nil {
		t.Fatalf("cannot read %s: %v", found[0], err)
	}
	return string(held)
}

func reverseIndex(t *testing.T) string {
	t.Helper()
	held, err := os.ReadFile("../docs/ai/flows/Index.md")
	if err != nil {
		t.Fatalf("the flow index could not be read: %v", err)
	}
	return string(held)
}

// Every non-test source file of the extension, testdata excluded because a
// fixture is not source.
func eventSources(t *testing.T) []string {
	t.Helper()
	var found []string
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		held, err := filepath.Rel("..", path)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(held))
		return nil
	}
	if err := filepath.WalkDir(filepath.Join("..", "event"), walk); err != nil {
		t.Fatalf("the event extension could not be walked: %v", err)
	}
	sort.Strings(found)
	return found
}

// The four documentation obligations of the phase that shipped the wait, the
// receipt and the bounded read. Each is a sentence a page has to carry or a
// sentence it may not, and none of them is visible to a compiler, to `make api`
// or to `check-event-kernel`: a signature can be right while the page beside it
// promises something the code refuses.
//
// Every clause is a pair of patterns, one per language, because the two module
// trees are parallel by design and a check that read only the English half would
// let the Russian one drift — which is the shape P1 backlog item 8 already
// records for the exactly-once walk.
type clause struct {
	what    string
	english *regexp.Regexp
	russian *regexp.Regexp
}

func (this clause) pattern(path string) *regexp.Regexp {
	if strings.Contains(filepath.ToSlash(path), "/ru/") {
		return this.russian
	}
	return this.english
}

func missingClauses(t *testing.T, path string, clauses []clause) []string {
	t.Helper()
	written := unwrapped(t, path)
	var missing []string
	for _, wanted := range clauses {
		if !wanted.pattern(path).MatchString(written) {
			missing = append(missing, fmt.Sprintf("%s never says %s", path, wanted.what))
		}
	}
	return missing
}

// A wrapped sentence is one sentence: these pages are hard-wrapped at eighty
// columns, so a clause read off the raw bytes is a clause that happens not to
// straddle a line break. Every pattern below is written against one line.
var whitespaceRun = regexp.MustCompile(`\s+`)

func unwrapped(t *testing.T, path string) string {
	t.Helper()
	held, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return whitespaceRun.ReplaceAllString(string(held), " ")
}

func writtenPage(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("the fixture page could not be written: %v", err)
	}
	return path
}

// §INV-108. [[D-128]] names the one restatement that destroys the promise —
// "the highest position of the page" — because it is the same number and a
// different promise, and a wait's documentation is exactly where it happens by
// accident: a wait compares that number, so the page explaining a wait is under
// pressure to explain what it is, and the easy explanation is the wrong one.
var highestRestatedAsAPage = []clause{
	{
		what:    "restates Progress.Highest as a page's last or highest position",
		english: regexp.MustCompile(`(?i)` + "`?" + `Progress\.Highest` + "`?" + `[^.]{0,80}(last|highest)[^.]{0,20}position[^.]{0,20}(of|in)[^.]{0,20}page|` + "`?" + `Progress\.Highest` + "`?" + `[^.]{0,80}page'?s? (last|highest) position`),
		russian: regexp.MustCompile(`(?i)` + "`?" + `Progress\.Highest` + "`?" + `[^.]{0,80}(последн|наибольш|высш)\p{L}*\s+позици\p{L}*[^.]{0,40}страниц`),
	},
}

// The other half, so the check cannot pass on a page that says nothing at all
// about the watermark.
var highestStatedAsAWatermark = []clause{
	{
		what:    "states Progress.Highest as a completeness watermark",
		english: regexp.MustCompile(`(?i)` + "`?" + `Progress\.Highest` + "`?" + ` is a completeness watermark`),
		russian: regexp.MustCompile(`(?i)` + "`?" + `Progress\.Highest` + "`?" + ` — водяной знак полноты`),
	},
}

func TestNoProjectionGuideRestatesHighestAsThePagesLastPosition(t *testing.T) {
	for _, page := range projectionPages {
		for _, reported := range missingClauses(t, page, highestStatedAsAWatermark) {
			t.Error(reported + ", so nothing on it is being held to §INV-108")
		}
		if found := highestRestatedAsAPage[0].pattern(page).FindString(unwrapped(t, page)); found != "" {
			t.Errorf("%s restates Progress.Highest as a page's position: %q. It is the same number and a different promise ([[D-128]])", page, found)
		}
	}

	t.Run("the control: a page that restates it is reported", func(t *testing.T) {
		for _, fixture := range []struct{ name, written string }{
			{"projection.md", "A page this partition matches nothing in still advances, so `Progress.Highest` is the read page's last position rather than a claim about what was applied.\n"},
			{"ru/projection.md", "Страница ни с чем не совпала, поэтому `Progress.Highest` — это последняя позиция прочитанной страницы, а не утверждение о применённом.\n"},
		} {
			path := writtenPage(t, filepath.Base(fixture.name), fixture.written)
			if strings.Contains(fixture.name, "ru/") {
				path = writtenPage(t, "ru_projection.md", fixture.written)
			}
			pattern := highestRestatedAsAPage[0].english
			if strings.Contains(fixture.name, "ru/") {
				pattern = highestRestatedAsAPage[0].russian
			}
			if !pattern.MatchString(unwrapped(t, path)) {
				t.Fatalf("the fixture paragraph restates Highest as the page's last position and was not reported, so the arm above proves nothing: %q", fixture.written)
			}
		}
	})
}

// §UC-235. A timestamp is the one boundary a historical read may not take, and
// the reason is not obvious enough to leave implicit: `recorded_at` IS a database
// clock, which is comparable across writers and is better than an application
// clock, and it is still not an ordering. A page that stops at "there is no
// timestamp parameter" leaves the next reader to conclude the omission is an
// oversight.
var timestampIsNotABoundary = []clause{
	{
		what:    "that a recorded instant is not business time",
		english: regexp.MustCompile(`(?i)not business time`),
		russian: regexp.MustCompile(`(?i)не бизнес-врем`),
	},
	{
		what:    "that a recorded instant is not commit order",
		english: regexp.MustCompile(`(?i)not commit order`),
		russian: regexp.MustCompile(`(?i)не порядок фиксации`),
	},
	{
		what:    "which clock the recorded instant is, by naming statement_timestamp()",
		english: regexp.MustCompile("`statement_timestamp\\(\\)`"),
		russian: regexp.MustCompile("`statement_timestamp\\(\\)`"),
	},
	{
		what:    "that a time-shaped entry point could only ever be a lookup resolving to a version",
		english: regexp.MustCompile(`(?i)lookup that resolves to a version`),
		russian: regexp.MustCompile(`(?i)поиском, разрешающимся в версию`),
	},
	{
		what:    "that no example on it sorts by that instant",
		english: regexp.MustCompile(`(?i)no example here sorts by an instant`),
		russian: regexp.MustCompile(`(?i)ни один пример здесь не сортирует по моменту`),
	},
}

var eventPages = []string{"../docs/modules/en/event.md", "../docs/modules/ru/event.md"}

func TestNoEventGuideOffersATimestampBoundary(t *testing.T) {
	for _, page := range eventPages {
		for _, reported := range missingClauses(t, page, timestampIsNotABoundary) {
			t.Error(reported + ", and §UC-235 is the clause that says it must")
		}
		if sorting := regexp.MustCompile(`(?i)order by recorded_at`).FindString(unwrapped(t, page)); sorting != "" {
			t.Errorf("%s shows %q, and ordering a read by a statement timestamp is what [[D-128]] forbids by name", page, sorting)
		}
	}

	t.Run("the control: a page that omits the sentence is reported", func(t *testing.T) {
		path := writtenPage(t, "event.md", "`Repo.StateAt` takes a version. There is no timestamp parameter.\n")
		reported := missingClauses(t, path, timestampIsNotABoundary)
		if len(reported) != len(timestampIsNotABoundary) {
			t.Fatalf("a page carrying none of the five clauses was reported for %d of them, so a page that states none reads as stating some: %v", len(reported), reported)
		}
	})
}

// §INV-111, §INV-125, §INV-126. Three obligations a hand-assembled WaitSpec
// carries and three a Sequencer carries, stated TOGETHER on the projection
// pages — because a reader who meets one set and not the other concludes the
// other does not exist — and the two the receipt page carries, which are the two
// this framework structurally cannot check about an operation key.
var theWaitsThreeObligations = []clause{
	{
		what:    "that Sequence must be the sequencer the projection runs",
		english: regexp.MustCompile("(?i)`Sequence` must be the sequencer the projection runs"),
		russian: regexp.MustCompile("(?i)`Sequence` обязан быть тем секвенсором, который выполняет проекция"),
	},
	{
		what:    "that Park must be the projection's own queue",
		english: regexp.MustCompile("(?i)`Park` must be the projection's queue"),
		russian: regexp.MustCompile("(?i)`Park` обязан быть очередью этой проекции"),
	},
	{
		what:    "that Over must be the cover the rows are recorded at",
		english: regexp.MustCompile("(?i)`Over` must be the cover the rows are recorded at"),
		russian: regexp.MustCompile("(?i)`Over` обязан быть тем покрытием, на котором записаны строки"),
	},
	{
		what:    "that a Sequencer is total, pure and stable, and that none of the three is checked at run time",
		english: regexp.MustCompile(`(?i)none of the three is checked at run time`),
		russian: regexp.MustCompile(`(?i)ни одно из трёх не проверяется во время выполнения`),
	},
}

var theReceiptsTwoObligations = []clause{
	{
		what:    "that a decision must encode to the same bytes under one key",
		english: regexp.MustCompile(`(?i)must encode to the same bytes under one key`),
		russian: regexp.MustCompile(`(?i)обязано кодироваться в одни и те же байты под одним ключом`),
	},
	{
		what:    "the key-per-append recipe for a command that writes to two aggregates",
		english: regexp.MustCompile(`(?i)recipe is a key per append`),
		russian: regexp.MustCompile(`(?i)рецепт — ключ на добавление`),
	},
}

var receiptPages = []string{"../docs/modules/en/receipt.md", "../docs/modules/ru/receipt.md"}

func TestTheThreeObligationsAWaitCannotCheckAreStatedTogether(t *testing.T) {
	for _, page := range projectionPages {
		for _, reported := range missingClauses(t, page, theWaitsThreeObligations) {
			t.Error(reported + ", and a reader who meets two of the three concludes the third does not exist")
		}
	}
	for _, page := range receiptPages {
		for _, reported := range missingClauses(t, page, theReceiptsTwoObligations) {
			t.Error(reported + ", and it is one of the two things nothing at any door can check about an operation key")
		}
	}

	t.Run("the control: a page stating two of three is reported for the third", func(t *testing.T) {
		path := writtenPage(t, "projection.md",
			"A hand-written spec: `Sequence` must be the sequencer the projection runs, and `Park` must be the projection's queue if it has one.\n\n"+
				"A `Sequencer` is total, pure and stable, and none of the three is checked at run time.\n")
		reported := missingClauses(t, path, theWaitsThreeObligations)
		if len(reported) != 1 || !strings.Contains(reported[0], "Over must be the cover") {
			t.Fatalf("a page stating three of the four clauses was reported as %v, so a page stating some reads as stating all", reported)
		}
	})
}

// The example ledger is called THE REFERENCE IMPLEMENTATION on two module pages
// and in a decision record, and that label is only honest because the live suite
// ran those exact statements. This is the comparison that makes it a fact.
//
// BOTH statements and IN ORDER. The finding this pins is about the ORDER — a
// `SELECT` placed before the `INSERT … ON CONFLICT DO NOTHING` sees nothing, its
// caller decides it is first and appends, and the insert's zero arrives after the
// decision — so a check that compared only the insert would pass an example whose
// select had moved, which is the one thing it must not do.
var theClaimsTwoStatements = []string{"claimInsertStatement", "claimSelectStatement"}

func TestTheExampleLedgerIsTheOneTheLiveSuiteProved(t *testing.T) {
	proved := claimStatementsIn(t, "../event/eventpg/receipt_integration_test.go")
	published := claimStatementsIn(t, "../_examples/event-receipts/main.go")

	if len(proved) != len(theClaimsTwoStatements) {
		t.Fatalf("the live suite declares %d of the two claim statements, so there is nothing to compare against: %v", len(proved), names(proved))
	}
	for _, complaint := range claimDrift(proved, published) {
		t.Error(complaint)
	}

	t.Run("the control: an example whose two statements are swapped is reported", func(t *testing.T) {
		swapped := []namedStatement{published[1], published[0]}
		if len(claimDrift(proved, swapped)) == 0 {
			t.Fatal("the two statements were exchanged and the comparison was still satisfied, so it reads a set rather than a sequence and the ordering half of P-17 is unpinned")
		}
	})
}

type namedStatement struct {
	name  string
	value string
}

func names(held []namedStatement) []string {
	found := make([]string, 0, len(held))
	for _, one := range held {
		found = append(found, one.name)
	}
	return found
}

// In source order, which is the half that matters: a const block that declares
// the select first is an implementation that issues it first.
func claimStatementsIn(t *testing.T, path string) []namedStatement {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	wanted := map[string]bool{}
	for _, name := range theClaimsTwoStatements {
		wanted[name] = true
	}
	var found []namedStatement
	for _, declaration := range parsed.Decls {
		general, isGeneral := declaration.(*ast.GenDecl)
		if !isGeneral || general.Tok != token.CONST {
			continue
		}
		for _, spec := range general.Specs {
			valued, isValued := spec.(*ast.ValueSpec)
			if !isValued || len(valued.Names) != 1 || len(valued.Values) != 1 {
				continue
			}
			if !wanted[valued.Names[0].Name] {
				continue
			}
			literal, isLiteral := valued.Values[0].(*ast.BasicLit)
			if !isLiteral || literal.Kind != token.STRING {
				continue
			}
			unquoted, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("%s declares %s as something that is not a string literal: %v", path, valued.Names[0].Name, err)
			}
			found = append(found, namedStatement{name: valued.Names[0].Name, value: unquoted})
		}
	}
	return found
}

func claimDrift(proved, published []namedStatement) []string {
	var complaints []string
	if len(published) != len(proved) {
		return []string{fmt.Sprintf("_examples/event-receipts declares %v and the live suite declares %v", names(published), names(proved))}
	}
	for index, one := range proved {
		other := published[index]
		if one.name != other.name {
			complaints = append(complaints, fmt.Sprintf("statement %d is %s in the live suite and %s in the example, so the two run their claim in a different ORDER — which is the whole of the finding this comparison exists for", index+1, one.name, other.name))
			continue
		}
		if one.value != other.value {
			complaints = append(complaints, fmt.Sprintf("%s differs between the live suite and _examples/event-receipts:\n  proved:    %q\n  published: %q", one.name, one.value, other.value))
		}
	}
	return complaints
}

const (
	theEventSourcingGuide = "../docs/usage-guides/event-sourcing.md"
	theCompiledGuide      = "../_examples/event-guide"
)

type goFence struct {
	line  int
	lines []string
}

// The two checks that read this page match backticked test names and
// file.go:Symbol citations, and neither can see a call, an argument order or a
// struct literal — which is how the adoption route came to carry four API errors
// while both were green. This one compares the page's Go with a package the build
// compiles, line for line and in order, so the compiler is what reads the guide.
func TestEveryGoFenceInTheEventSourcingGuideIsCompiled(t *testing.T) {
	fences := goFencesIn(t, theEventSourcingGuide)
	compiled := compiledGoLines(t, theCompiledGuide)

	if len(fences) == 0 {
		t.Fatalf("%s publishes no Go at all, so nothing here was compared", theEventSourcingGuide)
	}
	if len(compiled) == 0 {
		t.Fatalf("%s holds no Go, so every fence would be reported and none of them is compiled", theCompiledGuide)
	}
	for _, fence := range fences {
		if drift := fenceDrift(fence, compiled); drift != "" {
			t.Errorf("%s:%d %s", theEventSourcingGuide, fence.line, drift)
		}
	}

	for _, control := range []struct {
		what, was, now string
	}{
		{
			what: "event.Bind's arguments in the order that does not compile",
			was:  "orders, err := event.Bind(event.Open(store), Orders)",
			now:  "orders, err := event.Bind(Orders, store)",
		},
		{
			what: "an eventpg.Spec literal without the handle New refuses first",
			was:  `store, err := eventpg.New(eventpg.Spec{DB: db, Source: source, Schema: eventpg.Schema{Name: "events"}})`,
			now:  `store, err := eventpg.New(eventpg.Spec{Source: source, Schema: eventpg.Schema{Name: "events"}})`,
		},
		{
			what: "a Partition where a wait takes a Cover",
			was:  "waiting, err := projection.WaitOf(spec, whole)",
			now:  "waiting, err := projection.WaitOf(spec, projection.Whole())",
		},
	} {
		t.Run("the control: "+control.what+" is reported", func(t *testing.T) {
			edited := guideWith(t, fences, control.was, control.now)
			if fenceDrift(edited, compiled) == "" {
				t.Fatalf("the page was given %q and the comparison was still satisfied, so it is not reading the line", control.now)
			}
		})
	}
}

func TestNoDocCallsASupervisorMethodTheTypeDoesNotHave(t *testing.T) {
	declared := methodsOn(t, "../runtime", "Supervisor")
	if len(declared) == 0 {
		t.Fatal("runtime.Supervisor came back with no methods at all, so every call a doc makes would be accepted")
	}
	for _, call := range supervisorCallsIn(t, filepath.Join("..", "docs")) {
		if declared[call.method] {
			continue
		}
		t.Errorf("%s:%d writes supervisor.%s and runtime.Supervisor has %s: a supervisor takes its runners at construction, through Spec.Runners",
			call.path, call.line, call.method, strings.Join(methodNames(declared), " · "))
	}

	t.Run("the control: a page that calls one it does not have is reported", func(t *testing.T) {
		path := writtenPage(t, "supervisor.md", "```go\nsupervisor.Add(following)\nsupervisor.Start(ctx)\n```\n")
		calls := supervisorCallsIn(t, filepath.Dir(path))
		var reported []string
		for _, call := range calls {
			if !declared[call.method] {
				reported = append(reported, call.method)
			}
		}
		if len(reported) != 1 || reported[0] != "Add" {
			t.Fatalf("the fixture calls Add, which no Supervisor has, and Start, which one does, and the walk reported %v", reported)
		}
	})
}

func goFencesIn(t *testing.T, path string) []goFence {
	t.Helper()
	held, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	var fences []goFence
	var open *goFence
	for number, line := range strings.Split(string(held), "\n") {
		trimmed := strings.TrimSpace(line)
		if open == nil {
			if trimmed == "```go" {
				open = &goFence{line: number + 1}
			}
			continue
		}
		if trimmed == "```" {
			fences = append(fences, *open)
			open = nil
			continue
		}
		if code := collapsed(line); code != "" {
			open.lines = append(open.lines, code)
		}
	}
	if open != nil {
		t.Fatalf("%s opens a Go fence at line %d and never closes it", path, open.line)
	}
	return fences
}

func compiledGoLines(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("cannot read %s: %v", directory, err)
	}
	var lines []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		held, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatalf("cannot read %s: %v", entry.Name(), err)
		}
		for _, line := range strings.Split(string(held), "\n") {
			if code := collapsed(line); code != "" {
				lines = append(lines, code)
			}
		}
	}
	return lines
}

func collapsed(line string) string { return strings.Join(strings.Fields(line), " ") }

func guideWith(t *testing.T, fences []goFence, was, now string) goFence {
	t.Helper()
	for _, fence := range fences {
		for index, line := range fence.lines {
			if line != collapsed(was) {
				continue
			}
			edited := goFence{line: fence.line, lines: append([]string{}, fence.lines...)}
			edited.lines[index] = collapsed(now)
			return edited
		}
	}
	t.Fatalf("no fence of %s carries %q, so the control mutates nothing and proves nothing", theEventSourcingGuide, was)
	return goFence{}
}

func fenceDrift(fence goFence, compiled []string) string {
	longest := 0
	for start := range compiled {
		matched := 0
		for matched < len(fence.lines) && start+matched < len(compiled) && compiled[start+matched] == fence.lines[matched] {
			matched++
		}
		if matched == len(fence.lines) {
			return ""
		}
		if matched > longest {
			longest = matched
		}
	}
	if longest == 0 {
		return fmt.Sprintf("opens a Go block with %q and no line of %s reads that, so nothing compiles what the page publishes", fence.lines[0], theCompiledGuide)
	}
	return fmt.Sprintf("opens a Go block with %q whose first %d of %d lines are compiled and which then diverges at %q, so the page and %s are two different programs",
		fence.lines[0], longest, len(fence.lines), fence.lines[longest], theCompiledGuide)
}

type supervisorCall struct {
	path   string
	line   int
	method string
}

var supervisorCalled = regexp.MustCompile(`\bsupervisor\.([A-Z][A-Za-z0-9_]*)\(`)

func supervisorCallsIn(t *testing.T, root string) []supervisorCall {
	t.Helper()
	var calls []supervisorCall
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		held, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for number, line := range strings.Split(string(held), "\n") {
			for _, found := range supervisorCalled.FindAllStringSubmatch(line, -1) {
				calls = append(calls, supervisorCall{path: path, line: number + 1, method: found[1]})
			}
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("cannot walk %s: %v", root, err)
	}
	return calls
}

func methodsOn(t *testing.T, directory, receiver string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("cannot read %s: %v", directory, err)
	}
	declared := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			t.Fatalf("cannot read %s: %v", entry.Name(), err)
		}
		for _, declaration := range parsed.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			if named(function.Recv.List[0].Type) == receiver && function.Name.IsExported() {
				declared[function.Name.Name] = true
			}
		}
	}
	return declared
}

func named(expression ast.Expr) string {
	if starred, isStarred := expression.(*ast.StarExpr); isStarred {
		return named(starred.X)
	}
	if identifier, isIdentifier := expression.(*ast.Ident); isIdentifier {
		return identifier.Name
	}
	return ""
}

func methodNames(declared map[string]bool) []string {
	found := make([]string, 0, len(declared))
	for name := range declared {
		found = append(found, name)
	}
	sort.Strings(found)
	return found
}

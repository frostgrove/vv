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
// A refusal is a negation *attached to the claim*, not a negation nearby. The
// scope is the sentence up to the end of the clause the phrase sits in, the
// sentence before it in the same paragraph, and — for a list of things a
// document refuses — a heading that itself refuses. "Delivers it exactly once,
// so you do not need an inbox table" is a promise with a negation in it, and a
// window of neighbouring lines reads that as a refusal.
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
		refusing: regexp.MustCompile(`(?i)non-goal|out of scope|not in scope`),
	},
	{
		language: "Russian",
		written:  regexp.MustCompile(`[А-Яа-яЁё]`),
		promise:  regexp.MustCompile(`(?i)ровно один раз|ровно однажды|строго один раз|точно один раз|один и только один раз`),
		weaker:   regexp.MustCompile(`(?i)не менее одного раза|хотя бы один раз|как минимум один раз`),
		about:    regexp.MustCompile(`(?i)доставк|доставл|брокер|подписчик|потребител|проекц|обработчик|не менее одного раза|хотя бы один раз`),
		negation: regexp.MustCompile(`(?i)(^|[^\p{L}])(не|ни|нет|никогда|ничего|без)([^\p{L}]|$)`),
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

	promised := []string{
		"walk.md:3", "walk.md:7", "walk.md:9", "walk.md:11", "walk.md:15",
		"window.md:3", "window.md:7", "window.md:10",
		"обход.md:3", "обход.md:7",
	}
	if len(claims) != len(promised) {
		t.Fatalf("the fixture writes %d promises and %v came back", len(promised), claims)
	}
	for _, expected := range promised {
		if !endsWithOneOf(claims, expected) {
			t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing: %v", expected, claims)
		}
	}
	if endsWithOneOf(claims, "window.md:5") {
		t.Fatalf("the fixture refuses the claim in the sentence before it and %v came back, so a page that says it does not promise this is reported anyway", claims)
	}
	if read.checked["English"] != 12 || read.checked["Russian"] != 3 {
		t.Fatalf("the fixture writes twelve English uses about delivery and three Russian ones, and %d and %d were read", read.checked["English"], read.checked["Russian"])
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
	return this.refusing.MatchString(paragraph.heading) || this.negation.MatchString(paragraph.attachedTo(at))
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

func (this block) clauseTo(offset int) int {
	for index := offset; index < len(this.text); index++ {
		if strings.IndexByte(",;:", this.text[index]) >= 0 || strings.HasPrefix(this.text[index:], "—") || endsASentence(this.text, index) {
			return index
		}
	}
	return len(this.text)
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
		},
		"обход.md": {
			"# Обход журнала",
			"",
			"Проектор доставляет каждое событие ровно один раз.",
			"",
			"Доставка гарантируется хотя бы один раз, и ни один текст здесь не назовёт её «ровно один раз».",
			"",
			"Каждый подписчик получает событие один и только один раз.",
		},
	}
	for name, lines := range written {
		if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
			t.Fatalf("cannot write the fixture: %v", err)
		}
	}
	return root
}

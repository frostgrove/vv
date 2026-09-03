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
	if checked == 0 {
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
			continue
		}
		checked++
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
				if match == nil || match[2] == "" {
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

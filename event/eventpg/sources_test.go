package eventpg

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two checks below ask what a value *is* rather than what it is called, and
// a name is all go/ast can answer: a parameter called `log` is not the logging
// package and a `[]string` bound to a statement is not spelled differently from
// a `string`. So the sources are type-checked with go/types and the standard
// library's source importer, which adds no dependency.
type typedPackage struct {
	directory string
	fileset   *token.FileSet
	files     []*ast.File
	info      *types.Info
	pkg       *types.Package
}

func typeCheck(t *testing.T, directory string) typedPackage {
	t.Helper()
	fileset := token.NewFileSet()
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
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	config := types.Config{Importer: importer.ForCompiler(fileset, "source", nil)}
	pkg, err := config.Check(directory, fileset, files, info)
	if err != nil {
		t.Fatalf("%s does not type-check, so nothing about it was checked: %v", directory, err)
	}
	return typedPackage{directory: directory, fileset: fileset, files: files, info: info, pkg: pkg}
}

func (this typedPackage) at(node ast.Node) string {
	return this.fileset.Position(node.Pos()).String()
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

func writeFixture(t *testing.T, written string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(written), 0o600); err != nil {
		t.Fatalf("cannot write the fixture: %v", err)
	}
	return root
}

func declaringName(declaration ast.Decl) string {
	if found, isFunction := declaration.(*ast.FuncDecl); isFunction {
		return found.Name.Name
	}
	return "the package"
}

// Which package an identifier resolves to, or "" when it is a value of the
// caller's own. A parameter named `log` answers "" and the logging package
// answers "log", which is the whole difference the arms below rest on.
func qualifierOf(typed typedPackage, expression ast.Expr) (string, string) {
	selector, isSelector := expression.(*ast.SelectorExpr)
	if !isSelector {
		return "", ""
	}
	name, isName := selector.X.(*ast.Ident)
	if !isName {
		return "", ""
	}
	imported, isPackage := typed.info.Uses[name].(*types.PkgName)
	if !isPackage {
		return "", ""
	}
	return imported.Imported().Path(), selector.Sel.Name
}

func TestTheStoreStartsNothingAndReadsNoEnvironment(t *testing.T) {
	t.Run("nothing this package holds runs, logs or looks around before it is called", func(t *testing.T) {
		typed := typeCheck(t, ".")
		if len(typed.files) < 4 {
			t.Fatalf("%d files of the store were read, so this walked the wrong directory", len(typed.files))
		}
		for _, complaint := range lifecycleComplaints(typed) {
			t.Error(complaint)
		}
	})

	t.Run("each arm reports the shape it is written for and the idiom is not", func(t *testing.T) {
		reported := strings.Join(lifecycleComplaints(typeCheck(t, writeFixture(t, lifecycleFixture))), "\n")

		for _, expected := range []string{
			"init", "startsAGoroutine", "writesToTheProcessLogger", "printsToStandardOutput",
			"readsTheEnvironment", "registry", "buffered", "channel", "held",
		} {
			if !strings.Contains(reported, expected) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", expected, reported)
			}
		}
		for _, permitted := range []string{"sentinel", "permittedRecorderPrint", "permittedLocalState"} {
			if strings.Contains(reported, permitted) {
				t.Fatalf("the fixture's %s was reported, and a sentinel, a value of the caller's called log and local state are the idiom this repository is written in:\n%s", permitted, reported)
			}
		}
	})
}

func lifecycleComplaints(typed typedPackage) []string {
	var complaints []string
	for _, file := range typed.files {
		for _, declaration := range file.Decls {
			within := declaringName(declaration)
			if found, isFunction := declaration.(*ast.FuncDecl); isFunction && found.Recv == nil && found.Name.Name == "init" {
				complaints = append(complaints, typed.at(found)+" declares init, so importing this store runs it before the composition root exists")
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch found := node.(type) {
				case *ast.GoStmt:
					complaints = append(complaints, typed.at(found)+" in "+within+
						" starts a goroutine, and anything continuous here is a runtime.Runner the composition root chose")
				case *ast.SelectorExpr:
					if what := ambientReach(qualifierOf(typed, found)); what != "" {
						complaints = append(complaints, typed.at(found)+" in "+within+" "+what)
					}
				}
				return true
			})
		}
	}
	return append(complaints, packageLevelState(typed)...)
}

func ambientReach(path, name string) string {
	switch {
	case path == "log" || path == "log/slog":
		return "writes to a process-wide logger, and port.Logger(ctx) is the seam"
	case path == "fmt" && (strings.HasPrefix(name, "Print") || strings.HasPrefix(name, "Fprint")):
		return "prints where nobody asked it to"
	case path == "os" && (name == "Getenv" || name == "LookupEnv" || name == "Environ" || name == "Setenv"):
		return "reads the environment, and what a store is configured with is its Spec"
	}
	return ""
}

func packageLevelState(typed typedPackage) []string {
	var complaints []string
	scope := typed.pkg.Scope()
	for _, name := range scope.Names() {
		held, isVariable := scope.Lookup(name).(*types.Var)
		if !isVariable {
			continue
		}
		var kind string
		switch held.Type().Underlying().(type) {
		case *types.Map:
			kind = "a map"
		case *types.Slice:
			kind = "a slice"
		case *types.Chan:
			kind = "a channel"
		case *types.Pointer:
			kind = "a pointer"
		case *types.Array:
			kind = "an array"
		}
		if kind != "" {
			complaints = append(complaints, typed.fileset.Position(held.Pos()).String()+" declares "+name+" as "+kind+
				" at package level, and that is state every store value and every request goroutine shares")
		}
	}
	return complaints
}

// §1's promise that any database/sql driver for PostgreSQL serves this store is
// kept by construction and checked here rather than asserted: every value bound
// to a statement is one of the six types driver.DefaultParameterConverter and
// every driver's own converter agree about. A Go slice bound as an array, a
// named type spelled over int64 and an untyped constant are each a value one
// driver takes and another refuses.
var boundParameterTypes = map[string]bool{
	"string": true, "int32": true, "int64": true, "bool": true, "[]byte": true, "time.Time": true,
}

func TestEveryBoundParameterIsATypeEveryDatabaseSQLDriverAccepts(t *testing.T) {
	t.Run("every parameter this store binds is one of the six", func(t *testing.T) {
		typed := typeCheck(t, ".")
		if len(typed.files) < 4 {
			t.Fatalf("%d files of the store were read, so this walked the wrong directory", len(typed.files))
		}
		for _, complaint := range boundParameterComplaints(typed) {
			t.Error(complaint)
		}
	})

	t.Run("a parameter no driver agrees about is reported and the six are not", func(t *testing.T) {
		reported := strings.Join(boundParameterComplaints(typeCheck(t, writeFixture(t, boundParameterFixture))), "\n")

		for _, expected := range []string{
			"boundAsAStringSlice", "boundAsAnInt", "boundAsANamedVersion",
			"boundAsAnUntypedConstant", "boundThroughAnUnresolvableSpread", "boundAsWhateverWasHandedIn",
		} {
			if !strings.Contains(reported, expected) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", expected, reported)
			}
		}
		for _, permitted := range []string{
			"boundAsText", "boundAsAnInt32", "boundAsAnInt64", "boundAsABool",
			"boundAsBytes", "boundAsAnInstant", "boundThroughAResolvedSpread",
		} {
			if strings.Contains(reported, permitted) {
				t.Fatalf("the fixture's %s was reported, and those six are what every driver converts:\n%s", permitted, reported)
			}
		}
	})
}

func boundParameterComplaints(typed typedPackage) []string {
	var complaints []string
	for _, file := range typed.files {
		for _, declaration := range file.Decls {
			within := declaringName(declaration)
			enclosing, _ := declaration.(*ast.FuncDecl)
			ast.Inspect(declaration, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall || !bindsParameters(call) || len(call.Args) < 3 {
					return true
				}
				arguments := call.Args[2:]
				if call.Ellipsis != token.NoPos {
					spread := arguments[len(arguments)-1]
					arguments = arguments[:len(arguments)-1]
					elements, resolved := spreadElements(typed, enclosing, spread)
					if !resolved {
						complaints = append(complaints, typed.at(spread)+" in "+within+
							": the parameters are spread from a value this check cannot follow, so what the statement binds is unread")
						return true
					}
					arguments = append(arguments, elements...)
				}
				for _, argument := range arguments {
					held := typed.info.TypeOf(argument)
					if held == nil {
						complaints = append(complaints, typed.at(argument)+" in "+within+": the type of a bound parameter could not be read")
						continue
					}
					if spelled := types.TypeString(held, nil); !boundParameterTypes[spelled] {
						complaints = append(complaints, typed.at(argument)+" in "+within+": binds a value of type "+spelled+
							", and a driver other than this one converts it differently or refuses it")
					}
				}
				return true
			})
		}
	}
	return complaints
}

func bindsParameters(call *ast.CallExpr) bool {
	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	if !isSelector {
		return false
	}
	switch selector.Sel.Name {
	case "ExecContext", "QueryContext", "QueryRowContext":
		return true
	}
	return false
}

// What a `parameters...` was built out of, inside the function that spreads it.
// A shape this cannot follow is reported rather than passed: an unread binding
// is exactly the one that carries the type nobody meant to bind.
func spreadElements(typed typedPackage, within *ast.FuncDecl, spread ast.Expr) ([]ast.Expr, bool) {
	name, isName := spread.(*ast.Ident)
	if !isName || within == nil {
		return nil, false
	}
	object := typed.info.Uses[name]
	if object == nil {
		object = typed.info.Defs[name]
	}
	if object == nil {
		return nil, false
	}
	names := func(expression ast.Expr) bool {
		held, isName := expression.(*ast.Ident)
		return isName && (typed.info.Uses[held] == object || typed.info.Defs[held] == object)
	}

	var elements []ast.Expr
	resolved := true
	ast.Inspect(within, func(node ast.Node) bool {
		assignment, isAssignment := node.(*ast.AssignStmt)
		if !isAssignment {
			if declared, isDeclaration := node.(*ast.ValueSpec); isDeclaration {
				for index, held := range declared.Names {
					if typed.info.Defs[held] != object || index >= len(declared.Values) {
						continue
					}
					if literal, isLiteral := declared.Values[index].(*ast.CompositeLit); isLiteral {
						elements = append(elements, literal.Elts...)
					} else {
						resolved = false
					}
				}
			}
			return true
		}
		for index, left := range assignment.Lhs {
			if indexed, isIndexed := left.(*ast.IndexExpr); isIndexed && names(indexed.X) {
				if index < len(assignment.Rhs) {
					elements = append(elements, assignment.Rhs[index])
				} else {
					resolved = false
				}
				continue
			}
			if !names(left) {
				continue
			}
			if index >= len(assignment.Rhs) {
				resolved = false
				continue
			}
			switch built := assignment.Rhs[index].(type) {
			case *ast.CompositeLit:
				elements = append(elements, built.Elts...)
			case *ast.CallExpr:
				callee, isName := built.Fun.(*ast.Ident)
				switch {
				case isName && callee.Name == "make":
				case isName && callee.Name == "append" && built.Ellipsis == token.NoPos:
					elements = append(elements, built.Args[1:]...)
				default:
					resolved = false
				}
			default:
				resolved = false
			}
		}
		return true
	})
	return elements, resolved
}

const lifecycleFixture = `package fixture

import (
	"errors"
	"fmt"
	"log"
	"os"
)

var sentinel = errors.New("fixture: a sentinel")

var registry map[string]int
var buffered []byte
var channel chan int
var held *int

const frozen = 1

func init() { _ = frozen }

func startsAGoroutine() {
	go func() { _ = frozen }()
}

func writesToTheProcessLogger() { log.Printf("anything") }

func printsToStandardOutput() { fmt.Println("anything") }

func readsTheEnvironment() string { return os.Getenv("ANYTHING") }

type recorder struct{}

func (this recorder) Print(values ...any) {}

func permittedRecorderPrint(log recorder) { log.Print("anything") }

func permittedLocalState() int {
	counted := map[string]int{}
	counted["a"]++
	return counted["a"]
}
`

const boundParameterFixture = `package fixture

import (
	"context"
	"database/sql"
	"time"
)

type version int64

type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func boundAsText(ctx context.Context, on executor, value string) {
	on.ExecContext(ctx, "SELECT $1", value)
}

func boundAsAnInt32(ctx context.Context, on executor, value int32) {
	on.ExecContext(ctx, "SELECT $1", value)
}

func boundAsAnInt64(ctx context.Context, on executor, value int64) {
	on.ExecContext(ctx, "SELECT $1", value)
}

func boundAsABool(ctx context.Context, on executor, value bool) {
	on.ExecContext(ctx, "SELECT $1", value)
}

func boundAsBytes(ctx context.Context, on executor, value []byte) {
	on.QueryContext(ctx, "SELECT $1", value)
}

func boundAsAnInstant(ctx context.Context, on executor, value time.Time) {
	on.ExecContext(ctx, "SELECT $1", value)
}

func boundThroughAResolvedSpread(ctx context.Context, on executor, name string, count int64) {
	parameters := make([]any, 0, 2)
	parameters = append(parameters, name)
	parameters = append(parameters, count)
	on.ExecContext(ctx, "SELECT $1, $2", parameters...)
}

func boundAsAStringSlice(ctx context.Context, on executor, values []string) {
	on.ExecContext(ctx, "SELECT $1", values)
}

func boundAsAnInt(ctx context.Context, on executor, value int) {
	on.ExecContext(ctx, "SELECT $1", value)
}

func boundAsANamedVersion(ctx context.Context, on executor, value version) {
	on.ExecContext(ctx, "SELECT $1", value)
}

func boundAsAnUntypedConstant(ctx context.Context, on executor) {
	on.ExecContext(ctx, "SELECT $1", 1)
}

func boundAsWhateverWasHandedIn(ctx context.Context, on executor, value any) {
	on.ExecContext(ctx, "SELECT $1", value)
}

func boundThroughAnUnresolvableSpread(ctx context.Context, on executor, given []any) {
	parameters := given
	on.ExecContext(ctx, "SELECT $1", parameters...)
}
`

// The four rules below are the ones a test suite is structurally unable to see,
// because what they forbid is a call that would work: an append on the pool is
// three executions of one statement and every one of them succeeds; a
// transaction the store opened commits; a version read into Go compares equal.
// Each carries a fixture in this file that it must report, so none of them can
// pass by walking nothing.

const databaseHandle = "*database/sql.DB"

func TestNoStatementIsIssuedOnTheDatabaseHandle(t *testing.T) {
	t.Run("every statement this store issues runs on a transaction or a connection", func(t *testing.T) {
		typed := typeCheck(t, ".")
		if len(typed.files) < 4 {
			t.Fatalf("%d files of the store were read, so this walked the wrong directory", len(typed.files))
		}
		for _, complaint := range databaseHandleComplaints(typed) {
			t.Error(complaint)
		}
	})

	t.Run("a statement on the pool is reported and one on either of the two is not", func(t *testing.T) {
		reported := strings.Join(databaseHandleComplaints(typeCheck(t, writeFixture(t, databaseHandleFixture))), "\n")

		for _, expected := range []string{"execsOnThePool", "queriesOnThePool", "rowsOnThePool"} {
			if !strings.Contains(reported, expected) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", expected, reported)
			}
		}
		for _, permitted := range []string{"permittedOnAConnection", "permittedOnATransaction", "permittedOnAnExecutor"} {
			if strings.Contains(reported, permitted) {
				t.Fatalf("the fixture's %s was reported, and those two are the values that execute a statement exactly once:\n%s", permitted, reported)
			}
		}
	})
}

func databaseHandleComplaints(typed typedPackage) []string {
	var complaints []string
	for _, file := range typed.files {
		for _, declaration := range file.Decls {
			within := declaringName(declaration)
			ast.Inspect(declaration, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall || !bindsParameters(call) {
					return true
				}
				receiver := typed.info.TypeOf(call.Fun.(*ast.SelectorExpr).X)
				if receiver == nil {
					complaints = append(complaints, typed.at(call)+" in "+within+": what this statement is issued on could not be read")
					return true
				}
				if types.TypeString(receiver, nil) == databaseHandle {
					complaints = append(complaints, typed.at(call)+" in "+within+
						" issues a statement on the pool, which retries a bad connection on three of them and would run one append three times")
				}
				return true
			})
		}
	}
	return complaints
}

const migrationFile = "migration.go"

func TestNoDoorOpensCommitsOrRollsBackAnything(t *testing.T) {
	t.Run("nothing outside the migration opens, commits or rolls back", func(t *testing.T) {
		typed := typeCheck(t, ".")
		found := false
		for _, source := range sourcesIn(t, ".") {
			found = found || filepath.Base(source) == migrationFile
		}
		if !found {
			t.Fatalf("%s is not in this package, and it is the one file this check excludes by name: an exclusion for a file nobody wrote is a blanket exemption", migrationFile)
		}
		for _, complaint := range transactionControlComplaints(typed) {
			t.Error(complaint)
		}
	})

	t.Run("each arm reports the shape it is written for and plpgsql's own block is not", func(t *testing.T) {
		reported := strings.Join(transactionControlComplaints(typeCheck(t, writeFixture(t, transactionControlFixture))), "\n")

		for _, expected := range []string{
			"opensATransaction", "commits", "rollsBack", "beginsThroughABeginner",
			"setsTheIsolationLevel", "takesASavepoint",
		} {
			if !strings.Contains(reported, expected) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", expected, reported)
			}
		}
		for _, permitted := range []string{"permittedPlpgsqlBlock", "permittedCommittedWord"} {
			if strings.Contains(reported, permitted) {
				t.Fatalf("the fixture's %s was reported, and a plpgsql block and an English word are not transaction control:\n%s", permitted, reported)
			}
		}
	})

	t.Run("the exclusion is by file name and by nothing else", func(t *testing.T) {
		excluded := typeCheck(t, writeFixtureAs(t, migrationFile, transactionControlFixture))
		if complaints := transactionControlComplaints(excluded); len(complaints) != 0 {
			t.Fatalf("the same source in %s was reported, so the migration is not excluded:\n%s", migrationFile, strings.Join(complaints, "\n"))
		}
		beside := typeCheck(t, writeFixtureAs(t, "beside.go", transactionControlFixture))
		if complaints := transactionControlComplaints(beside); len(complaints) == 0 {
			t.Fatal("the same source under another name was not reported, so the exclusion covers every file rather than one")
		}
	})
}

// The keywords, not the calls: a statement text is how a store would set an
// isolation level or take a savepoint without naming a Go method at all. BEGIN
// is deliberately absent — it opens a plpgsql block as well as a transaction,
// and this package writes two of those into the schema.
var transactionKeywords = []string{"SET TRANSACTION", "START TRANSACTION", "COMMIT", "ROLLBACK", "SAVEPOINT"}

func transactionControlComplaints(typed typedPackage) []string {
	var complaints []string
	for _, file := range typed.files {
		if filepath.Base(typed.fileset.Position(file.Pos()).Filename) == migrationFile {
			continue
		}
		for _, declaration := range file.Decls {
			within := declaringName(declaration)
			ast.Inspect(declaration, func(node ast.Node) bool {
				switch found := node.(type) {
				case *ast.CallExpr:
					if selector, is := found.Fun.(*ast.SelectorExpr); is && opensOrResolves(selector.Sel.Name) {
						complaints = append(complaints, typed.at(found)+" in "+within+" calls "+selector.Sel.Name+
							", and the transaction is the caller's throughout")
					}
				case *ast.BasicLit:
					for _, keyword := range transactionKeywords {
						if found.Kind == token.STRING && namesWord(found.Value, keyword) {
							complaints = append(complaints, typed.at(found)+" in "+within+" writes "+keyword+
								" into a statement, and this store selects no level and resolves nothing")
						}
					}
				}
				return true
			})
		}
	}
	return complaints
}

func opensOrResolves(name string) bool {
	switch name {
	case "Begin", "BeginTx", "Commit", "Rollback":
		return true
	}
	return false
}

func TestNoStatementReadsTheStreamVersionIntoGo(t *testing.T) {
	t.Run("no query this store issues names the streams table", func(t *testing.T) {
		typed := typeCheck(t, ".")
		if len(typed.files) < 4 {
			t.Fatalf("%d files of the store were read, so this walked the wrong directory", len(typed.files))
		}
		for _, complaint := range streamReadComplaints(typed) {
			t.Error(complaint)
		}
	})

	t.Run("a query over the stream row is reported and one that writes it is not", func(t *testing.T) {
		reported := strings.Join(streamReadComplaints(typeCheck(t, writeFixture(t, streamReadFixture))), "\n")

		for _, expected := range []string{"readsTheStreamVersion", "readsThroughAHelper", "readsWhateverItWasHandedIn"} {
			if !strings.Contains(reported, expected) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", expected, reported)
			}
		}
		for _, permitted := range []string{"permittedAdmission", "permittedEventRead", "permittedQuotedRead"} {
			if strings.Contains(reported, permitted) {
				t.Fatalf("the fixture's %s was reported, and admission is a conditional update that never reads a version into Go:\n%s", permitted, reported)
			}
		}
	})
}

// Admission is a conditional row update PostgreSQL evaluates against a row it
// has locked, and the whole difference between that and a read-then-write is
// that nothing this store issues brings a stream's version back into Go. The
// only statement that names the streams table is the append, and an Exec answers
// a row count.
func streamReadComplaints(typed typedPackage) []string {
	var complaints []string
	for _, file := range typed.files {
		for _, declaration := range file.Decls {
			within := declaringName(declaration)
			enclosing, _ := declaration.(*ast.FuncDecl)
			ast.Inspect(declaration, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall || !readsRows(call) || len(call.Args) < 2 {
					return true
				}
				text, readable := statementText(typed, enclosing, call.Args[1])
				if !readable {
					complaints = append(complaints, typed.at(call)+" in "+within+
						": what this query reads could not be read by this check, so what it brings into Go is unknown")
					return true
				}
				if namesWord(text, streamsTable) {
					complaints = append(complaints, typed.at(call)+" in "+within+
						" reads the "+streamsTable+" table into Go, and admission is a conditional update rather than a read and a decision")
				}
				return true
			})
		}
	}
	return complaints
}

func readsRows(call *ast.CallExpr) bool {
	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	if !isSelector {
		return false
	}
	switch selector.Sel.Name {
	case "QueryContext", "QueryRowContext":
		return true
	}
	return false
}

// The literal halves of a statement: what it is written out of, through
// concatenation, through a string constant, through one hop into a function of
// this package that builds part of it, and — for a helper that issues a text its
// caller chose — through the arguments every call site passes it. A variable
// contributes nothing, and an expression that contributes nothing at all is
// unreadable rather than empty: an unread statement is exactly the one carrying
// the table nobody meant to read.
func statementText(typed typedPackage, within *ast.FuncDecl, expression ast.Expr) (string, bool) {
	var out strings.Builder
	collectText(typed, within, expression, &out, 1)
	return out.String(), out.Len() > 0
}

func collectText(typed typedPackage, within *ast.FuncDecl, expression ast.Expr, out *strings.Builder, hops int) {
	switch found := expression.(type) {
	case *ast.BasicLit:
		if found.Kind == token.STRING {
			out.WriteString(" " + found.Value)
		}
	case *ast.ParenExpr:
		collectText(typed, within, found.X, out, hops)
	case *ast.BinaryExpr:
		collectText(typed, within, found.X, out, hops)
		collectText(typed, within, found.Y, out, hops)
	case *ast.SelectorExpr:
		if constant := typed.info.Types[expression]; constant.Value != nil {
			out.WriteString(" " + constant.Value.String())
		}
	case *ast.Ident:
		if constant := typed.info.Types[expression]; constant.Value != nil {
			out.WriteString(" " + constant.Value.String())
			return
		}
		if hops <= 0 {
			return
		}
		for _, passed := range argumentsFor(typed, within, found) {
			collectText(typed, nil, passed, out, hops-1)
		}
	case *ast.CallExpr:
		if hops <= 0 {
			return
		}
		if body := declarationOf(typed, found.Fun); body != nil {
			ast.Inspect(body, func(node ast.Node) bool {
				if literal, is := node.(*ast.BasicLit); is && literal.Kind == token.STRING {
					out.WriteString(" " + literal.Value)
				}
				return true
			})
		}
	}
}

// What every call site of this function passes for this parameter. A helper that
// issues a statement its caller chose is read through its callers rather than
// reported as unreadable, and a parameter nobody in this package fills answers
// nothing, which is still unreadable.
func argumentsFor(typed typedPackage, within *ast.FuncDecl, name *ast.Ident) []ast.Expr {
	if within == nil {
		return nil
	}
	object := typed.info.Uses[name]
	if object == nil {
		return nil
	}
	index, position := -1, 0
	for _, parameter := range within.Type.Params.List {
		for _, declared := range parameter.Names {
			if typed.info.Defs[declared] == object {
				index = position
			}
			position++
		}
	}
	if index < 0 {
		return nil
	}
	function := typed.info.Defs[within.Name]
	var passed []ast.Expr
	for _, file := range typed.files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall || len(call.Args) <= index {
				return true
			}
			if callee, isName := call.Fun.(*ast.Ident); isName && typed.info.Uses[callee] == function {
				passed = append(passed, call.Args[index])
			}
			return true
		})
	}
	return passed
}

func declarationOf(typed typedPackage, callee ast.Expr) *ast.BlockStmt {
	name, isName := callee.(*ast.Ident)
	if !isName {
		return nil
	}
	object := typed.info.Uses[name]
	if object == nil {
		return nil
	}
	for _, file := range typed.files {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if isFunction && typed.info.Defs[function.Name] == object {
				return function.Body
			}
		}
	}
	return nil
}

func namesWord(text, word string) bool {
	for index := 0; index+len(word) <= len(text); index++ {
		if text[index:index+len(word)] != word {
			continue
		}
		if !identifierPart(neighbour(text, index-1)) && !identifierPart(neighbour(text, index+len(word))) {
			return true
		}
	}
	return false
}

func TestNoNotWrittenBranchIsUnguarded(t *testing.T) {
	t.Run("every answer of NotWritten this store gives is a branch that proved it", func(t *testing.T) {
		guarded, complaints := notWrittenBranches(typeCheck(t, "."))
		if guarded < 3 {
			t.Fatalf("this check found %d guarded answers of event.NotWritten in the package, and a rule that selects it on proof has several, so it walked the wrong thing", guarded)
		}
		for _, complaint := range complaints {
			t.Error(complaint)
		}
	})

	t.Run("a default, an else and a tail are reported and a guarded branch is not", func(t *testing.T) {
		_, found := notWrittenBranches(typeCheck(t, writeFixture(t, notWrittenFixture)))
		reported := strings.Join(found, "\n")

		for _, expected := range []string{"answersFromADefaultBranch", "answersFromAnElse", "answersAtTheTail"} {
			if !strings.Contains(reported, expected) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", expected, reported)
			}
		}
		for _, permitted := range []string{"permittedGuardedCase", "permittedGuardedIf", "permittedUnconfirmedTail"} {
			if strings.Contains(reported, permitted) {
				t.Fatalf("the fixture's %s was reported, and a proof is exactly what a guarded branch is:\n%s", permitted, reported)
			}
		}
	})
}

// NotWritten is the strongest claim the store makes, so every answer of it has
// to be a branch that proved something: a case of a switch or the body of an if.
// A default clause, an else and a function's own tail are the three shapes that
// answer it about every failure nobody thought of.
func notWrittenBranches(typed typedPackage) (int, []string) {
	guarded := 0
	var complaints []string
	for _, file := range typed.files {
		for _, declaration := range file.Decls {
			within := declaringName(declaration)
			var stack []ast.Node
			ast.Inspect(declaration, func(node ast.Node) bool {
				if node == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				if returned, is := node.(*ast.ReturnStmt); is && answersNotWritten(returned) {
					if provenBy(stack) {
						guarded++
					} else {
						complaints = append(complaints, typed.at(returned)+" in "+within+
							" answers event.NotWritten from a branch that proved nothing, and it is the one claim a caller cannot recover from")
					}
				}
				stack = append(stack, node)
				return true
			})
		}
	}
	return guarded, complaints
}

func answersNotWritten(returned *ast.ReturnStmt) bool {
	for _, result := range returned.Results {
		selector, isSelector := result.(*ast.SelectorExpr)
		if !isSelector || selector.Sel.Name != "NotWritten" {
			continue
		}
		if name, is := selector.X.(*ast.Ident); is && name.Name == "event" {
			return true
		}
	}
	return false
}

func provenBy(stack []ast.Node) bool {
	for index := len(stack) - 1; index >= 0; index-- {
		switch found := stack[index].(type) {
		case *ast.CaseClause:
			return len(found.List) > 0
		case *ast.BlockStmt:
			if index > 0 {
				if enclosing, is := stack[index-1].(*ast.IfStmt); is {
					return enclosing.Body == found
				}
			}
		case *ast.FuncDecl, *ast.FuncLit:
			return false
		}
	}
	return false
}

const databaseHandleFixture = `package fixture

import (
	"context"
	"database/sql"
)

type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func execsOnThePool(ctx context.Context, on *sql.DB) { on.ExecContext(ctx, "SELECT 1") }

func queriesOnThePool(ctx context.Context, on *sql.DB) { on.QueryContext(ctx, "SELECT 1") }

func rowsOnThePool(ctx context.Context, on *sql.DB) { on.QueryRowContext(ctx, "SELECT 1") }

func permittedOnAConnection(ctx context.Context, on *sql.Conn) { on.ExecContext(ctx, "SELECT 1") }

func permittedOnATransaction(ctx context.Context, on *sql.Tx) { on.ExecContext(ctx, "SELECT 1") }

func permittedOnAnExecutor(ctx context.Context, on executor) { on.ExecContext(ctx, "SELECT 1") }
`

const transactionControlFixture = `package fixture

import (
	"context"
	"database/sql"
)

func opensATransaction(ctx context.Context, on *sql.DB) { on.BeginTx(ctx, nil) }

func commits(on *sql.Tx) { on.Commit() }

func rollsBack(on *sql.Tx) { on.Rollback() }

type beginner interface{ Begin(ctx context.Context) (any, error) }

func beginsThroughABeginner(ctx context.Context, on beginner) { on.Begin(ctx) }

func setsTheIsolationLevel(ctx context.Context, on *sql.Conn) {
	on.ExecContext(ctx, "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
}

func takesASavepoint(ctx context.Context, on *sql.Conn) {
	on.ExecContext(ctx, "SAVEPOINT fixture_1")
}

func permittedPlpgsqlBlock() string {
	return "CREATE FUNCTION f() RETURNS trigger AS $$ BEGIN RETURN NULL; END $$"
}

func permittedCommittedWord() string { return "the committed version of this stream" }
`

const streamReadFixture = `package fixture

import (
	"context"
	"database/sql"
)

func readsTheStreamVersion(ctx context.Context, on *sql.Conn) {
	on.QueryRowContext(ctx, "SELECT version FROM app.streams WHERE family = $1 AND key = $2")
}

func streamsQuery() string { return "SELECT version FROM app.streams" }

func readsThroughAHelper(ctx context.Context, on *sql.Conn) {
	on.QueryContext(ctx, streamsQuery())
}

func readsWhateverItWasHandedIn(ctx context.Context, on *sql.Conn, given string) {
	on.QueryContext(ctx, given)
}

func permittedAdmission(ctx context.Context, on *sql.Conn) {
	on.ExecContext(ctx, "INSERT INTO app.streams AS s (family, key, version) VALUES ($1, $2, $3) ON CONFLICT (family, key) DO UPDATE SET version = s.version + 1")
}

func permittedEventRead(ctx context.Context, on *sql.Conn) {
	on.QueryContext(ctx, "SELECT position, version FROM app.events WHERE family = $1")
}

func quoted(name string) string { return name }

func permittedQuotedRead(ctx context.Context, on *sql.Conn, schema string) {
	on.QueryContext(ctx, "SELECT position FROM "+quoted(schema)+".events WHERE position > $1")
}
`

const notWrittenFixture = `package fixture

type outcome uint8

var event = struct {
	NotWritten  outcome
	Unconfirmed outcome
}{NotWritten: 1, Unconfirmed: 2}

func answersFromADefaultBranch(issued bool) outcome {
	switch {
	case issued:
		return event.Unconfirmed
	default:
		return event.NotWritten
	}
}

func answersFromAnElse(issued bool) outcome {
	if issued {
		return event.Unconfirmed
	} else {
		return event.NotWritten
	}
}

func answersAtTheTail(issued bool) outcome {
	if !issued {
		return event.Unconfirmed
	}
	return event.NotWritten
}

func permittedGuardedCase(issued bool) outcome {
	switch {
	case !issued:
		return event.NotWritten
	}
	return event.Unconfirmed
}

func permittedGuardedIf(issued bool) outcome {
	if !issued {
		return event.NotWritten
	}
	return event.Unconfirmed
}

func permittedUnconfirmedTail(issued bool) outcome {
	if !issued {
		return event.NotWritten
	}
	return event.Unconfirmed
}
`

func writeFixtureAs(t *testing.T, name, written string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte(written), 0o600); err != nil {
		t.Fatalf("cannot write the fixture: %v", err)
	}
	return root
}

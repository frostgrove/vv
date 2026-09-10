package scripts

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type frameworkLogExpectation struct {
	file          string
	message       string
	method        string
	loggerContext string
	callContext   string
}

type frameworkLogCall struct {
	frameworkLogExpectation
	position token.Position
}

func TestEveryContextBearingFrameworkLogPassesItsContext(t *testing.T) {
	expected := []frameworkLogExpectation{
		{file: "crud/http/crudnet/middleware.go", message: "crudnet: panic while serving a request", method: "ErrorContext", loggerContext: "r.Context()", callContext: "r.Context()"},
		{file: "crud/http/crudnet/options.go", message: "crudnet: encoding the response", method: "ErrorContext", loggerContext: "ctx", callContext: "ctx"},
		{file: "crud/http/crudgin/middleware.go", message: "crudgin: panic while serving a request", method: "ErrorContext", loggerContext: "c.Request.Context()", callContext: "c.Request.Context()"},
		{file: "crud/http/crudgin/options.go", message: "crudgin: encoding the response", method: "ErrorContext", loggerContext: "c.Request.Context()", callContext: "c.Request.Context()"},
		{file: "crud/http/crudfiber/middleware.go", message: "crudfiber: panic while serving a request", method: "ErrorContext", loggerContext: "c.Context()", callContext: "c.Context()"},
		{file: "crud/http/crudfiber/options.go", message: "crudfiber: encoding the response", method: "ErrorContext", loggerContext: "c.Context()", callContext: "c.Context()"},
		{file: "crud/rpc/crudgrpc/status.go", message: "crudgrpc: attaching the error details", method: "ErrorContext", loggerContext: "ctx", callContext: "ctx"},
		{file: "auth/http/authhttp/authhttp.go", message: "authhttp: encoding the refusal", method: "ErrorContext", loggerContext: "ctx", callContext: "ctx"},
		{file: "auth/http/authhttp/authhttp.go", message: "authhttp: writing the refusal", method: "ErrorContext", loggerContext: "ctx", callContext: "ctx"},
		{file: "jobs/classifier.go", message: "jobs: handler panicked", method: "ErrorContext", loggerContext: "ctx", callContext: "ctx"},
		{file: "runtime/supervisor.go", message: "a supervised runner stopped on its own", method: "ErrorContext", callContext: "ctx"},
	}

	found := frameworkContextLogCalls(t)
	if len(found) != len(expected) {
		t.Fatalf("found %d context-bearing framework log sites, want exactly %d: %v", len(found), len(expected), found)
	}

	bySite := make(map[string]frameworkLogCall, len(found))
	for _, call := range found {
		key := call.file + "\x00" + call.message
		if prior, duplicate := bySite[key]; duplicate {
			t.Fatalf("duplicate framework log site at %s and %s", prior.position, call.position)
		}
		bySite[key] = call
	}
	for _, want := range expected {
		key := want.file + "\x00" + want.message
		got, ok := bySite[key]
		if !ok {
			t.Errorf("missing framework log %q in %s", want.message, want.file)
			continue
		}
		if got.method != want.method || got.loggerContext != want.loggerContext || got.callContext != want.callContext {
			t.Errorf("%s is %s with logger context %q and call context %q; want %s with logger context %q and call context %q",
				got.position, got.method, got.loggerContext, got.callContext,
				want.method, want.loggerContext, want.callContext)
		}
		delete(bySite, key)
	}
	for _, extra := range bySite {
		t.Errorf("unexpected framework log site at %s: %q", extra.position, extra.message)
	}
}

func frameworkContextLogCalls(t *testing.T) []frameworkLogCall {
	t.Helper()
	positions := token.NewFileSet()
	var calls []frameworkLogCall
	err := filepath.WalkDir("..", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != ".." && (entry.Name() == ".git" || entry.Name() == ".agents" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel("..", path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !bytes.Contains(source, []byte("port.Logger(")) && relative != "runtime/supervisor.go" {
			return nil
		}
		parsed, err := parser.ParseFile(positions, path, source, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			found, ok := frameworkLogAt(relative, positions.Position(call.Pos()), call)
			if ok {
				calls = append(calls, found)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("cannot inventory framework log calls: %v", err)
	}
	return calls
}

func frameworkLogAt(file string, position token.Position, call *ast.CallExpr) (frameworkLogCall, bool) {
	method, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !slogMethod(method.Sel.Name) {
		return frameworkLogCall{}, false
	}
	loggerContext, portLogger := portLoggerContext(method.X)
	supervisorLogger := file == "runtime/supervisor.go" && expression(method.X) == "this.log"
	if !portLogger && !supervisorLogger {
		return frameworkLogCall{}, false
	}
	contextIndex, messageIndex := slogArgumentIndexes(method.Sel.Name)
	contextExpression := ""
	if contextIndex >= 0 && len(call.Args) > contextIndex {
		contextExpression = expression(call.Args[contextIndex])
	}
	message := "<missing>"
	if len(call.Args) > messageIndex {
		message = stringLiteral(call.Args[messageIndex])
	}
	return frameworkLogCall{
		frameworkLogExpectation: frameworkLogExpectation{
			file:          file,
			message:       message,
			method:        method.Sel.Name,
			loggerContext: loggerContext,
			callContext:   contextExpression,
		},
		position: position,
	}, true
}

func portLoggerContext(node ast.Expr) (string, bool) {
	call, ok := node.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Logger" {
		return "", false
	}
	owner, ok := selector.X.(*ast.Ident)
	if !ok || owner.Name != "port" {
		return "", false
	}
	return expression(call.Args[0]), true
}

func slogMethod(name string) bool {
	switch name {
	case "Debug", "Info", "Warn", "Error",
		"DebugContext", "InfoContext", "WarnContext", "ErrorContext",
		"Log", "LogAttrs":
		return true
	default:
		return false
	}
}

func slogArgumentIndexes(method string) (int, int) {
	switch method {
	case "DebugContext", "InfoContext", "WarnContext", "ErrorContext":
		return 0, 1
	case "Log", "LogAttrs":
		return 0, 2
	default:
		return -1, 0
	}
}

func expression(node ast.Node) string {
	var rendered bytes.Buffer
	if err := format.Node(&rendered, token.NewFileSet(), node); err != nil {
		return "<invalid>"
	}
	return rendered.String()
}

func stringLiteral(node ast.Expr) string {
	literal, ok := node.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return expression(node)
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return expression(node)
	}
	return value
}

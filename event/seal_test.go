package event

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

func sealingReaders() map[string]func(accountDeclaration) {
	return map[string]func(accountDeclaration){
		"Aggregate.Family": func(declared accountDeclaration) { _ = declared.aggregate.Family() },
		"Aggregate.Key": func(declared accountDeclaration) {
			_, _ = declared.aggregate.Key(accountID{tenant: "acme", number: "A-17"})
		},
		"Aggregate.Fold": func(declared accountDeclaration) {
			_, _ = declared.aggregate.Fold(accountID{tenant: "acme", number: "A-17"}, account{})
		},
		"Fact.New": func(declared accountDeclaration) {
			_ = declared.opened.New(accountID{tenant: "acme", number: "A-17"}, opened{Owner: "acme"})
		},
		"Fact.RoundTrip": func(declared accountDeclaration) {
			_, _ = declared.opened.RoundTrip(opened{Owner: "acme"})
		},
	}
}

func sealCallers(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("the package this test polices cannot be read: %v", err)
	}
	positions := token.NewFileSet()
	callers := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(positions, name, nil, 0)
		if err != nil {
			t.Fatalf("%s does not parse: %v", name, err)
		}
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction {
				continue
			}
			ast.Inspect(function, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector && selector.Sel.Name == "seal" {
					callers = append(callers, receiverTypeOf(function)+"."+function.Name.Name)
				}
				return true
			})
		}
	}
	sort.Strings(callers)
	return slices.Compact(callers)
}

func sealEnumeration(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "seal.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("event/seal.go does not parse, so the enumeration a reader of the library sees cannot be read: %v", err)
	}
	named := []string{}
	for _, declaration := range file.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction || function.Name.Name != "seal" || function.Doc == nil {
			continue
		}
		for _, line := range strings.Split(function.Doc.Text(), "\n") {
			if strings.HasPrefix(line, "\t") {
				named = append(named, strings.Fields(line)[0])
			}
		}
	}
	sort.Strings(named)
	return named
}

func receiverTypeOf(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return ""
	}
	named := ""
	ast.Inspect(function.Recv.List[0].Type, func(node ast.Node) bool {
		if identifier, is := node.(*ast.Ident); is && named == "" {
			named = identifier.Name
		}
		return true
	})
	return named
}

func TestTheSealRefusesALateFact(t *testing.T) {
	late := func(t *testing.T, aggregate *Aggregate[account, accountID]) error {
		t.Helper()
		_, err := TryDeclare(aggregate, "accounts.late", From(JSON[closed]()),
			func(this account, _ closed) account { return this })
		return err
	}

	t.Run("every reader of the declaration seals it", func(t *testing.T) {
		for name, read := range sealingReaders() {
			declared := declareAccounts(t)
			if err := late(t, declared.aggregate); err != nil {
				t.Fatalf("%s: a fact declared before any reader ran was refused, so the control cannot tell a seal from a broken declaration: %v", name, err)
			}
			sealed := declareAccounts(t)
			read(sealed)
			err := late(t, sealed.aggregate)
			if !errors.Is(err, ErrSealed) || !errors.Is(err, ErrDeclaration) {
				t.Fatalf("%s read the declaration and a later fact was still accepted (%v), so the type table is mutable while it is being read", name, err)
			}
			panicked := recoverDeclaration(t, "a fact declared after "+name+" read the aggregate", func() {
				Declare(sealed.aggregate, "accounts.later", From(JSON[closed]()),
					func(this account, _ closed) account { return this })
			})
			if !errors.Is(panicked, ErrSealed) {
				t.Fatalf("%s: the late declaration panicked with %v rather than the seal", name, panicked)
			}
		}
	})

	t.Run("the readers that seal are exhaustive and enumerated", func(t *testing.T) {
		want := []string{}
		for name := range sealingReaders() {
			want = append(want, name)
		}
		sort.Strings(want)
		if got := sealCallers(t); !slices.Equal(got, want) {
			t.Fatalf("the declaration is sealed from %v and this test drives %v; a reader that seals without a case here, or a case with no reader, is what leaves the table mutable while it is read",
				got, want)
		}
		if enumerated := sealEnumeration(t); !slices.Equal(enumerated, want) {
			t.Fatalf("event/seal.go tells a reader of the library that %v seal the declaration and %v do; the enumeration is the only artefact a consumer sees and nothing but this compares it to the code",
				enumerated, want)
		}
	})
}

package runtimecheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Activation struct {
	File string
	Line int
	Name string
}

func (this Activation) String() string {
	return fmt.Sprintf("%s:%d fx.Invoke(%s)", this.File, this.Line, this.Name)
}

type Scanner struct {
	SkipDirectory func(name string) bool
}

func EmptyInvokeActivations(root string) ([]Activation, error) {
	return Scanner{}.EmptyInvokeActivations(root)
}

func (this Scanner) EmptyInvokeActivations(root string) ([]Activation, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	var found []Activation
	walk := func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if path != absolute && this.skipped(entry.Name()) {
			return filepath.SkipDir
		}
		activations, err := this.scanPackage(path, absolute)
		if err != nil {
			return err
		}
		found = append(found, activations...)
		return nil
	}
	if err = filepath.WalkDir(absolute, walk); err != nil {
		return nil, err
	}
	slices.SortFunc(found, func(left, right Activation) int {
		if left.File != right.File {
			return strings.Compare(left.File, right.File)
		}
		return left.Line - right.Line
	})
	return found, nil
}

func (this Scanner) skipped(name string) bool {
	if this.SkipDirectory != nil {
		return this.SkipDirectory(name)
	}
	return SkipsHiddenAndVendored(name)
}

func SkipsHiddenAndVendored(name string) bool {
	return name == "testdata" || name == "vendor" || name == "node_modules" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// scanPackage reads a directory as one package rather than each file on its
// own: `fx.Invoke(reached)` and `func reached(Client) {}` are routinely written
// in different files of the same package, and a per-file walk would see the
// invoke without ever seeing that the function it names has an empty body.
func (this Scanner) scanPackage(directory, root string) ([]Activation, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}

	fileSet := token.NewFileSet()
	parsed := make([]*ast.File, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isProductionSource(entry.Name()) {
			continue
		}
		file, err := parser.ParseFile(fileSet, filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, file)
	}

	empty := emptyFunctions(parsed)

	var found []Activation
	for _, file := range parsed {
		for _, activation := range emptyInvokesIn(file, empty) {
			position := fileSet.Position(activation.position)
			relative, err := filepath.Rel(root, position.Filename)
			if err != nil {
				return nil, err
			}
			found = append(found, Activation{
				File: filepath.ToSlash(relative),
				Line: position.Line,
				Name: activation.name,
			})
		}
	}
	return found, nil
}

func isProductionSource(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func emptyFunctions(files []*ast.File) map[string]bool {
	empty := map[string]bool{}
	for _, file := range files {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Recv != nil {
				continue
			}
			if hasEmptyBody(function.Body) {
				empty[function.Name.Name] = true
			}
		}
	}
	return empty
}

type located struct {
	name     string
	position token.Pos
}

func emptyInvokesIn(file *ast.File, empty map[string]bool) []located {
	var found []located
	ast.Inspect(file, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall || !isFxInvoke(call.Fun) {
			return true
		}
		for _, argument := range call.Args {
			switch subject := argument.(type) {
			case *ast.FuncLit:
				if hasEmptyBody(subject.Body) {
					found = append(found, located{name: "func literal", position: subject.Pos()})
				}
			case *ast.Ident:
				if empty[subject.Name] {
					found = append(found, located{name: subject.Name, position: subject.Pos()})
				}
			}
		}
		return true
	})
	return found
}

func hasEmptyBody(body *ast.BlockStmt) bool {
	return body != nil && len(body.List) == 0
}

func isFxInvoke(function ast.Expr) bool {
	selector, isSelector := function.(*ast.SelectorExpr)
	if !isSelector || selector.Sel.Name != "Invoke" {
		return false
	}
	qualifier, isIdent := selector.X.(*ast.Ident)
	return isIdent && qualifier.Name == "fx"
}

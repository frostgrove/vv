package jobsgen

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"
)

const (
	jobsImportPath   = "github.com/frostgrove/vv/jobs"
	jobsFXImportPath = "github.com/frostgrove/vv/jobs/jobsfx"
)

type declaration struct {
	variable string
	kind     string
	payload  types.Type
	injected bool
}

type declarationReference struct {
	kind     string
	display  string
	injected bool
}

func discover(loaded *loadedPackage) ([]declaration, error) {
	result := make([]declaration, 0)
	problems := make([]string, 0)
	supported := map[*ast.Ident]struct{}{}
	for _, file := range sortedFiles(loaded.files, loaded.fileNames) {
		for _, raw := range file.Decls {
			general, ok := raw.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, item := range general.Specs {
				spec, ok := item.(*ast.ValueSpec)
				if !ok || len(spec.Names) != len(spec.Values) {
					continue
				}
				for index, value := range spec.Values {
					reference, declarationType := directDeclarationReference(loaded.info, value)
					if reference == nil {
						continue
					}
					supported[reference] = struct{}{}
					name := spec.Names[index]
					if name.Name == "_" {
						problems = append(problems, positionProblem(loaded, name, declarationType.display+" must be assigned to a named package-level variable"))
						continue
					}
					payload, err := automaticPayload(loaded.info.Defs[name], declarationType.injected)
					if err != nil {
						problems = append(problems, positionProblem(loaded, name, err.Error()))
						continue
					}
					result = append(result, declaration{variable: name.Name, kind: declarationType.kind, payload: payload, injected: declarationType.injected})
				}
			}
		}
	}
	for _, file := range sortedFiles(loaded.files, loaded.fileNames) {
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			declarationType, ok := jobsDeclarationObject(loaded.info.Uses[identifier])
			if !ok {
				return true
			}
			if _, ok := supported[identifier]; ok {
				return true
			}
			problems = append(problems, positionProblem(loaded, identifier, declarationType.display+" is only supported as a direct package-level variable initializer"))
			return true
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].variable < result[right].variable
	})
	if len(problems) != 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("jobsgen: declarations failed: %s", strings.Join(problems, "; "))
	}
	return result, nil
}

func directDeclarationReference(info *types.Info, expression ast.Expr) (*ast.Ident, declarationReference) {
	call, ok := unparen(expression).(*ast.CallExpr)
	if !ok {
		return nil, declarationReference{}
	}
	function := unparen(call.Fun)
	switch item := function.(type) {
	case *ast.IndexExpr:
		function = unparen(item.X)
	case *ast.IndexListExpr:
		function = unparen(item.X)
	}
	var identifier *ast.Ident
	switch item := function.(type) {
	case *ast.SelectorExpr:
		identifier = item.Sel
	case *ast.Ident:
		identifier = item
	default:
		return nil, declarationReference{}
	}
	declarationType, ok := jobsDeclarationObject(info.Uses[identifier])
	if !ok {
		return nil, declarationReference{}
	}
	return identifier, declarationType
}

func jobsDeclarationObject(object types.Object) (declarationReference, bool) {
	function, ok := object.(*types.Func)
	if !ok || function.Pkg() == nil {
		return declarationReference{}, false
	}
	switch function.Pkg().Path() {
	case jobsImportPath:
		if function.Name() != "Auto" && function.Name() != "Declare" {
			return declarationReference{}, false
		}
		return declarationReference{kind: function.Name(), display: "jobs." + function.Name()}, true
	case jobsFXImportPath:
		if function.Name() != "Auto" && function.Name() != "AutoAdapter" {
			return declarationReference{}, false
		}
		return declarationReference{kind: "Auto", display: "jobsfx." + function.Name(), injected: true}, true
	default:
		return declarationReference{}, false
	}
}

func automaticPayload(object types.Object, injected bool) (types.Type, error) {
	variable, ok := object.(*types.Var)
	if !ok {
		return nil, fmt.Errorf("cannot resolve automatic declaration type")
	}
	pointer, ok := types.Unalias(variable.Type()).(*types.Pointer)
	if !ok {
		return nil, fmt.Errorf("declaration must have type *jobs.Automatic[P]")
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	if injected {
		if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != jobsFXImportPath || named.Obj().Name() != "Binding" || named.TypeArgs().Len() != 2 {
			return nil, fmt.Errorf("declaration must have type *jobsfx.Binding[D, P]")
		}
		return named.TypeArgs().At(1), nil
	}
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != jobsImportPath || named.Obj().Name() != "Automatic" || named.TypeArgs().Len() != 1 {
		return nil, fmt.Errorf("declaration must have type *jobs.Automatic[P]")
	}
	return named.TypeArgs().At(0), nil
}

func positionProblem(loaded *loadedPackage, node ast.Node, message string) string {
	position := loaded.fset.Position(node.Pos())
	return fmt.Sprintf("%s:%d:%d: %s", position.Filename, position.Line, position.Column, message)
}

func unparen(expression ast.Expr) ast.Expr {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = parenthesized.X
	}
}

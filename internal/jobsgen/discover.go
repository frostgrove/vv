package jobsgen

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"
)

const jobsImportPath = "github.com/frostgrove/vv/jobs"

type declaration struct {
	variable string
	kind     string
	payload  types.Type
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
					reference, kind := directDeclarationReference(loaded.info, value)
					if reference == nil {
						continue
					}
					supported[reference] = struct{}{}
					name := spec.Names[index]
					if name.Name == "_" {
						problems = append(problems, positionProblem(loaded, name, "jobs."+kind+" must be assigned to a named package-level variable"))
						continue
					}
					payload, err := automaticPayload(loaded.info.Defs[name])
					if err != nil {
						problems = append(problems, positionProblem(loaded, name, err.Error()))
						continue
					}
					result = append(result, declaration{variable: name.Name, kind: kind, payload: payload})
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
			kind, ok := jobsDeclarationObject(loaded.info.Uses[identifier])
			if !ok {
				return true
			}
			if _, ok := supported[identifier]; ok {
				return true
			}
			problems = append(problems, positionProblem(loaded, identifier, "jobs."+kind+" is only supported as a direct package-level variable initializer"))
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

func directDeclarationReference(info *types.Info, expression ast.Expr) (*ast.Ident, string) {
	call, ok := unparen(expression).(*ast.CallExpr)
	if !ok {
		return nil, ""
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
		return nil, ""
	}
	kind, ok := jobsDeclarationObject(info.Uses[identifier])
	if !ok {
		return nil, ""
	}
	return identifier, kind
}

func jobsDeclarationObject(object types.Object) (string, bool) {
	function, ok := object.(*types.Func)
	if !ok || function.Pkg() == nil || function.Pkg().Path() != jobsImportPath {
		return "", false
	}
	if function.Name() != "Auto" && function.Name() != "Declare" {
		return "", false
	}
	return function.Name(), true
}

func automaticPayload(object types.Object) (types.Type, error) {
	variable, ok := object.(*types.Var)
	if !ok {
		return nil, fmt.Errorf("cannot resolve automatic declaration type")
	}
	pointer, ok := types.Unalias(variable.Type()).(*types.Pointer)
	if !ok {
		return nil, fmt.Errorf("declaration must have type *jobs.Automatic[P]")
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
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

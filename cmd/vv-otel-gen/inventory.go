package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var errSourceTypeAbsent = errors.New("source type is absent")

type interfaceResolver struct{ root string }

func (r interfaceResolver) methods(path, name string, visiting map[string]bool) (map[string]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	key := filepath.Clean(path) + ":" + name
	if visiting[key] {
		return nil, errors.New("recursive interface embedding")
	}
	visiting[key] = true
	defer delete(visiting, key)
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.TYPE {
			continue
		}
		for _, raw := range group.Specs {
			spec := raw.(*ast.TypeSpec)
			if spec.Name.Name == name {
				return r.resolve(path, file, spec.Type, visiting)
			}
		}
	}
	return nil, fmt.Errorf("%w: source file %s does not declare interface %s", errSourceTypeAbsent, path, name)
}

func (r interfaceResolver) resolve(path string, file *ast.File, expression ast.Expr, visiting map[string]bool) (map[string]string, error) {
	switch value := expression.(type) {
	case *ast.InterfaceType:
		result := map[string]string{}
		for _, field := range value.Methods.List {
			if len(field.Names) > 0 {
				if _, ok := field.Type.(*ast.FuncType); !ok {
					return nil, errors.New("interface member is not a method")
				}
				for _, name := range field.Names {
					result[name.Name] = sourceType(field.Type)
				}
				continue
			}
			embedded, err := r.resolve(path, file, field.Type, visiting)
			if err != nil {
				return nil, err
			}
			for name, signature := range embedded {
				if previous, found := result[name]; found && previous != signature {
					return nil, errors.New("conflicting embedded method signatures")
				}
				result[name] = signature
			}
		}
		return result, nil
	case *ast.IndexExpr:
		return r.resolve(path, file, value.X, visiting)
	case *ast.IndexListExpr:
		return r.resolve(path, file, value.X, visiting)
	case *ast.ParenExpr:
		return r.resolve(path, file, value.X, visiting)
	case *ast.Ident:
		if value.Name == "any" {
			return map[string]string{}, nil
		}
		if value.Name == "error" {
			return map[string]string{"Error": "func() string"}, nil
		}
		return r.packageMethods(filepath.Dir(path), file.Name.Name, value.Name, visiting)
	case *ast.SelectorExpr:
		qualifier, ok := value.X.(*ast.Ident)
		if !ok {
			return nil, errors.New("invalid embedded interface qualifier")
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return nil, err
			}
			alias := filepath.Base(importPath)
			if imported.Name != nil {
				alias = imported.Name.Name
			}
			if alias != qualifier.Name {
				continue
			}
			dir, err := r.importDirectory(importPath, filepath.Dir(path))
			if err != nil {
				return nil, err
			}
			return r.packageMethods(dir, "", value.Sel.Name, visiting)
		}
		return nil, fmt.Errorf("unresolved embedded interface import %s", qualifier.Name)
	default:
		return nil, errors.New("source type is not an interface or interface alias")
	}
}

func (r interfaceResolver) packageMethods(dir, packageName, name string, visiting map[string]bool) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		matched, err := build.Default.MatchFile(dir, entry.Name())
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil, err
		}
		if packageName != "" && file.Name.Name != packageName {
			continue
		}
		for _, declaration := range file.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, raw := range group.Specs {
				if raw.(*ast.TypeSpec).Name.Name == name {
					return r.methods(path, name, visiting)
				}
			}
		}
	}
	return nil, fmt.Errorf("unresolved embedded interface %s in %s", name, dir)
}

func (r interfaceResolver) importDirectory(importPath, from string) (string, error) {
	data, err := os.ReadFile(filepath.Join(r.root, "go.mod"))
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[0] != "module" {
				continue
			}
			module := strings.Trim(fields[1], "\"")
			if importPath == module {
				return r.root, nil
			}
			if strings.HasPrefix(importPath, module+"/") {
				return filepath.Join(r.root, strings.TrimPrefix(importPath, module+"/")), nil
			}
		}
	}
	pkg, err := build.Default.Import(importPath, from, build.FindOnly)
	if err != nil {
		return "", err
	}
	return pkg.Dir, nil
}

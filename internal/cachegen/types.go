package cachegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type loadedPackage struct {
	dir          string
	name         string
	importPath   string
	fset         *token.FileSet
	files        []*ast.File
	fileNames    map[*ast.File]string
	info         *types.Info
	types        *types.Package
	declarations map[string]struct{}
	imports      map[string]struct{}
	target       buildTarget
}

func loadPackage(dir, output string, target buildTarget) (*loadedPackage, error) {
	metadata, err := listPackage(dir, target)
	if err != nil {
		return nil, err
	}
	if err := validateUniversalSources(metadata, filepath.Base(output)); err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(metadata.GoFiles))
	fileNames := make(map[*ast.File]string, len(metadata.GoFiles))
	outputBase := filepath.Base(output)
	for _, name := range metadata.GoFiles {
		if name == outputBase || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(metadata.Dir, name)
		parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("cachegen: parse %s: %w", path, err)
		}
		if hasBuildConstraint(parsed) {
			return nil, fmt.Errorf("cachegen: conditional Go file %s is not supported by universal cache generation", path)
		}
		files = append(files, parsed)
		fileNames[parsed] = path
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("cachegen: no Go source files in %s", metadata.Dir)
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Instances:  map[*ast.Ident]types.Instance{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	checkFiles := files
	if !syntaxDeclaresName(files, "VVCacheSet") {
		stub, err := parser.ParseFile(fset, "<cachegen-activation>", "package "+metadata.Name+"\nimport _vvcachebootstrap \""+cacheImportPath+"\"\nvar VVCacheSet _vvcachebootstrap.Set\n", 0)
		if err != nil {
			return nil, fmt.Errorf("cachegen: build activation stub: %w", err)
		}
		checkFiles = append(append([]*ast.File(nil), files...), stub)
	}
	lookup := &exportLookup{dir: metadata.Dir, target: target, exports: map[string]string{}, failures: map[string]error{}}
	configuration := &types.Config{
		Importer:    importer.ForCompiler(fset, "gc", lookup.open),
		FakeImportC: true,
		Sizes:       types.SizesFor("gc", target.GOARCH),
	}
	checked, err := configuration.Check(metadata.ImportPath, fset, checkFiles, info)
	if err != nil {
		return nil, fmt.Errorf("cachegen: type-check package: %w", err)
	}
	return &loadedPackage{
		dir:          metadata.Dir,
		name:         metadata.Name,
		importPath:   metadata.ImportPath,
		fset:         fset,
		files:        files,
		fileNames:    fileNames,
		info:         info,
		types:        checked,
		declarations: packageDeclarations(files),
		imports:      packageImportNames(files, info),
		target:       target,
	}, nil
}

func syntaxDeclaresName(files []*ast.File, name string) bool {
	if _, exists := packageDeclarations(files)[name]; exists {
		return true
	}
	for _, file := range files {
		for _, spec := range file.Imports {
			if spec.Name != nil && spec.Name.Name == name {
				return true
			}
		}
	}
	return false
}

type packageMetadata struct {
	Dir            string
	ImportPath     string
	Name           string
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	InvalidGoFiles []string
	Error          *struct {
		Err string
	}
}

func validateUniversalSources(metadata packageMetadata, output string) error {
	platforms, err := goPlatformSuffixes()
	if err != nil {
		return err
	}
	conditional := make([]string, 0, len(metadata.IgnoredGoFiles)+len(metadata.CgoFiles))
	for _, name := range append(append([]string(nil), metadata.IgnoredGoFiles...), metadata.CgoFiles...) {
		if name != output && !strings.HasSuffix(name, "_test.go") {
			conditional = append(conditional, name)
		}
	}
	for _, name := range metadata.GoFiles {
		if name == output || strings.HasSuffix(name, "_test.go") {
			continue
		}
		stem := strings.TrimSuffix(name, ".go")
		parts := strings.Split(stem, "_")
		if len(parts) > 1 && platforms[parts[len(parts)-1]] {
			conditional = append(conditional, name)
		}
	}
	sort.Strings(conditional)
	conditional = compactStrings(conditional)
	if len(conditional) != 0 {
		return fmt.Errorf("cachegen: conditional or cgo source files are not supported by universal cache generation: %s", strings.Join(conditional, ", "))
	}
	return nil
}

func hasBuildConstraint(file *ast.File) bool {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if constraint.IsGoBuild(comment.Text) || constraint.IsPlusBuild(comment.Text) {
				return true
			}
		}
	}
	return false
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func listPackage(dir string, target buildTarget) (packageMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-json", ".")
	command.Dir = dir
	command.Env = buildEnvironment(target.GOOS, target.GOARCH)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return packageMetadata{}, fmt.Errorf("cachegen: list package: %s", message)
	}
	var metadata packageMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return packageMetadata{}, fmt.Errorf("cachegen: decode go list result: %w", err)
	}
	if metadata.Error != nil {
		return packageMetadata{}, fmt.Errorf("cachegen: list package: %s", metadata.Error.Err)
	}
	if len(metadata.InvalidGoFiles) != 0 {
		sort.Strings(metadata.InvalidGoFiles)
		return packageMetadata{}, fmt.Errorf("cachegen: package contains invalid Go files: %s", strings.Join(metadata.InvalidGoFiles, ", "))
	}
	if metadata.Dir == "" || metadata.Name == "" || metadata.ImportPath == "" {
		return packageMetadata{}, fmt.Errorf("cachegen: go list returned incomplete package metadata")
	}
	return metadata, nil
}

type exportLookup struct {
	dir      string
	target   buildTarget
	exports  map[string]string
	failures map[string]error
}

func (this *exportLookup) open(path string) (io.ReadCloser, error) {
	if export := this.exports[path]; export != "" {
		return os.Open(export)
	}
	if err := this.failures[path]; err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-mod=readonly", "-deps", "-export", "-json", path)
	command.Dir = this.dir
	command.Env = buildEnvironment(this.target.GOOS, this.target.GOARCH)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, runErr := command.Output()
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var item struct {
			ImportPath string
			Export     string
		}
		if err := decoder.Decode(&item); err != nil {
			if err != io.EOF && runErr == nil {
				runErr = err
			}
			break
		}
		if item.ImportPath != "" && item.Export != "" {
			this.exports[item.ImportPath] = item.Export
		}
	}
	if export := this.exports[path]; export != "" {
		return os.Open(export)
	}
	message := strings.TrimSpace(stderr.String())
	if message == "" && runErr != nil {
		message = runErr.Error()
	}
	if message == "" {
		message = "go list returned no export data"
	}
	err := fmt.Errorf("cachegen: resolve package %q: %s", path, message)
	this.failures[path] = err
	return nil, err
}

func packageDeclarations(files []*ast.File) map[string]struct{} {
	result := map[string]struct{}{}
	for _, file := range files {
		for _, declaration := range file.Decls {
			switch item := declaration.(type) {
			case *ast.FuncDecl:
				if item.Recv == nil {
					result[item.Name.Name] = struct{}{}
				}
			case *ast.GenDecl:
				for _, raw := range item.Specs {
					switch spec := raw.(type) {
					case *ast.TypeSpec:
						result[spec.Name.Name] = struct{}{}
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							result[name.Name] = struct{}{}
						}
					}
				}
			}
		}
	}
	return result
}

func packageImportNames(files []*ast.File, info *types.Info) map[string]struct{} {
	result := map[string]struct{}{}
	for _, file := range files {
		for _, spec := range file.Imports {
			name := ""
			if spec.Name != nil {
				name = spec.Name.Name
			} else if imported, ok := info.Implicits[spec].(*types.PkgName); ok {
				name = imported.Name()
			}
			if name != "" && name != "_" && name != "." {
				result[name] = struct{}{}
			}
		}
	}
	return result
}

func typeShape(value types.Type) string {
	var output strings.Builder
	renderTypeShape(&output, value, map[*types.Named]bool{})
	return output.String()
}

func renderTypeShape(output *strings.Builder, value types.Type, active map[*types.Named]bool) {
	value = types.Unalias(value)
	switch item := value.(type) {
	case *types.Basic:
		output.WriteString(item.Name())
	case *types.Pointer:
		output.WriteByte('*')
		renderTypeShape(output, item.Elem(), active)
	case *types.Slice:
		output.WriteString("[]")
		renderTypeShape(output, item.Elem(), active)
	case *types.Array:
		fmt.Fprintf(output, "[%d]", item.Len())
		renderTypeShape(output, item.Elem(), active)
	case *types.Map:
		output.WriteString("map[")
		renderTypeShape(output, item.Key(), active)
		output.WriteByte(']')
		renderTypeShape(output, item.Elem(), active)
	case *types.Named:
		if item.Obj().Pkg() != nil {
			output.WriteString(item.Obj().Pkg().Path())
			output.WriteByte('.')
		}
		output.WriteString(item.Obj().Name())
		if active[item] {
			output.WriteString("{recursive}")
			return
		}
		active[item] = true
		output.WriteByte('{')
		renderTypeShape(output, item.Underlying(), active)
		output.WriteByte('}')
		delete(active, item)
	case *types.Struct:
		output.WriteString("struct{")
		for index := 0; index < item.NumFields(); index++ {
			field := item.Field(index)
			if index != 0 {
				output.WriteByte(';')
			}
			if field.Embedded() {
				output.WriteByte('~')
			}
			output.WriteString(field.Name())
			output.WriteByte(' ')
			renderTypeShape(output, field.Type(), active)
			output.WriteByte(' ')
			output.WriteString(item.Tag(index))
		}
		output.WriteByte('}')
	case *types.Interface:
		output.WriteString(types.TypeString(item, packagePath))
	case *types.Signature:
		output.WriteString(types.TypeString(item, packagePath))
	case *types.Chan:
		output.WriteString(types.TypeString(item, packagePath))
	default:
		output.WriteString(types.TypeString(value, packagePath))
	}
}

func packagePath(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return pkg.Path()
}

func sortedFiles(files []*ast.File, names map[*ast.File]string) []*ast.File {
	result := append([]*ast.File(nil), files...)
	sort.Slice(result, func(left, right int) bool {
		return names[result[left]] < names[result[right]]
	})
	return result
}

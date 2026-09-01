package jobsgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
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
}

type packageMetadata struct {
	Dir        string
	ImportPath string
	Name       string
	GoFiles    []string
	Error      *struct {
		Err string
	}
}

func loadPackage(dir, output string) (*loadedPackage, error) {
	metadata, err := listPackage(dir)
	if err != nil {
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
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("jobsgen: parse %s: %w", path, err)
		}
		files = append(files, parsed)
		fileNames[parsed] = path
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("jobsgen: no Go source files in %s", metadata.Dir)
	}
	declarations := packageDeclarations(files)
	if _, exists := declarations["VVJobsCatalog"]; exists {
		return nil, fmt.Errorf("jobsgen: generated declaration VVJobsCatalog collides with authored code")
	}
	if _, exists := declarations["VVJobs"]; exists {
		return nil, fmt.Errorf("jobsgen: generated declaration VVJobs collides with authored code")
	}
	if _, exists := declarations["_vvJobsMustName"]; exists {
		return nil, fmt.Errorf("jobsgen: generated declaration _vvJobsMustName collides with authored code")
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Instances:  map[*ast.Ident]types.Instance{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	stubSource := "package " + metadata.Name + "\nimport _vvjobsbootstrap \"" + jobsImportPath + "\"\nvar VVJobsCatalog _vvjobsbootstrap.Catalog\n"
	if packageImports(files, jobsFXImportPath) {
		stubSource = "package " + metadata.Name + "\nimport (\n_vvjobsbootstrap \"" + jobsImportPath + "\"\n_vvjobsfxbootstrap \"" + jobsFXImportPath + "\"\n)\nvar VVJobsCatalog _vvjobsbootstrap.Catalog\nfunc VVJobs(..._vvjobsfxbootstrap.BundleOption) _vvjobsfxbootstrap.Option { panic(\"generated\") }\n"
	}
	stub, err := parser.ParseFile(fset, "<jobsgen-catalog>", stubSource, 0)
	if err != nil {
		return nil, fmt.Errorf("jobsgen: build catalog stub: %w", err)
	}
	checkFiles := append(append([]*ast.File(nil), files...), stub)
	lookup := &exportLookup{dir: metadata.Dir, exports: map[string]string{}, failures: map[string]error{}}
	configuration := &types.Config{Importer: importer.ForCompiler(fset, "gc", lookup.open), FakeImportC: true}
	checked, err := configuration.Check(metadata.ImportPath, fset, checkFiles, info)
	if err != nil {
		return nil, fmt.Errorf("jobsgen: type-check package: %w", err)
	}
	return &loadedPackage{dir: metadata.Dir, name: metadata.Name, importPath: metadata.ImportPath, fset: fset, files: files, fileNames: fileNames, info: info, types: checked, declarations: declarations}, nil
}

func packageImports(files []*ast.File, path string) bool {
	want := `"` + path + `"`
	for _, file := range files {
		for _, spec := range file.Imports {
			if spec.Path != nil && spec.Path.Value == want {
				return true
			}
		}
	}
	return false
}

func listPackage(dir string) (packageMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-json", ".")
	command.Dir = dir
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return packageMetadata{}, fmt.Errorf("jobsgen: list package: %s", message)
	}
	var metadata packageMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return packageMetadata{}, fmt.Errorf("jobsgen: decode go list result: %w", err)
	}
	if metadata.Error != nil {
		return packageMetadata{}, fmt.Errorf("jobsgen: list package: %s", metadata.Error.Err)
	}
	if metadata.Dir == "" || metadata.Name == "" || metadata.ImportPath == "" {
		return packageMetadata{}, fmt.Errorf("jobsgen: go list returned incomplete package metadata")
	}
	return metadata, nil
}

type exportLookup struct {
	dir      string
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
	err := fmt.Errorf("jobsgen: resolve package %q: %s", path, message)
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

func sortedFiles(files []*ast.File, names map[*ast.File]string) []*ast.File {
	result := append([]*ast.File(nil), files...)
	sort.Slice(result, func(left, right int) bool {
		return names[result[left]] < names[result[right]]
	})
	return result
}

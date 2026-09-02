package codegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type field struct {
	Name      string
	Type      string
	Tag       string
	Rel       string
	HasRel    bool
	RelTarget string
	Skip      bool
	PK        bool

	ExplicitPK bool
	Auto       bool

	NoAuto      bool
	Integral    bool
	Immutable   bool
	Generated   bool
	ServerOwned bool
	Tombstone   bool
	Secret      bool

	Version bool

	Excluded bool
}

func (this field) tagDropped() bool {
	return this.Skip || this.PK || this.Generated || this.Immutable || this.ServerOwned || this.Tombstone || this.Version
}

type model struct {
	Name   string
	Fields []field
}

func (this *model) pk() (field, bool) {
	for _, f := range this.Fields {
		if f.PK {
			return f, true
		}
	}
	return field{}, false
}

func (this *model) excluded() []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range this.Fields {
		if !f.Excluded || seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

type generator struct {
	dir      string
	pkg      string
	only     map[string]bool
	depth    int
	withDTO  bool
	withMeta bool
	withRepo bool
	adapter  bool
	wireOnly bool
	check    bool
	binding  string
	specsPkg string
	crudPkg  string
	utilsPkg string
	wirePkg  string
	portPkg  string
	errsPkg  string
	netPkg   string

	models     map[string]*model
	bodies     map[string]resourceBodies
	order      []string
	skip       map[string]bool
	readonly   map[string]bool
	structs    map[string]bool
	embeds     map[string]*ast.StructType
	embedFiles map[string]*ast.File

	mirrorProblems map[string]bool

	into        string
	modelImport string
	modelAlias  string

	imports       map[string]string
	fileImports   map[*ast.File]map[string]string
	sourceImports []sourceImport
	declaredNames map[string]bool
	pathAliases   map[string]string
	aliasPaths    map[string]string
	usedAliases   map[string]bool
	types         *sourceTypes

	log io.Writer
}

func (this *generator) run(outPath string) error {
	if !this.check {
		if err := validateGeneratedTarget(outPath); err != nil {
			return err
		}
	}
	if err := this.load(outPath); err != nil {
		return err
	}
	if this.into != "" {
		if this.modelImport == "" {
			return fmt.Errorf("-into needs -import so the generated file can name the model types")
		}
		if err := os.MkdirAll(this.into, 0o755); err != nil {
			return err
		}
		this.pkg = packageNameOf(this.into)
	}
	if len(this.order) == 0 {
		return fmt.Errorf("no models found in %s; put exported model structs in model.go, *.model.go or *_model.go", this.dir)
	}
	if this.withRepo && !this.withDTO {
		return fmt.Errorf("a generated repository needs its update DTO; drop -no-dto or add -no-repo")
	}
	if this.adapter && !this.withDTO {
		return fmt.Errorf("-adapter needs the update DTO; drop -no-dto")
	}
	if err := this.validateDeclarations(outPath); err != nil {
		return err
	}
	source, err := this.render()
	if err != nil {
		return err
	}
	if err := this.validateRenderedImports(outPath, source); err != nil {
		return err
	}
	if this.check {
		return checkArtifacts([]artifact{{outPath, source}})
	}
	if err := writeGenerated(outPath, source); err != nil {
		return err
	}
	if this.log != nil {
		fmt.Fprintf(this.log, "vv: wrote %s (%d models)\n", outPath, len(this.order))
	}
	return nil
}

func validateGeneratedTarget(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("codegen: inspect output %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("codegen: refusing symlink output %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("codegen: output %s is not a regular generated file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("codegen: inspect output %s: %w", path, err)
	}
	prefix := make([]byte, len(generatedHeader)+1)
	_, readErr := io.ReadFull(f, prefix)
	closeErr := f.Close()
	if readErr != nil || string(prefix) != generatedHeader+"\n" {
		return fmt.Errorf("codegen: refusing to overwrite authored file %s; choose another -out or remove it explicitly", path)
	}
	if closeErr != nil {
		return fmt.Errorf("codegen: inspect output %s: %w", path, closeErr)
	}
	return nil
}

func writeGenerated(path string, source []byte) error {
	return writeArtifact(path, source, validateGeneratedTarget)
}

func writeArtifact(path string, source []byte, validate func(string) error) (err error) {
	if err := validate(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("codegen: create temporary output beside %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("codegen: set output mode: %w", err)
	}
	if _, err := tmp.Write(source); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("codegen: write temporary output: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("codegen: sync temporary output: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("codegen: close temporary output: %w", err)
	}

	if err := validate(path); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("codegen: atomically replace %s: %w", path, err)
	}
	committed = true
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("codegen: open output directory for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return fmt.Errorf("codegen: sync output directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("codegen: close output directory: %w", closeErr)
	}
	return nil
}

func (this *generator) load(skip string) error {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, this.dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go") && filepath.Base(skip) != fi.Name()
	}, parser.ParseComments)
	if err != nil {
		return err
	}
	this.models = map[string]*model{}
	this.imports = map[string]string{}
	this.structs = map[string]bool{}
	this.embeds = map[string]*ast.StructType{}
	this.embedFiles = map[string]*ast.File{}
	this.mirrorProblems = map[string]bool{}
	this.fileImports = map[*ast.File]map[string]string{}
	this.sourceImports = nil
	this.declaredNames = map[string]bool{}
	this.pathAliases = map[string]string{}
	this.aliasPaths = map[string]string{}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				if ts, ok := n.(*ast.TypeSpec); ok {
					if st, isStruct := ts.Type.(*ast.StructType); isStruct {
						this.structs[ts.Name.Name] = true
						this.embeds[ts.Name.Name] = st
						this.embedFiles[ts.Name.Name] = file
					}
				}
				return true
			})
		}
	}
	if err := this.prepareImports(pkgs); err != nil {
		return err
	}
	this.prepareTypes(fset, pkgs)

	packageNames := make([]string, 0, len(pkgs))
	for name := range pkgs {
		packageNames = append(packageNames, name)
	}
	sort.Strings(packageNames)
	for _, name := range packageNames {
		pkg := pkgs[name]
		this.pkg = name
		fileNames := make([]string, 0, len(pkg.Files))
		for fileName := range pkg.Files {
			fileNames = append(fileNames, fileName)
		}
		sort.Strings(fileNames)
		for _, fileName := range fileNames {
			file := pkg.Files[fileName]
			modelFile := preferredModelFile(filepath.Base(fileName))
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				if !ts.Name.IsExported() {
					return true
				}
				if this.only != nil && !this.only[ts.Name.Name] {
					return true
				}
				m := this.parseModel(ts.Name.Name, st, modelFile, file)
				if m != nil {
					this.models[m.Name] = m
					this.order = append(this.order, m.Name)
				}
				return true
			})
		}
	}
	if len(this.mirrorProblems) > 0 {
		problems := make([]string, 0, len(this.mirrorProblems))
		for problem := range this.mirrorProblems {
			problems = append(problems, problem)
		}
		sort.Strings(problems)
		return fmt.Errorf("codegen: model fields cannot be mirrored safely:\n- %s", strings.Join(problems, "\n- "))
	}
	sort.Strings(this.order)
	return nil
}

type sourceImport struct {
	file      *ast.File
	fileName  string
	qualifier string
	path      string
}

func (this *generator) prepareImports(pkgs map[string]*ast.Package) error {
	packageNames := make([]string, 0, len(pkgs))
	for name := range pkgs {
		packageNames = append(packageNames, name)
	}
	sort.Strings(packageNames)
	if len(packageNames) == 0 {
		return fmt.Errorf("codegen: no package declaration in %s", this.dir)
	}
	inputPackage := packageNames[0]

	used := map[string]bool{
		"context": true, "http": true, "time": true,
		"crud": true, "utils": true, "specs": true, "sqlrepo": true,
		"port": true, "errs": true, "crudnet": true, "gorm": true,
	}
	this.usedAliases = used
	this.reserveGeneratedAndPackageNames(pkgs, used)
	for _, fixed := range []struct{ alias, path string }{
		{"crud", this.crudPkg}, {"utils", this.utilsPkg}, {"specs", this.specsPkg},
		{"port", this.portPkg}, {"errs", this.errsPkg}, {"crudnet", this.netPkg},
		{"context", "context"}, {"http", "net/http"}, {"time", "time"},
		{"sqlrepo", "github.com/frostgrove/vv/crud/sqlrepo"}, {"gorm", "gorm.io/gorm"},
	} {
		if fixed.path == "" || this.aliasPaths[fixed.alias] != "" || this.pathAliases[fixed.path] != "" {
			continue
		}
		this.aliasPaths[fixed.alias] = fixed.path
		this.pathAliases[fixed.path] = fixed.alias
	}
	if this.modelImport != "" {
		this.modelAlias = this.pathAliases[this.modelImport]
		if this.modelAlias == "" {
			this.modelAlias = allocateReadableImportAlias(inputPackage, this.modelImport, used, false)
			this.aliasPaths[this.modelAlias] = this.modelImport
		}
	}

	var records []sourceImport
	preferred := map[string][]string{}
	for _, packageName := range packageNames {
		pkg := pkgs[packageName]
		files := make([]string, 0, len(pkg.Files))
		for fileName := range pkg.Files {
			files = append(files, fileName)
		}
		sort.Strings(files)
		for _, fileName := range files {
			file := pkg.Files[fileName]
			prefixes := selectorPrefixes(file)
			for _, imp := range file.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return fmt.Errorf("codegen: import in %s: %w", fileName, err)
				}
				qualifier := ""
				if imp.Name != nil {
					qualifier = imp.Name.Name
					if qualifier == "_" {
						continue
					}
					if qualifier == "." {
						return fmt.Errorf("codegen: dot import %q in %s cannot be reproduced safely in one generated file; use an explicit import alias", path, fileName)
					}
				} else {
					qualifier = filepath.Base(path)

					if path == "C" {
						qualifier = "C"
					} else if declared := this.importPackageName(path); declared != "" {
						qualifier = declared
					} else if !prefixes[qualifier] || this.declaredNames[qualifier] {
						return fmt.Errorf("codegen: import %q in %s cannot safely use unresolved basename %q; make the import alias explicit or make its declared package resolvable", path, fileName, qualifier)
					}
				}
				records = append(records, sourceImport{file: file, fileName: fileName, qualifier: qualifier, path: path})
				preferred[path] = append(preferred[path], qualifier)
			}
		}
	}

	paths := make([]string, 0, len(preferred))
	for path := range preferred {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	preferredCounts := make(map[string]int, len(paths))
	for _, path := range paths {
		names := preferred[path]
		sort.Strings(names)
		if len(names) > 0 {
			preferredCounts[names[0]]++
		}
	}
	pathAlias := make(map[string]string, len(paths))
	for _, path := range paths {
		names := preferred[path]
		sort.Strings(names)
		alias := this.sharedImportAlias(path)
		if owner := this.aliasPaths[alias]; alias != "" && owner != "" && owner != path {
			alias = ""
		}
		if alias == "" {
			alias = allocateReadableImportAlias(names[0], path, used, preferredCounts[names[0]] > 1)
		}
		pathAlias[path] = alias
		this.pathAliases[path] = alias
		this.aliasPaths[alias] = path
		this.imports[alias] = path
	}
	for _, record := range records {
		aliases := this.fileImports[record.file]
		if aliases == nil {
			aliases = map[string]string{}
			this.fileImports[record.file] = aliases
		}
		aliases[record.qualifier] = pathAlias[record.path]
	}
	this.sourceImports = append(this.sourceImports, records...)
	return nil
}

func (this *generator) reserveGeneratedAndPackageNames(pkgs map[string]*ast.Package, used map[string]bool) {
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil {
						used[d.Name.Name] = true
						this.declaredNames[d.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							used[s.Name.Name] = true
							this.declaredNames[s.Name.Name] = true
							for _, name := range []string{
								s.Name.Name + "Update", s.Name.Name + "Attrs", s.Name.Name + "Repo",
								s.Name.Name + "Repository", "New" + s.Name.Name + "Repository",
								s.Name.Name + "Input", s.Name.Name + "Mapper", s.Name.Name + "Paths",
								s.Name.Name + "Service", "New" + s.Name.Name + "Service",
								"Mount" + s.Name.Name, s.Name.Name + "_",
							} {
								used[name] = true
							}
						case *ast.ValueSpec:
							for _, name := range s.Names {
								used[name.Name] = true
								this.declaredNames[name.Name] = true
							}
						}
					}
				}
			}
		}
	}
	this.reserveRelationAttrNames(used)
}

func (this *generator) reserveRelationAttrNames(used map[string]bool) {
	if this.depth <= 1 {
		return
	}
	type edge struct{ name, target string }
	edges := map[string][]edge{}
	for owner, structure := range this.embeds {
		for _, item := range structure.Fields.List {
			base, _ := relElem(exprString(item.Type))
			base = strings.TrimPrefix(base, "*")
			if !this.structs[base] {
				continue
			}
			if len(item.Names) == 0 {
				if name := embeddedSyntaxName(item.Type); name != "" {
					edges[owner] = append(edges[owner], edge{name: name, target: base})
				}
				continue
			}
			for _, name := range item.Names {
				if name.IsExported() {
					edges[owner] = append(edges[owner], edge{name: name.Name, target: base})
				}
			}
		}
		sort.Slice(edges[owner], func(i, j int) bool {
			if edges[owner][i].name != edges[owner][j].name {
				return edges[owner][i].name < edges[owner][j].name
			}
			return edges[owner][i].target < edges[owner][j].target
		})
	}

	var walk func(root, on, suffix string, level int, path map[string]bool)
	walk = func(root, on, suffix string, level int, path map[string]bool) {
		if level+1 >= this.depth {
			return
		}
		for _, relation := range edges[on] {
			if path[relation.target] {
				continue
			}
			nextSuffix := suffix + relation.name
			used[root+nextSuffix+"Attrs"] = true
			path[relation.target] = true
			walk(root, relation.target, nextSuffix, level+1, path)
			delete(path, relation.target)
		}
	}
	for root := range this.embeds {
		walk(root, root, "", 0, map[string]bool{root: true})
	}
}

func (this *generator) validateDeclarations(outPath string) error {
	emitted := map[string]string{}
	add := func(declaration, owner string) error {
		if previous := emitted[declaration]; previous != "" {
			return fmt.Errorf("codegen: generated declaration %s for %s collides with the declaration for %s", declaration, owner, previous)
		}
		emitted[declaration] = owner
		return nil
	}
	for _, name := range this.order {
		model := this.models[name]
		if this.withDTO {
			if err := add(model.Name+"Update", "model "+model.Name+" update DTO"); err != nil {
				return err
			}
		}
		if this.withMeta {
			if err := add(model.Name+"Attrs", "model "+model.Name+" metamodel"); err != nil {
				return err
			}
			if err := add(model.Name+"_", "model "+model.Name+" metamodel value"); err != nil {
				return err
			}
			if err := this.collectRelationAttrNames(emitted, model, model, "", 0, map[string]bool{model.Name: true}, nil); err != nil {
				return err
			}
		}
		if this.withRepo {
			for _, declaration := range []string{model.Name + "Repo", model.Name + "Repository", "New" + model.Name + "Repository"} {
				if err := add(declaration, "model "+model.Name+" repository"); err != nil {
					return err
				}
			}
		}
		if this.wireOnly {
			for _, declaration := range []string{
				model.Name + "Input", model.Name + "InputMapper",
				model.Name + "Patch", model.Name + "PatchMapper",
				model.Name + "Response", model.Name + "Presenter",
			} {
				if err := add(declaration, "model "+model.Name+" wire bodies"); err != nil {
					return err
				}
			}
		}
		if this.adapter {
			for _, declaration := range []string{
				model.Name + "Input", model.Name + "Mapper", model.Name + "Paths",
				model.Name + "Service", "New" + model.Name + "Service",
			} {
				if err := add(declaration, "model "+model.Name+" adapter"); err != nil {
					return err
				}
			}
			if this.binding == "net" {
				if err := add("Mount"+model.Name, "model "+model.Name+" net binding"); err != nil {
					return err
				}
			}
		}
	}
	authored := this.declaredNames
	if this.into != "" {
		var err error
		authored, err = packageDeclarationNames(this.into, this.pkg, filepath.Base(outPath))
		if err != nil {
			return err
		}
		imports, err := this.packageImportAliases(this.into, this.pkg, filepath.Base(outPath))
		if err != nil {
			return err
		}
		for _, imported := range imports {
			if emitted[imported.qualifier] != "" {
				return fmt.Errorf("codegen: import alias %q for %q in %s conflicts with generated declaration %s; rename the destination import", imported.qualifier, imported.path, imported.fileName, imported.qualifier)
			}
		}
	}
	emittedNames := make([]string, 0, len(emitted))
	for declaration := range emitted {
		emittedNames = append(emittedNames, declaration)
	}
	sort.Strings(emittedNames)
	for _, declaration := range emittedNames {
		if authored[declaration] {
			return fmt.Errorf("codegen: package declaration %s conflicts with generated declaration %s; rename the authored declaration or select a different generated artefact", declaration, declaration)
		}
	}

	if this.into != "" {
		return nil
	}
	sort.Slice(this.sourceImports, func(i, j int) bool {
		if this.sourceImports[i].fileName != this.sourceImports[j].fileName {
			return this.sourceImports[i].fileName < this.sourceImports[j].fileName
		}
		if this.sourceImports[i].qualifier != this.sourceImports[j].qualifier {
			return this.sourceImports[i].qualifier < this.sourceImports[j].qualifier
		}
		return this.sourceImports[i].path < this.sourceImports[j].path
	})
	for _, imported := range this.sourceImports {
		reason := ""
		switch {
		case emitted[imported.qualifier] != "":
			reason = "generated declaration"
		case authored[imported.qualifier]:
			reason = "package declaration"
		}
		if reason != "" {
			return fmt.Errorf("codegen: import alias %q for %q in %s conflicts with %s %s; rename the source import", imported.qualifier, imported.path, imported.fileName, reason, imported.qualifier)
		}
	}
	return nil
}

func (this *generator) validateRenderedImports(outPath string, source []byte) error {
	authored := this.declaredNames
	if this.into != "" {
		var err error
		authored, err = packageDeclarationNames(this.into, this.pkg, filepath.Base(outPath))
		if err != nil {
			return err
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), outPath, source, 0)
	if err != nil {
		return fmt.Errorf("codegen: inspect rendered imports: %w", err)
	}
	generated := map[string]bool{}
	for _, declaration := range file.Decls {
		switch item := declaration.(type) {
		case *ast.FuncDecl:
			if item.Recv == nil {
				generated[item.Name.Name] = true
			}
		case *ast.GenDecl:
			if item.Tok == token.IMPORT {
				continue
			}
			for _, raw := range item.Specs {
				switch spec := raw.(type) {
				case *ast.TypeSpec:
					generated[spec.Name.Name] = true
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						generated[name.Name] = true
					}
				}
			}
		}
	}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return fmt.Errorf("codegen: inspect rendered import: %w", err)
		}
		alias := filepath.Base(path)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		switch {
		case generated[alias]:
			return fmt.Errorf("codegen: generated import alias %q for %q conflicts with generated declaration %s; rename the source package alias or select generated artefacts that do not need this import", alias, path, alias)
		case authored[alias]:
			return fmt.Errorf("codegen: generated import alias %q for %q conflicts with package declaration %s; rename the declaration or select generated artefacts that do not need this import", alias, path, alias)
		}
	}
	return nil
}

func (this *generator) packageImportAliases(dir, packageName, skip string) ([]sourceImport, error) {
	packages, err := parser.ParseDir(token.NewFileSet(), dir, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go") && info.Name() != skip
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("codegen: inspect output package imports: %w", err)
	}
	parsed := packages[packageName]
	if parsed == nil {
		return nil, nil
	}
	declarations, err := packageDeclarationNames(dir, packageName, skip)
	if err != nil {
		return nil, err
	}
	fileNames := make([]string, 0, len(parsed.Files))
	for fileName := range parsed.Files {
		fileNames = append(fileNames, fileName)
	}
	sort.Strings(fileNames)
	var out []sourceImport
	for _, fileName := range fileNames {
		file := parsed.Files[fileName]
		prefixes := selectorPrefixes(file)
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("codegen: import in %s: %w", fileName, err)
			}
			qualifier := ""
			if spec.Name != nil {
				qualifier = spec.Name.Name
				switch qualifier {
				case "_":
					continue
				case ".":
					return nil, fmt.Errorf("codegen: dot import %q in destination file %s cannot be checked safely against generated declarations; use an explicit import alias", path, fileName)
				}
			} else {
				qualifier = filepath.Base(path)
				if path == "C" {
					qualifier = "C"
				} else if declared := this.importPackageNameAt(dir, path); declared != "" {
					qualifier = declared
				} else if !prefixes[qualifier] || declarations[qualifier] {
					return nil, fmt.Errorf("codegen: import %q in destination file %s cannot safely use unresolved basename %q; make the import alias explicit or make its declared package resolvable", path, fileName, qualifier)
				}
			}
			out = append(out, sourceImport{file: file, fileName: fileName, qualifier: qualifier, path: path})
		}
	}
	return out, nil
}

func packageDeclarationNames(dir, packageName, skip string) (map[string]bool, error) {
	packages, err := parser.ParseDir(token.NewFileSet(), dir, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go") && info.Name() != skip
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("codegen: inspect output package declarations: %w", err)
	}
	out := map[string]bool{}
	parsed := packages[packageName]
	if parsed == nil {
		return out, nil
	}
	for _, file := range parsed.Files {
		for _, declaration := range file.Decls {
			switch item := declaration.(type) {
			case *ast.FuncDecl:
				if item.Recv == nil {
					out[item.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, raw := range item.Specs {
					switch spec := raw.(type) {
					case *ast.TypeSpec:
						out[spec.Name.Name] = true
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							out[name.Name] = true
						}
					}
				}
			}
		}
	}
	return out, nil
}

func (this *generator) collectRelationAttrNames(out map[string]string, root, on *model, suffix string, level int, path map[string]bool, names []string) error {
	for _, item := range on.Fields {
		if !item.isRelation() || level+1 >= this.depth {
			continue
		}
		target := item.RelTarget
		if target == "" {
			target, _ = relElem(item.Type)
			if this.modelAlias != "" {
				target = strings.TrimPrefix(target, this.modelAlias+".")
			}
		}
		related := this.models[target]
		if related == nil || path[target] {
			continue
		}
		nextSuffix := suffix + item.Name
		nextNames := append(append([]string(nil), names...), item.Name)
		declaration := root.Name + nextSuffix + "Attrs"
		owner := "model " + root.Name + " relation " + strings.Join(nextNames, ".")
		if previous := out[declaration]; previous != "" {
			return fmt.Errorf("codegen: generated declaration %s for %s collides with the declaration for %s", declaration, owner, previous)
		}
		out[declaration] = owner
		path[target] = true
		if err := this.collectRelationAttrNames(out, root, related, nextSuffix, level+1, path, nextNames); err != nil {
			return err
		}
		delete(path, target)
	}
	return nil
}

func (this *generator) sharedImportAlias(path string) string {
	switch {
	case path != "" && path == this.crudPkg:
		return "crud"
	case path != "" && path == this.utilsPkg:
		return "utils"
	case path != "" && path == this.specsPkg:
		return "specs"
	case path != "" && path == this.portPkg:
		return "port"
	case path != "" && path == this.errsPkg:
		return "errs"
	case path != "" && path == this.netPkg:
		return "crudnet"
	}
	switch path {
	case "context":
		return "context"
	case "net/http":
		return "http"
	case "time":
		return "time"
	case DefaultCrudPkg:
		return "crud"
	case DefaultUtilsPkg:
		return "utils"
	case DefaultSpecsPkg:
		return "specs"
	case "github.com/frostgrove/vv/crud/sqlrepo":
		return "sqlrepo"
	case "gorm.io/gorm":
		return "gorm"
	}
	return ""
}

func allocateReadableImportAlias(preferred, path string, used map[string]bool, forcePath bool) string {
	preferred = strings.TrimSpace(preferred)
	if !forcePath && usableImportAlias(preferred, used) {
		used[preferred] = true
		return preferred
	}
	candidates := pathImportAliases(path)
	start := 0
	if forcePath && len(candidates) > 1 {
		start = 1
	}
	for _, candidate := range candidates[start:] {
		if usableImportAlias(candidate, used) {
			used[candidate] = true
			return candidate
		}
	}
	base := "pkg"
	if len(candidates) > 0 {
		base = candidates[len(candidates)-1]
	}
	for candidate := "imported" + upperFirst(base); ; candidate = "imported" + upperFirst(candidate) {
		if usableImportAlias(candidate, used) {
			used[candidate] = true
			return candidate
		}
	}
}

func usableImportAlias(alias string, used map[string]bool) bool {
	return alias != "" && alias != "_" && alias != "." && !token.Lookup(alias).IsKeyword() && !used[alias]
}

func pathImportAliases(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if len(part) > 1 && part[0] == 'v' {
			if _, err := strconv.Atoi(part[1:]); err == nil {
				continue
			}
		}
		if clean := importAliasPart(part); clean != "" {
			parts = append(parts, clean)
		}
	}
	if len(parts) == 0 {
		return []string{"pkg"}
	}
	out := make([]string, 0, len(parts))
	for width := 1; width <= len(parts); width++ {
		start := len(parts) - width
		var b strings.Builder
		b.WriteString(parts[start])
		for _, part := range parts[start+1:] {
			b.WriteString(upperFirst(part))
		}
		candidate := b.String()
		if len(out) == 0 || out[len(out)-1] != candidate {
			out = append(out, candidate)
		}
	}
	return out
}

func importAliasPart(part string) string {
	var b strings.Builder
	upper := false
	for _, r := range part {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			if b.Len() == 0 {
				if r >= 'A' && r <= 'Z' {
					r += 'a' - 'A'
				}
				b.WriteRune(r)
			} else if upper && r >= 'a' && r <= 'z' {
				b.WriteRune(r - ('a' - 'A'))
			} else {
				b.WriteRune(r)
			}
			upper = false
		case r >= '0' && r <= '9':
			if b.Len() == 0 {
				b.WriteByte('p')
			}
			b.WriteRune(r)
			upper = false
		default:
			upper = b.Len() > 0
		}
	}
	return b.String()
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-('a'-'A')) + s[1:]
	}
	return s
}

func selectorPrefixes(file *ast.File) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := selector.X.(*ast.Ident); ok && ident.Obj == nil {
			out[ident.Name] = true
		}
		return true
	})
	return out
}

func (this *generator) importPackageName(path string) string {
	return this.importPackageNameAt(this.dir, path)
}

func (this *generator) importPackageNameAt(dir, path string) string {
	directories := []string{dir}
	if working, err := os.Getwd(); err == nil && working != "" && working != dir {
		directories = append(directories, working)
	}
	for _, working := range directories {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "go", "list", "-mod=readonly", "-json", "-find", path)
		cmd.Dir = working
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		var listed struct{ Name string }
		if json.Unmarshal(out, &listed) == nil && listed.Name != "" {
			return listed.Name
		}
	}
	return ""
}

func (this *generator) typeString(expr ast.Expr, file *ast.File) string {
	if typ := this.goType(expr); typ != nil {
		if rendered := this.renderedType(typ); rendered != "" {
			return rendered
		}
	}
	aliases := this.fileImports[file]
	if len(aliases) == 0 {
		return exprString(expr)
	}
	type edit struct {
		ident *ast.Ident
		name  string
	}
	var edits []edit
	ast.Inspect(expr, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		alias, known := aliases[ident.Name]
		if !known || alias == ident.Name {
			return true
		}
		edits = append(edits, edit{ident: ident, name: ident.Name})
		ident.Name = alias
		return true
	})
	typ := exprString(expr)
	for i := len(edits) - 1; i >= 0; i-- {
		edits[i].ident.Name = edits[i].name
	}
	return typ
}

func (this *generator) parseModel(name string, st *ast.StructType, force bool, file *ast.File) *model {
	m := &model{Name: name}
	var mirrorProblems []string

	tagged := force || (this.only != nil && this.only[name])
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			var tag reflect.StructTag
			if f.Tag != nil {
				raw, _ := strconv.Unquote(f.Tag.Value)
				tag = reflect.StructTag(raw)
			}
			database, hasDB := tag.Lookup("db")
			relation, hasRel := tag.Lookup("rel")
			if hasDB && database == "-" {
				continue
			}
			typ := strings.TrimSpace(this.typeString(f.Type, file))
			resolved := this.goType(f.Type)
			if resolved != nil {
				if !hasDB && !hasRel && this.flattenableStruct(resolved) {
					if isPointerSource(resolved) {
						mirrorProblems = append(mirrorProblems, fmt.Sprintf(
							"model %s embeds pointer %s; runtime metadata refuses embedded pointer structs, so embed a value or tag it db:\"-\"", name, typ))
						continue
					}
					fields, problems := this.flattenType(name, typ, resolved, map[types.Type]bool{})
					m.Fields = append(m.Fields, fields...)
					mirrorProblems = append(mirrorProblems, problems...)
					tagged = tagged || len(fields) > 0
					continue
				}
				fieldName := this.anonymousName(f)
				if fieldName == "" {
					mirrorProblems = append(mirrorProblems, fmt.Sprintf("model %s has an anonymous %s whose field name cannot be resolved", name, typ))
					continue
				}
				if !ast.IsExported(fieldName) {
					if hasDB {
						mirrorProblems = append(mirrorProblems, fmt.Sprintf(
							"model %s maps unexported anonymous field %s; rename it or tag it db:\"-\"", name, fieldName))
					}
					continue
				}
				if problem := this.appendResolvedField(&m.Fields, name, fieldName, typ, resolved, database, hasDB, relation, hasRel); problem != "" {
					mirrorProblems = append(mirrorProblems, problem)
				}
				tagged = tagged || hasDB || hasRel
				continue
			}

			if !hasDB && !hasRel {
				if strings.HasPrefix(typ, "*") {
					mirrorProblems = append(mirrorProblems, fmt.Sprintf(
						"model %s embeds pointer %s; runtime metadata refuses embedded pointer structs, so embed a value or tag it db:\"-\"", name, typ))
					continue
				}
				if fields, ok := this.embedded(typ); ok {
					m.Fields = append(m.Fields, fields...)
					tagged = tagged || len(fields) > 0
					continue
				}
			}
			mirrorProblems = append(mirrorProblems, fmt.Sprintf(
				"model %s embeds unresolved type %s; resolve its package so vv can classify it, or tag it db:\"-\"", name, typ))
			continue
		}
		var tag reflect.StructTag
		if f.Tag != nil {
			raw, _ := strconv.Unquote(f.Tag.Value)
			tag = reflect.StructTag(raw)
		}
		database, hasDB := tag.Lookup("db")
		rel, hasRel := tag.Lookup("rel")
		if hasDB || hasRel {
			tagged = true
		}
		for _, ident := range f.Names {
			if !ident.IsExported() {
				if hasDB && database != "-" {
					mirrorProblems = append(mirrorProblems, fmt.Sprintf(
						"model %s maps unexported field %s; rename it or tag it db:\"-\"", name, ident.Name))
				}
				continue
			}
			rendered := this.typeString(f.Type, file)
			if resolved := this.goType(f.Type); resolved != nil {
				if problem := this.appendResolvedField(&m.Fields, name, ident.Name, rendered, resolved, database, hasDB, rel, hasRel); problem != "" {
					mirrorProblems = append(mirrorProblems, problem)
				}
				continue
			}
			fl := field{Name: ident.Name, Type: rendered, Tag: database, Rel: rel, HasRel: hasRel}
			if hasDB && database == "-" {
				continue
			}

			if !hasRel {
				if base, _ := relElem(fl.Type); this.structs[base] {
					continue
				}
			}
			if hasRel && rel != "-" {
				if this.skip[ident.Name] {
					continue
				}
				m.Fields = append(m.Fields, fl)
				continue
			}
			if database == "-" || (hasRel && rel == "-") {
				fl.Skip = true
			}
			parsed := this.columnField(fl.Name, fl.Type, nil, database)
			parsed.Rel, parsed.HasRel, parsed.Skip = fl.Rel, fl.HasRel, fl.Skip
			fl = parsed

			this.exclude(&fl)
			m.Fields = append(m.Fields, fl)
		}
	}
	if !tagged {
		return nil
	}
	mirrorProblems = append(mirrorProblems, resolvePrimaryKey(m)...)
	mirrorProblems = append(mirrorProblems, validateEffectiveFields(m)...)
	for _, problem := range mirrorProblems {
		this.mirrorProblems[problem] = true
	}
	return m
}

func resolvePrimaryKey(m *model) []string {
	if m == nil {
		return nil
	}
	var explicit []int
	for index := range m.Fields {
		if m.Fields[index].ExplicitPK && primaryKeyColumn(m.Fields[index]) {
			explicit = append(explicit, index)
		}

		m.Fields[index].PK = false
	}
	if len(explicit) > 1 {
		names := make([]string, 0, len(explicit))
		for _, index := range explicit {
			names = append(names, m.Fields[index].Name)
		}
		sort.Strings(names)
		return []string{fmt.Sprintf("model %s has multiple primary keys %s; composite primary keys are not supported", m.Name, strings.Join(names, ", "))}
	}

	chosen := -1
	if len(explicit) == 1 {
		chosen = explicit[0]
	} else {
		for index := range m.Fields {
			if primaryKeyColumn(m.Fields[index]) && m.Fields[index].Name == "ID" {
				chosen = index
				break
			}
		}
		if chosen < 0 {
			for index := range m.Fields {
				if !primaryKeyColumn(m.Fields[index]) {
					continue
				}
				if effectiveColumn(m.Fields[index]) == "ID" {
					chosen = index
					break
				}
			}
		}
		if chosen < 0 {
			for index := range m.Fields {
				if primaryKeyColumn(m.Fields[index]) && effectiveColumn(m.Fields[index]) == "id" {
					chosen = index
					break
				}
			}
		}
	}
	if chosen < 0 {
		return nil
	}
	key := &m.Fields[chosen]
	key.PK = true
	if key.Integral && !key.Auto && !key.NoAuto {
		key.Auto = true
	}
	return nil
}

func primaryKeyColumn(item field) bool {
	return item.Tag != "-" && !item.isRelation()
}

func effectiveColumn(item field) string {
	column := strings.TrimSpace(strings.Split(item.Tag, ",")[0])
	if column == "" {
		return codegenSnake(item.Name)
	}
	return column
}

func validateEffectiveFields(m *model) []string {
	names := map[string]bool{}
	columns := map[string]bool{}
	var problems []string
	for _, item := range m.Fields {
		if item.Tag == "-" {
			continue
		}
		if names[item.Name] {
			problems = append(problems, fmt.Sprintf("model %s has duplicate effective field name %s after embedding/flattening", m.Name, item.Name))
		} else {
			names[item.Name] = true
		}
		if item.isRelation() {
			continue
		}
		column := effectiveColumn(item)
		if columns[column] {
			problems = append(problems, fmt.Sprintf("model %s has duplicate effective database column %s after embedding/flattening", m.Name, column))
		} else {
			columns[column] = true
		}
	}
	return problems
}

func codegenSnake(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	runes := []rune(s)
	for index, current := range runes {
		if unicode.IsUpper(current) {
			previousLower := index > 0 && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]))
			nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if index > 0 && (previousLower || nextLower) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(current))
			continue
		}
		b.WriteRune(current)
	}
	return b.String()
}

func (this *generator) exclude(f *field) {
	skipped, readonly := this.skip[f.Name], this.readonly[f.Name]
	if !skipped && !readonly {
		return
	}
	if !f.tagDropped() {
		f.Excluded = true
	}
	f.Skip = f.Skip || skipped
	f.Immutable = f.Immutable || readonly
}

func exprString(e ast.Expr) string {
	var b bytes.Buffer
	if err := format.Node(&b, token.NewFileSet(), e); err != nil {
		return ""
	}
	return b.String()
}

func (this field) isRelation() bool { return this.HasRel && this.Rel != "-" }

func elem(typ string) (string, bool) {
	typ = strings.TrimSpace(typ)
	for _, name := range []string{"utils.Opt", "crud.Opt"} {
		if inner, ok := cutGeneric(typ, name); ok {
			return inner, true
		}
	}
	if strings.HasPrefix(typ, "*") {
		return strings.TrimPrefix(typ, "*"), true
	}
	return typ, false
}

func cutGeneric(typ, name string) (string, bool) {
	if !strings.HasPrefix(typ, name+"[") || !strings.HasSuffix(typ, "]") {
		return "", false
	}
	return typ[len(name)+1 : len(typ)-1], true
}

func relElem(typ string) (string, bool) {
	slice := strings.HasPrefix(typ, "[]")
	typ = strings.TrimPrefix(typ, "[]")
	typ = strings.TrimPrefix(typ, "*")
	return typ, slice
}

var ordered = map[string]bool{
	"string": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
}

func attrType(specsPkg, model, typ string) string {
	e, _ := elem(typ)
	switch {
	case e == "string":
		return fmt.Sprintf("%s.Str[%s]", specsPkg, model)
	case e == "time.Time":
		return fmt.Sprintf("%s.Cmp[%s, time.Time]", specsPkg, model)
	case ordered[e]:
		return fmt.Sprintf("%s.Ord[%s, %s]", specsPkg, model, e)
	default:
		return fmt.Sprintf("%s.Attr[%s, %s]", specsPkg, model, e)
	}
}

func dtoType(typ string) string {
	e, nullable := elem(typ)
	if nullable {
		return "utils.Opt[" + e + "]"
	}
	return "*" + e
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}

	i := 0
	for i < len(s) && s[i] >= 'A' && s[i] <= 'Z' {
		i++
	}
	if i > 1 && i < len(s) {
		i--
	}
	return strings.ToLower(s[:i]) + s[i:]
}

func (this *generator) qual(name string) string {
	if this.modelAlias == "" {
		return name
	}
	return this.modelAlias + "." + name
}

func packageNameOf(dir string) string {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.PackageClauseOnly)
	if err == nil {
		for name := range pkgs {
			return name
		}
	}
	return strings.ReplaceAll(filepath.Base(dir), "-", "")
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func names(csv string) map[string]bool {
	out := map[string]bool{}
	for _, n := range strings.Split(csv, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out[n] = true
		}
	}
	return out
}

var wellKnownEmbeds = map[string][]field{
	"gorm.Model": {
		{Name: "ID", Type: "uint", Integral: true},
		{Name: "CreatedAt", Type: "time.Time", Immutable: true, Excluded: true},
		{Name: "UpdatedAt", Type: "time.Time", Immutable: true, Excluded: true},
		{Name: "DeletedAt", Type: "gorm.DeletedAt", ServerOwned: true, Tombstone: true, Excluded: true},
	},
}

func (this *generator) embedded(typ string) ([]field, bool) {
	typ = strings.TrimPrefix(strings.TrimSpace(typ), "*")
	if fields, ok := wellKnownEmbeds[typ]; ok {
		out := make([]field, 0, len(fields))
		for _, f := range fields {
			this.exclude(&f)
			out = append(out, f)
		}
		return out, true
	}
	if st, ok := this.embeds[typ]; ok {
		m := this.parseModel(typ, st, true, this.embedFiles[typ])
		if m == nil {
			return nil, false
		}
		return m.Fields, true
	}
	return nil, false
}

type Options struct {
	Dir      string
	Out      string
	Into     string
	Types    string
	Skip     string
	Readonly string
	Import   string
	Depth    int
	WithDTO  bool
	WithMeta bool

	NoRepo bool

	Recursive bool
	Check     bool
	Adapter   bool
	Binding   string
	SpecsPkg  string
	CrudPkg   string
	UtilsPkg  string

	PortPkg string
	ErrsPkg string
	NetPkg  string

	Log io.Writer
}

const (
	DefaultCrudPkg  = "github.com/frostgrove/vv/crud"
	DefaultSpecsPkg = "github.com/frostgrove/vv/crud/decorators/specs"
	DefaultPortPkg  = "github.com/frostgrove/vv/port"
	DefaultErrsPkg  = "github.com/frostgrove/vv/errs"
	DefaultNetPkg   = "github.com/frostgrove/vv/crud/http/crudnet"
	DefaultUtilsPkg = "github.com/frostgrove/vv/utils"
)

func Run(o *Options) error {
	if o == nil {
		return fmt.Errorf("codegen: options are nil")
	}
	outName := o.Out
	if outName == "" {
		outName = "vv_gen.go"
	}
	if filepath.IsAbs(outName) || filepath.Base(outName) != outName || outName == "." || outName == ".." {
		return fmt.Errorf("codegen: -out must be a file name without directories, got %q; use -into to select the output directory", outName)
	}
	if o.Recursive {
		if o.Into != "" || o.Import != "" {
			return fmt.Errorf("-recursive writes beside each model package and cannot be combined with -into or -import")
		}
		dirs, err := modelDirs(o.Dir)
		if err != nil {
			return err
		}
		var stale []string
		for _, dir := range dirs {
			one := *o
			one.Dir = dir
			one.Out = outName
			one.Recursive = false
			err := Run(&one)
			if err == nil {
				continue
			}
			var drift *DriftError
			if errors.As(err, &drift) {
				stale = append(stale, drift.Paths...)
				continue
			}
			if strings.Contains(err.Error(), "no models found in ") {
				continue
			}
			return err
		}
		if len(stale) != 0 {
			sort.Strings(stale)
			return &DriftError{Paths: stale}
		}
		return nil
	}

	binding := o.Binding
	if binding == "" {
		binding = "net"
	}
	if binding != "net" && binding != "none" {
		return fmt.Errorf("-binding %s: only net and none are generated today", binding)
	}
	g := &generator{
		dir:      o.Dir,
		depth:    o.Depth,
		withDTO:  o.WithDTO,
		withMeta: o.WithMeta,
		withRepo: !o.NoRepo,
		adapter:  o.Adapter,
		check:    o.Check,
		binding:  binding,
		specsPkg: cmpOr(o.SpecsPkg, DefaultSpecsPkg),
		crudPkg:  cmpOr(o.CrudPkg, DefaultCrudPkg),
		utilsPkg: cmpOr(o.UtilsPkg, DefaultUtilsPkg),
		portPkg:  cmpOr(o.PortPkg, DefaultPortPkg),
		errsPkg:  cmpOr(o.ErrsPkg, DefaultErrsPkg),
		netPkg:   cmpOr(o.NetPkg, DefaultNetPkg),
		into:     o.Into,
		skip:     names(o.Skip),
		readonly: names(o.Readonly),
		log:      o.Log,
	}
	if o.Types != "" {
		g.only = map[string]bool{}
		for _, n := range strings.Split(o.Types, ",") {
			g.only[strings.TrimSpace(n)] = true
		}
	}
	g.modelImport = o.Import
	outDir := o.Dir
	if o.Into != "" {
		outDir = o.Into
	}
	outPath, err := containedOutputPath(outDir, outName)
	if err != nil {
		return err
	}
	return g.run(outPath)
}

func containedOutputPath(dir, name string) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("codegen: resolve output directory %s: %w", dir, err)
	}
	target, err := filepath.Abs(filepath.Join(root, name))
	if err != nil {
		return "", fmt.Errorf("codegen: resolve output path: %w", err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("codegen: verify output containment: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("codegen: output %s escapes controlled directory %s", target, root)
	}
	return target, nil
}

func modelDirs(root string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("-recursive needs a directory, got %s", root)
	}
	set := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && ignoredDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") &&
			!strings.HasSuffix(entry.Name(), "_gen.go") && preferredModelFile(entry.Name()) {
			set[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	dirs := make([]string, 0, len(set))
	for dir := range set {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs, nil
}

func preferredModelFile(name string) bool {
	return name == "model.go" || strings.HasSuffix(name, ".model.go") || strings.HasSuffix(name, "_model.go")
}

func ignoredDir(name string) bool {
	switch name {
	case ".git", "vendor", "migrations", "migration", "test", "tests", "generated":
		return true
	default:
		return false
	}
}

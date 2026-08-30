// Package codegen generates the update DTO and the typed metamodel from a
// package's model structs. It is the half of `vv generate` that has nothing to
// do with the command line, so it can be tested without one.
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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type field struct {
	Name      string
	Type      string // the type expression, as written
	Tag       string // db tag value
	Rel       string // rel tag value, "" when absent
	Skip      bool
	PK        bool
	Auto      bool
	Immutable bool
	Generated bool
	// Version is the optimistic lock. The repository advances it, so it is not
	// a field a caller may set — and a DTO that names it is refused at Define
	// time, which is why the generator has to know about it too.
	Version bool
	// Excluded records that the command line, rather than the model's own tags,
	// is what keeps this column out of the generated artefacts. Reflection reads
	// the struct and never the flags, so the generated file has to carry the
	// list or the coverage assertion refuses a column its author dropped on
	// purpose.
	Excluded bool
}

// tagDropped reports whether the model's own tags already keep this column out
// of the update DTO — the half the runtime can see for itself.
func (this field) tagDropped() bool {
	return this.Skip || this.PK || this.Generated || this.Immutable || this.Version
}

type model struct {
	Name   string
	Fields []field
}

// pk answers the primary key, which the adapter half needs to name the id type.
func (this *model) pk() (field, bool) {
	for _, f := range this.Fields {
		if f.PK {
			return f, true
		}
	}
	return field{}, false
}

// excluded lists the columns the command line keeps out of the artefacts, in a
// stable order — the output has to be byte-identical across runs ([[D-014]]).
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
	binding  string
	specsPkg string
	crudPkg  string
	utilsPkg string
	portPkg  string
	errsPkg  string
	netPkg   string

	models     map[string]*model
	order      []string
	skip       map[string]bool
	readonly   map[string]bool
	structs    map[string]bool            // struct types declared in this package
	embeds     map[string]*ast.StructType // …and their declarations, for embedding
	embedFiles map[string]*ast.File       // declaration file, so nested field imports keep their meaning
	// embedProblems are declaration failures collected while walking models.
	// The parser visits package files through maps, so they are kept as a set and
	// sorted before returning: the same broken package must produce the same
	// diagnostic on every machine.
	embedProblems map[string]bool

	// Set when the output lands in a different package from the models.
	into        string
	modelImport string
	modelAlias  string
	// imports collected from the source files, so generated field types keep
	// resolving (time.Time, uuid.UUID, …).
	imports     map[string]string               // generated package alias -> import path
	fileImports map[*ast.File]map[string]string // source qualifier -> generated alias
	pathAliases map[string]string               // import path -> generated package alias
	usedAliases map[string]bool                 // every identifier unavailable to an import
	types       *sourceTypes                    // best-effort go/types view of the parsed package

	// Where the "wrote …" line goes. Nil is silent, which is what a test wants;
	// swapping os.Stdout to get that made every test share global state.
	log io.Writer
}

func (this *generator) run(outPath string) error {
	// Check ownership before excluding the basename from the source parse. An
	// authored target must not disappear from the input immediately before the
	// generator refuses to overwrite it.
	if err := validateGeneratedTarget(outPath); err != nil {
		return err
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
		// The mapper, the service and the wiring all name <Model>Update. Emitting
		// them without it produces a file that does not compile, which is a worse
		// answer than this one.
		return fmt.Errorf("-adapter needs the update DTO; drop -no-dto")
	}
	source, err := this.render()
	if err != nil {
		return err
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

func writeGenerated(path string, source []byte) (err error) {
	if err := validateGeneratedTarget(path); err != nil {
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
	// Close the validation/write race. Rename replaces a symlink rather than
	// following it, but an authored regular file appearing here must still win.
	if err := validateGeneratedTarget(path); err != nil {
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
	this.embedProblems = map[string]bool{}
	this.fileImports = map[*ast.File]map[string]string{}
	this.pathAliases = map[string]string{}

	// A first pass over the struct names, so a field whose type is another
	// struct in this package can be recognised as a relation holder rather than
	// mistaken for a column. That is what keeps ent's `Edges UserEdges` out.
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
	if len(this.embedProblems) > 0 {
		problems := make([]string, 0, len(this.embedProblems))
		for problem := range this.embedProblems {
			problems = append(problems, problem)
		}
		sort.Strings(problems)
		return fmt.Errorf("codegen: embedded fields cannot be mirrored safely:\n- %s", strings.Join(problems, "\n- "))
	}
	sort.Strings(this.order)
	return nil
}

type sourceImport struct {
	file      *ast.File
	qualifier string
	path      string
}

// prepareImports gives every imported path one output-file alias and records
// how each source file's own qualifier maps onto it. Two Go files may legally
// use the same qualifier for different packages; one generated file cannot, so
// collisions receive stable numeric suffixes and field type expressions are
// rewritten per declaration file.
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
	if this.modelImport != "" {
		this.modelAlias = allocateReadableImportAlias(inputPackage, this.modelImport, used, false)
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
					// Most paths and package declarations agree. A version suffix
					// such as /v2 does not; when the basename is not used in this
					// file, ask the Go tool for the declared package name.
					if !prefixes[qualifier] {
						declared := this.importPackageName(path)
						if declared == "" {
							return fmt.Errorf("codegen: import %q in %s does not use basename %q and its declared package name could not be resolved; make the import alias explicit", path, fileName, qualifier)
						}
						qualifier = declared
					}
				}
				records = append(records, sourceImport{file: file, qualifier: qualifier, path: path})
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
		if alias == "" {
			alias = allocateReadableImportAlias(names[0], path, used, preferredCounts[names[0]] > 1)
		}
		pathAlias[path] = alias
		this.pathAliases[path] = alias
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
	return nil
}

// reserveGeneratedAndPackageNames keeps an imported package from being given
// an alias that is already meaningful at package scope or that this run will
// emit. Source files may legally call an import UserUpdate because imports are
// file-scoped; the generated file may not use that alias and declare the
// UserUpdate DTO in the same file.
func (this *generator) reserveGeneratedAndPackageNames(pkgs map[string]*ast.Package, used map[string]bool) {
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					used[d.Name.Name] = true
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							used[s.Name.Name] = true
							for _, name := range []string{
								s.Name.Name + "Update", s.Name.Name + "Attrs", s.Name.Name + "Repo",
								s.Name.Name + "Repository", "New" + s.Name.Name + "Repository",
								s.Name.Name + "Input", s.Name.Name + "Mapper", s.Name.Name + "Paths",
								s.Name.Name + "Service", "Mount" + s.Name.Name, s.Name.Name + "_",
							} {
								used[name] = true
							}
						case *ast.ValueSpec:
							for _, name := range s.Names {
								used[name.Name] = true
							}
						}
					}
				}
			}
		}
	}
}

func (this *generator) sharedImportAlias(path string) string {
	switch path {
	case "time":
		return "time"
	case "github.com/frostgrove/vv/crud":
		return "crud"
	case DefaultUtilsPkg:
		return "utils"
	case "github.com/frostgrove/vv/crud/decorators/specs":
		return "specs"
	case "gorm.io/gorm":
		return "gorm"
	}
	return ""
}

// allocateReadableImportAlias keeps collision handling visible to a human.
// Numeric suffixes such as crud2 and crud22 say only that a collision happened;
// alpha, alphaCommon and betaCommon say which package each selector names.
func allocateReadableImportAlias(preferred, path string, used map[string]bool, forcePath bool) string {
	preferred = strings.TrimSpace(preferred)
	if !forcePath && usableImportAlias(preferred, used) {
		used[preferred] = true
		return preferred
	}
	candidates := pathImportAliases(path)
	start := 0
	if forcePath && len(candidates) > 1 {
		// Two packages both called common are clearer as alphaCommon and
		// betaCommon than as common and betaCommon: neither gets privileged by
		// path sort order.
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
		if ident, ok := selector.X.(*ast.Ident); ok {
			out[ident.Name] = true
		}
		return true
	})
	return out
}

func (this *generator) importPackageName(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-mod=readonly", "-json", "-find", path)
	cmd.Dir = this.dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var listed struct{ Name string }
	if json.Unmarshal(out, &listed) != nil {
		return ""
	}
	return listed.Name
}

func (this *generator) typeString(expr ast.Expr, file *ast.File) string {
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
	var embedProblems []string
	// An explicitly named type is a model whether or not it carries tags — which
	// is how a generated entity from another tool qualifies. Model files carry
	// the same meaning without a tag: plain Go structs are vv models by
	// convention, independently of their database driver or ORM.
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
			if (hasDB && database == "-") || (hasRel && relation == "-") {
				continue
			}
			typ := strings.TrimSpace(this.typeString(f.Type, file))
			if strings.HasPrefix(typ, "*") {
				embedProblems = append(embedProblems, fmt.Sprintf(
					"model %s embeds pointer %s; runtime metadata refuses embedded pointer structs, so embed a value or tag it db:\"-\"", name, typ))
				continue
			}
			if hasDB || hasRel {
				embedProblems = append(embedProblems, fmt.Sprintf(
					"model %s tags anonymous %s; runtime does not flatten tagged embeds and codegen cannot infer a safe column/relation shape", name, typ))
				continue
			}
			// Embedded: the runtime flattens it, so the generator has to as
			// well or `gorm.Model` would silently take id and the timestamps
			// out of the metamodel.
			if fields, ok := this.embedded(typ); ok {
				m.Fields = append(m.Fields, fields...)
				tagged = tagged || len(fields) > 0
			} else {
				embedProblems = append(embedProblems, fmt.Sprintf(
					"model %s embeds unresolved type %s; flatten it into this package, tag it db:\"-\", or add an audited well-known embed", name, typ))
			}
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
				continue
			}
			fl := field{Name: ident.Name, Type: this.typeString(f.Type, file), Tag: database, Rel: rel}
			// A field whose type is another struct from this package is either a
			// relation or somebody else's bookkeeping; it is never a column.
			if !hasRel {
				if base, _ := relElem(fl.Type); this.structs[base] {
					continue
				}
			}
			if hasRel && rel != "-" {
				// A relation is not a column, so dropping one leaves nothing for
				// reflection to disagree about and nothing to declare.
				if this.skip[ident.Name] {
					continue
				}
				m.Fields = append(m.Fields, fl)
				continue
			}
			if database == "-" || (hasRel && rel == "-") {
				fl.Skip = true
			}
			for _, opt := range strings.Split(database, ",")[1:] {
				switch opt {
				case "pk", "primarykey", "primary_key":
					fl.PK = true
				case "auto", "identity", "serial", "autoincrement":
					fl.Auto = true
				case "immutable", "readonly", "insertonly", "insert_only":
					fl.Immutable = true
				case "generated", "computed":
					fl.Generated = true
				case "version", "lock":
					fl.Version = true
				}
			}
			if strings.EqualFold(fl.Name, "id") && !fl.PK && database != "-" {
				fl.PK = true
			}
			// After the tags, not before: whether the flag is the only reason
			// the column leaves is a question the tags have to have answered.
			this.exclude(&fl)
			m.Fields = append(m.Fields, fl)
		}
	}
	if !tagged {
		return nil
	}
	for _, problem := range embedProblems {
		this.embedProblems[problem] = true
	}
	return m
}

// exclude applies -skip and -readonly, and records when a flag is the only
// reason the column leaves the generated artefacts. A skipped column stays in
// the model with Skip set rather than being dropped: it is absent from the DTO
// and the metamodel either way, and the name is still needed for the exclusion
// list.
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

// ---------------------------------------------------------------------------
// classification

func (this field) isRelation() bool { return this.Rel != "" && this.Rel != "-" }

// elem strips *T and either compatibility crud.Opt[T] or canonical
// utils.Opt[T] down to T, reporting whether the column is nullable.
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

// relElem strips []T, []*T and *T down to T for a relation field.
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

// attrType picks the metamodel attribute for a column type: Str for text, Ord
// for anything cmp.Ordered, Cmp for time-like values, Attr for the rest.
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

// dtoType picks the DTO field type: Opt for a nullable column (three states),
// a pointer for a non-nullable one (two).
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
	// Keep an all-caps prefix together: ID -> id, HTTPCode -> httpCode.
	i := 0
	for i < len(s) && s[i] >= 'A' && s[i] <= 'Z' {
		i++
	}
	if i > 1 && i < len(s) {
		i--
	}
	return strings.ToLower(s[:i]) + s[i:]
}

// qual renders a model type name as the output package must spell it.
func (this *generator) qual(name string) string {
	if this.modelAlias == "" {
		return name
	}
	return this.modelAlias + "." + name
}

// packageNameOf reuses the package already declared in dir, falling back to its
// base name.
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

// cmpOr answers the first non-empty string.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// names parses a comma-separated flag into a set.
func names(csv string) map[string]bool {
	out := map[string]bool{}
	for _, n := range strings.Split(csv, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out[n] = true
		}
	}
	return out
}

// wellKnownEmbeds are embedded types from other packages whose fields the
// generator cannot read but that are common enough to be worth knowing.
//
// The three timestamps are marked Excluded as well as Immutable, and the two
// are not the same claim. gorm.Model carries no `db` tags, so reflection sees
// three ordinary writable columns where this table sees server-owned ones —
// only the generated file can say which, so it declares them.
var wellKnownEmbeds = map[string][]field{
	"gorm.Model": {
		{Name: "ID", Type: "uint", PK: true, Auto: true},
		{Name: "CreatedAt", Type: "time.Time", Immutable: true, Excluded: true},
		{Name: "UpdatedAt", Type: "time.Time", Immutable: true, Excluded: true},
		{Name: "DeletedAt", Type: "gorm.DeletedAt", Immutable: true, Excluded: true},
	},
}

// embedded resolves the fields of an embedded struct: from this package when it
// is declared here, from the table above otherwise.
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

// Options is one invocation, as the command line describes it.
type Options struct {
	Dir      string // package directory to read
	Out      string // output file name, written into Dir unless Into is set
	Into     string // write into this directory instead of Dir
	Types    string // comma-separated model names; empty means every tagged struct
	Skip     string // comma-separated field names to leave out entirely
	Readonly string // comma-separated field names to keep out of the update DTO
	Import   string // import path of Dir, to qualify models written elsewhere
	Depth    int    // how far to expand relation paths into the metamodel
	WithDTO  bool
	WithMeta bool
	// NoRepo keeps the generator useful for a DTO/metamodel-only consumer.
	// The normal command generates a datasource-independent repository
	// blueprint and binding factory for every model.
	NoRepo bool
	// Recursive discovers model files below Dir and writes one vv_gen.go next
	// to each package that contains an exported model.
	Recursive bool
	Adapter   bool   // also generate the resource adapter: input DTO, mapper, inverse map, service, wiring
	Binding   string // which transport the wiring is written for: "net" or "none"
	SpecsPkg  string
	CrudPkg   string
	UtilsPkg  string

	// The adapter half names three more packages than the DTO half does, and
	// they are fields rather than flags. -crud and -specs exist because a
	// consumer may point the generated file at a vendored copy of those two;
	// nobody has asked for the same over these, and six import-path flags is
	// five ways to produce a file that does not compile.
	PortPkg string
	ErrsPkg string
	NetPkg  string

	// Log receives the one line the command prints on success. Nil is silent.
	Log io.Writer
}

// The packages the adapter half names. See Options.
const (
	DefaultPortPkg  = "github.com/frostgrove/vv/port"
	DefaultErrsPkg  = "github.com/frostgrove/vv/errs"
	DefaultNetPkg   = "github.com/frostgrove/vv/crud/http/crudnet"
	DefaultUtilsPkg = "github.com/frostgrove/vv/utils"
)

// Run generates from o and writes the result. The output path is Out resolved
// against Into when set, Dir otherwise.
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
		for _, dir := range dirs {
			one := *o
			one.Dir = dir
			one.Out = outName
			one.Recursive = false
			if err := Run(&one); err != nil {
				// A model file may carry only package-private helpers. It is not a
				// generation target, and must not make an application-wide scan fail.
				if strings.Contains(err.Error(), "no models found in ") {
					continue
				}
				return err
			}
		}
		return nil
	}

	binding := o.Binding
	if binding == "" {
		binding = "net"
	}
	if binding != "net" && binding != "none" {
		// Fiber and Gin wiring would import a satellite module, which a
		// consumer may do and the library's own generated files may not
		// ([[D-033]]). Refused rather than emitted, so the failure is a message
		// and not a build error in the output.
		return fmt.Errorf("-binding %s: only net and none are generated today", binding)
	}
	g := &generator{
		dir:      o.Dir,
		depth:    o.Depth,
		withDTO:  o.WithDTO,
		withMeta: o.WithMeta,
		withRepo: !o.NoRepo,
		adapter:  o.Adapter,
		binding:  binding,
		specsPkg: o.SpecsPkg,
		crudPkg:  o.CrudPkg,
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

// modelDirs finds packages that opted into generation by placing a model in a
// conventional model file. It follows vvgoose's model-file convention and
// emits one generated Go file per package.
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

// Package codegen generates the update DTO and the typed metamodel from a
// package's model structs. It is the half of `vv generate` that has nothing to
// do with the command line, so it can be tested without one.
package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
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
func (f field) tagDropped() bool {
	return f.Skip || f.PK || f.Generated || f.Immutable || f.Version
}

type model struct {
	Name   string
	Fields []field
}

// pk answers the primary key, which the adapter half needs to name the id type.
func (m *model) pk() (field, bool) {
	for _, f := range m.Fields {
		if f.PK {
			return f, true
		}
	}
	return field{}, false
}

// excluded lists the columns the command line keeps out of the artefacts, in a
// stable order — the output has to be byte-identical across runs ([[D-014]]).
func (m *model) excluded() []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range m.Fields {
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

	models   map[string]*model
	order    []string
	skip     map[string]bool
	readonly map[string]bool
	structs  map[string]bool            // struct types declared in this package
	embeds   map[string]*ast.StructType // …and their declarations, for embedding

	// Set when the output lands in a different package from the models.
	into        string
	modelImport string
	modelAlias  string
	// imports collected from the source files, so generated field types keep
	// resolving (time.Time, uuid.UUID, …).
	imports map[string]string // package name -> import path

	// Where the "wrote …" line goes. Nil is silent, which is what a test wants;
	// swapping os.Stdout to get that made every test share global state.
	log io.Writer
}

func (g *generator) run(outPath string) error {
	if err := g.load(outPath); err != nil {
		return err
	}
	if g.into != "" {
		if g.modelImport == "" {
			return fmt.Errorf("-into needs -import so the generated file can name the model types")
		}
		if err := os.MkdirAll(g.into, 0o755); err != nil {
			return err
		}
		g.pkg = packageNameOf(g.into)
	}
	if len(g.order) == 0 {
		return fmt.Errorf("no models found in %s; put exported model structs in model.go, *.model.go or *_model.go", g.dir)
	}
	if g.withRepo && !g.withDTO {
		return fmt.Errorf("a generated repository needs its update DTO; drop -no-dto or add -no-repo")
	}
	if g.adapter && !g.withDTO {
		// The mapper, the service and the wiring all name <Model>Update. Emitting
		// them without it produces a file that does not compile, which is a worse
		// answer than this one.
		return fmt.Errorf("-adapter needs the update DTO; drop -no-dto")
	}
	src, err := g.render()
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, src, 0o644); err != nil {
		return err
	}
	if g.log != nil {
		fmt.Fprintf(g.log, "vv: wrote %s (%d models)\n", outPath, len(g.order))
	}
	return nil
}

func (g *generator) load(skip string) error {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, g.dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go") && filepath.Base(skip) != fi.Name()
	}, parser.ParseComments)
	if err != nil {
		return err
	}
	g.models = map[string]*model{}
	g.imports = map[string]string{}
	g.structs = map[string]bool{}
	g.embeds = map[string]*ast.StructType{}

	// A first pass over the struct names, so a field whose type is another
	// struct in this package can be recognised as a relation holder rather than
	// mistaken for a column. That is what keeps ent's `Edges UserEdges` out.
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				if ts, ok := n.(*ast.TypeSpec); ok {
					if st, isStruct := ts.Type.(*ast.StructType); isStruct {
						g.structs[ts.Name.Name] = true
						g.embeds[ts.Name.Name] = st
					}
				}
				return true
			})
		}
	}

	for name, pkg := range pkgs {
		g.pkg = name
		for fileName, file := range pkg.Files {
			modelFile := preferredModelFile(filepath.Base(fileName))
			for _, imp := range file.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				alias := filepath.Base(path)
				if imp.Name != nil {
					alias = imp.Name.Name
				}
				g.imports[alias] = path
			}
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
				if g.only != nil && !g.only[ts.Name.Name] {
					return true
				}
				m := g.parseModel(ts.Name.Name, st, modelFile)
				if m != nil {
					g.models[m.Name] = m
					g.order = append(g.order, m.Name)
				}
				return true
			})
		}
	}
	sort.Strings(g.order)
	return nil
}

func (g *generator) parseModel(name string, st *ast.StructType, force bool) *model {
	m := &model{Name: name}
	// An explicitly named type is a model whether or not it carries tags — which
	// is how a generated entity from another tool qualifies. Model files carry
	// the same meaning without a tag: plain Go structs are vv models by
	// convention, independently of their database driver or ORM.
	tagged := force || (g.only != nil && g.only[name])
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			// Embedded: the runtime flattens it, so the generator has to as
			// well or `gorm.Model` would silently take id and the timestamps
			// out of the metamodel.
			if fields, ok := g.embedded(exprString(f.Type)); ok {
				m.Fields = append(m.Fields, fields...)
				tagged = tagged || len(fields) > 0
			}
			continue
		}
		var tag reflect.StructTag
		if f.Tag != nil {
			raw, _ := strconv.Unquote(f.Tag.Value)
			tag = reflect.StructTag(raw)
		}
		db, hasDB := tag.Lookup("db")
		rel, hasRel := tag.Lookup("rel")
		if hasDB || hasRel {
			tagged = true
		}
		for _, ident := range f.Names {
			if !ident.IsExported() {
				continue
			}
			fl := field{Name: ident.Name, Type: exprString(f.Type), Tag: db, Rel: rel}
			// A field whose type is another struct from this package is either a
			// relation or somebody else's bookkeeping; it is never a column.
			if !hasRel {
				if base, _ := relElem(fl.Type); g.structs[base] {
					continue
				}
			}
			if hasRel && rel != "-" {
				// A relation is not a column, so dropping one leaves nothing for
				// reflection to disagree about and nothing to declare.
				if g.skip[ident.Name] {
					continue
				}
				m.Fields = append(m.Fields, fl)
				continue
			}
			if db == "-" || (hasRel && rel == "-") {
				fl.Skip = true
			}
			for _, opt := range strings.Split(db, ",")[1:] {
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
			if strings.EqualFold(fl.Name, "id") && !fl.PK && db != "-" {
				fl.PK = true
			}
			// After the tags, not before: whether the flag is the only reason
			// the column leaves is a question the tags have to have answered.
			g.exclude(&fl)
			m.Fields = append(m.Fields, fl)
		}
	}
	if !tagged {
		return nil
	}
	return m
}

// exclude applies -skip and -readonly, and records when a flag is the only
// reason the column leaves the generated artefacts. A skipped column stays in
// the model with Skip set rather than being dropped: it is absent from the DTO
// and the metamodel either way, and the name is still needed for the exclusion
// list.
func (g *generator) exclude(f *field) {
	skipped, readonly := g.skip[f.Name], g.readonly[f.Name]
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

func (f field) isRelation() bool { return f.Rel != "" && f.Rel != "-" }

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
func (g *generator) qual(name string) string {
	if g.modelAlias == "" {
		return name
	}
	return g.modelAlias + "." + name
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
func (g *generator) embedded(typ string) ([]field, bool) {
	typ = strings.TrimPrefix(strings.TrimSpace(typ), "*")
	if fields, ok := wellKnownEmbeds[typ]; ok {
		out := make([]field, 0, len(fields))
		for _, f := range fields {
			g.exclude(&f)
			out = append(out, f)
		}
		return out, true
	}
	if st, ok := g.embeds[typ]; ok {
		m := g.parseModel(typ, st, true)
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
func Run(o Options) error {
	if o.Recursive {
		if o.Into != "" || o.Import != "" {
			return fmt.Errorf("-recursive writes beside each model package and cannot be combined with -into or -import")
		}
		dirs, err := modelDirs(o.Dir)
		if err != nil {
			return err
		}
		for _, dir := range dirs {
			one := o
			one.Dir = dir
			one.Recursive = false
			if err := Run(one); err != nil {
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
	if g.modelImport != "" {
		g.modelAlias = filepath.Base(g.modelImport)
	}
	outDir := o.Dir
	if o.Into != "" {
		outDir = o.Into
	}
	return g.run(filepath.Join(outDir, o.Out))
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

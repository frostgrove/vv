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
}

type model struct {
	Name   string
	Fields []field
}

type generator struct {
	dir      string
	pkg      string
	only     map[string]bool
	depth    int
	withDTO  bool
	withMeta bool
	specsPkg string
	crudPkg  string

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
		return fmt.Errorf("no tagged models found in %s", g.dir)
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
		for _, file := range pkg.Files {
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
				if g.only != nil && !g.only[ts.Name.Name] {
					return true
				}
				m := g.parseModel(ts.Name.Name, st)
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

func (g *generator) parseModel(name string, st *ast.StructType) *model {
	m := &model{Name: name}
	// An explicitly named type is a model whether or not it carries tags — which
	// is how a generated entity from another tool qualifies.
	tagged := g.only != nil && g.only[name]
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
			if !ident.IsExported() || g.skip[ident.Name] {
				continue
			}
			fl := field{Name: ident.Name, Type: exprString(f.Type), Tag: db, Rel: rel}
			if g.readonly[ident.Name] {
				fl.Immutable = true
			}
			// A field whose type is another struct from this package is either a
			// relation or somebody else's bookkeeping; it is never a column.
			if !hasRel {
				if base, _ := relElem(fl.Type); g.structs[base] {
					continue
				}
			}
			if hasRel && rel != "-" {
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
			if fl.Name == "ID" && !fl.PK && db != "-" {
				fl.PK = true
			}
			m.Fields = append(m.Fields, fl)
		}
	}
	if !tagged {
		return nil
	}
	return m
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

// elem strips *T and crud.Opt[T] down to T, reporting whether the column is
// nullable.
func elem(typ string) (string, bool) {
	typ = strings.TrimSpace(typ)
	if inner, ok := cutGeneric(typ, "crud.Opt"); ok {
		return inner, true
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
		return "crud.Opt[" + e + "]"
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
var wellKnownEmbeds = map[string][]field{
	"gorm.Model": {
		{Name: "ID", Type: "uint", PK: true, Auto: true},
		{Name: "CreatedAt", Type: "time.Time", Immutable: true},
		{Name: "UpdatedAt", Type: "time.Time", Immutable: true},
		{Name: "DeletedAt", Type: "gorm.DeletedAt", Immutable: true},
	},
}

// embedded resolves the fields of an embedded struct: from this package when it
// is declared here, from the table above otherwise.
func (g *generator) embedded(typ string) ([]field, bool) {
	typ = strings.TrimPrefix(strings.TrimSpace(typ), "*")
	if fields, ok := wellKnownEmbeds[typ]; ok {
		out := make([]field, 0, len(fields))
		for _, f := range fields {
			if g.skip[f.Name] {
				continue
			}
			if g.readonly[f.Name] {
				f.Immutable = true
			}
			out = append(out, f)
		}
		return out, true
	}
	if st, ok := g.embeds[typ]; ok {
		m := g.parseModel(typ, st)
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
	SpecsPkg string
	CrudPkg  string

	// Log receives the one line the command prints on success. Nil is silent.
	Log io.Writer
}

// Run generates from o and writes the result. The output path is Out resolved
// against Into when set, Dir otherwise.
func Run(o Options) error {
	g := &generator{
		dir:      o.Dir,
		depth:    o.Depth,
		withDTO:  o.WithDTO,
		withMeta: o.WithMeta,
		specsPkg: o.SpecsPkg,
		crudPkg:  o.CrudPkg,
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

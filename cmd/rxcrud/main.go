// Command rxcrud generates the two things you would otherwise copy out of your
// model by hand: the partial-update DTO and the typed metamodel.
//
//	//go:generate go run github.com/shardit-io/go-rx-crud/cmd/rxcrud
//
// Point it at a package; it reads every struct that carries `db` or `rel` tags
// and writes rxcrud_gen.go next to them:
//
//	type ArticleUpdate struct { … }   // pointers for optional, Opt for nullable
//	type ArticleAttrs  struct { … }   // including nested relation paths
//	var  Article_ = specs.Metamodel[Article, ArticleAttrs]()
//
// After that `Article_.Author.Name.Eq("Ann")` is a compile-time-checked filter
// on a joined column, and a renamed field breaks the build instead of a request.
//
// Flags:
//
//	-dir     package directory (default ".")
//	-out     output file name (default "rxcrud_gen.go")
//	-types   comma-separated list; default is every tagged struct
//	-depth   how far to expand relation paths into the metamodel (default 2)
//	-skip    comma-separated field names to leave out entirely (like db:"-")
//	-readonly comma-separated field names kept out of the update DTO but still
//	         filterable and sortable (like db:",immutable")
//	-into    write into this directory instead of -dir
//	-import  import path of -dir, so model types are qualified when written
//	         somewhere else
//	-no-dto  skip the update DTOs
//	-no-meta skip the metamodels
//
// With -types the named structs are taken as models even without db tags, which
// is what makes ent's generated entities work as-is. Write the result into your
// own package rather than into ent's, where the names would collide:
//
//	go run github.com/shardit-io/go-rx-crud/cmd/rxcrud -dir ./ent -types User,Article -skip CreatedAt \
//	    -import myapp/ent -into ./internal/store
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func main() {
	var (
		dir      = flag.String("dir", ".", "package directory")
		out      = flag.String("out", "rxcrud_gen.go", "output file name")
		only     = flag.String("types", "", "comma-separated model names; default is every tagged struct")
		skip     = flag.String("skip", "", "comma-separated field names to leave out entirely")
		readonly = flag.String("readonly", "", "comma-separated field names to keep out of the update DTO")
		into     = flag.String("into", "", "write into this directory instead of -dir")
		impPath  = flag.String("import", "", "import path of -dir, used to qualify model types written elsewhere")
		depth    = flag.Int("depth", 2, "how far to expand relation paths into the metamodel")
		noDTO    = flag.Bool("no-dto", false, "skip update DTOs")
		noMeta   = flag.Bool("no-meta", false, "skip metamodels")
		specsPkg = flag.String("specs", "github.com/shardit-io/go-rx-crud/repo/decorators/specs", "import path of the specs package")
		crudPkg  = flag.String("crud", "github.com/shardit-io/go-rx-crud/crud", "import path of the crud package")
	)
	flag.Parse()

	g := &generator{
		dir:      *dir,
		depth:    *depth,
		withDTO:  !*noDTO,
		withMeta: !*noMeta,
		specsPkg: *specsPkg,
		crudPkg:  *crudPkg,
	}
	if *only != "" {
		g.only = map[string]bool{}
		for _, n := range strings.Split(*only, ",") {
			g.only[strings.TrimSpace(n)] = true
		}
	}
	g.into = *into
	g.modelImport = *impPath
	if g.modelImport != "" {
		g.modelAlias = filepath.Base(g.modelImport)
	}
	g.skip = names(*skip)
	g.readonly = names(*readonly)
	outDir := *dir
	if *into != "" {
		outDir = *into
	}
	if err := g.run(filepath.Join(outDir, *out)); err != nil {
		fmt.Fprintln(os.Stderr, "rxcrud:", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// model discovery

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
	fmt.Printf("rxcrud: wrote %s (%d models)\n", outPath, len(g.order))
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

package modelscan

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type packageKey struct {
	dir  string
	name string
}

type packageSource struct {
	key        packageKey
	constExprs map[string]ast.Expr
	consts     map[string]string
	structs    map[string]*structSource
	methods    []*ast.FuncDecl
}

type structSource struct {
	name      string
	typ       *ast.StructType
	file      string
	line      int
	preferred bool
	tagged    bool
	imports   map[string]string
	fset      *token.FileSet
}

// Discover walks the configured source roots and returns model declarations in
// a deterministic order. Model files (`model.go`, `*.model.go` and
// `*_model.go`) are models by convention. An ordinary Go file contributes a
// model only when its struct has db/rel/gorm evidence or a constant TableName
// method.
func Discover(o Options) ([]Model, error) {
	roots := o.Roots
	if len(roots) == 0 {
		roots = []string{"."}
	}
	files, err := sourceFiles(roots)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	packages := map[packageKey]*packageSource{}
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("modelscan: parsing %s: %w", path, err)
		}
		if ast.IsGenerated(file) {
			continue
		}
		key := packageKey{dir: filepath.Dir(path), name: file.Name.Name}
		pkg := packages[key]
		if pkg == nil {
			pkg = &packageSource{
				key:        key,
				constExprs: map[string]ast.Expr{},
				consts:     map[string]string{},
				structs:    map[string]*structSource{},
			}
			packages[key] = pkg
		}
		collectFile(pkg, fset, path, file)
	}

	keys := make([]packageKey, 0, len(packages))
	for key := range packages {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dir != keys[j].dir {
			return keys[i].dir < keys[j].dir
		}
		return keys[i].name < keys[j].name
	})

	var models []Model
	for _, key := range keys {
		found, err := packages[key].models()
		if err != nil {
			return nil, err
		}
		models = append(models, found...)
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].File != models[j].File {
			return models[i].File < models[j].File
		}
		return models[i].Name < models[j].Name
	})
	return models, nil
}

func sourceFiles(roots []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			return nil, fmt.Errorf("modelscan: source root is empty")
		}
		root, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("modelscan: resolving %s: %w", root, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("modelscan: source root %s: %w", root, err)
		}
		if !info.IsDir() {
			if sourceFileName(filepath.Base(root)) && !seen[root] {
				files = append(files, root)
				seen[root] = true
			}
			continue
		}
		if skippedDir(filepath.Base(root)) {
			continue
		}
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != root && skippedDir(entry.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !sourceFileName(entry.Name()) {
				return nil
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return err
			}
			if !seen[path] {
				files = append(files, path)
				seen[path] = true
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("modelscan: walking %s: %w", root, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

func skippedDir(name string) bool {
	switch name {
	case ".git", "vendor", "migrations", "migration", "test", "tests", "generated":
		return true
	default:
		return false
	}
}

func sourceFileName(name string) bool {
	return strings.HasSuffix(name, ".go") &&
		!strings.HasSuffix(name, "_test.go") &&
		!strings.HasSuffix(name, "_gen.go")
}

func preferredModelFile(name string) bool {
	return name == "model.go" || strings.HasSuffix(name, ".model.go") || strings.HasSuffix(name, "_model.go")
}

func collectFile(pkg *packageSource, fset *token.FileSet, path string, file *ast.File) {
	imports := importsOf(file)
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			switch decl.Tok {
			case token.CONST:
				collectConsts(pkg, decl)
			case token.TYPE:
				for _, spec := range decl.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					pos := fset.Position(ts.Pos())
					pkg.structs[ts.Name.Name] = &structSource{
						name:      ts.Name.Name,
						typ:       st,
						file:      path,
						line:      pos.Line,
						preferred: preferredModelFile(filepath.Base(path)),
						tagged:    structTagged(st, imports),
						imports:   imports,
						fset:      fset,
					}
				}
			}
		case *ast.FuncDecl:
			if decl.Recv != nil && decl.Name.Name == "TableName" {
				pkg.methods = append(pkg.methods, decl)
			}
		}
	}
}

func importsOf(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		out[name] = path
	}
	return out
}

func collectConsts(pkg *packageSource, decl *ast.GenDecl) {
	var previous []ast.Expr
	for _, raw := range decl.Specs {
		spec, ok := raw.(*ast.ValueSpec)
		if !ok {
			continue
		}
		values := spec.Values
		if len(values) == 0 {
			values = previous
		} else {
			previous = values
		}
		for i, name := range spec.Names {
			if len(values) == 0 {
				continue
			}
			expr := values[len(values)-1]
			if i < len(values) {
				expr = values[i]
			}
			pkg.constExprs[name.Name] = expr
		}
	}
}

func structTagged(st *ast.StructType, imports map[string]string) bool {
	for _, field := range st.Fields.List {
		if field.Tag != nil {
			raw, err := strconv.Unquote(field.Tag.Value)
			if err == nil {
				tag := reflect.StructTag(raw)
				if _, ok := tag.Lookup("db"); ok {
					return true
				}
				if _, ok := tag.Lookup("rel"); ok {
					return true
				}
				if _, ok := tag.Lookup("gorm"); ok {
					return true
				}
			}
		}
		if isGormModel(field.Type, imports) {
			return true
		}
	}
	return false
}

func (pkg *packageSource) models() ([]Model, error) {
	for name := range pkg.constExprs {
		if value, ok := pkg.evalConst(name, map[string]bool{}); ok {
			pkg.consts[name] = value
		}
	}
	tables := map[string]string{}
	for _, method := range pkg.methods {
		name := receiverName(method)
		if name == "" || pkg.structs[name] == nil {
			continue
		}
		table, ok := constantReturn(method, func(expr ast.Expr) (string, bool) {
			return pkg.evalString(expr, map[string]bool{})
		})
		if !ok || strings.TrimSpace(table) == "" {
			continue
		}
		if previous := tables[name]; previous != "" && previous != table {
			return nil, fmt.Errorf("modelscan: %s.%s has conflicting constant TableName values %q and %q", pkg.key.name, name, previous, table)
		}
		tables[name] = table
	}

	names := make([]string, 0, len(pkg.structs))
	for name := range pkg.structs {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []Model
	for _, name := range names {
		decl := pkg.structs[name]
		table, explicit := tables[name]
		if !decl.preferred && !decl.tagged && !explicit {
			continue
		}
		if table == "" {
			table = pluralise(snake(name))
		}
		fields := pkg.fields(decl, "", map[string]bool{})
		finishPrimaryKey(fields)
		out = append(out, Model{
			Package:       pkg.key.name,
			Name:          name,
			Table:         table,
			ExplicitTable: explicit,
			Dir:           displayPath(pkg.key.dir),
			File:          displayPath(decl.file),
			Line:          decl.line,
			Fields:        fields,
			Tagged:        decl.tagged,
		})
	}
	return out, nil
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	var unwrap func(ast.Expr) string
	unwrap = func(expr ast.Expr) string {
		switch expr := expr.(type) {
		case *ast.Ident:
			return expr.Name
		case *ast.StarExpr:
			return unwrap(expr.X)
		case *ast.IndexExpr:
			return unwrap(expr.X)
		case *ast.IndexListExpr:
			return unwrap(expr.X)
		case *ast.ParenExpr:
			return unwrap(expr.X)
		default:
			return ""
		}
	}
	return unwrap(fn.Recv.List[0].Type)
}

func constantReturn(fn *ast.FuncDecl, eval func(ast.Expr) (string, bool)) (string, bool) {
	if fn.Body == nil || len(fn.Body.List) != 1 {
		return "", false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return "", false
	}
	return eval(ret.Results[0])
}

func (pkg *packageSource) evalConst(name string, seen map[string]bool) (string, bool) {
	if value, ok := pkg.consts[name]; ok {
		return value, true
	}
	if seen[name] {
		return "", false
	}
	expr := pkg.constExprs[name]
	if expr == nil {
		return "", false
	}
	seen[name] = true
	value, ok := pkg.evalString(expr, seen)
	delete(seen, name)
	if ok {
		pkg.consts[name] = value
	}
	return value, ok
}

func (pkg *packageSource) evalString(expr ast.Expr, seen map[string]bool) (string, bool) {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		if expr.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(expr.Value)
		return value, err == nil
	case *ast.Ident:
		return pkg.evalConst(expr.Name, seen)
	case *ast.BinaryExpr:
		if expr.Op != token.ADD {
			return "", false
		}
		left, ok := pkg.evalString(expr.X, seen)
		if !ok {
			return "", false
		}
		right, ok := pkg.evalString(expr.Y, seen)
		return left + right, ok
	case *ast.ParenExpr:
		return pkg.evalString(expr.X, seen)
	default:
		return "", false
	}
}

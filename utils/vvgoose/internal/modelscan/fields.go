package modelscan

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"reflect"
	"strconv"
	"strings"
)

func (pkg *packageSource) fields(decl *structSource, prefix string, seen map[string]bool) []Field {
	if seen[decl.name] {
		return nil
	}
	seen[decl.name] = true
	defer delete(seen, decl.name)

	var out []Field
	for _, raw := range decl.typ.Fields.List {
		tag := fieldTag(raw)
		db, hasDB := tag.Lookup("db")
		rel, hasRel := tag.Lookup("rel")
		gorm, _ := tag.Lookup("gorm")
		gormOpts := parseGormTag(gorm)

		if len(raw.Names) == 0 {
			if hasDB || (hasRel && rel != "-") {
				continue
			}
			embeddedPrefix := prefix
			if value, ok := gormOpts["embeddedprefix"]; ok {
				embeddedPrefix += value
			}
			if name := localTypeName(raw.Type); name != "" {
				if embedded := pkg.structs[name]; embedded != nil {
					out = append(out, pkg.fields(embedded, embeddedPrefix, seen)...)
				}
				continue
			}
			if isGormModel(raw.Type, decl.imports) {
				out = append(out, gormModelFields(embeddedPrefix, decl.file, decl.fset.Position(raw.Pos()).Line)...)
			}
			continue
		}

		if db == "-" || hasRel || hasGormOption(gormOpts, "-") {
			continue
		}
		// A named local struct is normally a relation/bookkeeping field. GORM's
		// explicit embedded spelling is the exception and flattens its columns.
		if name := localTypeName(raw.Type); name != "" && pkg.structs[name] != nil {
			if hasGormOption(gormOpts, "embedded") || gormOpts["embeddedprefix"] != "" {
				out = append(out, pkg.fields(pkg.structs[name], prefix+gormOpts["embeddedprefix"], seen)...)
			}
			continue
		}
		// Without type-checking, an imported selector that is not one of the
		// database scalar packages is much more likely to be a relation than a
		// column. An explicit db/column tag remains the author's escape hatch for
		// an imported scalar type.
		if !hasDB && gormOpts["column"] == "" && importedRelationCandidate(raw.Type, decl.imports) {
			continue
		}

		for _, ident := range raw.Names {
			if !ident.IsExported() {
				continue
			}
			column, dbOpts := parseDBTag(db)
			if column == "" {
				column = gormOpts["column"]
			}
			if column == "" {
				column = snake(ident.Name)
			}
			column = prefix + column
			line := decl.fset.Position(ident.Pos()).Line
			f := Field{
				Name:           ident.Name,
				Column:         column,
				GoType:         expression(decl, raw.Type),
				CanonicalType:  canonicalExpression(decl.imports, decl.fset, raw.Type),
				UnderlyingType: pkg.underlyingType(decl.imports, decl.fset, raw.Type, map[string]bool{}),
				File:           displayPath(decl.file),
				Line:           line,
				Nullable:       nullableType(raw.Type),
				PrimaryKey:     dbOpts["pk"] || dbOpts["primarykey"] || dbOpts["primary_key"] || hasGormOption(gormOpts, "primarykey"),
				Auto:           dbOpts["auto"] || dbOpts["identity"] || dbOpts["serial"] || dbOpts["autoincrement"] || gormBool(gormOpts, "autoincrement", true),
				NoAuto:         dbOpts["noauto"] || gormBool(gormOpts, "autoincrement", false),
				Immutable:      dbOpts["immutable"] || dbOpts["readonly"] || dbOpts["insertonly"] || dbOpts["insert_only"] || strings.EqualFold(gormOpts["<-"], "create"),
				Generated:      dbOpts["generated"] || dbOpts["computed"] || hasGormOption(gormOpts, "->"),
				Version:        dbOpts["version"] || dbOpts["lock"],
			}
			out = append(out, f)
		}
	}
	return out
}

func finishPrimaryKey(fields []Field) {
	hasPK := false
	for i := range fields {
		hasPK = hasPK || fields[i].PrimaryKey
	}
	if !hasPK {
		for i := range fields {
			if fields[i].Name == "ID" || fields[i].Column == "id" {
				fields[i].PrimaryKey = true
				break
			}
		}
	}
	for i := range fields {
		if fields[i].PrimaryKey && !fields[i].Auto && !fields[i].NoAuto && integerType(databaseType(fields[i])) {
			fields[i].Auto = true
		}
	}
}

func fieldTag(field *ast.Field) reflect.StructTag {
	if field.Tag == nil {
		return ""
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return ""
	}
	return reflect.StructTag(raw)
}

func parseDBTag(raw string) (string, map[string]bool) {
	parts := strings.Split(raw, ",")
	name := strings.TrimSpace(parts[0])
	opts := map[string]bool{}
	for _, option := range parts[1:] {
		option = strings.ToLower(strings.TrimSpace(option))
		if option != "" {
			opts[option] = true
		}
	}
	return name, opts
}

func parseGormTag(raw string) map[string]string {
	out := map[string]string{}
	for _, item := range strings.Split(raw, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, value, found := strings.Cut(item, ":")
		key = strings.ToLower(strings.TrimSpace(key))
		if !found {
			value = "true"
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func hasGormOption(options map[string]string, name string) bool {
	_, ok := options[strings.ToLower(name)]
	return ok
}

func gormBool(options map[string]string, name string, want bool) bool {
	value, ok := options[strings.ToLower(name)]
	if !ok {
		return false
	}
	if value == "" || strings.EqualFold(value, "true") {
		return want
	}
	if strings.EqualFold(value, "false") {
		return !want
	}
	return false
}

func expression(decl *structSource, expr ast.Expr) string {
	return formatExpression(decl.fset, expr)
}

func formatExpression(fset *token.FileSet, expr ast.Expr) string {
	var b bytes.Buffer
	if err := format.Node(&b, fset, expr); err != nil {
		return ""
	}
	return b.String()
}

func canonicalExpression(imports map[string]string, fset *token.FileSet, expr ast.Expr) string {
	typ := formatExpression(fset, expr)
	for alias, path := range imports {
		if alias == "_" || alias == "." {
			continue
		}
		typ = strings.ReplaceAll(typ, alias+".", canonicalPackageName(path)+".")
	}
	return typ
}

func canonicalPackageName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func (pkg *packageSource) underlyingType(imports map[string]string, fset *token.FileSet, expr ast.Expr, seen map[string]bool) string {
	switch expr := expr.(type) {
	case *ast.StarExpr:
		return pkg.underlyingType(imports, fset, expr.X, seen)
	case *ast.ParenExpr:
		return pkg.underlyingType(imports, fset, expr.X, seen)
	case *ast.IndexExpr:
		if genericNullable(expr.X) {
			return pkg.underlyingType(imports, fset, expr.Index, seen)
		}
	case *ast.IndexListExpr:
		if genericNullable(expr.X) && len(expr.Indices) == 1 {
			return pkg.underlyingType(imports, fset, expr.Indices[0], seen)
		}
	case *ast.Ident:
		if source := pkg.types[expr.Name]; source != nil && !seen[expr.Name] {
			seen[expr.Name] = true
			resolved := pkg.underlyingType(source.imports, source.fset, source.typ, seen)
			delete(seen, expr.Name)
			return resolved
		}
	}
	return canonicalExpression(imports, fset, expr)
}

func databaseType(field Field) string {
	if field.UnderlyingType != "" {
		return field.UnderlyingType
	}
	if field.CanonicalType != "" {
		return field.CanonicalType
	}
	return field.GoType
}

func importedRelationCandidate(expr ast.Expr, imports map[string]string) bool {
	for {
		switch wrapped := expr.(type) {
		case *ast.StarExpr:
			expr = wrapped.X
		case *ast.ArrayType:
			expr = wrapped.Elt
		case *ast.ParenExpr:
			expr = wrapped.X
		default:
			goto unwrapped
		}
	}

unwrapped:
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	path := imports[alias.Name]
	if path == "" {
		return false
	}
	switch path {
	case "time", "database/sql", "encoding/json", "net", "net/url", "math/big", "gorm.io/gorm":
		return false
	}
	return !strings.Contains(path, "uuid") && !strings.Contains(path, "decimal")
}

func localTypeName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return localTypeName(expr.X)
	case *ast.ArrayType:
		return localTypeName(expr.Elt)
	case *ast.IndexExpr:
		return localTypeName(expr.X)
	case *ast.IndexListExpr:
		return localTypeName(expr.X)
	case *ast.ParenExpr:
		return localTypeName(expr.X)
	default:
		return ""
	}
}

func nullableType(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.StarExpr:
		return true
	case *ast.ParenExpr:
		return nullableType(expr.X)
	case *ast.IndexExpr:
		return genericNullable(expr.X)
	case *ast.IndexListExpr:
		return genericNullable(expr.X)
	case *ast.SelectorExpr:
		return expr.Sel.Name == "DeletedAt" || strings.HasPrefix(expr.Sel.Name, "Null")
	case *ast.Ident:
		return strings.HasPrefix(expr.Name, "Null")
	default:
		return false
	}
}

func genericNullable(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name == "Opt" || expr.Name == "Null"
	case *ast.SelectorExpr:
		return expr.Sel.Name == "Opt" || expr.Sel.Name == "Null"
	default:
		return false
	}
}

func integerType(typ string) bool {
	switch strings.TrimSpace(typ) {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	default:
		return false
	}
}

func isGormModel(expr ast.Expr, imports map[string]string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Model" {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	return ok && imports[alias.Name] == "gorm.io/gorm"
}

func gormModelFields(prefix, file string, line int) []Field {
	return []Field{
		{Name: "ID", Column: prefix + "id", GoType: "uint", CanonicalType: "uint", UnderlyingType: "uint", File: displayPath(file), Line: line, PrimaryKey: true, Auto: true},
		{Name: "CreatedAt", Column: prefix + "created_at", GoType: "time.Time", CanonicalType: "time.Time", UnderlyingType: "time.Time", File: displayPath(file), Line: line},
		{Name: "UpdatedAt", Column: prefix + "updated_at", GoType: "time.Time", CanonicalType: "time.Time", UnderlyingType: "time.Time", File: displayPath(file), Line: line},
		{Name: "DeletedAt", Column: prefix + "deleted_at", GoType: "gorm.DeletedAt", CanonicalType: "gorm.DeletedAt", UnderlyingType: "gorm.DeletedAt", File: displayPath(file), Line: line, Nullable: true},
	}
}

package modelscan

import (
	"bytes"
	"go/ast"
	"go/format"
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
		// A named local struct is a relation/bookkeeping field even without an
		// explicit rel tag, matching crud's runtime schema rules.
		if name := localTypeName(raw.Type); name != "" && pkg.structs[name] != nil {
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
				Name:       ident.Name,
				Column:     column,
				GoType:     expression(decl, raw.Type),
				File:       displayPath(decl.file),
				Line:       line,
				Nullable:   nullableType(raw.Type),
				PrimaryKey: dbOpts["pk"] || dbOpts["primarykey"] || dbOpts["primary_key"] || hasGormOption(gormOpts, "primarykey"),
				Auto:       dbOpts["auto"] || dbOpts["identity"] || dbOpts["serial"] || dbOpts["autoincrement"] || gormBool(gormOpts, "autoincrement", true),
				NoAuto:     dbOpts["noauto"] || gormBool(gormOpts, "autoincrement", false),
				Immutable:  dbOpts["immutable"] || dbOpts["readonly"] || dbOpts["insertonly"] || dbOpts["insert_only"] || strings.EqualFold(gormOpts["<-"], "create"),
				Generated:  dbOpts["generated"] || dbOpts["computed"] || hasGormOption(gormOpts, "->"),
				Version:    dbOpts["version"] || dbOpts["lock"],
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
		if fields[i].PrimaryKey && !fields[i].Auto && !fields[i].NoAuto && integerType(fields[i].GoType) {
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
	var b bytes.Buffer
	if err := format.Node(&b, decl.fset, expr); err != nil {
		return ""
	}
	return b.String()
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
		{Name: "ID", Column: prefix + "id", GoType: "uint", File: displayPath(file), Line: line, PrimaryKey: true, Auto: true},
		{Name: "CreatedAt", Column: prefix + "created_at", GoType: "time.Time", File: displayPath(file), Line: line},
		{Name: "UpdatedAt", Column: prefix + "updated_at", GoType: "time.Time", File: displayPath(file), Line: line},
		{Name: "DeletedAt", Column: prefix + "deleted_at", GoType: "gorm.DeletedAt", File: displayPath(file), Line: line, Nullable: true},
	}
}

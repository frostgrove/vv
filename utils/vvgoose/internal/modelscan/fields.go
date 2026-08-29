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

func (this *packageSource) fields(decl *structSource, prefix string, seen map[string]bool) []Field {
	if seen[decl.name] {
		return nil
	}
	seen[decl.name] = true
	defer delete(seen, decl.name)

	var out []Field
	for _, raw := range decl.typ.Fields.List {
		tag := fieldTag(raw)
		database, hasDB := tag.Lookup("db")
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
				if embedded := this.structs[name]; embedded != nil {
					out = append(out, this.fields(embedded, embeddedPrefix, seen)...)
				}
				continue
			}
			if isGormModel(raw.Type, decl.imports) {
				out = append(out, gormModelFields(embeddedPrefix, decl.file, decl.fset.Position(raw.Pos()).Line)...)
			}
			continue
		}

		if database == "-" || hasRel || hasGormOption(gormOpts, "-") {
			continue
		}
		// A named local struct is normally a relation/bookkeeping field. GORM's
		// explicit embedded spelling is the exception and flattens its columns.
		if name := localTypeName(raw.Type); name != "" && this.structs[name] != nil {
			if hasGormOption(gormOpts, "embedded") || gormOpts["embeddedprefix"] != "" {
				out = append(out, this.fields(this.structs[name], prefix+gormOpts["embeddedprefix"], seen)...)
			}
			continue
		}
		// Without type-checking, an imported selector that is not one of the
		// database scalar packages is much more likely to be a relation than a
		// column. An explicit db/column tag remains the author's escape hatch for
		// an imported scalar type.
		if !hasDB && gormOpts["column"] == "" && !gormScalarOption(gormOpts) &&
			(gormRelationOption(gormOpts) || importedRelationCandidate(raw.Type, decl.imports)) {
			continue
		}

		for _, ident := range raw.Names {
			if !ident.IsExported() {
				continue
			}
			column, dbOpts := parseDBTag(database)
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
				UnderlyingType: this.underlyingType(decl.imports, decl.fset, raw.Type, map[string]bool{}),
				File:           displayPath(decl.file),
				Line:           line,
				Nullable:       this.resolvedNullable(decl.imports, raw.Type, map[string]bool{}),
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
	primaryKeys := 0
	for i := range fields {
		if fields[i].PrimaryKey {
			primaryKeys++
		}
	}
	if primaryKeys == 0 {
		for i := range fields {
			if fields[i].Name == "ID" || fields[i].Column == "id" {
				fields[i].PrimaryKey = true
				primaryKeys = 1
				break
			}
		}
	}
	for i := range fields {
		// A single integer key follows vv's auto-ID convention. For a
		// composite key the source must name the one auto field explicitly;
		// inferring auto on every integer component produces invalid DDL.
		if primaryKeys == 1 && fields[i].PrimaryKey && !fields[i].Auto && !fields[i].NoAuto && integerType(databaseType(fields[i])) {
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
	options := map[string]bool{}
	for _, option := range parts[1:] {
		option = strings.ToLower(strings.TrimSpace(option))
		if option != "" {
			options[option] = true
		}
	}
	return name, options
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
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.SelectorExpr:
		if alias, ok := expr.X.(*ast.Ident); ok {
			if path := imports[alias.Name]; path != "" {
				return canonicalPackageName(path) + "." + expr.Sel.Name
			}
		}
		return formatExpression(fset, expr)
	case *ast.StarExpr:
		return "*" + canonicalExpression(imports, fset, expr.X)
	case *ast.ArrayType:
		length := ""
		if expr.Len != nil {
			length = formatExpression(fset, expr.Len)
		}
		return "[" + length + "]" + canonicalExpression(imports, fset, expr.Elt)
	case *ast.MapType:
		return "map[" + canonicalExpression(imports, fset, expr.Key) + "]" + canonicalExpression(imports, fset, expr.Value)
	case *ast.IndexExpr:
		return canonicalExpression(imports, fset, expr.X) + "[" + canonicalExpression(imports, fset, expr.Index) + "]"
	case *ast.IndexListExpr:
		parts := make([]string, 0, len(expr.Indices))
		for _, index := range expr.Indices {
			parts = append(parts, canonicalExpression(imports, fset, index))
		}
		return canonicalExpression(imports, fset, expr.X) + "[" + strings.Join(parts, ",") + "]"
	case *ast.ParenExpr:
		return "(" + canonicalExpression(imports, fset, expr.X) + ")"
	case *ast.Ellipsis:
		return "..." + canonicalExpression(imports, fset, expr.Elt)
	default:
		return formatExpression(fset, expr)
	}
}

func canonicalPackageName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func (this *packageSource) underlyingType(imports map[string]string, fset *token.FileSet, expr ast.Expr, seen map[string]bool) string {
	switch expr := expr.(type) {
	case *ast.StarExpr:
		return this.underlyingType(imports, fset, expr.X, seen)
	case *ast.ParenExpr:
		return this.underlyingType(imports, fset, expr.X, seen)
	case *ast.IndexExpr:
		if genericNullable(expr.X) {
			return this.underlyingType(imports, fset, expr.Index, seen)
		}
	case *ast.IndexListExpr:
		if genericNullable(expr.X) && len(expr.Indices) == 1 {
			return this.underlyingType(imports, fset, expr.Indices[0], seen)
		}
	case *ast.Ident:
		if source := this.types[expr.Name]; source != nil && !seen[expr.Name] {
			seen[expr.Name] = true
			resolved := this.underlyingType(source.imports, source.fset, source.typ, seen)
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
	relationShape := false
	for {
		switch wrapped := expr.(type) {
		case *ast.StarExpr:
			relationShape = true
			expr = wrapped.X
		case *ast.ArrayType:
			relationShape = true
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
	return relationShape && !knownImportedScalar(path, selector.Sel.Name)
}

// knownImportedScalar names source packages whose exported values are database
// scalar wrappers rather than domain entities. Keep this list deliberately
// narrow: treating an arbitrary imported pointer as a column is the dangerous
// direction, because it can produce plausible SQL for a relation. UUID and
// Decimal are also stable type names already understood by sqlType, independent
// of which implementation package an application chose.
func knownImportedScalar(path, typeName string) bool {
	if typeName == "UUID" || typeName == "Decimal" {
		return true
	}
	switch path {
	case "time", "database/sql", "encoding/json", "net", "net/netip", "net/url", "math/big", "gorm.io/gorm",
		"github.com/jackc/pgtype", "github.com/lib/pq", "github.com/jmoiron/sqlx/types", "github.com/sqlc-dev/pqtype",
		"github.com/oklog/ulid/v2", "github.com/segmentio/ksuid":
		return true
	}
	return strings.HasPrefix(path, "github.com/jackc/pgx/") && strings.HasSuffix(path, "/pgtype") ||
		modulePath(path, "github.com/guregu/null") || modulePath(path, "gopkg.in/guregu/null") ||
		modulePath(path, "github.com/volatiletech/null") || modulePath(path, "github.com/aarondl/null") ||
		modulePath(path, "github.com/google/uuid") || modulePath(path, "github.com/gofrs/uuid") ||
		modulePath(path, "github.com/satori/go.uuid") || modulePath(path, "github.com/shopspring/decimal")
}

func modulePath(path, module string) bool {
	return path == module || strings.HasPrefix(path, module+"/")
}

func gormRelationOption(options map[string]string) bool {
	for _, name := range []string{"foreignkey", "references", "many2many", "polymorphic", "polymorphicvalue"} {
		if hasGormOption(options, name) {
			return true
		}
	}
	return false
}

func gormScalarOption(options map[string]string) bool {
	for _, name := range []string{"type", "serializer", "size", "precision", "scale"} {
		if hasGormOption(options, name) {
			return true
		}
	}
	return false
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

func (this *packageSource) resolvedNullable(imports map[string]string, expr ast.Expr, seen map[string]bool) bool {
	if nullableType(expr) {
		return true
	}
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return this.resolvedNullable(imports, expr.X, seen)
	case *ast.Ident:
		if source := this.types[expr.Name]; source != nil && !seen[expr.Name] {
			seen[expr.Name] = true
			resolved := this.resolvedNullable(source.imports, source.typ, seen)
			delete(seen, expr.Name)
			return resolved
		}
	}
	return false
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

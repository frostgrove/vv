package codegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"time"
)

type sourceTypes struct {
	info       *types.Info
	locals     map[*types.Package]bool
	interfaces scalarInterfaces
}

type scalarInterfaces struct {
	valuer          *types.Interface
	scanner         *types.Interface
	textMarshaler   *types.Interface
	textUnmarshaler *types.Interface
}

func (this *generator) prepareTypes(fset *token.FileSet, parsed map[string]*ast.Package) {
	lookup := &exportLookup{dir: this.dir, exports: map[string]string{}, failures: map[string]error{}}
	imp := importer.ForCompiler(fset, "gc", lookup.open)
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Instances:  map[*ast.Ident]types.Instance{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	view := &sourceTypes{info: info, locals: map[*types.Package]bool{}}
	this.types = view

	names := make([]string, 0, len(parsed))
	for name := range parsed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		parsedPackage := parsed[name]
		fileNames := make([]string, 0, len(parsedPackage.Files))
		for fileName := range parsedPackage.Files {
			fileNames = append(fileNames, fileName)
		}
		sort.Strings(fileNames)
		files := make([]*ast.File, 0, len(fileNames))
		for _, fileName := range fileNames {
			files = append(files, parsedPackage.Files[fileName])
		}
		path := "codegen.local/" + name
		if len(names) == 1 && this.modelImport != "" {
			path = this.modelImport
		}
		config := &types.Config{
			Importer:         imp,
			IgnoreFuncBodies: true,
			FakeImportC:      true,
			Error:            func(error) {},
		}
		checked, _ := config.Check(path, fset, files, info)
		if checked != nil {
			view.locals[checked] = true
		}
	}
	view.interfaces = scalarInterfaces{
		valuer:          importedInterface(imp, "database/sql/driver", "Valuer"),
		scanner:         importedInterface(imp, "database/sql", "Scanner"),
		textMarshaler:   importedInterface(imp, "encoding", "TextMarshaler"),
		textUnmarshaler: importedInterface(imp, "encoding", "TextUnmarshaler"),
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-mod=readonly", "-deps", "-export", "-json", path)
	cmd.Dir = this.dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var listed struct {
			ImportPath string
			Export     string
		}
		if err := decoder.Decode(&listed); err != nil {
			if err != io.EOF && runErr == nil {
				runErr = err
			}
			break
		}
		if listed.ImportPath != "" && listed.Export != "" {
			this.exports[listed.ImportPath] = listed.Export
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
	err := fmt.Errorf("codegen: resolve Go type package %q: %s", path, message)
	this.failures[path] = err
	return nil, err
}

func importedInterface(imp types.Importer, path, name string) *types.Interface {
	pkg, err := imp.Import(path)
	if err != nil || pkg == nil {
		return nil
	}
	object := pkg.Scope().Lookup(name)
	if object == nil {
		return nil
	}
	iface, _ := types.Unalias(object.Type()).Underlying().(*types.Interface)
	if iface == nil {
		return nil
	}
	return iface.Complete()
}

func (this *generator) goType(expr ast.Expr) types.Type {
	if this.types == nil || this.types.info == nil {
		return nil
	}
	t := this.types.info.TypeOf(expr)
	if !usableTypeShape(t, map[types.Type]bool{}) {
		return nil
	}
	return t
}

func usableTypeShape(t types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	if seen[t] {
		return true
	}
	seen[t] = true
	t = types.Unalias(t)
	switch value := t.(type) {
	case *types.Basic:
		return value.Kind() != types.Invalid
	case *types.Pointer:
		return usableTypeShape(value.Elem(), seen)
	case *types.Slice:
		return usableTypeShape(value.Elem(), seen)
	case *types.Array:
		return usableTypeShape(value.Elem(), seen)
	case *types.Map:
		return usableTypeShape(value.Key(), seen) && usableTypeShape(value.Elem(), seen)
	case *types.Chan:
		return usableTypeShape(value.Elem(), seen)
	case *types.Named:
		if basic, ok := value.Underlying().(*types.Basic); ok {
			return basic.Kind() != types.Invalid
		}
		return usableTypeShape(value.Underlying(), seen)
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			if !usableTypeShape(value.Field(index).Type(), seen) {
				return false
			}
		}
	}
	return true
}

func (this *generator) renderedType(t types.Type) string {
	if t == nil {
		return ""
	}
	return types.TypeString(t, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		if this.types != nil && this.types.locals[pkg] {
			return this.modelAlias
		}
		return this.importAlias(pkg.Path(), pkg.Name())
	})
}

func (this *generator) importAlias(path, preferred string) string {
	if alias := this.pathAliases[path]; alias != "" {
		if owner := this.aliasPaths[alias]; owner == "" || owner == path {
			this.aliasPaths[alias] = path
			this.imports[alias] = path
			return alias
		}
		delete(this.pathAliases, path)
	}
	if alias := this.sharedImportAlias(path); alias != "" {
		if owner := this.aliasPaths[alias]; owner == "" || owner == path {
			this.pathAliases[path] = alias
			this.aliasPaths[alias] = path
			this.imports[alias] = path
			return alias
		}
	}
	alias := allocateReadableImportAlias(preferred, path, this.usedAliases, false)
	this.pathAliases[path] = alias
	this.aliasPaths[alias] = path
	this.imports[alias] = path
	return alias
}

func (this *generator) anonymousName(field *ast.Field) string {
	return embeddedSyntaxName(field.Type)
}

func embeddedSyntaxName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	case *ast.StarExpr:
		return embeddedSyntaxName(value.X)
	case *ast.IndexExpr:
		return embeddedSyntaxName(value.X)
	case *ast.IndexListExpr:
		return embeddedSyntaxName(value.X)
	case *ast.ParenExpr:
		return embeddedSyntaxName(value.X)
	default:
		return ""
	}
}

func unalias(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	return types.Unalias(t)
}

func pointerOf(t types.Type) (*types.Pointer, bool) {
	t = unalias(t)
	if t == nil {
		return nil, false
	}
	if pointer, ok := t.(*types.Pointer); ok {
		return pointer, true
	}
	pointer, ok := t.Underlying().(*types.Pointer)
	return pointer, ok
}

func dereference(t types.Type) types.Type {
	for {
		pointer, ok := pointerOf(t)
		if !ok {
			return unalias(t)
		}
		t = pointer.Elem()
	}
}

func sliceElement(t types.Type) (types.Type, bool) {
	t = unalias(t)
	if t == nil {
		return nil, false
	}
	slice, ok := t.Underlying().(*types.Slice)
	if !ok {
		return nil, false
	}
	return slice.Elem(), true
}

func structOf(t types.Type) (*types.Struct, bool) {
	t = dereference(t)
	if t == nil {
		return nil, false
	}
	structure, ok := t.Underlying().(*types.Struct)
	return structure, ok
}

func namedType(t types.Type) (*types.Named, bool) {
	t = dereference(t)
	named, ok := t.(*types.Named)
	if !ok {
		return nil, false
	}
	return named.Origin(), true
}

func isNamed(t types.Type, path, name string) bool {
	named, ok := namedType(t)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Pkg().Path() == path && named.Obj().Name() == name
}

func isOptTypeSource(t types.Type) bool {
	return isNamed(t, DefaultUtilsPkg, "Opt")
}

func implements(t types.Type, iface *types.Interface) bool {
	return t != nil && iface != nil && types.Implements(t, iface)
}

func (this *generator) scalarStruct(t types.Type) bool {
	if t == nil {
		return false
	}
	if isNamed(t, "time", "Time") && !isPointerSource(t) {
		return true
	}

	if !methodSetReliable(t) {
		return false
	}
	pointer := types.NewPointer(t)
	if this.types == nil {
		return false
	}
	i := this.types.interfaces
	return implements(t, i.valuer) || implements(pointer, i.valuer) ||
		implements(pointer, i.scanner) || implements(t, i.textMarshaler) ||
		implements(pointer, i.textUnmarshaler)
}

func methodSetReliable(t types.Type) bool {
	base := dereference(t)
	if base == nil {
		return false
	}
	structure, ok := base.Underlying().(*types.Struct)
	if !ok {
		return true
	}
	for index := 0; index < structure.NumFields(); index++ {
		if !usableTypeShape(structure.Field(index).Type(), map[types.Type]bool{}) {
			return false
		}
	}
	return true
}

func isPointerSource(t types.Type) bool {
	_, ok := pointerOf(t)
	return ok
}

func (this *generator) flattenableStruct(t types.Type) bool {
	_, structure := structOf(t)
	return structure && !isOptTypeSource(t) && !this.scalarStruct(t)
}

func (this *generator) relationCandidate(t types.Type) bool {
	if element, slice := sliceElement(t); slice {
		t = element
	}
	t = dereference(t)
	if t == nil || isOptTypeSource(t) || this.scalarStruct(t) {
		return false
	}
	_, ok := t.Underlying().(*types.Struct)
	return ok
}

func (this *generator) wellKnownFields(t types.Type) ([]field, bool) {
	if !isNamed(t, "gorm.io/gorm", "Model") {
		return nil, false
	}
	fields := wellKnownEmbeds["gorm.Model"]
	out := make([]field, 0, len(fields))
	for _, item := range fields {
		this.exclude(&item)
		out = append(out, item)
	}
	return out, true
}

func (this *generator) flattenType(modelName, display string, t types.Type, seen map[types.Type]bool) ([]field, []string) {
	if fields, ok := this.wellKnownFields(t); ok {
		return fields, nil
	}
	base := dereference(t)
	structure, ok := structOf(base)
	if !ok {
		return nil, []string{fmt.Sprintf("model %s embeds unresolved type %s; flatten it into this package, tag it db:\"-\", or add an audited well-known embed", modelName, display)}
	}
	if seen[base] {
		return nil, []string{fmt.Sprintf("model %s recursively embeds %s", modelName, display)}
	}
	seen[base] = true
	defer delete(seen, base)

	var out []field
	var problems []string
	for index := 0; index < structure.NumFields(); index++ {
		variable := structure.Field(index)
		tag := reflect.StructTag(structure.Tag(index))
		database, hasDB := tag.Lookup("db")
		relation, hasRel := tag.Lookup("rel")
		if hasDB && database == "-" {
			continue
		}
		fieldType := variable.Type()
		if variable.Embedded() && !hasDB && !hasRel && this.flattenableStruct(fieldType) {
			if isPointerSource(fieldType) {
				problems = append(problems, fmt.Sprintf(
					"model %s embeds pointer %s through %s; runtime metadata refuses embedded pointer structs, so embed a value or tag it db:\"-\"",
					modelName, this.renderedType(fieldType), display))
				continue
			}
			nested, nestedProblems := this.flattenType(modelName, this.renderedType(fieldType), fieldType, seen)
			out = append(out, nested...)
			problems = append(problems, nestedProblems...)
			continue
		}
		if !variable.Exported() {
			if hasDB {
				problems = append(problems, fmt.Sprintf(
					"model %s maps unexported embedded field %s.%s; rename it or tag it db:\"-\"",
					modelName, display, variable.Name()))
			}
			continue
		}
		if problem := this.appendResolvedField(&out, modelName, variable.Name(), this.renderedType(fieldType), fieldType, database, hasDB, relation, hasRel); problem != "" {
			problems = append(problems, problem+" through "+display)
		}
	}
	return out, problems
}

func (this *generator) appendResolvedField(out *[]field, modelName, name, rendered string, typ types.Type, database string, hasDB bool, relation string, hasRel bool) string {
	if hasDB && database == "-" {
		*out = append(*out, field{Name: name, Type: rendered, Tag: database, Skip: true})
		return ""
	}
	if this.relationCandidate(typ) {
		if !hasRel || relation == "-" || this.skip[name] {
			return ""
		}
		*out = append(*out, field{
			Name: name, Type: rendered, Tag: database, Rel: relation, HasRel: true,
			RelTarget: this.localRelationTarget(typ),
		})
		return ""
	}
	if hasRel && relation != "-" {
		return fmt.Sprintf("model %s field %s has a rel tag on %s, which is not a struct, *struct or []struct", modelName, name, rendered)
	}
	if inaccessible := this.inaccessibleTypeName(typ, map[types.Type]bool{}); inaccessible != "" {
		return fmt.Sprintf("model %s field %s has type %s, whose name %s is not accessible from generated code", modelName, name, rendered, inaccessible)
	}
	item := this.columnField(name, rendered, typ, database)
	item.Rel, item.HasRel = relation, hasRel
	this.exclude(&item)
	*out = append(*out, item)
	return ""
}

func (this *generator) localRelationTarget(t types.Type) string {
	if element, slice := sliceElement(t); slice {
		t = element
	}
	t = dereference(t)
	named, ok := t.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil || this.types == nil || !this.types.locals[named.Obj().Pkg()] {
		return ""
	}
	return named.Obj().Name()
}

func (this *generator) inaccessibleTypeName(t types.Type, seen map[types.Type]bool) string {
	if t == nil || seen[t] {
		return ""
	}
	seen[t] = true
	defer delete(seen, t)

	accessible := func(object types.Object) bool {
		if object == nil || object.Pkg() == nil {
			return true
		}
		return (this.types != nil && this.types.locals[object.Pkg()] && this.modelAlias == "") || object.Exported()
	}
	inaccessibleObject := func(object types.Object) string {
		if accessible(object) {
			return ""
		}
		if object.Pkg() == nil {
			return object.Name()
		}
		return object.Pkg().Path() + "." + object.Name()
	}
	typeArguments := func(arguments *types.TypeList) string {
		if arguments == nil {
			return ""
		}
		for index := 0; index < arguments.Len(); index++ {
			if problem := this.inaccessibleTypeName(arguments.At(index), seen); problem != "" {
				return problem
			}
		}
		return ""
	}

	switch value := t.(type) {
	case *types.Alias:
		if !accessible(value.Obj()) {
			return this.renderedType(t)
		}
		return typeArguments(value.TypeArgs())
	case *types.Named:
		if !accessible(value.Obj()) {
			return this.renderedType(t)
		}
		return typeArguments(value.TypeArgs())
	case *types.Pointer:
		return this.inaccessibleTypeName(value.Elem(), seen)
	case *types.Slice:
		return this.inaccessibleTypeName(value.Elem(), seen)
	case *types.Array:
		return this.inaccessibleTypeName(value.Elem(), seen)
	case *types.Map:
		if problem := this.inaccessibleTypeName(value.Key(), seen); problem != "" {
			return problem
		}
		return this.inaccessibleTypeName(value.Elem(), seen)
	case *types.Chan:
		return this.inaccessibleTypeName(value.Elem(), seen)
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			field := value.Field(index)

			if problem := inaccessibleObject(field); problem != "" {
				return problem
			}
			if problem := this.inaccessibleTypeName(field.Type(), seen); problem != "" {
				return problem
			}
		}
	case *types.Signature:
		if problem := this.inaccessibleTuple(value.Params(), seen); problem != "" {
			return problem
		}
		return this.inaccessibleTuple(value.Results(), seen)
	case *types.Interface:
		for index := 0; index < value.NumExplicitMethods(); index++ {
			method := value.ExplicitMethod(index)

			if problem := inaccessibleObject(method); problem != "" {
				return problem
			}
			if problem := this.inaccessibleTypeName(method.Type(), seen); problem != "" {
				return problem
			}
		}
		for index := 0; index < value.NumEmbeddeds(); index++ {
			if problem := this.inaccessibleTypeName(value.EmbeddedType(index), seen); problem != "" {
				return problem
			}
		}
	case *types.TypeParam:
		return value.Obj().Name()
	case *types.Union:
		for index := 0; index < value.Len(); index++ {
			if problem := this.inaccessibleTypeName(value.Term(index).Type(), seen); problem != "" {
				return problem
			}
		}
	}
	return ""
}

func (this *generator) inaccessibleTuple(tuple *types.Tuple, seen map[types.Type]bool) string {
	if tuple == nil {
		return ""
	}
	for index := 0; index < tuple.Len(); index++ {
		if problem := this.inaccessibleTypeName(tuple.At(index).Type(), seen); problem != "" {
			return problem
		}
	}
	return ""
}

func (this *generator) columnField(name, rendered string, typ types.Type, database string) field {
	item := field{Name: name, Type: rendered, Tag: database, Integral: integralSourceType(typ, rendered)}
	for _, option := range strings.Split(database, ",")[1:] {
		switch option {
		case "pk", "primarykey", "primary_key":
			item.ExplicitPK = true
		case "auto", "identity", "serial", "autoincrement":
			item.Auto = true
		case "noauto":
			item.NoAuto = true
		case "immutable", "readonly", "insertonly", "insert_only":
			item.Immutable = true
		case "generated", "computed":
			item.Generated = true
		case "serverowned", "server_owned":
			item.ServerOwned = true
		case "secret":
			item.Secret = true
		case "tombstone", "softdelete", "soft_delete":
			item.ServerOwned = true
			item.Tombstone = true
		case "version", "lock":
			item.Version = true
		}
	}
	return item
}

func integralSourceType(typ types.Type, rendered string) bool {
	if typ != nil {
		basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
		return ok && basic.Kind() != types.Uintptr && basic.Info()&types.IsInteger != 0
	}
	switch strings.TrimSpace(rendered) {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "byte", "rune":
		return true
	default:
		return false
	}
}

package cachegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	frameworkcache "github.com/frostgrove/vv/cache"
)

type declaration struct {
	variable    string
	logicalName string
	keySyntax   string
	valueSyntax string
	keyType     types.Type
	valueType   types.Type
	imports     map[string]string
	profile     profilePlan
	key         keyPlan
}

type profilePlan struct {
	expression  string
	description frameworkcache.ProfileDescription
}

type keyPlan struct {
	inferredMode  string
	partitionName string
}

func discover(loaded *loadedPackage) ([]declaration, error) {
	result := make([]declaration, 0)
	problems := make([]string, 0)
	supported := map[*ast.Ident]struct{}{}
	for _, file := range sortedFiles(loaded.files, loaded.fileNames) {
		for _, raw := range file.Decls {
			general, ok := raw.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, item := range general.Specs {
				spec, ok := item.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if len(spec.Names) != len(spec.Values) {
					continue
				}
				for index, value := range spec.Values {
					if reference := directAutoReference(loaded.info, value); reference != nil {
						supported[reference] = struct{}{}
					}
					found, err := discoverDeclaration(loaded, file, spec.Names[index], value)
					if err != nil {
						problems = append(problems, strings.TrimPrefix(err.Error(), "cachegen: "))
						continue
					}
					if found != nil {
						result = append(result, *found)
					}
				}
			}
		}
	}
	for _, file := range sortedFiles(loaded.files, loaded.fileNames) {
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if !ok || !isCacheAutoObject(loaded.info.Uses[identifier]) {
				return true
			}
			if _, ok := supported[identifier]; ok {
				return true
			}
			position := loaded.fset.Position(identifier.Pos())
			problems = append(problems, fmt.Sprintf("%s:%d:%d: cache.Auto is only supported as a direct package-level variable initializer", position.Filename, position.Line, position.Column))
			return true
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].logicalName < result[right].logicalName
	})
	if len(problems) != 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("cachegen: declarations failed: %s", strings.Join(problems, "; "))
	}
	return result, nil
}

func directAutoReference(info *types.Info, expression ast.Expr) *ast.Ident {
	call, ok := unparen(expression).(*ast.CallExpr)
	if !ok {
		return nil
	}
	indexed, ok := unparen(call.Fun).(*ast.IndexListExpr)
	if !ok || len(indexed.Indices) != 2 {
		return nil
	}
	selector, ok := unparen(indexed.X).(*ast.SelectorExpr)
	if !ok || !isPackageSelector(info, selector, cacheImportPath) {
		return nil
	}
	return selector.Sel
}

func isCacheAutoObject(object types.Object) bool {
	function, ok := object.(*types.Func)
	return ok && function.Name() == "Auto" && function.Pkg() != nil && function.Pkg().Path() == cacheImportPath
}

func discoverDeclaration(loaded *loadedPackage, file *ast.File, name *ast.Ident, expression ast.Expr) (*declaration, error) {
	call, ok := unparen(expression).(*ast.CallExpr)
	if !ok {
		return nil, nil
	}
	keyExpression, valueExpression, ok := autoTypes(loaded.info, call.Fun)
	if !ok {
		return nil, nil
	}
	if len(call.Args) > 1 {
		return nil, fmt.Errorf("cachegen: %s: cache.Auto accepts at most one profile", name.Name)
	}
	if name.Name == "_" {
		return nil, fmt.Errorf("cachegen: cache.Auto must be assigned to a named package-level variable")
	}
	keyType := loaded.info.TypeOf(keyExpression)
	valueType := loaded.info.TypeOf(valueExpression)
	if keyType == nil || valueType == nil {
		return nil, fmt.Errorf("cachegen: %s: cannot resolve key or value type", name.Name)
	}
	if _, pointer := types.Unalias(keyType).Underlying().(*types.Pointer); pointer {
		return nil, fmt.Errorf("cachegen: %s key: top-level pointer keys require a manual cache.KeyFunc and cache.New", name.Name)
	}
	if err := validateKeyType(keyType, map[types.Type]bool{}); err != nil {
		return nil, fmt.Errorf("cachegen: %s key: %w; declare cache.KeyFunc manually and use cache.New", name.Name, err)
	}
	if !isExactTimeValue(valueType) {
		if err := validateValueType(valueType, map[types.Type]bool{}); err != nil {
			return nil, fmt.Errorf("cachegen: %s value: %w; declare a cache.Codec manually and use cache.New", name.Name, err)
		}
	}
	key, err := analyzeKey(keyType)
	if err != nil {
		return nil, fmt.Errorf("cachegen: %s key: %w", name.Name, err)
	}
	profile, err := parseProfile(loaded.info, call.Args)
	if err != nil {
		return nil, fmt.Errorf("cachegen: %s profile: %w", name.Name, err)
	}
	keySyntax, err := renderExpression(loaded.fset, keyExpression)
	if err != nil {
		return nil, err
	}
	valueSyntax, err := renderExpression(loaded.fset, valueExpression)
	if err != nil {
		return nil, err
	}
	imports, err := typeImports(loaded, file, keyExpression, valueExpression)
	if err != nil {
		return nil, fmt.Errorf("cachegen: %s types: %w", name.Name, err)
	}
	return &declaration{
		variable:    name.Name,
		logicalName: loaded.importPath + "." + name.Name,
		keySyntax:   keySyntax,
		valueSyntax: valueSyntax,
		keyType:     keyType,
		valueType:   valueType,
		imports:     imports,
		profile:     profile,
		key:         key,
	}, nil
}

func autoTypes(info *types.Info, expression ast.Expr) (ast.Expr, ast.Expr, bool) {
	expression = unparen(expression)
	indexed, ok := expression.(*ast.IndexListExpr)
	if !ok || len(indexed.Indices) != 2 {
		return nil, nil, false
	}
	selector, ok := unparen(indexed.X).(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Auto" || !isPackageSelector(info, selector, cacheImportPath) {
		return nil, nil, false
	}
	return indexed.Indices[0], indexed.Indices[1], true
}

func isPackageSelector(info *types.Info, selector *ast.SelectorExpr, path string) bool {
	identifier, ok := unparen(selector.X).(*ast.Ident)
	if !ok {
		return false
	}
	packageName, ok := info.Uses[identifier].(*types.PkgName)
	return ok && packageName.Imported().Path() == path
}

func unparen(expression ast.Expr) ast.Expr {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = parenthesized.X
	}
}

func parseProfile(info *types.Info, arguments []ast.Expr) (profilePlan, error) {
	if len(arguments) == 0 {
		return profileOf("cache.Hot", frameworkcache.Hot), nil
	}
	profile, expression, err := parseProfileExpression(info, unparen(arguments[0]))
	if err != nil {
		return profilePlan{}, err
	}
	if _, err := profile.Build(); err != nil {
		return profilePlan{}, err
	}
	return profileOf(expression, profile), nil
}

func parseProfileExpression(info *types.Info, expression ast.Expr) (frameworkcache.Profile, string, error) {
	if selector, ok := expression.(*ast.SelectorExpr); ok && isPackageSelector(info, selector, cacheImportPath) {
		switch selector.Sel.Name {
		case "Hot":
			return frameworkcache.Hot, "cache.Hot", nil
		case "Warm":
			return frameworkcache.Warm, "cache.Warm", nil
		case "Durable":
			return frameworkcache.Durable, "cache.Durable", nil
		case "Disabled":
			return frameworkcache.Disabled, "cache.Disabled", nil
		}
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return frameworkcache.Profile{}, "", fmt.Errorf("profile must be Hot, Warm, Durable, Disabled, or one of them with supported overrides")
	}
	method, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || method.Sel.Name != "With" {
		return frameworkcache.Profile{}, "", fmt.Errorf("unsupported profile expression")
	}
	base, rendered, err := parseProfileExpression(info, unparen(method.X))
	if err != nil {
		return frameworkcache.Profile{}, "", err
	}
	options := make([]frameworkcache.Option, 0, len(call.Args))
	parts := make([]string, 0, len(call.Args))
	for _, argument := range call.Args {
		option, part, err := parseProfileOption(info, unparen(argument))
		if err != nil {
			return frameworkcache.Profile{}, "", err
		}
		options = append(options, option)
		parts = append(parts, part)
	}
	return base.With(options...), rendered + ".With(" + strings.Join(parts, ", ") + ")", nil
}

func parseProfileOption(info *types.Info, expression ast.Expr) (frameworkcache.Option, string, error) {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return nil, "", fmt.Errorf("profile override must be a cache option call")
	}
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || !isPackageSelector(info, selector, cacheImportPath) {
		return nil, "", fmt.Errorf("profile override must come from package cache")
	}
	switch selector.Sel.Name {
	case "MaxValueBytes", "MaxFlights", "MaxTransientWaiters":
		value, err := oneIntegerArgument(info, call)
		if err != nil {
			return nil, "", err
		}
		if int64(int(value)) != value {
			return nil, "", fmt.Errorf("option argument does not fit int")
		}
		if selector.Sel.Name == "MaxValueBytes" {
			return frameworkcache.MaxValueBytes(int(value)), fmt.Sprintf("cache.MaxValueBytes(%d)", value), nil
		}
		if selector.Sel.Name == "MaxTransientWaiters" {
			return frameworkcache.MaxTransientWaiters(int(value)), fmt.Sprintf("cache.MaxTransientWaiters(%d)", value), nil
		}
		return frameworkcache.MaxFlights(int(value)), fmt.Sprintf("cache.MaxFlights(%d)", value), nil
	case "MaxTransientBytes":
		value, err := oneIntegerArgument(info, call)
		if err != nil {
			return nil, "", err
		}
		return frameworkcache.MaxTransientBytes(value), fmt.Sprintf("cache.MaxTransientBytes(%d)", value), nil
	case "NegativeFor":
		value, err := oneIntegerArgument(info, call)
		if err != nil {
			return nil, "", err
		}
		return frameworkcache.NegativeFor(time.Duration(value)), fmt.Sprintf("cache.NegativeFor(%d)", value), nil
	case "FlightSaturation":
		value, rendered, err := parseFlightSaturation(info, call)
		if err != nil {
			return nil, "", err
		}
		return frameworkcache.FlightSaturation(value), "cache.FlightSaturation(" + rendered + ")", nil
	case "StaleBehavior":
		value, rendered, err := parseStalePolicy(info, call)
		if err != nil {
			return nil, "", err
		}
		return frameworkcache.StaleBehavior(value), "cache.StaleBehavior(" + rendered + ")", nil
	case "TransientSaturation":
		value, rendered, err := parseTransientSaturation(info, call)
		if err != nil {
			return nil, "", err
		}
		return frameworkcache.TransientSaturation(value), "cache.TransientSaturation(" + rendered + ")", nil
	default:
		return nil, "", fmt.Errorf("unsupported cache profile override %s", selector.Sel.Name)
	}
}

func parseTransientSaturation(info *types.Info, outer *ast.CallExpr) (frameworkcache.TransientSaturationPolicy, string, error) {
	if len(outer.Args) != 1 {
		return frameworkcache.TransientSaturationPolicy{}, "", fmt.Errorf("TransientSaturation expects one argument")
	}
	call, ok := unparen(outer.Args[0]).(*ast.CallExpr)
	if !ok {
		return frameworkcache.TransientSaturationPolicy{}, "", fmt.Errorf("unsupported transient saturation policy")
	}
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || !isPackageSelector(info, selector, cacheImportPath) {
		return frameworkcache.TransientSaturationPolicy{}, "", fmt.Errorf("unsupported transient saturation policy")
	}
	switch selector.Sel.Name {
	case "RejectTransient":
		if len(call.Args) != 0 {
			return frameworkcache.TransientSaturationPolicy{}, "", fmt.Errorf("RejectTransient expects no arguments")
		}
		return frameworkcache.RejectTransient(), "cache.RejectTransient()", nil
	case "WaitForTransient":
		value, err := oneIntegerArgument(info, call)
		if err != nil {
			return frameworkcache.TransientSaturationPolicy{}, "", err
		}
		return frameworkcache.WaitForTransient(time.Duration(value)), fmt.Sprintf("cache.WaitForTransient(%d)", value), nil
	default:
		return frameworkcache.TransientSaturationPolicy{}, "", fmt.Errorf("unsupported transient saturation policy %s", selector.Sel.Name)
	}
}

func parseFlightSaturation(info *types.Info, outer *ast.CallExpr) (frameworkcache.FlightSaturationPolicy, string, error) {
	if len(outer.Args) != 1 {
		return frameworkcache.FlightSaturationPolicy{}, "", fmt.Errorf("FlightSaturation expects one argument")
	}
	call, ok := unparen(outer.Args[0]).(*ast.CallExpr)
	if !ok {
		return frameworkcache.FlightSaturationPolicy{}, "", fmt.Errorf("unsupported flight saturation policy")
	}
	selector, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || !isPackageSelector(info, selector, cacheImportPath) {
		return frameworkcache.FlightSaturationPolicy{}, "", fmt.Errorf("unsupported flight saturation policy")
	}
	switch selector.Sel.Name {
	case "Reject":
		if len(call.Args) != 0 {
			return frameworkcache.FlightSaturationPolicy{}, "", fmt.Errorf("Reject expects no arguments")
		}
		return frameworkcache.Reject(), "cache.Reject()", nil
	case "ServeStale":
		if len(call.Args) != 0 {
			return frameworkcache.FlightSaturationPolicy{}, "", fmt.Errorf("ServeStale expects no arguments")
		}
		return frameworkcache.ServeStale(), "cache.ServeStale()", nil
	case "WaitBounded":
		value, err := oneIntegerArgument(info, call)
		if err != nil {
			return frameworkcache.FlightSaturationPolicy{}, "", err
		}
		return frameworkcache.WaitBounded(time.Duration(value)), fmt.Sprintf("cache.WaitBounded(%d)", value), nil
	default:
		return frameworkcache.FlightSaturationPolicy{}, "", fmt.Errorf("unsupported flight saturation policy %s", selector.Sel.Name)
	}
}

func parseStalePolicy(info *types.Info, call *ast.CallExpr) (frameworkcache.StalePolicy, string, error) {
	if len(call.Args) != 1 {
		return 0, "", fmt.Errorf("StaleBehavior expects one argument")
	}
	selector, ok := unparen(call.Args[0]).(*ast.SelectorExpr)
	if !ok || !isPackageSelector(info, selector, cacheImportPath) {
		return 0, "", fmt.Errorf("unsupported stale policy")
	}
	switch selector.Sel.Name {
	case "RefreshBlocking":
		return frameworkcache.RefreshBlocking, "cache.RefreshBlocking", nil
	case "ServeWhileRefreshing":
		return frameworkcache.ServeWhileRefreshing, "cache.ServeWhileRefreshing", nil
	case "ServeOnLoaderError":
		return frameworkcache.ServeOnLoaderError, "cache.ServeOnLoaderError", nil
	default:
		return 0, "", fmt.Errorf("unsupported stale policy %s", selector.Sel.Name)
	}
}

func oneIntegerArgument(info *types.Info, call *ast.CallExpr) (int64, error) {
	if len(call.Args) != 1 {
		return 0, fmt.Errorf("option expects one constant integer argument")
	}
	value := info.Types[call.Args[0]].Value
	if value == nil || value.Kind() != constant.Int {
		return 0, fmt.Errorf("option argument must be a constant integer")
	}
	integer, exact := constant.Int64Val(value)
	if !exact {
		return 0, fmt.Errorf("option argument does not fit int64")
	}
	return integer, nil
}

func profileOf(expression string, profile frameworkcache.Profile) profilePlan {
	return profilePlan{expression: expression, description: profile.Describe()}
}

func analyzeKey(value types.Type) (keyPlan, error) {
	underlying := types.Unalias(value).Underlying()
	structure, ok := underlying.(*types.Struct)
	if !ok {
		return keyPlan{inferredMode: "global"}, nil
	}
	explicit := make([]string, 0)
	tenantLike := make([]string, 0)
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		tags, err := parseStructTag(structure.Tag(index))
		if err != nil {
			return keyPlan{}, fmt.Errorf("field %s has a malformed struct tag: %w", field.Name(), err)
		}
		tag, present := tags["cache"]
		ignored := false
		tenant := false
		seenOptions := map[string]bool{}
		if !present {
			tag = ""
		}
		for _, option := range strings.Split(tag, ",") {
			option = strings.TrimSpace(option)
			if option != "" && seenOptions[option] {
				return keyPlan{}, fmt.Errorf("field %s has duplicate cache tag option %q", field.Name(), option)
			}
			seenOptions[option] = true
			switch option {
			case "":
			case "-":
				ignored = true
			case "tenant":
				tenant = true
			default:
				return keyPlan{}, fmt.Errorf("field %s has unknown cache tag option %q", field.Name(), option)
			}
		}
		if ignored && tenant {
			return keyPlan{}, fmt.Errorf("field %s cannot be both ignored and tenant-partitioned", field.Name())
		}
		if tenant {
			explicit = append(explicit, field.Name())
		}
		if !ignored && strings.Contains(strings.ToLower(field.Name()), "tenant") {
			tenantLike = append(tenantLike, field.Name())
		}
	}
	if len(explicit) > 1 {
		return keyPlan{}, fmt.Errorf("multiple fields carry cache tenant tags: %s", strings.Join(explicit, ", "))
	}
	partition := ""
	if len(explicit) == 1 {
		partition = explicit[0]
	} else {
		if len(tenantLike) > 1 {
			return keyPlan{}, fmt.Errorf("multiple tenant-like fields require one cache:\"tenant\" tag: %s", strings.Join(tenantLike, ", "))
		}
		if len(tenantLike) == 1 {
			partition = tenantLike[0]
		}
	}
	if partition == "" {
		return keyPlan{inferredMode: "global"}, nil
	}
	field := keyField(value, partition)
	if field == nil {
		return keyPlan{}, fmt.Errorf("tenant partition field %s is missing", partition)
	}
	if err := validatePartitionType(field.Type(), map[types.Type]bool{}); err != nil {
		return keyPlan{}, fmt.Errorf("tenant partition field %s: %w", partition, err)
	}
	return keyPlan{inferredMode: "partitioned", partitionName: partition}, nil
}

func parseStructTag(raw string) (map[string]string, error) {
	result := map[string]string{}
	for raw != "" {
		raw = strings.TrimLeft(raw, " ")
		if raw == "" {
			break
		}
		separator := 0
		for separator < len(raw) && raw[separator] > ' ' && raw[separator] != ':' && raw[separator] != '"' && raw[separator] != 0x7f {
			separator++
		}
		if separator == 0 || separator+1 >= len(raw) || raw[separator] != ':' || raw[separator+1] != '"' {
			return nil, fmt.Errorf("invalid key or quoted value")
		}
		key := raw[:separator]
		quoted := raw[separator+1:]
		end := 1
		for end < len(quoted) {
			if quoted[end] == '\\' {
				end++
				if end >= len(quoted) {
					return nil, fmt.Errorf("unterminated escape")
				}
			} else if quoted[end] == '"' {
				break
			}
			end++
		}
		if end >= len(quoted) || quoted[end] != '"' {
			return nil, fmt.Errorf("unterminated quoted value")
		}
		value, err := strconv.Unquote(quoted[:end+1])
		if err != nil {
			return nil, fmt.Errorf("invalid quoted value")
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		result[key] = value
		raw = quoted[end+1:]
	}
	return result, nil
}

func validatePartitionType(value types.Type, active map[types.Type]bool) error {
	value = types.Unalias(value)
	if active[value] {
		return fmt.Errorf("recursive partition type is not supported")
	}
	active[value] = true
	defer delete(active, value)
	switch item := value.(type) {
	case *types.Named:
		return validatePartitionType(item.Underlying(), active)
	case *types.Basic:
		switch item.Kind() {
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.String:
			return nil
		default:
			return fmt.Errorf("type %s is not a stable tenant identity", item.Name())
		}
	case *types.Array:
		return validatePartitionType(item.Elem(), active)
	case *types.Struct:
		for index := 0; index < item.NumFields(); index++ {
			if err := validatePartitionType(item.Field(index).Type(), active); err != nil {
				return fmt.Errorf("field %s: %w", item.Field(index).Name(), err)
			}
		}
		return nil
	default:
		return fmt.Errorf("use an immutable scalar, array, or struct value")
	}
}

func validateKeyType(value types.Type, active map[types.Type]bool) error {
	value = types.Unalias(value)
	if active[value] {
		return fmt.Errorf("recursive key type %s is not supported", types.TypeString(value, packagePath))
	}
	active[value] = true
	defer delete(active, value)
	switch item := value.(type) {
	case *types.Named:
		return validateKeyType(item.Underlying(), active)
	case *types.Basic:
		switch item.Kind() {
		case types.Bool, types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
			types.String, types.Float32, types.Float64:
			return nil
		default:
			return fmt.Errorf("unsupported scalar type %s", item.Name())
		}
	case *types.Pointer:
		return validateKeyType(item.Elem(), active)
	case *types.Slice:
		return validateKeyType(item.Elem(), active)
	case *types.Array:
		return validateKeyType(item.Elem(), active)
	case *types.Struct:
		for index := 0; index < item.NumFields(); index++ {
			field := item.Field(index)
			if !field.Exported() {
				return fmt.Errorf("field %s is unexported", field.Name())
			}
			if err := validateKeyType(field.Type(), active); err != nil {
				return fmt.Errorf("field %s: %w", field.Name(), err)
			}
		}
		return nil
	case *types.Map:
		return fmt.Errorf("map keys need an explicit canonical codec")
	case *types.Interface:
		return fmt.Errorf("interface keys need an explicit canonical codec")
	case *types.Chan, *types.Signature:
		return fmt.Errorf("type %s cannot be a cache key", types.TypeString(value, packagePath))
	default:
		return fmt.Errorf("unsupported key type %s", types.TypeString(value, packagePath))
	}
}

func validateValueType(value types.Type, active map[types.Type]bool) error {
	value = types.Unalias(value)
	if isTimeValuePath(value) {
		return fmt.Errorf("time.Time is only supported as the exact top-level value type")
	}
	if hasCustomSerialization(value) {
		return fmt.Errorf("type %s uses custom JSON or text serialization that cannot be bounded automatically", types.TypeString(value, packagePath))
	}
	if active[value] {
		return nil
	}
	active[value] = true
	defer delete(active, value)
	if named, ok := value.(*types.Named); ok {
		return validateValueType(named.Underlying(), active)
	}
	switch item := value.(type) {
	case *types.Basic:
		switch item.Kind() {
		case types.Bool, types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
			types.Float32, types.Float64, types.String:
			return nil
		default:
			return fmt.Errorf("type %s is not JSON round-trippable", item.Name())
		}
	case *types.Pointer:
		return validateValueType(item.Elem(), active)
	case *types.Slice:
		return validateValueType(item.Elem(), active)
	case *types.Array:
		return validateValueType(item.Elem(), active)
	case *types.Map:
		if err := validateJSONMapKey(item.Key()); err != nil {
			return err
		}
		return validateValueType(item.Elem(), active)
	case *types.Struct:
		return validateJSONStruct(item, active)
	case *types.Interface:
		return fmt.Errorf("interface values lose their concrete type during JSON decoding")
	case *types.Chan, *types.Signature:
		return fmt.Errorf("type %s is not JSON round-trippable", types.TypeString(value, packagePath))
	default:
		return fmt.Errorf("unsupported value type %s", types.TypeString(value, packagePath))
	}
}

func validateJSONMapKey(value types.Type) error {
	value = types.Unalias(value)
	if hasCustomSerialization(value) {
		return fmt.Errorf("map key %s uses custom serialization that cannot be bounded automatically", types.TypeString(value, packagePath))
	}
	if named, ok := value.(*types.Named); ok {
		value = named.Underlying()
	}
	basic, ok := value.(*types.Basic)
	if !ok {
		return fmt.Errorf("map key %s is not supported by JSON", types.TypeString(value, packagePath))
	}
	switch basic.Kind() {
	case types.String, types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
		return nil
	default:
		return fmt.Errorf("map key %s is not supported by JSON", types.TypeString(value, packagePath))
	}
}

func validateJSONStruct(structure *types.Struct, active map[types.Type]bool) error {
	names := map[string]string{}
	visible, err := collectJSONFields(structure, "", names, active, map[types.Type]bool{})
	if err != nil {
		return err
	}
	if structure.NumFields() != 0 && visible == 0 {
		return fmt.Errorf("struct has state but no JSON-visible fields")
	}
	return nil
}

func collectJSONFields(structure *types.Struct, prefix string, names map[string]string, active, embedded map[types.Type]bool) (int, error) {
	visible := 0
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		path := field.Name()
		if prefix != "" {
			path = prefix + "." + path
		}
		tags, err := parseStructTag(structure.Tag(index))
		if err != nil {
			return 0, fmt.Errorf("field %s has a malformed struct tag: %w", path, err)
		}
		jsonTag, tagged := tags["json"]
		if jsonTag == "-" {
			continue
		}
		name, omitZero, err := parseJSONTag(jsonTag, tagged)
		if err != nil {
			return 0, fmt.Errorf("field %s: %w", path, err)
		}
		if !field.Exported() && field.Embedded() {
			if _, pointer := types.Unalias(field.Type()).(*types.Pointer); pointer {
				return 0, fmt.Errorf("field %s is a JSON-visible unexported anonymous pointer; use json:\"-\" or a manual codec", path)
			}
		}
		if !field.Exported() && !(field.Embedded() && embeddedStruct(field.Type()) != nil) {
			return 0, fmt.Errorf("field %s is hidden JSON state; use json:\"-\" or a manual codec", path)
		}
		if field.Embedded() && name == "" {
			if _, pointer := types.Unalias(field.Type()).(*types.Pointer); pointer {
				return 0, fmt.Errorf("field %s is an anonymous pointer and needs an explicit JSON name", path)
			}
			nested := embeddedStruct(field.Type())
			if nested != nil {
				typeKey := types.Unalias(field.Type())
				if embedded[typeKey] {
					return 0, fmt.Errorf("field %s recursively embeds its JSON fields", path)
				}
				embedded[typeKey] = true
				count, err := collectJSONFields(nested, path, names, active, embedded)
				delete(embedded, typeKey)
				if err != nil {
					return 0, err
				}
				visible += count
				continue
			}
		}
		if omitZero && hasCustomZero(field.Type()) {
			return 0, fmt.Errorf("field %s uses application-defined IsZero with json omitzero", path)
		}
		if name == "" {
			name = field.Name()
		}
		if previous := names[name]; previous != "" {
			return 0, fmt.Errorf("JSON field name %q is ambiguous between %s and %s", name, previous, path)
		}
		names[name] = path
		visible++
		if err := validateValueType(field.Type(), active); err != nil {
			return 0, fmt.Errorf("field %s: %w", path, err)
		}
	}
	return visible, nil
}

func embeddedStruct(value types.Type) *types.Struct {
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	return underlyingStruct(value)
}

func underlyingStruct(value types.Type) *types.Struct {
	structure, _ := value.Underlying().(*types.Struct)
	return structure
}

func parseJSONTag(value string, present bool) (string, bool, error) {
	if !present {
		return "", false, nil
	}
	parts := strings.Split(value, ",")
	name := parts[0]
	if name != "" && !validJSONFieldName(name) {
		return "", false, fmt.Errorf("JSON field name %q is invalid", name)
	}
	seen := map[string]bool{}
	for _, option := range parts[1:] {
		if option != "omitempty" && option != "omitzero" && option != "string" {
			return "", false, fmt.Errorf("unknown JSON tag option %q", option)
		}
		if seen[option] {
			return "", false, fmt.Errorf("duplicate JSON tag option %q", option)
		}
		seen[option] = true
	}
	return name, seen["omitzero"], nil
}

func validJSONFieldName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", character) {
			continue
		}
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}

func isExactTimeValue(value types.Type) bool {
	value = types.Unalias(value)
	named, ok := value.(*types.Named)
	if !ok {
		return false
	}
	object := named.Obj()
	return object.Pkg() != nil && object.Pkg().Path() == "time" && object.Name() == "Time"
}

func isTimeValuePath(value types.Type) bool {
	value = types.Unalias(value)
	for {
		if isExactTimeValue(value) {
			return true
		}
		pointer, ok := value.(*types.Pointer)
		if !ok {
			return false
		}
		value = types.Unalias(pointer.Elem())
	}
}

func hasCustomSerialization(value types.Type) bool {
	pointer := types.NewPointer(value)
	for _, contract := range serializationContracts() {
		if types.Implements(value, contract) || types.Implements(pointer, contract) {
			return true
		}
	}
	return false
}

func hasCustomZero(value types.Type) bool {
	value = types.Unalias(value)
	result := types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Typ[types.Bool]))
	contract := types.NewInterfaceType([]*types.Func{
		types.NewFunc(token.NoPos, nil, "IsZero", types.NewSignatureType(nil, nil, nil, nil, result, false)),
	}, nil)
	contract.Complete()
	return types.Implements(value, contract) || types.Implements(types.NewPointer(value), contract)
}

func serializationContracts() []*types.Interface {
	bytesType := types.NewSlice(types.Typ[types.Byte])
	errorType := types.Universe.Lookup("error").Type()
	marshalResults := types.NewTuple(types.NewVar(token.NoPos, nil, "", bytesType), types.NewVar(token.NoPos, nil, "", errorType))
	unmarshalParams := types.NewTuple(types.NewVar(token.NoPos, nil, "", bytesType))
	unmarshalResults := types.NewTuple(types.NewVar(token.NoPos, nil, "", errorType))
	marshalSignature := types.NewSignatureType(nil, nil, nil, nil, marshalResults, false)
	unmarshalSignature := types.NewSignatureType(nil, nil, nil, unmarshalParams, unmarshalResults, false)
	result := make([]*types.Interface, 0, 4)
	for _, name := range []string{"MarshalJSON", "MarshalText"} {
		contract := types.NewInterfaceType([]*types.Func{types.NewFunc(token.NoPos, nil, name, marshalSignature)}, nil)
		contract.Complete()
		result = append(result, contract)
	}
	for _, name := range []string{"UnmarshalJSON", "UnmarshalText"} {
		contract := types.NewInterfaceType([]*types.Func{types.NewFunc(token.NoPos, nil, name, unmarshalSignature)}, nil)
		contract.Complete()
		result = append(result, contract)
	}
	return result
}

func renderExpression(fset *token.FileSet, expression ast.Expr) (string, error) {
	var output bytes.Buffer
	if err := format.Node(&output, fset, expression); err != nil {
		return "", fmt.Errorf("cachegen: render type expression: %w", err)
	}
	return output.String(), nil
}

func typeImports(loaded *loadedPackage, file *ast.File, expressions ...ast.Expr) (map[string]string, error) {
	needed := map[string]string{}
	for _, expression := range expressions {
		ast.Inspect(expression, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := unparen(selector.X).(*ast.Ident)
			if !ok {
				return true
			}
			packageName, ok := loaded.info.Uses[identifier].(*types.PkgName)
			if ok {
				needed[identifier.Name] = packageName.Imported().Path()
			}
			return true
		})
	}
	for _, item := range file.Imports {
		if item.Name != nil && item.Name.Name == "." {
			path, _ := strconv.Unquote(item.Path.Value)
			if path != "" {
				return nil, fmt.Errorf("dot import %q cannot be reproduced safely", path)
			}
		}
	}
	return needed, nil
}

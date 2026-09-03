package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultGuardPkg    = "github.com/frostgrove/vv/auth/access"
	DefaultGuardFunc   = "Require"
	DefaultDeclarePkg  = "github.com/frostgrove/vv/auth/http/authhttp"
	DefaultAuthPkg     = "github.com/frostgrove/vv/auth"
	DefaultRoutesOut   = "vv_routes_gen.go"
	DefaultRoutesFile  = "routes.manifest.yml"
	routesFormat       = 1
	routesGeneratedBy  = "vv generate routes"
	sourceInferred     = "inferred-from-guard"
	sourceFromManifest = "declared-in-manifest"
	sourceBound        = "bound-to-operation"
	confirmationHint   = "confirm every operation in routes.manifest.yml"
)

type RouteOptions struct {
	Dir        string
	Out        string
	Manifest   string
	GuardPkg   string
	GuardFunc  string
	DeclarePkg string
	AuthPkg    string
	Recursive  bool
	Check      bool
	Log        io.Writer
}

type RouteConfirmationError struct {
	Manifest   string
	Operations []string
}

func (this *RouteConfirmationError) Error() string {
	return fmt.Sprintf("codegen: the route of an operation was inferred from the guard that enforces it and nobody has confirmed the pair; set confirmed: true in %s for: %s",
		this.Manifest, strings.Join(this.Operations, ", "))
}

type UnenforcedDeclarationError struct {
	Declarations []string
}

func (this *UnenforcedDeclarationError) Error() string {
	return "codegen: these routes declare a permission no use case in the package enforces, so the declaration is a second policy rather than a projection of the one that runs: " +
		strings.Join(this.Declarations, ", ")
}

type routeGuard struct {
	operation string
	policy    []string
	boundTo   string
	position  string
}

type routeDeclaration struct {
	method   string
	path     string
	policy   []string
	position string
}

type importBinding struct {
	path string
	file string
}

type routeSurface struct {
	pkg          string
	guards       []routeGuard
	declarations []routeDeclaration
	imports      map[string]importBinding
	conflicts    map[string][]importBinding
}

type routesManifestDocument struct {
	Format      int                       `json:"format"`
	GeneratedBy string                    `json:"generated_by"`
	Package     string                    `json:"package"`
	Operations  []routesManifestOperation `json:"operations"`
}

type routesManifestOperation struct {
	Operation   string   `json:"operation"`
	Policy      []string `json:"policy"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Source      string   `json:"source"`
	Fingerprint string   `json:"guard_fingerprint"`
	Confirmed   bool     `json:"confirmed"`
}

func RunRoutes(options *RouteOptions) error {
	if options == nil {
		return fmt.Errorf("codegen: options are nil")
	}
	outName := cmpOr(options.Out, DefaultRoutesOut)
	if err := artifactName(outName, "-out"); err != nil {
		return err
	}
	manifestName := cmpOr(options.Manifest, DefaultRoutesFile)
	if err := artifactName(manifestName, "-manifest"); err != nil {
		return err
	}
	if options.Recursive {
		dirs, err := guardedDirs(options.Dir, cmpOr(options.GuardFunc, DefaultGuardFunc))
		if err != nil {
			return err
		}
		var waiting, stale []string
		for _, dir := range dirs {
			one := *options
			one.Dir, one.Out, one.Manifest, one.Recursive = dir, outName, manifestName, false
			err := RunRoutes(&one)
			var confirmation *RouteConfirmationError
			var drift *DriftError
			switch {
			case err == nil:
			case errors.As(err, &confirmation):
				for _, operation := range confirmation.Operations {
					waiting = append(waiting, filepath.Base(dir)+"."+operation)
				}
			case errors.As(err, &drift):
				stale = append(stale, drift.Paths...)
			case strings.Contains(err.Error(), "no guarded use case in "):
			default:
				return err
			}
		}
		if len(waiting) != 0 {
			sort.Strings(waiting)
			return &RouteConfirmationError{Manifest: manifestName, Operations: waiting}
		}
		if len(stale) != 0 {
			sort.Strings(stale)
			return &DriftError{Paths: stale}
		}
		return nil
	}

	outPath, err := containedOutputPath(options.Dir, outName)
	if err != nil {
		return err
	}
	manifestPath, err := containedOutputPath(options.Dir, manifestName)
	if err != nil {
		return err
	}
	if err := validateGeneratedTarget(outPath); err != nil {
		return err
	}
	surface, err := discoverRoutes(options.Dir, outName, cmpOr(options.GuardPkg, DefaultGuardPkg),
		cmpOr(options.GuardFunc, DefaultGuardFunc), cmpOr(options.DeclarePkg, DefaultDeclarePkg))
	if err != nil {
		return err
	}
	if len(surface.guards) == 0 {
		return fmt.Errorf("codegen: no guarded use case in %s: nothing calls %s.%s, so there is no policy to infer a route from",
			options.Dir, filepath.Base(cmpOr(options.GuardPkg, DefaultGuardPkg)), cmpOr(options.GuardFunc, DefaultGuardFunc))
	}

	prior, err := readRoutesManifest(manifestPath, surface.pkg)
	if err != nil {
		return err
	}
	document, err := buildRoutesManifest(surface, prior)
	if err != nil {
		return err
	}
	manifestSource, err := marshalRoutesManifest(document)
	if err != nil {
		return err
	}
	if unconfirmed := unconfirmedOperations(document); len(unconfirmed) != 0 {
		if !options.Check {
			if err := writeArtifact(manifestPath, manifestSource, validateRoutesManifestTarget); err != nil {
				return err
			}
			if err := writeGenerated(outPath, unconfirmedRoutes(surface.pkg)); err != nil {
				return err
			}
		}
		return &RouteConfirmationError{Manifest: filepath.Base(manifestPath), Operations: unconfirmed}
	}
	source, err := renderRoutes(surface, document, cmpOr(options.AuthPkg, DefaultAuthPkg), cmpOr(options.DeclarePkg, DefaultDeclarePkg))
	if err != nil {
		return err
	}
	if err := validateRouteDeclarations(options.Dir, surface.pkg, outName, document); err != nil {
		return err
	}
	if options.Check {
		return checkArtifacts([]artifact{{outPath, source}, {manifestPath, manifestSource}})
	}
	if err := writeArtifact(manifestPath, manifestSource, validateRoutesManifestTarget); err != nil {
		return err
	}
	if err := writeGenerated(outPath, source); err != nil {
		return err
	}
	if options.Log != nil {
		fmt.Fprintf(options.Log, "vv: wrote %s and %s (%d operations)\n", outPath, manifestPath, len(document.Operations))
	}
	return nil
}

func discoverRoutes(dir, skip, guardPkg, guardFunc, declarePkg string) (*routeSurface, error) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go") && info.Name() != skip
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("codegen: reading %s: %w", dir, err)
	}
	names := make([]string, 0, len(parsed))
	for name := range parsed {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) != 1 {
		return nil, fmt.Errorf("codegen: %s holds %d packages (%s); routes are generated for one package at a time",
			dir, len(names), strings.Join(names, ", "))
	}
	surface := &routeSurface{pkg: names[0], imports: map[string]importBinding{}, conflicts: map[string][]importBinding{}}
	pkg := parsed[names[0]]
	fileNames := make([]string, 0, len(pkg.Files))
	for name := range pkg.Files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		file := pkg.Files[name]
		aliases, err := importAliases(file, name)
		if err != nil {
			return nil, err
		}
		for alias, path := range aliases {
			surface.bindImportAlias(alias, importBinding{path: path, file: filepath.Base(name)})
		}
		if err := collectGuards(surface, fset, file, aliases, guardPkg, guardFunc); err != nil {
			return nil, err
		}
		collectDeclarations(surface, fset, file, aliases, declarePkg)
	}
	sort.Slice(surface.guards, func(i, j int) bool { return surface.guards[i].operation < surface.guards[j].operation })
	sort.Slice(surface.declarations, func(i, j int) bool {
		left, right := surface.declarations[i], surface.declarations[j]
		if left.path != right.path {
			return left.path < right.path
		}
		return left.method < right.method
	})
	if err := surface.refuseAmbiguousQualifiers(); err != nil {
		return nil, err
	}
	return surface, nil
}

func (this *routeSurface) bindImportAlias(alias string, binding importBinding) {
	if conflicting, ambiguous := this.conflicts[alias]; ambiguous {
		for _, known := range conflicting {
			if known.path == binding.path {
				return
			}
		}
		this.conflicts[alias] = append(conflicting, binding)
		return
	}
	held, seen := this.imports[alias]
	if !seen {
		this.imports[alias] = binding
		return
	}
	if held.path != binding.path {
		this.conflicts[alias] = []importBinding{held, binding}
		delete(this.imports, alias)
	}
}

func (this *routeSurface) refuseAmbiguousQualifiers() error {
	if len(this.conflicts) == 0 {
		return nil
	}
	for _, guard := range this.guards {
		for _, permission := range guard.policy {
			if err := this.refuseAmbiguousPermission(guard.operation+" at "+guard.position, permission); err != nil {
				return err
			}
		}
	}
	for _, declaration := range this.declarations {
		for _, permission := range declaration.policy {
			subject := declaration.method + " " + declaration.path + " at " + declaration.position
			if err := this.refuseAmbiguousPermission(subject, permission); err != nil {
				return err
			}
		}
	}
	return nil
}

func (this *routeSurface) refuseAmbiguousPermission(subject, permission string) error {
	qualifier, _, qualified := strings.Cut(permission, ".")
	if !qualified {
		return nil
	}
	conflicting := this.conflicts[qualifier]
	if len(conflicting) == 0 {
		return nil
	}
	return ambiguousAliasError(subject, permission, qualifier, conflicting)
}

func ambiguousAliasError(subject, permission, qualifier string, bindings []importBinding) error {
	named := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		named = append(named, fmt.Sprintf("%q in %s", binding.path, binding.file))
	}
	return fmt.Errorf("codegen: %s reads %s, and the import alias %s names %s; one alias cannot name two packages in a package whose routes are generated, so rename one of those imports",
		subject, permission, qualifier, strings.Join(named, " and "))
}

func importAliases(file *ast.File, fileName string) (map[string]string, error) {
	out := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("codegen: import in %s: %w", fileName, err)
		}
		alias := filepath.Base(path)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		if alias == "_" || alias == "." {
			continue
		}
		out[alias] = path
	}
	return out, nil
}

func collectGuards(surface *routeSurface, fset *token.FileSet, file *ast.File, aliases map[string]string, guardPkg, guardFunc string) error {
	seen := map[string]routeGuard{}
	for _, declaration := range file.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction || function.Body == nil {
			continue
		}
		operation := operationName(function)
		var failure error
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall || !callsPackageFunction(call, aliases, guardPkg, guardFunc) || len(call.Args) < 1 {
				return true
			}
			guard := routeGuard{operation: operation, position: fset.Position(call.Pos()).String()}
			if bound, isBound := boundOperation(call); isBound {
				guard.boundTo = bound
			} else {
				for _, argument := range call.Args[1:] {
					guard.policy = append(guard.policy, exprString(argument))
				}
			}
			earlier, twice := seen[operation]
			if twice && !sameGuard(earlier, guard) {
				failure = fmt.Errorf("codegen: %s enforces two different policies, %s at %s and %s at %s; one operation cannot name both",
					operation, guardPolicyText(earlier), earlier.position, guardPolicyText(guard), guard.position)
				return false
			}
			if !twice {
				seen[operation] = guard
				surface.guards = append(surface.guards, guard)
			}
			return true
		})
		if failure != nil {
			return failure
		}
	}
	return nil
}

func collectDeclarations(surface *routeSurface, fset *token.FileSet, file *ast.File, aliases map[string]string, declarePkg string) {
	ast.Inspect(file, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall || !callsPackageFunction(call, aliases, declarePkg, "Requires") || len(call.Args) < 2 {
			return true
		}
		method, known := httpMethodOf(call.Args[0])
		path, literal := stringLiteral(call.Args[1])
		if !known || !literal {
			return true
		}
		declaration := routeDeclaration{method: method, path: path, position: fset.Position(call.Pos()).String()}
		for _, argument := range call.Args[2:] {
			declaration.policy = append(declaration.policy, exprString(argument))
		}
		surface.declarations = append(surface.declarations, declaration)
		return true
	})
}

func operationName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	return receiverTypeName(function.Recv.List[0].Type) + "." + function.Name.Name
}

func receiverTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexListExpr:
		return receiverTypeName(typed.X)
	case *ast.Ident:
		return typed.Name
	default:
		return exprString(expr)
	}
}

func callsPackageFunction(call *ast.CallExpr, aliases map[string]string, path, name string) bool {
	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	if !isSelector || selector.Sel.Name != name {
		return false
	}
	qualifier, isIdent := selector.X.(*ast.Ident)
	if !isIdent {
		return false
	}
	return aliases[qualifier.Name] == path
}

func boundOperation(call *ast.CallExpr) (string, bool) {
	if call.Ellipsis == token.NoPos || len(call.Args) != 2 {
		return "", false
	}
	inner, isCall := call.Args[1].(*ast.CallExpr)
	if !isCall || len(inner.Args) != 0 {
		return "", false
	}
	selector, isSelector := inner.Fun.(*ast.SelectorExpr)
	if !isSelector || selector.Sel.Name != "Permissions" {
		return "", false
	}
	variable, isIdent := selector.X.(*ast.Ident)
	if !isIdent {
		return "", false
	}
	return variable.Name, true
}

func sameGuard(left, right routeGuard) bool {
	return left.boundTo == right.boundTo && policyKey(left.policy) == policyKey(right.policy)
}

func guardPolicyText(guard routeGuard) string {
	if guard.boundTo != "" {
		return guard.boundTo
	}
	if len(guard.policy) == 0 {
		return "no permission"
	}
	return strings.Join(guard.policy, "+")
}

var httpMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "CONNECT": true, "OPTIONS": true, "TRACE": true,
}

func httpMethodOf(expr ast.Expr) (string, bool) {
	if literal, ok := stringLiteral(expr); ok {
		upper := strings.ToUpper(literal)
		return upper, httpMethods[upper]
	}
	selector, isSelector := expr.(*ast.SelectorExpr)
	if !isSelector || !strings.HasPrefix(selector.Sel.Name, "Method") {
		return "", false
	}
	upper := strings.ToUpper(strings.TrimPrefix(selector.Sel.Name, "Method"))
	return upper, httpMethods[upper]
}

func stringLiteral(expr ast.Expr) (string, bool) {
	literal, isLiteral := expr.(*ast.BasicLit)
	if !isLiteral || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func policyKey(policy []string) string {
	sorted := append([]string(nil), policy...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\x00")
}

func buildRoutesManifest(surface *routeSurface, prior *routesManifestDocument) (*routesManifestDocument, error) {
	previous := map[string]routesManifestOperation{}
	if prior != nil {
		for _, operation := range prior.Operations {
			previous[operation.Operation] = operation
		}
	}
	byPolicy := map[string][]int{}
	for index, declaration := range surface.declarations {
		key := policyKey(declaration.policy)
		byPolicy[key] = append(byPolicy[key], index)
	}

	document := &routesManifestDocument{
		Format:      routesFormat,
		GeneratedBy: routesGeneratedBy,
		Package:     surface.pkg,
		Operations:  make([]routesManifestOperation, 0, len(surface.guards)),
	}
	enforced := map[string]bool{}
	for _, guard := range surface.guards {
		carried, hadPrior := previous[guard.operation]
		entry := routesManifestOperation{Operation: guard.operation, Policy: guard.policy, Source: sourceInferred}
		inferredMethod, inferredPath := "", ""
		if guard.boundTo != "" {
			if !hadPrior {
				return nil, fmt.Errorf("codegen: %s is guarded by %s, which %s does not carry: the manifest is the only place that operation's policy is written",
					guard.operation, guard.boundTo, DefaultRoutesFile)
			}
			entry.Policy = carried.Policy
			entry.Method, entry.Path = carried.Method, carried.Path
			entry.Source = sourceBound
		} else {
			enforced[policyKey(guard.policy)] = true
			if candidates := byPolicy[policyKey(guard.policy)]; len(candidates) == 1 {
				declaration := surface.declarations[candidates[0]]
				inferredMethod, inferredPath = declaration.method, declaration.path
				entry.Method, entry.Path = declaration.method, declaration.path
			} else if hadPrior {
				entry.Method, entry.Path, entry.Source = carried.Method, carried.Path, sourceFromManifest
			}
		}
		if entry.Policy == nil {
			entry.Policy = []string{}
		}
		entry.Fingerprint = routeFingerprint(entry.Operation, entry.Policy, inferredMethod, inferredPath)
		entry.Confirmed = entry.Source == sourceBound || (hadPrior && carried.Confirmed && carried.Fingerprint == entry.Fingerprint)
		if entry.Confirmed && (entry.Method == "" || entry.Path == "") {
			return nil, fmt.Errorf("codegen: %s is confirmed but names no route; give it method and path in %s",
				entry.Operation, DefaultRoutesFile)
		}
		document.Operations = append(document.Operations, entry)
	}

	var unenforced []string
	for _, declaration := range surface.declarations {
		if !enforced[policyKey(declaration.policy)] {
			unenforced = append(unenforced, declaration.method+" "+declaration.path+" ("+strings.Join(declaration.policy, "+")+") at "+declaration.position)
		}
	}
	if len(unenforced) != 0 {
		sort.Strings(unenforced)
		return nil, &UnenforcedDeclarationError{Declarations: unenforced}
	}
	sort.Slice(document.Operations, func(i, j int) bool {
		return document.Operations[i].Operation < document.Operations[j].Operation
	})
	return document, nil
}

func routeFingerprint(operation string, policy []string, inferredMethod, inferredPath string) string {
	sorted := append([]string(nil), policy...)
	sort.Strings(sorted)
	sum := sha256.New()
	for _, part := range append([]string{operation, inferredMethod, inferredPath}, sorted...) {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func unconfirmedOperations(document *routesManifestDocument) []string {
	out := []string{}
	for _, operation := range document.Operations {
		if !operation.Confirmed {
			out = append(out, operation.Operation)
		}
	}
	sort.Strings(out)
	return out
}

func readRoutesManifest(path, pkg string) (*routesManifestDocument, error) {
	source, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("codegen: read %s: %w", path, err)
	}
	var document routesManifestDocument
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("codegen: refusing to overwrite an unrelated manifest %s: %w", path, err)
	}
	if document.Format != routesFormat || document.GeneratedBy != routesGeneratedBy {
		return nil, fmt.Errorf("codegen: refusing to overwrite an unrelated manifest %s", path)
	}
	if document.Package != pkg {
		return nil, fmt.Errorf("codegen: %s belongs to package %q, not %q", path, document.Package, pkg)
	}
	seen := map[string]bool{}
	for _, operation := range document.Operations {
		if operation.Operation == "" {
			return nil, fmt.Errorf("codegen: %s carries an unnamed operation", path)
		}
		if seen[operation.Operation] {
			return nil, fmt.Errorf("codegen: %s carries %s twice", path, operation.Operation)
		}
		seen[operation.Operation] = true
	}
	return &document, nil
}

func marshalRoutesManifest(document *routesManifestDocument) ([]byte, error) {
	source, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("codegen: encode manifest: %w", err)
	}
	return append(source, '\n'), nil
}

func validateRoutesManifestTarget(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("codegen: inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("codegen: refusing symlink manifest %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("codegen: manifest %s is not a regular file", path)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("codegen: inspect %s: %w", path, err)
	}
	var header struct {
		GeneratedBy string `json:"generated_by"`
	}
	if json.Unmarshal(current, &header) != nil || header.GeneratedBy != routesGeneratedBy {
		return fmt.Errorf("codegen: refusing to overwrite an unrelated manifest %s", path)
	}
	return nil
}

func operationVariable(name string) string {
	return "Operation" + strings.NewReplacer(".", "", "-", "", "_", "").Replace(name)
}

func validateRouteDeclarations(dir, pkg, skip string, document *routesManifestDocument) error {
	declared, err := packageDeclarationNames(dir, pkg, skip)
	if err != nil {
		return err
	}
	wanted := []string{"Operation", "Operations", "Declarations"}
	for _, operation := range document.Operations {
		wanted = append(wanted, operationVariable(operation.Operation))
	}
	for _, name := range wanted {
		if declared[name] {
			return fmt.Errorf("codegen: package %s already declares %s, which the generated routes file also declares", pkg, name)
		}
	}
	return nil
}

func unconfirmedRoutes(pkg string) []byte {
	return []byte(fmt.Sprintf("%s\n\npackage %s\n\ntype vvRouteSet []struct{}\n\nvar VVRouteSet vvRouteSet = %q\n",
		generatedHeader, pkg, confirmationHint))
}

func renderRoutes(surface *routeSurface, document *routesManifestDocument, authPkg, declarePkg string) ([]byte, error) {
	imports := map[string]string{"auth": authPkg, "authhttp": declarePkg}
	for _, operation := range document.Operations {
		for _, permission := range operation.Policy {
			qualifier, _, qualified := strings.Cut(permission, ".")
			if !qualified {
				continue
			}
			binding, known := surface.imports[qualifier]
			if !known {
				if conflicting := surface.conflicts[qualifier]; len(conflicting) != 0 {
					return nil, ambiguousAliasError(operation.Operation, permission, qualifier, conflicting)
				}
				return nil, fmt.Errorf("codegen: %s is guarded by %s, whose package %s is not imported anywhere the generator can see",
					operation.Operation, permission, qualifier)
			}
			if held, taken := imports[qualifier]; taken && held != binding.path {
				return nil, fmt.Errorf("codegen: the generated routes file needs %q and %q under the same name %s", held, binding.path, qualifier)
			}
			imports[qualifier] = binding.path
		}
	}
	qualifiers := make([]string, 0, len(imports))
	for qualifier := range imports {
		qualifiers = append(qualifiers, qualifier)
	}
	sort.Strings(qualifiers)

	var out bytes.Buffer
	fmt.Fprintf(&out, "%s\n\npackage %s\n\nimport (\n", generatedHeader, surface.pkg)
	for _, qualifier := range qualifiers {
		fmt.Fprintf(&out, "\t%s %q\n", qualifier, imports[qualifier])
	}
	out.WriteString(")\n\n")
	out.WriteString(`type Operation struct {
	name        string
	method      string
	path        string
	permissions []auth.Permission
}

func (this Operation) Name() string { return this.name }

func (this Operation) Method() string { return this.method }

func (this Operation) Path() string { return this.path }

func (this Operation) Permissions() []auth.Permission {
	out := make([]auth.Permission, len(this.permissions))
	copy(out, this.permissions)
	return out
}

func (this Operation) Endpoint() authhttp.Endpoint {
	if len(this.permissions) == 0 {
		return authhttp.Authenticated(this.method, this.path, "operation "+this.name+" enforces no permission")
	}
	return authhttp.Requires(this.method, this.path, this.permissions...)
}

`)
	for _, operation := range document.Operations {
		fmt.Fprintf(&out, "var %s = Operation{\n\tname: %q,\n\tmethod: %q,\n\tpath: %q,\n\tpermissions: []auth.Permission{%s},\n}\n\n",
			operationVariable(operation.Operation), operation.Operation, operation.Method, operation.Path,
			strings.Join(operation.Policy, ", "))
	}
	out.WriteString("func Operations() []Operation {\n\treturn []Operation{")
	for index, operation := range document.Operations {
		if index > 0 {
			out.WriteString(", ")
		}
		out.WriteString(operationVariable(operation.Operation))
	}
	out.WriteString("}\n}\n\n")
	out.WriteString(`func Declarations() []authhttp.Endpoint {
	operations := Operations()
	out := make([]authhttp.Endpoint, 0, len(operations))
	for _, operation := range operations {
		out = append(out, operation.Endpoint())
	}
	return out
}
`)
	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("codegen: formatting the generated routes file: %w", err)
	}
	return formatted, nil
}

func guardedDirs(root, guardFunc string) ([]string, error) {
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
	needle := []byte("." + guardFunc + "(")
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
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") ||
			strings.HasSuffix(entry.Name(), "_gen.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(source, needle) {
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

package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/frostgrove/vv/i18n"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

const (
	i18nPackagePath = "github.com/frostgrove/vv/i18n"
	errsPackagePath = "github.com/frostgrove/vv/errs"
)

type extractPackageAnalysis struct {
	state                 *extractState
	info                  *types.Info
	files                 []extractParsedFile
	assignments           map[types.Object]extractAssignment
	origins               map[types.Object]extractAssignment
	capabilityValues      map[types.Object][]ast.Expr
	capabilityMemo        map[types.Object]uint8
	bindAliases           map[types.Object]int
	keyAliases            map[types.Object]extractKeyCallable
	qualifiers            map[types.Object]bool
	definitions           map[types.Object]bool
	definitionSpecs       map[types.Object]bool
	structDefinitions     map[types.Object]bool
	definitionFactories   map[extractFactoryResult]map[string]bool
	contractFactories     map[extractFactoryResult]bool
	directFactories       map[extractFactoryResult]string
	tupleOrigins          map[types.Object]extractTupleOrigin
	untrustedCallables    map[types.Object]bool
	trackedKeyWrites      map[types.Object]extractTrackedKeyWrites
	selectedFunctions     map[types.Object]bool
	packageVariables      map[types.Object]bool
	parameters            map[types.Object]bool
	resultVariables       map[types.Object]bool
	addressTaken          map[types.Object]bool
	trustedFactoryReturns map[extractFactoryReturn]bool
}

type extractKeyCallable struct {
	index int
	owner string
}

type extractTrackedKeyWrites struct {
	owner       string
	expressions []ast.Expr
	unresolved  bool
}

type extractAssignment struct {
	expression ast.Expr
	position   token.Pos
	count      int
}

type extractFactoryResult struct {
	function types.Object
	index    int
}

type extractFactoryReturn struct {
	statement *ast.ReturnStmt
	index     int
}

type extractTupleOrigin struct {
	call  *ast.CallExpr
	index int
}

type extractKeyResult struct {
	exact   string
	domain  string
	prefix  string
	dynamic bool
	covered bool
}

type extractResolver struct {
	analysis *extractPackageAnalysis
	seen     map[types.Object]bool
	budget   int
}

type extractPackageUnit struct {
	path      string
	files     []extractParsedFile
	info      *types.Info
	value     *types.Package
	importMap map[string]string
	checking  bool
	checked   bool
}

type extractPackageGraph struct {
	ctx                 context.Context
	state               *extractState
	units               []*extractPackageUnit
	byPath              map[string]*extractPackageUnit
	fallback            types.Importer
	standard            types.Importer
	i18n                *types.Package
	errs                *types.Package
	selectedFunctions   map[types.Object]bool
	definitionFactories map[extractFactoryResult]map[string]bool
	contractFactories   map[extractFactoryResult]bool
	directFactories     map[extractFactoryResult]string
}

func (s *extractState) analyze(ctx context.Context) error {
	groups := make(map[string][]extractParsedFile)
	groupPaths := make(map[string]string)
	for _, parsed := range s.parsed {
		path := ""
		selection, modeled := goUsageSelection{}, false
		if s.loader != nil {
			selection, modeled = s.loader.selection(parsed.path)
		}
		if modeled {
			path = selection.packagePath
			parsed.sourcePath = selection.logicalPath
		} else {
			parsed.sourcePath = s.fallbackSelection(parsed.path, true).logicalPath
		}
		key := filepath.Dir(parsed.path) + "\x00" + parsed.file.Name.Name
		if path != "" {
			key = path + "\x00" + parsed.file.Name.Name
			groupPaths[key] = path
		}
		groups[key] = append(groups[key], parsed)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	fallback := importer.ForCompiler(s.fileset, s.buildContext.Compiler, func(path string) (io.ReadCloser, error) {
		return s.loader.openExport(ctx, path)
	})
	graph := &extractPackageGraph{
		ctx: ctx, state: s, byPath: make(map[string]*extractPackageUnit),
		fallback: fallback, standard: importer.Default(), selectedFunctions: make(map[types.Object]bool),
		definitionFactories: make(map[extractFactoryResult]map[string]bool), contractFactories: make(map[extractFactoryResult]bool),
		directFactories: make(map[extractFactoryResult]string),
	}
	if imported, err := fallback.Import(errsPackagePath); err == nil {
		graph.errs = imported
	} else {
		graph.errs, err = syntheticErrsPackage(graph)
		if err != nil {
			return fmt.Errorf("construct fallback errs type surface: %w", err)
		}
	}
	for _, key := range keys {
		files := groups[key]
		slices.SortFunc(files, func(left, right extractParsedFile) int {
			return strings.Compare(left.path, right.path)
		})
		path := groupPaths[key]
		moduleBacked := path != ""
		if path == "" {
			var err error
			path, moduleBacked, err = extractPackagePath(ctx, filepath.Dir(files[0].path), files[0].file.Name.Name)
			if err != nil {
				return err
			}
		}
		for index := range files {
			if files[index].sourcePath != "" {
				s.sourcePaths[files[index].path] = files[index].sourcePath
			} else if moduleBacked {
				files[index].sourcePath = path + "/" + filepath.Base(files[index].path)
			} else {
				files[index].sourcePath = s.fallbackSourcePath(files[index].path)
			}
			s.sourcePaths[files[index].path] = files[index].sourcePath
		}
		if previous := graph.byPath[path]; previous != nil {
			return fmt.Errorf("extraction import path %q contains packages %q and %q", path, previous.files[0].file.Name.Name, files[0].file.Name.Name)
		}
		unit := &extractPackageUnit{path: path, files: files, importMap: cloneStringMap(s.loader.importMaps[path])}
		graph.units = append(graph.units, unit)
		graph.byPath[path] = unit
	}
	if imported, err := fallback.Import(i18nPackagePath); err == nil {
		graph.i18n = imported
	} else {
		graph.i18n, err = syntheticI18nPackage(graph)
		if err != nil {
			return fmt.Errorf("construct fallback i18n type surface: %w", err)
		}
	}
	for _, unit := range graph.units {
		if _, err := graph.check(unit); err != nil {
			return err
		}
	}
	graph.collectSelectedFunctions()
	analyses := make([]*extractPackageAnalysis, 0, len(graph.units))
	for _, unit := range graph.units {
		analysis, err := graph.prepareAnalysis(unit)
		if err != nil {
			return err
		}
		analyses = append(analyses, analysis)
	}
	graph.collectFactories(analyses)
	for _, analysis := range analyses {
		analysis.collectTrustedFactoryReturns()
		for _, file := range analysis.files {
			if err := analysis.inspectFile(ctx, file); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (g *extractPackageGraph) collectSelectedFunctions() {
	for _, unit := range g.units {
		for _, file := range unit.files {
			for _, declaration := range file.file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Body == nil {
					continue
				}
				if object := unit.info.Defs[function.Name]; object != nil {
					g.selectedFunctions[object] = true
				}
			}
		}
	}
}

func (g *extractPackageGraph) check(unit *extractPackageUnit) (*types.Package, error) {
	if unit.checked {
		return unit.value, nil
	}
	if unit.checking {
		return nil, fmt.Errorf("extraction import cycle reaches %q", unit.path)
	}
	if err := g.ctx.Err(); err != nil {
		return nil, err
	}
	unit.checking = true
	defer func() { unit.checking = false }()
	unit.info = &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Instances:  make(map[*ast.Ident]types.Instance),
	}
	syntax := make([]*ast.File, len(unit.files))
	for index := range unit.files {
		syntax[index] = unit.files[index].file
	}
	configuration := types.Config{
		Importer: extractUnitImporter{graph: g, importMap: unit.importMap},
		Error:    func(error) { g.state.complete = false },
	}
	unit.value, _ = configuration.Check(unit.path, g.state.fileset, syntax, unit.info)
	unit.checked = true
	if unit.value == nil {
		return nil, fmt.Errorf("type-check extracted package %q", unit.path)
	}
	return unit.value, nil
}

type extractUnitImporter struct {
	graph     *extractPackageGraph
	importMap map[string]string
}

func (i extractUnitImporter) Import(path string) (*types.Package, error) {
	if mapped := i.importMap[path]; mapped != "" {
		path = mapped
	}
	return i.graph.Import(path)
}

func (g *extractPackageGraph) Import(path string) (*types.Package, error) {
	if path == i18nPackagePath {
		if g.i18n == nil {
			return nil, fmt.Errorf("synthetic i18n package is not initialized")
		}
		return g.i18n, nil
	}
	if unit := g.byPath[path]; unit != nil {
		return g.check(unit)
	}
	if path == errsPackagePath {
		return g.errs, nil
	}
	if imported, err := g.fallback.Import(path); err == nil {
		return imported, nil
	}
	if imported, err := g.standard.Import(path); err == nil {
		return imported, nil
	}
	g.state.complete = false
	name := pathpkg.Base(path)
	name = strings.ReplaceAll(name, "-", "_")
	placeholder := types.NewPackage(path, name)
	placeholder.MarkComplete()
	return placeholder, nil
}

func (g *extractPackageGraph) prepareAnalysis(unit *extractPackageUnit) (*extractPackageAnalysis, error) {
	if _, err := g.check(unit); err != nil {
		return nil, err
	}
	analysis := &extractPackageAnalysis{
		state: g.state, info: unit.info, files: unit.files, assignments: make(map[types.Object]extractAssignment), origins: make(map[types.Object]extractAssignment),
		capabilityValues: make(map[types.Object][]ast.Expr), capabilityMemo: make(map[types.Object]uint8),
		bindAliases: make(map[types.Object]int), keyAliases: make(map[types.Object]extractKeyCallable),
		qualifiers: make(map[types.Object]bool), definitions: make(map[types.Object]bool),
		definitionSpecs: make(map[types.Object]bool), structDefinitions: make(map[types.Object]bool),
		definitionFactories: g.definitionFactories, contractFactories: g.contractFactories, directFactories: g.directFactories,
		tupleOrigins:       make(map[types.Object]extractTupleOrigin),
		untrustedCallables: make(map[types.Object]bool),
		trackedKeyWrites:   make(map[types.Object]extractTrackedKeyWrites),
		selectedFunctions:  g.selectedFunctions, packageVariables: make(map[types.Object]bool),
		parameters: make(map[types.Object]bool), resultVariables: make(map[types.Object]bool), addressTaken: make(map[types.Object]bool),
		trustedFactoryReturns: make(map[extractFactoryReturn]bool),
	}
	analysis.collectPackageVariables()
	analysis.collectCapabilityFlowObjects()
	analysis.collectAssignments()
	analysis.invalidateAddressTakenAssignments()
	analysis.collectTrackedKeyWrites()
	analysis.resolveCallableAliases()
	return analysis, nil
}

func (g *extractPackageGraph) collectFactories(analyses []*extractPackageAnalysis) {
	for _, analysis := range analyses {
		analysis.seedFactoryCandidates()
	}
	for {
		changed := false
		for _, analysis := range analyses {
			changed = analysis.pruneContractFactories() || changed
			changed = analysis.pruneDefinitionFactories() || changed
			changed = analysis.pruneDirectFactories() || changed
		}
		if !changed {
			return
		}
	}
}

func (a *extractPackageAnalysis) collectCapabilityFlowObjects() {
	for _, file := range a.files {
		ast.Inspect(file.file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.FuncDecl:
				a.collectParameters(node.Type.Params)
				a.collectResultVariables(node.Type.Results)
			case *ast.FuncLit:
				a.collectParameters(node.Type.Params)
				a.collectResultVariables(node.Type.Results)
			case *ast.UnaryExpr:
				if node.Op != token.AND {
					break
				}
				identifier, ok := extractUnparenthesized(node.X).(*ast.Ident)
				if ok {
					a.addressTaken[a.info.ObjectOf(identifier)] = true
				}
			}
			return true
		})
	}
}

func (a *extractPackageAnalysis) collectResultVariables(fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			if object := a.info.Defs[name]; object != nil {
				a.resultVariables[object] = true
			}
		}
	}
}

func (a *extractPackageAnalysis) collectParameters(fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			if object := a.info.Defs[name]; object != nil {
				a.parameters[object] = true
			}
		}
	}
}

func (a *extractPackageAnalysis) collectPackageVariables() {
	for _, file := range a.files {
		for _, declaration := range file.file.Decls {
			declaration, ok := declaration.(*ast.GenDecl)
			if !ok || declaration.Tok != token.VAR {
				continue
			}
			for _, raw := range declaration.Specs {
				specification, ok := raw.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range specification.Names {
					if object := a.info.Defs[name]; object != nil {
						a.packageVariables[object] = true
					}
				}
			}
		}
	}
}

func extractPackagePath(ctx context.Context, directory, packageName string) (string, bool, error) {
	for current := filepath.Clean(directory); ; current = filepath.Dir(current) {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		path := filepath.Join(current, "go.mod")
		info, err := os.Lstat(path)
		if err == nil {
			if info.Mode().IsRegular() {
				content, readErr := readRegularFile(ctx, path, 1<<20)
				if readErr != nil {
					return "", false, readErr
				}
				modulePath := modfile.ModulePath(content)
				if modulePath == "" {
					return "", false, fmt.Errorf("extract module path from %q", path)
				}
				if err := module.CheckImportPath(modulePath); err != nil {
					return "", false, fmt.Errorf("extract module path from %q: %w", path, err)
				}
				relative, relativeErr := filepath.Rel(current, directory)
				if relativeErr != nil {
					return "", false, fmt.Errorf("resolve package path for %q: %w", directory, relativeErr)
				}
				if relative == "." {
					return modulePath, true, nil
				}
				return modulePath + "/" + filepath.ToSlash(relative), true, nil
			}
			return "", false, fmt.Errorf("module metadata %q is not a regular file", path)
		}
		if !os.IsNotExist(err) {
			return "", false, fmt.Errorf("inspect module metadata %q: %w", path, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return "vv-i18n/extracted/" + strings.ReplaceAll(directory, string(filepath.Separator), "/") + "/" + packageName, false, nil
}

func (s *extractState) fallbackSourcePath(filename string) string {
	for index, root := range s.sourceRoots {
		var relative string
		if root.directory {
			candidate, err := filepath.Rel(root.path, filename)
			if err != nil || candidate == ".." || strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
				continue
			}
			relative = candidate
		} else if root.path == filename {
			relative = filepath.Base(filename)
		} else {
			continue
		}
		relative = filepath.ToSlash(relative)
		if len(s.sourceRoots) == 1 {
			return relative
		}
		return fmt.Sprintf("root-%d/%s", index+1, relative)
	}
	return filepath.Base(filename)
}

func (a *extractPackageAnalysis) collectAssignments() {
	for _, file := range a.files {
		ast.Inspect(file.file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.ValueSpec:
				for index, name := range node.Names {
					var expression ast.Expr
					if len(node.Values) == len(node.Names) {
						expression = node.Values[index]
					} else if len(node.Names) == 1 && len(node.Values) == 1 {
						expression = node.Values[0]
					}
					object := a.info.Defs[name]
					a.addAssignment(object, expression, name.Pos())
					if len(node.Values) == 1 && len(node.Names) > 1 && index == 0 {
						expression = node.Values[0]
					}
					a.addOrigin(object, expression, name.Pos())
					a.addCapabilityValue(object, expression)
				}
			case *ast.AssignStmt:
				for index, left := range node.Lhs {
					identifier, ok := left.(*ast.Ident)
					if !ok {
						continue
					}
					object := a.info.Defs[identifier]
					if object == nil {
						object = a.info.Uses[identifier]
					}
					if len(node.Lhs) > 1 && len(node.Rhs) == 1 {
						if call, ok := extractUnparenthesized(node.Rhs[0]).(*ast.CallExpr); ok {
							if tuple, ok := a.info.TypeOf(call).(*types.Tuple); ok && tuple.Len() == len(node.Lhs) {
								a.tupleOrigins[object] = extractTupleOrigin{call: call, index: index}
							}
						}
					}
					var expression ast.Expr
					if len(node.Rhs) == len(node.Lhs) {
						expression = node.Rhs[index]
					} else if len(node.Lhs) == 1 && len(node.Rhs) == 1 {
						expression = node.Rhs[0]
					}
					if expression == nil && len(node.Lhs) > 1 && len(node.Rhs) == 1 {
						a.addUnknownAssignment(object, identifier.Pos())
					} else {
						a.addAssignment(object, expression, identifier.Pos())
					}
					if len(node.Rhs) == 1 && len(node.Lhs) > 1 && index == 0 {
						expression = node.Rhs[0]
					}
					a.addOrigin(object, expression, identifier.Pos())
					a.addCapabilityValue(object, expression)
				}
			case *ast.RangeStmt:
				for _, expression := range []ast.Expr{node.Key, node.Value} {
					identifier, ok := extractUnparenthesized(expression).(*ast.Ident)
					if !ok {
						continue
					}
					object := a.info.Defs[identifier]
					if object == nil {
						object = a.info.Uses[identifier]
					}
					a.addUnknownAssignment(object, identifier.Pos())
				}
			}
			return true
		})
	}
}

func (a *extractPackageAnalysis) invalidateAddressTakenAssignments() {
	for object := range a.addressTaken {
		assignment, ok := a.assignments[object]
		if !ok || assignment.expression == nil {
			continue
		}
		assignment.count++
		a.assignments[object] = assignment
	}
}

func (a *extractPackageAnalysis) addCapabilityValue(object types.Object, expression ast.Expr) {
	if object == nil || object.Name() == "_" || expression == nil {
		return
	}
	a.capabilityValues[object] = append(a.capabilityValues[object], expression)
}

func (a *extractPackageAnalysis) collectTrackedKeyWrites() {
	for _, file := range a.files {
		ast.Inspect(file.file, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for index, left := range assignment.Lhs {
				selector, ok := extractUnparenthesized(left).(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Key" {
					continue
				}
				owner := extractTrackedKeyFieldOwner(a.info, selector)
				object := extractAssignedObject(a.info, extractUnparenthesized(selector.X))
				if owner == "" || object == nil {
					continue
				}
				writes := a.trackedKeyWrites[object]
				if writes.owner != "" && writes.owner != owner {
					writes.unresolved = true
				}
				writes.owner = owner
				if assignment.Tok != token.ASSIGN || len(assignment.Lhs) != len(assignment.Rhs) {
					writes.unresolved = true
				} else {
					writes.expressions = append(writes.expressions, assignment.Rhs[index])
				}
				a.trackedKeyWrites[object] = writes
			}
			return true
		})
	}
}

func (a *extractPackageAnalysis) addOrigin(object types.Object, expression ast.Expr, position token.Pos) {
	if object == nil || object.Name() == "_" || expression == nil {
		return
	}
	origin := a.origins[object]
	origin.count++
	if origin.count == 1 {
		origin.expression = expression
		origin.position = position
	}
	a.origins[object] = origin
}

func (a *extractPackageAnalysis) seedFactoryCandidates() {
	for _, file := range a.files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			object, ok := a.info.Defs[function.Name].(*types.Func)
			if !ok {
				continue
			}
			signature, ok := object.Type().(*types.Signature)
			if !ok || signature.Results().Len() == 0 {
				continue
			}
			for resultIndex := 0; resultIndex < signature.Results().Len(); resultIndex++ {
				key := extractFactoryResult{function: object, index: resultIndex}
				kind := extractNamedTypeName(signature.Results().At(resultIndex).Type())
				if kind == "ContractRef" {
					a.contractFactories[key] = true
				}
				switch kind {
				case "Definition", "DefinitionSpec", "Descriptor", "ErrorSpec", "ErrorPlanSpec", "ErrorMapping", "FieldLabel":
					a.directFactories[key] = kind
				}
				fields := extractDefinitionResultFields(signature.Results().At(resultIndex).Type())
				if len(fields) == 0 {
					continue
				}
				known := make(map[string]bool, len(fields))
				for _, field := range fields {
					known[field.Name()] = true
				}
				a.definitionFactories[key] = known
			}
		}
	}
}

func (a *extractPackageAnalysis) pruneDirectFactories() bool {
	changed := false
	for _, file := range a.files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			object, ok := a.info.Defs[function.Name].(*types.Func)
			if !ok {
				continue
			}
			signature, _ := object.Type().(*types.Signature)
			if signature == nil {
				continue
			}
			for key, kind := range a.directFactories {
				if key.function == object && !a.directFactoryResultKnown(function, signature, key.index, kind) {
					delete(a.directFactories, key)
					changed = true
				}
			}
		}
	}
	return changed
}

func (a *extractPackageAnalysis) directFactoryResultKnown(function *ast.FuncDecl, signature *types.Signature, index int, kind string) bool {
	returns := extractFunctionReturns(function.Body)
	if len(returns) == 0 {
		return false
	}
	for _, statement := range returns {
		expression, found := a.definitionReturnExpression(statement, signature, index)
		if found && a.directFactoryValueKnown(expression, kind, make(map[types.Object]bool)) {
			continue
		}
		call, forwarded := a.forwardedFactoryCall(statement, signature)
		if !forwarded || !a.forwardedDirectFactoryKnown(call, index, kind) {
			return false
		}
	}
	return true
}

func (a *extractPackageAnalysis) directFactoryValueKnown(expression ast.Expr, kind string, seen map[types.Object]bool) bool {
	switch kind {
	case "Definition":
		return a.definitionValueKnown(expression, seen)
	case "DefinitionSpec":
		return a.definitionSpecKnown(expression, seen)
	case "Descriptor":
		return a.descriptorValueKnown(expression, seen)
	case "ErrorSpec", "ErrorPlanSpec":
		return a.errorSpecKnown(expression, seen)
	case "ErrorMapping", "FieldLabel":
		return a.integrationKeyValueKnown(expression, kind, seen)
	}
	return false
}

func (a *extractPackageAnalysis) directFactoryCallKnown(call *ast.CallExpr, index int, kind string) bool {
	key := extractFactoryResult{function: extractCalledObject(a.info, call.Fun), index: index}
	return a.directFactories[key] == kind
}

func (a *extractPackageAnalysis) forwardedDirectFactoryKnown(call *ast.CallExpr, index int, kind string) bool {
	if a.directFactoryCallKnown(call, index, kind) {
		return true
	}
	switch kind {
	case "Definition":
		return index == 0 && a.definitionConstructorExpression(call)
	case "Descriptor":
		return index == 0 && a.descriptorCallKnown(call)
	}
	return false
}

func (a *extractPackageAnalysis) pruneContractFactories() bool {
	changed := false
	for _, file := range a.files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			object, ok := a.info.Defs[function.Name].(*types.Func)
			if !ok {
				continue
			}
			signature, _ := object.Type().(*types.Signature)
			if signature == nil {
				continue
			}
			for resultIndex := 0; resultIndex < signature.Results().Len(); resultIndex++ {
				key := extractFactoryResult{function: object, index: resultIndex}
				if a.contractFactories[key] && !a.contractFactoryResultKnown(function, signature, resultIndex) {
					delete(a.contractFactories, key)
					changed = true
				}
			}
		}
	}
	return changed
}

func (a *extractPackageAnalysis) contractFactoryResultKnown(function *ast.FuncDecl, signature *types.Signature, resultIndex int) bool {
	returns := extractFunctionReturns(function.Body)
	if len(returns) == 0 {
		return false
	}
	for _, statement := range returns {
		expression, found := a.definitionReturnExpression(statement, signature, resultIndex)
		if found && a.contractValueKnown(expression, make(map[types.Object]bool)) {
			continue
		}
		if !a.forwardedContractFactoryKnown(statement, signature, resultIndex) {
			return false
		}
	}
	return true
}

func (a *extractPackageAnalysis) pruneDefinitionFactories() bool {
	changed := false
	for _, file := range a.files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			object, ok := a.info.Defs[function.Name].(*types.Func)
			if !ok {
				continue
			}
			proven := a.definitionFactoryFields(function, object)
			for resultIndex, fields := range a.definitionFactoriesFor(object) {
				for fieldName := range fields {
					if !proven[resultIndex][fieldName] {
						delete(fields, fieldName)
						changed = true
					}
				}
				if len(fields) == 0 {
					delete(a.definitionFactories, extractFactoryResult{function: object, index: resultIndex})
				}
			}
		}
	}
	return changed
}

func (a *extractPackageAnalysis) collectTrustedFactoryReturns() {
	for _, file := range a.files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			object := a.info.Defs[function.Name]
			if object == nil {
				continue
			}
			signature, _ := object.Type().(*types.Signature)
			if signature == nil {
				continue
			}
			for _, statement := range extractFunctionReturns(function.Body) {
				if len(statement.Results) == signature.Results().Len() {
					for index := range statement.Results {
						if a.definitionFactoryResultPartiallyKnown(object, index) {
							a.trustedFactoryReturns[extractFactoryReturn{statement: statement, index: index}] = true
						}
					}
					continue
				}
				if len(statement.Results) != 1 || signature.Results().Len() < 2 {
					continue
				}
				trusted := true
				for index := 0; index < signature.Results().Len(); index++ {
					if extractContainsHiddenKeyType(signature.Results().At(index).Type(), make(map[types.Type]bool)) &&
						!a.definitionFactoryResultPartiallyKnown(object, index) {
						trusted = false
						break
					}
				}
				if trusted {
					a.trustedFactoryReturns[extractFactoryReturn{statement: statement, index: 0}] = true
				}
			}
		}
	}
}

func (a *extractPackageAnalysis) definitionFactoryResultPartiallyKnown(function types.Object, index int) bool {
	return len(a.definitionFactories[extractFactoryResult{function: function, index: index}]) != 0
}

func (a *extractPackageAnalysis) definitionFactoriesFor(function types.Object) map[int]map[string]bool {
	result := make(map[int]map[string]bool)
	for key, fields := range a.definitionFactories {
		if key.function == function {
			result[key.index] = fields
		}
	}
	return result
}

func (a *extractPackageAnalysis) definitionFactoryFields(function *ast.FuncDecl, object *types.Func) map[int]map[string]bool {
	signature, ok := object.Type().(*types.Signature)
	if !ok || signature.Results().Len() == 0 {
		return nil
	}
	returns := extractFunctionReturns(function.Body)
	if len(returns) == 0 {
		return nil
	}
	known := make(map[int]map[string]bool)
	for resultIndex := 0; resultIndex < signature.Results().Len(); resultIndex++ {
		for _, field := range extractDefinitionResultFields(signature.Results().At(resultIndex).Type()) {
			valid := true
			for _, statement := range returns {
				expression, ok := a.definitionReturnExpression(statement, signature, resultIndex)
				if ok && a.definitionReturnedFieldKnown(function, expression, field, statement.Pos(), token.NoPos) {
					continue
				}
				if !a.forwardedDefinitionFactoryFieldKnown(statement, signature, resultIndex, field) {
					valid = false
					break
				}
			}
			if valid {
				if known[resultIndex] == nil {
					known[resultIndex] = make(map[string]bool)
				}
				known[resultIndex][field.Name()] = true
			}
		}
	}
	return known
}

func extractFunctionReturns(body *ast.BlockStmt) []*ast.ReturnStmt {
	returns := make([]*ast.ReturnStmt, 0)
	ast.Inspect(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			returns = append(returns, node)
		}
		return true
	})
	return returns
}

func (a *extractPackageAnalysis) forwardedContractFactoryKnown(statement *ast.ReturnStmt, signature *types.Signature, index int) bool {
	call, ok := a.forwardedFactoryCall(statement, signature)
	if !ok {
		return false
	}
	key := extractFactoryResult{function: extractCalledObject(a.info, call.Fun), index: index}
	return a.contractFactories[key]
}

func (a *extractPackageAnalysis) forwardedDefinitionFactoryFieldKnown(statement *ast.ReturnStmt, signature *types.Signature, index int, field types.Object) bool {
	call, ok := a.forwardedFactoryCall(statement, signature)
	if !ok {
		return false
	}
	key := extractFactoryResult{function: extractCalledObject(a.info, call.Fun), index: index}
	return a.definitionFactories[key][field.Name()]
}

func (a *extractPackageAnalysis) forwardedFactoryCall(statement *ast.ReturnStmt, signature *types.Signature) (*ast.CallExpr, bool) {
	if len(statement.Results) != 1 || signature.Results().Len() < 2 {
		return nil, false
	}
	call, ok := extractUnparenthesized(statement.Results[0]).(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	tuple, ok := a.info.TypeOf(call).(*types.Tuple)
	return call, ok && tuple.Len() == signature.Results().Len()
}

func extractDefinitionResultFields(value types.Type) []types.Object {
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	structure, ok := value.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	fields := make([]types.Object, 0)
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if extractNamedTypeName(field.Type()) == "Definition" {
			fields = append(fields, field)
		}
	}
	return fields
}

func (a *extractPackageAnalysis) definitionReturnExpression(statement *ast.ReturnStmt, signature *types.Signature, index int) (ast.Expr, bool) {
	if len(statement.Results) == signature.Results().Len() {
		return statement.Results[index], true
	}
	if len(statement.Results) == 0 && signature.Results().At(index).Name() != "" {
		object := signature.Results().At(index)
		for identifier, defined := range a.info.Defs {
			if defined == object {
				return identifier, true
			}
		}
	}
	return nil, false
}

func (a *extractPackageAnalysis) definitionReturnedFieldKnown(function *ast.FuncDecl, expression ast.Expr, field types.Object, before, allowedAddress token.Pos) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.UnaryExpr:
		if expression.Op == token.AND {
			return a.definitionReturnedFieldKnown(function, expression.X, field, before, expression.Pos())
		}
		return false
	case *ast.StarExpr:
		return a.definitionReturnedFieldKnown(function, expression.X, field, before, allowedAddress)
	case *ast.CompositeLit:
		return a.definitionContainerExpressionFieldKnown(expression, field)
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		object := a.info.ObjectOf(expression)
		if object == nil || object.Parent() == nil || object.Pkg() == nil || object.Parent() == object.Pkg().Scope() || a.definitionObjectUntrusted(function, object, allowedAddress) {
			return false
		}
		known := a.definitionInitialFieldKnown(function, object, field)
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if !known || node == nil || node.Pos() >= before {
				return false
			}
			assignment, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for index, left := range assignment.Lhs {
				if a.info.ObjectOf(extractIdentifier(left)) == object {
					if tupleKnown, tuple := a.definitionTupleAssignmentFieldKnown(assignment, index, field); tuple {
						known = tupleKnown
						continue
					}
					right, exists := extractAssignmentExpression(assignment, index)
					if !exists || !a.definitionContainerExpressionFieldKnown(right, field) {
						known = false
						return false
					}
				}
				selector, selectorOK := extractUnparenthesized(left).(*ast.SelectorExpr)
				if !selectorOK || a.info.ObjectOf(extractIdentifier(selector.X)) != object || a.info.ObjectOf(selector.Sel) != field {
					continue
				}
				right, exists := extractAssignmentExpression(assignment, index)
				if !exists || assignment.Tok != token.ASSIGN || !a.definitionValueKnown(right, make(map[types.Object]bool)) {
					known = false
					return false
				}
			}
			return true
		})
		return known
	case *ast.CallExpr:
		return a.definitionFactories[extractFactoryResult{function: extractCalledObject(a.info, expression.Fun), index: 0}][field.Name()]
	}
	return false
}

func (a *extractPackageAnalysis) definitionContainerExpressionFieldKnown(expression ast.Expr, field types.Object) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.UnaryExpr:
		return expression.Op == token.AND && a.definitionContainerExpressionFieldKnown(expression.X, field)
	case *ast.StarExpr:
		return a.definitionContainerExpressionFieldKnown(expression.X, field)
	case *ast.CompositeLit:
		value, found := extractCompositeFieldValue(a.info, expression, field)
		return !found || a.definitionValueKnown(value, make(map[types.Object]bool))
	case *ast.Ident:
		return expression.Name == "nil"
	case *ast.CallExpr:
		return a.definitionFactories[extractFactoryResult{function: extractCalledObject(a.info, expression.Fun), index: 0}][field.Name()]
	}
	return false
}

func (a *extractPackageAnalysis) definitionTupleAssignmentFieldKnown(assignment *ast.AssignStmt, index int, field types.Object) (bool, bool) {
	if len(assignment.Rhs) != 1 || len(assignment.Lhs) < 2 || index >= len(assignment.Lhs) {
		return false, false
	}
	call, ok := extractUnparenthesized(assignment.Rhs[0]).(*ast.CallExpr)
	if !ok {
		return false, false
	}
	tuple, ok := a.info.TypeOf(call).(*types.Tuple)
	if !ok || tuple.Len() != len(assignment.Lhs) {
		return false, false
	}
	key := extractFactoryResult{function: extractCalledObject(a.info, call.Fun), index: index}
	return a.definitionFactories[key][field.Name()], true
}

func (a *extractPackageAnalysis) definitionInitialFieldKnown(function *ast.FuncDecl, object, field types.Object) bool {
	known := true
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if !known {
			return false
		}
		specification, ok := node.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for index, name := range specification.Names {
			if a.info.Defs[name] != object {
				continue
			}
			if len(specification.Values) == 0 {
				return false
			}
			var expression ast.Expr
			if len(specification.Values) == len(specification.Names) {
				expression = specification.Values[index]
			} else if index == 0 && len(specification.Values) == 1 {
				expression = specification.Values[0]
			}
			if !a.definitionContainerExpressionFieldKnown(expression, field) {
				known = false
				return false
			}
			return false
		}
		return true
	})
	return known
}

func extractIdentifier(expression ast.Expr) *ast.Ident {
	identifier, _ := extractUnparenthesized(expression).(*ast.Ident)
	return identifier
}

func extractAssignmentExpression(assignment *ast.AssignStmt, index int) (ast.Expr, bool) {
	if len(assignment.Lhs) == len(assignment.Rhs) && index < len(assignment.Rhs) {
		return assignment.Rhs[index], true
	}
	if index == 0 && len(assignment.Rhs) == 1 {
		return assignment.Rhs[0], true
	}
	return nil, false
}

func extractCompositeFieldValue(info *types.Info, literal *ast.CompositeLit, field types.Object) (ast.Expr, bool) {
	structure, ok := types.Unalias(info.TypeOf(literal)).Underlying().(*types.Struct)
	if !ok {
		return nil, false
	}
	for index, element := range literal.Elts {
		if pair, keyed := element.(*ast.KeyValueExpr); keyed {
			if info.ObjectOf(extractIdentifier(pair.Key)) == field {
				return pair.Value, true
			}
			continue
		}
		if index < structure.NumFields() && structure.Field(index) == field {
			return element, true
		}
	}
	return nil, false
}

func extractCompositeNamedFieldValue(info *types.Info, literal *ast.CompositeLit, name string) (ast.Expr, bool) {
	value := types.Unalias(info.TypeOf(literal))
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	structure, ok := value.Underlying().(*types.Struct)
	if !ok {
		return nil, false
	}
	for index, element := range literal.Elts {
		if pair, keyed := element.(*ast.KeyValueExpr); keyed {
			identifier := extractIdentifier(pair.Key)
			if identifier != nil && identifier.Name == name {
				return pair.Value, true
			}
			continue
		}
		if index < structure.NumFields() && structure.Field(index).Name() == name {
			return element, true
		}
	}
	return nil, false
}

func (a *extractPackageAnalysis) definitionObjectUntrusted(function *ast.FuncDecl, object types.Object, allowedAddress token.Pos) bool {
	untrusted := false
	if typed, ok := a.info.Defs[function.Name].(*types.Func); ok {
		if signature, signatureOK := typed.Type().(*types.Signature); signatureOK {
			for index := 0; index < signature.Params().Len(); index++ {
				if signature.Params().At(index) == object {
					return true
				}
			}
		}
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if untrusted {
			return false
		}
		switch node := node.(type) {
		case *ast.UnaryExpr:
			if node.Op == token.AND && expressionRefersToObject(a.info, node.X, object) {
				if node.Pos() == allowedAddress {
					return true
				}
				untrusted = true
				return false
			}
		case *ast.SendStmt:
			if expressionRefersToObject(a.info, node.Value, object) {
				untrusted = true
				return false
			}
		case *ast.AssignStmt:
			for index, right := range node.Rhs {
				if !expressionRefersToObject(a.info, right, object) {
					continue
				}
				if len(node.Lhs) != len(node.Rhs) || index >= len(node.Lhs) {
					untrusted = true
					return false
				}
				left := extractUnparenthesized(node.Lhs[index])
				if identifier, ok := left.(*ast.Ident); !ok || a.info.ObjectOf(identifier) != object {
					untrusted = true
					return false
				}
			}
		case *ast.RangeStmt:
			for _, expression := range []ast.Expr{node.Key, node.Value} {
				if expression != nil && expressionRefersToObject(a.info, expression, object) {
					untrusted = true
					return false
				}
			}
		case *ast.ValueSpec:
			for _, value := range node.Values {
				if expressionRefersToObject(a.info, value, object) {
					untrusted = true
					return false
				}
			}
		case *ast.CompositeLit:
			for _, element := range node.Elts {
				if pair, ok := element.(*ast.KeyValueExpr); ok {
					element = pair.Value
				}
				if expression, ok := element.(ast.Expr); ok && expressionRefersToObject(a.info, expression, object) {
					untrusted = true
					return false
				}
			}
		case *ast.CallExpr:
			if typed, ok := a.info.Types[extractUnparenthesized(node.Fun)]; ok && typed.IsType() {
				return true
			}
			for _, argument := range node.Args {
				if expressionRefersToObject(a.info, argument, object) {
					untrusted = true
					return false
				}
			}
		}
		return true
	})
	return untrusted
}

func expressionRefersToObject(info *types.Info, expression ast.Expr, object types.Object) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && info.ObjectOf(identifier) == object {
			found = true
			return false
		}
		return !found
	})
	return found
}

func (a *extractPackageAnalysis) addAssignment(object types.Object, expression ast.Expr, position token.Pos) {
	if object == nil || object.Name() == "_" || expression == nil {
		return
	}
	assignment := a.assignments[object]
	assignment.count++
	if assignment.count == 1 {
		assignment.expression = expression
		assignment.position = position
	}
	a.assignments[object] = assignment
}

func (a *extractPackageAnalysis) addUnknownAssignment(object types.Object, position token.Pos) {
	if object == nil || object.Name() == "_" {
		return
	}
	assignment := a.assignments[object]
	assignment.count++
	if assignment.count == 1 {
		assignment.position = position
	}
	a.assignments[object] = assignment
}

func (a *extractPackageAnalysis) resolveCallableAliases() {
	changed := true
	for changed {
		changed = false
		for object, assignment := range a.assignments {
			if assignment.count != 1 || assignment.expression == nil {
				continue
			}
			if a.packageVariables[object] {
				if !a.untrustedCallables[object] && a.callableAliasExpression(assignment.expression) {
					a.untrustedCallables[object] = true
					changed = true
				}
				continue
			}
			if assigned := extractAssignedObject(a.info, assignment.expression); assigned != nil && a.untrustedCallables[assigned] {
				if !a.untrustedCallables[object] {
					a.untrustedCallables[object] = true
					changed = true
				}
				continue
			}
			if _, exists := a.bindAliases[object]; !exists {
				if keyIndex, ok := a.snapshotBindExpression(assignment.expression); ok {
					a.bindAliases[object] = keyIndex
					changed = true
				} else if assigned := extractAssignedObject(a.info, assignment.expression); assigned != nil {
					if keyIndex, exists := a.bindAliases[assigned]; exists {
						a.bindAliases[object] = keyIndex
						changed = true
					}
				}
			}
			if !a.qualifiers[object] {
				if extractI18nObject(a.info, assignment.expression, "Qualify") {
					a.qualifiers[object] = true
					changed = true
				} else if assigned := extractAssignedObject(a.info, assignment.expression); assigned != nil && a.qualifiers[assigned] {
					a.qualifiers[object] = true
					changed = true
				}
			}
			if _, exists := a.keyAliases[object]; !exists {
				if callable, ok := a.snapshotKeyCallExpression(assignment.expression); ok {
					a.keyAliases[object] = callable
					changed = true
				} else if assigned := extractAssignedObject(a.info, assignment.expression); assigned != nil {
					if callable, exists := a.keyAliases[assigned]; exists {
						a.keyAliases[object] = callable
						changed = true
					}
				}
			}
			if !a.definitions[object] {
				if extractDefinitionKeyConstructor(a.info, assignment.expression) {
					a.definitions[object] = true
					changed = true
				} else if assigned := extractAssignedObject(a.info, assignment.expression); assigned != nil && a.definitions[assigned] {
					a.definitions[object] = true
					changed = true
				}
			}
			if !a.definitionSpecs[object] {
				if extractI18nObject(a.info, assignment.expression, "Define") {
					a.definitionSpecs[object] = true
					changed = true
				} else if assigned := extractAssignedObject(a.info, assignment.expression); assigned != nil && a.definitionSpecs[assigned] {
					a.definitionSpecs[object] = true
					changed = true
				}
			}
			if !a.structDefinitions[object] {
				if extractI18nObject(a.info, assignment.expression, "DefineStruct") {
					a.structDefinitions[object] = true
					changed = true
				} else if assigned := extractAssignedObject(a.info, assignment.expression); assigned != nil && a.structDefinitions[assigned] {
					a.structDefinitions[object] = true
					changed = true
				}
			}
		}
	}
	for object, assignment := range a.assignments {
		if assignment.count <= 1 || assignment.expression == nil {
			continue
		}
		if _, ok := a.snapshotBindExpression(assignment.expression); ok ||
			a.isSnapshotKeyCallExpression(assignment.expression) ||
			extractI18nObject(a.info, assignment.expression, "Qualify") ||
			extractDefinitionConstructor(a.info, assignment.expression) {
			a.state.addFindingAt(assignment.position, "reassigned i18n callable alias is not statically extractable")
		}
		_ = object
	}
}

func (a *extractPackageAnalysis) callableAliasExpression(expression ast.Expr) bool {
	if _, ok := a.snapshotBindExpression(expression); ok || a.isSnapshotKeyCallExpression(expression) ||
		extractI18nObject(a.info, expression, "Qualify") || extractDefinitionConstructor(a.info, expression) {
		return true
	}
	if extractDefinitionBindCallbackSignature(a.info.TypeOf(expression)) && a.definitionCallbackKnown(expression, make(map[types.Object]bool)) {
		return true
	}
	assigned := extractAssignedObject(a.info, expression)
	if assigned == nil {
		return false
	}
	_, bind := a.bindAliases[assigned]
	_, key := a.keyAliases[assigned]
	return bind || key || a.qualifiers[assigned] || a.definitions[assigned] || a.definitionSpecs[assigned] ||
		a.structDefinitions[assigned] || a.untrustedCallables[assigned]
}

func (a *extractPackageAnalysis) inspectFile(ctx context.Context, file extractParsedFile) error {
	var canceled error
	ast.Inspect(file.file, func(node ast.Node) bool {
		if err := ctx.Err(); err != nil {
			canceled = err
			return false
		}
		switch node := node.(type) {
		case *ast.FuncDecl:
			a.inspectCapabilityResults(a.info.TypeOf(node.Name))
		case *ast.FuncLit:
			a.inspectCapabilityResults(a.info.TypeOf(node))
		case *ast.CallExpr:
			a.inspectCall(node)
		case *ast.CompositeLit:
			a.inspectComposite(node)
			a.inspectCapabilityComposite(node)
		case *ast.AssignStmt:
			a.inspectKeyFieldAssignments(node)
			a.inspectHiddenKeyAssignments(node)
			a.inspectCapabilityAssignment(node)
		case *ast.ValueSpec:
			a.inspectCapabilityValues(node)
		case *ast.RangeStmt:
			a.inspectKeyFieldRange(node)
		case *ast.ReturnStmt:
			for index, result := range node.Results {
				if a.definitionConstructorCallable(result) {
					a.state.addFindingAt(result.Pos(), "escaped i18n definition constructor callable is not statically extractable")
				}
				if a.unresolvedDefinitionCallback(result) || a.unresolvedHiddenKeyValue(result) {
					a.state.complete = false
				}
				if !a.trustedFactoryReturns[extractFactoryReturn{statement: node, index: index}] &&
					a.usageCapabilityExpression(result, make(map[types.Object]bool)) {
					a.state.complete = false
				}
			}
		case *ast.SendStmt:
			if a.usageCapabilityExpression(node.Value, make(map[types.Object]bool)) ||
				a.unresolvedDefinitionCallback(node.Value) || a.unresolvedHiddenKeyValue(node.Value) {
				a.state.complete = false
			}
		case *ast.UnaryExpr:
			if node.Op == token.AND && a.trackedKeyAddressOwner(node.X) != "" {
				a.state.complete = false
			}
		}
		return true
	})
	return canceled
}

func (a *extractPackageAnalysis) inspectCapabilityResults(value types.Type) {
	signature, _ := types.Unalias(value).(*types.Signature)
	if signature == nil {
		return
	}
	for index := 0; index < signature.Results().Len(); index++ {
		if extractUsageCapabilityType(signature.Results().At(index).Type(), make(map[types.Type]bool)) {
			a.state.complete = false
			return
		}
	}
}

func (a *extractPackageAnalysis) inspectCapabilityAssignment(assignment *ast.AssignStmt) {
	for index, left := range assignment.Lhs {
		right, exists := extractAssignmentExpression(assignment, index)
		if !exists || !a.usageCapabilityExpression(right, make(map[types.Object]bool)) &&
			!a.unresolvedDefinitionCallback(right) && !a.unresolvedHiddenKeyValue(right) {
			continue
		}
		left = extractUnparenthesized(left)
		if identifier, ok := left.(*ast.Ident); ok {
			object := a.info.ObjectOf(identifier)
			if a.packageVariables[object] || a.resultVariables[object] {
				a.state.complete = false
			}
			continue
		}
		a.state.complete = false
	}
}

func (a *extractPackageAnalysis) inspectCapabilityValues(specification *ast.ValueSpec) {
	for index, name := range specification.Names {
		if !a.packageVariables[a.info.Defs[name]] {
			continue
		}
		var expression ast.Expr
		if len(specification.Values) == len(specification.Names) {
			expression = specification.Values[index]
		} else if index == 0 && len(specification.Values) == 1 {
			expression = specification.Values[0]
		}
		if expression != nil && (a.usageCapabilityExpression(expression, make(map[types.Object]bool)) ||
			a.unresolvedDefinitionCallback(expression) || a.unresolvedHiddenKeyValue(expression)) {
			a.state.complete = false
		}
	}
}

func (a *extractPackageAnalysis) inspectCapabilityComposite(literal *ast.CompositeLit) {
	if object := extractNamedTypeObject(a.info.TypeOf(literal)); object != nil && object.Pkg() != nil && object.Pkg().Path() == i18nPackagePath {
		return
	}
	value := a.info.TypeOf(literal)
	if value == nil {
		return
	}
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	_, mapLiteral := value.Underlying().(*types.Map)
	for index, element := range literal.Elts {
		if pair, ok := element.(*ast.KeyValueExpr); ok {
			if mapLiteral && (a.usageCapabilityExpression(pair.Key, make(map[types.Object]bool)) ||
				a.unresolvedDefinitionCallback(pair.Key) || a.unresolvedHiddenKeyValue(pair.Key)) {
				a.state.complete = false
				return
			}
			element = pair.Value
		}
		expression, ok := element.(ast.Expr)
		if !ok {
			continue
		}
		hiddenOwner := a.unresolvedHiddenKeyOwner(expression)
		if a.usageCapabilityExpression(expression, make(map[types.Object]bool)) ||
			a.unresolvedDefinitionCallback(expression) || hiddenOwner != "" && !a.compositeElementKeepsHiddenKey(literal, index, hiddenOwner) {
			a.state.complete = false
			return
		}
	}
}

func (a *extractPackageAnalysis) compositeElementKeepsHiddenKey(literal *ast.CompositeLit, index int, owner string) bool {
	value := types.Unalias(a.info.TypeOf(literal))
	if value == nil {
		return false
	}
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	var destination types.Type
	switch shape := value.Underlying().(type) {
	case *types.Array:
		destination = shape.Elem()
	case *types.Slice:
		destination = shape.Elem()
	case *types.Map:
		destination = shape.Elem()
	case *types.Struct:
		if index >= len(literal.Elts) {
			return false
		}
		if pair, ok := literal.Elts[index].(*ast.KeyValueExpr); ok {
			if field, ok := a.info.ObjectOf(extractIdentifier(pair.Key)).(*types.Var); ok && field.IsField() {
				destination = field.Type()
			}
		} else if index < shape.NumFields() {
			destination = shape.Field(index).Type()
		}
	}
	return extractContainedHiddenKeyTypeName(destination, make(map[types.Type]bool)) == owner
}

func (a *extractPackageAnalysis) inspectHiddenKeyAssignments(assignment *ast.AssignStmt) {
	for index, expression := range assignment.Lhs {
		if _, ok := extractUnparenthesized(expression).(*ast.Ident); ok {
			continue
		}
		owner := extractHiddenKeyTypeName(a.info.TypeOf(expression))
		if owner == "" {
			if extractContainedHiddenKeyTypeName(a.info.TypeOf(expression), make(map[types.Type]bool)) != "" {
				a.state.complete = false
			}
			continue
		}
		if assignment.Tok == token.ASSIGN {
			if len(assignment.Lhs) == len(assignment.Rhs) && a.hiddenKeyAssignmentKnown(assignment.Rhs[index], owner) {
				continue
			}
			if index == 0 && len(assignment.Rhs) == 1 && a.hiddenKeyAssignmentKnown(assignment.Rhs[0], owner) {
				continue
			}
		}
		a.state.complete = false
	}
}

func (a *extractPackageAnalysis) hiddenKeyAssignmentKnown(expression ast.Expr, owner string) bool {
	switch owner {
	case "ContractRef":
		return a.contractValueKnown(expression, make(map[types.Object]bool))
	case "ErrorMapping", "FieldLabel":
		return a.integrationKeyValueKnown(expression, owner, make(map[types.Object]bool))
	case "Definition":
		return a.definitionValueKnown(expression, make(map[types.Object]bool))
	case "DefinitionSpec":
		return a.definitionSpecKnown(expression, make(map[types.Object]bool))
	case "Descriptor":
		return a.descriptorValueKnown(expression, make(map[types.Object]bool))
	case "ErrorSpec", "ErrorPlanSpec":
		return a.errorSpecKnown(expression, make(map[types.Object]bool))
	}
	return false
}

func (a *extractPackageAnalysis) inspectKeyFieldAssignments(assignment *ast.AssignStmt) {
	tracked := make([]string, len(assignment.Lhs))
	for index, left := range assignment.Lhs {
		selector, ok := extractUnparenthesized(left).(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Key" {
			tracked[index] = extractTrackedKeyFieldOwner(a.info, selector)
		} else if owner := a.indirectTrackedKeyOwner(left, make(map[types.Object]bool)); owner != "" {
			tracked[index] = owner
			a.state.complete = false
		}
	}
	for index, owner := range tracked {
		if owner == "" {
			continue
		}
		if assignment.Tok != token.ASSIGN || len(assignment.Lhs) != len(assignment.Rhs) {
			a.state.complete = false
			continue
		}
		a.recordKeyExpression(assignment.Rhs[index], owner+".Key")
	}
}

func (a *extractPackageAnalysis) inspectKeyFieldRange(statement *ast.RangeStmt) {
	for _, expression := range []ast.Expr{statement.Key, statement.Value} {
		selector, ok := extractUnparenthesized(expression).(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Key" && extractTrackedKeyFieldOwner(a.info, selector) != "" {
			a.state.complete = false
			continue
		}
		if a.indirectTrackedKeyOwner(expression, make(map[types.Object]bool)) != "" {
			a.state.complete = false
		}
	}
}

func (a *extractPackageAnalysis) trackedKeyAddressOwner(expression ast.Expr) string {
	selector, ok := extractUnparenthesized(expression).(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Key" {
		return ""
	}
	return extractTrackedKeyFieldOwner(a.info, selector)
}

func (a *extractPackageAnalysis) indirectTrackedKeyOwner(expression ast.Expr, seen map[types.Object]bool) string {
	expression = extractUnparenthesized(expression)
	star, ok := expression.(*ast.StarExpr)
	if !ok {
		return ""
	}
	target := extractUnparenthesized(star.X)
	if address, ok := target.(*ast.UnaryExpr); ok && address.Op == token.AND {
		return a.trackedKeyAddressOwner(address.X)
	}
	identifier, ok := target.(*ast.Ident)
	if !ok {
		return ""
	}
	origin, ok := a.followOrigin(a.info.ObjectOf(identifier), seen)
	if !ok {
		return ""
	}
	address, ok := extractUnparenthesized(origin).(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return ""
	}
	return a.trackedKeyAddressOwner(address.X)
}

func extractTrackedKeyFieldOwner(info *types.Info, selector *ast.SelectorExpr) string {
	selection := info.Selections[selector]
	if selection == nil || selection.Kind() != types.FieldVal {
		return ""
	}
	current := selection.Recv()
	indexes := selection.Index()
	for offset, index := range indexes {
		current = types.Unalias(current)
		if pointer, ok := current.(*types.Pointer); ok {
			current = types.Unalias(pointer.Elem())
		}
		structure, ok := current.Underlying().(*types.Struct)
		if !ok || index >= structure.NumFields() {
			return ""
		}
		field := structure.Field(index)
		if offset == len(indexes)-1 {
			if field.Name() != "Key" {
				return ""
			}
			return extractTrackedKeyStructName(current)
		}
		current = field.Type()
	}
	return ""
}

func extractTrackedKeyStructName(value types.Type) string {
	if value == nil {
		return ""
	}
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	if named, ok := value.(*types.Named); ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == i18nPackagePath {
		switch named.Origin().Obj().Name() {
		case "ContractRef", "ErrorMapping", "FieldLabel":
			return named.Origin().Obj().Name()
		}
	}
	structure, ok := value.Underlying().(*types.Struct)
	if !ok {
		return ""
	}
	names := make([]string, structure.NumFields())
	for index := range names {
		field := structure.Field(index)
		if field.Pkg() == nil || field.Pkg().Path() != i18nPackagePath {
			return ""
		}
		names[index] = field.Name()
	}
	switch {
	case slices.Equal(names, []string{"Key", "Revision", "Digest"}):
		return "ContractRef"
	case slices.Equal(names, []string{"Ladder", "Key", "Params", "FieldArgument"}):
		return "ErrorMapping"
	case slices.Equal(names, []string{"Field", "Key"}):
		return "FieldLabel"
	default:
		return ""
	}
}

func extractUnparenthesized(expression ast.Expr) ast.Expr {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = parenthesized.X
	}
}

func (a *extractPackageAnalysis) inspectCall(call *ast.CallExpr) {
	keepsCapabilitiesVisible := a.callKeepsCapabilitiesVisible(call)
	if !keepsCapabilitiesVisible {
		for _, argument := range call.Args {
			if a.usageCapabilityExpression(argument, make(map[types.Object]bool)) || a.hiddenKeyTransferOwner(argument) != "" {
				a.state.complete = false
			}
		}
		if selector, ok := extractUnparenthesized(call.Fun).(*ast.SelectorExpr); ok && a.usageCapabilityExpression(selector.X, make(map[types.Object]bool)) {
			a.state.complete = false
		}
		if a.usageCapabilityExpression(call.Fun, make(map[types.Object]bool)) {
			a.state.complete = false
		}
	}
	for _, argument := range call.Args {
		if a.definitionConstructorCallable(argument) {
			a.state.addFindingAt(argument.Pos(), "escaped i18n definition constructor callable is not statically extractable")
		}
		if a.escapingHiddenKeyOwner(argument) != "" {
			a.state.complete = false
		}
		if extractDefinitionBindCallbackSignature(a.info.TypeOf(argument)) && !a.definitionCallbackKnown(argument, make(map[types.Object]bool)) {
			a.state.complete = false
		}
	}
	if a.inspectTrackedKeyConversion(call) {
		return
	}
	if conversion, _ := a.typeConversion(call.Fun); conversion {
		return
	}
	if keyIndex, ok := a.snapshotBindExpression(call.Fun); ok {
		a.recordKeyArgument(call, keyIndex, "Snapshot.Bind")
		return
	}
	if callable, ok := a.snapshotKeyCallExpression(call.Fun); ok {
		a.recordKeyArgument(call, callable.index, callable.owner)
		return
	}
	if receiver, ok := a.definitionBindReceiver(call); ok {
		if !a.definitionValueKnown(receiver, make(map[types.Object]bool)) {
			a.state.complete = false
		}
		return
	}
	if extractDefinitionBindCallbackSignature(a.info.TypeOf(call.Fun)) {
		if !a.definitionCallbackKnown(call.Fun, make(map[types.Object]bool)) {
			a.state.complete = false
		}
		return
	}
	if extractI18nObject(a.info, call.Fun, "Define") {
		if len(call.Args) != 2 || !a.definitionSpecKnown(call.Args[1], make(map[types.Object]bool)) {
			a.state.complete = false
		}
		return
	}
	if extractI18nObject(a.info, call.Fun, "DefineStruct") {
		if len(call.Args) != 2 || !a.contractValueKnown(call.Args[1], make(map[types.Object]bool)) {
			a.state.complete = false
		}
		return
	}
	if specIndex, ok := a.errorMessagesSpecIndex(call.Fun); ok {
		if specIndex >= len(call.Args) || !a.errorSpecKnown(call.Args[specIndex], make(map[types.Object]bool)) {
			a.state.complete = false
		}
		return
	}
	if called := extractAssignedObject(a.info, call.Fun); called != nil {
		if a.untrustedCallables[called] {
			a.state.complete = false
			return
		}
		if keyIndex, exists := a.bindAliases[called]; exists {
			a.recordKeyArgument(call, keyIndex, "Snapshot.Bind alias")
			return
		}
		if a.definitions[called] {
			a.recordKeyArgument(call, 1, "NewDefinition alias")
			return
		}
		if a.definitionSpecs[called] {
			if len(call.Args) != 2 || !a.definitionSpecKnown(call.Args[1], make(map[types.Object]bool)) {
				a.state.complete = false
			}
			return
		}
		if a.structDefinitions[called] {
			if len(call.Args) != 2 || !a.contractValueKnown(call.Args[1], make(map[types.Object]bool)) {
				a.state.complete = false
			}
			return
		}
		if callable, exists := a.keyAliases[called]; exists {
			a.recordKeyArgument(call, callable.index, callable.owner+" alias")
			return
		}
	}
	if extractDefinitionKeyConstructor(a.info, call.Fun) {
		a.recordKeyArgument(call, 1, "NewDefinition")
	}
}

func (a *extractPackageAnalysis) callKeepsCapabilitiesVisible(call *ast.CallExpr) bool {
	if conversion, safe := a.typeConversion(call.Fun); conversion {
		return safe
	}
	if _, ok := a.snapshotBindExpression(call.Fun); ok {
		return true
	}
	if _, ok := a.snapshotKeyCallExpression(call.Fun); ok {
		return true
	}
	if extractDefinitionBindCallbackSignature(a.info.TypeOf(call.Fun)) {
		return true
	}
	called := extractCalledObject(a.info, call.Fun)
	if called != nil && called.Pkg() != nil && called.Pkg().Path() == i18nPackagePath {
		return true
	}
	if called != nil && (a.definitions[called] || a.definitionSpecs[called] || a.structDefinitions[called]) {
		return true
	}
	function, signature := a.localCall(call.Fun, make(map[types.Object]bool))
	if function == nil && signature == nil {
		return false
	}
	var declared *types.Signature
	if function != nil {
		declared, _ = function.Type().(*types.Signature)
		if declared == nil {
			return false
		}
	}
	if signature == nil {
		signature, _ = types.Unalias(a.info.TypeOf(call.Fun)).(*types.Signature)
	}
	if signature == nil {
		return false
	}
	for index, argument := range call.Args {
		capability := a.usageCapabilityExpression(argument, make(map[types.Object]bool))
		hiddenOwner := a.hiddenKeyTransferOwner(argument)
		if !capability && hiddenOwner == "" {
			continue
		}
		if capability {
			if _, bind := extractSnapshotBindSignature(a.info.TypeOf(argument)); bind {
				if _, visible := a.snapshotBindExpression(argument); !visible {
					return false
				}
			}
		}
		parameter := index
		if signature.Variadic() && parameter >= signature.Params().Len()-1 {
			parameter = signature.Params().Len() - 1
		}
		if parameter < 0 || parameter >= signature.Params().Len() {
			return false
		}
		parameterType := signature.Params().At(parameter).Type()
		if capability && !extractUsageCapabilityType(parameterType, make(map[types.Type]bool)) {
			return false
		}
		if hiddenOwner != "" && extractContainedHiddenKeyTypeName(parameterType, make(map[types.Type]bool)) != hiddenOwner {
			return false
		}
		if declared != nil && declared.TypeParams() != nil && declared.TypeParams().Len() != 0 {
			declaredParameter := index
			if declared.Variadic() && declaredParameter >= declared.Params().Len()-1 {
				declaredParameter = declared.Params().Len() - 1
			}
			if declaredParameter < 0 || declaredParameter >= declared.Params().Len() {
				return false
			}
			declaredType := declared.Params().At(declaredParameter).Type()
			if capability && !extractUsageCapabilityType(declaredType, make(map[types.Type]bool)) {
				return false
			}
			if hiddenOwner != "" && extractContainedHiddenKeyTypeName(declaredType, make(map[types.Type]bool)) != hiddenOwner {
				return false
			}
		}
	}
	return true
}

func (a *extractPackageAnalysis) localCall(expression ast.Expr, seen map[types.Object]bool) (*types.Func, *types.Signature) {
	expression = extractUnparenthesized(expression)
	if literal, ok := expression.(*ast.FuncLit); ok {
		signature, _ := types.Unalias(a.info.TypeOf(literal)).(*types.Signature)
		return nil, signature
	}
	if conversion, ok := expression.(*ast.CallExpr); ok {
		converted, safe := a.typeConversion(conversion.Fun)
		if !converted || !safe || len(conversion.Args) != 1 {
			return nil, nil
		}
		return a.localCall(conversion.Args[0], seen)
	}
	object := extractCalledObject(a.info, expression)
	if object == nil || seen[object] {
		return nil, nil
	}
	if function, ok := object.(*types.Func); ok && a.selectedFunctions[function] {
		declared, _ := function.Type().(*types.Signature)
		if declared == nil {
			return nil, nil
		}
		signature, _ := types.Unalias(a.info.TypeOf(expression)).(*types.Signature)
		return function, signature
	}
	if a.packageVariables[object] || a.addressTaken[object] {
		return nil, nil
	}
	seen[object] = true
	origin, ok := a.followOrigin(object, make(map[types.Object]bool))
	if !ok {
		return nil, nil
	}
	return a.localCall(origin, seen)
}

func (a *extractPackageAnalysis) typeConversion(expression ast.Expr) (bool, bool) {
	typed, ok := a.info.Types[extractUnparenthesized(expression)]
	if !ok || !typed.IsType() {
		return false, false
	}
	object := extractCalledObject(a.info, expression)
	return true, object == nil || object.Pkg() == nil || object.Pkg().Path() != "unsafe"
}

func (a *extractPackageAnalysis) escapingHiddenKeyOwner(expression ast.Expr) string {
	expression = extractUnparenthesized(expression)
	if address, ok := expression.(*ast.UnaryExpr); ok && address.Op == token.AND {
		return extractContainedHiddenKeyTypeName(a.info.TypeOf(address.X), make(map[types.Type]bool))
	}
	value := types.Unalias(a.info.TypeOf(expression))
	switch value := value.(type) {
	case *types.Pointer:
		return extractContainedHiddenKeyTypeName(value.Elem(), make(map[types.Type]bool))
	case *types.Slice:
		return extractContainedHiddenKeyTypeName(value.Elem(), make(map[types.Type]bool))
	case *types.Map:
		if owner := extractContainedHiddenKeyTypeName(value.Key(), make(map[types.Type]bool)); owner != "" {
			return owner
		}
		return extractContainedHiddenKeyTypeName(value.Elem(), make(map[types.Type]bool))
	case *types.Chan:
		return extractContainedHiddenKeyTypeName(value.Elem(), make(map[types.Type]bool))
	}
	return ""
}

func extractContainedHiddenKeyTypeName(value types.Type, seen map[types.Type]bool) string {
	if value == nil {
		return ""
	}
	value = types.Unalias(value)
	if seen[value] {
		return ""
	}
	seen[value] = true
	if owner := extractHiddenKeyTypeName(value); owner != "" {
		return owner
	}
	if named, ok := value.(*types.Named); ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == i18nPackagePath {
		return ""
	}
	switch value := value.(type) {
	case *types.Pointer:
		return extractContainedHiddenKeyTypeName(value.Elem(), seen)
	case *types.Named:
		return extractContainedHiddenKeyTypeName(value.Underlying(), seen)
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			if owner := extractContainedHiddenKeyTypeName(value.Field(index).Type(), seen); owner != "" {
				return owner
			}
		}
	case *types.Array:
		return extractContainedHiddenKeyTypeName(value.Elem(), seen)
	case *types.Slice:
		return extractContainedHiddenKeyTypeName(value.Elem(), seen)
	case *types.Map:
		if owner := extractContainedHiddenKeyTypeName(value.Key(), seen); owner != "" {
			return owner
		}
		return extractContainedHiddenKeyTypeName(value.Elem(), seen)
	case *types.Chan:
		return extractContainedHiddenKeyTypeName(value.Elem(), seen)
	}
	return ""
}

func extractHiddenKeyTypeName(value types.Type) string {
	if owner := extractTrackedKeyStructName(value); owner != "" {
		return owner
	}
	switch name := extractNamedTypeName(value); name {
	case "Definition", "Message", "Descriptor", "DefinitionSpec", "ErrorSpec", "ErrorPlanSpec":
		return name
	}
	return ""
}

func (a *extractPackageAnalysis) definitionConstructorExpression(expression ast.Expr) bool {
	call, ok := extractUnparenthesized(expression).(*ast.CallExpr)
	if !ok {
		return false
	}
	if extractDefinitionConstructor(a.info, call.Fun) {
		return true
	}
	called := extractAssignedObject(a.info, call.Fun)
	return a.definitions[called] || a.definitionSpecs[called] || a.structDefinitions[called]
}

func extractDefinitionConstructor(info *types.Info, expression ast.Expr) bool {
	return extractI18nObject(info, expression, "Define") || extractI18nObject(info, expression, "NewDefinition") ||
		extractI18nObject(info, expression, "DefineStruct") || extractI18nObject(info, expression, "NewStructDefinition")
}

func extractDefinitionKeyConstructor(info *types.Info, expression ast.Expr) bool {
	return extractI18nObject(info, expression, "NewDefinition") || extractI18nObject(info, expression, "NewStructDefinition")
}

func (a *extractPackageAnalysis) definitionConstructorCallable(expression ast.Expr) bool {
	if extractDefinitionConstructor(a.info, expression) {
		return true
	}
	called := extractAssignedObject(a.info, expression)
	return a.definitions[called] || a.definitionSpecs[called] || a.structDefinitions[called]
}

func (a *extractPackageAnalysis) definitionBindReceiver(call *ast.CallExpr) (ast.Expr, bool) {
	receiver, methodExpression, ok := a.definitionBindExpression(call.Fun)
	if !ok {
		return nil, false
	}
	if methodExpression {
		if len(call.Args) == 0 {
			return nil, true
		}
		return call.Args[0], true
	}
	return receiver, true
}

func (a *extractPackageAnalysis) definitionBindExpression(expression ast.Expr) (ast.Expr, bool, bool) {
	selector, ok := extractUnparenthesized(expression).(*ast.SelectorExpr)
	if !ok {
		return nil, false, false
	}
	selection := a.info.Selections[selector]
	if selection == nil || selection.Obj().Name() != "Bind" || extractMethodReceiverName(selection.Obj()) != "Definition" {
		return nil, false, false
	}
	if selection.Kind() == types.MethodExpr {
		return nil, true, true
	}
	return selector.X, false, true
}

func extractDefinitionBindCallbackSignature(value types.Type) bool {
	value = types.Unalias(value)
	if named, ok := value.(*types.Named); ok {
		value = named.Underlying()
	}
	signature, ok := value.(*types.Signature)
	if !ok || signature.Variadic() || signature.Results().Len() != 2 || signature.Params().Len() < 1 || signature.Params().Len() > 2 {
		return false
	}
	if signature.Params().Len() == 2 && extractNamedTypeName(signature.Params().At(0).Type()) != "Definition" {
		return false
	}
	return extractNamedTypeName(signature.Results().At(0).Type()) == "Message" &&
		types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type())
}

func (a *extractPackageAnalysis) definitionCallbackKnown(expression ast.Expr, seen map[types.Object]bool) bool {
	if receiver, methodExpression, ok := a.definitionBindExpression(expression); ok {
		return !methodExpression && a.definitionValueKnown(receiver, seen)
	}
	if conversion, ok := extractUnparenthesized(expression).(*ast.CallExpr); ok {
		converted, safe := a.typeConversion(conversion.Fun)
		if converted && safe && len(conversion.Args) == 1 {
			return a.definitionCallbackKnown(conversion.Args[0], seen)
		}
	}
	identifier, ok := extractUnparenthesized(expression).(*ast.Ident)
	if !ok {
		return false
	}
	origin, ok := a.followOrigin(a.info.ObjectOf(identifier), seen)
	return ok && a.definitionCallbackKnown(origin, seen)
}

func (a *extractPackageAnalysis) unresolvedDefinitionCallback(expression ast.Expr) bool {
	return extractDefinitionBindCallbackSignature(a.info.TypeOf(expression)) &&
		!a.definitionCallbackKnown(expression, make(map[types.Object]bool))
}

func (a *extractPackageAnalysis) unresolvedHiddenKeyValue(expression ast.Expr) bool {
	return a.unresolvedHiddenKeyOwner(expression) != ""
}

func (a *extractPackageAnalysis) unresolvedHiddenKeyOwner(expression ast.Expr) string {
	return a.unresolvedHiddenKeyOwnerFrom(expression, make(map[types.Object]bool))
}

func (a *extractPackageAnalysis) unresolvedHiddenKeyOwnerFrom(expression ast.Expr, seen map[types.Object]bool) string {
	expression = extractUnparenthesized(expression)
	if conversion, ok := expression.(*ast.CallExpr); ok {
		converted, safe := a.typeConversion(conversion.Fun)
		if converted && safe && len(conversion.Args) == 1 {
			return a.unresolvedHiddenKeyOwnerFrom(conversion.Args[0], seen)
		}
	}
	owner := extractHiddenKeyTypeName(a.info.TypeOf(expression))
	if owner == "Message" {
		return ""
	}
	if owner != "" {
		if a.hiddenKeyAssignmentKnown(expression, owner) {
			return ""
		}
		return owner
	}
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return ""
	}
	object := a.info.ObjectOf(identifier)
	if object == nil || seen[object] {
		return ""
	}
	seen[object] = true
	if origin, found := a.followOrigin(object, make(map[types.Object]bool)); found {
		if hidden := a.unresolvedHiddenKeyOwnerFrom(origin, seen); hidden != "" {
			return hidden
		}
	}
	for _, value := range a.capabilityValues[object] {
		if hidden := a.unresolvedHiddenKeyOwnerFrom(value, seen); hidden != "" {
			return hidden
		}
	}
	return ""
}

func (a *extractPackageAnalysis) hiddenKeyTransferOwner(expression ast.Expr) string {
	if owner := a.unresolvedHiddenKeyOwner(expression); owner != "" {
		return owner
	}
	if extractHiddenKeyTypeName(a.info.TypeOf(expression)) != "" {
		return ""
	}
	return extractContainedHiddenKeyTypeName(a.info.TypeOf(expression), make(map[types.Type]bool))
}

func (a *extractPackageAnalysis) definitionValueKnown(expression ast.Expr, seen map[types.Object]bool) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.UnaryExpr:
		if expression.Op != token.AND {
			return false
		}
		if identifier, ok := extractUnparenthesized(expression.X).(*ast.Ident); ok {
			if origin, found := a.followAddressOrigin(a.info.ObjectOf(identifier), seen); found {
				return a.definitionValueKnown(origin, seen)
			}
		}
		return a.definitionValueKnown(expression.X, seen)
	case *ast.StarExpr:
		return a.definitionValueKnown(expression.X, seen)
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		object := a.info.ObjectOf(expression)
		if a.zeroValueObjectKnown(object) {
			return true
		}
		if tuple, ok := a.tupleOrigins[object]; ok && a.singleStableAssignment(object) {
			if a.directFactoryCallKnown(tuple.call, tuple.index, "Definition") {
				return true
			}
			return tuple.index == 0 && a.definitionConstructorExpression(tuple.call)
		}
		origin, ok := a.followOrigin(object, seen)
		if !ok {
			return false
		}
		return a.definitionConstructorExpression(origin) || a.definitionValueKnown(origin, seen)
	case *ast.SelectorExpr:
		selection := a.info.Selections[expression]
		return selection != nil && extractNamedTypeName(selection.Obj().Type()) == "Definition" &&
			a.definitionContainerPathKnown(expression.X, []types.Object{selection.Obj()}, seen, make(map[types.Object]bool))
	case *ast.CallExpr:
		return a.directFactoryCallKnown(expression, 0, "Definition") || a.definitionConstructorExpression(expression)
	case *ast.CompositeLit:
		return extractNamedTypeName(a.info.TypeOf(expression)) == "Definition" && len(expression.Elts) == 0
	}
	return false
}

func (a *extractPackageAnalysis) definitionContainerPathKnown(expression ast.Expr, fields []types.Object, seenObjects, seenFunctions map[types.Object]bool) bool {
	if len(fields) == 0 {
		return a.definitionValueKnown(expression, seenObjects)
	}
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		object := a.info.ObjectOf(expression)
		if object == nil || seenObjects[object] {
			return false
		}
		seenObjects[object] = true
		if a.zeroValueObjectKnown(object) {
			return true
		}
		if origin, ok := a.followAddressOrigin(object, make(map[types.Object]bool)); ok {
			return a.definitionContainerPathKnown(origin, fields, seenObjects, seenFunctions)
		}
		if tuple, ok := a.tupleOrigins[object]; ok && a.singleStableAssignment(object) {
			return a.definitionCallPathKnown(tuple.call, tuple.index, fields, seenObjects, seenFunctions)
		}
		origin, ok := a.followOrigin(object, make(map[types.Object]bool))
		return ok && a.definitionContainerPathKnown(origin, fields, seenObjects, seenFunctions)
	case *ast.UnaryExpr:
		return expression.Op == token.AND && a.definitionContainerPathKnown(expression.X, fields, seenObjects, seenFunctions)
	case *ast.StarExpr:
		return a.definitionContainerPathKnown(expression.X, fields, seenObjects, seenFunctions)
	case *ast.SelectorExpr:
		selection := a.info.Selections[expression]
		if selection == nil || selection.Kind() != types.FieldVal {
			return false
		}
		path := make([]types.Object, 0, len(fields)+1)
		path = append(path, selection.Obj())
		path = append(path, fields...)
		return a.definitionContainerPathKnown(expression.X, path, seenObjects, seenFunctions)
	case *ast.CallExpr:
		return a.definitionCallPathKnown(expression, 0, fields, seenObjects, seenFunctions)
	case *ast.CompositeLit:
		value, found := extractCompositeNamedFieldValue(a.info, expression, fields[0].Name())
		return !found || a.definitionContainerPathKnown(value, fields[1:], seenObjects, seenFunctions)
	}
	return false
}

func (a *extractPackageAnalysis) definitionCallPathKnown(call *ast.CallExpr, resultIndex int, fields []types.Object, seenObjects, seenFunctions map[types.Object]bool) bool {
	key := extractFactoryResult{function: extractCalledObject(a.info, call.Fun), index: resultIndex}
	if len(fields) == 1 {
		value := a.info.TypeOf(call)
		if tuple, ok := value.(*types.Tuple); ok {
			if resultIndex < 0 || resultIndex >= tuple.Len() {
				return false
			}
			value = tuple.At(resultIndex).Type()
		}
		for _, field := range extractDefinitionResultFields(value) {
			if field.Name() == fields[0].Name() {
				return a.definitionFactories[key][field.Name()]
			}
		}
	}
	function, ok := key.function.(*types.Func)
	if !ok || !a.selectedFunctions[function] || seenFunctions[function] {
		return false
	}
	declaration := a.localFunctionDeclaration(function)
	signature, _ := function.Type().(*types.Signature)
	if declaration == nil || signature == nil || resultIndex < 0 || resultIndex >= signature.Results().Len() {
		return false
	}
	seenFunctions[function] = true
	defer delete(seenFunctions, function)
	returns := extractFunctionReturns(declaration.Body)
	if len(returns) == 0 {
		return false
	}
	for _, statement := range returns {
		result, found := a.definitionReturnExpression(statement, signature, resultIndex)
		if !found || !a.definitionReturnPathKnown(result, fields, cloneObjectSet(seenObjects), seenFunctions) {
			return false
		}
	}
	return true
}

func (a *extractPackageAnalysis) definitionReturnPathKnown(expression ast.Expr, fields []types.Object, seenObjects, seenFunctions map[types.Object]bool) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name == "nil"
	case *ast.UnaryExpr:
		return expression.Op == token.AND && a.definitionReturnPathKnown(expression.X, fields, seenObjects, seenFunctions)
	case *ast.StarExpr:
		return a.definitionReturnPathKnown(expression.X, fields, seenObjects, seenFunctions)
	case *ast.CallExpr, *ast.CompositeLit:
		return a.definitionContainerPathKnown(expression, fields, seenObjects, seenFunctions)
	}
	return false
}

func (a *extractPackageAnalysis) localFunctionDeclaration(object types.Object) *ast.FuncDecl {
	for _, file := range a.files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && a.info.Defs[function.Name] == object {
				return function
			}
		}
	}
	return nil
}

func (a *extractPackageAnalysis) zeroValueObjectKnown(object types.Object) bool {
	if object == nil || a.packageVariables[object] || a.parameters[object] || a.resultVariables[object] || a.addressTaken[object] {
		return false
	}
	_, variable := object.(*types.Var)
	_, assigned := a.assignments[object]
	_, originated := a.origins[object]
	return variable && !assigned && !originated
}

func (a *extractPackageAnalysis) definitionContainerFieldKnown(expression ast.Expr, field types.Object, seen map[types.Object]bool) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		object := a.info.ObjectOf(expression)
		if tuple, ok := a.tupleOrigins[object]; ok && a.singleStableAssignment(object) {
			key := extractFactoryResult{function: extractCalledObject(a.info, tuple.call.Fun), index: tuple.index}
			return a.definitionFactories[key][field.Name()]
		}
		origin, ok := a.followOrigin(object, seen)
		return ok && a.definitionContainerFieldKnown(origin, field, seen)
	case *ast.CallExpr:
		key := extractFactoryResult{function: extractCalledObject(a.info, expression.Fun), index: 0}
		return a.definitionFactories[key][field.Name()]
	case *ast.UnaryExpr:
		return expression.Op == token.AND && a.definitionContainerFieldKnown(expression.X, field, seen)
	case *ast.StarExpr:
		return a.definitionContainerFieldKnown(expression.X, field, seen)
	case *ast.CompositeLit:
		value, found := extractCompositeFieldValue(a.info, expression, field)
		return !found || a.definitionValueKnown(value, seen)
	}
	return false
}

func (a *extractPackageAnalysis) definitionSpecKnown(expression ast.Expr, seen map[types.Object]bool) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.UnaryExpr:
		if expression.Op != token.AND {
			return false
		}
		if identifier, ok := extractUnparenthesized(expression.X).(*ast.Ident); ok {
			if origin, found := a.followAddressOrigin(a.info.ObjectOf(identifier), seen); found {
				return a.definitionSpecKnown(origin, seen)
			}
		}
		return a.definitionSpecKnown(expression.X, seen)
	case *ast.StarExpr:
		return a.definitionSpecKnown(expression.X, seen)
	case *ast.CompositeLit:
		contract := extractCompositeFieldExpression(expression, "Contract", 0)
		return contract == nil || a.contractValueKnown(contract, seen)
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		object := a.info.ObjectOf(expression)
		if tuple, ok := a.tupleOrigins[object]; ok && a.singleStableAssignment(object) {
			return a.directFactoryCallKnown(tuple.call, tuple.index, "DefinitionSpec")
		}
		origin, ok := a.followOrigin(object, seen)
		return ok && a.definitionSpecKnown(origin, seen)
	case *ast.CallExpr:
		return a.directFactoryCallKnown(expression, 0, "DefinitionSpec")
	}
	return false
}

func (a *extractPackageAnalysis) contractValueKnown(expression ast.Expr, seen map[types.Object]bool) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.UnaryExpr:
		if expression.Op != token.AND {
			return false
		}
		if identifier, ok := extractUnparenthesized(expression.X).(*ast.Ident); ok {
			if origin, found := a.followAddressOrigin(a.info.ObjectOf(identifier), seen); found {
				return a.contractValueKnown(origin, seen)
			}
		}
		return a.contractValueKnown(expression.X, seen)
	case *ast.StarExpr:
		return a.contractValueKnown(expression.X, seen)
	case *ast.CompositeLit:
		key := extractCompositeKeyExpression(expression, "ContractRef")
		return key == nil || a.keyExpressionBounded(key)
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		object := a.info.ObjectOf(expression)
		if tuple, ok := a.tupleOrigins[object]; ok && a.singleStableAssignment(object) {
			key := extractFactoryResult{function: extractCalledObject(a.info, tuple.call.Fun), index: tuple.index}
			if a.contractFactories[key] {
				return true
			}
			if tuple.index == 0 {
				return a.contractValueKnown(tuple.call, seen)
			}
			return false
		}
		origin, ok := a.followOrigin(object, seen)
		if !ok {
			return false
		}
		if literal, literalOK := extractUnparenthesized(origin).(*ast.CompositeLit); literalOK && extractCompositeKeyExpression(literal, "ContractRef") == nil && a.trackedKeyWritesKnown(object, "ContractRef") {
			return true
		}
		return a.contractValueKnown(origin, seen)
	case *ast.CallExpr:
		typed, typedOK := a.info.Types[extractUnparenthesized(expression.Fun)]
		if typedOK && typed.IsType() && extractTrackedKeyStructName(a.info.TypeOf(expression)) == "ContractRef" {
			converted, found := a.convertedKeyExpression(expression, "ContractRef", make(map[types.Object]bool))
			return found && converted != nil && a.keyExpressionBounded(converted)
		}
		if callable, ok := a.snapshotKeyCallExpression(expression.Fun); ok && callable.owner == "Snapshot.ContractRef" {
			return callable.index < len(expression.Args) && a.keyExpressionBounded(expression.Args[callable.index])
		}
		if a.contractFactories[extractFactoryResult{function: extractCalledObject(a.info, expression.Fun), index: 0}] {
			return true
		}
		selector, ok := extractUnparenthesized(expression.Fun).(*ast.SelectorExpr)
		if !ok || len(expression.Args) != 0 {
			return false
		}
		selection := a.info.Selections[selector]
		return selection != nil && selection.Obj().Name() == "ContractRef" && extractMethodReceiverName(selection.Obj()) == "Descriptor" && a.descriptorValueKnown(selector.X, seen)
	}
	return false
}

func (a *extractPackageAnalysis) descriptorValueKnown(expression ast.Expr, seen map[types.Object]bool) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.UnaryExpr:
		if expression.Op != token.AND {
			return false
		}
		if identifier, ok := extractUnparenthesized(expression.X).(*ast.Ident); ok {
			if origin, found := a.followAddressOrigin(a.info.ObjectOf(identifier), seen); found {
				return a.descriptorValueKnown(origin, seen)
			}
		}
		return a.descriptorValueKnown(expression.X, seen)
	case *ast.StarExpr:
		return a.descriptorValueKnown(expression.X, seen)
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		object := a.info.ObjectOf(expression)
		if tuple, ok := a.tupleOrigins[object]; ok && a.singleStableAssignment(object) {
			return a.directFactoryCallKnown(tuple.call, tuple.index, "Descriptor") || tuple.index == 0 && a.descriptorCallKnown(tuple.call)
		}
		origin, ok := a.followOrigin(object, seen)
		return ok && a.descriptorValueKnown(origin, seen)
	case *ast.CallExpr:
		return a.directFactoryCallKnown(expression, 0, "Descriptor") || a.descriptorCallKnown(expression)
	case *ast.CompositeLit:
		key := extractCompositeKeyExpression(expression, "Descriptor")
		return key == nil || a.keyExpressionBounded(key)
	}
	return false
}

func (a *extractPackageAnalysis) descriptorCallKnown(call *ast.CallExpr) bool {
	callable, ok := a.snapshotKeyCallExpression(call.Fun)
	return ok && callable.owner == "Snapshot.Descriptor" && callable.index < len(call.Args) && a.keyExpressionBounded(call.Args[callable.index])
}

func (a *extractPackageAnalysis) errorMessagesSpecIndex(expression ast.Expr) (int, bool) {
	selector, ok := extractUnparenthesized(expression).(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	selection := a.info.Selections[selector]
	if selection == nil || extractMethodReceiverName(selection.Obj()) != "Snapshot" || (selection.Obj().Name() != "ErrorMessages" && selection.Obj().Name() != "ErrorPlan") {
		return 0, false
	}
	if selection.Kind() == types.MethodExpr {
		return 1, true
	}
	return 0, true
}

func (a *extractPackageAnalysis) errorSpecKnown(expression ast.Expr, seen map[types.Object]bool) bool {
	expression = extractUnparenthesized(expression)
	kind := extractNamedTypeName(a.info.TypeOf(expression))
	switch expression := expression.(type) {
	case *ast.UnaryExpr:
		if expression.Op != token.AND {
			return false
		}
		if identifier, ok := extractUnparenthesized(expression.X).(*ast.Ident); ok {
			if origin, found := a.followAddressOrigin(a.info.ObjectOf(identifier), seen); found {
				return a.errorSpecKnown(origin, seen)
			}
		}
		return a.errorSpecKnown(expression.X, seen)
	case *ast.StarExpr:
		return a.errorSpecKnown(expression.X, seen)
	case *ast.CompositeLit:
		mappings := extractCompositeFieldExpression(expression, "Mappings", 0)
		labels := extractCompositeFieldExpression(expression, "FieldLabels", 1)
		return a.integrationSliceKnown(mappings, "ErrorMapping", seen) && a.integrationSliceKnown(labels, "FieldLabel", seen)
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		object := a.info.ObjectOf(expression)
		if tuple, ok := a.tupleOrigins[object]; ok && a.singleStableAssignment(object) {
			return a.directFactoryCallKnown(tuple.call, tuple.index, kind)
		}
		origin, ok := a.followOrigin(object, seen)
		return ok && a.errorSpecKnown(origin, seen)
	case *ast.CallExpr:
		return a.directFactoryCallKnown(expression, 0, kind)
	}
	return false
}

func (a *extractPackageAnalysis) integrationSliceKnown(expression ast.Expr, owner string, seen map[types.Object]bool) bool {
	if expression == nil {
		return true
	}
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.CompositeLit:
		for _, element := range expression.Elts {
			if pair, ok := element.(*ast.KeyValueExpr); ok {
				element = pair.Value
			}
			if !a.integrationKeyValueKnown(element, owner, seen) {
				return false
			}
		}
		return true
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		origin, ok := a.followOrigin(a.info.ObjectOf(expression), seen)
		return ok && a.integrationSliceKnown(origin, owner, seen)
	}
	return false
}

func (a *extractPackageAnalysis) integrationKeyValueKnown(expression ast.Expr, owner string, seen map[types.Object]bool) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.UnaryExpr:
		if expression.Op != token.AND {
			return false
		}
		if identifier, ok := extractUnparenthesized(expression.X).(*ast.Ident); ok {
			if origin, found := a.followAddressOrigin(a.info.ObjectOf(identifier), seen); found {
				return a.integrationKeyValueKnown(origin, owner, seen)
			}
		}
		return a.integrationKeyValueKnown(expression.X, owner, seen)
	case *ast.StarExpr:
		return a.integrationKeyValueKnown(expression.X, owner, seen)
	case *ast.CompositeLit:
		key := extractCompositeKeyExpression(expression, owner)
		return key == nil || a.keyExpressionBounded(key)
	case *ast.Ident:
		object := a.info.ObjectOf(expression)
		if tuple, ok := a.tupleOrigins[object]; ok && a.singleStableAssignment(object) {
			return a.directFactoryCallKnown(tuple.call, tuple.index, owner)
		}
		origin, ok := a.followOrigin(object, seen)
		if !ok {
			return false
		}
		if literal, literalOK := extractUnparenthesized(origin).(*ast.CompositeLit); literalOK && extractCompositeKeyExpression(literal, owner) == nil && a.trackedKeyWritesKnown(object, owner) {
			return true
		}
		return a.integrationKeyValueKnown(origin, owner, seen)
	case *ast.CallExpr:
		if a.directFactoryCallKnown(expression, 0, owner) {
			return true
		}
		converted, found := a.convertedKeyExpression(expression, owner, make(map[types.Object]bool))
		return found && converted != nil && a.keyExpressionBounded(converted)
	}
	return false
}

func (a *extractPackageAnalysis) keyExpressionBounded(expression ast.Expr) bool {
	resolver := extractResolver{analysis: a, seen: make(map[types.Object]bool), budget: 100000}
	_, err := resolver.key(expression)
	return err == nil
}

func (a *extractPackageAnalysis) trackedKeyWritesKnown(object types.Object, owner string) bool {
	writes, ok := a.trackedKeyWrites[object]
	if !ok || writes.owner != owner || writes.unresolved || len(writes.expressions) == 0 {
		return false
	}
	for _, expression := range writes.expressions {
		if !a.keyExpressionBounded(expression) {
			return false
		}
	}
	return true
}

func (a *extractPackageAnalysis) followOrigin(object types.Object, seen map[types.Object]bool) (ast.Expr, bool) {
	if object == nil || seen[object] || a.packageVariables[object] || a.addressTaken[object] {
		return nil, false
	}
	if assignment, ok := a.assignments[object]; ok && assignment.count != 1 {
		return nil, false
	}
	origin, ok := a.origins[object]
	if !ok || origin.count != 1 || origin.expression == nil {
		return nil, false
	}
	seen[object] = true
	return origin.expression, true
}

func (a *extractPackageAnalysis) singleStableAssignment(object types.Object) bool {
	assignment, ok := a.assignments[object]
	return ok && assignment.count == 1 && !a.packageVariables[object] && !a.addressTaken[object]
}

func (a *extractPackageAnalysis) followAddressOrigin(object types.Object, seen map[types.Object]bool) (ast.Expr, bool) {
	if object == nil || seen[object] || a.packageVariables[object] || !a.addressTaken[object] {
		return nil, false
	}
	assignment, assigned := a.assignments[object]
	origin, found := a.origins[object]
	stable := assigned && assignment.count == 2 && assignment.expression != nil
	if tuple, ok := a.tupleOrigins[object]; ok && assigned && assignment.count == 1 && assignment.expression == nil {
		stable = tuple.call == extractUnparenthesized(origin.expression)
	}
	if !stable || !found || origin.count != 1 || origin.expression == nil {
		return nil, false
	}
	seen[object] = true
	return origin.expression, true
}

func extractCalledObject(info *types.Info, expression ast.Expr) types.Object {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.Ident:
		return info.ObjectOf(expression)
	case *ast.SelectorExpr:
		return info.ObjectOf(expression.Sel)
	case *ast.IndexExpr:
		return extractCalledObject(info, expression.X)
	case *ast.IndexListExpr:
		return extractCalledObject(info, expression.X)
	}
	return nil
}

func (a *extractPackageAnalysis) inspectTrackedKeyConversion(call *ast.CallExpr) bool {
	typed, ok := a.info.Types[extractUnparenthesized(call.Fun)]
	owner := extractTrackedKeyStructName(a.info.TypeOf(call))
	if !ok || !typed.IsType() {
		owner = ""
	}
	if owner == "" {
		return false
	}
	if len(call.Args) != 1 {
		a.state.complete = false
		return true
	}
	expression, found := a.convertedKeyExpression(call.Args[0], owner, make(map[types.Object]bool))
	if !found {
		a.state.complete = false
		return true
	}
	if expression != nil {
		a.recordKeyExpression(expression, owner+".Key conversion")
	}
	return true
}

func extractI18nTypeName(info *types.Info, expression ast.Expr, name string) bool {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.IndexExpr:
		return extractI18nTypeName(info, expression.X, name)
	case *ast.IndexListExpr:
		return extractI18nTypeName(info, expression.X, name)
	}
	var object types.Object
	switch expression := expression.(type) {
	case *ast.Ident:
		object = info.ObjectOf(expression)
	case *ast.SelectorExpr:
		object = info.ObjectOf(expression.Sel)
	}
	_, typeName := object.(*types.TypeName)
	return typeName && extractObjectIs(object, name)
}

func (a *extractPackageAnalysis) convertedKeyExpression(expression ast.Expr, owner string, seen map[types.Object]bool) (ast.Expr, bool) {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.CompositeLit:
		return extractCompositeKeyExpression(expression, owner), true
	case *ast.Ident:
		object := a.info.ObjectOf(expression)
		if object == nil || seen[object] {
			return nil, false
		}
		assignment, ok := a.assignments[object]
		if !ok || assignment.count != 1 || assignment.expression == nil {
			return nil, false
		}
		seen[object] = true
		return a.convertedKeyExpression(assignment.expression, owner, seen)
	case *ast.CallExpr:
		if len(expression.Args) == 1 {
			return a.convertedKeyExpression(expression.Args[0], owner, seen)
		}
	}
	return nil, false
}

func (a *extractPackageAnalysis) isSnapshotKeyCallExpression(expression ast.Expr) bool {
	_, ok := a.snapshotKeyCallExpression(expression)
	return ok
}

func (a *extractPackageAnalysis) snapshotKeyCallExpression(expression ast.Expr) (extractKeyCallable, bool) {
	value := types.Unalias(a.info.TypeOf(extractUnparenthesized(expression)))
	if named, ok := value.(*types.Named); ok {
		value = named.Underlying()
	}
	signature, ok := value.(*types.Signature)
	if !ok || signature.Variadic() || signature.Params().Len() < 1 || signature.Params().Len() > 2 || signature.Results().Len() != 2 {
		return extractKeyCallable{}, false
	}
	index := signature.Params().Len() - 1
	if index == 1 && extractNamedTypeName(signature.Params().At(0).Type()) != "Snapshot" {
		return extractKeyCallable{}, false
	}
	if extractNamedTypeName(signature.Params().At(index).Type()) != "Key" || !types.Identical(signature.Results().At(1).Type(), types.Typ[types.Bool]) {
		return extractKeyCallable{}, false
	}
	owner := "Snapshot.SourceDigest"
	switch extractNamedTypeName(signature.Results().At(0).Type()) {
	case "ContractRef":
		owner = "Snapshot.ContractRef"
	case "Descriptor":
		owner = "Snapshot.Descriptor"
	default:
		if !types.Identical(types.Unalias(signature.Results().At(0).Type()), types.Typ[types.String]) {
			return extractKeyCallable{}, false
		}
	}
	return extractKeyCallable{index: index, owner: owner}, true
}

func (a *extractPackageAnalysis) snapshotBindExpression(expression ast.Expr) (int, bool) {
	return a.snapshotBindExpressionSeen(expression, make(map[types.Object]bool))
}

func (a *extractPackageAnalysis) snapshotBindExpressionSeen(expression ast.Expr, seen map[types.Object]bool) (int, bool) {
	expression = extractUnparenthesized(expression)
	if conversion, ok := expression.(*ast.CallExpr); ok {
		converted, safe := a.typeConversion(conversion.Fun)
		if !converted || !safe || len(conversion.Args) != 1 {
			return 0, false
		}
		return a.snapshotBindExpressionSeen(conversion.Args[0], seen)
	}
	if selector, ok := expression.(*ast.SelectorExpr); ok {
		selection := a.info.Selections[selector]
		if selection != nil && extractSnapshotBindMethod(selection.Obj()) {
			if selection.Kind() == types.MethodExpr {
				return 1, true
			}
			return 0, true
		}
	}
	if literal, ok := expression.(*ast.FuncLit); ok {
		return extractSnapshotBindSignature(a.info.TypeOf(literal))
	}
	object := extractCalledObject(a.info, expression)
	if object == nil || seen[object] {
		return 0, false
	}
	seen[object] = true
	if function, ok := object.(*types.Func); ok && a.selectedFunctions[function] {
		declared, _ := function.Type().(*types.Signature)
		if declared == nil || declared.TypeParams() != nil && declared.TypeParams().Len() != 0 {
			return 0, false
		}
		return extractSnapshotBindSignature(a.info.TypeOf(expression))
	}
	if a.parameters[object] {
		return extractSnapshotBindSignature(a.info.TypeOf(expression))
	}
	if a.packageVariables[object] || a.addressTaken[object] {
		return 0, false
	}
	origin, ok := a.followOrigin(object, make(map[types.Object]bool))
	if !ok {
		return 0, false
	}
	return a.snapshotBindExpressionSeen(origin, seen)
}

func extractSnapshotBindMethod(object types.Object) bool {
	if extractObjectIs(object, "Bind") && extractMethodReceiverName(object) == "Snapshot" {
		return true
	}
	function, ok := object.(*types.Func)
	if !ok || function.Name() != "Bind" {
		return false
	}
	_, matches := extractSnapshotBindSignature(function.Type())
	return matches
}

func extractSnapshotBindSignature(value types.Type) (int, bool) {
	value = types.Unalias(value)
	if named, ok := value.(*types.Named); ok {
		value = named.Underlying()
	}
	signature, ok := value.(*types.Signature)
	if !ok || !signature.Variadic() || signature.Results().Len() != 2 {
		return 0, false
	}
	keyIndex := 0
	if signature.Params().Len() == 3 {
		keyIndex = 1
	} else if signature.Params().Len() != 2 {
		return 0, false
	}
	arguments, ok := types.Unalias(signature.Params().At(keyIndex + 1).Type()).(*types.Slice)
	if !ok {
		return 0, false
	}
	return keyIndex, extractNamedTypeName(signature.Params().At(keyIndex).Type()) == "Key" &&
		extractNamedTypeName(arguments.Elem()) == "Argument" &&
		extractNamedTypeName(signature.Results().At(0).Type()) == "Message" &&
		types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type())
}

func extractMethodReceiverName(object types.Object) string {
	function, ok := object.(*types.Func)
	if !ok {
		return ""
	}
	signature, ok := function.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return ""
	}
	return extractNamedTypeName(signature.Recv().Type())
}

func (a *extractPackageAnalysis) recordKeyArgument(call *ast.CallExpr, index int, owner string) {
	if index >= len(call.Args) {
		a.state.addFindingAt(call.Pos(), owner+" has no message key argument")
		return
	}
	resolver := extractResolver{analysis: a, seen: make(map[types.Object]bool), budget: 100000}
	result, err := resolver.key(call.Args[index])
	if err != nil {
		a.state.addFindingAt(call.Args[index].Pos(), owner+": "+err.Error())
		return
	}
	if result.dynamic {
		a.state.recordUsageAt(call.Pos(), result)
		return
	}
	if result.covered {
		return
	}
	a.state.recordUsageAt(call.Pos(), result)
}

func (a *extractPackageAnalysis) inspectComposite(literal *ast.CompositeLit) {
	name := extractTrackedKeyStructName(a.info.TypeOf(literal))
	if name == "" {
		return
	}
	expression := extractCompositeKeyExpression(literal, name)
	if expression == nil {
		return
	}
	a.recordKeyExpression(expression, name+".Key")
}

func extractCompositeKeyExpression(literal *ast.CompositeLit, owner string) ast.Expr {
	index := 1
	if owner == "ContractRef" {
		index = 0
	}
	return extractCompositeFieldExpression(literal, "Key", index)
}

func extractCompositeFieldExpression(literal *ast.CompositeLit, name string, index int) ast.Expr {
	for elementIndex, element := range literal.Elts {
		if pair, ok := element.(*ast.KeyValueExpr); ok {
			identifier, ok := pair.Key.(*ast.Ident)
			if ok && identifier.Name == name {
				return pair.Value
			}
			continue
		}
		if elementIndex == index {
			return element
		}
	}
	return nil
}

func (a *extractPackageAnalysis) recordKeyExpression(expression ast.Expr, owner string) {
	resolver := extractResolver{analysis: a, seen: make(map[types.Object]bool), budget: 100000}
	result, err := resolver.key(expression)
	if err != nil {
		a.state.addFindingAt(expression.Pos(), owner+": "+err.Error())
		return
	}
	if result.dynamic {
		a.state.recordUsageAt(expression.Pos(), result)
		return
	}
	if result.covered {
		return
	}
	a.state.recordUsageAt(expression.Pos(), result)
}

func (r *extractResolver) key(expression ast.Expr) (extractKeyResult, error) {
	if !r.consume() {
		return extractKeyResult{}, fmt.Errorf("key expression exceeds analysis budget")
	}
	if value, ok := r.staticString(expression); ok {
		return extractKeyResult{exact: value}, nil
	}
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		return r.key(expression.X)
	case *ast.Ident:
		object := r.analysis.info.ObjectOf(expression)
		assignment, ok := r.follow(object)
		if !ok {
			return extractKeyResult{}, fmt.Errorf("dynamic key is not bounded to a static i18n domain")
		}
		defer r.leave(object)
		return r.key(assignment)
	case *ast.SelectorExpr:
		if result, ok := r.keyField(expression); ok {
			return result, nil
		}
	case *ast.CallExpr:
		if result, ok := r.keyAccessor(expression); ok {
			return result, nil
		}
		if extractI18nObject(r.analysis.info, expression.Fun, "Key") {
			if len(expression.Args) != 1 {
				return extractKeyResult{}, fmt.Errorf("i18n.Key requires one argument")
			}
			return r.key(expression.Args[0])
		}
		if r.isQualifier(expression.Fun) {
			if len(expression.Args) != 2 {
				return extractKeyResult{}, fmt.Errorf("i18n.Qualify requires two arguments")
			}
			domain, ok := r.staticString(expression.Args[0])
			if !ok || domain == "" {
				return extractKeyResult{}, fmt.Errorf("i18n.Qualify domain is not statically known")
			}
			if id, exact := r.staticString(expression.Args[1]); exact {
				return extractKeyResult{exact: string(i18n.Qualify(domain, id))}, nil
			}
			return extractKeyResult{domain: domain, prefix: r.prefix(expression.Args[1]), dynamic: true}, nil
		}
	}
	return extractKeyResult{}, fmt.Errorf("dynamic key is not bounded to a static i18n domain")
}

func (r *extractResolver) keyField(selector *ast.SelectorExpr) (extractKeyResult, bool) {
	selection := r.analysis.info.Selections[selector]
	if selection == nil || selection.Kind() != types.FieldVal || selection.Obj().Name() != "Key" {
		return extractKeyResult{}, false
	}
	owner := extractNamedTypeName(selection.Recv())
	if owner == "" {
		owner = extractTrackedKeyStructName(selection.Recv())
	}
	switch owner {
	case "ContractRef", "Descriptor", "ErrorMapping", "FieldLabel":
		result, err := r.keyContainer(selector.X, owner)
		return result, err == nil
	default:
		return extractKeyResult{}, false
	}
}

func (r *extractResolver) keyContainer(expression ast.Expr, owner string) (extractKeyResult, error) {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.Ident:
		object := r.analysis.info.ObjectOf(expression)
		origin, ok := r.followValue(object)
		if !ok {
			return extractKeyResult{}, fmt.Errorf("key container is not statically known")
		}
		defer r.leave(object)
		if literal, literalOK := extractUnparenthesized(origin).(*ast.CompositeLit); literalOK {
			key := extractContainerKeyExpression(literal, owner)
			if key == nil {
				if writes := r.analysis.trackedKeyWrites[object]; writes.owner == owner && !writes.unresolved && len(writes.expressions) != 0 {
					return r.key(writes.expressions[0])
				}
			}
		}
		return r.keyContainer(origin, owner)
	case *ast.CompositeLit:
		key := extractContainerKeyExpression(expression, owner)
		if key == nil {
			return extractKeyResult{}, fmt.Errorf("key container has no bounded key")
		}
		return r.key(key)
	case *ast.CallExpr:
		if callable, ok := r.analysis.snapshotKeyCallExpression(expression.Fun); ok && callable.index < len(expression.Args) {
			if callable.owner == "Snapshot."+owner {
				return r.key(expression.Args[callable.index])
			}
		}
		if owner == "ContractRef" {
			selector, ok := extractUnparenthesized(expression.Fun).(*ast.SelectorExpr)
			if ok && len(expression.Args) == 0 {
				selection := r.analysis.info.Selections[selector]
				if selection != nil && selection.Obj().Name() == "ContractRef" && extractMethodReceiverName(selection.Obj()) == "Descriptor" {
					return r.keyContainer(selector.X, "Descriptor")
				}
			}
		}
		converted, found := r.analysis.convertedKeyExpression(expression, owner, make(map[types.Object]bool))
		if found && converted != nil {
			return r.key(converted)
		}
	}
	return extractKeyResult{}, fmt.Errorf("key container is not statically known")
}

func extractContainerKeyExpression(literal *ast.CompositeLit, owner string) ast.Expr {
	index := 1
	if owner == "ContractRef" || owner == "Descriptor" {
		index = 0
	}
	return extractCompositeFieldExpression(literal, "Key", index)
}

func (r *extractResolver) keyAccessor(call *ast.CallExpr) (extractKeyResult, bool) {
	selector, ok := extractUnparenthesized(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return extractKeyResult{}, false
	}
	selection := r.analysis.info.Selections[selector]
	if selection == nil || selection.Obj().Name() != "Key" {
		return extractKeyResult{}, false
	}
	receiver := selector.X
	if selection.Kind() == types.MethodExpr {
		if len(call.Args) != 1 {
			return extractKeyResult{}, false
		}
		receiver = call.Args[0]
	} else if len(call.Args) != 0 {
		return extractKeyResult{}, false
	}
	switch extractMethodReceiverName(selection.Obj()) {
	case "Definition":
		if r.analysis.definitionValueKnown(receiver, make(map[types.Object]bool)) {
			return extractKeyResult{covered: true}, true
		}
	case "Message":
		if key, found := r.messageKeyExpression(receiver, make(map[types.Object]bool)); found {
			result, err := r.key(key)
			return result, err == nil
		}
	}
	return extractKeyResult{}, false
}

func (r *extractResolver) messageKeyExpression(expression ast.Expr, seen map[types.Object]bool) (ast.Expr, bool) {
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.Ident:
		origin, ok := r.analysis.followOrigin(r.analysis.info.ObjectOf(expression), seen)
		if !ok {
			return nil, false
		}
		return r.messageKeyExpression(origin, seen)
	case *ast.CallExpr:
		index, ok := r.analysis.snapshotBindExpression(expression.Fun)
		return extractCallArgument(expression, index, ok)
	}
	return nil, false
}

func extractCallArgument(call *ast.CallExpr, index int, valid bool) (ast.Expr, bool) {
	if !valid || index >= len(call.Args) {
		return nil, false
	}
	return call.Args[index], true
}

func (r *extractResolver) followValue(object types.Object) (ast.Expr, bool) {
	if object == nil || r.seen[object] {
		return nil, false
	}
	assignment, ok := r.analysis.assignments[object]
	if !ok || assignment.count != 1 || assignment.expression == nil {
		assignment, ok = r.analysis.origins[object]
	}
	if !ok || assignment.count != 1 || assignment.expression == nil {
		return nil, false
	}
	r.seen[object] = true
	return assignment.expression, true
}

func (r *extractResolver) staticString(expression ast.Expr) (string, bool) {
	if !r.consume() {
		return "", false
	}
	if typed, ok := r.analysis.info.Types[expression]; ok && typed.Value != nil && typed.Value.Kind() == constant.String {
		return constant.StringVal(typed.Value), true
	}
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		return r.staticString(expression.X)
	case *ast.BinaryExpr:
		if expression.Op != token.ADD {
			return "", false
		}
		left, leftOK := r.staticString(expression.X)
		right, rightOK := r.staticString(expression.Y)
		return left + right, leftOK && rightOK
	case *ast.Ident:
		object := r.analysis.info.ObjectOf(expression)
		assignment, ok := r.follow(object)
		if !ok {
			return "", false
		}
		defer r.leave(object)
		return r.staticString(assignment)
	}
	return "", false
}

func (r *extractResolver) prefix(expression ast.Expr) string {
	if value, ok := r.staticString(expression); ok {
		return value
	}
	if !r.consume() {
		return ""
	}
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		return r.prefix(expression.X)
	case *ast.BinaryExpr:
		if expression.Op != token.ADD {
			return ""
		}
		if left, exact := r.staticString(expression.X); exact {
			return left + r.prefix(expression.Y)
		}
		return r.prefix(expression.X)
	case *ast.Ident:
		object := r.analysis.info.ObjectOf(expression)
		assignment, ok := r.follow(object)
		if !ok {
			return ""
		}
		defer r.leave(object)
		return r.prefix(assignment)
	default:
		return ""
	}
}

func (r *extractResolver) isQualifier(expression ast.Expr) bool {
	if extractI18nObject(r.analysis.info, expression, "Qualify") {
		return true
	}
	return r.analysis.qualifiers[extractAssignedObject(r.analysis.info, expression)]
}

func (r *extractResolver) follow(object types.Object) (ast.Expr, bool) {
	if object == nil || r.seen[object] {
		return nil, false
	}
	assignment, ok := r.analysis.assignments[object]
	if !ok || assignment.count != 1 || assignment.expression == nil {
		return nil, false
	}
	r.seen[object] = true
	return assignment.expression, true
}

func (r *extractResolver) leave(object types.Object) {
	delete(r.seen, object)
}

func (r *extractResolver) consume() bool {
	if r.budget == 0 {
		return false
	}
	r.budget--
	return true
}

func extractI18nObject(info *types.Info, expression ast.Expr, name string) bool {
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		return extractI18nObject(info, expression.X, name)
	case *ast.IndexExpr:
		return extractI18nObject(info, expression.X, name)
	case *ast.IndexListExpr:
		return extractI18nObject(info, expression.X, name)
	}
	var object types.Object
	switch expression := expression.(type) {
	case *ast.Ident:
		object = info.ObjectOf(expression)
	case *ast.SelectorExpr:
		object = info.ObjectOf(expression.Sel)
	}
	return extractObjectIs(object, name)
}

func extractAssignedObject(info *types.Info, expression ast.Expr) types.Object {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return nil
	}
	return info.ObjectOf(identifier)
}

func extractObjectIs(object types.Object, name string) bool {
	return object != nil && object.Name() == name && object.Pkg() != nil && object.Pkg().Path() == i18nPackagePath
}

func extractNamedTypeName(value types.Type) string {
	if value == nil {
		return ""
	}
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	named, ok := value.(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != i18nPackagePath {
		return ""
	}
	return named.Origin().Obj().Name()
}

func extractNamedTypeObject(value types.Type) *types.TypeName {
	if value == nil {
		return nil
	}
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	named, _ := value.(*types.Named)
	if named == nil {
		return nil
	}
	return named.Origin().Obj()
}

func extractUsageCapabilityType(value types.Type, seen map[types.Type]bool) bool {
	if value == nil {
		return false
	}
	value = types.Unalias(value)
	if seen[value] {
		return false
	}
	seen[value] = true
	if name := extractNamedTypeName(value); name == "Snapshot" || name == "Head" || name == "Lease" || name == "Controller" || name == "ControllerSpec" {
		return true
	}
	if object := extractNamedTypeObject(value); object != nil && object.Pkg() != nil && object.Pkg().Path() == i18nPackagePath {
		return false
	}
	switch value := value.(type) {
	case *types.Pointer:
		return extractUsageCapabilityType(value.Elem(), seen)
	case *types.Signature:
		_, bind := extractSnapshotBindSignature(value)
		if bind {
			return true
		}
		for index := 0; index < value.Params().Len(); index++ {
			if extractUsageSensitiveType(value.Params().At(index).Type(), make(map[types.Type]bool)) {
				return true
			}
		}
		for index := 0; index < value.Results().Len(); index++ {
			if extractUsageSensitiveType(value.Results().At(index).Type(), make(map[types.Type]bool)) {
				return true
			}
		}
	case *types.Named:
		return extractUsageCapabilityType(value.Underlying(), seen)
	case *types.Interface:
		value.Complete()
		for index := 0; index < value.NumEmbeddeds(); index++ {
			if extractUsageCapabilityType(value.EmbeddedType(index), seen) {
				return true
			}
		}
		for index := 0; index < value.NumMethods(); index++ {
			method := value.Method(index)
			if _, bind := extractSnapshotBindSignature(method.Type()); bind {
				return true
			}
			if signature, ok := method.Type().(*types.Signature); ok {
				for parameter := 0; parameter < signature.Params().Len(); parameter++ {
					if extractUsageSensitiveType(signature.Params().At(parameter).Type(), make(map[types.Type]bool)) {
						return true
					}
				}
				for result := 0; result < signature.Results().Len(); result++ {
					if extractUsageSensitiveType(signature.Results().At(result).Type(), make(map[types.Type]bool)) {
						return true
					}
				}
			}
		}
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			if extractUsageCapabilityType(value.Field(index).Type(), seen) {
				return true
			}
		}
	case *types.Array:
		return extractUsageCapabilityType(value.Elem(), seen)
	case *types.Slice:
		return extractUsageCapabilityType(value.Elem(), seen)
	case *types.Map:
		return extractUsageCapabilityType(value.Key(), seen) || extractUsageCapabilityType(value.Elem(), seen)
	case *types.Chan:
		return extractUsageCapabilityType(value.Elem(), seen)
	case *types.Tuple:
		for index := 0; index < value.Len(); index++ {
			if extractUsageCapabilityType(value.At(index).Type(), seen) {
				return true
			}
		}
	case *types.TypeParam:
		return extractUsageCapabilityType(value.Constraint(), seen)
	case *types.Union:
		for index := 0; index < value.Len(); index++ {
			if extractUsageCapabilityType(value.Term(index).Type(), seen) {
				return true
			}
		}
	}
	return false
}

func extractUsageSensitiveType(value types.Type, seen map[types.Type]bool) bool {
	if value == nil {
		return false
	}
	value = types.Unalias(value)
	if seen[value] {
		return false
	}
	seen[value] = true
	if owner := extractHiddenKeyTypeName(value); owner != "" {
		return owner != "Message"
	}
	if name := extractNamedTypeName(value); name == "Snapshot" || name == "Head" || name == "Lease" || name == "Controller" || name == "ControllerSpec" {
		return true
	}
	if object := extractNamedTypeObject(value); object != nil && object.Pkg() != nil && object.Pkg().Path() == i18nPackagePath {
		return false
	}
	switch value := value.(type) {
	case *types.Pointer:
		return extractUsageSensitiveType(value.Elem(), seen)
	case *types.Signature:
		for index := 0; index < value.Params().Len(); index++ {
			if extractUsageSensitiveType(value.Params().At(index).Type(), seen) {
				return true
			}
		}
		for index := 0; index < value.Results().Len(); index++ {
			if extractUsageSensitiveType(value.Results().At(index).Type(), seen) {
				return true
			}
		}
	case *types.Named:
		return extractUsageSensitiveType(value.Underlying(), seen)
	case *types.Interface:
		value.Complete()
		for index := 0; index < value.NumEmbeddeds(); index++ {
			if extractUsageSensitiveType(value.EmbeddedType(index), seen) {
				return true
			}
		}
		for index := 0; index < value.NumMethods(); index++ {
			if extractUsageSensitiveType(value.Method(index).Type(), seen) {
				return true
			}
		}
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			if extractUsageSensitiveType(value.Field(index).Type(), seen) {
				return true
			}
		}
	case *types.Array:
		return extractUsageSensitiveType(value.Elem(), seen)
	case *types.Slice:
		return extractUsageSensitiveType(value.Elem(), seen)
	case *types.Map:
		return extractUsageSensitiveType(value.Key(), seen) || extractUsageSensitiveType(value.Elem(), seen)
	case *types.Chan:
		return extractUsageSensitiveType(value.Elem(), seen)
	case *types.Tuple:
		for index := 0; index < value.Len(); index++ {
			if extractUsageSensitiveType(value.At(index).Type(), seen) {
				return true
			}
		}
	case *types.TypeParam:
		return extractUsageSensitiveType(value.Constraint(), seen)
	case *types.Union:
		for index := 0; index < value.Len(); index++ {
			if extractUsageSensitiveType(value.Term(index).Type(), seen) {
				return true
			}
		}
	}
	return false
}

func extractContainsHiddenKeyType(value types.Type, seen map[types.Type]bool) bool {
	if value == nil {
		return false
	}
	value = types.Unalias(value)
	if seen[value] {
		return false
	}
	seen[value] = true
	if owner := extractHiddenKeyTypeName(value); owner != "" {
		return owner != "Message"
	}
	if object := extractNamedTypeObject(value); object != nil && object.Pkg() != nil && object.Pkg().Path() == i18nPackagePath {
		return false
	}
	switch value := value.(type) {
	case *types.Pointer:
		return extractContainsHiddenKeyType(value.Elem(), seen)
	case *types.Named:
		return extractContainsHiddenKeyType(value.Underlying(), seen)
	case *types.Struct:
		for index := 0; index < value.NumFields(); index++ {
			if extractContainsHiddenKeyType(value.Field(index).Type(), seen) {
				return true
			}
		}
	case *types.Array:
		return extractContainsHiddenKeyType(value.Elem(), seen)
	case *types.Slice:
		return extractContainsHiddenKeyType(value.Elem(), seen)
	case *types.Map:
		return extractContainsHiddenKeyType(value.Key(), seen) || extractContainsHiddenKeyType(value.Elem(), seen)
	case *types.Chan:
		return extractContainsHiddenKeyType(value.Elem(), seen)
	case *types.Tuple:
		for index := 0; index < value.Len(); index++ {
			if extractContainsHiddenKeyType(value.At(index).Type(), seen) {
				return true
			}
		}
	case *types.TypeParam:
		return extractContainsHiddenKeyType(value.Constraint(), seen)
	case *types.Union:
		for index := 0; index < value.Len(); index++ {
			if extractContainsHiddenKeyType(value.Term(index).Type(), seen) {
				return true
			}
		}
	}
	return false
}

func (a *extractPackageAnalysis) usageCapabilityExpression(expression ast.Expr, seen map[types.Object]bool) bool {
	if expression == nil {
		return false
	}
	if extractUsageCapabilityType(a.info.TypeOf(expression), make(map[types.Type]bool)) {
		return true
	}
	if extractHiddenKeyTypeName(a.info.TypeOf(expression)) == "" &&
		extractContainsHiddenKeyType(a.info.TypeOf(expression), make(map[types.Type]bool)) &&
		!a.hiddenKeyContainerKnown(expression, cloneObjectSet(seen)) {
		return true
	}
	expression = extractUnparenthesized(expression)
	switch expression := expression.(type) {
	case *ast.Ident:
		object := a.info.ObjectOf(expression)
		return a.usageCapabilityObject(object, seen)
	case *ast.CallExpr:
		if typed, ok := a.info.Types[extractUnparenthesized(expression.Fun)]; ok && typed.IsType() {
			for _, argument := range expression.Args {
				if a.usageCapabilityExpression(argument, seen) {
					return true
				}
			}
		}
	case *ast.UnaryExpr:
		return a.usageCapabilityExpression(expression.X, seen)
	case *ast.StarExpr:
		return a.usageCapabilityExpression(expression.X, seen)
	case *ast.TypeAssertExpr:
		return a.usageCapabilityExpression(expression.X, seen)
	case *ast.FuncLit:
		captured := false
		ast.Inspect(expression.Body, func(node ast.Node) bool {
			candidate, ok := node.(ast.Expr)
			if ok && candidate != expression && a.usageCapabilityExpression(candidate, cloneObjectSet(seen)) {
				captured = true
				return false
			}
			return !captured
		})
		return captured
	case *ast.CompositeLit:
		for _, element := range expression.Elts {
			if pair, ok := element.(*ast.KeyValueExpr); ok {
				element = pair.Value
			}
			candidate, ok := element.(ast.Expr)
			if ok && a.usageCapabilityExpression(candidate, cloneObjectSet(seen)) {
				return true
			}
		}
	}
	return false
}

func (a *extractPackageAnalysis) hiddenKeyContainerKnown(expression ast.Expr, seen map[types.Object]bool) bool {
	if expression == nil {
		return true
	}
	expression = extractUnparenthesized(expression)
	if owner := extractHiddenKeyTypeName(a.info.TypeOf(expression)); owner != "" {
		return owner == "Message" || a.hiddenKeyAssignmentKnown(expression, owner)
	}
	if !extractContainsHiddenKeyType(a.info.TypeOf(expression), make(map[types.Type]bool)) {
		return true
	}
	switch expression := expression.(type) {
	case *ast.Ident:
		if expression.Name == "nil" {
			return true
		}
		object := a.info.ObjectOf(expression)
		if object == nil || seen[object] {
			return false
		}
		seen[object] = true
		if a.zeroValueObjectKnown(object) {
			return true
		}
		if origin, ok := a.followAddressOrigin(object, make(map[types.Object]bool)); ok {
			return a.hiddenKeyContainerKnown(origin, seen)
		}
		if tuple, ok := a.tupleOrigins[object]; ok && a.singleStableAssignment(object) {
			return a.hiddenKeyFactoryResultKnown(tuple.call, tuple.index)
		}
		origin, ok := a.followOrigin(object, make(map[types.Object]bool))
		return ok && a.hiddenKeyContainerKnown(origin, seen)
	case *ast.UnaryExpr:
		return expression.Op == token.AND && a.hiddenKeyContainerKnown(expression.X, seen)
	case *ast.StarExpr:
		return a.hiddenKeyContainerKnown(expression.X, seen)
	case *ast.CallExpr:
		if converted, safe := a.typeConversion(expression.Fun); converted {
			return safe && len(expression.Args) == 1 && a.hiddenKeyContainerKnown(expression.Args[0], seen)
		}
		if tuple, ok := a.info.TypeOf(expression).(*types.Tuple); ok {
			for index := 0; index < tuple.Len(); index++ {
				if extractContainsHiddenKeyType(tuple.At(index).Type(), make(map[types.Type]bool)) &&
					!a.hiddenKeyFactoryResultKnown(expression, index) {
					return false
				}
			}
			return true
		}
		return a.hiddenKeyFactoryResultKnown(expression, 0)
	case *ast.CompositeLit:
		for _, value := range a.hiddenKeyCompositeExpressions(expression) {
			if !extractContainsHiddenKeyType(a.info.TypeOf(value), make(map[types.Type]bool)) {
				continue
			}
			if !a.hiddenKeyContainerKnown(value, cloneObjectSet(seen)) {
				return false
			}
		}
		return true
	}
	return false
}

func (a *extractPackageAnalysis) hiddenKeyCompositeExpressions(literal *ast.CompositeLit) []ast.Expr {
	value := a.info.TypeOf(literal)
	if value == nil {
		return nil
	}
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	_, mapLiteral := value.Underlying().(*types.Map)
	expressions := make([]ast.Expr, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		if pair, ok := element.(*ast.KeyValueExpr); ok {
			if mapLiteral {
				expressions = append(expressions, pair.Key)
			}
			expressions = append(expressions, pair.Value)
			continue
		}
		if expression, ok := element.(ast.Expr); ok {
			expressions = append(expressions, expression)
		}
	}
	return expressions
}

func (a *extractPackageAnalysis) hiddenKeyFactoryResultKnown(call *ast.CallExpr, index int) bool {
	tuple, tupleResult := a.info.TypeOf(call).(*types.Tuple)
	value := a.info.TypeOf(call)
	if tupleResult {
		if index < 0 || index >= tuple.Len() {
			return false
		}
		value = tuple.At(index).Type()
	} else if index != 0 {
		return false
	}
	if owner := extractHiddenKeyTypeName(value); owner != "" {
		key := extractFactoryResult{function: extractCalledObject(a.info, call.Fun), index: index}
		switch owner {
		case "Message":
			return true
		case "Definition":
			return a.directFactories[key] == owner || index == 0 && a.definitionConstructorExpression(call)
		case "ContractRef":
			if a.contractFactories[key] {
				return true
			}
			callable, ok := a.snapshotKeyCallExpression(call.Fun)
			return index == 0 && ok && callable.owner == "Snapshot.ContractRef" && callable.index < len(call.Args) && a.keyExpressionBounded(call.Args[callable.index])
		case "Descriptor":
			return a.directFactories[key] == owner || index == 0 && a.descriptorCallKnown(call)
		default:
			return a.directFactories[key] == owner
		}
	}
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	structure, ok := value.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	known := a.definitionFactories[extractFactoryResult{function: extractCalledObject(a.info, call.Fun), index: index}]
	for fieldIndex := 0; fieldIndex < structure.NumFields(); fieldIndex++ {
		field := structure.Field(fieldIndex)
		if !extractContainsHiddenKeyType(field.Type(), make(map[types.Type]bool)) {
			continue
		}
		if extractHiddenKeyTypeName(field.Type()) != "Definition" || !known[field.Name()] {
			return false
		}
	}
	return true
}

func (a *extractPackageAnalysis) usageCapabilityObject(object types.Object, seen map[types.Object]bool) bool {
	if object == nil {
		return false
	}
	switch a.capabilityMemo[object] {
	case 1:
		return false
	case 2:
		return true
	}
	if seen[object] {
		return true
	}
	seen[object] = true
	for _, expression := range a.capabilityValues[object] {
		if a.usageCapabilityExpression(expression, cloneObjectSet(seen)) {
			a.capabilityMemo[object] = 2
			return true
		}
	}
	a.capabilityMemo[object] = 1
	return false
}

func cloneObjectSet(source map[types.Object]bool) map[types.Object]bool {
	cloned := make(map[types.Object]bool, len(source))
	for object, value := range source {
		cloned[object] = value
	}
	return cloned
}

func (s *extractState) addFindingAt(position token.Pos, detail string) {
	if len(s.findings) >= 256 {
		return
	}
	resolved := s.fileset.PositionFor(position, false)
	s.findings = append(s.findings, extractFinding{path: resolved.Filename, line: resolved.Line, column: resolved.Column, detail: detail})
}

func (s *extractState) recordUsageAt(position token.Pos, result extractKeyResult) {
	occurrence := i18n.UsageOccurrence{}
	if result.dynamic {
		usage := i18n.DynamicUsage{Domain: result.domain, Prefix: result.prefix}
		s.dynamic[usage] = true
		occurrence.Domain = usage.Domain
		occurrence.Prefix = usage.Prefix
	} else {
		occurrence.Key = i18n.Key(result.exact)
		s.keys[occurrence.Key] = true
	}
	resolved := s.fileset.PositionFor(position, false)
	occurrence.Path = s.sourcePaths[filepath.Clean(resolved.Filename)]
	occurrence.Line = resolved.Line
	occurrence.Column = resolved.Column
	if occurrence.Path == "" || occurrence.Line < 1 || occurrence.Column < 1 {
		s.addFindingAt(position, "message usage has no reproducible source location")
		return
	}
	if s.occurrences[occurrence] {
		return
	}
	if len(s.occurrences) >= maximumExtractLocations {
		if !s.locationLimited {
			s.addFindingAt(position, fmt.Sprintf("extraction exceeds %d source occurrences", maximumExtractLocations))
			s.locationLimited = true
		}
		return
	}
	s.occurrences[occurrence] = true
}

const extractI18nAPI = `package i18n

import (
	"context"
	"io"
	"io/fs"
	"math/big"
	"time"

	"github.com/frostgrove/vv/errs"
)

const (
	GrammarProfile = "frostgrove-mf2/v1"
	MessageFormatSpec = "unicode-messageformat/48.2"
	EngineVersion = "messageformat-go/v0.8.6"
	LocaleDataVersion = "go-intl/v0.4.1:cldr/48.1.0+icu/78;x-text/v0.41.0"
	TimeZoneDataModel = "go/time.LoadLocation:runtime-selected"
	ArtifactVersion = "frostgrove.i18n.catalog/v1"
	SourceVersion = "frostgrove.i18n.source/v1"
	PublicContractSchema = "frostgrove.i18n.public-contract/v1"
	PublicTypeScriptGenerator = "frostgrove.i18n.typescript/v1"
	PublicValueContract = "frostgrove.i18n.json-scalars/v1"
	PublicTypeScriptTarget = "ES2020"
	PublicWireFormat = "json"
)

var (
	ErrInvalidArtifact error
	ErrIncompatibleArtifact error
	ErrArtifactIO error
	ErrConflict error
	ErrPinUnavailable error
	ErrSnapshotNotFound error
	ErrInvalidCatalog error
	ErrInvalidLocale error
	ErrInvalidMessage error
	ErrLimitExceeded error
	ErrNotFound error
	ErrInvalidSource error
	ErrSourceIO error
	ErrCheckFailed error
	ErrMergeWouldDiscard error
)

type Key string
func Qualify(string, string) Key
type ArgumentType uint8
const ( TypeText ArgumentType = iota + 1; TypeBool; TypeInteger; TypeUnsignedInteger; TypeBigInteger; TypeDecimal; TypeMoney; TypeDate; TypeInstant; TypeEnum )
func (ArgumentType) String() string
func (ArgumentType) Valid() bool
type ArgumentSpec struct { Name string; Type ArgumentType; Required bool; Nullable bool; Values []string }
type Argument struct{}
func Text(string, string) Argument
func Bool(string, bool) Argument
func Integer(string, int64) Argument
func UnsignedInteger(string, uint64) Argument
func BigInteger(string, *big.Int) Argument
func Decimal(string, string) Argument
func Money(string, string, string) Argument
func Date(string, int, time.Month, int) Argument
func Instant(string, time.Time) Argument
func Enum(string, string) Argument
func Null(string) Argument
func (Argument) Name() string
func (Argument) Type() ArgumentType
func (Argument) IsNull() bool
type DateValue struct { Year int; Month time.Month; Day int }

type Message struct { nonComparable []byte }
func (Message) Key() Key
func (Message) ContractRevision() string
func (Message) Arguments() []Argument
type ArgumentEncoder[A any] func(A) ([]Argument, error)
type ContractRef struct { Key Key; Revision string; Digest string }
type DefinitionSpec[A any] struct { Contract ContractRef; Encode ArgumentEncoder[A] }
type Definition[A any] struct { snapshot *Snapshot; key Key; encode ArgumentEncoder[A] }
func NewDefinition[A any](*Snapshot, Key, ArgumentEncoder[A]) (Definition[A], error) { panic("") }
func Define[A any](*Snapshot, DefinitionSpec[A]) (Definition[A], error) { panic("") }
func DefineStruct[A any](*Snapshot, ContractRef) (Definition[A], error) { panic("") }
func NewStructDefinition[A any](*Snapshot, Key) (Definition[A], error) { panic("") }
func (Definition[A]) Bind(A) (Message, error) { panic("") }
func (Definition[A]) Key() Key { panic("") }
type Optional[T any] struct { value T; state uint8 }
func Some[T any](T) Optional[T] { panic("") }
func NullValue[T any]() Optional[T] { panic("") }
func (Optional[T]) Present() bool { panic("") }
func (Optional[T]) IsNull() bool { panic("") }
func (Optional[T]) Get() (T, bool) { panic("") }

type OutputKind uint8
const ( OutputPlain OutputKind = iota; OutputRich )
func (OutputKind) String() string
func (OutputKind) Valid() bool
type ReviewState uint8
const ( ReviewUnset ReviewState = iota; ReviewApproved; ReviewRequired; ReviewRejected )
func (ReviewState) String() string
func (ReviewState) Valid() bool
type OverridePolicy uint8
const ( OverrideDenied OverridePolicy = 0; OverrideApplication OverridePolicy = 1; OverrideTenant OverridePolicy = 2; OverrideAny OverridePolicy = 3 )
func (OverridePolicy) String() string
func (OverridePolicy) Valid() bool
type Layer uint8
const ( LayerModule Layer = iota + 1; LayerApplication; LayerTenant )
func (Layer) String() string
func (Layer) Valid() bool
type Capability uint8
const ( CapabilityDateTime Capability = iota + 1; CapabilityUnit )
func (Capability) String() string
func (Capability) Valid() bool
type MatchMode uint8
const ( MatchLookup MatchMode = iota; MatchBestFit )
func (MatchMode) String() string
func (MatchMode) Valid() bool

type Translation struct { Locale string; Text string; Review ReviewState; ContractRevision string; SourceDigest string; ReviewDigest string }
type MessageSpec struct { ID string; Key Key; Revision string; Source string; Description string; Arguments []ArgumentSpec; Output OutputKind; Markup []string; Override OverridePolicy; Translations []Translation; AllowEmpty bool; Public bool }
type Module struct { Name string; Messages []MessageSpec }
type LocaleEdge struct { Locale string; Parent string }
type LocaleLimits struct { MaxChoices int; MaxHeaderBytes int; MaxRanges int; MaxSupported int; MaxTagBytes int; MaxFallbackDepth int }
type Limits struct { Locale LocaleLimits; MaxModules int; MaxMessages int; MaxTranslations int; MaxLocales int; MaxArguments int; MaxEnumValues int; MaxMarkupNames int; MaxCatalogItems int; MaxIdentifierBytes int; MaxRevisionBytes int; MaxDescriptionBytes int; MaxEnumValueBytes int; MaxArgumentBytes int; MaxBigIntegerBits int; MaxTemplateBytes int; MaxCatalogBytes int; MaxDeclarations int; MaxSelectors int; MaxVariants int; MaxLocalDepth int; MaxTemplateParts int; MaxMarkupDepth int; MaxOutputBytes int; MaxOutputParts int; MaxExplainSteps int }
func DefaultLimits() Limits
type Observer func(context.Context, Observation)
type CatalogSpec struct { Revision string; Profile string; SourceLocale string; DefaultLocale string; Supported []string; Required []string; Parents []LocaleEdge; Modules []Module; Overrides []Override; Capabilities []Capability; MatchMode MatchMode; DefaultOnMiss bool; DefaultTimeZone string; TimeZoneDataVersion string; Limits Limits; Observer Observer }
func New(CatalogSpec) (*Snapshot, error)
func NewContext(context.Context, CatalogSpec) (*Snapshot, error)
func ExpectedSourceDigestForLocale(string, string, string, MessageSpec) (string, error)
func ExpectedReviewDigest(string, string, string) (string, error)

type Override struct { Key Key; Locale string; Text string; ContractRevision string; SourceDigest string; ReviewDigest string; Review ReviewState }
type OverlaySpec struct { Layer Layer; Revision string; Overrides []Override }
func ApplicationOverlay(string, ...Override) OverlaySpec
func TenantOverlay(string, ...Override) OverlaySpec

type ChoiceSource uint8
const ( SourceExplicit ChoiceSource = iota + 1; SourceUser; SourceProtocol; SourceTenant; SourceApplication )
func (ChoiceSource) String() string
func (ChoiceSource) Valid() bool
type Choice struct { values []string }
func Exact(ChoiceSource, string) Choice
func AcceptLanguage(ChoiceSource, ...string) Choice
type LocalePolicy struct { Supported []string; Default string; Parents []LocaleEdge; Mode MatchMode; DefaultOnMiss bool; Limits LocaleLimits; Observer Observer }
type Resolution struct { Locale string; Source ChoiceSource; Outcome Outcome; Reason Reason; preferenceSteps []PreferenceStep }
func (Resolution) Matched() bool
type Resolver struct { supported []string }
func NewResolver(LocalePolicy) (*Resolver, error)
func (*Resolver) Supported() []string
func (*Resolver) Default() string
func (*Resolver) Resolve(...Choice) Resolution
func (*Resolver) ResolveContext(context.Context, ...Choice) Resolution

type Presentation uint8
const ( PresentationDefault Presentation = iota; PresentationNoIsolation )
func (Presentation) String() string
func (Presentation) Valid() bool
type ViewSpec struct { Resolution Resolution; FormattingLocale string; TimeZone string; Presentation Presentation }
type Snapshot struct { supported []string }
func (*Snapshot) Bind(Key, ...Argument) (Message, error)
func (*Snapshot) Overlay(OverlaySpec) (*Snapshot, error)
func (*Snapshot) Revision() string
func (*Snapshot) Digest() string
func (*Snapshot) Profile() string
func (*Snapshot) TimeZoneDataVersion() string
func (*Snapshot) Supported() []string
func (*Snapshot) Keys() []Key
func (*Snapshot) Descriptor(Key) (Descriptor, bool)
func (*Snapshot) ContractRef(Key) (ContractRef, bool)
func (*Snapshot) SourceDigest(Key) (string, bool)
func (*Snapshot) Resolve(...Choice) Resolution
func (*Snapshot) ResolveContext(context.Context, ...Choice) Resolution
func (*Snapshot) For(string) (*View, error)
func (*Snapshot) ForContext(context.Context, ...Choice) (*View, error)
func (*Snapshot) View(ViewSpec) (*View, error)
	func (*Snapshot) Reference() SnapshotRef
	func (*Snapshot) ErrorMessages(ErrorSpec) (*ErrorSource, error)
	func (*Snapshot) ErrorPlan(ErrorPlanSpec) (*ErrorPlan, error)
type Descriptor struct { Key Key; Revision string; Description string; Arguments []ArgumentSpec; Output OutputKind; Markup []string; Override OverridePolicy; AllowEmpty bool; Public bool }
func (Descriptor) ContractRef() ContractRef

type PartKind uint8
const ( PartText PartKind = iota + 1; PartValue; PartMarkupOpen; PartMarkupClose; PartMarkupStandalone; PartBidiIsolation )
func (PartKind) String() string
func (PartKind) Valid() bool
type Subpart struct { Type string; Text string }
type Part struct { Kind PartKind; Type string; Text string; Name string; ID string; Locale string; Direction string; Subparts []Subpart }
type Rendered struct { Text string; Parts []Part; TemplateLocale string; ResolvedLocale string; ResolutionSource ChoiceSource; ResolutionReason Reason; Revision string; Digest string; Layer Layer; Outcome Outcome; RenderKey string }
type ExplainStep struct { Locale string; Present bool }
type PreferenceStep struct { Source ChoiceSource; Outcome Outcome; Reason Reason }
type Explanation struct { Key Key; Source ChoiceSource; ResolvedLocale string; TemplateLocale string; Layer Layer; ResolutionOutcome Outcome; ResolutionReason Reason; TemplateOutcome Outcome; TemplateReason Reason; Profile string; Revision string; PreferenceSteps []PreferenceStep; Steps []ExplainStep; Truncated bool }
type View struct { resolution Resolution }
func (*View) Render(context.Context, Message) (Rendered, error)
	func (*View) RenderKey(Message) (string, error)
	func (*View) Explain(Message) (Explanation, error)
	func (*View) ErrorMessages(*ErrorPlan) (*ErrorSource, error)

type ArtifactLimits struct { MaxBytes int; MaxDepth int; MaxMembers int }
func DefaultArtifactLimits() ArtifactLimits
type Compiler struct { Limits ArtifactLimits; CatalogLimits Limits; Observer Observer }
func (Compiler) Compile(CatalogSpec) ([]byte, error)
func (Compiler) CompileContext(context.Context, CatalogSpec) ([]byte, error)
func (Compiler) Encode(*Snapshot) ([]byte, error)
func (Compiler) EncodeContext(context.Context, *Snapshot) ([]byte, error)
type Loader struct { Limits ArtifactLimits; CatalogLimits Limits; Observer Observer }
func (Loader) Load(context.Context, io.Reader) (*Snapshot, error)
func (Loader) LoadFS(context.Context, fs.FS, string) (*Snapshot, error)
func Compile(CatalogSpec) ([]byte, error)
func CompileContext(context.Context, CatalogSpec) ([]byte, error)
func Encode(*Snapshot) ([]byte, error)
func EncodeContext(context.Context, *Snapshot) ([]byte, error)
func Load(context.Context, io.Reader) (*Snapshot, error)
func LoadFS(context.Context, fs.FS, string) (*Snapshot, error)

type SnapshotRef struct { Revision string; Digest string }
func (SnapshotRef) Valid() bool
type Clock func() time.Time
type ControllerSpec struct { Initial *Snapshot; MaxRetained int; MaxPins int; MaxPinLifetime time.Duration; MaxSnapshotBytes int64; Clock Clock; Observer Observer }
type Head struct{}
func (Head) Snapshot() *Snapshot
func (Head) Reference() SnapshotRef
func (Head) Valid() bool
type Lease struct{}
func (*Lease) Snapshot() (*Snapshot, error)
func (*Lease) Reference() SnapshotRef
func (*Lease) ExpiresAt() time.Time
func (*Lease) Release()
type Controller struct { retained map[SnapshotRef]SnapshotRef }
func NewController(ControllerSpec) (*Controller, error)
func (*Controller) Current() Head
func (*Controller) Activate(Head, *Snapshot) (Head, error)
func (*Controller) ActivateContext(context.Context, Head, *Snapshot) (Head, error)
func (*Controller) Rollback(Head, SnapshotRef) (Head, error)
func (*Controller) RollbackContext(context.Context, Head, SnapshotRef) (Head, error)
func (*Controller) PinCurrent(time.Duration) (*Lease, error)
func (*Controller) Pin(SnapshotRef, time.Duration) (*Lease, error)
func (*Controller) Retained() []SnapshotRef
func (*Controller) Prune(SnapshotRef) error

type ErrorParam struct { Param string; Argument string; Currency string }
type ErrorMapping struct { Ladder string; Key Key; Params []ErrorParam; FieldArgument string }
type FieldLabel struct { Field string; Key Key }
	type ErrorSpec struct { Mappings []ErrorMapping; FieldLabels []FieldLabel; FormattingLocale string; TimeZone string; Presentation Presentation }
	type ErrorPlanSpec struct { Mappings []ErrorMapping; FieldLabels []FieldLabel }
	type ErrorPlan struct{}
type ErrorSource struct{}
func (*ErrorSource) Message(context.Context, errs.Violation, string) (string, bool)
func (*ErrorSource) MessageWithLocale(context.Context, errs.Violation, string) (string, string, bool)
type Operation uint8
const ( OperationResolve Operation = iota + 1; OperationRender; OperationCompile; OperationLoad; OperationActivate; OperationRollback )
func (Operation) String() string
func (Operation) Valid() bool
type Outcome uint8
const ( OutcomeSuccess Outcome = iota + 1; OutcomeFallback; OutcomeDefault; OutcomeNoMatch; OutcomeMissing; OutcomeInvalid; OutcomeLimited; OutcomeCanceled; OutcomeTimedOut )
func (Outcome) String() string
func (Outcome) Valid() bool
type Reason uint8
const ( ReasonNone Reason = iota; ReasonExact; ReasonLookup; ReasonBestFit; ReasonWildcard; ReasonPolicyDefault; ReasonMalformed; ReasonExcluded; ReasonUnsupported; ReasonLimit; ReasonMissingTemplate; ReasonSchemaMismatch; ReasonInvalidArgument; ReasonOutputLimit; ReasonContextCanceled; ReasonContextDeadline; ReasonTemplateFailure; ReasonConflict; ReasonSnapshotMissing; ReasonInvalidArtifact; ReasonArtifactIO; ReasonIncompatibleArtifact )
func (Reason) String() string
func (Reason) Valid() bool
type Observation struct { Operation Operation; Outcome Outcome; Reason Reason; Duration time.Duration; Count int }

type DynamicUsage struct { Domain string ` + "\x60json:\"domain\"\x60" + `; Prefix string ` + "\x60json:\"prefix\"\x60" + ` }
type UsageOccurrence struct { Key Key ` + "\x60json:\"key,omitempty\"\x60" + `; Domain string ` + "\x60json:\"domain,omitempty\"\x60" + `; Prefix string ` + "\x60json:\"prefix,omitempty\"\x60" + `; Path string ` + "\x60json:\"path\"\x60" + `; Line int ` + "\x60json:\"line\"\x60" + `; Column int ` + "\x60json:\"column\"\x60" + ` }
type UsageRootKind string
const ( UsageRootDirectory UsageRootKind = "directory"; UsageRootFile UsageRootKind = "file" )
func (UsageRootKind) String() string
func (UsageRootKind) Valid() bool
type UsageRoot struct { Path string ` + "\x60json:\"path\"\x60" + `; Kind UsageRootKind ` + "\x60json:\"kind\"\x60" + ` }
const GoUsageAnalyzer = "frostgrove.vv-i18n/go-list/v1"
type UsageFile struct { Root string ` + "\x60json:\"root\"\x60" + `; Path string ` + "\x60json:\"path\"\x60" + `; LogicalPath string ` + "\x60json:\"logical_path\"\x60" + `; SHA256 string ` + "\x60json:\"sha256\"\x60" + `; Selected bool ` + "\x60json:\"selected\"\x60" + ` }
type UsageMetadata struct { Kind string ` + "\x60json:\"kind\"\x60" + `; Path string ` + "\x60json:\"path\"\x60" + `; SHA256 string ` + "\x60json:\"sha256\"\x60" + ` }
type UsageSetting struct { Name string ` + "\x60json:\"name\"\x60" + `; Value string ` + "\x60json:\"value\"\x60" + ` }
type GoUsageScope struct { Analyzer string ` + "\x60json:\"analyzer\"\x60" + `; GOOS string ` + "\x60json:\"goos\"\x60" + `; GOARCH string ` + "\x60json:\"goarch\"\x60" + `; Compiler string ` + "\x60json:\"compiler\"\x60" + `; CgoEnabled bool ` + "\x60json:\"cgo_enabled\"\x60" + `; GoVersion string ` + "\x60json:\"go_version,omitempty\"\x60" + `; Toolchain string ` + "\x60json:\"toolchain,omitempty\"\x60" + `; GoExperiment string ` + "\x60json:\"go_experiment,omitempty\"\x60" + `; GoFlags string ` + "\x60json:\"go_flags,omitempty\"\x60" + `; GoWork string ` + "\x60json:\"go_work,omitempty\"\x60" + `; GoEnv string ` + "\x60json:\"go_env,omitempty\"\x60" + `; Environment []UsageSetting ` + "\x60json:\"environment,omitempty\"\x60" + `; BuildTags []string ` + "\x60json:\"build_tags\"\x60" + `; ToolTags []string ` + "\x60json:\"tool_tags\"\x60" + `; ReleaseTags []string ` + "\x60json:\"release_tags\"\x60" + `; Roots []UsageRoot ` + "\x60json:\"roots\"\x60" + `; Files []UsageFile ` + "\x60json:\"files,omitempty\"\x60" + `; Metadata []UsageMetadata ` + "\x60json:\"metadata,omitempty\"\x60" + `; SourceDigest string ` + "\x60json:\"source_digest\"\x60" + `; SelectedFiles int ` + "\x60json:\"selected_files\"\x60" + `; ExcludedFiles int ` + "\x60json:\"excluded_files\"\x60" + ` }
type UsageManifest struct { Keys []Key ` + "\x60json:\"keys\"\x60" + `; Dynamic []DynamicUsage ` + "\x60json:\"dynamic\"\x60" + `; Occurrences []UsageOccurrence ` + "\x60json:\"occurrences,omitempty\"\x60" + `; GoScope *GoUsageScope ` + "\x60json:\"go_scope,omitempty\"\x60" + `; ManifestDigest string ` + "\x60json:\"manifest_digest,omitempty\"\x60" + `; Complete bool ` + "\x60json:\"complete\"\x60" + ` }
func ExpectedUsageSourceDigest(GoUsageScope) string
func ExpectedUsageSourceDigestContext(context.Context, GoUsageScope) (string, error)
func ExpectedUsageManifestDigest(UsageManifest) string
func ExpectedUsageManifestDigestContext(context.Context, UsageManifest) (string, error)
type UsageLimits struct { MaxKeys int; MaxDynamic int; MaxOccurrences int; MaxRoots int; MaxFiles int; MaxMetadata int; MaxTags int; MaxEnvironment int; MaxStringBytes int; MaxMaterialBytes int }
func DefaultUsageLimits() UsageLimits
type CheckPolicy struct { Usage UsageManifest ` + "\x60json:\"usage\"\x60" + `; UsageLimits UsageLimits ` + "\x60json:\"usage_limits\"\x60" + `; StrictOptional bool ` + "\x60json:\"strict_optional\"\x60" + `; MaxFindings int ` + "\x60json:\"max_findings\"\x60" + ` }
type CheckStatus string
const ( CheckMissing CheckStatus = "missing"; CheckStale CheckStatus = "stale"; CheckReviewRequired CheckStatus = "review_required"; CheckRejected CheckStatus = "rejected"; CheckUnused CheckStatus = "unused"; CheckInvalid CheckStatus = "invalid" )
func (CheckStatus) String() string
func (CheckStatus) Valid() bool
type CheckSeverity string
const ( SeverityError CheckSeverity = "error"; SeverityWarning CheckSeverity = "warning" )
func (CheckSeverity) String() string
func (CheckSeverity) Valid() bool
type Finding struct { Status CheckStatus ` + "\x60json:\"status\"\x60" + `; Severity CheckSeverity ` + "\x60json:\"severity\"\x60" + `; Path string ` + "\x60json:\"path\"\x60" + `; Key Key ` + "\x60json:\"key\"\x60" + `; Locale string ` + "\x60json:\"locale\"\x60" + `; Detail string ` + "\x60json:\"detail\"\x60" + ` }
type LocaleCoverage struct { Locale string ` + "\x60json:\"locale\"\x60" + `; Required bool ` + "\x60json:\"required\"\x60" + `; Total int ` + "\x60json:\"total\"\x60" + `; Approved int ` + "\x60json:\"approved\"\x60" + `; Missing int ` + "\x60json:\"missing\"\x60" + `; Stale int ` + "\x60json:\"stale\"\x60" + `; ReviewRequired int ` + "\x60json:\"review_required\"\x60" + `; Rejected int ` + "\x60json:\"rejected\"\x60" + `; Invalid int ` + "\x60json:\"invalid\"\x60" + ` }
func (LocaleCoverage) Complete() bool
type Report struct { SourceDigest string ` + "\x60json:\"source_digest\"\x60" + `; Findings []Finding ` + "\x60json:\"findings\"\x60" + `; Coverage []LocaleCoverage ` + "\x60json:\"coverage\"\x60" + `; Errors int ` + "\x60json:\"errors\"\x60" + `; Warnings int ` + "\x60json:\"warnings\"\x60" + `; StructuralErrors int ` + "\x60json:\"structural_errors\"\x60" + `; FirstStructural *Finding ` + "\x60json:\"first_structural,omitempty\"\x60" + `; Truncated bool ` + "\x60json:\"truncated\"\x60" + ` }
func Check(CatalogSpec, CheckPolicy) Report
func CheckContext(context.Context, CatalogSpec, CheckPolicy) Report
func (Report) OK() bool
func (Report) Error() string
func (Report) Unwrap() error
func (Report) Err() error
func (Report) FirstStructuralError() (Finding, bool)
func (Report) Summary() string

type ProblemCode string
const ( ProblemInvalid ProblemCode = "invalid"; ProblemDuplicate ProblemCode = "duplicate"; ProblemCollision ProblemCode = "canonical_collision"; ProblemMissing ProblemCode = "missing"; ProblemUnsupported ProblemCode = "unsupported"; ProblemStale ProblemCode = "stale"; ProblemCycle ProblemCode = "cycle"; ProblemLimit ProblemCode = "limit"; ProblemSchema ProblemCode = "schema"; ProblemSecurity ProblemCode = "security"; ProblemInvalidSyntax ProblemCode = "invalid_syntax" )
func (ProblemCode) String() string
func (ProblemCode) Valid() bool
type Problem struct { Code ProblemCode; Path string; Detail string }
type Problems struct { problems []Problem }
func (*Problems) Error() string
func (*Problems) Unwrap() []error
func (*Problems) Items() []Problem

type PseudoMode uint8
const ( PseudoAccent PseudoMode = iota + 1; PseudoRTL )
func (PseudoMode) String() string
func (PseudoMode) Valid() bool
type PseudoSpec struct { Locale string; Mode PseudoMode; MaxOutputBytes int }
func Pseudo(CatalogSpec, PseudoSpec) (CatalogSpec, error)
func PseudoContext(context.Context, CatalogSpec, PseudoSpec) (CatalogSpec, error)
type GoGeneratorSpec struct { Package string; MaxOutputBytes int }
func GenerateGo(*Snapshot, GoGeneratorSpec) ([]byte, error)
func GenerateGoContext(context.Context, *Snapshot, GoGeneratorSpec) ([]byte, error)
type PublicExport struct { TypeScript []byte; Manifest []byte; Address string }
type PublicExportSpec struct { MaxOutputBytes int }
func ExportPublic(*Snapshot) (PublicExport, error)
func ExportPublicContext(context.Context, *Snapshot, PublicExportSpec) (PublicExport, error)
func ExpectedPublicExportAddress([]byte, []byte) string
func ExpectedPublicExportAddressContext(context.Context, []byte, []byte) (string, error)
type SourceCodec struct { Limits ArtifactLimits; CatalogLimits Limits }
func DecodeSource(context.Context, io.Reader) (CatalogSpec, error)
func EncodeSource(CatalogSpec) ([]byte, error)
func EncodeSourceContext(context.Context, CatalogSpec) ([]byte, error)
func (SourceCodec) Decode(context.Context, io.Reader) (CatalogSpec, error)
func (SourceCodec) Encode(CatalogSpec) ([]byte, error)
func (SourceCodec) EncodeContext(context.Context, CatalogSpec) ([]byte, error)
func (SourceCodec) EncodedSize(CatalogSpec) (int, error)
func (SourceCodec) EncodedSizeContext(context.Context, CatalogSpec) (int, error)
type SourceMergePolicy struct { PruneObsolete bool }
type SourceMerger struct { CatalogLimits Limits; SourceLimits ArtifactLimits }
func MergeSource(CatalogSpec, CatalogSpec, SourceMergePolicy) (CatalogSpec, error)
func MergeSourceContext(context.Context, CatalogSpec, CatalogSpec, SourceMergePolicy) (CatalogSpec, error)
func (SourceMerger) Merge(CatalogSpec, CatalogSpec, SourceMergePolicy) (CatalogSpec, error)
func (SourceMerger) MergeContext(context.Context, CatalogSpec, CatalogSpec, SourceMergePolicy) (CatalogSpec, error)

type NumericValue interface { isNumericValue() }
type SignedNumber int64
func (SignedNumber) isNumericValue()
type UnsignedNumber uint64
func (UnsignedNumber) isNumericValue()
type DecimalNumber string
func (DecimalNumber) isNumericValue()
type BigIntegerNumber struct { value *big.Int; invalid string }
func NewBigIntegerNumber(*big.Int) BigIntegerNumber
func (BigIntegerNumber) isNumericValue()
type DigitRange struct { minimum int; maximum int; set bool }
func Digits(int, int) DigitRange
func (DigitRange) Minimum() int
func (DigitRange) Maximum() int
type NumberGrouping uint8
const ( NumberGroupingDefault NumberGrouping = iota; NumberGroupingAlways; NumberGroupingNever; NumberGroupingMin2 )
func (NumberGrouping) String() string
func (NumberGrouping) Valid() bool
type NumberSignDisplay uint8
const ( NumberSignDefault NumberSignDisplay = iota; NumberSignAlways; NumberSignNever; NumberSignExceptZero; NumberSignNegative )
func (NumberSignDisplay) String() string
func (NumberSignDisplay) Valid() bool
type NumberNotation uint8
const ( NumberNotationDefault NumberNotation = iota; NumberNotationScientific; NumberNotationEngineering; NumberNotationCompact )
func (NumberNotation) String() string
func (NumberNotation) Valid() bool
type NumberCompactDisplay uint8
const ( NumberCompactDefault NumberCompactDisplay = iota; NumberCompactShort; NumberCompactLong )
func (NumberCompactDisplay) String() string
func (NumberCompactDisplay) Valid() bool
type NumberRoundingMode uint8
const ( NumberRoundingModeDefault NumberRoundingMode = iota; NumberRoundingModeCeil; NumberRoundingModeFloor; NumberRoundingModeExpand; NumberRoundingModeTrunc; NumberRoundingModeHalfCeil; NumberRoundingModeHalfFloor; NumberRoundingModeHalfExpand; NumberRoundingModeHalfTrunc; NumberRoundingModeHalfEven )
func (NumberRoundingMode) String() string
func (NumberRoundingMode) Valid() bool
type NumberRoundingPriority uint8
const ( NumberRoundingPriorityDefault NumberRoundingPriority = iota; NumberRoundingPriorityAuto; NumberRoundingPriorityMorePrecision; NumberRoundingPriorityLessPrecision )
func (NumberRoundingPriority) String() string
func (NumberRoundingPriority) Valid() bool
type NumberTrailingZeroDisplay uint8
const ( NumberTrailingZeroDefault NumberTrailingZeroDisplay = iota; NumberTrailingZeroAuto; NumberTrailingZeroStripIfInteger )
func (NumberTrailingZeroDisplay) String() string
func (NumberTrailingZeroDisplay) Valid() bool
type NumberRoundingIncrement uint16
const ( NumberRoundingIncrementDefault NumberRoundingIncrement = 0; NumberRoundingIncrement1 NumberRoundingIncrement = 1; NumberRoundingIncrement2 NumberRoundingIncrement = 2; NumberRoundingIncrement5 NumberRoundingIncrement = 5; NumberRoundingIncrement10 NumberRoundingIncrement = 10; NumberRoundingIncrement20 NumberRoundingIncrement = 20; NumberRoundingIncrement25 NumberRoundingIncrement = 25; NumberRoundingIncrement50 NumberRoundingIncrement = 50; NumberRoundingIncrement100 NumberRoundingIncrement = 100; NumberRoundingIncrement200 NumberRoundingIncrement = 200; NumberRoundingIncrement250 NumberRoundingIncrement = 250; NumberRoundingIncrement500 NumberRoundingIncrement = 500; NumberRoundingIncrement1000 NumberRoundingIncrement = 1000; NumberRoundingIncrement2000 NumberRoundingIncrement = 2000; NumberRoundingIncrement2500 NumberRoundingIncrement = 2500; NumberRoundingIncrement5000 NumberRoundingIncrement = 5000 )
func (NumberRoundingIncrement) String() string
func (NumberRoundingIncrement) Valid() bool
type NumberFormatSpec struct { Grouping NumberGrouping; Sign NumberSignDisplay; Notation NumberNotation; Compact NumberCompactDisplay; RoundingMode NumberRoundingMode; RoundingPriority NumberRoundingPriority; TrailingZeroDisplay NumberTrailingZeroDisplay; RoundingIncrement NumberRoundingIncrement; MinimumIntegerDigits int; FractionDigits DigitRange; SignificantDigits DigitRange; NumberingSystem string }
type CurrencyDisplay uint8
const ( CurrencyDisplayDefault CurrencyDisplay = iota; CurrencyDisplayCode; CurrencyDisplayName; CurrencyDisplayNarrowSymbol )
func (CurrencyDisplay) String() string
func (CurrencyDisplay) Valid() bool
type CurrencySign uint8
const ( CurrencySignDefault CurrencySign = iota; CurrencySignAccounting )
func (CurrencySign) String() string
func (CurrencySign) Valid() bool
type MoneyValue struct { Amount DecimalNumber; Currency string }
type MoneyFormatSpec struct { Number NumberFormatSpec; Display CurrencyDisplay; Sign CurrencySign }
type PercentFormatSpec struct { Number NumberFormatSpec }
type UnitFormatSpec struct { Number NumberFormatSpec; Unit string; Width FormatWidth }

type DateTimeStyle uint8
const ( DateTimeStyleDefault DateTimeStyle = iota; DateTimeStyleFull; DateTimeStyleLong; DateTimeStyleMedium; DateTimeStyleShort )
func (DateTimeStyle) String() string
func (DateTimeStyle) Valid() bool
type HourCycle uint8
const ( HourCycleDefault HourCycle = iota; HourCycleH11; HourCycleH12; HourCycleH23; HourCycleH24 )
func (HourCycle) String() string
func (HourCycle) Valid() bool
type DateFormatSpec struct { Style DateTimeStyle; Calendar string; NumberingSystem string }
type TimeFormatSpec struct { Style DateTimeStyle; Calendar string; NumberingSystem string; HourCycle HourCycle }
type DateTimeFormatSpec struct { DateStyle DateTimeStyle; TimeStyle DateTimeStyle; Calendar string; NumberingSystem string; HourCycle HourCycle }

type FormatWidth uint8
const ( FormatWidthDefault FormatWidth = iota; FormatWidthLong; FormatWidthShort; FormatWidthNarrow )
func (FormatWidth) String() string
func (FormatWidth) Valid() bool
type ListType uint8
const ( ListConjunction ListType = iota; ListDisjunction; ListUnit )
func (ListType) String() string
func (ListType) Valid() bool
type ListFormatSpec struct { Type ListType; Width FormatWidth }
type RelativeUnit uint8
const ( RelativeSecond RelativeUnit = iota + 1; RelativeMinute; RelativeHour; RelativeDay; RelativeWeek; RelativeMonth; RelativeQuarter; RelativeYear )
func (RelativeUnit) String() string
func (RelativeUnit) Valid() bool
type RelativeNumeric uint8
const ( RelativeNumericAlways RelativeNumeric = iota; RelativeNumericAuto )
func (RelativeNumeric) String() string
func (RelativeNumeric) Valid() bool
type RelativeFormatSpec struct { Width FormatWidth; Numeric RelativeNumeric; NumberingSystem string }
type DurationStyle uint8
const ( DurationShort DurationStyle = iota; DurationLong; DurationNarrow; DurationDigital )
func (DurationStyle) String() string
func (DurationStyle) Valid() bool
type DurationUnitStyle uint8
const ( DurationUnitDefault DurationUnitStyle = iota; DurationUnitLong; DurationUnitShort; DurationUnitNarrow; DurationUnitNumeric; DurationUnitTwoDigit )
func (DurationUnitStyle) String() string
func (DurationUnitStyle) Valid() bool
type DurationDisplay uint8
const ( DurationDisplayDefault DurationDisplay = iota; DurationDisplayAuto; DurationDisplayAlways )
func (DurationDisplay) String() string
func (DurationDisplay) Valid() bool
type DurationUnitSpec struct { Style DurationUnitStyle; Display DurationDisplay }
type DurationFormatSpec struct { Style DurationStyle; NumberingSystem string; FractionalDigits *int; Years DurationUnitSpec; Months DurationUnitSpec; Weeks DurationUnitSpec; Days DurationUnitSpec; Hours DurationUnitSpec; Minutes DurationUnitSpec; Seconds DurationUnitSpec; Milliseconds DurationUnitSpec; Microseconds DurationUnitSpec; Nanoseconds DurationUnitSpec }
type DurationValue struct { Years int64; Months int64; Weeks int64; Days int64; Hours int64; Minutes int64; Seconds int64; Milliseconds int64; Microseconds int64; Nanoseconds int64 }
type DisplayNameType uint8
const ( DisplayLanguage DisplayNameType = iota + 1; DisplayRegion; DisplayScript; DisplayCurrency; DisplayCalendar; DisplayDateTimeField )
func (DisplayNameType) String() string
func (DisplayNameType) Valid() bool
type DisplayNameFallback uint8
const ( DisplayNameCode DisplayNameFallback = iota; DisplayNameNone )
func (DisplayNameFallback) String() string
func (DisplayNameFallback) Valid() bool
type LanguageDisplay uint8
const ( LanguageDialect LanguageDisplay = iota; LanguageStandard )
func (LanguageDisplay) String() string
func (LanguageDisplay) Valid() bool
type DisplayNameSpec struct { Type DisplayNameType; Width FormatWidth; Fallback DisplayNameFallback; LanguageDisplay LanguageDisplay }
type FormattedValue struct { Text string; Parts []Part; Locale string }
func (*View) FormatList(context.Context, []string, ListFormatSpec) (FormattedValue, error)
func (*View) FormatRelative(context.Context, int64, RelativeUnit, RelativeFormatSpec) (FormattedValue, error)
func (*View) FormatDuration(context.Context, DurationValue, DurationFormatSpec) (FormattedValue, error)
func (*View) FormatDisplayName(context.Context, string, DisplayNameSpec) (FormattedValue, bool, error)
func (*View) FormatNumber(context.Context, NumericValue, NumberFormatSpec) (FormattedValue, error)
func (*View) FormatMoney(context.Context, MoneyValue, MoneyFormatSpec) (FormattedValue, error)
func (*View) FormatPercent(context.Context, NumericValue, PercentFormatSpec) (FormattedValue, error)
func (*View) FormatUnit(context.Context, NumericValue, UnitFormatSpec) (FormattedValue, error)
func (*View) FormatDate(context.Context, DateValue, DateFormatSpec) (FormattedValue, error)
func (*View) FormatInstantDate(context.Context, time.Time, DateFormatSpec) (FormattedValue, error)
func (*View) FormatTime(context.Context, time.Time, TimeFormatSpec) (FormattedValue, error)
func (*View) FormatDateTime(context.Context, time.Time, DateTimeFormatSpec) (FormattedValue, error)
`

func syntheticI18nPackage(fallback types.Importer) (*types.Package, error) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "i18n_api.go", extractI18nAPI, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	packageValue, err := (&types.Config{Importer: fallback}).Check(i18nPackagePath, fileset, []*ast.File{file}, nil)
	if err != nil {
		return nil, err
	}
	return packageValue, nil
}

const extractErrsAPI = `package errs

import (
	"context"
	"io/fs"
)

const (
	DefaultLocaleFile = "default"
	MaxCatalogueFileBytes = 1 << 20
	MaxCatalogueBytes = 16 << 20
	MaxCatalogueFiles = 128
	MaxCatalogueDirectoryEntries = 4096
	MaxCatalogueEntries = 10_000
	MaxMessageKeyBytes = 256
	MaxMessageTemplateBytes = 16 << 10
	MaxMessageOutputBytes = 16 << 10
	MaxLocaleBytes = 128
)

var (
	ErrCodeRedeclared error
	ErrMessageRedeclared error
)

type Kind uint8
const ( KindInternal Kind = iota; KindNotFound; KindUnauthorized; KindForbidden; KindRetryable; KindConflict; KindValidation; KindBadRequest; KindTooLarge; KindMethodNotAllowed )
func (Kind) String() string
func (Kind) MarshalJSON() ([]byte, error)
type Code string
const (
	CodeUnique Code = "unique"; CodeNotUnique Code = "not_unique"; CodeForeignKey Code = "foreign_key"; CodeRestrict Code = "restrict"; CodeRequired Code = "required"; CodeCheck Code = "check"; CodeExclusion Code = "exclusion"; CodeTooLong Code = "too_long"; CodeOutOfRange Code = "out_of_range"; CodeInvalidFormat Code = "invalid_format"; CodeInvalidEnum Code = "invalid_enum"; CodeStaleVersion Code = "stale_version"
	CodeMalformedBody Code = "malformed_body"; CodeInvalidID Code = "invalid_id"; CodeUnknownField Code = "unknown_field"; CodeBadQuery Code = "bad_query"; CodeTooLarge Code = "too_large"; CodeConflict Code = "conflict"; CodeNotFound Code = "not_found"; CodeForbidden Code = "forbidden"; CodeMethodNotAllowed Code = "method_not_allowed"; CodeUnauthenticated Code = "unauthenticated"; CodeDeadlock Code = "deadlock"; CodeSerializationFailure Code = "serialization_failure"; CodeLockTimeout Code = "lock_timeout"; CodeTransactionAborted Code = "transaction_aborted"; CodeUnavailable Code = "unavailable"; CodeSchemaNotReady Code = "schema_not_ready"; CodeInternal Code = "internal"
)
type Step struct { Name string; Index int; IsIndex bool }
func Named(string) Step
func Indexed(int) Step
type Path []Step
func ParsePath(string) Path
func (Path) MarshalJSON() ([]byte, error)
func (*Path) UnmarshalJSON([]byte) error
func (Path) String() string
func (Path) Pointer() string
type P = map[string]any
type Origin uint8
const ( OriginInput Origin = iota; OriginState )
func (Origin) String() string
type Source struct { Table string; Schema string; Columns []string; Constraint string }
type Violation struct { Path Path; Code Code; Origin Origin; Message string; MessageLocale string; Params map[string]any; Source Source; Approximate bool }
func (Violation) MarshalJSON() ([]byte, error)
func (Violation) String() string
type FieldViolation interface { Namespace() string; Tag() string; Param() string; Value() any }
func FromFieldViolations[T FieldViolation](string, ...T) []Violation { panic("") }
type MessageSource interface { Message(context.Context, Violation, string) (string, bool) }
type LocalizedMessageSource interface { MessageSource; MessageWithLocale(context.Context, Violation, string) (string, string, bool) }
type Resolver interface { Resolve(Path) (Path, bool) }
func Chain(...Resolver) Resolver
type Detail struct { Dialect string; SQLState string; Native int; Constraint string; Table string; Columns []string; Value string; RefTable string; RefColumns []string; Driver error }
type Fault struct { Kind Kind; Code Code; Message string; Violations []Violation; Op string; Entity string; Partial bool; Detail Detail }
func AsFault(error) (*Fault, bool)
func (*Fault) Error() string
func (Fault) String() string
func (Fault) MarshalJSON() ([]byte, error)
func (*Fault) Unwrap() []error
type Builder struct { fault Fault }
func New(Kind) *Builder
func Internal() *Builder
func NotFound() *Builder
func Unauthorized() *Builder
func Forbidden() *Builder
func Retryable() *Builder
func Conflict() *Builder
func Validation() *Builder
func BadRequest() *Builder
func TooLarge() *Builder
func MethodNotAllowed() *Builder
func (*Builder) Approximate(bool) *Builder
func (*Builder) At(Path) *Builder
func (*Builder) Code(Code) *Builder
func (*Builder) Detail(Detail) *Builder
func (*Builder) Entity(string) *Builder
func (*Builder) Fault() *Fault
func (*Builder) Field(string) *Builder
func (*Builder) General() *Builder
func (*Builder) Message(string) *Builder
func (*Builder) Op(string) *Builder
func (*Builder) Origin(Origin) *Builder
func (*Builder) Params(P) *Builder
func (*Builder) Partial(bool) *Builder
func (*Builder) Source(Source) *Builder
func (*Builder) Wrapping(...error) *Builder
type Classifier interface { Classify(error) (*Fault, bool) }
type CodeMapper interface { CodeFor(*Fault, Violation) (Code, bool) }
type CodeDef struct { Kind Kind; Message string }
type Codes struct { definitions map[Code]CodeDef }
func NewCodes() *Codes
func StandardCodes() *Codes
func (*Codes) Add(Code, Kind, string) error
func (*Codes) KindOf(Code) (Kind, bool)
func (*Codes) Message(context.Context, Violation, string) (string, bool)
func (*Codes) MessageFor(Code) (string, bool)
type Messages struct { templates map[string]map[string]string }
func LoadMessages(*Codes, fs.FS, string) (*Messages, error)
func NewMessages(*Codes) *Messages
func (*Messages) Add(string, string, string) error
func (*Messages) Load(fs.FS, string) error
func (*Messages) Locales() []string
func (*Messages) Message(context.Context, Violation, string) (string, bool)
func (*Messages) MessageWithLocale(context.Context, Violation, string) (string, string, bool)
func (*Messages) Missing(string) []Code
func SortViolations([]Violation)
`

func syntheticErrsPackage(fallback types.Importer) (*types.Package, error) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "errs_api.go", extractErrsAPI, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	packageValue, err := (&types.Config{Importer: fallback}).Check(errsPackagePath, fileset, []*ast.File{file}, nil)
	if err != nil {
		return nil, err
	}
	return packageValue, nil
}

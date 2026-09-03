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
	"strings"
)

const (
	DefaultModuleOut    = "vv_module_gen.go"
	DefaultModuleFile   = "module.manifest.yml"
	DefaultModulePkg    = "github.com/frostgrove/vv/app/module"
	DefaultCheckType    = "github.com/frostgrove/vv/health.Contribution"
	DefaultRouteType    = "github.com/frostgrove/vv/app/http/appfiber.Route"
	DefaultWorkerType   = "github.com/frostgrove/vv/runtime.Runner"
	DefaultSeederType   = "github.com/frostgrove/vv/app.Seeder"
	moduleFormat        = 1
	moduleGeneratedBy   = "vv generate module"
	moduleAlias         = "vvmodule"
	moduleVariable      = "VVModule"
	sourceFromSignature = "inferred-from-signature"
	moduleHint          = "confirm every contribution in module.manifest.yml"
)

const (
	kindProvide = "provide"
	kindRoute   = "route"
	kindWorker  = "worker"
	kindSeeder  = "seeder"
	kindCheck   = "check"
)

var moduleKinds = []struct {
	kind  string
	field string
}{
	{kindProvide, "Provide"},
	{kindRoute, "Routes"},
	{kindWorker, "Workers"},
	{kindSeeder, "Seeders"},
	{kindCheck, "Checks"},
}

func knownModuleKind(kind string) bool {
	for _, known := range moduleKinds {
		if known.kind == kind {
			return true
		}
	}
	return false
}

func moduleKindNames() string {
	names := make([]string, 0, len(moduleKinds))
	for _, known := range moduleKinds {
		names = append(names, known.kind)
	}
	return strings.Join(names, ", ")
}

type ModuleOptions struct {
	Dir        string
	Out        string
	Manifest   string
	Name       string
	Order      int
	Import     string
	ModulePkg  string
	CheckType  string
	RouteType  string
	WorkerType string
	SeederType string
	Recursive  bool
	Check      bool
	Log        io.Writer
}

type ModuleConfirmationError struct {
	Manifest      string
	Contributions []string
}

func (this *ModuleConfirmationError) Error() string {
	return fmt.Sprintf("codegen: the kind of a contribution was inferred from what its constructor returns and nobody has confirmed it; set confirmed: true (or excluded: true) in %s for: %s",
		this.Manifest, strings.Join(this.Contributions, ", "))
}

type moduleCandidate struct {
	symbol      string
	kind        string
	alias       string
	importPath  string
	fingerprint string
}

type moduleSurface struct {
	pkg        string
	name       string
	imports    map[string]string
	candidates []moduleCandidate
}

type moduleManifestDocument struct {
	Format        int                          `json:"format"`
	GeneratedBy   string                       `json:"generated_by"`
	Package       string                       `json:"package"`
	Module        string                       `json:"module"`
	Order         int                          `json:"order"`
	Contributions []moduleManifestContribution `json:"contributions"`
}

type moduleManifestContribution struct {
	Symbol      string `json:"symbol"`
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	Fingerprint string `json:"signature_fingerprint"`
	Excluded    bool   `json:"excluded"`
	Confirmed   bool   `json:"confirmed"`
}

func RunModule(options *ModuleOptions) error {
	if options == nil {
		return fmt.Errorf("codegen: options are nil")
	}
	outName := cmpOr(options.Out, DefaultModuleOut)
	if err := artifactName(outName, "-out"); err != nil {
		return err
	}
	manifestName := cmpOr(options.Manifest, DefaultModuleFile)
	if err := artifactName(manifestName, "-manifest"); err != nil {
		return err
	}
	if options.Recursive {
		return runModuleTree(options, outName, manifestName)
	}
	return runOneModule(options, outName, manifestName)
}

func runModuleTree(options *ModuleOptions, outName, manifestName string) error {
	if options.Import != "" || options.Name != "" {
		return fmt.Errorf("-recursive names every module after its own directory and cannot be combined with -name or -import")
	}
	dirs, err := moduleDirs(options.Dir)
	if err != nil {
		return err
	}
	var waiting, stale []string
	for _, dir := range dirs {
		one := *options
		one.Dir, one.Out, one.Manifest, one.Recursive = dir, outName, manifestName, false
		err := RunModule(&one)
		var confirmation *ModuleConfirmationError
		var drift *DriftError
		switch {
		case err == nil:
		case errors.As(err, &confirmation):
			for _, contribution := range confirmation.Contributions {
				waiting = append(waiting, filepath.Base(dir)+"."+contribution)
			}
		case errors.As(err, &drift):
			stale = append(stale, drift.Paths...)
		case strings.Contains(err.Error(), "no constructor in "):
		default:
			return err
		}
	}
	if len(waiting) != 0 {
		sort.Strings(waiting)
		return &ModuleConfirmationError{Manifest: manifestName, Contributions: waiting}
	}
	if len(stale) != 0 {
		sort.Strings(stale)
		return &DriftError{Paths: stale}
	}
	return nil
}

func runOneModule(options *ModuleOptions, outName, manifestName string) error {
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
	importPath := options.Import
	if importPath == "" {
		importPath, err = moduleImportPath(options.Dir)
		if err != nil {
			return err
		}
	}
	surface, err := discoverModule(options.Dir, importPath, outName, moduleMarkers(options))
	if err != nil {
		return err
	}
	surface.name = cmpOr(options.Name, filepath.Base(mustAbs(options.Dir)))
	if len(surface.candidates) == 0 {
		return fmt.Errorf("codegen: no constructor in %s: nothing under it returns a value a container could build, so there is no module to define", options.Dir)
	}

	prior, err := readModuleManifest(manifestPath, surface.pkg)
	if err != nil {
		return err
	}
	document, err := buildModuleManifest(surface, prior, options.Order, filepath.Base(manifestPath))
	if err != nil {
		return err
	}
	manifestSource, err := marshalModuleManifest(document)
	if err != nil {
		return err
	}
	if unconfirmed := unconfirmedContributions(document); len(unconfirmed) != 0 {
		if !options.Check {
			if err := writeArtifact(manifestPath, manifestSource, validateModuleManifestTarget); err != nil {
				return err
			}
			if err := writeGenerated(outPath, unconfirmedModule(surface.pkg)); err != nil {
				return err
			}
		}
		return &ModuleConfirmationError{Manifest: filepath.Base(manifestPath), Contributions: unconfirmed}
	}
	source, err := renderModule(surface, document, cmpOr(options.ModulePkg, DefaultModulePkg))
	if err != nil {
		return err
	}
	if err := validateModuleDeclarations(options.Dir, surface.pkg, outName); err != nil {
		return err
	}
	if options.Check {
		return checkArtifacts([]artifact{{outPath, source}, {manifestPath, manifestSource}})
	}
	if err := writeArtifact(manifestPath, manifestSource, validateModuleManifestTarget); err != nil {
		return err
	}
	if err := writeGenerated(outPath, source); err != nil {
		return err
	}
	if options.Log != nil {
		fmt.Fprintf(options.Log, "vv: wrote %s and %s (%d contributions)\n", outPath, manifestPath, len(includedContributions(document)))
	}
	return nil
}

func moduleMarkers(options *ModuleOptions) map[string]string {
	markers := map[string]string{}
	for _, marker := range []struct {
		typeName string
		kind     string
	}{
		{cmpOr(options.CheckType, DefaultCheckType), kindCheck},
		{cmpOr(options.RouteType, DefaultRouteType), kindRoute},
		{cmpOr(options.WorkerType, DefaultWorkerType), kindWorker},
		{cmpOr(options.SeederType, DefaultSeederType), kindSeeder},
	} {
		if marker.typeName != "" && marker.typeName != "-" {
			markers[marker.typeName] = marker.kind
		}
	}
	return markers
}

func mustAbs(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

func moduleImportPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("codegen: resolve %s: %w", dir, err)
	}
	for at := abs; ; at = filepath.Dir(at) {
		source, err := os.ReadFile(filepath.Join(at, "go.mod"))
		if err == nil {
			path := declaredModulePath(source)
			if path == "" {
				return "", fmt.Errorf("codegen: %s declares no module path; pass -import", filepath.Join(at, "go.mod"))
			}
			rest, err := filepath.Rel(at, abs)
			if err != nil {
				return "", fmt.Errorf("codegen: locate %s inside %s: %w", abs, at, err)
			}
			if rest == "." {
				return path, nil
			}
			return path + "/" + filepath.ToSlash(rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("codegen: read %s: %w", filepath.Join(at, "go.mod"), err)
		}
		if parent := filepath.Dir(at); parent != at {
			continue
		}
		return "", fmt.Errorf("codegen: no go.mod above %s, so the generated file cannot name the packages it imports; pass -import", abs)
	}
}

func declaredModulePath(source []byte) string {
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(line)
		if rest, isModule := strings.CutPrefix(line, "module"); isModule && strings.HasPrefix(rest, " ") {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func discoverModule(root, importPath, skip string, markers map[string]string) (*moduleSurface, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("codegen: resolve %s: %w", root, err)
	}
	dirs, err := packagedDirs(abs)
	if err != nil {
		return nil, err
	}
	surface := &moduleSurface{imports: map[string]string{}}
	byAlias := map[string]string{}
	for _, dir := range dirs {
		filter := func(info os.FileInfo) bool {
			name := info.Name()
			return !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, "_gen.go") &&
				(dir != abs || name != skip)
		}
		parsed, err := parser.ParseDir(token.NewFileSet(), dir, filter, 0)
		if err != nil {
			return nil, fmt.Errorf("codegen: reading %s: %w", dir, err)
		}
		names := make([]string, 0, len(parsed))
		for name := range parsed {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			continue
		}
		if len(names) != 1 {
			return nil, fmt.Errorf("codegen: %s holds %d packages (%s); a module is generated for one package tree at a time",
				dir, len(names), strings.Join(names, ", "))
		}
		alias, packageImport := "", importPath
		if dir != abs {
			rest, err := filepath.Rel(abs, dir)
			if err != nil {
				return nil, fmt.Errorf("codegen: locate %s inside %s: %w", dir, abs, err)
			}
			alias, packageImport = names[0], importPath+"/"+filepath.ToSlash(rest)
			if taken, twice := byAlias[alias]; twice {
				return nil, fmt.Errorf("codegen: %s and %s are both package %s, and the generated file would need one name for both",
					taken, packageImport, alias)
			}
			if alias == moduleAlias {
				return nil, fmt.Errorf("codegen: %s is package %s, which is the name the generated file gives the module package",
					packageImport, alias)
			}
			byAlias[alias] = packageImport
		} else {
			surface.pkg = names[0]
		}
		if err := collectContributions(surface, parsed[names[0]], alias, packageImport, markers); err != nil {
			return nil, err
		}
	}
	if surface.pkg == "" {
		return nil, fmt.Errorf("codegen: %s is not a Go package, so there is nothing to declare the module in", abs)
	}
	sort.Slice(surface.candidates, func(i, j int) bool {
		return surface.candidates[i].symbol < surface.candidates[j].symbol
	})
	return surface, nil
}

func collectContributions(surface *moduleSurface, pkg *ast.Package, alias, importPath string, markers map[string]string) error {
	fileNames := make([]string, 0, len(pkg.Files))
	for name := range pkg.Files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		file := pkg.Files[name]
		aliases, err := importAliases(file, name)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Recv != nil || function.Body == nil {
				continue
			}
			if function.Type.TypeParams != nil || function.Name.Name == "init" || function.Name.Name == "main" {
				continue
			}
			if alias != "" && !function.Name.IsExported() {
				continue
			}
			result, named := namedResultType(function)
			if !named {
				continue
			}
			kind, marked := markedKind(result, aliases, markers)
			if !marked {
				kind = kindProvide
			}
			symbol := function.Name.Name
			if alias != "" {
				symbol = alias + "." + symbol
			}
			candidate := moduleCandidate{
				symbol:      symbol,
				kind:        kind,
				alias:       alias,
				importPath:  importPath,
				fingerprint: contributionFingerprint(symbol, kind, exprString(function.Type)),
			}
			surface.candidates = append(surface.candidates, candidate)
			if alias != "" {
				surface.imports[alias] = importPath
			}
		}
	}
	return nil
}

var unnamedResultTypes = map[string]bool{
	"any": true, "bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true, "int8": true,
	"int16": true, "int32": true, "int64": true, "rune": true, "string": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true,
}

// A container builds named things. A function returning a string, a slice or a
// bare interface is a helper, not a constructor, and the walk leaves it out
// rather than making a person exclude it by hand later.
func namedResultType(function *ast.FuncDecl) (ast.Expr, bool) {
	results := function.Type.Results
	if results == nil || len(results.List) == 0 {
		return nil, false
	}
	expr := results.List[0].Type
	for {
		switch typed := expr.(type) {
		case *ast.StarExpr:
			expr = typed.X
		case *ast.IndexExpr:
			expr = typed.X
		case *ast.IndexListExpr:
			expr = typed.X
		case *ast.SelectorExpr:
			return typed, true
		case *ast.Ident:
			if unnamedResultTypes[typed.Name] {
				return nil, false
			}
			return typed, true
		default:
			return nil, false
		}
	}
}

func markedKind(result ast.Expr, aliases map[string]string, markers map[string]string) (string, bool) {
	selector, isSelector := result.(*ast.SelectorExpr)
	if !isSelector {
		return "", false
	}
	qualifier, isIdent := selector.X.(*ast.Ident)
	if !isIdent {
		return "", false
	}
	path, known := aliases[qualifier.Name]
	if !known {
		return "", false
	}
	kind, marked := markers[path+"."+selector.Sel.Name]
	return kind, marked
}

func contributionFingerprint(symbol, kind, signature string) string {
	sum := sha256.New()
	for _, part := range []string{symbol, kind, signature} {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func buildModuleManifest(surface *moduleSurface, prior *moduleManifestDocument, order int, manifestName string) (*moduleManifestDocument, error) {
	previous := map[string]moduleManifestContribution{}
	if prior != nil {
		for _, contribution := range prior.Contributions {
			previous[contribution.Symbol] = contribution
		}
	}
	document := &moduleManifestDocument{
		Format:        moduleFormat,
		GeneratedBy:   moduleGeneratedBy,
		Package:       surface.pkg,
		Module:        surface.name,
		Order:         order,
		Contributions: make([]moduleManifestContribution, 0, len(surface.candidates)),
	}
	for _, candidate := range surface.candidates {
		entry := moduleManifestContribution{
			Symbol:      candidate.symbol,
			Kind:        candidate.kind,
			Source:      sourceFromSignature,
			Fingerprint: candidate.fingerprint,
		}
		carried, hadPrior := previous[candidate.symbol]
		if hadPrior {
			entry.Excluded = carried.Excluded
			if carried.Fingerprint == entry.Fingerprint {
				if carried.Kind != "" && carried.Kind != entry.Kind {
					if !knownModuleKind(carried.Kind) {
						return nil, fmt.Errorf("codegen: %s gives %s the kind %q, which is not one of %s",
							manifestName, candidate.symbol, carried.Kind, moduleKindNames())
					}
					entry.Kind, entry.Source = carried.Kind, sourceFromManifest
				}
				entry.Confirmed = carried.Confirmed
			}
		}
		if entry.Excluded {
			entry.Confirmed = false
		}
		document.Contributions = append(document.Contributions, entry)
	}
	if len(includedContributions(document)) == 0 {
		return nil, fmt.Errorf("codegen: every contribution in %s is excluded, and a module that contributes nothing is not a module", manifestName)
	}
	return document, nil
}

func includedContributions(document *moduleManifestDocument) []moduleManifestContribution {
	out := make([]moduleManifestContribution, 0, len(document.Contributions))
	for _, contribution := range document.Contributions {
		if !contribution.Excluded {
			out = append(out, contribution)
		}
	}
	return out
}

func unconfirmedContributions(document *moduleManifestDocument) []string {
	out := []string{}
	for _, contribution := range includedContributions(document) {
		if !contribution.Confirmed {
			out = append(out, contribution.Symbol)
		}
	}
	sort.Strings(out)
	return out
}

func readModuleManifest(path, pkg string) (*moduleManifestDocument, error) {
	source, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("codegen: read %s: %w", path, err)
	}
	var document moduleManifestDocument
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("codegen: refusing to overwrite an unrelated manifest %s: %w", path, err)
	}
	if document.Format != moduleFormat || document.GeneratedBy != moduleGeneratedBy {
		return nil, fmt.Errorf("codegen: refusing to overwrite an unrelated manifest %s", path)
	}
	if document.Package != pkg {
		return nil, fmt.Errorf("codegen: %s belongs to package %q, not %q", path, document.Package, pkg)
	}
	seen := map[string]bool{}
	for _, contribution := range document.Contributions {
		if contribution.Symbol == "" {
			return nil, fmt.Errorf("codegen: %s carries an unnamed contribution", path)
		}
		if seen[contribution.Symbol] {
			return nil, fmt.Errorf("codegen: %s carries %s twice", path, contribution.Symbol)
		}
		seen[contribution.Symbol] = true
	}
	return &document, nil
}

func marshalModuleManifest(document *moduleManifestDocument) ([]byte, error) {
	source, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("codegen: encode manifest: %w", err)
	}
	return append(source, '\n'), nil
}

func validateModuleManifestTarget(path string) error {
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
	if json.Unmarshal(current, &header) != nil || header.GeneratedBy != moduleGeneratedBy {
		return fmt.Errorf("codegen: refusing to overwrite an unrelated manifest %s", path)
	}
	return nil
}

func validateModuleDeclarations(dir, pkg, skip string) error {
	declared, err := packageDeclarationNames(dir, pkg, skip)
	if err != nil {
		return err
	}
	if declared[moduleVariable] {
		return fmt.Errorf("codegen: package %s already declares %s, which the generated module file also declares", pkg, moduleVariable)
	}
	return nil
}

func unconfirmedModule(pkg string) []byte {
	return []byte(fmt.Sprintf("%s\n\npackage %s\n\ntype vvModule []struct{}\n\nvar %s vvModule = %q\n",
		generatedHeader, pkg, moduleVariable, moduleHint))
}

func renderModule(surface *moduleSurface, document *moduleManifestDocument, modulePkg string) ([]byte, error) {
	byKind := map[string][]string{}
	imports := map[string]string{moduleAlias: modulePkg}
	for _, contribution := range includedContributions(document) {
		byKind[contribution.Kind] = append(byKind[contribution.Kind], contribution.Symbol)
		alias, _, qualified := strings.Cut(contribution.Symbol, ".")
		if !qualified {
			continue
		}
		path, known := surface.imports[alias]
		if !known {
			return nil, fmt.Errorf("codegen: %s names package %s, which is not a package under the module", contribution.Symbol, alias)
		}
		imports[alias] = path
	}
	aliases := make([]string, 0, len(imports))
	for alias := range imports {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	var out bytes.Buffer
	fmt.Fprintf(&out, "%s\n\npackage %s\n\nimport (\n", generatedHeader, surface.pkg)
	for _, alias := range aliases {
		fmt.Fprintf(&out, "\t%s %q\n", alias, imports[alias])
	}
	out.WriteString(")\n\n")
	fmt.Fprintf(&out, "var %s = %s.MustDefine(%s.Spec{\n\tName: %q,\n\tOrder: %d,\n",
		moduleVariable, moduleAlias, moduleAlias, document.Module, document.Order)
	for _, known := range moduleKinds {
		symbols := byKind[known.kind]
		if len(symbols) == 0 {
			continue
		}
		fmt.Fprintf(&out, "\t%s: []any{\n", known.field)
		for _, symbol := range symbols {
			fmt.Fprintf(&out, "\t\t%s,\n", symbol)
		}
		out.WriteString("\t},\n")
	}
	out.WriteString("})\n")

	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("codegen: formatting the generated module file: %w", err)
	}
	return formatted, nil
}

func packagedDirs(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("codegen: reading %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("codegen: a module is generated for a directory, got %s", root)
	}
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
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			set[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("codegen: walking %s: %w", root, err)
	}
	dirs := make([]string, 0, len(set))
	for dir := range set {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs, nil
}

func moduleDirs(root string) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("codegen: resolving %s: %w", root, err)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("codegen: reading %s: %w", abs, err)
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() || ignoredDir(entry.Name()) || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(abs, entry.Name())
		holds, err := holdsGoFiles(dir)
		if err != nil {
			return nil, err
		}
		if holds {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func holdsGoFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("codegen: reading %s: %w", dir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			return true, nil
		}
	}
	return false, nil
}

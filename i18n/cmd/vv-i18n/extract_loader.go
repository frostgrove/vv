package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/i18n"
	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

const (
	maximumGoListBytes     = 128 << 20
	maximumGoListPackages  = 100000
	maximumMetadataWork    = 512 << 20
	maximumMetadataRecords = 100000
	maximumExportBytes     = 64 << 20
)

func createUsageGoCache() (string, func(), error) {
	directory, err := os.MkdirTemp("", "vv-i18n-gocache-")
	if err != nil {
		return "", nil, err
	}
	return directory, func() { _ = os.RemoveAll(directory) }, nil
}

var newUsageGoCache = createUsageGoCache

type goUsageEnvironment struct {
	GOOS         string
	GOARCH       string
	GOVersion    string
	GoExperiment string
	GoFlags      string
	GoWork       string
	GoEnv        string
	CgoEnabled   bool
	Toolchain    string
	Settings     []i18n.UsageSetting
}

type goUsageSelection struct {
	root        string
	relative    string
	logicalPath string
	readPath    string
	packagePath string
	selected    bool
}

type goUsageRootModel struct {
	path       string
	logical    string
	handle     *os.Root
	identity   fs.FileInfo
	selections map[string]goUsageSelection
	nested     map[string]bool
	goMod      []byte
	goSum      []byte
	vendored   bool
}

type goUsageLoader struct {
	environment       goUsageEnvironment
	roots             map[string]*goUsageRootModel
	packagePaths      map[string]string
	importMaps        map[string]map[string]string
	exports           map[string]string
	metadata          []i18n.UsageMetadata
	complete          bool
	metadataWork      int64
	goListFacts       []string
	overlay           map[string]string
	overlayRootBound  bool
	cacheDir          string
	cacheCleanup      func()
	buildTags         []string
	overrideTags      bool
	modMode           string
	modFilePath       string
	alternateMod      []byte
	alternateSum      []byte
	workFilePath      string
	workFileOverride  string
	workspaceVendor   bool
	metadataSeen      map[string]string
	resolvedSeen      map[string]bool
	packageFacts      map[string]string
	exportDigests     map[string]string
	physicalMetadata  map[string]physicalMetadata
	environmentValues map[string]string
}

type physicalMetadata struct {
	digest [sha256.Size]byte
	size   int64
	exists bool
}

type goListPackage struct {
	Dir             string
	ImportPath      string
	Name            string
	Export          string
	BuildID         string
	Standard        bool
	Goroot          bool
	DepOnly         bool
	Incomplete      bool
	Match           []string
	GoFiles         []string
	CgoFiles        []string
	CompiledGoFiles []string
	IgnoredGoFiles  []string
	TestGoFiles     []string
	XTestGoFiles    []string
	CFiles          []string
	CXXFiles        []string
	MFiles          []string
	HFiles          []string
	FFiles          []string
	SFiles          []string
	SwigFiles       []string
	SwigCXXFiles    []string
	SysoFiles       []string
	EmbedFiles      []string
	Imports         []string
	ImportMap       map[string]string
	Module          *goListModule
	Error           *goListError
	DepsErrors      []goListError
}

type goListModule struct {
	Path      string
	Version   string
	Dir       string
	GoMod     string
	GoVersion string
	Sum       string
	GoModSum  string
	Main      bool
	Indirect  bool
	Replace   *goListModule
}

type goListError struct {
	Err string
}

type boundedCommandBuffer struct {
	bytes.Buffer
	maximum int
}

func (b *boundedCommandBuffer) Write(value []byte) (int, error) {
	if len(value) > b.maximum-b.Len() {
		return 0, fmt.Errorf("command output exceeds %d bytes", b.maximum)
	}
	return b.Buffer.Write(value)
}

func loadGoUsage(ctx context.Context, roots []extractSourceRoot, buildTags []string, complete bool) (*goUsageLoader, error) {
	cacheDir, cleanup, err := newUsageGoCache()
	if err != nil {
		return nil, fmt.Errorf("create private Go build cache: %w", err)
	}
	loader := &goUsageLoader{
		roots: make(map[string]*goUsageRootModel), packagePaths: make(map[string]string), importMaps: make(map[string]map[string]string),
		exports: make(map[string]string), complete: complete, overlay: make(map[string]string), cacheDir: cacheDir,
		metadataSeen: make(map[string]string), resolvedSeen: make(map[string]bool), packageFacts: make(map[string]string), exportDigests: make(map[string]string),
		physicalMetadata: make(map[string]physicalMetadata),
	}
	loader.cacheCleanup = cleanup
	loader.overrideTags = buildTags != nil
	if err := loader.loadEnvironment(ctx, roots, buildTags); err != nil {
		loader.close()
		return nil, err
	}
	for index := range roots {
		root := &roots[index]
		if !root.directory {
			loader.complete = false
			continue
		}
		model, err := loader.openModuleRoot(ctx, root)
		if err != nil {
			loader.close()
			return nil, err
		}
		if model == nil {
			loader.complete = false
			continue
		}
		root.logical = model.logical
		loader.roots[root.path] = model
		if err := loader.loadRoot(ctx, model); err != nil {
			loader.close()
			return nil, err
		}
	}
	if err := loader.addGraphMetadata(); err != nil {
		loader.close()
		return nil, err
	}
	slices.SortFunc(loader.metadata, compareUsageMetadata)
	for index := 1; index < len(loader.metadata); index++ {
		if loader.metadata[index].Kind == loader.metadata[index-1].Kind && loader.metadata[index].Path == loader.metadata[index-1].Path {
			loader.close()
			return nil, fmt.Errorf("usage metadata identity %q/%q is repeated", loader.metadata[index].Kind, loader.metadata[index].Path)
		}
	}
	if err := loader.verifyMetadata(ctx); err != nil {
		loader.close()
		return nil, err
	}
	return loader, nil
}

func (l *goUsageLoader) close() {
	for _, root := range l.roots {
		_ = root.handle.Close()
	}
	if l.cacheCleanup != nil {
		l.cacheCleanup()
		l.cacheCleanup = nil
		l.cacheDir = ""
	}
}

func (l *goUsageLoader) selection(path string) (goUsageSelection, bool) {
	path = filepath.Clean(path)
	for _, root := range l.roots {
		selection, ok := root.selections[path]
		if ok {
			return selection, true
		}
	}
	return goUsageSelection{}, false
}

func (l *goUsageLoader) containingRoot(path string) *goUsageRootModel {
	path = filepath.Clean(path)
	var match *goUsageRootModel
	for _, root := range l.roots {
		if pathWithin(root.path, path) && (match == nil || len(root.path) > len(match.path)) {
			match = root
		}
	}
	return match
}

func usageGoEnvironmentNames() []string {
	return []string{
		"GOOS", "GOARCH", "GOVERSION", "GOEXPERIMENT", "GOFLAGS", "GOWORK", "GOENV", "CGO_ENABLED", "GO111MODULE",
		"GO386", "GOAMD64", "GOARM", "GOARM64", "GOMIPS", "GOMIPS64", "GOMIPS64LE", "GOMIPSLE", "GOPPC64", "GORISCV64", "GOWASM",
		"CC", "CXX", "FC", "PKG_CONFIG", "GOROOT", "GOTOOLDIR", "GOTOOLCHAIN", "GOPROXY", "GOSUMDB",
	}
}

func (l *goUsageLoader) loadEnvironment(ctx context.Context, roots []extractSourceRoot, buildTags []string) error {
	directory := "."
	for _, root := range roots {
		if root.directory {
			directory = root.path
			break
		}
	}
	names := usageGoEnvironmentNames()
	arguments := append([]string{"env", "-json"}, names...)
	output, _, err := runBoundedGo(ctx, directory, l.cacheDir, arguments)
	if err != nil {
		return fmt.Errorf("resolve effective Go environment: %w", err)
	}
	var values map[string]string
	if err := stdjson.Unmarshal(output, &values); err != nil {
		return fmt.Errorf("decode effective Go environment: %w", err)
	}
	l.environmentValues = cloneStringMap(values)
	cgo, err := strconv.ParseBool(values["CGO_ENABLED"])
	if err != nil {
		return fmt.Errorf("decode CGO_ENABLED %q: %w", values["CGO_ENABLED"], err)
	}
	l.environment = goUsageEnvironment{
		GOOS: values["GOOS"], GOARCH: values["GOARCH"], GOVersion: values["GOVERSION"], GoExperiment: values["GOEXPERIMENT"],
		GoFlags: values["GOFLAGS"], GoWork: usageEnvironmentMode(values["GOWORK"]), GoEnv: usageEnvironmentMode(values["GOENV"]), CgoEnabled: cgo,
	}
	for _, name := range names {
		value := values[name]
		switch name {
		case "GOENV", "GOWORK":
			value = usageEnvironmentMode(value)
		case "CGO_ENABLED":
			value = strconv.FormatBool(cgo)
		}
		l.environment.Settings = append(l.environment.Settings, i18n.UsageSetting{Name: name, Value: value})
	}
	slices.SortFunc(l.environment.Settings, func(left, right i18n.UsageSetting) int { return strings.Compare(left.Name, right.Name) })
	l.environment.Toolchain = values["GOVERSION"] + " " + runtime.Compiler
	goPath, err := exec.LookPath("go")
	if err != nil {
		return err
	}
	resolvedGo, err := filepath.EvalSymlinks(goPath)
	if err != nil {
		return err
	}
	if err := l.addMetadataFile(ctx, "toolchain", "environment/go-command", resolvedGo, 64<<20); err != nil {
		return err
	}
	if path := values["GOENV"]; path != "" && path != "off" {
		if err := l.addOptionalMetadataFile(ctx, "environment", "environment/go.env", path); err != nil {
			return err
		}
	}
	if path := values["GOWORK"]; path != "" && path != "off" {
		content, err := readSecureRegularFile(ctx, path, maximumExtractMetadataBytes)
		if err != nil {
			return err
		}
		if err := l.addMetadata("workspace", "workspace/go.work", content); err != nil {
			return err
		}
		l.recordPhysical(path, content)
		sumPath := filepath.Join(filepath.Dir(path), "go.work.sum")
		sum, sumExists, err := l.readOptionalMetadata(ctx, "workspace", "workspace/go.work.sum", sumPath)
		if err != nil {
			return err
		}
		_, l.workspaceVendor, err = l.readOptionalMetadata(ctx, "vendor", "workspace/vendor/modules.txt", filepath.Join(filepath.Dir(path), "vendor", "modules.txt"))
		if err != nil {
			return err
		}
		l.workFilePath = filepath.Clean(path)
		if !l.workspaceVendor {
			if err := l.prepareWorkspaceShadow(content, sum, sumExists); err != nil {
				return err
			}
		}
	}
	flags, err := splitGoFlags(values["GOFLAGS"])
	if err != nil {
		l.complete = false
		return nil
	}
	effective := make(map[string]string)
	for index := 0; index < len(flags); index++ {
		name, value, consumed := goFlagValue(flags, index)
		if consumed {
			index++
		}
		switch name {
		case "toolexec":
			return errors.New("GOFLAGS -toolexec is not allowed during usage extraction")
		case "tags", "overlay", "modfile", "mod":
			effective[name] = value
		}
	}
	if value, ok := effective["tags"]; ok {
		parsed, parseErr := extractBuildTags(value, "", true)
		if parseErr != nil {
			l.complete = false
		} else {
			l.buildTags = parsed
		}
	}
	if value, ok := effective["overlay"]; ok {
		if value == "" {
			return errors.New("GOFLAGS -overlay requires a file")
		} else {
			path := value
			if !filepath.IsAbs(path) {
				l.overlayRootBound = true
				path = filepath.Join(directory, path)
			}
			if err := l.loadOverlay(ctx, filepath.Clean(path), directory); err != nil {
				return fmt.Errorf("load GOFLAGS overlay: %w", err)
			}
			directoryRoots := 0
			for _, root := range roots {
				if root.directory {
					directoryRoots++
				}
			}
			if l.overlayRootBound && directoryRoots > 1 {
				l.complete = false
			}
		}
	}
	if value, ok := effective["modfile"]; ok {
		if value == "" || !strings.HasSuffix(value, ".mod") {
			l.complete = false
		} else {
			path := value
			if !filepath.IsAbs(path) {
				path = filepath.Join(directory, path)
			}
			path = filepath.Clean(path)
			content, readErr := readSecureRegularFile(ctx, path, maximumExtractMetadataBytes)
			if readErr != nil {
				l.complete = false
			} else if l.workFilePath != "" {
				return errors.New("GOFLAGS -modfile cannot be modeled with an active Go workspace")
			} else {
				if err := l.addMetadata("module", "environment/alternate.go.mod", content); err != nil {
					return err
				}
				l.recordPhysical(path, content)
				l.modFilePath = path
				l.alternateMod = slices.Clone(content)
				sumPath := strings.TrimSuffix(path, ".mod") + ".sum"
				sum, exists, err := l.readOptionalMetadata(ctx, "module", "environment/alternate.go.sum", sumPath)
				if err != nil {
					return err
				}
				if exists {
					l.alternateSum = slices.Clone(sum)
				}
			}
		}
	}
	if value, ok := effective["mod"]; ok {
		l.modMode = value
		if value == "mod" {
			l.complete = false
		}
	}
	if buildTags != nil {
		l.buildTags = slices.Clone(buildTags)
	}
	return nil
}

func (l *goUsageLoader) verifyRootEnvironment(ctx context.Context, root *goUsageRootModel) error {
	names := usageGoEnvironmentNames()
	output, stderr, err := runBoundedGo(ctx, root.path, l.cacheDir, append([]string{"env", "-json"}, names...))
	if err != nil {
		return fmt.Errorf("resolve Go environment for %q: %w", root.path, err)
	}
	if len(bytes.TrimSpace(stderr)) != 0 {
		l.complete = false
	}
	var values map[string]string
	if err := stdjson.Unmarshal(output, &values); err != nil {
		return fmt.Errorf("decode Go environment for %q: %w", root.path, err)
	}
	var fact strings.Builder
	for _, name := range names {
		writeUsageFact(&fact, name)
		writeUsageFact(&fact, values[name])
		if values[name] != l.environmentValues[name] {
			l.complete = false
		}
	}
	return l.addMetadata("go_environment", root.logical+"/go-env", []byte(fact.String()))
}

func writeUsageFact(builder *strings.Builder, value string) {
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteByte(':')
	builder.WriteString(value)
}

func (l *goUsageLoader) readOptionalMetadata(ctx context.Context, kind, logical, path string) ([]byte, bool, error) {
	content, err := readSecureRegularFile(ctx, path, maximumExtractMetadataBytes)
	if errors.Is(err, fs.ErrNotExist) {
		l.physicalMetadata[filepath.Clean(path)] = physicalMetadata{exists: false}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := l.addMetadata(kind, logical, content); err != nil {
		return nil, false, err
	}
	l.recordPhysical(path, content)
	return content, true, nil
}

func (l *goUsageLoader) prepareWorkspaceShadow(content, sum []byte, sumExists bool) error {
	work, err := modfile.ParseWork(l.workFilePath, content, nil)
	if err != nil {
		return fmt.Errorf("parse active Go workspace: %w", err)
	}
	directory := filepath.Dir(l.workFilePath)
	uses := slices.Clone(work.Use)
	for _, use := range uses {
		if filepath.IsAbs(use.Path) {
			continue
		}
		absolute := filepath.Clean(filepath.Join(directory, filepath.FromSlash(use.Path)))
		if err := work.DropUse(use.Path); err != nil {
			return err
		}
		if err := work.AddUse(absolute, use.ModulePath); err != nil {
			return err
		}
	}
	replacements := slices.Clone(work.Replace)
	for _, replacement := range replacements {
		if replacement.New.Version != "" || filepath.IsAbs(replacement.New.Path) {
			continue
		}
		absolute := filepath.Clean(filepath.Join(directory, filepath.FromSlash(replacement.New.Path)))
		if err := work.DropReplace(replacement.Old.Path, replacement.Old.Version); err != nil {
			return err
		}
		if err := work.AddReplace(replacement.Old.Path, replacement.Old.Version, absolute, ""); err != nil {
			return err
		}
	}
	work.Cleanup()
	shadowDirectory := filepath.Join(l.cacheDir, "workspace")
	if err := os.MkdirAll(shadowDirectory, 0o700); err != nil {
		return err
	}
	shadow := filepath.Join(shadowDirectory, "go.work")
	if err := os.WriteFile(shadow, modfile.Format(work.Syntax), 0o600); err != nil {
		return err
	}
	if sumExists {
		if err := os.WriteFile(filepath.Join(shadowDirectory, "go.work.sum"), sum, 0o600); err != nil {
			return err
		}
	}
	l.workFileOverride = shadow
	return nil
}

func (l *goUsageLoader) prepareModuleShadow(root *goUsageRootModel) (string, error) {
	if l.workFilePath != "" {
		return "", nil
	}
	content := root.goMod
	sum := root.goSum
	if l.modFilePath != "" {
		content = l.alternateMod
		sum = l.alternateSum
	}
	digest := sha256.Sum256([]byte(root.path))
	directory := filepath.Join(l.cacheDir, "modules", hex.EncodeToString(digest[:8]))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, "usage.mod")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return "", err
	}
	if sum != nil {
		if err := os.WriteFile(filepath.Join(directory, "usage.sum"), sum, 0o600); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (l *goUsageLoader) openModuleRoot(ctx context.Context, source *extractSourceRoot) (*goUsageRootModel, error) {
	path := source.path
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, err := source.handle.OpenRoot(".")
	if err != nil {
		return nil, fmt.Errorf("open extraction root %q: %w", path, err)
	}
	identity, err := handle.Stat(".")
	if err != nil || !os.SameFile(source.identity, identity) {
		_ = handle.Close()
		return nil, fmt.Errorf("inspect extraction root %q: root identity changed", path)
	}
	content, err := readRootRegularFile(ctx, handle, "go.mod", maximumExtractMetadataBytes)
	if errors.Is(err, fs.ErrNotExist) {
		_ = handle.Close()
		return nil, nil
	}
	if err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("read module metadata in %q: %w", path, err)
	}
	modulePath := modfile.ModulePath(content)
	if modulePath == "" {
		_ = handle.Close()
		return nil, fmt.Errorf("extract module path from %q", filepath.Join(path, "go.mod"))
	}
	if err := module.CheckImportPath(modulePath); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("extract module path from %q: %w", filepath.Join(path, "go.mod"), err)
	}
	model := &goUsageRootModel{
		path: path, logical: modulePath, handle: handle, identity: identity, selections: make(map[string]goUsageSelection), nested: make(map[string]bool),
		goMod: slices.Clone(content),
	}
	if err := l.addMetadata("module", modulePath+"/go.mod", content); err != nil {
		_ = handle.Close()
		return nil, err
	}
	l.recordPhysical(filepath.Join(path, "go.mod"), content)
	if content, err := readRootRegularFile(ctx, handle, "go.sum", maximumExtractMetadataBytes); err == nil {
		if err := l.addMetadata("module", modulePath+"/go.sum", content); err != nil {
			_ = handle.Close()
			return nil, err
		}
		l.recordPhysical(filepath.Join(path, "go.sum"), content)
		model.goSum = slices.Clone(content)
	} else if errors.Is(err, fs.ErrNotExist) {
		l.physicalMetadata[filepath.Join(path, "go.sum")] = physicalMetadata{exists: false}
	} else {
		_ = handle.Close()
		return nil, err
	}
	if content, err := readRootRegularFile(ctx, handle, filepath.Join("vendor", "modules.txt"), maximumExtractMetadataBytes); err == nil {
		if err := l.addMetadata("vendor", modulePath+"/vendor/modules.txt", content); err != nil {
			_ = handle.Close()
			return nil, err
		}
		l.recordPhysical(filepath.Join(path, "vendor", "modules.txt"), content)
		model.vendored = true
	} else if errors.Is(err, fs.ErrNotExist) {
		l.physicalMetadata[filepath.Join(path, "vendor", "modules.txt")] = physicalMetadata{exists: false}
	} else {
		_ = handle.Close()
		return nil, err
	}
	return model, nil
}

func (l *goUsageLoader) loadRoot(ctx context.Context, root *goUsageRootModel) error {
	if err := l.verifyRootEnvironment(ctx, root); err != nil {
		return err
	}
	if err := l.discoverNestedModules(ctx, root); err != nil {
		return err
	}
	arguments := []string{"list", "-e", "-deps", "-compiled", "-export", "-json"}
	if l.overrideTags {
		arguments = append(arguments, "-tags="+strings.Join(l.buildTags, ","))
	}
	mode := l.modMode
	if mode == "" {
		if l.workspaceVendor || root.vendored {
			mode = "vendor"
		} else {
			mode = "readonly"
		}
	}
	if mode == "mod" {
		mode = "readonly"
	}
	if mode != "readonly" && mode != "vendor" {
		l.complete = false
		mode = "readonly"
	}
	arguments = append(arguments, "-mod="+mode)
	if l.workFilePath == "" && (mode == "readonly" || l.modFilePath != "") {
		shadow, err := l.prepareModuleShadow(root)
		if err != nil {
			return fmt.Errorf("prepare read-only module shadow for %q: %w", root.path, err)
		}
		arguments = append(arguments, "-modfile="+shadow)
	}
	arguments = append(arguments, "./...")
	extraEnvironment := []string(nil)
	if l.workFileOverride != "" {
		extraEnvironment = append(extraEnvironment, "GOWORK="+l.workFileOverride)
	}
	output, stderr, runErr := runBoundedGo(ctx, root.path, l.cacheDir, arguments, extraEnvironment...)
	packages, decodeErr := decodeGoList(output)
	if decodeErr != nil {
		l.complete = false
	}
	if detail := bytes.TrimSpace(stderr); len(detail) != 0 {
		l.complete = false
		if err := l.addMetadata("go_list_stderr", root.logical+"/go-list.stderr", detail); err != nil {
			return err
		}
	}
	if runErr != nil || len(packages) == 0 || decodeErr != nil {
		l.complete = false
		if len(packages) == 0 {
			return nil
		}
	}
	packageSet := make(map[string]goListPackage, len(packages))
	for _, pkg := range packages {
		if err := ctx.Err(); err != nil {
			return err
		}
		packageSet[pkg.ImportPath] = pkg
		if err := l.addResolvedModuleMetadata(ctx, pkg.Module); err != nil {
			l.complete = false
		}
		if pkg.Incomplete || pkg.Error != nil || len(pkg.DepsErrors) != 0 || pkg.ImportPath == "" {
			l.complete = false
		}
		if !pkg.DepOnly && len(pkg.CompiledGoFiles) == 0 {
			l.complete = false
		}
		exportDigest := ""
		if pkg.Export == "" && pkg.ImportPath != "unsafe" {
			l.complete = false
		} else if pkg.Export != "" {
			content, err := readSecureRegularFile(ctx, pkg.Export, maximumExportBytes)
			if err != nil {
				l.complete = false
			} else if int64(len(content)) > maximumMetadataWork-l.metadataWork {
				return fmt.Errorf("usage metadata exceeds %d bytes of work", maximumMetadataWork)
			} else {
				l.metadataWork += int64(len(content))
				digest := sha256.Sum256(content)
				exportDigest = hex.EncodeToString(digest[:])
			}
			l.exports[pkg.ImportPath] = pkg.Export
		}
		fact := goPackageFact(pkg, exportDigest)
		if prior, ok := l.packageFacts[pkg.ImportPath]; ok && prior != fact {
			l.complete = false
		} else {
			l.packageFacts[pkg.ImportPath] = fact
		}
		if prior, ok := l.exportDigests[pkg.ImportPath]; ok && prior != exportDigest {
			l.complete = false
		} else if exportDigest != "" {
			l.exportDigests[pkg.ImportPath] = exportDigest
		}
		l.goListFacts = append(l.goListFacts, fact)
		if pkg.DepOnly {
			continue
		}
		if len(pkg.CFiles)+len(pkg.CXXFiles)+len(pkg.MFiles)+len(pkg.HFiles)+len(pkg.FFiles)+len(pkg.SFiles)+
			len(pkg.SwigFiles)+len(pkg.SwigCXXFiles)+len(pkg.SysoFiles)+len(pkg.EmbedFiles) != 0 {
			l.complete = false
		}
		if !pathWithin(root.path, pkg.Dir) {
			l.complete = false
			continue
		}
		directory := filepath.Clean(pkg.Dir)
		l.packagePaths[directory] = pkg.ImportPath
		l.importMaps[pkg.ImportPath] = cloneStringMap(pkg.ImportMap)
		for _, name := range pkg.IgnoredGoFiles {
			l.addSelection(root, pkg, filepath.Join(pkg.Dir, name), false)
		}
		for _, name := range pkg.TestGoFiles {
			l.addSelection(root, pkg, filepath.Join(pkg.Dir, name), false)
		}
		for _, name := range pkg.XTestGoFiles {
			l.addSelection(root, pkg, filepath.Join(pkg.Dir, name), false)
		}
		compiled := make(map[string]bool)
		for _, name := range pkg.CompiledGoFiles {
			path := name
			if !filepath.IsAbs(path) {
				path = filepath.Join(pkg.Dir, path)
			}
			path = filepath.Clean(path)
			compiled[path] = true
			l.addSelection(root, pkg, path, true)
		}
		for _, name := range append(slices.Clone(pkg.GoFiles), pkg.CgoFiles...) {
			path := filepath.Clean(filepath.Join(pkg.Dir, name))
			if !compiled[path] {
				l.addSelection(root, pkg, path, len(pkg.CgoFiles) == 0)
			}
		}
		if len(pkg.CgoFiles) != 0 {
			l.complete = false
		}
	}
	for _, pkg := range packages {
		if !pkg.DepOnly || pkg.Standard || pkg.ImportPath == i18nPackagePath || pkg.ImportPath == errsPackagePath {
			continue
		}
		if packageReachesI18n(pkg.ImportPath, packageSet, make(map[string]bool)) {
			l.complete = false
		}
	}
	currentRoot, err := openStableAbsoluteRoot(ctx, root.path, false, nil)
	if err != nil {
		return fmt.Errorf("extraction root %q changed identity during go list", root.path)
	}
	current, statErr := currentRoot.Stat(".")
	closeErr := currentRoot.Close()
	if statErr != nil || closeErr != nil || !os.SameFile(root.identity, current) {
		return fmt.Errorf("extraction root %q changed identity during go list", root.path)
	}
	return nil
}

func (l *goUsageLoader) discoverNestedModules(ctx context.Context, root *goUsageRootModel) error {
	return fs.WalkDir(root.handle.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("source tree entry %q is a symbolic link", filepath.Join(root.path, relative))
		}
		if !entry.IsDir() {
			return nil
		}
		if relative != "." && ignoredExtractDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if relative == "." {
			return nil
		}
		moduleFile := filepath.Join(relative, "go.mod")
		content, err := readRootRegularFile(ctx, root.handle, moduleFile, maximumExtractMetadataBytes)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		logical := filepath.ToSlash(relative)
		root.nested[logical] = true
		if err := l.addMetadata("nested_module", root.logical+"/"+logical+"/go.mod", content); err != nil {
			return err
		}
		l.recordPhysical(filepath.Join(root.path, relative, "go.mod"), content)
		return filepath.SkipDir
	})
}

func (l *goUsageLoader) addSelection(root *goUsageRootModel, pkg goListPackage, source string, selected bool) {
	source = filepath.Clean(source)
	relative, err := filepath.Rel(root.path, source)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		hash := sha256.Sum256([]byte(pkg.ImportPath + "\x00" + filepath.Base(source)))
		relative = filepath.Join(".compiled", hex.EncodeToString(hash[:8]), filepath.Base(source))
	}
	relative = filepath.ToSlash(relative)
	selection := goUsageSelection{
		root: root.logical, relative: relative, logicalPath: root.logical + "/" + relative,
		readPath: l.overlayReadPath(source), packagePath: pkg.ImportPath, selected: selected,
	}
	if prior, ok := root.selections[source]; ok {
		selection.selected = selection.selected || prior.selected
		if prior.selected {
			selection.readPath = prior.readPath
		}
	}
	root.selections[source] = selection
}

func (l *goUsageLoader) openExport(ctx context.Context, importPath string) (io.ReadCloser, error) {
	path := l.exports[importPath]
	if path == "" {
		return nil, fmt.Errorf("go list provided no export data for %q", importPath)
	}
	expected := l.exportDigests[importPath]
	if expected == "" {
		return nil, fmt.Errorf("go list export data for %q has no verified digest", importPath)
	}
	content, err := readSecureRegularFile(ctx, path, maximumExportBytes)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != expected {
		return nil, fmt.Errorf("go list export data for %q changed during extraction", importPath)
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (l *goUsageLoader) overlayReadPath(path string) string {
	if replacement, ok := l.overlay[filepath.Clean(path)]; ok {
		return replacement
	}
	return path
}

func (l *goUsageLoader) loadOverlay(ctx context.Context, path, directory string) error {
	content, err := readSecureRegularFile(ctx, path, maximumExtractMetadataBytes)
	if err != nil {
		return err
	}
	var document struct {
		Replace map[string]string `json:"Replace"`
	}
	if err := jsonv2.Unmarshal(content, &document, jsonv2.RejectUnknownMembers(true), jsontext.AllowDuplicateNames(false)); err != nil {
		return fmt.Errorf("decode Go overlay %q: %w", path, err)
	}
	if len(document.Replace) > maximumExtractFiles {
		return fmt.Errorf("Go overlay exceeds %d replacements", maximumExtractFiles)
	}
	for target, replacement := range document.Replace {
		if target == "" {
			return fmt.Errorf("Go overlay %q has an empty target", path)
		}
		if !filepath.IsAbs(target) {
			l.overlayRootBound = true
			target = filepath.Join(directory, target)
		}
		if replacement != "" && !filepath.IsAbs(replacement) {
			l.overlayRootBound = true
			replacement = filepath.Join(directory, replacement)
		}
		if replacement != "" {
			replacement = filepath.Clean(replacement)
		}
		l.overlay[filepath.Clean(target)] = replacement
	}
	l.recordPhysical(path, content)
	return l.addMetadata("overlay", "environment/overlay.json", content)
}

func (l *goUsageLoader) addGraphMetadata() error {
	slices.Sort(l.goListFacts)
	content := []byte(strings.Join(l.goListFacts, "\n"))
	return l.addMetadata("module_graph", "graph/packages", content)
}

func (l *goUsageLoader) addMetadata(kind, path string, content []byte) error {
	if len(l.metadata) >= maximumMetadataRecords {
		return fmt.Errorf("usage metadata exceeds %d records", maximumMetadataRecords)
	}
	if int64(len(content)) > maximumMetadataWork-l.metadataWork {
		return fmt.Errorf("usage metadata exceeds %d bytes of work", maximumMetadataWork)
	}
	l.metadataWork += int64(len(content))
	digest := sha256.Sum256(content)
	path = filepath.ToSlash(path)
	digestText := hex.EncodeToString(digest[:])
	identity := kind + "\x00" + path
	if prior, ok := l.metadataSeen[identity]; ok {
		if prior != digestText {
			return fmt.Errorf("usage metadata %q changed during extraction", path)
		}
		return nil
	}
	l.metadataSeen[identity] = digestText
	l.metadata = append(l.metadata, i18n.UsageMetadata{Kind: kind, Path: path, SHA256: digestText})
	return nil
}

func (l *goUsageLoader) addResolvedModuleMetadata(ctx context.Context, value *goListModule) error {
	if value == nil {
		return nil
	}
	identity := value.Path + "\x00" + value.Version + "\x00" + value.GoMod
	if value.Replace != nil {
		identity += "\x00" + value.Replace.Path + "\x00" + value.Replace.Version + "\x00" + value.Replace.GoMod
	}
	if l.resolvedSeen[identity] {
		return nil
	}
	l.resolvedSeen[identity] = true
	logical := "modules/" + value.Path + "/go.mod"
	if value.GoMod != "" {
		if err := l.addMetadataFile(ctx, "resolved_module", logical, value.GoMod, maximumExtractMetadataBytes); err != nil {
			return err
		}
	}
	if value.Replace != nil {
		replacement := value.Replace
		if replacement.GoMod != "" {
			if err := l.addMetadataFile(ctx, "replacement", "modules/"+value.Path+"/replacement/go.mod", replacement.GoMod, maximumExtractMetadataBytes); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *goUsageLoader) addMetadataFile(ctx context.Context, kind, logical, path string, maximum int64) error {
	content, err := readSecureRegularFile(ctx, path, maximum)
	if err != nil {
		return err
	}
	l.recordPhysical(path, content)
	return l.addMetadata(kind, logical, content)
}

func (l *goUsageLoader) addOptionalMetadataFile(ctx context.Context, kind, logical, path string) error {
	content, err := readSecureRegularFile(ctx, path, maximumExtractMetadataBytes)
	if errors.Is(err, fs.ErrNotExist) {
		l.physicalMetadata[filepath.Clean(path)] = physicalMetadata{exists: false}
		return nil
	}
	if err != nil {
		return err
	}
	l.recordPhysical(path, content)
	return l.addMetadata(kind, logical, content)
}

func (l *goUsageLoader) recordPhysical(path string, content []byte) {
	l.physicalMetadata[filepath.Clean(path)] = physicalMetadata{digest: sha256.Sum256(content), size: int64(len(content)), exists: true}
}

func (l *goUsageLoader) verifyMetadata(ctx context.Context) error {
	paths := make([]string, 0, len(l.physicalMetadata))
	for path := range l.physicalMetadata {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		expected := l.physicalMetadata[path]
		if !expected.exists {
			root, name, _, err := openStableParent(ctx, path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("revalidate absent usage metadata %q: %w", path, err)
			}
			_, inspectErr := root.Lstat(name)
			closeErr := root.Close()
			if errors.Is(inspectErr, fs.ErrNotExist) && closeErr == nil {
				continue
			}
			if inspectErr != nil && !errors.Is(inspectErr, fs.ErrNotExist) {
				return fmt.Errorf("revalidate absent usage metadata %q: %w", path, inspectErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close usage metadata parent %q: %w", path, closeErr)
			}
			return fmt.Errorf("usage metadata %q appeared during extraction", path)
		}
		content, err := readSecureRegularFile(ctx, path, expected.size)
		if err != nil {
			return fmt.Errorf("revalidate usage metadata %q: %w", path, err)
		}
		if sha256.Sum256(content) != expected.digest {
			return fmt.Errorf("usage metadata %q changed during extraction", path)
		}
	}
	return nil
}

func compareUsageMetadata(left, right i18n.UsageMetadata) int {
	if value := strings.Compare(left.Kind, right.Kind); value != 0 {
		return value
	}
	if value := strings.Compare(left.Path, right.Path); value != 0 {
		return value
	}
	return strings.Compare(left.SHA256, right.SHA256)
}

func decodeGoList(raw []byte) ([]goListPackage, error) {
	decoder := stdjson.NewDecoder(bytes.NewReader(raw))
	packages := make([]goListPackage, 0)
	for {
		var pkg goListPackage
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			return packages, nil
		}
		if err != nil {
			return packages, err
		}
		packages = append(packages, pkg)
		if len(packages) > maximumGoListPackages {
			return packages[:maximumGoListPackages], fmt.Errorf("go list exceeds %d packages", maximumGoListPackages)
		}
	}
}

func runBoundedGo(ctx context.Context, directory, cacheDir string, arguments []string, environment ...string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = directory
	command.Env = goCommandEnvironment(cacheDir, environment...)
	stdout := &boundedCommandBuffer{maximum: maximumGoListBytes}
	stderr := &boundedCommandBuffer{maximum: 4 << 20}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, stderr.Bytes(), ctxErr
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

func goCommandEnvironment(cacheDir string, overrides ...string) []string {
	values := os.Environ()
	overridden := map[string]bool{"GOTOOLCHAIN": true, "GOPROXY": true, "GOSUMDB": true, "GOCACHE": true}
	for _, value := range overrides {
		name, _, _ := strings.Cut(value, "=")
		overridden[name] = true
	}
	result := make([]string, 0, len(values)+3)
	for _, value := range values {
		name, _, _ := strings.Cut(value, "=")
		if overridden[name] {
			continue
		}
		result = append(result, value)
	}
	result = append(result, "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOCACHE="+cacheDir)
	return append(result, overrides...)
}

func goPackageFact(pkg goListPackage, exportDigest string) string {
	values := []string{pkg.ImportPath, pkg.Name, pkg.BuildID, exportDigest, strconv.FormatBool(pkg.Standard), strconv.FormatBool(pkg.Goroot), strconv.FormatBool(pkg.DepOnly), strconv.FormatBool(pkg.Incomplete)}
	if pkg.Module != nil {
		values = append(values, moduleFact(pkg.Module))
	}
	imports := slices.Clone(pkg.Imports)
	slices.Sort(imports)
	values = append(values, imports...)
	mapKeys := make([]string, 0, len(pkg.ImportMap))
	for key := range pkg.ImportMap {
		mapKeys = append(mapKeys, key)
	}
	slices.Sort(mapKeys)
	for _, key := range mapKeys {
		values = append(values, "import-map", key, pkg.ImportMap[key])
	}
	if pkg.Error != nil {
		values = append(values, "error", pkg.Error.Err)
	}
	dependencyErrors := make([]string, len(pkg.DepsErrors))
	for index := range pkg.DepsErrors {
		dependencyErrors[index] = pkg.DepsErrors[index].Err
	}
	slices.Sort(dependencyErrors)
	for _, detail := range dependencyErrors {
		values = append(values, "dependency-error", detail)
	}
	return strings.Join(values, "\x00")
}

func moduleFact(value *goListModule) string {
	if value == nil {
		return ""
	}
	return strings.Join([]string{value.Path, value.Version, value.GoVersion, value.Sum, value.GoModSum, strconv.FormatBool(value.Main), strconv.FormatBool(value.Indirect), moduleFact(value.Replace)}, "\x01")
}

func packageReachesI18n(path string, packages map[string]goListPackage, visiting map[string]bool) bool {
	if path == i18nPackagePath || path == errsPackagePath {
		return true
	}
	if visiting[path] {
		return false
	}
	visiting[path] = true
	pkg, ok := packages[path]
	if !ok {
		return false
	}
	for _, imported := range pkg.Imports {
		if packageReachesI18n(imported, packages, visiting) {
			return true
		}
	}
	return false
}

func splitGoFlags(value string) ([]string, error) {
	var fields []string
	for len(value) != 0 {
		value = strings.TrimLeft(value, " \t\n\r")
		if value == "" {
			break
		}
		if value[0] == '\'' || value[0] == '"' {
			quote := value[0]
			value = value[1:]
			index := strings.IndexByte(value, quote)
			if index < 0 {
				return nil, fmt.Errorf("unterminated %c string", quote)
			}
			fields = append(fields, value[:index])
			value = value[index+1:]
			continue
		}
		index := strings.IndexAny(value, " \t\n\r")
		if index < 0 {
			fields = append(fields, value)
			break
		}
		fields = append(fields, value[:index])
		value = value[index:]
	}
	return fields, nil
}

func goFlagValue(fields []string, index int) (string, string, bool) {
	field := strings.TrimLeft(fields[index], "-")
	if name, value, ok := strings.Cut(field, "="); ok {
		return name, value, false
	}
	switch field {
	case "overlay", "modfile", "mod", "toolexec", "tags":
		if index+1 < len(fields) {
			return field, fields[index+1], true
		}
	}
	return field, "", false
}

func usageEnvironmentMode(value string) string {
	if value == "" || value == "off" {
		return "off"
	}
	return "active"
}

func readSecureRegularFile(ctx context.Context, path string, maximum int64) ([]byte, error) {
	root, name, absolute, err := openStableParent(ctx, path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input %q is not a regular file", absolute)
	}
	return readRootRegularFileWithIdentity(ctx, root, name, maximum, info)
}

func readRootRegularFile(ctx context.Context, root *os.Root, path string, maximum int64) ([]byte, error) {
	return readRootRegularFileWithIdentity(ctx, root, path, maximum, nil)
}

func readRootRegularFileWithIdentity(ctx context.Context, root *os.Root, path string, maximum int64, expected fs.FileInfo) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("read context is nil")
	}
	if root == nil {
		return nil, errors.New("read root is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parent, name, err := openStableRootedParent(ctx, root, path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if before.Mode()&fs.ModeSymlink != 0 || !before.Mode().IsRegular() || expected != nil && !os.SameFile(expected, before) {
		return nil, fmt.Errorf("%q is not a regular file", path)
	}
	file, err := parent.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, fmt.Errorf("%q is not a regular file", path)
	}
	if info.Size() > maximum {
		return nil, fmt.Errorf("%q exceeds %d bytes", path, maximum)
	}
	content, err := readContextBounded(ctx, file, maximum)
	if err != nil {
		return nil, err
	}
	after, err := parent.Lstat(name)
	if err != nil || !os.SameFile(before, after) || expected != nil && !os.SameFile(expected, after) {
		return nil, fmt.Errorf("%q changed identity while being read", path)
	}
	return content, nil
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func usageBuildContext(environment goUsageEnvironment, buildTags []string) build.Context {
	value := build.Default
	value.GOOS = environment.GOOS
	value.GOARCH = environment.GOARCH
	value.CgoEnabled = environment.CgoEnabled
	value.Compiler = runtime.Compiler
	value.BuildTags = slices.Clone(buildTags)
	value.ToolTags = slices.Clone(value.ToolTags)
	value.ReleaseTags = slices.Clone(value.ReleaseTags)
	return value
}

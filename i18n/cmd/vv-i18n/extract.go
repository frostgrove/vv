package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/i18n"
)

const (
	maximumExtractRoots         = 1024
	maximumExtractEntries       = 1000000
	maximumExtractFiles         = 100000
	maximumExtractFileBytes     = 4 << 20
	maximumExtractMetadataBytes = 16 << 20
	maximumExtractBytes         = 256 << 20
	maximumExtractTagBytes      = 4096
	maximumExtractTags          = 256
	maximumExtractLocations     = 1 << 18
)

type extractFinding struct {
	path   string
	line   int
	column int
	detail string
}

type extractState struct {
	keys            map[i18n.Key]bool
	dynamic         map[i18n.DynamicUsage]bool
	occurrences     map[i18n.UsageOccurrence]bool
	findings        []extractFinding
	parsed          []extractParsedFile
	fileset         *token.FileSet
	sourceRoots     []extractSourceRoot
	sourcePaths     map[string]string
	files           int
	bytes           int64
	complete        bool
	entries         int
	locationLimited bool
	buildTags       []string
	buildContext    build.Context
	inputs          []extractInput
	loader          *goUsageLoader
}

type extractParsedFile struct {
	path       string
	sourcePath string
	file       *ast.File
	generated  bool
}

type extractSourceRoot struct {
	path      string
	directory bool
	logical   string
	handle    *os.Root
	identity  fs.FileInfo
	name      string
}

type extractInput struct {
	path        string
	root        string
	relative    string
	logicalPath string
	readPath    string
	digest      [sha256.Size]byte
	selected    bool
}

func extractUsage(ctx context.Context, roots []string, complete bool) (i18n.UsageManifest, error) {
	return extractUsageWithTags(ctx, roots, complete, nil)
}

func extractUsageWithTags(ctx context.Context, roots []string, complete bool, buildTags []string) (i18n.UsageManifest, error) {
	if ctx == nil {
		return i18n.UsageManifest{}, fmt.Errorf("extract context is nil")
	}
	if len(roots) == 0 {
		roots = []string{"."}
	}
	if len(roots) > maximumExtractRoots {
		return i18n.UsageManifest{}, fmt.Errorf("extraction exceeds %d roots", maximumExtractRoots)
	}
	if len(buildTags) > maximumExtractTags {
		return i18n.UsageManifest{}, fmt.Errorf("extraction exceeds %d build tags", maximumExtractTags)
	}
	state := extractState{
		keys: make(map[i18n.Key]bool), dynamic: make(map[i18n.DynamicUsage]bool), occurrences: make(map[i18n.UsageOccurrence]bool),
		fileset: token.NewFileSet(), sourcePaths: make(map[string]string), complete: complete, buildTags: slices.Clone(buildTags),
	}
	defer state.closeSourceRoots()
	seenRoots := make(map[string]bool)
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return i18n.UsageManifest{}, err
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return i18n.UsageManifest{}, fmt.Errorf("resolve extraction root %q: %w", root, err)
		}
		if seenRoots[absolute] {
			continue
		}
		resolved, err := openExtractSourceRoot(ctx, absolute)
		if err != nil {
			return i18n.UsageManifest{}, fmt.Errorf("secure extraction root %q: %w", root, err)
		}
		state.sourceRoots = append(state.sourceRoots, resolved)
		seenRoots[absolute] = true
	}
	state.normalizeSourceRoots()
	loader, err := loadGoUsage(ctx, state.sourceRoots, buildTags, complete)
	if err != nil {
		return i18n.UsageManifest{}, err
	}
	state.loader = loader
	defer loader.close()
	state.complete = loader.complete
	state.buildTags = slices.Clone(loader.buildTags)
	state.buildContext = usageBuildContext(loader.environment, loader.buildTags)
	seen := make(map[string]bool)
	for _, root := range state.sourceRoots {
		if err := state.extractRoot(ctx, root, seen); err != nil {
			return i18n.UsageManifest{}, err
		}
	}
	for _, root := range loader.roots {
		paths := make([]string, 0, len(root.selections))
		for path, selection := range root.selections {
			if selection.selected && !seen[path] {
				paths = append(paths, path)
			}
		}
		slices.Sort(paths)
		for _, path := range paths {
			if err := state.extractFile(ctx, path, seen); err != nil {
				return i18n.UsageManifest{}, err
			}
		}
	}
	if err := state.analyze(ctx); err != nil {
		return i18n.UsageManifest{}, err
	}
	if len(state.findings) != 0 {
		slices.SortFunc(state.findings, func(left, right extractFinding) int {
			if value := strings.Compare(left.path, right.path); value != 0 {
				return value
			}
			if left.line != right.line {
				return left.line - right.line
			}
			if left.column != right.column {
				return left.column - right.column
			}
			return strings.Compare(left.detail, right.detail)
		})
		finding := state.findings[0]
		return i18n.UsageManifest{}, fmt.Errorf("%s:%d:%d: %s (%d extraction finding(s))", finding.path, finding.line, finding.column, finding.detail, len(state.findings))
	}
	keys := make([]i18n.Key, 0, len(state.keys))
	for key := range state.keys {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	dynamic := make([]i18n.DynamicUsage, 0, len(state.dynamic))
	for usage := range state.dynamic {
		dynamic = append(dynamic, usage)
	}
	slices.SortFunc(dynamic, func(left, right i18n.DynamicUsage) int {
		if value := strings.Compare(left.Domain, right.Domain); value != 0 {
			return value
		}
		return strings.Compare(left.Prefix, right.Prefix)
	})
	occurrences := make([]i18n.UsageOccurrence, 0, len(state.occurrences))
	for occurrence := range state.occurrences {
		occurrences = append(occurrences, occurrence)
	}
	slices.SortFunc(occurrences, compareUsageOccurrence)
	scope, err := state.usageScope(ctx)
	if err != nil {
		return i18n.UsageManifest{}, err
	}
	manifest := i18n.UsageManifest{Keys: keys, Dynamic: dynamic, Occurrences: occurrences, GoScope: scope, Complete: state.complete}
	manifest.ManifestDigest = i18n.ExpectedUsageManifestDigest(manifest)
	return manifest, nil
}

func openExtractSourceRoot(ctx context.Context, path string) (extractSourceRoot, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return extractSourceRoot{}, err
	}
	absolute = filepath.Clean(absolute)
	volumeRoot := filepath.VolumeName(absolute) + string(filepath.Separator)
	if absolute == filepath.Clean(volumeRoot) {
		handle, err := openStableAbsoluteRoot(ctx, absolute, false, nil)
		if err != nil {
			return extractSourceRoot{}, err
		}
		identity, err := handle.Stat(".")
		if err != nil {
			_ = handle.Close()
			return extractSourceRoot{}, err
		}
		return extractSourceRoot{path: absolute, directory: true, handle: handle, identity: identity}, nil
	}
	parent, name, _, err := openStableParent(ctx, absolute)
	if err != nil {
		return extractSourceRoot{}, err
	}
	identity, err := parent.Lstat(name)
	if err != nil {
		_ = parent.Close()
		return extractSourceRoot{}, err
	}
	if identity.Mode()&fs.ModeSymlink != 0 {
		_ = parent.Close()
		return extractSourceRoot{}, errors.New("extraction root is a symbolic link")
	}
	if identity.IsDir() {
		handle, err := parent.OpenRoot(name)
		if err != nil {
			_ = parent.Close()
			return extractSourceRoot{}, err
		}
		opened, err := handle.Stat(".")
		if err != nil || !os.SameFile(identity, opened) {
			_ = handle.Close()
			_ = parent.Close()
			return extractSourceRoot{}, errors.New("extraction root changed while opening")
		}
		if err := parent.Close(); err != nil {
			_ = handle.Close()
			return extractSourceRoot{}, err
		}
		return extractSourceRoot{path: absolute, directory: true, handle: handle, identity: identity}, nil
	}
	if !identity.Mode().IsRegular() {
		_ = parent.Close()
		return extractSourceRoot{}, errors.New("extraction root is not a file or directory")
	}
	file, err := parent.Open(name)
	if err != nil {
		_ = parent.Close()
		return extractSourceRoot{}, err
	}
	opened, err := file.Stat()
	closeErr := file.Close()
	current, inspectErr := parent.Lstat(name)
	if err != nil || closeErr != nil || inspectErr != nil || !opened.Mode().IsRegular() || current.Mode()&fs.ModeSymlink != 0 || !os.SameFile(identity, opened) || !os.SameFile(identity, current) {
		_ = parent.Close()
		return extractSourceRoot{}, errors.New("extraction root changed while opening")
	}
	return extractSourceRoot{path: absolute, handle: parent, identity: identity, name: name}, nil
}

func (s *extractState) closeSourceRoots() {
	for index := range s.sourceRoots {
		if s.sourceRoots[index].handle != nil {
			_ = s.sourceRoots[index].handle.Close()
			s.sourceRoots[index].handle = nil
		}
	}
}

func (s *extractState) extractRoot(ctx context.Context, root extractSourceRoot, seen map[string]bool) error {
	if !root.directory {
		if filepath.Ext(root.path) != ".go" {
			return fmt.Errorf("extraction root %q is not a Go source file", root.path)
		}
		if err := s.accountEntry(); err != nil {
			return err
		}
		return s.extractFile(ctx, root.path, seen)
	}
	if model := s.loader.roots[root.path]; model != nil {
		err := fs.WalkDir(model.handle.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := s.accountEntry(); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("source tree entry %q is a symbolic link", filepath.Join(root.path, relative))
			}
			if entry.IsDir() && model.nested[filepath.ToSlash(relative)] {
				return filepath.SkipDir
			}
			if entry.IsDir() && relative != "." && ignoredExtractDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
				return nil
			}
			return s.extractFile(ctx, filepath.Join(root.path, relative), seen)
		})
		if err != nil {
			return fmt.Errorf("walk extraction root %q: %w", root.path, err)
		}
		current, err := os.Stat(root.path)
		if err != nil || !os.SameFile(model.identity, current) {
			return fmt.Errorf("extraction root %q changed identity during scan", root.path)
		}
		return nil
	}
	return filepath.WalkDir(root.path, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := s.accountEntry(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("source tree entry %q is a symbolic link", path)
		}
		if entry.IsDir() && path != root.path && ignoredExtractDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			return nil
		}
		return s.extractFile(ctx, path, seen)
	})
}

func (s *extractState) normalizeSourceRoots() {
	result := make([]extractSourceRoot, 0, len(s.sourceRoots))
	for index, root := range s.sourceRoots {
		covered := false
		for otherIndex, other := range s.sourceRoots {
			if index == otherIndex || !other.directory {
				continue
			}
			relative, err := filepath.Rel(other.path, root.path)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
				!crossesModuleBoundary(other.path, root.path, root.directory) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, root)
		}
	}
	slices.SortFunc(result, func(left, right extractSourceRoot) int { return strings.Compare(left.path, right.path) })
	s.sourceRoots = result
}

func crossesModuleBoundary(parent, child string, childDirectory bool) bool {
	current := child
	if !childDirectory {
		current = filepath.Dir(current)
	}
	for current != parent {
		if info, err := os.Lstat(filepath.Join(current, "go.mod")); err == nil {
			if info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
				return true
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return true
		}
		next := filepath.Dir(current)
		if next == current || !pathWithin(parent, next) {
			return true
		}
		current = next
	}
	return false
}

func compareUsageOccurrence(left, right i18n.UsageOccurrence) int {
	if value := strings.Compare(left.Path, right.Path); value != 0 {
		return value
	}
	if left.Line != right.Line {
		if left.Line < right.Line {
			return -1
		}
		return 1
	}
	if left.Column != right.Column {
		if left.Column < right.Column {
			return -1
		}
		return 1
	}
	if value := strings.Compare(string(left.Key), string(right.Key)); value != 0 {
		return value
	}
	if value := strings.Compare(left.Domain, right.Domain); value != 0 {
		return value
	}
	return strings.Compare(left.Prefix, right.Prefix)
}

func (s *extractState) accountEntry() error {
	s.entries++
	if s.entries > maximumExtractEntries {
		return fmt.Errorf("extraction exceeds %d filesystem entries", maximumExtractEntries)
	}
	return nil
}

func (s *extractState) extractFile(ctx context.Context, path string, seen map[string]bool) error {
	path = filepath.Clean(path)
	if seen[path] {
		return nil
	}
	seen[path] = true
	name := filepath.Base(path)
	selection := goUsageSelection{}
	modeled := false
	if s.loader != nil {
		selection, modeled = s.loader.selection(path)
	}
	readPath := path
	selected := false
	if modeled {
		readPath = selection.readPath
		selected = selection.selected
	} else if s.loader != nil && s.loader.containingRoot(path) != nil {
		root := s.loader.containingRoot(path)
		relative, err := filepath.Rel(root.path, path)
		if err != nil {
			return err
		}
		selection = goUsageSelection{
			root: root.logical, relative: filepath.ToSlash(relative), logicalPath: root.logical + "/" + filepath.ToSlash(relative),
			readPath: path,
		}
		modeled = true
	} else {
		selected = !strings.HasSuffix(name, "_test.go") && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "_")
	}
	if readPath == "" {
		if selected {
			return fmt.Errorf("selected Go overlay source %q was deleted", path)
		}
		readPath = path
	}
	content, err := readSecureRegularFile(ctx, readPath, maximumExtractFileBytes)
	if err != nil {
		return err
	}
	s.files++
	if s.files > maximumExtractFiles {
		return fmt.Errorf("extraction exceeds %d Go files", maximumExtractFiles)
	}
	if int64(len(content)) > maximumExtractBytes-s.bytes {
		return fmt.Errorf("extraction exceeds %d source bytes", maximumExtractBytes)
	}
	s.bytes += int64(len(content))
	if !modeled {
		if strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			selected = false
		} else {
			buildContext := s.buildContext
			buildContext.OpenFile = func(requested string) (io.ReadCloser, error) {
				if filepath.Clean(requested) != path {
					return nil, fmt.Errorf("unexpected source path %q", requested)
				}
				return io.NopCloser(bytes.NewReader(content)), nil
			}
			selected, err = buildContext.MatchFile(filepath.Dir(path), filepath.Base(path))
			if err != nil {
				return fmt.Errorf("select Go source %q: %w", path, err)
			}
		}
		selection = s.fallbackSelection(path, selected)
	}
	s.inputs = append(s.inputs, extractInput{
		path: path, root: selection.root, relative: selection.relative, logicalPath: selection.logicalPath,
		readPath: readPath, digest: sha256.Sum256(content), selected: selected,
	})
	if !selected {
		return nil
	}
	parsed, err := parser.ParseFile(s.fileset, path, content, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	s.parsed = append(s.parsed, extractParsedFile{path: path, file: parsed, generated: ast.IsGenerated(parsed)})
	return nil
}

func (s *extractState) fallbackSelection(path string, selected bool) goUsageSelection {
	for index, root := range s.sourceRoots {
		var relative string
		if root.directory {
			candidate, err := filepath.Rel(root.path, path)
			if err != nil || candidate == ".." || strings.HasPrefix(candidate, ".."+string(filepath.Separator)) {
				continue
			}
			relative = filepath.ToSlash(candidate)
		} else if root.path == path {
			relative = filepath.Base(path)
		} else {
			continue
		}
		logicalRoot := root.logical
		if logicalRoot == "" {
			logicalRoot = "root-" + strconv.Itoa(index+1)
		}
		return goUsageSelection{root: logicalRoot, relative: relative, logicalPath: logicalRoot + "/" + relative, readPath: path, selected: selected}
	}
	return goUsageSelection{root: "root-1", relative: filepath.Base(path), logicalPath: "root-1/" + filepath.Base(path), readPath: path, selected: selected}
}

func (s *extractState) usageScope(ctx context.Context) (*i18n.GoUsageScope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	roots := make([]i18n.UsageRoot, len(s.sourceRoots))
	for index, root := range s.sourceRoots {
		kind := i18n.UsageRootFile
		if root.directory {
			kind = i18n.UsageRootDirectory
		}
		logical := root.logical
		if logical == "" {
			logical = "root-" + strconv.Itoa(index+1)
		}
		roots[index] = i18n.UsageRoot{Path: logical, Kind: kind}
	}
	slices.SortFunc(roots, func(left, right i18n.UsageRoot) int {
		if value := strings.Compare(left.Path, right.Path); value != 0 {
			return value
		}
		return strings.Compare(left.Kind.String(), right.Kind.String())
	})
	for index := 1; index < len(roots); index++ {
		if roots[index] == roots[index-1] {
			return nil, fmt.Errorf("extraction roots resolve to repeated logical root %q", roots[index].Path)
		}
	}
	inputs := slices.Clone(s.inputs)
	if err := s.loader.verifyMetadata(ctx); err != nil {
		return nil, err
	}
	for _, input := range inputs {
		content, err := readSecureRegularFile(ctx, input.readPath, maximumExtractFileBytes)
		if err != nil {
			return nil, fmt.Errorf("revalidate usage source %q: %w", input.logicalPath, err)
		}
		if sha256.Sum256(content) != input.digest {
			return nil, fmt.Errorf("usage source %q changed during extraction", input.logicalPath)
		}
	}
	slices.SortFunc(inputs, func(left, right extractInput) int {
		if value := strings.Compare(left.root, right.root); value != 0 {
			return value
		}
		if value := strings.Compare(left.relative, right.relative); value != 0 {
			return value
		}
		if left.selected != right.selected {
			if left.selected {
				return -1
			}
			return 1
		}
		return strings.Compare(hex.EncodeToString(left.digest[:]), hex.EncodeToString(right.digest[:]))
	})
	for index := 1; index < len(inputs); index++ {
		if inputs[index].root == inputs[index-1].root && inputs[index].relative == inputs[index-1].relative {
			return nil, fmt.Errorf("usage source identity %q/%q is repeated", inputs[index].root, inputs[index].relative)
		}
	}
	environment := s.loader.environment
	scope := &i18n.GoUsageScope{
		Analyzer: i18n.GoUsageAnalyzerV2, GOOS: environment.GOOS, GOARCH: environment.GOARCH, Compiler: s.buildContext.Compiler,
		CgoEnabled: environment.CgoEnabled, GoVersion: environment.GOVersion, Toolchain: environment.Toolchain, GoExperiment: environment.GoExperiment,
		GoFlags: environment.GoFlags, GoWork: environment.GoWork, GoEnv: environment.GoEnv, BuildTags: slices.Clone(s.buildTags),
		ToolTags: slices.Clone(s.buildContext.ToolTags), ReleaseTags: slices.Clone(s.buildContext.ReleaseTags), Roots: roots,
		Environment: slices.Clone(environment.Settings), Metadata: slices.Clone(s.loader.metadata),
	}
	slices.Sort(scope.BuildTags)
	slices.Sort(scope.ToolTags)
	slices.Sort(scope.ReleaseTags)
	scope.BuildTags = slices.Compact(scope.BuildTags)
	scope.ToolTags = slices.Compact(scope.ToolTags)
	scope.ReleaseTags = slices.Compact(scope.ReleaseTags)
	for _, input := range inputs {
		scope.Files = append(scope.Files, i18n.UsageFile{
			Root: input.root, Path: input.relative, LogicalPath: input.logicalPath,
			SHA256: hex.EncodeToString(input.digest[:]), Selected: input.selected,
		})
		if input.selected {
			scope.SelectedFiles++
		} else {
			scope.ExcludedFiles++
		}
	}
	scope.SourceDigest = i18n.ExpectedUsageSourceDigest(*scope)
	return scope, nil
}

func ignoredExtractDirectory(name string) bool {
	if name == "vendor" || name == "testdata" || name == ".git" || name == ".agents" {
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

package cachegen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	cacheImportPath       = "github.com/frostgrove/vv/cache"
	generatedMarkerName   = "vvGeneratedCacheArtifact"
	generatedMarkerPrefix = "vv generate cache;format=1;current="
	manifestFormat        = 1
)

type Options struct {
	Dir      string
	Out      string
	Manifest string
	GOOS     string
	GOARCH   string
	Check    bool
	Log      io.Writer
}

type DriftError struct {
	Paths []string
}

func (this *DriftError) Error() string {
	return "cachegen: generated artifacts are stale: " + strings.Join(this.Paths, ", ")
}

type ConfirmationError struct {
	Caches []string
}

func (this *ConfirmationError) Error() string {
	return "cachegen: confirm scope in cache.manifest.yml for: " + strings.Join(this.Caches, ", ")
}

func Run(options *Options) error {
	configuration, err := normalizeOptions(options)
	if err != nil {
		return err
	}
	loaded, err := loadPackage(configuration.dir, configuration.out, configuration.target)
	if err != nil {
		return err
	}
	declarations, err := discover(loaded)
	if err != nil {
		return err
	}
	previous, err := readManifest(configuration.manifest, loaded.importPath)
	if err != nil {
		return err
	}
	marker, markerExists, err := readGeneratedMarker(configuration.out)
	if err != nil {
		return err
	}
	if err := validateCompatibilityAnchor(previous, marker, markerExists); err != nil {
		return err
	}
	if len(declarations) == 0 && previous == nil {
		return fmt.Errorf("cachegen: no package-level cache.Auto declarations found in %s", configuration.dir)
	}
	document, err := buildManifest(loaded, declarations, previous)
	if err != nil {
		return err
	}
	manifestSource, err := marshalManifest(document)
	if err != nil {
		return err
	}
	unconfirmed := unconfirmedCaches(document)
	goSource, err := render(loaded, declarations, document, len(unconfirmed) != 0, "")
	if err != nil {
		return err
	}
	recoveryGoSource := goSource
	if previous != nil && previous.CompatibilityProof != document.CompatibilityProof {
		recoveryGoSource, err = render(loaded, declarations, document, len(unconfirmed) != 0, previous.CompatibilityProof)
		if err != nil {
			return err
		}
	}
	if configuration.check {
		if err := checkArtifacts(configuration, goSource, manifestSource); err != nil {
			return err
		}
		if len(unconfirmed) != 0 {
			return &ConfirmationError{Caches: unconfirmed}
		}
		return nil
	}
	if err := validateManifestTarget(configuration.manifest); err != nil {
		return err
	}
	if err := validateGoTarget(configuration.out); err != nil {
		return err
	}
	if err := writeGo(configuration.out, recoveryGoSource); err != nil {
		return err
	}
	if err := writeManifest(configuration.manifest, manifestSource); err != nil {
		return err
	}
	if !bytes.Equal(recoveryGoSource, goSource) {
		if err := writeGo(configuration.out, goSource); err != nil {
			return err
		}
	}
	if configuration.log != nil {
		fmt.Fprintf(configuration.log, "vv: wrote %s and %s (%d caches)\n", configuration.out, configuration.manifest, len(document.Caches))
	}
	if len(unconfirmed) != 0 {
		return &ConfirmationError{Caches: unconfirmed}
	}
	return nil
}

type normalizedOptions struct {
	dir      string
	out      string
	manifest string
	target   buildTarget
	check    bool
	log      io.Writer
}

func normalizeOptions(options *Options) (normalizedOptions, error) {
	if options == nil {
		return normalizedOptions{}, fmt.Errorf("cachegen: options are nil")
	}
	dir := options.Dir
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return normalizedOptions{}, fmt.Errorf("cachegen: resolve directory: %w", err)
	}
	out, err := artifactPath(abs, options.Out, "vv_cache_gen.go", ".go")
	if err != nil {
		return normalizedOptions{}, err
	}
	if err := validateGoArtifactName(filepath.Base(out)); err != nil {
		return normalizedOptions{}, err
	}
	manifest, err := artifactPath(abs, options.Manifest, "cache.manifest.yml", ".yml")
	if err != nil {
		return normalizedOptions{}, err
	}
	target, err := resolveBuildTarget(abs, options.GOOS, options.GOARCH)
	if err != nil {
		return normalizedOptions{}, err
	}
	return normalizedOptions{dir: abs, out: out, manifest: manifest, target: target, check: options.Check, log: options.Log}, nil
}

type buildTarget struct {
	GOOS         string `json:"goos"`
	GOARCH       string `json:"goarch"`
	CGOEnabled   string `json:"cgo_enabled"`
	GOExperiment string `json:"goexperiment"`
	GoVersion    string `json:"go_version"`
	goosValues   []string
	goarchValues []string
}

func resolveBuildTarget(dir, goos, goarch string) (buildTarget, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "GOEXPERIMENT", "GOVERSION")
	command.Dir = dir
	command.Env = buildEnvironment(goos, goarch)
	output, err := command.Output()
	if err != nil {
		return buildTarget{}, fmt.Errorf("cachegen: resolve Go build target: %w", err)
	}
	var environment struct {
		GOOS         string
		GOARCH       string
		CGOEnabled   string `json:"CGO_ENABLED"`
		GOExperiment string `json:"GOEXPERIMENT"`
		GoVersion    string `json:"GOVERSION"`
	}
	if err := json.Unmarshal(output, &environment); err != nil {
		return buildTarget{}, fmt.Errorf("cachegen: decode Go build target: %w", err)
	}
	platforms, err := goPlatformInventory()
	if err != nil {
		return buildTarget{}, err
	}
	if environment.GOOS == "" || environment.GOARCH == "" || types.SizesFor("gc", environment.GOARCH) == nil || !platforms.targets[environment.GOOS+"/"+environment.GOARCH] {
		return buildTarget{}, fmt.Errorf("cachegen: unsupported Go build target %s/%s", environment.GOOS, environment.GOARCH)
	}
	return buildTarget{
		GOOS:         environment.GOOS,
		GOARCH:       environment.GOARCH,
		CGOEnabled:   environment.CGOEnabled,
		GOExperiment: environment.GOExperiment,
		GoVersion:    environment.GoVersion,
		goosValues:   platforms.goos,
		goarchValues: platforms.goarch,
	}, nil
}

func buildEnvironment(goos, goarch string) []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if goos != "" && strings.HasPrefix(entry, "GOOS=") || goarch != "" && strings.HasPrefix(entry, "GOARCH=") {
			continue
		}
		result = append(result, entry)
	}
	if goos != "" {
		result = append(result, "GOOS="+goos)
	}
	if goarch != "" {
		result = append(result, "GOARCH="+goarch)
	}
	return result
}

func validateGoArtifactName(name string) error {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_test.go") {
		return fmt.Errorf("cachegen: generated Go file %q would be ignored by ordinary builds", name)
	}
	platforms, err := goPlatformSuffixes()
	if err != nil {
		return err
	}
	stem := strings.TrimSuffix(name, ".go")
	parts := strings.Split(stem, "_")
	if len(parts) > 1 && platforms[parts[len(parts)-1]] {
		return fmt.Errorf("cachegen: generated Go file %q is platform-specific", name)
	}
	return nil
}

func goPlatformSuffixes() (map[string]bool, error) {
	platforms, err := goPlatformInventory()
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(platforms.goos)+len(platforms.goarch))
	for _, value := range append(append([]string(nil), platforms.goos...), platforms.goarch...) {
		result[value] = true
	}
	return result, nil
}

type goPlatforms struct {
	targets map[string]bool
	goos    []string
	goarch  []string
}

func goPlatformInventory() (goPlatforms, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "tool", "dist", "list")
	output, err := command.Output()
	if err != nil {
		return goPlatforms{}, fmt.Errorf("cachegen: list Go platforms: %w", err)
	}
	result := goPlatforms{targets: map[string]bool{}}
	goos := map[string]bool{}
	goarch := map[string]bool{}
	for _, line := range strings.Fields(string(output)) {
		parts := strings.Split(line, "/")
		if len(parts) == 2 {
			result.targets[line] = true
			goos[parts[0]] = true
			goarch[parts[1]] = true
		}
	}
	for value := range goos {
		result.goos = append(result.goos, value)
	}
	for value := range goarch {
		result.goarch = append(result.goarch, value)
	}
	sort.Strings(result.goos)
	sort.Strings(result.goarch)
	return result, nil
}

func artifactPath(dir, value, fallback, suffix string) (string, error) {
	if value == "" {
		value = fallback
	}
	if filepath.IsAbs(value) || filepath.Base(value) != value || value == "." || value == ".." || !strings.HasSuffix(value, suffix) {
		return "", fmt.Errorf("cachegen: artifact must be a %s file name without directories, got %q", suffix, value)
	}
	return filepath.Join(dir, value), nil
}

func checkArtifacts(configuration normalizedOptions, goSource, manifestSource []byte) error {
	paths := make([]string, 0, 2)
	for _, artifact := range []struct {
		path string
		want []byte
	}{{configuration.out, goSource}, {configuration.manifest, manifestSource}} {
		actual, err := os.ReadFile(artifact.path)
		if err != nil || !bytes.Equal(actual, artifact.want) {
			paths = append(paths, artifact.path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	return &DriftError{Paths: paths}
}

func writeGo(path string, source []byte) error {
	if err := validateGoTarget(path); err != nil {
		return err
	}
	return writeAtomic(path, source)
}

func validateGoTarget(path string) error {
	if err := validateArtifactKind(path); err != nil {
		return err
	}
	_, exists, err := readGeneratedMarker(path)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil && !exists {
		return fmt.Errorf("cachegen: refusing to overwrite authored file %s", path)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("cachegen: inspect %s: %w", path, statErr)
	}
	return nil
}

type generatedMarker struct {
	Current  string
	Previous string
}

func markerValue(current, previous string) string {
	return generatedMarkerPrefix + current + ";previous=" + previous
}

func readGeneratedMarker(path string) (generatedMarker, bool, error) {
	source, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return generatedMarker{}, false, nil
	}
	if err != nil {
		return generatedMarker{}, false, fmt.Errorf("cachegen: inspect %s: %w", path, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return generatedMarker{}, false, nil
	}
	value := ""
	count := 0
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, raw := range general.Specs {
			spec, ok := raw.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range spec.Names {
				if name.Name != generatedMarkerName || index >= len(spec.Values) {
					continue
				}
				literal, ok := spec.Values[index].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return generatedMarker{}, false, nil
				}
				decoded, err := strconv.Unquote(literal.Value)
				if err != nil {
					return generatedMarker{}, false, nil
				}
				value = decoded
				count++
			}
		}
	}
	if count != 1 || !strings.HasPrefix(value, generatedMarkerPrefix) {
		return generatedMarker{}, false, nil
	}
	parts := strings.Split(strings.TrimPrefix(value, generatedMarkerPrefix), ";previous=")
	if len(parts) != 2 || !validProof(parts[0]) || parts[1] != "" && !validProof(parts[1]) {
		return generatedMarker{}, false, nil
	}
	return generatedMarker{Current: parts[0], Previous: parts[1]}, true, nil
}

func validateCompatibilityAnchor(previous *manifestDocument, marker generatedMarker, markerExists bool) error {
	if previous == nil {
		if markerExists {
			return fmt.Errorf("cachegen: generated Go artifact exists without its cache manifest")
		}
		return nil
	}
	if !markerExists {
		return fmt.Errorf("cachegen: refusing to trust cache compatibility metadata without the generated Go anchor")
	}
	if previous.CompatibilityProof != marker.Current && previous.CompatibilityProof != marker.Previous {
		return fmt.Errorf("cachegen: cache manifest compatibility metadata does not match the generated Go anchor")
	}
	if previous.CompatibilityProof != manifestCompatibilityProof(*previous) {
		return fmt.Errorf("cachegen: cache manifest compatibility proof is stale")
	}
	return nil
}

func validProof(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func writeManifest(path string, source []byte) error {
	if err := validateManifestTarget(path); err != nil {
		return err
	}
	return writeAtomic(path, source)
}

func validateManifestTarget(path string) error {
	if err := validateArtifactKind(path); err != nil {
		return err
	}
	if current, err := os.ReadFile(path); err == nil {
		var header struct {
			GeneratedBy string `json:"generated_by"`
		}
		if json.Unmarshal(current, &header) != nil || header.GeneratedBy != "vv generate cache" {
			return fmt.Errorf("cachegen: refusing to overwrite an unrelated manifest %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cachegen: inspect %s: %w", path, err)
	}
	return nil
}

func validateArtifactKind(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cachegen: inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("cachegen: artifact %s must be a regular file, not a symlink", path)
	}
	return nil
}

func writeAtomic(path string, source []byte) (err error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("cachegen: create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(source); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	handle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("cachegen: open artifact directory: %w", err)
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return fmt.Errorf("cachegen: sync artifact directory: %w", err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("cachegen: close artifact directory: %w", err)
	}
	return nil
}

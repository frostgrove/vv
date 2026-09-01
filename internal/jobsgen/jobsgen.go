package jobsgen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const generatedMarker = "vv generate jobs;format=1"

type Options struct {
	Dir      string
	Out      string
	Manifest string
	Check    bool
	Log      io.Writer
}

type DriftError struct {
	Paths []string
}

func (this *DriftError) Error() string {
	return "jobsgen: generated artifacts are stale: " + strings.Join(this.Paths, ", ")
}

type configuration struct {
	dir      string
	out      string
	manifest string
	check    bool
	log      io.Writer
}

func Run(options *Options) error {
	config, err := normalizeOptions(options)
	if err != nil {
		return err
	}
	loaded, err := loadPackage(config.dir, config.out)
	if err != nil {
		return err
	}
	declarations, err := discover(loaded)
	if err != nil {
		return err
	}
	previous, err := readManifest(config.manifest, loaded.importPath)
	if err != nil {
		return err
	}
	if len(declarations) == 0 && previous == nil {
		return fmt.Errorf("jobsgen: no package-level jobs.Auto, jobs.Declare, jobsfx.Auto, or jobsfx.AutoAdapter declarations found in %s", config.dir)
	}
	document, err := buildManifest(loaded, declarations, previous)
	if err != nil {
		return err
	}
	manifestSource, err := marshalManifest(document)
	if err != nil {
		return err
	}
	goSource, err := render(loaded, declarations, document)
	if err != nil {
		return err
	}
	if config.check {
		return checkArtifacts(config, goSource, manifestSource)
	}
	if err := validateGoTarget(config.out); err != nil {
		return err
	}
	if err := writeAtomic(config.out, goSource); err != nil {
		return err
	}
	if err := writeAtomic(config.manifest, manifestSource); err != nil {
		return err
	}
	if config.log != nil {
		fmt.Fprintf(config.log, "vv: wrote %s and %s (%d jobs)\n", config.out, config.manifest, len(document.Jobs))
	}
	return nil
}

func normalizeOptions(options *Options) (configuration, error) {
	if options == nil {
		return configuration{}, fmt.Errorf("jobsgen: options are nil")
	}
	dir := options.Dir
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return configuration{}, fmt.Errorf("jobsgen: resolve directory: %w", err)
	}
	out, err := artifactPath(abs, options.Out, "vv_jobs_gen.go", ".go")
	if err != nil {
		return configuration{}, err
	}
	manifest, err := artifactPath(abs, options.Manifest, "jobs.manifest.yml", ".yml")
	if err != nil {
		return configuration{}, err
	}
	return configuration{dir: abs, out: out, manifest: manifest, check: options.Check, log: options.Log}, nil
}

func artifactPath(dir, value, fallback, suffix string) (string, error) {
	if value == "" {
		value = fallback
	}
	if filepath.IsAbs(value) || filepath.Base(value) != value || value == "." || value == ".." || !strings.HasSuffix(value, suffix) {
		return "", fmt.Errorf("jobsgen: artifact must be a %s file name without directories, got %q", suffix, value)
	}
	return filepath.Join(dir, value), nil
}

func checkArtifacts(config configuration, goSource, manifestSource []byte) error {
	paths := make([]string, 0, 2)
	for _, artifact := range []struct {
		path string
		want []byte
	}{{config.out, goSource}, {config.manifest, manifestSource}} {
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

func validateGoTarget(path string) error {
	source, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("jobsgen: inspect %s: %w", path, err)
	}
	if !bytes.Contains(source, []byte("const _vvGeneratedJobsArtifact = \""+generatedMarker+"\"")) {
		return fmt.Errorf("jobsgen: refusing to overwrite authored file %s", path)
	}
	return nil
}

func writeAtomic(path string, source []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".vv-jobs-*")
	if err != nil {
		return fmt.Errorf("jobsgen: create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("jobsgen: set artifact permissions: %w", err)
	}
	if _, err := temporary.Write(source); err != nil {
		return fmt.Errorf("jobsgen: write artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("jobsgen: sync artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("jobsgen: close artifact: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("jobsgen: replace artifact: %w", err)
	}
	keep = true
	return nil
}

package scripts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/build"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const otelModulePath = "github.com/frostgrove/vv/otel"

func TestPublishedModulesOutsideOTelRemainOTelFree(t *testing.T) {
	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("cannot resolve the repository root: %v", err)
	}
	modules := publishedModuleDirectories(t, repository)
	identitiesValid := true
	for _, module := range modules {
		graph, err := productionModuleGraph(repository, module)
		if err != nil {
			t.Errorf("cannot select %s module identity: %v", moduleLabel(repository, module), err)
			identitiesValid = false
			continue
		}
		if _, err := validatePublishedModuleIdentity(repository, module, graph); err != nil {
			t.Errorf("cannot validate %s module identity: %v", moduleLabel(repository, module), err)
			identitiesValid = false
		}
	}
	if !identitiesValid {
		t.FailNow()
	}
	for _, imported := range productionImports(t, repository, func(relative string, entry os.DirEntry) bool {
		if relative == "." {
			return false
		}
		first, _, _ := strings.Cut(filepath.ToSlash(relative), "/")
		return entry.IsDir() && (first == "otel" || first == "test" || first == "_examples" || entry.Name() == "testdata" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), "."))
	}) {
		if openTelemetryImport(imported.path) {
			t.Errorf("%s imports forbidden OpenTelemetry package %s", imported.file, imported.path)
		}
	}
	cache := make(map[string]packageImports)
	for _, module := range modules {
		finding, err := transitiveProductionOTelImport(repository, module, cache)
		if err != nil {
			t.Errorf("cannot inspect %s production dependencies without the workspace: %v", moduleLabel(repository, module), err)
			continue
		}
		if finding != "" {
			t.Errorf("%s reaches forbidden OpenTelemetry production code: %s", moduleLabel(repository, module), finding)
		}
	}
}

func TestVVOTelProductionImportsOnlyAllowedOTelAPIs(t *testing.T) {
	for _, imported := range productionImports(t, "../otel", func(relative string, entry os.DirEntry) bool {
		return relative != "." && entry.IsDir() && (entry.Name() == "testdata" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), "."))
	}) {
		if allowedOTelProductionImport(imported.path) {
			continue
		}
		t.Errorf("%s imports a package outside the D-128 production allow-list: %s", imported.file, imported.path)
	}
}

func TestVVOTelProductionAllowListRejectsSDKExportersAndBridges(t *testing.T) {
	for _, path := range []string{
		"go.opentelemetry.io/otel",
		"go.opentelemetry.io/otel/sdk/trace",
		"go.opentelemetry.io/otel/exporters/otlp/otlptrace",
		"go.opentelemetry.io/contrib/bridges/otelslog",
		"github.com/uptrace/opentelemetry-go-extra/otelsql",
		"github.com/frostgrove/vv/app/appfx",
		"github.com/frostgrove/vv/auth/http/authfiber",
		"github.com/frostgrove/vv/crud/adapter/crudpgx",
		"github.com/frostgrove/vv/event",
		"github.com/frostgrove/vv/jobs/jobsredis",
		"github.com/frostgrove/vv/port/porthttp",
		"github.com/frostgrove/vv/runtime/runtimefx",
		"github.com/frostgrove/vv/storage/storageminio",
	} {
		if allowedOTelProductionImport(path) {
			t.Errorf("forbidden production dependency %s is allowed", path)
		}
	}
}

func TestPublishedModuleIdentityRejectsForbiddenNamespaces(t *testing.T) {
	tests := []struct {
		name      string
		directory string
		identity  string
	}{
		{name: "vv otel alias", directory: "alias", identity: "github.com/frostgrove/vv/otel/alias"},
		{name: "upstream root", directory: "upstream", identity: "go.opentelemetry.io"},
		{name: "upstream otel", directory: "upstream-otel", identity: "go.opentelemetry.io/otel"},
		{name: "root identity", directory: ".", identity: "go.opentelemetry.io/otel/sdk"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			module := filepath.Join(repository, test.directory)
			writeDependencyGateFile(t, module, "go.mod", "module "+test.identity+"\n\ngo 1.26\n")
			writeDependencyGateFile(t, module, "library.go", "package library\n\nimport _ \"fmt\"\n")

			_, err := transitiveProductionOTelImport(repository, module, make(map[string]packageImports))
			if err == nil || !strings.Contains(err.Error(), "forbidden") || !strings.Contains(err.Error(), test.identity) {
				t.Fatalf("published identity %s was accepted: %v", test.identity, err)
			}
		})
	}
}

func TestPublishedModuleIdentityHandlesCanonicalAndNestedOTelModules(t *testing.T) {
	t.Run("canonical exact identity is exempt", func(t *testing.T) {
		repository := t.TempDir()
		module := filepath.Join(repository, "otel")
		writeDependencyGateFile(t, module, "go.mod", "module "+otelModulePath+"\n\ngo 1.26\n")
		writeDependencyGateFile(t, module, "otel.go", "package vvotel\n")

		finding, err := transitiveProductionOTelImport(repository, module, make(map[string]packageImports))
		if err != nil || finding != "" {
			t.Fatalf("canonical OTel module was not exempt: finding=%q err=%v", finding, err)
		}
	})

	t.Run("canonical directory renamed", func(t *testing.T) {
		repository := t.TempDir()
		module := filepath.Join(repository, "otel")
		writeDependencyGateFile(t, module, "go.mod", "module corp/telemetry\n\ngo 1.26\n")
		writeDependencyGateFile(t, module, "otel.go", "package telemetry\n")

		_, err := transitiveProductionOTelImport(repository, module, make(map[string]packageImports))
		if err == nil || !strings.Contains(err.Error(), "canonical otel module must have selected and declared identity") {
			t.Fatalf("renamed canonical OTel module was accepted: %v", err)
		}
	})

	t.Run("nested forbidden module is discovered", func(t *testing.T) {
		repository := t.TempDir()
		module := filepath.Join(repository, "otel")
		nested := filepath.Join(module, "second")
		writeDependencyGateFile(t, module, "go.mod", "module "+otelModulePath+"\n\ngo 1.26\n")
		writeDependencyGateFile(t, module, "otel.go", "package vvotel\n")
		writeDependencyGateFile(t, nested, "go.mod", "module "+otelModulePath+"/second\n\ngo 1.26\n")
		writeDependencyGateFile(t, nested, "second.go", "package second\n")
		modules := publishedModuleDirectories(t, repository)
		if !stringInSlice(nested, modules) {
			t.Fatalf("nested OTel module was not discovered: %v", modules)
		}

		_, err := transitiveProductionOTelImport(repository, nested, make(map[string]packageImports))
		if err == nil || !strings.Contains(err.Error(), otelModulePath+"/second") {
			t.Fatalf("nested forbidden OTel module was accepted: %v", err)
		}
	})
}

func writeDependencyGateFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDependencyGateNormalizesOneInitialUTF8BOM(t *testing.T) {
	bom := "\xef\xbb\xbf"
	for _, test := range []struct {
		name       string
		prefix     string
		constraint string
		wantSource bool
		wantError  bool
	}{
		{name: "zero", constraint: "//go:build bom_tag\n\n", wantSource: true},
		{name: "single", prefix: bom, constraint: "//go:build bom_tag\n\n", wantSource: true},
		{name: "single ignore", prefix: bom, constraint: "//go:build ignore\n\n"},
		{name: "double", prefix: bom + bom, constraint: "//go:build bom_tag\n\n", wantError: true},
	} {
		t.Run("direct "+test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeDependencyGateFile(t, directory, "source.go", test.prefix+test.constraint+"package library\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
			source, possible, err := productionSourceInFile(filepath.Join(directory, "source.go"), "source.go")
			if (err != nil) != test.wantError || possible != test.wantSource {
				t.Fatalf("direct BOM parse returned possible=%t err=%v", possible, err)
			}
			if possible && (len(source.imports) != 1 || !openTelemetryImport(source.imports[0].path)) {
				t.Fatalf("direct BOM parse lost the OTel import: %+v", source.imports)
			}
		})

		t.Run("reachable "+test.name, func(t *testing.T) {
			dependency := t.TempDir()
			writeDependencyGateFile(t, dependency, "go.mod", "module example.com/library\n\ngo 1.26\n")
			writeDependencyGateFile(t, dependency, "library.go", "package library\n")
			writeDependencyGateFile(t, dependency, "source.go", test.prefix+test.constraint+"package library\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
			repository := dependencyGateImportFixture(t, "example.com/library", dependency, "package consumer\n\nimport _ \"example.com/library\"\n")
			finding, err := transitiveProductionOTelImport(repository, repository, make(map[string]packageImports))
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "multiple leading UTF-8 byte order marks") {
					t.Fatalf("reachable double BOM was accepted: finding=%q err=%v", finding, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (finding != "") != test.wantSource {
				t.Fatalf("reachable BOM source produced finding %q, want finding=%t", finding, test.wantSource)
			}
		})
	}
}

func dependencyGateImportFixture(t *testing.T, modulePath, dependency, source string) string {
	t.Helper()
	repository := t.TempDir()
	writeDependencyGateFile(t, repository, "go.mod", "module github.com/frostgrove/vv/dependencygatefixture\n\ngo 1.26\n\nrequire "+modulePath+" v0.0.0\n\nreplace "+modulePath+" => "+dependency+"\n")
	writeDependencyGateFile(t, repository, "consumer.go", source)
	return repository
}

func TestDependencyGateUsesOneBuildConfigurationAcrossAPath(t *testing.T) {
	tests := []struct {
		name         string
		entryName    string
		entryHeader  string
		entryCgo     bool
		bridgeName   string
		bridgeHeader string
	}{
		{name: "filename contradictory", entryName: "entry_linux.go", bridgeName: "bridge_windows.go"},
		{name: "filename reachable", entryName: "entry_linux.go", bridgeName: "bridge_linux.go", bridgeHeader: "//go:build path_control\n\n"},
		{name: "custom contradictory", entryName: "entry.go", entryHeader: "//go:build path_custom\n\n", bridgeName: "bridge.go", bridgeHeader: "//go:build !path_custom\n\n"},
		{name: "custom reachable", entryName: "entry.go", entryHeader: "//go:build path_custom\n\n", bridgeName: "bridge.go", bridgeHeader: "//go:build path_custom\n\n"},
		{name: "compiler contradictory", entryName: "entry.go", entryHeader: "//go:build gc\n\n", bridgeName: "bridge.go", bridgeHeader: "//go:build !gc\n\n"},
		{name: "compiler reachable", entryName: "entry.go", entryHeader: "//go:build gccgo\n\n", bridgeName: "bridge.go", bridgeHeader: "//go:build gccgo\n\n"},
		{name: "release contradictory", entryName: "entry.go", entryHeader: "//go:build go1.999\n\n", bridgeName: "bridge.go", bridgeHeader: "//go:build !go1.999\n\n"},
		{name: "release reachable", entryName: "entry.go", entryHeader: "//go:build go1.999\n\n", bridgeName: "bridge.go", bridgeHeader: "//go:build go1.999\n\n"},
		{name: "architecture contradictory", entryName: "entry.go", entryHeader: "//go:build amd64.v2\n\n", bridgeName: "bridge.go", bridgeHeader: "//go:build !amd64.v2\n\n"},
		{name: "architecture reachable", entryName: "entry.go", entryHeader: "//go:build amd64.v2\n\n", bridgeName: "bridge.go", bridgeHeader: "//go:build amd64.v2\n\n"},
		{name: "cgo contradictory", entryName: "entry.go", entryCgo: true, bridgeName: "bridge.go", bridgeHeader: "//go:build !cgo\n\n"},
		{name: "cgo reachable", entryName: "entry.go", entryCgo: true, bridgeName: "bridge.go", bridgeHeader: "//go:build cgo\n\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bridge := t.TempDir()
			writeDependencyGateFile(t, bridge, "go.mod", "module example.com/bridge\n\ngo 1.26\n")
			writeDependencyGateFile(t, bridge, test.bridgeName, test.bridgeHeader+"package bridge\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n")
			imports := "import _ \"example.com/bridge\"\n"
			if test.entryCgo {
				imports = "import (\n\t\"C\"\n\t_ \"example.com/bridge\"\n)\n"
			}
			repository := dependencyGateImportFixture(t, "example.com/bridge", bridge, "package consumer\n")
			writeDependencyGateFile(t, repository, test.entryName, test.entryHeader+"package consumer\n\n"+imports)

			finding, err := transitiveProductionOTelImport(repository, repository, make(map[string]packageImports))
			if err != nil {
				t.Fatal(err)
			}
			wantFinding := strings.HasSuffix(test.name, "reachable")
			if (finding != "") != wantFinding {
				t.Fatalf("global build configuration produced finding %q, want finding=%t", finding, wantFinding)
			}
		})
	}
}

func TestDependencyGateConvergentGraphDoesNotExhaustWorkLimit(t *testing.T) {
	for _, forbidden := range []bool{false, true} {
		name := "clean"
		if forbidden {
			name = "forbidden"
		}
		t.Run(name, func(t *testing.T) {
			graph := t.TempDir()
			writeDependencyGateFile(t, graph, "go.mod", "module example.com/convergent\n\ngo 1.26\n")
			for level := 0; level < 18; level++ {
				next := "example.com/convergent/terminal"
				if level+1 < 18 {
					next = fmt.Sprintf("example.com/convergent/level%02da\"\n\t_ \"example.com/convergent/level%02db", level+1, level+1)
				}
				source := "package convergent\n\nimport (\n\t_ \"" + next + "\"\n)\n"
				writeDependencyGateFile(t, graph, fmt.Sprintf("level%02da/source.go", level), source)
				writeDependencyGateFile(t, graph, fmt.Sprintf("level%02db/source.go", level), source)
			}
			terminal := "package terminal\n"
			if forbidden {
				terminal = "package terminal\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n"
			}
			writeDependencyGateFile(t, graph, "terminal/source.go", terminal)
			repository := dependencyGateImportFixture(t, "example.com/convergent", graph, "package consumer\n\nimport (\n\t_ \"example.com/convergent/level00a\"\n\t_ \"example.com/convergent/level00b\"\n)\n")

			finding, err := transitiveProductionOTelImport(repository, repository, make(map[string]packageImports))
			if err != nil {
				t.Fatalf("convergent graph failed closed before completion: %v", err)
			}
			if (finding != "") != forbidden {
				t.Fatalf("convergent graph produced finding %q, want finding=%t", finding, forbidden)
			}
		})
	}
}

func TestDependencyGateTraversesProductionCommandRoots(t *testing.T) {
	for _, test := range []struct {
		name          string
		librarySource string
		commandHeader string
		commandImport string
		bridgeSource  string
		wantFinding   bool
	}{
		{
			name:          "direct command",
			commandImport: "go.opentelemetry.io/otel/trace",
			bridgeSource:  "package bridge\n",
			wantFinding:   true,
		},
		{
			name:          "command only",
			commandHeader: "",
			bridgeSource:  "package bridge\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
			wantFinding:   true,
		},
		{
			name:          "mutually exclusive tagged command",
			librarySource: "//go:build !tool\n\npackage library\n",
			commandHeader: "//go:build tool\n\n",
			bridgeSource:  "package bridge\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
			wantFinding:   true,
		},
		{
			name:          "global tag contradiction",
			librarySource: "//go:build !tool\n\npackage library\n",
			commandHeader: "//go:build tool\n\n",
			bridgeSource:  "//go:build !tool\n\npackage bridge\n\nimport _ \"go.opentelemetry.io/otel/trace\"\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			commandImport := test.commandImport
			if commandImport == "" {
				commandImport = "example.com/bridge"
			}
			bridge := t.TempDir()
			writeDependencyGateFile(t, bridge, "go.mod", "module example.com/bridge\n\ngo 1.26\n")
			writeDependencyGateFile(t, bridge, "bridge.go", test.bridgeSource)
			repository := t.TempDir()
			writeDependencyGateFile(t, repository, "go.mod", "module example.com/command\n\ngo 1.26\n\nrequire example.com/bridge v0.0.0\n\nreplace example.com/bridge => "+bridge+"\n")
			if test.librarySource != "" {
				writeDependencyGateFile(t, repository, "library.go", test.librarySource)
			}
			writeDependencyGateFile(t, repository, "tool.go", test.commandHeader+"package main\n\nimport _ \""+commandImport+"\"\n")

			finding, err := transitiveProductionOTelImport(repository, repository, make(map[string]packageImports))
			if err != nil {
				t.Fatal(err)
			}
			if (finding != "") != test.wantFinding {
				t.Fatalf("command root produced finding %q, want finding=%t", finding, test.wantFinding)
			}
		})
	}
}

func TestDependencyGateDuplicateBlankImportsDoNotConsumeTheWalkLimit(t *testing.T) {
	repository := t.TempDir()
	writeDependencyGateFile(t, repository, "go.mod", "module example.com/duplicates\n\ngo 1.26\n")
	var source strings.Builder
	source.WriteString("package duplicates\n\nimport (\n")
	for range 65537 {
		source.WriteString("\t_ \"fmt\"\n")
	}
	source.WriteString(")\n")
	writeDependencyGateFile(t, repository, "duplicates.go", source.String())

	finding, err := transitiveProductionOTelImport(repository, repository, make(map[string]packageImports))
	if err != nil || finding != "" {
		t.Fatalf("duplicate blank imports exhausted semantic walk work: finding=%q err=%v", finding, err)
	}
}

func TestDependencyGateUsesCgoInPackageOverlapConditions(t *testing.T) {
	t.Run("direct cgo contradiction is excluded", func(t *testing.T) {
		directory := t.TempDir()
		writeDependencyGateFile(t, directory, "source.go", "//go:build !cgo\n\npackage library\n\nimport (\n\t\"C\"\n\t_ \"go.opentelemetry.io/otel/trace\"\n)\n")
		_, possible, err := productionSourceInFile(filepath.Join(directory, "source.go"), "source.go")
		if err != nil || possible {
			t.Fatalf("an impossible direct cgo source was admitted: possible=%t err=%v", possible, err)
		}
	})

	t.Run("cgo overlap fails", func(t *testing.T) {
		directory := t.TempDir()
		writeDependencyGateFile(t, directory, "alpha.go", "//go:build package_conflict\n\npackage alpha\n\nimport \"C\"\n")
		writeDependencyGateFile(t, directory, "beta.go", "//go:build package_conflict\n\npackage beta\n")
		imports := readProductionPackage(directory, ".", true)
		if imports.err == nil || !strings.Contains(imports.err.Error(), "declare different packages alpha and beta") {
			t.Fatalf("cgo-capable package overlap was accepted: %v", imports.err)
		}
	})

	t.Run("cgo contradiction is excluded", func(t *testing.T) {
		directory := t.TempDir()
		writeDependencyGateFile(t, directory, "alpha.go", "//go:build !cgo\n\npackage alpha\n\nimport \"C\"\n")
		writeDependencyGateFile(t, directory, "beta.go", "package beta\n")
		imports := readProductionPackage(directory, ".", true)
		if imports.err != nil {
			t.Fatalf("impossible cgo source created a package conflict: %v", imports.err)
		}
	})
}

func TestARM64FeatureConfigurationsMatchGoImplications(t *testing.T) {
	configurations := arm64FeatureConfigurations()
	if len(configurations) != 16 {
		t.Fatalf("arm64 configuration count = %d, want 16", len(configurations))
	}
	v83 := configurations[3]
	for minor := 0; minor <= 3; minor++ {
		if !v83[fmt.Sprintf("arm64.v8.%d", minor)] {
			t.Fatalf("arm64.v8.3 omits v8.%d: %v", minor, v83)
		}
	}
	if v83["arm64.v8.4"] || v83["arm64.v9.0"] {
		t.Fatalf("arm64.v8.3 includes a later feature: %v", v83)
	}
	v92 := configurations[12]
	for minor := 0; minor <= 2; minor++ {
		if !v92[fmt.Sprintf("arm64.v9.%d", minor)] {
			t.Fatalf("arm64.v9.2 omits v9.%d: %v", minor, v92)
		}
	}
	for minor := 0; minor <= 7; minor++ {
		if !v92[fmt.Sprintf("arm64.v8.%d", minor)] {
			t.Fatalf("arm64.v9.2 omits v8.%d: %v", minor, v92)
		}
	}
	if v92["arm64.v8.8"] || v92["arm64.v9.3"] {
		t.Fatalf("arm64.v9.2 includes a later feature: %v", v92)
	}
}

func allowedOTelProductionImport(path string) bool {
	if standardLibraryImport(path) || admittedOTelSeam(path) {
		return true
	}
	switch path {
	case "go.opentelemetry.io/otel/attribute",
		"go.opentelemetry.io/otel/codes",
		"go.opentelemetry.io/otel/metric",
		"go.opentelemetry.io/otel/propagation",
		"go.opentelemetry.io/otel/trace":
		return true
	default:
		return false
	}
}

func standardLibraryImport(path string) bool {
	standardLibraryOnce.Do(func() {
		standardLibrary = make(map[string]bool)
		root := filepath.Join(build.Default.GOROOT, "src")
		standardLibraryErr = filepath.WalkDir(root, func(directory string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() {
				return nil
			}
			if directory != root && (entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				return err
			}
			for _, source := range entries {
				if !source.IsDir() && strings.HasSuffix(source.Name(), ".go") && !strings.HasSuffix(source.Name(), "_test.go") {
					relative, err := filepath.Rel(root, directory)
					if err != nil {
						return err
					}
					standardLibrary[filepath.ToSlash(relative)] = true
					break
				}
			}
			return nil
		})
		if standardLibraryErr == nil && len(standardLibrary) == 0 {
			standardLibraryErr = fmt.Errorf("Go standard-library source inventory is empty")
		}
	})
	return standardLibraryErr == nil && standardLibrary[path]
}

func admittedOTelSeam(path string) bool {
	switch path {
	case "github.com/frostgrove/vv/auth",
		"github.com/frostgrove/vv/cache",
		"github.com/frostgrove/vv/cache/cachememory",
		"github.com/frostgrove/vv/crud",
		"github.com/frostgrove/vv/errs",
		"github.com/frostgrove/vv/health",
		"github.com/frostgrove/vv/jobs",
		"github.com/frostgrove/vv/port",
		"github.com/frostgrove/vv/remote",
		"github.com/frostgrove/vv/runtime",
		"github.com/frostgrove/vv/storage":
		return true
	default:
		return false
	}
}

type productionImport struct {
	file      string
	path      string
	predicate importPredicate
}

type packageImports struct {
	edges []productionImport
	err   error
}

type productionSource struct {
	file        string
	name        string
	imports     []productionImport
	constraint  constraint.Expr
	requiresCgo bool
}

type productionCondition struct {
	file        string
	constraint  constraint.Expr
	requiresCgo bool
}

type importPredicate struct {
	source      productionCondition
	mainSources []productionCondition
	signature   string
}

type buildPlatform struct {
	goos     string
	goarch   string
	compiler string
	cgo      bool
	features map[string]bool
}

var (
	buildPlatformsOnce  sync.Once
	buildPlatforms      []buildPlatform
	buildPlatformsErr   error
	standardLibraryOnce sync.Once
	standardLibrary     map[string]bool
	standardLibraryErr  error
	moduleEdits         sync.Map
	moduleDeclarations  sync.Map
	filenameMatches     sync.Map
)

type moduleEdit struct {
	Module struct {
		Path string
	}
	Require []listedModule
	Replace []moduleReplacement
}

type moduleReplacement struct {
	Old listedModule
	New listedModule
}

type listedModule struct {
	Path                    string
	Version                 string
	Dir                     string
	GoMod                   string
	Main                    bool
	Replace                 *listedModule
	RepositoryFallbackDir   string
	RepositoryFallbackGoMod string
	RepositorySource        bool
	Error                   *struct {
		Err string
	}
}

type moduleDeclarationResult struct {
	path string
	err  error
}

type moduleEditResult struct {
	edit moduleEdit
	err  error
}

type filenameMatchResult struct {
	matches bool
	err     error
}

type importWalk struct {
	path       string
	chain      []string
	provenance importProvenance
	predicates []importPredicate
	packages   map[string]bool
}

type importProvenance uint8

const (
	importProvenanceUndecided importProvenance = iota
	importProvenanceSelected
	importProvenanceRepository
)

type resolvedImport struct {
	directory  string
	provenance importProvenance
	identity   string
}

func publishedModuleDirectories(t *testing.T, repository string) []string {
	t.Helper()
	modules, err := discoverPublishedModuleDirectories(repository)
	if err != nil {
		t.Fatalf("cannot discover published modules: %s", normalizeDependencyDiagnostic(err.Error(), repository, repository, ""))
	}
	return modules
}

func discoverPublishedModuleDirectories(repository string) ([]string, error) {
	var modules []string
	err := filepath.WalkDir(repository, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == repository {
				return nil
			}
			relative, err := filepath.Rel(repository, path)
			if err != nil {
				return err
			}
			first, _, _ := strings.Cut(filepath.ToSlash(relative), "/")
			if first == "test" || first == "_examples" || entry.Name() == "testdata" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "go.mod" {
			modules = append(modules, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(modules)
	return modules, nil
}

func transitiveProductionOTelImport(repository, module string, cache map[string]packageImports) (string, error) {
	modules, err := productionModuleGraph(repository, module)
	if err != nil {
		return "", err
	}
	exempt, err := validatePublishedModuleIdentity(repository, module, modules)
	if err != nil {
		return "", err
	}
	if exempt {
		return "", nil
	}
	initial, err := moduleProductionImports(module)
	if err != nil {
		return "", fmt.Errorf("cannot inventory production source: %s", normalizeDependencyDiagnostic(err.Error(), repository, module, ""))
	}
	queue := make([]importWalk, 0, len(initial))
	for _, imported := range initial {
		queue = append(queue, importWalk{path: imported.path, chain: []string{imported.file}, predicates: []importPredicate{imported.predicate}, packages: make(map[string]bool)})
	}
	work := 0
	visitedStates := make(map[string][][]string)
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		possible, err := importPredicatesSatisfiable(current.predicates)
		if err != nil {
			return "", err
		}
		if !possible {
			continue
		}
		if openTelemetryImport(current.path) {
			return strings.Join(append(current.chain, current.path), " -> "), nil
		}
		if err := validateImportPath(current.path); err != nil {
			return "", fmt.Errorf("invalid reachable import path %q from %s: %w", current.path, strings.Join(current.chain, " -> "), err)
		}
		if standardLibraryImport(current.path) || current.path == "C" {
			continue
		}
		resolved, err := resolveReachableImport(modules, current.path, current.provenance)
		if err != nil {
			return "", fmt.Errorf("cannot resolve reachable package %s from %s: %w", current.path, strings.Join(current.chain, " -> "), err)
		}
		cacheKey := resolvedImportCacheKey(resolved.directory, resolved.provenance, resolved.identity)
		if current.packages[cacheKey] {
			continue
		}
		state := importPredicateSignatureSet(current.predicates)
		if buildStateCovered(visitedStates[cacheKey], state) {
			continue
		}
		visitedStates[cacheKey] = retainUncoveredBuildStates(visitedStates[cacheKey], state)
		work++
		if work > 65536 {
			return "", fmt.Errorf("reachable dependency paths exceed the dependency gate's safe work limit")
		}
		packages := make(map[string]bool, len(current.packages)+1)
		for visited := range current.packages {
			packages[visited] = true
		}
		packages[cacheKey] = true
		imports := cachedPackageImports(cache, resolved.directory, resolved)
		if imports.err != nil {
			return "", fmt.Errorf("cannot read reachable package %s: %w", current.path, imports.err)
		}
		for _, imported := range imports.edges {
			chain := append([]string(nil), current.chain...)
			chain = append(chain, current.path+" ("+filepath.Base(imported.file)+")")
			predicates := append([]importPredicate(nil), current.predicates...)
			predicates = append(predicates, imported.predicate)
			queue = append(queue, importWalk{path: imported.path, chain: chain, provenance: resolved.provenance, predicates: predicates, packages: packages})
		}
	}
	return "", nil
}

func validateImportPath(importPath string) error {
	if importPath == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.HasPrefix(importPath, "-") {
		return fmt.Errorf("path has a leading dash")
	}
	if strings.Contains(importPath, "\\") {
		return fmt.Errorf("backslashes are not allowed")
	}
	if strings.HasPrefix(importPath, "/") || strings.HasSuffix(importPath, "/") || strings.Contains(importPath, "//") {
		return fmt.Errorf("path contains an empty component")
	}
	if pathpkg.Clean(importPath) != importPath {
		return fmt.Errorf("path is not canonical")
	}
	for _, component := range strings.Split(importPath, "/") {
		if component == "." || component == ".." {
			return fmt.Errorf("path contains a relative component")
		}
		if strings.HasSuffix(component, ".") {
			return fmt.Errorf("path component has a trailing dot")
		}
		for _, character := range component {
			if !(character == '-' || character == '.' || character == '_' || character == '~' || character == '+' || character >= '0' && character <= '9' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z') {
				return fmt.Errorf("path contains an invalid character")
			}
		}
		short, _, _ := strings.Cut(component, ".")
		for _, reserved := range windowsReservedImportComponents {
			if strings.EqualFold(short, reserved) {
				return fmt.Errorf("path contains a reserved component")
			}
		}
		if tilde := strings.LastIndexByte(short, '~'); tilde >= 0 && tilde < len(short)-1 {
			digits := true
			for _, character := range short[tilde+1:] {
				if character < '0' || character > '9' {
					digits = false
				}
			}
			if digits {
				return fmt.Errorf("path contains a short-name component")
			}
		}
	}
	return nil
}

var windowsReservedImportComponents = []string{
	"CON", "PRN", "AUX", "NUL",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
}

func productionModuleGraph(repository, module string) ([]listedModule, error) {
	var err error
	repository, err = filepath.Abs(repository)
	if err != nil {
		return nil, err
	}
	module, err = filepath.Abs(module)
	if err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp("", "vv-otel-deps-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	modfile := filepath.Join(temporary, "gate.mod")
	content, err := os.ReadFile(filepath.Join(module, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("cannot read go.mod: %s", pathErrorDetail(err))
	}
	if err := os.WriteFile(modfile, content, 0o644); err != nil {
		return nil, fmt.Errorf("cannot write temporary go.mod: %s", pathErrorDetail(err))
	}
	if sum, err := os.ReadFile(filepath.Join(module, "go.sum")); err == nil {
		if err := os.WriteFile(filepath.Join(temporary, "gate.sum"), sum, 0o644); err != nil {
			return nil, fmt.Errorf("cannot write temporary go.sum: %s", pathErrorDetail(err))
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read go.sum: %s", pathErrorDetail(err))
	}
	edit, err := readModuleEdit(module)
	if err != nil {
		return nil, fmt.Errorf("%s", normalizeDependencyDiagnostic(err.Error(), repository, module, temporary))
	}
	for _, replacement := range edit.Replace {
		if replacement.New.Version != "" || filepath.IsAbs(replacement.New.Path) {
			continue
		}
		old := replacement.Old.Path
		if replacement.Old.Version != "" {
			old += "@" + replacement.Old.Version
		}
		if err := editModule(module, modfile, "-dropreplace="+old); err != nil {
			return nil, fmt.Errorf("%s", normalizeDependencyDiagnostic(err.Error(), repository, module, temporary))
		}
		target, err := filepath.Abs(filepath.Join(module, replacement.New.Path))
		if err != nil {
			return nil, err
		}
		if err := editModule(module, modfile, "-replace="+old+"="+target); err != nil {
			return nil, fmt.Errorf("%s", normalizeDependencyDiagnostic(err.Error(), repository, module, temporary))
		}
	}
	if err := injectLocalSentinelReplacements(repository, module, modfile, edit); err != nil {
		return nil, fmt.Errorf("%s", normalizeDependencyDiagnostic(err.Error(), repository, module, temporary))
	}
	command := exec.Command("go", "list", "-e", "-mod=mod", "-modfile="+modfile, "-m", "-json", "all")
	command.Dir = module
	command.Env = offlineGoEnvironment()
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("go list -m all failed: %s", normalizeDependencyDiagnostic(strings.TrimSpace(string(exit.Stderr)), repository, module, temporary))
		}
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var modules []listedModule
	hasSelectedRepositoryModule := false
	for {
		var listed listedModule
		if err := decoder.Decode(&listed); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if listed.Dir == "" && listed.Replace != nil {
			listed.Dir = listed.Replace.Dir
		}
		if listed.GoMod == "" && listed.Replace != nil {
			listed.GoMod = listed.Replace.GoMod
		}
		if listed.Main {
			listed.Dir = module
			listed.GoMod = filepath.Join(module, "go.mod")
			if listed.Path == "github.com/frostgrove/vv" {
				listed.RepositorySource = true
			}
		}
		if listed.Path == "github.com/frostgrove/vv" {
			hasSelectedRepositoryModule = true
			if listed.Replace == nil {
				listed.RepositoryFallbackDir = repository
				listed.RepositoryFallbackGoMod = filepath.Join(repository, "go.mod")
			}
		}
		modules = append(modules, listed)
	}
	if !hasSelectedRepositoryModule {
		modules = append(modules, listedModule{Path: "github.com/frostgrove/vv", Dir: repository, GoMod: filepath.Join(repository, "go.mod"), RepositorySource: true})
	}
	return modules, nil
}

func injectLocalSentinelReplacements(repository, module, modfile string, mainEdit moduleEdit) error {
	directories, err := discoverPublishedModuleDirectories(repository)
	if err != nil {
		return fmt.Errorf("cannot discover local modules: %w", err)
	}
	localModules := make(map[string]string, len(directories))
	for _, directory := range directories {
		edit, err := readModuleEdit(directory)
		if err != nil {
			return fmt.Errorf("cannot read local module %s", filepath.Base(directory))
		}
		if previous, exists := localModules[edit.Module.Path]; exists && filepath.Clean(previous) != filepath.Clean(directory) {
			return fmt.Errorf("local module identity %s is declared more than once", edit.Module.Path)
		}
		localModules[edit.Module.Path] = directory
	}
	explicit := make(map[string]moduleReplacement, len(mainEdit.Replace))
	for _, replacement := range mainEdit.Replace {
		explicit[moduleVersionKey(replacement.Old.Path, replacement.Old.Version)] = replacement
	}
	pending := []string{module}
	visited := make(map[string]bool)
	injected := make(map[string]bool)
	for len(pending) != 0 {
		current := pending[0]
		pending = pending[1:]
		current, err = filepath.Abs(current)
		if err != nil {
			return err
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		if len(visited) > 65536 {
			return fmt.Errorf("local module closure exceeds the dependency gate's safe work limit")
		}
		currentEdit := mainEdit
		if filepath.Clean(current) != filepath.Clean(module) {
			currentEdit, err = readModuleEdit(current)
			if err != nil {
				return fmt.Errorf("cannot read local dependency module %s", filepath.Base(current))
			}
		}
		for _, requirement := range currentEdit.Require {
			if !localDependencyVersion(requirement.Version) {
				continue
			}
			localDirectory, exists := localModules[requirement.Path]
			if !exists || filepath.Clean(localDirectory) == filepath.Clean(module) {
				continue
			}
			replacement, replaced := explicit[moduleVersionKey(requirement.Path, requirement.Version)]
			if !replaced {
				replacement, replaced = explicit[moduleVersionKey(requirement.Path, "")]
			}
			if replaced {
				if directory, ok := localReplacementDirectory(module, replacement.New); ok {
					pending = append(pending, directory)
				}
				continue
			}
			key := moduleVersionKey(requirement.Path, requirement.Version)
			if !injected[key] {
				target, err := filepath.Abs(localDirectory)
				if err != nil {
					return err
				}
				if err := editModule(module, modfile, "-replace="+key+"="+target); err != nil {
					return err
				}
				injected[key] = true
			}
			pending = append(pending, localDirectory)
		}
	}
	return nil
}

func localDependencyVersion(version string) bool {
	return version == "v0.0.0" || version == "v0.0.0-00010101000000-000000000000"
}

func moduleVersionKey(path, version string) string {
	if version == "" {
		return path
	}
	return path + "@" + version
}

func localReplacementDirectory(module string, replacement listedModule) (string, bool) {
	if replacement.Version != "" {
		return "", false
	}
	directory := replacement.Path
	if !filepath.IsAbs(directory) {
		if directory != "." && directory != ".." && !strings.HasPrefix(directory, "./") && !strings.HasPrefix(directory, "../") && !strings.HasPrefix(directory, `.\`) && !strings.HasPrefix(directory, `..\`) {
			return "", false
		}
		directory = filepath.Join(module, filepath.FromSlash(directory))
	}
	if _, err := os.Stat(filepath.Join(directory, "go.mod")); err != nil {
		return "", false
	}
	return directory, true
}

func validatePublishedModuleIdentity(repository, module string, modules []listedModule) (bool, error) {
	selectedIdentity := ""
	for _, listed := range modules {
		if listed.Main {
			selectedIdentity = listed.Path
			break
		}
	}
	if selectedIdentity == "" {
		return false, fmt.Errorf("published module has no selected main identity")
	}
	declaredIdentity, err := declaredModulePath(filepath.Join(module, "go.mod"))
	if err != nil {
		return false, fmt.Errorf("published module has an unreadable go.mod")
	}
	canonicalOTelDirectory := filepath.Clean(module) == filepath.Clean(filepath.Join(repository, "otel"))
	if canonicalOTelDirectory {
		if selectedIdentity != otelModulePath || declaredIdentity != otelModulePath {
			return false, fmt.Errorf("canonical otel module must have selected and declared identity %s", otelModulePath)
		}
		return true, nil
	}
	if openTelemetryImport(selectedIdentity) {
		return false, fmt.Errorf("published module has forbidden selected identity %s", selectedIdentity)
	}
	if openTelemetryImport(declaredIdentity) {
		return false, fmt.Errorf("published module has forbidden declared identity %s", declaredIdentity)
	}
	return false, nil
}

func readModuleEdit(module string) (moduleEdit, error) {
	absolute, err := filepath.Abs(module)
	if err != nil {
		return moduleEdit{}, err
	}
	module = absolute
	goMod := filepath.Join(module, "go.mod")
	if cached, ok := moduleEdits.Load(goMod); ok {
		result := cached.(moduleEditResult)
		return result.edit, result.err
	}
	command := exec.Command("go", "mod", "edit", "-json")
	command.Dir = module
	command.Env = offlineGoEnvironment()
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("go mod edit -json failed: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		result := moduleEditResult{err: err}
		actual, _ := moduleEdits.LoadOrStore(goMod, result)
		stored := actual.(moduleEditResult)
		return stored.edit, stored.err
	}
	var edit moduleEdit
	if err := json.Unmarshal(output, &edit); err != nil {
		result := moduleEditResult{err: err}
		actual, _ := moduleEdits.LoadOrStore(goMod, result)
		stored := actual.(moduleEditResult)
		return stored.edit, stored.err
	}
	result := moduleEditResult{edit: edit}
	actual, _ := moduleEdits.LoadOrStore(goMod, result)
	stored := actual.(moduleEditResult)
	return stored.edit, stored.err
}

func editModule(module, modfile string, argument string) error {
	command := exec.Command("go", "mod", "edit", "-modfile="+modfile, argument)
	command.Dir = module
	command.Env = offlineGoEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go mod edit %s failed: %s", argument, strings.TrimSpace(string(output)))
	}
	return nil
}

func moduleProductionImports(module string) ([]productionImport, error) {
	var found []productionImport
	err := filepath.WalkDir(module, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == module {
				imports := readProductionRootPackage(path, ".")
				if imports.err != nil {
					return imports.err
				}
				found = append(found, imports.edges...)
				return nil
			}
			if entry.Name() == "testdata" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			} else if !os.IsNotExist(err) {
				return err
			}
			relative, err := filepath.Rel(module, path)
			if err != nil {
				return err
			}
			imports := readProductionRootPackage(path, filepath.ToSlash(relative))
			if imports.err != nil {
				return imports.err
			}
			found = append(found, imports.edges...)
			return nil
		}
		return nil
	})
	return found, err
}

func cachedPackageImports(cache map[string]packageImports, directory string, resolution ...resolvedImport) packageImports {
	resolved := resolvedImport{directory: directory}
	if len(resolution) != 0 {
		resolved = resolution[0]
	}
	key := resolvedImportCacheKey(directory, resolved.provenance, resolved.identity)
	if imports, ok := cache[key]; ok {
		return imports
	}
	imports := readProductionPackage(directory, ".", true)
	cache[key] = imports
	return imports
}

func resolvedImportCacheKey(directory string, provenance importProvenance, identity string) string {
	return directory + "\x00" + strconv.Itoa(int(provenance)) + "\x00" + identity
}

func ignoredProductionFile(name string) bool {
	return !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func productionSourcesInDirectory(directory, diagnosticDirectory string) ([]productionSource, int, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read package directory: %s", pathErrorDetail(err))
	}
	var sources []productionSource
	var candidates int
	for _, entry := range entries {
		if entry.IsDir() || ignoredProductionFile(entry.Name()) {
			continue
		}
		candidates++
		diagnosticPath := entry.Name()
		if diagnosticDirectory != "." {
			diagnosticPath = filepath.ToSlash(filepath.Join(diagnosticDirectory, entry.Name()))
		}
		source, possible, err := productionSourceInFile(filepath.Join(directory, entry.Name()), diagnosticPath)
		if err != nil {
			return nil, 0, err
		}
		if possible {
			sources = append(sources, source)
		}
	}
	return sources, candidates, nil
}

func readProductionRootPackage(directory, diagnosticDirectory string) packageImports {
	sources, _, err := productionSourcesInDirectory(directory, diagnosticDirectory)
	if err != nil {
		return packageImports{err: err}
	}
	return productionImportsForSources(sources, func(source productionSource) []productionCondition {
		excluded := make([]productionCondition, 0, len(sources))
		for _, candidate := range sources {
			if candidate.name != source.name {
				excluded = append(excluded, conditionForSource(candidate))
			}
		}
		return excluded
	})
}

func readProductionPackage(directory, diagnosticDirectory string, required bool) packageImports {
	sources, candidates, err := productionSourcesInDirectory(directory, diagnosticDirectory)
	if err != nil {
		return packageImports{err: err}
	}
	if len(sources) == 0 {
		if required {
			return packageImports{err: fmt.Errorf("package has no satisfiable production Go files")}
		}
		if candidates != 0 {
			return packageImports{}
		}
		return packageImports{}
	}
	var mainSources []productionSource
	var importableSources []productionSource
	for _, source := range sources {
		if source.name == "main" {
			mainSources = append(mainSources, source)
		} else {
			importableSources = append(importableSources, source)
		}
	}
	if len(importableSources) == 0 {
		if required {
			return packageImports{err: fmt.Errorf("package declares main and cannot be imported")}
		}
		return packageImports{}
	}
	sources = importableSources
	if err := validateProductionPackageNames(sources); err != nil {
		return packageImports{err: err}
	}
	mainConditions := make([]productionCondition, 0, len(mainSources))
	for _, mainSource := range mainSources {
		mainConditions = append(mainConditions, conditionForSource(mainSource))
	}
	return productionImportsForSources(sources, func(productionSource) []productionCondition {
		return mainConditions
	})
}

func validateProductionPackageNames(sources []productionSource) error {
	for left := 0; left < len(sources); left++ {
		for right := left + 1; right < len(sources); right++ {
			if sources[left].name == sources[right].name {
				continue
			}
			overlap, err := productionSourcesOverlap(sources[left], sources[right])
			if err != nil {
				return err
			}
			if overlap {
				return fmt.Errorf("overlapping production files %s and %s declare different packages %s and %s", sources[left].file, sources[right].file, sources[left].name, sources[right].name)
			}
		}
	}
	return nil
}

func productionImportsForSources(sources []productionSource, exclusions func(productionSource) []productionCondition) packageImports {
	var found []productionImport
	seen := make(map[string]bool)
	for _, source := range sources {
		predicate := importPredicate{source: conditionForSource(source), mainSources: exclusions(source)}
		signature, err := importPredicateSignature(predicate)
		if err != nil {
			return packageImports{err: err}
		}
		predicate.signature = signature
		possible, err := importPredicatesSatisfiable([]importPredicate{predicate})
		if err != nil {
			return packageImports{err: err}
		}
		if possible {
			for _, imported := range source.imports {
				imported.predicate = predicate
				key := imported.path + "\x00" + predicate.signature
				if seen[key] {
					continue
				}
				seen[key] = true
				found = append(found, imported)
			}
		}
	}
	return packageImports{edges: found}
}

func importPredicateSignature(predicate importPredicate) (string, error) {
	if len(predicate.mainSources) == 0 && predicate.source.constraint == nil && !predicate.source.requiresCgo {
		platforms, err := supportedBuildPlatforms()
		if err != nil {
			return "", err
		}
		universal := true
		for _, platform := range platforms {
			matches, err := conditionMatchesPlatform(predicate.source, platform)
			if err != nil {
				return "", err
			}
			if !matches {
				universal = false
				break
			}
		}
		if universal {
			return "", nil
		}
	}
	source, err := productionConditionSignature(predicate.source)
	if err != nil {
		return "", err
	}
	mains := make([]string, 0, len(predicate.mainSources))
	for _, mainSource := range predicate.mainSources {
		signature, err := productionConditionSignature(mainSource)
		if err != nil {
			return "", err
		}
		mains = append(mains, signature)
	}
	sort.Strings(mains)
	return source + "!" + strings.Join(mains, "|"), nil
}

func productionConditionSignature(condition productionCondition) (string, error) {
	platforms, err := supportedBuildPlatforms()
	if err != nil {
		return "", err
	}
	var mask strings.Builder
	for _, platform := range platforms {
		matches, err := conditionMatchesPlatform(condition, platform)
		if err != nil {
			return "", err
		}
		if matches {
			mask.WriteByte('1')
		} else {
			mask.WriteByte('0')
		}
	}
	expression := "true"
	if condition.constraint != nil {
		expression = condition.constraint.String()
	}
	return mask.String() + ":" + expression, nil
}

func importPredicateSignatureSet(predicates []importPredicate) []string {
	signatures := make([]string, 0, len(predicates))
	seen := make(map[string]bool)
	for _, predicate := range predicates {
		if predicate.signature != "" && !seen[predicate.signature] {
			signatures = append(signatures, predicate.signature)
			seen[predicate.signature] = true
		}
	}
	sort.Strings(signatures)
	return signatures
}

func buildStateCovered(existing [][]string, candidate []string) bool {
	for _, state := range existing {
		if sortedStringsSubset(state, candidate) {
			return true
		}
	}
	return false
}

func retainUncoveredBuildStates(existing [][]string, candidate []string) [][]string {
	retained := make([][]string, 0, len(existing)+1)
	for _, state := range existing {
		if !sortedStringsSubset(candidate, state) {
			retained = append(retained, state)
		}
	}
	return append(retained, candidate)
}

func sortedStringsSubset(left, right []string) bool {
	index := 0
	for _, value := range right {
		if index < len(left) && left[index] == value {
			index++
		}
	}
	return index == len(left)
}

func conditionForSource(source productionSource) productionCondition {
	return productionCondition{file: source.file, constraint: source.constraint, requiresCgo: source.requiresCgo}
}

func importPredicatesSatisfiable(predicates []importPredicate) (bool, error) {
	platforms, err := supportedBuildPlatforms()
	if err != nil {
		return false, err
	}
	for _, platform := range platforms {
		var constraints []productionSource
		blocked := false
		for _, predicate := range predicates {
			matches, err := conditionMatchesPlatform(predicate.source, platform)
			if err != nil {
				return false, err
			}
			if !matches {
				blocked = true
				break
			}
			constraints = append(constraints, productionSource{constraint: predicate.source.constraint})
			for _, mainSource := range predicate.mainSources {
				matches, err := conditionMatchesPlatform(mainSource, platform)
				if err != nil {
					return false, err
				}
				if !matches {
					continue
				}
				if mainSource.constraint == nil {
					blocked = true
					break
				}
				constraints = append(constraints, productionSource{constraint: &constraint.NotExpr{X: mainSource.constraint}})
			}
			if blocked {
				break
			}
		}
		if blocked {
			continue
		}
		possible, err := buildConstraintsSatisfiable(platform, constraints)
		if err != nil {
			return false, err
		}
		if possible {
			return true, nil
		}
	}
	return false, nil
}

func conditionMatchesPlatform(condition productionCondition, platform buildPlatform) (bool, error) {
	if condition.requiresCgo && !platform.cgo {
		return false, nil
	}
	return filenameMatchesPlatform(filepath.Base(condition.file), platform)
}

func productionSourceInFile(path, diagnosticPath string) (productionSource, bool, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return productionSource{}, false, fmt.Errorf("cannot read %s: %s", diagnosticPath, pathErrorDetail(err))
	}
	source, err = normalizeGoSource(source)
	if err != nil {
		return productionSource{}, false, fmt.Errorf("%s: %w", diagnosticPath, err)
	}
	expression, err := parseBuildConstraint(source)
	if err != nil {
		return productionSource{}, false, fmt.Errorf("%s: %w", diagnosticPath, err)
	}
	possible, err := productionSourcesSatisfiable(productionSource{file: diagnosticPath, constraint: expression})
	if err != nil {
		return productionSource{}, false, err
	}
	if !possible {
		return productionSource{}, false, nil
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), diagnosticPath, source, parser.ImportsOnly)
	if err != nil {
		return productionSource{}, false, err
	}
	found := make([]productionImport, 0, len(parsed.Imports))
	requiresCgo := false
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return productionSource{}, false, err
		}
		requiresCgo = requiresCgo || importPath == "C"
		found = append(found, productionImport{file: diagnosticPath, path: importPath})
	}
	parsedSource := productionSource{file: diagnosticPath, name: parsed.Name.Name, imports: found, constraint: expression, requiresCgo: requiresCgo}
	possible, err = productionSourcesSatisfiable(parsedSource)
	if err != nil {
		return productionSource{}, false, err
	}
	if !possible {
		return productionSource{}, false, nil
	}
	return parsedSource, true, nil
}

func requiresIgnoredBuildTag(source []byte) (bool, error) {
	expression, err := parseBuildConstraint(source)
	if err != nil {
		return false, err
	}
	possible, err := productionSourcesSatisfiable(productionSource{file: "source.go", constraint: expression})
	return !possible, err
}

func parseBuildConstraint(source []byte) (constraint.Expr, error) {
	trimmed, goBuild, err := dependencyFileHeader(source)
	if err != nil {
		return nil, err
	}
	if goBuild != nil {
		expression, err := constraint.Parse(string(goBuild))
		if err != nil {
			return nil, fmt.Errorf("parsing //go:build line: %w", err)
		}
		return expression, nil
	}
	var expression constraint.Expr
	for len(trimmed) > 0 {
		line := trimmed
		if index := bytes.IndexByte(line, '\n'); index >= 0 {
			line, trimmed = line[:index], trimmed[index+1:]
		} else {
			trimmed = trimmed[len(trimmed):]
		}
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("//")) || !bytes.Contains(line, []byte("+build")) || !constraint.IsPlusBuild(string(line)) {
			continue
		}
		parsed, err := constraint.Parse(string(line))
		if err != nil {
			continue
		}
		if expression == nil {
			expression = parsed
		} else {
			expression = &constraint.AndExpr{X: expression, Y: parsed}
		}
	}
	return expression, nil
}

func normalizeGoSource(source []byte) ([]byte, error) {
	bom := []byte{0xef, 0xbb, 0xbf}
	source = bytes.TrimPrefix(source, bom)
	if bytes.HasPrefix(source, bom) {
		return nil, fmt.Errorf("multiple leading UTF-8 byte order marks")
	}
	return source, nil
}

func dependencyFileHeader(content []byte) ([]byte, []byte, error) {
	end := 0
	remainder := content
	ended := false
	inBlock := false
	var goBuild []byte
	for len(remainder) > 0 {
		line := remainder
		if index := bytes.IndexByte(line, '\n'); index >= 0 {
			line, remainder = line[:index], remainder[index+1:]
		} else {
			remainder = remainder[len(remainder):]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 && !ended {
			end = len(content) - len(remainder)
			continue
		}
		if !bytes.HasPrefix(line, []byte("//")) {
			ended = true
		}
		if !inBlock && dependencyGoBuildComment(line) {
			if goBuild != nil {
				return nil, nil, fmt.Errorf("multiple //go:build comments")
			}
			goBuild = line
		}
		for len(line) > 0 {
			if inBlock {
				if index := bytes.Index(line, []byte("*/")); index >= 0 {
					inBlock = false
					line = bytes.TrimSpace(line[index+2:])
					continue
				}
				break
			}
			if bytes.HasPrefix(line, []byte("//")) {
				break
			}
			if bytes.HasPrefix(line, []byte("/*")) {
				inBlock = true
				line = bytes.TrimSpace(line[2:])
				continue
			}
			return content[:end], goBuild, nil
		}
	}
	return content[:end], goBuild, nil
}

func dependencyGoBuildComment(line []byte) bool {
	prefix := []byte("//go:build")
	if !bytes.HasPrefix(line, prefix) {
		return false
	}
	line = bytes.TrimSpace(line)
	rest := line[len(prefix):]
	return len(rest) == 0 || len(bytes.TrimSpace(rest)) < len(rest)
}

func productionSourcesOverlap(left, right productionSource) (bool, error) {
	return productionSourcesSatisfiable(left, right)
}

func productionSourcesSatisfiable(sources ...productionSource) (bool, error) {
	platforms, err := supportedBuildPlatforms()
	if err != nil {
		return false, err
	}
	for _, platform := range platforms {
		eligible := true
		for _, source := range sources {
			if source.requiresCgo && !platform.cgo {
				eligible = false
				break
			}
			matches, err := filenameMatchesPlatform(filepath.Base(source.file), platform)
			if err != nil {
				return false, err
			}
			if !matches {
				eligible = false
				break
			}
		}
		if eligible {
			possible, err := buildConstraintsSatisfiable(platform, sources)
			if err != nil {
				return false, err
			}
			if possible {
				return true, nil
			}
		}
	}
	return false, nil
}

func supportedBuildPlatforms() ([]buildPlatform, error) {
	buildPlatformsOnce.Do(func() {
		command := exec.Command("go", "tool", "dist", "list", "-json")
		command.Env = offlineGoEnvironment()
		output, err := command.Output()
		if err != nil {
			buildPlatformsErr = fmt.Errorf("cannot enumerate supported Go platforms")
			return
		}
		var tuples []struct {
			GOOS         string
			GOARCH       string
			CgoSupported bool
		}
		if err := json.Unmarshal(output, &tuples); err != nil {
			buildPlatformsErr = fmt.Errorf("go tool dist list returned invalid JSON")
			return
		}
		for _, tuple := range tuples {
			for _, compiler := range []string{"gc", "gccgo"} {
				for _, features := range architectureFeatureConfigurations(tuple.GOARCH) {
					buildPlatforms = append(buildPlatforms, buildPlatform{goos: tuple.GOOS, goarch: tuple.GOARCH, compiler: compiler, features: features})
					if tuple.CgoSupported {
						buildPlatforms = append(buildPlatforms, buildPlatform{goos: tuple.GOOS, goarch: tuple.GOARCH, compiler: compiler, cgo: true, features: features})
					}
				}
			}
		}
		if len(buildPlatforms) == 0 {
			buildPlatformsErr = fmt.Errorf("go tool dist list returned no platforms")
		}
	})
	return buildPlatforms, buildPlatformsErr
}

func architectureFeatureConfigurations(goarch string) []map[string]bool {
	switch goarch {
	case "386":
		return exclusiveFeatureConfigurations(goarch, "387", "sse2")
	case "amd64":
		return cumulativeFeatureConfigurations(goarch, "v1", "v2", "v3")
	case "arm":
		return cumulativeFeatureConfigurations(goarch, "5", "6", "7")
	case "arm64":
		return arm64FeatureConfigurations()
	case "mips", "mipsle", "mips64", "mips64le":
		return exclusiveFeatureConfigurations(goarch, "hardfloat", "softfloat")
	case "ppc64", "ppc64le":
		return cumulativeFeatureConfigurations(goarch, "power8", "power9", "power10")
	case "riscv64":
		return cumulativeFeatureConfigurations(goarch, "rva20u64", "rva22u64", "rva23u64")
	case "wasm":
		return []map[string]bool{
			{},
			{"wasm.satconv": true},
			{"wasm.signext": true},
			{"wasm.satconv": true, "wasm.signext": true},
		}
	default:
		return []map[string]bool{{}}
	}
}

func arm64FeatureConfigurations() []map[string]bool {
	var configurations []map[string]bool
	for minor := 0; minor <= 9; minor++ {
		features := make(map[string]bool, minor+1)
		for included := 0; included <= minor; included++ {
			features[fmt.Sprintf("arm64.v8.%d", included)] = true
		}
		configurations = append(configurations, features)
	}
	for minor := 0; minor <= 5; minor++ {
		features := make(map[string]bool, minor+7)
		for included := 0; included <= minor; included++ {
			features[fmt.Sprintf("arm64.v9.%d", included)] = true
		}
		for included := 0; included <= min(minor+5, 9); included++ {
			features[fmt.Sprintf("arm64.v8.%d", included)] = true
		}
		configurations = append(configurations, features)
	}
	return configurations
}

func exclusiveFeatureConfigurations(goarch string, levels ...string) []map[string]bool {
	configurations := make([]map[string]bool, 0, len(levels))
	for _, level := range levels {
		configurations = append(configurations, map[string]bool{goarch + "." + level: true})
	}
	return configurations
}

func cumulativeFeatureConfigurations(goarch string, levels ...string) []map[string]bool {
	configurations := make([]map[string]bool, 0, len(levels))
	features := make(map[string]bool)
	for _, level := range levels {
		features[goarch+"."+level] = true
		copy := make(map[string]bool, len(features))
		for feature := range features {
			copy[feature] = true
		}
		configurations = append(configurations, copy)
	}
	return configurations
}

func filenameMatchesPlatform(name string, platform buildPlatform) (bool, error) {
	key := name + "\x00" + platform.goos + "\x00" + platform.goarch
	if cached, ok := filenameMatches.Load(key); ok {
		result := cached.(filenameMatchResult)
		return result.matches, result.err
	}
	context := build.Default
	context.GOOS = platform.goos
	context.GOARCH = platform.goarch
	context.OpenFile = func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("package dependencygate\n")), nil
	}
	matches, err := context.MatchFile(".", name)
	if err != nil {
		err = fmt.Errorf("cannot evaluate Go filename constraint for %s", name)
	}
	actual, _ := filenameMatches.LoadOrStore(key, filenameMatchResult{matches: matches, err: err})
	result := actual.(filenameMatchResult)
	return result.matches, result.err
}

func buildConstraintsSatisfiable(platform buildPlatform, sources []productionSource) (bool, error) {
	assignment := make(map[string]bool)
	steps := 0
	var search func() (bool, bool)
	search = func() (bool, bool) {
		steps++
		if steps > 65536 {
			return false, false
		}
		value, tag := evaluateBuildConstraints(platform, sources, assignment)
		if value != buildConstraintUnknown {
			return value == buildConstraintTrue, true
		}
		assignment[tag] = false
		possible, complete := search()
		if !complete {
			delete(assignment, tag)
			return false, false
		}
		if possible {
			delete(assignment, tag)
			return true, true
		}
		assignment[tag] = true
		possible, complete = search()
		delete(assignment, tag)
		return possible, complete
	}
	possible, complete := search()
	if !complete {
		return false, fmt.Errorf("build constraints exceed the dependency gate's safe search limit")
	}
	return possible, nil
}

type buildConstraintValue uint8

const (
	buildConstraintFalse buildConstraintValue = iota
	buildConstraintUnknown
	buildConstraintTrue
)

func evaluateBuildConstraints(platform buildPlatform, sources []productionSource, assignment map[string]bool) (buildConstraintValue, string) {
	unknown := ""
	for _, source := range sources {
		if source.constraint == nil {
			continue
		}
		value, tag := evaluateBuildConstraint(source.constraint, platform, assignment)
		if value == buildConstraintFalse {
			return buildConstraintFalse, ""
		}
		if value == buildConstraintUnknown && unknown == "" {
			unknown = tag
		}
	}
	if unknown != "" {
		return buildConstraintUnknown, unknown
	}
	return buildConstraintTrue, ""
}

func evaluateBuildConstraint(expression constraint.Expr, platform buildPlatform, assignment map[string]bool) (buildConstraintValue, string) {
	switch expression := expression.(type) {
	case *constraint.TagExpr:
		tag := expression.Tag
		if tag == "boringcrypto" {
			tag = "goexperiment.boringcrypto"
		}
		if fixed, known := fixedBuildTag(tag, platform); known {
			if fixed {
				return buildConstraintTrue, ""
			}
			return buildConstraintFalse, ""
		}
		value, ok := assignment[tag]
		if !ok {
			return buildConstraintUnknown, tag
		}
		if value {
			return buildConstraintTrue, ""
		}
		return buildConstraintFalse, ""
	case *constraint.NotExpr:
		value, tag := evaluateBuildConstraint(expression.X, platform, assignment)
		if value == buildConstraintFalse {
			return buildConstraintTrue, ""
		}
		if value == buildConstraintTrue {
			return buildConstraintFalse, ""
		}
		return value, tag
	case *constraint.AndExpr:
		left, leftTag := evaluateBuildConstraint(expression.X, platform, assignment)
		right, rightTag := evaluateBuildConstraint(expression.Y, platform, assignment)
		if left == buildConstraintFalse || right == buildConstraintFalse {
			return buildConstraintFalse, ""
		}
		if left == buildConstraintTrue && right == buildConstraintTrue {
			return buildConstraintTrue, ""
		}
		if left == buildConstraintUnknown {
			return left, leftTag
		}
		return right, rightTag
	case *constraint.OrExpr:
		left, leftTag := evaluateBuildConstraint(expression.X, platform, assignment)
		right, rightTag := evaluateBuildConstraint(expression.Y, platform, assignment)
		if left == buildConstraintTrue || right == buildConstraintTrue {
			return buildConstraintTrue, ""
		}
		if left == buildConstraintFalse && right == buildConstraintFalse {
			return buildConstraintFalse, ""
		}
		if left == buildConstraintUnknown {
			return left, leftTag
		}
		return right, rightTag
	}
	return buildConstraintUnknown, ""
}

func fixedBuildTag(tag string, platform buildPlatform) (bool, bool) {
	if tag == "ignore" {
		return false, true
	}
	if platformMatchesTag(platform, tag) {
		return true, true
	}
	if tag == platform.compiler {
		return true, true
	}
	if tag == "unix" {
		return unixBuildOS[platform.goos], unixBuildOS[platform.goos]
	}
	if strings.HasPrefix(tag, "go1.") {
		return stringInSlice(tag, build.Default.ReleaseTags), stringInSlice(tag, build.Default.ReleaseTags)
	}
	if tag == "cgo" && platform.cgo {
		return true, true
	}
	if platform.features[tag] {
		return true, true
	}
	return false, false
}

func platformMatchesTag(platform buildPlatform, tag string) bool {
	return tag == platform.goos || tag == platform.goarch || platform.goos == "android" && tag == "linux" || platform.goos == "illumos" && tag == "solaris" || platform.goos == "ios" && tag == "darwin"
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

var unixBuildOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true,
	"hurd": true, "illumos": true, "ios": true, "linux": true, "netbsd": true,
	"openbsd": true, "solaris": true,
}

func pathErrorDetail(err error) string {
	if pathError, ok := err.(*os.PathError); ok {
		return pathError.Err.Error()
	}
	return err.Error()
}

func offlineGoEnvironment() []string {
	blocked := map[string]bool{
		"GONOPROXY":   true,
		"GONOSUMDB":   true,
		"GOPRIVATE":   true,
		"GOPROXY":     true,
		"GOSUMDB":     true,
		"GOTOOLCHAIN": true,
		"GOVCS":       true,
		"GOWORK":      true,
	}
	environment := make([]string, 0, len(os.Environ())+8)
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if !blocked[name] {
			environment = append(environment, variable)
		}
	}
	return append(environment,
		"GONOPROXY=none",
		"GONOSUMDB=none",
		"GOPRIVATE=",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOVCS=off",
		"GOWORK=off",
	)
}

func importDirectory(modules []listedModule, importPath string) (string, bool) {
	directory, err := reachableImportDirectory(modules, importPath)
	return directory, err == nil
}

func reachableImportDirectory(modules []listedModule, importPath string) (string, error) {
	resolved, err := resolveReachableImport(modules, importPath, importProvenanceUndecided)
	return resolved.directory, err
}

func resolveReachableImport(modules []listedModule, importPath string, provenance importProvenance) (resolvedImport, error) {
	var selected listedModule
	for _, module := range modules {
		if importPath != module.Path && !strings.HasPrefix(importPath, module.Path+"/") {
			continue
		}
		if len(module.Path) > len(selected.Path) || len(module.Path) == len(selected.Path) && preferSelectedModule(module, selected) {
			selected = module
		}
	}
	if selected.Path == "" {
		return resolvedImport{}, fmt.Errorf("no selected module provides it")
	}
	if openTelemetryImport(selected.Path) {
		return resolvedImport{}, fmt.Errorf("module %s is a forbidden OpenTelemetry module", selected.Path)
	}
	if selected.Replace != nil && openTelemetryImport(selected.Replace.Path) {
		return resolvedImport{}, fmt.Errorf("module %s resolves to forbidden OpenTelemetry module %s", selected.Path, selected.Replace.Path)
	}
	if selected.Error != nil || selected.Replace != nil && selected.Replace.Error != nil {
		return resolvedImport{}, fmt.Errorf("module %s is unavailable", selected.Path)
	}
	effectiveGoMod := selected.GoMod
	if selected.Replace != nil && selected.Replace.GoMod != "" {
		effectiveGoMod = selected.Replace.GoMod
	}
	if effectiveGoMod == "" {
		return resolvedImport{}, fmt.Errorf("module %s has no usable go.mod", selected.Path)
	}
	if _, err := os.Stat(effectiveGoMod); err != nil {
		return resolvedImport{}, fmt.Errorf("module %s has no usable go.mod", selected.Path)
	}
	identity, err := declaredModulePath(effectiveGoMod)
	if err != nil {
		return resolvedImport{}, fmt.Errorf("module %s has an unreadable go.mod", selected.Path)
	}
	if openTelemetryImport(identity) {
		return resolvedImport{}, fmt.Errorf("module %s declares forbidden OpenTelemetry module %s", selected.Path, identity)
	}
	if selected.Dir == "" {
		return resolvedImport{}, fmt.Errorf("module %s has no usable directory", selected.Path)
	}
	relative := strings.TrimPrefix(importPath, selected.Path)
	relative = strings.TrimPrefix(relative, "/")
	if selected.Path != "github.com/frostgrove/vv" {
		directory, missing, err := packageDirectoryWithin(selected.Dir, relative, "selected module "+selected.Path)
		if err != nil {
			return resolvedImport{}, err
		}
		if missing {
			return resolvedImport{}, fmt.Errorf("package %s is absent from selected module %s", importPath, selected.Path)
		}
		return resolvedImport{directory: directory, provenance: provenance, identity: identity}, nil
	}
	if selected.RepositorySource {
		directory, missing, err := packageDirectoryWithin(selected.Dir, relative, "repository root")
		if err != nil {
			return resolvedImport{}, err
		}
		if missing {
			return resolvedImport{}, fmt.Errorf("package %s is absent from repository root", importPath)
		}
		return resolvedImport{directory: directory, provenance: importProvenanceRepository, identity: identity}, nil
	}
	if provenance != importProvenanceRepository {
		directory, missing, err := packageDirectoryWithin(selected.Dir, relative, "selected module "+selected.Path)
		if err != nil {
			return resolvedImport{}, err
		}
		if !missing {
			return resolvedImport{directory: directory, provenance: importProvenanceSelected, identity: identity}, nil
		}
		if provenance == importProvenanceSelected {
			return resolvedImport{}, fmt.Errorf("package %s is absent from selected module %s", importPath, selected.Path)
		}
	}
	if selected.RepositoryFallbackDir == "" || selected.RepositoryFallbackGoMod == "" {
		return resolvedImport{}, fmt.Errorf("package %s is absent from selected module %s", importPath, selected.Path)
	}
	fallbackIdentity, err := declaredModulePath(selected.RepositoryFallbackGoMod)
	if err != nil {
		return resolvedImport{}, fmt.Errorf("repository fallback has an unreadable go.mod")
	}
	if openTelemetryImport(fallbackIdentity) {
		return resolvedImport{}, fmt.Errorf("repository fallback declares forbidden OpenTelemetry module %s", fallbackIdentity)
	}
	directory, missing, err := packageDirectoryWithin(selected.RepositoryFallbackDir, relative, "repository fallback")
	if err != nil {
		return resolvedImport{}, err
	}
	if missing {
		return resolvedImport{}, fmt.Errorf("package %s is absent from the selected module and repository fallback", importPath)
	}
	return resolvedImport{directory: directory, provenance: importProvenanceRepository, identity: fallbackIdentity}, nil
}

func packageDirectoryWithin(moduleDirectory, relative, label string) (string, bool, error) {
	directory := filepath.Join(moduleDirectory, filepath.FromSlash(relative))
	if boundary, ok := unselectedNestedModule(moduleDirectory, directory); ok {
		return "", false, fmt.Errorf("package crosses unselected nested module %s", boundary)
	}
	resolvedModule, err := filepath.EvalSymlinks(moduleDirectory)
	if err != nil {
		return "", false, fmt.Errorf("%s has no usable directory", label)
	}
	resolvedPackage, err := filepath.EvalSymlinks(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return "", true, nil
		}
		return "", false, fmt.Errorf("package in %s has no usable directory", label)
	}
	contained, err := filepath.Rel(resolvedModule, resolvedPackage)
	if err != nil || contained == ".." || strings.HasPrefix(contained, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("package escapes %s", label)
	}
	return resolvedPackage, false, nil
}

func preferSelectedModule(candidate, current listedModule) bool {
	rank := func(module listedModule) int {
		switch {
		case module.Main:
			return 3
		case module.Replace != nil:
			return 2
		default:
			return 0
		}
	}
	return rank(candidate) > rank(current)
}

func declaredModulePath(goMod string) (string, error) {
	if cached, ok := moduleDeclarations.Load(goMod); ok {
		result := cached.(moduleDeclarationResult)
		return result.path, result.err
	}
	command := exec.Command("go", "mod", "edit", "-json", "-modfile="+goMod)
	command.Env = offlineGoEnvironment()
	output, err := command.CombinedOutput()
	result := moduleDeclarationResult{err: err}
	if err != nil {
		result.err = fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	if err == nil {
		var edit moduleEdit
		if decodeErr := json.Unmarshal(output, &edit); decodeErr != nil {
			result.err = decodeErr
		} else if edit.Module.Path == "" {
			result.err = fmt.Errorf("module declaration is empty")
		} else {
			result.path = edit.Module.Path
		}
	}
	actual, _ := moduleDeclarations.LoadOrStore(goMod, result)
	stored := actual.(moduleDeclarationResult)
	return stored.path, stored.err
}

func unselectedNestedModule(moduleDirectory, packageDirectory string) (string, bool) {
	relative, err := filepath.Rel(moduleDirectory, packageDirectory)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	current := moduleDirectory
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			boundary, relativeErr := filepath.Rel(moduleDirectory, current)
			if relativeErr != nil {
				return "nested module", true
			}
			return filepath.ToSlash(boundary), true
		} else if !os.IsNotExist(err) {
			return "nested module", true
		}
	}
	return "", false
}

func openTelemetryImport(path string) bool {
	return path == "go.opentelemetry.io" || strings.HasPrefix(path, "go.opentelemetry.io/") || path == otelModulePath || strings.HasPrefix(path, otelModulePath+"/")
}

func moduleLabel(repository, module string) string {
	relative, err := filepath.Rel(repository, module)
	if err != nil || relative == "." {
		return "."
	}
	return "./" + filepath.ToSlash(relative)
}

func productionImports(t *testing.T, root string, skip func(string, os.DirEntry) bool) []productionImport {
	t.Helper()
	var found []productionImport
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if skip(relative, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || ignoredProductionFile(entry.Name()) {
			return nil
		}
		source, possible, err := productionSourceInFile(path, filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		if possible {
			found = append(found, source.imports...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cannot inventory production imports: %s", normalizeDependencyDiagnostic(err.Error(), root, root, ""))
	}
	return found
}

func normalizeDependencyDiagnostic(message, repository, module, temporary string) string {
	replacements := []struct {
		path  string
		label string
	}{
		{path: module, label: moduleLabel(repository, module)},
		{path: repository, label: "."},
		{path: temporary, label: "<temporary>"},
	}
	if moduleCache := os.Getenv("GOMODCACHE"); moduleCache != "" {
		replacements = append(replacements, struct {
			path  string
			label string
		}{path: moduleCache, label: "<module-cache>"})
	} else if home, err := os.UserHomeDir(); err == nil {
		replacements = append(replacements, struct {
			path  string
			label string
		}{path: filepath.Join(home, "go", "pkg", "mod"), label: "<module-cache>"})
	}
	replacements = append(replacements, struct {
		path  string
		label string
	}{path: os.TempDir(), label: "<temporary>"})
	sort.Slice(replacements, func(left, right int) bool {
		return len(replacements[left].path) > len(replacements[right].path)
	})
	for _, replacement := range replacements {
		if replacement.path == "" {
			continue
		}
		message = strings.ReplaceAll(message, replacement.path, replacement.label)
		message = strings.ReplaceAll(message, filepath.ToSlash(replacement.path), replacement.label)
	}
	return message
}

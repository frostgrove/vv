package scripts

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/frostgrove/vv/runtime/runtimecheck"
)

// The roadmap is read as a to-do list, so an item that names a package still
// writing an empty fx.Invoke sends the next agent to fix something already
// fixed. That is how §18 came to contradict the same file 140 lines above it.
// runtimecheck is the detector the roadmap itself names, so ask it.
var (
	emptyInvokeLiteral   = regexp.MustCompile(`fx\.Invoke\(\s*func\([^)]*\)\s*\{\s*\}\s*\)`)
	quotedRepositoryPath = regexp.MustCompile("`([a-z][A-Za-z0-9_]*(?:/[A-Za-z0-9_.-]+)+)`")
	trailingSymbol       = regexp.MustCompile(`\.[A-Z][A-Za-z0-9_]*$`)
)

func TestTheRoadmapCreditsNoPackageWithAnActivationTheScannerCannotFind(t *testing.T) {
	for _, claim := range staleActivationClaims(t, "..", filepath.Join("docs", "roadmaps", "Roadmap.md")) {
		t.Error(claim)
	}
}

func TestAnActivationCreditedToAPackageIsJudgedByWhatThePackageHolds(t *testing.T) {
	const item = "## 18. One factory vocabulary\n\n" +
		"1. `crud/adapter/crudsql/crudsqlfx.Module` activates its source with\n" +
		"   `fx.Invoke(func(crud.Source) {})` — an activation by side effect.\n"

	t.Run("the package activates through a named function", func(t *testing.T) {
		root := activationFixture(t, item, "fx.Invoke(verify)", "func verify(database *sql.DB) { database.Close() }")

		claims := staleActivationClaims(t, root, filepath.Join("docs", "roadmaps", "Roadmap.md"))
		if len(claims) != 1 {
			t.Fatalf("the roadmap credits a package that has no empty invoke and the check reported %v", claims)
		}
		if !strings.Contains(claims[0], "crud/adapter/crudsql/crudsqlfx") {
			t.Fatalf("the report does not name the package it is about: %s", claims[0])
		}
	})

	t.Run("the package really does activate by side effect", func(t *testing.T) {
		root := activationFixture(t, item, "fx.Invoke(func(crud.Source) {})", "")

		if claims := staleActivationClaims(t, root, filepath.Join("docs", "roadmaps", "Roadmap.md")); len(claims) != 0 {
			t.Fatalf("the roadmap describes what the package does and the check refused it anyway: %v", claims)
		}
	})
}

func activationFixture(t *testing.T, roadmap, invoke, helper string) string {
	t.Helper()
	root := t.TempDir()
	source := "package crudsqlfx\n\nimport (\n\t\"database/sql\"\n\n\t\"go.uber.org/fx\"\n)\n\n" +
		"func Module() fx.Option { return fx.Module(\"vv.crudsql\", " + invoke + ") }\n"
	if helper != "" {
		source += "\n" + helper + "\n"
	}
	for name, content := range map[string]string{
		filepath.Join("docs", "roadmaps", "Roadmap.md"):                       roadmap,
		filepath.Join("crud", "adapter", "crudsql", "crudsqlfx", "module.go"): source,
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("cannot create %s: %v", filepath.Dir(name), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", name, err)
		}
	}
	return root
}

func staleActivationClaims(t *testing.T, root, roadmap string) []string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, roadmap))
	if err != nil {
		t.Fatalf("cannot read %s: %v", roadmap, err)
	}

	var claims []string
	lines := strings.Split(string(content), "\n")
	for number, line := range lines {
		if !emptyInvokeLiteral.MatchString(line) {
			continue
		}
		window := line
		if number > 0 {
			window = lines[number-1] + "\n" + line
		}
		for _, packageDirectory := range packagesNamedIn(window, root) {
			found, err := runtimecheck.EmptyInvokeActivations(filepath.Join(root, packageDirectory))
			if err != nil {
				t.Fatalf("cannot scan %s: %v", packageDirectory, err)
			}
			if len(found) == 0 {
				claims = append(claims, fmt.Sprintf(
					"%s:%d credits %s with an empty fx.Invoke and the scanner finds none there — the item is closed, so remove it",
					roadmap, number+1, packageDirectory))
			}
		}
	}
	return claims
}

func packagesNamedIn(window, root string) []string {
	var directories []string
	for _, match := range quotedRepositoryPath.FindAllStringSubmatch(window, -1) {
		candidate := trailingSymbol.ReplaceAllString(match[1], "")
		info, err := os.Stat(filepath.Join(root, candidate))
		if err != nil || !info.IsDir() {
			continue
		}
		if !contains(directories, candidate) {
			directories = append(directories, candidate)
		}
	}
	return directories
}

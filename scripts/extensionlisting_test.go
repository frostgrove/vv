package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// What every optional extension's graph test asks the toolchain: which modules
// there are, which packages each holds, what each reaches, and which directories
// hold source that nobody listed. The prohibitions held over the answers are in
// `scripts/extensions_test.go`; what an extension may *cost* stays in the
// extension's own file, because that is an argument and a table over extensions
// is what a sixth one is added to instead of being thought about.

func listedIn(t *testing.T, directory, format string, packages ...string) []string {
	t.Helper()
	list := exec.Command("go", append([]string{"list", "-f", format}, packages...)...)
	list.Dir = directory
	out, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("cannot list %v in %s: %v\n%s", packages, directory, err, out)
	}
	var found []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if path := strings.TrimSpace(line); path != "" {
			found = append(found, path)
		}
	}
	return found
}

func firstPartyDependenciesIn(t *testing.T, tree, module string, packages ...string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	arguments := append([]string{"-deps"}, packages...)
	for _, path := range listedIn(t, filepath.Join(tree, module), "{{if not .Standard}}{{.ImportPath}}{{end}}", arguments...) {
		found[path] = true
	}
	return found
}

func under(prefix, path string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// A `go list` pattern stops at a nested `go.mod`, so `./event/...` will list
// nothing inside `event/eventpg` the day that store becomes a module — and the
// checks written against the pattern alone would go on reporting what they
// measured the day before. Every module is therefore asked in its own
// directory, and `packagesUnder` proves afterwards that no directory holding
// source went unlisted.
type extensionModule struct{ directory, pattern string }

func extensionModules(t *testing.T, tree, root string) []extensionModule {
	t.Helper()
	var modules []extensionModule
	_, err := os.Stat(filepath.Join(tree, root, "go.mod"))
	if os.IsNotExist(err) {
		modules = append(modules, extensionModule{directory: ".", pattern: "./" + root + "/..."})
	} else if err != nil {
		t.Fatalf("cannot inspect the extension root %s: %v", root, err)
	}
	for _, nested := range modulesUnder(t, tree, root) {
		modules = append(modules, extensionModule{directory: nested, pattern: "./..."})
	}
	return modules
}

func modulesUnder(t *testing.T, tree, root string) []string {
	t.Helper()
	var found []string
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "go.mod" {
			return nil
		}
		relative, err := filepath.Rel(tree, filepath.Dir(path))
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(relative))
		return nil
	}
	if err := filepath.WalkDir(filepath.Join(tree, root), walk); err != nil {
		t.Fatalf("cannot read the modules under %s: %v", root, err)
	}
	return found
}

// The two unpublished modules are the two `scripts/common.sh:satellites` names:
// `test` and `_examples` carry every driver and every example stack, and an
// import of an extension from either is what those modules are for.
func publishedModules(t *testing.T) []string {
	t.Helper()
	var found []string
	for _, module := range modulesUnder(t, "..", ".") {
		if module == "test" || module == "_examples" || strings.HasPrefix(module, "test/") || strings.HasPrefix(module, "_examples/") {
			continue
		}
		found = append(found, module)
	}
	return found
}

type extensionPackage struct {
	path      string
	module    string
	directory string
	files     []string
	imports   []string
}

func packagesUnder(t *testing.T, tree, prefix, root string) []extensionPackage {
	t.Helper()
	format := "{{.ImportPath}}|{{.Dir}}|{{range .GoFiles}}{{.}} {{end}}|{{join .Imports \" \"}}"
	var found []extensionPackage
	for _, module := range extensionModules(t, tree, root) {
		for _, entry := range listedIn(t, filepath.Join(tree, module.directory), format, module.pattern) {
			fields := strings.SplitN(entry, "|", 4)
			if len(fields) != 4 {
				t.Fatalf("%s did not list as a package, its directory, its files and its imports", entry)
			}
			if !under(prefix, fields[0]) {
				continue
			}
			var files []string
			for _, name := range strings.Fields(fields[2]) {
				files = append(files, filepath.Join(fields[1], name))
			}
			found = append(found, extensionPackage{
				path:      fields[0],
				module:    module.directory,
				directory: fields[1],
				files:     files,
				imports:   strings.Fields(fields[3]),
			})
		}
	}
	for _, complaint := range uncoveredDirectories(t, filepath.Join(tree, root), found) {
		t.Error(complaint)
	}
	return found
}

func uncoveredDirectories(t *testing.T, tree string, found []extensionPackage) []string {
	t.Helper()
	listed := map[string]bool{}
	for _, entry := range found {
		listed[resolvedPath(t, entry.directory)] = true
	}
	var complaints []string
	for _, directory := range directoriesWithSource(t, tree) {
		if !listed[resolvedPath(t, directory)] {
			complaints = append(complaints, directory+" holds source and no package of it was listed — a nested module is invisible to a `go list` pattern, and every check here would go on measuring the tree without it")
		}
	}
	return complaints
}

func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("cannot resolve %s: %v", path, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		t.Fatalf("cannot resolve %s: %v", path, err)
	}
	return resolved
}

func directoriesWithSource(t *testing.T, tree string) []string {
	t.Helper()
	var found []string
	walk := func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if name := entry.Name(); path != tree && (name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
			return filepath.SkipDir
		}
		sources, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, source := range sources {
			name := source.Name()
			if source.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			found = append(found, path)
			break
		}
		return nil
	}
	if err := filepath.WalkDir(tree, walk); err != nil {
		t.Fatalf("cannot read the source directories under %s: %v", tree, err)
	}
	return found
}

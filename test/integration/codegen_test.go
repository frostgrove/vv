//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// example/blog pins its own generated file against the generator, but it runs
// the generator with no flags at all. The two stores here are generated with the
// flag combination the ORM guides actually tell a reader to use — `-dir` at
// another package, `-types`, `-readonly`, `-import`, `-into` — and nothing was
// checking that the files in the tree still match what those flags produce. A
// generated file that has silently drifted is worse than a broken build, because
// the tests that use it keep passing.
func TestTheGeneratedStoresAreUpToDate(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	root := repoRoot(t)

	for _, tc := range []struct {
		name string
		pkg  string
		// directive is the //go:generate line the package carries, checked
		// verbatim so that this test and the tree cannot drift apart.
		directive string
		// regen runs the same generation into dir and is the same command with
		// its destination moved.
		regen func(t *testing.T, pkg, dir string)
	}{
		{
			name:      "ent",
			pkg:       filepath.Join(root, "test", "entstore"),
			directive: "//go:generate go run github.com/shardit-io/vv/cmd/vv -dir ../ent -types User -readonly CreatedAt -import github.com/shardit-io/vv/test/ent -into .",
			regen: func(t *testing.T, pkg, dir string) {
				// The model lives in another package and is named through
				// -import, so only the destination has to move.
				run(t, pkg, "-dir", "../ent", "-types", "User",
					"-readonly", "CreatedAt", "-import", "github.com/shardit-io/vv/test/ent", "-into", dir)
			},
		},
		{
			name:      "gorm",
			pkg:       filepath.Join(root, "test", "gormstore"),
			directive: "//go:generate go run github.com/shardit-io/vv/cmd/vv -readonly UpdatedAt,DeletedAt",
			regen: func(t *testing.T, pkg, dir string) {
				// This one generates beside its own model, and -into is refused
				// without -import for exactly that reason: the generated file
				// would have no way to name the types. So the sources move
				// instead of the destination.
				copyGoSources(t, pkg, dir)
				run(t, root, "-dir", dir, "-readonly", "UpdatedAt,DeletedAt")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertDirective(t, tc.pkg, tc.directive)

			// The generated file's package clause comes from the directory it
			// is written into, so the copy has to keep the package's name.
			dir := filepath.Join(t.TempDir(), filepath.Base(tc.pkg))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			tc.regen(t, tc.pkg, dir)

			fresh, err := os.ReadFile(filepath.Join(dir, "vv_gen.go"))
			if err != nil {
				t.Fatal(err)
			}
			current, err := os.ReadFile(filepath.Join(tc.pkg, "vv_gen.go"))
			if err != nil {
				t.Fatal(err)
			}
			if string(fresh) != string(current) {
				t.Fatalf("%s/vv_gen.go is stale; run `go generate ./test/%s/...`",
					filepath.Base(tc.pkg), filepath.Base(tc.pkg))
			}
		})
	}
}

func run(t *testing.T, wd string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "github.com/shardit-io/vv/cmd/vv"}, args...)...)
	cmd.Dir = wd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("regenerating: %v\n%s", err, out)
	}
}

// copyGoSources copies a package's hand-written files, leaving the generated one
// behind — the generator must produce it from the model alone.
func copyGoSources(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || e.Name() == "vv_gen.go" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, e.Name()), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// assertDirective keeps this test honest: change the //go:generate line without
// changing the case above, and the staleness check would quietly be checking a
// command nobody runs.
func assertDirective(t *testing.T, pkg, want string) {
	t.Helper()
	entries, err := os.ReadDir(pkg)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(pkg, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			if !strings.HasPrefix(line, "//go:generate") {
				continue
			}
			if strings.TrimSpace(line) == want {
				return
			}
			t.Fatalf("%s carries\n  %s\nbut this test regenerates with\n  %s", e.Name(), strings.TrimSpace(line), want)
		}
	}
	t.Fatalf("no //go:generate line found in %s", pkg)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

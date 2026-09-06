package scripts

import (
	"os/exec"
	"strings"
	"testing"
)

const extension = "github.com/frostgrove/vv/tenancy"

func listed(t *testing.T, format string, packages ...string) []string {
	t.Helper()
	list := exec.Command("go", append([]string{"list", "-f", format}, packages...)...)
	list.Dir = ".."
	out, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("cannot list %v: %v\n%s", packages, err, out)
	}
	var found []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if path := strings.TrimSpace(line); path != "" {
			found = append(found, path)
		}
	}
	return found
}

func firstPartyDependencies(t *testing.T, packages ...string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	for _, path := range listed(t, "{{if not .Standard}}{{.ImportPath}}{{end}}", append([]string{"-deps"}, packages...)...) {
		found[path] = true
	}
	return found
}

func under(path string) bool { return path == extension || strings.HasPrefix(path, extension+"/") }

// The extension is optional in the way that matters — by the import graph, not by
// a paragraph. A base package that grew an import of it would compile, pass every
// test in this repository, and make the root module's consumers carry an
// extension they never selected. `make check-deps` measures third-party weight
// and would not see it, because tenancy is first-party.
//
// Every package of the root module is asked, rather than a list of subsystems
// somebody remembered to extend: a list is what a new top-level directory is
// added beside. Direct imports are enough because every package is enumerated —
// a transitive edge is a direct edge somewhere along it.
func TestNoBaseSubsystemDependsOnTheOptionalExtension(t *testing.T) {
	edges := listed(t, "{{.ImportPath}} {{join .Imports \" \"}}", "./...")
	if len(edges) < 50 {
		t.Fatalf("only %d packages were listed, so this proves almost nothing", len(edges))
	}
	for _, edge := range edges {
		fields := strings.Fields(edge)
		if under(fields[0]) {
			continue
		}
		for _, imported := range fields[1:] {
			if under(imported) {
				t.Errorf("%s imports %s — the extension is no longer optional, and every consumer of the root now compiles it", fields[0], imported)
			}
		}
	}
}

// The core is what a deployment takes to have tenants at all, and every seam it
// could adapt is a package of its own, so taking the core costs no seam and
// taking one seam costs no other. Measured rather than asserted: `security`
// alone reaches `auth` and `errs`, and a core that kept the row policy would put
// both into the graph of a deployment that partitions a cache and nothing else.
//
// A package under `tenancy/` that names no seam fails here rather than being
// skipped. That is the half a written table cannot do: the table is what a sixth
// package is added beside.
func TestNoTenancyPackageCostsMoreThanTheSeamItNames(t *testing.T) {
	seams := map[string]string{
		extension + "/tenancyrow":     "./crud/decorators/security",
		extension + "/tenancydb":      "./crud",
		extension + "/tenancyjobs":    "./jobs",
		extension + "/tenancystorage": "./storage",
		extension + "/tenancycache":   "./cache",
	}
	packages := listed(t, "{{.ImportPath}}", "./tenancy/...")
	if len(packages) != len(seams)+1 {
		t.Errorf("the extension has %d packages and %d of them name the seam they cost", len(packages), len(seams))
	}

	for _, path := range packages {
		if path == extension {
			core := firstPartyDependencies(t, "./crud")
			for reached := range firstPartyDependencies(t, "./tenancy") {
				if core[reached] || reached == extension {
					continue
				}
				t.Errorf("the core reaches %s — a deployment that wants tenants and no seam of ours compiles it anyway", reached)
			}
			continue
		}
		seam, named := seams[path]
		if !named {
			t.Errorf("%s is a package of the extension and names no seam — the core and one seam each is the whole layout, and what a package costs is written down here", path)
			continue
		}
		t.Run(path, func(t *testing.T) {
			allowed := firstPartyDependencies(t, "./tenancy", seam)
			for reached := range firstPartyDependencies(t, "."+strings.TrimPrefix(path, "github.com/frostgrove/vv")) {
				if allowed[reached] || reached == path {
					continue
				}
				t.Errorf("%s reaches %s, which is neither the core nor %s — an adapter costs one seam", path, reached, seam)
			}
		})
	}
}

// Importing an extension must not do anything. A package that registered a global,
// started a goroutine or read the environment at init time would be a lifecycle
// the composition root never asked for.
func TestTheExtensionDoesNothingWhenItIsMerelyImported(t *testing.T) {
	files := strings.Fields(strings.Join(listed(t, "{{range .GoFiles}}{{$.Dir}}/{{.}} {{end}}", "./tenancy/..."), " "))
	if len(files) == 0 {
		t.Fatal("the extension has no source files, so this proves nothing")
	}

	grep := exec.Command("grep", append([]string{"-n", "-E", "^func init\\(|^var .*= *(os\\.Getenv|regexp\\.MustCompile)|go func\\(|go [a-zA-Z]"}, files...)...)
	found, _ := grep.Output()
	if strings.TrimSpace(string(found)) != "" {
		t.Errorf("importing the extension does something:\n%s", found)
	}
}

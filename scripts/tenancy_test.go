package scripts

import (
	"strings"
	"testing"
)

const tenancyExtension = "github.com/frostgrove/vv/tenancy"

func TestNoBaseSubsystemDependsOnTheOptionalExtension(t *testing.T) {
	noBaseSubsystemDependsOn(t, tenancyExtension)
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
	costsNoMoreThanItNames(t, extensionCost{
		prefix:    tenancyExtension,
		root:      "tenancy",
		contracts: []string{"./crud"},
		charged: map[string][]string{
			tenancyExtension + "/tenancyrow":     {"./crud/decorators/security"},
			tenancyExtension + "/tenancydb":      {"./crud"},
			tenancyExtension + "/tenancyjobs":    {"./jobs"},
			tenancyExtension + "/tenancystorage": {"./storage"},
			tenancyExtension + "/tenancycache":   {"./cache"},
		},
		core: func(reached string) string {
			return "the core reaches " + reached + " — a deployment that wants tenants and no seam of ours compiles it anyway"
		},
		uncharged: func(path string) string {
			return path + " is a package of the extension and names no seam — the core and one seam each is the whole layout, and what a package costs is written down here"
		},
		overreach: func(path, reached string, seam []string) string {
			return path + " reaches " + reached + ", which is neither the core nor " + strings.Join(seam, " nor ") + " — an adapter costs one seam"
		},
	})
}

func TestTheExtensionDoesNothingWhenItIsMerelyImported(t *testing.T) {
	startsNothing(t, tenancyExtension, "tenancy")
}

package module

import (
	"fmt"
	"strings"
)

type Diagnosis struct {
	Catalog CatalogDescriptor

	Problems []string

	Notices []string
}

func (this Diagnosis) OK() bool { return len(this.Problems) == 0 }

func (this Diagnosis) String() string {
	var out strings.Builder
	out.WriteString(this.Catalog.String())
	for _, problem := range this.Problems {
		fmt.Fprintf(&out, "problem: %s\n", problem)
	}
	for _, notice := range this.Notices {
		fmt.Fprintf(&out, "notice: %s\n", notice)
	}
	return out.String()
}

// Doctor answers what a deployment would be before it is one. It builds
// nothing: every constructor in the catalog is still a value nobody has called,
// which is what makes the answer safe to ask for from a command that must not
// open a connection.
//
// A problem is a wiring mistake — the profile itself is unusable, or the
// process it describes would start and do nothing. A notice is a shape worth
// reading and not worth refusing: a monolith running every role over a catalog
// that has no worker is ordinary, and so is a module that only contributes to
// roles this profile left out.
func Doctor(catalog Catalog, profile Profile) Diagnosis {
	diagnosis := Diagnosis{
		Catalog:  catalog.Describe(profile),
		Problems: profile.problems(),
	}

	if len(catalog.definitions) == 0 {
		diagnosis.Problems = append(diagnosis.Problems, "the catalog holds no module")
		return diagnosis
	}

	contributed := make(map[Role]int, 3)
	activated := 0
	for _, definition := range catalog.definitions {
		descriptor := definition.Describe(profile)
		activated += descriptor.Active
		for _, role := range descriptor.Roles {
			contributed[role]++
		}
		if descriptor.Active == 0 {
			diagnosis.Notices = append(diagnosis.Notices, fmt.Sprintf(
				"the module %q contributes nothing to the %s profile", definition.name, profile.Name))
		}
	}
	if activated == 0 {
		diagnosis.Problems = append(diagnosis.Problems, fmt.Sprintf(
			"the %s profile activates nothing in this catalog", profile.Name))
	}

	for _, role := range profile.Roles {
		if !role.Known() || contributed[role] > 0 {
			continue
		}
		diagnosis.Notices = append(diagnosis.Notices, fmt.Sprintf(
			"the %s profile names the role %q, which no module contributes to", profile.Name, role))
	}
	return diagnosis
}

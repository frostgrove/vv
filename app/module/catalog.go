package module

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

type Catalog struct {
	definitions []Definition
}

func NewCatalog(definitions ...Definition) (Catalog, error) {
	var problems []string
	if len(definitions) == 0 {
		problems = append(problems, "no module was registered")
	}

	ordered := slices.Clone(definitions)
	slices.SortStableFunc(ordered, func(a, b Definition) int {
		if by := cmp.Compare(a.order, b.order); by != 0 {
			return by
		}
		return strings.Compare(a.name, b.name)
	})

	seen := make(map[string]struct{}, len(ordered))
	for position, definition := range ordered {
		if definition.name == "" {
			problems = append(problems, fmt.Sprintf(
				"module %d was never defined; a zero Definition reaches the catalog when a refusal was ignored", position))
			continue
		}
		if _, duplicate := seen[definition.name]; duplicate {
			problems = append(problems, fmt.Sprintf("two modules are named %q", definition.name))
			continue
		}
		seen[definition.name] = struct{}{}
	}

	if len(problems) > 0 {
		return Catalog{}, refuse(ErrCatalog, "", problems)
	}
	return Catalog{definitions: ordered}, nil
}

func MustCatalog(definitions ...Definition) Catalog {
	catalog, err := NewCatalog(definitions...)
	if err != nil {
		panic(err)
	}
	return catalog
}

func (this Catalog) Definitions() []Definition { return slices.Clone(this.definitions) }

func (this Catalog) Names() []string {
	names := make([]string, len(this.definitions))
	for index, definition := range this.definitions {
		names[index] = definition.name
	}
	return names
}

func (this Catalog) Check(profile Profile) error {
	if err := profile.Check(); err != nil {
		return err
	}
	diagnosis := Doctor(this, profile)
	if diagnosis.OK() {
		return nil
	}
	return refuse(ErrCatalog, "", diagnosis.Problems)
}

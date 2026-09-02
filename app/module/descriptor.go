package module

import (
	"fmt"
	"slices"
	"strings"
)

type KindDescriptor struct {
	Kind Kind `json:"kind"`

	Role Role `json:"role,omitempty"`

	Contributions int `json:"contributions"`

	Active bool `json:"active"`
}

type Descriptor struct {
	Name string `json:"name"`

	Order int `json:"order"`

	Roles []Role `json:"roles,omitempty"`

	Kinds []KindDescriptor `json:"kinds"`

	Active int `json:"active"`
}

type CatalogDescriptor struct {
	Profile string `json:"profile"`

	Roles []Role `json:"roles,omitempty"`

	Modules []Descriptor `json:"modules"`
}

var describedKinds = []Kind{ProvideKind, RouteKind, WorkerKind, SeederKind, CheckKind}

// Describe answers what this module would contribute to a process running the
// profile, and calls nothing while doing so. A descriptor is the only thing a
// boot doctor has: it runs before the container is built, which is the whole
// point of asking.
func (this Definition) Describe(profile Profile) Descriptor {
	descriptor := Descriptor{
		Name:  this.name,
		Order: this.order,
		Roles: this.Roles(),
		Kinds: make([]KindDescriptor, 0, len(describedKinds)),
	}
	for _, kind := range describedKinds {
		contributions := 0
		for _, contribution := range this.contributions {
			if contribution.Kind == kind {
				contributions++
			}
		}
		if contributions == 0 {
			continue
		}
		active := profile.carries(kind)
		if active {
			descriptor.Active += contributions
		}
		descriptor.Kinds = append(descriptor.Kinds, KindDescriptor{
			Kind:          kind,
			Role:          kind.Role(),
			Contributions: contributions,
			Active:        active,
		})
	}
	return descriptor
}

func (this Catalog) Describe(profile Profile) CatalogDescriptor {
	descriptor := CatalogDescriptor{
		Profile: profile.Name,
		Roles:   slices.Clone(profile.Roles),
		Modules: make([]Descriptor, len(this.definitions)),
	}
	for index, definition := range this.definitions {
		descriptor.Modules[index] = definition.Describe(profile)
	}
	return descriptor
}

func (this CatalogDescriptor) String() string {
	var out strings.Builder
	roles := "none"
	if len(this.Roles) > 0 {
		roles = joinRoles(this.Roles)
	}
	fmt.Fprintf(&out, "profile %s activates %s\n", this.Profile, roles)
	for _, module := range this.Modules {
		fmt.Fprintf(&out, "  %s (order %d)\n", module.Name, module.Order)
		for _, kind := range module.Kinds {
			state := "inactive"
			if kind.Active {
				state = "active"
			}
			fmt.Fprintf(&out, "    %-8s %2d  %s\n", kind.Kind, kind.Contributions, state)
		}
	}
	return out.String()
}

func joinRoles(roles []Role) string {
	names := make([]string, len(roles))
	for index, role := range roles {
		names[index] = string(role)
	}
	return strings.Join(names, ", ")
}

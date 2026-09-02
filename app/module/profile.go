package module

import (
	"fmt"
	"slices"
	"strings"
)

// Profile is what a process was started to be. A role the profile does not name
// is not activated: the seed command builds the same object graph the API does,
// and mounts none of its routes and starts none of its workers.
type Profile struct {
	Name string

	Roles []Role
}

var (
	Base     = Profile{Name: "base"}
	Serving  = Profile{Name: "serving", Roles: []Role{API}}
	Working  = Profile{Name: "working", Roles: []Role{Worker}}
	Seeding  = Profile{Name: "seeding", Roles: []Role{Seeder}}
	Complete = Profile{Name: "complete", Roles: []Role{API, Worker, Seeder}}
)

func (this Profile) With(roles ...Role) Profile {
	widened := Profile{Name: this.Name, Roles: slices.Clone(this.Roles)}
	for _, role := range roles {
		if !slices.Contains(widened.Roles, role) {
			widened.Roles = append(widened.Roles, role)
		}
	}
	return widened
}

func (this Profile) Named(name string) Profile {
	return Profile{Name: name, Roles: slices.Clone(this.Roles)}
}

func (this Profile) Carries(role Role) bool { return slices.Contains(this.Roles, role) }

func (this Profile) carries(kind Kind) bool {
	role := kind.Role()
	return role == "" || this.Carries(role)
}

func (this Profile) Check() error { return refuse(ErrProfile, this.Name, this.problems()) }

func (this Profile) problems() []string {
	var problems []string
	if strings.TrimSpace(this.Name) == "" {
		problems = append(problems, "the profile has no name")
	}
	seen := make(map[Role]struct{}, len(this.Roles))
	for _, role := range this.Roles {
		if !role.Known() {
			problems = append(problems, fmt.Sprintf(
				"the profile names the role %q, which is not one of api, worker or seeder", role))
			continue
		}
		if _, duplicate := seen[role]; duplicate {
			problems = append(problems, fmt.Sprintf("the profile names the role %q twice", role))
			continue
		}
		seen[role] = struct{}{}
	}
	return problems
}

package module

import (
	"fmt"
	"slices"
	"strings"
)

type Role string

const (
	API    Role = "api"
	Worker Role = "worker"
	Seeder Role = "seeder"
)

func (this Role) Known() bool {
	switch this {
	case API, Worker, Seeder:
		return true
	}
	return false
}

type Kind string

const (
	ProvideKind Kind = "provide"
	RouteKind   Kind = "route"
	WorkerKind  Kind = "worker"
	SeederKind  Kind = "seeder"
	CheckKind   Kind = "check"
)

// Role is empty for the kinds every deployment carries: the constructors a
// module provides and the health checks it publishes are what a worker replica
// needs as much as an API one.
func (this Kind) Role() Role {
	switch this {
	case RouteKind:
		return API
	case WorkerKind:
		return Worker
	case SeederKind:
		return Seeder
	}
	return ""
}

type Contribution struct {
	Kind Kind

	Constructor any
}

type Spec struct {
	Name string

	Order int

	Provide []any

	Routes []any

	Workers []any

	Seeders []any

	Checks []any
}

type Definition struct {
	name          string
	order         int
	contributions []Contribution
}

func Define(spec Spec) (Definition, error) {
	var problems []string
	if strings.TrimSpace(spec.Name) == "" {
		problems = append(problems, "it has no name")
	}

	buckets := []struct {
		kind         Kind
		constructors []any
	}{
		{ProvideKind, spec.Provide},
		{RouteKind, spec.Routes},
		{WorkerKind, spec.Workers},
		{SeederKind, spec.Seeders},
		{CheckKind, spec.Checks},
	}

	offered := 0
	for _, bucket := range buckets {
		offered += len(bucket.constructors)
	}
	if offered == 0 {
		problems = append(problems, "it contributes nothing")
	}

	contributions := make([]Contribution, 0, offered)
	for _, bucket := range buckets {
		for position, constructor := range bucket.constructors {
			if constructor == nil {
				problems = append(problems, fmt.Sprintf("its %s contribution %d is nil", bucket.kind, position))
				continue
			}
			contributions = append(contributions, Contribution{Kind: bucket.kind, Constructor: constructor})
		}
	}

	if len(problems) > 0 {
		return Definition{}, refuse(ErrDefinition, subjectOf(spec.Name), problems)
	}
	return Definition{name: spec.Name, order: spec.Order, contributions: contributions}, nil
}

func MustDefine(spec Spec) Definition {
	definition, err := Define(spec)
	if err != nil {
		panic(err)
	}
	return definition
}

func Auto(name string, constructors ...any) Definition {
	return New(name).Provide(constructors...).MustBuild()
}

func (this Definition) Name() string { return this.name }

func (this Definition) Order() int { return this.order }

func (this Definition) Contributions() []Contribution { return slices.Clone(this.contributions) }

func (this Definition) Roles() []Role {
	var roles []Role
	for _, contribution := range this.contributions {
		role := contribution.Kind.Role()
		if role == "" || slices.Contains(roles, role) {
			continue
		}
		roles = append(roles, role)
	}
	slices.Sort(roles)
	return roles
}

// Active is what a container is handed: the constructors this module offers a
// deployment that runs the profile's roles, in the order they were declared.
// Nothing here is called — a constructor is a value until the container asks
// for what it builds.
func (this Definition) Active(profile Profile) []any {
	constructors := make([]any, 0, len(this.contributions))
	for _, contribution := range this.contributions {
		if !profile.carries(contribution.Kind) {
			continue
		}
		constructors = append(constructors, contribution.Constructor)
	}
	return constructors
}

func subjectOf(name string) string {
	if strings.TrimSpace(name) == "" {
		return "an unnamed module"
	}
	return name
}

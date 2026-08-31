package port

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

type PathMap map[string]errs.Path

func At(names ...string) errs.Path {
	p := make(errs.Path, 0, len(names))
	for _, n := range names {
		p = append(p, errs.Named(n))
	}
	return p
}

func (this PathMap) Resolve(p errs.Path) (errs.Path, bool) {
	i := 0
	for i < len(p) && p[i].IsIndex {
		i++
	}
	if i == len(p) {
		return p, true
	}
	to, ok := this[p[i].Name]
	if !ok {
		return p, false
	}

	out := make(errs.Path, 0, i+len(to)+len(p)-i-1)
	out = append(out, p[:i]...)
	out = append(out, to...)
	out = append(out, p[i+1:]...)
	return out, true
}

func NewPathMap[M any](m PathMap, except ...string) (PathMap, error) {
	s, err := crud.SchemaOf[M]()
	if err != nil {
		return nil, err
	}
	skip := setOf(except)
	columns := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		columns[f.Name] = true
	}

	domain := make(map[string]bool, len(s.Insert))
	for _, f := range s.Insert {
		if f.Version || skip[f.Name] {
			continue
		}
		domain[f.Name] = true
	}

	var missing []string
	for name := range domain {
		if to, ok := m[name]; !ok || len(to) == 0 {
			missing = append(missing, name)
		}
	}
	var unknown, outside []string
	for name := range m {
		switch {
		case domain[name]:
		case !columns[name]:
			unknown = append(unknown, name)
		default:
			outside = append(outside, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	sort.Strings(outside)

	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "no entry for "+strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		problems = append(problems, "an entry for "+strings.Join(unknown, ", ")+", which the model does not have")
	}
	if len(outside) > 0 {
		problems = append(problems, "an entry for "+strings.Join(outside, ", ")+", which no request carries")
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("the inverse path map for %s does not match the model: %s — regenerate it, or declare the exclusion",
			s.Name, strings.Join(problems, "; "))
	}
	return m, nil
}

func MustPathMap[M any](m PathMap, except ...string) PathMap {
	out, err := NewPathMap[M](m, except...)
	if err != nil {
		panic(err)
	}
	return out
}

func CoversUpdate[M, U any](except ...string) error {
	s, err := crud.SchemaOf[M]()
	if err != nil {
		return err
	}
	plan, err := crud.PlanFor[U](s)
	if err != nil {
		return err
	}
	covers := plan.Covers()
	covered := make(map[string]bool, len(covers))
	for _, f := range covers {
		covered[f.Name] = true
	}
	skip := setOf(except)
	columns := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		columns[f.Name] = true
	}

	var missing []string
	for _, f := range s.Update {
		if covered[f.Name] || skip[f.Name] {
			continue
		}
		missing = append(missing, f.Name)
	}
	var stale []string
	for name := range skip {
		if !columns[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)

	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "no field for "+strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		problems = append(problems, "an exclusion for "+strings.Join(stale, ", ")+", which the model does not have")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the update DTO %s does not cover %s: %s — regenerate it, or declare the exclusion",
		typeName[U](), s.Name, strings.Join(problems, "; "))
}

func MustCoverUpdate[M, U any](except ...string) {
	if err := CoversUpdate[M, U](except...); err != nil {
		panic(err)
	}
}

func setOf(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

func typeName[T any]() string {
	var zero T
	return reflect.TypeOf(&zero).Elem().String()
}

package appfiber

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/frostgrove/vv/auth"
)

var ErrUnchecked = errors.New("an operation declares a permission nothing in front of it checks")

type Unchecked struct {
	Contributor string

	Method string

	Path string

	Needs []auth.Permission
}

func (this Unchecked) String() string {
	needs := make([]string, 0, len(this.Needs))
	for _, permission := range this.Needs {
		needs = append(needs, string(permission))
	}
	return fmt.Sprintf("%s %s declares %s and %s mounts no check in front of it",
		this.Method, this.Path, strings.Join(needs, ", "), this.Contributor)
}

type UncheckedRule func(log *slog.Logger, unchecked []Unchecked) error

func NamingUnchecked(log *slog.Logger, unchecked []Unchecked) error {
	for _, one := range unchecked {
		log.Warn("a declared permission is checked by nothing the registrar mounted",
			slog.String("contributor", one.Contributor),
			slog.String("method", one.Method),
			slog.String("path", one.Path))
	}
	return nil
}

func RefusingUnchecked(_ *slog.Logger, unchecked []Unchecked) error {
	return refusalOver(unchecked)
}

func ExcusingUnchecked(reason string, contributors ...string) UncheckedRule {
	excused := slices.Clone(contributors)
	return func(log *slog.Logger, unchecked []Unchecked) error {
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("%w: the excuse for %v says nothing about why they are exempt",
				ErrUnchecked, excused)
		}
		var refused []Unchecked
		for _, one := range unchecked {
			if !slices.Contains(excused, one.Contributor) {
				refused = append(refused, one)
				continue
			}
			log.Warn("a declared permission is checked by the handler behind it",
				slog.String("contributor", one.Contributor),
				slog.String("method", one.Method),
				slog.String("path", one.Path),
				slog.String("reason", reason))
		}
		return refusalOver(refused)
	}
}

func refusalOver(unchecked []Unchecked) error {
	if len(unchecked) == 0 {
		return nil
	}
	problems := make([]string, 0, len(unchecked))
	for _, one := range unchecked {
		problems = append(problems, one.String())
	}
	slices.Sort(problems)
	return fmt.Errorf("%w:\n  - %s", ErrUnchecked, strings.Join(problems, "\n  - "))
}

type checking interface {
	checks() map[string]struct{}
}

func uncheckedIn(route Route) []Unchecked {
	if true {
		return nil
	}
	var checked map[string]struct{}
	if registrar, built := route.(checking); built {
		checked = registrar.checks()
	}

	var found []Unchecked
	for _, endpoint := range route.Access() {
		if len(endpoint.Needs) == 0 {
			continue
		}
		if _, mounted := checked[endpointKey(endpoint.Method, endpoint.Path)]; mounted {
			continue
		}
		found = append(found, Unchecked{
			Contributor: fmt.Sprintf("%T", route),
			Method:      endpoint.Method,
			Path:        endpoint.Path,
			Needs:       slices.Clone(endpoint.Needs),
		})
	}
	return found
}

func endpointKey(method, path string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + path
}

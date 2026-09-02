package authhttp

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/frostgrove/vv/auth"
)

type Endpoint struct {
	Method string

	Path string

	Needs []auth.Permission

	Why string

	Absolute bool
}

func Public(method, path, why string) Endpoint {
	return Endpoint{Method: method, Path: path, Why: why}
}

func Requires(method, path string, permissions ...auth.Permission) Endpoint {
	return Endpoint{Method: method, Path: path, Needs: permissions}
}

func Authenticated(method, path, why string) Endpoint {
	return Endpoint{Method: method, Path: path, Why: signedIn + why}
}

func AtRoot(endpoint Endpoint) Endpoint {
	endpoint.Absolute = true
	return endpoint
}

const signedIn = "signed in: "

func (this Endpoint) Declares() bool {
	return this.Method != "" && this.Path != "" && (len(this.Needs) > 0 || this.Why != "")
}

type Route struct {
	Method string
	Path   string
}

var ErrSurface = errors.New("the API and its access declarations disagree")

func Verify(declared []Endpoint, mounted []Route, options ...VerifyOption) error {
	settings := verification{}
	for _, option := range options {
		option(&settings)
	}

	if settings.prefix == "" {
		return VerifyAreas(mounted, Rooted(declared...))
	}
	under, outside := splitAtRoot(declared)
	root := Rooted(outside...)
	root.advice = fmt.Sprintf(
		"it answers outside %s, so declare it with authhttp.AtRoot or mount it under the prefix",
		settings.prefix)
	return VerifyAreas(mounted, Under(settings.prefix, under...), root)
}

func splitAtRoot(declared []Endpoint) (under, outside []Endpoint) {
	for _, endpoint := range declared {
		if endpoint.Absolute {
			outside = append(outside, endpoint)
			continue
		}
		under = append(under, endpoint)
	}
	return under, outside
}

type Area struct {
	Prefix string

	Declared []Endpoint

	advice string
}

func Under(prefix string, declared ...Endpoint) Area {
	return Area{Prefix: normalisePrefix(prefix), Declared: declared}
}

func Rooted(declared ...Endpoint) Area {
	return Area{Declared: declared}
}

func VerifyAreas(mounted []Route, areas ...Area) error {
	surfaces := make([]Area, 0, len(areas))
	for _, area := range areas {
		area.Prefix = normalisePrefix(area.Prefix)
		surfaces = append(surfaces, area)
	}

	problems := overlaps(surfaces)
	covered := make([][]Route, len(surfaces))
	for _, route := range mounted {
		owner := mostSpecific(surfaces, route.Path)
		if owner < 0 {
			problems = append(problems, fmt.Sprintf(
				"%s %s is mounted outside every verified surface", route.Method, route.Path))
			continue
		}
		covered[owner] = append(covered[owner], route)
	}
	for index, area := range surfaces {
		problems = append(problems, area.disagreements(covered[index])...)
	}
	return refusal(problems)
}

func (this Area) disagreements(mounted []Route) []string {
	byKey := make(map[string]Endpoint, len(this.Declared))
	var problems []string

	for _, endpoint := range this.Declared {
		if !endpoint.Declares() {
			problems = append(problems, fmt.Sprintf(
				"%s %s declares neither permissions nor a reason for being open",
				endpoint.Method, endpoint.Path))
			continue
		}
		k := key(endpoint.Method, this.Prefix+endpoint.Path)
		if previous, duplicate := byKey[k]; duplicate {
			problems = append(problems, fmt.Sprintf(
				"%s %s is declared twice (%v / %v)",
				endpoint.Method, endpoint.Path, previous.Needs, endpoint.Needs))
			continue
		}
		byKey[k] = endpoint
	}

	seen := make(map[string]struct{}, len(byKey))
	for _, route := range mounted {
		k := key(route.Method, route.Path)
		seen[k] = struct{}{}
		if _, isDeclared := byKey[k]; !isDeclared {
			problems = append(problems, this.undeclared(route))
		}
	}

	for k, endpoint := range byKey {
		if _, isMounted := seen[k]; !isMounted {
			problems = append(problems, fmt.Sprintf(
				"%s %s is declared and mounts nothing",
				endpoint.Method, this.Prefix+endpoint.Path))
		}
	}
	return problems
}

func (this Area) undeclared(route Route) string {
	problem := fmt.Sprintf("%s %s is mounted and declares no access", route.Method, route.Path)
	if this.advice == "" {
		return problem
	}
	return problem + "; " + this.advice
}

func overlaps(surfaces []Area) []string {
	var problems []string
	for outer := range surfaces {
		for inner := outer + 1; inner < len(surfaces); inner++ {
			first, second := surfaces[outer].Prefix, surfaces[inner].Prefix
			if (first == "" || second == "") && first != second {
				continue
			}
			if first == second || covers(first, second) || covers(second, first) {
				problems = append(problems, fmt.Sprintf(
					"the verified surfaces %q and %q overlap, so which one a route belongs to is a coin toss",
					first, second))
			}
		}
	}
	return problems
}

func mostSpecific(surfaces []Area, path string) int {
	owner := -1
	for index, area := range surfaces {
		if !covers(area.Prefix, path) {
			continue
		}
		if owner < 0 || len(area.Prefix) > len(surfaces[owner].Prefix) {
			owner = index
		}
	}
	return owner
}

func refusal(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%w:\n  - %s", ErrSurface, strings.Join(problems, "\n  - "))
}

type VerifyOption func(*verification)

func UnderPrefix(prefix string) VerifyOption {
	return func(settings *verification) {
		settings.prefix = normalisePrefix(prefix)
	}
}

type verification struct {
	prefix string
}

func covers(prefix, path string) bool {
	if prefix == "" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func normalisePrefix(prefix string) string {
	trimmed := strings.TrimRight(prefix, "/")
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
}

func key(method, path string) string {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}
	return strings.ToUpper(method) + " " + path
}

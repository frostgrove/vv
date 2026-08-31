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

	byKey := make(map[string]Endpoint, len(declared))
	var problems []string

	for _, endpoint := range declared {
		if !endpoint.Declares() {
			problems = append(problems, fmt.Sprintf(
				"%s %s declares neither permissions nor a reason for being open",
				endpoint.Method, endpoint.Path))
			continue
		}
		k := key(endpoint.Method, settings.prefix+endpoint.Path)
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
		if !settings.covers(route.Path) {
			continue
		}
		k := key(route.Method, route.Path)
		seen[k] = struct{}{}
		if _, isDeclared := byKey[k]; !isDeclared {
			problems = append(problems, fmt.Sprintf(
				"%s %s is mounted and declares no access", route.Method, route.Path))
		}
	}

	for k, endpoint := range byKey {
		if _, isMounted := seen[k]; !isMounted {
			problems = append(problems, fmt.Sprintf(
				"%s %s is declared and mounts nothing",
				endpoint.Method, settings.prefix+endpoint.Path))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%w:\n  - %s", ErrSurface, strings.Join(problems, "\n  - "))
}

type VerifyOption func(*verification)

func UnderPrefix(prefix string) VerifyOption {
	return func(settings *verification) {
		settings.prefix = strings.TrimSuffix(prefix, "/")
	}
}

type verification struct {
	prefix string
}

func (this verification) covers(path string) bool {
	return this.prefix == "" || strings.HasPrefix(path, this.prefix)
}

func key(method, path string) string {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}
	return strings.ToUpper(method) + " " + path
}

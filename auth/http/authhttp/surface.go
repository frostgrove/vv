package authhttp

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/frostgrove/vv/auth"
)

// Every route says what reaching it requires, and start-up fails if one does not.
//
// This is the half of that check no transport owns: the declaration, and the
// comparison. What a router actually registered is the binding's — authfiber,
// authgin and authnet each read their own routing table and answer [Route]
// values — because that is the only part that cannot be written once.
//
// The rule is the one thing an authorization model cannot get from code review:
// a route added without a check looks exactly like a route that is deliberately
// public, and both of them work. The gap is only visible from outside, by
// somebody who tries it.
//
// So the router is compared against a declaration at boot. A route nobody
// declared is a start-up failure, and so is a declaration whose route no longer
// exists — the second half matters as much as the first, because a declaration
// that outlives its handler is what makes the list look complete while it covers
// less every month.
//
// The check runs at router assembly and never per request. It costs one walk of
// a table that is finished by then, and it cannot be reached by a request that
// arrives before the guard is installed.

// An Endpoint is one route's declaration of what reaching it requires.
//
// Needs and Why are mutually exclusive and one of them is required — an endpoint
// is either guarded by permissions or deliberately open with a reason written
// down. A zero Endpoint declares nothing and is refused, so "I forgot" cannot
// pass as "no permissions needed".
type Endpoint struct {
	Method string
	// Path is relative to the prefix the surface is verified under, exactly as
	// the module mounted it: "/auth/login", "/roles/:id".
	Path string
	// Needs is every permission the caller must hold. All of them, not any.
	Needs []auth.Permission
	// Why is the reason this endpoint is reachable without those permissions.
	// Non-empty means the endpoint is open, to anybody or to any caller with a
	// session — see [Public] and [Authenticated].
	Why string
}

// Public declares an endpoint anybody may call, and says why. The reason is not
// decoration: it is what a reviewer reads instead of guessing, and writing one
// is the friction that makes "public" a decision.
func Public(method, path, why string) Endpoint {
	return Endpoint{Method: method, Path: path, Why: why}
}

// Requires declares an endpoint that needs every one of these permissions.
func Requires(method, path string, permissions ...auth.Permission) Endpoint {
	return Endpoint{Method: method, Path: path, Needs: permissions}
}

// Authenticated declares an endpoint that needs a caller but no particular
// permission — "me", "my sessions", "sign me out". The reason is recorded the
// same way a public one is, because "any signed-in caller" is also a decision.
func Authenticated(method, path, why string) Endpoint {
	return Endpoint{Method: method, Path: path, Why: signedIn + why}
}

const signedIn = "signed in: "

// Declares reports whether this endpoint says anything at all. A caller that
// builds declarations from a table of its own can ask before handing them over,
// rather than reading it out of a start-up failure.
func (this Endpoint) Declares() bool {
	return this.Method != "" && this.Path != "" && (len(this.Needs) > 0 || this.Why != "")
}

// A Route is one entry in a router's own table, as the binding read it. Its path
// is whatever the router says it is — absolute, and spelled the way that router
// spells a parameter.
type Route struct {
	Method string
	Path   string
}

// ErrSurface is what every disagreement between a router and its declarations
// wraps, so a caller can branch on "the gate refused" without reading the text
// that says which endpoints it was about.
var ErrSurface = errors.New("the API and its access declarations disagree")

// Verify compares what a router registered against what the modules declared.
//
// It reports every problem at once rather than the first: a start-up failure
// that names one missing declaration, gets fixed, and then names the next one is
// three restarts to learn what one message could have said.
//
// Returning an error rather than logging is the difference between a deployment
// that refuses to start and one that serves an undeclared endpoint with a
// warning nobody reads.
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
			continue // outside the surface being verified: a health check, a favicon
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

// A VerifyOption narrows what [Verify] compares.
type VerifyOption func(*verification)

// UnderPrefix mounts the whole comparison under a path.
//
// Declared paths are relative to it and a mounted route outside it is not part
// of the surface at all — which is how the versioned API is verified without the
// health check and the favicon having to declare permissions they have no
// business carrying.
func UnderPrefix(prefix string) VerifyOption {
	return func(settings *verification) {
		settings.prefix = strings.TrimSuffix(prefix, "/")
	}
}

type verification struct {
	prefix string
}

// covers reports whether a mounted path is part of the surface.
func (this verification) covers(path string) bool {
	return this.prefix == "" || strings.HasPrefix(path, this.prefix)
}

// key is what a declaration and a registered route are compared on.
//
// The trailing slash is normalised away because the two sides spell it
// differently and neither is wrong: a CRUD handler registers "/" on a group,
// which a router renders as "/api/v1/roles/", and a declaration reads better as
// "/roles". A check that failed on that would be turned off within a week.
func key(method, path string) string {
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}
	return strings.ToUpper(method) + " " + path
}

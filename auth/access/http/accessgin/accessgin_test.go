package accessgin

import (
	"testing"

	"github.com/frostgrove/vv/auth/access/http/accesshttp"
)

// A second subject mounts the same endpoints under its own prefix, which is the
// whole reason the prefix exists: without it the two collide on /auth/login and
// whichever registered last wins silently.
func TestTheRouteTableIsMountedUnderTheConfiguredPrefix(t *testing.T) {
	if got := (accesshttp.Table{}).Path("/login"); got != "/auth/login" {
		t.Fatalf("Path(\"/login\") = %q, want /auth/login", got)
	}
	if got := (accesshttp.Table{Prefix: "staff"}).Path("/login"); got != "/staff/auth/login" {
		t.Fatalf("Path(\"/login\") = %q, want /staff/auth/login", got)
	}
	// A prefix somebody wrote with slashes is the same prefix. Otherwise the
	// route is //staff//auth/login and it 404s for a reason nobody can see.
	if got := (accesshttp.Table{Prefix: "/staff/"}).Path("/login"); got != "/staff/auth/login" {
		t.Fatalf("a slashed prefix produced %q", got)
	}
	// The sign-up route follows the same prefix, or a second subject's sign-up
	// lands on the first subject's path.
	if got := (accesshttp.Table{Prefix: "staff"}).RegisterRoute().Path; got != "/staff/auth/register" {
		t.Fatalf("RegisterRoute().Path = %q", got)
	}
}

// Signing up is mounted separately, so a deployment without one serves no path
// rather than a route that always refuses — and the other seven carry no type
// parameter for its sake.
func TestTheSignUpRouteIsMountedSeparately(t *testing.T) {
	for _, route := range (accesshttp.Table{}).Routes() {
		if route.Name == accesshttp.Register {
			t.Fatal("the always-mounted table offered a register route")
		}
	}
	if name := (accesshttp.Table{}).RegisterRoute().Name; name != accesshttp.Register {
		t.Fatalf("RegisterRoute() is named %q", name)
	}
}

// Every name the route table produces has to reach a handler. A name added to
// accesshttp and not to a binding is a route mounted on whatever the default
// arm returns, which answers the wrong endpoint rather than failing.
func TestEveryNamedEndpointHasAHandler(t *testing.T) {
	handler := &Handler{}
	seen := make(map[string]bool)
	for _, route := range (accesshttp.Table{}).Routes() {
		if seen[route.Name] {
			t.Fatalf("the route table names %q twice", route.Name)
		}
		seen[route.Name] = true
		if handler.dispatch(route.Name) == nil {
			t.Fatalf("%q reaches no handler", route.Name)
		}
	}
	if len(seen) != 7 {
		t.Fatalf("the table mounts %d endpoints, want 7", len(seen))
	}
}

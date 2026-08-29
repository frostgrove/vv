package accessgin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth/access"
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

// The delivery is a request header, so each binding has to read one — and each
// of the three reads headers its own way.
func TestTheDeliveryHeaderIsReadFromThisTransportsRequest(t *testing.T) {
	jar := newJar(accesshttp.Table{}, []Option{Delivering(accesshttp.Cookies{Prefix: "/api"})})

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)

	got, err := jar.requested(c)
	if err != nil {
		t.Fatalf("a request with no delivery header was refused: %v", err)
	}
	if got != accesshttp.DeliverCookies {
		t.Fatalf("silence took %q, want the most closed delivery", got)
	}

	c.Request.Header.Set(accesshttp.DeliveryHeader, string(accesshttp.DeliverBody))
	if got, err = jar.requested(c); err != nil || got != accesshttp.DeliverBody {
		t.Fatalf("the header asked for the body and this binding read %q (%v)", got, err)
	}
}

// The cookie half is where the three bindings differ most: each has its own
// cookie type or setter, and an attribute lost in one of them is a credential a
// script can read.
func TestACredentialCookieIsWrittenThroughThisTransport(t *testing.T) {
	jar := newJar(accesshttp.Table{}, []Option{
		Delivering(accesshttp.Cookies{Prefix: "/api", Secure: true}),
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)

	jar.answer(c, http.StatusOK, access.AuthResponse{
		Token:   "an access token",
		Refresh: "a rotating credential",
	}, accesshttp.DeliverCookies)

	assertCookiesLeftTheBody(t, recorder.Result(), recorder.Body.Bytes())
}

// assertCookiesLeftTheBody is the check the three bindings share: both
// credentials arrive in cookies a script cannot read, and no copy of either
// stayed in the body.
func assertCookiesLeftTheBody(t *testing.T, response *http.Response, body []byte) {
	t.Helper()

	set := map[string]*http.Cookie{}
	for _, cookie := range response.Cookies() {
		set[cookie.Name] = cookie
	}
	for name, path := range map[string]string{"access": "/api", "refresh": "/api/auth/refresh"} {
		cookie, ok := set[name]
		if !ok || cookie.Value == "" {
			t.Fatalf("no %q cookie was set: %v", name, response.Cookies())
		}
		if !cookie.HttpOnly {
			t.Fatalf("the %q cookie is readable from JavaScript, which is the one thing it must not be", name)
		}
		if !cookie.Secure {
			t.Fatalf("the %q cookie would travel over plain HTTP", name)
		}
		if cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("the %q cookie is SameSite=%v, want Strict", name, cookie.SameSite)
		}
		if cookie.Path != path {
			t.Fatalf("the %q cookie is scoped to %q, want %q", name, cookie.Path, path)
		}
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the response body is not JSON: %s", body)
	}
	if _, present := decoded["token"]; present {
		t.Fatalf("the access token went into a cookie and stayed in the body: %s", body)
	}
	if _, present := decoded["refresh"]; present {
		t.Fatalf("the rotating credential went into a cookie and stayed in the body: %s", body)
	}
}

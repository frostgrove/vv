package accessnet

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/access/http/accesshttp"
)

func TestTheRouteTableIsMountedUnderTheConfiguredPrefix(t *testing.T) {
	if got := (accesshttp.Table{}).Path("/login"); got != "/auth/login" {
		t.Fatalf("Path(\"/login\") = %q, want /auth/login", got)
	}
	if got := (accesshttp.Table{Prefix: "staff"}).Path("/login"); got != "/staff/auth/login" {
		t.Fatalf("Path(\"/login\") = %q, want /staff/auth/login", got)
	}

	if got := (accesshttp.Table{Prefix: "/staff/"}).Path("/login"); got != "/staff/auth/login" {
		t.Fatalf("a slashed prefix produced %q", got)
	}

	if got := (accesshttp.Table{Prefix: "staff"}).RegisterRoute().Path; got != "/staff/auth/register" {
		t.Fatalf("RegisterRoute().Path = %q", got)
	}
}

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

func TestTheDeliveryHeaderIsReadFromThisTransportsRequest(t *testing.T) {
	jar := newJar(accesshttp.Table{}, optionsOf([]Option{
		Delivering(accesshttp.Cookies{Prefix: "/api"}),
	}))

	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	got, err := jar.requested(request)
	if err != nil {
		t.Fatalf("a request with no delivery header was refused: %v", err)
	}
	if got != accesshttp.DeliverCookies {
		t.Fatalf("silence took %q, want the most closed delivery", got)
	}

	request.Header.Set(accesshttp.DeliveryHeader, string(accesshttp.DeliverBody))
	if got, err = jar.requested(request); err != nil || got != accesshttp.DeliverBody {
		t.Fatalf("the header asked for the body and this binding read %q (%v)", got, err)
	}
}

func TestACredentialCookieIsWrittenThroughThisTransport(t *testing.T) {
	jar := newJar(accesshttp.Table{}, optionsOf([]Option{
		Delivering(accesshttp.Cookies{Prefix: "/api", Secure: true}),
	}))

	recorder := httptest.NewRecorder()
	jar.answer(recorder, http.StatusOK, access.AuthResponse{
		Token:   "an access token",
		Refresh: "a rotating credential",
	}, accesshttp.DeliverCookies)

	assertCookiesLeftTheBody(t, recorder.Result(), recorder.Body.Bytes())
}

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

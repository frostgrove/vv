package accessfiber

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

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
	jar := newJar(accesshttp.Table{}, []Option{Delivering(accesshttp.Cookies{Prefix: "/api"})})

	var got accesshttp.Delivery
	var failed error
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		got, failed = jar.requested(c)
		return c.SendStatus(http.StatusOK)
	})

	if _, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil)); err != nil {
		t.Fatal(err)
	}
	if failed != nil {
		t.Fatalf("a request with no delivery header was refused: %v", failed)
	}
	if got != accesshttp.DeliverCookies {
		t.Fatalf("silence took %q, want the most closed delivery", got)
	}

	asked := httptest.NewRequest(http.MethodPost, "/", nil)
	asked.Header.Set(accesshttp.DeliveryHeader, string(accesshttp.DeliverBody))
	if _, err := app.Test(asked); err != nil {
		t.Fatal(err)
	}
	if failed != nil || got != accesshttp.DeliverBody {
		t.Fatalf("the header asked for the body and this binding read %q (%v)", got, failed)
	}
}

func TestACredentialCookieIsWrittenThroughThisTransport(t *testing.T) {
	jar := newJar(accesshttp.Table{}, []Option{
		Delivering(accesshttp.Cookies{Prefix: "/api", Secure: true}),
	})

	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		return jar.answer(c, http.StatusOK, access.AuthResponse{
			Token:   "an access token",
			Refresh: "a rotating credential",
		}, accesshttp.DeliverCookies)
	})

	response, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	assertCookiesLeftTheBody(t, response, body)
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

func crossSiteWrite() *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: "access", Value: "the-session-cookie"})
	request.Header.Set(accesshttp.HeaderFetchSite, "cross-site")
	request.Header.Set(accesshttp.HeaderOrigin, "https://evil.test")
	return request
}

func TestACookieBorneWriteFromAnotherSiteIsRefusedByThisTransport(t *testing.T) {
	jar := newJar(accesshttp.Table{}, []Option{
		Delivering(accesshttp.Cookies{Prefix: "/api", Secure: true, SameSite: accesshttp.SameSiteNone}),
	})
	handler := &Handler{jar: jar}

	answer := func(request *http.Request, run func(fiber.Ctx) error) error {
		var captured error
		app := fiber.New()
		app.Post("/api/auth/logout", func(c fiber.Ctx) error {
			captured = run(c)
			return nil
		})
		if _, err := app.Test(request); err != nil {
			t.Fatalf("serving the request: %v", err)
		}
		return captured
	}

	if err := answer(crossSiteWrite(), jar.protect); err == nil {
		t.Fatal("this transport read no cookie or no origin from the request, so nothing was checked")
	}

	sameOrigin := crossSiteWrite()
	sameOrigin.Header.Set(accesshttp.HeaderFetchSite, "same-origin")
	if err := answer(sameOrigin, jar.protect); err != nil {
		t.Fatalf("the deployment's own page was refused: %v", err)
	}

	if err := answer(crossSiteWrite(), handler.SignOut); err == nil {
		t.Fatal("signing out ran for a request made from another site; the handler never asks")
	}
}

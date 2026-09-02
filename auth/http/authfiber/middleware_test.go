package authfiber_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authfiber"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

func serve(t *testing.T, guard *auth.Guard, header string) (*seen, *http.Response, string) {
	t.Helper()
	h := &seen{}
	app := fiber.New()
	app.Use(authfiber.Middleware(guard))
	app.Get("/articles", h.handle)

	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	if header != "" {
		request.Header.Set("Authorization", header)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("serving the request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return h, response, string(body)
}

func TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal(t *testing.T) {
	h, response, _ := serve(t, auth.NewGuard(accepts()), "Bearer t")

	if response.StatusCode != http.StatusOK {
		t.Fatalf("an authenticated request answered %d, want 200", response.StatusCode)
	}
	if !h.ran {
		t.Fatal("the handler behind the middleware never ran")
	}

	if !h.found {
		t.Fatal("the handler saw no principal on c.Context, so no policy downstream would see one either")
	}
	if h.principal.Subject() != "u-1" {
		t.Fatalf("the handler saw subject %q, want u-1", h.principal.Subject())
	}
}

func TestAnUnauthenticatedRequestIs401AndTheHandlerNeverRuns(t *testing.T) {
	t.Run("no credential at all", func(t *testing.T) {
		h, response, _ := serve(t, auth.NewGuard(accepts()), "")
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a request with no credential answered %d, want 401", response.StatusCode)
		}
		if h.ran {
			t.Fatal("the handler ran for a request nobody authenticated")
		}
	})

	t.Run("a credential that does not verify", func(t *testing.T) {
		h, response, _ := serve(t, auth.NewGuard(refuses()), "Bearer forged")
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a forged credential answered %d, want 401", response.StatusCode)
		}
		if h.ran {
			t.Fatal("the handler ran for a request that failed to authenticate")
		}
	})
}

func TestAKeyProviderOutageIsInfrastructureAndTheHandlerNeverRuns(t *testing.T) {
	h, response, body := serve(t, auth.NewGuard(unavailable()), "Bearer valid-looking")
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("a key-provider outage answered %d, want 500 rather than 401", response.StatusCode)
	}
	if h.ran {
		t.Fatal("the handler ran when verification trust was unavailable")
	}
	if strings.Contains(body, "unauthenticated") || strings.Contains(body, "key source") {
		t.Fatalf("the infrastructure response exposed or misclassified the outage: %s", body)
	}
}

func TestTheRefusalBodyIsTheSharedEnvelopeAndNamesNoReason(t *testing.T) {
	_, response, body := serve(t, auth.NewGuard(refuses()), "Bearer forged")

	if !strings.Contains(body, `"unauthenticated"`) {
		t.Fatalf("the refusal carries no code a client can branch on: %s", body)
	}
	if !strings.Contains(body, "authentication is required") {
		t.Fatalf("the refusal carries no message: %s", body)
	}
	if strings.Contains(body, badToken) {
		t.Fatalf("the refusal says which half of the token was wrong: %s", body)
	}
	if ct := response.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("the refusal was written as %q, want JSON", ct)
	}
}

func TestAnOptionalGuardLetsAnAnonymousRequestThrough(t *testing.T) {
	t.Run("no credential reaches the handler unauthenticated", func(t *testing.T) {
		h, response, _ := serve(t, auth.NewGuard(accepts(), auth.Optional()), "")
		if response.StatusCode != http.StatusOK || !h.ran {
			t.Fatalf("an optional guard refused an anonymous request: %d", response.StatusCode)
		}
		if h.found {
			t.Fatal("an optional guard invented a principal for a request that presented none")
		}
	})

	t.Run("a bad credential is still refused", func(t *testing.T) {
		h, response, _ := serve(t, auth.NewGuard(refuses(), auth.Optional()), "Bearer forged")
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("an optional guard accepted a forged token as anonymous: %d", response.StatusCode)
		}
		if h.ran {
			t.Fatal("the handler ran with a forged token downgraded to anonymous")
		}
	})
}

func TestADoubleInstallAuthenticatesOnce(t *testing.T) {
	n := 0
	guard := auth.NewGuard(counting(&n))
	h := &seen{}

	app := fiber.New()
	app.Use(authfiber.Middleware(guard))
	app.Use(authfiber.Middleware(guard))
	app.Get("/articles", h.handle)

	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	request.Header.Set("Authorization", "Bearer t")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	if !h.found {
		t.Fatal("a double install lost the principal")
	}
	if n != 1 {
		t.Fatalf("the credential was verified %d times; a guard mounted on the app and again on a group pays twice", n)
	}
}

func TestDifferentGuardsAuthenticateIndependently(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := auth.NewGuard(counting(&firstCalls))
	second := auth.NewGuard(counting(&secondCalls))
	h := &seen{}

	app := fiber.New()
	app.Use(authfiber.Middleware(first))
	app.Use(authfiber.Middleware(second))
	app.Get("/articles", h.handle)

	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	request.Header.Set("Authorization", "Bearer t")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	if !h.found {
		t.Fatal("composed guards lost the principal")
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("composed guards authenticated %d and %d times, want once each", firstCalls, secondCalls)
	}
}

func TestAReenteredGuardFailsClosedWithoutGuessingAssurance(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		firstSubject, secondSubject string
	}{
		{"ordinary -> step-up -> ordinary", "ordinary", "step-up"},
		{"strict -> weak -> strict", "strict", "weak"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			firstCalls, middleCalls := 0, 0
			first := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
				firstCalls++
				return auth.Claims{Sub: tc.firstSubject}, nil
			}))
			middle := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
				middleCalls++
				return auth.Claims{Sub: tc.secondSubject}, nil
			}))
			h := &seen{}

			app := fiber.New()
			app.Use(authfiber.Middleware(first))
			app.Use(authfiber.Middleware(middle))
			app.Use(authfiber.Middleware(first))
			app.Get("/articles", h.handle)
			request := httptest.NewRequest(http.MethodGet, "/articles", nil)
			request.Header.Set("Authorization", "Bearer t")
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = response.Body.Close() })

			if response.StatusCode != http.StatusInternalServerError {
				t.Fatalf("ambiguous guard order answered %d, want 500 rather than a caller-facing 401", response.StatusCode)
			}
			if h.ran {
				t.Fatal("the handler ran after an assurance-ambiguous guard re-entry")
			}
			if firstCalls != 1 || middleCalls != 1 {
				t.Fatalf("ambiguous re-entry called authenticators %d and %d times, want once each", firstCalls, middleCalls)
			}
		})
	}
}

func TestANilGuardRefusesToStart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		guard *auth.Guard
	}{
		{"nil", nil},
		{"zero", new(auth.Guard)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("Middleware accepted a Guard with no authenticator")
				}
			}()
			authfiber.Middleware(tc.guard)
		})
	}
}

type corsAnswer struct {
	ran bool
}

func (this *corsAnswer) handle(fiberContext fiber.Ctx) error {
	this.ran = true
	fiberContext.Set("Access-Control-Allow-Origin", "https://app.example")
	return fiberContext.SendStatus(http.StatusNoContent)
}

func preflightRequest() *http.Request {
	request := httptest.NewRequest(http.MethodOptions, "/articles", nil)
	request.Header.Set(authhttp.HeaderOrigin, "https://app.example")
	request.Header.Set(authhttp.HeaderRequestMethod, "POST")
	return request
}

func TestACorsPreflightIsAnsweredByTheHandlerNamedForItAndABareOptionsIsNot(t *testing.T) {
	answer := func(request *http.Request) (*corsAnswer, *seen, *http.Response) {
		cors := &corsAnswer{}
		route := &seen{}
		app := fiber.New()
		app.Use(authfiber.AnswerPreflight(authfiber.Middleware(auth.NewGuard(accepts())), cors.handle))
		app.Add([]string{fiber.MethodOptions}, "/articles", route.handle)

		response, err := app.Test(request)
		if err != nil {
			t.Fatalf("serving the request: %v", err)
		}
		t.Cleanup(func() { _ = response.Body.Close() })
		return cors, route, response
	}

	cors, route, response := answer(preflightRequest())
	if !cors.ran {
		t.Fatalf("the browser's preflight was refused with %d, so the request it precedes never happens",
			response.StatusCode)
	}
	if route.ran {
		t.Fatal("the preflight reached the route, so an unauthenticated OPTIONS runs application code")
	}
	if response.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Fatal("the preflight was answered by something other than the CORS handler")
	}

	cors, route, response = answer(httptest.NewRequest(http.MethodOptions, "/articles", nil))
	if cors.ran || route.ran || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an OPTIONS request that is not a preflight walked past the guard: %d", response.StatusCode)
	}

	authorized := preflightRequest()
	authorized.Header.Set("Authorization", "Bearer t")
	cors, route, response = answer(authorized)
	if cors.ran || !route.ran || response.StatusCode != http.StatusOK {
		t.Fatalf("an OPTIONS carrying a credential answered %d; it is a request, not a preflight",
			response.StatusCode)
	}
}

func TestAPreflightNobodyAnsweredStopsAtTheDoorInsteadOfAtTheRoute(t *testing.T) {
	route := &seen{}
	app := fiber.New()
	app.Use(authfiber.SkipPreflight(authfiber.Middleware(auth.NewGuard(accepts()))))
	app.Add([]string{fiber.MethodOptions}, "/articles", route.handle)

	response, err := app.Test(preflightRequest())
	if err != nil {
		t.Fatalf("serving the request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })

	if route.ran {
		t.Fatal("two forgeable headers carried an unauthenticated OPTIONS into a hand-written handler")
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("a preflight nobody was named to answer came back %d, want 204", response.StatusCode)
	}
}

func TestAPreflightAnswerThatIsNotThereRefusesToStart(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("AnswerPreflight accepted nothing to answer a preflight with")
		}
	}()
	authfiber.AnswerPreflight(authfiber.Middleware(auth.NewGuard(accepts())), nil)
}

package authgin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authgin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// serve runs one request through the middleware and answers what the handler
// saw and what the client got.
func serve(t *testing.T, guard *auth.Guard, header string) (*seen, *httptest.ResponseRecorder) {
	t.Helper()
	h := &seen{}
	r := gin.New()
	r.Use(authgin.Middleware(guard))
	r.GET("/articles", h.handle)

	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	if header != "" {
		request.Header.Set("Authorization", header)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	return h, w
}

func TestAnAuthenticatedRequestReachesTheHandlerWithItsPrincipal(t *testing.T) {
	h, w := serve(t, auth.NewGuard(accepts()), "Bearer t")

	if w.Code != http.StatusOK {
		t.Fatalf("an authenticated request answered %d, want 200", w.Code)
	}
	if !h.ran {
		t.Fatal("the handler behind the middleware never ran")
	}
	if !h.found {
		t.Fatal("the handler saw no principal on c.Request's context, so no policy downstream would see one either")
	}
	if h.principal.Subject() != "u-1" {
		t.Fatalf("the handler saw subject %q, want u-1", h.principal.Subject())
	}
}

func TestAnUnauthenticatedRequestIs401AndTheHandlerNeverRuns(t *testing.T) {
	t.Run("no credential at all", func(t *testing.T) {
		h, w := serve(t, auth.NewGuard(accepts()), "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("a request with no credential answered %d, want 401", w.Code)
		}
		if h.ran {
			t.Fatal("the handler ran for a request nobody authenticated")
		}
	})

	t.Run("a credential that does not verify", func(t *testing.T) {
		h, w := serve(t, auth.NewGuard(refuses()), "Bearer forged")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("a forged credential answered %d, want 401", w.Code)
		}
		if h.ran {
			t.Fatal("the handler ran for a request that failed to authenticate")
		}
	})
}

func TestAKeyProviderOutageIsInfrastructureAndTheHandlerNeverRuns(t *testing.T) {
	h, w := serve(t, auth.NewGuard(unavailable()), "Bearer valid-looking")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("a key-provider outage answered %d, want 500 rather than 401", w.Code)
	}
	if h.ran {
		t.Fatal("the handler ran when verification trust was unavailable")
	}
	if body := w.Body.String(); strings.Contains(body, "unauthenticated") || strings.Contains(body, "key source") {
		t.Fatalf("the infrastructure response exposed or misclassified the outage: %s", body)
	}
}

func TestTheRefusalBodyIsTheSharedEnvelopeAndNamesNoReason(t *testing.T) {
	_, w := serve(t, auth.NewGuard(refuses()), "Bearer forged")
	body := w.Body.String()

	if !strings.Contains(body, `"unauthenticated"`) {
		t.Fatalf("the refusal carries no code a client can branch on: %s", body)
	}
	if !strings.Contains(body, "authentication is required") {
		t.Fatalf("the refusal carries no message: %s", body)
	}
	if strings.Contains(body, badToken) {
		t.Fatalf("the refusal says which half of the token was wrong: %s", body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("the refusal was written as %q, want JSON", ct)
	}
}

func TestAnOptionalGuardLetsAnAnonymousRequestThrough(t *testing.T) {
	t.Run("no credential reaches the handler unauthenticated", func(t *testing.T) {
		h, w := serve(t, auth.NewGuard(accepts(), auth.Optional()), "")
		if w.Code != http.StatusOK || !h.ran {
			t.Fatalf("an optional guard refused an anonymous request: %d", w.Code)
		}
		if h.found {
			t.Fatal("an optional guard invented a principal for a request that presented none")
		}
	})

	t.Run("a bad credential is still refused", func(t *testing.T) {
		h, w := serve(t, auth.NewGuard(refuses(), auth.Optional()), "Bearer forged")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("an optional guard accepted a forged token as anonymous: %d", w.Code)
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

	r := gin.New()
	r.Use(authgin.Middleware(guard), authgin.Middleware(guard))
	r.GET("/articles", h.handle)

	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	request.Header.Set("Authorization", "Bearer t")
	r.ServeHTTP(httptest.NewRecorder(), request)

	if !h.found {
		t.Fatal("a double install lost the principal")
	}
	if n != 1 {
		t.Fatalf("the credential was verified %d times; a guard mounted on the engine and again on a group pays twice", n)
	}
}

func TestDifferentGuardsAuthenticateIndependently(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := auth.NewGuard(counting(&firstCalls))
	second := auth.NewGuard(counting(&secondCalls))
	h := &seen{}

	r := gin.New()
	r.Use(authgin.Middleware(first), authgin.Middleware(second))
	r.GET("/articles", h.handle)

	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	request.Header.Set("Authorization", "Bearer t")
	r.ServeHTTP(httptest.NewRecorder(), request)

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

			router := gin.New()
			router.Use(authgin.Middleware(first), authgin.Middleware(middle), authgin.Middleware(first))
			router.GET("/articles", h.handle)
			request := httptest.NewRequest(http.MethodGet, "/articles", nil)
			request.Header.Set("Authorization", "Bearer t")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, request)

			if w.Code != http.StatusInternalServerError {
				t.Fatalf("ambiguous guard order answered %d, want 500 rather than a caller-facing 401", w.Code)
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
			authgin.Middleware(tc.guard)
		})
	}
}

package authhttp_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

// presented answers the credential a guard reached its authenticator with, and
// whether it reached one at all.
func presented(t *testing.T, header http.Header, options ...auth.Option) (auth.Credential, bool) {
	t.Helper()

	var seen auth.Credential
	reached := false
	authenticator := auth.AuthenticatorFunc(
		func(_ context.Context, c auth.Credential) (auth.Principal, error) {
			seen, reached = c, true
			return nil, auth.Unauthenticated("this test never accepts anything")
		})

	// Optional, so a request that presents nothing is anonymous rather than an
	// error: what this fixture reports is whether a credential was found at all,
	// and a refusal would make the two cases look alike.
	guard := auth.NewGuard(authenticator, append([]auth.Option{auth.Optional()}, options...)...)
	_, _ = guard.Authenticate(context.Background(), header.Get)
	return seen, reached
}

// A browser that holds its access token in an HttpOnly cookie sends no
// Authorization header at all, so a guard that only reads one refuses every
// request it makes.
func TestACredentialIsReadFromACookie(t *testing.T) {
	header := http.Header{}
	header.Set("Cookie", "other=x; access=a signed token; another=y")

	credential, reached := presented(t, header, authhttp.Cookie("access"))
	if !reached {
		t.Fatal("a request carrying the cookie reached no authenticator")
	}
	if credential.Token != "a signed token" {
		t.Fatalf("the credential is %q", credential.Token)
	}
	// A cookie carries a bare token, and every authenticator in this library
	// refuses a credential that is not Bearer.
	if !credential.Is(auth.SchemeBearer) {
		t.Fatalf("the credential arrived under the scheme %q, which no authenticator here accepts", credential.Scheme)
	}

	// The control: without the option, the same request reaches nothing — so the
	// test above is about the option and not about something else.
	if _, reached := presented(t, header); reached {
		t.Fatal("a guard with no cookie option read the cookie anyway")
	}
}

// auth.Lookup *replaces* the credential lookup rather than adding to it, so a
// cookie option written the obvious way turns the Authorization header off — in
// the same application that wants both, because its pages send a cookie and its
// native client sends a header.
func TestACookieLookupStillReadsTheAuthorizationHeader(t *testing.T) {
	header := http.Header{}
	header.Set("Authorization", "Bearer a signed token")

	credential, reached := presented(t, header, authhttp.Cookie("access"))
	if !reached {
		t.Fatal("a request with a header and no cookie reached no authenticator; the fallback is gone")
	}
	if credential.Token != "a signed token" {
		t.Fatalf("the credential is %q", credential.Token)
	}

	// And the cookie wins when both are there. A browser holding a stale header
	// is not a case this library has; a page whose cookie was just rotated is.
	header.Set("Cookie", "access=the cookie")
	if credential, _ := presented(t, header, authhttp.Cookie("access")); credential.Token != "the cookie" {
		t.Fatalf("with both present the guard took %q", credential.Token)
	}
}

// A Cookie header no parser accepts is not something the caller of a guard can
// act on, and the request still has an Authorization header to be judged on.
func TestAMalformedCookieHeaderIsNotACredential(t *testing.T) {
	header := http.Header{}
	header.Set("Cookie", "=;;")
	if _, reached := presented(t, header, authhttp.Cookie("access")); reached {
		t.Fatal("a malformed Cookie header produced a credential")
	}

	header.Set("Authorization", "Bearer a signed token")
	if credential, reached := presented(t, header, authhttp.Cookie("access")); !reached || credential.Token != "a signed token" {
		t.Fatalf("a malformed Cookie header stopped the header from being read: %q", credential.Token)
	}
}

// A cookie is what one transport calls a place to put a credential, and the
// name is what tells two of them apart. The wrong one must not authenticate.
func TestAnotherCookiesValueIsNotTheCredential(t *testing.T) {
	header := http.Header{}
	header.Set("Cookie", "refresh=a rotating credential")
	if _, reached := presented(t, header, authhttp.Cookie("access")); reached {
		t.Fatal("the rotating credential was presented as an access token; " +
			"a guard reading it would authenticate a request the rotation endpoint alone should see")
	}
}

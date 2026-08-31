package authhttp_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

func presented(t *testing.T, header http.Header, options ...auth.Option) (auth.Credential, bool) {
	t.Helper()

	var seen auth.Credential
	reached := false
	authenticator := auth.AuthenticatorFunc(
		func(_ context.Context, c auth.Credential) (auth.Principal, error) {
			seen, reached = c, true
			return nil, auth.Unauthenticated("this test never accepts anything")
		})

	guard := auth.NewGuard(authenticator, append([]auth.Option{auth.Optional()}, options...)...)
	_, _ = guard.Authenticate(context.Background(), header.Get)
	return seen, reached
}

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

	if !credential.Is(auth.SchemeBearer) {
		t.Fatalf("the credential arrived under the scheme %q, which no authenticator here accepts", credential.Scheme)
	}

	if _, reached := presented(t, header); reached {
		t.Fatal("a guard with no cookie option read the cookie anyway")
	}
}

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

	header.Set("Cookie", "access=the cookie")
	if credential, _ := presented(t, header, authhttp.Cookie("access")); credential.Token != "the cookie" {
		t.Fatalf("with both present the guard took %q", credential.Token)
	}
}

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

func TestAnotherCookiesValueIsNotTheCredential(t *testing.T) {
	header := http.Header{}
	header.Set("Cookie", "refresh=a rotating credential")
	if _, reached := presented(t, header, authhttp.Cookie("access")); reached {
		t.Fatal("the rotating credential was presented as an access token; " +
			"a guard reading it would authenticate a request the rotation endpoint alone should see")
	}
}

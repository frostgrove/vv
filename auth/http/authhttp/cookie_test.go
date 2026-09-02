package authhttp_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
)

func presented(t *testing.T, header http.Header, options ...auth.Option) (auth.Credential, bool) {
	t.Helper()

	credential, reached, _ := lookedUp(t, header, options...)
	return credential, reached
}

func lookedUp(t *testing.T, header http.Header, options ...auth.Option) (auth.Credential, bool, error) {
	t.Helper()

	var seen auth.Credential
	reached := false
	authenticator := auth.AuthenticatorFunc(
		func(_ context.Context, c auth.Credential) (auth.Principal, error) {
			seen, reached = c, true
			return nil, auth.Unauthenticated("this test never accepts anything")
		})

	guard := auth.NewGuard(authenticator, append([]auth.Option{auth.Optional()}, options...)...)
	_, err := guard.Authenticate(context.Background(), header.Get)
	return seen, reached, err
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

	header.Set("Cookie", "other=x")
	if credential, reached := presented(t, header, authhttp.Cookie("access")); !reached || credential.Token != "a signed token" {
		t.Fatalf("a cookie header carrying no access cookie stopped the header from being read: %q", credential.Token)
	}
}

func TestARequestPresentingTwoCredentialsIsRefusedRatherThanRanked(t *testing.T) {
	for name, request := range map[string]struct{ cookie, authorization string }{
		"a cookie beside an Authorization header": {cookie: "access=the cookie", authorization: "Bearer a signed token"},
		"two cookies of the same name":            {cookie: "access=the first; access=the second"},
	} {
		t.Run(name, func(t *testing.T) {
			header := http.Header{}
			header.Set("Cookie", request.cookie)
			if request.authorization != "" {
				header.Set("Authorization", request.authorization)
			}

			_, reached, err := lookedUp(t, header, authhttp.Cookie("access"))
			if reached {
				t.Fatal("one of two credentials was picked and handed to the authenticator; " +
					"which one wins is not something the request may decide")
			}
			if !errors.Is(err, auth.ErrCredentialCardinality) || !errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf("two credentials answered %v, want a typed authentication refusal", err)
			}
		})
	}
}

func TestTheLegacyCookiePrecedenceIsAvailableOnlyByNamingIt(t *testing.T) {
	header := http.Header{}
	header.Set("Cookie", "access=the cookie")
	header.Set("Authorization", "Bearer a signed token")

	credential, reached := presented(t, header, authhttp.UnsafeCookieWinsOverAuthorization("access"))
	if !reached || credential.Token != "the cookie" {
		t.Fatalf("the legacy option handed the authenticator %q, want the cookie it prefers", credential.Token)
	}

	header.Del("Cookie")
	if credential, reached := presented(t, header, authhttp.UnsafeCookieWinsOverAuthorization("access")); !reached ||
		credential.Token != "a signed token" {
		t.Fatalf("the legacy option stopped reading the header: %q", credential.Token)
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

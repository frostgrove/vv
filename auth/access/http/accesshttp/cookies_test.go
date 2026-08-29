package accesshttp

import (
	"net/http"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth/access"
)

func minted() access.AuthResponse {
	return access.AuthResponse{
		Token:            "an access token",
		ExpiresAt:        time.Now().Add(5 * time.Minute),
		Refresh:          "a rotating credential",
		RefreshExpiresAt: time.Now().Add(720 * time.Hour),
	}
}

func find(t *testing.T, jar []Cookie, name string) Cookie {
	t.Helper()
	for _, cookie := range jar {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("no %q cookie among %v", name, jar)
	return Cookie{}
}

// What travels in a cookie leaves the body entirely. A `"token": ""` is a field
// a client has to know to ignore, and one that did not would present the empty
// string as a bearer and be told it is not signed in.
func TestACredentialInACookieIsNotAlsoInTheBody(t *testing.T) {
	credentials := NewCredentials(Table{}, Cookies{Prefix: "/api/v1", Secure: true})

	body, jar := credentials.Answer(minted(), DeliverCookies)
	if body.Token != "" || !body.ExpiresAt.IsZero() {
		t.Fatal("the access token went into a cookie and stayed in the body; two copies of a secret is one place too many")
	}
	if body.Refresh != "" || !body.RefreshExpiresAt.IsZero() {
		t.Fatal("the rotating credential went into a cookie and stayed in the body")
	}
	if got := find(t, jar, "access").Value; got != "an access token" {
		t.Fatalf("the access cookie carries %q", got)
	}
	if got := find(t, jar, "refresh").Value; got != "a rotating credential" {
		t.Fatalf("the refresh cookie carries %q", got)
	}

	// The control: the same response delivered to the body keeps both, or the
	// assertions above would pass for a response that never carried anything.
	body, _ = credentials.Answer(minted(), DeliverBody)
	if body.Token == "" || body.Refresh == "" {
		t.Fatal("a body delivery answered without the credentials it was supposed to carry")
	}
}

// A browser that signs in again asking for the body would otherwise keep the
// previous session's access cookie for the rest of its five minutes — and a
// guard reading the cookie prefers it to the header, so the page would hold a
// fresh token and go on acting as the session it just replaced.
func TestAHalfDeliveredToTheBodyClearsTheCookieItDidNotGoInto(t *testing.T) {
	credentials := NewCredentials(Table{}, Cookies{Prefix: "/api/v1"})

	_, jar := credentials.Answer(minted(), DeliverRefreshCookie)
	if !find(t, jar, "access").Clearing() {
		t.Fatal("the access token went to the body and left a cookie behind that a guard would prefer to it")
	}
	if find(t, jar, "refresh").Clearing() {
		t.Fatal("the rotating credential was cleared out of the cookie it was supposed to go into")
	}

	_, jar = credentials.Answer(minted(), DeliverBody)
	for _, name := range []string{"access", "refresh"} {
		if !find(t, jar, name).Clearing() {
			t.Fatalf("a body delivery left the %q cookie in place", name)
		}
	}

	// A surface with no cookies configured writes none at all, rather than
	// clearing cookies it was never asked about.
	if _, jar := (Credentials{}).Answer(minted(), DeliverBody); len(jar) != 0 {
		t.Fatalf("a body-only surface wrote %d cookies", len(jar))
	}
}

// The access cookie travels with API calls and the rotating one with the
// endpoint that spends it. A credential attached to every request is one that
// reaches every log, proxy and error report the API touches.
func TestEachCookieIsScopedToWhatSpendsIt(t *testing.T) {
	credentials := NewCredentials(Table{}, Cookies{Prefix: "/api/v1"})
	_, jar := credentials.Answer(minted(), DeliverCookies)

	if got := find(t, jar, "access").Path; got != "/api/v1" {
		t.Fatalf("the access cookie is scoped to %q, want the API", got)
	}
	if got := find(t, jar, "refresh").Path; got != "/api/v1/auth/refresh" {
		t.Fatalf("the refresh cookie is scoped to %q, want the rotation endpoint alone", got)
	}

	// An API at the root scopes the access cookie to the site, because that is
	// what "everything this API serves" means there. A path of "" would be
	// resolved by the browser against the request's own directory.
	_, jar = NewCredentials(Table{}, Cookies{}).Answer(minted(), DeliverCookies)
	if got := find(t, jar, "access").Path; got != "/" {
		t.Fatalf("an API at the root scoped its access cookie to %q", got)
	}

	// A prefix somebody wrote with slashes is the same prefix.
	_, jar = NewCredentials(Table{}, Cookies{Prefix: "api/"}).Answer(minted(), DeliverCookies)
	if got := find(t, jar, "access").Path; got != "/api" {
		t.Fatalf("a slashed prefix produced the path %q", got)
	}
}

// Two kinds of caller on one host both hold an access cookie scoped to the whole
// API. Named alike, the browser would overwrite one with the other and a member
// of staff would find themselves signed in as a customer.
func TestTwoSubjectsDoNotShareACookieName(t *testing.T) {
	user := NewCredentials(Table{}, Cookies{Prefix: "/api"})
	staff := NewCredentials(Table{Prefix: "staff"}, Cookies{Prefix: "/api"})

	_, mine := user.Answer(minted(), DeliverCookies)
	_, theirs := staff.Answer(minted(), DeliverCookies)
	if find(t, mine, "access").Name == find(t, theirs, "staff_access").Name {
		t.Fatal("two subjects share the access cookie name")
	}
	if staff.RefreshCookie() != "staff_refresh" {
		t.Fatalf("the second subject's refresh cookie is named %q", staff.RefreshCookie())
	}
	// A nested prefix is a legal path and not a legal cookie name.
	if got := (Table{Prefix: "/eu/staff/"}).AccessCookie(); got != "eu_staff_access" {
		t.Fatalf("a nested prefix produced the cookie name %q", got)
	}
}

// Signing out clears both, whatever the session was delivered as: the delivery
// is not recorded anywhere, and a browser left holding credentials that
// authorise nothing presents them on the next visit and gets a 401 where it
// expects a login screen.
func TestSigningOutClearsBothCookiesAndARefusedRotationOnlyTheRotatingOne(t *testing.T) {
	credentials := NewCredentials(Table{}, Cookies{Prefix: "/api"})

	cleared := credentials.Clear()
	if len(cleared) != 2 {
		t.Fatalf("signing out wrote %d cookies, want both", len(cleared))
	}
	for _, cookie := range cleared {
		if !cookie.Clearing() || cookie.Expires.After(time.Now()) {
			t.Fatalf("the %q cookie was not cleared: %q expiring %v", cookie.Name, cookie.Value, cookie.Expires)
		}
	}

	// A rotation is anonymous, so the caller may well be holding a perfectly
	// good access cookie from a session this one knows nothing about.
	refused := credentials.ClearRefresh()
	if len(refused) != 1 || refused[0].Name != "refresh" {
		t.Fatalf("a refused rotation cleared %v", refused)
	}

	if credentials := (Credentials{}); credentials.Clear() != nil || credentials.ClearRefresh() != nil {
		t.Fatal("a body-only surface cleared cookies it never set")
	}
}

// The three attributes that make the whole arrangement worth doing, on the way
// out to a net/http writer.
func TestACookieLeavesUnreadableFromThePage(t *testing.T) {
	_, jar := NewCredentials(Table{}, Cookies{Prefix: "/api", Secure: true}).Answer(minted(), DeliverCookies)

	rendered := find(t, jar, "access").HTTP()
	if !rendered.HttpOnly {
		t.Fatal("the access cookie is readable from JavaScript, which is the one thing it must not be")
	}
	if !rendered.Secure {
		t.Fatal("a deployment that asked for Secure got a cookie that travels over plain HTTP")
	}
	if rendered.SameSite != http.SameSiteStrictMode {
		t.Fatalf("the access cookie is SameSite=%v, want Strict", rendered.SameSite)
	}
	// A clearing cookie says so twice: an expiry in the past and a Max-Age. A
	// client that honours only one of them is one that goes on presenting a
	// credential nothing accepts.
	if cleared := NewCredentials(Table{}, Cookies{}).Clear()[0].HTTP(); cleared.MaxAge >= 0 {
		t.Fatalf("a cleared cookie carries Max-Age=%d", cleared.MaxAge)
	}
}

// A browser discards a SameSite=None cookie that is not Secure, so a process
// that started with one would set credentials nothing ever receives — and every
// session would end at the next request with nothing in any log to say why.
func TestSameSiteNoneWithoutSecureRefusesToStart(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a policy a browser silently discards was accepted")
		}
	}()
	NewCredentials(Table{}, Cookies{SameSite: SameSiteNone})
}

// The control for the test above: the same policy with Secure is accepted, and
// travels as None rather than being quietly narrowed to Strict — which would
// leave a cross-site deployment sending no cookies at all.
func TestSameSiteNoneWithSecureIsAccepted(t *testing.T) {
	_, jar := NewCredentials(Table{}, Cookies{SameSite: SameSiteNone, Secure: true}).
		Answer(minted(), DeliverCookies)
	if got := find(t, jar, "access").HTTP().SameSite; got != http.SameSiteNoneMode {
		t.Fatalf("SameSite=None was written as %v", got)
	}
}

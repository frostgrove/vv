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

	body, _ = credentials.Answer(minted(), DeliverBody)
	if body.Token == "" || body.Refresh == "" {
		t.Fatal("a body delivery answered without the credentials it was supposed to carry")
	}
}

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

	if _, jar := (Credentials{}).Answer(minted(), DeliverBody); len(jar) != 0 {
		t.Fatalf("a body-only surface wrote %d cookies", len(jar))
	}
}

func TestEachCookieIsScopedToWhatSpendsIt(t *testing.T) {
	credentials := NewCredentials(Table{}, Cookies{Prefix: "/api/v1"})
	_, jar := credentials.Answer(minted(), DeliverCookies)

	if got := find(t, jar, "access").Path; got != "/api/v1" {
		t.Fatalf("the access cookie is scoped to %q, want the API", got)
	}
	if got := find(t, jar, "refresh").Path; got != "/api/v1/auth/refresh" {
		t.Fatalf("the refresh cookie is scoped to %q, want the rotation endpoint alone", got)
	}

	_, jar = NewCredentials(Table{}, Cookies{}).Answer(minted(), DeliverCookies)
	if got := find(t, jar, "access").Path; got != "/" {
		t.Fatalf("an API at the root scoped its access cookie to %q", got)
	}

	_, jar = NewCredentials(Table{}, Cookies{Prefix: "api/"}).Answer(minted(), DeliverCookies)
	if got := find(t, jar, "access").Path; got != "/api" {
		t.Fatalf("a slashed prefix produced the path %q", got)
	}
}

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

	if got := (Table{Prefix: "/eu/staff/"}).AccessCookie(); got != "eu_staff_access" {
		t.Fatalf("a nested prefix produced the cookie name %q", got)
	}
}

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

	refused := credentials.ClearRefresh()
	if len(refused) != 1 || refused[0].Name != "refresh" {
		t.Fatalf("a refused rotation cleared %v", refused)
	}

	if credentials := (Credentials{}); credentials.Clear() != nil || credentials.ClearRefresh() != nil {
		t.Fatal("a body-only surface cleared cookies it never set")
	}
}

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

	if cleared := NewCredentials(Table{}, Cookies{}).Clear()[0].HTTP(); cleared.MaxAge >= 0 {
		t.Fatalf("a cleared cookie carries Max-Age=%d", cleared.MaxAge)
	}
}

func TestSameSiteNoneWithoutSecureRefusesToStart(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a policy a browser silently discards was accepted")
		}
	}()
	NewCredentials(Table{}, Cookies{SameSite: SameSiteNone})
}

func TestSameSiteNoneWithSecureIsAccepted(t *testing.T) {
	_, jar := NewCredentials(Table{}, Cookies{SameSite: SameSiteNone, Secure: true}).
		Answer(minted(), DeliverCookies)
	if got := find(t, jar, "access").HTTP().SameSite; got != http.SameSiteNoneMode {
		t.Fatalf("SameSite=None was written as %v", got)
	}
}
